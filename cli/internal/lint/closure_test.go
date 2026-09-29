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

	"github.com/org-runink/river/cli/internal/rootcmd"
)

// closureFake stands in for pacman: tree maps a package to its resolved closure; a package
// missing from it resolves to nothing, as one in no configured repo does.
func closureFake(t *testing.T, have bool, tree map[string][]string) {
	t.Helper()
	savedP, savedH := closurePactree, closureHaveTools
	t.Cleanup(func() { closurePactree, closureHaveTools = savedP, savedH })
	closureHaveTools = func() bool { return have }
	closurePactree = func(_ context.Context, sync bool, pkg string) []string {
		if !sync {
			return nil
		}
		return tree[pkg]
	}
}

func closureFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	lbWrite(t, dir, "p/Packages-Root", "# allow-list\nbase linux-runink\nlinux-firmware-intel\nruninkx\n")
	lbWrite(t, dir, "p/forbidden.closure", "# never in the closure\nmesa\nxorg-.*\n")
	lbWrite(t, dir, "p/forbidden.explicit", "sudo\n")
	return filepath.Join(dir, "p/Packages-Root")
}

var closureTree = map[string][]string{
	"base":                 {"base", "glibc", "filesystem"},
	"linux-runink":         {"linux-runink", "kmod"},
	"linux-firmware-intel": {"linux-firmware-intel"},
}

func closureRun(t *testing.T, manifest, mode string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := Closure(context.Background(), manifest, mode, &errb, &outb)
	return errb.String() + outb.String(), err
}

func TestClosureClean(t *testing.T) {
	closureFake(t, true, closureTree)
	out, err := closureRun(t, closureFixture(t), "closure")
	if err != nil {
		t.Fatalf("clean manifest failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"NOTE: 1/4 package(s) not in any configured repo",
		"       runinkx\n",
		"lint-closure: OK [closure — resolved 6 package(s) from 3/4 manifest entries]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in:\n%s", want, out)
		}
	}
}

func TestClosureManifestOnly(t *testing.T) {
	closureFake(t, false, nil)
	out, err := closureRun(t, closureFixture(t), "manifest-only")
	if err != nil || !strings.Contains(out, "lint-closure: OK [MANIFEST-ONLY — grepped 4 explicit package name(s); transitive dependencies were NOT examined]") {
		t.Fatalf("manifest-only: %v\n%s", err, out)
	}
}

func TestClosureFailures(t *testing.T) {
	cases := []struct {
		name, mode, want string
		tree             map[string][]string
		have             bool
		mutate           func(t *testing.T, manifest string)
	}{
		{name: "no pactree", mode: "closure", want: "lint-closure: REFUSING TO RUN — pacman/pactree not found."},
		{name: "unknown mode", mode: "grep", have: true, want: "unknown LINT_CLOSURE_MODE='grep'"},
		{name: "forbidden in closure", mode: "closure", have: true, want: "  FORBIDDEN in closure: xorg-.*",
			tree: map[string][]string{"base": {"base", "xorg-server"}, "linux-runink": {"linux-runink"}}},
		{name: "mainline kernel in closure", mode: "closure", have: true, want: "FORBIDDEN in closure: linux (mainline kernel — keep linux-runink only)",
			tree: map[string][]string{"base": {"base", "linux"}, "linux-runink": {"linux-runink"}}},
		{name: "firmware meta in closure", mode: "closure", have: true, want: "linux-firmware (meta — use linux-firmware-intel)",
			tree: map[string][]string{"base": {"base", "linux-firmware"}, "linux-runink": {"linux-runink"}}},
		{name: "nothing resolved", mode: "closure", have: true, tree: map[string][]string{},
			want: "lint-closure: FAIL — pactree resolved 0 packages from 4 manifest entries."},
		{name: "hardening", mode: "manifest-only", want: "FORBIDDEN (hardening — do not re-add): sudo", mutate: func(t *testing.T, m string) {
			lbWrite(t, filepath.Dir(m), "Packages-Root", "base linux-runink sudo\n")
		}},
		{name: "forbidden explicit, manifest-only", mode: "manifest-only", want: "lint-closure: FAIL [MANIFEST-ONLY", mutate: func(t *testing.T, m string) {
			lbWrite(t, filepath.Dir(m), "Packages-Root", "base linux-runink mesa\n")
		}},
		{name: "no kernel pinned", mode: "manifest-only", want: "linux-runink not explicitly listed", mutate: func(t *testing.T, m string) {
			lbWrite(t, filepath.Dir(m), "Packages-Root", "base\n")
		}},
		{name: "two kernels", mode: "manifest-only", want: "linux-lts (superseded by linux-runink", mutate: func(t *testing.T, m string) {
			lbWrite(t, filepath.Dir(m), "Packages-Root", "base linux-runink linux-lts\n")
		}},
		{name: "mainline listed", mode: "manifest-only", want: "linux (mainline kernel listed explicitly)", mutate: func(t *testing.T, m string) {
			lbWrite(t, filepath.Dir(m), "Packages-Root", "base linux-runink linux\n")
		}},
		{name: "firmware meta listed", mode: "manifest-only", want: "linux-firmware (use linux-firmware-intel)", mutate: func(t *testing.T, m string) {
			lbWrite(t, filepath.Dir(m), "Packages-Root", "base linux-runink linux-firmware\n")
		}},
		{name: "missing policy", mode: "manifest-only", want: "lint-closure: missing policy file: ", mutate: func(t *testing.T, m string) {
			os.Remove(filepath.Join(filepath.Dir(m), "forbidden.explicit"))
		}},
		{name: "empty policy", mode: "manifest-only", want: "forbidden.closure has no entries — this would check nothing", mutate: func(t *testing.T, m string) {
			lbWrite(t, filepath.Dir(m), "forbidden.closure", "# nothing\n\n")
		}},
		{name: "empty manifest", mode: "manifest-only", want: "lint-closure: manifest is empty", mutate: func(t *testing.T, m string) {
			lbWrite(t, filepath.Dir(m), "Packages-Root", "# nothing\n")
		}},
		{name: "no manifest", mode: "manifest-only", want: "lint-closure: no such file: ", mutate: func(t *testing.T, m string) {
			os.Remove(m)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			closureFake(t, c.have, c.tree)
			m := closureFixture(t)
			if c.mutate != nil {
				c.mutate(t, m)
			}
			out, err := closureRun(t, m, c.mode)
			if err == nil {
				t.Fatalf("passed:\n%s", out)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("want %q in:\n%s", c.want, out)
			}
		})
	}
}

// The script read LINT_CLOSURE_MODE; the command still does.
func TestClosureReadsLINT_CLOSURE_MODE(t *testing.T) {
	closureFake(t, false, nil)
	t.Setenv("LINT_CLOSURE_MODE", "manifest-only")
	root := rootcmd.New()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"lint", "closure", closureFixture(t)})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), "MANIFEST-ONLY mode") {
		t.Fatalf("LINT_CLOSURE_MODE was not read: %v\n%s", err, out.String())
	}
}
