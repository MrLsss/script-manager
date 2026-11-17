package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.WhenReady(func() {
		useAppDataDir()
		settings, err := LoadSettings()
		if err != nil {
			mygo.Dialog.Error("读取设置失败", err.Error())
			mygo.App.Quit()
			return
		}
		store, err := OpenStore(settings.Library)
		if err != nil {
			mygo.Dialog.Error("打开脚本库失败", err.Error())
			mygo.App.Quit()
			return
		}
		a := newApp(settings, store)
		if n := len(store.Problems()); n > 0 {
			mygo.Dialog.Error("脚本库有 %d 项需要注意", strings.Join(store.Problems(), "\n"))
		}
		a.applyLanguage()
		a.runner.Reconcile()
		a.notifySupported = mygo.NotificationsSupported()
		a.applyAppearance()

		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "script-manager",
			Width:     1180,
			Height:    760,
			MinWidth:  960,
			MinHeight: 620,
			StateKey:  "script-manager",
			Content:   ui.View(a.view),
		})
		a.win = win
		a.setupMenu()
		a.setupTray()
		mygo.App.OnLocaleChanged(func(string) {
			setUILanguage(mygo.App.Locale())
			a.setupMenu()
			a.requestUpdate()
		})
		if a.settings.CheckUpdates {
			a.checkForUpdates(false)
		}
		go func() {
			state, _ := a.sync.CheckRemote()
			snaps := a.sync.Snapshots()
			a.updateUI(func() {
				a.changeSyncData(func(d *syncPanelState) {
					d.state = state
					d.loaded = true
					d.snapshots = snaps
					d.notice = NoticeText(state)
				})
			})
		}()
		// Closing the window keeps the app in the menu bar; running scripts
		// continue and the tray icon brings the window back.
		win.OnClose(func(e *mygo.CloseEvent) {
			// Hiding without a tray icon leaves the app running with no way
			// back, so only hide when the icon is there.
			if a.quitting || a.tray == nil {
				return
			}
			e.PreventDefault()
			win.Hide()
		})
		win.OnClosed(func() {
			a.runner.StopAll()
			a.settings.Save()
		})
	})
	mygo.App.Run()
}

// useAppDataDir keeps the preferences under script-manager instead of the
// localized application name mygo derives from the bundle.
func useAppDataDir() {
	if os.Getenv("SCRIPT_MANAGER_HOME") != "" {
		return
	}
	appData, err := mygo.App.Path(mygo.PathAppData)
	if err != nil {
		return
	}
	mygo.App.SetPath(mygo.PathUserData, filepath.Join(appData, "script-manager"))
}

func (a *App) setupMenu() {
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: tr("文件"), Submenu: []*mygo.MenuItem{
			{Label: tr("新建脚本…"), Accelerator: "CmdOrCtrl+N", Click: func(*mygo.MenuItem, *mygo.Window) {
				a.openNewScript("")
			}},
			{Label: tr("新建文件夹…"), Accelerator: "CmdOrCtrl+Shift+N", Click: func(*mygo.MenuItem, *mygo.Window) {
				a.openNewFolder("")
			}},
			mygo.Separator(),
			{Label: tr("导入目录…"), Click: func(*mygo.MenuItem, *mygo.Window) {
				a.importDirectory()
			}},
			{Label: tr("导入文件…"), Click: func(*mygo.MenuItem, *mygo.Window) {
				a.importFile()
			}},
			{Label: tr("导入导出包…"), Click: func(*mygo.MenuItem, *mygo.Window) {
				a.openImportDialog()
			}},
			{Label: tr("导出脚本…"), Click: func(*mygo.MenuItem, *mygo.Window) {
				a.openExportDialog()
			}},
			mygo.Separator(),
			{Label: tr("保存"), Accelerator: "CmdOrCtrl+S", Click: func(*mygo.MenuItem, *mygo.Window) {
				if sc, ok := a.currentScript(); ok {
					a.saveEditor(sc)
				}
			}},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: tr("脚本"), Submenu: []*mygo.MenuItem{
			{Label: tr("运行"), Accelerator: "CmdOrCtrl+R", Click: func(*mygo.MenuItem, *mygo.Window) {
				if sc, ok := a.currentScript(); ok {
					a.runScript(sc)
				}
			}},
			{Label: tr("停止"), Accelerator: "CmdOrCtrl+.", Click: func(*mygo.MenuItem, *mygo.Window) {
				if sc, ok := a.currentScript(); ok {
					a.stopScript(sc)
				}
			}},
		}},
		{Role: mygo.RoleWindowMenu},
	}))
}

func (a *App) setupTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "script-manager",
		Icon:           trayIconPNG(),
		IconIsTemplate: true,
		ToolTip:        "script-manager",
	})
	if err != nil {
		return
	}
	a.tray = tray
	a.setupTrayBehavior()
}
