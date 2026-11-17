package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// adminLaunch is a command that asks the system for administrator rights and
// runs the interpreter through a wrapper script. The wrapper writes the
// output to the log and the exit code to a status file, because an elevated
// process has no usable pipes back to the app.
type adminLaunch struct {
	Program   string
	Args      []string
	Wrapper   string
	status    string
	cleanupFn func()
}

func (l adminLaunch) cleanup() {
	if l.cleanupFn != nil {
		l.cleanupFn()
	}
}

// newAdminLaunch builds the wrapper and the command that starts it for the
// current system. It touches nothing but a temporary directory.
func newAdminLaunch(it Interpreter, args []string, cwd, logPath, statusPath string) (adminLaunch, error) {
	dir, err := os.MkdirTemp("", "script-manager-admin-")
	if err != nil {
		return adminLaunch{}, err
	}
	launch := adminLaunch{status: statusPath}
	launch.cleanupFn = func() { _ = os.RemoveAll(dir) }
	// A window opened from Finder has a short PATH; the wrapper needs the
	// login PATH or it cannot find python3, node and friends.
	pathEnv := loginPATH()
	home := os.Getenv("HOME")
	switch runtime.GOOS {
	case "windows":
		wrapper := filepath.Join(dir, "run.cmd")
		if err := os.WriteFile(wrapper, []byte(windowsAdminWrapper(it.Path, args, cwd, logPath, statusPath, pathEnv, home)), 0o700); err != nil {
			launch.cleanup()
			return adminLaunch{}, err
		}
		launch.Wrapper = wrapper
		launch.Program = "powershell"
		launch.Args = []string{"-NoProfile", "-Command",
			"Start-Process -Verb RunAs -Wait -FilePath " + psQuote(wrapper)}
	default:
		wrapper := filepath.Join(dir, "run.sh")
		if err := os.WriteFile(wrapper, []byte(unixAdminWrapper(it.Path, args, cwd, logPath, statusPath, pathEnv, home)), 0o700); err != nil {
			launch.cleanup()
			return adminLaunch{}, err
		}
		launch.Wrapper = wrapper
		if runtime.GOOS == "darwin" {
			launch.Program = "osascript"
			launch.Args = []string{"-e",
				`do shell script "` + appleScriptEscape("/bin/sh "+shQuote(wrapper)) + `" with administrator privileges`}
		} else {
			launch.Program = "pkexec"
			launch.Args = []string{"/bin/sh", wrapper}
		}
	}
	return launch, nil
}

// unixAdminWrapper is the shell the system runs with administrator rights. It
// sets the environment an elevated process loses, redirects output and leaves
// the exit code where the app can read it.
func unixAdminWrapper(interp string, args []string, cwd, logPath, statusPath, pathEnv, home string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("cd " + shQuote(cwd) + " || exit 125\n")
	b.WriteString("export PATH=" + shQuote(pathEnv) + "\n")
	if home != "" {
		b.WriteString("export HOME=" + shQuote(home) + "\n")
	}
	b.WriteString(shQuote(interp))
	for _, a := range args {
		b.WriteString(" " + shQuote(a))
	}
	b.WriteString(" >> " + shQuote(logPath) + " 2>&1\n")
	b.WriteString("code=$?\n")
	b.WriteString("printf '%s' \"$code\" > " + shQuote(statusPath) + "\n")
	b.WriteString("exit \"$code\"\n")
	return b.String()
}

// windowsAdminWrapper is the batch file started elevated on Windows. A cmd
// wrapper is used because Start-Process -Verb RunAs cannot redirect output on
// its own.
func windowsAdminWrapper(interp string, args []string, cwd, logPath, statusPath, pathEnv, home string) string {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("cd /d " + cmdQuote(cwd) + "\r\n")
	b.WriteString("set \"PATH=" + pathEnv + "\"\r\n")
	if home != "" {
		b.WriteString("set \"HOME=" + home + "\"\r\n")
	}
	b.WriteString(cmdQuote(interp))
	for _, a := range args {
		b.WriteString(" " + cmdQuote(a))
	}
	b.WriteString(" >> " + cmdQuote(logPath) + " 2>&1\r\n")
	b.WriteString("echo %ERRORLEVEL%> " + cmdQuote(statusPath) + "\r\n")
	b.WriteString("exit /b %ERRORLEVEL%\r\n")
	return b.String()
}

// shQuote makes one argv element safe inside a POSIX shell script.
func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appleScriptEscape makes text safe inside an AppleScript string literal.
func appleScriptEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// psQuote makes text safe inside a PowerShell single-quoted string.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// cmdQuote makes one argv element safe inside a batch file.
func cmdQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`^"`)
		case '^', '&', '|', '<', '>', '(', ')', '%', '!':
			b.WriteByte('^')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// readExitStatus reads the code an elevated wrapper wrote.
func readExitStatus(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return code, true
}

// waitExitStatus gives the wrapper a moment to flush its status file.
func waitExitStatus(path string) (int, bool) {
	for i := 0; i < 20; i++ {
		if code, ok := readExitStatus(path); ok {
			return code, true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0, false
}

// adminStatusPath is where a run's exit code lands.
func (s *Store) adminStatusPath(scriptID, runID string) string {
	return filepath.Join(s.root, "logs", scriptID, runID+".status")
}

// adminLaunchProgram reports whether the helper needed for elevation exists.
func adminLaunchProgram() (string, error) {
	program := "pkexec"
	if runtime.GOOS == "darwin" {
		program = "osascript"
	}
	if runtime.GOOS == "windows" {
		program = "powershell"
	}
	if _, err := exec.LookPath(program); err != nil {
		return program, errors.New("没有找到 " + program + "，无法请求管理员权限")
	}
	return program, nil
}
