package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *App) settingsPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Width(640).Padding(24).Gap(18).Children(func() {
			a.settingsSection(c, "脚本库")
			a.settingsField(c, "库目录", func() {
				ui.Text(c, a.store.Root()).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1)
				if ui.Button(c, tr("更换…")).Padding(2, 10).Clicked() {
					a.changeLibrary()
				}
				if ui.Button(c, tr("打开目录")).Padding(2, 10).Clicked() {
					mygo.Shell.OpenPath(a.store.Root())
				}
			})
			if problems := a.store.Problems(); len(problems) > 0 {
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, tr("脚本库有需要留意的条目")).FontSize(12).TextColor(t.Warning)
					for _, problem := range problems {
						ui.Text(c, "· "+problem).FontSize(11).TextColor(t.TextMuted).Wrap()
					}
				})
			}
			a.settingsField(c, "内容", func() {
				ui.Textf(c, tr("%d 个脚本 · %d 个解释器 · %d 条运行记录"),
					len(a.store.Scripts()), len(a.store.Interpreters()), len(a.store.Runs())).
					FontSize(12).TextColor(t.TextMuted)
			})
			a.settingsField(c, "导入导出", func() {
				if ui.Button(c, tr("导出…")).Padding(2, 10).Clicked() {
					a.openExportDialog()
				}
				if ui.Button(c, tr("导入…")).Padding(2, 10).Clicked() {
					a.openImportDialog()
				}
			})

			a.settingsSection(c, "运行")
			a.settingsField(c, "默认超时", func() {
				labels, values := timeoutOptions()
				a.settingsChoice(c, labels, values, a.settings.DefaultTimeout, func(v int) {
					a.settings.DefaultTimeout = v
					a.saveSettings()
				})
			})
			a.settingsField(c, "并发运行", func() {
				labels, values := concurrencyOptions()
				a.settingsChoice(c, labels, values, a.settings.Concurrency, func(v int) {
					a.settings.Concurrency = v
					a.saveSettings()
				})
			})
			a.settingsField(c, "运行结束通知", func() {
				ui.Switch(c, &a.settings.Notify).OnChange(func() { a.saveSettings() })
			})
			a.settingsField(c, "日志脱敏", func() {
				ui.Switch(c, &a.settings.RedactLogs).OnChange(func() {
					a.runner.SetRedaction(a.settings.RedactLogs)
					a.saveSettings()
				})
			})
			a.settingsField(c, "完成后展开日志面板", func() {
				ui.Switch(c, &a.settings.OpenPanel).OnChange(func() { a.saveSettings() })
			})

			a.settingsSection(c, "日志与历史")
			a.settingsField(c, "日志保留", func() {
				labels := []string{trf("%d 天", 7), trf("%d 天", 30), trf("%d 天", 90), trf("%d 天", 180)}
				values := []int{7, 30, 90, 180}
				a.settingsChoice(c, labels, values, a.settings.RetentionDays, func(v int) {
					a.settings.RetentionDays = v
					a.saveSettings()
				})
			})
			a.settingsField(c, "单次日志上限", func() {
				labels := []string{"5 MB", "10 MB", "20 MB", "50 MB", "100 MB"}
				values := []int{5, 10, 20, 50, 100}
				a.settingsChoice(c, labels, values, int(a.settings.MaxLogBytes>>20), func(v int) {
					a.settings.MaxLogBytes = int64(v) << 20
					a.runner.logLimit = a.settings.MaxLogBytes
					a.saveSettings()
				})
			})
			a.settingsField(c, "保存脚本内容修订", func() {
				ui.Switch(c, &a.keepRev).OnChange(func() {
					if a.keepRev {
						a.settings.KeepRevisions = 50
					} else {
						a.settings.KeepRevisions = 0
					}
					a.saveSettings()
				})
			})
			a.settingsField(c, "清理旧日志", func() {
				if ui.Button(c, tr("立即清理")).Padding(2, 10).Clicked() {
					if err := a.store.PruneRuns(a.settings.RetentionDays); err != nil {
						a.alertError("清理失败", err.Error())
						return
					}
					a.showNotice("已清理旧日志")
				}
			})

			a.settingsSection(c, "同步")
			a.settingsField(c, "快照保留", func() {
				labels := []string{trf("最近 %d 份", 10), trf("最近 %d 份", 30), trf("最近 %d 份", 90), tr("不限")}
				values := []int{10, 30, 90, -1}
				a.settingsChoice(c, labels, values, a.settings.SnapshotKeep, func(v int) {
					a.settings.SnapshotKeep = v
					a.saveSettings()
				})
			})

			a.settingsSection(c, "网络")
			a.settingsField(c, "网络代理", func() {
				ui.Switch(c, &a.settings.ProxyEnabled).OnChange(func() {
					if a.settings.ProxyEnabled && strings.TrimSpace(a.settings.ProxyURL) == "" {
						a.settings.ProxyURL = defaultProxyURL
					}
					a.applyProxySettings()
					a.saveSettings()
				})
				input := ui.TextInput(c, &a.settings.ProxyURL).Width(240)
				input.Placeholder(defaultProxyURL)
				if input.Changed() {
					a.applyProxySettings()
					a.saveSettings()
				}
			})
			if a.proxyErr != "" {
				ui.Text(c, a.proxyErr).FontSize(12).TextColor(t.Danger)
			}

			a.settingsSection(c, "更新")
			a.settingsField(c, "版本", func() {
				ui.Text(c, appVersion()).FontSize(12).TextColor(t.TextMuted)
				if ui.Button(c, tr("检查更新")).Padding(2, 10).Clicked() {
					checkUpdates()
				}
			})
			a.settingsSection(c, "权限")
			a.settingsField(c, "通知", func() {
				status, color := a.notifyStatus(t)
				ui.Text(c, status).FontSize(12).TextColor(color)
				if ui.Button(c, tr("发送测试通知")).Padding(2, 10).Disabled(a.permBusy).Clicked() {
					a.requestNotificationPermission()
				}
				if a.settings.NotifyState == "denied" {
					if ui.Button(c, tr("打开系统设置")).Padding(2, 10).Clicked() {
						a.openNotificationSettings()
					}
				}
			})
			a.settingsField(c, "脚本库访问", func() {
				if !a.libChecked {
					a.checkLibraryWrite()
				}
				if a.libWritable {
					ui.Text(c, tr("可写")).FontSize(12).TextColor(t.Success)
					ui.Text(c, a.store.Root()).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1)
					if ui.Button(c, tr("重新检查")).Padding(2, 10).Clicked() {
						a.recheckLibrary()
					}
					return
				}
				ui.Text(c, tr("不可写")).FontSize(12).TextColor(t.Danger)
				if ui.Button(c, tr("重新检查")).Padding(2, 10).Clicked() {
					a.recheckLibrary()
				}
				if ui.Button(c, tr("打开系统设置")).Padding(2, 10).Clicked() {
					a.openFileSettings()
				}
				if ui.Button(c, tr("更换目录…")).Padding(2, 10).Clicked() {
					a.changeLibrary()
				}
			})
			if a.libChecked && !a.libWritable && a.libProblem != "" {
				ui.Text(c, tr("无法写入 ")+a.store.Root()+"："+a.libProblem).FontSize(12).TextColor(t.Danger)
			}

			a.settingsSection(c, "外观")
			a.settingsField(c, "语言", func() {
				labels := []string{tr("跟随系统"), "简体中文", "English"}
				idx := 0
				switch a.settings.Language {
				case "zh":
					idx = 1
				case "en":
					idx = 2
				}
				choice := labels[idx]
				ui.Select(c, &choice, labels).OnChange(func() {
					switch choice {
					case "简体中文":
						a.settings.Language = "zh"
					case "English":
						a.settings.Language = "en"
					default:
						a.settings.Language = ""
					}
					a.saveSettings()
					a.applyLanguage()
					a.requestUpdate()
				})
			})
			a.settingsField(c, "主题", func() {
				ui.Segmented(c, &a.appearanceIdx, tr("跟随系统"), tr("浅色"), tr("深色")).
					OnChange(func() { a.applyAppearance() })
			})
		})
	})
}

// settingsChoice draws a select over labels and the values they stand for. A
// value outside the list is added, so the row always shows the real setting.
func (a *App) settingsChoice(c *ui.Context, labels []string, values []int, current int, set func(int)) {
	idx := -1
	for i, v := range values {
		if v == current {
			idx = i
		}
	}
	if idx < 0 {
		labels = append([]string{fmt.Sprint(current)}, labels...)
		values = append([]int{current}, values...)
		idx = 0
	}
	choice := labels[idx]
	ui.Select(c, &choice, labels).OnChange(func() {
		for i, label := range labels {
			if label == choice {
				set(values[i])
			}
		}
	})
}

// timeoutOptions are the default-timeout choices and the seconds each stands
// for; "不限制" is the 0 the setting stores.
func timeoutOptions() ([]string, []int) {
	labels := []string{tr("不限制")}
	values := []int{0}
	for _, sec := range []int{30, 60, 120, 300, 600, 1800, 3600} {
		labels = append(labels, durationLabel(sec))
		values = append(values, sec)
	}
	return labels, values
}

func durationLabel(sec int) string {
	switch {
	case sec >= 3600 && sec%3600 == 0:
		return trf("%d 小时", sec/3600)
	case sec >= 60 && sec%60 == 0:
		return trf("%d 分钟", sec/60)
	}
	return trf("%d 秒", sec)
}

// concurrencyOptions are the concurrency choices; 0 is "自动".
func concurrencyOptions() ([]string, []int) {
	labels := []string{tr("自动")}
	values := []int{0}
	for _, n := range []int{1, 2, 3, 4, 6, 8, 12, 16} {
		labels = append(labels, fmt.Sprint(n))
		values = append(values, n)
	}
	return labels, values
}

func (a *App) settingsSection(c *ui.Context, title string) {
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, tr(title)).FontSize(13).FontWeight(600).TextColor(t.Text)
		ui.Divider(c)
	})
}

func (a *App) settingsField(c *ui.Context, label string, control func()) {
	t := c.Theme()
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, tr(label)).FontSize(12).TextColor(t.TextMuted).Width(170).Shrink(0)
		ui.Row(c).Grow(1).Gap(8).AlignItems(ui.Center).Children(control)
	})
}

// settingsFieldHint is the same row with a small info icon after the label; the
// explanation lives in its tooltip.

func (a *App) notifyStatus(t *ui.Theme) (string, ui.Color) {
	if !a.notifySupported {
		return "当前系统不支持系统通知", t.TextMuted
	}
	switch a.settings.NotifyState {
	case "granted":
		return "已允许", t.Success
	case "denied":
		return "已拒绝，脚本结束时不会提醒", t.Danger
	case "error":
		return "发送失败", t.Danger
	}
	return "尚未请求", t.TextMuted
}

func (a *App) requestNotificationPermission() {
	if a.permBusy {
		return
	}
	a.permBusy = true
	a.requestUpdate()
	go func() {
		err := mygo.NewNotification(mygo.NotificationOptions{
			Title: "script-manager",
			Body:  "通知已开启，脚本运行结束时会提醒你",
		}).Show()
		state, msg := "granted", "通知已开启"
		switch {
		case errors.Is(err, mygo.ErrNotificationsDenied):
			state, msg = "denied", "通知被拒绝，可以在系统设置里允许"
		case err != nil:
			state, msg = "error", "通知发送失败："+err.Error()
		}
		apply := func() {
			a.permBusy = false
			a.settings.NotifyState = state
			a.saveSettings()
			a.showNotice(msg)
		}
		if a.win != nil {
			a.win.Update(apply)
			return
		}
		apply()
	}()
}

func (a *App) openNotificationSettings() {
	switch runtime.GOOS {
	case "darwin":
		mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.preference.notifications")
	default:
		mygo.Shell.OpenPath(a.store.Root())
	}
}

func (a *App) openFileSettings() {
	switch runtime.GOOS {
	case "darwin":
		mygo.Shell.OpenExternal("x-apple.systempreferences:com.apple.preference.security?Privacy_FilesAndFolders")
	default:
		mygo.Shell.OpenPath(a.store.Root())
	}
}

func (a *App) checkLibraryWrite() {
	a.libChecked = true
	probe := filepath.Join(a.store.Root(), ".write-check")
	err := os.WriteFile(probe, []byte("ok"), 0o600)
	if err == nil {
		err = os.Remove(probe)
	}
	if err != nil {
		a.libWritable = false
		a.libProblem = err.Error()
		return
	}
	a.libWritable = true
	a.libProblem = ""
}

func (a *App) recheckLibrary() {
	a.checkLibraryWrite()
	if a.libWritable {
		a.showNotice("脚本库可以写入")
		return
	}
	a.showNotice("脚本库不可写：" + a.libProblem)
}

func (a *App) saveSettings() {
	if err := a.settings.Save(); err != nil {
		a.alertError("设置没有保存", err.Error())
	}
}

func (a *App) applyAppearance() {
	source := mygo.ThemeSystem
	switch a.appearanceIdx {
	case 1:
		source = mygo.ThemeLight
	case 2:
		source = mygo.ThemeDark
	}
	a.settings.Appearance = []string{"system", "light", "dark"}[a.appearanceIdx]
	mygo.Theme.SetSource(source)
	a.saveSettings()
}

func (a *App) changeLibrary() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择脚本库目录", Directory: true, Multiple: false})
	if err != nil || len(paths) == 0 {
		return
	}
	root := paths[0]
	store, err := OpenStore(root)
	if err != nil {
		a.alertError("无法使用这个目录", err.Error())
		return
	}
	if a.importData().busy {
		a.alertError("暂时无法切换", "导入还在进行，等它结束或取消后再切换脚本库。")
		return
	}
	a.runner.StopAll()
	a.bindStore(store)
	a.applyProxySettings()
	a.settings.Library = root
	a.saveSettings()
	a.forgetAllEditors()
	a.selected = ""
	a.runOpen = ""
	a.revScriptID = ""
	a.revSt = nil
	a.revFiles = nil
	a.router.Replace("/scripts")
	a.showNotice("脚本库已切换")
}
