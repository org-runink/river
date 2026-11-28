// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package isocmd is `river iso build`: Runink River's own live-ISO builder on the Arch base
// (docs/ARCH-BASE.md, step D2). It replaces Artix's artools/buildiso and
// scripts/patch-artools.go on that path and keeps the medium they produce:
//
//	LiveOS/rootfs.img   squashfs: the golden system, Packages-Root.arch + root-overlay/
//	LiveOS/livefs.img   squashfs: the live-only layer above it, Packages-Live.arch +
//	                    live-overlay/ + the live session (user, enabled s6 services)
//	boot/vmlinuz-x86_64, boot/initramfs-x86_64.img, boot/{intel,amd}-ucode.img
//	boot/grub/          the profile's grub.cfg + kernels.cfg, River's theme, BIOS + EFI
//	boot/efi.img        the EFI system partition (label <LABEL>_EFI), appended as partition 2
//
// volume label <LABEL> (RIVER), file <iso-name>-<version>-x86_64.iso. The live initramfs uses
// our own hooks (build/pkgbuilds/runink-live-initcpio): river_live, river_loop_mnt, upstream
// kms/plymouth/shutdown; no artix_* hook.
//
// It runs as root in the privileged Arch builder container that build/iso-root-stage.sh
// starts (RIVER_BASE=arch); nothing else in the tree calls it.
package isocmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/org-runink/river/cli/internal/rootcmd"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootcmd.Register(func(v *viper.Viper) *cobra.Command {
		c := &cobra.Command{
			Use:   "iso",
			Short: "The live ISO on the Arch base: river iso build",
		}
		c.AddCommand(buildCmd(v))
		return c
	})
}

// Options are the inputs of one build.
type Options struct {
	ProfileDir string // the profile: profile.yaml, Packages-*.arch, overlays, grub/
	Repo       string // the river checkout (branding/grub/river, the default theme)
	LocalRepo  string // the [runink] repository this build made (runink.db)
	PacmanConf string // the pinned Arch pacman.conf; [runink] is put in front of its repos
	Work       string // scratch directory; replaced
	Out        string // where the ISO is written
	Label      string // the ISO volume label, also the live label= kernel argument
	ISOName    string // the file name prefix
	Version    string // the file name's version part (default: the UTC date, YYYYMMDD)
	SquashComp string // mksquashfs -comp
}

var (
	reLabel   = regexp.MustCompile(`^[A-Z0-9_]{1,32}$`)
	reISOName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	reVersion = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
)

func (o *Options) validate() error {
	for name, v := range map[string]string{"--profile-dir": o.ProfileDir, "--repo": o.Repo, "--localrepo": o.LocalRepo,
		"--pacman-conf": o.PacmanConf, "--work": o.Work, "--out": o.Out} {
		if !filepath.IsAbs(v) {
			return fmt.Errorf("%s must be an absolute path (got %q)", name, v)
		}
	}
	if !reLabel.MatchString(o.Label) {
		return fmt.Errorf("--label %q: want [A-Z0-9_], at most 32 characters", o.Label)
	}
	if !reISOName.MatchString(o.ISOName) {
		return fmt.Errorf("--iso-name %q: want [a-z0-9-]", o.ISOName)
	}
	if o.Version == "" {
		o.Version = time.Now().UTC().Format("20060102")
	}
	if !reVersion.MatchString(o.Version) {
		return fmt.Errorf("--version %q: want letters, digits, . _ -", o.Version)
	}
	if _, err := os.Stat(filepath.Join(o.LocalRepo, "runink.db")); err != nil {
		return fmt.Errorf("no [runink] repository at %s: %w", o.LocalRepo, err)
	}
	return nil
}

func buildCmd(v *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:   "build",
		Short: "Build the live ISO from a profile on the Arch base (root, in the privileged builder container)",
		Long: "Builds the live ISO: pacstrap the rootfs from Packages-Root.arch and the live layer from\n" +
			"Packages-Live.arch out of the pinned Arch repositories and [runink], apply root-overlay/ and\n" +
			"live-overlay/, set up the live session, write the live initramfs with River's own hooks,\n" +
			"squash both layers and make a hybrid BIOS/EFI ISO with the profile's GRUB menu.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			o := Options{
				ProfileDir: v.GetString("profile-dir"), Repo: v.GetString("repo"), LocalRepo: v.GetString("localrepo"),
				PacmanConf: v.GetString("pacman-conf"), Work: v.GetString("work"), Out: v.GetString("out"),
				Label: v.GetString("label"), ISOName: v.GetString("iso-name"), Version: v.GetString("version"),
				SquashComp: v.GetString("squashfs-comp"),
			}
			return Build(cmd.Context(), o, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	f := c.Flags()
	f.String("profile-dir", "", "the profile directory (required)")
	f.String("repo", "/os", "the river checkout")
	f.String("localrepo", "/os/localrepo", "the [runink] repository built for this image")
	f.String("pacman-conf", "/etc/pacman.conf", "the pinned Arch pacman.conf to install from")
	f.String("work", "", "scratch directory, replaced (required; not on ZFS: it holds overlay upper directories)")
	f.String("out", "", "output directory for the ISO (required)")
	f.String("label", "RIVER", "ISO volume label and live label= argument")
	f.String("iso-name", "runink-river", "ISO file name prefix")
	f.String("version", "", "ISO file name version (default: today's UTC date, YYYYMMDD)")
	f.String("squashfs-comp", "zstd", "squashfs compressor (the kernel reads zstd, xz, lz4, lzo, gzip)")
	return c
}

type builder struct {
	opt                 Options
	prof                *Profile
	out, errw           io.Writer
	rootfs, livefs, iso string
	mounts              []string // mounted by this build, unmounted in reverse at the end
}

// Build makes the ISO. Every step fails the build; nothing is a warning.
func Build(ctx context.Context, o Options, out, errw io.Writer) (err error) {
	if os.Geteuid() != 0 {
		return fmt.Errorf("river iso build needs root (it mounts, chroots and pacstraps); run it through build/iso-root-stage.sh")
	}
	if err := o.validate(); err != nil {
		return err
	}
	for _, t := range []string{"pacstrap", "arch-chroot", "chroot", "pacman", "mount", "umount", "mksquashfs", "xorriso", "mkfs.fat", "mmd", "mcopy"} {
		if _, err := exec.LookPath(t); err != nil {
			return fmt.Errorf("%s is required in the builder image", t)
		}
	}
	prof, err := LoadProfile(o.ProfileDir)
	if err != nil {
		return err
	}
	b := &builder{opt: o, prof: prof, out: out, errw: errw,
		rootfs: filepath.Join(o.Work, "rootfs"), livefs: filepath.Join(o.Work, "livefs"), iso: filepath.Join(o.Work, "iso")}
	defer func() {
		if uerr := b.unmountAll(); uerr != nil && err == nil {
			err = uerr
		}
	}()
	return b.build(ctx)
}

func (b *builder) logf(format string, a ...any) { fmt.Fprintf(b.out, "[iso] "+format+"\n", a...) }

func (b *builder) run(ctx context.Context, name string, args ...string) error {
	return b.runIn(ctx, nil, name, args...)
}

func (b *builder) runIn(ctx context.Context, stdin io.Reader, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Stdin, c.Stdout, c.Stderr = stdin, b.out, b.errw
	if err := c.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func (b *builder) output(ctx context.Context, name string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.Stderr = b.errw
	o, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(o), nil
}

// mountOverlay mounts lower (colon-separated, top first) under upper at target.
func (b *builder) mountOverlay(ctx context.Context, lower, upper, work, target string) error {
	for _, d := range []string{upper, work, target} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := b.run(ctx, "mount", "-t", "overlay", "overlay", "-o",
		"lowerdir="+lower+",upperdir="+upper+",workdir="+work, target); err != nil {
		return err
	}
	b.mounts = append(b.mounts, target)
	return nil
}

func (b *builder) unmount(ctx context.Context, target string) error {
	i := slices.Index(b.mounts, target)
	if i < 0 {
		return nil
	}
	b.mounts = slices.Delete(b.mounts, i, i+1)
	return b.run(ctx, "umount", "-R", target)
}

func (b *builder) unmountAll() error {
	var first error
	for i := len(b.mounts) - 1; i >= 0; i-- {
		if err := exec.Command("umount", "-R", b.mounts[i]).Run(); err != nil && first == nil {
			first = fmt.Errorf("umount %s: %w", b.mounts[i], err)
		}
	}
	b.mounts = nil
	return first
}

func (b *builder) build(ctx context.Context) error {
	o, p := b.opt, b.prof
	b.logf("profile %s: label %s, %d rootfs packages, livefs %v (%d packages)", p.Dir, o.Label, len(p.RootPackages), p.HasLivefs(), len(p.LivePackages))
	if err := os.RemoveAll(o.Work); err != nil {
		return err
	}
	for _, d := range []string{b.rootfs, filepath.Join(b.iso, "LiveOS"), filepath.Join(b.iso, "boot"), o.Out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	conf, err := os.ReadFile(o.PacmanConf)
	if err != nil {
		return err
	}
	pconf, err := injectRunink(string(conf), o.LocalRepo)
	if err != nil {
		return fmt.Errorf("%s: %w", o.PacmanConf, err)
	}
	confPath := filepath.Join(o.Work, "pacman.conf")
	if err := os.WriteFile(confPath, []byte(pconf), 0o644); err != nil {
		return err
	}
	pacstrap := func(root string, pkgs []string) error {
		// -c: the builder's package cache; -G, -M: the image gets its keyring and mirror list
		// from its own packages, not from the builder.
		return b.run(ctx, "pacstrap", append([]string{"-C", confPath, "-c", "-G", "-M", root}, pkgs...)...)
	}

	// 1. rootfs.img: the golden system.
	// pacstrap and arch-chroot expect a mount point: the rootfs is bind-mounted onto itself.
	if err := b.run(ctx, "mount", "--bind", b.rootfs, b.rootfs); err != nil {
		return err
	}
	b.mounts = append(b.mounts, b.rootfs)
	b.logf("1/6 rootfs: pacstrap %d packages from %s", len(p.RootPackages), rootManifest)
	if err := pacstrap(b.rootfs, p.RootPackages); err != nil {
		return err
	}
	if err := copyOverlay(filepath.Join(p.Dir, "root-overlay"), b.rootfs); err != nil {
		return fmt.Errorf("root-overlay: %w", err)
	}
	if !p.HasLivefs() {
		if err := b.configureLive(ctx, b.rootfs); err != nil {
			return err
		}
	}
	if err := cleanChroot(b.rootfs); err != nil {
		return err
	}

	// 2. livefs.img: the live layer, built as the upper directory of an overlay on the rootfs.
	live := b.rootfs
	if p.HasLivefs() {
		b.logf("2/6 livefs: pacstrap %d packages from %s, live-overlay, live session", len(p.LivePackages), liveManifest)
		mnt := filepath.Join(o.Work, "livefs.mnt")
		if err := b.mountOverlay(ctx, b.rootfs, b.livefs, filepath.Join(o.Work, "livefs.work"), mnt); err != nil {
			return err
		}
		if err := pacstrap(mnt, p.LivePackages); err != nil {
			return err
		}
		if err := copyOverlay(filepath.Join(p.Dir, "live-overlay"), mnt); err != nil {
			return fmt.Errorf("live-overlay: %w", err)
		}
		if err := b.configureLive(ctx, mnt); err != nil {
			return err
		}
		if err := b.unmount(ctx, mnt); err != nil {
			return err
		}
		if err := cleanChroot(b.livefs); err != nil {
			return err
		}
		live = b.livefs + ":" + b.rootfs
	} else {
		b.logf("2/6 livefs: none (one layer; the live session is on the rootfs)")
	}

	// 3. The live system as it boots (scratch upper, discarded): kernel, initramfs, checks.
	view := filepath.Join(o.Work, "boot.mnt")
	if err := b.mountOverlay(ctx, live, filepath.Join(o.Work, "boot.upper"), filepath.Join(o.Work, "boot.work"), view); err != nil {
		return err
	}
	b.logf("3/6 kernel and live initramfs")
	if err := b.bootFiles(ctx, view); err != nil {
		return err
	}
	b.logf("4/6 checks: ZFS module and hook, forbidden packages, live boot database")
	if err := b.checks(ctx, view); err != nil {
		return err
	}
	if err := b.unmount(ctx, view); err != nil {
		return err
	}

	// 5. GRUB (before squashing: grub-mkimage runs in the rootfs and leaves nothing behind).
	b.logf("5/6 GRUB: BIOS + EFI, the profile's menu, River's theme")
	if err := b.writeGrub(ctx, markerName(o.Version)); err != nil {
		return err
	}

	// 6. squashfs layers and the ISO.
	b.logf("6/6 squashfs and ISO")
	if err := b.unmount(ctx, b.rootfs); err != nil {
		return err
	}
	squash := func(src, img string) error {
		return b.run(ctx, "mksquashfs", src, img, "-noappend", "-comp", o.SquashComp, "-b", "1M", "-no-progress")
	}
	if err := squash(b.rootfs, filepath.Join(b.iso, "LiveOS/rootfs.img")); err != nil {
		return err
	}
	if p.HasLivefs() {
		if err := squash(b.livefs, filepath.Join(b.iso, "LiveOS/livefs.img")); err != nil {
			return err
		}
	}
	isoFile := filepath.Join(o.Out, o.ISOName+"-"+o.Version+"-x86_64.iso")
	if err := os.Remove(isoFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := b.run(ctx, "xorriso", xorrisoArgs(b.iso, isoFile, o.Label)...); err != nil {
		return err
	}
	b.logf("DONE %s", isoFile)
	return nil
}

// markerName is the file under /boot that GRUB's core images search for: unique per build.
func markerName(version string) string {
	r := make([]byte, 8)
	_, _ = rand.Read(r)
	return "river-" + version + "-" + hex.EncodeToString(r) + ".uuid"
}

// liveGroups are the live user's supplementary groups, artools' default set; those the image
// does not define are skipped (Arch creates several only through sysusers).
var liveGroups = []string{"video", "power", "optical", "network", "lp", "scanner", "wheel", "users", "log"}

// configureLive sets up the live session in root, as artools' configure_live_image did: the
// live user (bash, like an installed machine's admin) and its password, then the live
// session's s6 services enabled and compiled into the boot database. live-session.autologin
// and use-xlibre fed artools' own live setup and package sets, which this image never had; the
// autologin is live-overlay/'s greetd and tty configuration.
func (b *builder) configureLive(ctx context.Context, root string) error {
	ls := b.prof.LiveSession
	groupFile, err := os.ReadFile(filepath.Join(root, "etc/group"))
	if err != nil {
		return err
	}
	var grps []string
	for _, g := range liveGroups {
		if regexp.MustCompile(`(?m)^` + g + `:`).Match(groupFile) {
			grps = append(grps, g)
		}
	}
	b.logf("live user %s (groups %s)", ls.User, strings.Join(grps, ","))
	if err := b.run(ctx, "arch-chroot", root, "useradd", "-m", "-G", strings.Join(grps, ","), "-s", "/bin/bash", ls.User); err != nil {
		return err
	}
	if err := b.runIn(ctx, strings.NewReader(ls.User+":"+ls.Password+"\n"), "arch-chroot", root, "chpasswd"); err != nil {
		return err
	}

	// The s6 services. Overlay-supplied services must be known before they are enabled, so
	// the repository is synced first.
	if err := b.run(ctx, "arch-chroot", root, "s6", "repository", "sync"); err != nil {
		return err
	}
	for _, svc := range ls.Services {
		if !serviceExists(root, svc) {
			return fmt.Errorf("live-session service %s not found in %s/etc/s6/{sv,adminsv}", svc, root)
		}
	}
	if len(ls.Services) > 0 {
		b.logf("enabling %s", strings.Join(ls.Services, " "))
		if err := b.run(ctx, "arch-chroot", append([]string{root, "s6", "set", "enable"}, ls.Services...)...); err != nil {
			return err
		}
	}
	if err := b.run(ctx, "arch-chroot", root, "s6", "set", "commit"); err != nil {
		return err
	}
	if err := b.run(ctx, "arch-chroot", root, "s6", "live", "install", "--init"); err != nil {
		return err
	}
	// user-services: artools enabled them through its own s6 user-service support. Our s6
	// layer has not defined how a user's services are enabled yet (docs/ARCH-BASE.md, the s6
	// layer stream); building without them would ship a live session with no session bus or
	// audio, so it is refused instead.
	if len(ls.UserServices) > 0 {
		return fmt.Errorf("live-session.user-services (%s): the s6 layer does not yet define how a user's services are enabled; "+
			"`river iso build` cannot set them up", strings.Join(ls.UserServices, " "))
	}
	return nil
}

func serviceExists(root, svc string) bool {
	for _, d := range []string{"etc/s6/sv/" + svc, "etc/s6/sv/" + svc + "-srv", "etc/s6/adminsv/" + svc} {
		if st, err := os.Stat(filepath.Join(root, d)); err == nil && st.IsDir() {
			return true
		}
	}
	return false
}

// kernelVersion is the one linux-runink module directory in the rootfs. Exactly one, or the
// initramfs would be built against the wrong kernel or none (patch-artools.go step 2 records
// how an empty version once went unnoticed).
func kernelVersion(rootfs string) (string, error) {
	m, _ := filepath.Glob(filepath.Join(rootfs, "usr/lib/modules/*-runink"))
	var dirs []string
	for _, d := range m {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			dirs = append(dirs, filepath.Base(d))
		}
	}
	if len(dirs) != 1 {
		return "", fmt.Errorf("want exactly one linux-runink module directory under usr/lib/modules, found %d: %v", len(dirs), dirs)
	}
	return dirs[0], nil
}

// liveHooks is the live initramfs's HOOKS line. river_live finds and mounts the medium,
// river_loop_mnt (after it) handles an ISO booted as a file; kms puts the GPU driver in the
// image so the splash (plymouth, when the image has it) can take the display; shutdown keeps
// a copy of the initramfs for unmounting the medium at power-off. No autodetect: the image
// must boot on any machine, not the builder.
func liveHooks(plymouth bool) string {
	h := []string{"base", "udev", "river_live", "river_loop_mnt", "kms"}
	if plymouth {
		h = append(h, "plymouth")
	}
	h = append(h, "modconf", "block", "filesystems", "keyboard", "keymap", "shutdown")
	return strings.Join(h, " ")
}

const liveConf = "/etc/mkinitcpio-river-live.conf"

// bootFiles writes boot/vmlinuz-x86_64, boot/initramfs-x86_64.img and the microcode images.
func (b *builder) bootFiles(ctx context.Context, view string) error {
	k, err := kernelVersion(b.rootfs)
	if err != nil {
		return err
	}
	_, perr := os.Stat(filepath.Join(view, "usr/lib/initcpio/install/plymouth"))
	hooks := liveHooks(perr == nil)
	conf := "# written by river iso build: the live medium's initramfs\nMODULES=()\nBINARIES=()\nFILES=()\nHOOKS=(" + hooks + ")\nCOMPRESSION=\"zstd\"\n"
	if err := os.WriteFile(filepath.Join(view, liveConf), []byte(conf), 0o644); err != nil {
		return err
	}
	b.logf("live initramfs for %s: HOOKS=(%s)", k, hooks)
	if err := b.run(ctx, "arch-chroot", view, "mkinitcpio", "-c", liveConf, "-k", k, "-g", "/boot/initramfs-x86_64.img"); err != nil {
		return err
	}
	boot := filepath.Join(b.iso, "boot")
	for src, dst := range map[string]string{
		filepath.Join(view, "boot/initramfs-x86_64.img"):         "initramfs-x86_64.img",
		filepath.Join(b.rootfs, "usr/lib/modules", k, "vmlinuz"): "vmlinuz-x86_64",
		filepath.Join(b.rootfs, "boot/intel-ucode.img"):          "intel-ucode.img",
		filepath.Join(b.rootfs, "boot/amd-ucode.img"):            "amd-ucode.img",
	} {
		if err := copyFile(src, filepath.Join(boot, dst), 0o644); err != nil {
			return fmt.Errorf("boot/%s: %w", dst, err)
		}
	}
	return nil
}

// checks are the ones scripts/build-iso-box.sh makes on the Artix path, read from the built
// trees rather than from a log:
//   - the ZFS module is in the rootfs, the zfs initcpio hook too, and runink-zfs and
//     runink-zfs-utils are one OpenZFS release (a ZFS-root image is useless without them);
//   - no package the profile's forbidden.closure names is installed on the live system;
//   - every live-session service is in the live boot database's default bundle.
func (b *builder) checks(ctx context.Context, view string) error {
	k, err := kernelVersion(b.rootfs)
	if err != nil {
		return err
	}
	found := false
	_ = filepath.WalkDir(filepath.Join(b.rootfs, "usr/lib/modules", k), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "zfs.ko") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if !found {
		return fmt.Errorf("no zfs.ko under usr/lib/modules/%s in the rootfs: this is a ZFS-root image (did runink-zfs reach [runink]?)", k)
	}
	if _, err := os.Stat(filepath.Join(b.rootfs, "usr/lib/initcpio/install/zfs")); err != nil {
		return fmt.Errorf("the zfs initcpio hook is missing from the rootfs (runink-zfs-utils ships it)")
	}
	ver := func(pkg string) string {
		s, err := b.output(ctx, "pacman", "--root", b.rootfs, "-Q", pkg)
		if err != nil {
			return ""
		}
		f := strings.Fields(s)
		if len(f) != 2 {
			return ""
		}
		v := f[1]
		if i := strings.LastIndexByte(v, '-'); i > 0 {
			v = v[:i]
		}
		return v
	}
	if zm, zu := ver("runink-zfs"), ver("runink-zfs-utils"); zm == "" || zm != zu {
		return fmt.Errorf("ZFS userland/module mismatch: runink-zfs=%q runink-zfs-utils=%q", zm, zu)
	}

	forbidden, err := readForbidden(filepath.Join(b.prof.Dir, "forbidden.closure"))
	if err != nil {
		return err
	}
	installed, err := b.output(ctx, "pacman", "--root", view, "-Qq")
	if err != nil {
		return err
	}
	var bad []string
	for _, n := range strings.Fields(installed) {
		if slices.Contains(forbidden, n) {
			bad = append(bad, n)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("forbidden.closure names installed package(s): %s", strings.Join(bad, " "))
	}

	db, err := b.output(ctx, "chroot", view, "s6-rc-db", "-c", "/etc/s6/rc/compiled", "contents", "default")
	if err != nil {
		return fmt.Errorf("cannot read the live boot database: %w", err)
	}
	have := strings.Fields(db)
	var missing []string
	for _, svc := range b.prof.LiveSession.Services {
		want := svc
		if st, err := os.Stat(filepath.Join(view, "etc/s6/sv", svc+"-srv")); err == nil && st.IsDir() {
			want = svc + "-srv"
		}
		if !slices.Contains(have, want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("live-session service(s) missing from the live boot database's default bundle: %s", strings.Join(missing, " "))
	}
	return nil
}
