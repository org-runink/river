#!/bin/sh
# runink-zram.sh — configure a zstd zram swap device. Runs at boot from rc.local. Idempotent.
#
# Swap on a Runink River machine is never on a disk or on the ZFS pool (swap on ZFS is
# discouraged); a compressed zram device in RAM is the only swap. systemd's zram-generator
# is unavailable on s6, so this drives zramctl directly.
#
# The size, first match wins:
#   1. RUNINK_ZRAM_SIZE in the environment   an operator's explicit override
#   2. RUNINK_ZRAM_SIZE= in /etc/runink/zram.conf
#                                            the install plan's swap.zram_mib, written by the
#                                            installer (30-target-config, installer/lib/memtune.sh):
#                                            RAM/2 below 32 GiB, else RAM/4, capped at 16 GiB
#   3. RUNINK_ZRAM_DEFAULT in the environment the image's default when there is no plan (rc.local
#                                            passes `ram`, which also covers the live medium)
#   4. 16G
# A size is what zramctl takes (digits with an optional K/M/G/T suffix) or `ram`: the
# device 1:1 with physical RAM, what systemd's zram-generator calls `zram-size = ram`.
# Sizing does not RESERVE memory: zram allocates pages only as they are swapped in, so the
# figure is a ceiling, not a cost.
#
#   runink-zram.sh               set the device up
#   runink-zram.sh --print-size  print the size it would use and exit (river test memtune)
set -u

PRIORITY="${RUNINK_ZRAM_PRIORITY:-100}"
CONF="${RUNINK_ZRAM_CONF:-/etc/runink/zram.conf}"

valid() {
	case "$1" in
	ram) return 0 ;;
	'' | *[!0-9KMGT]* | [!0-9]* | *[KMGT]*[0-9KMGT]*) return 1 ;;
	esac
	return 0
}

SIZE=""
FROM=""
if [ -n "${RUNINK_ZRAM_SIZE:-}" ]; then
	SIZE="$RUNINK_ZRAM_SIZE" FROM="environment"
elif [ -r "$CONF" ]; then
	# Parsed, not sourced: one key is read and nothing in the file runs.
	SIZE="$(sed -n "s/^RUNINK_ZRAM_SIZE=[\"']\{0,1\}\([^\"']*\)[\"']\{0,1\}[[:space:]]*$/\1/p" "$CONF" | tail -1)"
	FROM="$CONF"
fi
if [ -n "$SIZE" ] && ! valid "$SIZE"; then
	echo "runink-zram: size '$SIZE' from $FROM is not a size — ignored" >&2
	SIZE=""
fi
if [ -z "$SIZE" ]; then
	SIZE="${RUNINK_ZRAM_DEFAULT:-16G}" FROM="default"
	valid "$SIZE" || SIZE=16G
fi

if [ "$SIZE" = ram ]; then
	_kb="$(awk '/^MemTotal:/{print $2}' /proc/meminfo 2>/dev/null)"
	if [ -n "${_kb:-}" ] && [ "$_kb" -gt 0 ] 2>/dev/null; then
		SIZE="$((_kb / 1024))M"
	else
		echo "runink-zram: could not read MemTotal — falling back to 16G"
		SIZE=16G
	fi
fi

if [ "${1:-}" = --print-size ]; then
	echo "$SIZE"
	exit 0
fi

command -v zramctl >/dev/null 2>&1 || { echo "runink-zram: zramctl absent — skipping"; exit 0; }

# zswap off: it is a compressed cache IN FRONT OF a swap device (Documentation/admin-guide/
# mm/zswap.rst). linux-runink keeps it compiled in and on by default, which suits disk swap,
# but on this image the only swap is the zram device below, so zswap would compress a page
# and then write it back into zram to be compressed again. Done before the "already active"
# exit so a re-run still applies it; harmless if the zram device then fails (zswap does
# nothing without swap). tests/assert-golden.sh checks it.
if [ -w /sys/module/zswap/parameters/enabled ]; then
	echo N > /sys/module/zswap/parameters/enabled || echo "runink-zram: could not disable zswap"
fi

# Already active? (any zram device already used as swap)
if swapon --show=NAME --noheadings 2>/dev/null | grep -q '/dev/zram'; then
	echo "runink-zram: zram swap already active"
	exit 0
fi

modprobe zram 2>/dev/null || true

DEV="$(zramctl --find --size "$SIZE" --algorithm zstd 2>/dev/null)" || {
	echo "runink-zram: could not create zram device — skipping"; exit 0; }

mkswap "$DEV" >/dev/null 2>&1 || { echo "runink-zram: mkswap failed on $DEV"; exit 0; }
swapon --priority "$PRIORITY" "$DEV" 2>/dev/null || { echo "runink-zram: swapon failed on $DEV"; exit 0; }

echo "runink-zram: $DEV active ($SIZE zstd from $FROM, priority $PRIORITY)"
