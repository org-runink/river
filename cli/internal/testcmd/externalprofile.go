// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
)

// readProfile is the second fixed piece of shell: source build/profile-lib.sh, call
// river_profile_read with the remaining arguments and print the variables it set, one line,
// KIND/ISO_LABEL/ISO_NAME/GRUB_TITLE/EDITION.
const readProfile = `. "$1" && shift && river_profile_read "$@" || exit
printf '%s/%s/%s/%s/%s\n' "$RP_KIND" "$RP_ISO_LABEL" "$RP_ISO_NAME" "$RP_GRUB_TITLE" "$RP_EDITION"`

// ExternalProfile — a profile that lives OUTSIDE this repository (a downstream distribution,
// docs/BUILD.md "Downstream distributions"), exercised through the same helpers
// build/local-iso.sh and scripts/build-iso-box.sh use (build/profile-lib.sh), in a scratch
// directory, no root and no network. Was tests/external-profile.sh.
//
//	in-tree     river (Runink River, the one in-tree profile) is known by name: a workstation,
//	            label RIVER, ISO runink-river-<date>
//	described   an external profile reads its kind, label, ISO name and title from
//	            river-profile.env; a missing file, an unknown key or a bad value is refused
//	staged      the copy: branding overlay/ layered on, remove list applied, the source untouched;
//	            a stale or escaping remove entry is refused
//	editions    exactly one edition descriptor must carry the image's own label; on a medium
//	            with several editions under one label (EDITION), exactly one with that label
//	            and id, and every live menu entry passes river.edition=<id>
//	lints       lint-installer-sync and lint-profile-manifest accept the staged copy, and catch
//	            a drifted installer step in it
//
// Tier 1 runs it (scripts/ci-tier1.sh).
func ExternalProfile(ctx context.Context, repo string, outw, errw io.Writer) error {
	root, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	t, err := os.MkdirTemp("", "external-profile.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(t)
	// The ported lints are subcommands of this same binary.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("external-profile: %w", err)
	}
	e := &epFixture{ctx: ctx, root: root, lib: filepath.Join(root, "build/profile-lib.sh"), t: t, river: self,
		c: &checks{out: outw, err: errw}}
	if err := e.checks(outw); err != nil {
		return fmt.Errorf("external-profile: %w", err)
	}
	c := e.c
	verdict := "all passed"
	if c.failed != 0 {
		verdict = "FAILURES"
	}
	fmt.Fprintf(outw, "external-profile: %d checks, %s\n", c.n, verdict)
	if c.failed != 0 {
		return fmt.Errorf("external-profile: %d of %d checks failed", c.failed, c.n)
	}
	return nil
}

type epFixture struct {
	ctx     context.Context
	root    string // the checkout
	river   string // the river binary running this test, for `river lint`
	lib     string // build/profile-lib.sh
	t       string
	c       *checks
	edition string // RP_EDITION from the last successful read
}

// read is river_profile_read DIR NAME: the KIND/ISO_LABEL/ISO_NAME/GRUB_TITLE it set, or the
// error (with what it printed) when it refused.
func (e *epFixture) read(dir, name string) (string, error) {
	out, err := combined(e.ctx, pipe.Cmd("sh", "-c", readProfile, "sh", e.lib, dir, name))
	if err != nil {
		return "", fmt.Errorf("%w\n%s", err, out)
	}
	ls := lines(out)
	last := ""
	if len(ls) > 0 {
		last = ls[len(ls)-1]
	}
	i := strings.LastIndex(last, "/")
	if i < 0 {
		return "", fmt.Errorf("river_profile_read printed %q", out)
	}
	e.edition = last[i+1:]
	return last[:i], nil
}

// expect runs a command that must succeed; on a failure, its output is shown indented.
func (e *epFixture) expect(d string, cmd pipe.Command) {
	out, err := combined(e.ctx, cmd)
	if err == nil {
		e.c.ok(d)
		return
	}
	e.c.ko(d)
	for _, l := range lines(out) {
		fmt.Fprintf(e.c.err, "        %s\n", l)
	}
}

// refuse runs a command that must fail.
func (e *epFixture) refuse(d string, cmd pipe.Command) {
	if _, err := combined(e.ctx, cmd); err == nil {
		e.c.ko(d + " (was accepted)")
	} else {
		e.c.ok(d)
	}
}

func (e *epFixture) fn(name string, args ...string) pipe.Command {
	return libCall(e.lib, name, args...)
}

// riverLint is `river lint NAME --repo ROOT ARGS...`.
func (e *epFixture) riverLint(name string, args ...string) pipe.Command {
	return pipe.Command{Name: e.river, Args: append([]string{"lint", name, "--repo", e.root}, args...)}
}

// check is `[ cond ] && ok d || ko bad`.
func (e *epFixture) check(cond bool, d, bad string) {
	if cond {
		e.c.ok(d)
	} else {
		e.c.ko(bad)
	}
}

// bad reads a copy of the external profile whose river-profile.env has line appended.
func (e *epFixture) bad(src, line string) error {
	dir := filepath.Join(e.t, "bad")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(src, "profile.yaml"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "profile.yaml"), b, 0o644); err != nil {
		return err
	}
	env, err := os.ReadFile(filepath.Join(src, "river-profile.env"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "river-profile.env"), append(env, []byte(line+"\n")...), 0o644)
}

// refuseBad is `refuse D bad LINE`.
func (e *epFixture) refuseBad(d, src, line string) error {
	if err := e.bad(src, line); err != nil {
		return err
	}
	if _, err := e.read(filepath.Join(e.t, "bad"), "bad"); err == nil {
		e.c.ko(d + " (was accepted)")
	} else {
		e.c.ok(d)
	}
	return nil
}

func (e *epFixture) checks(outw io.Writer) error {
	c, root := e.c, e.root

	fmt.Fprintln(outw, "== the in-tree profile, known by name")
	got, err := e.read(filepath.Join(root, "iso-profiles/river"), "river")
	if err != nil {
		return err
	}
	e.check(got == "workstation/RIVER/runink-river/Runink River",
		"river: workstation, RIVER, runink-river, Runink River", "river: got "+got)
	e.expect("river: its own edition descriptor carries its label",
		e.fn("river_profile_self_edition", filepath.Join(root, "iso-profiles/river"), "RIVER"))
	profiles, err := os.ReadDir(filepath.Join(root, "iso-profiles"))
	if err != nil {
		return err
	}
	var names []string
	for _, p := range profiles {
		names = append(names, p.Name())
	}
	e.check(slices.Equal(names, []string{"river"}), "river is the only in-tree profile",
		"in-tree profiles: "+strings.Join(names, " ")+" ")

	fmt.Fprintln(outw, "== an external profile describes itself")
	src := filepath.Join(e.t, "downstream/profiles/example-desk")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(root, "iso-profiles/river"), src); err != nil {
		return err
	}
	e.refuse("no river-profile.env: refused", pipe.Cmd("sh", "-c", readProfile, "sh", e.lib, src, "example-desk"))
	if err := os.WriteFile(filepath.Join(src, "river-profile.env"), []byte(
		"# an example downstream edition\nKIND=workstation\nISO_LABEL=EXAMPLE_DESK\nISO_NAME=example-desk\nGRUB_TITLE=Example Desk\n"), 0o644); err != nil {
		return err
	}
	got, err = e.read(src, "example-desk")
	if err != nil {
		return err
	}
	e.check(got == "workstation/EXAMPLE_DESK/example-desk/Example Desk",
		"river-profile.env read (kind, label, name, title)", "river-profile.env: got "+got)
	for _, b := range []struct{ d, line string }{
		{"KIND=desktop refused", "KIND=desktop"},
		{"lower-case ISO_LABEL refused", "ISO_LABEL=example"},
		{"33-character ISO_LABEL refused", "ISO_LABEL=ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456"},
		{"ISO_NAME with a slash refused", "ISO_NAME=../x"},
		{"GRUB_TITLE with a quote refused", `GRUB_TITLE=Ex"ample`},
		{"GRUB_TITLE with a pipe refused", "GRUB_TITLE=a|b"},
		{"unknown key refused", "COLOUR=red"},
	} {
		if err := e.refuseBad(b.d, src, b.line); err != nil {
			return err
		}
	}

	fmt.Fprintln(outw, "== the staged copy: branding overlay and remove list")
	br := filepath.Join(e.t, "downstream/branding/example-desk")
	edRel := "live-overlay/usr/share/river/installer/editions"
	for _, d := range []string{"overlay/root-overlay/etc", "overlay/grub/theme", "overlay/" + edRel} {
		if err := os.MkdirAll(filepath.Join(br, d), 0o755); err != nil {
			return err
		}
	}
	riverJSON, err := os.ReadFile(filepath.Join(src, edRel, "river.json"))
	if err != nil {
		return err
	}
	for path, body := range map[string]string{
		"overlay/root-overlay/etc/os-release": "NAME=\"Example Desk\"\n",
		"overlay/grub/theme/theme.txt":        "title-text: \"\"\n",
		"overlay/" + edRel + "/river.json":    sedFirst(string(riverJSON), `"medium_label": *"RIVER"`, `"medium_label": "EXAMPLE_DESK"`),
		"remove":                              "# River-branded pieces this edition does not ship\nroot-overlay/usr/share/wallpapers/River\n",
	} {
		if err := os.WriteFile(filepath.Join(br, path), []byte(body), 0o644); err != nil {
			return err
		}
	}
	before, err := listTree(src)
	if err != nil {
		return err
	}
	dst := filepath.Join(e.t, "state/profiles/example-desk/example-desk")
	e.expect("staged", e.fn("river_profile_stage", src, br, dst))
	after, err := listTree(src)
	if err != nil {
		return err
	}
	e.check(slices.Equal(before, after), "the source profile is untouched", "the source profile changed")
	e.check(strings.Contains(readTrim(filepath.Join(dst, "root-overlay/etc/os-release")), "Example Desk"),
		"overlay file replaced the profile's", "overlay file not applied")
	e.check(isFile(filepath.Join(dst, "grub/theme/theme.txt")), "overlay added the live GRUB theme (grub/theme/)", "grub/theme not staged")
	e.check(!exists(filepath.Join(dst, "root-overlay/usr/share/wallpapers/River")), "remove list applied", "remove list not applied")
	e.check(isDir(filepath.Join(dst, "root-overlay/usr/share/wallpapers")), "remove list deletes only what it names", "remove list deleted too much")
	e.expect("the staged profile finds itself among its edition descriptors", e.fn("river_profile_self_edition", dst, "EXAMPLE_DESK"))
	e.refuse("the unbranded source does not (its descriptor still says RIVER)", e.fn("river_profile_self_edition", src, "EXAMPLE_DESK"))
	e.expect("restaging replaces the previous copy", e.fn("river_profile_stage", src, "", dst))
	e.check(!isFile(filepath.Join(dst, "grub/theme/theme.txt")), "no branding left over from the previous staging", "stale branding survived a restage")
	remove := filepath.Join(br, "remove")
	for _, r := range []struct{ entry, d string }{
		{"root-overlay/does/not/exist", "a stale remove entry is refused"},
		{"../../etc", "a remove entry with .. is refused"},
		{"/etc", "an absolute remove entry is refused"},
	} {
		if err := os.WriteFile(remove, []byte(r.entry+"\n"), 0o644); err != nil {
			return err
		}
		e.refuse(r.d, e.fn("river_profile_stage", src, br, dst))
	}
	if err := os.Remove(remove); err != nil {
		return err
	}
	noOverlay := filepath.Join(e.t, "nooverlay")
	if err := os.MkdirAll(noOverlay, 0o755); err != nil {
		return err
	}
	e.refuse("a branding dir without overlay/ is refused", e.fn("river_profile_stage", src, noOverlay, dst))

	fmt.Fprintln(outw, "== one medium, several editions (EDITION, river.edition=)")
	if err := e.refuseBad("EDITION with upper case refused", src, "EDITION=Desk"); err != nil {
		return err
	}
	if err := e.refuseBad("EDITION starting with a digit refused", src, "EDITION=1desk"); err != nil {
		return err
	}
	if err := e.bad(src, "EDITION=desk-2"); err != nil {
		return err
	}
	if _, err := e.read(filepath.Join(e.t, "bad"), "bad"); err == nil && e.edition == "desk-2" {
		c.ok("EDITION read")
	} else {
		c.ko("EDITION not read (got '" + e.edition + "')")
	}
	m := filepath.Join(e.t, "medium")
	if err := os.RemoveAll(m); err != nil {
		return err
	}
	if err := copyTree(dst, m); err != nil {
		return err
	}
	ed := filepath.Join(m, edRel)
	// A second edition on the same medium (a downstream's, say): its own id.
	rj, err := os.ReadFile(filepath.Join(ed, "river.json"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(ed, "desk.json"), []byte(sedFirst(string(rj), `"id": *"river"`, `"id": "desk"`)), 0o644); err != nil {
		return err
	}
	jsons, err := filepath.Glob(filepath.Join(ed, "*.json"))
	if err != nil {
		return err
	}
	for _, f := range jsons {
		if err := editFile(f, func(s string) string {
			return sedFirst(s, `"medium_label": *"[A-Z0-9_]*"`, `"medium_label": "EXAMPLE"`)
		}); err != nil {
			return err
		}
	}
	e.refuse("two descriptors with one label and no EDITION: refused", e.fn("river_profile_self_edition", m, "EXAMPLE"))
	e.refuse("EDITION=river while the menu does not pass river.edition: refused", e.fn("river_profile_self_edition", m, "EXAMPLE", "river"))
	kernels := filepath.Join(m, "grub/kernels.cfg")
	if err := editFile(kernels, func(s string) string {
		return sedFirst(s, `^([[:space:]]*linux[[:space:]].*)$`, `$1 river.edition=river`)
	}); err != nil {
		return err
	}
	e.expect("EDITION=river with river.edition=river on every entry", e.fn("river_profile_self_edition", m, "EXAMPLE", "river"))
	e.refuse("EDITION=other (no descriptor with that id): refused", e.fn("river_profile_self_edition", m, "EXAMPLE", "other"))
	if err := appendFile(kernels, "menuentry \"other\" {\n\tlinux /boot/vmlinuz-x86_64 river.edition=desk\n}\n"); err != nil {
		return err
	}
	e.refuse("one entry passes another edition: refused", e.fn("river_profile_self_edition", m, "EXAMPLE", "river"))
	if err := os.Remove(kernels); err != nil {
		return err
	}
	e.refuse("EDITION without grub/kernels.cfg: refused", e.fn("river_profile_self_edition", m, "EXAMPLE", "river"))

	fmt.Fprintln(outw, "== the profile lints accept the staged copy")
	e.expect("staged", e.fn("river_profile_stage", src, br, dst))
	e.expect("lint-installer-sync on the staged copy", e.riverLint("installer-sync", dst))
	e.expect("lint-profile-manifest on the staged copy", e.riverLint("profile-manifest", dst))
	steps, err := os.ReadDir(filepath.Join(dst, "root-overlay/usr/local/lib/runink-install"))
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		return fmt.Errorf("no installer steps in the staged copy")
	}
	if err := appendFile(filepath.Join(dst, "root-overlay/usr/local/lib/runink-install", steps[0].Name()), "# drift\n"); err != nil {
		return err
	}
	e.refuse("lint-installer-sync catches a drifted step in the staged copy", e.riverLint("installer-sync", dst))
	// A downstream SERVER profile (none is in this tree any more): a minimal one, with its own
	// common.yaml, as artools reads it.
	s := filepath.Join(e.t, "downstream/profiles/example-srv")
	if err := os.MkdirAll(s, 0o755); err != nil {
		return err
	}
	for name, body := range map[string]string{
		"river-profile.env": "KIND=server\nISO_LABEL=EXAMPLE_SRV\nISO_NAME=example-srv\nGRUB_TITLE=Example Server\n",
		"profile.yaml":      "rootfs:\n  packages:\n    - openssh\n    - nftables\n  packages-init:\n    s6:\n      - openssh-s6\n",
		"common.yaml":       "packages-base:\n  - base\n  - linux-runink\npackages-init:\n  s6:\n    - s6\n",
		"Packages-Root":     "base\nlinux-runink\ns6\nopenssh\nnftables\nopenssh-s6\n",
	} {
		if err := os.WriteFile(filepath.Join(s, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	e.expect("lint-profile-manifest on an external server profile", e.riverLint("profile-manifest", s))
	if err := os.Remove(filepath.Join(s, "common.yaml")); err != nil {
		return err
	}
	e.refuse("lint-profile-manifest refuses an external server profile without common.yaml", e.riverLint("profile-manifest", s))
	return nil
}

// sedFirst is `sed 's/RE/REPL/'` (extended syntax, Go's $1 for \1): the first match on each
// line replaced.
func sedFirst(s, re, repl string) string {
	rx := regexp.MustCompile(re)
	parts := strings.SplitAfter(s, "\n")
	for i, p := range parts {
		line := strings.TrimSuffix(p, "\n")
		loc := rx.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		var out []byte
		out = rx.ExpandString(out, repl, line, loc)
		parts[i] = line[:loc[0]] + string(out) + line[loc[1]:] + p[len(line):]
	}
	return strings.Join(parts, "")
}

// editFile is `sed -i`: the file rewritten through f, its mode kept.
func editFile(path string, f func(string) string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(f(string(b))), st.Mode().Perm())
}
