package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ansiPattern = regexp.MustCompile(`\x1b\][^\x07]*\x07|\x1b\[[0-9;?]*[ -/]*[@-~]`)

type activeRun struct {
	run          Run
	cmd          *exec.Cmd
	stopping     bool
	timedOut     bool
	disconnected bool
	admin        bool
	remote       bool
	uploading    bool
	cancel       context.CancelFunc
	host         RemoteHost
	version      string
	statusPath   string
	file         *os.File
	log          *runLog
}

type runLog struct {
	mu        sync.Mutex
	lines     []LogLine
	bytes     int64
	truncated bool
	limit     int64
	redact    bool
	file      *os.File
}

func (l *runLog) add(kind int, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.redact {
		// Every line, headers included: the command line a run prints can
		// carry an argument that is a credential.
		text = redactSecrets(text)
	}
	line := LogLine{Kind: kind, Text: text}
	l.lines = append(l.lines, line)
	if len(l.lines) > 5000 {
		l.lines = l.lines[len(l.lines)-5000:]
	}
	if l.file == nil {
		return
	}
	if l.limit > 0 && l.bytes >= l.limit {
		l.truncated = true
		return
	}
	n, err := l.file.WriteString(text + "\n")
	l.bytes += int64(n)
	if err != nil {
		l.truncated = true
	}
}

func (l *runLog) snapshot() []LogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LogLine, len(l.lines))
	copy(out, l.lines)
	return out
}

// Runner starts, streams and stops the scripts' processes.
type Runner struct {
	store    *Store
	mu       sync.Mutex
	active   map[string]*activeRun
	logs     map[string]*runLog
	logLimit int64
	proxyOn  bool
	proxyURL string
	redact   atomic.Bool
	reserved int
	onChange func()
	onFinish func(run Run)
}

// SetRedaction turns secret masking in run output on or off.
func (r *Runner) SetRedaction(on bool) { r.redact.Store(on) }

// SetProxy makes scripts run with these proxy variables.
func (r *Runner) SetProxy(on bool, address string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.proxyOn = on
	r.proxyURL = address
}

// NewRunner makes a runner for a store.
func NewRunner(store *Store) *Runner {
	return &Runner{store: store, active: map[string]*activeRun{}, logs: map[string]*runLog{}}
}

// Start runs a script with an interpreter. The entry file, not the first file
// in the directory, is what runs, and the directory defaults to where the
// entry sits so relative references work.
func (r *Runner) Start(sc Script, it Interpreter, timeout time.Duration) (Run, error) {
	if it.State != StateOK {
		return Run{}, fmt.Errorf("解释器「%s」不可用", it.Name)
	}
	entryPath, err := r.store.EntryPath(sc)
	if err != nil {
		return Run{}, fmt.Errorf("入口不可用：%w", err)
	}
	if err := interpreterRunsEntry(it, sc.Entry); err != nil {
		return Run{}, err
	}
	rev, err := r.store.AddRevision(sc.ID)
	if err != nil && !errors.Is(err, ErrSnapshotTooLarge) {
		return Run{}, err
	}
	entryDir := filepath.Dir(entryPath)
	args := commandArgs(it, entryPath, entryDir, sc.Args)
	cwd := sc.Cwd
	if cwd == "" {
		cwd = entryDir
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return Run{}, fmt.Errorf("工作目录不存在: %s", cwd)
	}

	run := Run{
		ID:            newID("run"),
		ScriptID:      sc.ID,
		RevID:         rev.ID,
		InterpID:      it.ID,
		InterpName:    it.Name,
		InterpVersion: it.Version,
		Entry:         sc.Entry,
		Admin:         sc.RunAsAdmin,
		Args:          sc.Args,
		Cwd:           cwd,
		Dangerous:     sc.Dangerous,
		Status:        RunRunning,
		StartedAt:     time.Now(),
	}
	rel := filepath.Join("logs", sc.ID, run.ID+".log")
	run.LogPath = rel
	abs := filepath.Join(r.store.Root(), rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Run{}, err
	}
	file, err := os.Create(abs)
	if err != nil {
		return Run{}, err
	}

	lg := &runLog{file: file, limit: r.logLimit, redact: r.redact.Load()}
	lg.add(2, fmt.Sprintf("$ %s %s", it.Path, strings.Join(args, " ")))
	lg.add(2, fmt.Sprintf("开始 %s · 解释器 %s %s · 入口 %s · 修订 %s",
		run.StartedAt.Format("2006-01-02 15:04:05"), it.Name, it.Version, sc.Entry, rev.Hash))
	if sc.RunAsAdmin {
		lg.add(2, "以管理员身份运行，系统会先请求授权")
	}

	r.mu.Lock()
	proxyOn, proxyURL := r.proxyOn, r.proxyURL
	r.mu.Unlock()

	cmd := exec.Command(it.Path, args...)
	cmd.Dir = cwd
	cmd.Env = applyProxyEnv(applyScriptEnv(os.Environ(), sc.Env), proxyOn, proxyURL)
	configureProcess(cmd)

	var launch adminLaunch
	statusPath := ""
	if sc.RunAsAdmin {
		if _, err := adminLaunchProgram(); err != nil {
			lg.add(1, "启动失败: "+err.Error())
			run.Status = RunAborted
			run.EndedAt = time.Now()
			_ = file.Close()
			_ = r.store.CreateRun(run)
			return Run{}, err
		}
		if rev.ID == "" {
			lg.add(2, "目录超过快照上限，这次运行不记录修订")
		}
		statusPath = r.store.adminStatusPath(sc.ID, run.ID)
		launch, err = newAdminLaunch(it, args, cwd, abs, statusPath)
		if err != nil {
			lg.add(1, "启动失败: "+err.Error())
			_ = file.Close()
			return Run{}, err
		}
		cmd = exec.Command(launch.Program, launch.Args...)
		cmd.Dir = cwd
		cmd.Env = applyProxyEnv(applyScriptEnv(os.Environ(), sc.Env), proxyOn, proxyURL)
		configureProcess(cmd)
	}

	var stdout, stderr io.Reader
	if !sc.RunAsAdmin {
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			_ = file.Close()
			return Run{}, err
		}
		stderr, err = cmd.StderrPipe()
		if err != nil {
			_ = file.Close()
			return Run{}, err
		}
	}
	if err := cmd.Start(); err != nil {
		launch.cleanup()
		lg.add(1, "启动失败: "+err.Error())
		run.Status = RunAborted
		run.EndedAt = time.Now()
		_ = file.Close()
		_ = r.store.CreateRun(run)
		return Run{}, err
	}

	if err := r.store.CreateRun(run); err != nil {
		_ = terminateProcess(cmd)
		launch.cleanup()
		_ = file.Close()
		return Run{}, err
	}

	a := &activeRun{run: run, cmd: cmd, file: file, log: lg, admin: sc.RunAsAdmin, statusPath: statusPath}
	r.mu.Lock()
	r.active[run.ID] = a
	r.logs[run.ID] = lg
	r.mu.Unlock()

	stopFollow := make(chan struct{})
	if sc.RunAsAdmin {
		offset := int64(0)
		if info, err := os.Stat(abs); err == nil {
			offset = info.Size()
		}
		go r.followLog(abs, offset, lg, stopFollow)
	}

	if timeout > 0 {
		time.AfterFunc(timeout, func() {
			r.mu.Lock()
			ar, ok := r.active[run.ID]
			if ok && !ar.stopping {
				ar.timedOut = true
			}
			r.mu.Unlock()
			if ok {
				lg.add(1, fmt.Sprintf("超过 %s，终止进程", timeout))
				if sc.RunAsAdmin {
					lg.add(1, "已请求终止授权的包装进程，被提权的子进程可能仍在运行")
				}
				_ = terminateProcess(cmd)
				time.AfterFunc(5*time.Second, func() { _ = killProcess(cmd) })
			}
		})
	}

	go func() {
		var wg sync.WaitGroup
		if !sc.RunAsAdmin {
			read := func(f io.Reader, kind int) {
				defer wg.Done()
				scanner := bufio.NewScanner(f)
				scanner.Buffer(make([]byte, 64*1024), 1024*1024)
				for scanner.Scan() {
					line := ansiPattern.ReplaceAllString(scanner.Text(), "")
					lg.add(kind, line)
					r.notify()
				}
			}
			wg.Add(2)
			go read(stdout, 0)
			go read(stderr, 1)
		}
		wg.Wait()
		err := cmd.Wait()
		ended := time.Now()
		if sc.RunAsAdmin {
			close(stopFollow)
			// The follower may still be flushing; give it a moment.
			time.Sleep(50 * time.Millisecond)
			launch.cleanup()
		}
		r.mu.Lock()
		ar := r.active[run.ID]
		status := RunOK
		code := 0
		switch {
		case ar != nil && ar.timedOut:
			status = RunTimeout
		case ar != nil && ar.disconnected:
			status = RunUnknown
		case ar != nil && ar.stopping:
			status = RunCancelled
		case err != nil:
			status = RunFailed
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else {
				code = -1
			}
		}
		delete(r.active, run.ID)
		r.mu.Unlock()

		if sc.RunAsAdmin {
			if got, ok := waitExitStatus(statusPath); ok {
				code = got
				switch {
				case ar != nil && ar.timedOut:
					status = RunTimeout
				case got == 0:
					if status != RunUnknown {
						status = RunOK
					}
				default:
					status = RunFailed
				}
			} else if status != RunTimeout {
				switch {
				case ar != nil && ar.disconnected:
					status = RunUnknown
					code = -1
					lg.add(1, "没有读到退出码：授权的进程与程序断开了，状态未知")
				case err != nil:
					// The wrapper never wrote a status file, so the system
					// refused or the user cancelled the authorization.
					status = RunAborted
					code = -1
					var exit *exec.ExitError
					if errors.As(err, &exit) {
						code = exit.ExitCode()
					}
					lg.add(1, "授权被取消，或包装进程在写入退出码前结束")
				default:
					status = RunUnknown
					code = -1
					lg.add(1, "没有读到退出码，状态未知")
				}
			}
		}

		lg.mu.Lock()
		truncated := lg.truncated
		lg.mu.Unlock()
		if info, err := os.Stat(abs); err == nil {
			lg.bytes = info.Size()
		}
		bytes := lg.bytes

		lg.add(3, fmt.Sprintf("退出码 %d · 耗时 %s", code, ended.Sub(run.StartedAt).Round(time.Millisecond)))
		_ = file.Close()

		// Finish on a copy: the value returned to the caller must not change
		// under its feet.
		finished := run
		finished.Status = status
		finished.ExitCode = code
		finished.EndedAt = ended
		finished.LogBytes = bytes
		finished.Truncated = truncated
		_ = r.store.FinishRun(finished)
		if sc.RunAsAdmin {
			_ = os.Remove(statusPath)
		}
		if r.onFinish != nil {
			r.onFinish(finished)
		}
		r.notify()
	}()

	return run, nil
}

// followLog tails a log an elevated wrapper writes, refreshing the panel about
// every 200 ms while the run lasts.
func (r *Runner) followLog(path string, offset int64, lg *runLog, done <-chan struct{}) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var partial string
	read := func() {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return
		}
		data, _ := io.ReadAll(f)
		if len(data) == 0 {
			return
		}
		offset += int64(len(data))
		parts := strings.Split(partial+string(data), "\n")
		partial = parts[len(parts)-1]
		for _, line := range parts[:len(parts)-1] {
			lg.add(0, ansiPattern.ReplaceAllString(line, ""))
		}
		r.notify()
	}
	for {
		select {
		case <-done:
			read()
			if partial != "" {
				lg.add(0, ansiPattern.ReplaceAllString(partial, ""))
				r.notify()
			}
			return
		case <-ticker.C:
			read()
		}
	}
}

// Reconcile marks runs a previous session left behind. A status file written
// by an elevated wrapper settles the code; anything else is unknown, never
// pretended to be finished.
func (r *Runner) Reconcile() {
	for _, run := range r.store.Runs() {
		if run.Status != RunRunning {
			continue
		}
		run.EndedAt = time.Now()
		if code, ok := readExitStatus(r.store.adminStatusPath(run.ScriptID, run.ID)); ok {
			run.ExitCode = code
			if code == 0 {
				run.Status = RunOK
			} else {
				run.Status = RunFailed
			}
			_ = os.Remove(r.store.adminStatusPath(run.ScriptID, run.ID))
		} else {
			run.ExitCode = -1
			run.Status = RunUnknown
		}
		_ = r.store.FinishRun(run)
	}
}

// Stop asks a run's process to end, and kills it if it stays. For a run that
// was elevated it can only end the wrapper; the elevated child may live on, so
// the run is reported as disconnected rather than stopped.
func (r *Runner) Stop(runID string) error {
	r.mu.Lock()
	ar, ok := r.active[runID]
	if !ok {
		r.mu.Unlock()
		return errors.New("这次运行已经结束")
	}
	if ar.admin {
		ar.disconnected = true
	} else {
		ar.stopping = true
	}
	uploading := ar.uploading
	cancelUpload := ar.cancel
	r.mu.Unlock()
	if uploading {
		if cancelUpload != nil {
			cancelUpload()
		}
		ar.log.add(1, "已请求取消上传")
		return nil
	}
	switch {
	case ar.admin:
		ar.log.add(1, "已断开查看：只结束了授权包装进程，被提权的子进程可能仍在运行")
	case ar.remote:
		ar.log.add(1, "已请求停止远端进程")
		go killRemoteProcess(ar.host, ar.run.ID)
	default:
		ar.log.add(1, "收到停止请求")
	}
	if err := terminateProcess(ar.cmd); err != nil {
		return err
	}
	cmd := ar.cmd
	time.AfterFunc(5*time.Second, func() { _ = killProcess(cmd) })
	return nil
}

// StopAll ends every run, for quitting the app.
func (r *Runner) StopAll() {
	r.mu.Lock()
	var runs []*activeRun
	for _, ar := range r.active {
		ar.stopping = true
		if ar.uploading && ar.cancel != nil {
			ar.cancel()
		}
		runs = append(runs, ar)
	}
	r.mu.Unlock()
	for _, ar := range runs {
		if ar.cmd == nil {
			continue
		}
		_ = terminateProcess(ar.cmd)
		time.AfterFunc(2*time.Second, func() { _ = killProcess(ar.cmd) })
	}
}

// Active returns the running run of a script.
func (r *Runner) Active(scriptID string) (Run, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ar := range r.active {
		if ar.run.ScriptID == scriptID {
			return ar.run, true
		}
	}
	return Run{}, false
}

// Reserve takes one place of the concurrency budget. It is held until the run
// shows up in Active, so two quick clicks cannot both pass the check.
func (r *Runner) Reserve(limit int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.active)+r.reserved >= limit {
		return false
	}
	r.reserved++
	return true
}

// ReleaseReserve returns the place taken by Reserve.
func (r *Runner) ReleaseReserve() {
	r.mu.Lock()
	if r.reserved > 0 {
		r.reserved--
	}
	r.mu.Unlock()
}

// ActiveCount reports how many runs are going, for the concurrency limit.
func (r *Runner) ActiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active)
}

// Busy reports whether anything runs.
func (r *Runner) Busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.active) > 0
}

// Lines returns the output of a run this session started.
func (r *Runner) Lines(runID string) ([]LogLine, bool) {
	r.mu.Lock()
	lg, ok := r.logs[runID]
	r.mu.Unlock()
	if !ok {
		return nil, false
	}
	return lg.snapshot(), true
}

// LogText reads a run's log from the library, for runs of earlier sessions.
func (r *Runner) LogText(run Run) string {
	data, err := os.ReadFile(filepath.Join(r.store.Root(), run.LogPath))
	if err != nil {
		return "日志无法读取: " + err.Error()
	}
	return string(data)
}

func (r *Runner) notify() {
	if r.onChange != nil {
		r.onChange()
	}
}

// StartRemote runs a script on a Linux host over ssh. The directory upload
// happens in the background: the run appears at once, progress is written to
// the log, and Stop cancels the transfer. The entry starts only after the
// upload finishes.
func (r *Runner) StartRemote(sc Script, host RemoteHost, ri RemoteInterp, timeout time.Duration) (Run, error) {
	if host.Status == HostStateNotLinux {
		return Run{}, errors.New("远程执行只支持 Linux 服务器")
	}
	if ri.State != RemoteStateOK || ri.Path == "" {
		return Run{}, errors.New("远程解释器不可用，先检测远程解释器")
	}
	if err := interpreterRunsEntry(Interpreter{Name: ri.Name, Language: ri.Language, Path: ri.Path}, sc.Entry); err != nil {
		return Run{}, err
	}
	entryRel, err := cleanRelPath(sc.Entry)
	if err != nil {
		return Run{}, fmt.Errorf("入口不可用：%w", err)
	}
	rev, err := r.store.AddRevision(sc.ID)
	if err != nil && !errors.Is(err, ErrSnapshotTooLarge) {
		return Run{}, err
	}
	version := rev.Hash
	if version == "" {
		version = newID("v")
	}
	run := Run{
		ID:           newID("run"),
		ScriptID:     sc.ID,
		RevID:        rev.ID,
		Entry:        entryRel,
		HostID:       host.ID,
		HostName:     host.Name,
		RemoteDir:    "~/.script-manager/scripts/" + sc.ID + "/" + version,
		RemoteInterp: ri.Path + " " + ri.Version,
		Args:         sc.Args,
		Dangerous:    sc.Dangerous,
		Status:       RunRunning,
		Uploading:    true,
		StartedAt:    time.Now(),
	}
	rel := filepath.Join("logs", sc.ID, run.ID+".log")
	run.LogPath = rel
	abs := filepath.Join(r.store.Root(), rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Run{}, err
	}
	file, err := os.Create(abs)
	if err != nil {
		return Run{}, err
	}
	lg := &runLog{file: file, limit: r.logLimit, redact: r.redact.Load()}
	lg.add(2, fmt.Sprintf("$ ssh %s  (远程 %s)", host.Target, host.Name))
	lg.add(2, fmt.Sprintf("开始 %s · 入口 %s · 远端解释器 %s %s · 修订 %s",
		run.StartedAt.Format("2006-01-02 15:04:05"), sc.Entry, ri.Name, ri.Version, rev.Hash))
	lg.add(2, "远端目录 "+run.RemoteDir)
	if rev.ID == "" {
		lg.add(2, "目录超过快照上限，这次运行不记录修订")
	}

	uploadCtx, uploadCancel := context.WithCancel(context.Background())
	a := &activeRun{run: run, file: file, log: lg, remote: true, uploading: true, cancel: uploadCancel,
		host: host, version: version}
	if err := r.store.CreateRun(run); err != nil {
		uploadCancel()
		_ = file.Close()
		return Run{}, err
	}
	r.mu.Lock()
	r.active[run.ID] = a
	r.logs[run.ID] = lg
	r.mu.Unlock()

	go r.runRemoteUpload(uploadCtx, a, sc, host, ri, timeout, abs)
	return run, nil
}

// runRemoteUpload transfers the directory, then starts ssh and waits for the
// remote exit marker.
func (r *Runner) runRemoteUpload(ctx context.Context, a *activeRun, sc Script, host RemoteHost, ri RemoteInterp, timeout time.Duration, abs string) {
	if err := r.uploadScript(ctx, sc, host, a.version, a.log); err != nil {
		// A partial upload is never reused (no .ready), but it should not be
		// left behind either.
		go removeRemoteDir(host, a.version, sc.ID)
		r.mu.Lock()
		ar := r.active[a.run.ID]
		stopping := ar != nil && ar.stopping
		delete(r.active, a.run.ID)
		r.mu.Unlock()
		status := RunAborted
		if stopping {
			status = RunCancelled
			a.log.add(1, "上传已取消")
		} else {
			a.log.add(1, "上传失败: "+err.Error())
		}
		r.finishRemoteRun(a, status, -1, false)
		return
	}

	r.mu.Lock()
	ar := r.active[a.run.ID]
	stop := ar == nil || ar.stopping
	if !stop {
		a.uploading = false
		a.run.Uploading = false
	}
	r.mu.Unlock()
	if stop {
		r.mu.Lock()
		delete(r.active, a.run.ID)
		r.mu.Unlock()
		a.log.add(1, "上传完成后收到停止请求，未启动远端脚本")
		r.finishRemoteRun(a, RunCancelled, -1, false)
		return
	}

	// The run script is written first, so that its exports, credentials
	// included, never travel as an ssh command argument.
	scriptPath, err := pushRemoteScript(host, a.run.ID, remoteRunCommand(sc, a.version, a.run.ID, ri.Path))
	if err != nil {
		r.mu.Lock()
		delete(r.active, a.run.ID)
		r.mu.Unlock()
		a.log.add(1, err.Error())
		r.finishRemoteRun(a, RunAborted, -1, false)
		return
	}
	defer removeRemoteScript(host, a.run.ID)

	cmd := exec.Command("ssh", append([]string{"-tt"}, sshArgs(host, "bash "+scriptPath)...)...)
	configureProcess(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.mu.Lock()
		delete(r.active, a.run.ID)
		r.mu.Unlock()
		a.log.add(1, "启动 ssh 失败: "+err.Error())
		r.finishRemoteRun(a, RunAborted, -1, false)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.mu.Lock()
		delete(r.active, a.run.ID)
		r.mu.Unlock()
		a.log.add(1, "启动 ssh 失败: "+err.Error())
		r.finishRemoteRun(a, RunAborted, -1, false)
		return
	}
	if err := cmd.Start(); err != nil {
		r.mu.Lock()
		delete(r.active, a.run.ID)
		r.mu.Unlock()
		a.log.add(1, "启动 ssh 失败: "+err.Error())
		r.finishRemoteRun(a, RunAborted, -1, false)
		return
	}

	r.mu.Lock()
	a.cmd = cmd
	stopping := a.stopping
	r.mu.Unlock()
	if stopping {
		go killRemoteProcess(host, a.run.ID)
		_ = terminateProcess(cmd)
	}

	if timeout > 0 {
		time.AfterFunc(timeout, func() {
			r.mu.Lock()
			ar, ok := r.active[a.run.ID]
			if ok && !ar.stopping {
				ar.timedOut = true
			}
			r.mu.Unlock()
			if ok {
				a.log.add(1, fmt.Sprintf("超过 %s，终止远端进程", timeout))
				go killRemoteProcess(host, a.run.ID)
				_ = terminateProcess(cmd)
				time.AfterFunc(5*time.Second, func() { _ = killProcess(cmd) })
			}
		})
	}

	remoteCode := 0
	remoteDone := false
	var wg sync.WaitGroup
	read := func(f io.Reader, kind int) {
		defer wg.Done()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSuffix(ansiPattern.ReplaceAllString(scanner.Text(), ""), "\r")
			if kind == 0 && strings.HasPrefix(line, "__SM_EXIT:") {
				if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "__SM_EXIT:"))); err == nil {
					remoteCode, remoteDone = n, true
				}
				continue
			}
			a.log.add(kind, line)
			r.notify()
		}
	}
	wg.Add(2)
	go read(stdout, 0)
	go read(stderr, 1)
	wg.Wait()
	waitErr := cmd.Wait()

	r.mu.Lock()
	current := r.active[a.run.ID]
	wasStopping := current != nil && current.stopping
	status := RunUnknown
	code := -1
	switch {
	case current != nil && current.timedOut:
		status = RunTimeout
	case current != nil && current.stopping:
		status = RunCancelled
	case remoteDone:
		code = remoteCode
		if remoteCode == 0 {
			status = RunOK
		} else {
			status = RunFailed
		}
	case waitErr != nil:
		status = RunFailed
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			code = exit.ExitCode()
		}
		a.log.add(1, "没有收到远端退出码，ssh 已断开："+waitErr.Error())
	default:
		a.log.add(1, "没有收到远端退出码，状态未知")
	}
	delete(r.active, a.run.ID)
	r.mu.Unlock()

	r.finishRemoteRun(a, status, code, wasStopping && status == RunCancelled)
}

// finishRemoteRun writes the footer, records the result and tells the app.
func (r *Runner) finishRemoteRun(a *activeRun, status string, code int, checkRemote bool) {
	run := a.run
	if checkRemote {
		if stopped, confirmed := waitRemoteStopped(a.host, run.ID); confirmed {
			if stopped {
				a.log.add(1, "已确认远端进程结束")
			} else {
				status = RunUnknown
				a.log.add(1, "远端进程仍在运行，这次运行记为状态未知")
			}
		} else {
			a.log.add(1, "无法确认远端进程是否已结束")
		}
	}

	a.log.mu.Lock()
	truncated := a.log.truncated
	a.log.mu.Unlock()
	if info, err := os.Stat(filepath.Join(r.store.Root(), run.LogPath)); err == nil {
		a.log.bytes = info.Size()
	}
	bytes := a.log.bytes
	ended := time.Now()
	a.log.add(3, fmt.Sprintf("退出码 %d · 耗时 %s", code, ended.Sub(run.StartedAt).Round(time.Millisecond)))
	_ = a.file.Close()
	if a.cancel != nil {
		a.cancel()
	}

	finished := run
	finished.Uploading = false
	finished.Status = status
	finished.ExitCode = code
	finished.EndedAt = ended
	finished.LogBytes = bytes
	finished.Truncated = truncated
	_ = r.store.FinishRun(finished)
	if r.onFinish != nil {
		r.onFinish(finished)
	}
	r.notify()
}

// LogTail reads at most limit bytes from the end of a run's log.
func (r *Runner) LogTail(run Run, limit int64) string {
	f, err := os.Open(filepath.Join(r.store.Root(), run.LogPath))
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	if info.Size() > limit {
		if _, err := f.Seek(info.Size()-limit, io.SeekStart); err != nil {
			return ""
		}
	}
	data, _ := io.ReadAll(io.LimitReader(f, limit))
	return string(data)
}
