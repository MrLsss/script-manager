package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store owns the library: folders.json, one directory per script holding its
// metadata and content, local.json for what stays on this machine, the logs of
// runs and the revisions of content.
type Store struct {
	mu   sync.Mutex
	root string
	lib  Library

	bindings  map[string]string // script id → interpreter id
	conflicts []SyncConflict

	// problems records what the last load had to skip or repair, so the UI can
	// say so instead of silently losing entries.
	problems []string
}

const storeVersion = 2

// ErrSnapshotTooLarge says a directory is bigger than one snapshot may be. The
// script itself is saved; only its history skips this version.
var ErrSnapshotTooLarge = errors.New("目录超过单份快照的大小上限")

// maxSnapshotBytes caps one snapshot.
const maxSnapshotBytes = 5 << 20

// maxEditableBytes is the largest file the editor opens.
const maxEditableBytes = 2 << 20

// import limits keep a careless import from filling the disk.
const (
	maxImportFiles = 500
	maxImportFile  = 20 << 20
	maxImportTotal = 100 << 20
)

// OpenStore opens the library at root, making it when it is empty.
func OpenStore(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{"", "scripts", "logs", "revisions", ".trash"} {
		if err := os.MkdirAll(filepath.Join(abs, dir), 0o755); err != nil {
			return nil, err
		}
	}
	s := &Store{root: abs, lib: Library{Version: storeVersion}, bindings: map[string]string{}}
	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(s.foldersPath()); errors.Is(err, os.ErrNotExist) {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Reload reads the library from disk again, after files changed outside the app.
func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// Root is the absolute path of the library.
func (s *Store) Root() string { return s.root }

func (s *Store) foldersPath() string { return filepath.Join(s.root, "folders.json") }
func (s *Store) localPath() string   { return filepath.Join(s.root, "local.json") }
func (s *Store) legacyPath() string  { return filepath.Join(s.root, "library.json") }
func (s *Store) scriptMetaPath(id string) string {
	return filepath.Join(s.root, "scripts", id, "script.json")
}

// localFile is what local.json holds: everything that belongs to this machine.
type localFile struct {
	Version      int               `json:"version"`
	Interpreters []*Interpreter    `json:"interpreters"`
	Bindings     map[string]string `json:"bindings"`
	Revisions    []*Revision       `json:"revisions"`
	Runs         []*Run            `json:"runs"`
	Conflicts    []SyncConflict    `json:"conflicts"`
}

func (s *Store) loadLocked() error {
	if err := s.migrateLocked(); err != nil {
		return err
	}
	s.lib = Library{Version: storeVersion}
	s.bindings = map[string]string{}
	s.conflicts = nil

	version := 0
	if data, err := os.ReadFile(s.foldersPath()); err == nil {
		var f struct {
			Version int       `json:"version"`
			Folders []*Folder `json:"folders"`
		}
		if err := json.Unmarshal(data, &f); err != nil {
			return fmt.Errorf("folders.json 无法解析: %w", err)
		}
		s.lib.Folders = f.Folders
		version = f.Version
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	s.problems = nil
	entries, err := os.ReadDir(filepath.Join(s.root, "scripts"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Leftovers of an interrupted restore or overwrite.
		if strings.HasPrefix(e.Name(), ".stage-") || strings.HasPrefix(e.Name(), ".old-") {
			_ = os.RemoveAll(filepath.Join(s.root, "scripts", e.Name()))
			continue
		}
		if !validScriptID(e.Name()) {
			s.problems = append(s.problems, "目录 scripts/"+e.Name()+" 不能作为脚本 ID，已跳过")
			continue
		}
		data, err := os.ReadFile(s.scriptMetaPath(e.Name()))
		if err != nil {
			s.problems = append(s.problems, "scripts/"+e.Name()+" 缺少或读不到 script.json，已跳过")
			continue
		}
		var sc Script
		if err := json.Unmarshal(data, &sc); err != nil {
			s.problems = append(s.problems, "scripts/"+e.Name()+"/script.json 无法解析，已跳过")
			continue
		}
		// The directory name is authoritative: an id in the file must never
		// point at another directory.
		sc.ID = e.Name()
		s.lib.Scripts = append(s.lib.Scripts, &sc)
	}

	if data, err := os.ReadFile(s.localPath()); err == nil {
		var lf localFile
		if err := json.Unmarshal(data, &lf); err != nil {
			return fmt.Errorf("local.json 无法解析: %w", err)
		}
		s.lib.Interpreters = lf.Interpreters
		s.bindings = lf.Bindings
		s.conflicts = lf.Conflicts
		// local.json can be hand-edited or come from another machine; a path
		// in it must never name a file outside the library.
		for _, rev := range lf.Revisions {
			if rev == nil {
				continue
			}
			if !s.insideLibrary(rev.Path) {
				s.problems = append(s.problems, "修订 "+rev.ID+" 的路径在库外，已忽略")
				continue
			}
			s.lib.Revisions = append(s.lib.Revisions, rev)
		}
		for _, run := range lf.Runs {
			if run == nil {
				continue
			}
			if run.LogPath != "" && !s.insideLibrary(run.LogPath) {
				s.problems = append(s.problems, "运行记录 "+run.ID+" 的日志路径在库外，已忽略")
				continue
			}
			s.lib.Runs = append(s.lib.Runs, run)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if s.bindings == nil {
		s.bindings = map[string]string{}
	}
	folderIDs := map[string]bool{}
	for _, f := range s.lib.Folders {
		folderIDs[f.ID] = true
	}
	for _, sc := range s.lib.Scripts {
		sc.InterpID = s.bindings[sc.ID]
		sc.normalize()
		sc.Env = sanitizeEnv(sc.Env)
		// A script whose folder is gone (a partial sync, a hand-edited index)
		// goes back to the root instead of disappearing from the tree.
		if sc.FolderID != "" && !folderIDs[sc.FolderID] {
			sc.FolderID = ""
		}
	}
	// Revisions from the single-file format remember no entry; point them at
	// their script's entry so their content can still be read.
	for _, rev := range s.lib.Revisions {
		if !isLegacyRevision(*rev) || rev.Entry != "" {
			continue
		}
		for _, sc := range s.lib.Scripts {
			if sc.ID == rev.ScriptID {
				rev.Entry = sc.Entry
				break
			}
		}
	}
	needsUpgrade := version > 0 && version < storeVersion
	for _, sc := range s.lib.Scripts {
		if sc.Entry == "" {
			needsUpgrade = true
		}
	}
	s.pruneRevisionsLocked()
	if needsUpgrade {
		if version == 0 {
			version = 1
		}
		if err := s.upgradeLocked(version); err != nil {
			return err
		}
	}
	return nil
}

// normalize fills the derived fields of a script from its entry, so Entry is
// the only thing that decides the language and extension.
func (sc *Script) normalize() {
	sc.Entry = strings.ReplaceAll(strings.TrimPrefix(sc.Entry, "/"), "\\", "/")
	sc.Ext = strings.TrimPrefix(path.Ext(sc.Entry), ".")
	if lang, ok := languageForEntry(sc.Entry); ok {
		sc.Language = lang
	}
	if sc.Language == "" {
		sc.Language = "plain"
	}
}

// insideLibrary reports whether a stored relative path stays inside the root.
func (s *Store) insideLibrary(rel string) bool {
	if rel == "" {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return false
	}
	return withinDir(s.root, filepath.Join(s.root, clean))
}

// Problems lists what the last load skipped or repaired.
func (s *Store) Problems() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.problems))
	copy(out, s.problems)
	return out
}

// pruneRevisionsLocked drops records whose snapshot is gone, and removes
// snapshot directories no record points at, so the two cannot drift apart.
func (s *Store) pruneRevisionsLocked() {
	keep := make([]*Revision, 0, len(s.lib.Revisions))
	referenced := map[string]bool{}
	for _, rev := range s.lib.Revisions {
		if _, err := os.Stat(filepath.Join(s.root, rev.Path)); err != nil {
			s.problems = append(s.problems, "修订 "+rev.Hash+" 的内容已丢失，记录已移除")
			continue
		}
		referenced[filepath.Clean(rev.Path)] = true
		keep = append(keep, rev)
	}
	s.lib.Revisions = keep
	// Only reclaim space after a clean load: with a damaged or partial index a
	// directory can look orphaned while a record still needs it.
	if len(s.problems) > 0 {
		return
	}
	base := filepath.Join(s.root, "revisions")
	scripts, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, dir := range scripts {
		if !dir.IsDir() {
			continue
		}
		children, err := os.ReadDir(filepath.Join(base, dir.Name()))
		if err != nil {
			continue
		}
		for _, child := range children {
			rel := filepath.Join("revisions", dir.Name(), child.Name())
			if referenced[filepath.Clean(rel)] {
				continue
			}
			_ = os.RemoveAll(filepath.Join(base, dir.Name(), child.Name()))
		}
	}
}

// migrateLocked splits a library.json from an older version into the files the
// app uses now, once.
func (s *Store) migrateLocked() error {
	data, err := os.ReadFile(s.legacyPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if _, err := os.Stat(s.foldersPath()); err == nil {
		return nil
	}
	var lib Library
	if err := json.Unmarshal(data, &lib); err != nil {
		return fmt.Errorf("library.json 无法解析: %w", err)
	}
	s.lib = lib
	s.bindings = map[string]string{}
	for _, sc := range lib.Scripts {
		if sc.InterpID != "" {
			s.bindings[sc.ID] = sc.InterpID
		}
	}
	s.conflicts = nil
	if err := s.saveLocked(); err != nil {
		return err
	}
	return os.Rename(s.legacyPath(), s.legacyPath()+".migrated")
}

// upgradeLocked moves a version 1 library to the directory model: the single
// main.<ext> file takes the script's name and becomes its entry. On failure it
// returns before writing the index, so the library on disk stays as it was.
func (s *Store) upgradeLocked(from int) error {
	for _, sc := range s.lib.Scripts {
		dir := s.ScriptDir(*sc)
		entry := strings.ReplaceAll(sc.Entry, "/", string(filepath.Separator))
		if entry == "" || !isRegularFile(filepath.Join(dir, entry)) {
			found, err := legacyEntryFile(dir)
			if err != nil {
				return fmt.Errorf("scripts/%s 迁移失败: %w", sc.ID, err)
			}
			if found == "" {
				return fmt.Errorf("scripts/%s 里没有可以运行的入口文件", sc.ID)
			}
			entry = found
		}
		if from < 2 && !strings.Contains(entry, string(filepath.Separator)) &&
			strings.HasPrefix(strings.ToLower(entry), "main.") {
			ext := strings.TrimPrefix(path.Ext(entry), ".")
			taken, err := topLevelNames(dir)
			if err != nil {
				return fmt.Errorf("scripts/%s 迁移失败: %w", sc.ID, err)
			}
			var others []string
			for _, name := range taken {
				if !strings.EqualFold(name, entry) {
					others = append(others, name)
				}
			}
			target := sanitizeFileName(sc.Name, ext)
			// A name already taken keeps the old file name, as the migration
			// must never overwrite anything.
			if !strings.EqualFold(target, entry) && !nameTakenFold(others, target) {
				if err := os.Rename(filepath.Join(dir, entry), filepath.Join(dir, target)); err != nil {
					return fmt.Errorf("scripts/%s 重命名失败: %w", sc.ID, err)
				}
				entry = target
			}
		}
		sc.Entry = filepath.ToSlash(entry)
		sc.normalize()
		for _, rev := range s.lib.Revisions {
			if rev.ScriptID == sc.ID && isLegacyRevision(*rev) && rev.Entry == "" {
				rev.Entry = sc.Entry
			}
		}
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	return nil
}

// legacyEntryFile picks the entry of a script that has none recorded: the one
// main.* file, or the only managed file, or init.sh.
func legacyEntryFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || managedIgnore(e.Name()) {
			continue
		}
		if isRegularFile(filepath.Join(dir, e.Name())) {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	if len(files) == 1 {
		return files[0], nil
	}
	for _, name := range files {
		if strings.HasPrefix(strings.ToLower(name), "main.") || strings.EqualFold(name, "init.sh") {
			return name, nil
		}
	}
	if len(files) > 0 {
		return files[0], nil
	}
	return "", nil
}

func topLevelNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if managedIgnore(e.Name()) {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

func isRegularFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func (s *Store) saveLocked() error {
	folders := struct {
		Version int       `json:"version"`
		Folders []*Folder `json:"folders"`
	}{storeVersion, s.lib.Folders}
	if err := s.writeJSON(s.foldersPath(), folders); err != nil {
		return err
	}
	for _, sc := range s.lib.Scripts {
		meta := *sc
		meta.InterpID = ""
		if err := s.writeJSON(s.scriptMetaPath(sc.ID), meta); err != nil {
			return err
		}
	}
	lf := localFile{
		Version:      storeVersion,
		Interpreters: s.lib.Interpreters,
		Bindings:     s.bindings,
		Revisions:    s.lib.Revisions,
		Runs:         s.lib.Runs,
		Conflicts:    s.conflicts,
	}
	return s.writeJSON(s.localPath(), lf)
}

func (s *Store) writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newID(prefix string) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + "-" + hex.EncodeToString(b)
}

// ScriptDir is the directory holding every file of a script.
func (s *Store) ScriptDir(sc Script) string {
	return filepath.Join(s.root, "scripts", sc.ID)
}

// ScriptPath is the entry file of a script.
func (s *Store) ScriptPath(sc Script) string {
	return filepath.Join(s.ScriptDir(sc), filepath.FromSlash(sc.Entry))
}

// EntryPath resolves and checks the entry of a script: it must be a relative
// path inside the script directory that names a regular file.
func (s *Store) EntryPath(sc Script) (string, error) {
	rel, err := cleanRelPath(sc.Entry)
	if err != nil {
		return "", err
	}
	return s.resolveFile(sc.ID, rel)
}

// scriptRealDir is the resolved directory of a script. Resolving it once means
// a link anywhere above the script cannot redirect the check below.
func (s *Store) scriptRealDir(scriptID string) (string, error) {
	if !validScriptID(scriptID) {
		return "", errors.New("脚本 ID 不合法")
	}
	dir := filepath.Join(s.root, "scripts", scriptID)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", errors.New("脚本目录不存在")
	}
	return real, nil
}

// resolveFile resolves a path inside a script directory, refusing anything
// that leaves it, directly or through a symbolic link in any part of the path.
func (s *Store) resolveFile(scriptID, rel string) (string, error) {
	rel, err := cleanRelPath(rel)
	if err != nil {
		return "", err
	}
	realDir, err := s.scriptRealDir(scriptID)
	if err != nil {
		return "", err
	}
	real, err := realPath(filepath.Join(realDir, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("%s 不存在", rel)
	}
	if !withinDir(realDir, real) {
		return "", errors.New("路径超出脚本目录")
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("%s 不存在", rel)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s 不是常规文件", rel)
	}
	return real, nil
}

// resolveNew resolves a path that may not exist yet. The closest existing
// ancestor must resolve inside the script directory, so a link cannot be used
// to create a file outside it.
func (s *Store) resolveNew(scriptID, rel string) (string, error) {
	rel, err := cleanRelPath(rel)
	if err != nil {
		return "", err
	}
	realDir, err := s.scriptRealDir(scriptID)
	if err != nil {
		return "", err
	}
	real, err := realPath(filepath.Join(realDir, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	if !withinDir(realDir, real) {
		return "", errors.New("路径超出脚本目录")
	}
	return real, nil
}

// ScriptFiles lists the managed files of a script, sorted by path.
func (s *Store) ScriptFiles(scriptID string) []ScriptFile {
	dir := filepath.Join(s.root, "scripts", scriptID)
	var out []ScriptFile
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == dir {
			return nil
		}
		name := d.Name()
		if managedIgnore(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		out = append(out, ScriptFile{
			Path:     filepath.ToSlash(rel),
			Size:     info.Size(),
			Mode:     uint32(info.Mode().Perm()),
			Editable: info.Size() <= maxEditableBytes,
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ScriptSymlinks lists symbolic links inside a script directory. They are not
// snapshotted or uploaded, so a script that relies on one behaves differently
// once it runs remotely.
func (s *Store) ScriptSymlinks(scriptID string) []string {
	dir := filepath.Join(s.root, "scripts", scriptID)
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == dir {
			return nil
		}
		if managedIgnore(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		if rel, err := filepath.Rel(dir, p); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// IsMultiFile reports whether a script holds more than its entry file.
func (s *Store) IsMultiFile(sc Script) bool {
	files := s.ScriptFiles(sc.ID)
	if len(files) <= 1 {
		return false
	}
	return true
}

// FileEditable reports whether a file is small enough and textual enough for
// the editor. It reads only the head of the file.
func (s *Store) FileEditable(sc Script, rel string) bool {
	abs, err := s.resolveFile(sc.ID, rel)
	if err != nil {
		return false
	}
	info, err := os.Stat(abs)
	if err != nil || info.Size() > maxEditableBytes {
		return false
	}
	f, err := os.Open(abs)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 8<<10)
	n, _ := io.ReadFull(f, head)
	return !isBinaryContent(head[:n])
}

// ReadScriptFile reads one file of a script.
func (s *Store) ReadScriptFile(sc Script, rel string) (string, error) {
	data, err := s.ReadScriptFileBytes(sc, rel)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReadScriptFileBytes reads a file without assuming it is UTF-8.
func (s *Store) ReadScriptFileBytes(sc Script, rel string) ([]byte, error) {
	abs, err := s.resolveFile(sc.ID, rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

// WriteScriptFile writes one file of a script, keeping a snapshot when asked.
func (s *Store) WriteScriptFile(id, rel, content string, snapshot bool) (Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID != id {
			continue
		}
		abs, err := s.resolveFile(sc.ID, rel)
		if err != nil {
			return Revision{}, err
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(abs); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(abs, []byte(content), mode); err != nil {
			return Revision{}, err
		}
		return s.finishWriteLocked(sc, snapshot)
	}
	return Revision{}, errors.New("脚本不存在")
}

func (s *Store) finishWriteLocked(sc *Script, snapshot bool) (Revision, error) {
	sc.UpdatedAt = time.Now()
	if !snapshot {
		return Revision{}, s.saveLocked()
	}
	rev, err := s.addRevisionLocked(sc)
	if errors.Is(err, ErrSnapshotTooLarge) {
		return Revision{}, s.saveLocked()
	}
	if err != nil {
		return Revision{}, err
	}
	return rev, s.saveLocked()
}

// AddScriptFile creates a file inside a script directory.
func (s *Store) AddScriptFile(scriptID, rel, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID != scriptID {
			continue
		}
		abs, err := s.resolveNew(sc.ID, rel)
		if err != nil {
			return err
		}
		if _, err := os.Stat(abs); err == nil {
			return fmt.Errorf("%s 已经存在", rel)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			return err
		}
		sc.UpdatedAt = time.Now()
		return s.saveLocked()
	}
	return errors.New("脚本不存在")
}

// MkdirScript creates a directory inside a script directory.
func (s *Store) MkdirScript(scriptID, rel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID != scriptID {
			continue
		}
		abs, err := s.resolveNew(sc.ID, rel)
		if err != nil {
			return err
		}
		if _, err := os.Stat(abs); err == nil {
			return fmt.Errorf("%s 已经存在", rel)
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return err
		}
		return s.saveLocked()
	}
	return errors.New("脚本不存在")
}

// RenameScriptFile renames a file, keeping Entry in step with it.
func (s *Store) RenameScriptFile(scriptID, rel, next string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID != scriptID {
			continue
		}
		from, err := s.resolveFile(sc.ID, rel)
		if err != nil {
			return err
		}
		to, err := s.resolveNew(sc.ID, next)
		if err != nil {
			return err
		}
		if _, err := os.Stat(to); err == nil {
			return fmt.Errorf("%s 已经存在", next)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		if sc.Entry == rel {
			sc.Entry = next
			sc.normalize()
		}
		sc.UpdatedAt = time.Now()
		return s.saveLocked()
	}
	return errors.New("脚本不存在")
}

// DeleteScriptFile removes a file. The entry cannot be deleted; switch the
// entry first.
func (s *Store) DeleteScriptFile(scriptID, rel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID != scriptID {
			continue
		}
		rel, err := cleanRelPath(rel)
		if err != nil {
			return err
		}
		if sc.Entry == rel {
			return errors.New("这是入口文件，先把它设为别的文件再删除")
		}
		abs, err := s.resolveFile(sc.ID, rel)
		if err != nil {
			return err
		}
		if err := os.Remove(abs); err != nil {
			return err
		}
		sc.UpdatedAt = time.Now()
		return s.saveLocked()
	}
	return errors.New("脚本不存在")
}

// SetEntry points a script at another file inside its directory.
func (s *Store) SetEntry(scriptID, rel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID != scriptID {
			continue
		}
		rel, err := cleanRelPath(rel)
		if err != nil {
			return err
		}
		if _, err := s.resolveFile(sc.ID, rel); err != nil {
			return err
		}
		sc.Entry = rel
		sc.normalize()
		sc.UpdatedAt = time.Now()
		return s.saveLocked()
	}
	return errors.New("脚本不存在")
}

// RunLogPath is the log a run writes.
func (s *Store) RunLogPath(scriptID, runID string) string {
	return filepath.Join(s.root, "logs", scriptID, runID+".log")
}

// Folders returns every folder.
func (s *Store) Folders() []Folder {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Folder, 0, len(s.lib.Folders))
	for _, f := range s.lib.Folders {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

// Folder looks a folder up.
func (s *Store) Folder(id string) (Folder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.lib.Folders {
		if f.ID == id {
			return *f, true
		}
	}
	return Folder{}, false
}

// Scripts returns every script.
func (s *Store) Scripts() []Script {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Script, 0, len(s.lib.Scripts))
	for _, sc := range s.lib.Scripts {
		out = append(out, *sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

// Script looks a script up.
func (s *Store) Script(id string) (Script, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID == id {
			return *sc, true
		}
	}
	return Script{}, false
}

// Interpreters returns every interpreter.
func (s *Store) Interpreters() []Interpreter {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Interpreter, 0, len(s.lib.Interpreters))
	for _, it := range s.lib.Interpreters {
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Interpreter looks an interpreter up.
func (s *Store) Interpreter(id string) (Interpreter, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.lib.Interpreters {
		if it.ID == id {
			return *it, true
		}
	}
	return Interpreter{}, false
}

// Runs returns every run, newest first.
func (s *Store) Runs() []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Run, 0, len(s.lib.Runs))
	for _, r := range s.lib.Runs {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Run looks a run up.
func (s *Store) Run(id string) (Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.lib.Runs {
		if r.ID == id {
			return *r, true
		}
	}
	return Run{}, false
}

// RevisionsOf returns the revisions of a script, newest first.
func (s *Store) RevisionsOf(scriptID string) []Revision {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Revision
	for _, r := range s.lib.Revisions {
		if r.ScriptID == scriptID {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// RevisionContent reads the entry file of a revision.
func (s *Store) RevisionContent(id string) (string, error) {
	s.mu.Lock()
	rev, ok := s.revisionByID(id)
	s.mu.Unlock()
	if !ok {
		return "", errors.New("修订不存在")
	}
	if isLegacyRevision(rev) {
		data, err := os.ReadFile(filepath.Join(s.root, rev.Path))
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	entry := s.RevisionEntry(id)
	if entry == "" {
		files := s.RevisionFiles(id)
		if len(files) == 0 {
			return "", errors.New("这份快照没有文件")
		}
		entry = files[0].Path
	}
	return s.RevisionFileContent(id, entry)
}

func validName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("名称不能为空")
	}
	if len([]rune(name)) > 60 {
		return errors.New("名称不能超过 60 个字符")
	}
	if strings.ContainsAny(name, "/\\:*?\"<>|\x00") || name == "." || name == ".." {
		return errors.New("名称不能包含 / \\ : * ? \" < > |")
	}
	return nil
}

func (s *Store) folderNameTaken(parentID, name, exceptID string) bool {
	for _, f := range s.lib.Folders {
		if f.ParentID == parentID && f.ID != exceptID && strings.EqualFold(f.Name, name) {
			return true
		}
	}
	return false
}

func (s *Store) scriptNameTaken(folderID, name, exceptID string) bool {
	for _, sc := range s.lib.Scripts {
		if sc.FolderID == folderID && sc.ID != exceptID && strings.EqualFold(sc.Name, name) {
			return true
		}
	}
	return false
}

// CreateFolder adds a folder under parentID, "" for the root.
func (s *Store) CreateFolder(parentID, name string) (Folder, error) {
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return Folder{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if parentID != "" {
		found := false
		for _, f := range s.lib.Folders {
			if f.ID == parentID {
				found = true
				break
			}
		}
		if !found {
			return Folder{}, errors.New("上级文件夹不存在")
		}
	}
	if s.folderNameTaken(parentID, name, "") || s.scriptNameTaken(parentID, name, "") {
		return Folder{}, errors.New("同一目录下已经有同名的项")
	}
	pos := 0
	for _, f := range s.lib.Folders {
		if f.ParentID == parentID {
			pos++
		}
	}
	f := &Folder{ID: newID("f"), ParentID: parentID, Name: name, Position: pos}
	s.lib.Folders = append(s.lib.Folders, f)
	if err := s.saveLocked(); err != nil {
		return Folder{}, err
	}
	return *f, nil
}

// RenameFolder renames a folder.
func (s *Store) RenameFolder(id, name string) error {
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.lib.Folders {
		if f.ID != id {
			continue
		}
		if s.folderNameTaken(f.ParentID, name, id) || s.scriptNameTaken(f.ParentID, name, "") {
			return errors.New("同一目录下已经有同名的项")
		}
		f.Name = name
		return s.saveLocked()
	}
	return errors.New("文件夹不存在")
}

// DeleteFolder moves a folder and everything under it to the trash.
func (s *Store) DeleteFolder(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.folderByIDLocked(id); !ok {
		return errors.New("文件夹不存在")
	}
	ids := s.descendantFoldersLocked(id)
	keepFolders := s.lib.Folders[:0]
	for _, f := range s.lib.Folders {
		if !contains(ids, f.ID) {
			keepFolders = append(keepFolders, f)
		}
	}
	s.lib.Folders = keepFolders
	var trashed []*Script
	keepScripts := s.lib.Scripts[:0]
	for _, sc := range s.lib.Scripts {
		if contains(ids, sc.FolderID) {
			trashed = append(trashed, sc)
			continue
		}
		keepScripts = append(keepScripts, sc)
	}
	s.lib.Scripts = keepScripts
	for _, sc := range trashed {
		if err := s.trashScriptLocked(sc); err != nil {
			return err
		}
	}
	return s.saveLocked()
}

func contains(list []string, value string) bool {
	for _, s := range list {
		if s == value {
			return true
		}
	}
	return false
}

func (s *Store) folderByIDLocked(id string) (*Folder, bool) {
	for _, f := range s.lib.Folders {
		if f.ID == id {
			return f, true
		}
	}
	return nil, false
}

func (s *Store) descendantFoldersLocked(id string) []string {
	out := []string{id}
	for i := 0; i < len(out); i++ {
		for _, f := range s.lib.Folders {
			if f.ParentID == out[i] {
				out = append(out, f.ID)
			}
		}
	}
	return out
}

// buildScriptDir makes a staging directory next to the script's own and fills
// it with the files fill writes. The caller swaps it in, so a failure in the
// middle never leaves the script half empty.
func (s *Store) buildScriptDir(scriptID string, fill func(dir string) error) (string, error) {
	if !validScriptID(scriptID) {
		return "", errors.New("脚本 ID 不合法")
	}
	base := filepath.Join(s.root, "scripts")
	tmp, err := os.MkdirTemp(base, ".stage-"+scriptID+"-")
	if err != nil {
		return "", err
	}
	if err := fill(tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	// Keep the metadata with the files: saveLocked writes it again, but a
	// failure right after the swap must not lose the script.
	if data, err := os.ReadFile(s.scriptMetaPath(scriptID)); err == nil {
		_ = os.WriteFile(filepath.Join(tmp, "script.json"), data, 0o644)
	}
	return tmp, nil
}

// swapScriptDir replaces the script directory with a staged one, keeping the
// old directory aside until the new one is in place.
func (s *Store) swapScriptDir(scriptID, staged string) error {
	dir := filepath.Join(s.root, "scripts", scriptID)
	backup := filepath.Join(s.root, "scripts", ".old-"+scriptID+"-"+newID("b"))
	if _, err := os.Stat(dir); err == nil {
		if err := os.Rename(dir, backup); err != nil {
			_ = os.RemoveAll(staged)
			return err
		}
	}
	if err := os.Rename(staged, dir); err != nil {
		_ = os.Rename(backup, dir)
		_ = os.RemoveAll(staged)
		return err
	}
	_ = os.RemoveAll(backup)
	return nil
}

func (s *Store) trashScriptLocked(sc *Script) error {
	dir := filepath.Join(s.root, ".trash", sc.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Move everything first: a failure here leaves the script untouched in the
	// index, instead of deleting records of files that are still on disk.
	if err := os.Rename(filepath.Join(s.root, "scripts", sc.ID), filepath.Join(dir, "content")); err != nil {
		return fmt.Errorf("移到回收站失败: %w", err)
	}
	if err := os.Rename(filepath.Join(s.root, "logs", sc.ID), filepath.Join(dir, "logs")); err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Rename(filepath.Join(dir, "content"), filepath.Join(s.root, "scripts", sc.ID))
		return fmt.Errorf("日志移到回收站失败: %w", err)
	}
	var keep []*Revision
	for _, r := range s.lib.Revisions {
		if r.ScriptID != sc.ID {
			keep = append(keep, r)
		} else {
			_ = os.RemoveAll(filepath.Join(s.root, r.Path))
		}
	}
	s.lib.Revisions = keep
	var keepRuns []*Run
	for _, r := range s.lib.Runs {
		if r.ScriptID != sc.ID {
			keepRuns = append(keepRuns, r)
		}
	}
	s.lib.Runs = keepRuns
	delete(s.bindings, sc.ID)
	return nil
}

// CreateScript adds a single-file script. The file is named after the script,
// so the directory can be read without the app.
func (s *Store) CreateScript(folderID, name, language, ext, interpID string) (Script, error) {
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return Script{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if folderID != "" {
		if _, ok := s.folderByIDLocked(folderID); !ok {
			return Script{}, errors.New("文件夹不存在")
		}
	}
	if s.scriptNameTaken(folderID, name, "") || s.folderNameTaken(folderID, name, "") {
		return Script{}, errors.New("同一目录下已经有同名的项")
	}
	pos := 0
	for _, sc := range s.lib.Scripts {
		if sc.FolderID == folderID {
			pos++
		}
	}
	now := time.Now()
	sc := &Script{ID: newID("s"), FolderID: folderID, Name: name, Language: language,
		InterpID: interpID, Position: pos, CreatedAt: now, UpdatedAt: now}
	sc.Entry = sanitizeFileName(name, ext)
	sc.normalize()
	dir := s.ScriptDir(*sc)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Script{}, err
	}
	taken, err := topLevelNames(dir)
	if err != nil {
		return Script{}, err
	}
	sc.Entry = uniqueFileName(sc.Entry, taken)
	path := s.ScriptPath(*sc)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return Script{}, err
	}
	s.lib.Scripts = append(s.lib.Scripts, sc)
	if interpID != "" {
		s.bindings[sc.ID] = interpID
	}
	if _, err := s.addRevisionLocked(sc); err != nil {
		return Script{}, err
	}
	if err := s.saveLocked(); err != nil {
		return Script{}, err
	}
	return *sc, nil
}

// UpdateScript saves the settings of a script.
func (s *Store) UpdateScript(updated Script) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID != updated.ID {
			continue
		}
		sc.FolderID = updated.FolderID
		sc.InterpID = updated.InterpID
		if updated.InterpID == "" {
			delete(s.bindings, sc.ID)
		} else {
			s.bindings[sc.ID] = updated.InterpID
		}
		if bad := invalidEnvKey(updated.Env); bad != "" {
			return fmt.Errorf("环境变量名 %q 不合法", bad)
		}
		sc.Args = updated.Args
		sc.Env = updated.Env
		sc.Cwd = updated.Cwd
		sc.Timeout = updated.Timeout
		sc.RunAsAdmin = updated.RunAsAdmin
		sc.Dangerous = updated.Dangerous
		sc.Preflight = updated.Preflight
		sc.Profiles = updated.Profiles
		sc.ProfileID = updated.ProfileID
		sc.DefaultName = updated.DefaultName
		sc.HostID = updated.HostID
		sc.HostName = updated.HostName
		sc.HostTarget = updated.HostTarget
		sc.UpdatedAt = time.Now()
		return s.saveLocked()
	}
	return errors.New("脚本不存在")
}

// RenameScript renames a script. renameEntry also renames a single-file
// script's entry to the cleaned new name; multi-file directories keep theirs
// because other files may refer to it.
func (s *Store) RenameScript(id, name string, renameEntry ...bool) error {
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID != id {
			continue
		}
		if s.scriptNameTaken(sc.FolderID, name, id) || s.folderNameTaken(sc.FolderID, name, "") {
			return errors.New("同一目录下已经有同名的项")
		}
		sc.Name = name
		doRename := len(renameEntry) > 0 && renameEntry[0]
		if doRename && !strings.Contains(sc.Entry, "/") {
			dir := s.ScriptDir(*sc)
			old := sc.Entry
			ext := strings.TrimPrefix(path.Ext(old), ".")
			taken, err := topLevelNames(dir)
			if err != nil {
				return err
			}
			var others []string
			for _, n := range taken {
				if !strings.EqualFold(n, old) {
					others = append(others, n)
				}
			}
			target := uniqueFileName(sanitizeFileName(name, ext), others)
			if !strings.EqualFold(target, old) {
				if err := os.Rename(filepath.Join(dir, old), filepath.Join(dir, target)); err != nil {
					return err
				}
				sc.Entry = target
				sc.normalize()
			}
		}
		sc.UpdatedAt = time.Now()
		return s.saveLocked()
	}
	return errors.New("脚本不存在")
}

// siblingPositionLocked is the position a new item takes at the end of a
// folder: after every script and folder it already holds.
func (s *Store) siblingPositionLocked(folderID string) int {
	n := 0
	for _, sc := range s.lib.Scripts {
		if sc.FolderID == folderID {
			n++
		}
	}
	for _, f := range s.lib.Folders {
		if f.ParentID == folderID {
			n++
		}
	}
	return n
}

// MoveScript puts a script in another folder, "" for the root. A name already
// taken there gets a suffix, as an import of the same name does.
func (s *Store) MoveScript(id, folderID string) (Script, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sc *Script
	for _, candidate := range s.lib.Scripts {
		if candidate.ID == id {
			sc = candidate
			break
		}
	}
	if sc == nil {
		return Script{}, errors.New("脚本不存在")
	}
	if sc.FolderID == folderID {
		return *sc, nil
	}
	if folderID != "" {
		if _, ok := s.folderByIDLocked(folderID); !ok {
			return Script{}, errors.New("文件夹不存在")
		}
	}
	sc.Name = s.uniqueScriptNameLocked(folderID, sc.Name)
	sc.FolderID = folderID
	sc.Position = s.siblingPositionLocked(folderID)
	sc.UpdatedAt = time.Now()
	return *sc, s.saveLocked()
}

// MoveFolder puts a folder under another one, "" for the root. A folder cannot
// move into itself or into one of its own descendants, which would cut the
// subtree out of the tree.
func (s *Store) MoveFolder(id, parentID string) (Folder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.folderByIDLocked(id)
	if !ok {
		return Folder{}, errors.New("文件夹不存在")
	}
	if f.ParentID == parentID {
		return *f, nil
	}
	for _, d := range s.descendantFoldersLocked(id) {
		if d == parentID {
			return Folder{}, errors.New("不能把文件夹移到它自己或它的子文件夹里")
		}
	}
	if parentID != "" {
		if _, ok := s.folderByIDLocked(parentID); !ok {
			return Folder{}, errors.New("文件夹不存在")
		}
	}
	// A sibling of the same name would make two rows look alike; the moved
	// one takes a suffix, as an import does.
	name := f.Name
	if s.folderNameTaken(parentID, name, id) || s.scriptNameTaken(parentID, name, "") {
		for i := 2; i < 1000; i++ {
			candidate := fmt.Sprintf("%s (%d)", name, i)
			if !s.folderNameTaken(parentID, candidate, id) && !s.scriptNameTaken(parentID, candidate, "") {
				name = candidate
				break
			}
		}
	}
	f.Name = name
	f.ParentID = parentID
	f.Position = s.siblingPositionLocked(parentID)
	return *f, s.saveLocked()
}

// DeleteScript moves a script to the trash.
func (s *Store) DeleteScript(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sc := range s.lib.Scripts {
		if sc.ID != id {
			continue
		}
		if err := s.trashScriptLocked(sc); err != nil {
			return err
		}
		s.lib.Scripts = append(s.lib.Scripts[:i], s.lib.Scripts[i+1:]...)
		return s.saveLocked()
	}
	return errors.New("脚本不存在")
}

// ReadScript reads the entry of a script.
func (s *Store) ReadScript(sc Script) (string, error) {
	abs, err := s.EntryPath(sc)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteScript writes the entry of a script and keeps a snapshot of the whole
// directory. A directory over the snapshot limit is still saved; only its
// history skips this version.
func (s *Store) WriteScript(id, content string) (Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID != id {
			continue
		}
		abs, err := s.EntryPath(*sc)
		if err != nil {
			return Revision{}, err
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(abs); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(abs, []byte(content), mode); err != nil {
			return Revision{}, err
		}
		return s.finishWriteLocked(sc, true)
	}
	return Revision{}, errors.New("脚本不存在")
}

// AddRevision snapshots the content of a script as it is on disk.
func (s *Store) AddRevision(id string) (Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.ID != id {
			continue
		}
		rev, err := s.addRevisionLocked(sc)
		if err != nil {
			return Revision{}, err
		}
		return rev, s.saveLocked()
	}
	return Revision{}, errors.New("脚本不存在")
}

// snapshotSource is one file going into a snapshot.
type snapshotSource struct {
	rel  string
	abs  string
	mode os.FileMode
	size int64
}

// collectSnapshot lists the files a snapshot covers and their total size.
func (s *Store) collectSnapshot(sc *Script) ([]snapshotSource, int64, error) {
	dir := s.ScriptDir(*sc)
	var files []snapshotSource
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == dir {
			return nil
		}
		if managedIgnore(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		total += info.Size()
		files = append(files, snapshotSource{
			rel:  filepath.ToSlash(rel),
			abs:  p,
			mode: info.Mode().Perm(),
			size: info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, total, nil
}

// addRevisionLocked writes a snapshot of the whole script directory. The hash
// covers relative paths, permission bits and content, so restoring keeps the
// executable bit and an unchanged directory is not stored twice.
func (s *Store) addRevisionLocked(sc *Script) (Revision, error) {
	files, total, err := s.collectSnapshot(sc)
	if err != nil {
		return Revision{}, err
	}
	if total > maxSnapshotBytes {
		return Revision{}, ErrSnapshotTooLarge
	}
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(f.abs)
		if err != nil {
			return Revision{}, err
		}
		fmt.Fprintf(h, "%s\x00%o\x00%d\x00", f.rel, f.mode.Perm(), len(data))
		h.Write(data)
		h.Write([]byte{0})
	}
	hash := hex.EncodeToString(h.Sum(nil))[:8]
	for _, r := range s.lib.Revisions {
		if r.ScriptID == sc.ID && r.Hash == hash && !isLegacyRevision(*r) {
			return *r, nil
		}
	}
	rel := filepath.Join("revisions", sc.ID, hash)
	abs := filepath.Join(s.root, rel)
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return Revision{}, err
	}
	for _, f := range files {
		data, err := os.ReadFile(f.abs)
		if err != nil {
			return Revision{}, err
		}
		target := filepath.Join(abs, filepath.FromSlash(f.rel))
		if !withinDir(abs, target) {
			return Revision{}, errors.New("快照路径超出目录")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return Revision{}, err
		}
		if err := os.WriteFile(target, data, f.mode); err != nil {
			return Revision{}, err
		}
	}
	meta := revisionMeta{Entry: sc.Entry, Bytes: total}
	for _, f := range files {
		meta.Files = append(meta.Files, RevisionFile{Path: f.rel, Size: f.size, Mode: uint32(f.mode.Perm())})
	}
	if err := s.writeJSON(filepath.Join(abs, "meta.json"), meta); err != nil {
		return Revision{}, err
	}
	rev := &Revision{ID: newID("r"), ScriptID: sc.ID, Hash: hash, Path: rel,
		Entry: sc.Entry, Files: len(files), Size: total, CreatedAt: time.Now()}
	s.lib.Revisions = append(s.lib.Revisions, rev)
	return *rev, nil
}

// revisionMeta is written next to every snapshot.
type revisionMeta struct {
	Entry      string         `json:"entry"`
	Files      []RevisionFile `json:"files"`
	Bytes      int64          `json:"bytes"`
	Incomplete bool           `json:"incomplete,omitempty"`
}

func isLegacyRevision(r Revision) bool {
	return strings.HasSuffix(r.Path, ".txt")
}

func (s *Store) revisionByID(id string) (Revision, bool) {
	for _, r := range s.lib.Revisions {
		if r.ID == id {
			return *r, true
		}
	}
	return Revision{}, false
}

// RevisionFiles lists the files of a snapshot, or a single pseudo entry for a
// revision from the old single-file format.
func (s *Store) RevisionFiles(id string) []RevisionFile {
	s.mu.Lock()
	rev, ok := s.revisionByID(id)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if isLegacyRevision(rev) {
		return []RevisionFile{{Path: rev.Entry, Size: rev.Size, Mode: 0o644}}
	}
	data, err := os.ReadFile(filepath.Join(s.root, rev.Path, "meta.json"))
	if err == nil {
		var meta revisionMeta
		if json.Unmarshal(data, &meta) == nil && len(meta.Files) > 0 {
			return meta.Files
		}
	}
	var out []RevisionFile
	_ = filepath.WalkDir(filepath.Join(s.root, rev.Path), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == "meta.json" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(filepath.Join(s.root, rev.Path), p)
		if err != nil {
			return nil
		}
		out = append(out, RevisionFile{Path: filepath.ToSlash(rel), Size: info.Size(), Mode: uint32(info.Mode().Perm())})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// RevisionEntry is the entry a snapshot recorded.
func (s *Store) RevisionEntry(id string) string {
	s.mu.Lock()
	rev, ok := s.revisionByID(id)
	s.mu.Unlock()
	if !ok {
		return ""
	}
	if isLegacyRevision(rev) {
		return rev.Entry
	}
	data, err := os.ReadFile(filepath.Join(s.root, rev.Path, "meta.json"))
	if err != nil {
		return rev.Entry
	}
	var meta revisionMeta
	if json.Unmarshal(data, &meta) != nil {
		return rev.Entry
	}
	if meta.Entry != "" {
		return meta.Entry
	}
	return rev.Entry
}

// RevisionFileContent reads one file out of a snapshot.
func (s *Store) RevisionFileContent(id, rel string) (string, error) {
	s.mu.Lock()
	rev, ok := s.revisionByID(id)
	s.mu.Unlock()
	if !ok {
		return "", errors.New("修订不存在")
	}
	if isLegacyRevision(rev) {
		data, err := os.ReadFile(filepath.Join(s.root, rev.Path))
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	rel, err := cleanRelPath(rel)
	if err != nil {
		return "", err
	}
	base := filepath.Join(s.root, rev.Path)
	abs := filepath.Join(base, filepath.FromSlash(rel))
	if !withinDir(base, abs) {
		return "", errors.New("路径超出快照目录")
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RestoreRevision puts a snapshot back over the whole script directory. An
// incomplete or unreadable snapshot is refused rather than restored.
func (s *Store) RestoreRevision(scriptID, revID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sc *Script
	for _, candidate := range s.lib.Scripts {
		if candidate.ID == scriptID {
			sc = candidate
			break
		}
	}
	if sc == nil {
		return errors.New("脚本不存在")
	}
	rev, ok := s.revisionByID(revID)
	if !ok || rev.ScriptID != scriptID {
		return errors.New("修订不存在")
	}
	if isLegacyRevision(rev) {
		data, err := os.ReadFile(filepath.Join(s.root, rev.Path))
		if err != nil {
			return err
		}
		abs, err := s.EntryPath(*sc)
		if err != nil {
			return err
		}
		return os.WriteFile(abs, data, 0o644)
	}
	snapDir := filepath.Join(s.root, rev.Path)
	data, err := os.ReadFile(filepath.Join(snapDir, "meta.json"))
	if err != nil {
		return errors.New("这份快照不完整，无法恢复")
	}
	var meta revisionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return errors.New("这份快照的 meta.json 无法解析")
	}
	if meta.Incomplete {
		return errors.New("这份快照不完整，无法恢复")
	}
	// Read every file before touching the script directory: a snapshot with a
	// missing or unreadable file must fail with the old content still in place.
	type pending struct {
		rel     string
		content []byte
		mode    os.FileMode
	}
	var files []pending
	for _, f := range meta.Files {
		rel, err := cleanRelPath(f.Path)
		if err != nil {
			return errors.New("快照里有不合法的路径: " + f.Path)
		}
		content, err := os.ReadFile(filepath.Join(snapDir, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("快照缺少 %s，无法恢复", rel)
		}
		files = append(files, pending{rel: rel, content: content, mode: os.FileMode(f.Mode).Perm()})
	}
	staged, err := s.buildScriptDir(sc.ID, func(dir string) error {
		for _, f := range files {
			dst := filepath.Join(dir, filepath.FromSlash(f.rel))
			if !withinDir(dir, dst) {
				return errors.New("快照路径超出脚本目录")
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(dst, f.content, f.mode); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	oldEntry := sc.Entry
	if meta.Entry != "" {
		sc.Entry = meta.Entry
		sc.normalize()
	}
	if err := s.swapScriptDir(sc.ID, staged); err != nil {
		sc.Entry = oldEntry
		sc.normalize()
		return err
	}
	if _, err := s.resolveFile(sc.ID, sc.Entry); err != nil {
		sc.Entry = pickImportEntry(s.ScriptDir(*sc), s.ScriptFiles(sc.ID))
		sc.normalize()
	}
	sc.UpdatedAt = time.Now()
	if _, err := s.addRevisionLocked(sc); err != nil && !errors.Is(err, ErrSnapshotTooLarge) {
		return err
	}
	return s.saveLocked()
}

// CreateRun records a run that is starting.
func (s *Store) CreateRun(run Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := run
	s.lib.Runs = append(s.lib.Runs, &r)
	return s.saveLocked()
}

// FinishRun records how a run ended.
func (s *Store) FinishRun(run Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.lib.Runs {
		if r.ID != run.ID {
			continue
		}
		r.Status = run.Status
		r.Uploading = false
		r.ExitCode = run.ExitCode
		r.EndedAt = run.EndedAt
		r.LogBytes = run.LogBytes
		r.Truncated = run.Truncated
		return s.saveLocked()
	}
	return errors.New("运行记录不存在")
}

// CreateInterpreter adds an interpreter.
func (s *Store) CreateInterpreter(it Interpreter) (Interpreter, error) {
	if strings.TrimSpace(it.Name) == "" {
		return Interpreter{}, errors.New("解释器名称不能为空")
	}
	if strings.TrimSpace(it.Path) == "" {
		return Interpreter{}, errors.New("解释器路径不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	it.ID = newID("i")
	for _, old := range s.lib.Interpreters {
		if old.Path == it.Path && strings.Join(old.Args, " ") == strings.Join(it.Args, " ") {
			return Interpreter{}, errors.New("相同的解释器已经存在")
		}
	}
	cp := it
	s.lib.Interpreters = append(s.lib.Interpreters, &cp)
	return cp, s.saveLocked()
}

// UpdateInterpreter saves an interpreter, keeping its identity.
func (s *Store) UpdateInterpreter(it Interpreter) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.lib.Interpreters {
		if old.ID != it.ID {
			continue
		}
		old.Name = it.Name
		old.Language = it.Language
		old.Path = it.Path
		old.Args = it.Args
		old.ProbeArgs = it.ProbeArgs
		old.Version = it.Version
		old.State = it.State
		old.CheckedAt = it.CheckedAt
		return s.saveLocked()
	}
	return errors.New("解释器不存在")
}

// DeleteInterpreter removes an interpreter no script uses.
func (s *Store) DeleteInterpreter(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sc := range s.lib.Scripts {
		if sc.InterpID == id {
			return fmt.Errorf("「%s」还在使用这个解释器", sc.Name)
		}
	}
	for i, it := range s.lib.Interpreters {
		if it.ID == id {
			s.lib.Interpreters = append(s.lib.Interpreters[:i], s.lib.Interpreters[i+1:]...)
			return s.saveLocked()
		}
	}
	return errors.New("解释器不存在")
}

// Usage counts the scripts that use an interpreter.
func (s *Store) Usage(interpID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, sc := range s.lib.Scripts {
		if sc.InterpID == interpID {
			n++
		}
	}
	return n
}

// DeleteRuns removes run records and their logs. A run that is still going is
// kept: its log file is being written. It reports how many were removed.
func (s *Store) DeleteRuns(ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	keep := make([]*Run, 0, len(s.lib.Runs))
	for _, r := range s.lib.Runs {
		if !drop[r.ID] || r.Status == RunRunning {
			keep = append(keep, r)
			continue
		}
		_ = os.Remove(filepath.Join(s.root, r.LogPath))
		_ = os.Remove(s.adminStatusPath(r.ScriptID, r.ID))
		removed++
	}
	if removed == 0 {
		return 0, nil
	}
	s.lib.Runs = keep
	return removed, s.saveLocked()
}

// DeleteRun removes one run record and its log.
func (s *Store) DeleteRun(id string) error {
	removed, err := s.DeleteRuns([]string{id})
	if err != nil {
		return err
	}
	if removed == 0 {
		return errors.New("这次运行还没有结束，不能删除")
	}
	return nil
}

// PruneRuns drops runs older than days and their logs, 0 keeps every run.
func (s *Store) PruneRuns(days int) error {
	if days <= 0 {
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	s.mu.Lock()
	defer s.mu.Unlock()
	var keep []*Run
	for _, r := range s.lib.Runs {
		if r.StartedAt.Before(cutoff) && r.Status != RunRunning {
			_ = os.Remove(filepath.Join(s.root, r.LogPath))
			_ = os.Remove(s.adminStatusPath(r.ScriptID, r.ID))
			continue
		}
		keep = append(keep, r)
	}
	s.lib.Runs = keep
	return s.saveLocked()
}

// PruneRevisions keeps the newest keep revisions of each script.
// ClearRevisions deletes every revision of a script except the newest one.
func (s *Store) ClearRevisions(scriptID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var revs []*Revision
	for _, r := range s.lib.Revisions {
		if r.ScriptID == scriptID {
			revs = append(revs, r)
		}
	}
	if len(revs) <= 1 {
		return 0, nil
	}
	sort.Slice(revs, func(i, j int) bool { return revs[i].CreatedAt.After(revs[j].CreatedAt) })
	drop := map[string]bool{}
	for _, r := range revs[1:] {
		drop[r.ID] = true
		_ = os.RemoveAll(filepath.Join(s.root, r.Path))
	}
	var kept []*Revision
	for _, r := range s.lib.Revisions {
		if !drop[r.ID] {
			kept = append(kept, r)
		}
	}
	s.lib.Revisions = kept
	return len(drop), s.saveLocked()
}

// DeleteRevision drops one snapshot. The script file itself is untouched.
func (s *Store) DeleteRevision(scriptID, revID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]*Revision, 0, len(s.lib.Revisions))
	found := false
	for _, r := range s.lib.Revisions {
		if r.ID == revID && r.ScriptID == scriptID {
			found = true
			_ = os.RemoveAll(filepath.Join(s.root, r.Path))
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return fmt.Errorf("没有找到这份历史版本")
	}
	s.lib.Revisions = kept
	return s.saveLocked()
}

func (s *Store) PruneRevisions(keep int) error {
	if keep <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	byScript := map[string][]*Revision{}
	for _, r := range s.lib.Revisions {
		byScript[r.ScriptID] = append(byScript[r.ScriptID], r)
	}
	drop := map[string]bool{}
	for _, revs := range byScript {
		sort.Slice(revs, func(i, j int) bool { return revs[i].CreatedAt.After(revs[j].CreatedAt) })
		for i, r := range revs {
			if i >= keep {
				drop[r.ID] = true
				_ = os.RemoveAll(filepath.Join(s.root, r.Path))
			}
		}
	}
	var keepRevs []*Revision
	for _, r := range s.lib.Revisions {
		if !drop[r.ID] {
			keepRevs = append(keepRevs, r)
		}
	}
	s.lib.Revisions = keepRevs
	return s.saveLocked()
}

// Conflicts lists the scripts whose local and remote copies both changed.
func (s *Store) Conflicts() []SyncConflict {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SyncConflict, len(s.conflicts))
	copy(out, s.conflicts)
	return out
}

// SaveConflicts replaces the pending conflict list.
func (s *Store) SaveConflicts(list []SyncConflict) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conflicts = list
	return s.saveLocked()
}
