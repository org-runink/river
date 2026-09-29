//go:build ignore

// patch-artools — patch the builder's artools so the LIVE ISO can boot linux-runink
// (needed for ZFS: mainline linux 7.x is too new for OpenZFS). buildiso assumes
// mainline `linux` in a few spots.
//
// Ported from patch-artools.py (deleted) — behaviour-for-behaviour: same four
// patches, same stdout/stderr wording, same idempotence guards.
//
// Run it as root (it writes under /usr/share/artools and /usr/bin):
//
//	go build -o /tmp/patch-artools scripts/patch-artools.go && sudo /tmp/patch-artools
//
// Build-then-run rather than `sudo go run`: `go run` as root would need a root
// GOCACHE, and the build host already has Go (build/20-installer-binaries.sh).
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// read slurps a file that MUST exist. The python version used a bare open().read()
// here, so a missing artools file was a hard failure — keep that.
func read(path string) (string, os.FileMode) {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "patch-artools: %v\n", err)
		os.Exit(1)
	}
	fi, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "patch-artools: %v\n", err)
		os.Exit(1)
	}
	return string(b), fi.Mode().Perm()
}

// write rewrites a file in place, PRESERVING its mode. This matters: /usr/bin/buildiso
// is executable, and clobbering it to 0644 would break the build with a confusing
// "permission denied". (os.WriteFile ignores perm for an existing file, but we pass
// the real mode so the create-path is correct too.)
func write(path, s string, mode os.FileMode) {
	if err := os.WriteFile(path, []byte(s), mode); err != nil {
		fmt.Fprintf(os.Stderr, "patch-artools: %v\n", err)
		os.Exit(1)
	}
}

// 1. common.yaml: swap the live kernel linux -> linux-runink, headers -> linux-runink-headers.
var (
	reLinux        = regexp.MustCompile(`(?m)^(\s*-\s*)linux$`)
	reLinuxHeaders = regexp.MustCompile(`(?m)^(\s*-\s*)linux-headers$`)
)

// 3. grub.sh injection. The Artix live grub.cfg is interactive with NO `set timeout`,
// so a headless node waits at the menu forever. Worse, the menu is built by shell
// FUNCTIONS with the tz/keytable/lang *settings submenus* at index 0, and `set default`
// matched by the CD entry's title does NOT resolve against it — a title miss drops GRUB
// into the keytable submenu and the box hangs there (verified). Fix: give the CD/DVD live
// entry a stable `--id runink-live` in kernels.cfg and default to that id + a short timeout.
//
// The override is APPENDED at the very END of grub.cfg on purpose: the live cfg sources
// variable.cfg (which sets `timeout=-1`, i.e. wait forever) and calls boot_menu (which
// resets `default`), both AFTER the top of the file — so the last assignment wins.
//
// NOTE: the `\n` sequences in the printf line below are LITERAL backslash-n for printf to
// interpret, not real newlines. That is why this is a raw string literal.
const grubMarker = `cp "${livecfg}"/cfg/*.cfg "${grub}"`

// grubInjectFmt's arguments: %[1]s the profile DIRECTORY (profileDir: /os/iso-profiles/<profile>,
// or the staged external profile named by RIVER_PROFILE_DIR; its grub/ may hold the profile's
// own live menu), %[2]s the menu title from brandFor(profile) for profiles that keep Artix's
// menu, %[3]s the theme DIRECTORY from grubThemeDir (the profile's grub/theme/, else
// branding/grub/<grubThemeFor(profile)>). The
// titles used to be the literal "Runink Sovereign Server" for every profile, so a workstation
// stick offered to install a server. Cosmetic on its own — but it sat next to the volume
// label, which is NOT cosmetic (see the iso_label patch), and both came from the same
// hardcoded assumption that there would only ever be one profile.
//
// A profile with iso-profiles/<profile>/grub/grub.cfg (both shipped profiles) REPLACES Artix's live
// menu with its own grub.cfg + kernels.cfg: exactly its boot entries, no clock/tz/keytable/
// lang entries, no efi_uga. The other profiles keep Artix's menu, rebranded as before.
const grubInjectFmt = grubMarker + `
    # runink: auto-boot + COMMERCIAL REBRAND the live menu (see scripts/patch-artools.go)
    if [ -f %[1]s/grub/grub.cfg ]; then
        cp %[1]s/grub/grub.cfg %[1]s/grub/kernels.cfg "${grub}/"
        rm -f "${grub}/defaults.cfg" "${grub}/variable.cfg"
    else
        sed -i 's|menuentry "From CD/DVD/ISO: artix.x86_64 "|menuentry "From CD/DVD/ISO: artix.x86_64 " --id runink-live|' "${grub}/kernels.cfg"
        sed -i 's|From CD/DVD/ISO: artix.x86_64|%[2]s   -   Install / Live|g; s|From Stick/HDD: artix.x86_64|%[2]s   -   Boot from USB / HDD|g' "${grub}/kernels.cfg"
        # efi_uga does not exist in GRUB's x86_64-efi build: "efi_uga.mod not found" at every boot.
        sed -i '/insmod efi_uga/d' "${grub}/grub.cfg"
        printf '\n# runink: commercial branding + unattended auto-boot\nset menu_color_normal=light-gray/black\nset menu_color_highlight=black/cyan\nset color_normal=light-gray/black\nset color_highlight=black/cyan\nset default=runink-live\nset timeout=10\n' >> "${grub}/grub.cfg"
    fi
    # runink: Runink River GRUB theme on the live menu. The profile's own grub.cfg loads
    # themes/river/theme.txt; Artix's live grub.cfg loads whatever ${grub_theme} names
    # (gfxterm + unicode.pf2 + the theme dir's images) when the file exists, and variable.cfg
    # is where it is set, so re-point it there. /os is the repo checkout inside the build
    # container (see build-iso-box.sh). The source dir is per profile (grubThemeFor); it always
    # lands at themes/river on the stick.
    if [ -f %[3]s/theme.txt ]; then
        mkdir -p "${grub}/themes/river"
        cp %[3]s/* "${grub}/themes/river/"
        if [ -f "${grub}/variable.cfg" ]; then
            sed -i 's|^grub_theme=.*|grub_theme=/boot/grub/themes/river/theme.txt|' "${grub}/variable.cfg"
        fi
        # A JPEG background needs the jpeg reader. The live cfg's theme loop already does
        # "insmod jpeg" for *.jpg; this makes it explicit and independent of that loop.
        for f in "${grub}"/themes/river/*.jpg; do
            [ -e "$f" ] && printf '\n# runink: JPEG theme background\ninsmod jpeg\n' >> "${grub}/grub.cfg"
            break
        done
    fi`

// pacmanConfInject is prepended to the FIRST [system] stanza in artools' own
// iso-x86_64.conf, so pacman prefers our pinned linux-runink/runink-zfs/runink-zfs-utils
// over anything the Artix repos carry under the same names. [runink] is TrustAll (our own
// file:// local repo, built in this same container).
//
// [archzfs] used to be injected here too, with SigLevel = Required. It was removed on
// 2026-09-22: its zfs-utils (2.3.3) was the last thing any profile took from it, and that
// is now runink-zfs-utils, built from the same OpenZFS tarball as runink-zfs. See the
// matching note in build-iso-box.sh before re-adding it.
const pacmanConfInject = `[runink]
SigLevel = Optional TrustAll
Server = file:///os/localrepo

[system]`

// patchPacmanConf replaces the first "[system]" with the injected block. Ported from
// the inline `python3 -c` that used to live in build-iso-box.sh. The caller still
// guards with `grep -q '[runink]'`, so this is not self-guarded — same as before.
func patchPacmanConf(path string) {
	s, mode := read(path)
	if !strings.Contains(s, "[system]") {
		fmt.Fprintln(os.Stderr, "WARN: pacman conf has no [system] stanza — nothing injected")
		return
	}
	write(path, strings.Replace(s, "[system]", pacmanConfInject, 1), mode)
	fmt.Println("patched pacman conf: [runink] injected before [system]")
}

// brandFor maps a profile name to its ISO volume label and live GRUB menu title.
//
// The label must be UNIQUE PER PROFILE and must be a valid ISO9660/Joliet volume id
// (uppercase, no spaces) — it is consumed as `root=LABEL=<x>` at live boot, so anything
// the kernel cannot match is an unbootable stick.
// grubThemeFor maps a profile to its branding/grub/<dir> theme: branding/grub/river, the one
// Runink River theme, for every profile (a downstream edition brands its live menu with its
// own grub/theme/, see grubThemeDir).
func grubThemeFor(string) string { return "river" }

func brandFor(profile string) (isoLabel, grubTitle string) {
	// An external profile (river-profile.env, read by build/profile-lib.sh and passed on by
	// scripts/build-iso-box.sh) names its own label and title; they override the defaults.
	defer func() {
		if v := os.Getenv("RIVER_ISO_LABEL"); v != "" {
			isoLabel = mustMatch("RIVER_ISO_LABEL", v, reLabel)
		}
		if v := os.Getenv("RIVER_GRUB_TITLE"); v != "" {
			grubTitle = mustMatch("RIVER_GRUB_TITLE", v, reTitle)
		}
	}()
	switch profile {
	case "river":
		return "RIVER", "Runink River"
	default:
		// An unknown profile gets a label derived from its name rather than silently
		// inheriting the server's — inheriting is exactly the collision this fixes.
		l := strings.ToUpper(strings.ReplaceAll(profile, "-", "_"))
		return l, profile
	}
}

func main() {
	if len(os.Args) > 1 {
		if os.Args[1] == "pacman-conf" && len(os.Args) == 3 {
			patchPacmanConf(os.Args[2])
			return
		}
		// --print-label lets the build assert that the ISO it produced carries the label
		// this profile asked for. It resolves through the SAME brandFor() the patch uses,
		// so the check cannot drift from the thing it is checking.
		if os.Args[1] == "--print-label" {
			p := os.Getenv("RUNINK_PROFILE")
			if p == "" {
				p = "river"
			}
			l, _ := brandFor(p)
			fmt.Println(l)
			return
		}
		fmt.Fprintf(os.Stderr, "usage: %s [pacman-conf <path>] [--print-label]\n", os.Args[0])
		os.Exit(2)
	}

	// The ISO volume label and the live GRUB titles are derived from the PROFILE.
	//
	// This is not branding. Read the comment on the label patch below: iso_label is used
	// both as the -volid AND as the live `root=LABEL` kernel argument. Both profiles used
	// to bake RUNINK_SERVER (the server label before the RIVER rename), so a workstation ISO
	// booted `root=LABEL=RUNINK_SERVER` — and
	// with both sticks present (or a server stick left in while booting the workstation)
	// the kernel resolves that label to whichever device it finds first and mounts the
	// WRONG image's rootfs. Two images cannot share one label.
	//
	// RUNINK_PROFILE is set by scripts/build-iso-box.sh, which already knows it. Default
	// is the in-tree profile, river (Runink River).
	profile := os.Getenv("RUNINK_PROFILE")
	if profile == "" {
		profile = "river"
	}
	isoLabel, grubTitle := brandFor(profile)
	fmt.Printf("branding for profile %q: label=%s title=%q\n", profile, isoLabel, grubTitle)

	// 1. common.yaml
	const common = "/usr/share/artools/iso-profiles/common/common.yaml"
	s, mode := read(common)
	// linux-runink, not linux-lts, since 2026-09-06. Artix's linux-lts reached 6.18.49 and
	// NO available OpenZFS builds against it: archzfs zfs-dkms is 2.3.3, and its prebuilt
	// zfs-linux-lts targets 6.12. Both shipped ISOs therefore had no ZFS module at all
	// (os#70). The repo's own matched pair is the answer: linux-runink (zen-kernel 7.2.x)
	// + runink-zfs (OpenZFS 2.4.4 stable, declared range Linux 4.18..7.2).
	s = reLinux.ReplaceAllString(s, "${1}linux-runink")
	s = reLinuxHeaders.ReplaceAllString(s, "${1}linux-runink-headers")
	write(common, s, mode)
	fmt.Println("patched common.yaml: kernel -> linux-runink")

	// 2. initcpio.sh: derive the kernel version from the installed modules dir, not the
	//    mainline-only /usr/src/linux/version.
	//
	//    THE GLOB IS BOOT-CRITICAL. linux-runink installs
	//    /usr/lib/modules/7.2.7-zen1-1-runink/ — the suffix is "-runink", NOT "-lts". With
	//    the old "*-lts" glob and only a runink kernel present, find returns nothing, k is
	//    EMPTY, and artools builds the live initramfs against "" — an ISO that boots to
	//    nothing. Nothing downstream validates k, so it would have failed silently.
	const ic = "/usr/share/artools/lib/iso/initcpio.sh"
	const icOld = `k=$(<"$mnt"/usr/src/linux/version)`
	const icNew = `k=$(basename "$(find "$mnt"/usr/lib/modules -maxdepth 1 -mindepth 1 -type d -name "*-runink" | head -1)")`
	s, mode = read(ic)
	if strings.Contains(s, icOld) {
		write(ic, strings.ReplaceAll(s, icOld, icNew), mode)
		fmt.Println("patched initcpio.sh: kernel version from modules dir")
	} else {
		fmt.Fprintln(os.Stderr, "WARN: initcpio.sh pattern not found (already patched?)")
	}

	// 3. grub.sh — idempotence-guarded so re-running doesn't stack injections.
	const gs = "/usr/share/artools/lib/iso/grub.sh"
	s, mode = read(gs)
	pdir := profileDir(profile)
	grubInject := fmt.Sprintf(grubInjectFmt, pdir, grubTitle, grubThemeDir(profile, pdir))
	if strings.Contains(s, grubMarker) &&
		!strings.Contains(s, "runink: auto-boot") &&
		!strings.Contains(s, "COMMERCIAL REBRAND") {
		write(gs, strings.Replace(s, grubMarker, grubInject, 1), mode)
		fmt.Println("patched grub.sh: branded live menu + auto-boot (Runink titles, colours, default+timeout)")
	} else {
		fmt.Fprintln(os.Stderr, "WARN: grub.sh marker not found or already patched")
	}

	// 4. buildiso: brand the ISO VOLUME LABEL. It is used both as the -volid (the name shown
	//    when the stick is mounted) AND as the live root=LABEL kopt (initcpio.sh/dracut.sh
	//    derive `label=${iso_label}` from the same var), so changing it here keeps boot +
	//    mount in sync. A post-hoc relabel would desync those and break the live boot.
	//
	//    PER-PROFILE since 2026-09-05. Both profiles used to bake RUNINK_SERVER (pre-RIVER), so a
	//    workstation ISO booted `root=LABEL=RUNINK_SERVER`; with both sticks present the
	//    kernel resolves that to whichever it finds first and mounts the wrong rootfs.
	const bi = "/usr/bin/buildiso"
	const oldLabel = `iso_label="ARTIX_$(date +%Y%m)"`
	newLabel := fmt.Sprintf(`iso_label="%s"`, isoLabel)
	s, mode = read(bi)
	switch {
	case strings.Contains(s, oldLabel):
		write(bi, strings.Replace(s, oldLabel, newLabel, 1), mode)
		fmt.Printf("patched buildiso: iso_label ARTIX_YYYYMM -> %s\n", isoLabel)
	case strings.Contains(s, newLabel):
		fmt.Printf("buildiso iso_label already branded (%s)\n", isoLabel)
	default:
		// Reached when buildiso already carries a DIFFERENT profile's label — the
		// container is reused across builds, so this is a real possibility and silently
		// leaving the previous profile's label is the bug this change exists to fix.
		// Look for EITHER label family this tool bakes: RUNINK_* (workstation, and the
		// pre-rebrand server) or RIVER (the server since the rebrand, no RUNINK_ prefix).
		// Matching RUNINK_ alone would leave a reused container stuck on RIVER when it
		// next builds the workstation.
		i := strings.Index(s, `iso_label="RUNINK_`)
		if i < 0 {
			i = strings.Index(s, `iso_label="RIVER"`)
		}
		if i >= 0 {
			end := strings.Index(s[i:], "\"\n")
			if end < 0 {
				end = strings.Index(s[i:], "\"")
			}
			old := s[i : i+end+1]
			write(bi, strings.Replace(s, old, newLabel, 1), mode)
			fmt.Printf("patched buildiso: iso_label %s -> %s (re-branded for this profile)\n", old, newLabel)
		} else {
			fmt.Fprintln(os.Stderr, "WARN: buildiso iso_label line not found")
		}
	}

	// 4b. The ISO's EFI system partition label. artools formats efi.img as ARTIX_EFI, and that
	//     is the name a file manager shows for the stick's second partition, so every medium
	//     looked like an Artix one and two different sticks looked alike. It becomes
	//     <label>_EFI (FAT allows 11 characters: the ISO label is cut to 7 first), e.g.
	//     RIVER_EFI, RUNINK_EFI. Nothing boots by this label (GRUB finds the ISO by its volume
	//     label), so it is cosmetic but it is what the owner reads when a stick is plugged in.
	const gs2 = "/usr/share/artools/lib/iso/grub.sh"
	efiLabel := strings.TrimRight(isoLabel[:min(len(isoLabel), 7)], "_") + "_EFI"
	s, mode = read(gs2)
	if m := reEFILabel.FindString(s); m != "" {
		write(gs2, strings.Replace(s, m, "mkfs.fat -n "+efiLabel+" ", 1), mode)
		fmt.Printf("patched grub.sh: EFI partition label %s\n", efiLabel)
	} else {
		fmt.Fprintln(os.Stderr, "WARN: grub.sh has no `mkfs.fat -n <label>` line; the EFI partition keeps artools' label")
	}

	// 5. buildiso: the ISO FILE NAME. buildiso names it artix-<profile>-<init>-<date>-<arch>.iso
	//    (gen_iso_fn), which put "artix" and the init system in front of every Runink River
	//    image. The name is now the product's: runink-river-<date>-x86_64.iso (isoNameFor). The whole assignment is
	//    replaced, so a reused container re-brands for the profile at hand.
	isoLine := fmt.Sprintf(`iso_file="%s-${ISO_VERSION}-${arch}.iso"`, isoNameFor(profile))
	s, mode = read(bi)
	if m := reISOFile.FindString(s); m != "" {
		write(bi, strings.Replace(s, m, isoLine, 1), mode)
		fmt.Printf("patched buildiso: %s\n", isoLine)
	} else {
		fmt.Fprintln(os.Stderr, "patch-artools: buildiso has no iso_file= assignment; refusing to build an artix-* file name")
		os.Exit(1)
	}

	// 6. Tolerate what a profile does not install, and sync s6 before enabling. artools copies Artix's GRUB theme
	//    (artix-grub-theme) onto every ISO and memtest86+'s binary into /boot, and fails the
	//    build when either package is absent. A profile with its own common.yaml (a downstream
	//    server) may drop both, so each copy becomes conditional. A profile that still installs them keeps
	//    them on its ISO.
	optional := []struct{ file, old, new, what string }{
		{gs, `    cp -r "${theme}"/themes/artix "${grub}"/themes`,
			`    if [[ -d "${theme}"/themes/artix ]]; then cp -r "${theme}"/themes/artix "${grub}"/themes; fi`,
			"grub.sh: Artix GRUB theme optional"},
		{"/usr/share/artools/lib/iso/firmware.sh", `    cp "$src"/boot/memtest86+/memtest.bin "$dest"/memtest`,
			`    if [[ -f "$src"/boot/memtest86+/memtest.bin ]]; then cp "$src"/boot/memtest86+/memtest.bin "$dest"/memtest; fi`,
			"firmware.sh: memtest86+ optional"},
		// artools enables the live-session services BEFORE it syncs the s6 repository with the
		// stores, so a service that arrived with the root or live overlay (not with a package,
		// whose install hook syncs) is unknown to `s6 set enable`, and the build dies
		// ("river-perms is not a valid identifier"). Sync first.
		{"/usr/share/artools/lib/iso/services.sh", `    dep="$mnt"/etc/s6/sv/"$display_manager"-srv/dependencies.d
`,
			`    dep="$mnt"/etc/s6/sv/"$display_manager"-srv/dependencies.d
    chroot "$mnt" s6 repository sync # runink: overlay services must be known before enable
`,
			"services.sh: s6 repository sync before enabling"},
		// artools makes the display manager's s6 service depend on `artix-live` whenever a
		// display manager is among the live-session services, whether or not artix-live-s6 is
		// installed. Runink River installs no artix-live (the workstation's live autologin is
		// its own live-overlay SDDM drop-in), so on the first workstation ISO (2026-09-25) the
		// dependency was dangling: "s6-rc-compile: fatal: during dependency resolution for
		// service sddm-srv: undefined service name artix-live", and no live-session service
		// reached the boot database. The dependency is now added only when the service exists,
		// so an Artix profile that does install artix-live-s6 behaves exactly as upstream.
		{"/usr/share/artools/lib/iso/services.sh", `                if [[ -d "$dep" ]]; then
                    touch "$dep"/artix-live`,
			`                if [[ -d "$dep" ]] && [[ -d "$mnt"/etc/s6/sv/artix-live ]]; then # runink: only with artix-live-s6
                    touch "$dep"/artix-live`,
			"services.sh: display manager depends on artix-live only when it is installed"},
		// The live user's login shell. artools creates it with a fixed /bin/bash; a profile
		// that installs fish (Runink River: its terminal greeting, etc/fish/conf.d/
		// runink-greeting.fish) gives the live session the same shell an installed machine's
		// admin gets (installer/lib/50-runink-user.sh). A profile without fish keeps bash.
		{"/usr/share/artools/lib/iso/config.sh", `    chroot "$1" useradd -m -G "$grps" -s /bin/bash "${LIVEUSER}"`,
			`    chroot "$1" useradd -m -G "$grps" -s "$(if [[ -x "$1"/usr/bin/fish ]]; then echo /usr/bin/fish; else echo /bin/bash; fi)" "${LIVEUSER}" # runink: fish when installed`,
			"config.sh: the live user's shell is fish when the profile installs it"},
	}
	for _, o := range optional {
		s, mode = read(o.file)
		switch {
		case strings.Contains(s, o.new): // first: a patch may keep its original line
			fmt.Println("already patched " + o.what)
		case strings.Contains(s, o.old):
			write(o.file, strings.Replace(s, o.old, o.new, 1), mode)
			fmt.Println("patched " + o.what)
		default:
			fmt.Fprintf(os.Stderr, "patch-artools: %s: pattern not found; artools changed, update this patch\n", o.file)
			os.Exit(1)
		}
	}
}

// reISOFile matches buildiso's ISO file name assignment in prepare_build, stock
// (iso_file=$(gen_iso_fn).iso) or already re-branded by a previous run.
var reISOFile = regexp.MustCompile(`(?m)iso_file=.*\.iso"?$`)

// isoNameFor is the ISO file name prefix: runink-river for the in-tree profile (river),
// runink-river-<profile> for any other profile without RIVER_ISO_NAME.
func isoNameFor(profile string) string {
	if v := os.Getenv("RIVER_ISO_NAME"); v != "" {
		return mustMatch("RIVER_ISO_NAME", v, reISOName)
	}
	if profile == "river" {
		return "runink-river"
	}
	return "runink-river-" + profile
}

// The values an external profile may set reach a shell script (grub.sh), a sed expression and
// the ISO volume id, so they are held to what those can carry unquoted.
var (
	reLabel = regexp.MustCompile(`^[A-Z0-9_]{1,32}$`)
	// artools' `mkfs.fat -n ARTIX_EFI "${efi_img}"`, or one this tool already re-labelled
	// (a reused builder container).
	reEFILabel = regexp.MustCompile(`mkfs\.fat -n [A-Z0-9_]{1,11} `)
	reTitle    = regexp.MustCompile(`^[A-Za-z0-9 ._+-]{1,60}$`)
	reISOName  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	rePath     = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
)

func mustMatch(name, v string, re *regexp.Regexp) string {
	if !re.MatchString(v) || strings.Contains(v, "..") {
		fmt.Fprintf(os.Stderr, "patch-artools: %s=%q is not allowed (%s)\n", name, v, re)
		os.Exit(1)
	}
	return v
}

// profileDir is the profile's directory inside the build container: the staged profile
// build/iso-root-stage.sh mounts (RIVER_PROFILE_DIR), else the checkout's iso-profiles/<profile>.
func profileDir(profile string) string {
	if v := os.Getenv("RIVER_PROFILE_DIR"); v != "" {
		return mustMatch("RIVER_PROFILE_DIR", v, rePath)
	}
	return "/os/iso-profiles/" + profile
}

// grubThemeDir is the live menu's GRUB theme: the profile's own grub/theme/ when it has one
// (how a downstream edition brands its live menu), else branding/grub/<grubThemeFor(profile)>.
func grubThemeDir(profile, pdir string) string {
	if _, err := os.Stat(pdir + "/grub/theme/theme.txt"); err == nil {
		return pdir + "/grub/theme"
	}
	return "/os/branding/grub/" + grubThemeFor(profile)
}
