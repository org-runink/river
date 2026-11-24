#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# 38-cloud-headless — the cloud edition's package set: the workstation clone (20-clone-rootfs)
# minus everything that draws pixels or plays sound. Runs before 40-boot-grub-zfs so the
# initramfs and the kernel command line are built for a machine with no display. Only for
# RUNINK_CLOUD_PROFILE=base; the node profile (a downstream server) has no desktop to remove.
# docs/cloud/edition.md.
#
# The kernel, ZFS, s6, NetworkManager, OpenSSH, chrony and the developer toolchain stay: the
# image is the ISO's system without its desktop, not a different system.
set -eu

TARGET="${RUNINK_TARGET:?}"
: "${RUNINK_CLOUD:?38-cloud-headless: cloud images only}"
[ "${RUNINK_CLOUD_PROFILE:-node}" = base ] || { echo "headless: profile ${RUNINK_CLOUD_PROFILE:-node}: nothing to remove"; exit 0; }

# The roots of the desktop in iso-profiles/river/Packages-Root. `pacman -Rns` also removes the
# dependencies only these needed. libelogind stays (elogind itself needs it), and so does
# everything a kept package depends on: pacman refuses to remove those, and the loop below
# says which.
DESKTOP="runink-desk runink-wlroots0.18 greetd runink-greetd-s6 runink-cage runink-gtkgreet
mate-polkit network-manager-applet nm-connection-editor runink-bluetuith gnome-keyring
xdg-desktop-portal xdg-desktop-portal-wlr xdg-desktop-portal-gtk xorg-xwayland wdisplays grim
wl-clipboard libnotify librsvg gsfonts foot thunar tumbler gvfs udisks2 mousepad ristretto atril
engrampa parole firefox plymouth mesa vulkan-icd-loader vulkan-radeon vulkan-intel
libva-mesa-driver mesa-utils gst-plugins-base gst-plugins-good gst-plugins-bad gst-plugins-ugly
gst-libav ffmpeg intel-media-driver libva-utils pipewire pipewire-pulse pipewire-alsa
wireplumber pipewire-jack bluez-s6 bluez bluez-utils cups-s6 cups ttf-dejavu ttf-liberation
noto-fonts noto-fonts-emoji noto-fonts-cjk linux-firmware-amdgpu linux-firmware-intel"
# These must be gone, or the image is not headless; the rest is best effort.
MUST_GO="runink-desk greetd runink-greetd-s6 runink-cage runink-gtkgreet firefox plymouth"

chroot_run() { chroot "$TARGET" /bin/sh -c "$1"; }
mounted=""
cleanup() { for fs in $mounted; do umount -R "$TARGET/$fs" 2>/dev/null || true; done; }
trap cleanup EXIT
for fs in dev proc sys; do mount --rbind "/$fs" "$TARGET/$fs"; mounted="$fs $mounted"; done

installed=""
for p in $DESKTOP; do
	chroot "$TARGET" pacman -Q "$p" >/dev/null 2>&1 && installed="$installed $p"
done
echo "headless: ${installed:+removing}${installed:-nothing to remove}:$installed"
if [ -n "$installed" ]; then
	# One transaction first (it orders the removals); packages that something kept needs are
	# retried one at a time so one blocker does not keep the rest.
	# shellcheck disable=SC2086 # a package list
	if ! chroot_run "pacman -Rns --noconfirm $installed" >/dev/null 2>&1; then
		for p in $installed; do
			chroot "$TARGET" pacman -Q "$p" >/dev/null 2>&1 || continue
			chroot_run "pacman -Rns --noconfirm $p" >/dev/null 2>&1 || echo "headless: $p kept (a kept package needs it)"
		done
	fi
fi
for p in $MUST_GO; do
	if chroot "$TARGET" pacman -Q "$p" >/dev/null 2>&1; then
		echo "headless: $p is still installed" >&2
		exit 1
	fi
done

# Files the profile overlay put on the image for the desktop, which no package owns.
rm -rf "$TARGET/etc/greetd" "$TARGET/etc/xdg" "$TARGET/etc/pam.d/greetd" "$TARGET/etc/firefox" \
	"$TARGET/etc/plymouth" "$TARGET/usr/share/plymouth" "$TARGET/usr/share/wallpapers" \
	"$TARGET/usr/share/applications" "$TARGET/usr/share/icons/hicolor/scalable/apps/river"* 2>/dev/null || true
for s in greetd-srv greetd sddm-srv bluetoothd-srv cupsd-srv; do
	[ -e "$TARGET/etc/s6/sv/$s" ] && { echo "headless: s6 service $s is still on the image" >&2; exit 1; }
done

# The workstation's initramfs hooks name kms and plymouth, which are gone with the desktop; a
# hook that is not installed fails mkinitcpio. Later drop-ins win, so this one sorts after zfs.conf
# and 78-cloud-target's zz-river-cloud.conf adds the cloud's drivers on top.
install -d -m 0755 "$TARGET/etc/mkinitcpio.conf.d"
cat > "$TARGET/etc/mkinitcpio.conf.d/yy-river-headless.conf" <<'EOF'
# Cloud edition (installer step 38-cloud-headless): no display, so no kms and no splash.
HOOKS=(base udev autodetect microcode modconf keyboard keymap block zfs filesystems)
EOF
if grep -q 'plymouth\|kms' "$TARGET"/etc/mkinitcpio.conf.d/zz-*.conf 2>/dev/null; then
	echo "headless: a later drop-in names plymouth or kms" >&2; exit 1
fi
echo "headless: desktop removed; initramfs hooks without kms and plymouth"
