// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"testing"
)

// Vectors from pacman's test/util/vercmptest.sh (libalpm's own expectations).
func TestVercmpMatchesLibalpm(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.5.0", "1.5.0", 0},
		{"1.5.1", "1.5.0", 1},
		{"1.5.1", "1.5", 1},
		{"1.5.0-1", "1.5.0-2", -1},
		{"1.5.0-1", "1.5.1-1", -1},
		{"1.5.0-2", "1.5.1-1", -1},
		{"1.1-1", "1.1", 0}, // a release is compared only when both sides have one
		{"1.0a", "1.0", -1}, // an alphabetic remainder is older
		{"1.0alpha", "1.0", -1},
		{"1.0rc1", "1.0", -1},
		{"1.0", "1.0.1", -1},
		{"1.001", "1.1", 0}, // leading zeros do not count
		{"1.0", "1_0", 0},   // separators of equal length are equal
		{"1:1.0", "2.0", 1}, // the epoch wins
		{"2.0", "1:1.0", -1},
		{"2:9.0.2-1", "2:9.0.2-1", 0},
		{"3.6.4-1", "3.6.3-2", 1},
		{"1.2.3", "1.2.3a", 1},
		{"1.2a", "1.2b", -1},
	} {
		if got := sign(vercmp(c.a, c.b)); got != c.want {
			t.Errorf("vercmp(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := sign(vercmp(c.b, c.a)); got != -c.want {
			t.Errorf("vercmp(%q, %q) = %d, want %d (antisymmetry)", c.b, c.a, got, -c.want)
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

func TestAuditBlocksOnlyFixableSevereAndHonoursWaivers(t *testing.T) {
	s := func(v string) *string { return &v }
	pkgs := map[string]string{"openssl": "3.6.3-1", "curl": "8.9.0-1", "vim": "9.1.0-1", "zlib": "1.3-1"}
	groups := []avg{
		{Name: "AVG-1", Packages: []string{"openssl"}, Status: "Fixed", Severity: "High", Fixed: s("3.6.4-1"), Issues: []string{"CVE-1"}},
		{Name: "AVG-2", Packages: []string{"curl"}, Status: "Vulnerable", Severity: "Critical", Issues: []string{"CVE-2"}},               // no fix: report
		{Name: "AVG-3", Packages: []string{"vim"}, Status: "Fixed", Severity: "Medium", Fixed: s("9.1.1-1"), Issues: []string{"CVE-3"}},  // medium: report
		{Name: "AVG-4", Packages: []string{"zlib"}, Status: "Fixed", Severity: "Critical", Fixed: s("1.2-1"), Issues: []string{"CVE-4"}}, // already past fix
		{Name: "AVG-5", Packages: []string{"zlib"}, Status: "Not affected", Severity: "High", Fixed: s("9-1")},
	}
	fs := audit(pkgs, groups, nil)
	got := map[string]bool{}
	for _, f := range fs {
		got[f.Group] = f.Blocking
	}
	if len(fs) != 3 || !got["AVG-1"] || got["AVG-2"] || got["AVG-3"] {
		t.Fatalf("findings %+v", fs)
	}
	if _, ok := got["AVG-4"]; ok {
		t.Error("a package at or past the fix is not a finding")
	}
	fs = audit(pkgs, groups, map[string]waiver{"CVE-1": {"CVE-1", "2099-01-01", "owner"}})
	for _, f := range fs {
		if f.Group == "AVG-1" && (f.Blocking || f.WaivedBy == "") {
			t.Errorf("a live waiver on one of the group's issues must unblock it: %+v", f)
		}
	}
}

func TestExpiredWaiverIsReported(t *testing.T) {
	path := t.TempDir() + "/w.txt"
	if err := writeFile(path, "# comment\nCVE-9 2020-01-01 someone reason text\nCVE-8 2099-01-01 other reason\n"); err != nil {
		t.Fatal(err)
	}
	ws, expired, err := readWaivers(path, "2026-09-27")
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || len(ws) != 1 {
		t.Fatalf("live %v, expired %v", ws, expired)
	}
}

func writeFile(path, s string) error { return os.WriteFile(path, []byte(s), 0o644) }

func TestMtreeUnescape(t *testing.T) {
	for in, want := range map[string]string{
		`usr/share/a\040b.txt`: "usr/share/a b.txt",
		`plain/name`:           "plain/name",
		`trailing\04`:          `trailing\04`, // too short to be an escape: kept
		`two\040\041`:          "two !",
	} {
		if got := mtreeUnescape(in); got != want {
			t.Errorf("mtreeUnescape(%q) = %q, want %q", in, got, want)
		}
	}
}
