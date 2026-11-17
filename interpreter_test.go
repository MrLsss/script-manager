package main

import (
	"os/exec"
	"testing"
)

func TestDetectInterpretersFindsShell(t *testing.T) {
	found := DetectInterpreters(nil)
	if len(found) == 0 {
		t.Fatal("no interpreter detected")
	}
	ok := 0
	for i := range found {
		Probe(&found[i])
		if found[i].State == StateOK && found[i].Version != "" {
			ok++
		}
	}
	if ok == 0 {
		t.Fatalf("no usable interpreter among %d detected", len(found))
	}
}

func TestProbeReadsVersion(t *testing.T) {
	path, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	it := Interpreter{Path: path, ProbeArgs: []string{"-c", "echo 5.4.1"}}
	Probe(&it)
	if it.State != StateOK {
		t.Fatalf("state = %s", it.State)
	}
	if it.Version != "5.4.1" {
		t.Fatalf("version = %q", it.Version)
	}
}

func TestCommandArgsExpandsScript(t *testing.T) {
	it := Interpreter{Path: "/usr/bin/python3", Args: []string{"-u", "{script}"}}
	got := commandArgs(it, "/lib/a.py", "/lib", []string{"x", "y"})
	want := []string{"-u", "/lib/a.py", "x", "y"}
	if len(got) != len(want) {
		t.Fatalf("args = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v", got)
		}
	}
}
