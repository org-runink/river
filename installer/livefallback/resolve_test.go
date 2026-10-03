// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The live medium reached a bare login prompt on a machine that had both installers sitting in
// /usr/local/bin. rc.local starts this program by absolute path, in the init context, where
// $PATH does not include /usr/local/bin -- so exec.LookPath failed for river-kiosk AND
// runink-install, every fallback "errored", and the operator was offered nothing at all.
// resolve must therefore find a tool with NO usable $PATH.
func TestResolveFindsToolsWithoutPATH(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "river-kiosk")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := binDirs
	binDirs = []string{dir}
	defer func() { binDirs = old }()

	t.Setenv("PATH", "")
	if got := resolve("river-kiosk"); got != bin {
		t.Errorf("resolve with an empty PATH = %q, want %q", got, bin)
	}
	if got := resolve("runink-install"); got != "" {
		t.Errorf("a genuinely missing tool resolved to %q, want \"\"", got)
	}
}

// A file that is present but not executable is not a fallback: treating it as one would make
// the program report success and then fail at exec, which is the confusing half-state this
// whole change exists to remove.
func TestResolveIgnoresNonExecutable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "river-kiosk"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "runink-install"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := binDirs
	binDirs = []string{dir}
	defer func() { binDirs = old }()
	t.Setenv("PATH", "")

	if got := resolve("river-kiosk"); got != "" {
		t.Errorf("a non-executable file resolved to %q, want \"\"", got)
	}
	if got := resolve("runink-install"); got != "" {
		t.Errorf("a directory resolved to %q, want \"\"", got)
	}
}

// The explicit directories must win over $PATH: on the live medium $PATH may point at a
// different, older copy, and the image's own /usr/local/bin is the one that was installed.
func TestResolvePrefersBinDirsOverPATH(t *testing.T) {
	want, other := t.TempDir(), t.TempDir()
	for _, d := range []string{want, other} {
		if err := os.WriteFile(filepath.Join(d, "river-kiosk"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := binDirs
	binDirs = []string{want}
	defer func() { binDirs = old }()
	t.Setenv("PATH", other)

	if got := resolve("river-kiosk"); got != filepath.Join(want, "river-kiosk") {
		t.Errorf("resolve = %q, want the binDirs copy %q", got, filepath.Join(want, "river-kiosk"))
	}
}
