package main

import "github.com/egoist/mygo"

// checkUpdates opens the updater window. main sets it to the updater
// plugin's CheckForUpdates; tests, and builds without the plugin, keep the
// no-op.
var checkUpdates = func() {}

// appVersion is the version mygo build injected from mygo.json, which the
// updater compares releases against. Under go run and go test it is empty.
func appVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "dev"
}
