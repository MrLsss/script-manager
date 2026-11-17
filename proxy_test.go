package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestApplyProxyEnvReplacesExistingKeys(t *testing.T) {
	env := []string{"PATH=/bin", "HTTPS_PROXY=http://old:1", "https_proxy=http://old:1", "HOME=/tmp"}
	got := applyProxyEnv(env, true, "http://127.0.0.1:7890")
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "old:1") {
		t.Fatalf("old proxy survived:\n%s", joined)
	}
	for _, key := range []string{"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "http_proxy="} {
		if !strings.Contains(joined, key+"http://127.0.0.1:7890") {
			t.Fatalf("missing %s in:\n%s", key, joined)
		}
	}
	if !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "HOME=/tmp") {
		t.Fatalf("unrelated variables dropped:\n%s", joined)
	}
}

func TestApplyProxyEnvOffKeepsEnv(t *testing.T) {
	env := []string{"HTTPS_PROXY=http://old:1"}
	got := applyProxyEnv(env, false, "http://127.0.0.1:7890")
	if len(got) != 1 || got[0] != "HTTPS_PROXY=http://old:1" {
		t.Fatalf("env changed while off: %v", got)
	}
}

func TestValidProxyURL(t *testing.T) {
	for _, ok := range []string{"http://127.0.0.1:7890", "https://proxy.example.com:443", "socks5://127.0.0.1:1080"} {
		if err := validProxyURL(ok); err != nil {
			t.Fatalf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "127.0.0.1:7890", "ftp://127.0.0.1:21", "http://"} {
		if err := validProxyURL(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestRunnerUsesProxyEnv(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	folder, err := s.CreateFolder("", "运维")
	if err != nil {
		t.Fatal(err)
	}
	sc, err := s.CreateScript(folder.ID, "代理", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteScript(sc.ID, "echo \"proxy=$HTTPS_PROXY\"\n"); err != nil {
		t.Fatal(err)
	}
	r := NewRunner(s)
	r.SetProxy(true, "http://127.0.0.1:7890")
	run, err := r.Start(sc, it, 5*time.Second)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := waitRun(t, s, run.ID)
	if done.Status != RunOK {
		t.Fatalf("status = %s", done.Status)
	}
	data, err := os.ReadFile(filepath.Join(s.Root(), done.LogPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "proxy=http://127.0.0.1:7890") {
		t.Fatalf("proxy missing in log:\n%s", data)
	}
}

func TestSyncEnvUsesProxy(t *testing.T) {
	s := newTestStore(t)
	sy := NewSync(s)
	sy.SetProxy(true, "http://127.0.0.1:7890")
	env := strings.Join(sy.env(), "\n")
	if !strings.Contains(env, "HTTPS_PROXY=http://127.0.0.1:7890") {
		t.Fatalf("git env missing proxy:\n%s", env)
	}
	sy.SetProxy(false, "http://127.0.0.1:7890")
	if strings.Contains(strings.Join(sy.env(), "\n"), "7890") {
		t.Fatalf("git env still has proxy after disabling")
	}
}

func TestSettingsShowsProxyRow(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(texts, "网络代理") || !strings.Contains(texts, "网络") {
		t.Fatalf("missing proxy row in settings:\n%s", texts)
	}
	if strings.Contains(texts, "Git 拉取与推送也使用它") {
		t.Fatalf("explanation should live in the tooltip, not the row:\n%s", texts)
	}
}

func TestEnablingProxyAppliesToRunner(t *testing.T) {
	a, _, _ := newTestApp(t)
	a.settings.ProxyURL = "http://127.0.0.1:1087"
	a.settings.ProxyEnabled = true
	a.applyProxySettings()
	if a.proxyErr != "" {
		t.Fatalf("proxy error: %s", a.proxyErr)
	}
	on, address := a.runner.proxyOn, a.runner.proxyURL
	if !on || address != "http://127.0.0.1:1087" {
		t.Fatalf("runner proxy = %v %q", on, address)
	}
	a.settings.ProxyURL = "nonsense"
	a.applyProxySettings()
	if a.proxyErr == "" {
		t.Fatalf("invalid address accepted")
	}
	if a.runner.proxyOn {
		t.Fatalf("runner still proxied with an invalid address")
	}
}
