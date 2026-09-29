#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# make-multiboot-usb.sh — one USB stick that boots SEVERAL ISOs (Runink River and, say, a
# downstream distribution's images), with the rest of the stick as an exFAT data partition.
# Layout and procedure: docs/USB-MULTIBOOT.md.
#
#   GPT  1    RUNINK_BOOT   EFI system partition, FAT32, 512 MiB: a standalone GRUB
#                           (EFI/BOOT/BOOTX64.EFI) and a menu with one entry per image
#        2..  <ISO label>   each ISO, written raw (its own ISO 9660 volume label), in order
#        last RUNINK_DATA   exFAT, the rest of the stick
#
# Each menu entry finds its image by the ISO's volume label and hands over to that ISO's own
# GRUB configuration, so each live system boots exactly as from its own stick (the live
# initramfs finds its root by the same label). The labels must therefore differ.
#
# Commands:
#   prepare-grub [--out DIR]
#       As your user: build the standalone GRUB EFI binary in the Artix builder container
#       (rootless podman). Default DIR: ~/.cache/river-build/usb.
#   write --iso ISO [--title TITLE] [--iso ISO [--title TITLE]]... --grub-efi FILE
#         --size-bytes N  TARGET
#       one to four ISOs, as the builds name them (Runink River: runink-river-<date>-x86_64.iso,
#       volume label RIVER). --title names the menu entry of the --iso before it (default:
#       "Runink River" for the label RIVER, else the label).
#       TARGET is either
#         --image-file FILE          a sparse image file (no root; for checking the layout)
#         --device /dev/sdX --serial-suffix XXXXXXXX
#                                    a real stick (root). REFUSES unless the device is a
#                                    whole, removable, USB disk of exactly N bytes whose serial
#                                    ends in XXXXXXXX (8 characters) and nothing on it is
#                                    mounted, used as swap or held by another device; then asks
#                                    you to type those 8 characters again. There is no flag to
#                                    skip that question.
#
# Every ISO is checked against its .sha256 before anything is written, and every image
# partition is read back and compared with its ISO afterwards.
set -eu

CACHE="${XDG_CACHE_HOME:-$HOME/.cache}"
MIB=1048576
BOOT_MIB=512
SLACK_MIB=64       # room after each ISO, so a slightly larger rebuild fits the same layout
MAX_ISOS=4

die() { echo "make-multiboot-usb: $*" >&2; exit 1; }
log() { echo "make-multiboot-usb: $*"; }
usage() { sed -n '5,38p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

# --- prepare-grub ------------------------------------------------------------------------
prepare_grub() {
	OUT="$CACHE/river-build/usb"
	while [ $# -gt 0 ]; do
		case "$1" in --out) OUT="${2:?}"; shift ;; *) usage ;; esac
		shift
	done
	[ "$(id -u)" -ne 0 ] || die "prepare-grub runs as your user"
	command -v podman >/dev/null 2>&1 || die "podman required"
	img="${BUILDER_IMAGE:-runink-os-builder}"
	podman image exists "$img" || die "no $img image (make builder)"
	mkdir -p "$OUT"
	# The embedded config only finds RUNINK_BOOT and reads its menu. The prefix stays in GRUB's
	# memdisk, where grub-mkstandalone put every module, so the ISOs' own configurations can
	# insmod whatever they need.
	printf '%s\n' 'insmod part_gpt' 'insmod part_msdos' 'insmod fat' 'insmod iso9660' \
		'search --no-floppy --label RUNINK_BOOT --set=root' \
		'configfile ($root)/boot/grub/grub.cfg' > "$OUT/embed.cfg"
	podman run --rm --network=none --user root -v "$OUT:/out" "$img" \
		grub-mkstandalone -O x86_64-efi -o /out/BOOTX64.EFI "boot/grub/grub.cfg=/out/embed.cfg"
	sha256sum "$OUT/BOOTX64.EFI"
	log "GRUB EFI binary: $OUT/BOOTX64.EFI"
}

# --- helpers --------------------------------------------------------------------------------
iso_label() { blkid -p -s LABEL -o value "$1" 2>/dev/null || true; }
size_of() { wc -c < "$1" | tr -d ' '; }
mib_up() { echo $(( ($1 + MIB - 1) / MIB )); }

check_iso() { # ISO — must carry a matching .sha256 and an ISO 9660 label
	[ -f "$1" ] || die "no ISO at $1"
	[ -f "$1.sha256" ] || die "no $1.sha256 next to the ISO (the build writes one)"
	want="$(cut -c1-64 "$1.sha256")"
	log "verifying $(basename "$1") against its .sha256"
	[ "$(sha256sum "$1" | cut -c1-64)" = "$want" ] || die "$1 does not match $1.sha256"
	[ "$(blkid -p -s TYPE -o value "$1")" = iso9660 ] || die "$1 is not an ISO 9660 image"
	[ -n "$(iso_label "$1")" ] || die "$1 has no volume label"
}

# write_grub_cfg OUTFILE — the RUNINK_BOOT menu, one entry per image (ISO_LABEL<i>, TITLE<i>).
write_grub_cfg() {
	{
		cat <<'HDR'
# RUNINK_BOOT menu (build/make-multiboot-usb.sh). Each entry finds an image by its ISO 9660
# volume label and runs that image's own GRUB configuration.
set timeout=15
set default=0
insmod part_gpt
insmod iso9660
insmod all_video
HDR
		i=1
		while [ "$i" -le "$N" ]; do
			eval "l=\$ISO_LABEL$i t=\$TITLE$i"
			printf '\nmenuentry "%s  (install / live)" {\n\tsearch --no-floppy --label %s --set=root\n\tconfigfile /boot/grub/grub.cfg\n}\n' "$t" "$l"
			i=$((i + 1))
		done
		printf '\nmenuentry "UEFI firmware settings" {\n\tfwsetup\n}\n'
	} > "$1"
}

# --- write -------------------------------------------------------------------------------------
write_usb() {
	N=0 GRUB_EFI="" SIZE="" IMAGE="" DEVICE="" SUFFIX=""
	while [ $# -gt 0 ]; do
		case "$1" in
			--iso)
				N=$((N + 1))
				[ "$N" -le "$MAX_ISOS" ] || die "at most $MAX_ISOS ISOs"
				eval "ISO$N=\${2:?}; TITLE$N="
				shift ;;
			--title)
				[ "$N" -gt 0 ] || die "--title follows the --iso it names"
				case "${2:?}" in *[!A-Za-z0-9\ ._+-]*|'') die "--title: letters, digits, spaces and . _ + - only" ;; esac
				eval "TITLE$N=\$2"
				shift ;;
			--grub-efi) GRUB_EFI="${2:?}"; shift ;;
			--size-bytes) SIZE="${2:?}"; shift ;;
			--image-file) IMAGE="${2:?}"; shift ;;
			--device) DEVICE="${2:?}"; shift ;;
			--serial-suffix) SUFFIX="${2:?}"; shift ;;
			*) usage ;;
		esac
		shift
	done
	[ "$N" -ge 1 ] && [ -n "$GRUB_EFI" ] && [ -n "$SIZE" ] || usage
	case "$SIZE" in *[!0-9]*) die "--size-bytes is a number of bytes" ;; esac
	[ -f "$GRUB_EFI" ] || die "no GRUB EFI binary at $GRUB_EFI (run: $0 prepare-grub)"
	for t in sfdisk mkfs.fat mkfs.exfat mcopy mmd blkid dd sha256sum; do
		command -v "$t" >/dev/null 2>&1 || die "$t required (util-linux, dosfstools, exfatprogs, mtools)"
	done
	if [ -n "$IMAGE" ] && [ -n "$DEVICE" ]; then die "--image-file or --device, not both"; fi
	[ -n "$IMAGE$DEVICE" ] || die "a TARGET is required: --image-file FILE or --device /dev/sdX"

	# ---- guards for a real device: all of them before anything is read or written ----
	if [ -n "$DEVICE" ]; then
		[ "$(id -u)" -eq 0 ] || die "writing a device needs root (sudo)"
		case "$DEVICE" in /dev/sd[a-z]|/dev/sd[a-z][a-z]) ;; *) die "--device must be a whole USB disk like /dev/sdb" ;; esac
		[ -b "$DEVICE" ] || die "$DEVICE is not a block device"
		[ "${#SUFFIX}" -eq 8 ] || die "--serial-suffix must be the LAST 8 characters of the stick's serial (lsblk -dno SERIAL $DEVICE)"
		guard_device
	else
		[ "$(id -u)" -ne 0 ] || die "--image-file runs as your user, not root"
		[ -z "$SUFFIX" ] || die "--serial-suffix applies to --device only"
	fi

	# ---- the ISOs: verified, labelled, labels distinct ----
	labels=" " iso="" t="" l="" m="" off=""
	used_mib=$(( 1 + BOOT_MIB + 1 ))
	i=1
	while [ "$i" -le "$N" ]; do
		eval "iso=\$ISO$i t=\$TITLE$i"
		check_iso "$iso"
		l="$(iso_label "$iso")"
		case "$labels" in *" $l "*) die "two ISOs carry the label $l; each live system finds its root by label, so they must differ" ;; esac
		labels="$labels$l "
		[ -n "$t" ] || { if [ "$l" = RIVER ]; then t="Runink River"; else t="$l"; fi; }
		m=$(( $(mib_up "$(size_of "$iso")") + SLACK_MIB ))
		eval "ISO_LABEL$i=\$l TITLE$i=\$t ISO_MIB$i=\$m"
		used_mib=$(( used_mib + m ))
		log "image $i: $(basename "$iso") label $l, ${m} MiB, menu \"$t\""
		i=$((i + 1))
	done

	# ---- layout (MiB-aligned) ----
	total_mib=$(( SIZE / MIB ))
	data_mib=$(( total_mib - used_mib - 1 ))   # the last MiB holds the backup GPT
	[ "$data_mib" -ge 1024 ] || die "the ISOs need ${used_mib} MiB; $SIZE bytes leaves no room for the data partition"
	log "layout: boot ${BOOT_MIB} MiB, $N image(s) $(( used_mib - BOOT_MIB - 2 )) MiB, data ${data_mib} MiB (of ${total_mib} MiB)"

	if [ -n "$DEVICE" ]; then
		confirm_device
		T="$DEVICE"
	else
		case "$IMAGE" in /tmp/*) log "WARNING: $IMAGE is probably on tmpfs" ;; esac
		rm -f "$IMAGE"
		truncate -s "$SIZE" "$IMAGE"
		T="$IMAGE"
	fi

	# ---- partition table ----
	{
		printf 'label: gpt\nunit: sectors\nfirst-lba: 2048\n'
		printf 'start=2048, size=%s, type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B, name="RUNINK_BOOT"\n' $(( BOOT_MIB * 2048 ))
		i=1
		while [ "$i" -le "$N" ]; do
			eval "m=\$ISO_MIB$i l=\$ISO_LABEL$i"
			printf 'size=%s, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name="%s"\n' $(( m * 2048 )) "$l"
			i=$((i + 1))
		done
		printf 'size=%s, type=EBD0A0A2-B9E5-4433-87C0-68B6B72699C7, name="RUNINK_DATA"\n' $(( data_mib * 2048 ))
	} | sfdisk --quiet --wipe always --no-reread --no-tell-kernel "$T"
	# Start sectors as sfdisk laid them out: P1 boot, P2.. the images, P<N+2> data.
	starts="$(sfdisk -d "$T" | sed -n 's/.*: start= *\([0-9]*\),.*/\1/p')"
	# shellcheck disable=SC2086 # a word list by construction
	set -- $starts
	[ $# -eq $(( N + 2 )) ] || die "expected $(( N + 2 )) partitions after sfdisk, found $#"
	k=1
	for s in "$@"; do eval "P$k=$(( s * 512 ))"; k=$((k + 1)); done
	DP=$(( N + 2 ))
	eval "PD=\$P$DP"

	B="" D=""
	if [ -n "$DEVICE" ]; then
		command -v partprobe >/dev/null 2>&1 && partprobe "$DEVICE" || blockdev --rereadpt "$DEVICE"
		command -v udevadm >/dev/null 2>&1 && udevadm settle
		k=1
		while [ "$k" -le "$DP" ]; do [ -b "$DEVICE$k" ] || die "$DEVICE$k did not appear"; k=$((k + 1)); done
		B="$DEVICE"1 D="$DEVICE$DP"
	fi

	# ---- 1 RUNINK_BOOT ----
	cfg="$(mktemp)"
	write_grub_cfg "$cfg"
	if [ -n "$DEVICE" ]; then
		mkfs.fat -F 32 -s 8 -n RUNINK_BOOT "$B" >/dev/null
		MT="$B"
	else
		mkfs.fat -F 32 -s 8 -n RUNINK_BOOT --offset=$(( P1 / 512 )) "$IMAGE" $(( BOOT_MIB * 1024 )) >/dev/null
		MT="$IMAGE@@$P1"
	fi
	export MTOOLS_SKIP_CHECK=1
	mmd -i "$MT" ::/EFI ::/EFI/BOOT ::/boot ::/boot/grub
	mcopy -i "$MT" "$GRUB_EFI" ::/EFI/BOOT/BOOTX64.EFI
	mcopy -i "$MT" "$cfg" ::/boot/grub/grub.cfg
	rm -f "$cfg"
	log "RUNINK_BOOT: GRUB and menu written"

	# ---- 2.. the ISOs ----
	i=1
	while [ "$i" -le "$N" ]; do
		k=$((i + 1))
		eval "iso=\$ISO$i off=\$P$k"
		if [ -n "$DEVICE" ]; then
			dd if="$iso" of="$DEVICE$k" bs=4M oflag=direct conv=fsync status=progress
		else
			dd if="$iso" of="$IMAGE" bs=4M seek="$off" oflag=seek_bytes conv=notrunc,sparse status=none
		fi
		i=$((i + 1))
	done
	log "ISOs written"

	# ---- last RUNINK_DATA ----
	if [ -n "$DEVICE" ]; then
		mkfs.exfat -L RUNINK_DATA "$D" >/dev/null
	else
		tmp="$IMAGE.exfat.tmp"
		rm -f "$tmp"
		truncate -s $(( data_mib * MIB )) "$tmp"
		mkfs.exfat -L RUNINK_DATA "$tmp" >/dev/null
		dd if="$tmp" of="$IMAGE" bs=4M seek="$PD" oflag=seek_bytes conv=notrunc,sparse status=none
		rm -f "$tmp"
	fi
	sync
	log "RUNINK_DATA: exFAT created"

	# ---- read back and check ----
	fail=0
	chk() { # label offset-bytes path-or-empty expected-type expected-label
		if [ -n "$3" ]; then got="$(blkid -p -s TYPE -s LABEL -o export "$3")"
		else got="$(blkid -p -O "$2" -s TYPE -s LABEL -o export "$IMAGE")"; fi
		case "$got" in *"TYPE=$4"*"LABEL=$5"*|*"LABEL=$5"*"TYPE=$4"*) log "ok  $1: $4 '$5'" ;;
			*) log "BAD $1: expected $4 '$5', found: $(echo "$got" | tr '\n' ' ')"; fail=1 ;; esac
	}
	cmp_iso() { # iso offset path-or-empty
		n="$(size_of "$1")"
		if [ -n "$3" ]; then src="$3" off=0; else src="$IMAGE" off="$2"; fi
		got="$(dd if="$src" bs=4M skip="$off" count="$n" iflag=skip_bytes,count_bytes status=none | sha256sum | cut -c1-64)"
		if [ "$got" = "$(cut -c1-64 "$1.sha256")" ]; then log "ok  $(basename "$1") reads back byte-identical"
		else log "BAD $(basename "$1") does not read back identical"; fail=1; fi
	}
	chk "partition 1" "$P1" "$B" vfat RUNINK_BOOT
	i=1
	while [ "$i" -le "$N" ]; do
		k=$((i + 1))
		eval "iso=\$ISO$i off=\$P$k l=\$ISO_LABEL$i"
		dev=""; [ -z "$DEVICE" ] || dev="$DEVICE$k"
		chk "partition $k" "$off" "$dev" iso9660 "$l"
		cmp_iso "$iso" "$off" "$dev"
		i=$((i + 1))
	done
	chk "partition $DP" "$PD" "$D" exfat RUNINK_DATA
	sfdisk -d "$T" | sed 's/^/  /'
	[ "$fail" -eq 0 ] || die "read-back check FAILED"
	log "DONE: $T"
}

guard_device() {
	name="${DEVICE#/dev/}"
	[ "$(lsblk -dno TYPE "$DEVICE")" = disk ] || die "$DEVICE is not a whole disk"
	[ "$(lsblk -dno TRAN "$DEVICE" | tr -d ' ')" = usb ] || die "$DEVICE is not attached over USB; refusing"
	[ "$(cat "/sys/block/$name/removable" 2>/dev/null)" = 1 ] || die "$DEVICE is not removable media; refusing"
	have="$(lsblk -bdno SIZE "$DEVICE" | tr -d ' ')"
	[ "$have" = "$SIZE" ] || die "$DEVICE is $have bytes, --size-bytes says $SIZE; refusing"
	SERIAL="$(lsblk -dno SERIAL "$DEVICE" | tr -d ' ')"
	[ "${#SERIAL}" -ge 8 ] || die "$DEVICE reports no usable serial; refusing"
	case "$SERIAL" in *"$SUFFIX") ;; *) die "$DEVICE serial does not end in $SUFFIX; refusing" ;; esac
	guard_idle
}

guard_idle() { # nothing on the device may be in use
	if lsblk -nro MOUNTPOINTS "$DEVICE" 2>/dev/null | grep -q .; then
		die "something on $DEVICE is mounted; unmount it first:
$(lsblk -o NAME,MOUNTPOINTS "$DEVICE")"
	fi
	if lsblk -nro MOUNTPOINT "$DEVICE" 2>/dev/null | grep -q .; then die "something on $DEVICE is mounted"; fi
	if grep -q "^$DEVICE" /proc/swaps; then die "$DEVICE holds active swap"; fi
	if lsblk -nro TYPE "$DEVICE" | grep -qv -e '^disk$' -e '^part$'; then
		die "$DEVICE has holders (crypt, lvm, raid, ...); release them first"
	fi
	for p in /sys/block/"${DEVICE#/dev/}"/*/holders /sys/block/"${DEVICE#/dev/}"/holders; do
		[ -d "$p" ] && [ -n "$(ls -A "$p" 2>/dev/null)" ] && die "$DEVICE (or a partition) is held by $(ls "$p")"
	done
	return 0
}

confirm_device() {
	echo
	echo "  ABOUT TO ERASE: $DEVICE"
	lsblk -o NAME,SIZE,TRAN,RM,MODEL,SERIAL,LABEL "$DEVICE" | sed 's/^/    /'
	echo
	[ -t 0 ] || die "the confirmation needs a terminal"
	printf '  Type the LAST 8 characters of the serial to erase it (anything else aborts): '
	read -r answer
	[ "$answer" = "$SUFFIX" ] || die "aborted; nothing was written"
	# Re-check right before the first write: nothing may have been mounted meanwhile.
	guard_idle
}

cmd="${1:-}"
[ -n "$cmd" ] || usage
shift
case "$cmd" in
	prepare-grub) prepare_grub "$@" ;;
	write) write_usb "$@" ;;
	-h|--help) usage ;;
	*) usage ;;
esac
