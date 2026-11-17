package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// preflight checks the entry's syntax before a run. It is opt-in per script:
// only shell, Python and JavaScript have a cheap, side-effect-free check.
func (a *App) preflightLocal(sc Script, it Interpreter) error {
	lang, _ := languageForEntry(sc.Entry)
	entry, err := a.store.EntryPath(sc)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch lang {
	case "shell":
		cmd = exec.CommandContext(ctx, it.Path, "-n", entry)
	case "python":
		// tokenize.open honors a PEP 263 coding cookie, so a GBK source file
		// is checked in its own encoding.
		cmd = exec.CommandContext(ctx, it.Path, "-c",
			`import ast,sys,tokenize; ast.parse(tokenize.open(sys.argv[1]).read())`, entry)
	case "javascript":
		cmd = exec.CommandContext(ctx, it.Path, "--check", entry)
	default:
		return nil
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", checkMessage(out, err))
	}
	return nil
}

func (a *App) preflightRemote(sc Script, h RemoteHost, ri RemoteInterp) error {
	lang, _ := languageForEntry(sc.Entry)
	// The entry is streamed to the far side and checked there, so the check
	// uses the remote interpreter and the file's own encoding.
	if strings.TrimSpace(ri.Path) == "" {
		return errors.New("远程解释器还没有确定路径，先到连接页重新检测")
	}
	var command string
	switch lang {
	case "shell":
		command = shQuote(ri.Path) + " -n"
	case "python":
		command = shQuote(ri.Path) + ` -c 'import ast,sys,tokenize; ast.parse(tokenize.open(sys.argv[1]).read())'`
	case "javascript":
		command = shQuote(ri.Path) + " --check"
	default:
		return nil
	}
	entry, err := a.store.EntryPath(sc)
	if err != nil {
		return err
	}
	f, err := os.Open(entry)
	if err != nil {
		return err
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := sshWithInput(ctx, h, "tmp=$(mktemp) && cat > \"$tmp\" && "+command+" \"$tmp\"; code=$?; rm -f \"$tmp\"; exit $code", f)
	if err != nil {
		return errors.New(checkMessage([]byte(out), err))
	}
	return nil
}

func checkMessage(out []byte, err error) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "语法检查未通过：" + err.Error()
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 6 {
		lines = lines[:6]
	}
	return "语法检查未通过：\n" + strings.Join(lines, "\n")
}
