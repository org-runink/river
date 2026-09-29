#!/bin/sh
# 30-target-config — per-node config on the cloned target. The root-overlay config (s6 tree,
# sysctl, elogind, subuid, keepalive scripts, storage.conf, etc.) is ALREADY on the target
# — it came across in the install-from-live clone (20-clone-rootfs). This step only sets the
# node-specific bits the clone shouldn't carry verbatim.
set -eu

TARGET="${RUNINK_TARGET:?}"

# Hostname (installer prompt, default 'runink').
echo "${RUNINK_HOSTNAME:-runink}" > "$TARGET/etc/hostname"

# Record the golden-image version for banners / firstboot.
cp "${RUNINK_PROFILE_ROOT:-/run/archiso/bootmnt/runink}/VERSION" "$TARGET/etc/runink-os-version" 2>/dev/null \
	|| echo "runink-os" > "$TARGET/etc/runink-os-version"

# HARDENING: strip runtime fetch-tool BINARIES that arrive TRANSITIVELY on the deployed node.
# The manifest never requests curl/wget, but curl is pulled by NetworkManager (libcurl), and
# Artix's `curl` package bundles the CLI with the lib. Remove the binaries while leaving the
# shared libraries the network stack needs — a sovereign node keeps no fetch tooling
# (rsync-over-SSH is the only transfer path). libcurl.so.* stays; /usr/bin/curl does not.
for tool in curl wget; do
	if [ -f "$TARGET/usr/bin/$tool" ]; then
		rm -f "$TARGET/usr/bin/$tool" && echo "target-config: hardening — removed /usr/bin/$tool (shared libs kept)"
	fi
done

# --- /etc/fstab: the ESP, mounted at /boot -----------------------------------
# The ZFS datasets mount themselves from pool properties, so the node boots without an
# fstab at all — but the ESP needs an entry or the running node never mounts it again.
#
# Since ZFS native encryption the ESP is mounted at /boot, not /boot/efi: GRUB cannot read
# an encrypted pool, so the kernel, the initramfs and grub.cfg live on the ESP itself
# (10-disk-zfs.sh explains the choice over a separate unencrypted boot pool). This entry is
# therefore load-bearing for UPDATES, not for booting: firmware + GRUB find the ESP on their
# own, but without the mount a kernel upgrade writes its image and initramfs into the
# /boot DIRECTORY on the encrypted BE, where GRUB cannot see them, and the node keeps
# booting the old kernel — or none, once the old modules are gone.
#
# History: before this, /boot/efi was the mount point and a missing entry meant only that
# `grub-install --efi-directory=/boot/efi` aborted ("doesn't look like an EFI partition").
# Nodes installed that way keep /boot/efi; installer/remediation/esp-fstab-repair.sh is
# for them.
#
# `nofail` + fsck pass 0 on purpose: a replaced disk or a wiped ESP must degrade to "not
# mounted", never to a headless node stuck in a boot-time mount/fsck failure.
# fmask=0077,dmask=0077: root-only, like /etc/shadow — the ESP holds no secrets, but
# grub.cfg reveals the pool/BE layout and there is no reason for any user to read it.
esp_dev="$(findmnt -n -o SOURCE "$TARGET/boot" 2>/dev/null || true)"
if [ -n "$esp_dev" ]; then
	esp_uuid="$(blkid -s UUID -o value "$esp_dev" 2>/dev/null || true)"
	if [ -n "$esp_uuid" ]; then esp_src="UUID=$esp_uuid"; else esp_src="$esp_dev"; fi
	fstab="$TARGET/etc/fstab"
	[ -f "$fstab" ] || : > "$fstab"
	# Idempotent: drop any existing /boot or legacy /boot/efi entry (re-run / reinstall over
	# a pool) before appending, so the file never accumulates duplicate or stale-UUID lines.
	awk '$2 != "/boot" && $2 != "/boot/efi"' "$fstab" > "$fstab.new" && mv "$fstab.new" "$fstab"
	printf '%s\t/boot\tvfat\trw,noatime,fmask=0077,dmask=0077,nofail\t0 0\n' \
		"$esp_src" >> "$fstab"
	echo "target-config: fstab — $esp_src -> /boot (vfat, nofail)"
else
	echo "target-config: WARN — nothing mounted at $TARGET/boot; no ESP entry written to" \
		"$TARGET/etc/fstab. The installed node will not mount its ESP." >&2
fi

# --- the plan's memory sizes: ZFS ARC and zram ---------------------------------
# memtune.sh has the rules and the fallback. Written here, before 40-boot-grub-zfs runs
# mkinitcpio: the zfs module loads in the initramfs on a ZFS root, and runink-zfs's `zfs`
# hook copies /etc/modprobe.d/zfs.conf into it, so the limit is in force from the pool
# import on (and every later kernel or ZFS upgrade's mkinitcpio carries it again).
# A cloud image is built in a VM that is not the instance: nothing is sized for it here.
# shellcheck source=installer/lib/memtune.sh
. "$(dirname "$0")/memtune.sh"
if [ -n "${RUNINK_CLOUD:-}" ]; then
	echo "target-config: cloud image — ZFS ARC and zram left at the image defaults"
else
	if arc="$(memtune_arc_bytes)"; then
		memtune_write_arc "$TARGET" "$arc"
		echo "target-config: /etc/modprobe.d/zfs.conf — zfs_arc_max=$arc (RUNINK_ZFS_ARC_MAX='${RUNINK_ZFS_ARC_MAX:-}'; empty = RAM/16 rule)"
	else
		echo "target-config: WARN — no plan and /proc/meminfo unreadable; zfs_arc_max left at the ZFS default" >&2
	fi
	if [ -n "${RUNINK_ZRAM_SIZE:-}" ]; then
		memtune_write_zram "$TARGET" "$RUNINK_ZRAM_SIZE" &&
			echo "target-config: /etc/runink/zram.conf — RUNINK_ZRAM_SIZE=$RUNINK_ZRAM_SIZE (plan)"
	else
		rm -f "$TARGET/etc/runink/zram.conf"
		echo "target-config: no plan zram size — runink-zram.sh keeps the image default"
	fi
fi

# --- shadow-grade state/secret paths -----------------------------------------
# Every node secret or state file is handled like /etc/shadow: root:root, 0600 files in
# 0700 dirs. Set here (not only in 70-secrets-models) because runink-autoinstall skips 70.
# On the server profile the s6 oneshot river-perms re-asserts the same modes on every boot
# and logs any drift (root-overlay/usr/local/bin/river-perms holds the full table).
install -d -o root -g root -m 0700 "$TARGET/etc/runink" "$TARGET/var/lib/runink"
find "$TARGET/etc/runink" "$TARGET/var/lib/runink" -type f -exec chown root:root {} + -exec chmod 0600 {} +
find "$TARGET/etc/runink" "$TARGET/var/lib/runink" -mindepth 1 -type d -exec chown root:root {} + -exec chmod 0700 {} +
if [ -d "$TARGET/etc/zfs" ]; then chmod 0755 "$TARGET/etc/zfs"; fi
if [ -d "$TARGET/etc/zfs/initramfs-tools-load-key.d" ]; then
	chown -R root:root "$TARGET/etc/zfs/initramfs-tools-load-key.d"
	chmod 0700 "$TARGET/etc/zfs/initramfs-tools-load-key.d"
	find "$TARGET/etc/zfs/initramfs-tools-load-key.d" -type f -exec chmod 0600 {} +
fi
echo "target-config: /etc/runink, /var/lib/runink -> root:root 0700/0600"

echo "target-config: hostname=$(cat "$TARGET/etc/hostname"), config already present from the clone"
