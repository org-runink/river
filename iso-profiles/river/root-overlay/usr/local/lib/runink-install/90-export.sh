#!/bin/sh
# 90-export — cleanly export the pool so it imports + mounts correctly on first boot.
#
# CRITICAL final step. The pool is created with `-R $TARGET` (altroot=/mnt) in 10-disk-zfs.
# If the installer leaves it IMPORTED, that /mnt altroot persists in the pool's on-disk
# state, and the first-boot initramfs then mounts the boot environment at the stale altroot
# instead of `/` — so init execs into a broken filesystem layout and the kernel panics
# ("Attempted to kill init"). A clean `zpool export` clears the altroot; the next import
# (the initramfs, with no altroot) mounts the BE at `/`. This is the standard closing step
# of every ZFS-root install — without it the installed system does not boot.
set -eu

POOL="${RUNINK_POOL:?}"
TARGET="${RUNINK_TARGET:?}"

# Unmount everything under the target, deepest path first (the ESP at /boot, /home, the BE,
# chroot binds). /boot/efi is kept for an install made by an older step 10.
umount "$TARGET/boot/efi" 2>/dev/null || true
umount "$TARGET/boot" 2>/dev/null || true
awk -v t="$TARGET" '$2 == t || index($2, t"/") == 1 { print $2 }' /proc/mounts \
	| sort -r | while read -r m; do
		umount "$m" 2>/dev/null || umount -l "$m" 2>/dev/null || true
	done
zfs umount -a 2>/dev/null || true

# Export the pool (retry with lazy-unmount fallback if something still holds it).
if ! zpool export "$POOL" 2>/dev/null; then
	sync; sleep 1
	zpool export -f "$POOL"
fi
# Exporting also unloads the encryption key: nothing of it survives on the target. The
# first boot's initramfs asks for it again (a key provider, else the console prompt).
echo "export: pool $POOL cleanly exported (altroot cleared) — ready for first boot"
