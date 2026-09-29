// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bufio"
	"context"
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

// profile-manifest — the ISO must install exactly the allow-list.
//
// `river lint closure` checks the dependency closure of Packages-Root. But buildiso never
// reads Packages-Root: it installs profile.yaml's lists plus common.yaml's. The first server
// ISO (2026-09-25) showed what that gap costs: artools' stock common.yaml added 30-odd
// desktop packages (ModemManager, avahi, bluez, lvm2, mdadm, zsh, memtest86+, ...) that no
// lint saw, while bubblewrap, cpupower and runink-tayga, which Packages-Root lists, were never
// installed at all.
//
// For every profile that carries its own common.yaml (a downstream server profile does), this
// checks, as SETS:
//
//	rootfs  = common.yaml packages-base + packages-init.s6 + packages-apps
//	          + profile.yaml rootfs.packages + rootfs.packages-init.s6   == Packages-Root
//	livefs  = profile.yaml livefs.packages                                == Packages-Live
//	common.yaml packages-xorg, packages-xlibre, packages-misc            are empty
//
// and that a server profile has a common.yaml at all (without one, artools falls back to its
// own list and every check above would pass having examined nothing).
//
// For a profile WITHOUT its own common.yaml (Runink River, iso-profiles/river, keeps artools'
// desktop common.yaml for the Plasma base): every package its profile.yaml installs is in
// Packages-Root (rootfs ⊆ Packages-Root; what artools' common.yaml adds is not checked here).
//
// For EVERY profile: livefs == Packages-Live, a live-overlay/ comes with a `livefs:` key (artools
// ignores the overlay otherwise), and no list installs Artix's live-session setup
// (artix-live-*), which would reach installed machines through the clone.
//
// Usage: river lint profile-manifest [profile-dir ...]   (default: every profile under
// iso-profiles/; a relative profile-dir is relative to --repo)

func init() { extraLints = append(extraLints, profileManifestCmd) }

func profileManifestCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "profile-manifest [profile-dir ...]",
		Short: "a profile installs exactly its allow-list (Packages-Root, Packages-Live)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ProfileManifest(cmd.Context(), v.GetString("repo"), args, cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

// pmItem is one "- item" of a YAML block list: path is the chain of parent keys joined by "/".
type pmItem struct{ path, item string }

var (
	pmSkipRe    = regexp.MustCompile(`^[[:space:]]*(#|$)`)
	pmCommentRe = regexp.MustCompile(`[[:space:]]+#.*$`)
	pmKeyRe     = regexp.MustCompile(`^[A-Za-z0-9_.-]+:`)
	pmArtixRe   = regexp.MustCompile(`^artix-live(-[a-z0-9]+)?$`)
)

// pmYAMLLists reads the block lists of an artools profile file (block lists of scalars,
// comments, `key: []`); anything fancier is returned as a parse error rather than guessed.
func pmYAMLLists(name string, b []byte) (items []pmItem, perr []string) {
	key := map[int]string{}
	for _, raw := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		if pmSkipRe.MatchString(raw) || strings.HasPrefix(raw, "---") {
			continue
		}
		ind := len(raw) - len(strings.TrimLeft(raw, " "))
		line := pmCommentRe.ReplaceAllString(raw[ind:], "")
		switch {
		case strings.HasPrefix(line, "- "):
			var parts []string
			for i := 0; i <= ind; i++ {
				if k, ok := key[i]; ok {
					parts = append(parts, k)
				}
			}
			items = append(items, pmItem{strings.Join(parts, "/"), strings.TrimSpace(line[2:])})
		case pmKeyRe.MatchString(line):
			for i := range key {
				if i >= ind {
					delete(key, i)
				}
			}
			key[ind] = line[:strings.Index(line, ":")]
		default:
			perr = append(perr, name+": "+raw)
		}
	}
	return items, perr
}

// pmListOf returns the items under exactly the given paths, in file order.
func pmListOf(items []pmItem, paths ...string) []string {
	var out []string
	for _, it := range items {
		if slices.Contains(paths, it.path) {
			out = append(out, it.item)
		}
	}
	return out
}

// pmManifest returns the package names of a Packages-* file (comments and blanks dropped).
func pmManifest(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = l[:i]
		}
		out = append(out, strings.Fields(l)...)
	}
	return out
}

func pmSet(xs []string) []string {
	s := slices.Clone(xs)
	slices.Sort(s)
	return slices.Compact(s)
}

// pmMinus returns the sorted members of a not in b (both sorted, unique), `comm -23 a b`.
func pmMinus(a, b []string) []string {
	var out []string
	for _, x := range a {
		if _, found := slices.BinarySearch(b, x); !found {
			out = append(out, x)
		}
	}
	return out
}

// pmWords is `tr '\n' ' '` over a list: each word followed by a space.
func pmWords(xs []string) string {
	var sb strings.Builder
	for _, x := range xs {
		sb.WriteString(x)
		sb.WriteByte(' ')
	}
	return sb.String()
}

// ProfileManifest runs the lint over the named profile directories, or with none over every
// profile under repo's iso-profiles/.
func ProfileManifest(_ context.Context, repo string, dirs []string, errw, outw io.Writer) error {
	r := &report{name: "lint-profile-manifest", w: errw}

	// With no argument every in-tree profile is checked. With arguments (build/local-iso.sh
	// passes a staged external profile), each named profile is checked, and one that is a
	// server by its river-profile.env must carry one.
	defaultSet := len(dirs) == 0
	if defaultSet {
		m, _ := filepath.Glob(filepath.Join(repo, "iso-profiles", "*"))
		for _, d := range m {
			if st, err := os.Stat(d); err == nil && st.IsDir() {
				dirs = append(dirs, "iso-profiles/"+filepath.Base(d))
			}
		}
	}

	// compare: set equality, with both directions reported.
	compare := func(name string, want, got []string) {
		w, g := pmSet(want), pmSet(got)
		if missing := pmMinus(w, g); len(missing) > 0 {
			r.fail("%s: in the allow-list but not installed by the profile: %s", name, pmWords(missing))
		}
		if extra := pmMinus(g, w); len(extra) > 0 {
			r.fail("%s: installed by the profile but not in the allow-list: %s", name, pmWords(extra))
		}
		if len(w) == 0 {
			r.fail("%s: the allow-list is empty (checked nothing)", name)
		}
	}
	parse := func(d, shown, file string) []pmItem {
		b, _ := os.ReadFile(filepath.Join(d, file))
		items, perr := pmYAMLLists(shown+"/"+file, b)
		if len(perr) > 0 {
			r.fail("cannot parse %s/%s:", shown, file)
			for _, e := range perr {
				fmt.Fprintln(errw, e)
			}
		}
		return items
	}

	checked, subset, liveChecked := 0, 0, 0
	for _, d := range dirs {
		d = strings.TrimSuffix(d, "/")
		shown := d // messages name the profile as given, as the script did
		if !filepath.IsAbs(d) {
			d = filepath.Join(repo, d)
		}
		pyb, err := os.ReadFile(filepath.Join(d, "profile.yaml"))
		if err != nil {
			continue
		}
		p := filepath.Base(d)
		liveChecked++
		prof := parse(d, shown, "profile.yaml")

		// EVERY profile: the live layer. artools applies live-overlay/ only to a livefs layer, so
		// a profile with a live-overlay/ and no `livefs:` key ships an ISO without it. Both the
		// server (2026-09-25: no guide, no tty1 override) and the workstation (its live
		// hostname, never applied) did exactly that.
		if st, err := os.Stat(filepath.Join(d, "live-overlay")); err == nil && st.IsDir() &&
			!regexp.MustCompile(`(?m)^livefs:`).Match(pyb) {
			r.fail("%s: has live-overlay/ but profile.yaml has no livefs: key, so artools never copies it", p)
		}
		livefs := pmListOf(prof, "livefs/packages")
		if lb, err := os.ReadFile(filepath.Join(d, "Packages-Live")); err == nil {
			compare(p+" livefs", pmManifest(lb), livefs)
		} else if len(livefs) > 0 {
			r.fail("%s: profile.yaml has livefs packages but there is no Packages-Live", p)
		}
		// No Artix live-session setup on any Runink River medium (see the header of
		// iso-profiles/river/profile.yaml): the live session is the profile's own
		// live-overlay/, and nothing Artix-live can then follow a clone onto an installed
		// machine.
		var art []string
		for _, it := range prof {
			if pmArtixRe.MatchString(it.item) {
				art = append(art, it.item)
			}
		}
		if len(art) > 0 {
			r.fail("%s: profile.yaml installs Artix's live-session setup: %s", p, pmWords(art))
		}

		rootb, _ := os.ReadFile(filepath.Join(d, "Packages-Root"))
		// Profiles with their own common.yaml (the server): the rootfs is exactly Packages-Root.
		if _, err := os.Stat(filepath.Join(d, "common.yaml")); err != nil {
			if kind, ok := pmProfileKind(d, p); ok && kind == "server" {
				r.fail("%s: a server profile without its own common.yaml (artools would install its desktop list)", p)
			}
			// Everything profile.yaml installs is on the allow-list.
			subset++
			rootfs := pmSet(pmListOf(prof, "rootfs/packages", "rootfs/packages-init/s6"))
			if len(rootfs) == 0 {
				r.fail("%s: profile.yaml installs no rootfs package (checked nothing)", p)
			}
			if extra := pmMinus(rootfs, pmSet(pmManifest(rootb))); len(extra) > 0 {
				r.fail("%s rootfs: installed by profile.yaml but not in the allow-list (Packages-Root): %s", p, pmWords(extra))
			}
			continue
		}
		checked++
		common := parse(d, shown, "common.yaml")
		rootfs := append(pmListOf(common, "packages-base", "packages-init/s6", "packages-apps"),
			pmListOf(prof, "rootfs/packages", "rootfs/packages-init/s6")...)
		compare(p+" rootfs", pmManifest(rootb), rootfs)

		if desk := pmListOf(common, "packages-xorg", "packages-xlibre", "packages-misc"); len(desk) > 0 {
			r.fail("%s: common.yaml adds display/desktop packages: %s", p, pmWords(desk))
		}
		// profile.yaml carries no copy of the kernel line: common.yaml owns it.
		sorted := slices.Clone(rootfs)
		slices.Sort(sorted)
		var dups []string
		for i := 1; i < len(sorted); i++ {
			if sorted[i] == sorted[i-1] && (len(dups) == 0 || dups[len(dups)-1] != sorted[i]) {
				dups = append(dups, sorted[i])
			}
		}
		if len(dups) > 0 {
			r.fail("%s: listed twice across common.yaml and profile.yaml: %s", p, pmWords(dups))
		}
	}

	if liveChecked == 0 {
		r.fail("no profile.yaml was checked")
	}
	if defaultSet && checked+subset == 0 {
		r.fail("no profile's rootfs was checked")
	}
	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-profile-manifest: OK (%d profile(s): livefs == Packages-Live, live-overlay applied; %d with a common.yaml: rootfs == Packages-Root; %d on artools' common.yaml: rootfs ⊆ Packages-Root)\n", liveChecked, checked, subset)
	return nil
}

var (
	pmISOLabelRe = regexp.MustCompile(`^[A-Z0-9_]+$`)
	pmISONameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	pmTitleRe    = regexp.MustCompile(`^[A-Za-z0-9 ._+-]+$`)
	pmEditionRe  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// pmProfileKind is build/profile-lib.sh's river_profile_read, as far as this lint needs it:
// the profile's KIND, and whether its description is valid (ok false where the shell function
// returned 1, whose message the script discarded).
func pmProfileKind(dir, name string) (kind string, ok bool) {
	var label, isoName, title, edition string
	if name == "river" {
		kind, label, isoName, title = "workstation", "RIVER", "runink-river", "Runink River"
	}
	f, err := os.Open(filepath.Join(dir, "river-profile.env"))
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			l := sc.Text()
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			k, val, _ := strings.Cut(l, "=")
			switch k {
			case "KIND":
				kind = val
			case "ISO_LABEL":
				label = val
			case "ISO_NAME":
				isoName = val
			case "GRUB_TITLE":
				title = val
			case "EDITION":
				edition = val
			default:
				return kind, false
			}
		}
	} else if kind == "" {
		return "", false
	}
	switch {
	case kind != "server" && kind != "workstation",
		!pmISOLabelRe.MatchString(label) || len(label) > 32,
		!pmISONameRe.MatchString(isoName),
		!pmTitleRe.MatchString(title) || len(title) > 60,
		edition != "" && (!pmEditionRe.MatchString(edition) || len(edition) > 32):
		return kind, false
	}
	return kind, true
}
