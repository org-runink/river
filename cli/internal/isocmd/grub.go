// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package isocmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var placeholderRe = regexp.MustCompile(`@[a-z_]+@`)

// kernelOptions is what the live menu's @kopts@ becomes: the medium's label, which river_live
// looks the medium up by, and overlay=livefs when the image has the live layer. These are the
// same words artools wrote (qemu-test.sh checks label=<LABEL>; the live installer service and
// rc.local check overlay=livefs). @ro_opts@ is empty on an mkinitcpio medium, as with artools.
func kernelOptions(label string, livefs bool) string {
	if livefs {
		return "label=" + label + " overlay=livefs"
	}
	return "label=" + label
}

// renderMenu substitutes @kopts@ and @ro_opts@ in a profile's grub/kernels.cfg and fails if
// either survives or the menu has no linux line.
func renderMenu(cfg, kopts string) (string, error) {
	out := strings.ReplaceAll(cfg, "@kopts@", kopts)
	out = strings.ReplaceAll(out, " @ro_opts@", "")
	out = strings.ReplaceAll(out, "@ro_opts@", "")
	if m := placeholderRe.FindString(out); m != "" {
		return "", fmt.Errorf("kernels.cfg: unknown placeholder %s", m)
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) > 0 && f[0] == "linux" {
			n++
			if !strings.Contains(l, "label=") {
				return "", fmt.Errorf("kernels.cfg: a linux line without label= cannot find the medium: %s", strings.TrimSpace(l))
			}
		}
	}
	if n == 0 {
		return "", fmt.Errorf("kernels.cfg has no linux line")
	}
	return out, nil
}

// efiLabel is the EFI system partition's FAT label: the ISO label cut to 7 characters, any
// trailing underscore dropped, then _EFI (RIVER -> RIVER_EFI), as on the Artix path.
func efiLabel(isoLabel string) string {
	l := isoLabel
	if len(l) > 7 {
		l = l[:7]
	}
	return strings.TrimRight(l, "_") + "_EFI"
}

// earlyConfig is embedded in both GRUB core images. GRUB finds the medium by a marker file
// whose name is unique to this build, not by the volume label, so a second stick with the same
// label (an older image) can never supply the menu or the kernel.
func earlyConfig(marker string) string {
	return "search --no-floppy --set=root --file /boot/" + marker + "\nset prefix=($root)/boot/grub\n"
}

// loopbackCfg is written when the profile's grub/ has no loopback.cfg: GRUB reads it instead
// of grub.cfg when the ISO is booted as a file, with iso_path set; grub.cfg reads that.
const loopbackCfg = "# Booted as a file from a multi-image stick: the one menu, which reads ${iso_path}.\nsource /boot/grub/grub.cfg\n"

// Modules built into the core images: enough to find the marker on an ISO 9660 medium (and,
// for EFI, on the appended ESP) and read the menu. Everything else is insmod'ed from
// /boot/grub/<platform>/ on the medium.
var (
	biosModules = []string{"biosdisk", "iso9660", "part_gpt", "part_msdos", "search", "search_fs_file", "normal", "configfile"}
	efiModules  = []string{"iso9660", "part_gpt", "part_msdos", "fat", "search", "search_fs_file", "normal", "configfile"}
)

// writeGrub lays out /boot/grub on the ISO tree and the EFI image:
//
//	boot/grub/{i386-pc,x86_64-efi}/   GRUB's modules, from the rootfs's grub package
//	boot/grub/i386-pc/eltorito.img    BIOS El Torito image (cdboot.img + core.img)
//	boot/grub/{grub,kernels,loopback}.cfg, unicode.pf2, themes/river/
//	efi/boot/bootx64.efi              the EFI core image
//	boot/efi.img                      FAT image (label <LABEL>_EFI) holding EFI/BOOT/BOOTX64.EFI
//
// The core images are made by the rootfs's own grub-mkimage (chroot), so the modules and the
// tool are one GRUB version.
func (b *builder) writeGrub(ctx context.Context, marker string) error {
	grub := filepath.Join(b.iso, "boot", "grub")
	for _, pf := range []string{"i386-pc", "x86_64-efi"} {
		if err := os.MkdirAll(filepath.Join(grub, pf), 0o755); err != nil {
			return err
		}
		if err := copyOverlay(filepath.Join(b.rootfs, "usr/lib/grub", pf), filepath.Join(grub, pf)); err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(grub, pf, "normal.mod")); err != nil {
			return fmt.Errorf("the rootfs has no GRUB %s modules (is grub in rootfs.packages?)", pf)
		}
	}

	// The menu: the profile's own grub.cfg and kernels.cfg (with @kopts@ filled in).
	pg := filepath.Join(b.prof.Dir, "grub")
	gcfg, err := os.ReadFile(filepath.Join(pg, "grub.cfg"))
	if err != nil {
		return fmt.Errorf("the profile has no grub/grub.cfg (the Arch builder brings no stock menu): %w", err)
	}
	kcfg, err := os.ReadFile(filepath.Join(pg, "kernels.cfg"))
	if err != nil {
		return fmt.Errorf("the profile has no grub/kernels.cfg: %w", err)
	}
	menu, err := renderMenu(string(kcfg), kernelOptions(b.opt.Label, b.prof.HasLivefs()))
	if err != nil {
		return err
	}
	loop := []byte(loopbackCfg)
	if lb, err := os.ReadFile(filepath.Join(pg, "loopback.cfg")); err == nil {
		loop = lb
	}
	for name, data := range map[string][]byte{"grub.cfg": gcfg, "kernels.cfg": []byte(menu), "loopback.cfg": loop} {
		if err := os.WriteFile(filepath.Join(grub, name), data, 0o644); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(b.rootfs, "usr/share/grub/unicode.pf2"), filepath.Join(grub, "unicode.pf2"), 0o644); err != nil {
		return fmt.Errorf("unicode.pf2 (the menu's font, from the rootfs's grub): %w", err)
	}
	// The theme: the profile's grub/theme/ (a downstream edition's), else River's own.
	theme := filepath.Join(pg, "theme")
	if _, err := os.Stat(filepath.Join(theme, "theme.txt")); err != nil {
		theme = filepath.Join(b.opt.Repo, "branding/grub/river")
	}
	if _, err := os.Stat(filepath.Join(theme, "theme.txt")); err != nil {
		return fmt.Errorf("no GRUB theme at %s", theme)
	}
	if err := os.MkdirAll(filepath.Join(grub, "themes/river"), 0o755); err != nil {
		return err
	}
	if err := copyOverlay(theme, filepath.Join(grub, "themes/river")); err != nil {
		return err
	}

	// The marker GRUB searches for, and the core images that search for it.
	if err := os.WriteFile(filepath.Join(b.iso, "boot", marker), []byte(marker+"\n"), 0o644); err != nil {
		return err
	}
	tmp := filepath.Join(b.rootfs, "tmp/river-grub")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "early.cfg"), []byte(earlyConfig(marker)), 0o644); err != nil {
		return err
	}
	mkimage := func(platform, out string, mods []string) error {
		args := append([]string{b.rootfs, "grub-mkimage", "-d", "/usr/lib/grub/" + platform, "-O", platform,
			"-p", "/boot/grub", "-c", "/tmp/river-grub/early.cfg", "-o", "/tmp/river-grub/" + out}, mods...)
		return b.run(ctx, "chroot", args...)
	}
	if err := mkimage("i386-pc", "core.img", biosModules); err != nil {
		return err
	}
	if err := concat(filepath.Join(grub, "i386-pc/eltorito.img"),
		filepath.Join(grub, "i386-pc/cdboot.img"), filepath.Join(tmp, "core.img")); err != nil {
		return err
	}
	if err := mkimage("x86_64-efi", "bootx64.efi", efiModules); err != nil {
		return err
	}
	efiDir := filepath.Join(b.iso, "efi/boot")
	if err := os.MkdirAll(efiDir, 0o755); err != nil {
		return err
	}
	efiBin := filepath.Join(efiDir, "bootx64.efi")
	if err := copyFile(filepath.Join(tmp, "bootx64.efi"), efiBin, 0o644); err != nil {
		return err
	}

	// The EFI image: big enough for the core image plus FAT overhead, at least 4 MiB.
	st, err := os.Stat(efiBin)
	if err != nil {
		return err
	}
	size := max(int64(4)<<20, (st.Size()+(2<<20)+(1<<20)-1)&^((1<<20)-1))
	img := filepath.Join(b.iso, "boot/efi.img")
	if err := os.WriteFile(img, nil, 0o644); err != nil {
		return err
	}
	if err := os.Truncate(img, size); err != nil {
		return err
	}
	if err := b.run(ctx, "mkfs.fat", "-n", efiLabel(b.opt.Label), img); err != nil {
		return err
	}
	if err := b.run(ctx, "mmd", "-i", img, "::/EFI", "::/EFI/BOOT"); err != nil {
		return err
	}
	return b.run(ctx, "mcopy", "-i", img, efiBin, "::/EFI/BOOT/BOOTX64.EFI")
}

func concat(dst string, srcs ...string) error {
	var all []byte
	for _, s := range srcs {
		b, err := os.ReadFile(s)
		if err != nil {
			return err
		}
		all = append(all, b...)
	}
	return os.WriteFile(dst, all, 0o644)
}

// xorrisoArgs is the hybrid ISO: El Torito for BIOS (with GRUB's hybrid MBR, so the same file
// written to a stick boots on BIOS), the EFI image appended as partition 2 and used as the
// El Torito EFI image, Rock Ridge, ISO level 3 for squashfs images over 4 GiB.
func xorrisoArgs(isoRoot, out, label string) []string {
	return []string{
		"-as", "mkisofs",
		"--protective-msdos-label",
		"-volid", label,
		"-appid", "Runink River live medium",
		"-preparer", "river iso build",
		"-r", "-graft-points", "-no-pad",
		"--sort-weight", "0", "/",
		"--sort-weight", "1", "/boot",
		"--grub2-mbr", filepath.Join(isoRoot, "boot/grub/i386-pc/boot_hybrid.img"),
		"-partition_offset", "16",
		"-b", "boot/grub/i386-pc/eltorito.img",
		"-c", "boot.catalog",
		"-no-emul-boot", "-boot-load-size", "4", "-boot-info-table", "--grub2-boot-info",
		"-eltorito-alt-boot",
		"-append_partition", "2", "0xef", filepath.Join(isoRoot, "boot/efi.img"),
		"-e", "--interval:appended_partition_2:all::", "-iso_mbr_part_type", "0x00",
		"-no-emul-boot",
		"-iso-level", "3",
		"-o", out,
		isoRoot + "/",
	}
}
