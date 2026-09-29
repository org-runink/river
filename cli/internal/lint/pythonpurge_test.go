// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	pyContainerfile = `FROM artix
RUN pacman -Syu --noconfirm base-devel git \
      go zstd  # the toolchain
RUN py="$(pacman -Qq python 2>/dev/null)"; [ -z "$py" ] || pacman -Rdd --noconfirm $py; \
    ! command -v python3 && ! command -v python
`
	pyKernelPKGBUILD = `pkgname=linux-runink
makedepends=(bc cpio go  # bpfdoc replaces the interpreter
  xz)
_install_bpfdoc() {
	install -m755 "$srcdir/bpfdoc" scripts/bpf_doc.py
}
prepare() {
	cd linux
	_install_bpfdoc
}
`
	pyWorkflow = `jobs:
  build:
    steps:
      - run: |
          pacman -Syu --noconfirm base-devel git
          if command -v python3 || command -v python; then pacman -Rdd --noconfirm python; exit 1; fi
          makepkg -s
`
)

// pyFixture is a git repository that passes: a dozen shell scripts, the two build
// Containerfiles, five PKGBUILDs (the two kernel ones wiring bpfdoc), two makepkg workflows
// and a CI path that runs build-iso-box.sh with go installed.
func pyFixture(t *testing.T) string {
	t.Helper()
	dir := fixtureRepo(t)
	for i := 0; i < 12; i++ {
		lbWrite(t, dir, fmt.Sprintf("scripts/s%02d.sh", i), "#!/bin/sh\n# python was removed from here\necho ok\n")
	}
	lbWrite(t, dir, "scripts/build-iso-box.sh", "#!/bin/sh\ngo build ./scripts/patch-artools.go\n")
	lbWrite(t, dir, "builder/Containerfile", pyContainerfile)
	lbWrite(t, dir, "build/kernel-builder.Containerfile", pyContainerfile)
	lbWrite(t, dir, "build/pkgbuilds/runink-kernel/PKGBUILD", pyKernelPKGBUILD)
	lbWrite(t, dir, "packaging/aur/linux-runink/PKGBUILD", pyKernelPKGBUILD)
	for _, p := range []string{"a", "b", "c"} {
		lbWrite(t, dir, "build/pkgbuilds/"+p+"/PKGBUILD", "depends=(glibc)\n")
	}
	lbWrite(t, dir, ".github/workflows/kernel-build.yml", pyWorkflow)
	lbWrite(t, dir, ".github/workflows/zfs-build.yml", pyWorkflow)
	lbWrite(t, dir, ".github/workflows/iso.yml", "steps:\n  - run: |\n      pacman -S --noconfirm \\\n        go git\n      sh scripts/build-iso-box.sh\n")
	lbWrite(t, dir, "cli/vendor/x/y/helper.sh", "python3 -c 'print(1)'\n")
	gitAdd(t, dir)
	return dir
}

func pyRun(t *testing.T, dir string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := PythonPurge(context.Background(), dir, &errb, &outb)
	return errb.String() + outb.String(), err
}

func TestPythonPurgeClean(t *testing.T) {
	out, err := pyRun(t, pyFixture(t))
	if err != nil {
		t.Fatalf("clean tree failed: %v\n%s", err, out)
	}
	// 12 + build-iso-box.sh + one-engine's assert-golden.sh; cli/vendor is skipped.
	if !strings.Contains(out, "lint-python-purge: OK (14 shell scripts scanned for inline python)") {
		t.Fatalf("unexpected OK line: %q", out)
	}
}

func TestPythonPurgeFailures(t *testing.T) {
	cases := []struct {
		name, want string
		mutate     func(t *testing.T, dir string)
	}{
		{"1 tracked .py", "tracked python file(s) found", func(t *testing.T, d string) {
			lbWrite(t, d, "tools/x.py", "print(1)\n")
		}},
		{"2 build-iso-box names python", "scripts/build-iso-box.sh invokes python", func(t *testing.T, d string) {
			lbWrite(t, d, "scripts/build-iso-box.sh", "#!/bin/sh\n# python gone\n")
		}},
		{"3 CI path without go", "./.github/workflows/iso.yml provisions packages and runs build-iso-box.sh, but does not install 'go'", func(t *testing.T, d string) {
			lbWrite(t, d, ".github/workflows/iso.yml", "steps:\n  - run: |\n      pacman -S --noconfirm git\n      sh scripts/build-iso-box.sh\n")
		}},
		{"3b builder image without go", "builder/Containerfile does not install 'go'", func(t *testing.T, d string) {
			lbWrite(t, d, "builder/Containerfile", strings.ReplaceAll(pyContainerfile, " go ", " "))
		}},
		{"4 inline python", "scripts/s03.sh invokes python (comment lines excluded):\n    2:python3 -c 'import os'", func(t *testing.T, d string) {
			lbWrite(t, d, "scripts/s03.sh", "#!/bin/sh\npython3 -c 'import os'\n")
		}},
		{"5a PKGBUILD depends on python", "build/pkgbuilds/a/PKGBUILD declares a python dependency:\n    depends=(glibc python-yaml)", func(t *testing.T, d string) {
			lbWrite(t, d, "build/pkgbuilds/a/PKGBUILD", "depends=(glibc python-yaml)\n")
		}},
		{"5b Containerfile installs python", "build/kernel-builder.Containerfile installs python", func(t *testing.T, d string) {
			lbWrite(t, d, "build/kernel-builder.Containerfile", strings.Replace(pyContainerfile, "go zstd", "go zstd python3", 1))
		}},
		{"5b Containerfile keeps the interpreter", "builder/Containerfile no longer removes the interpreter", func(t *testing.T, d string) {
			lbWrite(t, d, "builder/Containerfile", strings.Replace(pyContainerfile, "pacman -Rdd --noconfirm $py", "true", 1))
		}},
		{"5b Containerfile never fails", "builder/Containerfile no longer fails its build while an interpreter is on PATH", func(t *testing.T, d string) {
			lbWrite(t, d, "builder/Containerfile", strings.Replace(pyContainerfile, "! command -v python3 && ! command -v python", "true", 1))
		}},
		{"5b kernel builder without go", "build/kernel-builder.Containerfile does not install 'go'", func(t *testing.T, d string) {
			lbWrite(t, d, "build/kernel-builder.Containerfile", strings.Replace(pyContainerfile, "go zstd", "zstd", 1))
		}},
		{"5c workflow installs python", ".github/workflows/zfs-build.yml installs python for a package build", func(t *testing.T, d string) {
			lbWrite(t, d, ".github/workflows/zfs-build.yml", strings.Replace(pyWorkflow, "base-devel git", "base-devel git python", 1))
		}},
		{"5c workflow keeps base-devel's python", ".github/workflows/zfs-build.yml installs base-devel (which pulls in python) without removing it", func(t *testing.T, d string) {
			lbWrite(t, d, ".github/workflows/zfs-build.yml", strings.Replace(pyWorkflow, "if command -v python3 || command -v python; then", "if false; then", 1))
		}},
		{"5c too few workflows", "check 5 found only 1 package-build workflow(s)", func(t *testing.T, d string) {
			os.Remove(filepath.Join(d, ".github/workflows/zfs-build.yml"))
		}},
		{"5 too few PKGBUILDs", "check 5 found only 4 PKGBUILD(s)", func(t *testing.T, d string) {
			os.Remove(filepath.Join(d, "build/pkgbuilds/a/PKGBUILD"))
		}},
		{"5 bpfdoc unwired", "packaging/aur/linux-runink/PKGBUILD no longer installs bpfdoc as scripts/bpf_doc.py in prepare()", func(t *testing.T, d string) {
			lbWrite(t, d, "packaging/aur/linux-runink/PKGBUILD", strings.Replace(pyKernelPKGBUILD, "\t_install_bpfdoc\n}", "}", 1))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := pyFixture(t)
			c.mutate(t, dir)
			gitAdd(t, dir)
			out, err := pyRun(t, dir)
			if err == nil {
				t.Fatalf("passed:\n%s", out)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("want %q in:\n%s", c.want, out)
			}
		})
	}
}

// A tracked .py already deleted in the working tree is a purge in progress, not a violation.
func TestPythonPurgeIgnoresDeletedPy(t *testing.T) {
	dir := pyFixture(t)
	lbWrite(t, dir, "tools/x.py", "print(1)\n")
	gitAdd(t, dir)
	os.Remove(filepath.Join(dir, "tools/x.py"))
	if out, err := pyRun(t, dir); err != nil {
		t.Fatalf("a deleted tracked .py failed the lint: %v\n%s", err, out)
	}
}

// FLOOR: a scan that looked at nothing must not report OK.
func TestPythonPurgeFloor(t *testing.T) {
	dir := pyFixture(t)
	for i := 0; i < 12; i++ {
		os.Remove(filepath.Join(dir, fmt.Sprintf("scripts/s%02d.sh", i)))
	}
	gitAdd(t, dir)
	if out, err := pyRun(t, dir); err == nil || !strings.Contains(out, "check 4 scanned only 2 shell script(s)") {
		t.Fatalf("want the floor to fail: %v\n%s", err, out)
	}
}
