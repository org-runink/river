// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeTree writes files (path → body) under dir, creating directories; a body starting
// with "#!" is made executable.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(body, "#!") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
}

func shellFixture(t *testing.T) string {
	t.Helper()
	dir := fixtureRepo(t)
	writeTree(t, dir, map[string]string{
		"scripts/ok.sh":                     "#!/bin/sh\nset -eu\necho \"$1\"\n",
		"tests/runner":                      "#!/bin/bash\nset -eu\necho ok\n",
		"installer/notes.txt":               "not a script\n",
		"build/.out/generated.sh":           "#!/bin/sh\necho $UNSET_AND_UNQUOTED\n",
		"build/pkgbuilds/k/src/upstream.sh": "#!/bin/sh\ncd /nowhere\n",

		"iso-profiles/river/root-overlay/usr/local/bin/tool": "#!/bin/sh\nset -eu\necho tool\n",
		"iso-profiles/river/live-overlay/usr/local/bin/live": "#!/usr/bin/env bash\necho live\n",
	})
	// fixtureRepo's tests/assert-golden.sh has no shebang; give it one, as the real one has.
	golden := filepath.Join(dir, "tests/assert-golden.sh")
	b, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(golden, append([]byte("#!/bin/sh\n"), b...), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ok.sh", filepath.Join(dir, "scripts/link.sh")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestShellFindsTheScripts(t *testing.T) {
	dir := shellFixture(t)
	got, err := shellScripts(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"iso-profiles/river/live-overlay/usr/local/bin/live",
		"iso-profiles/river/root-overlay/usr/local/bin/tool",
		"scripts/ok.sh",
		"tests/assert-golden.sh", // from fixtureRepo
		"tests/runner",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("found %q\nwant %q", got, want)
	}
}

func needShellcheck(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not installed")
	}
}

func TestShellClean(t *testing.T) {
	needShellcheck(t)
	dir := shellFixture(t)
	var errb, outb bytes.Buffer
	if err := Shell(context.Background(), dir, ShellOptions{Strict: true}, &errb, &outb); err != nil {
		t.Fatalf("clean tree failed: %v\n%s%s", err, outb.String(), errb.String())
	}
	if !strings.Contains(outb.String(), "lint-shell: checking 5 scripts") || !strings.HasSuffix(outb.String(), "lint-shell: OK\n") {
		t.Fatalf("unexpected output: %q", outb.String())
	}
}

func TestShellReportsAFinding(t *testing.T) {
	needShellcheck(t)
	dir := shellFixture(t)
	writeTree(t, dir, map[string]string{"bench/bad.sh": "#!/bin/sh\nunused=1\n"})
	var errb, outb bytes.Buffer
	err := Shell(context.Background(), dir, ShellOptions{Severity: "warning"}, &errb, &outb)
	if err == nil {
		t.Fatal("a shellcheck warning passed")
	}
	if !strings.Contains(outb.String(), "SC2034") || !strings.Contains(errb.String(), "lint-shell: shellcheck reported issues") {
		t.Fatalf("finding not reported:\nout: %s\nerr: %s", outb.String(), errb.String())
	}
}

func TestShellMissingShellcheck(t *testing.T) {
	dir := shellFixture(t)
	t.Setenv("PATH", t.TempDir())
	var outb bytes.Buffer
	if err := Shell(context.Background(), dir, ShellOptions{}, &bytes.Buffer{}, &outb); err != nil {
		t.Fatalf("without --strict a missing shellcheck skips, got %v", err)
	}
	if !strings.Contains(outb.String(), "shellcheck not installed — skipping") {
		t.Fatalf("no skip note: %q", outb.String())
	}
	if err := Shell(context.Background(), dir, ShellOptions{Strict: true}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("--strict passed without shellcheck")
	}
}

func TestShellNothingFound(t *testing.T) {
	needShellcheck(t)
	dir := t.TempDir()
	var outb bytes.Buffer
	if err := Shell(context.Background(), dir, ShellOptions{}, &bytes.Buffer{}, &outb); err != nil || !strings.Contains(outb.String(), "no shell scripts found") {
		t.Fatalf("an empty tree skips without --strict: %v %q", err, outb.String())
	}
	if err := Shell(context.Background(), dir, ShellOptions{Strict: true}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "search paths are stale") {
		t.Fatalf("--strict must fail on an empty search, got %v", err)
	}
}
