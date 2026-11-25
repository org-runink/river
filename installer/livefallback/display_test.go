// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// connector writes /sys/class/drm/<name>/enabled under root.
func connector(t *testing.T, root, name, state string) {
	t.Helper()
	d := filepath.Join(root, "sys/class/drm", name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "enabled"), []byte(state+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDisplayDriven(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(root string)
		want  bool
	}{
		{"no graphics device at all", func(string) {}, false},
		{"a device whose connector nothing drives", func(r string) { connector(t, r, "card0-eDP-1", "disabled") }, false},
		{"a connector that is scanned out", func(r string) { connector(t, r, "card0-eDP-1", "enabled") }, true},
		{"one of two connectors scanned out", func(r string) {
			connector(t, r, "card0-HDMI-A-1", "disabled")
			connector(t, r, "card1-eDP-1", "enabled")
		}, true},
		{"the card node alone is not a display", func(r string) {
			if err := os.MkdirAll(filepath.Join(r, "sys/class/drm/card0"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.setup(root)
			if got := displayDriven(root); got != tc.want {
				t.Fatalf("displayDriven = %v, want %v", got, tc.want)
			}
		})
	}
}

// A compositor that is present but drives no display must not count as a session: that is the
// dead screen the fallback exists to prevent.
func TestProbesNeedADrivenDisplay(t *testing.T) {
	old := driven
	defer func() { driven = old }()
	for _, tc := range []struct {
		name string
		on   bool
		want bool
	}{{"display not driven", false, false}, {"display driven", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			driven = func() bool { return tc.on }
			ps := probes()
			if len(ps) == 0 {
				t.Fatal("no probes")
			}
			// Whatever the compositor probes find on this machine, none may match when the
			// display is not driven.
			if !tc.on {
				for _, p := range ps {
					if p.find() {
						t.Fatalf("probe %q matched with no driven display", p.name)
					}
				}
			}
		})
	}
}
