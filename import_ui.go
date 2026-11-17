package main

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// importDirectory picks a directory, then asks which folder in the library it
// should go into.
func (a *App) importDirectory() { a.pickImport(true, "", true) }

// importFile picks a single file, then asks which folder it goes into.
func (a *App) importFile() { a.pickImport(false, "", true) }

// pickImport asks for the source with the system dialog. With askTarget the
// user chooses the destination folder in the app; without it the import goes
// straight into folderID, which is what a folder's menu does.
func (a *App) pickImport(isDir bool, folderID string, askTarget bool) {
	title := "导入文件"
	if isDir {
		title = "导入目录"
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:     title,
		Directory: isDir,
		Multiple:  false,
	})
	if err != nil || len(paths) == 0 {
		return
	}
	if askTarget {
		a.openImportTarget(isDir, paths[0], folderID)
		return
	}
	if err := a.runImport(isDir, paths[0], folderID, defaultImportName(paths[0], isDir)); err != nil {
		a.alertError("导入失败", err.Error())
	}
}

// openImportTarget shows the step that names the destination folder. The last
// folder used is selected again, so repeated imports do not need reselecting.
func (a *App) openImportTarget(isDir bool, src, folderID string) {
	title := "导入文件"
	if isDir {
		title = "导入目录"
	}
	folders := a.folderChoices()
	want := folderID
	if want == "" {
		want = a.importFolderID
	}
	idx := 0
	for i, f := range folders {
		if f.ID == want {
			idx = i
		}
	}
	a.form = formState{
		kind:        dialogImport,
		open:        true,
		title:       title,
		path:        src,
		name:        defaultImportName(src, isDir),
		importIsDir: isDir,
		folderIdx:   idx,
		preview:     "正在统计文件…",
	}
	a.requestUpdate()
	// A network share or a huge folder would freeze the frame if the scan ran
	// here, so it runs off the UI thread and fills the dialog when done.
	go func() {
		count, bytes, err := PreviewImport(src, isDir)
		a.updateUI(func() {
			if !a.form.open || a.form.kind != dialogImport || a.form.path != src {
				return
			}
			if err != nil {
				a.form.preview = ""
				a.form.previewErr = err.Error()
			} else {
				a.form.preview = trf("将复制约 %d 个文件，共 %s（复制时会重新检查）", count, sizeText(bytes))
				a.form.previewErr = ""
			}
			a.requestUpdate()
		})
	}()
}

// importTargetForm draws the "导入到" step.
func (a *App) importTargetForm(c *ui.Context) {
	t := c.Theme()
	folders := a.folderChoices()
	if a.form.folderIdx >= len(folders) {
		a.form.folderIdx = 0
	}
	labels := make([]string, len(folders))
	for i, f := range folders {
		if f.ID == "" {
			labels[i] = "脚本库根目录"
		} else {
			labels[i] = a.folderPath(f.ID)
		}
	}
	if a.form.folderIdx < 0 {
		a.form.folderIdx = 0
	}
	a.formField(c, "来源", func() {
		ui.Text(c, filepath.Base(a.form.path)).FontSize(12).TextColor(t.Text).SingleLine().Grow(1)
	})
	a.formField(c, "导入到", func() {
		choice := labels[a.form.folderIdx]
		ui.Select(c, &choice, labels).OnChange(func() {
			for i, label := range labels {
				if label == choice {
					a.form.folderIdx = i
				}
			}
		})
	})
	a.formField(c, "名称", func() {
		ui.TextInput(c, &a.form.name).Grow(1)
	})
	if a.form.preview != "" {
		ui.Text(c, a.form.preview).FontSize(11.5).TextColor(t.TextMuted)
	}
	if a.form.previewErr != "" {
		ui.Text(c, a.form.previewErr).FontSize(11.5).TextColor(t.Danger).Wrap()
	}
	if a.form.importIsDir {
		ui.Text(c, tr("整个目录会复制进脚本库，.DS_Store、.git、node_modules 会被忽略。")).
			FontSize(11).TextColor(t.TextMuted).Wrap()
	} else {
		ui.Text(c, tr("文件会复制成脚本目录里的入口文件，扩展名保持不变。")).
			FontSize(11).TextColor(t.TextMuted).Wrap()
	}
}

// doImportTarget runs the import after the user picked a folder and a name.
func (a *App) doImportTarget() {
	name := strings.TrimSpace(a.form.name)
	if name == "" {
		a.form.err = "名称不能为空"
		return
	}
	folders := a.folderChoices()
	folderID := ""
	if a.form.folderIdx > 0 && a.form.folderIdx < len(folders) {
		folderID = folders[a.form.folderIdx].ID
	}
	saved := a.form
	a.form.open = false
	a.startImport(saved.importIsDir, saved.path, folderID, name)
}

// runImport starts an import; it reports nothing because the copy runs in the
// background and reports through the progress bar.
func (a *App) runImport(isDir bool, src, folderID, name string) error {
	a.startImport(isDir, src, folderID, name)
	return nil
}

// startImport copies in the background, showing progress and allowing cancel.
func (a *App) startImport(isDir bool, src, folderID, name string) {
	if !a.importRunning.CompareAndSwap(false, true) {
		a.alertError("暂时无法导入", "已有导入在进行，等它结束或取消后再试。")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.changeImport(func(st *importPanelState) {
		st.busy = true
		st.status = "准备导入…"
		st.cancel = cancel
	})
	a.requestUpdate()
	go func() {
		progress := func(done int, bytes int64) {
			// Only the atomic panel state changes here: the store holds its
			// lock while copying, and asking the UI to render inside it would
			// invert the locks between the frame and the import.
			a.changeImport(func(st *importPanelState) {
				st.status = trf("已复制 %d 个文件（%s）", done, sizeText(bytes))
			})
			a.requestUpdate()
		}
		var sc Script
		var err error
		if isDir {
			sc, err = a.store.ImportDirContext(ctx, src, folderID, name, "", progress)
		} else {
			sc, err = a.store.ImportFileContext(ctx, src, folderID, name, "", progress)
		}
		a.importRunning.Store(false)
		result := &pendingImportResult{script: sc}
		if err != nil {
			if ctx.Err() != nil {
				result.canceled = true
			} else {
				result.err = err.Error()
			}
		}
		a.pendingImport.Store(result)
		a.changeImport(func(st *importPanelState) {
			st.busy = false
			st.cancel = nil
		})
		a.requestUpdate()
	}()
}

// cancelImport stops the copy; the half-made script directory is removed.
func (a *App) cancelImport() {
	cancel := a.importData().cancel
	if cancel == nil {
		return
	}
	cancel()
	a.changeImport(func(st *importPanelState) { st.status = "正在取消…" })
	a.requestUpdate()
}

// importBar shows the copy progress above the content.
func (a *App) importBar(c *ui.Context, status string) {
	t := c.Theme()
	ui.Row(c).Height(34).PaddingX(16).Gap(10).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		ui.Spinner(c).Width(13).Height(13)
		ui.Text(c, status).FontSize(12).TextColor(t.Text).Grow(1).SingleLine()
		if ui.Button(c, tr("取消")).Padding(1, 8).Clicked() {
			a.cancelImport()
		}
	})
}

// finishImport opens the imported script and binds what it needs.
func (a *App) finishImport(sc Script) {
	folderID := sc.FolderID
	a.importFolderID = folderID
	a.reloadLibrary()
	a.folderOpen[folderID] = true
	a.selected = sc.ID
	a.router.Push("/scripts/" + sc.ID)
	var warning string
	sc, warning = a.bindImportedInterpreter(sc)
	if len(a.store.ScriptFiles(sc.ID)) > 1 {
		a.openEntryPicker(sc.ID)
	}
	msg := trf("已导入「%s」，入口 %s", sc.Name, sc.Entry)
	if warning != "" {
		msg += "；" + warning
	}
	a.showNotice(msg)
}

// defaultImportName is what the name field starts with.
func defaultImportName(src string, isDir bool) string {
	base := filepath.Base(src)
	if isDir {
		return base
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if strings.TrimSpace(stem) == "" {
		return base
	}
	return stem
}

// bindImportedInterpreter picks the interpreter the entry's language needs, so
// an imported script runs as soon as one is installed.
func (a *App) bindImportedInterpreter(sc Script) (Script, string) {
	if sc.InterpID != "" {
		return sc, ""
	}
	fresh, ok := a.store.Script(sc.ID)
	if !ok {
		return sc, ""
	}
	if its := a.interpretersFor(fresh.Language); len(its) > 0 {
		fresh.InterpID = its[0].ID
		if err := a.store.UpdateScript(fresh); err == nil {
			return fresh, ""
		}
	}
	return sc, "还没有 " + languageName(fresh.Language) + " 的解释器，先到解释器页添加"
}
