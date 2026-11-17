package main

import (
	"path/filepath"
	"testing"
	"time"
)

func newBareRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	if _, err := gitRun(t.TempDir(), 20*time.Second, "init", "--bare", "-b", "main", dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSyncPushesAndPulls(t *testing.T) {
	if !GitAvailable() {
		t.Skip("git 不可用")
	}
	remote := newBareRemote(t)

	dirA := t.TempDir()
	storeA, err := OpenStore(dirA)
	if err != nil {
		t.Fatal(err)
	}
	it := testInterpreter(t, storeA)
	sc, err := storeA.CreateScript("", "部署", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeA.WriteScript(sc.ID, "echo one\n"); err != nil {
		t.Fatal(err)
	}
	syncA := NewSync(storeA)
	if err := syncA.SetRemote(remote); err != nil {
		t.Fatal(err)
	}
	st, err := syncA.SyncNow()
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if st.Health != healthHealthy || st.Ahead != 0 {
		t.Fatalf("state = %+v", st)
	}

	dirB := filepath.Join(t.TempDir(), "b")
	if _, err := gitRun(t.TempDir(), 30*time.Second, "clone", remote, dirB); err != nil {
		t.Fatal(err)
	}
	storeB, err := OpenStore(dirB)
	if err != nil {
		t.Fatal(err)
	}
	var scB Script
	for _, s := range storeB.Scripts() {
		if s.Name == "部署" {
			scB = s
		}
	}
	if scB.ID == "" {
		t.Fatal("clone did not bring the script")
	}
	body, err := storeB.ReadScript(scB)
	if err != nil || body != "echo one\n" {
		t.Fatalf("body = %q err=%v", body, err)
	}

	if _, err := storeA.WriteScript(sc.ID, "echo two\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := syncA.SyncNow(); err != nil {
		t.Fatal(err)
	}
	syncB := NewSync(storeB)
	st, err = syncB.SyncNow()
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if st.Behind != 0 {
		t.Fatalf("behind = %d", st.Behind)
	}
	body, err = storeB.ReadScript(scB)
	if err != nil || body != "echo two\n" {
		t.Fatalf("after pull body = %q err=%v", body, err)
	}
}

func TestSyncKeepsLocalOnConflictAndResolves(t *testing.T) {
	if !GitAvailable() {
		t.Skip("git 不可用")
	}
	remote := newBareRemote(t)
	dirA := t.TempDir()
	storeA, err := OpenStore(dirA)
	if err != nil {
		t.Fatal(err)
	}
	it := testInterpreter(t, storeA)
	scA, err := storeA.CreateScript("", "报表", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storeA.WriteScript(scA.ID, "echo base\n"); err != nil {
		t.Fatal(err)
	}
	syncA := NewSync(storeA)
	if err := syncA.SetRemote(remote); err != nil {
		t.Fatal(err)
	}
	if _, err := syncA.SyncNow(); err != nil {
		t.Fatal(err)
	}

	dirB := filepath.Join(t.TempDir(), "b")
	if _, err := gitRun(t.TempDir(), 30*time.Second, "clone", remote, dirB); err != nil {
		t.Fatal(err)
	}
	storeB, err := OpenStore(dirB)
	if err != nil {
		t.Fatal(err)
	}
	var scB Script
	for _, s := range storeB.Scripts() {
		if s.Name == "报表" {
			scB = s
		}
	}

	if _, err := storeA.WriteScript(scA.ID, "echo from-a\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := syncA.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if _, err := storeB.WriteScript(scB.ID, "echo from-b\n"); err != nil {
		t.Fatal(err)
	}
	syncB := NewSync(storeB)
	if _, err := syncB.SyncNow(); err != nil {
		t.Fatalf("conflicting sync: %v", err)
	}
	body, err := storeB.ReadScript(scB)
	if err != nil || body != "echo from-b\n" {
		t.Fatalf("local copy should stay, body = %q err=%v", body, err)
	}
	conflicts := storeB.Conflicts()
	if len(conflicts) != 1 || conflicts[0].ScriptID != scB.ID {
		t.Fatalf("conflicts = %+v", conflicts)
	}

	if err := syncB.ResolveConflict(conflicts[0], "remote"); err != nil {
		t.Fatal(err)
	}
	body, err = storeB.ReadScript(scB)
	if err != nil || body != "echo from-a\n" {
		t.Fatalf("after using remote body = %q err=%v", body, err)
	}
	if len(storeB.Conflicts()) != 0 {
		t.Fatal("conflict should be cleared")
	}
}

func TestSnapshotsListAndRestore(t *testing.T) {
	if !GitAvailable() {
		t.Skip("git 不可用")
	}
	remote := newBareRemote(t)
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	it := testInterpreter(t, store)
	sc, err := store.CreateScript("", "快照", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteScript(sc.ID, "echo first\n"); err != nil {
		t.Fatal(err)
	}
	sync := NewSync(store)
	if err := sync.SetRemote(remote); err != nil {
		t.Fatal(err)
	}
	if _, err := sync.SyncNow(); err != nil {
		t.Fatal(err)
	}
	first := sync.Snapshots()
	if len(first) != 1 {
		t.Fatalf("snapshots after first sync = %+v", first)
	}
	if _, err := store.WriteScript(sc.ID, "echo second\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := sync.SyncNow(); err != nil {
		t.Fatal(err)
	}
	snaps := sync.Snapshots()
	if len(snaps) < 2 {
		t.Fatalf("snapshots = %+v", snaps)
	}
	if err := sync.RestoreSnapshot(first[0].Tag); err != nil {
		t.Fatal(err)
	}
	body, err := store.ReadScript(sc)
	if err != nil {
		t.Fatal(err)
	}
	if body != "echo first\n" {
		t.Fatalf("restored body = %q", body)
	}
}
