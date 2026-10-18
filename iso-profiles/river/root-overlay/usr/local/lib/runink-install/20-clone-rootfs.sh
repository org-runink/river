#!/bin/sh
# 20-clone-rootfs — populate the ZFS target from the LIVE rootfs (install-from-live).
#
# The lean ISO's live rootfs IS the golden system (base + s6 + zfs + our
# root-overlay config, all already installed at build time). So the install is a clone of
# the running live system to the ZFS boot environment — SELF-CONTAINED: no package repo, no
# network, no version pinning at install time. This replaces the old pacstrap step (which
# needed the [runink] repo + mirrors on the target).
set -eu

TARGET="${RUNINK_TARGET:?}"
[ -d "$TARGET" ] || { echo "clone: target $TARGET not mounted" >&2; exit 1; }

echo "clone: rsync live rootfs -> $TARGET (this is the bulk of the install)"
# /boot/* is excluded: $TARGET/boot is the FAT ESP (10-disk-zfs), which cannot hold the
# ownership/ACLs/xattrs -aHAXS preserves, and nothing in the live /boot belongs on the node.
# 40-boot-grub-zfs installs the kernel image from /usr/lib/modules and builds the initramfs.
rsync -aHAXS --numeric-ids --info=progress2 \
  --exclude='/proc/*'  --exclude='/sys/*'   --exclude='/dev/*'   --exclude='/run/*' \
  --exclude='/tmp/*'   --exclude='/mnt/*'   --exclude='/media/*' --exclude='/lost+found' \
  --exclude='/var/lib/artools/*' \
  --exclude='/run/archiso' --exclude='/bootmnt' \
  --exclude='/boot/*' \
  --exclude='/etc/runink/enrollment.env' \
  --exclude='/var/lib/runink/.enrolled' \
  --exclude='/usr/local/bin/runink-install' \
  --exclude='/usr/local/lib/runink-install' \
  --exclude='/usr/local/bin/river-guide' --exclude='/usr/local/bin/river-guide-tty' \
  --exclude='/usr/local/bin/river-guide-model' --exclude='/usr/lib/river-guide' \
  --exclude='/usr/share/river-guide' --exclude='/etc/river-guide' \
  --exclude='/etc/s6/sv/river-guide-model' --exclude='/var/log/river-guide' \
  --exclude='/usr/local/lib/river-pair' --exclude='/var/log/river-pair' \
  --exclude='/usr/share/river/installer' --exclude='/etc/s6/sv/river-installer' \
  --exclude='/usr/local/bin/river-installer-tty' --exclude='/etc/xdg/autostart/river-installer.desktop' \
  --exclude='/usr/local/bin/river-installer-desktop' --exclude='/var/log/river-installer' \
  --exclude='/etc/xdg/kscreenlockerrc' --exclude='/etc/xdg/powermanagementprofilesrc' \
  --exclude='/etc/xdg/weston' \
  / "$TARGET/"

# kscreenlockerrc and powermanagementprofilesrc: the LIVE session must not lock or blank,
# because somebody is copying a 64-character recovery key off the screen and the autologin user
# has no password they could use to get back in. An INSTALLED machine must do both. These are
# live-overlay files and the overlay is part of the running system this clones, so without an
# explicit exclude they would follow the install and leave every installed workstation unable
# to lock its screen. /etc/xdg/weston goes with them: the kiosk config has no meaning on a
# machine that has Plasma.

# Recreate the virtual-fs mountpoints the excludes emptied.
for d in proc sys dev run tmp mnt media; do mkdir -p "$TARGET/$d"; done
chmod 1777 "$TARGET/tmp"

# --- reset LIVE-only customisations so the installed system is a server, not a live CD ---
# The live ISO baked an autologin 'runink' live user (profile.yaml live-session) with a
# throwaway password. Remove it here so the installed system boots headless to a login; the
# real 'runink' service user (no autologin, proper subuid/groups) is created fresh by step
# 50-runink-user. Same name, but sequential: this delete runs before 50 recreates it.
if [ -f "$TARGET/etc/passwd" ] && grep -q '^runink:' "$TARGET/etc/passwd"; then
  echo "clone: removing the live 'runink' autologin account from the target (step 50 recreates it)"
  chroot "$TARGET" /usr/sbin/userdel -r runink 2>/dev/null || true
fi
# Drop any live autologin drop-ins (agetty --autologin), keep our sudoers/elogind/etc.
# The server's live overlay logs tty2 in through ARGS="--autologin runink" in
# /etc/s6/config/tty2.conf; an installed node must boot to a login prompt on every tty.
rm -f "$TARGET"/etc/s6/sv/*/env/autologin 2>/dev/null || true
find "$TARGET/etc" -name '*autologin*' -path '*getty*' -delete 2>/dev/null || true
for _c in "$TARGET"/etc/s6/config/tty*.conf; do
  [ -f "$_c" ] && sed -i '/^ARGS=/{s/--autologin[= ][^ "]*//; s/--noclear//; s/=" */="/; s/ *"$/"/}' "$_c"
done
# The workstation's live overlay logs the live user into Plasma (an SDDM [Autologin] drop-in)
# and gives it passwordless sudo. An installed machine boots to the SDDM greeter, and its admin
# uses sudo with their own password (root-overlay/etc/sudoers.d/10-wheel).
rm -f "$TARGET/etc/sddm.conf.d/50-live-autologin.conf" "$TARGET/etc/sudoers.d/90-live-nopasswd"
# Its Plasma autostart opens river-netsetup (network first) in Konsole; an installed machine
# does not. river-netsetup itself stays (runink-installer) for reconfiguring the network.
rm -f "$TARGET/etc/xdg/autostart/river-netsetup.desktop"
if grep -rqs '^[[:space:]]*User=[^[:space:]]' "$TARGET/etc/sddm.conf" "$TARGET/etc/sddm.conf.d"; then
  echo "clone: an SDDM autologin user survived on the target:" >&2
  grep -rs '^[[:space:]]*User=' "$TARGET/etc/sddm.conf" "$TARGET/etc/sddm.conf.d" >&2
  exit 1
fi
# artools makes the display manager depend on artix-live when that service is installed.
# Neither profile installs it; should a future one, its dependency must not outlive it here.
rm -f "$TARGET"/etc/s6/sv/*-srv/dependencies.d/artix-live
# river-guide (the install guide agent) is live-medium only: its files were excluded above,
# its guide model never leaves the medium, and the audit log of the install session stays
# in the live RAM overlay. Drop its package record and give every tty its ordinary getty back
# (the live overlay pointed tty1 at the graphical installer and tty3 at the guide).
# 80-enable-s6 disables their s6 services in the target's database; should one ever run, it
# finds no program and stays down.
for _c in "$TARGET"/etc/s6/config/tty*.conf; do
  if [ -f "$_c" ]; then sed -i 's|^GETTY=.*river-.*|GETTY="agetty"|' "$_c"; fi
done
# The livefs layer's packages (Packages-Live) are live-only too; runink-grub-live is the live
# ISO's GRUB scaffolding and means nothing on a node. artix-grub-live (what runink-grub-live
# replaced), artix-live-s6 and artix-live-base are in no Packages-Live; they are listed so that
# one arriving by accident never reaches a node.
for _p in river-guide runink-grub-live artix-grub-live artix-live-s6 artix-live-base; do
  chroot "$TARGET" pacman -Rdd --noconfirm --noscriptlet "$_p" >/dev/null 2>&1 || true
done
# The graphical installer's kiosk (a web view on the local display: the server live medium's
# Packages-Live) is live-only as well, with one exception: a server installed WITH the
# graphical first boot (RUNINK_FIRSTBOOT_UI=1) keeps it until that first boot finishes, whose
# Finish removes it again (pacman -Rns; 77-firstboot-ui records the list). Every other install
# drops the kiosk and everything only it needed here, so the node carries nothing that draws
# pixels (forbidden.closure).
KIOSK_LIST=/usr/share/river/installer/kiosk-packages
if [ -r "$KIOSK_LIST" ]; then
  _kp=""
  for _p in $(sed 's/#.*//' "$KIOSK_LIST"); do
    chroot "$TARGET" pacman -Q "$_p" >/dev/null 2>&1 && _kp="$_kp $_p"
  done
  if [ -n "$_kp" ] && [ "${RUNINK_FIRSTBOOT_UI:-0}" = 1 ]; then
    echo "clone: keeping the kiosk for the graphical first boot:$_kp (removed when it finishes)"
  elif [ -n "$_kp" ]; then
    echo "clone: removing the live kiosk from the target:$_kp"
    # shellcheck disable=SC2086 # a package list
    chroot "$TARGET" pacman -Rns --noconfirm $_kp >/dev/null
  fi
fi
for _f in passwd shadow group gshadow; do
  if [ -f "$TARGET/etc/$_f" ]; then sed -i '/^river-kiosk:/d' "$TARGET/etc/$_f"; fi
done
rm -rf "$TARGET/run/river-kiosk" "$TARGET/var/lib/river-kiosk"
# LAN-install pairing (river-pair-announce, docs/INSTALL.md "LAN installs") is live-only: its
# service template and session log were excluded above, and the session's pairing user,
# which exists in the live system while an operator drives this very install, must never
# reach a node.
for _f in passwd shadow group gshadow; do
  if [ -f "$TARGET/etc/$_f" ]; then sed -i '/^river-pair:/d' "$TARGET/etc/$_f"; fi
done
# A NEW machine-id for this machine (never the live medium's, which every install from the
# same medium would share). Nothing on an s6 system regenerates an empty one at boot, and D-Bus,
# elogind and the desktop read it. 32 lower-case hex digits, as machine-id(5) requires. SSH host
# keys regenerate on first boot (sshd-srv).
mid="$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')"
if [ "${#mid}" -eq 32 ]; then
  printf '%s\n' "$mid" > "$TARGET/etc/machine-id"
else
  : > "$TARGET/etc/machine-id" 2>/dev/null || true
fi
rm -f "$TARGET/var/lib/dbus/machine-id" 2>/dev/null || true
rm -f "$TARGET"/etc/ssh/ssh_host_* 2>/dev/null || true
# The baked app-image archive + stack manifest are large but belong on the target (first
# boot loads them) — they came across in the rsync; leave them.

echo "clone: done — target populated from the live golden rootfs"
