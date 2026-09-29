// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// python-purge — guard the python purge in the ISO-build path, in BOTH directions.
//
// The purge itself already happened: scripts/patch-artools.py became
// scripts/patch-artools.go and scrub-manifest.py was deleted outright, and the ISO build
// pipeline stopped provisioning `python`. This lint is the control that keeps it that way, and
// it has two halves that fail differently:
//
//  1. python creeping back into the ISO-build path — the thing the workspace rule bans.
//  2. `go` NOT being installed where build-iso-box.sh now needs it. This is the failure
//     mode a ban-python-only check would call clean: the ISO pipeline provisioned `python`
//     for the old patch-artools.py, and if `go` had not gone in alongside, the first
//     `go build` in build-iso-box.sh would die with "go: command not found" under
//     `set -e`. The guard has to assert the replacement is present, not just that the
//     original is gone.
//
// What this does NOT establish:
//   - NOT that the ISO builds. Nothing here runs buildiso, pacman, or go.
//   - NOT that `go` resolves to a usable toolchain at runtime — only that the package is
//     requested. A broken mirror, or a `go` package shipping without the compiler, still
//     fails at build time.
//   - NOT that patch-artools.go behaves like the .py it replaced. That was established in
//     os#60 by running both against identical fixtures and diffing; it is not re-checked
//     here, and this lint would not notice a behavioural regression in it.
//   - NOT that python is absent from the whole repo, though it is closer than it was.
//     Check 4 covers INLINE python in every tracked shell script, which is the gap check 1
//     cannot see: `python3 -c '...'` inside a .sh is python however it is spelled, and a
//     .py-extension scan calls that file clean. the old install/ fetch script was
//     exactly that until the check existed. The kernel build was the last exception: kernel-build.yml
//     and zfs-build.yml installed `python` for the kernel's scripts/bpf_doc.py. That script
//     now runs as build/tools/bpfdoc (a Go port), and check 5 keeps the interpreter out of
//     the kernel and OpenZFS build path.
//   - NOT that python is absent from anything but shell scripts, .py files and the package
//     builds check 5 reads (PKGBUILD dependency arrays, the two build Containerfiles, the
//     makepkg workflows). A Makefile recipe, another Containerfile, or a python script named
//     without .py and run by its shebang all walk straight past every check here.
//   - NOT that the kernel build runs no python: check 5 reads files, it builds nothing. That
//     was established by building linux-runink in build/kernel-builder.Containerfile with no
//     interpreter on PATH (river PR "no-python kernel build"); a new kernel series can add a
//     python step, and only a build would show it.
//   - NOT that vendored third-party python is gone: build/.out/ is gitignored build output
//     and can hold an untracked upstream checkout with .py files in it. It is not ours.
//
// This lint is Go, so check 4 (which reads shell scripts) never reads its own patterns.

func init() { extraLints = append(extraLints, pythonPurgeCmd) }

func pythonPurgeCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "python-purge",
		Short: "no python in the build path, and go provisioned where build-iso-box.sh needs it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return PythonPurge(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

var (
	// Check 2's word: deliberately blunt.
	pyWord2 = regexp.MustCompile(`(^|[^[:alnum:]_-])python3?([^[:alnum:]_-]|$)`)
	// Check 4's word: a `.python` suffix (a file name) is not an invocation.
	pyWord4 = regexp.MustCompile(`(^|[^[:alnum:]_.-])python3?([^[:alnum:]_-]|$)`)
	// Check 5's word: python, python3, python3.12, python-foo.
	pyWord5 = regexp.MustCompile(`(^|[^[:alnum:]_.-])python[0-9.]*(-[[:alnum:]_.-]+)?([^[:alnum:]_.-]|$)`)
	// `go` as a package word in an install list.
	pyGoWord      = regexp.MustCompile(`(^|[[:space:]])go([[:space:]]|\\|$)`)
	pyTrailComm   = regexp.MustCompile(`[[:space:]]*#.*$`)
	pyDependsRe   = regexp.MustCompile(`^[[:space:]]*(make|check|opt)?depends(_[A-Za-z0-9_]+)?=\(`)
	pyBpfdocInst  = regexp.MustCompile(`install -m755 "\$srcdir/bpfdoc" scripts/bpf_doc.py`)
	pyBpfdocCall  = regexp.MustCompile(`^[[:space:]]*_install_bpfdoc$`)
	pyPrepareOpen = regexp.MustCompile(`^prepare\(\)`)
)

// pyLines splits a file into lines, without a phantom last one.
func pyLines(b []byte) []string {
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func pyAnyLine(lines []string, re *regexp.Regexp) bool {
	for _, l := range lines {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// pyStripHash is `sub(/#.*/, "", line)`.
func pyStripHash(l string) string {
	if i := strings.IndexByte(l, '#'); i >= 0 {
		return l[:i]
	}
	return l
}

// pyPkgLists returns the package-install lines of a file: from each `pacman -S...` to the end
// of that command (the first line that does not end in `\`), comments stripped.
func pyPkgLists(lines []string) []string {
	var out []string
	on := false
	for _, l := range lines {
		if strings.Contains(l, "pacman -S") {
			on = true
		}
		if on {
			out = append(out, pyStripHash(l))
			if !strings.HasSuffix(l, `\`) {
				on = false
			}
		}
	}
	return out
}

// pyDepends returns a PKGBUILD's dependency arrays, comments stripped.
func pyDepends(lines []string) []string {
	var out []string
	on := false
	for _, l := range lines {
		if pyDependsRe.MatchString(l) {
			on = true
		}
		if on {
			out = append(out, pyStripHash(l))
			if strings.Contains(l, ")") {
				on = false
			}
		}
	}
	return out
}

// PythonPurge runs the lint over repo.
func PythonPurge(ctx context.Context, repo string, errw, outw io.Writer) error {
	r := &report{name: "lint-python-purge", w: errw}
	at := func(p string) string { return filepath.Join(repo, p) }
	read := func(p string) ([]string, bool) {
		b, err := os.ReadFile(at(p))
		if err != nil {
			return nil, false
		}
		return pyLines(b), true
	}
	exists := func(p string) bool { _, err := os.Stat(at(p)); return err == nil }

	// 1. No first-party python, anywhere. Tracked files only — build/.out/ is untracked.
	//    Filtered to files that still EXIST: a tracked .py already deleted in the working tree
	//    is a purge in progress, not a violation, and must not fail the lint before it is staged.
	pys, _ := gitLines(ctx, repo, "ls-files", "*.py")
	var py []string
	for _, f := range pys {
		if exists(f) {
			py = append(py, f)
		}
	}
	if len(py) > 0 {
		r.fail("tracked python file(s) found — this repo ships no python:")
		for _, f := range py {
			fmt.Fprintf(errw, "    %s\n", f)
		}
	}

	// 2. The ISO build script itself must not shell out to python. Deliberately blunt: it
	//    matches the word anywhere, comments included, so documenting the removal IN that
	//    file by naming python would trip it. Say it here instead, not there.
	if lines, ok := read("scripts/build-iso-box.sh"); ok {
		hit := false
		for i, l := range lines {
			if pyWord2.MatchString(l) {
				fmt.Fprintf(errw, "%d:%s\n", i+1, l)
				hit = true
			}
		}
		if hit {
			r.fail("scripts/build-iso-box.sh invokes python (see the matching line(s) above)")
		}
	}

	// 3. Wherever build-iso-box.sh runs in a container that provisions its OWN packages, that
	//    package list must include `go`, because the script `go build`s patch-artools.go.
	//    Derived rather than hardcoded so a NEW ci path is covered the day it is added.
	_ = filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := filepath.Ext(p); ext != ".yml" && ext != ".yaml" || !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), "build-iso-box") {
			return nil
		}
		lines := pyLines(b)
		// Uses a prebuilt image when it installs nothing: not its job to install go.
		var ctxLines []string
		after := -1
		for i, l := range lines {
			if strings.Contains(l, "pacman -S") {
				after = i + 3
			}
			if i <= after {
				ctxLines = append(ctxLines, l)
			}
		}
		if after < 0 {
			return nil
		}
		if !pyAnyLine(ctxLines, pyGoWord) {
			rel, _ := filepath.Rel(repo, p)
			r.fail("./%s provisions packages and runs build-iso-box.sh, but does not install 'go' — the script's 'go build' would fail", filepath.ToSlash(rel))
		}
		return nil
	})

	// 3b. A release pipeline runs build-iso-box.sh inside the prebuilt runink-os-builder image,
	//     so that image is the other place `go` has to exist. Checked by name because the
	//     Containerfile never mentions build-iso-box.sh and so cannot be derived above.
	if lines, _ := read("builder/Containerfile"); !pyAnyLine(lines, pyGoWord) {
		r.fail("builder/Containerfile does not install 'go' — release builds run build-iso-box.sh in that image")
	}

	// 4. No tracked shell script INVOKES python. This is the gap check 1 cannot see: a
	//    `python3 -c '...'` one-liner inside a .sh has no .py extension, so an extension scan
	//    reports a clean repo over it. The old install/ fetch script was exactly that.
	//
	//    Comment lines are stripped FIRST, which is the opposite of check 2's deliberate
	//    bluntness and is deliberate too: this runs over the whole repo, where the right way
	//    to remove python is to leave a comment saying what was removed and why. A check that
	//    forbade the explanation would be paid for by deleting the history, which is the trade
	//    this repo consistently refuses. The cost is that a python invocation hidden in a
	//    commented-out line is not seen — it is also not run.
	scanned := 0
	shs, _ := gitLines(ctx, repo, "ls-files", "*.sh")
	for _, f := range shs {
		// cli/vendor is third-party Go modules copied by `go mod vendor`; their helper scripts
		// are never run (the same reason build/.out/ is not ours).
		if strings.HasPrefix(f, "cli/vendor/") {
			continue
		}
		lines, ok := read(f)
		if !ok {
			continue
		}
		scanned++
		var hits []string
		for i, l := range lines {
			if l = pyTrailComm.ReplaceAllString(l, ""); pyWord4.MatchString(l) {
				hits = append(hits, fmt.Sprintf("    %d:%s", i+1, l))
			}
		}
		if len(hits) > 0 {
			r.fail("%s invokes python (comment lines excluded):", f)
			fmt.Fprintln(errw, strings.Join(hits, "\n"))
		}
	}
	//    FLOOR. The loop above iterates `git ls-files '*.sh'`; if that returns nothing —
	//    wrong working directory, no git, a glob that stops matching — every file is clean
	//    and the check passes silently. A scan that looked at nothing must not report OK.
	if scanned < 10 {
		r.fail("check 4 scanned only %d shell script(s); this repo has far more, so the scan is not looking at it", scanned)
	}

	// 5. No interpreter in the package builds, kernel included. Three places could put one back:
	//    a. a PKGBUILD naming it in depends/makedepends/checkdepends/optdepends. The kernel ones
	//       (in-tree and AUR) needed it for scripts/bpf_doc.py until build/tools/bpfdoc
	//       replaced it.
	//    b. a build container: builder/Containerfile and build/kernel-builder.Containerfile must
	//       not install it and must keep the RUN that removes it (base-devel pulls it in through
	//       debugedit -> gdb) and fails the image build while an interpreter is on PATH.
	//    c. a workflow that runs makepkg in its own container: it must not install it, and when
	//       it installs base-devel it must remove it and fail while it is still there.
	//    And the replacement must stay wired: both kernel PKGBUILDs install bpfdoc as
	//    scripts/bpf_doc.py in prepare(), or the kernel build would need the interpreter again.
	npkg := 0
	var pkgbuilds []string
	for _, g := range []string{"build/pkgbuilds/*/PKGBUILD", "packaging/aur/*/PKGBUILD"} {
		m, _ := filepath.Glob(at(g))
		pkgbuilds = append(pkgbuilds, m...)
	}
	for _, p := range pkgbuilds {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		npkg++
		b, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(repo, p)
		var bad []string
		for _, l := range pyDepends(pyLines(b)) {
			if pyWord5.MatchString(l) {
				bad = append(bad, "    "+l)
			}
		}
		if len(bad) > 0 {
			r.fail("%s declares a python dependency:", filepath.ToSlash(rel))
			fmt.Fprintln(errw, strings.Join(bad, "\n"))
		}
	}
	if npkg < 5 {
		r.fail("check 5 found only %d PKGBUILD(s); the search paths are stale", npkg)
	}
	for _, f := range []string{"builder/Containerfile", "build/kernel-builder.Containerfile"} {
		lines, ok := read(f)
		if !ok {
			r.fail("missing %s", f)
			continue
		}
		if pyAnyLine(pyPkgLists(lines), pyWord5) {
			r.fail("%s installs python", f)
		}
		if !pyContains(lines, "pacman -Rdd --noconfirm $py") {
			r.fail("%s no longer removes the interpreter base-devel pulls in", f)
		}
		if !pyContains(lines, "! command -v python3 && ! command -v python") {
			r.fail("%s no longer fails its build while an interpreter is on PATH", f)
		}
	}
	if lines, _ := read("build/kernel-builder.Containerfile"); !pyAnyLine(pyPkgLists(lines), pyGoWord) {
		r.fail("build/kernel-builder.Containerfile does not install 'go' (the kernel PKGBUILD compiles bpfdoc)")
	}
	nwf := 0
	wfs, _ := filepath.Glob(at(".github/workflows/*.yml"))
	for _, p := range wfs {
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), "makepkg") || !strings.Contains(string(b), "pacman -S") {
			continue
		}
		nwf++
		rel, _ := filepath.Rel(repo, p)
		rel = filepath.ToSlash(rel)
		lines := pyLines(b)
		pl := pyPkgLists(lines)
		if pyAnyLine(pl, pyWord5) {
			r.fail("%s installs python for a package build", rel)
		}
		if pyContains(pl, "base-devel") && !pyContains(lines, "if command -v python3 || command -v python; then") {
			r.fail("%s installs base-devel (which pulls in python) without removing it and failing while it remains", rel)
		}
	}
	if nwf < 2 {
		r.fail("check 5 found only %d package-build workflow(s); kernel-build.yml and zfs-build.yml should be two", nwf)
	}
	for _, f := range []string{"build/pkgbuilds/runink-kernel/PKGBUILD", "packaging/aur/linux-runink/PKGBUILD"} {
		lines, _ := read(f)
		if !pyAnyLine(lines, pyBpfdocInst) || !pyAnyLine(pyPrepare(lines), pyBpfdocCall) {
			r.fail("%s no longer installs bpfdoc as scripts/bpf_doc.py in prepare(); the kernel build would need python", f)
		}
	}

	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-python-purge: OK (%d shell scripts scanned for inline python)\n", scanned)
	return nil
}

func pyContains(lines []string, s string) bool {
	for _, l := range lines {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

// pyPrepare returns a PKGBUILD's prepare() function, from its opening line to before the
// first line starting with `}`.
func pyPrepare(lines []string) []string {
	var out []string
	on := false
	for _, l := range lines {
		if pyPrepareOpen.MatchString(l) {
			on = true
		}
		if on && strings.HasPrefix(l, "}") {
			break
		}
		if on {
			out = append(out, l)
		}
	}
	return out
}
