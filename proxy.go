package main

import (
	"fmt"
	"net/url"
	"strings"
)

// defaultProxyURL is what the field starts with when the proxy is switched on
// without an address, matching the common local client ports.
const defaultProxyURL = "http://127.0.0.1:7890"

var proxyKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
	"http_proxy", "https_proxy", "all_proxy",
}

// applyProxyEnv replaces the proxy variables in env so scripts reach the
// network through the configured address.
func applyProxyEnv(env []string, on bool, address string) []string {
	address = strings.TrimSpace(address)
	if !on || address == "" {
		return env
	}
	out := make([]string, 0, len(env)+len(proxyKeys))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if isProxyKey(key) {
			continue
		}
		out = append(out, kv)
	}
	for _, key := range proxyKeys {
		out = append(out, key+"="+address)
	}
	return out
}

func isProxyKey(key string) bool {
	switch strings.ToLower(key) {
	case "http_proxy", "https_proxy", "all_proxy":
		return true
	}
	return false
}

// validProxyURL checks the address before it is handed to git and scripts.
func validProxyURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("请填写代理地址，例如 %s", defaultProxyURL)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("代理地址格式不正确")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("代理地址需要以 http:// 或 socks5:// 开头")
	}
	if u.Host == "" {
		return fmt.Errorf("代理地址缺少主机与端口")
	}
	return nil
}

// applyProxySettings hands the current address to the runner and to git. An
// address that does not parse leaves both without a proxy and shows the reason
// in the settings row.
// bindStore points the runner and sync at a store. They hold the store, so a
// library switch must rebuild them rather than leave them on the old one.
func (a *App) bindStore(store *Store) {
	a.store = store
	a.runner = NewRunner(store)
	a.runner.logLimit = int64(a.settings.MaxLogBytes)
	a.runner.SetRedaction(a.settings.RedactLogs)
	a.runner.onChange = a.requestUpdate
	a.runner.onFinish = func(run Run) { a.afterRun(run) }
	a.sync = NewSync(store)
}

func (a *App) applyProxySettings() {
	address := strings.TrimSpace(a.settings.ProxyURL)
	on := a.settings.ProxyEnabled
	a.proxyErr = ""
	if on {
		if err := validProxyURL(address); err != nil {
			a.proxyErr = err.Error()
			on = false
		}
	}
	a.runner.SetProxy(on, address)
	a.sync.SetProxy(on, address)
}
