package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type candidate struct {
	binary   string
	language string
	probe    []string
}

var candidates = []candidate{
	{"python3", "python", []string{"--version"}},
	{"python", "python", []string{"--version"}},
	{"node", "javascript", []string{"--version"}},
	{"bun", "typescript", []string{"--version"}},
	{"deno", "typescript", []string{"--version"}},
	{"zsh", "shell", []string{"--version"}},
	{"bash", "shell", []string{"--version"}},
	{"pwsh", "powershell", []string{"--version"}},
	{"ruby", "ruby", []string{"--version"}},
	{"perl", "perl", []string{"--version"}},
	{"lua", "lua", []string{"-v"}},
	{"go", "go", []string{"version"}},
	{"swift", "swift", []string{"--version"}},
	{"dart", "dart", []string{"--version"}},
	{"php", "php", []string{"--version"}},
	{"Rscript", "r", []string{"--version"}},
}

// languageNames are how languages show in the interface.
var languageNames = map[string]string{
	"python": "Python", "javascript": "JavaScript", "typescript": "TypeScript",
	"shell": "Shell", "powershell": "PowerShell", "ruby": "Ruby", "perl": "Perl",
	"lua": "Lua", "go": "Go", "swift": "Swift", "dart": "Dart", "php": "PHP",
	"r": "R", "plain": "纯文本",
}

// languageExts are the file extensions scripts of a language take.
var languageExts = map[string]string{
	"python": "py", "javascript": "js", "typescript": "ts", "shell": "sh",
	"powershell": "ps1", "ruby": "rb", "perl": "pl", "lua": "lua", "go": "go",
	"swift": "swift", "dart": "dart", "php": "php", "r": "r", "plain": "txt",
}

// languageOrder sorts languages in pickers.
var languageOrder = []string{"python", "shell", "javascript", "typescript", "ruby",
	"lua", "perl", "go", "php", "swift", "dart", "r", "powershell", "plain"}

func languageName(lang string) string {
	if name, ok := languageNames[lang]; ok {
		return name
	}
	return lang
}

func languageExt(lang string) string {
	if ext, ok := languageExts[lang]; ok {
		return ext
	}
	return "txt"
}

var versionPattern = regexp.MustCompile(`\d+\.\d+(?:\.\d+)?`)

// loginPATH returns the PATH of the user's login shell: a window opened from
// Finder does not get the PATH a terminal has.
func loginPATH() string {
	path := os.Getenv("PATH")
	shell := os.Getenv("SHELL")
	if shell == "" {
		return path
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, shell, "-lc", `printf %s "$PATH"`).Output()
	if err != nil {
		return path
	}
	extra := string(out)
	if strings.TrimSpace(extra) == "" {
		return path
	}
	seen := map[string]bool{}
	var parts []string
	for _, dir := range append(strings.Split(extra, ":"), strings.Split(path, ":")...) {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		parts = append(parts, dir)
	}
	return strings.Join(parts, ":")
}

// Probe checks an interpreter: whether the file is there, is executable, and
// which version it reports.
func Probe(it *Interpreter) {
	it.CheckedAt = time.Now()
	info, err := os.Stat(it.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		it.State, it.Version = StateMissing, ""
		return
	case err != nil:
		it.State, it.Version = StateProbeFailed, ""
		return
	case info.IsDir() || info.Mode()&0o111 == 0:
		it.State, it.Version = StateNoExec, ""
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, it.Path, it.ProbeArgs...)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		it.State, it.Version = StateProbeFailed, ""
		return
	}
	if m := versionPattern.FindString(text); m != "" {
		it.Version = m
	} else {
		it.Version = ""
	}
	it.State = StateOK
}

// DetectInterpreters finds the interpreters installed on this machine.
func DetectInterpreters(existing []Interpreter) []Interpreter {
	path := loginPATH()
	seen := map[string]bool{}
	for _, it := range existing {
		seen[it.Path] = true
	}
	var found []Interpreter
	for _, cand := range candidates {
		full, err := lookPathIn(cand.binary, path)
		if err != nil || seen[full] {
			continue
		}
		seen[full] = true
		it := Interpreter{
			Name:      cand.binary,
			Language:  cand.language,
			Path:      full,
			Args:      []string{"{script}"},
			ProbeArgs: cand.probe,
			Detected:  true,
		}
		Probe(&it)
		found = append(found, it)
	}
	return found
}

func lookPathIn(name, path string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, nil
	}
	for _, dir := range strings.Split(path, string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		full := filepath.Join(dir, name)
		info, err := os.Stat(full)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return full, nil
		}
	}
	return "", errors.New("not found")
}

// commandArgs builds the argument list of a run: the interpreter's template
// with {script} and {dir} replaced, then the script's own arguments. A
// template without {script} gets the entry path appended, so an entry is
// always what runs.
func commandArgs(it Interpreter, scriptPath, dir string, extra []string) []string {
	replaced := false
	var args []string
	for _, a := range it.Args {
		if strings.Contains(a, "{script}") {
			replaced = true
		}
		a = strings.ReplaceAll(a, "{script}", scriptPath)
		a = strings.ReplaceAll(a, "{dir}", dir)
		args = append(args, a)
	}
	if !replaced {
		args = append(args, scriptPath)
	}
	return append(args, extra...)
}

// interpreterRunsEntry checks that an interpreter speaks the language of the
// entry file. Binding bash to init.py would silently run the wrong program.
func interpreterRunsEntry(it Interpreter, entry string) error {
	lang, ok := languageForEntry(entry)
	if !ok || lang == "plain" {
		return nil
	}
	if it.Language != lang {
		return fmt.Errorf("解释器「%s」是 %s，入口 %s 是 %s，请更换解释器",
			it.Name, languageName(it.Language), entry, languageName(lang))
	}
	return nil
}
