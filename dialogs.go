package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *App) dialogs(c *ui.Context) {
	if a.form.open {
		a.formModal(c)
	}
	if a.bundle.open {
		a.bundleModal(c)
	}
	if a.confirm.open {
		idx := ui.AlertDialog(c, &a.confirm.open, a.confirm.title, a.confirm.message, a.confirm.buttons...)
		if idx >= 0 {
			act := a.confirm.action
			a.confirm.open = false
			if act != nil {
				act(idx)
			}
		}
	}
}

func (a *App) formModal(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.form.open, func() {
		ui.Column(c).Width(460).Padding(20).Gap(12).Children(func() {
			ui.Text(c, tr(a.form.title)).FontSize(16).FontWeight(600)
			switch a.form.kind {
			case dialogScript:
				a.newScriptForm(c)
			case dialogFolder:
				a.newFolderForm(c)
			case dialogRename:
				a.renameForm(c)
			case dialogInterpreter:
				a.interpreterForm(c)
			case dialogScriptOptions:
				a.scriptOptionsForm(c)
			case dialogRunArgs:
				a.runArgsForm(c)
			case dialogImport:
				a.importTargetForm(c)
			case dialogRemoteHost:
				a.hostForm(c)
			case dialogRemote:
				a.remoteForm(c)
			case dialogNewFile:
				a.newFileForm(c)
			case dialogNewDir:
				a.newDirForm(c)
			case dialogRenameFile:
				a.renameFileForm(c)
			case dialogEntry:
				a.entryForm(c)
			}
			if a.form.err != "" {
				ui.Text(c, a.form.err).FontSize(11.5).TextColor(t.Danger).Wrap()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, tr("取消")).Clicked() {
					a.form.open = false
				}
				if ui.PrimaryButton(c, a.formSubmitLabel()).Clicked() {
					a.submitForm()
				}
			})
		})
	})
}

func (a *App) newScriptForm(c *ui.Context) {
	langs := languageOrder
	if !hasString(langs, a.form.lang) {
		a.form.lang = langs[0]
	}
	a.formField(c, "名称", func() {
		ui.TextInput(c, &a.form.name).Placeholder(tr("例如 备份网站")).Grow(1)
	})
	a.formField(c, "语言", func() {
		labels := make([]string, len(langs))
		for i, l := range langs {
			labels[i] = languageName(l)
		}
		choice := languageName(a.form.lang)
		ui.Select(c, &choice, labels).OnChange(func() {
			for _, l := range langs {
				if languageName(l) == choice {
					a.form.lang = l
				}
			}
		})
	})
	preview := sanitizeFileName(strings.TrimSpace(a.form.name), languageExt(a.form.lang))
	ui.Text(c, tr("文件会保存为 scripts/<脚本 id>/")+preview+"，脚本名与文件名一致").
		FontSize(11).TextColor(c.Theme().TextMuted)
}

func (a *App) newFolderForm(c *ui.Context) {
	a.formField(c, "名称", func() {
		ui.TextInput(c, &a.form.name).Placeholder(tr("例如 服务器日常运维")).Grow(1)
	})
	ui.Text(c, tr("文件夹会建在")+a.folderPath(a.form.folderID)+"。").FontSize(11).TextColor(c.Theme().TextMuted)
}

func (a *App) renameForm(c *ui.Context) {
	a.formField(c, "名称", func() {
		ui.TextInput(c, &a.form.name).Grow(1)
	})
	if a.form.folderTarget {
		return
	}
	if sc, ok := a.store.Script(a.form.targetID); ok {
		if a.store.IsMultiFile(sc) {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c, &a.form.renameEntry, "同时重命名入口文件")
			})
			ui.Text(c, tr("其他文件可能引用了这个文件名，默认不改动入口。")).
				FontSize(11).TextColor(c.Theme().TextMuted).Wrap()
		} else {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c, &a.form.renameEntry, "同时重命名入口文件")
			})
		}
	}
}

func (a *App) newFileForm(c *ui.Context) {
	a.formField(c, "相对路径", func() {
		ui.TextInput(c, &a.form.rel).Placeholder(tr("例如 lib/common.sh")).Grow(1)
	})
	ui.Text(c, tr("路径相对脚本目录，可以用 / 建子目录。新文件是空的，保存后会记入修订。")).
		FontSize(11).TextColor(c.Theme().TextMuted).Wrap()
}

func (a *App) newDirForm(c *ui.Context) {
	a.formField(c, "相对路径", func() {
		ui.TextInput(c, &a.form.rel).Placeholder(tr("例如 lib")).Grow(1)
	})
}

func (a *App) renameFileForm(c *ui.Context) {
	a.formField(c, "相对路径", func() {
		ui.TextInput(c, &a.form.rel).Grow(1)
	})
	ui.Text(c, tr("入口文件改名时，入口会跟着改。")).
		FontSize(11).TextColor(c.Theme().TextMuted)
}

func (a *App) entryForm(c *ui.Context) {
	t := c.Theme()
	if len(a.form.entryChoices) == 0 {
		ui.Text(c, tr("这个脚本还没有文件")).FontSize(12).TextColor(t.Danger)
		return
	}
	if a.form.entryIdx >= len(a.form.entryChoices) {
		a.form.entryIdx = 0
	}
	labels := a.form.entryChoices
	choice := labels[a.form.entryIdx]
	a.formField(c, "入口文件", func() {
		ui.Select(c, &choice, labels).OnChange(func() {
			for i, label := range labels {
				if label == choice {
					a.form.entryIdx = i
				}
			}
		})
	})
	ui.Text(c, tr("运行、日志与修订都以入口为准。")).
		FontSize(11).TextColor(t.TextMuted).Wrap()
}

func (a *App) interpreterForm(c *ui.Context) {
	a.formField(c, "名称", func() {
		ui.TextInput(c, &a.form.name).Placeholder(tr("例如 Python 3.12")).Grow(1)
	})
	a.formField(c, "可执行文件", func() {
		ui.TextInput(c, &a.form.path).Placeholder("/usr/bin/python3").Grow(1)
		if ui.Button(c, tr("选择…")).Clicked() {
			if paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择解释器", Multiple: false}); err == nil && len(paths) > 0 {
				a.form.path = paths[0]
			}
		}
	})
	langs := languageOrder
	a.formField(c, "语言", func() {
		labels := make([]string, len(langs))
		for i, l := range langs {
			labels[i] = languageName(l)
		}
		choice := languageName(a.form.lang)
		ui.Select(c, &choice, labels).OnChange(func() {
			for _, l := range langs {
				if languageName(l) == choice {
					a.form.lang = l
				}
			}
		})
	})
	a.formField(c, "参数模板", func() {
		ui.TextInput(c, &a.form.args).Placeholder(tr("{script}、{dir}，或 -u {script}")).Grow(1)
	})
	a.formField(c, "版本探针参数", func() {
		ui.TextInput(c, &a.form.probe).Placeholder("--version").Grow(1)
	})
	ui.Text(c, tr("参数模板里的 {script} 会替换成入口文件的路径，{dir} 替换成入口所在目录；没有 {script} 时会在末尾追加入口。")).FontSize(11).TextColor(c.Theme().TextMuted).Wrap()
}

func (a *App) scriptOptionsForm(c *ui.Context) {
	sc, ok := a.store.Script(a.form.targetID)
	if !ok {
		return
	}
	a.formField(c, "高危脚本", func() {
		ui.Switch(c, &a.form.dangerous)
		ui.Text(c, tr("运行时先弹出确认框")).FontSize(12).TextColor(c.Theme().TextMuted)
	})
	a.formField(c, "运行前检查", func() {
		ui.Switch(c, &a.form.preflight)
		ui.Text(c, tr("检查 shell、Python、JavaScript 的语法")).FontSize(12).TextColor(c.Theme().TextMuted)
	})
	a.formField(c, "文件", func() {
		if ui.Button(c, tr("添加文件…")).Padding(2, 10).Clicked() {
			a.form.open = false
			a.openNewFile(sc, "")
		}
		ui.Text(c, tr("新增后脚本页会显示文件树")).FontSize(11).TextColor(c.Theme().TextMuted)
	})
	ui.Text(c, tr("运行参数、环境变量、工作目录、超时和管理员开关在页头的「参数」按钮里，可按配置保存多套。")).
		FontSize(11).TextColor(c.Theme().TextMuted).Wrap()
}

func (a *App) runArgsForm(c *ui.Context) {
	sc, ok := a.store.Script(a.form.targetID)
	if !ok {
		return
	}
	labels := []string{sc.defaultProfileName()}
	ids := []string{""}
	for _, p := range sc.Profiles {
		labels = append(labels, p.Name)
		ids = append(ids, p.ID)
	}
	idx := 0
	for i, id := range ids {
		if id == a.form.profileID {
			idx = i
		}
	}
	a.formField(c, "配置", func() {
		choice := labels[idx]
		ui.Select(c, &choice, labels).OnChange(func() {
			for i, label := range labels {
				if label == choice {
					a.form.profileID = ids[i]
					a.fillRunProfileForm(sc)
				}
			}
		})
		if ui.Button(c, tr("另存为…")).Padding(2, 10).Clicked() {
			a.saveRunProfileCopy(sc)
		}
		if a.form.profileID != "" {
			if ui.Button(c, tr("删除")).Padding(2, 10).Clicked() {
				a.deleteRunProfile(sc)
			}
		}
	})
	a.formField(c, "名称", func() {
		ui.TextInput(c, &a.form.profileName).Grow(1)
	})
	a.formField(c, "参数", func() {
		ui.TextInput(c, &a.form.args).Placeholder(tr("附加参数，空格分隔；支持引号")).Grow(1)
	})
	a.formField(c, "环境变量", func() {
		ui.TextArea(c, &a.form.env).Height(72).Grow(1)
	})
	a.formField(c, "工作目录", func() {
		ui.TextInput(c, &a.form.cwd).Placeholder(tr("留空则用入口所在目录")).Grow(1)
		if ui.Button(c, tr("选择…")).Clicked() {
			if paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择工作目录", Directory: true}); err == nil && len(paths) > 0 {
				a.form.cwd = paths[0]
			}
		}
	})
	a.formField(c, "超时", func() {
		ui.TextInput(c, &a.form.timeout).Width(80)
		ui.Text(c, tr("秒，留空或 0 用设置里的默认值")).FontSize(11).TextColor(c.Theme().TextMuted)
	})
	a.formField(c, "权限", func() {
		ui.Switch(c, &a.form.admin)
		ui.Text(c, tr("以管理员身份运行")).FontSize(12).TextColor(c.Theme().TextMuted)
	})
	ui.Text(c, tr("每行一个 KEY=VALUE，# 开头为注释；参数会追加在解释器模板之后。")).
		FontSize(11).TextColor(c.Theme().TextMuted).Wrap()
}

// fillRunProfileForm loads the selected profile (or the default fields) into
// the editor.
func (a *App) fillRunProfileForm(sc Script) {
	a.form.profileName = sc.defaultProfileName()
	args := sc.Args
	env := sc.Env
	cwd := sc.Cwd
	timeout := sc.Timeout
	admin := sc.RunAsAdmin
	if p, ok := sc.profileByID(a.form.profileID); ok {
		a.form.profileName = p.Name
		args, env, cwd, timeout, admin = p.Args, p.Env, p.Cwd, p.Timeout, p.RunAsAdmin
	}
	a.form.args = strings.Join(args, " ")
	a.form.env = formatEnv(env)
	a.form.cwd = cwd
	a.form.timeout = ""
	if timeout > 0 {
		a.form.timeout = fmt.Sprint(timeout)
	}
	a.form.admin = admin
}

// profileFromForm turns the current fields into a profile.
func (a *App) profileFromForm(name string) (RunProfile, error) {
	env, err := parseEnvLines(a.form.env)
	if err != nil {
		return RunProfile{}, err
	}
	timeout := 0
	if v := strings.TrimSpace(a.form.timeout); v != "" {
		if _, err := fmt.Sscan(v, &timeout); err != nil || timeout < 0 {
			return RunProfile{}, fmt.Errorf("超时必须是 0 或正整数")
		}
	}
	return RunProfile{
		ID:         newID("p"),
		Name:       name,
		Args:       splitArgs(a.form.args),
		Env:        env,
		Cwd:        strings.TrimSpace(a.form.cwd),
		Timeout:    timeout,
		RunAsAdmin: a.form.admin,
	}, nil
}

// saveRunProfileCopy stores the current fields as a new named profile.
func (a *App) saveRunProfileCopy(sc Script) {
	name := strings.TrimSpace(a.form.profileName)
	if name == "" || (name == tr("默认") && sc.DefaultName == "") {
		name = nextProfileName(sc.Profiles)
	}
	if name == sc.defaultProfileName() || profileNameTaken(sc.Profiles, name, a.form.profileID) {
		a.form.err = "已经有同名的配置"
		return
	}
	profile, err := a.profileFromForm(name)
	if err != nil {
		a.form.err = err.Error()
		return
	}
	sc.Profiles = append(sc.Profiles, profile)
	sc.ProfileID = profile.ID
	if err := a.store.UpdateScript(sc); err != nil {
		a.form.err = err.Error()
		return
	}
	a.form.err = ""
	a.form.profileID = profile.ID
	a.form.profileName = profile.Name
	a.showNotice("已另存为配置「" + profile.Name + "」")
}

func (a *App) deleteRunProfile(sc Script) {
	keep := make([]RunProfile, 0, len(sc.Profiles))
	for _, p := range sc.Profiles {
		if p.ID != a.form.profileID {
			keep = append(keep, p)
		}
	}
	sc.Profiles = keep
	sc.ProfileID = ""
	if err := a.store.UpdateScript(sc); err != nil {
		a.form.err = err.Error()
		return
	}
	a.form.profileID = ""
	a.fillRunProfileForm(sc)
	a.showNotice("已删除这个配置，回到默认")
}

func (a *App) submitForm() {
	a.form.err = ""
	switch a.form.kind {
	case dialogScript:
		a.createScript()
	case dialogFolder:
		name := strings.TrimSpace(a.form.name)
		if _, err := a.store.CreateFolder(a.form.folderID, name); err != nil {
			a.form.err = err.Error()
			return
		}
		a.folderOpen[a.form.folderID] = true
		a.form.open = false
		a.showNotice("文件夹已创建")
	case dialogRename:
		name := strings.TrimSpace(a.form.name)
		var err error
		if a.form.folderTarget {
			err = a.store.RenameFolder(a.form.targetID, name)
		} else {
			err = a.store.RenameScript(a.form.targetID, name, a.form.renameEntry)
		}
		if err != nil {
			a.form.err = err.Error()
			return
		}
		if !a.form.folderTarget {
			// The entry may have been renamed with the script, so no cached
			// path of it can be trusted any more.
			a.forgetScript(a.form.targetID)
		}
		a.form.open = false
		a.showNotice("已重命名")
	case dialogInterpreter:
		a.saveInterpreter()
	case dialogScriptOptions:
		a.saveScriptOptions()
	case dialogRunArgs:
		a.saveRunArgs()
	case dialogImport:
		a.doImportTarget()
	case dialogRemoteHost:
		a.saveHost()
	case dialogRemote:
		a.saveRemote(a.form.path)
	case dialogNewFile:
		a.createFile()
	case dialogNewDir:
		a.createDir()
	case dialogRenameFile:
		a.renameFile()
	case dialogEntry:
		a.saveEntryChoice()
	}
}

func (a *App) createScript() {
	name := strings.TrimSpace(a.form.name)
	lang := a.form.lang
	if !hasString(languageOrder, lang) {
		a.form.err = "请选择语言"
		return
	}
	// The interpreter is not chosen here: it depends on where the script
	// runs, and it follows the run target when the script is started.
	sc, err := a.store.CreateScript(a.form.folderID, name, lang, languageExt(lang), "")
	if err != nil {
		a.form.err = err.Error()
		return
	}
	if starter := starterContent(lang, name); starter != "" {
		if _, err := a.store.WriteScript(sc.ID, starter); err != nil {
			a.form.err = err.Error()
			return
		}
	}
	a.folderOpen[a.form.folderID] = true
	a.selected = sc.ID
	a.form.open = false
	a.router.Push("/scripts/" + sc.ID)
	a.showNotice("脚本已创建")
}

func (a *App) saveInterpreter() {
	name := strings.TrimSpace(a.form.name)
	path := strings.TrimSpace(a.form.path)
	if name == "" {
		a.form.err = "名称不能为空"
		return
	}
	if path == "" {
		a.form.err = "可执行文件不能为空"
		return
	}
	lang := a.form.lang
	if lang == "" {
		lang = languageOrder[0]
	}
	args := splitArgs(a.form.args)
	probe := splitArgs(a.form.probe)
	if len(probe) == 0 {
		probe = []string{"--version"}
	}
	if a.form.targetID == "" {
		it, err := a.store.CreateInterpreter(Interpreter{Name: name, Language: lang, Path: path, Args: args, ProbeArgs: probe})
		if err != nil {
			a.form.err = err.Error()
			return
		}
		Probe(&it)
		_ = a.store.UpdateInterpreter(it)
	} else {
		it, ok := a.store.Interpreter(a.form.targetID)
		if !ok {
			a.form.err = "解释器不存在"
			return
		}
		it.Name, it.Language, it.Path = name, lang, path
		it.Args, it.ProbeArgs = args, probe
		Probe(&it)
		if err := a.store.UpdateInterpreter(it); err != nil {
			a.form.err = err.Error()
			return
		}
	}
	a.form.open = false
	a.showNotice("解释器已保存")
}

func (a *App) saveScriptOptions() {
	sc, ok := a.store.Script(a.form.targetID)
	if !ok {
		return
	}
	sc.Dangerous = a.form.dangerous
	sc.Preflight = a.form.preflight
	if err := a.store.UpdateScript(sc); err != nil {
		a.form.err = err.Error()
		return
	}
	a.form.open = false
	a.showNotice("脚本设置已保存")
}

func (a *App) saveRunArgs() {
	sc, ok := a.store.Script(a.form.targetID)
	if !ok {
		return
	}
	profile, err := a.profileFromForm(strings.TrimSpace(a.form.profileName))
	if err != nil {
		a.form.err = err.Error()
		return
	}
	if a.form.profileID == "" {
		sc.Args = profile.Args
		sc.Env = profile.Env
		sc.Cwd = profile.Cwd
		sc.Timeout = profile.Timeout
		sc.RunAsAdmin = profile.RunAsAdmin
		sc.ProfileID = ""
		// The default set takes a name like any other; empty falls back to 默认.
		sc.DefaultName = strings.TrimSpace(a.form.profileName)
	} else {
		name := strings.TrimSpace(a.form.profileName)
		if name == "" {
			a.form.err = "配置名称不能为空"
			return
		}
		if profileNameTaken(sc.Profiles, name, a.form.profileID) {
			a.form.err = "已经有同名的配置"
			return
		}
		found := false
		for i := range sc.Profiles {
			if sc.Profiles[i].ID != a.form.profileID {
				continue
			}
			profile.ID = sc.Profiles[i].ID
			profile.Name = name
			sc.Profiles[i] = profile
			found = true
			break
		}
		if !found {
			a.form.err = "配置已经不存在"
			return
		}
		sc.ProfileID = profile.ID
	}
	if err := a.store.UpdateScript(sc); err != nil {
		a.form.err = err.Error()
		return
	}
	a.form.open = false
	a.showNotice("运行参数已保存")
}

func (a *App) formSubmitLabel() string {
	if a.form.kind == dialogImport {
		return tr("导入")
	}
	return tr("保存")
}

func (a *App) formField(c *ui.Context, label string, control func()) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, tr(label)).FontSize(12).TextColor(t.TextMuted).Width(104).Shrink(0).Wrap()
		ui.Row(c).Grow(1).Gap(6).AlignItems(ui.Center).Children(control)
	})
}

func (a *App) hostForm(c *ui.Context) {
	t := c.Theme()
	a.formField(c, "名称", func() {
		ui.TextInput(c, &a.form.name).Placeholder(tr("例如 生产服务器")).Grow(1)
	})
	a.formField(c, "地址", func() {
		ui.TextInput(c, &a.form.target).Placeholder(tr("user@host 或 ~/.ssh/config 里的别名")).Grow(1)
	})
	a.formField(c, "端口", func() {
		ui.TextInput(c, &a.form.port).Width(80)
		ui.Text(c, tr("留空用 ssh 配置，默认 22")).FontSize(11).TextColor(t.TextMuted)
	})
	a.formField(c, "备注", func() {
		ui.TextInput(c, &a.form.note).Placeholder(tr("可选")).Grow(1)
	})
	ui.Text(c, tr("认证完全交给系统 ssh：先用终端确认能登录，密钥、ssh-agent、跳板机都沿用 ~/.ssh/config。应用不保存密码或私钥，远程执行只支持 Linux。")).
		FontSize(11).TextColor(t.TextMuted).Wrap()
}

func (a *App) openHostForm(h *RemoteHost) {
	f := formState{kind: dialogRemoteHost, open: true, title: "添加连接"}
	if h != nil {
		f.title = "编辑连接"
		f.targetID = h.ID
		f.name = h.Name
		f.target = h.Target
		f.note = h.Note
		if h.Port > 0 {
			f.port = fmt.Sprint(h.Port)
		}
	}
	a.form = f
	a.requestUpdate()
}

func (a *App) saveHost() {
	name := strings.TrimSpace(a.form.name)
	target := strings.TrimSpace(a.form.target)
	note := strings.TrimSpace(a.form.note)
	if name == "" {
		a.form.err = "名称不能为空"
		return
	}
	for _, old := range a.remotes.Hosts() {
		if old.ID != a.form.targetID && strings.EqualFold(old.Name, name) {
			a.form.err = "已经有同名的连接"
			return
		}
	}
	port := 0
	if v := strings.TrimSpace(a.form.port); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			a.form.err = "端口必须是 1 到 65535 之间的数字"
			return
		}
		port = n
	}
	if a.form.targetID == "" {
		h, err := a.remotes.CreateHost(RemoteHost{Name: name, Target: target, Port: port, Note: note})
		if err != nil {
			a.form.err = err.Error()
			return
		}
		a.remoteSel = h.ID
	} else {
		h, ok := a.remotes.Host(a.form.targetID)
		if !ok {
			a.form.err = "连接不存在"
			return
		}
		h.Name, h.Target, h.Port, h.Note = name, target, port, note
		if err := a.remotes.UpdateHost(h); err != nil {
			a.form.err = err.Error()
			return
		}
		a.remoteSel = h.ID
	}
	a.form.open = false
	a.showNotice("连接已保存")
}

func (a *App) openNewScript(folderID string) {
	a.form = formState{kind: dialogScript, open: true, title: "新建脚本", folderID: folderID}
	a.requestUpdate()
}

func (a *App) openNewFolder(parentID string) {
	a.form = formState{kind: dialogFolder, open: true, title: "新建文件夹", folderID: parentID}
	a.requestUpdate()
}

func (a *App) openRename(kind dialogKind, id, name, title string) {
	folder := kind == dialogFolder
	f := formState{kind: dialogRename, open: true, title: title, targetID: id, name: name, folderTarget: folder}
	if !folder {
		if sc, ok := a.store.Script(id); ok {
			f.renameEntry = !a.store.IsMultiFile(sc)
		}
	}
	a.form = f
	a.requestUpdate()
}

func (a *App) openInterpreter(it *Interpreter) {
	f := formState{kind: dialogInterpreter, open: true, title: "添加解释器"}
	if it != nil {
		f.title = "编辑解释器"
		f.targetID = it.ID
		f.name = it.Name
		f.path = it.Path
		f.args = strings.Join(it.Args, " ")
		f.probe = strings.Join(it.ProbeArgs, " ")
		f.lang = it.Language
	} else {
		f.lang = languageOrder[0]
		f.probe = "--version"
	}
	a.form = f
	a.requestUpdate()
}

func (a *App) openScriptOptions(sc Script) {
	f := formState{kind: dialogScriptOptions, open: true, title: "脚本设置", targetID: sc.ID}
	f.dangerous = sc.Dangerous
	f.preflight = sc.Preflight
	a.form = f
	a.requestUpdate()
}

// openRunArgs edits only the extra arguments, which keep living next to the
// interpreter selector in the header.
func (a *App) openRunArgs(sc Script) {
	profileID := sc.ProfileID
	if _, ok := sc.activeProfile(); !ok {
		profileID = ""
	}
	a.form = formState{kind: dialogRunArgs, open: true, title: "运行参数", targetID: sc.ID,
		profileID: profileID}
	a.fillRunProfileForm(sc)
	a.requestUpdate()
}

func (a *App) folderChoices() []Folder {
	folders := a.store.Folders()
	sort.SliceStable(folders, func(i, j int) bool { return folders[i].Name < folders[j].Name })
	return append([]Folder{{ID: "", Name: "根目录"}}, folders...)
}

func (a *App) folderPath(id string) string {
	if id == "" {
		return "脚本库根目录"
	}
	var parts []string
	seen := 0
	for id != "" && seen < 32 {
		f, ok := a.store.Folder(id)
		if !ok {
			break
		}
		parts = append([]string{f.Name}, parts...)
		id = f.ParentID
		seen++
	}
	return strings.Join(parts, " / ")
}

func (a *App) interpretersFor(language string) []Interpreter {
	var out []Interpreter
	for _, it := range a.store.Interpreters() {
		if it.State != StateOK {
			continue
		}
		// A plain-text entry has no language of its own; any interpreter may
		// be bound to it, and the runner checks the entry's extension.
		if language == "plain" || it.Language == language {
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// splitArgs splits a command line into arguments, honoring single quotes,
// double quotes and backslash escapes, so an argument may contain spaces.
func splitArgs(s string) []string {
	var out []string
	var b strings.Builder
	var quote rune
	escaped := false
	started := false
	flush := func() {
		if started {
			out = append(out, b.String())
			b.Reset()
			started = false
		}
	}
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			started = true
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			started = true
		case ' ', '\t', '\n':
			flush()
		default:
			b.WriteRune(r)
			started = true
		}
	}
	flush()
	return out
}

func hasString(list []string, value string) bool {
	for _, s := range list {
		if s == value {
			return true
		}
	}
	return false
}

// openNewFile asks for a new file inside a script, under dirPrefix.
func (a *App) openNewFile(sc Script, dirPrefix string) {
	rel := ""
	if dirPrefix != "" {
		rel = dirPrefix + "/"
	}
	a.form = formState{kind: dialogNewFile, open: true, title: "新建文件", targetID: sc.ID, rel: rel}
	a.requestUpdate()
}

// openNewDir asks for a new directory inside a script.
func (a *App) openNewDir(sc Script, dirPrefix string) {
	rel := ""
	if dirPrefix != "" {
		rel = dirPrefix + "/"
	}
	a.form = formState{kind: dialogNewDir, open: true, title: "新建子目录", targetID: sc.ID, rel: rel}
	a.requestUpdate()
}

func (a *App) openRenameFile(sc Script, rel string) {
	a.form = formState{kind: dialogRenameFile, open: true, title: "重命名文件", targetID: sc.ID, rel: rel, name: rel}
	a.requestUpdate()
}

// openEntryPicker lets the user say which imported file runs.
func (a *App) openEntryPicker(scriptID string) {
	choices := a.store.ImportEntryChoices(scriptID)
	if len(choices) == 0 {
		return
	}
	idx := 0
	if sc, ok := a.store.Script(scriptID); ok {
		for i, rel := range choices {
			if rel == sc.Entry {
				idx = i
				break
			}
		}
	}
	a.form = formState{kind: dialogEntry, open: true, title: "选择入口文件",
		targetID: scriptID, entryChoices: choices, entryIdx: idx}
	a.requestUpdate()
}

func (a *App) createFile() {
	sc, ok := a.store.Script(a.form.targetID)
	if !ok {
		return
	}
	rel := strings.TrimSpace(a.form.rel)
	if rel == "" {
		a.form.err = "请填写文件路径"
		return
	}
	if err := a.store.AddScriptFile(sc.ID, rel, ""); err != nil {
		a.form.err = err.Error()
		return
	}
	a.invalidateScriptCache(sc.ID)
	a.form.open = false
	a.openScriptFile(sc, rel)
	a.showNotice("已新建 " + rel)
}

func (a *App) createDir() {
	rel := strings.TrimSpace(a.form.rel)
	if rel == "" {
		a.form.err = "请填写目录路径"
		return
	}
	if err := a.store.MkdirScript(a.form.targetID, rel); err != nil {
		a.form.err = err.Error()
		return
	}
	a.treeOpen[rel] = true
	a.form.open = false
	a.showNotice("已新建目录 " + rel)
}

func (a *App) renameFile() {
	sc, ok := a.store.Script(a.form.targetID)
	if !ok {
		return
	}
	old := a.form.name
	next := strings.TrimSpace(a.form.rel)
	if next == "" || next == old {
		a.form.open = false
		return
	}
	if st, ok := a.editors[editorKey(sc.ID, old)]; ok && st.Buffer.Modified() {
		if err := a.writeEditorFile(sc, old, st.Buffer.String()); err != nil {
			a.form.err = "先保存这个文件再重命名: " + err.Error()
			return
		}
		st.Buffer.MarkSaved()
	}
	if err := a.store.RenameScriptFile(sc.ID, old, next); err != nil {
		a.form.err = err.Error()
		return
	}
	a.invalidateScriptCache(sc.ID)
	a.closeScriptFile(sc, old)
	a.openScriptFile(sc, next)
	a.form.open = false
	a.showNotice("已重命名为 " + next)
}

func (a *App) saveEntryChoice() {
	if a.form.entryIdx < 0 || a.form.entryIdx >= len(a.form.entryChoices) {
		a.form.err = "请选择入口文件"
		return
	}
	rel := a.form.entryChoices[a.form.entryIdx]
	if err := a.store.SetEntry(a.form.targetID, rel); err != nil {
		a.form.err = err.Error()
		return
	}
	a.form.open = false
	a.openScriptFileByID(a.form.targetID, rel)
	a.showNotice("入口已设为 " + rel)
}

func (a *App) openScriptFileByID(scriptID, rel string) {
	if sc, ok := a.store.Script(scriptID); ok {
		a.selected = sc.ID
		a.router.Push("/scripts/" + sc.ID)
		a.openScriptFile(sc, rel)
	}
}
