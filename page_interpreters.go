package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

func (a *App) interpretersPage(c *ui.Context) {
	t := c.Theme()
	list := a.store.Interpreters()
	ui.Column(c).Grow(1).Padding(16).Gap(12).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, tr("%d 个解释器"), len(list)).FontSize(12).TextColor(t.TextMuted)
			ui.Spacer(c)
			if ui.Button(c, tr("添加解释器")).Padding(2, 10).Clicked() {
				a.openInterpreter(nil)
			}
			if a.detecting.Load() {
				ui.Spinner(c).Width(14).Height(14)
				ui.Text(c, tr("检测中…")).FontSize(12).TextColor(t.TextMuted)
			} else if ui.Button(c, tr("重新检测全部")).Padding(2, 10).Clicked() {
				a.detectInterpreters()
			}
		})
		if len(list) == 0 {
			a.emptyInterpreters(c)
			return
		}
		if a.interpSel >= len(list) {
			a.interpSel = 0
		}
		cols := []ui.TableColumn{
			{Title: "名称", Width: 180},
			{Title: "语言", Width: 80},
			{Title: "版本", Width: 80},
			{Title: "路径"},
			{Title: "状态", Width: 96},
			{Title: "引用", Width: 56, Align: ui.End},
		}
		ui.Table(c, &ui.ListState{Selected: &a.interpSel}, cols, len(list), func(row, col int) {
			it := list[row]
			switch col {
			case 0:
				ui.Text(c, it.Name).SingleLine()
			case 1:
				ui.Text(c, languageName(it.Language)).TextColor(t.TextMuted)
			case 2:
				ui.Text(c, it.Version).TextColor(t.TextMuted)
			case 3:
				ui.Text(c, it.Path).TextColor(t.TextMuted).SingleLine()
			case 4:
				ui.Text(c, stateText(it.State)).TextColor(interpStateColor(t, it.State))
			case 5:
				ui.Textf(c, "%d", a.store.Usage(it.ID)).TextColor(t.TextMuted)
			}
		}).Grow(1).Submitted()
		a.interpreterDetail(c, list[a.interpSel])
	})
}

func (a *App) emptyInterpreters(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(12).Children(func() {
		ui.Icon(c, lucide.Terminal()).Width(40).Height(40).TextColor(t.TextMuted)
		ui.Text(c, tr("还没有解释器")).FontSize(16).FontWeight(600)
		ui.Text(c, tr("自动检测会查找 python3、node、bun、zsh、bash、go、ruby、lua 等命令。")).FontSize(12).TextColor(t.TextMuted)
		if a.detecting.Load() {
			ui.Spinner(c).Width(14).Height(14)
			ui.Text(c, tr("正在检测本机解释器…")).FontSize(12).TextColor(t.TextMuted)
		} else if ui.PrimaryButton(c, tr("检测本机解释器")).Clicked() {
			a.detectInterpreters()
		}
	})
}

func (a *App) interpreterDetail(c *ui.Context, it Interpreter) {
	t := c.Theme()
	ui.Column(c).Padding(14).Gap(10).Radius(10).BorderWidth(1, 1, 1, 1).
		BorderColor(t.Border).Background(t.Surface).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, it.Name+" "+it.Version).FontSize(14).FontWeight(600)
			ui.Text(c, stateText(it.State)).FontSize(11).TextColor(interpStateColor(t, it.State))
			ui.Spacer(c)
			if ui.Button(c, tr("重新检测")).Padding(2, 10).Clicked() {
				Probe(&it)
				if err := a.store.UpdateInterpreter(it); err != nil {
					a.alertError("保存失败", err.Error())
					return
				}
				a.showNotice("已重新检测")
			}
			if ui.Button(c, tr("编辑")).Padding(2, 10).Clicked() {
				a.openInterpreter(&it)
			}
			del := ui.Button(c, tr("删除")).Padding(2, 10).OnClick(func() {
				if n := a.store.Usage(it.ID); n > 0 {
					a.alertError("无法删除", trf("有 %d 个脚本正在使用这个解释器，先给它们换一个解释器。", n))
					return
				}
				a.confirmAction("删除解释器", "「"+it.Name+"」会从列表里移除，本机的可执行文件不受影响。",
					[]string{"取消", "删除"}, func(i int) {
						if i != 1 {
							return
						}
						if err := a.store.DeleteInterpreter(it.ID); err != nil {
							a.alertError("删除失败", err.Error())
							return
						}
						a.showNotice("解释器已删除")
					})
			})
			if a.store.Usage(it.ID) > 0 {
				del.Tooltip(tr("有脚本正在使用这个解释器"))
			}
		})
		ui.Row(c).Gap(20).Children(func() {
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				a.kv(c, "可执行文件", it.Path)
				a.kv(c, "参数模板", strings.Join(it.Args, " "))
				a.kv(c, "探针参数", strings.Join(it.ProbeArgs, " "))
			})
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				a.kv(c, "上次校验", fmtTime(it.CheckedAt))
				a.kv(c, "引用脚本", trf("%d 个", a.store.Usage(it.ID)))
				a.kv(c, "加入方式", interpSource(it.Detected))
			})
		})
	})
}

func (a *App) kv(c *ui.Context, label, value string) {
	t := c.Theme()
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, tr(label)).FontSize(11).TextColor(t.TextMuted).Width(72).Shrink(0)
		ui.Text(c, value).FontSize(11.5).TextColor(t.Text).Wrap()
	})
}

func interpSource(detected bool) string {
	if detected {
		return "自动检测"
	}
	return "手动添加"
}

func interpStateColor(t *ui.Theme, state string) ui.Color {
	switch state {
	case StateOK:
		return t.Success
	case StateMissing, StateNoExec:
		return t.Danger
	}
	return t.Warning
}

func (a *App) runsPage(c *ui.Context) {
	ui.Split(c, &a.runsW, func() { a.runListPane(c) }, func() { a.runDetailPane(c) }).
		Grow(1).FillWidth().SplitReset(320)
}

func (a *App) runListPane(c *ui.Context) {
	t := c.Theme()
	runs := a.visibleRuns()
	ui.Column(c).Grow(1).MinWidth(0).Background(t.Background).Children(func() {
		ui.Column(c).Padding(10).Gap(8).Children(func() {
			ui.Segmented(c, &a.runFilter, tr("全部"), tr("成功"), tr("失败"))
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Textf(c, tr("%d 条记录"), len(runs)).FontSize(11).TextColor(t.TextMuted)
				ui.Spacer(c)
				a.runBulkControls(c, runs)
			})
		})
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(6).Gap(1).Children(func() {
				if len(runs) == 0 {
					ui.Text(c, tr("没有运行记录")).FontSize(12).TextColor(t.TextMuted).Padding(8)
				}
				for _, run := range runs {
					a.runRow(c, run)
				}
			})
		})
	})
}

// runBulkControls selects and deletes runs of the list shown.
func (a *App) runBulkControls(c *ui.Context, runs []Run) {
	all := len(runs) > 0
	for _, run := range runs {
		if !a.runChecks[run.ID] {
			all = false
			break
		}
	}
	ui.Checkbox(c, &all, tr("全选")).OnChange(func() {
		for _, run := range runs {
			if all {
				a.runChecks[run.ID] = true
			} else {
				delete(a.runChecks, run.ID)
			}
		}
	})
	selected := a.selectedRunIDs()
	if ui.Button(c, trf("删除选中 (%d)", len(selected))).Padding(1, 8).
		Disabled(len(selected) == 0).Clicked() {
		a.deleteRuns(selected)
	}
}

// selectedRunIDs lists the checked runs that still exist.
func (a *App) selectedRunIDs() []string {
	var out []string
	for _, run := range a.store.Runs() {
		if a.runChecks[run.ID] {
			out = append(out, run.ID)
		}
	}
	return out
}

// deleteRuns asks once, then removes the records and their logs.
func (a *App) deleteRuns(ids []string) {
	if len(ids) == 0 {
		return
	}
	a.confirmAction(tr("删除运行记录"),
		trf("%d 条运行记录和对应的日志文件会被删除，此操作不可撤销。", len(ids)),
		[]string{"取消", "删除"}, func(i int) {
			if i != 1 {
				return
			}
			removed, err := a.store.DeleteRuns(ids)
			if err != nil {
				a.alertError("删除失败", err.Error())
				return
			}
			for _, id := range ids {
				delete(a.runChecks, id)
				if a.runOpen == id {
					a.runOpen = ""
				}
			}
			a.showNotice(trf("已删除 %d 条运行记录", removed))
		})
}

func (a *App) runRow(c *ui.Context, run Run) {
	t := c.Theme()
	bg := t.Background
	if a.runOpen == run.ID {
		bg = t.Surface
	}
	name := "已删除的脚本"
	if sc, ok := a.store.Script(run.ScriptID); ok {
		name = sc.Name
	}
	ui.Column(c).Padding(8).Radius(6).Gap(2).Background(bg).
		OnClick(func() { a.runOpen = run.ID }).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			checked := a.runChecks[run.ID]
			ui.Checkbox(c, &checked, "").OnChange(func() {
				if checked {
					a.runChecks[run.ID] = true
				} else {
					delete(a.runChecks, run.ID)
				}
			})
			ui.Box(c).Width(6).Height(6).Radius(3).Background(statusColor(t, run.Status))
			ui.Text(c, name).FontSize(12.5).TextColor(t.Text).SingleLine().Grow(1)
			ui.Text(c, fmtAgo(run.StartedAt)).FontSize(10.5).TextColor(t.TextMuted)
		})
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, runStatusText(run.Status)).FontSize(11).TextColor(statusColor(t, run.Status))
			if run.Dangerous {
				ui.Text(c, tr("高危")).FontSize(10.5).TextColor(t.Warning)
			}
			ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
			if run.HostName != "" {
				ui.Text(c, tr("远程 ")+run.HostName).FontSize(11).TextColor(t.Accent).SingleLine()
				ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
			}
			if run.Entry != "" {
				ui.Text(c, run.Entry).FontSize(11).TextColor(t.TextMuted).SingleLine()
				ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
			}
			if run.HostName != "" {
				ui.Text(c, run.RemoteInterp).FontSize(11).TextColor(t.TextMuted).SingleLine()
			} else {
				ui.Text(c, run.InterpName+" "+run.InterpVersion).FontSize(11).TextColor(t.TextMuted).SingleLine()
			}
		})
	})
}

func (a *App) visibleRuns() []Run {
	var out []Run
	for _, run := range a.store.Runs() {
		switch a.runFilter {
		case 1:
			if run.Status != RunOK {
				continue
			}
		case 2:
			if run.Status != RunFailed && run.Status != RunTimeout && run.Status != RunAborted && run.Status != RunUnknown {
				continue
			}
		}
		out = append(out, run)
	}
	return out
}

func (a *App) runDetailPane(c *ui.Context) {
	t := c.Theme()
	run, ok := a.store.Run(a.runOpen)
	if !ok {
		ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Text(c, tr("选一条运行记录查看日志")).FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	sc, hasScript := a.store.Script(run.ScriptID)
	name := "已删除的脚本"
	if hasScript {
		name = sc.Name
	}
	ui.Column(c).Grow(1).MinWidth(0).Padding(16).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, name).FontSize(16).FontWeight(600)
			ui.Text(c, runStatusText(run.Status)).FontSize(12).TextColor(statusColor(t, run.Status))
			ui.Spacer(c)
			if hasScript {
				if ui.Button(c, tr("重跑")).Padding(2, 10).Clicked() {
					a.runScript(sc)
					a.runOpen = ""
					a.router.Push("/scripts/" + sc.ID)
				}
			}
			if ui.Button(c, tr("复制日志")).Padding(2, 10).Clicked() {
				mygo.Clipboard.WriteText(a.logText(run))
				a.showNotice("日志已复制")
			}
			if ui.Button(c, tr("打开日志")).Padding(2, 10).Clicked() {
				mygo.Shell.ShowItemInFolder(a.store.RunLogPath(run.ScriptID, run.ID))
			}
		})
		ui.Row(c).Gap(24).Children(func() {
			a.kv(c, "开始", fmtTime(run.StartedAt))
			a.kv(c, "耗时", fmtDur(run))
			a.kv(c, "退出码", fmt.Sprint(run.ExitCode))
		})
		ui.Row(c).Gap(24).Children(func() {
			if run.HostName != "" {
				a.kv(c, "运行位置", "远程 "+run.HostName)
				a.kv(c, "远端解释器", run.RemoteInterp)
			} else {
				a.kv(c, "解释器", run.InterpName+" "+run.InterpVersion)
			}
			a.kv(c, "入口", run.Entry)
			rev := run.RevID
			if rev == "" {
				rev = "无（目录超过快照上限）"
			}
			a.kv(c, "修订", rev)
			if run.HostName != "" {
				a.kv(c, "远端目录", run.RemoteDir)
			} else {
				a.kv(c, "工作目录", run.Cwd)
			}
		})
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(1).Children(func() {
				for _, line := range a.fileLines(run) {
					ui.Text(c, line.Text).Font("Menlo").FontSize(12).
						TextColor(logColor(t, line.Kind)).Wrap()
				}
			})
		})
	})
}
