// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ratchetRepo(t *testing.T, list string, files map[string]string) string {
	t.Helper()
	dir := fixtureRepo(t)
	files["scripts/shell-ratchet.txt"] = list
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitAdd(t, dir)
	return dir
}

func runRatchet(t *testing.T, dir string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := ShellRatchet(context.Background(), dir, &errb, &outb)
	return errb.String() + outb.String(), err
}

// The fixture's tests/assert-golden.sh is shell, so every list below names it.
const golden = "tests/assert-golden.sh\n"

func TestRatchetListedShellPasses(t *testing.T) {
	dir := ratchetRepo(t, "# header\n"+golden+"build/old.sh\n", map[string]string{
		"build/old.sh":                "#!/bin/sh\necho old\n",
		"build/pkgbuilds/x/PKGBUILD":  "pkgname=x\nbuild() { make; }\n",
		"build/pkgbuilds/x/x.install": "post_install() { :; }\n",
		"profile/s6/svc/run":          "#!/bin/execlineb -P\nexec river service run svc\n",
		"tools/notshell.txt":          "echo not a script\n",
		"tools/withshebang-but-perl":  "#!/usr/bin/env perl\nprint 1\n",
	})
	out, err := runRatchet(t, dir)
	if err != nil {
		t.Fatalf("clean tree failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "OK (2 shell scripts left to port)") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRatchetRejectsNewShell(t *testing.T) {
	dir := ratchetRepo(t, golden, map[string]string{
		"scripts/new-thing":  "#!/usr/bin/env bash\nset -eu\n",
		"profile/s6/svc/run": "#!/bin/sh\nmkdir -p /run/x\nchown a /run/x\nexec river service run svc\n",
	})
	out, err := runRatchet(t, dir)
	if err == nil {
		t.Fatal("new shell passed")
	}
	for _, want := range []string{"scripts/new-thing is a new shell script", "profile/s6/svc/run is a new shell script"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRatchetRejectsStaleLine(t *testing.T) {
	dir := ratchetRepo(t, golden+"build/ported.sh\n", map[string]string{})
	out, err := runRatchet(t, dir)
	if err == nil || !strings.Contains(out, "build/ported.sh is listed") {
		t.Fatalf("a ported script left in the list passed: %v\n%s", err, out)
	}
}

func TestRatchetRejectsDuplicateLine(t *testing.T) {
	dir := ratchetRepo(t, golden+golden, map[string]string{})
	if _, err := runRatchet(t, dir); err == nil || !strings.Contains(err.Error(), "listed twice") {
		t.Fatalf("duplicate line passed: %v", err)
	}
}
