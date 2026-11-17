package main

import "regexp"

// secretPatterns mask the common ways a log line carries a credential. The
// goal is to keep the line readable ("TOKEN=***") rather than to hide every
// byte of output.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)\b(Bearer)\s+[A-Za-z0-9._~+/-]+=*`), `${1} ***`},
	{regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key)(\s*[:=]\s*)([^\s,;]+)`), `${1}${2}***`},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), `AKIA***`},
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://[^:/\s]+):([^@/\s]+)@`), `${1}:***@`},
}

// redactSecrets replaces credentials in one line of output.
func redactSecrets(line string) string {
	for _, p := range secretPatterns {
		line = p.re.ReplaceAllString(line, p.repl)
	}
	return line
}
