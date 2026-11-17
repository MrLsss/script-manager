package main

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// windowsReserved are the names Windows refuses as file names, with or
// without an extension.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// cleanRelPath turns a path relative to a script directory into the slash
// separated form stored on disk, rejecting anything that could leave it.
func cleanRelPath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("路径不能为空")
	}
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) || filepath.IsAbs(rel) {
		return "", errors.New("路径不能是绝对路径")
	}
	rel = strings.ReplaceAll(rel, `\`, "/")
	if len(rel) >= 2 && rel[1] == ':' {
		return "", errors.New("路径不能包含盘符")
	}
	if strings.HasSuffix(rel, "/") {
		return "", errors.New("路径不能以 / 结尾")
	}
	var parts []string
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("路径不能包含 . 或 ..")
		}
		if strings.HasSuffix(part, " ") || strings.HasSuffix(part, ".") {
			return "", errors.New("文件名不能以空格或点结尾")
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return "", errors.New("文件名不能包含控制字符")
			}
		}
		stem := strings.ToLower(strings.TrimSuffix(part, path.Ext(part)))
		if windowsReserved[stem] {
			return "", errors.New("文件名不能使用 Windows 保留名 " + part)
		}
		parts = append(parts, part)
	}
	out := strings.Join(parts, "/")
	if len(out) > 255 {
		return "", errors.New("路径过长")
	}
	return out, nil
}

// withinDir reports whether target sits inside dir, comparing whole path
// elements so that scripts/sc-1 never matches scripts/sc-10.
func withinDir(dir, target string) bool {
	dir = filepath.Clean(dir)
	target = filepath.Clean(target)
	if target == dir {
		return true
	}
	return strings.HasPrefix(target, dir+string(filepath.Separator))
}

// sanitizeFileName turns a script name into a file name: characters the file
// systems refuse become "-", spaces become "-", Chinese is kept.
func sanitizeFileName(name, ext string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case strings.ContainsRune(`/\:*?"<>|`, r), r < 0x20, r == 0x7f:
			b.WriteRune('-')
		case unicode.IsSpace(r):
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), ".-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if out == "" {
		out = "script"
	}
	stem := strings.ToLower(strings.TrimSuffix(out, path.Ext(out)))
	if windowsReserved[strings.ToLower(out)] || windowsReserved[stem] {
		out = "_" + out
	}
	if ext != "" && !strings.EqualFold(path.Ext(out), "."+ext) {
		out += "." + ext
	}
	return out
}

// uniqueFileName returns name, name-2, name-3… until no entry in taken matches
// it, comparing case-insensitively for macOS and Windows.
func uniqueFileName(name string, taken []string) string {
	if !nameTakenFold(taken, name) {
		return name
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; i < 1000; i++ {
		candidate := stem + "-" + strconv.Itoa(i) + ext
		if !nameTakenFold(taken, candidate) {
			return candidate
		}
	}
	return stem + "-copy" + ext
}

// nameTakenFold matches names the way macOS and Windows file systems do.
func nameTakenFold(taken []string, name string) bool {
	for _, t := range taken {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}

// languageFromExt maps a file extension to the language the app highlights it
// as. Unknown extensions fall back to plain text.
func languageFromExt(ext string) (string, bool) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	for lang, known := range languageExts {
		if known == ext {
			return lang, true
		}
	}
	switch ext {
	case "bash", "zsh":
		return "shell", true
	case "jsx", "mjs", "cjs":
		return "javascript", true
	case "tsx", "mts", "cts":
		return "typescript", true
	case "txt", "md", "markdown", "conf", "cfg", "ini", "json", "yaml", "yml",
		"toml", "xml", "html", "css", "csv", "log", "env", "example", "gitignore":
		return "plain", true
	}
	return "", false
}

// languageForEntry is the language of the file an entry names.
func languageForEntry(entry string) (string, bool) {
	return languageFromExt(path.Ext(entry))
}

// managedIgnore reports whether a file inside a script directory is kept out
// of listings, snapshots and imports.
func managedIgnore(name string) bool {
	switch name {
	case ".DS_Store", ".git", "node_modules", ".trash", ".AppleDouble", "script.json":
		return true
	}
	return strings.HasPrefix(name, "._")
}

// isBinaryContent reports whether content should stay out of the editor: a
// NUL byte in the first 8 KB, or a file over 2 MB.
func isBinaryContent(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

// realPath resolves p to the place it really points at, following symbolic
// links in the longest existing prefix. A path that does not exist yet still
// resolves, so a new file cannot be created through a link that leaves the
// library.
func realPath(p string) (string, error) {
	p = filepath.Clean(p)
	cur := p
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("无法解析路径 %s", p)
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// validScriptID keeps an id read from disk from naming anything but one
// directory inside scripts/.
func validScriptID(id string) bool {
	if id == "" || len(id) > 64 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r == '/' || r == '\\' || r == ':' || r < 0x20 || r == 0x7f:
			return false
		}
	}
	return true
}

// realpathOrSelf resolves p, falling back to the cleaned path.
func realpathOrSelf(p string) (string, error) {
	return realPath(p)
}
