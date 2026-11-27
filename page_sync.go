package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

// loadSyncState reads the repository in the background and redraws.
func (a *App) loadSyncState() {
	start := false
	a.changeSyncData(func(d *syncPanelState) {
		if d.busy {
			return
		}
		d.busy = true
		start = true
	})
	if !start {
		return
	}
	go func() {
		state := a.sync.Status()
		var snaps []Snapshot
		if state.Repo {
			snaps = a.sync.Snapshots()
		}
		a.updateUI(func() {
			a.changeSyncData(func(d *syncPanelState) {
				d.state = state
				d.snapshots = snaps
				d.busy = false
				d.loaded = true
			})
		})
	}()
}

func (a *App) refreshSync() {
	start := false
	a.changeSyncData(func(d *syncPanelState) {
		if d.busy {
			return
		}
		d.busy = true
		start = true
	})
	if !start {
		return
	}
	go func() {
		state, _ := a.sync.CheckRemote()
		var snaps []Snapshot
		if state.Repo {
			snaps = a.sync.Snapshots()
		}
		a.updateUI(func() {
			a.changeSyncData(func(d *syncPanelState) {
				d.state = state
				d.snapshots = snaps
				d.notice = NoticeText(state)
				d.busy = false
				d.loaded = true
			})
		})
	}()
}

func (a *App) runSync() {
	// Committing while a buffer is dirty would sync an older version of the
	// file than the one on screen; save first and say so.
	if err := a.saveAllScripts(); err != nil {
		a.alertError("无法同步", err.Error())
		return
	}
	// A literal credential in an environment variable would be committed to
	// the repository history, where deleting it later does not help. Warn
	// once a session, then let the user decide.
	if hits := suspiciousEnvSecrets(a.store.Scripts()); len(hits) > 0 && !a.secretWarned {
		a.confirmAction("环境变量里可能有明文密钥",
			"这些环境变量看起来是密钥，同步会写进仓库历史，之后删掉也没用："+
				strings.Join(hits, "、")+"。建议改成 ${secret:名称} 引用。仍要同步吗？",
			[]string{"取消", "仍然同步"}, func(i int) {
				if i != 1 {
					return
				}
				a.secretWarned = true
				a.doSync()
			})
		return
	}
	a.doSync()
}

// doSync is the body of runSync, after the checks that may stop it.
func (a *App) doSync() {
	keep := a.settings.SnapshotKeep
	start := false
	a.changeSyncData(func(d *syncPanelState) {
		if d.busy {
			return
		}
		d.busy = true
		start = true
	})
	if !start {
		return
	}
	go func() {
		state, err := a.sync.SyncNow()
		pruned := 0
		if err == nil {
			pruned, _ = a.sync.PruneSnapshots(keep)
		}
		var snaps []Snapshot
		var objects, library int64
		if state.Repo {
			snaps = a.sync.Snapshots()
			objects, library = a.sync.SnapshotUsage()
		}
		a.updateUI(func() {
			a.changeSyncData(func(d *syncPanelState) {
				d.busy = false
				d.loaded = true
				d.state = state
				d.snapshots = snaps
				d.objects = objects
				d.library = library
				if err == nil {
					d.notice = NoticeText(state)
				}
			})
			if err != nil {
				a.alertError("同步失败", err.Error())
				return
			}
			a.reloadLibrary()
			switch n := len(a.store.Conflicts()); {
			case n > 0:
				a.showNotice(trf("已同步，%d 个脚本需要处理冲突", n))
			case pruned > 0:
				a.showNotice(fmt.Sprintf("同步完成，清理了 %d 份旧快照", pruned))
			default:
				a.showNotice("同步完成")
			}
		})
	}()
}

func (a *App) reloadLibrary() {
	for key := range a.editors {
		delete(a.editors, key)
	}
	if sc, ok := a.currentScript(); ok {
		if _, still := a.store.Script(sc.ID); !still {
			a.router.Replace("/scripts")
		}
	}
}

func (a *App) syncPage(c *ui.Context) {
	t := c.Theme()
	if d := a.syncData(); !d.loaded && !d.busy {
		a.loadSyncState()
	}
	d := a.syncData()
	busy := d.busy
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Width(760).Padding(20, 20).Gap(16).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					ui.Text(c, tr("同步")).FontSize(16).FontWeight(600)
					ui.Text(c, tr("把脚本库放进一个 Git 仓库，多台设备之间保持一致。")).
						FontSize(12).TextColor(t.TextMuted)
				})
				if busy {
					ui.Spinner(c).Width(14).Height(14)
				}
				remoteReady := d.state.Remote != ""
				if ui.Button(c, tr("检查远端")).Padding(3, 12).
					Disabled(busy || !remoteReady).
					Tooltip("先配置远端仓库").
					Clicked() {
					a.refreshSync()
				}
				if ui.PrimaryButton(c, tr("立即同步")).Padding(3, 14).
					Disabled(busy || !remoteReady).
					Tooltip("先配置远端仓库").
					Clicked() {
					a.runSync()
				}
			})
			a.syncStatusCard(c)
			if len(a.store.Conflicts()) > 0 {
				a.syncConflictCard(c)
			}
			a.syncSnapshotsCard(c)
		})
	})
}

func (a *App) syncStatusCard(c *ui.Context) {
	t := c.Theme()
	st := a.syncData().state
	ui.Column(c).Padding(14).Gap(10).Radius(8).Background(t.Surface).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, tr("仓库状态")).FontSize(13).FontWeight(600)
			ui.Spacer(c)
			ui.Text(c, healthText(st.Health)).FontSize(11.5).TextColor(healthColor(c, st.Health))
		})
		if st.Health == healthNoGit {
			ui.Text(c, tr("没有找到 git 命令。安装 Git 之后回到这里重新检查。")).
				FontSize(12).TextColor(t.TextMuted).Wrap()
			return
		}
		if !st.Repo || st.Remote == "" {
			ui.Text(c, tr("给这个脚本库配置一个远端仓库，例如 GitHub 上的私有仓库。")).
				FontSize(12).TextColor(t.TextMuted).Wrap()
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &a.remoteText).Placeholder("git@github.com:you/scripts.git").Grow(1)
				if ui.Button(c, tr("连接远端")).Padding(3, 12).Clicked() {
					a.connectRemote()
				}
			})
			return
		}
		a.syncRow(c, "远端", st.Remote)
		a.syncRow(c, "分支", st.Branch)
		a.syncRow(c, "本地改动", fmt.Sprintf("%d 个文件", st.Dirty))
		a.syncRow(c, "与远端", syncDistance(st))
		a.syncRow(c, "最近提交", commitText(st))
		a.syncRow(c, "最近快照", orDash(st.LastSnapshot))
		if st.Problem != "" {
			ui.Text(c, st.Problem).FontSize(11.5).TextColor(t.Danger).Wrap()
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, tr("打开仓库目录")).Padding(3, 12).Clicked() {
				mygo.Shell.OpenPath(a.store.Root())
			}
			if ui.Button(c, tr("更换远端…")).Padding(3, 12).Clicked() {
				a.openRemoteDialog()
			}
		})
	})
}

func (a *App) syncRow(c *ui.Context, label, value string) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted).Width(80)
		ui.Text(c, value).FontSize(12).TextColor(t.Text).SingleLine().Grow(1)
	})
}

func (a *App) syncConflictCard(c *ui.Context) {
	t := c.Theme()
	conflicts := a.store.Conflicts()
	ui.Column(c).Padding(14).Gap(10).Radius(8).Background(t.Surface).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, tr("需要处理的脚本")).FontSize(13).FontWeight(600)
			ui.Badge(c, fmt.Sprint(len(conflicts)))
		})
		ui.Text(c, tr("这些脚本在本机和远端都改过。本机内容保留着，远端内容另存了一份。")).
			FontSize(12).TextColor(t.TextMuted).Wrap()
		for _, conflict := range conflicts {
			conflict := conflict
			ui.Column(c).Padding(10).Gap(8).Radius(6).Background(t.Background).Children(func() {
				ui.Text(c, conflict.ScriptName).FontSize(12.5).FontWeight(600)
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, tr("保留本机")).Padding(2, 10).Clicked() {
						a.resolveConflict(conflict, "local")
					}
					if ui.Button(c, tr("使用远端")).Padding(2, 10).Clicked() {
						a.resolveConflict(conflict, "remote")
					}
					if ui.Button(c, tr("两个都保留")).Padding(2, 10).Clicked() {
						a.resolveConflict(conflict, "both")
					}
				})
			})
		}
	})
}

func (a *App) resolveConflict(conflict SyncConflict, choice string) {
	a.changeSyncData(func(d *syncPanelState) { d.busy = true })
	go func() {
		err := a.sync.ResolveConflict(conflict, choice)
		a.updateUI(func() {
			a.changeSyncData(func(d *syncPanelState) { d.busy = false })
			if err != nil {
				a.alertError("处理冲突失败", err.Error())
				return
			}
			a.reloadLibrary()
			a.loadSyncState()
			a.showNotice("已处理「" + conflict.ScriptName + "」")
		})
	}()
}

func (a *App) syncSnapshotsCard(c *ui.Context) {
	t := c.Theme()
	d := a.syncData()
	snaps := d.snapshots
	ui.Column(c).Padding(14).Gap(8).Radius(8).Background(t.Surface).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, tr("快照")).FontSize(13).FontWeight(600)
			ui.Textf(c, tr("共 %d 份 · 占用 %s"), len(snaps), sizeText(d.objects)).
				FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			if ui.Button(c, tr("刷新")).Padding(2, 10).Clicked() {
				a.loadSnapshots()
			}
		})
		if d.library > 0 {
			ui.Textf(c, tr("仓库总大小 %s，包含日志与历史版本"), sizeText(d.library)).
				FontSize(11).TextColor(t.TextMuted)
		}
		if d.state.Remote == "" || !d.state.Repo {
			ui.Text(c, tr("同步一次之后，每次推送都会留下一个可以恢复的快照。")).
				FontSize(12).TextColor(t.TextMuted).Wrap()
			return
		}
		if len(snaps) == 0 {
			ui.Text(c, tr("还没有快照。")).FontSize(12).TextColor(t.TextMuted)
			return
		}
		for _, snap := range snaps {
			snap := snap
			ui.Row(c).Height(36).AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Icon(c, lucide.History()).Width(14).Height(14).TextColor(t.TextMuted)
				ui.Column(c).Gap(1).Grow(1).Children(func() {
					ui.Text(c, snap.Tag).FontSize(12).FontWeight(600).SingleLine()
					ui.Text(c, snap.When.Format("2006-01-02 15:04")+" · "+snap.Subject).FontSize(11).
						TextColor(t.TextMuted).SingleLine()
				})
				ui.ButtonBase(c).Padding(3).Tooltip(tr("删除这个快照")).
					OnClick(func() { a.deleteSnapshot(snap.Tag) }).
					Children(func() {
						ui.Icon(c, lucide.Trash2()).Width(13).Height(13).TextColor(t.TextMuted)
					})
				if ui.Button(c, tr("恢复")).Padding(2, 10).Clicked() {
					a.confirmAction("恢复到这个快照",
						"当前内容会先保存成一个新快照，然后回到 "+snap.Tag+" 的状态。",
						[]string{"取消", "恢复"}, func(i int) {
							if i != 1 {
								return
							}
							a.restoreSnapshot(snap.Tag)
						})
				}
			})
		}
	})
}

func (a *App) loadSnapshots() {
	if !a.syncData().state.Repo {
		return
	}
	snaps := a.sync.Snapshots()
	objects, library := a.sync.SnapshotUsage()
	a.changeSyncData(func(d *syncPanelState) {
		d.snapshots = snaps
		d.objects = objects
		d.library = library
	})
}

func (a *App) deleteSnapshot(tag string) {
	a.confirmAction("删除这个快照",
		"标签 "+tag+" 会从列表移除。对应的提交仍在仓库里，需要时可以用命令行按提交找回。",
		[]string{"取消", "删除"}, func(i int) {
			if i != 1 {
				return
			}
			if err := a.sync.DeleteSnapshot(tag); err != nil {
				a.alertError("删除失败", err.Error())
				return
			}
			a.loadSnapshots()
			a.showNotice("已删除 " + tag)
		})
}

func (a *App) restoreSnapshot(tag string) {
	a.changeSyncData(func(d *syncPanelState) { d.busy = true })
	go func() {
		err := a.sync.RestoreSnapshot(tag)
		a.updateUI(func() {
			a.changeSyncData(func(d *syncPanelState) { d.busy = false })
			if err != nil {
				a.alertError("恢复失败", err.Error())
				return
			}
			a.reloadLibrary()
			a.loadSyncState()
			a.loadSnapshots()
			a.showNotice("已恢复到 " + tag)
		})
	}()
}

func (a *App) connectRemote() {
	url := strings.TrimSpace(a.remoteText)
	if url == "" {
		a.showNotice("先填写远端仓库地址")
		return
	}
	a.changeSyncData(func(d *syncPanelState) { d.busy = true })
	go func() {
		err := a.sync.SetRemote(url)
		var state SyncState
		if err == nil {
			state, _ = a.sync.CheckRemote()
		}
		a.updateUI(func() {
			a.changeSyncData(func(d *syncPanelState) {
				d.busy = false
				if err != nil {
					return
				}
				d.state = state
				d.loaded = true
			})
			if err != nil {
				a.alertError("连接失败", err.Error())
				return
			}
			a.showNotice("已连接远端仓库，点「立即同步」推送")
		})
	}()
}

func (a *App) openRemoteDialog() {
	a.form.kind = dialogRemote
	a.form.open = true
	a.form.title = "更换远端仓库"
	a.form.path = a.syncData().state.Remote
	a.form.err = ""
}

func (a *App) saveRemote(url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		a.form.err = "请填写远端仓库地址"
		return
	}
	a.form.open = false
	a.remoteText = url
	a.connectRemote()
}

func (a *App) syncHeaderDot(c *ui.Context) {
	t := c.Theme()
	st := a.syncData().state
	if !st.Repo || st.Remote == "" {
		return
	}
	color := t.Success
	switch {
	case st.Problem != "" || st.Health == healthUnrelated:
		color = t.Danger
	case st.Behind > 0 || st.Ahead > 0 || st.Dirty > 0:
		color = t.Warning
	}
	ui.Box(c).Width(8).Height(8).Radius(4).Background(color).Tooltip(syncDistance(st)).
		OnClick(func() { a.router.Push("/sync") })
}

func healthText(health string) string {
	switch health {
	case healthNoGit:
		return tr("没有找到 git")
	case healthNoRepo:
		return tr("还没有建立仓库")
	case healthNoRemote:
		return tr("还没有配置远端")
	case healthNoUpstream:
		return tr("远端分支还没有建立")
	case healthUnrelated:
		return tr("与远端的提交历史没有关系")
	case healthDetached:
		return tr("当前不在任何分支上")
	}
	return tr("正常")
}

func healthColor(c *ui.Context, health string) ui.Color {
	t := c.Theme()
	switch health {
	case healthHealthy:
		return t.Success
	case healthNoRepo, healthNoRemote, healthNoUpstream:
		return t.TextMuted
	}
	return t.Danger
}

func syncDistance(st SyncState) string {
	switch {
	case st.Health == healthNoUpstream:
		return tr("还没有推送过")
	case st.Behind > 0 && st.Ahead > 0:
		return fmt.Sprintf(tr("落后 %d 个提交，领先 %d 个提交"), st.Behind, st.Ahead)
	case st.Behind > 0:
		return fmt.Sprintf(tr("落后 %d 个提交"), st.Behind)
	case st.Ahead > 0:
		return fmt.Sprintf(tr("领先 %d 个提交"), st.Ahead)
	}
	return tr("与远端一致")
}

func commitText(st SyncState) string {
	if st.LastCommit == "" {
		return "—"
	}
	if st.LastCommitTime.IsZero() {
		return st.LastCommit
	}
	return st.LastCommit + " · " + fmtAgo(st.LastCommitTime)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (a *App) syncNoticeBar(c *ui.Context) {
	t := c.Theme()
	notice := a.syncData().notice
	ui.Row(c).Height(34).PaddingX(16).Gap(10).AlignItems(ui.Center).Background(t.InactiveAccent).Children(func() {
		ui.Icon(c, lucide.RefreshCw()).Width(14).Height(14).TextColor(t.Accent)
		ui.Text(c, notice).FontSize(12).TextColor(t.Text).Grow(1).SingleLine()
		if ui.Button(c, tr("查看")).Padding(2, 10).Clicked() {
			a.router.Push("/sync")
		}
		if ui.Button(c, tr("忽略")).Padding(2, 10).Clicked() {
			a.changeSyncData(func(d *syncPanelState) { d.notice = "" })
		}
	})
}
