package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

func (a *App) scriptPage(c *ui.Context, id string) {
	sc, ok := a.store.Script(id)
	if !ok {
		ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Text(c, tr("这个脚本不存在或已经删除")).FontSize(13).TextColor(c.Theme().TextMuted)
		})
		return
	}
	a.selected = sc.ID
	if a.curFile(sc) == "" {
		a.openScriptFile(sc, sc.Entry)
	}
	if a.runCollapsed {
		ui.Column(c).Grow(1).FillWidth().Children(func() {
			a.editorPane(c, sc)
			a.runPane(c, sc)
		})
		return
	}
	ui.SplitVerticalPercent(c, &a.panelPct, func() {
		a.editorPane(c, sc)
	}, func() {
		a.runPane(c, sc)
	}).Grow(1).FillWidth().SplitReset(70)
}

func (a *App) editorPane(c *ui.Context, sc Script) {
	t := c.Theme()
	files := a.store.ScriptFiles(sc.ID)
	multi := len(files) > 1
	ui.Column(c).Grow(1).MinHeight(0).Background(t.Background).Children(func() {
		if multi {
			ui.Split(c, &a.treeW, func() {
				a.fileTreePane(c, sc, files)
			}, func() {
				a.editorArea(c, sc, files, true)
			}).Grow(1).FillWidth().SplitReset(200)
		} else {
			a.editorArea(c, sc, files, false)
		}
	})
}

// editorArea holds the tabs, the editor and the status bar.
func (a *App) editorArea(c *ui.Context, sc Script, files []ScriptFile, multi bool) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinHeight(0).MinWidth(0).Background(t.Background).Children(func() {
		if sc.Dangerous {
			a.dangerBar(c)
		}
		if links := a.scriptSymlinks(sc.ID); len(links) > 0 {
			a.symlinkBar(c, links)
		}
		if multi {
			a.fileTabs(c, sc)
			ui.Divider(c)
		}
		rel := a.curFile(sc)
		if rel == "" {
			rel = sc.Entry
			a.files[sc.ID] = rel
		}
		if !a.editable(sc, rel) {
			a.notEditable(c, sc, rel)
			a.editorStatus(c, sc, nil, rel)
			return
		}
		st := a.fileEditor(sc, rel)
		editor.View(c, st).Grow(1).FillWidth()
		a.editorStatus(c, sc, st, rel)
	})
}

// dangerBar warns above the editor that this script asks again before it
// runs.
func (a *App) dangerBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Height(30).PaddingX(12).Gap(8).AlignItems(ui.Center).Background(t.Surface).
		Children(func() {
			ui.Icon(c, lucide.TriangleAlert()).Width(14).Height(14).TextColor(t.Warning)
			ui.Text(c, tr("高危脚本：每次运行前都会弹出确认框")).FontSize(11.5).TextColor(t.Warning)
		})
}

// symlinkBar says that links are left out of snapshots and uploads.
func (a *App) symlinkBar(c *ui.Context, links []string) {
	t := c.Theme()
	ui.Row(c).Height(30).PaddingX(12).Gap(8).AlignItems(ui.Center).Background(t.Surface).
		Children(func() {
			ui.Icon(c, lucide.Info()).Width(14).Height(14).TextColor(t.Warning)
			text := strings.Join(links, "、")
			ui.Text(c, tr("符号链接不会进入历史快照，也不会上传到远程：")+text).
				FontSize(11.5).TextColor(t.Warning).SingleLine()
		})
}

// notEditable stands in for binary or very large files, which still take part
// in runs, imports and snapshots.
func (a *App) notEditable(c *ui.Context, sc Script, rel string) {
	t := c.Theme()
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(10).Children(func() {
		ui.Icon(c, lucide.FileCode()).Width(30).Height(30).TextColor(t.TextMuted)
		ui.Text(c, tr("该文件不在编辑器中打开")).FontSize(13).FontWeight(600)
		ui.Text(c, rel+" 可能是二进制文件，或者超过 2MB。").FontSize(11.5).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, tr("在访达中显示")).Padding(2, 10).Clicked() {
				mygo.Shell.ShowItemInFolder(filepath.Join(a.store.ScriptDir(sc), filepath.FromSlash(rel)))
			}
			if ui.Button(c, tr("设为入口")).Padding(2, 10).Clicked() {
				a.setEntry(sc, rel)
			}
		})
	})
}

// fileTabs shows one tab per open file, with a dot when it is unsaved.
func (a *App) fileTabs(c *ui.Context, sc Script) {
	t := c.Theme()
	cur := a.curFile(sc)
	ui.ScrollHorizontal(c).Height(32).Shrink(0).Children(func() {
		ui.Row(c).Gap(1).FillHeight().Children(func() {
			for _, rel := range a.scriptTabs(sc) {
				rel := rel
				active := rel == cur
				bg := t.Background
				if active {
					bg = t.Surface
				}
				dirty := false
				if st, ok := a.editors[editorKey(sc.ID, rel)]; ok && st.Buffer.Modified() {
					dirty = true
				}
				ui.Row(c).Height(31).PaddingX(10).Gap(6).Background(bg).
					AlignItems(ui.Center).OnClick(func() { a.openScriptFile(sc, rel) }).
					ContextMenu(func(m *ui.Menu) {
						if rel != sc.Entry && m.Item(tr("关闭标签")).Chosen() {
							a.closeScriptFile(sc, rel)
						}
						if m.Item(tr("设为入口")).Chosen() {
							a.setEntry(sc, rel)
						}
					}).Children(func() {
					ui.Text(c, path.Base(rel)).FontSize(12).SingleLine()
					if rel == sc.Entry {
						ui.Text(c, tr("入口")).FontSize(9.5).TextColor(t.Accent)
					}
					if dirty {
						ui.Box(c).Width(5).Height(5).Radius(2.5).Background(t.Warning).Tooltip(tr("未保存"))
					}
				})
			}
		})
	})
}

// fileTreePane lists every file of a multi-file script.
func (a *App) fileTreePane(c *ui.Context, sc Script, files []ScriptFile) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).Background(t.Surface).Children(func() {
		ui.Row(c).Height(32).PaddingX(10).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("文件")).FontSize(11.5).FontWeight(600).TextColor(t.TextMuted)
			ui.Spacer(c)
			ui.MenuButton(c, "＋", func(m *ui.Menu) {
				if m.Item(tr("新建文件…")).Chosen() {
					a.openNewFile(sc, "")
				}
				if m.Item(tr("新建子目录…")).Chosen() {
					a.openNewDir(sc, "")
				}
				if m.Item(tr("在访达中显示")).Chosen() {
					mygo.Shell.ShowItemInFolder(a.store.ScriptPath(sc))
				}
			}).Tooltip(tr("添加文件"))
		})
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(6).Gap(1).Children(func() {
				a.renderFileTree(c, sc, buildFileTree(files), 0)
			})
		})
	})
}

type treeNode struct {
	name string
	rel  string
	dir  bool
	kids []*treeNode
}

func buildFileTree(files []ScriptFile) []*treeNode {
	var roots []*treeNode
	dirs := map[string]*treeNode{}
	find := func(rel string) *treeNode { return dirs[rel] }
	var insert func(parts []string, prefix string, into *[]*treeNode)
	insert = func(parts []string, prefix string, into *[]*treeNode) {
		name := parts[0]
		rel := name
		if prefix != "" {
			rel = prefix + "/" + name
		}
		if len(parts) == 1 {
			*into = append(*into, &treeNode{name: name, rel: rel})
			return
		}
		node := find(rel)
		if node == nil {
			node = &treeNode{name: name, rel: rel, dir: true}
			dirs[rel] = node
			*into = append(*into, node)
		}
		insert(parts[1:], rel, &node.kids)
	}
	for i := range files {
		insert(strings.Split(files[i].Path, "/"), "", &roots)
	}
	sortNodes(roots)
	return roots
}

func sortNodes(nodes []*treeNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].dir != nodes[j].dir {
			return nodes[i].dir
		}
		return nodes[i].name < nodes[j].name
	})
	for _, n := range nodes {
		sortNodes(n.kids)
	}
}

func (a *App) renderFileTree(c *ui.Context, sc Script, nodes []*treeNode, depth int) {
	for _, node := range nodes {
		if node.dir {
			open, known := a.treeOpen[node.rel]
			if !known {
				open = true
			}
			t := c.Theme()
			ui.Row(c).Height(26).PaddingX(4).Radius(5).Gap(5).
				MarginStart(float32(depth) * 12).AlignItems(ui.Center).
				OnClick(func() { a.treeOpen[node.rel] = !open }).
				ContextMenu(func(m *ui.Menu) {
					if m.Item(tr("新建文件…")).Chosen() {
						a.openNewFile(sc, node.rel)
					}
					if m.Item(tr("新建子目录…")).Chosen() {
						a.openNewDir(sc, node.rel)
					}
				}).Children(func() {
				if open {
					ui.Icon(c, lucide.ChevronDown()).Width(11).Height(11).TextColor(t.TextMuted)
				} else {
					ui.Icon(c, lucide.ChevronRight()).Width(11).Height(11).TextColor(t.TextMuted)
				}
				ui.Icon(c, lucide.Folder()).Width(13).Height(13).TextColor(t.TextMuted)
				ui.Text(c, node.name).FontSize(12).TextColor(t.Text).SingleLine()
			})
			if open {
				a.renderFileTree(c, sc, node.kids, depth+1)
			}
			continue
		}
		a.fileRow(c, sc, node, depth)
	}
}

func (a *App) fileRow(c *ui.Context, sc Script, node *treeNode, depth int) {
	t := c.Theme()
	entry := node.rel == sc.Entry
	bg := t.Surface
	if node.rel == a.curFile(sc) {
		bg = t.Background
	}
	ui.Row(c).Height(26).PaddingX(4).Radius(5).Gap(5).Background(bg).
		MarginStart(float32(depth) * 12).AlignItems(ui.Center).
		OnClick(func() {
			a.openScriptFile(sc, node.rel)
		}).ContextMenu(func(m *ui.Menu) {
		if entry {
			m.Item(tr("当前入口")).Disabled(true)
		} else if m.Item(tr("设为入口")).Chosen() {
			a.setEntry(sc, node.rel)
		}
		if m.Item(tr("重命名…")).Chosen() {
			a.openRenameFile(sc, node.rel)
		}
		if m.Item(tr("在访达中显示")).Chosen() {
			mygo.Shell.ShowItemInFolder(filepath.Join(a.store.ScriptDir(sc), filepath.FromSlash(node.rel)))
		}
		m.Separator()
		if m.Item(tr("删除…")).Chosen() {
			a.deleteScriptFile(sc, node.rel)
		}
	}).Children(func() {
		ui.Icon(c, iconForPath(node.rel)).Width(13).Height(13).TextColor(t.TextMuted)
		ui.Text(c, node.name).FontSize(12).TextColor(t.Text).SingleLine().Grow(1)
		if entry {
			ui.Text(c, tr("入口")).FontSize(9.5).TextColor(t.Accent)
		} else if st, ok := a.editors[editorKey(sc.ID, node.rel)]; ok && st.Buffer.Modified() {
			ui.Box(c).Width(5).Height(5).Radius(2.5).Background(t.Warning)
		}
	})
}

func (a *App) editorStatus(c *ui.Context, sc Script, st *editor.State, rel string) {
	t := c.Theme()
	line, col := 0, 0
	if st != nil {
		pos := st.CursorPos()
		line, col = pos.Line+1, pos.Col+1
	}
	rev := "—"
	if revs := a.store.RevisionsOf(sc.ID); len(revs) > 0 {
		rev = revs[0].Hash
	}
	lang := "纯文本"
	if l, ok := languageForEntry(rel); ok {
		lang = languageName(l)
	}
	ui.Row(c).Height(26).PaddingX(12).Gap(14).AlignItems(ui.Center).
		Background(t.Surface).Children(func() {
		ui.Text(c, rel).FontSize(11).TextColor(t.TextMuted).SingleLine()
		ui.Textf(c, tr("第 %d 行，第 %d 列"), line, col).FontSize(11).TextColor(t.TextMuted)
		ui.Spacer(c)
		ui.Text(c, lang).FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, "UTF-8").FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, tr("修订 ")+rev).FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *App) runPane(c *ui.Context, sc Script) {
	t := c.Theme()
	run, lines, ok := a.panelRun(sc)
	col := ui.Column(c).Background(t.Surface)
	if a.runCollapsed {
		col.Height(34).Shrink(0)
	} else {
		col.Grow(1).MinHeight(0)
	}
	col.Children(func() {
		ui.Row(c).Height(34).PaddingX(12).Gap(10).AlignItems(ui.Center).Children(func() {
			if !ok {
				ui.Text(c, tr("还没有运行记录，点右上角「运行」开始")).FontSize(12).TextColor(t.TextMuted)
				ui.Spacer(c)
				if a.runCollapsed {
					if ui.Button(c, tr("展开")).Padding(1, 8).Clicked() {
						a.runCollapsed = false
					}
				}
				return
			}
			if run.Status == RunRunning {
				ui.Spinner(c).Width(12).Height(12)
			} else {
				ui.Box(c).Width(7).Height(7).Radius(3.5).Background(statusColor(t, run.Status))
			}
			ui.Text(c, runStatusText(run.Status)).FontSize(12).FontWeight(600)
			if run.Dangerous {
				ui.Text(c, tr("高危")).FontSize(10.5).TextColor(t.Warning)
			}
			if run.RevID == "" {
				ui.Text(c, tr("本次无历史快照")).FontSize(11).TextColor(t.Warning).
					Tooltip("脚本目录超过 5MB，本次运行没有记录可恢复的版本")
			}
			if run.Admin {
				ui.Text(c, tr("管理员")).FontSize(10.5).TextColor(t.Warning)
			}
			if run.Status != RunRunning {
				ui.Textf(c, tr("退出码 %d"), run.ExitCode).FontSize(11).TextColor(t.TextMuted)
			} else if run.Uploading {
				ui.Text(c, tr("正在上传脚本…")).FontSize(11).TextColor(t.TextMuted)
			} else if run.Admin {
				ui.Text(c, tr("等待系统授权…")).FontSize(11).TextColor(t.TextMuted)
			}
			ui.Text(c, fmtDur(run)).FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, fmtTime(run.StartedAt)).FontSize(11).TextColor(t.TextMuted)
			if run.Entry != "" {
				ui.Text(c, run.Entry).FontSize(11).TextColor(t.TextMuted).Tooltip(tr("入口"))
			}
			if run.Truncated {
				ui.Text(c, tr("日志已截断")).FontSize(11).TextColor(t.Warning)
			}
			ui.Spacer(c)
			if ui.Button(c, tr("复制")).Padding(1, 8).Clicked() {
				mygo.Clipboard.WriteText(a.logText(run))
				a.showNotice("日志已复制")
			}
			if ui.Button(c, tr("打开日志")).Padding(1, 8).Clicked() {
				mygo.Shell.ShowItemInFolder(a.store.RunLogPath(run.ScriptID, run.ID))
			}
			if run.Status == RunRunning && run.Admin {
				if ui.Button(c, tr("断开查看")).Padding(1, 8).Clicked() {
					a.stopScript(sc)
				}
			}
			if a.runCollapsed {
				if ui.Button(c, tr("展开")).Padding(1, 8).Clicked() {
					a.runCollapsed = false
				}
			} else if ui.Button(c, tr("收起")).Padding(1, 8).Clicked() {
				a.runCollapsed = true
			}
		})
		if a.runCollapsed {
			return
		}
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(8, 12).Gap(1).Children(func() {
				start := 0
				if len(lines) > 400 {
					start = len(lines) - 400
				}
				if start > 0 {
					ui.Textf(c, tr("省略前面 %d 行"), start).FontSize(11).TextColor(t.TextMuted)
				}
				for _, line := range lines[start:] {
					ui.Text(c, line.Text).Font("Menlo").FontSize(12).
						TextColor(logColor(t, line.Kind)).Wrap()
				}
			})
		})
	})
}

// setEntry points the script at a file and refreshes the header.
func (a *App) setEntry(sc Script, rel string) {
	if rel == sc.Entry {
		return
	}
	if err := a.store.SetEntry(sc.ID, rel); err != nil {
		a.alertError("无法设为入口", err.Error())
		return
	}
	a.openScriptFile(sc, rel)
	a.showNotice("入口已改为 " + rel)
}

func (a *App) deleteScriptFile(sc Script, rel string) {
	a.confirmAction("删除文件",
		fmt.Sprintf("「%s」会从脚本目录中删除。文件树里只有删除，没有撤销。", rel),
		[]string{"取消", "删除"}, func(i int) {
			if i != 1 {
				return
			}
			if err := a.store.DeleteScriptFile(sc.ID, rel); err != nil {
				a.alertError("删除失败", err.Error())
				return
			}
			a.invalidateScriptCache(sc.ID)
			a.closeScriptFile(sc, rel)
			a.showNotice("已删除 " + rel)
		})
}

func (a *App) panelRun(sc Script) (Run, []LogLine, bool) {
	if run, ok := a.runner.Active(sc.ID); ok {
		lines, _ := a.runner.Lines(run.ID)
		return run, lines, true
	}
	run, ok := a.lastRun(sc.ID)
	if !ok {
		return Run{}, nil, false
	}
	lines, ok := a.runner.Lines(run.ID)
	if !ok {
		lines = a.fileLines(run)
	}
	return run, lines, true
}

func (a *App) fileLines(run Run) []LogLine {
	data, err := os.ReadFile(a.store.RunLogPath(run.ScriptID, run.ID))
	if err != nil {
		return []LogLine{{Kind: 1, Text: "日志无法读取: " + err.Error()}}
	}
	raw := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	lines := make([]LogLine, 0, len(raw))
	for _, text := range raw {
		kind := 0
		if strings.HasPrefix(text, "$ ") {
			kind = 2
		}
		lines = append(lines, LogLine{Kind: kind, Text: text})
	}
	return lines
}

func (a *App) logText(run Run) string {
	if lines, ok := a.runner.Lines(run.ID); ok {
		var b strings.Builder
		for _, line := range lines {
			b.WriteString(line.Text)
			b.WriteByte('\n')
		}
		return b.String()
	}
	return a.runner.LogText(run)
}

func logColor(t *ui.Theme, kind int) ui.Color {
	switch kind {
	case 1:
		return t.Danger
	case 2:
		return t.Accent
	case 3:
		return t.TextMuted
	}
	return t.Text
}

func durationOf(run Run) time.Duration {
	end := run.EndedAt
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(run.StartedAt)
}

func scriptIcon() *ui.SVG { return lucide.FileCode() }
