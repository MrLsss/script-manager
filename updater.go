package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// appVersion is the version shown by the update check; keep it in step with
// mygo.json.
const appVersion = "0.1.0"

// previousUpdates are the temp directories of earlier downloads, cleared when
// a new download starts.
var previousUpdates atomic.Value

// loadedUpdateDirs is the temp directories of earlier downloads.
func loadedUpdateDirs() []string {
	if dirs, ok := previousUpdates.Load().([]string); ok {
		return dirs
	}
	return nil
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Name    string        `json:"name"`
	HTMLURL string        `json:"html_url"`
	Body    string        `json:"body"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name        string `json:"name"`
	URL         string `json:"browser_download_url"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

func fetchLatestRelease(repo string) (*githubRelease, error) {
	repo = strings.TrimSpace(repo)
	if !validRepo(repo) {
		return nil, errors.New("仓库地址应为 owner/repo")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	url := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "script-manager")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub 返回 %s", resp.Status)
	}
	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("无法解析 GitHub 响应: %w", err)
	}
	if rel.TagName == "" || rel.HTMLURL == "" {
		return nil, errors.New("这个仓库还没有发布 Release")
	}
	return &rel, nil
}

func validRepo(repo string) bool {
	if strings.Count(repo, "/") != 1 {
		return false
	}
	parts := strings.Split(repo, "/")
	if parts[0] == "" || parts[1] == "" {
		return false
	}
	return !strings.ContainsAny(repo, " \t\r\n")
}

// versionNewer compares dotted versions, ignoring a leading v and any
// pre-release suffix.
func versionNewer(current, tag string) bool {
	return compareVersions(tag, current) > 0
}

func compareVersions(a, b string) int {
	pa := versionParts(a)
	pb := versionParts(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out []int
	for _, part := range strings.Split(v, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return out
		}
		out = append(out, n)
	}
	return out
}

// checkForUpdates asks GitHub for the newest release. Manual checks report
// every failure; the startup check stays quiet unless a new version exists.
func (a *App) checkForUpdates(manual bool) {
	repo := strings.TrimSpace(a.settings.UpdateRepo)
	if repo == "" {
		if manual {
			a.alertError("无法检查更新", "先在设置里填写 GitHub 仓库，例如 owner/repo。")
		}
		return
	}
	go func() {
		rel, err := fetchLatestRelease(repo)
		a.updateUI(func() {
			if err != nil {
				if manual {
					a.alertError("检查更新失败", err.Error())
				}
				return
			}
			if !versionNewer(appVersion, rel.TagName) {
				if manual {
					a.showNotice("已经是最新版本 " + appVersion)
				}
				return
			}
			message := trf("当前版本 %s，最新版本 %s。", appVersion, rel.TagName)
			if asset, ok := pickUpdateAsset(rel.Assets); ok {
				message += "\n\n将为当前系统下载：" + asset.Name
				a.confirmAction("发现新版本", message, []string{"稍后", "下载更新", "下载页"}, func(i int) {
					switch i {
					case 1:
						a.downloadUpdate(rel, asset)
					case 2:
						_ = mygo.Shell.OpenExternal(rel.HTMLURL)
					}
				})
				return
			}
			message += "\n\n这个 Release 里没有当前系统的安装包。"
			a.confirmAction("发现新版本", message, []string{"稍后", "打开下载页"}, func(i int) {
				if i == 1 {
					_ = mygo.Shell.OpenExternal(rel.HTMLURL)
				}
			})
		})
	}()
}

// pickUpdateAsset chooses the file for this machine.
func pickUpdateAsset(assets []githubAsset) (githubAsset, bool) {
	return pickUpdateAssetFor(runtime.GOOS, runtime.GOARCH, assets)
}

// pickUpdateAssetFor is the rule itself: only an exact platform and
// architecture match is accepted, because a wrong-arch build cannot launch.
// The Windows installer carries no OS name ("script-manager-setup-x-amd64.exe"),
// so it is matched by "setup" and the architecture alone.
func pickUpdateAssetFor(goos, goarch string, assets []githubAsset) (githubAsset, bool) {
	for _, a := range assets {
		n := strings.ToLower(a.Name)
		if !strings.Contains(n, goarch) {
			continue
		}
		switch goos {
		case "darwin":
			if strings.Contains(n, "darwin") && strings.HasSuffix(n, ".zip") {
				return a, true
			}
		case "windows":
			if strings.Contains(n, "setup") && strings.HasSuffix(n, ".exe") {
				return a, true
			}
		case "linux":
			if strings.Contains(n, "linux") && strings.HasSuffix(n, ".tar.gz") {
				return a, true
			}
		}
	}
	return githubAsset{}, false
}

// safeAssetName keeps a download inside the temp directory and readable.
func safeAssetName(url string) string {
	name := path.Base(url)
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == filepath.Separator || r == ':' || r == '\\' {
			return -1
		}
		return r
	}, name)
	name = strings.ReplaceAll(name, "..", "_")
	name = strings.Trim(name, ".")
	if name == "" {
		return "script-manager-update"
	}
	return name
}

// updateHTTPClient uses the app's proxy when it is enabled and valid, so a
// download works on a machine that can only reach GitHub through it.
func updateHTTPClient(on bool, address string) *http.Client {
	client := &http.Client{}
	if !on || validProxyURL(address) != nil {
		return client
	}
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		return client
	}
	client.Transport = &http.Transport{Proxy: http.ProxyURL(u)}
	return client
}

// downloadUpdate fetches the chosen asset and offers to open it. It never
// replaces the running app: installers and disk images decide how to install.
func (a *App) downloadUpdate(rel *githubRelease, asset githubAsset) {
	a.showNotice("正在下载 " + asset.Name + "…")
	go func() {
		sum := checksumFor(rel, asset.Name, a.settings.ProxyEnabled, a.settings.ProxyURL)
		path, err := downloadAsset(asset, sum, a.settings.ProxyEnabled, a.settings.ProxyURL)
		a.updateUI(func() {
			if err != nil {
				a.alertError("下载更新失败", err.Error())
				return
			}
			trust := "这个 Release 没有提供校验和，文件未做完整性校验。"
			if sum != "" {
				trust = "SHA-256 与 Release 公布的校验和一致（安装包本身仍未签名）。"
			}
			a.confirmAction("更新已下载", "安装包保存在：\n"+path+"\n\n"+trust+
				"\n打开后按安装程序完成更新，当前版本不会被自动替换。",
				[]string{"关闭", "打开安装包", "打开所在文件夹"}, func(i int) {
					switch i {
					case 1:
						if err := mygo.Shell.OpenPath(path); err != nil {
							a.alertError("无法打开", err.Error())
						}
					case 2:
						mygo.Shell.ShowItemInFolder(path)
					}
				})
		})
	}()
}

// checksumFor looks for a checksums file among the release assets and returns
// the SHA-256 of name. An empty result means the release does not publish one.
func checksumFor(rel *githubRelease, name string, proxyOn bool, proxyURL string) string {
	var sums *githubAsset
	for i := range rel.Assets {
		switch strings.ToLower(rel.Assets[i].Name) {
		case "checksums.txt", "sha256sums", "sha256sums.txt":
			sums = &rel.Assets[i]
		}
	}
	if sums == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sums.URL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "script-manager")
	resp, err := updateHTTPClient(proxyOn, proxyURL).Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name || strings.HasSuffix(fields[1], "/"+name) {
			return strings.ToLower(fields[0])
		}
	}
	return ""
}

func downloadAsset(asset githubAsset, wantSum string, proxyOn bool, proxyURL string) (string, error) {
	if asset.URL == "" {
		return "", errors.New("这个文件没有下载地址")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "script-manager")
	resp, err := updateHTTPClient(proxyOn, proxyURL).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub 返回 %s", resp.Status)
	}
	name := safeAssetName(asset.URL)
	// Old downloads are kept only until the next one starts.
	for _, old := range loadedUpdateDirs() {
		_ = os.RemoveAll(old)
	}
	dir, err := os.MkdirTemp("", "script-manager-update-*")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hasher), io.LimitReader(resp.Body, 1<<30))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	// A truncated or swapped download must not reach the installer.
	if asset.Size > 0 && written != asset.Size {
		_ = os.Remove(path)
		return "", fmt.Errorf("下载不完整：收到 %d 字节，应为 %d 字节", written, asset.Size)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n != written {
			_ = os.Remove(path)
			return "", fmt.Errorf("下载不完整：收到 %d 字节，响应声明 %d 字节", written, n)
		}
	}
	defer func() { previousUpdates.Store(append(loadedUpdateDirs(), dir)) }()
	if wantSum != "" {
		if got := hex.EncodeToString(hasher.Sum(nil)); got != wantSum {
			_ = os.Remove(path)
			return "", fmt.Errorf("校验失败：下载的 SHA-256 与 Release 公布的不一致")
		}
	}
	return path, nil
}
