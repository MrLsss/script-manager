package main

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// openExportDialog fills the export dialog with every script.
func (a *App) openExportDialog() {
	scripts := a.store.Scripts()
	sort.SliceStable(scripts, func(i, j int) bool {
		if scripts[i].FolderID != scripts[j].FolderID {
			return a.folderPath(scripts[i].FolderID) < a.folderPath(scripts[j].FolderID)
		}
		return scripts[i].Position < scripts[j].Position
	})
	b := bundleState{open: true, mode: "export"}
	for _, sc := range scripts {
		b.ids = append(b.ids, sc.ID)
		b.labels = append(b.labels, a.folderPath(sc.FolderID)+" / "+sc.Name)
		b.checks = append(b.checks, true)
	}
	if len(b.ids) == 0 {
		a.showNotice("脚本库还是空的，没有可以导出的内容")
		return
	}
	a.bundle = b
}

// openImportDialog asks for a file, reads its manifest and shows the picker.
func (a *App) openImportDialog() {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:   "导入脚本",
		Filters: []mygo.FileFilter{{Name: "script-manager 导出文件", Extensions: []string{"zip"}}},
	})
	if err != nil || len(paths) == 0 {
		return
	}
	manifest, err := InspectBundle(paths[0])
	if err != nil {
		a.alertError("无法导入", err.Error())
		return
	}
	b := bundleState{open: true, mode: "import", path: paths[0], conflict: "导入为副本"}
	for _, bs := range manifest.Scripts {
		b.ids = append(b.ids, bs.ID)
		label := bs.Name
		if len(bs.Folder) > 0 {
			label = strings.Join(bs.Folder, " / ") + " / " + bs.Name
		}
		b.labels = append(b.labels, label)
		b.checks = append(b.checks, true)
	}
	if len(b.ids) == 0 {
		a.showNotice("这个文件里没有脚本")
		return
	}
	a.bundle = b
}

func (a *App) bundleModal(c *ui.Context) {
	t := c.Theme()
	b := &a.bundle
	title := "导出脚本"
	sub := "选中的脚本会打包成一个 zip，包含内容与清单。"
	action := "导出…"
	if b.mode == "import" {
		title = "导入脚本"
		sub = filepath.Base(b.path) + " · 选中的脚本会加入脚本库。"
		action = "导入"
	}
	ui.Modal(c, &b.open, func() {
		ui.Column(c).Width(560).Padding(20).Gap(12).Children(func() {
			ui.Text(c, title).FontSize(16).FontWeight(600)
			ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted).Wrap()
			ui.Scroll(c).MaxHeight(300).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					for i := range b.ids {
						ui.Checkbox(c, &b.checks[i], b.labels[i]).OnChange(func() {})
					}
				})
			})
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, tr("全选")).Padding(2, 10).Clicked() {
					all := true
					for _, on := range b.checks {
						if !on {
							all = false
						}
					}
					for i := range b.checks {
						b.checks[i] = !all
					}
				}
				ui.Text(c, trf("%d 个脚本", len(b.ids))).FontSize(11.5).TextColor(t.TextMuted)
			})
			if b.mode == "export" {
				ui.Text(c, tr("导出只包含当前内容；历史版本留在本机。")).
					FontSize(11.5).TextColor(t.TextMuted).Wrap()
			} else {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, tr("同名脚本")).FontSize(12).TextColor(t.TextMuted).Width(70)
					ui.Select(c, &b.conflict, []string{"跳过同名", "导入为副本", "覆盖同名"})
				})
			}
			if b.err != "" {
				ui.Text(c, b.err).FontSize(11.5).TextColor(t.Danger).Wrap()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, tr("取消")).Clicked() {
					b.open = false
				}
				if ui.PrimaryButton(c, action).Clicked() {
					if b.mode == "export" {
						a.doExport()
					} else {
						a.doImport()
					}
				}
			})
		})
	})
}

func (a *App) selectedBundleIDs() []string {
	var out []string
	for i, on := range a.bundle.checks {
		if on {
			out = append(out, a.bundle.ids[i])
		}
	}
	return out
}

func (a *App) doExport() {
	ids := a.selectedBundleIDs()
	if len(ids) == 0 {
		a.bundle.err = "至少选择一个脚本"
		return
	}
	target, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:       "导出脚本",
		DefaultPath: "script-manager-" + todayStamp() + ".zip",
		Filters:     []mygo.FileFilter{{Name: "Zip", Extensions: []string{"zip"}}},
	})
	if err != nil || target == "" {
		return
	}
	if !strings.HasSuffix(strings.ToLower(target), ".zip") {
		target += ".zip"
	}
	err = ExportLibrary(a.store, target, ExportOptions{
		ScriptIDs:        ids,
		IncludeRevisions: false,
		Host: func(hostID string) (string, string) {
			if h, ok := a.remotes.Host(hostID); ok {
				return h.Name, h.Target
			}
			return "", ""
		},
	})
	if err != nil {
		a.bundle.err = err.Error()
		return
	}
	a.bundle.open = false
	a.showNotice(trf("已导出 %d 个脚本", len(ids)))
}

func (a *App) doImport() {
	ids := a.selectedBundleIDs()
	if len(ids) == 0 {
		a.bundle.err = "至少选择一个脚本"
		return
	}
	conflict := ConflictCopy
	switch a.bundle.conflict {
	case "跳过同名":
		conflict = ConflictSkip
	case "覆盖同名":
		conflict = ConflictOverwrite
	}
	result, err := ImportLibrary(a.store, a.bundle.path, ImportOptions{
		ScriptIDs: ids,
		Conflict:  conflict,
		BindHost: func(name, target string) string {
			for _, h := range a.remotes.Hosts() {
				if target != "" && strings.EqualFold(h.Target, target) {
					return h.ID
				}
			}
			for _, h := range a.remotes.Hosts() {
				if name != "" && strings.EqualFold(h.Name, name) {
					return h.ID
				}
			}
			return ""
		},
	})
	if err != nil {
		a.bundle.err = err.Error()
		return
	}
	a.bundle.open = false
	a.reloadLibrary()
	parts := []string{}
	if len(result.Imported) > 0 {
		parts = append(parts, trf("导入 %d 个", len(result.Imported)))
	}
	if len(result.Replaced) > 0 {
		parts = append(parts, trf("覆盖 %d 个", len(result.Replaced)))
	}
	if len(result.Skipped) > 0 {
		parts = append(parts, trf("跳过 %d 个", len(result.Skipped)))
	}
	a.showNotice("导入完成：" + strings.Join(parts, "，"))
	if len(result.Unbound) > 0 {
		a.alertError("部分脚本没有解释器",
			"这些脚本已经导入，但本机没有匹配的解释器，需要到解释器页添加：\n"+strings.Join(result.Unbound, "、"))
	}
}

func (a *App) remoteForm(c *ui.Context) {
	ui.Text(c, tr("远端仓库地址")).FontSize(12).TextColor(c.Theme().TextMuted)
	ui.TextInput(c, &a.form.path).Placeholder("git@github.com:you/scripts.git")
	ui.Text(c, tr("支持 SSH 与 HTTPS 地址，认证沿用系统 Git 的凭据设置。")).
		FontSize(11.5).TextColor(c.Theme().TextMuted).Wrap()
}

func todayStamp() string {
	return time.Now().Format("20060102")
}
