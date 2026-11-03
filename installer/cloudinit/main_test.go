// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import "testing"

func TestMovedMountpoint(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"/var/lib/core", "/var/lib/tide"},
		{"/var/lib/core/models/shared", "/var/lib/tide/models/shared"},
		{"/var/lib/tide", ""},
		{"/var/lib/core-runner", ""},
		{"/var/lib/corex/models", ""},
		{"/home", ""},
		{"", ""},
	} {
		got, ok := movedMountpoint(c.in)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("movedMountpoint(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
	}
}

// Every data dataset an older image mounted under /var/lib/core must map onto its current
// mountpoint, or the migration would never fire for it.
func TestLegacyDatasetsMapToCurrent(t *testing.T) {
	for _, d := range dataDatasets {
		old := ""
		switch d.name {
		case "state":
			old = legacyStateDir
		case "models":
			old = legacyStateDir + "/models/shared"
		default:
			continue
		}
		if got, ok := movedMountpoint(old); !ok || got != d.mountpoint {
			t.Errorf("%s: old path %s maps to %q, current mountpoint is %s", d.name, old, got, d.mountpoint)
		}
	}
}
