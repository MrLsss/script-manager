package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SyncHealth describes how usable the remote is.
const (
	healthNoGit      = "no_git"
	healthNoRepo     = "no_repo"
	healthNoRemote   = "no_remote"
	healthNoUpstream = "no_upstream"
	healthUnrelated  = "unrelated"
	healthDetached   = "detached"
	healthHealthy    = "healthy"
)

// SyncState is what the sync page and the header show.
type SyncState struct {
	GitAvailable   bool
	Repo           bool
	Remote         string
	Branch         string
	Dirty          int
	Ahead          int
	Behind         int
	LastCommit     string
	LastCommitTime time.Time
	LastAuthor     string
	LastSnapshot   string
	Health         string
	Problem        string
}

// Snapshot is one tagged point in the history.
type Snapshot struct {
	Tag     string
	Commit  string
	Subject string
	Author  string
	When    time.Time
}

// Sync drives the library as a Git repository.
type Sync struct {
	store *Store
	mu    sync.RWMutex
	on    bool
	proxy string
}

// NewSync makes the sync helper for a library.
func NewSync(store *Store) *Sync { return &Sync{store: store} }

// SetProxy makes git talk to the remote through this address.
func (s *Sync) SetProxy(on bool, address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.on = on
	s.proxy = address
}

func (s *Sync) proxyEnv() (bool, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.on, s.proxy
}

func (s *Sync) env() []string {
	on, address := s.proxyEnv()
	return applyProxyEnv(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"), on, address)
}

// GitAvailable reports whether the system git can be run.
func GitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func gitRun(dir string, timeout time.Duration, args ...string) (string, error) {
	return gitRunEnv(dir, timeout, append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"), args...)
}

func gitRunEnv(dir string, timeout time.Duration, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotepath=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return text, fmt.Errorf("git %s 超时", args[0])
		}
		return text, fmt.Errorf("git %s: %s", args[0], text)
	}
	return text, nil
}

func (s *Sync) git(timeout time.Duration, args ...string) (string, error) {
	return gitRunEnv(s.store.Root(), timeout, s.env(), args...)
}

// gitContent reads bytes from git without trimming them, for file contents.
func (s *Sync) gitContent(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotepath=false"}, args...)...)
	cmd.Dir = s.store.Root()
	cmd.Env = s.env()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s 失败", args[0])
	}
	return string(out), nil
}

// DeviceName is the name this machine writes as the commit author.
func DeviceName() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "本机"
	}
	host = strings.TrimSpace(host)
	host = strings.TrimSuffix(host, ".local")
	return host
}

// InitRepo makes the library a repository with the ignore rules it needs.
func (s *Sync) InitRepo() error {
	if !GitAvailable() {
		return errors.New("没有找到 git 命令，请先安装 Git")
	}
	if _, err := s.git(10*time.Second, "rev-parse", "--is-inside-work-tree"); err != nil {
		if _, err := s.git(30*time.Second, "init", "-b", "main"); err != nil {
			return err
		}
	}
	if err := s.writeIgnore(); err != nil {
		return err
	}
	device := DeviceName()
	_, _ = s.git(10*time.Second, "config", "user.name", device)
	_, _ = s.git(10*time.Second, "config", "user.email", "script-manager@local")
	if _, err := s.git(30*time.Second, "add", "-A"); err != nil {
		return err
	}
	if _, err := s.git(30*time.Second, "commit", "-m", "建立脚本库"); err != nil {
		if !strings.Contains(err.Error(), "nothing to commit") {
			return err
		}
	}
	return nil
}

func (s *Sync) writeIgnore() error {
	want := []string{"logs/", "revisions/", ".trash/", "conflicts/", "local.json", "library.json", "library.json.migrated", "*.tmp", ".DS_Store"}
	path := filepath.Join(s.store.Root(), ".gitignore")
	current, _ := os.ReadFile(path)
	have := map[string]bool{}
	for _, line := range strings.Split(string(current), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, line := range want {
		if !have[line] {
			missing = append(missing, line)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	body := string(current)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += strings.Join(missing, "\n") + "\n"
	return os.WriteFile(path, []byte(body), 0o644)
}

// SetRemote points the repository at a remote and fetches it.
func (s *Sync) SetRemote(url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("请填写远端仓库地址")
	}
	if err := s.InitRepo(); err != nil {
		return err
	}
	if _, err := s.git(10*time.Second, "remote", "get-url", "origin"); err == nil {
		if _, err := s.git(10*time.Second, "remote", "set-url", "origin", url); err != nil {
			return err
		}
	} else if _, err := s.git(10*time.Second, "remote", "add", "origin", url); err != nil {
		return err
	}
	return nil
}

// Status reads the repository without changing it.
func (s *Sync) Status() SyncState {
	var st SyncState
	if !GitAvailable() {
		st.Health = healthNoGit
		st.Problem = "没有找到 git 命令"
		return st
	}
	st.GitAvailable = true
	root := s.store.Root()
	if _, err := gitRun(root, 10*time.Second, "rev-parse", "--is-inside-work-tree"); err != nil {
		st.Health = healthNoRepo
		return st
	}
	st.Repo = true
	st.Remote, _ = gitRun(root, 10*time.Second, "remote", "get-url", "origin")
	st.Branch, _ = gitRun(root, 10*time.Second, "rev-parse", "--abbrev-ref", "HEAD")
	if st.Branch == "" {
		st.Branch = "main"
	}
	if porcelain, err := gitRun(root, 20*time.Second, "status", "--porcelain"); err == nil {
		for _, line := range strings.Split(porcelain, "\n") {
			if strings.TrimSpace(line) != "" {
				st.Dirty++
			}
		}
	}
	if log, err := gitRun(root, 10*time.Second, "log", "-1", "--format=%h%x00%s%x00%an%x00%cI"); err == nil && log != "" {
		parts := strings.Split(log, "\x00")
		if len(parts) == 4 {
			st.LastCommit = parts[0] + " " + parts[1]
			st.LastAuthor = parts[2]
			st.LastCommitTime, _ = time.Parse(time.RFC3339, parts[3])
		}
	}
	if tag, err := gitRun(root, 10*time.Second, "describe", "--tags", "--abbrev=0", "--match", "sm-v-*"); err == nil {
		st.LastSnapshot = tag
	}
	switch {
	case st.Remote == "":
		st.Health = healthNoRemote
		return st
	case st.Branch == "HEAD":
		st.Health = healthDetached
		return st
	}
	upstream := "@{u}"
	if _, err := gitRun(root, 10*time.Second, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
		st.Health = healthNoUpstream
		return st
	}
	if counts, err := gitRun(root, 10*time.Second, "rev-list", "--left-right", "--count", "HEAD..."+upstream); err == nil {
		fields := strings.Fields(counts)
		if len(fields) == 2 {
			st.Ahead, _ = strconv.Atoi(fields[0])
			st.Behind, _ = strconv.Atoi(fields[1])
		}
	}
	if _, err := gitRun(root, 10*time.Second, "merge-base", "HEAD", upstream); err != nil {
		st.Health = healthUnrelated
		st.Problem = "本地与远端的提交历史没有共同起点"
		return st
	}
	st.Health = healthHealthy
	return st
}

// CheckRemote fetches without merging and reports how far apart the two sides are.
func (s *Sync) CheckRemote() (SyncState, error) {
	st := s.Status()
	if st.Health != healthHealthy {
		return st, nil
	}
	if _, err := s.git(20*time.Second, "fetch", "origin", "--prune"); err != nil {
		st.Problem = "无法连接远端：" + err.Error()
		return st, nil
	}
	return s.Status(), nil
}

// SyncNow commits, tags, fetches, merges and pushes. Conflicts keep the local
// file and are reported in the library so the user can choose later.
func (s *Sync) SyncNow() (SyncState, error) {
	st := s.Status()
	if st.Health == healthNoGit {
		return st, errors.New("没有找到 git 命令，请先安装 Git")
	}
	if err := s.InitRepo(); err != nil {
		return st, err
	}
	st = s.Status()
	if st.Remote == "" {
		return st, errors.New("还没有配置远端仓库")
	}
	if err := s.commitAll("同步脚本改动"); err != nil {
		return st, err
	}
	if _, err := s.snapshot(); err != nil {
		return st, err
	}
	if _, err := s.git(60*time.Second, "fetch", "origin", "--prune"); err != nil {
		return st, err
	}
	remoteRef := "origin/" + st.Branch
	if _, err := s.git(10*time.Second, "rev-parse", "--verify", remoteRef); err != nil {
		// The remote has no branch yet: publish this one.
		if _, err := s.git(120*time.Second, "push", "-u", "origin", "HEAD"); err != nil {
			return st, err
		}
		return s.Status(), nil
	}
	if _, err := s.git(10*time.Second, "merge-base", "HEAD", remoteRef); err != nil {
		return st, errors.New("本地与远端的提交历史没有共同起点")
	}
	localPaths, _ := s.git(10*time.Second, "diff", "--name-only", "HEAD")
	_ = localPaths
	overlap, err := s.overlappingPaths(remoteRef)
	if err != nil {
		return st, err
	}
	if len(overlap) > 0 {
		if err := s.recordConflicts(overlap, remoteRef); err != nil {
			return st, err
		}
		if _, err := s.git(60*time.Second, "merge", "-X", "ours", "--no-edit", remoteRef); err != nil {
			return st, err
		}
	} else if _, err := s.git(60*time.Second, "merge", "--no-edit", remoteRef); err != nil {
		return st, err
	}
	if _, err := s.git(120*time.Second, "push", "origin", "HEAD"); err != nil {
		return st, err
	}
	if err := s.store.Reload(); err != nil {
		return st, err
	}
	return s.Status(), nil
}

func (s *Sync) commitAll(message string) error {
	device := DeviceName()
	if _, err := s.git(60*time.Second, "add", "-A"); err != nil {
		return err
	}
	if _, err := s.git(10*time.Second, "diff", "--cached", "--quiet"); err == nil {
		return nil
	}
	_, err := s.git(60*time.Second, "commit", "-m", message+"（"+device+"）")
	if err != nil && strings.Contains(err.Error(), "nothing to commit") {
		return nil
	}
	return err
}

// overlappingPaths lists the files both sides changed since their common base.
func (s *Sync) overlappingPaths(remoteRef string) ([]string, error) {
	base, err := s.git(10*time.Second, "merge-base", "HEAD", remoteRef)
	if err != nil {
		return nil, err
	}
	local, err := s.git(10*time.Second, "diff", "--name-only", base, "HEAD")
	if err != nil {
		return nil, err
	}
	remote, err := s.git(10*time.Second, "diff", "--name-only", base, remoteRef)
	if err != nil {
		return nil, err
	}
	remoteSet := map[string]bool{}
	for _, p := range strings.Split(remote, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			remoteSet[p] = true
		}
	}
	var out []string
	for _, p := range strings.Split(local, "\n") {
		p = strings.TrimSpace(p)
		if p != "" && remoteSet[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Sync) recordConflicts(paths []string, remoteRef string) error {
	seen := map[string]bool{}
	var list []SyncConflict
	for _, p := range paths {
		id := scriptIDFromPath(p)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		name := id
		if sc, ok := s.store.Script(id); ok {
			name = sc.Name
		}
		// A directory conflict is kept whole: one file of a multi-file
		// script is useless on its own.
		hash := ""
		for _, rel := range s.remoteScriptPaths(remoteRef, id) {
			content, err := s.gitContent(20*time.Second, "show", remoteRef+":"+rel)
			if err != nil {
				continue
			}
			if hash == "" {
				hash = contentHash(content)
			}
			if err := s.writeRemoteCopy(rel, content); err != nil {
				return err
			}
		}
		list = append(list, SyncConflict{ScriptID: id, ScriptName: name, RemoteHash: hash, CreatedAt: time.Now()})
	}
	return s.store.SaveConflicts(list)
}

// remoteScriptPaths lists the files of one script in the remote ref.
func (s *Sync) remoteScriptPaths(remoteRef, scriptID string) []string {
	out, err := s.git(20*time.Second, "ls-tree", "-r", "--name-only", remoteRef, "--", "scripts/"+scriptID)
	if err != nil {
		return nil
	}
	var rels []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.HasPrefix(line, "scripts/"+scriptID+"/") {
			rels = append(rels, line)
		}
	}
	return rels
}

// writeRemoteCopy keeps the remote version of a conflicting file so the user can
// read it and choose it later, even after the branch moves on.
func (s *Sync) writeRemoteCopy(relPath, content string) error {
	target := filepath.Join(s.store.Root(), "conflicts", filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(content), 0o644)
}

func scriptIDFromPath(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "./"), "/")
	if len(parts) >= 3 && parts[0] == "scripts" {
		return parts[1]
	}
	return ""
}

// Snapshot tags the current commit.
func (s *Sync) snapshot() (string, error) {
	head, err := s.git(10*time.Second, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	if existing, err := s.git(10*time.Second, "tag", "--points-at", "HEAD", "-l", "sm-v-*"); err == nil && existing != "" {
		return strings.Split(existing, "\n")[0], nil
	}
	tag := "sm-v-" + time.Now().Format("20060102-150405") + "-" + head
	if _, err := s.git(10*time.Second, "tag", tag); err != nil {
		return "", err
	}
	return tag, nil
}

// Snapshots lists the tags this app made, newest first.
// DeleteSnapshot drops one tag. The commit it points at stays in the history.
func (s *Sync) DeleteSnapshot(tag string) error {
	if !strings.HasPrefix(tag, "sm-v-") {
		return fmt.Errorf("不是快照标签: %s", tag)
	}
	if _, err := s.git(10*time.Second, "tag", "-d", tag); err != nil {
		return err
	}
	s.gcAuto()
	return nil
}

// PruneSnapshots keeps the newest n tags and drops the rest.
func (s *Sync) PruneSnapshots(keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	snaps := s.Snapshots()
	if len(snaps) <= keep {
		return 0, nil
	}
	removed := 0
	for _, snap := range snaps[keep:] {
		if _, err := s.git(10*time.Second, "tag", "-d", snap.Tag); err != nil {
			return removed, err
		}
		removed++
	}
	if removed > 0 {
		s.gcAuto()
	}
	return removed, nil
}

func (s *Sync) gcAuto() {
	_, _ = s.git(60*time.Second, "gc", "--auto")
}

// SnapshotUsage reports the size of the git object store and of the whole
// library directory.
func (s *Sync) SnapshotUsage() (objects int64, library int64) {
	if out, err := s.git(15*time.Second, "count-objects", "-vH"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			key, value, ok := strings.Cut(line, ": ")
			if !ok {
				continue
			}
			if key == "size" || key == "size-pack" {
				objects += parseGitSize(value)
			}
		}
	}
	root := s.store.Root()
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			library += info.Size()
		}
		return nil
	})
	return objects, library
}

func parseGitSize(text string) int64 {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return 0
	}
	n, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	mult := 1.0
	switch fields[1] {
	case "KiB":
		mult = 1 << 10
	case "MiB":
		mult = 1 << 20
	case "GiB":
		mult = 1 << 30
	case "TiB":
		mult = 1 << 40
	}
	return int64(n * mult)
}

func (s *Sync) Snapshots() []Snapshot {
	out, err := s.git(10*time.Second, "tag", "-l", "sm-v-*", "--sort=-creatordate")
	if err != nil {
		return nil
	}
	var list []Snapshot
	for _, tag := range strings.Split(out, "\n") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		snap := Snapshot{Tag: tag}
		if info, err := s.git(10*time.Second, "log", "-1", "--format=%h%x00%s%x00%an%x00%cI", tag); err == nil {
			parts := strings.Split(info, "\x00")
			if len(parts) == 4 {
				snap.Commit = parts[0]
				snap.Subject = parts[1]
				snap.Author = parts[2]
				snap.When, _ = time.Parse(time.RFC3339, parts[3])
			}
		}
		list = append(list, snap)
	}
	return list
}

// RestoreSnapshot brings the library back to a tag as a new commit.
func (s *Sync) RestoreSnapshot(tag string) error {
	if tag == "" {
		return errors.New("请选择要恢复的版本")
	}
	if _, err := s.git(10*time.Second, "rev-parse", "--verify", tag); err != nil {
		return fmt.Errorf("找不到快照 %s", tag)
	}
	if err := s.commitAll("恢复前保存当前状态"); err != nil {
		return err
	}
	if _, err := s.snapshot(); err != nil {
		return err
	}
	added, err := s.git(30*time.Second, "diff", "--name-only", "--diff-filter=A", tag, "HEAD")
	if err != nil {
		return err
	}
	if _, err := s.git(60*time.Second, "checkout", tag, "--", "."); err != nil {
		return err
	}
	for _, p := range strings.Split(added, "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, err := s.git(10*time.Second, "rm", "-q", "--cached", "--ignore-unmatch", p); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(s.store.Root(), p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if _, err := s.git(60*time.Second, "commit", "-m", "恢复到 "+tag); err != nil {
		return err
	}
	return s.store.Reload()
}

// ResolveConflict applies the user's choice for one script.
func (s *Sync) ResolveConflict(conflict SyncConflict, choice string) error {
	sc, ok := s.store.Script(conflict.ScriptID)
	if !ok {
		return s.dropConflict(conflict.ScriptID)
	}
	remoteDir := filepath.Join(s.store.Root(), "conflicts", "scripts", sc.ID)
	hasRemote := false
	if info, err := os.Stat(remoteDir); err == nil && info.IsDir() {
		hasRemote = true
	}
	entry := sc.Entry
	if data, err := os.ReadFile(filepath.Join(remoteDir, "script.json")); err == nil {
		var remoteSc Script
		if json.Unmarshal(data, &remoteSc) == nil && remoteScriptEntryExists(remoteDir, remoteSc.Entry) {
			entry = remoteSc.Entry
		}
	}
	switch choice {
	case "local":
		return s.dropConflict(conflict.ScriptID)
	case "remote":
		if !hasRemote {
			return errors.New("没有保存远端版本，无法使用")
		}
		if err := s.store.ReplaceScriptFiles(sc.ID, remoteDir, entry); err != nil {
			return err
		}
		if err := s.commitAll("使用远端版本：" + sc.Name); err != nil {
			return err
		}
	case "both":
		if !hasRemote {
			return errors.New("没有保存远端版本，无法保留")
		}
		name := sc.Name + "（远端）"
		folderID := sc.FolderID
		for i := 2; ; i++ {
			if _, taken := s.store.ScriptByName(folderID, name); !taken {
				break
			}
			name = sc.Name + "（远端 " + strconv.Itoa(i) + "）"
		}
		copyScript, err := s.store.ImportDir(remoteDir, folderID, name, sc.InterpID)
		if err != nil {
			return err
		}
		if err := s.store.SetEntry(copyScript.ID, entry); err != nil {
			_ = s.store.SetEntry(copyScript.ID, copyScript.Entry)
		}
		if err := s.commitAll("保留远端版本：" + copyScript.Name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("未知的处理方式 %q", choice)
	}
	if err := s.dropConflict(conflict.ScriptID); err != nil {
		return err
	}
	if st := s.Status(); st.Remote != "" {
		_, _ = s.git(120*time.Second, "push", "origin", "HEAD")
	}
	return nil
}

func (s *Sync) dropConflict(scriptID string) error {
	var keep []SyncConflict
	for _, c := range s.store.Conflicts() {
		if c.ScriptID != scriptID {
			keep = append(keep, c)
		}
	}
	if sc, ok := s.store.Script(scriptID); ok {
		_ = os.RemoveAll(filepath.Join(s.store.Root(), "conflicts", "scripts", sc.ID))
	} else {
		_ = os.RemoveAll(filepath.Join(s.store.Root(), "conflicts", "scripts", scriptID))
	}
	return s.store.SaveConflicts(keep)
}

// NoticeText turns a startup check into the line shown above the content.
func NoticeText(st SyncState) string {
	if !st.Repo || st.Remote == "" || st.Problem != "" {
		return ""
	}
	switch {
	case st.Behind > 0 && st.Ahead > 0:
		return trf("远端有 %d 个更新，本地有 %d 个提交没有推送", st.Behind, st.Ahead)
	case st.Behind > 0:
		return trf("远端有 %d 个更新", st.Behind)
	case st.Ahead > 0:
		return trf("本地有 %d 个提交没有推送", st.Ahead)
	}
	return ""
}

// remoteScriptEntryExists checks that a remote script.json points at a file the
// conflict copy actually holds.
func remoteScriptEntryExists(dir, entry string) bool {
	if entry == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(entry)))
	return err == nil && info.Mode().IsRegular()
}
