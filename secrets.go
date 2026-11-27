package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// secretRefPattern matches a value that points at the secret store instead of
// carrying the credential itself, so script.json and the Git history it syncs
// to never hold one.
var secretRefPattern = regexp.MustCompile(`\$\{secret:([^}\s]+)\}`)

// Secrets holds credentials outside the library. They are read at run time
// only, which keeps them out of script.json, out of sync and out of exports.
type Secrets struct {
	mu    sync.Mutex
	path  string
	items map[string]string
}

type secretsFile struct {
	Version int               `json:"version"`
	Items   map[string]string `json:"items"`
}

const secretsVersion = 1

// secretsPath is beside settings.json and remote.json, outside the library.
func secretsPath() (string, error) {
	dir, err := userDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "secrets.json"), nil
}

// OpenSecrets reads the secret store, making an empty one when it is missing.
func OpenSecrets(path string) (*Secrets, error) {
	s := &Secrets{path: path, items: map[string]string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f secretsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("secrets.json 无法解析: %w", err)
	}
	if f.Items != nil {
		s.items = f.Items
	}
	return s, nil
}

// emptySecrets keeps the app working when the preferences directory is not
// available, for example in tests: it simply does not persist.
func emptySecrets() *Secrets {
	return &Secrets{items: map[string]string{}}
}

func (s *Secrets) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(secretsFile{Version: secretsVersion, Items: s.items}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	// 0600 from the start: the store holds credentials, and a world readable
	// window between write and rename would defeat the point.
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns the secret names, sorted, never their values.
func (s *Secrets) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.items))
	for k := range s.items {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Get looks one secret up.
func (s *Secrets) Get(name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.items[name]
	return v, ok
}

// Has reports whether a secret exists.
func (s *Secrets) Has(name string) bool {
	_, ok := s.Get(name)
	return ok
}

// Set stores a secret.
func (s *Secrets) Set(name, value string) error {
	name = strings.TrimSpace(name)
	if !validSecretName(name) {
		return errors.New("密钥名称只能用字母、数字、点、横线和下划线")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = map[string]string{}
	}
	s.items[name] = value
	return s.saveLocked()
}

// Delete removes a secret.
func (s *Secrets) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, name)
	return s.saveLocked()
}

func validSecretName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return true
}

// MissingSecrets lists the references an environment points at that the store
// does not hold, so a run fails before it starts instead of running with an
// empty credential.
func MissingSecrets(env map[string]string, s *Secrets) []string {
	var missing []string
	seen := map[string]bool{}
	for _, value := range env {
		for _, m := range secretRefPattern.FindAllStringSubmatch(value, -1) {
			name := m[1]
			if seen[name] || s.Has(name) {
				continue
			}
			seen[name] = true
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// resolveEnv replaces ${secret:name} references with the stored values. It
// fails with the missing names rather than letting a blank credential through.
func resolveEnv(env map[string]string, s *Secrets) (map[string]string, error) {
	if len(env) == 0 {
		return env, nil
	}
	if missing := MissingSecrets(env, s); len(missing) > 0 {
		return nil, fmt.Errorf("密钥库缺少 %s，先到「密钥」里补上", strings.Join(missing, "、"))
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = secretRefPattern.ReplaceAllStringFunc(v, func(ref string) string {
			m := secretRefPattern.FindStringSubmatch(ref)
			value, _ := s.Get(m[1])
			return value
		})
	}
	return out, nil
}

// hasSecretRef reports whether a value points at the store, which the UI shows
// instead of the credential itself.
func hasSecretRef(value string) bool {
	return secretRefPattern.MatchString(value)
}

// maskSecretRefs hides a stored reference for display: the UI says where the
// value comes from without printing it.
func maskSecretRefs(value string) string {
	return secretRefPattern.ReplaceAllStringFunc(value, func(ref string) string {
		m := secretRefPattern.FindStringSubmatch(ref)
		return "${secret:" + m[1] + "}"
	})
}

// suspiciousEnvSecrets finds environment values that look like a literal
// credential rather than a reference, so a sync can warn before the value ends
// up in the repository history.
func suspiciousEnvSecrets(scripts []Script) []string {
	var hits []string
	for _, sc := range scripts {
		for _, key := range envKeys(sc.Env) {
			value := sc.Env[key]
			if value == "" || hasSecretRef(value) {
				continue
			}
			if looksLikeCredential(key, value) {
				hits = append(hits, sc.Name+"："+key)
			}
		}
	}
	sort.Strings(hits)
	return hits
}

func looksLikeCredential(key, value string) bool {
	upper := strings.ToUpper(key)
	for _, word := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "APIKEY", "API_KEY",
		"ACCESS_KEY", "PRIVATE_KEY", "CREDENTIAL"} {
		if strings.Contains(upper, word) {
			return true
		}
	}
	// The same shapes the log redaction recognizes, for keys that do not
	// announce themselves.
	return redactSecrets(value) != value
}
