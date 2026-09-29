// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// branding-sync — the Runink River artwork lives once under branding/ and is COPIED into the
// profile overlay by branding/render.sh, because buildiso copies overlays verbatim and the
// live ISO's GRUB theme is copied from /os/branding directly (scripts/patch-artools.go).
// That is three copies of the GRUB theme; a hand edit to one of them is exactly how the live
// menu and the installed menu would drift apart. This asserts every shipped copy is
// byte-identical to its branding/ original.
//
// FIX A FAILURE by editing under branding/ and re-running branding/render.sh — never by
// editing an overlay copy.
//
// Its messages go to standard output, as the script's did.

const brandOverlay = "iso-profiles/river/root-overlay"

func init() { extraLints = append(extraLints, brandingSyncCmd) }

func brandingSyncCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "branding-sync",
		Short: "every shipped branding copy matches branding/, the mark and palette hold, sizes in budget",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return BrandingSync(cmd.Context(), v.GetString("repo"), cmd.OutOrStdout())
		},
	}
}

// brandDiff is `diff -r -q a b`: one line per difference, in diff's words, or an error line.
func brandDiff(repo, a, b string) []string {
	var out []string
	var walk func(a, b string)
	walk = func(a, b string) {
		names := map[string]bool{}
		ea, errA := os.ReadDir(filepath.Join(repo, a))
		eb, errB := os.ReadDir(filepath.Join(repo, b))
		if errA != nil || errB != nil {
			out = append(out, fmt.Sprintf("diff: cannot read %s or %s", a, b))
			return
		}
		for _, e := range ea {
			names[e.Name()] = true
		}
		for _, e := range eb {
			names[e.Name()] = true
		}
		sorted := make([]string, 0, len(names))
		for n := range names {
			sorted = append(sorted, n)
		}
		slices.Sort(sorted)
		for _, n := range sorted {
			pa, pb := a+"/"+n, b+"/"+n
			sa, errA := os.Stat(filepath.Join(repo, pa))
			sb, errB := os.Stat(filepath.Join(repo, pb))
			switch {
			case errA != nil && errB != nil:
			case errB != nil:
				out = append(out, fmt.Sprintf("Only in %s: %s", a, n))
			case errA != nil:
				out = append(out, fmt.Sprintf("Only in %s: %s", b, n))
			case sa.IsDir() && sb.IsDir():
				walk(pa, pb)
			case sa.IsDir() != sb.IsDir():
				kind := func(s fs.FileInfo) string {
					if s.IsDir() {
						return "directory"
					}
					return "regular file"
				}
				out = append(out, fmt.Sprintf("File %s is a %s while file %s is a %s", pa, kind(sa), pb, kind(sb)))
			default:
				if !brandSameFile(filepath.Join(repo, pa), filepath.Join(repo, pb)) {
					out = append(out, fmt.Sprintf("Files %s and %s differ", pa, pb))
				}
			}
		}
	}
	walk(a, b)
	return out
}

// brandSameFile is `cmp -s a b`.
func brandSameFile(a, b string) bool {
	x, errA := os.ReadFile(a)
	y, errB := os.ReadFile(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}

// brandFiles lists the regular files under the given repo-relative roots (a missing root is
// skipped, as `find … 2>/dev/null` did), as repo-relative slash paths.
func brandFiles(repo string, roots ...string) []string {
	var out []string
	for _, root := range roots {
		_ = filepath.WalkDir(filepath.Join(repo, root), func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(repo, p)
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
	}
	return out
}

func brandSize(repo, f string) int64 {
	st, err := os.Stat(filepath.Join(repo, f))
	if err != nil {
		return 0
	}
	return st.Size()
}

// brandSedAll is `sed -n 's/…\(x\)…/\1/p'`: the first capture of re on every line that matches.
func brandSedAll(b []byte, re *regexp.Regexp) string {
	var out []string
	for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if m := re.FindStringSubmatch(l); m != nil {
			out = append(out, m[1])
		}
	}
	return strings.Join(out, "\n")
}

// brandLineMatch reports whether any line of the file matches re (grep -q); a missing file
// matches nothing.
func brandLineMatch(path string, re *regexp.Regexp) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// brandSha256Check is `sha256sum --quiet --strict -c SUMS` run in dir: the failures, in
// sha256sum's words, and whether every line checked.
func brandSha256Check(dir string) ([]string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return []string{"sha256sum: SHA256SUMS: No such file or directory"}, false
	}
	var out []string
	ok, n := true, 0
	line := regexp.MustCompile(`^([0-9a-fA-F]{64}) [ *](.+)$`)
	for _, l := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if strings.HasPrefix(l, "#") {
			continue
		}
		m := line.FindStringSubmatch(l)
		if m == nil {
			out = append(out, "sha256sum: SHA256SUMS: improperly formatted SHA256 checksum line")
			ok = false
			continue
		}
		n++
		f, err := os.ReadFile(filepath.Join(dir, m[2]))
		if err != nil {
			out = append(out, m[2]+": FAILED open or read")
			ok = false
			continue
		}
		sum := sha256.Sum256(f)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), m[1]) {
			out = append(out, m[2]+": FAILED")
			ok = false
		}
	}
	if n == 0 {
		out = append(out, "sha256sum: SHA256SUMS: no properly formatted checksum lines found")
		ok = false
	}
	return out, ok
}

var (
	brandSourceRe  = regexp.MustCompile(`.*"source": *"([^"]*)".*`)
	brandIconRe    = regexp.MustCompile(`.*"icon": *"([^"]*)".*`)
	brandNetModRe  = regexp.MustCompile(`(?i)"(publicip|weather|dns|netio)"|"type": *"(publicip|weather|netio)"`)
	brandRetiredRe = regexp.MustCompile(`(?i)#(D9764E|C4623A|FBF7F1|1A1614|241F1B|EDE2D3|3F382D|ABA397|C2BBB0)`)
	brandImportsOK = regexp.MustCompile(`^import (QtQuick|QtQml|QtQuick\.Controls\.Basic)$`)
)

const (
	brandMaxAsset     = 1048576
	brandMaxWallpaper = 6291456
)

// BrandingSync runs the lint over repo. Everything it says goes to w, as the script's did.
func BrandingSync(_ context.Context, repo string, w io.Writer) error {
	r := &report{name: "lint-branding-sync", w: w}
	O := brandOverlay
	at := func(p string) string { return filepath.Join(repo, p) }
	isFile := func(p string) bool { st, err := os.Stat(at(p)); return err == nil && st.Mode().IsRegular() }
	isDir := func(p string) bool { st, err := os.Stat(at(p)); return err == nil && st.IsDir() }
	indent := func(lines []string) {
		for _, l := range lines {
			fmt.Fprintf(w, "  %s\n", l)
		}
	}

	checked := 0
	same := func(src, dst string) {
		if !isDir(dst) {
			r.fail("MISSING: %s (copy of %s)", dst, src)
			return
		}
		if d := brandDiff(repo, src, dst); len(d) > 0 {
			r.fail("DRIFT: %s != %s", dst, src)
			indent(d)
		}
		checked++
	}
	same("branding/grub/river", O+"/usr/share/runink/branding/grub/river")
	same("branding/plymouth/river", O+"/usr/share/plymouth/themes/river")
	same("branding/splash", O+"/usr/share/plasma/look-and-feel/org.runink.river.desktop/contents/splash")
	same("branding/sddm/runink-river", O+"/usr/share/sddm/themes/runink-river")
	same("branding/icons/hicolor", O+"/usr/share/icons/hicolor")
	same("branding/wallpapers/River", O+"/usr/share/wallpapers/River")
	same("branding/fastfetch", O+"/usr/share/runink/fastfetch")

	// The terminal greeting (etc/fish/conf.d/runink-greeting.fish): six frames of 22 rows, the
	// last one the logo fastfetch prints (so it draws over the animation with no jump), and the
	// greeting and fastfetch's config name files the overlay ships.
	frames, _ := filepath.Glob(at("branding/fastfetch/anim/*.ansi"))
	n := 0
	for _, f := range frames {
		rel := "branding/fastfetch/anim/" + filepath.Base(f)
		if !isFile(rel) {
			continue
		}
		n++
		b, _ := os.ReadFile(f)
		if bytes.Count(b, []byte("\n")) != 22 {
			r.fail("%s is not 22 rows", rel)
		}
	}
	if n != 6 {
		r.fail("the greeting has %d frames, want 6", n)
	}
	if !brandSameFile(at("branding/fastfetch/anim/6.ansi"), at("branding/fastfetch/river-mark.ansi")) {
		r.fail("the greeting's last frame is not the fastfetch logo")
	}
	if !brandLineMatch(at(O+"/etc/fish/conf.d/runink-greeting.fish"), regexp.MustCompile(`/usr/share/runink/fastfetch/anim/\*\.ansi`)) {
		r.fail("runink-greeting.fish does not play /usr/share/runink/fastfetch/anim/")
	}
	ffcfg, _ := os.ReadFile(at(O + "/etc/xdg/fastfetch/config.jsonc"))
	if src := brandSedAll(ffcfg, brandSourceRe); src == "" || !isFile(O+src) {
		r.fail("fastfetch's logo '%s' is not in the overlay", src)
	}
	// No module that reaches the network (invariant 5: no telemetry, no phone-home).
	if brandLineMatch(at(O+"/etc/xdg/fastfetch/config.jsonc"), brandNetModRe) {
		r.fail("fastfetch's config has a network module")
	}

	// Third-party wallpapers (CachyOS Emerald, GPL-3.0): the vendored JPEGs must match the
	// SHA256SUMS derive.sh wrote (UPSTREAM.sha256 pins what they were derived from), and every
	// package must be shipped unmodified.
	if bad, ok := brandSha256Check(at("branding/wallpapers/emerald")); !ok {
		for _, l := range bad {
			fmt.Fprintln(w, l)
		}
		r.fail("branding/wallpapers/emerald does not match its SHA256SUMS")
	}
	emerald, _ := os.ReadDir(at("branding/wallpapers/emerald"))
	for _, e := range emerald {
		if isDir("branding/wallpapers/emerald/" + e.Name()) {
			same("branding/wallpapers/emerald/"+e.Name(), O+"/usr/share/wallpapers/"+e.Name())
		}
	}

	// The community mark is the M2 mascot: branding/logo/river-mark.svg and
	// river-mark-small.svg are GENERATED from branding/mascot/river-mascot.svg, and each records
	// the mascot's sha256, which the lockups carry along (they embed the mark). A mascot edit
	// without a re-render, or a hand-drawn mark, fails here.
	mascotSum := ""
	if b, err := os.ReadFile(at("branding/mascot/river-mascot.svg")); err == nil {
		s := sha256.Sum256(b)
		mascotSum = hex.EncodeToString(s[:])
	} else {
		r.fail("MISSING: branding/mascot/river-mascot.svg (the mark's one source)")
	}
	for _, f := range []string{"branding/logo/river-mark.svg", "branding/logo/river-mark-small.svg", "branding/logo/river-lockup.svg",
		"branding/logo/river-lockup-dark.svg", O + "/usr/share/icons/hicolor/scalable/apps/runink-river.svg"} {
		if mascotSum == "" || !brandLineMatch(at(f), regexp.MustCompile(`mascot-sha256 `+mascotSum+`$`)) {
			r.fail("%s does not draw the current branding/mascot/river-mascot.svg (re-run branding/render.sh; for the dark lockup, its outline mode)", f)
		}
	}

	// Every visual surface carries the Runink River community mark (branding/render.sh): the
	// places that NAME an icon or an image must name the ones render.sh ships.
	if !brandLineMatch(at(O+"/etc/os-release"), regexp.MustCompile(`^LOGO=runink-river$`)) {
		r.fail("os-release LOGO is not runink-river")
	}
	if !brandLineMatch(at(O+"/usr/share/plasma/look-and-feel/org.runink.river.desktop/contents/layouts/org.kde.plasma.desktop-layout.js"), regexp.MustCompile(`"runink-river"`)) {
		r.fail("the Kickoff button does not show runink-river")
	}
	// SDDM: the image selects the Runink River greeter theme, which is a Qt 6 theme that draws
	// the animated community mark (all five layers of river-mark.svg, the M2 mascot) and imports
	// nothing beyond QtQuick and QtQuick.Controls.Basic (qt6-declarative, a dependency of sddm;
	// a Plasma or Kirigami import could fail in the greeter). The Plasma splash draws the same
	// mark.
	sddm := O + "/usr/share/sddm/themes/runink-river"
	splash := O + "/usr/share/plasma/look-and-feel/org.runink.river.desktop/contents/splash"
	if !brandLineMatch(at(O+"/etc/sddm.conf.d/10-river.conf"), regexp.MustCompile(`^Current=runink-river$`)) {
		r.fail("etc/sddm.conf.d/10-river.conf does not select Current=runink-river")
	}
	if !brandLineMatch(at(sddm+"/metadata.desktop"), regexp.MustCompile(`^QtVersion=6$`)) {
		r.fail("the SDDM theme's metadata.desktop does not say QtVersion=6")
	}
	for _, d := range []string{sddm, splash} {
		for _, l := range []string{"back", "head", "front", "water", "waves"} {
			if brandSize(repo, d+"/images/mark-"+l+".svg") == 0 {
				r.fail("%s lacks the mark layer images/mark-%s.svg", d, l)
			}
		}
		if !isFile(d + "/RiverLockup.qml") {
			r.fail("%s lacks RiverLockup.qml (the animated mark)", d)
		}
	}
	nqml := 0
	_ = filepath.WalkDir(at(sddm), func(p string, _ fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(filepath.Base(p), ".qml") {
			nqml++
		}
		return nil
	})
	if nqml <= 3 {
		r.fail("the SDDM theme has only %d QML files — its path is stale", nqml)
	}
	imports := map[string]bool{}
	for _, d := range []string{sddm, splash} {
		qmls, _ := filepath.Glob(at(d + "/*.qml"))
		for _, q := range qmls {
			b, _ := os.ReadFile(q)
			for _, l := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(l, "import ") {
					if i := strings.Index(l, " as "); i >= 0 {
						l = l[:i]
					}
					if !brandImportsOK.MatchString(l) {
						imports[l] = true
					}
				}
			}
		}
	}
	if len(imports) > 0 {
		bad := make([]string, 0, len(imports))
		for l := range imports {
			bad = append(bad, l)
		}
		slices.Sort(bad)
		r.fail("the greeter or splash imports a module outside the allow-list:")
		indent(bad)
	}
	// The QML the render copies must be the hand-written sources, unmodified.
	srcs, _ := filepath.Glob(at("branding/src/sddm/runink-river/*"))
	for _, f := range srcs {
		b := filepath.Base(f)
		if !brandSameFile(f, at("branding/sddm/runink-river/"+b)) {
			r.fail("branding/sddm/runink-river/%s != branding/src/sddm/runink-river/%s (re-run branding/render.sh)", b, b)
		}
	}
	for _, p := range []string{"branding/sddm/runink-river", "branding/splash"} {
		if !brandSameFile(at("branding/src/qml/RiverLockup.qml"), at(p+"/RiverLockup.qml")) {
			r.fail("%s/RiverLockup.qml != branding/src/qml/RiverLockup.qml", p)
		}
	}
	if !brandSameFile(at("branding/src/splash/Splash.qml"), at("branding/splash/Splash.qml")) {
		r.fail("branding/splash/Splash.qml != branding/src/splash/Splash.qml")
	}
	eds, _ := filepath.Glob(at("iso-profiles/river/live-overlay/usr/share/river/installer/editions/*.json"))
	for _, e := range eds {
		b, _ := os.ReadFile(e)
		if ic := brandSedAll(b, brandIconRe); ic != "" && !isFile(O+ic) {
			r.fail("iso-profiles/river/live-overlay/usr/share/river/installer/editions/%s names icon %s, which the overlay does not ship", filepath.Base(e), ic)
		}
	}

	// The palette (branding/palette.md): the warm palette it replaced is retired everywhere the
	// image or the installer draws a colour, and the lockups every surface draws are outlined
	// (no live text, so no render depends on an installed font).
	paletteRoots := []string{"branding/src", "branding/logo", "branding/grub", "branding/plymouth", O, "installer/internal/wizard/web"}
	scanned := brandFiles(repo, paletteRoots...)
	if len(scanned) <= 20 {
		r.fail("palette scan found only %d files — its paths are stale", len(scanned))
	}
	var old []string
	for _, f := range scanned {
		b, err := os.ReadFile(at(f))
		if err != nil || bytes.IndexByte(b, 0) >= 0 { // -I: binary files are skipped
			continue
		}
		if brandRetiredRe.Match(b) {
			old = append(old, f)
		}
	}
	if len(old) > 0 {
		r.fail("retired palette colours in:")
		indent(old)
	}
	for _, f := range []string{"branding/logo/river-lockup-dark.svg", "branding/logo/runink-tagline.svg"} {
		b, err := os.ReadFile(at(f))
		if err != nil {
			r.fail("MISSING: %s (branding/render.sh, outline mode)", f)
			continue
		}
		if bytes.Contains(b, []byte("<text")) {
			r.fail("%s has live text; re-run branding/render.sh in outline mode", f)
		}
	}

	// Size budgets, so the artwork stays small: no single asset over 1 MiB, the wallpapers
	// (Runink River + Emerald) under 6 MiB in total, and ONE resolution per River variant.
	var big []string
	for _, f := range brandFiles(repo, "branding", O+"/usr/share/runink/branding", O+"/usr/share/wallpapers", O+"/usr/share/icons",
		O+"/usr/share/plymouth", O+"/usr/share/plasma") {
		if brandSize(repo, f) > brandMaxAsset {
			big = append(big, f)
		}
	}
	if len(big) > 0 {
		r.fail("asset(s) over %d bytes:", brandMaxAsset)
		indent(big)
	}
	var wpTotal int64
	for _, f := range brandFiles(repo, O+"/usr/share/wallpapers") {
		wpTotal += brandSize(repo, f)
	}
	if wpTotal > brandMaxWallpaper {
		r.fail("wallpapers total %d bytes (> %d)", wpTotal, brandMaxWallpaper)
	}
	for _, v := range []string{"images", "images_dark"} {
		n := 0
		for _, f := range brandFiles(repo, O+"/usr/share/wallpapers/River/contents/"+v) {
			if strings.HasSuffix(f, ".jpg") {
				n++
			}
		}
		if n != 1 {
			r.fail("River/contents/%s holds %d JPEGs (want exactly one resolution)", v, n)
		}
	}

	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(w, "lint-branding-sync: %d shipped branding copies match branding/, every surface names the community mark, no retired colour, sizes in budget (wallpapers %d bytes) ✓\n", checked, wpTotal)
	return nil
}
