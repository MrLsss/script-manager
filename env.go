package main

import (
	"fmt"
	"sort"
	"strings"
)

// parseEnvLines reads KEY=VALUE lines. Blank lines and # comments are
// ignored; values may contain spaces and =.
func parseEnvLines(text string) (map[string]string, error) {
	env := map[string]string{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("第 %d 行缺少 =", i+1)
		}
		key = strings.TrimSpace(key)
		if !validEnvKey(key) {
			return nil, fmt.Errorf("第 %d 行的变量名 %q 不合法", i+1, key)
		}
		env[key] = strings.TrimSpace(value)
	}
	return env, nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		switch {
		case r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// formatEnv renders the map as stable KEY=VALUE lines for the editor.
func formatEnv(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + env[k] + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// applyScriptEnv overrides the process environment with a script's own
// variables. Keys that are not shell identifiers are dropped.
func applyScriptEnv(base []string, extra map[string]string) []string {
	extra = sanitizeEnv(extra)
	if len(extra) == 0 {
		return base
	}
	values := map[string]string{}
	var order []string
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, seen := values[key]; !seen {
			order = append(order, key)
		}
		values[key] = entry
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, seen := values[k]; !seen {
			order = append(order, k)
		}
		values[k] = k + "=" + extra[k]
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, values[k])
	}
	return out
}

// sanitizeEnv drops keys that are not shell identifiers. A hand-edited or
// synced script.json must never smuggle a command into the remote shell, where
// keys are written into a script verbatim.
func sanitizeEnv(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if validEnvKey(k) {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// invalidEnvKey returns the first key that is not a shell identifier.
func invalidEnvKey(env map[string]string) string {
	for k := range env {
		if !validEnvKey(k) {
			return k
		}
	}
	return ""
}
