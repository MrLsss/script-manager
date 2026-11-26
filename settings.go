package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"github.com/egoist/mygo"
)

// Settings are the preferences of the app, kept outside the library so a
// change of library keeps them.
type Settings struct {
	Library        string `json:"library"`
	DefaultTimeout int    `json:"default_timeout_sec"`
	RetentionDays  int    `json:"retention_days"`
	MaxLogBytes    int64  `json:"max_log_bytes"`
	KeepRevisions  int    `json:"keep_revisions"`
	Notify         bool   `json:"notify"`
	NotifyState    string `json:"notify_state"`
	OpenPanel      bool   `json:"open_panel"`
	Appearance     string `json:"appearance"`
	Language       string `json:"language"`
	ProxyEnabled   bool   `json:"proxy_enabled"`
	ProxyURL       string `json:"proxy_url"`
	SnapshotKeep   int    `json:"snapshot_keep"`
	Concurrency    int    `json:"concurrency"`
	RedactLogs     bool   `json:"redact_logs"`
}

func defaultSettings() Settings {
	return Settings{
		Library:        defaultLibraryRoot(),
		DefaultTimeout: 0,
		RetentionDays:  30,
		MaxLogBytes:    20 << 20,
		KeepRevisions:  50,
		Notify:         false,
		OpenPanel:      true,
		Appearance:     "system",
		ProxyEnabled:   false,
		ProxyURL:       defaultProxyURL,
		SnapshotKeep:   30,
		RedactLogs:     true,
	}
}

// suggestedConcurrency is the default limit: one run per core, capped at 8 so
// a large machine does not open dozens of processes at once.
func suggestedConcurrency() int {
	n := runtime.NumCPU()
	if n < 1 {
		n = 1
	}
	if n > 8 {
		n = 8
	}
	return n
}

func userDataDir() (string, error) {
	if dir := os.Getenv("SCRIPT_MANAGER_HOME"); dir != "" {
		return dir, nil
	}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return dir, nil
}

func settingsPath() (string, error) {
	dir, err := userDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

func defaultLibraryRoot() string {
	if dir := os.Getenv("SCRIPT_MANAGER_LIB"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "ScriptManager")
}

// LoadSettings reads the preferences, making defaults when there are none.
func LoadSettings() (Settings, error) {
	s := defaultSettings()
	path, err := settingsPath()
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	if s.Library == "" {
		s.Library = defaultLibraryRoot()
	}
	if s.MaxLogBytes <= 0 {
		s.MaxLogBytes = 20 << 20
	}
	if s.KeepRevisions <= 0 {
		s.KeepRevisions = 50
	}
	if s.ProxyURL == "" {
		s.ProxyURL = defaultProxyURL
	}
	if s.SnapshotKeep == 0 {
		s.SnapshotKeep = 30
	}
	return s, nil
}

// Save writes the preferences.
func (s Settings) Save() error {
	dir, err := userDataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.json")
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
