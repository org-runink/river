#!/bin/sh
# esp-fstab-repair.sh — MANUAL remediation for nodes installed before the installer wrote
# an ESP entry to /etc/fstab.
#
# NOT WIRED TO ANYTHING, BY DESIGN. The installer drivers (runink-install /
# runink-autoinstall) run a HARD-CODED list of installer/lib/NN-*.sh step names — a script
# outside that list can never be picked up — and nothing in rc.local, the s6 tree or
# firstboot references this file. Copy it to a node and run it deliberately, or paste the
# equivalent commands by hand.
#
# THE DEFECT IT REPAIRS
#   The ZFS datasets mount from pool properties, so an installed node boots fine with no
#   fstab. But /boot/efi is a plain directory on the boot environment: the ESP is mounted
#   only DURING the install, and no fstab entry was ever generated, so on the running node
#   /boot/efi is empty and the ESP is not mounted anywhere.
#   Impact is contained — GRUB refuses to install to a non-FAT --efi-directory, so a
#   bootloader write FAILS LOUDLY rather than silently landing in the empty dir — but the
#   ESP cannot be refreshed at all until it is mounted, and kernel/initramfs updates
#   (which land in /boot on ZFS, not on the ESP) mask the problem until someone needs it.
#
# WHAT IT DOES
#   report (default) : print the current state and the exact entry it WOULD add.
#   --apply          : add the /etc/fstab entry (idempotent) and mount /boot/efi.
#
# It never runs grub-install, never touches partitioning, the pool, or GRUB config.
# Safe to run on a node that is already correct: it re-writes the same line and exits 0.
#
#   sh esp-fstab-repair.sh            # report only
#   sudo sh esp-fstab-repair.sh --apply
set -eu

APPLY=0
case "${1:-}" in
	--apply) APPLY=1 ;;
	""|--report|--dry-run) APPLY=0 ;;
	*) echo "usage: $0 [--report|--apply]" >&2; exit 2 ;;
esac

FSTAB=/etc/fstab
MP=/boot/efi
EFI_GUID=c12a7328-f81f-11d2-ba4b-00a0c93ec93b   # GPT EFI System Partition type GUID

say() { echo "esp-fstab: $*"; }

# --- 1. current state --------------------------------------------------------
mounted_src="$(findmnt -n -o SOURCE "$MP" 2>/dev/null || true)"
if [ -n "$mounted_src" ]; then
	say "$MP is currently MOUNTED from $mounted_src"
else
	say "$MP is NOT mounted"
fi

if [ -f "$FSTAB" ] && awk '$2 == "'"$MP"'" { found = 1 } END { exit !found }' "$FSTAB"; then
	has_entry=1
	say "$FSTAB already has a $MP entry:"
	awk '$2 == "'"$MP"'" { print "  " $0 }' "$FSTAB"
else
	has_entry=0
	say "$FSTAB has NO $MP entry — this is the defect"
fi

# --- 2. identify the ESP -----------------------------------------------------
if [ -n "$mounted_src" ]; then
	esp="$mounted_src"
else
	# Exactly one GPT EFI System Partition, or refuse to guess.
	esp="$(lsblk -rno NAME,PARTTYPE 2>/dev/null \
		| awk -v g="$EFI_GUID" 'tolower($2) == g { print "/dev/" $1 }')"
	n="$(printf '%s\n' "$esp" | grep -c . || true)"
	if [ "$n" -ne 1 ]; then
		say "ERROR: found $n EFI System Partitions, refusing to guess. Candidates:"
		printf '%s\n' "$esp" | sed 's/^/  /'
		say "Re-run after mounting the right one at $MP, or edit $FSTAB by hand."
		exit 1
	fi
fi
say "ESP device: $esp"

esp_uuid="$(blkid -s UUID -o value "$esp" 2>/dev/null || true)"
if [ -n "$esp_uuid" ]; then esp_src="UUID=$esp_uuid"; else esp_src="$esp"; fi
line="$(printf '%s\t%s\tvfat\trw,noatime,fmask=0077,dmask=0077,nofail\t0 0' "$esp_src" "$MP")"

# Content already sitting in the (unmounted) directory would be SHADOWED by the mount —
# that is where a previous forced bootloader write would have landed. Report it, never
# delete it.
if [ -z "$mounted_src" ] && [ -d "$MP" ] && [ -n "$(ls -A "$MP" 2>/dev/null || true)" ]; then
	say "WARNING: $MP is non-empty while unmounted — mounting will shadow:"
	ls -A "$MP" | sed 's/^/  /'
	say "Inspect (and probably delete) that content AFTER mounting; it is on the ZFS root."
fi

if [ "$APPLY" -eq 0 ]; then
	say "REPORT ONLY. Would ensure this $FSTAB line:"
	echo "  $line"
	if [ "$has_entry" -eq 1 ] && [ -n "$mounted_src" ]; then
		say "node looks correct already."
	fi
	say "Re-run with --apply to write it and mount $MP."
	exit 0
fi

# --- 3. apply ----------------------------------------------------------------
[ "$(id -u)" -eq 0 ] || { say "ERROR: --apply needs root"; exit 1; }

[ -f "$FSTAB" ] || : > "$FSTAB"
cp -a "$FSTAB" "$FSTAB.bak.$(date +%Y%m%d%H%M%S)"
awk '$2 != "'"$MP"'"' "$FSTAB" > "$FSTAB.new"
printf '%s\n' "$line" >> "$FSTAB.new"
mv "$FSTAB.new" "$FSTAB"
say "wrote $FSTAB entry: $line"

mkdir -p "$MP"
if [ -z "$mounted_src" ]; then
	if mount "$MP"; then
		say "mounted $MP"
	else
		say "ERROR: mount $MP failed"
		exit 1
	fi
fi

findmnt "$MP" || { say "ERROR: $MP still not mounted"; exit 1; }
say "done. The ESP now mounts at boot (Artix s6 boot bundle runs mount-filesystems)."
say "If the ESP needs refreshing, run grub-install YOURSELF — this script never does."
