package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func newTestApp(t *testing.T) (*App, *Store, Script) {
	t.Helper()
	// Keep preferences and remote connections in a temp directory so tests
	// never read or write the real ones.
	t.Setenv("SCRIPT_MANAGER_HOME", t.TempDir())
	store := newTestStore(t)
	it := testInterpreter(t, store)
	folder, err := store.CreateFolder("", "运维")
	if err != nil {
		t.Fatal(err)
	}
	sc, err := store.CreateScript(folder.ID, "问候", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteScript(sc.ID, "echo hello\n"); err != nil {
		t.Fatal(err)
	}
	settings := defaultSettings()
	settings.Library = store.Root()
	a := newApp(settings, store)
	return a, store, sc
}

func TestWindowShowsSidebarAndScript(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"问候", "运维", "运行", "解释器", "运行记录", "设置", "已保存"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("missing %q in window text:\n%s", want, texts)
		}
	}
}

func TestRunButtonRunsScriptAndShowsLog(t *testing.T) {
	a, store, sc := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
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
	if runs[0].Status != RunOK {
		t.Fatalf("status = %s", runs[0].Status)
	}
	texts := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(texts, "hello") {
		t.Fatalf("log not shown:\n%s", texts)
	}
	if !strings.Contains(texts, "退出码 0") {
		t.Fatalf("status line not shown:\n%s", texts)
	}
}

func TestPagesSwitch(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("解释器"); err != nil {
		t.Fatalf("click 解释器: %v", err)
	}
	tt.Frame()
	if !tt.HasText("重新检测全部") {
		t.Fatalf("interpreter page missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if !tt.HasText("/bin/sh") {
		t.Fatalf("interpreter path missing")
	}
	if err := tt.Click("运行记录"); err != nil {
		t.Fatalf("click 运行记录: %v", err)
	}
	tt.Frame()
	if !tt.HasText("条记录") {
		t.Fatalf("runs page missing")
	}
	if err := tt.Click("设置"); err != nil {
		t.Fatalf("click 设置: %v", err)
	}
	tt.Frame()
	if !tt.HasText("库目录") || !tt.HasText("默认超时") {
		t.Fatalf("settings page missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestSearchFiltersSidebar(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	a.query = "问候"
	tt.Frame()
	if !tt.HasText("问候") {
		t.Fatalf("search result missing")
	}
	a.query = "没有这个脚本"
	tt.Frame()
	if !tt.HasText("没有匹配的脚本") {
		t.Fatalf("empty result hint missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestNewScriptDialogOpens(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("新建脚本"); err != nil {
		t.Fatalf("click 新建脚本: %v", err)
	}
	tt.Frame()
	if !tt.HasText("名称") || !tt.HasText("语言") || !tt.HasText("保存") {
		t.Fatalf("dialog missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestNewScriptSeedsStarterContent(t *testing.T) {
	a, store, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("新建脚本"); err != nil {
		t.Fatalf("click 新建脚本: %v", err)
	}
	a.form.name = "演示脚本"
	a.form.lang = "shell"
	a.form.interpIdx = 0
	tt.Frame()
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("click 保存: %v", err)
	}
	tt.Frame()
	var created Script
	for _, sc := range store.Scripts() {
		if sc.Name == "演示脚本" {
			created = sc
		}
	}
	if created.ID == "" {
		t.Fatalf("script not created:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	content, err := store.ReadScript(created)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "演示脚本") {
		t.Fatalf("starter content = %q", content)
	}
	if !tt.HasText("演示脚本") {
		t.Fatalf("new script not shown")
	}
}

func TestDetectAddsInterpreters(t *testing.T) {
	store := newTestStore(t)
	settings := defaultSettings()
	settings.Library = store.Root()
	a := newApp(settings, store)
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("检测本机解释器"); err != nil {
		t.Fatalf("click 检测: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if !a.detecting.Load() && len(store.Interpreters()) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	tt.Frame()
	list := store.Interpreters()
	if len(list) == 0 {
		t.Fatalf("no interpreter added:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if err := tt.Click("解释器"); err != nil {
		t.Fatalf("click 解释器: %v", err)
	}
	tt.Frame()
	if !tt.HasText(list[0].Name) {
		t.Fatalf("interpreter page does not show %q:\n%s", list[0].Name, strings.Join(tt.Texts(), "\n"))
	}
}

func TestNewFolderDialogCreatesFolder(t *testing.T) {
	a, store, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("新建文件夹"); err != nil {
		t.Fatalf("click 新建文件夹: %v", err)
	}
	tt.Frame()
	a.form.name = "归档"
	tt.Frame()
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("click 保存: %v", err)
	}
	tt.Frame()
	found := false
	for _, f := range store.Folders() {
		if f.Name == "归档" {
			found = true
		}
	}
	if !found {
		t.Fatalf("folder not created:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if !tt.HasText("归档") {
		t.Fatal("new folder missing from sidebar")
	}
}

func TestHistoryPageListsRevisions(t *testing.T) {
	a, store, sc := newTestApp(t)
	if _, err := store.WriteScript(sc.ID, "echo hello again\n"); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("⋯"); err != nil {
		t.Fatalf("open menu: %v", err)
	}
	if err := tt.ChooseMenuItem("历史版本…"); err != nil {
		t.Fatalf("choose 历史版本: %v", err)
	}
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("历史版本") || !tt.HasText("全部版本") {
		t.Fatalf("history page missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	revs := store.RevisionsOf(sc.ID)
	if len(revs) == 0 {
		t.Fatal("no revisions recorded")
	}
	if !tt.HasText(revs[0].Hash) {
		t.Fatalf("revision %s not listed:\n%s", revs[0].Hash, strings.Join(tt.Texts(), "\n"))
	}
	if err := tt.Click("恢复整个目录"); err != nil {
		t.Fatalf("restore: %v", err)
	}
	tt.Frame()
	if err := tt.Click("恢复"); err != nil {
		t.Fatalf("confirm restore: %v", err)
	}
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if got := a.router.Path(); got != "/scripts/"+sc.ID {
		t.Fatalf("still on %s after restore", got)
	}
	if got := a.editor(sc).Buffer.String(); !strings.Contains(got, "hello again") {
		t.Fatalf("editor content not restored: %q", got)
	}
}

func TestSettingsShowsPermissions(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	a.router.Push("/settings")
	tt.Frame()
	for _, want := range []string{"权限", "通知", "脚本库访问", "可写", "重新检查"} {
		if !tt.HasText(want) {
			t.Fatalf("settings page lacks %q", want)
		}
	}
}

func TestSettingsSaveRoundTrip(t *testing.T) {
	t.Setenv("SCRIPT_MANAGER_HOME", t.TempDir())
	s := defaultSettings()
	s.DefaultTimeout = 15
	s.Notify = true
	s.NotifyState = "granted"
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadSettings()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.DefaultTimeout != 15 || !got.Notify || got.NotifyState != "granted" {
		t.Fatalf("settings did not round trip: %+v", got)
	}
}

func runAndWait(t *testing.T, a *App, tt *ui.Tester, sc Script) {
	t.Helper()
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
}

func TestSidebarShowsLastRunTimeAndStatus(t *testing.T) {
	a, _, sc := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if !tt.HasText("未运行") {
		t.Fatalf("new script should show 未运行:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	runAndWait(t, a, tt, sc)
	if tt.HasText("未运行") {
		t.Fatalf("row still shows 未运行 after a run:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if !tt.HasText("刚刚") {
		t.Fatalf("row missing last run time:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestInterpreterBadgePopoverShowsDetails(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	a.interpOpen = true
	tt.Frame()
	for _, want := range []string{"名称", "路径", "参数", "探针", "上次校验", "引用脚本"} {
		if !tt.HasText(want) {
			t.Fatalf("popover missing %q:\n%s", want, strings.Join(tt.Texts(), "\n"))
		}
	}
}

func TestSwitchInterpreterUpdatesScript(t *testing.T) {
	a, store, sc := newTestApp(t)
	second := Interpreter{ID: "i-bash", Name: "bash", Language: "shell", Path: "/bin/bash",
		Args: []string{"{script}"}, ProbeArgs: []string{"--version"}, Version: "5.2.0", State: StateOK}
	created, err := store.CreateInterpreter(second)
	if err != nil {
		t.Fatal(err)
	}
	second = created
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	a.switchInterpreter(sc, second.ID)
	tt.Frame()
	got, _ := store.Script(sc.ID)
	if got.InterpID != second.ID {
		t.Fatalf("interpreter not switched: %s", got.InterpID)
	}
	if !tt.HasText("bash 5.2.0") {
		t.Fatalf("badge does not show the new interpreter:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestRunPanelCollapseKeepsProportion(t *testing.T) {
	a, _, sc := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	runAndWait(t, a, tt, sc)
	before := a.panelPct
	if err := tt.Click("收起"); err != nil {
		t.Fatalf("click 收起: %v", err)
	}
	tt.Frame()
	if !a.runCollapsed {
		t.Fatal("run panel did not collapse")
	}
	if a.panelPct != before {
		t.Fatalf("proportion changed from %v to %v", before, a.panelPct)
	}
	if !tt.HasText("展开") {
		t.Fatalf("collapsed bar lacks 展开:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if err := tt.Click("展开"); err != nil {
		t.Fatalf("click 展开: %v", err)
	}
	tt.Frame()
	if a.runCollapsed {
		t.Fatal("run panel did not expand")
	}
	if a.panelPct != before {
		t.Fatalf("proportion changed after expand: %v", a.panelPct)
	}
}

func TestInfoIconOpensInterpreterPopover(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	r, ok := tt.Find("sh 1.0")
	if !ok {
		t.Fatalf("badge missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	tt.ClickAt(r.X-11, r.Y+r.H/2)
	tt.Frame()
	if !a.interpOpen {
		t.Fatal("clicking the info icon did not open the popover")
	}
	if !tt.HasText("引用脚本") {
		t.Fatalf("popover body missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestChevronMenuSwitchesInterpreter(t *testing.T) {
	a, store, sc := newTestApp(t)
	second, err := store.CreateInterpreter(Interpreter{Name: "bash", Language: "shell", Path: "/bin/bash",
		Args: []string{"{script}"}, ProbeArgs: []string{"--version"}, Version: "5.2.0", State: StateOK})
	if err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	r, ok := tt.Find("sh 1.0")
	if !ok {
		t.Fatalf("badge missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	tt.ClickAt(r.X+r.W+14, r.Y+r.H/2)
	tt.Frame()
	if err := tt.ChooseMenuItem("bash 5.2.0"); err != nil {
		t.Fatalf("choose interpreter: %v", err)
	}
	tt.Frame()
	got, _ := store.Script(sc.ID)
	if got.InterpID != second.ID {
		t.Fatalf("script interpreter = %s, want %s", got.InterpID, second.ID)
	}
	if !tt.HasText("bash 5.2.0") {
		t.Fatalf("badge not updated:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestClearRevisionsFromMenu(t *testing.T) {
	a, store, sc := newTestApp(t)
	if _, err := store.AddRevision(sc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteScript(sc.ID, "echo bye\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddRevision(sc.ID); err != nil {
		t.Fatal(err)
	}
	if len(store.RevisionsOf(sc.ID)) != 3 {
		t.Fatalf("revisions = %d", len(store.RevisionsOf(sc.ID)))
	}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if err := tt.Click("⋯"); err != nil {
		t.Fatalf("open menu: %v", err)
	}
	if err := tt.ChooseMenuItem("清除历史版本…"); err != nil {
		t.Fatalf("choose 清除历史版本: %v", err)
	}
	tt.Frame()
	if !tt.HasText("只保留最新一份") {
		t.Fatalf("confirm dialog missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if err := tt.Click("清除"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	tt.Frame()
	if n := len(store.RevisionsOf(sc.ID)); n != 1 {
		t.Fatalf("revisions after clear = %d", n)
	}
}

func TestSyncPageShowsRemoteSetup(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("同步"); err != nil {
		t.Fatalf("click 同步: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if a.syncData().loaded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"仓库状态", "连接远端", "快照", "还没有建立仓库"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("missing %q in sync page:\n%s", want, texts)
		}
	}
}

func TestExportDialogListsScripts(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	a.openExportDialog()
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"导出脚本", "全选", "导出只包含当前内容", "问候"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("missing %q in export dialog:\n%s", want, texts)
		}
	}
}

func TestImportDialogListsBundle(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	a.bundle = bundleState{
		open:     true,
		mode:     "import",
		path:     "scripts.zip",
		ids:      []string{"x-1"},
		labels:   []string{"运维 / 问候"},
		checks:   []bool{true},
		conflict: "导入为副本",
	}
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"导入脚本", "同名脚本", "运维 / 问候", "导入"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("missing %q in import dialog:\n%s", want, texts)
		}
	}
}

func TestSyncPageShowsConnectedRepo(t *testing.T) {
	a, store, _ := newTestApp(t)
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
	state, err := a.sync.SyncNow()
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if state.Health != healthHealthy {
		t.Fatalf("health = %q problem %q", state.Health, state.Problem)
	}
	a.changeSyncData(func(d *syncPanelState) {
		d.state = state
		d.snapshots = a.sync.Snapshots()
		d.loaded = true
	})

	tt := ui.NewTester(a.view, 1180, 760)
	if err := tt.Click("同步"); err != nil {
		t.Fatalf("click 同步: %v", err)
	}
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"仓库状态", "与远端一致", "快照", "最近快照"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("missing %q in sync page:\n%s", want, texts)
		}
	}
	snaps := a.syncData().snapshots
	if len(snaps) == 0 || !strings.Contains(texts, snaps[0].Tag) {
		t.Fatalf("snapshot tag missing, snapshots=%v\n%s", snaps, texts)
	}
	if len(store.Conflicts()) != 0 {
		t.Fatalf("conflicts = %v", store.Conflicts())
	}
}

func TestSidebarFilterSegmentsFillWidth(t *testing.T) {
	a, _, _ := newTestApp(t)
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	r, ok := tt.Find("最近")
	if !ok {
		t.Fatalf("segment missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	tt.ClickAt(a.sidebarW-16, r.Y+r.H/2)
	tt.Frame()
	if a.filter != 1 {
		t.Fatalf("right edge click did not hit 最近: filter=%d", a.filter)
	}
}
