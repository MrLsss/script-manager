package main

import "fmt"

// RunProfile is one saved set of run parameters. The empty profile id is the
// script's default: the fields stored on Script itself.
type RunProfile struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Cwd        string            `json:"cwd,omitempty"`
	Timeout    int               `json:"timeout_sec,omitempty"`
	RunAsAdmin bool              `json:"run_as_admin,omitempty"`
}

// profileByID returns one named profile.
func (sc Script) profileByID(id string) (RunProfile, bool) {
	if id == "" {
		return RunProfile{}, false
	}
	for _, p := range sc.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return RunProfile{}, false
}

// activeProfile returns the selected named profile, if it still exists.
func (sc Script) activeProfile() (RunProfile, bool) {
	return sc.profileByID(sc.ProfileID)
}

// applyRunProfile returns a copy of the script with the selected profile's
// fields in place, ready for the runner. The run target stays on the script.
func applyRunProfile(sc Script) Script {
	p, ok := sc.activeProfile()
	if !ok {
		return sc
	}
	sc.Args = p.Args
	sc.Env = p.Env
	sc.Cwd = p.Cwd
	sc.Timeout = p.Timeout
	sc.RunAsAdmin = p.RunAsAdmin
	return sc
}

// defaultProfileName is what the unnamed set on the script itself is
// called: a name the user gave it, or 默认 before that.
func (sc Script) defaultProfileName() string {
	if sc.DefaultName != "" {
		return sc.DefaultName
	}
	return tr("默认")
}

// profileLabel is what the header button shows: 参数 plus the name of the set
// the next run uses, once anything has been named.
func profileLabel(sc Script) string {
	if p, ok := sc.activeProfile(); ok {
		return tr("参数") + " · " + p.Name
	}
	if sc.DefaultName != "" {
		return tr("参数") + " · " + sc.DefaultName
	}
	return tr("参数")
}

// profileNameTaken reports a duplicate name among a script's profiles.
func profileNameTaken(profiles []RunProfile, name, exceptID string) bool {
	for _, p := range profiles {
		if p.ID != exceptID && p.Name == name {
			return true
		}
	}
	return false
}

// nextProfileName suggests a unique name for "另存为新配置".
func nextProfileName(profiles []RunProfile) string {
	for i := 2; i < 1000; i++ {
		name := fmt.Sprintf("配置 %d", i)
		if !profileNameTaken(profiles, name, "") {
			return name
		}
	}
	return "新配置"
}
