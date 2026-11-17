package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

func (a *App) openRevisions(sc Script) {
	revs := a.store.RevisionsOf(sc.ID)
	if len(revs) == 0 {
		a.showNotice("还没有历史版本，保存或运行后会自动记录")
		return
	}
	a.revScriptID = sc.ID
	a.revSel = 0
	a.loadRevisionPreview(sc, revs[0])
	a.router.Push("/scripts/" + sc.ID + "/history")
}

// loadRevisionPreview picks the file of a snapshot to show: the entry the
// snapshot recorded, or its first file.
func (a *App) loadRevisionPreview(sc Script, rev Revision) {
	a.revFiles = a.store.RevisionFiles(rev.ID)
	rel := a.store.RevisionEntry(rev.ID)
	if rel == "" && len(a.revFiles) > 0 {
		rel = a.revFiles[0].Path
	}
	a.revFile = rel
	content, err := a.store.RevisionFileContent(rev.ID, rel)
	if err != nil {
		content = ""
	}
	buf := editor.NewBuffer(content)
	buf.SetLanguage(editor.LanguageForText(rel, content))
	a.revSt = &editor.State{Buffer: buf, ReadOnly: true, NoFolding: true}
}

func (a *App) historyHeader(c *ui.Context, sc Script) {
	t := c.Theme()
	ui.Icon(c, lucide.History()).Width(16).Height(16).TextColor(t.TextMuted)
	ui.Text(c, tr("历史版本")).FontSize(15).FontWeight(600)
	ui.Text(c, sc.Name+" · 入口 "+sc.Entry).FontSize(12).TextColor(t.TextMuted)
}

func (a *App) historyPage(c *ui.Context, id string) {
	sc, ok := a.store.Script(id)
	if !ok {
		a.router.Replace("/scripts")
		return
	}
	revs := a.store.RevisionsOf(sc.ID)
	if len(revs) == 0 {
		a.router.Replace("/scripts/" + sc.ID)
		return
	}
	if a.revScriptID != sc.ID || a.revSt == nil || a.revSel >= len(revs) {
		a.revScriptID = sc.ID
		a.revSel = 0
		a.loadRevisionPreview(sc, revs[0])
	}
	ui.Split(c, &a.revW, func() {
		a.revisionList(c, sc, revs)
	}, func() {
		a.revisionPreview(c, sc, revs)
	}).Grow(1).FillWidth().SplitReset(320)
}

func (a *App) revisionList(c *ui.Context, sc Script, revs []Revision) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).Background(t.Background).Children(func() {
		ui.Row(c).Height(40).PaddingX(12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("全部版本")).FontSize(12).FontWeight(600)
			ui.Spacer(c)
			ui.Textf(c, tr("%d 份"), len(revs)).FontSize(11).TextColor(t.TextMuted)
		})
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(6).Gap(2).Children(func() {
				for i, rev := range revs {
					a.revisionRow(c, sc, revs, rev, i)
				}
			})
		})
	})
}

func (a *App) revisionRow(c *ui.Context, sc Script, revs []Revision, rev Revision, i int) {
	t := c.Theme()
	bg := t.Background
	if i == a.revSel {
		bg = t.Surface
	}
	ui.Row(c).FillWidth().Padding(7, 8).Radius(6).Background(bg).Gap(8).AlignItems(ui.Center).
		OnClick(func() {
			if a.revSel == i {
				return
			}
			a.revSel = i
			a.loadRevisionPreview(sc, rev)
		}).Children(func() {
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, fmtAgo(rev.CreatedAt)).FontSize(12).TextColor(t.Text).SingleLine()
			ui.Textf(c, tr("%s · %d 个文件 · %s"), rev.Hash, rev.Files, sizeText(rev.Size)).
				FontSize(10.5).TextColor(t.TextMuted).SingleLine()
			if rev.Entry != "" {
				ui.Text(c, tr("入口 ")+rev.Entry).FontSize(10.5).TextColor(t.TextMuted).SingleLine()
			}
		})
		if i == 0 {
			ui.Text(c, tr("最新")).FontSize(10).TextColor(t.TextMuted)
		}
		ui.ButtonBase(c).Padding(3).Tooltip(tr("删除这份版本")).
			OnClick(func() { a.deleteRevision(sc, rev) }).
			Children(func() {
				ui.Icon(c, lucide.Trash2()).Width(13).Height(13).TextColor(t.TextMuted)
			})
	})
}

func (a *App) deleteRevision(sc Script, rev Revision) {
	a.confirmAction("删除这份历史版本",
		trf("%s · %s，删除后无法恢复。脚本文件本身不会改变。", rev.Hash, sizeText(rev.Size)),
		[]string{"取消", "删除"}, func(i int) {
			if i != 1 {
				return
			}
			if err := a.store.DeleteRevision(sc.ID, rev.ID); err != nil {
				a.alertError("删除失败", err.Error())
				return
			}
			a.revSt = nil
			a.revSel = 0
			a.showNotice("已删除这份历史版本")
			a.requestUpdate()
		})
}

func (a *App) revisionPreview(c *ui.Context, sc Script, revs []Revision) {
	t := c.Theme()
	rev := revs[a.revSel]
	ui.Column(c).Grow(1).MinWidth(0).Background(t.Background).Children(func() {
		ui.Row(c).Height(40).PaddingX(12).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmtAgo(rev.CreatedAt)).FontSize(12).FontWeight(600).SingleLine().Shrink(0)
			ui.Textf(c, tr("%s · %d 个文件 · %s"), rev.Hash, rev.Files, sizeText(rev.Size)).
				FontSize(11).TextColor(t.TextMuted).SingleLine()
			ui.Spacer(c)
			if ui.Button(c, tr("复制内容")).Padding(1, 8).Shrink(0).Clicked() {
				if content, err := a.store.RevisionFileContent(rev.ID, a.revFile); err == nil {
					mygo.Clipboard.WriteText(content)
					a.showNotice("已复制这份内容")
				}
			}
			if ui.PrimaryButton(c, tr("恢复整个目录")).Padding(1, 8).Shrink(0).Clicked() {
				a.restoreRevision(sc, revs, a.revSel)
			}
		})
		if len(a.revFiles) > 0 {
			ui.Row(c).Height(32).PaddingX(12).Gap(8).AlignItems(ui.Center).
				Background(t.Surface).Children(func() {
				ui.Text(c, tr("查看文件")).FontSize(11.5).TextColor(t.TextMuted)
				labels := make([]string, len(a.revFiles))
				for i, f := range a.revFiles {
					labels[i] = f.Path
				}
				choice := a.revFile
				if ui.Select(c, &choice, labels).Changed() {
					a.revFile = choice
					content, err := a.store.RevisionFileContent(rev.ID, choice)
					if err != nil {
						content = ""
					}
					a.revSt.Buffer.Set(content)
					a.revSt.Buffer.SetLanguage(editor.LanguageForText(choice, content))
					a.revSt.Buffer.MarkSaved()
				}
			})
		}
		ui.Divider(c)
		if a.revSt != nil {
			editor.View(c, a.revSt).Grow(1).FillWidth()
		}
	})
}

// restoreRevision puts the whole snapshot back after the user confirms, since
// it replaces every file and drops unsaved edits.
func (a *App) restoreRevision(sc Script, revs []Revision, idx int) {
	if idx < 0 || idx >= len(revs) {
		return
	}
	rev := revs[idx]
	a.confirmAction("恢复整个目录",
		trf("「%s」的 %d 个文件会替换成 %s 的内容，当前未保存的修改会被丢弃。",
			sc.Name, rev.Files, rev.Hash),
		[]string{"取消", "恢复"}, func(i int) {
			if i != 1 {
				return
			}
			if err := a.store.RestoreRevision(sc.ID, rev.ID); err != nil {
				a.alertError("恢复失败", err.Error())
				return
			}
			a.refreshEditors(sc)
			a.router.Replace("/scripts/" + sc.ID)
			a.showNotice("已恢复整个目录")
		})
}

func sizeText(n int64) string {
	if n < 1024 {
		return trf("%d 字节", n)
	}
	if n < 1024*1024 {
		return trf("%.1f KB", float64(n)/1024)
	}
	return trf("%.1f MB", float64(n)/(1024*1024))
}
