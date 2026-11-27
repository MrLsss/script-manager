package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Remote execution states.
const (
	RemoteStateOK        = "ok"
	RemoteStateMissing   = "missing"
	RemoteStateNoExec    = "noexec"
	RemoteStateProbeFail = "probe-failed"
	RemoteStateUnknown   = "unknown"

	HostStateOK          = "ok"
	HostStateUnreachable = "unreachable"
	HostStateAuth        = "auth-failed"
	HostStateNotLinux    = "not-linux"
	HostStateUnknown     = "unknown"
)

// remoteRoot is the directory every remote run lives under. It is written as
// a shell expression so $HOME is expanded on the remote side.
const remoteRoot = `"$HOME"/.script-manager`

// RemoteHost is an SSH destination the library may run scripts on. It holds
// no password and no key path: ssh, its agent and ~/.ssh/config do the
// authentication, exactly as they do in a terminal.
type RemoteHost struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Target    string            `json:"target"`
	Port      int               `json:"port,omitempty"`
	Note      string            `json:"note,omitempty"`
	Status    string            `json:"status"`
	Problem   string            `json:"problem,omitempty"`
	CheckedAt time.Time         `json:"checked_at"`
	Preferred map[string]string `json:"preferred,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// RemoteInterp is one interpreter found on a remote host.
type RemoteInterp struct {
	Language  string    `json:"language"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Version   string    `json:"version"`
	State     string    `json:"state"`
	CheckedAt time.Time `json:"checked_at"`
}

// RemoteStore holds hosts and their detected interpreters. It lives in the
// preferences directory, never in the library, so it is not synced.
type RemoteStore struct {
	mu      sync.Mutex
	path    string
	hosts   []*RemoteHost
	interps map[string][]RemoteInterp
}

type remoteFile struct {
	Version      int                       `json:"version"`
	Hosts        []*RemoteHost             `json:"hosts"`
	Interpreters map[string][]RemoteInterp `json:"interpreters"`
}

const remoteFileVersion = 1

// remoteStorePath is beside settings.json, outside the library.
func remoteStorePath() (string, error) {
	dir, err := userDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "remote.json"), nil
}

// OpenRemoteStore reads the remote configuration, making it when missing.
func OpenRemoteStore(path string) (*RemoteStore, error) {
	s := &RemoteStore{path: path, interps: map[string][]RemoteInterp{}}
	data, err := os.ReadFile(path)
	if err == nil {
		var f remoteFile
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, fmt.Errorf("remote.json 无法解析: %w", err)
		}
		for _, h := range f.Hosts {
			if h == nil || !validScriptID(h.ID) {
				continue
			}
			if err := validRemoteTarget(h.Target); err != nil {
				h.Status = HostStateUnreachable
				h.Problem = err.Error()
			}
			s.hosts = append(s.hosts, h)
		}
		if f.Interpreters != nil {
			s.interps = f.Interpreters
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

// emptyRemoteStore keeps working when the preferences directory is not
// available yet, for example in tests: it simply does not persist.
func emptyRemoteStore() *RemoteStore {
	return &RemoteStore{interps: map[string][]RemoteInterp{}}
}

func (s *RemoteStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	f := remoteFile{Version: remoteFileVersion, Hosts: s.hosts, Interpreters: s.interps}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Hosts returns every host in creation order.
func (s *RemoteStore) Hosts() []RemoteHost {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RemoteHost, 0, len(s.hosts))
	for _, h := range s.hosts {
		copied := *h
		copied.Preferred = copyPrefs(h.Preferred)
		out = append(out, copied)
	}
	return out
}

func copyPrefs(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Host looks one host up.
func (s *RemoteStore) Host(id string) (RemoteHost, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.hosts {
		if h.ID == id {
			copied := *h
			copied.Preferred = copyPrefs(h.Preferred)
			return copied, true
		}
	}
	return RemoteHost{}, false
}

// CreateHost adds a host.
func (s *RemoteStore) CreateHost(h RemoteHost) (RemoteHost, error) {
	h.Name = strings.TrimSpace(h.Name)
	if h.Name == "" {
		return RemoteHost{}, errors.New("名称不能为空")
	}
	if err := validRemoteTarget(h.Target); err != nil {
		return RemoteHost{}, err
	}
	if h.Port < 0 || h.Port > 65535 {
		return RemoteHost{}, errors.New("端口必须在 1 到 65535 之间")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.hosts {
		if strings.EqualFold(old.Name, h.Name) {
			return RemoteHost{}, errors.New("已经有同名的连接")
		}
	}
	h.ID = newID("h")
	h.Status = HostStateUnknown
	h.CreatedAt = time.Now()
	h.Target = strings.TrimSpace(h.Target)
	s.hosts = append(s.hosts, &h)
	if err := s.saveLocked(); err != nil {
		return RemoteHost{}, err
	}
	return h, nil
}

// UpdateHost saves a host, keeping its identity.
func (s *RemoteStore) UpdateHost(h RemoteHost) error {
	h.Target = strings.TrimSpace(h.Target)
	if err := validRemoteTarget(h.Target); err != nil {
		return err
	}
	if h.Port < 0 || h.Port > 65535 {
		return errors.New("端口必须在 1 到 65535 之间")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.hosts {
		if old.ID != h.ID {
			continue
		}
		old.Name = strings.TrimSpace(h.Name)
		old.Target = h.Target
		old.Port = h.Port
		old.Note = h.Note
		old.Status = h.Status
		old.Problem = h.Problem
		old.CheckedAt = h.CheckedAt
		old.Preferred = copyPrefs(h.Preferred)
		return s.saveLocked()
	}
	return errors.New("连接不存在")
}

// DeleteHost removes a host and everything detected on it.
func (s *RemoteStore) DeleteHost(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, h := range s.hosts {
		if h.ID == id {
			s.hosts = append(s.hosts[:i], s.hosts[i+1:]...)
			delete(s.interps, id)
			return s.saveLocked()
		}
	}
	return errors.New("连接不存在")
}

// Interpreters lists what was found on a host.
func (s *RemoteStore) Interpreters(hostID string) []RemoteInterp {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RemoteInterp, len(s.interps[hostID]))
	copy(out, s.interps[hostID])
	return out
}

// SetInterpreters replaces the detection result of a host.
func (s *RemoteStore) SetInterpreters(hostID string, list []RemoteInterp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interps[hostID] = list
	return s.saveLocked()
}

// SetPreferred remembers which interpreter a language should use on a host.
func (s *RemoteStore) SetPreferred(hostID, language, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.hosts {
		if h.ID != hostID {
			continue
		}
		if h.Preferred == nil {
			h.Preferred = map[string]string{}
		}
		if path == "" {
			delete(h.Preferred, language)
		} else {
			h.Preferred[language] = path
		}
		return s.saveLocked()
	}
	return errors.New("连接不存在")
}

// Interpreter picks the interpreter to use for a language: the preferred one
// when it is still usable, otherwise the first usable candidate.
func (s *RemoteStore) Interpreter(host RemoteHost, language string) (RemoteInterp, bool) {
	list := s.Interpreters(host.ID)
	want := ""
	if host.Preferred != nil {
		want = host.Preferred[language]
	}
	var first *RemoteInterp
	for i := range list {
		it := list[i]
		if it.Language != language || it.State != RemoteStateOK {
			continue
		}
		if want != "" && it.Path == want {
			return it, true
		}
		if first == nil {
			first = &list[i]
		}
	}
	if first != nil {
		return *first, true
	}
	return RemoteInterp{}, false
}

// validRemoteTarget refuses anything that would turn into another ssh option
// or a second command.
func validRemoteTarget(target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return errors.New("远程地址不能为空")
	}
	if strings.HasPrefix(target, "-") {
		return errors.New("远程地址不能以 - 开头")
	}
	if strings.ContainsAny(target, " \t\r\n") {
		return errors.New("远程地址不能包含空格")
	}
	if strings.ContainsAny(target, ";&|`$") {
		return errors.New("远程地址包含不支持的字符")
	}
	return nil
}

// sshArgs builds the argument list for a non-interactive ssh call. BatchMode
// keeps a missing key from hanging the app: the user sets up the agent or
// ~/.ssh/config first, as they would in a terminal.
func sshArgs(h RemoteHost, command string) []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=12"}
	if h.Port > 0 && h.Port <= 65535 {
		args = append(args, "-p", strconv.Itoa(h.Port))
	}
	target := h.Target
	// remote.json is a plain file; a target starting with "-" would become
	// another ssh option, so anything invalid is replaced by a placeholder
	// that fails the connection instead.
	if validRemoteTarget(target) != nil {
		target = "invalid.target"
	}
	return append(args, target, command)
}

// sshOutput runs one remote command and returns its combined output.
func sshOutput(ctx context.Context, h RemoteHost, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(h, command)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// sshWithInput runs a remote command with stdin from a local reader.
func sshWithInput(ctx context.Context, h RemoteHost, command string, in io.Reader) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(h, command)...)
	cmd.Stdin = in
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// CheckRemoteHost tests the connection and makes sure the far side is Linux,
// which is the only remote system runs support.
func CheckRemoteHost(h RemoteHost) RemoteHost {
	if err := validRemoteTarget(h.Target); err != nil {
		h.Status = HostStateUnreachable
		h.Problem = err.Error()
		return h
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := sshOutput(ctx, h, `echo __SM_OK && uname -s`)
	h.CheckedAt = time.Now()
	text := strings.TrimSpace(out)
	if err != nil {
		setHostProblem(&h, sshProblem(text, err))
		return h
	}
	if !strings.Contains(text, "__SM_OK") {
		setHostProblem(&h, "连接成功但没有收到预期回应")
		return h
	}
	osName := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "__SM_OK" {
			osName = line
		}
	}
	if !strings.EqualFold(osName, "Linux") {
		h.Status = HostStateNotLinux
		h.Problem = "远程执行只支持 Linux，这台机器是 " + orDash(osName)
		return h
	}
	h.Status = HostStateOK
	h.Problem = ""
	return h
}

// sshProblem turns ssh output into one readable line.
func sshProblem(out string, err error) string {
	text := strings.TrimSpace(out)
	if text == "" {
		return "无法连接：" + err.Error()
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, key := range []string{"Permission denied", "Host key verification failed",
			"Connection refused", "timed out", "Could not resolve", "No route to host",
			"Too many authentication failures"} {
			if !strings.Contains(line, key) {
				continue
			}
			if strings.Contains(line, "Permission denied") || strings.Contains(line, "authentication") {
				return "认证失败：先在本机终端用 ssh 登录一次，确认密钥或 ssh-agent 可用"
			}
			return line
		}
	}
	return text
}

// remoteDirExpr is the shell expression of a version directory.
func remoteDirExpr(scriptID, version string) string {
	return remoteRoot + "/scripts/" + dq(scriptID) + "/" + dq(version)
}

// remotePathExpr joins a relative path under a directory expression.
func remotePathExpr(dirExpr, rel string) string {
	rel = strings.TrimPrefix(filepath.ToSlash(rel), "/")
	if rel == "" {
		return dirExpr
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		parts[i] = dq(parts[i])
	}
	return dirExpr + "/" + strings.Join(parts, "/")
}

// dq quotes a component for use inside a double-quoted shell word while still
// letting "$HOME" expand where it appears literally.
func dq(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "$", `\$`)
	return `"` + s + `"`
}

// probeScript asks the remote shell for every candidate interpreter in one
// round trip.
func probeScript() string {
	var b strings.Builder
	b.WriteString(`echo "__SM_OS|$(uname -s)"` + "\n")
	for _, cand := range candidates {
		probe := ""
		for _, a := range cand.probe {
			probe += " " + shQuote(a)
		}
		fmt.Fprintf(&b, "p=$(command -v %s 2>/dev/null) || p=\"\"\n", shQuote(cand.binary))
		b.WriteString("if [ -n \"$p\" ]; then\n")
		b.WriteString("  if [ -x \"$p\" ]; then\n")
		fmt.Fprintf(&b, "    v=$(\"$p\"%s 2>&1 | head -n 1)\n", probe)
		fmt.Fprintf(&b, "    echo \"__SM_I|%s|%s|$p|$v\"\n", cand.language, cand.binary)
		b.WriteString("  else\n")
		fmt.Fprintf(&b, "    echo \"__SM_N|%s|%s|$p|\"\n", cand.language, cand.binary)
		b.WriteString("  fi\nfi\n")
	}
	return b.String()
}

// ProbeRemote detects interpreters and the remote system in one ssh call.
// Non-Linux hosts are refused here so a macOS server is never run on.
func ProbeRemote(h RemoteHost) (RemoteHost, []RemoteInterp, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	out, err := sshOutput(ctx, h, probeScript())
	h.CheckedAt = time.Now()
	if err != nil {
		setHostProblem(&h, sshProblem(out, err))
		return h, nil, errors.New(h.Problem)
	}
	osName := ""
	var list []RemoteInterp
	seen := map[string]bool{}
	now := time.Now()
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "__SM_OS|"):
			osName = strings.TrimSpace(strings.TrimPrefix(line, "__SM_OS|"))
		case strings.HasPrefix(line, "__SM_I|"):
			parts := strings.SplitN(strings.TrimPrefix(line, "__SM_I|"), "|", 4)
			if len(parts) != 4 {
				continue
			}
			lang, name, path, version := parts[0], parts[1], parts[2], strings.TrimSpace(parts[3])
			if seen[path] {
				continue
			}
			seen[path] = true
			state := RemoteStateOK
			if versionPattern.MatchString(version) {
				version = versionPattern.FindString(version)
			} else {
				state = RemoteStateProbeFail
			}
			list = append(list, RemoteInterp{Language: lang, Name: name, Path: path,
				Version: version, State: state, CheckedAt: now})
		case strings.HasPrefix(line, "__SM_N|"):
			parts := strings.SplitN(strings.TrimPrefix(line, "__SM_N|"), "|", 4)
			if len(parts) != 4 || seen[parts[2]] {
				continue
			}
			seen[parts[2]] = true
			list = append(list, RemoteInterp{Language: parts[0], Name: parts[1], Path: parts[2],
				State: RemoteStateNoExec, CheckedAt: now})
		}
	}
	if osName == "" {
		h.Status = HostStateUnreachable
		h.Problem = "没有读到远程系统类型"
		return h, nil, errors.New(h.Problem)
	}
	if !strings.EqualFold(osName, "Linux") {
		h.Status = HostStateNotLinux
		h.Problem = "远程执行只支持 Linux，这台机器是 " + osName
		return h, nil, errors.New(h.Problem)
	}
	h.Status = HostStateOK
	h.Problem = ""
	return h, list, nil
}

// setHostProblem records why a host is not usable, keeping authentication
// failures apart from an unreachable network.
func setHostProblem(h *RemoteHost, problem string) {
	h.Status = HostStateUnreachable
	if strings.HasPrefix(problem, "认证失败") {
		h.Status = HostStateAuth
	}
	h.Problem = problem
}

// remoteRunCommand is the shell script run on the far side. It is written to
// a 0600 file and started with bash, never passed as an ssh command argument,
// so the environment it exports stays out of process listings. The exit marker
// is the only line the app reads back; the pid file lets Stop reach the
// process.
func remoteRunCommand(sc Script, version, runID, interpPath string) string {
	dirExpr := remoteDirExpr(sc.ID, version)
	entryDir := filepath.ToSlash(filepath.Dir(sc.Entry))
	if entryDir == "." {
		entryDir = ""
	}
	cwdExpr := dirExpr
	if entryDir != "" {
		cwdExpr = remotePathExpr(dirExpr, entryDir)
	}
	entryExpr := remotePathExpr(dirExpr, sc.Entry)
	runDirExpr := remoteRoot + `/runs`
	pidExpr := remotePathExpr(runDirExpr, runID+".pid")
	var b strings.Builder
	b.WriteString("cd " + cwdExpr + " || { echo \"__SM_EXIT:125\"; exit 125; }\n")
	for _, key := range envKeys(sc.Env) {
		if !validEnvKey(key) {
			continue
		}
		b.WriteString("export " + key + "=" + shQuote(sc.Env[key]) + "\n")
	}
	b.WriteString("mkdir -p " + runDirExpr + "\n")
	b.WriteString("echo $$ > " + pidExpr + "\n")
	b.WriteString("child=\"\"\n")
	// Kill the whole subtree: the interpreter may start its own children.
	b.WriteString("term() { [ -n \"$child\" ] || return 0; pkill -TERM -P \"$child\" 2>/dev/null; kill -TERM \"$child\" 2>/dev/null; sleep 1; pkill -KILL -P \"$child\" 2>/dev/null; kill -KILL \"$child\" 2>/dev/null; }\n")
	b.WriteString("trap term TERM INT HUP\n")
	b.WriteString(shQuote(interpPath))
	b.WriteString(" " + entryExpr)
	for _, a := range sc.Args {
		b.WriteString(" " + shQuote(a))
	}
	b.WriteString(" &\n")
	b.WriteString("child=$!\n")
	b.WriteString("wait \"$child\"\n")
	b.WriteString("code=$?\n")
	b.WriteString("rm -f " + pidExpr + "\n")
	b.WriteString("echo \"__SM_EXIT:$code\"\n")
	b.WriteString("exit \"$code\"\n")
	return b.String()
}

// pushRemoteScript writes the run script to the far side and returns the
// shell expression of its path. The environment exports, credentials
// included, live only in this 0600 file: passing them as the ssh command
// would put them in the local ps output and the remote bash -c argv, where
// any user of either machine could read them.
func pushRemoteScript(h RemoteHost, runID, body string) (string, error) {
	pathExpr := remotePathExpr(remoteRoot+`/runs`, runID+".sh")
	cmd := "mkdir -p " + remoteRoot + `/runs && umask 077 && cat > ` + pathExpr +
		` && chmod 600 ` + pathExpr + ` && echo __SM_PUSHED`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := sshWithInput(ctx, h, cmd, strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("无法写入远端运行脚本: %s", firstLine(out, err))
	}
	if !strings.Contains(out, "__SM_PUSHED") {
		return "", fmt.Errorf("无法写入远端运行脚本: %s", firstLine(out, err))
	}
	return pathExpr, nil
}

// removeRemoteScript deletes a run script once its run has finished.
func removeRemoteScript(h RemoteHost, runID string) {
	pathExpr := remotePathExpr(remoteRoot+`/runs`, runID+".sh")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = sshOutput(ctx, h, "rm -f "+pathExpr)
}

// killRemoteProcess asks the far side to stop the process group of a run.
func killRemoteProcess(h RemoteHost, runID string) {
	pidExpr := remotePathExpr(remoteRoot+`/runs`, runID+".pid")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	_, _ = sshOutput(ctx, h, `pid=$(cat `+pidExpr+` 2>/dev/null); if [ -n "$pid" ]; then kill -TERM "$pid" 2>/dev/null; sleep 1; kill -KILL "$pid" 2>/dev/null; fi; rm -f `+pidExpr)
}

// uploadScript sends the script directory to the remote version directory
// once. A .ready marker makes later runs skip the transfer.
func (r *Runner) uploadScript(parent context.Context, sc Script, h RemoteHost, version string, lg *runLog) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	dirExpr := remoteDirExpr(sc.ID, version)

	if out, err := sshOutput(ctx, h, "test -f "+dirExpr+"/.ready && echo __SM_READY"); err == nil && strings.Contains(out, "__SM_READY") {
		lg.add(2, "远端目录已是最新，跳过上传")
		return nil
	}
	if ctx.Err() != nil {
		return errors.New("上传已取消")
	}

	files := r.store.ScriptFiles(sc.ID)
	if len(files) == 0 {
		return errors.New("脚本目录里没有文件")
	}
	var rels []string
	for _, f := range files {
		// tar's name list is newline separated, so a name with a newline
		// cannot be expressed. The app never creates one; a hand-made file is
		// reported instead of uploading a broken archive.
		if strings.ContainsAny(f.Path, "\n\r") {
			return fmt.Errorf("文件名包含换行，无法上传：%q", f.Path)
		}
		rels = append(rels, f.Path)
	}
	list, err := os.CreateTemp("", "script-manager-files-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(list.Name())
	if err := list.Close(); err != nil {
		return err
	}
	if err := os.WriteFile(list.Name(), []byte(strings.Join(rels, "\n")+"\n"), 0o600); err != nil {
		return err
	}

	tarCmd := exec.CommandContext(ctx, "tar", "-cf", "-", "-C", r.store.ScriptDir(sc), "-T", list.Name())
	tarOut, err := tarCmd.StdoutPipe()
	if err != nil {
		return err
	}
	var tarErr bytes.Buffer
	tarCmd.Stderr = &tarErr

	remoteCmd := "mkdir -p " + dirExpr + " && tar -xf - -C " + dirExpr + " && touch " + dirExpr + "/.ready"
	total := int64(0)
	for _, f := range files {
		total += f.Size
	}
	counter := &countingReader{r: tarOut}
	sshCmd := exec.CommandContext(ctx, "ssh", sshArgs(h, remoteCmd)...)
	sshCmd.Stdin = counter
	var sshErr bytes.Buffer
	sshCmd.Stderr = &sshErr

	lg.add(2, fmt.Sprintf("上传 %d 个文件（%s）到 %s …", len(rels), sizeText(total), h.Name))
	progressDone := make(chan struct{})
	defer close(progressDone)
	go func() {
		ticker := time.NewTicker(400 * time.Millisecond)
		defer ticker.Stop()
		lastPercent := -10
		for {
			select {
			case <-progressDone:
				return
			case <-ticker.C:
				if total <= 0 {
					continue
				}
				percent := int(counter.read.Load() * 100 / total)
				if percent > 100 {
					percent = 100
				}
				if percent < lastPercent+10 {
					continue
				}
				lastPercent = percent
				lg.add(2, fmt.Sprintf("上传 %d%%（%s / %s）", percent, sizeText(counter.read.Load()), sizeText(total)))
			}
		}
	}()
	if err := sshCmd.Start(); err != nil {
		return fmt.Errorf("启动 ssh 失败: %w", err)
	}
	if err := tarCmd.Start(); err != nil {
		_ = sshCmd.Process.Kill()
		return err
	}
	if err := tarCmd.Wait(); err != nil {
		_ = sshCmd.Process.Kill()
		if ctx.Err() != nil {
			return errors.New("上传已取消")
		}
		return fmt.Errorf("打包脚本失败: %v %s", err, strings.TrimSpace(tarErr.String()))
	}
	_ = tarOut.Close()
	if err := sshCmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return errors.New("上传已取消")
		}
		return fmt.Errorf("上传失败：%s", firstLine(sshErr.String(), err))
	}
	lg.add(2, fmt.Sprintf("上传完成 100%%（%s）", sizeText(total)))
	return nil
}

type countingReader struct {
	r    io.Reader
	read atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read.Add(int64(n))
	return n, err
}

func firstLine(text string, err error) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return err.Error()
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return text
}

// waitRemoteStopped polls the far side after a stop. A single check races with
// the process exiting, so it retries before reporting "unknown".
func waitRemoteStopped(h RemoteHost, runID string) (stopped bool, confirmed bool) {
	for i := 0; i < 6; i++ {
		alive, checked := remoteProcessState(h, runID)
		if !checked {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		if !alive {
			return true, true
		}
		time.Sleep(400 * time.Millisecond)
	}
	return false, true
}

// remoteProcessState asks the far side whether a run's pid is still alive.
// checked is false when the question itself could not be asked.
func remoteProcessState(h RemoteHost, runID string) (alive bool, checked bool) {
	pidExpr := remotePathExpr(remoteRoot+"/runs", runID+".pid")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := sshOutput(ctx, h, `pid=$(cat `+pidExpr+` 2>/dev/null) || pid=""; if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then echo __SM_ALIVE; fi; echo __SM_CHECKED`)
	if err != nil || !strings.Contains(out, "__SM_CHECKED") {
		return false, false
	}
	return strings.Contains(out, "__SM_ALIVE"), true
}

func envKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		if !validEnvKey(k) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// removeRemoteDir drops one version directory after a cancelled upload.
func removeRemoteDir(h RemoteHost, version, scriptID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, _ = sshOutput(ctx, h, "rm -rf "+remoteDirExpr(scriptID, version))
}
