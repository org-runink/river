// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureRepo builds a git repository that passes the lint: 120 filler files plus every
// allow-listed file carrying its reason.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 120; i++ {
		write(fmt.Sprintf("filler/f%03d.txt", i), "mistral.rs\n")
	}
	write("tests/assert-golden.sh", "test ! -e /usr/local/bin/llama-server\n")
	write("validation/results.json", `{"engine":"llama.cpp","not_evidence":true}`+"\n")
	write("validation/SELECTION.md", "llama.cpp run: NOT EVIDENCE\n")
	write("models.evidence.json", `{"quantizer":"llama.cpp","not_evidence":true}`+"\n")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func gitAdd(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
}

func TestOneEngineClean(t *testing.T) {
	dir := fixtureRepo(t)
	var errb, outb bytes.Buffer
	if err := OneEngine(context.Background(), dir, &errb, &outb); err != nil {
		t.Fatalf("clean repo failed: %v\n%s", err, errb.String())
	}
	if !strings.Contains(outb.String(), "OK (124 tracked files") {
		t.Fatalf("unexpected OK line: %q", outb.String())
	}
}

func TestOneEngineFlagsAStrayMention(t *testing.T) {
	dir := fixtureRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "docs.md"), []byte("we could use LLAMA_CPP here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAdd(t, dir)
	var errb bytes.Buffer
	if err := OneEngine(context.Background(), dir, &errb, &bytes.Buffer{}); err == nil {
		t.Fatal("a stray llama.cpp mention passed")
	}
	if !strings.Contains(errb.String(), "docs.md names llama.cpp") || !strings.Contains(errb.String(), "1:we could use LLAMA_CPP here") {
		t.Fatalf("report does not name the file and line:\n%s", errb.String())
	}
}

func TestOneEngineReasonGone(t *testing.T) {
	dir := fixtureRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "validation/SELECTION.md"), []byte("llama.cpp run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitAdd(t, dir)
	var errb bytes.Buffer
	if err := OneEngine(context.Background(), dir, &errb, &bytes.Buffer{}); err == nil {
		t.Fatal("an allow-list entry whose reason is gone passed")
	}
	if !strings.Contains(errb.String(), "labels nothing NOT EVIDENCE") {
		t.Fatalf("missing reason message:\n%s", errb.String())
	}
}

func TestOneEngineRefusesATinyTree(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := OneEngine(context.Background(), dir, &bytes.Buffer{}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "not looking at this repository") {
		t.Fatalf("empty repo must fail as the wrong tree, got %v", err)
	}
}

func TestOneEngineAllowListIsDocumented(t *testing.T) {
	for _, f := range oneEngineAllowed() {
		if oneEngineAllow[f] == "" {
			t.Errorf("%s is allowed without a reason", f)
		}
	}
}
