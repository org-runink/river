#!/bin/sh
# 40-boot-grub-zfs — hostid, kernel + initramfs (with hostid baked) on the ESP, GRUB for an
# encrypted ZFS root. GRUB reads only the FAT ESP at /boot; the initramfs `zfs` hook imports
# the pool and loads its key (key providers, else the console passphrase prompt).
#
# The pool imports by /etc/hostid; mkinitcpio bakes it into the initramfs so early boot's
# import matches. GRUB is installed for UEFI with ZFS-root support. Runs mostly in a chroot
# on the target.
set -eu

TARGET="${RUNINK_TARGET:?}"
POOL="${RUNINK_POOL:?}"
BE="${RUNINK_BE:?}"

# 1. hostid — the target MUST boot on the hostid the POOL carries, or the early-boot
#    zfs import fails ("pool was last accessed by another system") → no root → PID1
#    exits → kernel panic. Derive it from the (now-imported) pool itself rather than
#    assuming the live-env value: a coexistence install into an existing pool must adopt
#    THAT pool's owner hostid — baking the live-env hostid (the old behaviour) strands
#    the node on a mismatch. A fresh pool carries the live-env hostid stamped at create
#    (10-disk-zfs ensures it is non-zero), so reading it back covers both cases.
echo "boot: matching /etc/hostid to pool $POOL"
pool_hostid="$(zdb -C "$POOL" 2>/dev/null | awk '$1=="hostid:"{print $2; exit}')"
if [ -n "${pool_hostid:-}" ] && [ "$pool_hostid" != "0" ] && command -v zgenhostid >/dev/null 2>&1; then
	pool_hostid_hex="$(printf '0x%08x' "$pool_hostid")"
	echo "boot:   pool hostid $pool_hostid_hex → target /etc/hostid"
	zgenhostid -f -o "$TARGET/etc/hostid" "$pool_hostid_hex"
elif command -v zgenhostid >/dev/null 2>&1; then
	echo "boot:   pool reports no hostid; falling back to live-env hostid $(hostid)"
	zgenhostid -f -o "$TARGET/etc/hostid" "$(hostid)"
else
	cp -f /etc/hostid "$TARGET/etc/hostid" 2>/dev/null || true
fi

# 2. chroot to build initramfs + install GRUB.
chroot_run() { chroot "$TARGET" /bin/sh -c "$1"; }

# Bind mounts for the chroot.
for fs in dev proc sys; do mount --rbind "/$fs" "$TARGET/$fs"; done
mount --make-rslave "$TARGET/dev" 2>/dev/null || true

# /boot is the FAT ESP (10-disk-zfs), so it starts EMPTY: 20-clone-rootfs excludes the live
# /boot. Install each kernel image the way mkinitcpio's pacman hook does on an upgrade —
# /usr/lib/modules/<rel>/vmlinuz -> /boot/vmlinuz-<pkgbase> — so the preset finds it and GRUB
# (which can no longer read the encrypted pool) finds it on FAT. Plain cp: FAT has no
# ownership, and a chmod onto vfat fails.
if ! findmnt -n "$TARGET/boot" >/dev/null 2>&1; then
	echo "boot: $TARGET/boot is not a mount — the ESP must be mounted there (10-disk-zfs)" >&2
	exit 1
fi
n_kernels=0
for pb in "$TARGET"/usr/lib/modules/*/pkgbase; do
	[ -f "$pb" ] || continue
	kdir="${pb%/pkgbase}"
	[ -f "$kdir/vmlinuz" ] || continue
	cp -f "$kdir/vmlinuz" "$TARGET/boot/vmlinuz-$(cat "$pb")"
	n_kernels=$((n_kernels + 1))
	echo "boot:   kernel $(basename "$kdir") -> /boot/vmlinuz-$(cat "$pb") (ESP)"
done
[ "$n_kernels" -gt 0 ] || { echo "boot: no kernel image under $TARGET/usr/lib/modules" >&2; exit 1; }

# mkinitcpio (reads /etc/mkinitcpio.conf.d/zfs.conf from the overlay; bakes hostid). The
# `zfs` hook also copies every regular file in /etc/zfs/initramfs-tools-load-key.d/ into the
# image and sources them before it prompts for the pool passphrase — the key-provider
# interface (docs/ENCRYPTION.md). The image lands on the ESP: it holds code and the hostid,
# never key material (a provider may carry a TPM-SEALED blob, which is useless off this TPM).
echo "boot: mkinitcpio -P (linux-runink preset; bakes zfs hook + hostid + key providers)"
chroot_run "mkinitcpio -P"

# GRUB: boots from the ESP; the ZFS root is imported and unlocked by the initramfs.
# --removable ALSO writes the UEFI removable-media fallback (EFI/BOOT/BOOTX64.EFI) so the
# appliance boots on ANY UEFI without a firmware NVRAM boot entry — essential for a headless
# sovereign node (fresh hardware / after an NVRAM reset) and for VM validation (a clean OVMF
# has no 'runink' boot var, so without this it falls through to PXE and never boots).
echo "boot: installing GRUB (UEFI, ZFS root, removable fallback)"
#
# --efi-directory=/boot: the ESP is mounted at /boot (encrypted pool, see above), so GRUB's
# core image, modules, fonts, theme and grub.cfg all live on FAT and GRUB never touches ZFS.
# UNTESTED ON HARDWARE: this layout has not been booted yet (see docs/ENCRYPTION.md).
chroot_run "grub-install --target=x86_64-efi --efi-directory=/boot --bootloader-id=runink --removable --recheck"

# GRUB cmdline: root=ZFS=<pool>/ROOT/<be> + kernel memory-safety hardening + zfs_force.
# grub-mkconfig sources /etc/default/grub.d/*.cfg — create the dir BEFORE writing the drop-in.
#
# HARDEN flags (all safe with unsigned out-of-tree modules — they do NOT gate module loading,
# unlike lockdown/module.sig_enforce): slab_nomerge (no slab-cache merge → removes a heap
# cross-cache primitive); init_on_alloc=1 / init_on_free=1 (zero heap on alloc/free → infoleak
# + UAF mitigation); randomize_kstack_offset=1 (per-syscall kernel-stack offset randomization).
# NOT set: lockdown=integrity / module.sig_enforce=1 — the ZFS root module (runink-zfs, an
# OOT module whose signing is best-effort until the staged v3) loads from the initramfs;
# lockdown-integrity would refuse an unsigned one and PANIC the import. (See
# build/pkgbuilds/runink-kernel/README.md, "Staged rollout".)
#
# zfs_force=1: the initramfs zfs hook force-imports regardless of a hostid mismatch. This is a
# single-node sovereign box that solely owns its pool, so multihost protection has no value —
# but an unclean power-cycle (this box crashes under load) otherwise leaves the pool "in use by
# another system" and panics PID1 at boot. Belt-and-suspenders with the matched hostid above.
HARDEN="slab_nomerge init_on_alloc=1 init_on_free=1 randomize_kstack_offset=1"
mkdir -p "$TARGET/etc/default/grub.d"
# RIVER boot branding. Everything here is DRIVEN BY WHAT THE PROFILE SHIPPED into the
# target, so this one step (byte-identical across profiles, see `river lint installer-sync`)
# does the right thing for each without knowing which profile it is running for:
#
#   usr/share/runink/branding/grub/river/theme.txt   both profiles  -> GRUB_THEME
#   usr/share/runink/branding/grub-bg.png            fallback only  -> GRUB_BACKGROUND
#   usr/share/runink/branding/console/vt-palette.cmdline   server   -> console palette
#   usr/share/plymouth/themes/river + /usr/bin/plymouth    workstation -> quiet splash
#
# /boot/grub is on the ESP (FAT — GRUB cannot read the encrypted pool), so the theme is
# copied there (grub-mkconfig then emits `insmod png` + the theme's `loadfont`s itself).
# GRUB cannot animate: the theme's only motion is the __timeout__ progress bar draining
# over GRUB_TIMEOUT, which is why a finite timeout with the menu shown is set with it.
BRAND="$TARGET/usr/share/runink/branding"
brand_grub=""
if [ -f "$BRAND/grub/river/theme.txt" ]; then
	rm -rf "$TARGET/boot/grub/themes/river"
	mkdir -p "$TARGET/boot/grub/themes"
	cp -R "$BRAND/grub/river" "$TARGET/boot/grub/themes/river"
	brand_grub='GRUB_THEME="/boot/grub/themes/river/theme.txt"
GRUB_GFXMODE=auto
GRUB_TERMINAL_OUTPUT=gfxterm
GRUB_TIMEOUT=5
GRUB_TIMEOUT_STYLE=menu'
elif [ -f "$BRAND/grub-bg.png" ]; then
	mkdir -p "$TARGET/boot/grub"
	cp -f "$BRAND/grub-bg.png" "$TARGET/boot/grub/runink-bg.png"   # FAT: no chmod
	brand_grub='GRUB_BACKGROUND="/boot/grub/runink-bg.png"'
fi
# The server's GRUB -> console hand-off: the kernel applies this 16-colour palette to every
# VT from its first line of output, so the console continues the GRUB theme's colours.
brand_vt=""
if [ -f "$BRAND/console/vt-palette.cmdline" ]; then
	brand_vt=" $(tr -d '\n' < "$BRAND/console/vt-palette.cmdline")"
fi
# The workstation's splash. Needs the `plymouth` mkinitcpio hook too, which that profile's
# own etc/mkinitcpio.conf.d/zfs.conf adds; gfxpayload=keep avoids a mode switch flicker.
brand_splash=""
brand_payload=""
if [ -f "$TARGET/usr/share/plymouth/themes/river/river.plymouth" ] && [ -x "$TARGET/usr/bin/plymouth" ]; then
	brand_splash=" quiet splash"
	brand_payload="GRUB_GFXPAYLOAD_LINUX=keep"
fi
cat > "$TARGET/etc/default/grub.d/10-runink-zfs.cfg" <<EOF
GRUB_CMDLINE_LINUX="\$GRUB_CMDLINE_LINUX root=ZFS=$POOL/ROOT/$BE rw $HARDEN zfs_force=1$brand_vt$brand_splash"
GRUB_PRELOAD_MODULES="\$GRUB_PRELOAD_MODULES part_gpt fat"
# No UUID root: the ZFS root is named by root=ZFS= above. grub-probe cannot read an
# encrypted pool, so grub-mkconfig sees the root fs as "unknown" and emits a plain
# root=<dataset> ahead of GRUB_CMDLINE_LINUX; the later root=ZFS= wins in the initramfs.
GRUB_DISABLE_LINUX_UUID=true
$brand_grub
$brand_payload
EOF

# A SECOND menu entry for the same kernel and initramfs, differing only in how the machine
# talks to whoever is trying to fix it: no splash, full kernel log, and a serial console as
# well as the screen.
#
# This exists because of river#11. An installed workstation booted to a black screen on every
# console — graphical and text — and the only way to find out why was to take the disk apart
# from a live medium, because the installed system had no way to say anything. The splash hid
# the log, and there was no serial console to watch. One menu entry would have turned a
# multi-hour forensic exercise into reading a boot log.
#
# Written as a grub.d generator rather than a literal menuentry so it stays correct after a
# kernel upgrade: it re-resolves the ESP and re-globs the kernels every time grub-mkconfig
# runs. It is numbered 11, AFTER 10_linux, so the normal entry stays first and GRUB_DEFAULT=0
# still boots the machine the way it is meant to boot. Nothing here changes the default boot.
#
# No serial getty is enabled with it: an agetty respawning against a port that does not exist
# is noise on every machine without one, and the boot log — which is what was missing — needs
# no login. The cloud profile, which always has a port, enables one in 78-cloud-target.
cat > "$TARGET/etc/grub.d/11_runink_debug" <<EOF
#!/bin/sh
# Emitted by installer step 40-boot-grub-zfs. See that step for why this entry exists.
# Prints a GRUB menu entry on stdout, the way every /etc/grub.d script does.
set -e
esp_uuid="\$(grub-probe --target=fs_uuid /boot 2>/dev/null)" || exit 0
[ -n "\$esp_uuid" ] || exit 0
for img in /boot/vmlinuz-*; do
	[ -f "\$img" ] || continue
	pkgbase="\${img#/boot/vmlinuz-}"
	initrd="/boot/initramfs-\$pkgbase.img"
	[ -f "\$initrd" ] || continue
	cat <<ENTRY
menuentry 'Runink (\$pkgbase) — verbose, serial console' --class runink --class gnu-linux --id 'runink-debug-\$pkgbase' {
	# No load_video: 00_header defines it only for a gfxterm boot, and this entry wants a
	# text console. An undefined command here would print an error on the one boot that
	# has to go smoothly.
	insmod gzio
	insmod part_gpt
	insmod fat
	search --no-floppy --fs-uuid --set=root \$esp_uuid
	echo 'Booting verbose, with a console on ttyS0 at 115200. No splash.'
	linux /vmlinuz-\$pkgbase root=ZFS=$POOL/ROOT/$BE rw $HARDEN zfs_force=1 console=tty1 console=ttyS0,115200n8 loglevel=7
	initrd /initramfs-\$pkgbase.img
}
ENTRY
done
EOF
chmod 0755 "$TARGET/etc/grub.d/11_runink_debug"

chroot_run "grub-mkconfig -o /boot/grub/grub.cfg"
# Prove the debug entry actually reached grub.cfg. A grub.d script that exits non-zero, or is
# not executable, is skipped by grub-mkconfig with a line on stderr and nothing else — and the
# entry would then be missing on exactly the boot that needed it, with nobody the wiser.
if ! grep -q 'runink-debug-' "$TARGET/boot/grub/grub.cfg"; then
	echo "boot: WARNING: no verbose/serial entry in grub.cfg — a black screen will not be diagnosable" >&2
fi

# Cleanup bind mounts.
for fs in sys proc dev; do umount -R "$TARGET/$fs" 2>/dev/null || true; done

echo "boot: GRUB + initramfs configured for ZFS root $POOL/ROOT/$BE"
