package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSplitArgsHonorsQuotesAndEscapes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`--yes -v`, []string{"--yes", "-v"}},
		{`--name "a b" 'c d'`, []string{"--name", "a b", "c d"}},
		{`a\ b`, []string{"a b"}},
		{`""`, []string{""}},
		{`a""b`, []string{"ab"}},
		{`"a\"b"`, []string{`a"b`}},
		{`  spaced   out  `, []string{"spaced", "out"}},
	}
	for _, tc := range cases {
		got := splitArgs(tc.in)
		if strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") {
			t.Fatalf("splitArgs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSuggestedConcurrencyIsBounded(t *testing.T) {
	n := suggestedConcurrency()
	if n < 1 || n > 8 {
		t.Fatalf("suggestedConcurrency = %d", n)
	}
}

func TestConcurrencyLimitRefusesAnotherRun(t *testing.T) {
	a, store, sc := newTestApp(t)
	long, _ := store.Script(sc.ID)
	if _, err := store.WriteScript(sc.ID, "sleep 5\n"); err != nil {
		t.Fatal(err)
	}
	it, ok := store.Interpreter(long.InterpID)
	if !ok {
		t.Fatal("interpreter missing")
	}
	run, err := a.runner.Start(long, it, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = a.runner.Stop(run.ID)
		waitRun(t, store, run.ID)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && a.runner.ActiveCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	a.settings.Concurrency = 1
	if _, err := a.reserveRun(); err == nil {
		t.Fatal("a full concurrency limit should refuse another run")
	}
	a.settings.Concurrency = 2
	release, err := a.reserveRun()
	if err != nil {
		t.Fatalf("under the limit: %v", err)
	}
	release()
}

func TestSearchCoversEntryFolderAndLog(t *testing.T) {
	a, store, sc := newTestApp(t)
	// The test script lives in folder 运维 and is called 问候.
	if !a.scriptMatches(sc, "运维") {
		t.Fatal("folder name should match")
	}
	if !a.scriptMatches(sc, "问候.sh") {
		t.Fatal("entry name should match")
	}
	if _, err := store.WriteScript(sc.ID, "echo needle-log-output\n"); err != nil {
		t.Fatal(err)
	}
	it, _ := store.Interpreter(sc.InterpID)
	run, err := a.runner.Start(sc, it, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, store, run.ID)
	if done.Status != RunOK {
		t.Fatalf("run = %+v", done)
	}
	if !a.scriptMatches(sc, "needle-log-output") {
		t.Fatal("log text should match the search")
	}
	if a.scriptMatches(sc, "no-such-text-anywhere") {
		t.Fatal("unrelated query matched")
	}
}

func TestFolderCountsCountTheWholeTreeOnce(t *testing.T) {
	a, store, _ := newTestApp(t)
	parent, err := store.CreateFolder("", "父")
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateFolder(parent.ID, "子")
	if err != nil {
		t.Fatal(err)
	}
	it := store.Interpreters()[0]
	if _, err := store.CreateScript(child.ID, "子脚本", "shell", "sh", it.ID); err != nil {
		t.Fatal(err)
	}
	counts := a.buildFolderCounts()
	if counts[parent.ID] != 1 || counts[child.ID] != 1 {
		t.Fatalf("counts = %v", counts)
	}
}

func TestTrayPanelListsAndSearchesScripts(t *testing.T) {
	a, store, sc := newTestApp(t)
	tt := ui.NewTester(a.trayPanelView, 400, 540)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"快速运行", sc.Name, sc.Entry, "运维", "运行"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("quick panel lacks %q:\n%s", want, texts)
		}
	}
	a.trayQuery = "运维"
	tt.Frame()
	if !tt.HasText(sc.Name) {
		t.Fatalf("folder search filtered the script away:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	a.trayQuery = "不存在"
	tt.Frame()
	if !tt.HasText("没有匹配的脚本") {
		t.Fatalf("empty search state missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	a.trayQuery = ""
	if _, err := store.WriteScript(sc.ID, "echo tray-log-needle\n"); err != nil {
		t.Fatal(err)
	}
	it, _ := store.Interpreter(sc.InterpID)
	run, err := a.runner.Start(sc, it, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if done := waitRun(t, store, run.ID); done.Status != RunOK {
		t.Fatalf("run = %+v", done)
	}
	a.trayQuery = "tray-log-needle"
	tt.Frame()
	if !tt.HasText(sc.Name) {
		t.Fatalf("log search did not surface the script:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if got := len(a.recentScripts(5)); got == 0 {
		t.Fatal("recent scripts should list the run")
	}
}

func TestVersionComparison(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.1.0", "v0.1.0", false},
		{"0.1.0", "v0.2.0", true},
		{"1.2.3", "v1.2.10", true},
		{"1.2.10", "v1.2.9", false},
		{"0.1.0", "0.1.1-rc1", true},
	}
	for _, tc := range cases {
		if got := versionNewer(tc.current, tc.latest); got != tc.want {
			t.Fatalf("versionNewer(%q, %q) = %v", tc.current, tc.latest, got)
		}
	}
	if validRepo("owner/repo") != true || validRepo("owner") || validRepo("a/b/c") {
		t.Fatal("validRepo is wrong")
	}
}

func TestBundleCarriesAndRebindsRemoteTarget(t *testing.T) {
	src := newTestStore(t)
	it := testInterpreter(t, src)
	sc, err := src.CreateScript("", "远程导出", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.WriteScript(sc.ID, "echo export\n"); err != nil {
		t.Fatal(err)
	}
	fresh, _ := src.Script(sc.ID)
	fresh.HostName = "生产机"
	fresh.HostTarget = "user@prod"
	if err := src.UpdateScript(fresh); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "host.zip")
	if err := ExportLibrary(src, target, ExportOptions{
		ScriptIDs: []string{sc.ID},
		Host: func(string) (string, string) {
			return "生产机", "user@prod"
		},
	}); err != nil {
		t.Fatal(err)
	}
	manifest, err := InspectBundle(target)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Scripts[0].HostTarget != "user@prod" {
		t.Fatalf("manifest host = %+v", manifest.Scripts[0])
	}
	dst := newTestStore(t)
	if _, err := ImportLibrary(dst, target, ImportOptions{
		BindHost: func(name, target string) string {
			if target == "user@prod" {
				return "h-local"
			}
			return ""
		},
	}); err != nil {
		t.Fatal(err)
	}
	var imported Script
	for _, s := range dst.Scripts() {
		if s.Name == "远程导出" {
			imported = s
		}
	}
	if imported.HostID != "h-local" || imported.HostTarget != "user@prod" {
		t.Fatalf("imported host = %q target = %q", imported.HostID, imported.HostTarget)
	}
}

func TestScriptEnvRoundTripAndOverride(t *testing.T) {
	env, err := parseEnvLines("# 注释\nA=1\nB=hello world\nC=")
	if err != nil {
		t.Fatal(err)
	}
	if env["A"] != "1" || env["B"] != "hello world" || env["C"] != "" {
		t.Fatalf("env = %v", env)
	}
	if _, err := parseEnvLines("NOVALUE"); err == nil {
		t.Fatal("a line without = should fail")
	}
	if _, err := parseEnvLines("1BAD=x"); err == nil {
		t.Fatal("an invalid name should fail")
	}
	rendered := formatEnv(env)
	if !strings.Contains(rendered, "A=1") || !strings.Contains(rendered, "B=hello world") {
		t.Fatalf("formatEnv = %q", rendered)
	}
	base := []string{"PATH=/bin", "A=old"}
	merged := applyScriptEnv(base, map[string]string{"A": "new", "B": "2"})
	joined := strings.Join(merged, "\n")
	if !strings.Contains(joined, "A=new") || !strings.Contains(joined, "B=2") || strings.Contains(joined, "A=old") {
		t.Fatalf("applyScriptEnv = %v", merged)
	}
}

func TestRemoteCommandExportsScriptEnv(t *testing.T) {
	sc := Script{ID: "s-1", Entry: "run.sh", Env: map[string]string{"TOKEN": "a b", "MODE": "prod"}}
	cmd := remoteRunCommand(sc, "hash", "run-1", "/bin/sh")
	if !strings.Contains(cmd, "export TOKEN='a b'") || !strings.Contains(cmd, "export MODE='prod'") {
		t.Fatalf("remote command lacks env:\n%s", cmd)
	}
}

func TestRunArgsSavesEnvironment(t *testing.T) {
	a, store, sc := newTestApp(t)
	a.openRunArgs(sc)
	a.form.env = "APP_ENV=prod\nTOKEN=a b"
	a.saveRunArgs()
	fresh, _ := store.Script(sc.ID)
	if fresh.Env["APP_ENV"] != "prod" || fresh.Env["TOKEN"] != "a b" {
		t.Fatalf("env = %v", fresh.Env)
	}
}

func TestRunLogRedactsSecrets(t *testing.T) {
	a, store, sc := newTestApp(t)
	if _, err := store.WriteScript(sc.ID, "echo 'TOKEN=abc123 password: hunter2'\n"); err != nil {
		t.Fatal(err)
	}
	it, _ := store.Interpreter(sc.InterpID)
	run, err := a.runner.Start(sc, it, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, store, run.ID)
	if done.Status != RunOK {
		t.Fatalf("run = %+v", done)
	}
	log := a.runner.LogText(done)
	if strings.Contains(log, "abc123") || strings.Contains(log, "hunter2") {
		t.Fatalf("secrets leaked:\n%s", log)
	}
	if !strings.Contains(log, "TOKEN=***") || !strings.Contains(log, "password: ***") {
		t.Fatalf("redaction did not keep the key:\n%s", log)
	}
	a.runner.SetRedaction(false)
	if _, err := store.WriteScript(sc.ID, "echo 'TOKEN=abc123'\n"); err != nil {
		t.Fatal(err)
	}
	run, err = a.runner.Start(sc, it, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done = waitRun(t, store, run.ID)
	if !strings.Contains(a.runner.LogText(done), "abc123") {
		t.Fatal("redaction stayed on after being disabled")
	}
}

func TestRedactSecretsPatterns(t *testing.T) {
	cases := map[string]string{
		"api_key=secretvalue":             "api_key=***",
		"Authorization: Bearer abc.def":   "Authorization: Bearer ***",
		"https://user:pass@example.com/x": "https://user:***@example.com/x",
		"nothing to hide":                 "nothing to hide",
	}
	for in, want := range cases {
		if got := redactSecrets(in); got != want {
			t.Fatalf("redactSecrets(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPreflightCatchesBrokenShell(t *testing.T) {
	a, store, sc := newTestApp(t)
	it, _ := store.Interpreter(sc.InterpID)
	if _, err := store.WriteScript(sc.ID, "if then\n"); err != nil {
		t.Fatal(err)
	}
	fresh, _ := store.Script(sc.ID)
	if err := a.preflightLocal(fresh, it); err == nil {
		t.Fatal("broken shell should fail preflight")
	}
	if _, err := store.WriteScript(sc.ID, "echo fine\n"); err != nil {
		t.Fatal(err)
	}
	fresh, _ = store.Script(sc.ID)
	if err := a.preflightLocal(fresh, it); err != nil {
		t.Fatalf("valid shell failed preflight: %v", err)
	}
}

func TestRemotePreflightUsesTheRemoteInterpreter(t *testing.T) {
	fakeSSH(t)
	a, store, sc := newTestApp(t)
	rs := newRemoteStoreForTest(t)
	h, _ := probeTestHost(t, rs)
	ri, ok := rs.Interpreter(h, "shell")
	if !ok {
		t.Fatal("no shell interpreter")
	}
	if _, err := store.WriteScript(sc.ID, "if then\n"); err != nil {
		t.Fatal(err)
	}
	fresh, _ := store.Script(sc.ID)
	if err := a.preflightRemote(fresh, h, ri); err == nil {
		t.Fatal("broken shell should fail the remote check")
	}
	if _, err := store.WriteScript(sc.ID, "echo fine\n"); err != nil {
		t.Fatal(err)
	}
	fresh, _ = store.Script(sc.ID)
	if err := a.preflightRemote(fresh, h, ri); err != nil {
		t.Fatalf("valid shell failed the remote check: %v", err)
	}
}

func TestPreviewImportCountsFilesAndBytes(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.sh"), []byte("echo a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "b.conf"), []byte("key=value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	count, bytes, err := PreviewImport(src, true)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || bytes <= 0 {
		t.Fatalf("preview = %d files, %d bytes", count, bytes)
	}
	single, size, err := PreviewImport(filepath.Join(src, "a.sh"), false)
	if err != nil || single != 1 || size != int64(len("echo a\n")) {
		t.Fatalf("file preview = %d %d %v", single, size, err)
	}
}

func TestCancelledImportLeavesNoScript(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a.sh"), []byte("echo a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ImportDirContext(ctx, src, "", "取消导入", "", nil); err == nil {
		t.Fatal("a cancelled import should fail")
	}
	if len(s.Scripts()) != 0 {
		t.Fatalf("scripts left: %+v", s.Scripts())
	}
}

func TestEnglishInterface(t *testing.T) {
	defer setUILanguage("zh")
	store := newTestStore(t)
	settings := defaultSettings()
	settings.Library = store.Root()
	settings.Language = "en"
	a := newApp(settings, store)
	setUILanguage("en")
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	texts := strings.Join(tt.Texts(), "\n")
	for _, want := range []string{"New script", "New folder", "Interpreters", "Runs", "Connections", "Settings"} {
		if !strings.Contains(texts, want) {
			t.Fatalf("English shell lacks %q:\n%s", want, texts)
		}
	}
}

func TestPickUpdateAsset(t *testing.T) {
	// The names match .github/workflows/release.yml.
	switch runtime.GOOS {
	case "darwin":
		want := "script-manager-0.2.0-darwin-" + runtime.GOARCH + ".zip"
		got, ok := pickUpdateAsset([]githubAsset{{Name: want}, {Name: "other.zip"}})
		if !ok || got.Name != want {
			t.Fatalf("picked %+v ok=%v", got, ok)
		}
	case "windows":
		want := "script-manager-setup-0.2.0-amd64.exe"
		got, ok := pickUpdateAsset([]githubAsset{{Name: "script-manager.exe"}, {Name: want}})
		if !ok || got.Name != want {
			t.Fatalf("picked %+v ok=%v", got, ok)
		}
	case "linux":
		want := "script-manager-0.2.0-linux-" + runtime.GOARCH + ".tar.gz"
		got, ok := pickUpdateAsset([]githubAsset{{Name: "notes.txt"}, {Name: want}})
		if !ok || got.Name != want {
			t.Fatalf("picked %+v ok=%v", got, ok)
		}
	}
	if _, ok := pickUpdateAsset([]githubAsset{{Name: "notes.txt"}}); ok {
		t.Fatal("a release without a matching asset should not pick one")
	}
}

func TestEnvKeysCannotInjectRemoteCommands(t *testing.T) {
	sc := Script{ID: "s-1", Entry: "run.sh", Env: map[string]string{
		"HOME":          "/root",
		"X; rm -rf /":   "boom",
		"BAD KEY":       "boom",
		"OK=1":          "boom",
		"$INJECT":       "boom",
		"ALSO_GOOD_123": "value",
	}}
	cmd := remoteRunCommand(sc, "hash", "run-1", "/bin/sh")
	if strings.Contains(cmd, "rm -rf") || strings.Contains(cmd, "BAD KEY") ||
		strings.Contains(cmd, "OK=1") || strings.Contains(cmd, "$INJECT") {
		t.Fatalf("remote command took an unsafe key:\n%s", cmd)
	}
	if !strings.Contains(cmd, "export HOME='/root'") || !strings.Contains(cmd, "export ALSO_GOOD_123='value'") {
		t.Fatalf("remote command lost a valid key:\n%s", cmd)
	}
	if got := strings.Join(applyScriptEnv(nil, sc.Env), " "); strings.Contains(got, "rm -rf") {
		t.Fatalf("local env took an unsafe key: %v", got)
	}
}

func TestStoreRejectsAnIllegalEnvKey(t *testing.T) {
	a, store, sc := newTestApp(t)
	_ = a
	fresh, _ := store.Script(sc.ID)
	fresh.Env = map[string]string{"A B": "x"}
	if err := store.UpdateScript(fresh); err == nil {
		t.Fatal("an illegal env key should be rejected on save")
	}
	fresh.Env = map[string]string{"GOOD": "x"}
	if err := store.UpdateScript(fresh); err != nil {
		t.Fatalf("a legal key was rejected: %v", err)
	}
}

func TestImportFileRefusesASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows 上没有可用的符号链接测试")
	}
	s := newTestStore(t)
	target := filepath.Join(t.TempDir(), "real.sh")
	if err := os.WriteFile(target, []byte("echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.sh")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if _, err := s.ImportFile(link, "", "链接", ""); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("import error = %v, want a symlink refusal", err)
	}
	if _, _, err := PreviewImport(link, false); err == nil {
		t.Fatal("the preview should refuse a symlink too")
	}
}

func TestImportRefusesTheLibraryItself(t *testing.T) {
	s := newTestStore(t)
	it := testInterpreter(t, s)
	sc, err := s.CreateScript("", "已有", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportDir(s.ScriptDir(sc), "", "递归", ""); err == nil {
		t.Fatal("importing a script directory back into the library must be refused")
	}
	if _, err := s.ImportDir(filepath.Join(s.Root(), "scripts"), "", "递归", ""); err == nil {
		t.Fatal("importing scripts/ must be refused")
	}
}

func TestClosingADirtyTabAsksFirst(t *testing.T) {
	a, store, sc := newTestApp(t)
	if err := store.AddScriptFile(sc.ID, "notes.sh", "echo one\n"); err != nil {
		t.Fatal(err)
	}
	a.openScriptFile(sc, "notes.sh")
	st := a.fileEditor(sc, "notes.sh")
	st.Buffer.Set("echo two\n")
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	a.closeScriptFile(sc, "notes.sh")
	tt.Frame()
	if !tt.HasText("未保存") {
		t.Fatalf("no confirmation shown:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if _, ok := a.editors[editorKey(sc.ID, "notes.sh")]; !ok {
		t.Fatal("the tab was closed before the user chose")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatalf("click 取消: %v", err)
	}
	tt.Frame()
	if _, ok := a.editors[editorKey(sc.ID, "notes.sh")]; !ok {
		t.Fatal("cancel should keep the tab")
	}
}

func TestSyncSavesDirtyBuffersFirst(t *testing.T) {
	a, store, sc := newTestApp(t)
	a.openScriptFile(sc, sc.Entry)
	st := a.fileEditor(sc, sc.Entry)
	st.Buffer.Set("echo saved-before-sync\n")
	if err := a.saveAllScripts(); err != nil {
		t.Fatal(err)
	}
	if st.Buffer.Modified() {
		t.Fatal("the buffer is still dirty")
	}
	body, err := store.ReadScript(sc)
	if err != nil || !strings.Contains(body, "saved-before-sync") {
		t.Fatalf("body = %q err=%v", body, err)
	}
}

func TestPickUpdateAssetRequiresMatchingArch(t *testing.T) {
	// A package for another architecture must not be offered.
	other := "amd64"
	if runtime.GOARCH == "amd64" {
		other = "arm64"
	}
	wrong := "script-manager-0.2.0-" + runtime.GOOS + "-" + other
	if runtime.GOOS == "windows" {
		wrong = "script-manager-setup-0.2.0-" + other + ".exe"
	}
	if runtime.GOOS == "darwin" {
		wrong += ".zip"
	}
	if runtime.GOOS == "linux" {
		wrong += ".tar.gz"
	}
	if got, ok := pickUpdateAsset([]githubAsset{{Name: wrong}}); ok {
		t.Fatalf("picked a build for another arch: %+v", got)
	}
}

func TestSafeAssetNameCannotEscapeTheTempDir(t *testing.T) {
	cases := map[string]string{
		"https://x/y/../../etc/passwd":   "passwd",
		"https://x/y/script-manager.zip": "script-manager.zip",
		"https://x/y/a%20b.zip?token=1":  "a%20b.zip",
		"https://x/":                     "x",
		"https://x/y/":                   "y",
	}
	for in, want := range cases {
		if got := safeAssetName(in); got != want {
			t.Fatalf("safeAssetName(%q) = %q, want %q", in, got, want)
		}
	}
	if name := safeAssetName("https://x/y/..%2f..%2fevil"); strings.Contains(name, "..") || strings.Contains(name, "/") {
		t.Fatalf("unsafe name: %q", name)
	}
}

func TestUpdateDownloadRejectsATruncatedFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	asset := githubAsset{Name: "pkg.zip", URL: srv.URL + "/pkg.zip", Size: 99}
	if _, err := downloadAsset(asset, "", false, ""); err == nil || !strings.Contains(err.Error(), "不完整") {
		t.Fatalf("error = %v, want a size mismatch", err)
	}
}

func TestUpdateDownloadVerifiesTheChecksum(t *testing.T) {
	body := []byte("payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	asset := githubAsset{Name: "pkg.zip", URL: srv.URL + "/pkg.zip", Size: int64(len(body))}
	sum := sha256.Sum256(body)
	if _, err := downloadAsset(asset, hex.EncodeToString(sum[:]), false, ""); err != nil {
		t.Fatalf("valid download rejected: %v", err)
	}
	if _, err := downloadAsset(asset, strings.Repeat("0", 64), false, ""); err == nil || !strings.Contains(err.Error(), "校验失败") {
		t.Fatalf("error = %v, want a checksum failure", err)
	}
}

func TestFailedRunsGiveTheConcurrencyPlaceBack(t *testing.T) {
	a, store, sc := newTestApp(t)
	a.settings.Concurrency = 1
	fresh, _ := store.Script(sc.ID)
	fresh.Preflight = true
	if _, err := store.WriteScript(sc.ID, "if then\n"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateScript(fresh); err != nil {
		t.Fatal(err)
	}
	fresh, _ = store.Script(sc.ID)
	// Preflight fails, so the run never starts; the place must come back.
	for i := 0; i < 3; i++ {
		a.startScriptRun(fresh)
	}
	release, err := a.reserveRun()
	if err != nil {
		t.Fatalf("a failed run leaked its concurrency place: %v", err)
	}
	release()
}

func TestWindowsSetupAssetIsFoundWithoutTheOSName(t *testing.T) {
	assets := []githubAsset{{Name: "script-manager-setup-0.2.0-amd64.exe"}}
	if _, ok := pickUpdateAssetFor("windows", "amd64", assets); !ok {
		t.Fatal("the setup exe was not recognised")
	}
	if _, ok := pickUpdateAssetFor("windows", "arm64", assets); ok {
		t.Fatal("a wrong-arch setup exe was accepted")
	}
	if _, ok := pickUpdateAssetFor("linux", "amd64", assets); ok {
		t.Fatal("a Windows setup was offered on Linux")
	}
}

func TestFormattedMessagesTranslate(t *testing.T) {
	defer setUILanguage("zh")
	setUILanguage("en")
	if got := trf("已导出 %d 个脚本", 3); got != "Exported 3 scripts" {
		t.Fatalf("trf = %q", got)
	}
	if got := trf("在 %s 上发现 %d 个解释器", "prod", 2); got != "On prod: found 2 interpreters" {
		t.Fatalf("trf = %q", got)
	}
	setUILanguage("zh")
	if got := trf("已导出 %d 个脚本", 3); got != "已导出 3 个脚本" {
		t.Fatalf("trf in Chinese = %q", got)
	}
}

func TestRetentionLabelRoundTripsInBothLanguages(t *testing.T) {
	defer setUILanguage("zh")
	for _, lang := range []string{"zh", "en"} {
		setUILanguage(lang)
		for _, days := range []int{7, 30, 90, 180} {
			label := trf("%d 天", days)
			got, err := retentionDaysFromLabel(label)
			if err != nil || got != days {
				t.Fatalf("%s: %q -> %d, %v", lang, label, got, err)
			}
		}
	}
}
