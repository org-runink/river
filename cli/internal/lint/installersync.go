// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// installer-sync — installer step scripts live in TWO places:
//
//	installer/lib/<NN>-*.sh                                          (in-repo mirror)
//	iso-profiles/river/root-overlay/usr/local/lib/runink-install/<NN>-*.sh
//	  (the copy buildiso bakes into the live rootfs — AUTHORITATIVE, what RUNS at install)
//
// The two dirs may legitimately hold DIFFERENT sets (e.g. a legacy step only in the mirror, or
// a shipping-only step like 90-export). But any step present in BOTH must be byte-identical —
// they drifted once (a hostid fix landed only in installer/lib and never shipped, because the
// ISO uses the root-overlay copy) and it cost a live boot-panic + a rebuild.
//
// HOW TO FIX A FAILURE — reconcile by CONTENT, not by a fixed direction. The overlay is
// authoritative about what RUNS, but it is not automatically the newer or more correct
// text: buildiso reads iso-profiles/ and nothing ever copies installer/lib into it, so
// BOTH copies are hand-maintained and either one can be the stale side. Read `git log` on
// both files, decide which change is the real fix, then make them byte-identical.
// This header used to say "sync the mirror TO the overlay" unconditionally; following that
// in 2026-08 would have DELETED the hardware-confirmed `modprobe zfs` fix in 10-disk-zfs.sh
// instead of shipping it.
//
// Usage: river lint installer-sync [profile-dir ...]   (default: every iso-profiles/*;
// build/local-iso.sh passes an external profile's staged copy). A relative profile-dir is
// taken from the current directory, not from --repo.
//
// It also checks, per profile, that the installer DRIVER (runink-install) is identical in
// root-overlay/ and live-overlay/ and runs river-netsetup, then the LAN-install menu, before
// it plans; and that every installer command is built by build/20-installer-binaries.sh and
// packaged by runink-installer.

// installerTestOnly are the installer/ commands that are tests only and never ship.
var installerTestOnly = []string{"fakemeta"}

const (
	installerSrc = "installer/lib"
	installerBin = "build/20-installer-binaries.sh"
	installerPkg = "build/pkgbuilds/runink-installer/PKGBUILD"
)

func init() { extraLints = append(extraLints, installerSyncCmd) }

func installerSyncCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "installer-sync [profile-dir ...]",
		Short: "installer steps, the driver and the installer commands agree across installer/ and every profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			return InstallerSync(cmd.Context(), v.GetString("repo"), args, cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

// InstallerSync runs the lint over repo and the given profile directories (every
// iso-profiles/* when none is given). Findings go to outw, as the script printed them.
func InstallerSync(_ context.Context, repo string, profileDirs []string, errw, outw io.Writer) error {
	const name = "lint-installer-sync"
	r := &report{name: name, w: outw}
	// at maps a path as it is printed (relative to the repository, or absolute) to the file.
	at := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(repo, p)
	}

	var pdirs []string
	for _, a := range profileDirs {
		abs, err := filepath.Abs(a)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			return fmt.Errorf("%s: no such profile directory: %s", name, abs)
		}
		pdirs = append(pdirs, filepath.Clean(abs))
	}
	if len(pdirs) == 0 {
		m, _ := filepath.Glob(filepath.Join(repo, "iso-profiles", "*"))
		for _, p := range m {
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				rel, _ := filepath.Rel(repo, p)
				pdirs = append(pdirs, filepath.ToSlash(rel))
			}
		}
	}

	if st, err := os.Stat(at(installerSrc)); err != nil || !st.IsDir() {
		r.fail("%s missing", installerSrc)
		return r.err()
	}

	// EVERY profile's overlay: the in-tree one, and a downstream profile staged by
	// build/local-iso.sh (which passes it here). Each carries its own copy of the steps, and
	// checking only one is how the drift this lint exists to catch comes back — the
	// unchecked copy is the one that ships to whichever machine nobody rebuilt recently,
	// which is exactly the boot-panic that motivated this lint.
	var overlays []string
	for _, p := range pdirs {
		d := p + "/root-overlay/usr/local/lib/runink-install"
		if st, err := os.Stat(at(d)); err == nil && st.IsDir() {
			overlays = append(overlays, d)
		}
	}
	if len(overlays) == 0 {
		return errors.New(name + ": no profile installer overlays found under iso-profiles/*\n" +
			"  (this check would otherwise pass having compared nothing)")
	}

	steps, _ := filepath.Glob(filepath.Join(at(installerSrc), "*.sh"))
	shared := 0
	for _, ovl := range overlays {
		for _, s := range steps {
			step := installerSrc + "/" + filepath.Base(s)
			other := ovl + "/" + filepath.Base(s)
			if st, err := os.Stat(at(other)); err != nil || !st.Mode().IsRegular() {
				continue // set differences are allowed; only shared steps must match
			}
			shared++
			if !sameFile(at(step), at(other)) {
				r.fail("DRIFT: %s != %s", step, other)
				fmt.Fprintln(outw, "  the overlay copy is what SHIPS; check git log on both before picking a side")
			}
		}
	}

	// The DRIVER, runink-install, also exists twice per profile: root-overlay/ (the rootfs
	// copy) and live-overlay/ (the livefs layer, which is what the live session runs). The two
	// must be byte-identical, or which one an operator gets depends on the layer order. And
	// each driver must open with the network-first step (river-netsetup) before it probes or
	// plans anything (docs/INSTALL.md, "Network first").
	drivers := 0
	for _, p := range pdirs {
		root := p + "/root-overlay/usr/local/bin/runink-install"
		live := p + "/live-overlay/usr/local/bin/runink-install"
		hasRoot, hasLive := isRegular(at(root)), isRegular(at(live))
		if !hasRoot && !hasLive {
			continue
		}
		drivers++
		if !hasRoot || !hasLive {
			r.fail("%s has only one runink-install (want root-overlay AND live-overlay)", p)
			continue
		}
		if !sameFile(at(root), at(live)) {
			r.fail("DRIFT: %s != %s", root, live)
		}
		b, _ := os.ReadFile(at(root))
		lines := strings.Split(string(b), "\n")
		net := firstLine(lines, func(l string) bool { return strings.Contains(l, "river-netsetup") && !strings.HasPrefix(l, "#") })
		plan := firstLine(lines, func(l string) bool { return strings.HasPrefix(l, "hwplan_init") })
		if net == 0 || plan == 0 || net >= plan {
			r.fail("%s does not run river-netsetup (step 0) before hwplan_init", root)
		}
		// ... and then the LAN-install menu (pair-menu.sh, docs/INSTALL.md "LAN installs"):
		// after the network step, because pairing needs a link, and before the probe, because
		// "target" and "operator" never plan this machine. Two squash-merges once dropped one
		// or the other.
		menu := firstLine(lines, func(l string) bool { return strings.HasPrefix(l, "pair_menu ") })
		if menu == 0 || net == 0 || plan == 0 || menu <= net || menu >= plan {
			r.fail("%s does not offer the LAN-install menu (pair_menu) between river-netsetup and hwplan_init", root)
		}
	}
	if drivers == 0 {
		return errors.New(name + ": no runink-install driver found under iso-profiles/*\n" +
			"  (this check would otherwise pass having compared nothing)")
	}

	// The installer COMMANDS: every Go command under installer/ is built by
	// build/20-installer-binaries.sh and packaged by runink-installer, and river-netsetup
	// ships beside them. Three squash-merges in a row each rewrote both lists to their own
	// branch's set (network-first dropped river-payloadpack; the airgap follow-up dropped
	// river-netcheck; LAN pairing dropped river-payloadpack and river-cloud-init), so an image
	// lost a command nobody had removed. TEST-only commands never ship: installerTestOnly.
	texts := map[string]string{}
	for _, f := range []string{installerBin, installerPkg} {
		b, err := os.ReadFile(at(f))
		if err != nil || len(b) == 0 {
			return fmt.Errorf("%s: %s missing (nothing to check the commands against)", name, f)
		}
		texts[f] = string(b)
	}
	built := installerBuildList(texts[installerBin])
	if len(built) == 0 {
		return fmt.Errorf("%s: no 'for b in ...' build list in %s", name, installerBin)
	}
	packaged, ok := installerPackageList(texts[installerPkg])
	if !ok {
		return fmt.Errorf("%s: no 'for b in ...' package list in %s", name, installerPkg)
	}
	mains, _ := filepath.Glob(filepath.Join(repo, "installer", "*", "main.go"))
	cmds := 0
	for _, m := range mains {
		d := filepath.Base(filepath.Dir(m))
		if slices.Contains(installerTestOnly, d) {
			continue
		}
		cmds++
		out := ""
		for _, b := range built {
			src, dst, found := strings.Cut(b, ":")
			if !found {
				dst = b
			}
			if src == d {
				out = dst
			}
		}
		if out == "" {
			r.fail("installer/%s is a command but %s does not build it", d, installerBin)
			continue
		}
		if !slices.Contains(packaged, out) {
			r.fail("%s does not package %s (installer/%s)", installerPkg, out, d)
		}
	}
	if cmds == 0 {
		return fmt.Errorf("%s: no installer/*/main.go found", name)
	}
	if !strings.Contains(texts[installerBin], "installer/netsetup/river-netsetup") {
		r.fail("%s does not stage river-netsetup", installerBin)
	}
	if !slices.Contains(packaged, "river-netsetup") {
		r.fail("%s does not package river-netsetup", installerPkg)
	}

	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "%s: %d shared step(s) in sync across %d profile overlay(s) ✓\n", name, shared, len(overlays))
	fmt.Fprintf(outw, "%s: %d profile driver pair(s) identical, network first, then the LAN-install menu ✓\n", name, drivers)
	fmt.Fprintf(outw, "%s: %d installer command(s) + river-netsetup built and packaged ✓\n", name, cmds)
	return nil
}

// installerBuildList is the build script's `for b in ...; do` list, which may continue over
// backslash-newlines: from the line starting "for b in " to the first line ending "; do".
func installerBuildList(script string) []string {
	var l strings.Builder
	on := false
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, "for b in ") {
			on = true
		}
		if !on {
			continue
		}
		l.WriteString(" " + line)
		if strings.HasSuffix(line, "; do") {
			break
		}
	}
	s := strings.TrimLeft(l.String(), " ")
	s = strings.TrimPrefix(s, "for b in ")
	s = strings.TrimSuffix(s, "; do")
	return strings.Fields(strings.ReplaceAll(s, `\`, " "))
}

var installerPkgLoop = regexp.MustCompile(`(?m)^  for b in (.*); do$`)

// installerPackageList is the PKGBUILD's first `  for b in ...; do` list; ok is false when
// there is none or it is empty.
func installerPackageList(pkgbuild string) ([]string, bool) {
	m := installerPkgLoop.FindStringSubmatch(pkgbuild)
	if m == nil {
		return nil, false
	}
	// The script matched " $list " as a string; splitting on spaces is the same test for
	// every name without a space in it.
	if m[1] == "" {
		return nil, false
	}
	return strings.Split(m[1], " "), true
}

func firstLine(lines []string, match func(string) bool) int {
	for i, l := range lines {
		if match(l) {
			return i + 1
		}
	}
	return 0
}

func isRegular(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// sameFile is `cmp -s a b`: both readable and byte-identical.
func sameFile(a, b string) bool {
	x, errA := os.ReadFile(a)
	y, errB := os.ReadFile(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}
