package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func newMultiFileScript(t *testing.T, s *Store, it Interpreter) Script {
	t.Helper()
	sc, err := s.CreateScript("", "初始化", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddScriptFile(sc.ID, "lib/common.sh", "greet() { echo \"$1\"; }\n"); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\n. ./lib/common.sh\ngreet \"entry-ok\"\n"
	if _, err := s.WriteScript(sc.ID, body); err != nil {
		t.Fatal(err)
	}
	sc, _ = s.Script(sc.ID)
	return sc
}

func TestStoreReadsAndWritesDirectories(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	if sc.Entry != "初始化.sh" {
		t.Fatalf("entry = %q", sc.Entry)
	}
	if !s.IsMultiFile(sc) {
		t.Fatalf("script with %d files should be multi-file", len(s.ScriptFiles(sc.ID)))
	}
	files := s.ScriptFiles(sc.ID)
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	seen := map[string]bool{}
	for _, f := range files {
		seen[f.Path] = true
	}
	if !seen[sc.Entry] || !seen["lib/common.sh"] {
		t.Fatalf("files = %+v", files)
	}
	body, err := s.ReadScriptFile(sc, "lib/common.sh")
	if err != nil || !strings.Contains(body, "greet") {
		t.Fatalf("lib body = %q err=%v", body, err)
	}
	if _, err := s.WriteScriptFile(sc.ID, "lib/common.sh", "greet() { echo \"two\"; }\n", true); err != nil {
		t.Fatal(err)
	}
	revs := s.RevisionsOf(sc.ID)
	if len(revs) == 0 || revs[0].Files != 2 || revs[0].Entry != sc.Entry {
		t.Fatalf("newest revision = %+v", revs)
	}
	if got, err := s.RevisionFileContent(revs[0].ID, "lib/common.sh"); err != nil || !strings.Contains(got, "two") {
		t.Fatalf("revision lib = %q err=%v", got, err)
	}
}

func TestStoreRejectsPathsThatLeaveTheScript(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	bad := []string{"../evil.sh", "/etc/hosts", "lib/../../evil.sh", `lib\..\..\evil.sh`, "CON.sh", ""}
	for _, rel := range bad {
		if _, err := s.ReadScriptFile(sc, rel); err == nil {
			t.Fatalf("reading %q should fail", rel)
		}
		if err := s.SetEntry(sc.ID, rel); err == nil {
			t.Fatalf("entry %q should be refused", rel)
		}
		if err := s.AddScriptFile(sc.ID, rel, "x"); err == nil {
			t.Fatalf("adding %q should be refused", rel)
		}
	}
	if err := s.SetEntry(sc.ID, "lib/common.sh"); err != nil {
		t.Fatalf("setting a real file as entry: %v", err)
	}
	fresh, _ := s.Script(sc.ID)
	if fresh.Entry != "lib/common.sh" {
		t.Fatalf("entry = %q", fresh.Entry)
	}
}

func TestStoreRefusesSymlinkOutsideScriptDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上没有可用的符号链接测试")
	}
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	outside := filepath.Join(t.TempDir(), "secret.sh")
	if err := os.WriteFile(outside, []byte("echo secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.ScriptDir(sc), "link.sh")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if err := s.SetEntry(sc.ID, "link.sh"); err == nil {
		t.Fatal("a symlink leaving the script directory must be refused")
	}
}

func TestPrefixCheckComparesWholeElements(t *testing.T) {
	dir := filepath.Join(string(filepath.Separator), "lib", "scripts", "sc-1")
	if withinDir(dir, filepath.Join(dir, "a.sh")) != true {
		t.Fatal("a file inside the directory should be inside")
	}
	if withinDir(dir, filepath.Join(string(filepath.Separator), "lib", "scripts", "sc-10", "a.sh")) {
		t.Fatal("sc-10 must not count as inside sc-1")
	}
}

func TestRenameEntryKeepsScriptRunnable(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	if err := s.RenameScriptFile(sc.ID, "lib/common.sh", "lib/shared.sh"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEntry(sc.ID, "lib/shared.sh"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameScriptFile(sc.ID, "lib/shared.sh", "lib/run-lib.sh"); err != nil {
		t.Fatal(err)
	}
	fresh, _ := s.Script(sc.ID)
	if fresh.Entry != "lib/run-lib.sh" {
		t.Fatalf("entry = %q after renaming the entry file", fresh.Entry)
	}
	if _, err := s.EntryPath(fresh); err != nil {
		t.Fatalf("renamed entry does not resolve: %v", err)
	}
	if err := s.DeleteScriptFile(sc.ID, "lib/run-lib.sh"); err == nil {
		t.Fatal("deleting the entry should be refused")
	}
	if err := s.RenameScript(sc.ID, "新名字", true); err != nil {
		t.Fatal(err)
	}
	fresh, _ = s.Script(sc.ID)
	if fresh.Name != "新名字" {
		t.Fatalf("name = %q", fresh.Name)
	}
}

func TestSnapshotRestoresWholeDirectoryWithModes(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	entryAbs := filepath.Join(s.ScriptDir(sc), sc.Entry)
	if err := os.Chmod(entryAbs, 0o755); err != nil {
		t.Fatal(err)
	}
	rev, err := s.AddRevision(sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Files != 2 || rev.Entry != sc.Entry {
		t.Fatalf("revision = %+v", rev)
	}
	if err := os.RemoveAll(filepath.Join(s.ScriptDir(sc), "lib")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteScript(sc.ID, "echo destroyed\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreRevision(sc.ID, rev.ID); err != nil {
		t.Fatal(err)
	}
	body, err := s.ReadScriptFile(sc, "lib/common.sh")
	if err != nil || !strings.Contains(body, "greet") {
		t.Fatalf("restored lib = %q err=%v", body, err)
	}
	info, err := os.Stat(entryAbs)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("restored mode = %v, executable bit lost", info.Mode().Perm())
	}
	if got, _ := s.ReadScript(sc); !strings.Contains(got, "entry-ok") {
		t.Fatalf("entry content = %q", got)
	}
}

func TestOversizedDirectorySkipsHistoryButStillSaves(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	if err := s.AddScriptFile(sc.ID, "big.bin", ""); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(s.ScriptDir(sc), "big.bin")
	if err := os.Truncate(big, maxSnapshotBytes+1024); err != nil {
		t.Fatal(err)
	}
	before := len(s.RevisionsOf(sc.ID))
	if _, err := s.WriteScript(sc.ID, "echo still-saved\n"); err != nil {
		t.Fatalf("saving with an oversized directory must not fail: %v", err)
	}
	if _, err := s.AddRevision(sc.ID); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("AddRevision error = %v, want ErrSnapshotTooLarge", err)
	}
	if got := len(s.RevisionsOf(sc.ID)); got != before {
		t.Fatalf("revisions %d -> %d; an oversized directory must not add one", before, got)
	}
	if body, err := s.ReadScript(sc); err != nil || !strings.Contains(body, "still-saved") {
		t.Fatalf("entry body = %q err=%v", body, err)
	}
}

func TestImportDirKeepsStructureAndIgnoresJunk(t *testing.T) {
	src := t.TempDir()
	files := map[string]string{
		"init.sh":              "#!/bin/sh\n. ./lib/common.sh\ngreet\n",
		"lib/common.sh":        "greet() { echo imported-ok; }\n",
		"answers.example.conf": "key=value\n",
		"README.md":            "# readme\n",
		".DS_Store":            "junk",
		".git/config":          "junk",
		"node_modules/a.js":    "junk",
	}
	for rel, body := range files {
		full := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(src, "init.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc, err := s.ImportDir(src, "", "导入项目", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Entry != "init.sh" {
		t.Fatalf("entry = %q, want init.sh", sc.Entry)
	}
	var paths []string
	for _, f := range s.ScriptFiles(sc.ID) {
		paths = append(paths, f.Path)
	}
	want := []string{"README.md", "answers.example.conf", "init.sh", "lib/common.sh"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("files = %v, want %v", paths, want)
	}
	info, err := os.Stat(filepath.Join(s.ScriptDir(sc), "init.sh"))
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("import lost the executable bit: %v %v", info.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(s.ScriptDir(sc), "node_modules")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("node_modules was copied")
	}
	r := NewRunner(s)
	run, err := r.Start(sc, it, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, s, run.ID)
	if done.Status != RunOK {
		t.Fatalf("imported run = %+v\n%s", done, r.LogText(done))
	}
	if !strings.Contains(r.LogText(done), "imported-ok") {
		t.Fatalf("log = %q", r.LogText(done))
	}
}

func TestImportRefusesSymlinksAndOversizedFiles(t *testing.T) {
	if runtime.GOOS != "windows" {
		src := t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "a.sh"), []byte("echo a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/etc/hosts", filepath.Join(src, "link")); err != nil {
			t.Skipf("无法创建符号链接: %v", err)
		}
		s := newTestStore(t)
		if _, err := s.ImportDir(src, "", "带链接", ""); err == nil || !strings.Contains(err.Error(), "符号链接") {
			t.Fatalf("import error = %v, want a symlink refusal", err)
		}
	}
	src := t.TempDir()
	big := filepath.Join(src, "big.sh")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxImportFile + 1024); err != nil {
		t.Fatal(err)
	}
	f.Close()
	s := newTestStore(t)
	if _, err := s.ImportDir(src, "", "太大", ""); err == nil || !strings.Contains(err.Error(), "MB") {
		t.Fatalf("import error = %v, want a size refusal", err)
	}
}

func TestUpgradeFromVersionOneNamesTheEntryAfterTheScript(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts", "s-old")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "main.sh"), []byte("echo old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"id": "s-old", "folder_id": "", "name": "旧脚本", "language": "shell", "position": 0}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(scriptDir, "script.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "folders.json"), []byte(`{"version":1,"folders":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "local.json"), []byte(`{"version":1,"interpreters":[],"bindings":{},"revisions":[],"runs":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sc, ok := s.Script("s-old")
	if !ok {
		t.Fatal("script missing after upgrade")
	}
	if sc.Entry != "旧脚本.sh" {
		t.Fatalf("entry = %q, want 旧脚本.sh", sc.Entry)
	}
	if body, err := s.ReadScript(sc); err != nil || body != "echo old\n" {
		t.Fatalf("body = %q err=%v", body, err)
	}
	if _, err := os.Stat(filepath.Join(scriptDir, "main.sh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("main.sh should have been renamed")
	}
	var folders struct {
		Version int `json:"version"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "folders.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &folders); err != nil {
		t.Fatal(err)
	}
	if folders.Version != storeVersion {
		t.Fatalf("folders.json version = %d, want %d", folders.Version, storeVersion)
	}
}

func TestUpgradeKeepsTheOldNameWhenItWouldCollide(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts", "s-old")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.sh", "旧脚本.sh"} {
		if err := os.WriteFile(filepath.Join(scriptDir, name), []byte("echo "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	meta := map[string]any{"id": "s-old", "name": "旧脚本", "language": "shell", "entry": ""}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(scriptDir, "script.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "folders.json"), []byte(`{"version":1,"folders":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sc, ok := s.Script("s-old")
	if !ok {
		t.Fatal("script missing")
	}
	if sc.Entry != "main.sh" {
		t.Fatalf("entry = %q; a taken name must keep the old file name", sc.Entry)
	}
	if _, err := os.Stat(filepath.Join(scriptDir, "旧脚本.sh")); err != nil {
		t.Fatalf("the other file was overwritten: %v", err)
	}
}

func TestRunnerUsesEntryDirectoryAsCwd(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	r := NewRunner(s)
	run, err := r.Start(sc, it, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, s, run.ID)
	if done.Status != RunOK {
		t.Fatalf("run = %+v\n%s", done, r.LogText(done))
	}
	if done.Entry != sc.Entry {
		t.Fatalf("run entry = %q", done.Entry)
	}
	if !strings.Contains(r.LogText(done), "入口 "+sc.Entry) {
		t.Fatalf("log header lacks the entry: %q", r.LogText(done))
	}
	if !strings.Contains(r.LogText(done), "entry-ok") {
		t.Fatalf("log = %q; source ./lib/common.sh did not work", r.LogText(done))
	}
}

func TestRunnerRefusesMissingEntryAndWrongInterpreter(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	broken := sc
	broken.Entry = "missing.sh"
	broken.normalize()
	r := NewRunner(s)
	if _, err := r.Start(broken, it, time.Second); err == nil || !strings.Contains(err.Error(), "入口不可用") {
		t.Fatalf("missing entry error = %v", err)
	}
	python := sc
	python.Entry = "tool.py"
	python.normalize()
	if err := s.AddScriptFile(sc.ID, "tool.py", "print('hi')\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start(python, it, time.Second); err == nil || !strings.Contains(err.Error(), "请更换解释器") {
		t.Fatalf("language mismatch error = %v", err)
	}
}

func TestAdminWrapperQuotesEveryArgument(t *testing.T) {
	wrapper := unixAdminWrapper("/bin/sh", []string{"a b", "x'y", "$(rm -rf /)", "`id`"}, "/tmp/work dir",
		"/tmp/log file.log", "/tmp/status", "/usr/bin:/bin", "/home/me")
	for _, want := range []string{`'/bin/sh'`, `'a b'`, `'x'\''y'`, `'$(rm -rf /)'`, "'`id`'",
		`cd '/tmp/work dir'`, `export PATH='/usr/bin:/bin'`, `export HOME='/home/me'`, `>> '/tmp/log file.log'`} {
		if !strings.Contains(wrapper, want) {
			t.Fatalf("wrapper lacks %q:\n%s", want, wrapper)
		}
	}
	if strings.Count(wrapper, "cd ") != 1 {
		t.Fatalf("wrapper should cd once:\n%s", wrapper)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	launch, err := newAdminLaunch(Interpreter{Path: "/bin/sh"}, []string{"{script}"}, "/tmp", "/tmp/l.log", "/tmp/s")
	if err != nil {
		t.Fatal(err)
	}
	defer launch.cleanup()
	if launch.Program == "" || len(launch.Args) == 0 {
		t.Fatalf("launch = %+v", launch)
	}
	if _, err := os.Stat(launch.Wrapper); err != nil {
		t.Fatalf("wrapper file missing: %v", err)
	}
}

func TestReconcileSettlesRunsLeftBehind(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	done := Run{ID: "run-done", ScriptID: sc.ID, Entry: sc.Entry, Status: RunRunning, StartedAt: time.Now()}
	unknown := Run{ID: "run-unknown", ScriptID: sc.ID, Entry: sc.Entry, Status: RunRunning, StartedAt: time.Now()}
	if err := s.CreateRun(done); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRun(unknown); err != nil {
		t.Fatal(err)
	}
	statusPath := s.adminStatusPath(sc.ID, done.ID)
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte("3"), 0o644); err != nil {
		t.Fatal(err)
	}
	NewRunner(s).Reconcile()
	got, _ := s.Run(done.ID)
	if got.Status != RunFailed || got.ExitCode != 3 {
		t.Fatalf("status file did not settle the run: %+v", got)
	}
	got, _ = s.Run(unknown.ID)
	if got.Status != RunUnknown {
		t.Fatalf("run without a status file = %+v, want unknown", got)
	}
}

func TestFileEditableStopsAtSizeAndNul(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	if err := s.AddScriptFile(sc.ID, "notes.txt", "hello\n"); err != nil {
		t.Fatal(err)
	}
	if !s.FileEditable(sc, "notes.txt") {
		t.Fatal("a small text file should be editable")
	}
	if err := s.AddScriptFile(sc.ID, "blob.bin", "a\x00b"); err != nil {
		t.Fatal(err)
	}
	if s.FileEditable(sc, "blob.bin") {
		t.Fatal("a file with a NUL byte should not be editable")
	}
	if err := s.AddScriptFile(sc.ID, "big.txt", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(s.ScriptDir(sc), "big.txt"), maxEditableBytes+1); err != nil {
		t.Fatal(err)
	}
	if s.FileEditable(sc, "big.txt") {
		t.Fatal("a file over 2MB should not be editable")
	}
}

func TestSanitizeFileNameKeepsNamesReadableAndSafe(t *testing.T) {
	cases := map[string]string{
		"备份 网站":    "备份-网站.sh",
		"a/b:c*?":  "a-b-c.sh",
		"CON":      "_CON.sh",
		"con.sh":   "_con.sh",
		"   ":      "script.sh",
		"hello.py": "hello.py",
	}
	for in, want := range cases {
		ext := "sh"
		if strings.HasSuffix(in, ".py") {
			ext = "py"
		}
		if got := sanitizeFileName(in, ext); got != want {
			t.Fatalf("sanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := uniqueFileName("init.sh", []string{"INIT.sh"}); got != "init-2.sh" {
		t.Fatalf("case-insensitive dedupe = %q", got)
	}
}

func TestCommandArgsUsesEntryAndDirectory(t *testing.T) {
	it := Interpreter{Path: "/bin/bash", Args: []string{"{dir}/run.sh", "--flag", "{script}"}}
	got := commandArgs(it, "/lib/a.sh", "/lib", []string{"--yes"})
	want := []string{"/lib/run.sh", "--flag", "/lib/a.sh", "--yes"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("args = %v, want %v", got, want)
	}
	noScript := Interpreter{Path: "/bin/bash", Args: []string{"-e"}}
	got = commandArgs(noScript, "/lib/a.sh", "/lib", nil)
	if len(got) != 2 || got[0] != "-e" || got[1] != "/lib/a.sh" {
		t.Fatalf("args without {script} = %v", got)
	}
}

func TestBundleCarriesWholeDirectories(t *testing.T) {
	src := newTestStore(t)
	it := testInterpreter(t, src)
	sc := newMultiFileScript(t, src, it)
	target := filepath.Join(t.TempDir(), "dir.zip")
	if err := ExportLibrary(src, target, ExportOptions{ScriptIDs: []string{sc.ID}}); err != nil {
		t.Fatal(err)
	}
	manifest, err := InspectBundle(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Scripts) != 1 || len(manifest.Scripts[0].Files) != 2 {
		t.Fatalf("manifest = %+v", manifest.Scripts)
	}
	if manifest.Scripts[0].Entry != sc.Entry {
		t.Fatalf("manifest entry = %q", manifest.Scripts[0].Entry)
	}
	dst := newTestStore(t)
	dstIt := testInterpreter(t, dst)
	if _, err := ImportLibrary(dst, target, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	var imported Script
	for _, s := range dst.Scripts() {
		if s.Name == sc.Name {
			imported = s
		}
	}
	if imported.ID == "" {
		t.Fatal("imported script missing")
	}
	if imported.Entry != sc.Entry {
		t.Fatalf("entry = %q, want %q", imported.Entry, sc.Entry)
	}
	if imported.InterpID != dstIt.ID {
		// The manifest names the source interpreter; the destination has one
		// shell interpreter, so the fallback must bind it.
		t.Fatalf("interpreter = %q, want %q", imported.InterpID, dstIt.ID)
	}
	body, err := dst.ReadScriptFile(imported, "lib/common.sh")
	if err != nil || !strings.Contains(body, "greet") {
		t.Fatalf("lib body = %q err=%v", body, err)
	}
}

func TestFollowLogReadsGrowingFileWithoutBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	if err := os.WriteFile(path, []byte("header\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	lg := &runLog{}
	r := NewRunner(newTestStore(t))
	done := make(chan struct{})
	go r.followLog(path, info.Size(), lg, done)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("one\ntwo\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(350 * time.Millisecond)
	if _, err := f.WriteString("three\npartial"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	f.Close()
	close(done)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lines := lg.snapshot()
		if len(lines) >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var texts []string
	for _, line := range lg.snapshot() {
		texts = append(texts, line.Text)
	}
	want := []string{"one", "two", "three", "partial"}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %v, want %v", texts, want)
	}
}

func TestAdminCleanupRemovesWrapper(t *testing.T) {
	launch, err := newAdminLaunch(Interpreter{Path: "/bin/sh"}, []string{"{script}"}, "/tmp", "/tmp/l.log", "/tmp/s")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := launch.Wrapper
	launch.cleanup()
	if _, err := os.Stat(wrapper); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrapper still there: %v", err)
	}
}

func TestNestedEntryRunsFromItsOwnDirectory(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc, err := s.CreateScript("", "嵌套入口", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddScriptFile(sc.ID, "src/tool.sh", "echo nested-ok\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddScriptFile(sc.ID, "src/main.sh", "#!/bin/sh\n. ./tool.sh\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEntry(sc.ID, "src/main.sh"); err != nil {
		t.Fatal(err)
	}
	sc, _ = s.Script(sc.ID)
	if sc.Entry != "src/main.sh" || sc.Ext != "sh" || sc.Language != "shell" {
		t.Fatalf("script after setting a nested entry = %+v", sc)
	}
	r := NewRunner(s)
	run, err := r.Start(sc, it, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, s, run.ID)
	if done.Status != RunOK || !strings.Contains(r.LogText(done), "nested-ok") {
		t.Fatalf("run = %+v log = %q", done, r.LogText(done))
	}
	wantCwd, err := filepath.EvalSymlinks(filepath.Join(s.ScriptDir(sc), "src"))
	if err != nil {
		t.Fatal(err)
	}
	if done.Cwd != wantCwd {
		t.Fatalf("cwd = %q, want %q", done.Cwd, wantCwd)
	}
}

func TestSymlinkedSubdirectoryCannotEscapeTheScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上没有可用的符号链接测试")
	}
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.sh"), []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// lib is a real directory; escape is a link inside it pointing out.
	link := filepath.Join(s.ScriptDir(sc), "lib", "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if _, err := s.ReadScriptFile(sc, "lib/escape/secret.sh"); err == nil {
		t.Fatal("reading through a linked directory must be refused")
	}
	if _, err := s.WriteScriptFile(sc.ID, "lib/escape/new.sh", "pwn\n", false); err == nil {
		t.Fatal("writing through a linked directory must be refused")
	}
	if err := s.AddScriptFile(sc.ID, "lib/escape/added.sh", "pwn\n"); err == nil {
		t.Fatal("adding a file through a linked directory must be refused")
	}
	if err := s.SetEntry(sc.ID, "lib/escape/secret.sh"); err == nil {
		t.Fatal("setting an entry through a linked directory must be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.sh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file was created outside the script directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "added.sh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file was created outside the script directory")
	}
}

func TestScriptIDFromDiskCannotEscapeTheLibrary(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts", "s-safe")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "run.sh"), []byte("echo safe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The file claims an id that would resolve outside scripts/.
	meta := map[string]any{"id": "../../evil", "name": "伪造", "entry": "run.sh", "language": "shell"}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(scriptDir, "script.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "folders.json"), []byte(`{"version":2,"folders":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sc, ok := s.Script("s-safe")
	if !ok {
		t.Fatalf("script missing; ids = %+v", s.Scripts())
	}
	if sc.ID != "s-safe" {
		t.Fatalf("id = %q, want the directory name", sc.ID)
	}
	if !strings.HasPrefix(s.ScriptDir(sc), filepath.Join(dir, "scripts")) {
		t.Fatalf("script dir escaped: %s", s.ScriptDir(sc))
	}
	if body, err := s.ReadScript(sc); err != nil || body != "echo safe\n" {
		t.Fatalf("body = %q err=%v", body, err)
	}
}

func TestScriptWithMissingFolderGoesToTheRoot(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts", "s-orphan")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "run.sh"), []byte("echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"id": "s-orphan", "folder_id": "f-gone", "name": "孤儿", "entry": "run.sh"}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(scriptDir, "script.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "folders.json"), []byte(`{"version":2,"folders":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sc, ok := s.Script("s-orphan")
	if !ok {
		t.Fatal("script missing")
	}
	if sc.FolderID != "" {
		t.Fatalf("folder = %q, want the root", sc.FolderID)
	}
}

func TestRestoreKeepsTheOldContentWhenTheSnapshotIsBroken(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	rev, err := s.AddRevision(sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(s.root, rev.Path, "lib")); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadScript(sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreRevision(sc.ID, rev.ID); err == nil {
		t.Fatal("a snapshot with a missing file should refuse to restore")
	}
	after, err := s.ReadScript(sc)
	if err != nil || after != before {
		t.Fatalf("entry changed after a failed restore: %q -> %q err=%v", before, after, err)
	}
	if _, err := s.ReadScriptFile(sc, "lib/common.sh"); err != nil {
		t.Fatalf("the other file was lost: %v", err)
	}
}

func TestOverwriteImportKeepsTheOldContentWhenTheCopyFails(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	before, _ := s.ReadScript(sc)
	missing := filepath.Join(t.TempDir(), "not-there")
	if err := s.ReplaceScriptFiles(sc.ID, missing, ""); err == nil {
		t.Fatal("replacing from a missing directory should fail")
	}
	after, err := s.ReadScript(sc)
	if err != nil || after != before {
		t.Fatalf("entry changed after a failed replace: %q -> %q", before, after)
	}
	if _, err := s.ReadScriptFile(sc, "lib/common.sh"); err != nil {
		t.Fatalf("the other file was lost: %v", err)
	}
}

func TestLoadReportsSkippedScriptsInsteadOfHidingThem(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts", "s-broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "s-broken", "script.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(dir, "scripts", "s-good")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "run.sh"), []byte("echo ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(map[string]any{"id": "s-good", "name": "正常", "entry": "run.sh"})
	if err := os.WriteFile(filepath.Join(good, "script.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Script("s-good"); !ok {
		t.Fatal("the healthy script should still load")
	}
	problems := s.Problems()
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "s-broken") {
		t.Fatalf("the broken script was skipped silently: %v", problems)
	}
}

func TestLoadIgnoresPathsOutsideTheLibrary(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "folders.json"), []byte(`{"version":2,"folders":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	local := `{"version":2,"interpreters":[],"bindings":{},"revisions":[
		{"id":"r-bad","script_id":"s-1","hash":"x","path":"../../../etc/passwd"},
		{"id":"r-ok","script_id":"s-1","hash":"y","path":"revisions/s-1/y"}
	],"runs":[{"id":"run-bad","script_id":"s-1","log_path":"../../etc/hosts","status":"ok"}]}`
	if err := os.WriteFile(filepath.Join(dir, "local.json"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "revisions", "s-1", "y"), 0o755)
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rev := range s.lib.Revisions {
		if rev.ID == "r-bad" {
			t.Fatal("a revision pointing outside the library was kept")
		}
	}
	if len(s.Runs()) != 0 {
		t.Fatalf("a run with an outside log path was kept: %+v", s.Runs())
	}
	if len(s.Problems()) == 0 {
		t.Fatal("the rejected entries should be reported")
	}
}

func TestPruneRemovesOrphanRevisionDirsAndRecords(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	rev, err := s.AddRevision(sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A directory nobody references, and a record whose content is gone.
	orphan := filepath.Join(s.root, "revisions", sc.ID, "orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	missing := &Revision{ID: "r-missing", ScriptID: sc.ID, Hash: "gone", Path: filepath.Join("revisions", sc.ID, "gone")}
	s.mu.Lock()
	s.lib.Revisions = append(s.lib.Revisions, missing)
	s.mu.Unlock()
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an orphan snapshot directory was left behind")
	}
	for _, r := range s.RevisionsOf(sc.ID) {
		if r.ID == "r-missing" {
			t.Fatal("a revision whose content is gone was kept")
		}
	}
	if _, err := os.Stat(filepath.Join(s.root, rev.Path)); err != nil {
		t.Fatalf("a real snapshot was removed: %v", err)
	}
}

func TestInterruptedSwapLeftoversAreCleanedUp(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".stage-s-1-abc", ".old-s-1-xyz"} {
		if err := os.MkdirAll(filepath.Join(dir, "scripts", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := OpenStore(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".stage-") || strings.HasPrefix(e.Name(), ".old-") {
			t.Fatalf("leftover %s was not removed", e.Name())
		}
	}
}

func TestSymlinksAreListedButNeverSnapshotted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上没有可用的符号链接测试")
	}
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc := newMultiFileScript(t, s, it)
	target := filepath.Join(t.TempDir(), "outside.sh")
	if err := os.WriteFile(target, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(s.ScriptDir(sc), "link.sh")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	links := s.ScriptSymlinks(sc.ID)
	if len(links) != 1 || links[0] != "link.sh" {
		t.Fatalf("symlinks = %v", links)
	}
	rev, err := s.AddRevision(sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range s.RevisionFiles(rev.ID) {
		if f.Path == "link.sh" {
			t.Fatal("a symlink reached the snapshot")
		}
	}
}
