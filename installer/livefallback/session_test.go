// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The "Install only" GRUB entry passes river.installer=1; anything else is the try session.
func TestInstallerOnly(t *testing.T) {
	for cmdline, want := range map[string]bool{
		"label=RIVER overlay=livefs quiet splash":                    false,
		"label=RIVER overlay=livefs quiet splash river.installer=1":  true,
		"label=RIVER river.installer":                                true,
		"label=RIVER river.installer=yes":                            true,
		"label=RIVER river.installer=0":                              false,
		"label=RIVER river.installers=1":                             false,
		"label=RIVER overlay=livefs nomodeset river.text=1":          false,
		"label=RIVER overlay=livefs quiet splash river.installer=ON": true,
	} {
		if got := installerOnly(cmdline); got != want {
			t.Errorf("installerOnly(%q) = %v, want %v", cmdline, got, want)
		}
	}
}

// A desktop that dies at once, or fails before the budget, did not start; one that ran for a
// while and ended was logged out of, and must not be replaced by the kiosk.
func TestDesktopFailed(t *testing.T) {
	cases := []struct {
		rc   int
		ran  time.Duration
		want bool
	}{
		{1, 2 * time.Second, true},
		{0, 2 * time.Second, true},
		{0, 20 * time.Second, false},
		{1, 20 * time.Second, true},
		{1, desktopStartBudget, false},
		{0, 10 * time.Minute, false},
		{127, 0, true},
	}
	for _, c := range cases {
		if got := desktopFailed(c.rc, c.ran); got != c.want {
			t.Errorf("desktopFailed(%d, %s) = %v, want %v", c.rc, c.ran, got, c.want)
		}
	}
}

func TestCappedWriterStops(t *testing.T) {
	var b bytes.Buffer
	w := &cappedWriter{w: &b, max: 10}
	for _, s := range []string{"12345", "6789", "abcdef", "more"} {
		if n, err := w.Write([]byte(s)); err != nil || n != len(s) {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	got := b.String()
	if !strings.HasPrefix(got, "123456789") || !strings.Contains(got, "session log stopped at 10 bytes") || strings.Contains(got, "abc") {
		t.Errorf("capped log = %q", got)
	}
	if strings.Count(got, "stopped") != 1 {
		t.Errorf("the stop note must appear once: %q", got)
	}
}

// The session log lives under the live user's runtime directory, where the report looks.
func TestSessionLogPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got := sessionLogPath(); got != "/run/user/1000/river-live/session.log" {
		t.Errorf("sessionLogPath() = %q", got)
	}
	if _, err := filepath.Match(liveLogPaths[1], "/run/user/1000/river-live/session.log"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := filepath.Match(liveLogPaths[1], "/run/user/1000/river-live/session.log"); !ok {
		t.Errorf("the report's pattern %q does not match the session log", liveLogPaths[1])
	}
}

// The report names every live log and, in the full view, carries the last lines of each.
func TestReportLiveLogs(t *testing.T) {
	root := t.TempDir()
	write := func(p, s string) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("run/user/1000/river-live/session.log", "river-live-session: starting the live desktop\nDEsk: no render node\n")
	write("run/river-installer/installer.log", "12:00:00  install UI on http://[::1]:47660/\n")
	r := rpt{root: root}
	full := strings.Join(r.liveLogs(true), "\n")
	for _, want := range []string{
		"/run/user/1000/river-live/session.log",
		"| DEsk: no render node",
		"/run/river-installer/installer.log",
		"/var/log/runink-install.log  (not written)",
	} {
		if !strings.Contains(full, want) {
			t.Errorf("full live logs lack %q:\n%s", want, full)
		}
	}
	summary := strings.Join(r.liveLogs(false), "\n")
	if strings.Contains(summary, "no render node") || !strings.Contains(summary, "session.log") {
		t.Errorf("the summary names the logs without their lines:\n%s", summary)
	}
	if !strings.Contains(hardwareReport(root, nil, true), "live logs") {
		t.Error("the full report has no live logs section")
	}
}
