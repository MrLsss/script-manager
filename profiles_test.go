package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestApplyRunProfileOverridesAndFallsBack(t *testing.T) {
	sc := Script{
		Args:      []string{"default-arg"},
		Env:       map[string]string{"MODE": "dev"},
		Timeout:   1,
		Profiles:  []RunProfile{{ID: "p1", Name: "生产", Args: []string{"--prod"}, Env: map[string]string{"MODE": "prod"}, Timeout: 30}},
		ProfileID: "p1",
	}
	got := applyRunProfile(sc)
	if strings.Join(got.Args, " ") != "--prod" || got.Env["MODE"] != "prod" || got.Timeout != 30 {
		t.Fatalf("profile not applied: %+v", got)
	}
	sc.ProfileID = "missing"
	got = applyRunProfile(sc)
	if strings.Join(got.Args, " ") != "default-arg" || got.Env["MODE"] != "dev" || got.Timeout != 1 {
		t.Fatalf("missing profile should use the default: %+v", got)
	}
	if label := profileLabel(sc); label != "参数" {
		t.Fatalf("label = %q", label)
	}
	sc.ProfileID = "p1"
	if label := profileLabel(sc); label != "参数 · 生产" {
		t.Fatalf("label = %q", label)
	}
}

func TestRunProfileSavedFromTheArgsDialog(t *testing.T) {
	a, store, sc := newTestApp(t)
	if _, err := store.WriteScript(sc.ID, "echo \"mode=$MODE args=$*\"\n"); err != nil {
		t.Fatal(err)
	}
	a.openRunArgs(sc)
	a.form.profileName = "生产"
	a.form.args = "--flag"
	a.form.env = "MODE=prod"
	a.saveRunProfileCopy(sc)
	a.saveRunArgs()
	fresh, _ := store.Script(sc.ID)
	if len(fresh.Profiles) != 1 || fresh.ProfileID != fresh.Profiles[0].ID {
		t.Fatalf("profile not saved: %+v", fresh)
	}
	if fresh.Profiles[0].Name != "生产" || fresh.Profiles[0].Args[0] != "--flag" {
		t.Fatalf("profile = %+v", fresh.Profiles[0])
	}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if !tt.HasText("参数 · 生产") {
		t.Fatalf("header lacks the profile label:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	a.runScript(fresh)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runs := store.Runs()
		if len(runs) == 1 && runs[0].Status != RunRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	runs := store.Runs()
	if len(runs) != 1 || runs[0].Status != RunOK {
		t.Fatalf("runs = %+v", runs)
	}
	log := a.runner.LogText(runs[0])
	if !strings.Contains(log, "mode=prod") || !strings.Contains(log, "args=--flag") {
		t.Fatalf("profile did not reach the run: %q", log)
	}
	if strings.Join(runs[0].Args, " ") != "--flag" {
		t.Fatalf("run args = %v", runs[0].Args)
	}
}

func TestDeletingAProfileFallsBackToDefault(t *testing.T) {
	a, store, sc := newTestApp(t)
	a.openRunArgs(sc)
	a.form.profileName = "临时"
	a.form.args = "--temp"
	a.saveRunProfileCopy(sc)
	a.saveRunArgs()
	fresh, _ := store.Script(sc.ID)
	if len(fresh.Profiles) != 1 {
		t.Fatalf("profiles = %+v", fresh.Profiles)
	}
	a.openRunArgs(fresh)
	if a.form.profileID == "" {
		t.Fatal("the selected profile should load")
	}
	a.deleteRunProfile(fresh)
	fresh, _ = store.Script(sc.ID)
	if len(fresh.Profiles) != 0 || fresh.ProfileID != "" {
		t.Fatalf("profile not removed: %+v", fresh)
	}
}
