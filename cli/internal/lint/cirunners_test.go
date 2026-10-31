// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const ciGood = `name: CI
on:
  pull_request:
  push:
    branches: [main]
jobs:
  test:
    if: ` + ownerOnlyIf + `
    runs-on: [self-hosted, public-ci]
    steps:
      - if: github.event_name == 'push'
        run: echo ok
  deploy:
    if: (github.ref == 'refs/heads/main') && (` + ownerOnlyIf + `)
    runs-on: [ self-hosted,public-ci ]   # spacing does not matter
    steps:
      - run: echo ok
  gate:
    if: ` + ownerOnlyIf + `
    uses: ./.github/workflows/reusable.yml
`

func ciRun(t *testing.T, files map[string]string) (string, error) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	for name, body := range files {
		lbWrite(t, dir, name, body)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	gitAdd(t, dir)
	var errb, outb bytes.Buffer
	err := CIRunners(context.Background(), dir, &errb, &outb)
	return errb.String() + outb.String(), err
}

func TestCIRunnersClean(t *testing.T) {
	out, err := ciRun(t, map[string]string{".github/workflows/ci.yml": ciGood})
	if err != nil || !strings.Contains(out, "lint-ci-runners: OK (1 workflows, 3 jobs)") {
		t.Fatalf("clean workflow failed: %v\n%s", err, out)
	}
}

func TestCIRunnersFlagsEachViolation(t *testing.T) {
	cases := map[string]struct{ from, to, want string }{
		"github-hosted":  {"runs-on: [self-hosted, public-ci]\n    steps:\n      - if:", "runs-on: ubuntu-latest\n    steps:\n      - if:", `runs on "ubuntu-latest"`},
		"hosted-list":    {"runs-on: [self-hosted, public-ci]\n    steps:\n      - if:", "runs-on: [ubuntu-24.04]\n    steps:\n      - if:", `runs on "[ubuntu-24.04]"`},
		"matrix":         {"runs-on: [self-hosted, public-ci]\n    steps:\n      - if:", "runs-on: ${{ matrix.os }}\n    steps:\n      - if:", `runs on "${{ matrix.os }}"`},
		"other-pool":     {"runs-on: [self-hosted, public-ci]\n    steps:\n      - if:", "runs-on: [self-hosted, public-ci, gpu]\n    steps:\n      - if:", `runs on "[self-hosted, public-ci, gpu]"`},
		"no-runs-on":     {"    runs-on: [self-hosted, public-ci]\n    steps:\n      - if:", "    steps:\n      - if:", "job test has no runs-on"},
		"no-if":          {"  test:\n    if: " + ownerOnlyIf + "\n", "  test:\n", "job test lacks the owner-only condition"},
		"other-if":       {"  test:\n    if: " + ownerOnlyIf + "\n", "  test:\n    if: github.event_name == 'push'\n", "job test lacks the owner-only condition"},
		"ored-not-anded": {"(github.ref == 'refs/heads/main') && (", "(github.ref == 'refs/heads/main') || (", "job deploy lacks the owner-only condition"},
		"reusable-no-if": {"  gate:\n    if: " + ownerOnlyIf + "\n", "  gate:\n", "job gate lacks the owner-only condition"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(ciGood, c.from) {
				t.Fatalf("fixture does not contain %q", c.from)
			}
			out, err := ciRun(t, map[string]string{".github/workflows/ci.yml": strings.Replace(ciGood, c.from, c.to, 1)})
			if err == nil || !strings.Contains(out, c.want) {
				t.Fatalf("violation passed or was misreported (want %q): %v\n%s", c.want, err, out)
			}
		})
	}
}

// A tree with no workflow is not a pass: the lint would have examined nothing.
func TestCIRunnersFailsWithNoWorkflow(t *testing.T) {
	if _, err := ciRun(t, map[string]string{"README.md": "hi\n"}); err == nil {
		t.Fatal("a tree without workflows passed")
	}
}

// This repository's own workflows pass.
func TestCIRunnersThisRepository(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Skipf("not a git checkout at %s", root)
	}
	var errb, outb bytes.Buffer
	if err := CIRunners(context.Background(), root, &errb, &outb); err != nil {
		t.Fatalf("%v\n%s", err, errb.String())
	}
}
