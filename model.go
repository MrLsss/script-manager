package main

import "time"

// Folder groups scripts in the library. The tree lives in folders.json; each
// script's files live in their own directory under scripts/<id>/.
type Folder struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

// Interpreter states.
const (
	StateOK          = "ok"
	StateMissing     = "missing"
	StateNoExec      = "noexec"
	StateProbeFailed = "probe-failed"
)

// Interpreter is a program that runs scripts, as python3 or zsh.
type Interpreter struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Language  string    `json:"language"`
	Path      string    `json:"path"`
	Args      []string  `json:"args"`
	ProbeArgs []string  `json:"probe_args"`
	Version   string    `json:"version"`
	State     string    `json:"state"`
	CheckedAt time.Time `json:"checked_at"`
	Detected  bool      `json:"detected"`
}

// Script is one script the app manages. Its files live in
// scripts/<id>/, and Entry names the file that runs, relative to that
// directory with / as the separator.
type Script struct {
	ID         string            `json:"id"`
	FolderID   string            `json:"folder_id"`
	Name       string            `json:"name"`
	Entry      string            `json:"entry"`
	Language   string            `json:"language"`
	InterpID   string            `json:"interp_id,omitempty"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env,omitempty"`
	Cwd        string            `json:"cwd"`
	Timeout    int               `json:"timeout_sec"`
	RunAsAdmin bool              `json:"run_as_admin,omitempty"`
	Dangerous  bool              `json:"dangerous,omitempty"`
	Preflight  bool              `json:"preflight,omitempty"`
	Profiles   []RunProfile      `json:"profiles,omitempty"`
	ProfileID  string            `json:"profile_id,omitempty"`
	// DefaultName is what the unnamed default parameter set is called.
	// Empty means it still shows as 默认.
	DefaultName string            `json:"default_name,omitempty"`
	HostID     string            `json:"host_id,omitempty"`
	HostName   string            `json:"host_name,omitempty"`
	HostTarget string            `json:"host_target,omitempty"`
	Position   int               `json:"position"`
	CreatedAt  time.Time         `json:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at"`

	// Ext is derived from Entry and never written to script.json, so there
	// is only one source of truth for the file that runs.
	Ext string `json:"-"`
}

// ScriptFile is one regular file inside a script's directory.
type ScriptFile struct {
	Path     string // relative to the script directory, / separated
	Size     int64
	Mode     uint32
	Editable bool
}

// SyncConflict is a script changed on this machine and on the remote, waiting
// for the user to choose which version to keep.
type SyncConflict struct {
	ScriptID   string    `json:"script_id"`
	ScriptName string    `json:"script_name"`
	RemoteHash string    `json:"remote_hash"`
	CreatedAt  time.Time `json:"created_at"`
}

// Revision is a snapshot of a script's whole directory at one moment, kept
// before it runs and when it is saved.
type Revision struct {
	ID        string    `json:"id"`
	ScriptID  string    `json:"script_id"`
	Hash      string    `json:"hash"`
	Path      string    `json:"path"`
	Entry     string    `json:"entry"`
	Files     int       `json:"files"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// RevisionFile is one file inside a snapshot.
type RevisionFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Mode uint32 `json:"mode"`
}

// Run states.
const (
	RunRunning   = "running"
	RunOK        = "ok"
	RunFailed    = "failed"
	RunCancelled = "cancelled"
	RunTimeout   = "timeout"
	RunAborted   = "aborted"
	RunUnknown   = "unknown"
)

// Run records one execution of a script and the log it wrote.
type Run struct {
	ID            string    `json:"id"`
	ScriptID      string    `json:"script_id"`
	RevID         string    `json:"rev_id"`
	InterpID      string    `json:"interp_id"`
	InterpName    string    `json:"interp_name"`
	InterpVersion string    `json:"interp_version"`
	Entry         string    `json:"entry"`
	Admin         bool      `json:"admin,omitempty"`
	Dangerous     bool      `json:"dangerous,omitempty"`
	HostID        string    `json:"host_id,omitempty"`
	HostName      string    `json:"host_name,omitempty"`
	RemoteDir     string    `json:"remote_dir,omitempty"`
	RemoteInterp  string    `json:"remote_interp,omitempty"`
	Args          []string  `json:"args"`
	Cwd           string    `json:"cwd"`
	Status        string    `json:"status"`
	Uploading     bool      `json:"uploading,omitempty"`
	ExitCode      int       `json:"exit_code"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	LogPath       string    `json:"log_path"`
	LogBytes      int64     `json:"log_bytes"`
	Truncated     bool      `json:"truncated"`
}

// Library is the whole index. It is split across folders.json, one
// script.json per script and local.json, which is why the struct itself is
// only a view used while reading and migrating.
type Library struct {
	Version      int            `json:"version"`
	Folders      []*Folder      `json:"folders"`
	Interpreters []*Interpreter `json:"interpreters"`
	Scripts      []*Script      `json:"scripts"`
	Revisions    []*Revision    `json:"revisions"`
	Runs         []*Run         `json:"runs"`
}

// LogLine is one line of a run's output.
type LogLine struct {
	Kind int // 0 stdout, 1 stderr, 2 header, 3 footer
	Text string
}
