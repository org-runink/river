// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package isocmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testProfile = `live-session:
  user: runink
  password: runink
  services:
    - greetd
  user-services:
    - pipewire
rootfs:
  packages:
    - grub
  packages-init:
    s6: []
livefs:
  packages:
    - weston
`

func writeProfile(t *testing.T, yaml, root, live string) string {
	t.Helper()
	d := t.TempDir()
	for name, s := range map[string]string{"profile.yaml": yaml, rootManifest: root, liveManifest: live} {
		if s == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(d, name), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

const okRoot = "# comment\nlinux-runink mkinitcpio\ngrub\nintel-ucode amd-ucode # inline\ngrub\n"

func TestLoadProfile(t *testing.T) {
	d := writeProfile(t, testProfile, okRoot, "weston\nrunink-live-initcpio\n")
	p, err := LoadProfile(d)
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasLivefs() || p.LiveSession.User != "runink" || len(p.LiveSession.UserServices) != 1 {
		t.Fatalf("parsed %+v", p)
	}
	if got := strings.Join(p.RootPackages, " "); got != "amd-ucode grub intel-ucode linux-runink mkinitcpio" {
		t.Fatalf("root packages %q (want sorted, deduplicated, comments dropped)", got)
	}
	if got := strings.Join(p.LivePackages, " "); got != "runink-live-initcpio weston" {
		t.Fatalf("live packages %q", got)
	}
}

func TestLoadProfileRefuses(t *testing.T) {
	for name, c := range map[string]struct{ yaml, root, live, want string }{
		"no live hooks":    {testProfile, okRoot, "weston\n", "runink-live-initcpio"},
		"no ucode":         {testProfile, "linux-runink mkinitcpio grub\n", "runink-live-initcpio\n", "intel-ucode amd-ucode"},
		"empty live":       {testProfile, okRoot, "# nothing\n", "lists no package"},
		"in both":          {testProfile, okRoot, "grub runink-live-initcpio\n", "in both"},
		"no live password": {strings.Replace(testProfile, "  password: runink\n", "", 1), okRoot, "runink-live-initcpio\n", "password"},
	} {
		t.Run(name, func(t *testing.T) {
			d := writeProfile(t, c.yaml, c.root, c.live)
			if c.live == "" {
				os.Remove(filepath.Join(d, liveManifest))
			}
			_, err := LoadProfile(d)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestOneLayerTakesLiveHooksInRootfs(t *testing.T) {
	y := strings.Replace(testProfile, "livefs:\n  packages:\n    - weston\n", "", 1)
	d := writeProfile(t, y, okRoot, "")
	if _, err := LoadProfile(d); err == nil || !strings.Contains(err.Error(), "runink-live-initcpio") {
		t.Fatalf("one layer without the hooks in rootfs: err = %v", err)
	}
	d = writeProfile(t, y, okRoot+"runink-live-initcpio\n", "")
	if _, err := LoadProfile(d); err != nil {
		t.Fatal(err)
	}
}

// The in-tree profile parses (its Arch manifests are checked only when they exist).
func TestInTreeProfileParses(t *testing.T) {
	d := filepath.Join("..", "..", "..", "iso-profiles", "river")
	b, err := os.ReadFile(filepath.Join(d, "profile.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := parseProfile(d, b)
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasLivefs() || len(p.LiveSession.Services) == 0 {
		t.Fatalf("the river profile: livefs %v, %d services", p.HasLivefs(), len(p.LiveSession.Services))
	}
}

func TestInjectRunink(t *testing.T) {
	conf := "[options]\nArchitecture = auto\n\n[core]\nInclude = /etc/pacman.d/mirrorlist\n\n[extra]\nInclude = x\n"
	got, err := injectRunink(conf, "/os/localrepo")
	if err != nil {
		t.Fatal(err)
	}
	want := "[options]\nArchitecture = auto\n\n[runink]\nSigLevel = Optional TrustAll\nServer = file:///os/localrepo\n\n[core]\n"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("got\n%s", got)
	}
	if _, err := injectRunink(got, "/x"); err == nil {
		t.Fatal("a second [runink] was accepted")
	}
	if _, err := injectRunink(conf+"\n[blackarch]\nInclude = y\n", "/x"); err == nil {
		t.Fatal("a conf with [blackarch] was accepted as a build source")
	}
	if _, err := injectRunink("[options]\n", "/x"); err == nil {
		t.Fatal("a conf without repositories was accepted")
	}
}

func TestRenderMenuInTree(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "iso-profiles", "river", "grub", "kernels.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := renderMenu(string(b), kernelOptions("RIVER", true))
	if err != nil {
		t.Fatal(err)
	}
	// The entries river#100 and river#101 rely on, with their kernel options.
	for _, want := range []string{
		"--id runink-live ", "--id runink-live-install ", "--id runink-live-safe ", "--id runink-live-verbose ", "--id runink-live-text ",
		"label=RIVER overlay=livefs ${kopts} quiet splash\n",
		"quiet splash river.installer=1", "nomodeset quiet splash", "plymouth.enable=0 river.text=1",
		"/boot/intel-ucode.img /boot/amd-ucode.img /boot/initramfs-x86_64.img",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered menu lacks %q", want)
		}
	}
	if placeholderRe.MatchString(out) {
		t.Error("a placeholder survived")
	}
	if _, err := renderMenu("menuentry x {\n linux /boot/vmlinuz @kopts@ @bogus@\n}\n", "label=X"); err == nil {
		t.Error("an unknown placeholder was accepted")
	}
	if _, err := renderMenu("menuentry x {\n linux /boot/vmlinuz quiet\n}\n", "label=X"); err == nil {
		t.Error("a linux line without label= was accepted")
	}
}

func TestLabelsAndHooks(t *testing.T) {
	for in, want := range map[string]string{"RIVER": "RIVER_EFI", "RUNINK_SERVER": "RUNINK_EFI", "EXAMPLE_SRV": "EXAMPLE_EFI"} {
		if got := efiLabel(in); got != want {
			t.Errorf("efiLabel(%s) = %s, want %s", in, got, want)
		}
	}
	if kernelOptions("RIVER", false) != "label=RIVER" {
		t.Error("one-layer options")
	}
	h := liveHooks(true)
	if !strings.Contains(h, "udev river_live river_loop_mnt kms plymouth modconf") || strings.Contains(h, "artix") || strings.Contains(h, "autodetect") {
		t.Errorf("hooks %q", h)
	}
	if strings.Contains(liveHooks(false), "plymouth") {
		t.Error("plymouth hook without plymouth")
	}
	if !strings.Contains(earlyConfig("m.uuid"), "--file /boot/m.uuid") {
		t.Error("early config does not search the marker")
	}
}

func TestCopyOverlayAndClean(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "etc/s6"), 0o755))
	must(os.WriteFile(filepath.Join(src, "etc/s6/rc.local"), []byte("x"), 0o755))
	must(os.Symlink("rc.local", filepath.Join(src, "etc/s6/link")))
	must(copyOverlay(src, dst))
	if b, err := os.ReadFile(filepath.Join(dst, "etc/s6/link")); err != nil || string(b) != "x" {
		t.Fatalf("symlink not dereferenced: %q %v", b, err)
	}
	if st, _ := os.Lstat(filepath.Join(dst, "etc/s6/link")); st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("copied a symlink, want its target")
	}
	must(copyOverlay(filepath.Join(src, "absent"), dst))

	for _, f := range []string{"var/log/a.log", "var/lib/pacman/sync/core.db", "boot/initramfs-linux.img", "boot/vmlinuz", "etc/machine-id", "etc/x.pacnew", "tmp/a/b"} {
		must(os.MkdirAll(filepath.Dir(filepath.Join(dst, f)), 0o755))
		must(os.WriteFile(filepath.Join(dst, f), nil, 0o644))
	}
	must(cleanChroot(dst))
	for _, gone := range []string{"var/log/a.log", "var/lib/pacman/sync/core.db", "boot/initramfs-linux.img", "etc/machine-id", "etc/x.pacnew", "tmp/a"} {
		if _, err := os.Stat(filepath.Join(dst, gone)); err == nil {
			t.Errorf("%s survived cleanChroot", gone)
		}
	}
	for _, kept := range []string{"boot/vmlinuz", "var/log", "etc/s6/rc.local"} {
		if _, err := os.Stat(filepath.Join(dst, kept)); err != nil {
			t.Errorf("%s removed by cleanChroot", kept)
		}
	}
}

func TestKernelVersion(t *testing.T) {
	r := t.TempDir()
	if _, err := kernelVersion(r); err == nil {
		t.Fatal("no kernel accepted")
	}
	os.MkdirAll(filepath.Join(r, "usr/lib/modules/7.2.7-zen1-1-runink"), 0o755)
	if k, err := kernelVersion(r); err != nil || k != "7.2.7-zen1-1-runink" {
		t.Fatalf("%q %v", k, err)
	}
	os.MkdirAll(filepath.Join(r, "usr/lib/modules/7.2.8-zen1-1-runink"), 0o755)
	if _, err := kernelVersion(r); err == nil {
		t.Fatal("two kernels accepted")
	}
}

func TestOptionsValidate(t *testing.T) {
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "runink.db"), nil, 0o644)
	o := Options{ProfileDir: "/p", Repo: "/os", LocalRepo: repo, PacmanConf: "/etc/pacman.conf", Work: "/w", Out: "/o", Label: "RIVER", ISOName: "runink-river"}
	if err := o.validate(); err != nil || len(o.Version) != 8 {
		t.Fatalf("%v, version %q", err, o.Version)
	}
	bad := o
	bad.Label = "river"
	if bad.validate() == nil {
		t.Error("lower-case label accepted")
	}
	bad = o
	bad.Work = "w"
	if bad.validate() == nil {
		t.Error("relative work dir accepted")
	}
}
