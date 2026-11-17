package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

// connectionsPage manages the Linux hosts scripts can run on.
func (a *App) connectionsPage(c *ui.Context) {
	t := c.Theme()
	hosts := a.remotes.Hosts()
	ui.Column(c).Grow(1).Padding(16).Gap(12).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, tr("%d 个连接"), len(hosts)).FontSize(12).TextColor(t.TextMuted)
			if a.remoteBusy.Load() {
				ui.Spinner(c).Width(14).Height(14)
				ui.Text(c, tr("正在检测…")).FontSize(12).TextColor(t.TextMuted)
			}
			ui.Spacer(c)
			if ui.Button(c, tr("添加连接")).Padding(2, 10).Clicked() {
				a.openHostForm(nil)
			}
		})
		ui.Text(c, tr("远程执行只支持 Linux 服务器。认证使用本机的 ssh、ssh-agent 和 ~/.ssh/config，应用不保存密码或私钥。")).
			FontSize(11.5).TextColor(t.TextMuted).Wrap()
		if len(hosts) == 0 {
			a.emptyConnections(c)
			return
		}
		if a.remoteSel == "" || !hostFound(hosts, a.remoteSel) {
			a.remoteSel = hosts[0].ID
		}
		ui.Split(c, &a.connW, func() {
			a.connectionList(c, hosts)
		}, func() {
			if h, ok := a.remotes.Host(a.remoteSel); ok {
				a.connectionDetail(c, h)
			}
		}).Grow(1).FillWidth().SplitReset(320)
	})
}

func hostFound(hosts []RemoteHost, id string) bool {
	for _, h := range hosts {
		if h.ID == id {
			return true
		}
	}
	return false
}

func (a *App) emptyConnections(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(12).Children(func() {
		ui.Icon(c, lucide.Server()).Width(40).Height(40).TextColor(t.TextMuted)
		ui.Text(c, tr("还没有远程连接")).FontSize(16).FontWeight(600)
		ui.Text(c, tr("先确保本机终端能 ssh 登录目标 Linux 服务器，再在这里添加它。")).
			FontSize(12).TextColor(t.TextMuted)
		if ui.PrimaryButton(c, tr("添加连接")).Clicked() {
			a.openHostForm(nil)
		}
	})
}

func (a *App) connectionList(c *ui.Context, hosts []RemoteHost) {
	t := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).Background(t.Background).Children(func() {
		ui.Row(c).Height(38).PaddingX(12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("远程服务器")).FontSize(12).FontWeight(600)
			ui.Spacer(c)
			ui.Textf(c, tr("%d 台"), len(hosts)).FontSize(11).TextColor(t.TextMuted)
		})
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Padding(6).Gap(2).Children(func() {
				for _, h := range hosts {
					a.connectionRow(c, h)
				}
			})
		})
	})
}

func (a *App) connectionRow(c *ui.Context, h RemoteHost) {
	t := c.Theme()
	bg := t.Background
	if h.ID == a.remoteSel {
		bg = t.Surface
	}
	ui.Column(c).FillWidth().Padding(8).Radius(6).Gap(2).Background(bg).
		OnClick(func() { a.remoteSel = h.ID }).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(6).Height(6).Radius(3).Background(hostStateColor(t, h.Status))
			ui.Text(c, h.Name).FontSize(12.5).TextColor(t.Text).SingleLine().Grow(1)
		})
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, h.Target).FontSize(11).TextColor(t.TextMuted).SingleLine()
			ui.Text(c, "·").FontSize(11).TextColor(t.TextMuted)
			ui.Text(c, hostStateText(h.Status)).FontSize(11).TextColor(hostStateColor(t, h.Status))
		})
	})
}

func (a *App) connectionDetail(c *ui.Context, h RemoteHost) {
	t := c.Theme()
	list := a.remotes.Interpreters(h.ID)
	ui.Column(c).Grow(1).MinWidth(0).Padding(16).Gap(12).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, h.Name).FontSize(16).FontWeight(600).SingleLine()
			ui.Text(c, hostStateText(h.Status)).FontSize(11.5).TextColor(hostStateColor(t, h.Status))
			ui.Spacer(c)
			if ui.Button(c, tr("检测连接")).Padding(2, 10).
				Disabled(a.remoteBusy.Load()).Clicked() {
				a.checkHost(h)
			}
			if ui.Button(c, tr("检测解释器")).Padding(2, 10).
				Disabled(a.remoteBusy.Load()).Clicked() {
				a.detectHostInterpreters(h)
			}
			if ui.Button(c, tr("编辑")).Padding(2, 10).Clicked() {
				a.openHostForm(&h)
			}
			if ui.Button(c, tr("删除")).Padding(2, 10).Clicked() {
				a.deleteHost(h)
			}
		})
		ui.Column(c).Gap(6).Children(func() {
			a.metaLine(c, "地址", h.Target)
			if h.Port > 0 {
				a.metaLine(c, "端口", fmt.Sprint(h.Port))
			}
			a.metaLine(c, "上次检测", fmtAgo(h.CheckedAt))
			if h.Note != "" {
				a.metaLine(c, "备注", h.Note)
			}
			if h.Problem != "" {
				ui.Text(c, h.Problem).FontSize(11.5).TextColor(t.Danger).Wrap()
			}
		})
		ui.Divider(c)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("远程解释器")).FontSize(12.5).FontWeight(600)
			ui.Text(c, tr("检测结果会记在本机，运行时优先使用默认项")).FontSize(11).TextColor(t.TextMuted)
		})
		if len(list) == 0 {
			ui.Text(c, tr("还没有检测到解释器。")+hostEmptyHint(h)).
				FontSize(12).TextColor(t.TextMuted)
			return
		}
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				for _, it := range list {
					a.remoteInterpRow(c, h, list, it)
				}
			})
		})
	})
}

func hostEmptyHint(h RemoteHost) string {
	switch h.Status {
	case HostStateUnknown:
		return "先点「检测解释器」。"
	case HostStateUnreachable, HostStateAuth:
		return "连接还不可用，先点「检测连接」确认地址与密钥。"
	case HostStateNotLinux:
		return "远程执行只支持 Linux。"
	}
	return "可以在服务器上安装所需的解释器后重新检测。"
}

func (a *App) remoteInterpRow(c *ui.Context, h RemoteHost, list []RemoteInterp, it RemoteInterp) {
	t := c.Theme()
	defaultPath := h.Preferred[it.Language]
	if defaultPath == "" {
		for _, other := range list {
			if other.Language == it.Language && other.State == RemoteStateOK {
				defaultPath = other.Path
				break
			}
		}
	}
	isDefault := defaultPath == it.Path && it.State == RemoteStateOK
	ui.Row(c).FillWidth().Padding(7, 8).Radius(6).Background(t.Surface).Gap(8).
		AlignItems(ui.Center).Children(func() {
		ui.Text(c, languageName(it.Language)).FontSize(12).Width(90).Shrink(0)
		ui.Text(c, orDash(it.Version)).FontSize(12).TextColor(t.TextMuted).Width(70).Shrink(0)
		ui.Text(c, it.Name).FontSize(12).TextColor(t.Text).Width(80).Shrink(0).SingleLine()
		ui.Text(c, it.Path).FontSize(11).TextColor(t.TextMuted).SingleLine().Grow(1)
		ui.Text(c, remoteStateText(it.State)).FontSize(11).
			TextColor(remoteStateColor(t, it.State)).Shrink(0)
		if isDefault {
			ui.Text(c, tr("默认")).FontSize(10.5).TextColor(t.Accent).Shrink(0)
		} else if it.State == RemoteStateOK {
			if ui.Button(c, tr("设为默认")).Padding(1, 8).Clicked() {
				if err := a.remotes.SetPreferred(h.ID, it.Language, it.Path); err != nil {
					a.alertError("保存失败", err.Error())
					return
				}
				a.showNotice("已把 " + it.Name + " 设为 " + languageName(it.Language) + " 的默认解释器")
			}
		}
	})
}

// checkHost tests the connection and the Linux requirement.
func (a *App) checkHost(h RemoteHost) {
	if !a.remoteBusy.CompareAndSwap(false, true) {
		return
	}
	a.requestUpdate()
	go func() {
		updated := CheckRemoteHost(h)
		_ = a.remotes.UpdateHost(updated)
		a.updateUI(func() {
			a.remoteBusy.Store(false)
			a.requestUpdate()
			if updated.Status == HostStateOK {
				a.showNotice("连接正常：" + updated.Name)
				return
			}
			a.showNotice(updated.Problem)
		})
	}()
}

// detectHostInterpreters probes every candidate in one ssh call and stores the
// result for the run path to use without asking again.
func (a *App) detectHostInterpreters(h RemoteHost) {
	if !a.remoteBusy.CompareAndSwap(false, true) {
		return
	}
	a.requestUpdate()
	go func() {
		updated, list, err := ProbeRemote(h)
		_ = a.remotes.UpdateHost(updated)
		if err == nil {
			_ = a.remotes.SetInterpreters(h.ID, list)
		}
		a.updateUI(func() {
			a.remoteBusy.Store(false)
			a.requestUpdate()
			if err != nil {
				a.alertError("检测失败", err.Error())
				return
			}
			a.showNotice(trf("在 %s 上发现 %d 个解释器", updated.Name, len(list)))
		})
	}()
}

func (a *App) deleteHost(h RemoteHost) {
	a.confirmAction("删除连接",
		"「"+h.Name+"」以及它的解释器检测结果会从本机删除。使用它的脚本会改回在本机运行。",
		[]string{"取消", "删除"}, func(i int) {
			if i != 1 {
				return
			}
			for _, sc := range a.store.Scripts() {
				if sc.HostID != h.ID {
					continue
				}
				sc.HostID = ""
				_ = a.store.UpdateScript(sc)
			}
			if err := a.remotes.DeleteHost(h.ID); err != nil {
				a.alertError("删除失败", err.Error())
				return
			}
			a.remoteSel = ""
			a.showNotice("已删除连接 " + h.Name)
		})
}

func hostStateText(state string) string {
	switch state {
	case HostStateOK:
		return tr("可用")
	case HostStateUnreachable:
		return tr("无法连接")
	case HostStateAuth:
		return tr("认证失败")
	case HostStateNotLinux:
		return tr("不是 Linux")
	}
	return tr("未检测")
}

func hostStateColor(t *ui.Theme, state string) ui.Color {
	switch state {
	case HostStateOK:
		return t.Success
	case HostStateUnreachable, HostStateAuth, HostStateNotLinux:
		return t.Danger
	}
	return t.TextMuted
}

func remoteStateText(state string) string {
	switch state {
	case RemoteStateOK:
		return tr("可用")
	case RemoteStateMissing:
		return tr("不存在")
	case RemoteStateNoExec:
		return tr("没有执行权限")
	case RemoteStateProbeFail:
		return tr("版本检测失败")
	}
	return tr("未知")
}

func remoteStateColor(t *ui.Theme, state string) ui.Color {
	switch state {
	case RemoteStateOK:
		return t.Success
	case RemoteStateMissing, RemoteStateNoExec, RemoteStateProbeFail:
		return t.Danger
	}
	return t.TextMuted
}
