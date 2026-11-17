package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// BundleFormat names the file an export writes, so an import can tell a script
// library from any other zip.
const BundleFormat = "script-manager/export"

// BundleVersion is the version of the manifest layout.
const BundleVersion = 2

// BundleManifest describes everything an export holds, so an import can show it
// before touching the library.
type BundleManifest struct {
	Format     string         `json:"format"`
	Version    int            `json:"version"`
	ExportedAt time.Time      `json:"exported_at"`
	App        string         `json:"app"`
	Folders    []BundleFolder `json:"folders"`
	Scripts    []BundleScript `json:"scripts"`
}

// BundleFolder is a folder in an export, named by the path from the root.
type BundleFolder struct {
	Path     []string `json:"path"`
	Position int      `json:"position"`
}

// BundleScript is one script in an export.
type BundleScript struct {
	ID          string            `json:"id"`
	Folder      []string          `json:"folder"`
	Name        string            `json:"name"`
	Entry       string            `json:"entry"`
	Ext         string            `json:"ext,omitempty"`
	Language    string            `json:"language"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	Timeout     int               `json:"timeout,omitempty"`
	RunAsAdmin  bool              `json:"run_as_admin,omitempty"`
	Dangerous   bool              `json:"dangerous,omitempty"`
	Preflight   bool              `json:"preflight,omitempty"`
	Profiles    []RunProfile      `json:"profiles,omitempty"`
	ProfileID   string            `json:"profile_id,omitempty"`
	HostName    string            `json:"host_name,omitempty"`
	HostTarget  string            `json:"host_target,omitempty"`
	Hash        string            `json:"hash"`
	Files       []BundleFile      `json:"files,omitempty"`
	Interpreter BundleInterp      `json:"interpreter"`
	Revisions   []BundleRevision  `json:"revisions,omitempty"`
}

// BundleFile is one file of a script, with the hash and permission bits the
// import checks and restores.
type BundleFile struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Mode uint32 `json:"mode"`
	Size int64  `json:"size"`
}

// BundleInterp is the interpreter a script was bound to when it was exported.
type BundleInterp struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	Path     string `json:"path"`
	Version  string `json:"version"`
}

// BundleRevision is one saved revision of a script.
type BundleRevision struct {
	ID        string    `json:"id"`
	ScriptID  string    `json:"script_id"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
}

// ExportOptions picks what goes into an export.
type ExportOptions struct {
	ScriptIDs        []string
	IncludeRevisions bool
	// Host returns the name and SSH target of a remote host, so an export can
	// carry the run target even though remote.json is machine local.
	Host func(hostID string) (name, target string)
}

// ImportConflict decides what happens to a script whose name is already taken.
type ImportConflict string

const (
	ConflictSkip      ImportConflict = "skip"
	ConflictCopy      ImportConflict = "copy"
	ConflictOverwrite ImportConflict = "overwrite"
)

// ImportOptions picks what an import brings in and how it resolves names.
type ImportOptions struct {
	ScriptIDs []string
	Conflict  ImportConflict
	// BindHost maps an exported host target/name to a host on this machine.
	// Returning "" leaves the script unbound, with its target name kept.
	BindHost func(name, target string) string
}

// ImportResult reports what an import changed.
type ImportResult struct {
	Imported []string
	Skipped  []string
	Replaced []string
	Unbound  []string
}

// ExportLibrary writes the selected scripts into a zip at target.
func ExportLibrary(store *Store, target string, opts ExportOptions) error {
	folders := store.Folders()
	folderPaths := make(map[string][]string, len(folders))
	byID := make(map[string]Folder, len(folders))
	for _, f := range folders {
		byID[f.ID] = f
	}
	var walk func(id string, depth int) []string
	walk = func(id string, depth int) []string {
		if depth > 8 {
			return nil
		}
		f, ok := byID[id]
		if !ok {
			return nil
		}
		return append(walk(f.ParentID, depth+1), f.Name)
	}
	for _, f := range folders {
		folderPaths[f.ID] = walk(f.ID, 0)
	}

	scripts := store.Scripts()
	if len(opts.ScriptIDs) > 0 {
		wanted := make(map[string]bool, len(opts.ScriptIDs))
		for _, id := range opts.ScriptIDs {
			wanted[id] = true
		}
		var keep []Script
		for _, sc := range scripts {
			if wanted[sc.ID] {
				keep = append(keep, sc)
			}
		}
		scripts = keep
	}
	if len(scripts) == 0 {
		return errors.New("没有选中任何脚本")
	}

	manifest := BundleManifest{
		Format:     BundleFormat,
		Version:    BundleVersion,
		ExportedAt: time.Now(),
		App:        "script-manager",
	}

	used := map[string]bool{}
	for _, sc := range scripts {
		if p := folderPaths[sc.FolderID]; len(p) > 0 {
			if !used[strings.Join(p, "/")] {
				used[strings.Join(p, "/")] = true
				manifest.Folders = append(manifest.Folders, BundleFolder{Path: p, Position: sc.Position})
			}
		}
		entry, err := store.ReadScript(sc)
		if err != nil {
			return err
		}
		bs := BundleScript{
			ID:         sc.ID,
			Folder:     folderPaths[sc.FolderID],
			Name:       sc.Name,
			Entry:      sc.Entry,
			Ext:        sc.Ext,
			Language:   sc.Language,
			Args:       sc.Args,
			Env:        sc.Env,
			Cwd:        sc.Cwd,
			Timeout:    sc.Timeout,
			RunAsAdmin: sc.RunAsAdmin,
			Dangerous:  sc.Dangerous,
			Preflight:  sc.Preflight,
			Profiles:   sc.Profiles,
			ProfileID:  sc.ProfileID,
			HostName:   sc.HostName,
			HostTarget: sc.HostTarget,
			Hash:       contentHash(entry),
		}
		if bs.HostName == "" && bs.HostTarget == "" && opts.Host != nil && sc.HostID != "" {
			bs.HostName, bs.HostTarget = opts.Host(sc.HostID)
		}
		for _, f := range store.ScriptFiles(sc.ID) {
			body, err := store.ReadScriptFile(sc, f.Path)
			if err != nil {
				return err
			}
			bs.Files = append(bs.Files, BundleFile{Path: f.Path, Hash: contentHash(body), Mode: f.Mode, Size: f.Size})
		}
		if it, ok := store.Interpreter(sc.InterpID); ok {
			bs.Interpreter = BundleInterp{Name: it.Name, Language: it.Language, Path: it.Path, Version: it.Version}
		}
		if opts.IncludeRevisions {
			for _, rev := range store.RevisionsOf(sc.ID) {
				bs.Revisions = append(bs.Revisions, BundleRevision{ID: rev.ID, ScriptID: sc.ID, Hash: rev.Hash, CreatedAt: rev.CreatedAt})
			}
		}
		manifest.Scripts = append(manifest.Scripts, bs)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer file.Close()
	zw := zip.NewWriter(file)
	meta, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeZipFile(zw, "manifest.json", meta); err != nil {
		return err
	}
	for _, bs := range manifest.Scripts {
		sc, ok := store.Script(bs.ID)
		if !ok {
			continue
		}
		for _, f := range bs.Files {
			body, err := store.ReadScriptFile(sc, f.Path)
			if err != nil {
				return err
			}
			hdr := &zip.FileHeader{Name: path.Join("scripts", bs.ID, f.Path), Method: zip.Deflate}
			hdr.SetMode(os.FileMode(f.Mode).Perm())
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			if _, err := w.Write([]byte(body)); err != nil {
				return err
			}
		}
		if opts.IncludeRevisions {
			for _, rev := range bs.Revisions {
				content, err := store.RevisionContent(rev.ID)
				if err != nil {
					continue
				}
				ext := bs.Ext
				if ext == "" {
					ext = "txt"
				}
				name := path.Join("revisions", bs.ID, rev.ID+"."+ext)
				if err := writeZipFile(zw, name, []byte(content)); err != nil {
					return err
				}
			}
		}
	}
	return zw.Close()
}

func writeZipFile(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// InspectBundle reads the manifest of an export without changing anything.
func InspectBundle(file string) (*BundleManifest, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("无法打开文件: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, 8<<20))
		if err != nil {
			return nil, err
		}
		var manifest BundleManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, fmt.Errorf("manifest.json 无法解析: %w", err)
		}
		if manifest.Format != BundleFormat {
			return nil, fmt.Errorf("这个文件不是 script-manager 的导出文件")
		}
		if manifest.Version < 1 || manifest.Version > BundleVersion {
			return nil, fmt.Errorf("导出文件的版本 %d 无法识别", manifest.Version)
		}
		return &manifest, nil
	}
	return nil, errors.New("导出文件里没有 manifest.json")
}

// ImportLibrary brings the selected scripts of an export into the library.
func ImportLibrary(store *Store, file string, opts ImportOptions) (ImportResult, error) {
	var result ImportResult
	manifest, err := InspectBundle(file)
	if err != nil {
		return result, err
	}
	zr, err := zip.OpenReader(file)
	if err != nil {
		return result, err
	}
	defer zr.Close()

	wanted := map[string]bool{}
	for _, id := range opts.ScriptIDs {
		wanted[id] = true
	}
	for _, bs := range manifest.Scripts {
		if len(wanted) > 0 && !wanted[bs.ID] {
			continue
		}
		dir, err := extractBundleScript(&zr.Reader, bs)
		if err != nil {
			return result, err
		}
		entryRel := bs.Entry
		if entryRel == "" {
			entryRel = "main." + bs.Ext
		}
		folderID, err := store.EnsureFolderPath(bs.Folder)
		if err != nil {
			os.RemoveAll(dir)
			return result, err
		}
		existing, taken := store.ScriptByName(folderID, bs.Name)
		if taken && opts.Conflict == ConflictSkip {
			result.Skipped = append(result.Skipped, bs.Name)
			os.RemoveAll(dir)
			continue
		}
		interpID := pickInterpreter(store, bs.Interpreter)
		var script Script
		switch {
		case taken && opts.Conflict == ConflictOverwrite:
			if err := store.ReplaceScriptFiles(existing.ID, dir, entryRel); err != nil {
				os.RemoveAll(dir)
				return result, err
			}
			script = existing
			result.Replaced = append(result.Replaced, bs.Name)
		default:
			imported, err := store.ImportDir(dir, folderID, bs.Name, interpID)
			if err != nil {
				os.RemoveAll(dir)
				return result, err
			}
			script = imported
			result.Imported = append(result.Imported, script.Name)
		}
		os.RemoveAll(dir)
		if entryRel != "" && script.Entry != entryRel {
			if err := store.SetEntry(script.ID, entryRel); err != nil {
				return result, fmt.Errorf("%s 的入口 %s 无法设置: %w", bs.Name, entryRel, err)
			}
			script.Entry = entryRel
		}
		updated := script
		updated.Args = bs.Args
		updated.Env = bs.Env
		updated.Cwd = bs.Cwd
		updated.Timeout = bs.Timeout
		updated.RunAsAdmin = bs.RunAsAdmin
		updated.Dangerous = bs.Dangerous
		updated.Preflight = bs.Preflight
		updated.Profiles = bs.Profiles
		updated.ProfileID = bs.ProfileID
		updated.HostName = bs.HostName
		updated.HostTarget = bs.HostTarget
		updated.HostID = ""
		if opts.BindHost != nil && (bs.HostName != "" || bs.HostTarget != "") {
			updated.HostID = opts.BindHost(bs.HostName, bs.HostTarget)
		}
		updated.InterpID = interpID
		if err := store.UpdateScript(updated); err != nil {
			return result, err
		}
		script = updated
		if interpID == "" {
			result.Unbound = append(result.Unbound, script.Name)
		}
	}
	return result, nil
}

// extractBundleScript unpacks one script into a temporary directory, checking
// every file against the manifest before it lands.
func extractBundleScript(zr *zip.Reader, bs BundleScript) (string, error) {
	dir, err := os.MkdirTemp("", "script-manager-import-")
	if err != nil {
		return "", err
	}
	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	prefix := path.Join("scripts", bs.ID) + "/"
	files := bs.Files
	if len(files) == 0 {
		files = []BundleFile{{Path: "main." + bs.Ext, Hash: bs.Hash, Mode: 0o644}}
	}
	for _, want := range files {
		rel, err := cleanRelPath(want.Path)
		if err != nil {
			os.RemoveAll(dir)
			return "", fmt.Errorf("%s：%w", want.Path, err)
		}
		zf, ok := byName[prefix+rel]
		if !ok {
			os.RemoveAll(dir)
			return "", fmt.Errorf("导出文件缺少 %s 的 %s", bs.Name, rel)
		}
		body, err := readZipFile(zf)
		if err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		if want.Hash != "" && contentHash(string(body)) != want.Hash {
			os.RemoveAll(dir)
			return "", fmt.Errorf("%s 的 %s 与清单不一致", bs.Name, rel)
		}
		mode := os.FileMode(want.Mode).Perm()
		if mode == 0 {
			mode = zf.Mode().Perm()
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if !withinDir(dir, target) {
			os.RemoveAll(dir)
			return "", errors.New("导出文件里的路径超出脚本目录")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		if err := os.WriteFile(target, body, mode); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 32<<20))
}

// EnsureFolderPath makes every folder of a path and returns the deepest one.
func (s *Store) EnsureFolderPath(names []string) (string, error) {
	parent := ""
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		found := ""
		for _, f := range s.Folders() {
			if f.ParentID == parent && f.Name == name {
				found = f.ID
				break
			}
		}
		if found == "" {
			f, err := s.CreateFolder(parent, name)
			if err != nil {
				return "", err
			}
			found = f.ID
		}
		parent = found
	}
	return parent, nil
}

// ScriptByName finds a script in a folder by its display name.
func (s *Store) ScriptByName(folderID, name string) (Script, bool) {
	for _, sc := range s.Scripts() {
		if sc.FolderID == folderID && sc.Name == name {
			return sc, true
		}
	}
	return Script{}, false
}

// ReplaceScriptContent writes a script's content and records a revision.
func (s *Store) ReplaceScriptContent(id, content string) error {
	_, err := s.WriteScript(id, content)
	return err
}

func copyName(store *Store, folderID, name string) string {
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s (%d)", name, i)
		if _, taken := store.ScriptByName(folderID, candidate); !taken {
			return candidate
		}
	}
	return name + " 副本"
}

func pickInterpreter(store *Store, want BundleInterp) string {
	var fallback string
	for _, it := range store.Interpreters() {
		if it.Language != want.Language {
			continue
		}
		if it.Name == want.Name {
			return it.ID
		}
		if fallback == "" && it.State == StateOK {
			fallback = it.ID
		}
	}
	return fallback
}

func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:8])
}
