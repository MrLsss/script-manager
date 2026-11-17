package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// copyTree copies a directory into a script directory. Limits are enforced
// while copying, not only while scanning, so a file swapped in mid-import
// cannot slip past them. Symbolic links, FIFOs and device files are refused.
func copyTree(ctx context.Context, srcDir, destDir string, progress func(done int, bytes int64)) ([]ScriptFile, error) {
	var files []ScriptFile
	var total int64
	err := filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == srcDir {
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
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("不支持符号链接：%s", d.Name())
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("只复制常规文件：%s", d.Name())
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		clean, err := cleanRelPath(filepath.ToSlash(rel))
		if err != nil {
			return fmt.Errorf("%s：%w", rel, err)
		}
		if info.Size() > maxImportFile {
			return fmt.Errorf("%s 超过单文件 %d MB 的限制", clean, maxImportFile>>20)
		}
		if len(files) >= maxImportFiles {
			return fmt.Errorf("文件超过 %d 个的限制", maxImportFiles)
		}
		total += info.Size()
		if total > maxImportTotal {
			return fmt.Errorf("总大小超过 %d MB 的限制", maxImportTotal>>20)
		}
		if err := copyOneFile(p, filepath.Join(destDir, filepath.FromSlash(clean)), info.Mode().Perm()); err != nil {
			return err
		}
		files = append(files, ScriptFile{Path: clean, Size: info.Size(), Mode: uint32(info.Mode().Perm())})
		if progress != nil {
			progress(len(files), total)
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

// copyOneFile copies a file, re-checking its size as it goes.
func copyOneFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(in, maxImportFile+1))
	closeErr := out.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n > maxImportFile {
		return fmt.Errorf("%s 超过单文件大小限制", filepath.Base(dst))
	}
	return os.Chmod(dst, mode)
}

// pickImportEntry chooses the entry of an imported directory: init.sh, then a
// main.* file, then the only executable file, then the first file.
func pickImportEntry(dir string, files []ScriptFile) string {
	if len(files) == 0 {
		return ""
	}
	for _, f := range files {
		if !strings.Contains(f.Path, "/") && strings.EqualFold(f.Path, "init.sh") {
			return f.Path
		}
	}
	for _, f := range files {
		if !strings.Contains(f.Path, "/") && strings.HasPrefix(strings.ToLower(f.Path), "main.") {
			return f.Path
		}
	}
	var execs []string
	for _, f := range files {
		if !strings.Contains(f.Path, "/") && f.Mode&0o111 != 0 {
			execs = append(execs, f.Path)
		}
	}
	if len(execs) == 1 {
		return execs[0]
	}
	return files[0].Path
}

// ImportDir copies an external directory into the library as one script.
func (s *Store) ImportDir(srcDir, folderID, name, interpID string) (Script, error) {
	return s.ImportDirContext(context.Background(), srcDir, folderID, name, interpID, nil)
}

// ImportDirContext is ImportDir with progress and cancellation.
func (s *Store) ImportDirContext(ctx context.Context, srcDir, folderID, name, interpID string, progress func(done int, bytes int64)) (Script, error) {
	srcDir = strings.TrimSpace(srcDir)
	if srcDir == "" {
		return Script{}, errors.New("请选择要导入的目录")
	}
	info, err := os.Stat(srcDir)
	if err != nil {
		return Script{}, err
	}
	if !info.IsDir() {
		return Script{}, errors.New("请选择一个目录")
	}
	// Copying a directory that holds the destination would recurse forever
	// and duplicate the whole library into the new script.
	if insidePath(srcDir, filepath.Join(s.root, "scripts")) {
		return Script{}, errors.New("不能导入脚本库自己的目录")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(srcDir)
	}
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
	name = s.uniqueScriptNameLocked(folderID, name)
	sc, err := s.copyIntoLibraryLocked(ctx, srcDir, folderID, name, interpID, nil, progress)
	if err != nil {
		return Script{}, err
	}
	return *sc, nil
}

// ImportFile copies one external file into the library as a script.
func (s *Store) ImportFile(srcFile, folderID, name, interpID string) (Script, error) {
	return s.ImportFileContext(context.Background(), srcFile, folderID, name, interpID, nil)
}

// ImportFileContext is ImportFile with progress and cancellation.
func (s *Store) ImportFileContext(ctx context.Context, srcFile, folderID, name, interpID string, progress func(done int, bytes int64)) (Script, error) {
	srcFile = strings.TrimSpace(srcFile)
	if srcFile == "" {
		return Script{}, errors.New("请选择要导入的文件")
	}
	info, err := os.Lstat(srcFile)
	if err != nil {
		return Script{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Script{}, errors.New("不支持符号链接")
	}
	if !info.Mode().IsRegular() {
		return Script{}, errors.New("只能导入常规文件")
	}
	if info.Size() > maxImportFile {
		return Script{}, fmt.Errorf("文件超过 %d MB 的限制", maxImportFile>>20)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(srcFile), filepath.Ext(srcFile))
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(srcFile)
	}
	if err := validName(name); err != nil {
		return Script{}, err
	}
	ext := strings.TrimPrefix(filepath.Ext(srcFile), ".")
	if ext == "" {
		ext = "txt"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if folderID != "" {
		if _, ok := s.folderByIDLocked(folderID); !ok {
			return Script{}, errors.New("文件夹不存在")
		}
	}
	name = s.uniqueScriptNameLocked(folderID, name)
	entry := sanitizeFileName(name, ext)
	sc, err := s.copyIntoLibraryLocked(ctx, srcFile, folderID, name, interpID, map[string]string{entry: srcFile}, progress)
	if err != nil {
		return Script{}, err
	}
	return *sc, nil
}

// copyIntoLibraryLocked makes the script directory, copies the source into it
// and records the script. A failure removes the half-made directory.
func (s *Store) copyIntoLibraryLocked(ctx context.Context, src, folderID, name, interpID string, single map[string]string, progress func(done int, bytes int64)) (*Script, error) {
	pos := 0
	for _, sc := range s.lib.Scripts {
		if sc.FolderID == folderID {
			pos++
		}
	}
	id := newID("s")
	dest := filepath.Join(s.root, "scripts", id)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dest)
		}
	}()
	var files []ScriptFile
	if single != nil {
		for rel, from := range single {
			info, err := os.Stat(from)
			if err != nil {
				return nil, err
			}
			if err := copyOneFile(from, filepath.Join(dest, filepath.FromSlash(rel)), info.Mode().Perm()); err != nil {
				return nil, err
			}
			files = append(files, ScriptFile{Path: rel, Size: info.Size(), Mode: uint32(info.Mode().Perm())})
			if progress != nil {
				progress(len(files), info.Size())
			}
		}
	} else {
		var err error
		files, err = copyTree(ctx, src, dest, progress)
		if err != nil {
			return nil, err
		}
	}
	entry := pickImportEntry(dest, files)
	if entry == "" {
		return nil, errors.New("目录里没有可以运行的文件")
	}
	now := time.Now()
	sc := &Script{ID: id, FolderID: folderID, Name: name, Entry: entry,
		InterpID: interpID, Position: pos, CreatedAt: now, UpdatedAt: now}
	sc.normalize()
	if lang, ok := languageForEntry(entry); ok {
		sc.Language = lang
	}
	s.lib.Scripts = append(s.lib.Scripts, sc)
	if interpID != "" {
		s.bindings[id] = interpID
	}
	if _, err := s.addRevisionLocked(sc); err != nil && !errors.Is(err, ErrSnapshotTooLarge) {
		s.lib.Scripts = s.lib.Scripts[:len(s.lib.Scripts)-1]
		return nil, err
	}
	if err := s.saveLocked(); err != nil {
		s.lib.Scripts = s.lib.Scripts[:len(s.lib.Scripts)-1]
		return nil, err
	}
	ok = true
	return sc, nil
}

// uniqueScriptNameLocked appends " (2)" until the name is free in a folder.
func (s *Store) uniqueScriptNameLocked(folderID, name string) string {
	if !s.scriptNameTaken(folderID, name, "") && !s.folderNameTaken(folderID, name, "") {
		return name
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)", name, i)
		if !s.scriptNameTaken(folderID, candidate, "") && !s.folderNameTaken(folderID, candidate, "") {
			return candidate
		}
	}
	return name + " 副本"
}

// ImportEntryChoices lists the files of a script that could be its entry.
func (s *Store) ImportEntryChoices(scriptID string) []string {
	var out []string
	for _, f := range s.ScriptFiles(scriptID) {
		out = append(out, f.Path)
	}
	return out
}

// ReplaceScriptFiles replaces every managed file of a script with a new set of
// files, keeping the script's identity. Used when an import overwrites.
func (s *Store) ReplaceScriptFiles(id, srcDir, entry string) error {
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
		return errors.New("脚本不存在")
	}
	// Copy into a staging directory first, so a failed copy leaves the script
	// as it was.
	var copied []ScriptFile
	staged, err := s.buildScriptDir(sc.ID, func(dir string) error {
		files, err := copyTree(context.Background(), srcDir, dir, nil)
		if err != nil {
			return err
		}
		copied = files
		if entry == "" || !containsFile(files, entry) {
			entry = pickImportEntry(dir, files)
		}
		if entry == "" {
			return errors.New("目录里没有可以运行的文件")
		}
		if _, err := resolveIn(dir, entry); err != nil {
			fallback := pickImportEntry(dir, files)
			if fallback == "" {
				return err
			}
			entry = fallback
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(copied) == 0 {
		_ = os.RemoveAll(staged)
		return errors.New("目录里没有可以运行的文件")
	}
	oldEntry := sc.Entry
	sc.Entry = entry
	sc.normalize()
	if err := s.swapScriptDir(sc.ID, staged); err != nil {
		sc.Entry = oldEntry
		sc.normalize()
		return err
	}
	sc.UpdatedAt = time.Now()
	if _, err := s.addRevisionLocked(sc); err != nil && !errors.Is(err, ErrSnapshotTooLarge) {
		return err
	}
	return s.saveLocked()
}

// containsFile reports whether a file list has a path.
func containsFile(files []ScriptFile, rel string) bool {
	for _, f := range files {
		if f.Path == rel {
			return true
		}
	}
	return false
}

// resolveIn resolves a relative path inside an existing directory.
func resolveIn(dir, rel string) (string, error) {
	rel, err := cleanRelPath(rel)
	if err != nil {
		return "", err
	}
	real, err := realPath(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	if !withinDir(realDir, real) {
		return "", errors.New("路径超出脚本目录")
	}
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s 不存在", rel)
	}
	return real, nil
}

// PreviewImport reports what an import would copy, without touching the
// library: file count, total bytes and any limit or symlink problem.
func PreviewImport(src string, isDir bool) (int, int64, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return 0, 0, err
	}
	if !isDir {
		if info.Mode()&os.ModeSymlink != 0 {
			return 0, 0, errors.New("不支持符号链接")
		}
		if !info.Mode().IsRegular() {
			return 0, 0, errors.New("只能导入常规文件")
		}
		if info.Size() > maxImportFile {
			return 0, 0, fmt.Errorf("文件超过 %d MB 的限制", maxImportFile>>20)
		}
		return 1, info.Size(), nil
	}
	if !info.IsDir() {
		return 0, 0, errors.New("请选择一个目录")
	}
	count := 0
	var total int64
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == src {
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
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("不支持符号链接：%s", d.Name())
		}
		fileInfo, err := d.Info()
		if err != nil {
			return err
		}
		if !fileInfo.Mode().IsRegular() {
			return fmt.Errorf("只复制常规文件：%s", d.Name())
		}
		if fileInfo.Size() > maxImportFile {
			return fmt.Errorf("%s 超过单文件 %d MB 的限制", d.Name(), maxImportFile>>20)
		}
		count++
		total += fileInfo.Size()
		if count > maxImportFiles {
			return fmt.Errorf("文件超过 %d 个的限制", maxImportFiles)
		}
		if total > maxImportTotal {
			return fmt.Errorf("总大小超过 %d MB 的限制", maxImportTotal>>20)
		}
		return nil
	})
	return count, total, err
}

// insidePath reports whether p is dir or lies under it, after resolving links.
func insidePath(p, dir string) bool {
	p, dir = realOrSelf(p), realOrSelf(dir)
	return withinDir(dir, p)
}

// realOrSelf resolves p, or returns it cleaned when it does not exist.
func realOrSelf(p string) string {
	if resolved, err := realpathOrSelf(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}
