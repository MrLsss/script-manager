package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
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
				ui.Text(c, tr("打包成 zip，可在另一台设备导入")).FontSize(12).TextColor(t.TextMuted)
			})

			a.settingsSection(c, "运行")
			a.settingsField(c, "默认超时", func() {
				if ui.TextInput(c, &a.timeoutText).Width(80).Changed() {
					if n, err := strconv.Atoi(strings.TrimSpace(a.timeoutText)); err == nil && n >= 0 {
						a.settings.DefaultTimeout = n
						a.saveSettings()
					}
				}
				ui.Text(c, tr("秒，0 表示不限制")).FontSize(12).TextColor(t.TextMuted)
			})
			a.settingsField(c, "并发运行", func() {
				if ui.TextInput(c, &a.concurrencyText).Width(70).Changed() {
					if n, err := strconv.Atoi(strings.TrimSpace(a.concurrencyText)); err == nil && n >= 0 {
						a.settings.Concurrency = n
						a.saveSettings()
					}
				}
				ui.Textf(c, tr("个脚本同时运行，0 表示自动；本机 %d 核，建议 %d"),
					runtime.NumCPU(), suggestedConcurrency()).FontSize(12).TextColor(t.TextMuted)
			})
			a.settingsField(c, "运行结束通知", func() {
				ui.Switch(c, &a.settings.Notify).OnChange(func() { a.saveSettings() })
				ui.Text(c, tr("运行结束时发送系统通知")).FontSize(12).TextColor(t.TextMuted)
			})
			a.settingsField(c, "日志脱敏", func() {
				ui.Switch(c, &a.settings.RedactLogs).OnChange(func() {
					a.runner.SetRedaction(a.settings.RedactLogs)
					a.saveSettings()
				})
				ui.Text(c, tr("把 token、password、Bearer 等值替换成 ***")).FontSize(12).TextColor(t.TextMuted)
			})
			a.settingsField(c, "完成后展开日志面板", func() {
				ui.Switch(c, &a.settings.OpenPanel).OnChange(func() { a.saveSettings() })
			})

			a.settingsSection(c, "日志与历史")
			a.settingsField(c, "日志保留", func() {
				options := []string{trf("%d 天", 7), trf("%d 天", 30), trf("%d 天", 90), trf("%d 天", 180)}
				choice := a.retentionLabel
				if !hasString(options, choice) {
					choice = trf("%d 天", a.settings.RetentionDays)
				}
				ui.Select(c, &choice, options).OnChange(func() {
					a.retentionLabel = choice
					if n, err := retentionDaysFromLabel(choice); err == nil {
						a.settings.RetentionDays = n
						a.saveSettings()
					}
				})
			})
			a.settingsField(c, "单次日志上限", func() {
				if ui.TextInput(c, &a.logLimitText).Width(80).Changed() {
					if n, err := strconv.Atoi(strings.TrimSpace(a.logLimitText)); err == nil && n > 0 {
						a.settings.MaxLogBytes = int64(n) << 20
						a.runner.logLimit = a.settings.MaxLogBytes
						a.saveSettings()
					}
				}
				ui.Text(c, tr("MB，超出后截断")).FontSize(12).TextColor(t.TextMuted)
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
				ui.Text(c, tr("每次运行前记录内容快照")).FontSize(12).TextColor(t.TextMuted)
			})
			a.settingsField(c, "清理旧记录", func() {
				if ui.Button(c, tr("清理超过保留期的日志")).Padding(2, 10).Clicked() {
					if err := a.store.PruneRuns(a.settings.RetentionDays); err != nil {
						a.alertError("清理失败", err.Error())
						return
					}
					a.showNotice("已清理旧日志")
				}
			})

			a.settingsSection(c, "同步")
			a.settingsFieldHint(c, "快照保留", "每次同步后自动删除更早的快照，删除标签不会删除提交", func() {
				label := snapshotKeepLabel(a.settings.SnapshotKeep)
				if ui.Select(c, &label, snapshotKeepLabels).Changed() {
					a.settings.SnapshotKeep = snapshotKeepValue(label)
					a.saveSettings()
				}
			})

			a.settingsSection(c, "网络")
			a.settingsFieldHint(c, "网络代理", "脚本运行时使用这个代理，Git 拉取与推送也使用它", func() {
				input := ui.TextInput(c, &a.settings.ProxyURL).Width(240)
				input.Placeholder(defaultProxyURL)
				if input.Changed() {
					a.applyProxySettings()
					a.saveSettings()
				}
				ui.Switch(c, &a.settings.ProxyEnabled).OnChange(func() {
					if a.settings.ProxyEnabled && strings.TrimSpace(a.settings.ProxyURL) == "" {
						a.settings.ProxyURL = defaultProxyURL
					}
					a.applyProxySettings()
					a.saveSettings()
				})
			})
			if a.proxyErr != "" {
				ui.Text(c, a.proxyErr).FontSize(12).TextColor(t.Danger)
			}

			a.settingsSection(c, "更新")
			a.settingsField(c, "GitHub 仓库", func() {
				if ui.TextInput(c, &a.updateRepoText).Placeholder("owner/repo").Width(220).Changed() {
					a.settings.UpdateRepo = strings.TrimSpace(a.updateRepoText)
					a.saveSettings()
				}
				ui.Text(c, tr("从 GitHub Release 检查新版本")).FontSize(12).TextColor(t.TextMuted)
			})
			a.settingsField(c, "启动时检查更新", func() {
				ui.Switch(c, &a.settings.CheckUpdates).OnChange(func() { a.saveSettings() })
			})
			a.settingsField(c, "检查更新", func() {
				if ui.Button(c, tr("立即检查")).Padding(2, 10).Clicked() {
					a.checkForUpdates(true)
				}
				ui.Text(c, tr("当前版本 ")+appVersion).FontSize(12).TextColor(t.TextMuted)
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

var snapshotKeepLabels = []string{"最近 10 份", "最近 30 份", "最近 90 份", "不限"}

func snapshotKeepLabel(keep int) string {
	switch keep {
	case 10:
		return snapshotKeepLabels[0]
	case 90:
		return snapshotKeepLabels[2]
	case -1:
		return snapshotKeepLabels[3]
	}
	return snapshotKeepLabels[1]
}

func snapshotKeepValue(label string) int {
	switch label {
	case snapshotKeepLabels[0]:
		return 10
	case snapshotKeepLabels[2]:
		return 90
	case snapshotKeepLabels[3]:
		return -1
	}
	return 30
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
func (a *App) settingsFieldHint(c *ui.Context, label, hint string, control func()) {
	t := c.Theme()
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Width(170).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr(label)).FontSize(12).TextColor(t.TextMuted).Wrap()
			ui.Icon(c, lucide.Info()).Width(12).Height(12).TextColor(t.TextMuted).Tooltip(hint)
		})
		ui.Row(c).Grow(1).Gap(8).AlignItems(ui.Center).Children(control)
	})
}

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

// retentionDaysFromLabel reads the number out of a retention option, which is
// the day count followed by the unit as the language writes it.
func retentionDaysFromLabel(label string) (int, error) {
	for i := len(label); i > 0; i-- {
		if n, err := strconv.Atoi(strings.TrimSpace(label[:i])); err == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("无法识别的保留期: %s", label)
}
