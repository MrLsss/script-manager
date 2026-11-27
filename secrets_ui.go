package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
)

// secretsModal manages the credentials a script's environment refers to with
// ${secret:name}. It is its own modal rather than a form kind, so opening it
// from the run parameters dialog never drops what the user was editing.
func (a *App) secretsModal(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.secretsOpen, func() {
		ui.Column(c).Width(480).Padding(20).Gap(12).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, lucide.KeyRound()).Width(16).Height(16).TextColor(t.TextMuted)
				ui.Text(c, tr("密钥")).FontSize(16).FontWeight(600)
			})
			ui.Text(c, tr("密钥只存在本机的密钥库里，不写进脚本文件，也不随「同步」上传。环境变量里用 ${secret:名称} 引用。")).
				FontSize(11.5).TextColor(t.TextMuted).Wrap()
			names := a.secrets.List()
			if len(names) == 0 {
				ui.Text(c, tr("还没有保存任何密钥。")).FontSize(12).TextColor(t.TextMuted)
			} else {
				ui.Column(c).Gap(2).Children(func() {
					for _, name := range names {
						a.secretRow(c, name)
					}
				})
			}
			ui.Divider(c)
			a.formField(c, "名称", func() {
				ui.TextInput(c, &a.secretName).Placeholder("github-token").Grow(1)
			})
			a.formField(c, "值", func() {
				ui.TextInput(c, &a.secretValue).Placeholder(tr("密钥内容")).Grow(1)
			})
			if a.secretErr != "" {
				ui.Text(c, a.secretErr).FontSize(11.5).TextColor(t.Danger).Wrap()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, tr("完成")).Clicked() {
					a.secretsOpen = false
					a.secretName, a.secretValue, a.secretErr = "", "", ""
				}
				if ui.PrimaryButton(c, tr("保存密钥")).Clicked() {
					a.saveSecret()
				}
			})
		})
	})
}

// secretRow lists one stored secret by name; its value is never shown.
func (a *App) secretRow(c *ui.Context, name string) {
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(6, 8).Radius(6).Background(t.Surface).Gap(8).
		AlignItems(ui.Center).Children(func() {
		ui.Icon(c, lucide.Key()).Width(13).Height(13).TextColor(t.TextMuted)
		ui.Text(c, name).FontSize(12).TextColor(t.Text).Grow(1).SingleLine()
		ui.Text(c, "••••••••").FontSize(12).TextColor(t.TextMuted)
		if ui.Button(c, tr("删除")).Padding(1, 8).Clicked() {
			if err := a.secrets.Delete(name); err != nil {
				a.secretErr = err.Error()
				return
			}
			a.showNotice("已删除密钥 " + name)
		}
	})
}

// saveSecret stores a credential and clears the fields for the next one.
func (a *App) saveSecret() {
	name := strings.TrimSpace(a.secretName)
	if name == "" {
		a.secretErr = "密钥名称不能为空"
		return
	}
	if !validSecretName(name) {
		a.secretErr = "密钥名称只能用字母、数字、点、横线和下划线"
		return
	}
	if a.secretValue == "" {
		a.secretErr = "密钥内容不能为空"
		return
	}
	if err := a.secrets.Set(name, a.secretValue); err != nil {
		a.secretErr = err.Error()
		return
	}
	a.secretErr = ""
	a.secretName, a.secretValue = "", ""
	a.showNotice("已保存密钥 " + name)
}

func (a *App) openSecrets() {
	a.secretName, a.secretValue, a.secretErr = "", "", ""
	a.secretsOpen = true
	a.requestUpdate()
}
