#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# memtune.sh — the install plan's memory sizes, applied to the target. SOURCED by
# 30-target-config (and by `river test memtune`), never run as a step.
#
# The plan (docs/INSTALLER-HARDWARE.md, "Swap, ARC, GPU, TPM, network") reaches the steps as
# two variables that `river-plan --env` prints and both drivers export:
#
#   RUNINK_ZFS_ARC_MAX  bytes   zfs.arc_max_bytes -> /etc/modprobe.d/zfs.conf (options zfs
#                               zfs_arc_max=N); the initramfs carries the file, because the
#                               zfs module loads there on a ZFS root.
#   RUNINK_ZRAM_SIZE    <N>M    swap.zram_mib     -> /etc/runink/zram.conf, which
#                               runink-zram.sh reads at every boot.
#
# Without a plan (a driver that resolves none): the ARC falls back to the planner's own rule,
# clamp(MemTotal/16, 1 GiB, 16 GiB), computed from /proc/meminfo of the machine being
# installed, which is the machine that will run it. OpenZFS's own default (most of RAM on
# Linux) is sized for a file server, not for a desktop, so it is never left in place. The
# zram size has no fallback: no zram.conf is written and runink-zram.sh keeps the image's
# default.

MEMTUNE_MEMINFO="${MEMTUNE_MEMINFO:-/proc/meminfo}"
# The planner's clamp (installer/internal/planner/plan.go: ARCMinMiB, ARCMaxMiB).
MEMTUNE_ARC_MIN_MIB=1024
MEMTUNE_ARC_MAX_MIB=16384

# memtune_arc_bytes — prints zfs_arc_max in bytes: the plan's, else the meminfo fallback.
# Fails (prints nothing) only when neither is usable.
memtune_arc_bytes() {
	case "${RUNINK_ZFS_ARC_MAX:-}" in
	'' | *[!0-9]* | 0) ;;
	*)
		echo "$RUNINK_ZFS_ARC_MAX"
		return 0
		;;
	esac
	[ -z "${RUNINK_ZFS_ARC_MAX:-}" ] ||
		echo "memtune: RUNINK_ZFS_ARC_MAX='$RUNINK_ZFS_ARC_MAX' is not a byte count; using the RAM/16 rule" >&2
	_kb="$(awk '/^MemTotal:/{print $2; exit}' "$MEMTUNE_MEMINFO" 2>/dev/null)"
	case "$_kb" in '' | *[!0-9]*) return 1 ;; esac
	_mib=$((_kb / 1024 / 16))
	[ "$_mib" -ge "$MEMTUNE_ARC_MIN_MIB" ] || _mib="$MEMTUNE_ARC_MIN_MIB"
	[ "$_mib" -le "$MEMTUNE_ARC_MAX_MIB" ] || _mib="$MEMTUNE_ARC_MAX_MIB"
	echo $((_mib * 1048576))
}

# memtune_write_arc TARGET BYTES — set zfs_arc_max in TARGET/etc/modprobe.d/zfs.conf.
# Merges: every other option on an `options zfs` line, and every other line, is kept; only an
# earlier zfs_arc_max (and this function's own comment) is replaced, so a re-run is a no-op.
memtune_write_arc() {
	_conf="$1/etc/modprobe.d/zfs.conf"
	mkdir -p "$1/etc/modprobe.d"
	[ -f "$_conf" ] || : > "$_conf"
	awk '
		/^# zfs_arc_max: the install plan/ { next }
		$1 == "options" && $2 == "zfs" {
			out = "options zfs"; n = 0
			for (i = 3; i <= NF; i++) if ($i !~ /^zfs_arc_max=/) { out = out " " $i; n++ }
			if (n) print out
			next
		}
		{ print }
	' "$_conf" > "$_conf.new"
	{
		echo "# zfs_arc_max: the install plan's zfs.arc_max_bytes (runink-install, 30-target-config)"
		echo "options zfs zfs_arc_max=$2"
	} >> "$_conf.new"
	chmod 0644 "$_conf.new"
	mv "$_conf.new" "$_conf"
}

# memtune_zram_valid SIZE — a size zramctl takes (digits with an optional K/M/G/T), or `ram`.
memtune_zram_valid() {
	case "$1" in
	ram) return 0 ;;
	'' | *[!0-9KMGT]* | [!0-9]* | *[KMGT]*[0-9KMGT]*) return 1 ;;
	esac
	return 0
}

# memtune_write_zram TARGET SIZE — TARGET/etc/runink/zram.conf, root 0600 like everything in
# /etc/runink (river-perms re-asserts the modes).
memtune_write_zram() {
	memtune_zram_valid "$2" || {
		echo "memtune: zram size '$2' is not a size; zram.conf not written" >&2
		return 1
	}
	_d="$1/etc/runink"
	mkdir -p "$_d"
	chmod 0700 "$_d"
	_umask="$(umask)"
	umask 077
	{
		echo "# The install plan's swap.zram_mib (runink-install, 30-target-config); read by"
		echo "# runink-zram.sh at boot. RUNINK_ZRAM_SIZE in its environment still overrides it."
		echo "RUNINK_ZRAM_SIZE=$2"
	} > "$_d/zram.conf.new"
	umask "$_umask"
	mv "$_d/zram.conf.new" "$_d/zram.conf"
}
