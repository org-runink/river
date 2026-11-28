// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package repocmd

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The Arch package source (docs/ARCH-BASE.md): one dated archive snapshot, written once in
// pacman/mirrorlist.pin and repeated as the Arch builder image's build-arg default, which
// cannot read that file. The two must agree, and the base image must be pinned by digest.
func TestArchPin(t *testing.T) {
	r := root(t)
	pin, err := os.ReadFile(filepath.Join(r, "pacman/mirrorlist.pin"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^ARCH_PIN_DATE='([0-9]{4}-[0-9]{2}-[0-9]{2})'$`).FindSubmatch(pin)
	if m == nil {
		t.Fatal("pacman/mirrorlist.pin: no ARCH_PIN_DATE='YYYY-MM-DD' line")
	}
	date := string(m[1])
	if _, err := time.Parse("2006-01-02", date); err != nil {
		t.Fatalf("ARCH_PIN_DATE %q is not a date: %v", date, err)
	}
	want := "ARCH_ARCHIVE='Server = https://archive.archlinux.org/repos/" +
		strings.ReplaceAll(date, "-", "/") + "/$repo/os/$arch'"
	if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(want) + `$`).Match(pin) {
		t.Fatalf("pacman/mirrorlist.pin: want the line\n  %s", want)
	}

	cf, err := os.ReadFile(filepath.Join(r, "builder/arch.Containerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^ARG ARCH_PIN_DATE=` + regexp.QuoteMeta(date) + `$`).Match(cf) {
		t.Errorf("builder/arch.Containerfile: ARG ARCH_PIN_DATE must default to %s (pacman/mirrorlist.pin)", date)
	}
	from := regexp.MustCompile(`(?m)^FROM\s+(\S+)`).FindAllSubmatch(cf, -1)
	if len(from) != 1 || !regexp.MustCompile(`^docker\.io/library/archlinux@sha256:[0-9a-f]{64}$`).Match(from[0][1]) {
		t.Errorf("builder/arch.Containerfile: want exactly one FROM docker.io/library/archlinux@sha256:<digest>, got %q", from)
	}
	if regexp.MustCompile(`(?i)artix`).Match(stripComments(cf)) {
		t.Error("builder/arch.Containerfile: an instruction names Artix")
	}
}

// The Arch build-time pacman.conf: [runink] first (our rebuilds of Arch packages keep their
// names and must win), then [core] and [extra] from the pinned archive, nothing from Artix, no
// testing or multilib repository, and the [blackarch] spot last.
func TestArchPacmanConf(t *testing.T) {
	r := root(t)
	b, err := os.ReadFile(filepath.Join(r, "pacman/pacman.conf.in"))
	if err != nil {
		t.Fatal(err)
	}
	var sections []string
	include := map[string]string{}
	cur := ""
	for _, l := range strings.Split(string(stripComments(b)), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			cur = strings.Trim(l, "[]")
			sections = append(sections, cur)
			continue
		}
		if v, ok := strings.CutPrefix(l, "Include = "); ok {
			include[cur] = v
		}
	}
	if want := []string{"options", "runink", "core", "extra"}; !slices.Equal(sections, want) &&
		!slices.Equal(sections, append(want, "blackarch")) {
		t.Fatalf("pacman/pacman.conf.in sections %v, want %v (optionally then blackarch)", sections, want)
	}
	for _, s := range []string{"core", "extra"} {
		if include[s] != "/etc/pacman.d/mirrorlist-arch" {
			t.Errorf("[%s] must Include = /etc/pacman.d/mirrorlist-arch, got %q", s, include[s])
		}
	}
	if regexp.MustCompile(`(?i)artix`).Match(stripComments(b)) {
		t.Error("pacman/pacman.conf.in: a setting names Artix")
	}
	marker := strings.Index(string(b), "==== [blackarch] goes here")
	if marker < 0 || marker < strings.Index(string(b), "[extra]") {
		t.Error("pacman/pacman.conf.in: the [blackarch] spot must be marked, after [extra]")
	}

	// The Artix configuration stays only for `make iso` until the Arch-based ISO boots.
	art, err := os.ReadFile(filepath.Join(r, "scripts/build-iso.sh"))
	if err != nil || !strings.Contains(string(art), "pacman/artix/pacman.conf.in") {
		t.Errorf("scripts/build-iso.sh must render pacman/artix/pacman.conf.in (err %v)", err)
	}
	if _, err := os.Stat(filepath.Join(r, "pacman/artix/pacman.conf.in")); err != nil {
		t.Error(err)
	}
}

// stripComments drops every line whose first non-blank character is '#'.
func stripComments(b []byte) []byte {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			out = append(out, l)
		}
	}
	return []byte(strings.Join(out, "\n"))
}
