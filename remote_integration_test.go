package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRemoteAgainstRealSSH exercises the real ssh client against a throwaway
// server. It runs only when SM_SSH_TEST_HOST, SM_SSH_TEST_PORT and
// SM_SSH_TEST_KEY name one, because it needs a machine to log in to.
//
//	SM_SSH_TEST_HOST=127.0.0.1 SM_SSH_TEST_PORT=2222 \
//	SM_SSH_TEST_KEY=/tmp/sm-ssh/testkey go test -run TestRemoteAgainstRealSSH -v
func TestRemoteAgainstRealSSH(t *testing.T) {
	host := os.Getenv("SM_SSH_TEST_HOST")
	port := os.Getenv("SM_SSH_TEST_PORT")
	key := os.Getenv("SM_SSH_TEST_KEY")
	if host == "" || key == "" {
		t.Skip("没有配置真实 ssh 服务器；设置 SM_SSH_TEST_HOST/SM_SSH_TEST_PORT/SM_SSH_TEST_KEY")
	}
	// ssh reads the key and the host settings from HOME, so the test never
	// touches the user's real ssh configuration.
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	conf := "Host *\n  IdentityFile " + key + "\n  StrictHostKeyChecking no\n" +
		"  UserKnownHostsFile /dev/null\n  BatchMode yes\n  LogLevel ERROR\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "config"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	if port == "" {
		port = "22"
	}

	s := newTestStore(t)
	it := testInterpreter(t, s)
	rs := newRemoteStoreForTest(t)
	rh, err := rs.CreateHost(RemoteHost{Name: "docker", Target: "root@" + host, Port: atoiOr(port, 22)})
	if err != nil {
		t.Fatal(err)
	}
	rh.Target = "root@" + host
	if _, ok := os.LookupEnv("SM_SSH_TEST_USER"); ok {
		rh.Target = os.Getenv("SM_SSH_TEST_USER") + "@" + host
	}

	t.Run("check", func(t *testing.T) {
		checked := CheckRemoteHost(rh)
		if checked.Status != HostStateOK {
			t.Fatalf("status = %s problem = %s", checked.Status, checked.Problem)
		}
		rh = checked
	})

	t.Run("probe", func(t *testing.T) {
		probed, list, err := ProbeRemote(rh)
		if err != nil {
			t.Fatalf("probe: %v", err)
		}
		if len(list) == 0 {
			t.Fatal("no interpreter found on the real host")
		}
		if err := rs.SetInterpreters(probed.ID, list); err != nil {
			t.Fatal(err)
		}
		if _, ok := rs.Interpreter(probed, "shell"); !ok {
			t.Fatalf("no shell interpreter among %+v", list)
		}
		rh = probed
	})

	sc, err := s.CreateScript("", "真实远程", "shell", "sh", it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddScriptFile(sc.ID, "lib/common.sh", "greet() { echo real-lib; }\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteScript(sc.ID, "#!/bin/sh\n. ./lib/common.sh\ngreet\necho real-ok \"$1\"\n"); err != nil {
		t.Fatal(err)
	}
	sc, _ = s.Script(sc.ID)
	sc.Args = []string{"arg-one"}

	t.Run("preflight", func(t *testing.T) {
		ri, _ := rs.Interpreter(rh, "shell")
		if err := (&App{store: s}).preflightRemote(sc, rh, ri); err != nil {
			t.Fatalf("preflight: %v", err)
		}
		if _, err := s.WriteScript(sc.ID, "if then\n"); err != nil {
			t.Fatal(err)
		}
		broken, _ := s.Script(sc.ID)
		if err := (&App{store: s}).preflightRemote(broken, rh, ri); err == nil {
			t.Fatal("broken shell passed the real remote preflight")
		}
		if _, err := s.WriteScript(sc.ID, "#!/bin/sh\n. ./lib/common.sh\ngreet\necho real-ok \"$1\"\n"); err != nil {
			t.Fatal(err)
		}
		sc, _ = s.Script(sc.ID)
		sc.Args = []string{"arg-one"}
	})

	t.Run("run", func(t *testing.T) {
		ri, ok := rs.Interpreter(rh, "shell")
		if !ok {
			t.Fatal("no shell interpreter")
		}
		r := NewRunner(s)
		run, err := r.StartRemote(sc, rh, ri, 90*time.Second)
		if err != nil {
			t.Fatalf("StartRemote: %v", err)
		}
		done := waitRun(t, s, run.ID)
		log := r.LogText(done)
		if done.Status != RunOK {
			t.Fatalf("run = %+v\n%s", done, log)
		}
		for _, want := range []string{"real-lib", "real-ok arg-one", "上传完成"} {
			if !strings.Contains(log, want) {
				t.Fatalf("log lacks %q:\n%s", want, log)
			}
		}
		if done.RemoteDir == "" || done.HostName != "docker" {
			t.Fatalf("run record = %+v", done)
		}
		// The script's own environment must reach the far side, and the files
		// must be there with their relative paths.
		if _, err := s.WriteScript(sc.ID, "#!/bin/sh\n. ./lib/common.sh\ngreet\nenv | grep '^SM_TEST_ENV=' || echo no-env\n"); err != nil {
			t.Fatal(err)
		}
		withEnv, _ := s.Script(sc.ID)
		withEnv.Env = map[string]string{"SM_TEST_ENV": "value with space"}
		run2, err := r.StartRemote(withEnv, rh, ri, 90*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		done2 := waitRun(t, s, run2.ID)
		if done2.Status != RunOK {
			t.Fatalf("env run = %+v\n%s", done2, r.LogText(done2))
		}
		if !strings.Contains(r.LogText(done2), "SM_TEST_ENV=value with space") {
			t.Fatalf("the environment did not reach the remote:\n%s", r.LogText(done2))
		}
	})

	t.Run("second run skips the upload", func(t *testing.T) {
		ri, _ := rs.Interpreter(rh, "shell")
		r := NewRunner(s)
		run, err := r.StartRemote(sc, rh, ri, 90*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		done := waitRun(t, s, run.ID)
		if done.Status != RunOK {
			t.Fatalf("run = %+v\n%s", done, r.LogText(done))
		}
		if !strings.Contains(r.LogText(done), "跳过上传") {
			t.Fatalf("the second run re-uploaded:\n%s", r.LogText(done))
		}
	})

	t.Run("newline file name is refused", func(t *testing.T) {
		if _, err := s.WriteScript(sc.ID, "#!/bin/sh\necho x\n"); err != nil {
			t.Fatal(err)
		}
		plain, _ := s.Script(sc.ID)
		// tar's name list cannot express a newline; the upload must say so
		// instead of sending a broken archive.
		bad := filepath.Join(s.ScriptDir(plain), "bad\nname.sh")
		if err := os.WriteFile(bad, []byte("echo x\n"), 0o644); err != nil {
			t.Skipf("无法创建带换行的文件名: %v", err)
		}
		defer os.Remove(bad)
		ri, _ := rs.Interpreter(rh, "shell")
		r := NewRunner(s)
		run, err := r.StartRemote(plain, rh, ri, 60*time.Second)
		if err != nil {
			t.Fatalf("StartRemote: %v", err)
		}
		// The upload runs in the background, so the refusal shows up in the
		// log and the run ends without starting the script.
		done := waitRun(t, s, run.ID)
		if done.Status != RunAborted {
			t.Fatalf("status = %s\n%s", done.Status, r.LogText(done))
		}
		if !strings.Contains(r.LogText(done), "换行") {
			t.Fatalf("log = %q", r.LogText(done))
		}
	})

	t.Run("stop", func(t *testing.T) {
		if _, err := s.WriteScript(sc.ID, "#!/bin/sh\necho started\nsleep 60\n"); err != nil {
			t.Fatal(err)
		}
		long, _ := s.Script(sc.ID)
		ri, _ := rs.Interpreter(rh, "shell")
		r := NewRunner(s)
		run, err := r.StartRemote(long, rh, ri, 120*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(r.LogText(run), "started") {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err := r.Stop(run.ID); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		done := waitRun(t, s, run.ID)
		if done.Status != RunCancelled {
			t.Fatalf("status = %s\n%s", done.Status, r.LogText(done))
		}
		if !strings.Contains(r.LogText(done), "已确认远端进程结束") {
			t.Fatalf("log = %q", r.LogText(done))
		}
	})
}

func atoiOr(s string, fallback int) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return fallback
		}
		n = n*10 + int(r-'0')
	}
	if n == 0 {
		return fallback
	}
	return n
}
