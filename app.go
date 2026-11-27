package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

type dialogKind int

const (
	dialogNone dialogKind = iota
	dialogScript
	dialogFolder
	dialogRename
	dialogInterpreter
	dialogScriptOptions
	dialogRunArgs
	dialogImport
	dialogRemote
	dialogRemoteHost
	dialogNewFile
	dialogNewDir
	dialogRenameFile
	dialogEntry
)

// App holds the state of the window.
type App struct {
	win               *mygo.Window
	tray              *mygo.Tray
	trayPanel         *mygo.Window
	trayQuery         string
	quitting          bool
	logSearch         map[string]string
	encodings         map[string]textEncoding
	importPanel       atomic.Pointer[importPanelState]
	importMu          sync.Mutex
	pendingImport     atomic.Pointer[pendingImportResult]
	importRunning     atomic.Bool
	importQueue       []importJob
	queueMu           sync.Mutex
	uiMu              sync.Mutex
	folderCountsCache map[string]int
	lastRunCache      map[string]Run
	editableCache     map[string]bool
	symlinkCache      map[string][]string
	router            *ui.Router
	store             *Store
	runner            *Runner
	settings          Settings

	sidebarW float32
	panelPct float32
	runsW    float32
	connW    float32

	query      string
	filter     int
	selected   string
	sideSel    string
	folderOpen map[string]bool

	editors  map[string]*editor.State
	files    map[string]string   // script id → 当前相对路径
	tabs     map[string][]string // script id → 打开的相对路径
	treeW    float32
	treeOpen map[string]bool

	interpSel  int
	interpOpen bool
	runSel     int
	runFilter  int
	runOpen    string
	runChecks  map[string]bool

	logScript  string
	logRunID   string
	logLines   []LogLine
	logVersion int
	logShown   int

	toast      atomic.Pointer[toastState]
	toastToken atomic.Int64

	keepRev       bool
	appearanceIdx int

	detecting atomic.Bool

	notifySupported bool
	permBusy        bool
	libChecked      bool
	libWritable     bool
	libProblem      string
	proxyErr        string

	revScriptID string
	revSel      int
	revSt       *editor.State
	revW        float32
	revFile     string
	revFiles    []RevisionFile

	runCollapsed bool

	sync       *Sync
	remotes    *RemoteStore
	secrets    *Secrets
	remoteSel  string
	remoteBusy atomic.Bool
	syncPanel  atomic.Pointer[syncPanelState]
	syncMu     sync.Mutex
	remoteText string

	bundle         bundleState
	importFolderID string

	secretsOpen  bool
	secretName   string
	secretValue  string
	secretErr    string
	secretWarned bool

	form    formState
	confirm confirmState
}

// pendingImportResult is completed on the UI thread, because a background
// import may not touch the router, dialogs or the editor map.
type pendingImportResult struct {
	script   Script
	err      string
	canceled bool
}

func (a *App) consumePendingImport() {
	p := a.pendingImport.Swap(nil)
	if p == nil {
		return
	}
	if p.canceled {
		a.showNotice("已取消导入")
		return
	}
	if p.err != "" {
		a.alertError("导入失败", p.err)
		return
	}
	a.finishImport(p.script)
}

// importPanelState is the copy progress line. It is replaced as a whole so a
// frame never reads a value a background import is changing.
type importPanelState struct {
	busy   bool
	status string
	cancel context.CancelFunc
}

func (a *App) importData() *importPanelState {
	if p := a.importPanel.Load(); p != nil {
		return p
	}
	return &importPanelState{}
}

func (a *App) changeImport(fn func(*importPanelState)) {
	a.importMu.Lock()
	defer a.importMu.Unlock()
	next := &importPanelState{}
	if cur := a.importPanel.Load(); cur != nil {
		*next = *cur
	}
	fn(next)
	a.importPanel.Store(next)
}

// toastState is the line shown above the content. It is replaced through an
// atomic pointer because a background task may show it.
type toastState struct {
	text  string
	token int64
}

// syncPanelState is everything the sync page and the header dot draw. It is
// replaced as a whole through changeSyncData, so a background sync never
// mutates a value a frame is reading.
type syncPanelState struct {
	state     SyncState
	snapshots []Snapshot
	objects   int64
	library   int64
	busy      bool
	loaded    bool
	notice    string
}

// syncData returns the current panel state. The value is immutable: writers
// copy it instead of editing it in place.
func (a *App) syncData() *syncPanelState {
	if p := a.syncPanel.Load(); p != nil {
		return p
	}
	return &syncPanelState{}
}

// changeSyncData applies a change to a copy of the panel state and publishes
// it atomically.
func (a *App) changeSyncData(fn func(*syncPanelState)) {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	next := &syncPanelState{}
	if cur := a.syncPanel.Load(); cur != nil {
		*next = *cur
	}
	fn(next)
	a.syncPanel.Store(next)
}

// bundleState drives the export and import dialogs.
type bundleState struct {
	open       bool
	mode       string // "export" or "import"
	path       string
	ids        []string
	labels     []string
	checks     []bool
	includeRev bool
	conflict   string
	err        string
}

type formState struct {
	kind    dialogKind
	open    bool
	title   string
	err     string
	name    string
	path    string
	args    string
	probe   string
	cwd     string
	timeout string
	lang    string
	tags    string

	folderIdx int

	targetID     string
	folderID     string
	folderTarget bool

	rel          string
	renameEntry  bool
	admin        bool
	dangerous    bool
	preflight    bool
	env          string
	importIsDir  bool
	target       string
	port         string
	note         string
	preview      string
	previewErr   string
	profileID    string
	profileName  string
	entryChoices []string
	entryIdx     int
}

type confirmState struct {
	open    bool
	title   string
	message string
	buttons []string
	action  func(int)
}

func newApp(settings Settings, store *Store) *App {
	a := &App{
		router:     ui.NewRouter("/scripts"),
		store:      store,
		settings:   settings,
		sidebarW:   280,
		panelPct:   70,
		runsW:      320,
		connW:      320,
		revW:       320,
		treeW:      200,
		folderOpen: map[string]bool{},
		treeOpen:   map[string]bool{},
		editors:    map[string]*editor.State{},
		files:      map[string]string{},
		tabs:       map[string][]string{},
		encodings:  map[string]textEncoding{},
		runChecks:  map[string]bool{},
	}
	a.bindStore(store)
	if path, err := remoteStorePath(); err == nil {
		if rs, err := OpenRemoteStore(path); err == nil {
			a.remotes = rs
		}
	}
	if a.remotes == nil {
		a.remotes = emptyRemoteStore()
	}
	if path, err := secretsPath(); err == nil {
		if s, err := OpenSecrets(path); err == nil {
			a.secrets = s
		}
	}
	if a.secrets == nil {
		a.secrets = emptySecrets()
	}
	a.bundle.conflict = string(ConflictCopy)
	a.applyProxySettings()
	a.keepRev = settings.KeepRevisions > 0
	a.applyLanguage()
	a.checkLibraryWrite()
	switch settings.Appearance {
	case "light":
		a.appearanceIdx = 1
	case "dark":
		a.appearanceIdx = 2
	}
	for _, sc := range store.Scripts() {
		a.folderOpen[sc.FolderID] = true
	}
	if scripts := store.Scripts(); len(scripts) > 0 {
		sort.Slice(scripts, func(i, j int) bool { return scripts[i].Position < scripts[j].Position })
		a.selected = scripts[0].ID
		a.router = ui.NewRouter("/scripts/" + scripts[0].ID)
	}
	return a
}

func (a *App) view(c *ui.Context) {
	a.uiMu.Lock()
	defer a.uiMu.Unlock()
	a.consumePendingImport()
	theme.Apply(c)
	ui.Column(c).Fill().Children(func() {
		a.titleBar(c)
		ui.Split(c, &a.sidebarW, func() { a.sidebar(c) }, func() { a.mainPane(c) }).
			Grow(1).FillWidth().SplitReset(280)
		a.statusBars(c)
		a.dialogs(c)
	})
	a.shortcuts(c)
}

// titleBarHeight is the strip the window controls sit in, above the content.
const titleBarHeight = 32

// titleBar is the room the window controls take: dragging it moves the window,
// and the window shows no title text there.
func (a *App) titleBar(c *ui.Context) {
	bar := c.TitleBar()
	ui.Row(c).Height(max(bar.Height, titleBarHeight)).FillWidth().
		Padding(0, bar.Right, 0, bar.Left).DragWindow()
}

// statusBars floats the import progress and the notice over the bottom of the
// window. They are overlays rather than rows: a row that comes and goes would
// resize everything above it, which showed up as the sidebar jumping up for a
// couple of seconds after every action.
func (a *App) statusBars(c *ui.Context) {
	importing := a.importData()
	toast := a.toast.Load()
	hasToast := toast != nil && toast.text != ""
	if !importing.busy && !hasToast {
		return
	}
	// The bars sit over the main pane, right of the sidebar, so they never
	// cover the navigation.
	ui.Overlay(c, func() {
		bottom := float32(16)
		if importing.busy {
			a.importBar(c, importing.status)
			bottom = 48
		}
		if hasToast {
			a.toastBar(c, toast.text, bottom)
		}
	})
}

func (a *App) mainPane(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).Background(t.Background).Children(func() {
		a.header(c)
		ui.Divider(c)
		if a.syncData().notice != "" {
			a.syncNoticeBar(c)
		}
		a.router.View(c, func(r *ui.Route) {
			switch {
			case r.Match("/scripts/{id}/history"):
				r.Title("历史版本")
				a.historyPage(c, r.Param("id"))
			case r.Match("/scripts/{id}"):
				r.Title("脚本")
				a.scriptPage(c, r.Param("id"))
			case r.Match("/scripts"):
				r.Title("脚本")
				a.welcomePage(c)
			case r.Match("/interpreters"):
				r.Title("解释器")
				a.interpretersPage(c)
			case r.Match("/runs"):
				r.Title("运行记录")
				a.runsPage(c)
			case r.Match("/connections"):
				r.Title("连接")
				a.connectionsPage(c)
			case r.Match("/sync"):
				r.Title("同步")
				a.syncPage(c)
			case r.Match("/settings"):
				r.Title("设置")
				a.settingsPage(c)
			}
		})
	})
}

func (a *App) header(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Height(48).PaddingX(16).Gap(10).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		if sc, ok := a.currentScript(); ok {
			if a.onHistory() {
				a.historyHeader(c, sc)
				ui.Spacer(c)
				if ui.Button(c, tr("返回编辑")).Padding(4, 14).Clicked() {
					a.router.Replace("/scripts/" + sc.ID)
				}
				return
			}
			a.headerTitle(c, sc)
			ui.Spacer(c)
			a.syncHeaderDot(c)
			a.headerActions(c, sc)
			return
		}
		ui.Text(c, tr(a.pageTitle())).FontSize(15).FontWeight(600)
		ui.Spacer(c)
		a.syncHeaderDot(c)
		switch a.pageTitle() {
		case "解释器":
			if a.detecting.Load() {
				ui.Spinner(c).Width(14).Height(14)
				ui.Text(c, tr("正在检测本机解释器…")).FontSize(12).TextColor(t.TextMuted)
			} else if ui.PrimaryButton(c, tr("检测本机解释器")).Clicked() {
				a.detectInterpreters()
			}
		}
	})
}

// headerTitle leads with the file that runs, bold and black: the script's
// own name stays in the sidebar, and a duplicate in the header was noise.
func (a *App) headerTitle(c *ui.Context, sc Script) {
	t := c.Theme()
	ui.Text(c, sc.Entry).FontSize(15).FontWeight(700).SingleLine().
		Tooltip(tr("脚本") + "：" + sc.Name)
	state, color := "已保存", t.Success
	dirty := 0
	for _, rel := range a.scriptTabs(sc) {
		if st, ok := a.editors[editorKey(sc.ID, rel)]; ok && st.Buffer.Modified() {
			dirty++
		}
	}
	if dirty > 0 {
		state, color = trf("%d 个文件未保存", dirty), t.Warning
	}
	ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
	ui.Text(c, state).FontSize(11).TextColor(color)
	if sc.RunAsAdmin {
		ui.Text(c, tr("管理员")).FontSize(10.5).TextColor(t.Warning).Tooltip(tr("运行时请求管理员权限"))
	}
	if sc.HostID != "" {
		label := tr("远程 ") + "· " + tr("连接已删除")
		color := t.Danger
		if h, ok := a.findHost(sc); ok {
			label = tr("远程 ") + "· " + h.Name
			color = t.Accent
		} else if sc.HostName != "" {
			label = tr("远程 ") + "· " + sc.HostName + tr("（未绑定）")
		}
		ui.Text(c, label).FontSize(10.5).TextColor(color).Tooltip(tr("运行目标"))
	}
}

func (a *App) headerActions(c *ui.Context, sc Script) {
	a.interpBadge(c, sc)
	a.profileControl(c, sc)
	if run, ok := a.runner.Active(sc.ID); ok {
		label := "停止"
		if run.Admin {
			label = "断开查看"
		}
		if ui.Button(c, label).Padding(4, 14).Clicked() {
			a.stopScript(sc)
		}
	} else {
		if ui.PrimaryButton(c, tr("运行")).Padding(4, 14).Clicked() {
			a.runScript(sc)
		}
	}
	a.runTargetControl(c, sc)
	ui.MenuButton(c, "⋯", func(m *ui.Menu) {
		if m.Item(tr("脚本设置…")).Chosen() {
			a.openScriptOptions(sc)
		}
		if m.Item(tr("历史版本…")).Chosen() {
			a.openRevisions(sc)
		}
		if m.Item(tr("清除历史版本…")).Chosen() {
			a.clearRevisions(sc)
		}
		if m.Item(tr("重命名…")).Chosen() {
			a.openRename(dialogScript, sc.ID, sc.Name, "重命名脚本")
		}
		m.Separator()
		if m.Item(tr("在访达中显示")).Chosen() {
			mygo.Shell.ShowItemInFolder(a.store.ScriptPath(sc))
		}
		if m.Item(tr("复制文件路径")).Chosen() {
			mygo.Clipboard.WriteText(a.store.ScriptPath(sc))
			a.showNotice("已复制文件路径")
		}
		m.Separator()
		if m.Item(tr("移到回收站…")).Chosen() {
			a.confirmAction("移到回收站", "「"+sc.Name+"」及其全部运行记录会移入库的回收站。",
				[]string{"取消", "移到回收站"}, func(i int) {
					if i != 1 {
						return
					}
					if err := a.store.DeleteScript(sc.ID); err != nil {
						a.alertError("删除失败", err.Error())
						return
					}
					a.forgetScript(sc.ID)
					a.selected = ""
					a.router.Replace("/scripts")
					a.showNotice("已移到回收站")
				})
		}
	})
}

// runTargetControl lets the user pick the machine a script runs on without
// opening the settings dialog.
func (a *App) runTargetControl(c *ui.Context, sc Script) {
	t := c.Theme()
	label := "本机"
	if sc.HostID != "" {
		label = "连接已删除"
		if h, ok := a.findHost(sc); ok {
			label = h.Name
		} else if sc.HostName != "" {
			label = sc.HostName + "（未绑定）"
		}
	}
	ui.Text(c, tr("运行目标")).FontSize(11).TextColor(t.TextMuted)
	ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		if m.Item(tr("本机")).Radio(sc.HostID == "").Chosen() {
			a.switchRunTarget(sc, "")
		}
		m.Separator()
		hosts := a.remotes.Hosts()
		if len(hosts) == 0 {
			m.Item(tr("还没有远程连接")).Disabled(true)
		}
		for _, h := range hosts {
			h := h
			if m.Item(h.Name).Radio(sc.HostID == h.ID).Chosen() {
				a.switchRunTarget(sc, h.ID)
			}
		}
		m.Separator()
		if m.Item(tr("管理连接…")).Chosen() {
			a.router.Push("/connections")
		}
	}).Tooltip(tr("选择在哪个机器上运行"))
}

// switchRunTarget saves the choice immediately, so the header and the run
// button always agree.
func (a *App) switchRunTarget(sc Script, hostID string) {
	if sc.HostID == hostID {
		return
	}
	fresh, ok := a.store.Script(sc.ID)
	if !ok {
		return
	}
	fresh.HostID = hostID
	fresh.HostName = ""
	fresh.HostTarget = ""
	if hostID != "" {
		if h, ok := a.remotes.Host(hostID); ok {
			fresh.HostName = h.Name
			fresh.HostTarget = h.Target
		}
	}
	if err := a.store.UpdateScript(fresh); err != nil {
		a.alertError("保存失败", err.Error())
		return
	}
	if hostID == "" {
		a.showNotice("运行目标已改为本机")
		return
	}
	if h, ok := a.remotes.Host(hostID); ok {
		a.showNotice("运行目标已改为 " + h.Name)
	}
}

// findHost resolves a script's target even after the library moved to a
// machine where the host has a different id: the stored target or name is
// enough to find it again.
func (a *App) findHost(sc Script) (RemoteHost, bool) {
	if sc.HostID != "" {
		if h, ok := a.remotes.Host(sc.HostID); ok {
			return h, true
		}
	}
	for _, h := range a.remotes.Hosts() {
		if sc.HostTarget != "" && strings.EqualFold(h.Target, sc.HostTarget) {
			return h, true
		}
		if sc.HostName != "" && strings.EqualFold(h.Name, sc.HostName) {
			return h, true
		}
	}
	return RemoteHost{}, false
}

// rebindHost stores the id found by target or name, so later runs do not have
// to look it up again.
func (a *App) rebindHost(sc Script, h RemoteHost) {
	fresh, ok := a.store.Script(sc.ID)
	if !ok || fresh.HostID == h.ID {
		return
	}
	fresh.HostID = h.ID
	fresh.HostName = h.Name
	fresh.HostTarget = h.Target
	_ = a.store.UpdateScript(fresh)
}

// profileControl is the 参数 button. Once several parameter sets exist it
// opens a menu that switches between them by name; 设置参数 is the one entry
// that edits the active set. With only the default it opens the dialog
// straight away.
func (a *App) profileControl(c *ui.Context, sc Script) {
	if len(sc.Profiles) == 0 && sc.DefaultName == "" {
		if ui.Button(c, profileLabel(sc)).Padding(4, 14).Clicked() {
			a.openRunArgs(sc)
		}
		return
	}
	ui.PopoverMenuButton(c, profileLabel(sc), func(m *ui.Menu) {
		if m.Item(sc.defaultProfileName()).Radio(sc.ProfileID == "").Chosen() {
			a.switchProfile(sc, "")
		}
		for _, p := range sc.Profiles {
			p := p
			if m.Item(p.Name).Radio(sc.ProfileID == p.ID).Chosen() {
				a.switchProfile(sc, p.ID)
			}
		}
		m.Separator()
		if m.Item(tr("设置参数…")).Chosen() {
			a.openRunArgs(sc)
		}
	}).Tooltip(tr("切换运行参数"))
}

// switchProfile saves the chosen parameter set at once, so the button and
// the next run always agree.
func (a *App) switchProfile(sc Script, id string) {
	if sc.ProfileID == id {
		return
	}
	if _, ok := sc.profileByID(id); !ok && id != "" {
		return
	}
	fresh, ok := a.store.Script(sc.ID)
	if !ok {
		return
	}
	fresh.ProfileID = id
	if err := a.store.UpdateScript(fresh); err != nil {
		a.alertError("切换失败", err.Error())
		return
	}
	name := fresh.defaultProfileName()
	if p, ok := fresh.profileByID(id); ok {
		name = p.Name
	}
	a.showNotice(trf("已切换到参数「%s」", name))
}

// badgeHost is the machine the header shows the interpreter for: a remote
// host when the script points at one, the local machine otherwise.
func (a *App) badgeHost(sc Script) (RemoteHost, bool) {
	if sc.HostID == "" && sc.HostName == "" && sc.HostTarget == "" {
		return RemoteHost{}, false
	}
	if h, ok := a.findHost(sc); ok {
		return h, true
	}
	return RemoteHost{}, false
}

// interpBadge shows the interpreter the current run target will actually
// use, and its whole label sits inside the dropdown button. The info icon
// left of it opens the details popover.
func (a *App) interpBadge(c *ui.Context, sc Script) {
	t := c.Theme()
	host, isRemote := a.badgeHost(sc)
	label := tr("未配置解释器")
	color := t.Danger
	if isRemote {
		if ri, ok := a.remotes.Interpreter(host, sc.Language); ok {
			label = ri.Name + " " + ri.Version
			color = t.TextMuted
		} else {
			label = tr("未检测远程解释器")
		}
	} else if it, ok := a.resolvedLocalInterpreter(sc); ok {
		label = it.Name + " " + it.Version
		color = t.TextMuted
	}
	info := ui.ButtonBase(c).Padding(3).Radius(6).Tooltip(tr("解释器信息")).
		OnClick(func() { a.interpOpen = !a.interpOpen })
	info.Children(func() {
		ui.Icon(c, lucide.Info()).Width(15).Height(15).TextColor(color)
	})
	ui.PopoverMenuButton(c, label, func(m *ui.Menu) {
		if isRemote {
			a.remoteInterpMenu(m, sc, host)
			return
		}
		a.localInterpMenu(m, sc)
	}).Tooltip(tr("切换解释器"))
	ui.Popover(c, info, &a.interpOpen, func() {
		a.interpPopover(c, sc, isRemote, host)
	})
}

// localInterpMenu offers the local interpreters that can run the language,
// marking the one a run would actually use.
func (a *App) localInterpMenu(m *ui.Menu, sc Script) {
	cur, _ := a.resolvedLocalInterpreter(sc)
	opts, ids := a.interpreterOptions(sc.Language)
	if len(opts) == 0 {
		m.Item(tr("没有可用于") + languageName(sc.Language) + "的解释器").Disabled(true)
		return
	}
	for i, opt := range opts {
		if m.Item(opt).Radio(ids[i] == cur.ID).Chosen() {
			a.switchInterpreter(sc, ids[i])
		}
	}
	m.Separator()
	if m.Item(tr("脚本设置…")).Chosen() {
		a.openScriptOptions(sc)
	}
}

// remoteInterpMenu offers the interpreters detected on the host for the
// script's language, remembering the choice as the host's default.
func (a *App) remoteInterpMenu(m *ui.Menu, sc Script, host RemoteHost) {
	cur := ""
	if ri, ok := a.remotes.Interpreter(host, sc.Language); ok {
		cur = ri.Path
	}
	var usable []RemoteInterp
	for _, it := range a.remotes.Interpreters(host.ID) {
		if it.Language == sc.Language && it.State == RemoteStateOK {
			usable = append(usable, it)
		}
	}
	if len(usable) == 0 {
		m.Item(tr("还没有检测到") + languageName(sc.Language) + "解释器").Disabled(true)
	} else {
		for _, it := range usable {
			it := it
			if m.Item(it.Name + " " + it.Version).Radio(it.Path == cur).Chosen() {
				if err := a.remotes.SetPreferred(host.ID, sc.Language, it.Path); err != nil {
					a.alertError("保存失败", err.Error())
					return
				}
				a.showNotice("已切换到 " + it.Name + " " + it.Version)
			}
		}
	}
	m.Separator()
	if m.Item(tr("检测远程解释器…")).Chosen() {
		a.detectHostInterpreters(host)
	}
	if m.Item(tr("管理连接…")).Chosen() {
		a.router.Push("/connections")
	}
}

// resolvedLocalInterpreter is the interpreter a local run of this script
// would use: the configured one when it is usable, otherwise the first
// usable one for the language. A script created without an interpreter
// still shows and runs with a sensible default instead of refusing.
func (a *App) resolvedLocalInterpreter(sc Script) (Interpreter, bool) {
	if it, ok := a.store.Interpreter(sc.InterpID); ok && it.State == StateOK {
		return it, true
	}
	list := a.interpretersFor(sc.Language)
	if len(list) > 0 {
		return list[0], true
	}
	return Interpreter{}, false
}

func (a *App) clearRevisions(sc Script) {
	revs := a.store.RevisionsOf(sc.ID)
	if len(revs) <= 1 {
		a.showNotice("只有最新一份版本，无需清除")
		return
	}
	a.confirmAction("清除历史版本",
		trf("「%s」的历史版本只保留最新一份，其余 %d 份会被删除，此操作不可撤销。", sc.Name, len(revs)-1),
		[]string{"取消", "清除"},
		func(i int) {
			if i != 1 {
				return
			}
			n, err := a.store.ClearRevisions(sc.ID)
			if err != nil {
				a.alertError("清除失败", err.Error())
				return
			}
			if a.revScriptID == sc.ID {
				a.revSt = nil
				a.revSel = 0
			}
			a.showNotice(trf("已清除 %d 份历史版本", n))
		})
}

func (a *App) switchInterpreter(sc Script, id string) {
	if id == sc.InterpID {
		return
	}
	sc.InterpID = id
	if err := a.store.UpdateScript(sc); err != nil {
		a.alertError("切换失败", err.Error())
		return
	}
	if it, ok := a.store.Interpreter(id); ok {
		a.showNotice("已切换到 " + it.Name + " " + it.Version)
	}
}

func (a *App) interpPopover(c *ui.Context, sc Script, isRemote bool, host RemoteHost) {
	t := c.Theme()
	ui.Column(c).Width(320).Padding(14).Gap(8).Children(func() {
		ui.Text(c, tr("解释器")).FontSize(12).FontWeight(600)
		if isRemote {
			ri, ok := a.remotes.Interpreter(host, sc.Language)
			if !ok {
				ui.Text(c, tr("还没有在这台服务器上检测解释器。")).FontSize(12).TextColor(t.TextMuted)
				return
			}
			a.metaLine(c, "名称", ri.Name+" "+ri.Version)
			a.metaLine(c, "路径", ri.Path)
			a.metaLine(c, "状态", remoteStateText(ri.State))
			a.metaLine(c, "上次检测", fmtTime(ri.CheckedAt))
			a.metaLine(c, "运行目标", host.Name)
			return
		}
		it, ok := a.resolvedLocalInterpreter(sc)
		if !ok {
			ui.Text(c, tr("这个脚本还没有配置解释器。")).FontSize(12).TextColor(t.TextMuted)
			return
		}
		a.metaLine(c, "名称", it.Name+" "+it.Version)
		a.metaLine(c, "路径", it.Path)
		a.metaLine(c, "参数", strings.Join(it.Args, " "))
		a.metaLine(c, "探针", strings.Join(it.ProbeArgs, " "))
		a.metaLine(c, "上次校验", it.CheckedAt.Format("2006-01-02 15:04"))
		a.metaLine(c, "引用脚本", trf("%d 个", a.store.Usage(it.ID)))
	})
}

func (a *App) metaLine(c *ui.Context, label, value string) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
		ui.Text(c, tr(label)).FontSize(11).TextColor(t.TextMuted).Width(64).Shrink(0)
		ui.Text(c, value).FontSize(11).TextColor(t.Text).Wrap()
	})
}

func (a *App) pageTitle() string {
	p := a.router.Path()
	switch {
	case strings.HasPrefix(p, "/interpreters"):
		return "解释器"
	case strings.HasPrefix(p, "/runs"):
		return "运行记录"
	case strings.HasPrefix(p, "/sync"):
		return "同步"
	case strings.HasPrefix(p, "/connections"):
		return "连接"
	case strings.HasPrefix(p, "/settings"):
		return "设置"
	}
	return "脚本"
}

func (a *App) currentScript() (Script, bool) {
	path := a.router.Path()
	p := strings.TrimPrefix(path, "/scripts/")
	if p == path {
		return Script{}, false
	}
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return a.store.Script(p)
}

func (a *App) onHistory() bool {
	return strings.HasSuffix(a.router.Path(), "/history")
}

func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()
	a.folderCountsCache = a.buildFolderCounts()
	a.lastRunCache = a.buildLastRuns()
	ui.Column(c).Fill().Padding(10).Gap(8).Background(t.Background).Children(func() {
		ui.SearchField(c, &a.query).Placeholder(tr("搜索名称、入口、文件夹或日志"))
		ui.Segmented(c, &a.filter, tr("全部"), tr("最近")).FillEqually()
		ui.Scroll(c).Grow(1).Children(func() {
			// The tree itself takes drops: a row takes its own, and what is
			// left of the column means the root of the library.
			tree := ui.Column(c).Gap(1).FillHeight()
			a.dropTarget(c, tree, "", false)
			tree.Children(func() {
				if a.query != "" || a.filter == 1 {
					a.flatList(c)
					return
				}
				a.folderTree(c, "", 0)
			})
		})
		ui.Row(c).Gap(6).Children(func() {
			if ui.Button(c, tr("新建脚本")).Grow(1).Padding(3, 8).Clicked() {
				a.openNewScript("")
			}
			if ui.Button(c, tr("新建文件夹")).Grow(1).Padding(3, 8).Clicked() {
				a.openNewFolder("")
			}
		})
		ui.Row(c).Gap(6).Children(func() {
			if ui.Button(c, tr("导入目录…")).Grow(1).Padding(3, 8).Clicked() {
				a.importDirectory()
			}
			if ui.Button(c, tr("导入文件…")).Grow(1).Padding(3, 8).Clicked() {
				a.importFile()
			}
		})
		ui.Divider(c)
		a.navRow(c, "/interpreters", lucide.Terminal(), "解释器")
		a.navRow(c, "/runs", lucide.History(), "运行记录")
		a.navRow(c, "/connections", lucide.Server(), "连接")
		a.navRow(c, "/sync", lucide.RefreshCw(), "同步")
		a.navRow(c, "/settings", lucide.Settings(), "设置")
	})
}

func (a *App) navRow(c *ui.Context, path string, icon *ui.SVG, label string) {
	t := c.Theme()
	active := strings.HasPrefix(a.router.Path(), path)
	bg := t.Background
	text := t.Text
	if active {
		bg = t.Surface
	}
	ui.Row(c).Height(32).PaddingX(8).Radius(6).Gap(8).Background(bg).
		AlignItems(ui.Center).OnClick(func() { a.router.Push(path) }).Children(func() {
		ui.Icon(c, icon).Width(15).Height(15).TextColor(text)
		ui.Text(c, tr(label)).FontSize(13).TextColor(text)
	})
}

// dragItem is what a sidebar row carries while it is dragged. Folder and
// script ids never collide, but the kind makes the drop rules explicit.
type dragItem struct {
	kind string // "script" or "folder"
	id   string
}

// dropTarget makes an element take dragged sidebar rows and files dragged in
// from the file manager. A row highlights while a row hovers it.
func (a *App) dropTarget(c *ui.Context, e ui.Element, folderID string, highlight bool) {
	if _, over := ui.DragOver[dragItem](e); over && highlight {
		e.Border(1, c.Theme().Accent)
	}
	if item, ok := ui.Drop[dragItem](e); ok {
		a.dropOnFolder(folderID, item)
	}
	if files := e.DroppedFiles(); len(files) > 0 {
		a.dropFiles(folderID, files)
	}
}

// dropOnFolder moves what was dropped into a folder, "" for the root.
func (a *App) dropOnFolder(folderID string, item dragItem) {
	switch item.kind {
	case "script":
		moved, err := a.store.MoveScript(item.id, folderID)
		if err != nil {
			a.alertError("无法移动", err.Error())
			return
		}
		a.folderOpen[folderID] = true
		a.forgetScript(item.id)
		a.showNotice(trf("已移动「%s」", moved.Name))
	case "folder":
		moved, err := a.store.MoveFolder(item.id, folderID)
		if err != nil {
			a.alertError("无法移动", err.Error())
			return
		}
		a.folderOpen[folderID] = true
		a.showNotice(trf("已移动「%s」", moved.Name))
	}
}

// dropFiles imports paths dragged in from the file manager: a folder becomes a
// script, a file becomes a single-file script.
func (a *App) dropFiles(folderID string, paths []string) {
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		a.startImport(info.IsDir(), p, folderID, defaultImportName(p, info.IsDir()))
	}
}

func (a *App) folderTree(c *ui.Context, parent string, depth int) {
	folders := a.store.Folders()
	sort.SliceStable(folders, func(i, j int) bool { return folders[i].Position < folders[j].Position })
	scripts := a.store.Scripts()
	sort.SliceStable(scripts, func(i, j int) bool { return scripts[i].Position < scripts[j].Position })
	found := false
	for _, f := range folders {
		if f.ParentID != parent {
			continue
		}
		found = true
		a.folderRow(c, f, depth)
		if a.folderOpen[f.ID] {
			a.folderTree(c, f.ID, depth+1)
		}
	}
	for _, sc := range scripts {
		if sc.FolderID != parent {
			continue
		}
		found = true
		a.scriptRow(c, sc, depth)
	}
	if !found && depth == 0 {
		a.emptySidebar(c)
	}
}

func (a *App) folderRow(c *ui.Context, f Folder, depth int) {
	t := c.Theme()
	count := a.folderCount(f.ID)
	row := ui.Row(c).Height(30).PaddingX(6).Radius(6).Gap(6).MarginStart(float32(depth) * 14).
		AlignItems(ui.Center).Drag(dragItem{kind: "folder", id: f.ID}).
		OnClick(func() { a.folderOpen[f.ID] = !a.folderOpen[f.ID] }).
		ContextMenu(func(m *ui.Menu) {
			if m.Item(tr("新建脚本…")).Chosen() {
				a.sideSel = f.ID
				a.openNewScript(f.ID)
			}
			if m.Item(tr("新建子文件夹…")).Chosen() {
				a.openNewFolder(f.ID)
			}
			m.Separator()
			if m.Item(tr("导入目录…")).Chosen() {
				a.pickImport(true, f.ID, false)
			}
			if m.Item(tr("导入文件…")).Chosen() {
				a.pickImport(false, f.ID, false)
			}
			m.Separator()
			if m.Item(tr("重命名…")).Chosen() {
				a.openRename(dialogFolder, f.ID, f.Name, "重命名文件夹")
			}
			if m.Item(tr("删除文件夹…")).Chosen() {
				a.confirmAction("删除文件夹", "「"+f.Name+"」及其中的脚本会整树移入回收站。",
					[]string{"取消", "删除"}, func(i int) {
						if i != 1 {
							return
						}
						if err := a.store.DeleteFolder(f.ID); err != nil {
							a.alertError("删除失败", err.Error())
							return
						}
						a.showNotice("文件夹已移入回收站")
					})
			}
		})
	a.dropTarget(c, row, f.ID, true)
	row.Children(func() {
		if a.folderOpen[f.ID] {
			ui.Icon(c, lucide.ChevronDown()).Width(12).Height(12).TextColor(t.TextMuted)
		} else {
			ui.Icon(c, lucide.ChevronRight()).Width(12).Height(12).TextColor(t.TextMuted)
		}
		ui.Icon(c, lucide.Folder()).Width(14).Height(14).TextColor(t.TextMuted)
		ui.Text(c, f.Name).FontSize(12.5).TextColor(t.Text).SingleLine().Grow(1)
		ui.Text(c, fmt.Sprint(count)).FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *App) scriptRow(c *ui.Context, sc Script, depth int) {
	t := c.Theme()
	active := a.selected == sc.ID
	bg := t.Background
	if active {
		bg = t.Surface
	}
	row := ui.Row(c).Height(30).PaddingX(6).Radius(6).Gap(6).MarginStart(float32(depth) * 14).
		Background(bg).AlignItems(ui.Center).Drag(dragItem{kind: "script", id: sc.ID}).
		OnClick(func() {
			a.selected = sc.ID
			a.router.Push("/scripts/" + sc.ID)
		}).
		ContextMenu(func(m *ui.Menu) {
			if m.Item(tr("运行")).Chosen() {
				a.runScript(sc)
			}
			if m.Item(tr("停止")).Chosen() {
				a.stopScript(sc)
			}
			m.Separator()
			if m.Item(tr("重命名…")).Chosen() {
				a.openRename(dialogScript, sc.ID, sc.Name, "重命名脚本")
			}
			if m.Item(tr("脚本设置…")).Chosen() {
				a.openScriptOptions(sc)
			}
			m.Separator()
			if m.Item(tr("在访达中显示")).Chosen() {
				mygo.Shell.ShowItemInFolder(a.store.ScriptPath(sc))
			}
			if m.Item(tr("移到回收站…")).Chosen() {
				a.confirmAction("移到回收站", "「"+sc.Name+"」及其全部运行记录会移入库的回收站。",
					[]string{"取消", "移到回收站"}, func(i int) {
						if i != 1 {
							return
						}
						if err := a.store.DeleteScript(sc.ID); err != nil {
							a.alertError("删除失败", err.Error())
							return
						}
						a.forgetScript(sc.ID)
						if a.selected == sc.ID {
							a.selected = ""
							a.router.Replace("/scripts")
						}
					})
			}
		})
	// A script takes drops too: what is dropped on it lands in its folder.
	a.dropTarget(c, row, sc.FolderID, false)
	row.Children(func() {
		ui.Icon(c, iconFor(sc.Language)).Width(14).Height(14).TextColor(t.TextMuted)
		ui.Text(c, sc.Name).FontSize(12.5).TextColor(t.Text).SingleLine().Grow(1)
		if sc.Dangerous {
			ui.Icon(c, lucide.TriangleAlert()).Width(12).Height(12).TextColor(t.Warning).
				Tooltip("高危脚本，运行前会二次确认")
		}
		if run, ok := a.runner.Active(sc.ID); ok {
			_ = run
			ui.Text(c, tr("运行中")).FontSize(10.5).TextColor(t.Accent)
			ui.Spinner(c).Width(10).Height(10)
			return
		}
		if run, ok := a.lastRun(sc.ID); ok {
			ui.Text(c, fmtAgo(run.StartedAt)).FontSize(10.5).TextColor(t.TextMuted).SingleLine()
			ui.Box(c).Width(6).Height(6).Radius(3).Background(statusColor(t, run.Status)).
				Tooltip(runStatusText(run.Status))
			return
		}
		ui.Text(c, tr("未运行")).FontSize(10.5).TextColor(t.TextMuted)
	})
}

func (a *App) emptySidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Padding(10).Gap(8).Children(func() {
		ui.Text(c, tr("还没有脚本")).FontSize(12).TextColor(t.TextMuted)
		if ui.Button(c, tr("新建脚本")).Padding(2, 10).Clicked() {
			a.openNewScript("")
		}
		if ui.Button(c, tr("新建文件夹")).Padding(2, 10).Clicked() {
			a.openNewFolder("")
		}
	})
}

func (a *App) flatList(c *ui.Context) {
	t := c.Theme()
	var list []Script
	for _, sc := range a.store.Scripts() {
		if !a.scriptMatches(sc, a.query) {
			continue
		}
		if a.filter == 1 {
			if run, ok := a.lastRun(sc.ID); !ok || time.Since(run.StartedAt) > 7*24*time.Hour {
				continue
			}
		}
		list = append(list, sc)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	if len(list) == 0 {
		ui.Text(c, tr("没有匹配的脚本")).FontSize(12).TextColor(t.TextMuted).Padding(6)
		return
	}
	for _, sc := range list {
		a.scriptRow(c, sc, 0)
	}
}

func (a *App) folderCount(id string) int {
	if a.folderCountsCache != nil {
		return a.folderCountsCache[id]
	}
	return a.buildFolderCounts()[id]
}

// buildFolderCounts counts every folder once per frame instead of walking the
// tree for each row.
func (a *App) buildFolderCounts() map[string]int {
	counts := map[string]int{}
	for _, sc := range a.store.Scripts() {
		counts[sc.FolderID]++
	}
	folders := a.store.Folders()
	byParent := map[string][]string{}
	parentOf := map[string]string{}
	for _, f := range folders {
		byParent[f.ParentID] = append(byParent[f.ParentID], f.ID)
		parentOf[f.ID] = f.ParentID
	}
	var walk func(id string) int
	walk = func(id string) int {
		total := counts[id]
		for _, child := range byParent[id] {
			total += walk(child)
		}
		counts[id] = total
		return total
	}
	for _, f := range folders {
		if parentOf[f.ID] == "" {
			walk(f.ID)
		}
	}
	return counts
}

func (a *App) welcomePage(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(14).Children(func() {
		ui.Icon(c, lucide.SquareTerminal()).Width(44).Height(44).TextColor(t.TextMuted)
		ui.Text(c, tr("脚本库是空的")).FontSize(18).FontWeight(600)
		ui.Text(c, tr("可以先新建脚本，解释器在运行时按目标自动选择；也可以先检测本机解释器。")).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(10).Children(func() {
			if ui.PrimaryButton(c, tr("新建脚本")).Clicked() {
				a.openNewScript("")
			}
			if a.detecting.Load() {
				ui.Spinner(c).Width(14).Height(14)
				ui.Text(c, tr("正在检测本机解释器…")).FontSize(12).TextColor(t.TextMuted)
			} else if ui.Button(c, tr("检测本机解释器")).Clicked() {
				a.detectInterpreters()
			}
			if ui.Button(c, tr("打开库目录")).Clicked() {
				mygo.Shell.OpenPath(a.store.Root())
			}
		})
	})
}

// toastBar is a floating pill over the main pane, so it never takes layout
// space and never covers the sidebar.
func (a *App) toastBar(c *ui.Context, text string, bottom float32) {
	t := c.Theme()
	// The pill is the inverse of the page: text colour behind, background
	// colour for the text, which holds in both light and dark themes.
	ui.Row(c).Absolute().Left(a.sidebarW+16).Bottom(bottom).Height(30).Padding(0, 14).
		Radius(8).Background(t.Text).AlignItems(ui.Center).Children(func() {
		ui.Text(c, tr(text)).FontSize(12).TextColor(t.Background)
	})
}

func (a *App) showNotice(text string) {
	text = tr(text)
	token := a.toastToken.Add(1)
	a.toast.Store(&toastState{text: text, token: token})
	time.AfterFunc(2400*time.Millisecond, func() {
		a.requestUpdate()
		if cur := a.toast.Load(); cur != nil && cur.token == token {
			a.toast.Store(&toastState{})
		}
	})
	a.requestUpdate()
}

// applyLanguage follows the user's choice, or the system when none is made,
// and tells the mygo controls which languages the app ships.
func (a *App) applyLanguage() {
	if mygo.App.IsReady() {
		mygo.App.SetLanguages("zh-Hans", "en")
		switch a.settings.Language {
		case "en":
			mygo.App.SetLocale("en")
		case "zh":
			mygo.App.SetLocale("zh-Hans")
		default:
			mygo.App.SetLocale("")
		}
		setUILanguage(mygo.App.Locale())
		return
	}
	setUILanguage(a.settings.Language)
}

func (a *App) requestUpdate() {
	if a.win == nil {
		return
	}
	a.win.Update(func() {})
}

// updateUI runs fn on the main thread, or straight away when there is no window
// yet, which is the case in tests.
func (a *App) updateUI(fn func()) {
	if a.win == nil {
		a.uiMu.Lock()
		defer a.uiMu.Unlock()
		fn()
		return
	}
	a.win.Update(func() {
		a.uiMu.Lock()
		defer a.uiMu.Unlock()
		fn()
	})
}

func (a *App) afterRun(run Run) {
	if a.win != nil {
		a.win.Update(func() { a.notifyRun(run) })
		return
	}
	a.notifyRun(run)
}

func (a *App) notifyRun(run Run) {
	if !a.settings.Notify {
		return
	}
	title := "运行结束"
	body := trf("%s · 退出码 %d", runStatusText(run.Status), run.ExitCode)
	if sc, ok := a.store.Script(run.ScriptID); ok {
		title = sc.Name + " " + runStatusText(run.Status)
	}
	if n := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body}); n != nil {
		_ = n.Show()
	}
}

func (a *App) shortcuts(c *ui.Context) {
	c.OnWindowShortcut(ui.Cmd, ui.KeyS, func() {
		if sc, ok := a.currentScript(); ok {
			a.saveEditor(sc)
		}
	})
	c.OnWindowShortcut(ui.Cmd, ui.KeyR, func() {
		if sc, ok := a.currentScript(); ok {
			a.runScript(sc)
		}
	})
	c.OnWindowShortcut(ui.Cmd, ui.KeyN, func() { a.openNewScript("") })
}

// saveEditor saves the file the user is looking at.
func (a *App) saveEditor(sc Script) {
	rel := a.curFile(sc)
	st, ok := a.editors[editorKey(sc.ID, rel)]
	if !ok || !st.Buffer.Modified() {
		return
	}
	if err := a.writeEditorFile(sc, rel, st.Buffer.String()); err != nil {
		a.alertError("保存失败", err.Error())
		return
	}
	st.Buffer.MarkSaved()
	a.showNotice("已保存")
}

// saveAllFiles saves every modified file of a script before a run, so the run
// always uses what is on screen.
func (a *App) saveAllFiles(sc Script) error {
	saved := false
	for _, rel := range a.scriptTabs(sc) {
		st, ok := a.editors[editorKey(sc.ID, rel)]
		if !ok || !st.Buffer.Modified() {
			continue
		}
		if err := a.writeEditorFile(sc, rel, st.Buffer.String()); err != nil {
			return fmt.Errorf("%s 保存失败: %w", rel, err)
		}
		st.Buffer.MarkSaved()
		saved = true
	}
	if !saved {
		return nil
	}
	// One snapshot for the whole save, so a run records one version.
	if _, err := a.store.AddRevision(sc.ID); err != nil && !errors.Is(err, ErrSnapshotTooLarge) {
		return err
	}
	return nil
}

// forgetScript drops the editors and tabs of a deleted script.
func (a *App) forgetScript(id string) {
	prefix := id + "\x00"
	for key := range a.editors {
		if strings.HasPrefix(key, prefix) {
			delete(a.editors, key)
			delete(a.encodings, key)
		}
	}
	delete(a.files, id)
	delete(a.tabs, id)
	a.invalidateScriptCache(id)
}

func (a *App) runScript(sc Script) {
	if _, ok := a.runner.Active(sc.ID); ok {
		return
	}
	if err := a.saveAllFiles(sc); err != nil {
		a.alertError("无法运行", err.Error())
		return
	}
	fresh, ok := a.store.Script(sc.ID)
	if !ok {
		return
	}
	sc = fresh
	if sc.Dangerous {
		a.confirmAction("高危脚本",
			"「"+sc.Name+"」被标记为高危脚本。它可能修改系统或删除数据，确认要运行吗？",
			[]string{"取消", "仍然运行"}, func(i int) {
				if i == 1 {
					a.startScriptRun(sc)
				}
			})
		return
	}
	a.startScriptRun(sc)
}

// startScriptRun runs the script after the danger confirmation, when there is
// one.
func (a *App) startScriptRun(sc Script) {
	sc = applyRunProfile(sc)
	if sc.HostID == "" && (sc.HostName != "" || sc.HostTarget != "") {
		// The script came from another machine, where the host had an id this
		// machine does not know. Find it by target or name, or refuse rather
		// than silently running locally.
		if h, ok := a.findHost(sc); ok {
			a.rebindHost(sc, h)
			sc.HostID = h.ID
			a.runOnRemote(sc)
			return
		}
		a.alertError("无法运行", "这个脚本的运行目标「"+sc.HostName+"」还没有绑定到本机的连接，请在页头的「运行目标」里选择。")
		return
	}
	if sc.HostID != "" {
		a.runOnRemote(sc)
		return
	}
	release, err := a.reserveRun()
	if err != nil {
		a.alertError("无法运行", err.Error())
		return
	}
	defer release()
	if sc.InterpID != "" {
		if configured, ok := a.store.Interpreter(sc.InterpID); ok && configured.State != StateOK {
			a.alertError("无法运行", "解释器「"+configured.Name+"」当前不可用："+stateText(configured.State))
			return
		}
	}
	it, ok := a.resolvedLocalInterpreter(sc)
	if !ok {
		a.alertError("无法运行", "本机还没有可用的 "+languageName(sc.Language)+" 解释器，先到解释器页检测本机解释器。")
		return
	}
	if sc.InterpID == "" {
		// Remember the interpreter this run used, so the header always agrees
		// with what actually ran. This saves the script as stored, before the
		// environment is resolved: writing the resolved one back would put the
		// credentials into script.json, and from there into the synced history.
		sc.InterpID = it.ID
		_ = a.store.UpdateScript(sc)
	}
	if sc, ok = a.resolveRunEnv(sc); !ok {
		return
	}
	if sc.Preflight {
		if err := a.preflightLocal(sc, it); err != nil {
			a.alertError("无法运行", err.Error())
			return
		}
	}
	timeout := a.settings.DefaultTimeout
	if sc.Timeout > 0 {
		timeout = sc.Timeout
	}
	run, err := a.runner.Start(sc, it, time.Duration(timeout)*time.Second)
	if err != nil {
		a.alertError("启动失败", err.Error())
		return
	}
	a.logRunID = run.ID
	a.logScript = sc.ID
	a.logLines = nil
	a.logVersion++
	if a.settings.OpenPanel && a.runCollapsed {
		a.runCollapsed = false
	}
	a.showNotice("开始运行")
}

// runOnRemote makes sure the host has an interpreter for the entry language.
// The first run probes the server and stores the result; later runs use the
// cache and do not pay another ssh round trip.
func (a *App) runOnRemote(sc Script) {
	host, ok := a.findHost(sc)
	if !ok {
		a.alertError("无法运行", "这个脚本配置的远程连接已不存在，请在页头的「运行目标」里重新选择。")
		return
	}
	a.rebindHost(sc, host)
	if host.Status == HostStateNotLinux {
		a.alertError("无法在远程运行", "远程执行只支持 Linux 服务器。")
		return
	}
	if ri, ok := a.remotes.Interpreter(host, sc.Language); ok {
		a.startRemoteRun(sc, host, ri)
		return
	}
	if !a.remoteBusy.CompareAndSwap(false, true) {
		a.showNotice("正在检测远程解释器，请稍候")
		return
	}
	a.showNotice("正在检测 " + host.Name + " 上的 " + languageName(sc.Language) + " 解释器…")
	go func() {
		updated, list, err := ProbeRemote(host)
		_ = a.remotes.UpdateHost(updated)
		if err == nil {
			_ = a.remotes.SetInterpreters(host.ID, list)
		}
		a.updateUI(func() {
			a.remoteBusy.Store(false)
			a.requestUpdate()
			if err != nil {
				a.alertError("无法在远程运行", err.Error())
				return
			}
			ri, ok := a.remotes.Interpreter(updated, sc.Language)
			if !ok {
				a.alertError("无法在远程运行",
					"远程没有可用的 "+languageName(sc.Language)+" 解释器，可以先在服务器上安装，再到连接页重新检测。")
				return
			}
			a.startRemoteRun(sc, updated, ri)
		})
	}()
}

func (a *App) startRemoteRun(sc Script, host RemoteHost, ri RemoteInterp) {
	sc = applyRunProfile(sc)
	if _, active := a.runner.Active(sc.ID); active {
		return
	}
	resolved, ok := a.resolveRunEnv(sc)
	if !ok {
		return
	}
	sc = resolved
	release, err := a.reserveRun()
	if err != nil {
		a.alertError("无法运行", err.Error())
		return
	}
	timeout := a.settings.DefaultTimeout
	if sc.Timeout > 0 {
		timeout = sc.Timeout
	}
	// The syntax check and the revision hash both touch the network or the
	// whole directory; on the UI thread they would freeze the window.
	go func() {
		defer release()
		if sc.Preflight {
			if err := a.preflightRemote(sc, host, ri); err != nil {
				a.updateUI(func() { a.alertError("无法运行", err.Error()) })
				return
			}
		}
		run, err := a.runner.StartRemote(sc, host, ri, time.Duration(timeout)*time.Second)
		a.updateUI(func() {
			if err != nil {
				a.alertError("启动失败", err.Error())
				return
			}
			a.logRunID = run.ID
			a.logScript = sc.ID
			a.logLines = nil
			a.logVersion++
			if a.settings.OpenPanel && a.runCollapsed {
				a.runCollapsed = false
			}
			a.showNotice("已在 " + host.Name + " 上开始运行")
		})
	}()
}

// resolveRunEnv turns ${secret:name} references into the stored credentials
// just before a run. A missing secret stops the run instead of letting a blank
// value through, and the resolved values never touch script.json.
func (a *App) resolveRunEnv(sc Script) (Script, bool) {
	env, err := resolveEnv(sc.Env, a.secrets)
	if err != nil {
		a.alertError("无法运行", err.Error())
		return sc, false
	}
	sc.Env = env
	return sc, true
}

func (a *App) stopScript(sc Script) {
	run, ok := a.runner.Active(sc.ID)
	if !ok {
		return
	}
	if err := a.runner.Stop(run.ID); err != nil {
		a.alertError("停止失败", err.Error())
		return
	}
	if run.Admin {
		a.showNotice("已断开查看：被提权的子进程可能仍在运行")
	}
}

func (a *App) detectInterpreters() {
	if a.detecting.Load() {
		return
	}
	a.detecting.Store(true)
	a.requestUpdate()
	go func() {
		found := DetectInterpreters(a.store.Interpreters())
		added := 0
		for _, it := range found {
			if _, err := a.store.CreateInterpreter(it); err == nil {
				added++
			}
		}
		for _, it := range a.store.Interpreters() {
			Probe(&it)
			_ = a.store.UpdateInterpreter(it)
		}
		msg := "没有发现新的解释器"
		if added > 0 {
			msg = trf("已加入 %d 个解释器", added)
		}
		if a.win != nil {
			a.win.Update(func() {
				a.detecting.Store(false)
				a.showNotice(msg)
			})
			return
		}
		a.detecting.Store(false)
		a.showNotice(msg)
	}()
}

func editorKey(scriptID, rel string) string { return scriptID + "\x00" + rel }

// curFile is the file the script page is showing.
func (a *App) curFile(sc Script) string {
	if rel := a.files[sc.ID]; rel != "" {
		return rel
	}
	return sc.Entry
}

// scriptTabs lists the open files of a script, the entry first.
func (a *App) scriptTabs(sc Script) []string {
	tabs := a.tabs[sc.ID]
	if len(tabs) == 0 {
		return []string{sc.Entry}
	}
	seen := false
	for _, rel := range tabs {
		if rel == sc.Entry {
			seen = true
		}
	}
	if !seen {
		tabs = append([]string{sc.Entry}, tabs...)
	}
	return tabs
}

// fileEditor returns the editor state of one file of a script, reading it on
// first use.
func (a *App) fileEditor(sc Script, rel string) *editor.State {
	key := editorKey(sc.ID, rel)
	if st, ok := a.editors[key]; ok {
		return st
	}
	data, err := a.store.ReadScriptFileBytes(sc, rel)
	if err != nil {
		data = nil
	}
	content, enc := decodeEditorText(data)
	a.encodings[key] = enc
	buf := editor.NewBuffer(content)
	buf.SetLanguage(editor.LanguageFor(rel))
	st := &editor.State{Buffer: buf}
	a.editors[key] = st
	return st
}

// openScriptFile switches the editor to a file and remembers the tab.
func (a *App) openScriptFile(sc Script, rel string) {
	a.files[sc.ID] = rel
	tabs := a.tabs[sc.ID]
	for _, open := range tabs {
		if open == rel {
			a.tabs[sc.ID] = tabs
			return
		}
	}
	a.tabs[sc.ID] = append(tabs, rel)
}

// closeScriptFile closes one tab, keeping the entry open.
func (a *App) closeScriptFile(sc Script, rel string) {
	if st, ok := a.editors[editorKey(sc.ID, rel)]; ok && st.Buffer.Modified() {
		a.confirmAction("这个文件还有未保存的修改",
			"「"+rel+"」关闭后会丢失未保存的修改。",
			[]string{"取消", "保存并关闭", "放弃修改"}, func(i int) {
				switch i {
				case 1:
					if err := a.writeEditorFile(sc, rel, st.Buffer.String()); err != nil {
						a.alertError("保存失败", err.Error())
						return
					}
				case 2:
				default:
					return
				}
				a.dropScriptTab(sc, rel)
			})
		return
	}
	a.dropScriptTab(sc, rel)
}

// dropScriptTab removes one tab and its cached editor without asking.
func (a *App) dropScriptTab(sc Script, rel string) {
	var keep []string
	for _, open := range a.tabs[sc.ID] {
		if open != rel {
			keep = append(keep, open)
		}
	}
	a.tabs[sc.ID] = keep
	delete(a.editors, editorKey(sc.ID, rel))
	delete(a.encodings, editorKey(sc.ID, rel))
	if a.curFile(sc) == rel {
		delete(a.files, sc.ID)
	}
}

// editor keeps the old single-file call site working.
func (a *App) editor(sc Script) *editor.State {
	return a.fileEditor(sc, a.curFile(sc))
}

// refreshEditors rereads open files from disk, after a restore.
func (a *App) refreshEditors(sc Script) {
	for _, rel := range a.scriptTabs(sc) {
		key := editorKey(sc.ID, rel)
		delete(a.editors, key)
		delete(a.encodings, key)
	}
}

// writeEditorFile saves text with the file's original charset and line
// endings.
func (a *App) writeEditorFile(sc Script, rel, text string) error {
	key := editorKey(sc.ID, rel)
	enc := a.encodings[key]
	data, stored := encodeEditorText(text, enc)
	if _, err := a.store.WriteScriptFile(sc.ID, rel, string(data), true); err != nil {
		return err
	}
	if a.encodings != nil {
		a.encodings[key] = stored
	}
	delete(a.editableCache, key)
	return nil
}

func (a *App) lastRun(scriptID string) (Run, bool) {
	if a.lastRunCache != nil {
		run, ok := a.lastRunCache[scriptID]
		return run, ok
	}
	run, ok := a.buildLastRuns()[scriptID]
	return run, ok
}

// buildLastRuns indexes the newest run of each script once per frame; Runs()
// is already sorted newest first.
func (a *App) buildLastRuns() map[string]Run {
	out := map[string]Run{}
	for _, run := range a.store.Runs() {
		if _, seen := out[run.ScriptID]; !seen {
			out[run.ScriptID] = run
		}
	}
	return out
}

func (a *App) interpreterOptions(language string) ([]string, []string) {
	var labels, ids []string
	for _, it := range a.interpretersFor(language) {
		labels = append(labels, it.Name+" "+it.Version)
		ids = append(ids, it.ID)
	}
	return labels, ids
}

// maxConcurrent is the configured limit, or the suggestion when it is 0.
func (a *App) maxConcurrent() int {
	if a.settings.Concurrency > 0 {
		return a.settings.Concurrency
	}
	return suggestedConcurrency()
}

// reserveRun takes a place in the concurrency budget and returns the release
// for it. Callers defer release: a run that fails before starting must give
// the place back, or a few failed attempts would block every later run.
func (a *App) reserveRun() (func(), error) {
	limit := a.maxConcurrent()
	if !a.runner.Reserve(limit) {
		return nil, fmt.Errorf("已达到并发上限 %d，先停止一个运行，或到设置里调整并发数", limit)
	}
	return a.runner.ReleaseReserve, nil
}

func (a *App) alertError(title, message string) {
	a.confirm = confirmState{open: true, title: tr(title), message: tr(message),
		buttons: []string{tr("好")}, action: func(int) {}}
	a.requestUpdate()
}

func (a *App) confirmAction(title, message string, buttons []string, action func(int)) {
	translated := make([]string, len(buttons))
	for i, b := range buttons {
		translated[i] = tr(b)
	}
	a.confirm = confirmState{open: true, title: tr(title), message: tr(message), buttons: translated, action: action}
	a.requestUpdate()
}

func stateText(state string) string {
	switch state {
	case StateOK:
		return tr("可用")
	case StateMissing:
		return tr("文件不存在")
	case StateNoExec:
		return tr("没有执行权限")
	case StateProbeFailed:
		return tr("版本检测失败")
	}
	return state
}

func runStatusText(status string) string {
	switch status {
	case RunRunning:
		return tr("运行中")
	case RunOK:
		return tr("成功")
	case RunFailed:
		return tr("失败")
	case RunCancelled:
		return tr("已停止")
	case RunTimeout:
		return tr("超时")
	case RunAborted:
		return tr("未启动")
	case RunUnknown:
		return tr("状态未知")
	}
	return status
}

func statusColor(t *ui.Theme, status string) ui.Color {
	switch status {
	case RunRunning:
		return t.Accent
	case RunOK:
		return t.Success
	case RunCancelled:
		return t.TextMuted
	case RunTimeout:
		return t.Warning
	case RunAborted, RunUnknown:
		return t.TextMuted
	}
	return t.Danger
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02 15:04:05")
}

func fmtAgo(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return tr("刚刚")
	case d < time.Hour:
		return fmt.Sprintf(tr("%d 分钟前"), int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf(tr("%d 小时前"), int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf(tr("%d 天前"), int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

func fmtDur(run Run) string {
	end := run.EndedAt
	if run.Status == RunRunning || end.IsZero() {
		end = time.Now()
	}
	d := end.Sub(run.StartedAt)
	if d < time.Second {
		return trf("%d 毫秒", d.Milliseconds())
	}
	return trf("%.1f 秒", d.Seconds())
}

func iconFor(language string) *ui.SVG {
	switch language {
	case "shell":
		return lucide.Terminal()
	case "go":
		return lucide.Box()
	case "lua":
		return lucide.Moon()
	case "ruby":
		return lucide.Gem()
	case "python":
		return lucide.FileCode()
	}
	return lucide.FileCode()
}

func iconForPath(p string) *ui.SVG {
	_ = p
	return lucide.FileCode()
}

func ensureDir(p string) error {
	return os.MkdirAll(p, 0o755)
}

func baseName(p string) string {
	return filepath.Base(p)
}

// invalidateScriptCache forgets the per-file facts of one script (whether a
// file opens in the editor, which links it has).
func (a *App) invalidateScriptCache(scriptID string) {
	prefix := scriptID + "\x00"
	for key := range a.editableCache {
		if strings.HasPrefix(key, prefix) {
			delete(a.editableCache, key)
		}
	}
	delete(a.symlinkCache, scriptID)
}

// editable reports whether a file opens in the editor, asking the disk once.
func (a *App) editable(sc Script, rel string) bool {
	key := editorKey(sc.ID, rel)
	if v, ok := a.editableCache[key]; ok {
		return v
	}
	v := a.store.FileEditable(sc, rel)
	if a.editableCache == nil {
		a.editableCache = map[string]bool{}
	}
	a.editableCache[key] = v
	return v
}

// scriptSymlinks lists a script's links, walking the directory once.
func (a *App) scriptSymlinks(scriptID string) []string {
	if v, ok := a.symlinkCache[scriptID]; ok {
		return v
	}
	v := a.store.ScriptSymlinks(scriptID)
	if a.symlinkCache == nil {
		a.symlinkCache = map[string][]string{}
	}
	a.symlinkCache[scriptID] = v
	return v
}

// saveAllScripts saves every modified buffer of every script. It is used
// before anything that rereads the library, so edits are never dropped.
func (a *App) saveAllScripts() error {
	for key, st := range a.editors {
		if !st.Buffer.Modified() {
			continue
		}
		idx := strings.IndexByte(key, 0)
		if idx < 0 {
			continue
		}
		scriptID, rel := key[:idx], key[idx+1:]
		sc, ok := a.store.Script(scriptID)
		if !ok {
			continue
		}
		if err := a.writeEditorFile(sc, rel, st.Buffer.String()); err != nil {
			return fmt.Errorf("%s 保存失败: %w", rel, err)
		}
		st.Buffer.MarkSaved()
	}
	return nil
}

// forgetAllEditors drops every cached editor, tab and encoding. It is used
// when the library itself changes.
func (a *App) forgetAllEditors() {
	for key := range a.editors {
		delete(a.editors, key)
	}
	a.files = map[string]string{}
	a.tabs = map[string][]string{}
	a.encodings = map[string]textEncoding{}
	a.logSearch = map[string]string{}
	a.editableCache = map[string]bool{}
	a.symlinkCache = map[string][]string{}
}
