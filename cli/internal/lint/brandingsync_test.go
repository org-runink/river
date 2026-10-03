// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// brandFixture is the smallest tree that passes: every branding/ source with its overlay
// copy, the greeting frames, the mark, the SDDM theme and the splash, the palette and budgets.
func brandFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	w := func(name, body string) { lbWrite(t, dir, name, body) }
	O := brandOverlay + "/"

	mascot := "<svg>mascot</svg>\n"
	s := sha256.Sum256([]byte(mascot))
	mark := "<svg><!-- mascot-sha256 " + hex.EncodeToString(s[:]) + "\n--></svg>\n"
	w("branding/mascot/river-mascot.svg", mascot)
	for _, f := range []string{"river-mark.svg", "river-mark-small.svg", "river-lockup.svg", "river-lockup-dark.svg"} {
		w("branding/logo/"+f, mark)
	}
	w("branding/logo/runink-tagline.svg", "<svg><path d='M0 0'/></svg>\n")
	w("branding/icons/hicolor/scalable/apps/runink-river.svg", mark)

	w("branding/grub/river/theme.txt", "title-text: \"\"\n")
	w("branding/plymouth/river/river.plymouth", "[Plymouth Theme]\n")
	w("branding/wallpapers/River/contents/images/1920x1080.jpg", "jpeg")
	w("branding/wallpapers/River/contents/images_dark/1920x1080.jpg", "jpeg")
	rows := strings.Repeat("row\n", 22)
	w("branding/fastfetch/river-mark.ansi", rows)

	qml := "import QtQuick\nimport QtQuick.Controls.Basic as C\n"
	w("branding/src/qml/RiverLockup.qml", qml)
	w("branding/src/splash/Splash.qml", qml)
	w("branding/src/sddm/runink-river/Main.qml", qml)
	for _, d := range []string{"branding/sddm/runink-river/", "branding/splash/"} {
		w(d+"RiverLockup.qml", qml)
		for _, l := range []string{"back", "head", "front", "water", "waves"} {
			w(d+"images/mark-"+l+".svg", "<svg/>\n")
		}
	}
	w("branding/splash/Splash.qml", qml)
	w("branding/sddm/runink-river/Main.qml", qml)
	w("branding/sddm/runink-river/Login.qml", qml)
	w("branding/sddm/runink-river/Clock.qml", qml)
	w("branding/sddm/runink-river/metadata.desktop", "[SddmGreeterTheme]\nQtVersion=6\n")

	w("branding/wallpapers/emerald/Abstract/contents/images/960x540.jpg", "emerald")
	e := sha256.Sum256([]byte("emerald"))
	w("branding/wallpapers/emerald/SHA256SUMS", hex.EncodeToString(e[:])+"  Abstract/contents/images/960x540.jpg\n")

	for src, dst := range map[string]string{
		"branding/grub/river":                  O + "usr/share/runink/branding/grub/river",
		"branding/plymouth/river":              O + "usr/share/plymouth/themes/river",
		"branding/splash":                      O + "usr/share/plasma/look-and-feel/org.runink.river.desktop/contents/splash",
		"branding/sddm/runink-river":           O + "usr/share/sddm/themes/runink-river",
		"branding/icons/hicolor":               O + "usr/share/icons/hicolor",
		"branding/wallpapers/River":            O + "usr/share/wallpapers/River",
		"branding/fastfetch":                   O + "usr/share/runink/fastfetch",
		"branding/wallpapers/emerald/Abstract": O + "usr/share/wallpapers/Abstract",
	} {
		brandMirror(t, dir, src, dst)
	}
	w(O+"etc/profile.d/runink-greeting.sh", "case $- in *i*) ;; *) return 0 ;; esac\n[ \"${RUNINK_GREETING:-}\" = off ] && return 0\nfastfetch\n")
	w(O+"etc/xdg/fastfetch/config.jsonc", "{\"logo\": {\"source\": \"/usr/share/runink/fastfetch/river-mark.ansi\"},\n\"modules\": [\"os\", \"kernel\"]}\n")
	w(O+"etc/os-release", "NAME=\"Runink River\"\nLOGO=runink-river\n")
	w(O+"usr/share/plasma/look-and-feel/org.runink.river.desktop/contents/layouts/org.kde.plasma.desktop-layout.js", "icon = \"runink-river\";\n")
	w(O+"etc/sddm.conf.d/10-river.conf", "[Theme]\nCurrent=runink-river\n")
	w("iso-profiles/river/live-overlay/usr/share/river/installer/editions/river.json", "{\"icon\": \"/usr/share/icons/hicolor/scalable/apps/runink-river.svg\"}\n")
	w("installer/internal/wizard/web/style.css", "body { color: #E6E8E3; }\n")
	return dir
}

// brandMirror copies the tree src to dst inside dir, as branding/render.sh does.
func brandMirror(t *testing.T, dir, src, dst string) {
	t.Helper()
	root := filepath.Join(dir, src)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lbWrite(t, dir, filepath.Join(dst, rel), string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func brandRun(t *testing.T, dir string) (string, error) {
	t.Helper()
	var b bytes.Buffer
	err := BrandingSync(context.Background(), dir, &b)
	return b.String(), err
}

func TestBrandingSyncClean(t *testing.T) {
	out, err := brandRun(t, brandFixture(t))
	if err != nil {
		t.Fatalf("clean tree failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "lint-branding-sync: 8 shipped branding copies match branding/") {
		t.Fatalf("unexpected OK line: %q", out)
	}
}

func TestBrandingSyncFailures(t *testing.T) {
	O := brandOverlay + "/"
	splash := O + "usr/share/plasma/look-and-feel/org.runink.river.desktop/contents/splash"
	sddm := O + "usr/share/sddm/themes/runink-river"
	cases := []struct {
		name, want string
		mutate     func(t *testing.T, dir string)
	}{
		{"missing copy", "MISSING: " + O + "usr/share/plymouth/themes/river (copy of branding/plymouth/river)", func(t *testing.T, d string) {
			os.RemoveAll(filepath.Join(d, O+"usr/share/plymouth/themes/river"))
		}},
		{"drift", "DRIFT: " + O + "usr/share/runink/branding/grub/river != branding/grub/river\n  Files branding/grub/river/theme.txt and " + O + "usr/share/runink/branding/grub/river/theme.txt differ", func(t *testing.T, d string) {
			lbWrite(t, d, O+"usr/share/runink/branding/grub/river/theme.txt", "hand edit\n")
		}},
		{"drift: only in", "  Only in " + O + "usr/share/runink/branding/grub/river: extra.png", func(t *testing.T, d string) {
			lbWrite(t, d, O+"usr/share/runink/branding/grub/river/extra.png", "x")
		}},
		{"greeting missing", "etc/profile.d/runink-greeting.sh is missing", func(t *testing.T, d string) {
			if err := os.Remove(filepath.Join(d, O+"etc/profile.d/runink-greeting.sh")); err != nil {
				t.Fatal(err)
			}
		}},
		{"greeting not interactive-guarded", "does not guard on an interactive shell", func(t *testing.T, d string) {
			lbWrite(t, d, O+"etc/profile.d/runink-greeting.sh", "[ \"${RUNINK_GREETING:-}\" = off ] && return 0\nfastfetch\n")
		}},
		{"greeting ignores RUNINK_GREETING", "does not honour RUNINK_GREETING=off", func(t *testing.T, d string) {
			lbWrite(t, d, O+"etc/profile.d/runink-greeting.sh", "case $- in *i*) ;; *) return 0 ;; esac\nfastfetch\n")
		}},
		{"greeting still animates", "still plays an animation", func(t *testing.T, d string) {
			lbWrite(t, d, O+"etc/profile.d/runink-greeting.sh", "case $- in *i*) ;; *) return 0 ;; esac\n[ \"${RUNINK_GREETING:-}\" = off ] && return 0\nfor f in /usr/share/runink/fastfetch/anim/*.ansi; do :; done\n")
		}},
		{"animation frames returned", "branding/fastfetch/anim/ still exists", func(t *testing.T, d string) {
			lbWrite(t, d, "branding/fastfetch/anim/1.ansi", strings.Repeat("r\n", 22))
		}},
		{"fastfetch logo", "fastfetch's logo '/nowhere.ansi' is not in the overlay", func(t *testing.T, d string) {
			lbWrite(t, d, O+"etc/xdg/fastfetch/config.jsonc", "{\"source\": \"/nowhere.ansi\"}\n")
		}},
		{"network module", "fastfetch's config has a network module", func(t *testing.T, d string) {
			lbWrite(t, d, O+"etc/xdg/fastfetch/config.jsonc", "{\"logo\": {\"source\": \"/usr/share/runink/fastfetch/river-mark.ansi\"},\n\"modules\": [\"PublicIP\"]}\n")
		}},
		{"emerald sums", "branding/wallpapers/emerald does not match its SHA256SUMS", func(t *testing.T, d string) {
			lbWrite(t, d, "branding/wallpapers/emerald/Abstract/contents/images/960x540.jpg", "re-encoded")
			brandMirror(t, d, "branding/wallpapers/emerald/Abstract", O+"usr/share/wallpapers/Abstract")
		}},
		{"emerald copy", "MISSING: " + O + "usr/share/wallpapers/Abstract (copy of branding/wallpapers/emerald/Abstract)", func(t *testing.T, d string) {
			os.RemoveAll(filepath.Join(d, O+"usr/share/wallpapers/Abstract"))
		}},
		{"stale mark", "branding/logo/river-mark.svg does not draw the current branding/mascot/river-mascot.svg", func(t *testing.T, d string) {
			lbWrite(t, d, "branding/mascot/river-mascot.svg", "<svg>new mascot</svg>\n")
		}},
		{"os-release", "os-release LOGO is not runink-river", func(t *testing.T, d string) {
			lbWrite(t, d, O+"etc/os-release", "LOGO=archlinux\n")
		}},
		{"kickoff", "the Kickoff button does not show runink-river", func(t *testing.T, d string) {
			lbWrite(t, d, O+"usr/share/plasma/look-and-feel/org.runink.river.desktop/contents/layouts/org.kde.plasma.desktop-layout.js", "icon = \"start-here\";\n")
		}},
		{"sddm theme selection", "does not select Current=runink-river", func(t *testing.T, d string) {
			lbWrite(t, d, O+"etc/sddm.conf.d/10-river.conf", "[Theme]\nCurrent=breeze\n")
		}},
		{"sddm qt6", "the SDDM theme's metadata.desktop does not say QtVersion=6", func(t *testing.T, d string) {
			lbWrite(t, d, sddm+"/metadata.desktop", "QtVersion=5\n")
		}},
		{"mark layer", splash + " lacks the mark layer images/mark-waves.svg", func(t *testing.T, d string) {
			lbWrite(t, d, splash+"/images/mark-waves.svg", "")
		}},
		{"lockup qml", sddm + " lacks RiverLockup.qml (the animated mark)", func(t *testing.T, d string) {
			os.Remove(filepath.Join(d, sddm, "RiverLockup.qml"))
		}},
		{"qml count", "the SDDM theme has only 3 QML files", func(t *testing.T, d string) {
			os.Remove(filepath.Join(d, sddm, "Clock.qml"))
		}},
		{"imports", "the greeter or splash imports a module outside the allow-list:\n  import org.kde.plasma.core", func(t *testing.T, d string) {
			lbWrite(t, d, sddm+"/Login.qml", "import QtQuick\nimport org.kde.plasma.core as P\n")
		}},
		{"qml source", "branding/sddm/runink-river/Main.qml != branding/src/sddm/runink-river/Main.qml", func(t *testing.T, d string) {
			lbWrite(t, d, "branding/src/sddm/runink-river/Main.qml", "import QtQml\n")
		}},
		{"lockup source", "branding/splash/RiverLockup.qml != branding/src/qml/RiverLockup.qml", func(t *testing.T, d string) {
			lbWrite(t, d, "branding/splash/RiverLockup.qml", "import QtQml\n")
		}},
		{"splash source", "branding/splash/Splash.qml != branding/src/splash/Splash.qml", func(t *testing.T, d string) {
			lbWrite(t, d, "branding/src/splash/Splash.qml", "import QtQml\n")
		}},
		{"edition icon", "river.json names icon /usr/share/icons/nope.svg, which the overlay does not ship", func(t *testing.T, d string) {
			lbWrite(t, d, "iso-profiles/river/live-overlay/usr/share/river/installer/editions/river.json", "{\"icon\": \"/usr/share/icons/nope.svg\"}\n")
		}},
		{"retired colour", "retired palette colours in:\n  installer/internal/wizard/web/style.css", func(t *testing.T, d string) {
			lbWrite(t, d, "installer/internal/wizard/web/style.css", "body { color: #d9764e; }\n")
		}},
		{"live text", "branding/logo/runink-tagline.svg has live text", func(t *testing.T, d string) {
			lbWrite(t, d, "branding/logo/runink-tagline.svg", "<svg><text>Runink</text></svg>\n")
		}},
		{"tagline missing", "MISSING: branding/logo/runink-tagline.svg (branding/render.sh, outline mode)", func(t *testing.T, d string) {
			os.Remove(filepath.Join(d, "branding/logo/runink-tagline.svg"))
		}},
		{"big asset", "asset(s) over 1048576 bytes:\n  branding/big.png", func(t *testing.T, d string) {
			lbWrite(t, d, "branding/big.png", strings.Repeat("x", brandMaxAsset+1))
		}},
		{"wallpaper budget", "wallpapers total", func(t *testing.T, d string) {
			for i := 0; i < 7; i++ {
				lbWrite(t, d, fmt.Sprintf("%susr/share/wallpapers/Extra/w%d.jpg", O, i), strings.Repeat("x", brandMaxAsset-1))
			}
		}},
		{"one resolution", "River/contents/images holds 2 JPEGs (want exactly one resolution)", func(t *testing.T, d string) {
			lbWrite(t, d, "branding/wallpapers/River/contents/images/3840x2160.jpg", "jpeg")
			brandMirror(t, d, "branding/wallpapers/River", O+"usr/share/wallpapers/River")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := brandFixture(t)
			c.mutate(t, dir)
			out, err := brandRun(t, dir)
			if err == nil {
				t.Fatalf("passed:\n%s", out)
			}
			if !strings.Contains(out, c.want) {
				t.Fatalf("want %q in:\n%s", c.want, out)
			}
		})
	}
}
