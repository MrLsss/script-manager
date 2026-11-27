package main

import (
	"runtime"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

// setupTrayBehavior wires the menu bar icon. macOS and Windows keep the left
// click for the main window and build the menu only for a right click; Linux
// panels only show menus, so the icon gets a small static menu there.
func (a *App) setupTrayBehavior() {
	if a.tray == nil {
		return
	}
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.quitting = true })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if a.tray != nil {
			a.tray.Destroy()
		}
	})
	if runtime.GOOS == "linux" {
		a.tray.SetMenu(a.buildTrayMenu(false))
		return
	}
	a.tray.OnClick(func() { a.showMainWindow() })
	a.tray.OnRightClick(func() {
		a.closeTrayPanel()
		a.tray.SetMenu(a.buildTrayMenu(true))
		defer a.tray.SetMenu(nil)
		a.tray.PopUpMenu()
	})
}

// buildTrayMenu keeps the menu short: fixed actions plus the most recent
// scripts. The full list lives in the quick panel, which has search.
func (a *App) buildTrayMenu(withRecent bool) *mygo.Menu {
	items := []*mygo.MenuItem{
		{Label: tr("打开主界面"), Click: func(*mygo.MenuItem, *mygo.Window) { a.showMainWindow() }},
		{Label: tr("快速运行…"), Click: func(*mygo.MenuItem, *mygo.Window) { a.showTrayPanel() }},
	}
	if withRecent {
		recent := a.recentScripts(5)
		if len(recent) > 0 {
			var sub []*mygo.MenuItem
			for _, sc := range recent {
				sc := sc
				label := sc.Name
				if sc.Dangerous {
					label = "⚠ " + label
				}
				sub = append(sub, &mygo.MenuItem{Label: label, Click: func(*mygo.MenuItem, *mygo.Window) {
					a.openScriptInMain(sc.ID)
				}})
			}
			items = append(items, mygo.Separator(), &mygo.MenuItem{Label: tr("最近运行"), Submenu: sub})
		}
	}
	items = append(items,
		mygo.Separator(),
		&mygo.MenuItem{Label: tr("全部脚本…"), Click: func(*mygo.MenuItem, *mygo.Window) { a.showTrayPanel() }},
		mygo.Separator(),
		&mygo.MenuItem{Role: mygo.RoleQuit},
	)
	return mygo.NewMenu(items)
}

// recentScripts lists the scripts of the newest runs, without repeats.
func (a *App) recentScripts(limit int) []Script {
	seen := map[string]bool{}
	var out []Script
	for _, run := range a.store.Runs() {
		if seen[run.ScriptID] {
			continue
		}
		sc, ok := a.store.Script(run.ScriptID)
		if !ok {
			continue
		}
		seen[run.ScriptID] = true
		out = append(out, sc)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (a *App) showMainWindow() {
	a.closeTrayPanel()
	if a.win == nil {
		return
	}
	a.win.Show()
	a.win.Restore()
	a.win.Focus()
}

func (a *App) openScriptInMain(id string) {
	a.showMainWindow()
	if sc, ok := a.store.Script(id); ok {
		a.selected = sc.ID
		a.router.Push("/scripts/" + sc.ID)
	}
}

// showTrayPanel opens the searchable quick-run panel next to the icon.
func (a *App) showTrayPanel() {
	if a.tray == nil {
		return
	}
	if a.trayPanel != nil {
		a.trayPanel.Show()
		a.trayPanel.Focus()
		return
	}
	a.trayPanel = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      a.tray,
		Placement: mygo.PlacementBottom,
		Gap:       6,
		Width:     400,
		Height:    540,
		Focusable: true,
		Shadow:    true,
		Popover:   true,
		Content:   ui.View(a.trayPanelView),
	})
	a.trayPanel.OnClosed(func() {
		a.trayPanel = nil
		a.trayQuery = ""
	})
}

func (a *App) closeTrayPanel() {
	if a.trayPanel != nil {
		a.trayPanel.Close()
		a.trayPanel = nil
	}
	a.trayQuery = ""
}

// trayPanelView is the quick-run panel: search, recent scripts, then every
// script grouped by folder. Running is an explicit button, never a click on
// the row.
func (a *App) trayPanelView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(12).Gap(10).Background(t.Background).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("快速运行")).FontSize(15).FontWeight(600)
			ui.Spacer(c)
			if ui.Button(c, tr("打开主界面")).Padding(1, 8).Clicked() {
				a.showMainWindow()
			}
		})
		ui.SearchField(c, &a.trayQuery).Placeholder(tr("搜索名称、入口、文件夹或日志"))
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				query := strings.TrimSpace(a.trayQuery)
				if query == "" {
					recent := a.recentScripts(5)
					if len(recent) > 0 {
						ui.Text(c, tr("最近运行")).FontSize(11).TextColor(t.TextMuted).Padding(4, 2)
						for _, sc := range recent {
							a.trayScriptRow(c, sc)
						}
						ui.Divider(c)
					}
				}
				groups := a.trayGroupedScripts(query)
				if len(groups) == 0 {
					ui.Text(c, tr("没有匹配的脚本")).FontSize(12).TextColor(t.TextMuted).Padding(8)
					return
				}
				for _, group := range groups {
					ui.Text(c, group.path).FontSize(11).TextColor(t.TextMuted).Padding(4, 2)
					for _, sc := range group.scripts {
						a.trayScriptRow(c, sc)
					}
				}
			})
		})
	})
}

type trayGroup struct {
	path    string
	scripts []Script
}

func (a *App) trayGroupedScripts(query string) []trayGroup {
	byPath := map[string][]Script{}
	for _, sc := range a.store.Scripts() {
		if !a.scriptMatches(sc, query) {
			continue
		}
		path := a.folderPath(sc.FolderID)
		byPath[path] = append(byPath[path], sc)
	}
	var groups []trayGroup
	for path, scripts := range byPath {
		sort.SliceStable(scripts, func(i, j int) bool {
			if scripts[i].Position != scripts[j].Position {
				return scripts[i].Position < scripts[j].Position
			}
			return scripts[i].Name < scripts[j].Name
		})
		groups = append(groups, trayGroup{path: path, scripts: scripts})
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].path < groups[j].path })
	return groups
}

func (a *App) trayScriptRow(c *ui.Context, sc Script) {
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(6, 8).Radius(6).Gap(8).AlignItems(ui.Center).
		Background(t.Surface).Children(func() {
		ui.Icon(c, iconFor(sc.Language)).Width(13).Height(13).TextColor(t.TextMuted)
		ui.Column(c).Gap(1).Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				if sc.Dangerous {
					ui.Icon(c, lucide.TriangleAlert()).Width(12).Height(12).TextColor(t.Warning)
				}
				ui.Text(c, sc.Name).FontSize(12).TextColor(t.Text).SingleLine()
			})
			meta := sc.Entry + " · " + a.scriptTargetLabel(sc)
			ui.Text(c, meta).FontSize(10.5).TextColor(t.TextMuted).SingleLine()
		})
		if ui.Button(c, tr("打开")).Padding(1, 7).Shrink(0).Clicked() {
			a.openScriptInMain(sc.ID)
		}
		if ui.Button(c, tr("运行")).Padding(1, 7).Shrink(0).Clicked() {
			a.trayRun(sc)
		}
	})
}

// trayRun moves to the main window before starting, so the danger
// confirmation, the interpreter probe and the log are all visible.
func (a *App) trayRun(sc Script) {
	a.closeTrayPanel()
	a.showMainWindow()
	a.selected = sc.ID
	a.router.Push("/scripts/" + sc.ID)
	a.runScript(sc)
}

func (a *App) scriptTargetLabel(sc Script) string {
	if sc.HostID == "" {
		return tr("本机")
	}
	if h, ok := a.remotes.Host(sc.HostID); ok {
		return h.Name
	}
	if sc.HostName != "" {
		return sc.HostName + tr("（未绑定）")
	}
	return tr("连接已删除")
}

// scriptMatches is shared by the sidebar search and the quick panel: name,
// entry, folder path and the newest log. Logs are read tail-first and cached
// per run, since they never change once the run has finished.
func (a *App) scriptMatches(sc Script, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	if strings.Contains(strings.ToLower(sc.Name), q) ||
		strings.Contains(strings.ToLower(sc.Entry), q) ||
		tagMatches(sc.Tags, q) ||
		strings.Contains(strings.ToLower(a.folderPath(sc.FolderID)), q) {
		return true
	}
	run, ok := a.lastRun(sc.ID)
	if !ok || run.Status == RunRunning {
		return false
	}
	return strings.Contains(strings.ToLower(a.logSearchText(run)), q)
}

func (a *App) logSearchText(run Run) string {
	if a.logSearch == nil {
		a.logSearch = map[string]string{}
	}
	if text, ok := a.logSearch[run.ID]; ok {
		return text
	}
	text := a.runner.LogTail(run, 512<<10)
	a.logSearch[run.ID] = text
	return text
}
