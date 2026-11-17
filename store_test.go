package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return s
}

func testInterpreter(t *testing.T, s *Store) Interpreter {
	t.Helper()
	it, err := s.CreateInterpreter(Interpreter{
		Name:      "sh",
		Language:  "shell",
		Path:      "/bin/sh",
		Args:      []string{"{script}"},
		ProbeArgs: []string{"-c", "echo 1.0"},
	})
	if err != nil {
		t.Fatalf("CreateInterpreter: %v", err)
	}
	Probe(&it)
	if err := s.UpdateInterpreter(it); err != nil {
		t.Fatalf("UpdateInterpreter: %v", err)
	}
	return it
}

func TestStoreFoldersAndScripts(t *testing.T) {
	s := newTestStore(t)
	folder, err := s.CreateFolder("", "运维")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	sub, err := s.CreateFolder(folder.ID, "数据库")
	if err != nil {
		t.Fatalf("CreateFolder sub: %v", err)
	}
	it := testInterpreter(t, s)
	sc, err := s.CreateScript(sub.ID, "备份", "shell", "sh", it.ID)
	if err != nil {
		t.Fatalf("CreateScript: %v", err)
	}
	if _, err := s.WriteScript(sc.ID, "echo hello\n"); err != nil {
		t.Fatalf("WriteScript: %v", err)
	}
	content, err := s.ReadScript(sc)
	if err != nil {
		t.Fatalf("ReadScript: %v", err)
	}
	if content != "echo hello\n" {
		t.Fatalf("content = %q", content)
	}
	revs := s.RevisionsOf(sc.ID)
	if len(revs) < 2 {
		t.Fatalf("revisions = %d, want at least 2", len(revs))
	}
	if got := s.Usage(it.ID); got != 1 {
		t.Fatalf("Usage = %d, want 1", got)
	}
	if err := s.DeleteInterpreter(it.ID); err == nil {
		t.Fatal("deleting a used interpreter should fail")
	}
	if err := s.DeleteFolder(folder.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if len(s.Scripts()) != 0 {
		t.Fatalf("scripts left: %d", len(s.Scripts()))
	}
	if len(s.Folders()) != 0 {
		t.Fatalf("folders left: %d", len(s.Folders()))
	}
	trash, err := os.ReadDir(filepath.Join(s.Root(), ".trash"))
	if err != nil || len(trash) == 0 {
		t.Fatalf("trash entries = %d, err = %v", len(trash), err)
	}
}

func TestStoreRejectsDuplicateNames(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateFolder("", "同名"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFolder("", "同名"); err == nil {
		t.Fatal("duplicate folder name accepted")
	}
	if _, err := s.CreateFolder("", "a/b"); err == nil {
		t.Fatal("name with slash accepted")
	}
}

func TestRunnerSuccessAndFailure(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc, err := s.CreateScript("", "hello", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteScript(sc.ID, "echo hello world\n"); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(s)
	run, err := r.Start(sc, it, 5*time.Second)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := waitRun(t, s, run.ID)
	if done.Status != RunOK {
		t.Fatalf("status = %s", done.Status)
	}
	if done.ExitCode != 0 {
		t.Fatalf("exit = %d", done.ExitCode)
	}
	lines, ok := r.Lines(run.ID)
	if !ok {
		t.Fatal("no lines for the run")
	}
	joined := ""
	for _, line := range lines {
		joined += line.Text + "\n"
	}
	if !strings.Contains(joined, "hello world") {
		t.Fatalf("log = %q", joined)
	}
	data, err := os.ReadFile(filepath.Join(s.Root(), done.LogPath))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "hello world") {
		t.Fatalf("log file = %q", data)
	}

	fail, err := s.CreateScript("", "fail", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteScript(fail.ID, "echo boom >&2\nexit 7\n"); err != nil {
		t.Fatal(err)
	}
	run2, err := r.Start(fail, it, 5*time.Second)
	if err != nil {
		t.Fatalf("Start fail: %v", err)
	}
	done2 := waitRun(t, s, run2.ID)
	if done2.Status != RunFailed || done2.ExitCode != 7 {
		t.Fatalf("status = %s, exit = %d", done2.Status, done2.ExitCode)
	}
}

func TestRunnerStop(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc, err := s.CreateScript("", "sleep", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteScript(sc.ID, "sleep 30\n"); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(s)
	run, err := r.Start(sc, it, 30*time.Second)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if err := r.Stop(run.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	done := waitRun(t, s, run.ID)
	if done.Status != RunCancelled {
		t.Fatalf("status = %s", done.Status)
	}
	if r.Busy() {
		t.Fatal("runner still busy")
	}
}

func TestProbeMissingPath(t *testing.T) {
	it := Interpreter{Path: "/definitely/not/here"}
	Probe(&it)
	if it.State != StateMissing {
		t.Fatalf("state = %s", it.State)
	}
}

func waitRun(t *testing.T, s *Store, id string) Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, ok := s.Run(id)
		if ok && run.Status != RunRunning {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish", id)
	return Run{}
}

func TestClearRevisionsKeepsNewest(t *testing.T) {
	store := newTestStore(t)
	it := testInterpreter(t, store)
	sc, err := store.CreateScript("", "清理", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"echo one\n", "echo two\n", "echo three\n"} {
		if _, err := store.WriteScript(sc.ID, body); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AddRevision(sc.ID); err != nil {
			t.Fatal(err)
		}
	}
	revs := store.RevisionsOf(sc.ID)
	if len(revs) != 4 {
		t.Fatalf("revisions = %d", len(revs))
	}
	removed, err := store.ClearRevisions(sc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 {
		t.Fatalf("removed = %d", removed)
	}
	left := store.RevisionsOf(sc.ID)
	if len(left) != 1 || left[0].ID != revs[0].ID {
		t.Fatalf("kept %+v, want newest %s", left, revs[0].ID)
	}
}

func TestStoreMigratesLegacyIndex(t *testing.T) {
	dir := t.TempDir()
	legacy := Library{
		Version:      1,
		Folders:      []*Folder{{ID: "f-old", Name: "旧目录", Position: 0}},
		Scripts:      []*Script{{ID: "s-old", FolderID: "f-old", Name: "旧脚本", Ext: "sh", Language: "shell", InterpID: "i-old", Position: 0}},
		Interpreters: []*Interpreter{{ID: "i-old", Name: "sh", Language: "shell", Path: "/bin/sh", Args: []string{"{script}"}, State: StateOK}},
	}
	data, err := json.MarshalIndent(&legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts", "s-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scripts", "s-old", "main.sh"), []byte("echo old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "library.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "folders.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "local.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "library.json.migrated")); err != nil {
		t.Fatal(err)
	}
	sc, ok := store.Script("s-old")
	if !ok || sc.InterpID != "i-old" || sc.Name != "旧脚本" {
		t.Fatalf("script = %+v ok=%v", sc, ok)
	}
	body, err := store.ReadScript(sc)
	if err != nil || body != "echo old\n" {
		t.Fatalf("body = %q err=%v", body, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "scripts", "s-old", "script.json")); err != nil {
		t.Fatal(err)
	}
}
