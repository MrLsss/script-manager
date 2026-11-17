package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func syncedApp(t *testing.T, commits int) (*App, *Store, Script) {
	t.Helper()
	a, store, sc := newTestApp(t)
	remote := t.TempDir()
	if _, err := gitRun(remote, 5*time.Second, "init", "--bare", "-b", "main"); err != nil {
		t.Fatalf("init bare: %v", err)
	}
	if err := a.sync.InitRepo(); err != nil {
		t.Fatalf("init repo: %v", err)
	}
	if err := a.sync.SetRemote(remote); err != nil {
		t.Fatalf("set remote: %v", err)
	}
	for i := 0; i < commits; i++ {
		body := "echo version-" + string(rune('a'+i)) + "\n"
		if _, err := store.WriteScript(sc.ID, body); err != nil {
			t.Fatal(err)
		}
		if _, err := a.sync.SyncNow(); err != nil {
			t.Fatalf("sync %d: %v", i, err)
		}
	}
	return a, store, sc
}

func TestPruneSnapshotsKeepsNewest(t *testing.T) {
	a, _, _ := syncedApp(t, 3)
	all := a.sync.Snapshots()
	if len(all) != 3 {
		t.Fatalf("snapshots = %d, want 3", len(all))
	}
	removed, err := a.sync.PruneSnapshots(2)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d", removed)
	}
	left := a.sync.Snapshots()
	if len(left) != 2 {
		t.Fatalf("snapshots after prune = %d", len(left))
	}
	if left[0].Tag != all[0].Tag || left[1].Tag != all[1].Tag {
		t.Fatalf("kept %v, want %v", left, all[:2])
	}
	for _, snap := range left {
		if snap.Tag == all[2].Tag {
			t.Fatalf("oldest snapshot survived")
		}
	}
}

func TestPruneSnapshotsUnlimited(t *testing.T) {
	a, _, _ := syncedApp(t, 3)
	removed, err := a.sync.PruneSnapshots(-1)
	if err != nil || removed != 0 {
		t.Fatalf("prune unlimited: removed=%d err=%v", removed, err)
	}
	if len(a.sync.Snapshots()) != 3 {
		t.Fatalf("snapshots changed while unlimited")
	}
}

func TestDeleteSnapshotRemovesTag(t *testing.T) {
	a, _, _ := syncedApp(t, 2)
	all := a.sync.Snapshots()
	if len(all) != 2 {
		t.Fatalf("snapshots = %d", len(all))
	}
	if err := a.sync.DeleteSnapshot(all[0].Tag); err != nil {
		t.Fatalf("delete: %v", err)
	}
	left := a.sync.Snapshots()
	if len(left) != 1 || left[0].Tag != all[1].Tag {
		t.Fatalf("left = %v", left)
	}
	if err := a.sync.DeleteSnapshot("v1.0"); err == nil {
		t.Fatalf("non snapshot tag accepted")
	}
}

func TestSnapshotUsageReportsSizes(t *testing.T) {
	a, _, _ := syncedApp(t, 2)
	objects, library := a.sync.SnapshotUsage()
	if objects <= 0 {
		t.Fatalf("object size = %d", objects)
	}
	if library <= 0 {
		t.Fatalf("library size = %d", library)
	}
}

func TestSyncPageShowsSnapshotUsageAndDelete(t *testing.T) {
	a, _, _ := syncedApp(t, 1)
	a.changeSyncData(func(d *syncPanelState) {
		d.state = a.sync.Status()
		d.loaded = true
	})
	a.loadSnapshots()
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("同步"); err != nil {
		t.Fatalf("click 同步: %v", err)
	}
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(texts, "共 1 份") || !strings.Contains(texts, "占用") {
		t.Fatalf("usage line missing:\n%s", texts)
	}
	if !strings.Contains(texts, "仓库总大小") {
		t.Fatalf("library size missing:\n%s", texts)
	}
	tag := a.syncData().snapshots[0].Tag
	a.deleteSnapshot(tag)
	tt.Frame()
	if !tt.HasText("删除这个快照") {
		t.Fatalf("confirm missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatalf("click 删除: %v", err)
	}
	tt.Frame()
	if len(a.syncData().snapshots) != 0 {
		t.Fatalf("snapshots = %v", a.syncData().snapshots)
	}
	if !tt.HasText("还没有快照。") {
		t.Fatalf("empty state missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestSettingsShowsSnapshotKeep(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(texts, "快照保留") || !strings.Contains(texts, "最近 30 份") {
		t.Fatalf("snapshot keep row missing:\n%s", texts)
	}
}

func TestSnapshotKeepLabelMapping(t *testing.T) {
	cases := map[int]string{10: "最近 10 份", 30: "最近 30 份", 90: "最近 90 份", -1: "不限"}
	for keep, label := range cases {
		if got := snapshotKeepLabel(keep); got != label {
			t.Fatalf("label(%d) = %q, want %q", keep, got, label)
		}
		if got := snapshotKeepValue(label); got != keep {
			t.Fatalf("value(%q) = %d, want %d", label, got, keep)
		}
	}
}
