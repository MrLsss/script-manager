package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func markDangerous(t *testing.T, store *Store, sc Script) {
	t.Helper()
	fresh, ok := store.Script(sc.ID)
	if !ok {
		t.Fatal("script missing")
	}
	fresh.Dangerous = true
	if err := store.UpdateScript(fresh); err != nil {
		t.Fatal(err)
	}
}

func TestDangerousScriptShowsWarnings(t *testing.T) {
	a, store, sc := newTestApp(t)
	markDangerous(t, store, sc)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(texts, "高危脚本：每次运行前都会弹出确认框") {
		t.Fatalf("editor warning missing:\n%s", texts)
	}
	if !tt.HasText("高危") {
		t.Fatalf("sidebar tag missing:\n%s", texts)
	}
}

func TestDangerousScriptAsksBeforeEveryRun(t *testing.T) {
	a, store, sc := newTestApp(t)
	markDangerous(t, store, sc)
	if _, err := store.WriteScript(sc.ID, "echo danger-run\n"); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if err := tt.Click("运行"); err != nil {
		t.Fatalf("click 运行: %v", err)
	}
	tt.Frame()
	if !tt.HasText("高危脚本") || !tt.HasText("仍然运行") {
		t.Fatalf("confirmation missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if runs := store.Runs(); len(runs) != 0 {
		t.Fatalf("script ran before confirmation: %v", runs)
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click 取消: %v", err)
	}
	tt.Frame()
	if runs := store.Runs(); len(runs) != 0 {
		t.Fatalf("cancelled run started: %v", runs)
	}
	if err := tt.Click("运行"); err != nil {
		t.Fatalf("click 运行 again: %v", err)
	}
	tt.Frame()
	if err := tt.Click("仍然运行"); err != nil {
		t.Fatalf("click 仍然运行: %v", err)
	}
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
}

func TestScriptSettingsSavesTheDangerousTag(t *testing.T) {
	a, store, sc := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if err := tt.Click("⋯"); err != nil {
		t.Fatalf("open menu: %v", err)
	}
	if err := tt.ChooseMenuItem("脚本设置…"); err != nil {
		t.Fatalf("choose 脚本设置: %v", err)
	}
	tt.Frame()
	a.form.dangerous = true
	tt.Frame()
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("save: %v", err)
	}
	tt.Frame()
	fresh, _ := store.Script(sc.ID)
	if !fresh.Dangerous {
		t.Fatal("the dangerous tag was not saved")
	}
	if !tt.HasText("高危脚本：每次运行前都会弹出确认框") {
		t.Fatalf("editor warning missing after saving:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}
