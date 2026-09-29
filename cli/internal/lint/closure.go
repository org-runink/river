// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// closure — closure-lint the package manifest: fail if any FORBIDDEN package appears in the
// dependency closure of Packages-Root. This is what keeps the desktop/GUI/codec stack (and a
// second kernel, and non-Intel firmware) from creeping back into the golden image.
//
// Usage: river lint closure <Packages-Root>
//
// REQUIRES pacman + pactree. It used to fall back to a literal grep of the manifest when they
// were absent and print the SAME "OK — no forbidden packages in the manifest closure" either
// way, so a run that resolved 206 packages and a run that read 33 lines of text were
// indistinguishable in the output. Measured on this manifest: the closure is ~206 packages,
// the explicit list is 33 — the fallback saw 16% of what its verdict claimed. It now refuses
// to run rather than emit a verdict it cannot support.
//
// The weak check still exists, but you have to ask for it by name and it says what it did:
//
//	river lint closure --mode manifest-only <Packages-Root>
//	LINT_CLOSURE_MODE=manifest-only river lint closure <Packages-Root>   (the script's spelling)
//
// WHY NOT JUST RUN IT IN AN ARCH CONTAINER (the standing suggestion). Because resolving this
// manifest needs FIVE repos, and no public base image has them all — see pacman/pacman.conf.in:
// Artix [system]/[world]/[galaxy], Arch [extra], and [runink], which is `file://@LOCALREPO@` —
// a local repo that does not exist until `make components && make localrepo` has built it.
// Measured against a full Arch install, 14 of the 33 explicit packages (every s6-*/-s6 Artix
// package and every runink-* package) resolve to ZERO dependencies in both the local and the
// sync database, because they are simply not there. A stock `archlinux:latest` job would
// therefore reproduce the same hollow verdict inside a container. The only context where a
// true closure is resolvable is after the component build — i.e. inside the ISO build (make
// iso-in-builder), not a bare hosted runner.

func init() { extraLints = append(extraLints, closureCmd) }

func closureCmd(v *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:   "closure <Packages-Root>",
		Short: "no forbidden package in the dependency closure of a profile's Packages-Root (needs pacman + pactree)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return Closure(cmd.Context(), args[0], v.GetString("mode"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
	c.Flags().String("mode", "closure", "closure (resolve with pactree) or manifest-only (the weak check, by name)")
	// The script read LINT_CLOSURE_MODE; RIVER_MODE is the CLI's own spelling. Both work.
	_ = v.BindEnv("mode", "RIVER_MODE", "LINT_CLOSURE_MODE")
	return c
}

// closurePactree resolves one package's dependency tree, as a list of names. It is a variable
// so tests can stand in for pacman.
var closurePactree = func(ctx context.Context, sync bool, pkg string) []string {
	args := []string{"-u", "-d", "99", pkg}
	if sync {
		args = append([]string{"-s"}, args...)
	}
	out, err := pipe.Output(ctx, nil, pipe.Cmd("pactree", args...))
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// closureHaveTools reports whether pacman and pactree are installed; a variable for tests.
var closureHaveTools = func() bool {
	_, e1 := exec.LookPath("pactree")
	_, e2 := exec.LookPath("pacman")
	return e1 == nil && e2 == nil
}

var closureCommentRe = regexp.MustCompile(`^\s*(#|$)`)

// closurePolicy reads a policy file: its non-comment, non-blank lines, split into words as
// the script's unquoted `for bad in $FORBIDDEN` did.
func closurePolicy(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if closureCommentRe.MatchString(l) {
			continue
		}
		out = append(out, strings.Fields(l)...)
	}
	return out
}

// closureHas is `printf '%s\n' "$list" | grep -qxE "$re"`.
func closureHas(list []string, re string) bool {
	x, err := regexp.Compile(`^(?:` + re + `)$`)
	if err != nil {
		return false
	}
	for _, s := range list {
		if x.MatchString(s) {
			return true
		}
	}
	return false
}

// Closure runs the lint on the manifest (relative to the working directory, as the script's
// argument was). mode is "closure" or "manifest-only".
func Closure(ctx context.Context, manifest, mode string, errw, outw io.Writer) error {
	fail := func(format string, args ...any) error {
		fmt.Fprintf(errw, format+"\n", args...)
		return errors.New("lint-closure: failed")
	}
	mb, err := os.ReadFile(manifest)
	if err != nil {
		return fail("lint-closure: no such file: %s", manifest)
	}
	if mode == "" {
		mode = "closure"
	}
	if mode != "closure" && mode != "manifest-only" {
		return fail("lint-closure: unknown LINT_CLOSURE_MODE='%s' (want 'closure' or 'manifest-only')", mode)
	}

	// Refuse to produce a verdict we cannot support. A closure lint without a dependency
	// resolver is a text search wearing the name of a dependency check.
	if mode == "closure" && !closureHaveTools() {
		return fail(`lint-closure: REFUSING TO RUN — pacman/pactree not found.

  This lint resolves the full dependency closure of the manifest. Without pactree
  it can only grep the 33 explicit package names, which is ~16%% of the ~206-package
  closure it claims to check. It used to do that silently and still print "OK";
  it no longer will, because a green tick over no coverage is worse than a red one.

  Run it on Artix/Arch (or in the iso-build pipeline, which has the [runink]
  localrepo the manifest actually needs), or ask for the weak check explicitly:

    LINT_CLOSURE_MODE=manifest-only river lint closure <Packages-Root>

  That mode reports what it covered and does NOT claim to have resolved a closure.`)
	}

	// The policy is owned by the PROFILE, not by this linter.
	//
	// It used to be two literals in the linter, which was correct while river was the only
	// profile. It stopped being correct the moment a workstation profile arrived: the server
	// list bans mesa, xorg, cairo, pipewire and base-devel — exactly right for a headless
	// sovereign appliance, and exactly wrong for a KDE developer desktop. One global list could
	// only ever be right for one of them, and being wrong means either failing a legitimate
	// build or, worse, passing an illegitimate one.
	//
	// Both files are REQUIRED. A missing file is an error, never an empty policy: linting
	// against nothing is the exact failure this linter exists to prevent, and it would report
	// a confident green having checked no packages at all.
	profileDir := filepath.Dir(manifest)
	closurePolicyFile := profileDir + "/forbidden.closure"
	explicitPolicyFile := profileDir + "/forbidden.explicit"
	policies := map[string][]byte{}
	for _, f := range []string{closurePolicyFile, explicitPolicyFile} {
		b, err := os.ReadFile(f)
		if err != nil {
			return fail("lint-closure: missing policy file: %s\n"+
				"  Every profile must declare what it forbids. Copy one from a sibling profile\n"+
				"  and EDIT it — never copy river's into a desktop profile unread.", f)
		}
		policies[f] = b
	}
	// Forbidden = must not appear anywhere in the resolved closure.
	forbidden := closurePolicy(policies[closurePolicyFile])
	// Hardening: must never be listed EXPLICITLY (checked against the manifest, not the closure).
	hardening := closurePolicy(policies[explicitPolicyFile])
	// An empty closure policy is almost certainly a mistake — a profile that forbids nothing
	// makes every check below a no-op. Say so rather than passing.
	if len(forbidden) == 0 {
		return fail("lint-closure: %s has no entries — this would check nothing", closurePolicyFile)
	}

	// Explicit packages (comments and blanks stripped).
	pkgs := closurePolicy(mb)
	if len(pkgs) == 0 {
		return fail("lint-closure: manifest is empty")
	}
	nExplicit := len(pkgs)

	failed := false
	report := func(s string) { fmt.Fprintf(errw, "  FORBIDDEN in closure: %s\n", s); failed = true }
	reportHard := func(s string) { fmt.Fprintf(errw, "  FORBIDDEN (hardening — do not re-add): %s\n", s); failed = true }

	for _, bad := range hardening {
		if closureHas(pkgs, bad) {
			reportHard(bad)
		}
	}

	var verdict string
	if mode == "closure" {
		fmt.Fprintln(outw, "lint-closure: resolving closure with pactree ...")
		// `-s` reads the SYNC databases (the pinned repos this manifest is built from), which is
		// what the header always claimed. Without it pactree reads the LOCAL database — i.e. the
		// packages that happen to be installed on whatever machine runs the lint — so coverage
		// silently tracked the linting host rather than the manifest. Fall back to the local db
		// per package so an installed-but-not-in-a-repo package still contributes.
		set := map[string]bool{}
		var unresolved []string
		for _, p := range pkgs {
			t := closurePactree(ctx, true, p)
			if len(t) == 0 {
				t = closurePactree(ctx, false, p)
			}
			if len(t) == 0 {
				unresolved = append(unresolved, p)
				continue
			}
			for _, x := range t {
				set[x] = true
			}
		}
		closure := make([]string, 0, len(set))
		for x := range set {
			closure = append(closure, x)
		}
		slices.Sort(closure)

		for _, bad := range forbidden {
			// Match whole package names, and vendored firmware splits by prefix rules below.
			if closureHas(closure, bad) {
				report(bad)
			}
		}
		// A second (mainline) kernel or the firmware meta are the sneaky ones:
		if closureHas(closure, "linux") {
			report("linux (mainline kernel — keep linux-runink only)")
		}
		if closureHas(closure, "linux-firmware") {
			report("linux-firmware (meta — use linux-firmware-intel)")
		}

		// Say how much of the manifest actually resolved. A package no repo can resolve
		// contributes NOTHING to the closure, so its subtree is unchecked — that is a real gap
		// in coverage and it must be visible in the output, not inferred from a silent `|| true`.
		if len(unresolved) > 0 {
			fmt.Fprintf(errw, "  NOTE: %d/%d package(s) not in any configured repo — their subtrees were NOT checked:\n", len(unresolved), nExplicit)
			fmt.Fprintf(errw, "       %s\n", strings.Join(unresolved, " "))
			fmt.Fprintln(errw, "       (expected outside a built tree: the s6/Artix and runink-* packages come from [system]/[world]/[galaxy] and the [runink] localrepo)")
		}
		// Resolving NOTHING is not a pass. If no manifest entry resolved, pactree exists but has
		// no usable database for this manifest (wrong repos configured, empty sync db), and the
		// run examined exactly as much as the old grep fallback did.
		if len(closure) == 0 {
			return fail("lint-closure: FAIL — pactree resolved 0 packages from %d manifest entries.\n"+
				"  pacman is installed but none of these packages are in any configured repo, so nothing was checked.\n"+
				"  Run `pacman -Sy`, or run this where the manifest's repos exist (see pacman/pacman.conf.in).", nExplicit)
		}
		verdict = fmt.Sprintf("closure — resolved %d package(s) from %d/%d manifest entries", len(closure), nExplicit-len(unresolved), nExplicit)
	} else {
		fmt.Fprintln(outw, "lint-closure: MANIFEST-ONLY mode — NOT resolving any dependency closure.")
		for _, bad := range forbidden {
			if closureHas(pkgs, bad) {
				report(bad)
			}
		}
		verdict = fmt.Sprintf("MANIFEST-ONLY — grepped %d explicit package name(s); transitive dependencies were NOT examined", nExplicit)
	}

	// Positive assertions independent of pacman: exactly one kernel, intel-only firmware.
	if closureHas(pkgs, "linux") {
		report("linux (mainline kernel listed explicitly)")
	}
	// linux-runink since 2026-09-06 (os#70). Promoted from a WARN to a real failure: this is
	// the assertion that the image pins EXACTLY ONE kernel, and that it is the one runink-zfs's
	// prebuilt modules were compiled against. A warning was the wrong severity — the entire
	// reason for the swap is that a ZFS/kernel mismatch shipped twice while only warning.
	if !closureHas(pkgs, "linux-runink") {
		report("linux-runink not explicitly listed (the profile must pin its kernel)")
	}
	// The old kernel must not linger alongside it. linux-runink declares no conflicts= or
	// replaces= for linux-lts, so nothing else would prevent both being installed.
	if closureHas(pkgs, "linux-lts") {
		report("linux-lts (superseded by linux-runink — that would be two kernels)")
	}
	if closureHas(pkgs, "linux-firmware") {
		report("linux-firmware (use linux-firmware-intel)")
	}

	if failed {
		return fail("lint-closure: FAIL [%s]", verdict)
	}
	fmt.Fprintf(outw, "lint-closure: OK [%s]\n", verdict)
	return nil
}
