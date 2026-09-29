// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The exit codes are part of the CLI contract (docs/INSTALLER-HARDWARE.md).
func TestExitCodes(t *testing.T) {
	td := "../internal/planner/testdata"
	man := filepath.Join(td, "models.tiers")
	ok := filepath.Join(td, "server-64g.probe.json")
	refused := filepath.Join(td, "edge-16g.probe.json")

	// Silence stdout/stderr for the table.
	null, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	so, se := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = null, null
	defer func() { os.Stdout, os.Stderr = so, se }()

	planFile := filepath.Join(t.TempDir(), "plan.json")
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"json", []string{"--probe", ok, "--manifest", man, "--json"}, 0},
		{"text", []string{"--probe", ok, "--manifest", man}, 0},
		{"no-models", []string{"--probe", ok, "--no-models", "--json"}, 0},
		{"refused", []string{"--probe", refused, "--manifest", man, "--json"}, 3},
		{"lab", []string{"--probe", refused, "--manifest", man, "--lab", "--json"}, 0},
		{"env confirmed", []string{"--probe", ok, "--manifest", man, "--env", "--confirm", "NVA0001", "--confirm", "NVA0002"}, 0},
		{"env unconfirmed", []string{"--probe", ok, "--manifest", man, "--env", "--confirm", "NVA0001"}, 4},
		{"env refused", []string{"--probe", refused, "--manifest", man, "--env", "--confirm", "SSD0256"}, 4},
		{"no probe", []string{"--manifest", man}, 2},
		{"both manifest and no-models", []string{"--probe", ok, "--manifest", man, "--no-models"}, 2},
		{"json and env", []string{"--probe", ok, "--manifest", man, "--json", "--env"}, 2},
		{"plan-file with manifest", []string{"--plan-file", planFile, "--manifest", man}, 2},
		{"list-disks", []string{"--probe", ok, "--manifest", man, "--list-disks"}, 0},
		{"list-disks and json", []string{"--plan-file", planFile, "--list-disks", "--json"}, 2},
		{"env without probe", []string{"--plan-file", planFile, "--env"}, 2},
		{"missing manifest", []string{"--probe", ok, "--manifest", "/nonexistent"}, 1},
		{"stray arg", []string{"--probe", ok, "--manifest", man, "extra"}, 2},
		// Workstation profile: the server-refused 16 GiB edge machine and the 25 GiB laptop pass.
		{"workstation edge", []string{"--probe", refused, "--no-models", "--profile", "workstation", "--json"}, 0},
		{"workstation laptop", []string{"--probe", filepath.Join(td, "workstation-25g.probe.json"), "--no-models", "--profile", "workstation", "--json"}, 0},
		{"workstation with manifest", []string{"--probe", ok, "--manifest", man, "--profile", "workstation"}, 2},
		{"unknown profile", []string{"--probe", ok, "--no-models", "--profile", "desktop"}, 2},
		{"plan-file with profile", []string{"--plan-file", planFile, "--profile", "server"}, 2},
	}
	for _, c := range cases {
		if got := run(c.args); got != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, got, c.want)
		}
	}

	// Round trip: a plan written to a file resolves against a fresh probe.
	f, err := os.Create(planFile)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = f
	if got := run([]string{"--probe", ok, "--manifest", man, "--json"}); got != 0 {
		t.Fatalf("write plan: exit %d", got)
	}
	f.Close()
	os.Stdout = null
	if got := run([]string{"--plan-file", planFile, "--probe", ok, "--env", "--confirm", "NVA0001", "--confirm", "NVA0002"}); got != 0 {
		t.Errorf("plan-file resolve: exit %d", got)
	}
	if got := run([]string{"--plan-file", planFile, "--list-disks"}); got != 0 {
		t.Errorf("plan-file list-disks: exit %d", got)
	}
	if got := run([]string{"--plan-file", planFile}); got != 0 {
		t.Errorf("plan-file render: exit %d", got)
	}
}
