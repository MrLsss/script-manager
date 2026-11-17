package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// fakeSSH puts a script named ssh first in PATH. It answers the connection
// check itself and otherwise runs the remote command with a sandbox HOME, so
// the whole remote flow can be exercised without a server.
func fakeSSH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	remoteHome := filepath.Join(t.TempDir(), "remote-home")
	if err := os.MkdirAll(remoteHome, 0o755); err != nil {
		t.Fatal(err)
	}
	ssh := `#!/bin/sh
for last in "$@"; do :; done
case "$last" in
  "echo __SM_OK && uname -s")
    echo "__SM_OK"
    echo "${FAKE_UNAME:-Linux}"
    exit 0
    ;;
esac
case "$last" in
  *"tar -xf"*)
    if [ -n "$FAKE_REMOTE_LOG" ]; then echo upload >> "$FAKE_REMOTE_LOG"; fi
    if [ -n "$FAKE_SLOW_UPLOAD" ]; then sleep 5; fi
    ;;
esac
HOME="$FAKE_REMOTE_HOME" /bin/sh -c "$last"
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(ssh), 0o755); err != nil {
		t.Fatal(err)
	}
	uname := `#!/bin/sh
if [ "$1" = "-s" ]; then echo "${FAKE_UNAME:-Linux}"; exit 0; fi
exec /bin/uname "$@"
`
	if _, err := os.Stat("/bin/uname"); err != nil {
		uname = strings.Replace(uname, "/bin/uname", "/usr/bin/uname", 1)
	}
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(uname), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_REMOTE_HOME", remoteHome)
	return remoteHome
}

func newRemoteStoreForTest(t *testing.T) *RemoteStore {
	t.Helper()
	s, err := OpenRemoteStore(filepath.Join(t.TempDir(), "remote.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func remoteTestScript(t *testing.T, store *Store) Script {
	t.Helper()
	it := testInterpreter(t, store)
	sc, err := store.CreateScript("", "远程任务", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddScriptFile(sc.ID, "lib/common.sh", "greet() { echo remote-lib; }\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteScript(sc.ID, "#!/bin/sh\n. ./lib/common.sh\ngreet\necho remote-ok\n"); err != nil {
		t.Fatal(err)
	}
	sc, _ = store.Script(sc.ID)
	return sc
}

func probeTestHost(t *testing.T, rs *RemoteStore) (RemoteHost, []RemoteInterp) {
	t.Helper()
	h, err := rs.CreateHost(RemoteHost{Name: "测试机", Target: "user@test"})
	if err != nil {
		t.Fatal(err)
	}
	checked := CheckRemoteHost(h)
	if checked.Status != HostStateOK {
		t.Fatalf("check status = %s problem = %s", checked.Status, checked.Problem)
	}
	h, list, err := ProbeRemote(checked)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if h.Status != HostStateOK {
		t.Fatalf("probe status = %s", h.Status)
	}
	if err := rs.SetInterpreters(h.ID, list); err != nil {
		t.Fatal(err)
	}
	if err := rs.UpdateHost(h); err != nil {
		t.Fatal(err)
	}
	return h, list
}

func TestRemoteHostCheckAndProbe(t *testing.T) {
	fakeSSH(t)
	rs := newRemoteStoreForTest(t)
	h, list := probeTestHost(t, rs)
	if len(list) == 0 {
		t.Fatal("no interpreter detected on the fake host")
	}
	ri, ok := rs.Interpreter(h, "shell")
	if !ok || ri.State != RemoteStateOK || ri.Path == "" {
		t.Fatalf("shell interpreter = %+v ok=%v", ri, ok)
	}
	persisted, err := OpenRemoteStore(rs.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := persisted.Host(h.ID); !ok {
		t.Fatal("host did not persist")
	}
	if len(persisted.Interpreters(h.ID)) != len(list) {
		t.Fatalf("interpreters did not persist: %d", len(persisted.Interpreters(h.ID)))
	}
}

func TestRemoteRefusesNonLinux(t *testing.T) {
	fakeSSH(t)
	t.Setenv("FAKE_UNAME", "Darwin")
	rs := newRemoteStoreForTest(t)
	h, err := rs.CreateHost(RemoteHost{Name: "mac", Target: "user@mac"})
	if err != nil {
		t.Fatal(err)
	}
	checked := CheckRemoteHost(h)
	if checked.Status != HostStateNotLinux {
		t.Fatalf("status = %s, want not-linux", checked.Status)
	}
	if !strings.Contains(checked.Problem, "只支持 Linux") {
		t.Fatalf("problem = %q", checked.Problem)
	}
	if _, _, err := ProbeRemote(checked); err == nil || !strings.Contains(err.Error(), "Linux") {
		t.Fatalf("probe error = %v, want a Linux refusal", err)
	}
}

func TestRemoteRunUploadsAndStreamsOutput(t *testing.T) {
	remoteHome := fakeSSH(t)
	store := newTestStore(t)
	sc := remoteTestScript(t, store)
	rs := newRemoteStoreForTest(t)
	h, _ := probeTestHost(t, rs)
	ri, ok := rs.Interpreter(h, "shell")
	if !ok {
		t.Fatal("no shell interpreter")
	}
	r := NewRunner(store)
	run, err := r.StartRemote(sc, h, ri, 20*time.Second)
	if err != nil {
		t.Fatalf("StartRemote: %v", err)
	}
	done := waitRun(t, store, run.ID)
	if done.Status != RunOK || done.ExitCode != 0 {
		t.Fatalf("run = %+v\n%s", done, r.LogText(done))
	}
	if done.HostName != h.Name || done.Entry != sc.Entry || done.RemoteDir == "" {
		t.Fatalf("run record lacks remote fields: %+v", done)
	}
	log := r.LogText(done)
	if !strings.Contains(log, "remote-ok") || !strings.Contains(log, "remote-lib") {
		t.Fatalf("log = %q", log)
	}
	if !strings.Contains(log, "ssh user@test") || !strings.Contains(log, h.Name) {
		t.Fatalf("log header lacks the host: %q", log)
	}
	version := ""
	for _, rev := range store.RevisionsOf(sc.ID) {
		if rev.ID == done.RevID {
			version = rev.Hash
		}
	}
	if version == "" {
		t.Fatalf("revision %s not found", done.RevID)
	}
	dir := filepath.Join(remoteHome, ".script-manager", "scripts", sc.ID, version)
	for _, rel := range []string{sc.Entry, "lib/common.sh"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("remote file %s missing: %v", rel, err)
		}
	}
}

func TestRemoteUploadRunsOncePerRevision(t *testing.T) {
	fakeSSH(t)
	logPath := filepath.Join(t.TempDir(), "uploads.log")
	t.Setenv("FAKE_REMOTE_LOG", logPath)
	store := newTestStore(t)
	sc := remoteTestScript(t, store)
	rs := newRemoteStoreForTest(t)
	h, _ := probeTestHost(t, rs)
	ri, _ := rs.Interpreter(h, "shell")
	r := NewRunner(store)
	for i := 0; i < 2; i++ {
		run, err := r.StartRemote(sc, h, ri, 20*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if done := waitRun(t, store, run.ID); done.Status != RunOK {
			t.Fatalf("run %d = %+v %s", i, done, r.LogText(done))
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "upload"); got != 1 {
		t.Fatalf("uploads = %d, want 1\n%s", got, data)
	}
}

func TestRemoteStopRequestsTheFarSideToStop(t *testing.T) {
	fakeSSH(t)
	store := newTestStore(t)
	it := testInterpreter(t, store)
	sc, err := store.CreateScript("", "长任务", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteScript(sc.ID, "#!/bin/sh\necho started\nsleep 30\n"); err != nil {
		t.Fatal(err)
	}
	sc, _ = store.Script(sc.ID)
	rs := newRemoteStoreForTest(t)
	h, _ := probeTestHost(t, rs)
	ri, _ := rs.Interpreter(h, "shell")
	r := NewRunner(store)
	run, err := r.StartRemote(sc, h, ri, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(r.LogText(run), "started") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := r.Stop(run.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	done := waitRun(t, store, run.ID)
	if done.Status != RunCancelled {
		t.Fatalf("status = %s, want cancelled\n%s", done.Status, r.LogText(done))
	}
	if !strings.Contains(r.LogText(done), "已请求停止远端进程") {
		t.Fatalf("log = %q", r.LogText(done))
	}
}

func TestRemoteRunRefusesMissingInterpreter(t *testing.T) {
	fakeSSH(t)
	store := newTestStore(t)
	sc := remoteTestScript(t, store)
	rs := newRemoteStoreForTest(t)
	h, _ := probeTestHost(t, rs)
	r := NewRunner(store)
	if _, err := r.StartRemote(sc, h, RemoteInterp{Language: "shell", Path: "/bin/sh", State: RemoteStateMissing}, 5*time.Second); err == nil {
		t.Fatal("a missing remote interpreter should be refused")
	}
}

func TestRemoteScriptRunsFromTheHeader(t *testing.T) {
	fakeSSH(t)
	t.Setenv("SCRIPT_MANAGER_HOME", t.TempDir())
	a, store, sc := newTestApp(t)
	h, err := a.remotes.CreateHost(RemoteHost{Name: "服务器", Target: "user@prod"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.remotes.SetInterpreters(h.ID, []RemoteInterp{{
		Language: "shell", Name: "sh", Path: "/bin/sh", Version: "5.0", State: RemoteStateOK,
	}}); err != nil {
		t.Fatal(err)
	}
	h.Status = HostStateOK
	if err := a.remotes.UpdateHost(h); err != nil {
		t.Fatal(err)
	}
	fresh, _ := store.Script(sc.ID)
	fresh.HostID = h.ID
	if err := store.UpdateScript(fresh); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("远程 · 服务器") {
		t.Fatalf("header lacks the remote target:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if err := tt.Click("运行"); err != nil {
		t.Fatalf("click 运行: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		runs := store.Runs()
		if len(runs) == 1 && runs[0].Status != RunRunning {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	runs := store.Runs()
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	if runs[0].HostName != "服务器" || runs[0].Status != RunOK {
		t.Fatalf("run = %+v", runs[0])
	}
}

func TestRunTargetIsChosenInTheHeader(t *testing.T) {
	a, store, sc := newTestApp(t)
	h, err := a.remotes.CreateHost(RemoteHost{Name: "保存目标", Target: "user@save"})
	if err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if !tt.HasText("运行目标") {
		t.Fatalf("header lacks the run target control:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if err := tt.Click("本机"); err != nil {
		t.Fatalf("open run target menu: %v", err)
	}
	if err := tt.ChooseMenuItem("保存目标"); err != nil {
		t.Fatalf("choose host: %v", err)
	}
	tt.Frame()
	fresh, _ := store.Script(sc.ID)
	if fresh.HostID != h.ID {
		t.Fatalf("host = %q, want %q", fresh.HostID, h.ID)
	}
	if !tt.HasText("保存目标") {
		t.Fatalf("header does not show the target:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	// Clear the notice so it cannot be mistaken for the dialog.
	a.toast.Store(&toastState{})
	tt.Frame()
	// Changing script settings must keep the chosen target.
	a.openScriptOptions(sc)
	a.form.dangerous = true
	tt.Frame()
	if !tt.HasText("脚本设置") || !tt.HasText("高危脚本") {
		t.Fatalf("script settings dialog missing:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if err := tt.Click("保存"); err != nil {
		t.Fatalf("save script settings: %v", err)
	}
	tt.Frame()
	fresh, _ = store.Script(sc.ID)
	if fresh.HostID != h.ID {
		t.Fatalf("script settings cleared the target: %q", fresh.HostID)
	}
	a.deleteHost(h)
	if a.confirm.open {
		action := a.confirm.action
		a.confirm.open = false
		action(1)
	}
	fresh, _ = store.Script(sc.ID)
	if fresh.HostID != "" {
		t.Fatalf("deleting a connection should clear the script target, got %q", fresh.HostID)
	}
}

func TestConnectionsPageListsAndDetects(t *testing.T) {
	fakeSSH(t)
	a, _, _ := newTestApp(t)
	h, err := a.remotes.CreateHost(RemoteHost{Name: "生产机", Target: "user@prod"})
	if err != nil {
		t.Fatal(err)
	}
	a.remoteSel = h.ID
	a.router.Push("/connections")
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	for _, want := range []string{"生产机", "user@prod", "未检测", "检测解释器", "添加连接", "只支持 Linux"} {
		if !tt.HasText(want) {
			t.Fatalf("connections page lacks %q:\n%s", want, strings.Join(tt.Texts(), "\n"))
		}
	}
	if err := tt.Click("检测解释器"); err != nil {
		t.Fatalf("click 检测解释器: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if !a.remoteBusy.Load() && len(a.remotes.Interpreters(h.ID)) > 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if len(a.remotes.Interpreters(h.ID)) == 0 {
		t.Fatalf("detection found no interpreter:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	checked, _ := a.remotes.Host(h.ID)
	if checked.Status != HostStateOK {
		t.Fatalf("host status = %s problem = %s", checked.Status, checked.Problem)
	}
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if !tt.HasText("可用") {
		t.Fatalf("interpreters are not shown:\n%s", strings.Join(tt.Texts(), "\n"))
	}
	if !tt.HasText("/bin/") {
		t.Fatalf("interpreter path is not shown:\n%s", strings.Join(tt.Texts(), "\n"))
	}
}

func TestRemoteUploadCanBeCancelled(t *testing.T) {
	fakeSSH(t)
	t.Setenv("FAKE_SLOW_UPLOAD", "1")
	store := newTestStore(t)
	sc := remoteTestScript(t, store)
	rs := newRemoteStoreForTest(t)
	h, _ := probeTestHost(t, rs)
	ri, _ := rs.Interpreter(h, "shell")
	r := NewRunner(store)
	run, err := r.StartRemote(sc, h, ri, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if active, ok := r.Active(sc.ID); ok && active.Uploading {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := r.Stop(run.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	done := waitRun(t, store, run.ID)
	if done.Status != RunCancelled {
		t.Fatalf("status = %s, want cancelled\n%s", done.Status, r.LogText(done))
	}
	if !strings.Contains(r.LogText(done), "取消") {
		t.Fatalf("log = %q", r.LogText(done))
	}
}

func TestHostTargetCannotBecomeAnSSHOption(t *testing.T) {
	bad := RemoteHost{Target: "-oProxyCommand=touch /tmp/pwned", Port: 99999}
	args := sshArgs(bad, "echo hi")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "ProxyCommand") {
		t.Fatalf("an option was injected: %v", args)
	}
	if strings.Contains(joined, "99999") {
		t.Fatalf("an out-of-range port was used: %v", args)
	}
	if !strings.Contains(joined, "BatchMode=yes") {
		t.Fatalf("BatchMode is missing: %v", args)
	}
}

func TestRemoteHostFileSkipsUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "remote.json")
	body := `{"version":1,"hosts":[{"id":"h-1","name":"坏","target":"-oProxyCommand=x"}],"interpreters":{}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rs, err := OpenRemoteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	hosts := rs.Hosts()
	if len(hosts) != 1 {
		t.Fatalf("hosts = %+v", hosts)
	}
	if hosts[0].Status != HostStateUnreachable || hosts[0].Problem == "" {
		t.Fatalf("an unsafe target should be marked unreachable: %+v", hosts[0])
	}
}
