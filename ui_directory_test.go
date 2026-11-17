package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSingleFileScriptHidesTheFileTree(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if tt.HasText("＋") {
		t.Fatalf("single-file script should not show the file tree:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if tt.HasText("入口") {
		t.Fatalf("single-file script should not show an entry tag:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestMultiFileScriptShowsTreeTabsAndSeparateBuffers(t *testing.T) {
	a, store, sc := newTestApp(t)
	if err := store.AddScriptFile(sc.ID, "lib/common.sh", "echo lib\n"); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"＋", "文件", "lib", "common.sh", "入口"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("missing %q in a multi-file script page:\n%s", want, texts)
		}
	}
	if err := tt.Click("common.sh"); err != nil {
		t.Fatalf("open lib/common.sh: %v", err)
	}
	tt.Frame()
	if got := a.curFile(sc); got != "lib/common.sh" {
		t.Fatalf("current file = %q", got)
	}
	if !strings.Contains(a.editor(sc).Buffer.String(), "echo lib") {
		t.Fatalf("editor shows %q", a.editor(sc).Buffer.String())
	}
	a.editor(sc).Buffer.Set("echo edited lib\n")
	tt.Frame()
	if !a.editor(sc).Buffer.Modified() {
		t.Fatal("editing the second file should mark it dirty")
	}
	a.openScriptFile(sc, sc.Entry)
	tt.Frame()
	if got := a.curFile(sc); got != sc.Entry {
		t.Fatalf("current file = %q", got)
	}
	if strings.Contains(a.editor(sc).Buffer.String(), "edited lib") {
		t.Fatal("the two tabs share one buffer")
	}
	if err := tt.Click("common.sh"); err != nil {
		t.Fatalf("switch back to the second tab: %v", err)
	}
	tt.Frame()
	if got := a.editor(sc).Buffer.String(); !strings.Contains(got, "edited lib") {
		t.Fatalf("second tab lost its content: %q", got)
	}
}

func TestRunUsesTheEntryNotTheFirstFile(t *testing.T) {
	a, store, sc := newTestApp(t)
	if err := store.AddScriptFile(sc.ID, "aaa.sh", "echo wrong-file\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteScript(sc.ID, "echo entry-right\n"); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if err := tt.Click("运行"); err != nil {
		t.Fatalf("click 运行: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := a.runner.Active(sc.ID); !ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	runs := store.Runs()
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	if runs[0].Entry != sc.Entry {
		t.Fatalf("run entry = %q, want %q", runs[0].Entry, sc.Entry)
	}
	log := a.logText(runs[0])
	if !strings.Contains(log, "entry-right") || strings.Contains(log, "wrong-file") {
		t.Fatalf("log = %q", log)
	}
}

func TestSetEntryFromTheFileTree(t *testing.T) {
	a, store, sc := newTestApp(t)
	if err := store.AddScriptFile(sc.ID, "other.sh", "echo other\n"); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	a.setEntry(sc, "other.sh")
	tt.Frame()
	fresh, _ := store.Script(sc.ID)
	if fresh.Entry != "other.sh" {
		t.Fatalf("entry = %q", fresh.Entry)
	}
	if !tt.HasText("other.sh") {
		t.Fatalf("header does not show the new entry:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestHeaderMenuHasNoFileCreation(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if err := tt.Click("⋯"); err != nil {
		t.Fatalf("open header menu: %v", err)
	}
	for _, label := range []string{"新建文件…", "新建子目录…"} {
		if err := tt.ChooseMenuItem(label); err == nil {
			t.Fatalf("header menu should not offer %q", label)
		}
	}
}

func TestScriptSettingsHasNoRunFields(t *testing.T) {
	a, store, sc := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if err := tt.Click("⋯"); err != nil {
		t.Fatalf("open header menu: %v", err)
	}
	if err := tt.ChooseMenuItem("脚本设置…"); err != nil {
		t.Fatalf("choose 脚本设置: %v", err)
	}
	tt.Frame()
	if a.form.kind != dialogScriptOptions {
		t.Fatalf("form kind = %v", a.form.kind)
	}
	for _, want := range []string{"高危脚本", "运行前检查", "文件"} {
		if !tt.HasText(want) {
			t.Fatalf("script settings lacks %q:\n%s", want, strings.Join(tt.Texts(), "\n"))
		}
	}
	a.form.dangerous = true
	tt.Frame()
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("save script settings: %v", err)
	}
	tt.Frame()
	fresh, _ := store.Script(sc.ID)
	if !fresh.Dangerous {
		t.Fatal("dangerous tag was not saved")
	}
	if fresh.InterpID != sc.InterpID {
		t.Fatalf("script settings changed the interpreter: %s -> %s", sc.InterpID, fresh.InterpID)
	}
	if fresh.Timeout != sc.Timeout || fresh.Cwd != sc.Cwd {
		t.Fatal("script settings changed run parameters")
	}
}

func TestRunArgsDialogSavesOnlyArguments(t *testing.T) {
	a, store, sc := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if err := tt.Click("参数"); err != nil {
		t.Fatalf("click 参数: %v", err)
	}
	tt.Frame()
	if !tt.HasText("运行参数") {
		t.Fatalf("run args dialog missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	a.form.args = "--yes -v"
	tt.Frame()
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("save run args: %v", err)
	}
	tt.Frame()
	fresh, _ := store.Script(sc.ID)
	if strings.Join(fresh.Args, " ") != "--yes -v" {
		t.Fatalf("args = %v", fresh.Args)
	}
	if fresh.InterpID != sc.InterpID || fresh.Timeout != sc.Timeout || fresh.Cwd != sc.Cwd {
		t.Fatalf("run args changed other settings: %+v", fresh)
	}
}

func TestImportAsksForFolderAndLandsThere(t *testing.T) {
	a, store, _ := newTestApp(t)
	folder, err := store.CreateFolder("", "导入目标")
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "only.sh"), []byte("echo only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openImportTarget(true, src, folder.ID)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	for _, want := range []string{"来源", "导入到", "名称", "整个目录会复制进脚本库"} {
		if !tt.HasText(want) {
			t.Fatalf("import target dialog lacks %q:\n%s", want, strings.Join(tt.Texts(), "\n"))
		}
	}
	if err := tt.Click("导入"); err != nil {
		t.Fatalf("click 导入: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && a.importData().busy {
		time.Sleep(20 * time.Millisecond)
	}
	tt.Frame()
	var imported Script
	for _, sc := range store.Scripts() {
		if sc.FolderID == folder.ID {
			imported = sc
		}
	}
	if imported.ID == "" {
		t.Fatalf("no script landed in the folder:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if imported.Entry != "only.sh" {
		t.Fatalf("entry = %q", imported.Entry)
	}
	if a.importFolderID != folder.ID {
		t.Fatalf("last import folder = %q, want %q", a.importFolderID, folder.ID)
	}
}

func TestImportTargetRemembersTheLastFolder(t *testing.T) {
	a, store, _ := newTestApp(t)
	folder, err := store.CreateFolder("", "记住我")
	if err != nil {
		t.Fatal(err)
	}
	a.importFolderID = folder.ID
	a.openImportTarget(false, filepath.Join(t.TempDir(), "note.txt"), "")
	folders := a.folderChoices()
	if a.form.folderIdx < 0 || a.form.folderIdx >= len(folders) {
		t.Fatalf("folderIdx = %d", a.form.folderIdx)
	}
	if folders[a.form.folderIdx].ID != folder.ID {
		t.Fatalf("default folder = %q, want %q", folders[a.form.folderIdx].ID, folder.ID)
	}
}

func TestFolderMenuOffersImport(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if err := tt.RightClick("运维"); err != nil {
		t.Fatalf("right click folder: %v", err)
	}
	menu := strings.Join(tt.Menu(), "\n")
	for _, want := range []string{"导入目录…", "导入文件…"} {
		if !strings.Contains(menu, want) {
			t.Fatalf("folder menu lacks %q:\n%s", want, menu)
		}
	}
	tt.CloseMenu()
	tt.Frame()
}

func TestRunImportTargetsTheGivenFolder(t *testing.T) {
	a, store, _ := newTestApp(t)
	folder, err := store.CreateFolder("", "直连导入")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "deploy.sh")
	if err := os.WriteFile(src, []byte("echo deploy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.runImport(false, src, folder.ID, "部署脚本"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := store.ScriptByName(folder.ID, "部署脚本"); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	sc, ok := store.ScriptByName(folder.ID, "部署脚本")
	if !ok {
		t.Fatalf("script missing in %q", folder.Name)
	}
	if sc.Entry != "部署脚本.sh" {
		t.Fatalf("entry = %q", sc.Entry)
	}
	if err := a.runImport(false, src, folder.ID, "部署脚本"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := store.ScriptByName(folder.ID, "部署脚本 (2)"); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := store.ScriptByName(folder.ID, "部署脚本 (2)"); !ok {
		t.Fatalf("second import should be suffixed: %+v", store.Scripts())
	}
}
