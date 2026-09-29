#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# iso-root-stage.sh — the ONE step of a local ISO build that needs root.
#
#   sudo sh build/iso-root-stage.sh <state>/root-stage-<profile>.env
#
# build/local-iso.sh prepares everything else as the calling user and writes the .env file
# this script reads. Here, as root:
#
#   1. load the Artix builder image (saved by local-iso.sh) into root's podman and check its
#      image ID against the one recorded;
#   2. run scripts/build-iso-box.sh (buildiso -i s6) in a PRIVILEGED container, with the
#      checkout at /os, a staged profile (PROFILE_DIR, an external or branded one) at
#      /profile/<profile>, the prepared [runink] repository at /os/localrepo (read-only) and
#      artools' work directory on disk under the state directory (overlayfs cannot use a
#      ZFS upper directory, and a RAM tmpfs of that size is not needed on a workstation);
#   3. for a PRIVATE server image, add its encrypted payloads to the ISO (/river-models and
#      every /river-<kind>/ of the downstream; docs/PAYLOADS.md) with xorriso (boot records
#      replayed, volume label kept), name it <iso-name>-<variant>-<date>, and check
#      every payload MANIFEST is there. A PUBLIC image refuses every payload, and is checked
#      to carry none;
#   4. check the volume label, write <iso>.sha256, remove the work directory, and hand the
#      ISO back to the invoking user (SUDO_UID).
#
# The .env file is not sourced: only the known keys are read, every value must be an
# absolute path without shell metacharacters, and the file must belong to the invoking user
# and not be writable by anyone else.
#
# A private image too large to exist as a file (a model payload of the size of the free disk)
# can be written by step 3 STRAIGHT ONTO a removable USB disk instead of into OUT_DIR:
#
#   sudo ISO_OUTDEV=/dev/disk/by-id/usb-<id> ISO_OUTDEV_CONFIRM=usb-<id> \
#        sh build/iso-root-stage.sh <state>/root-stage-<profile>.env
#
# ISO_OUTDEV must be a /dev/disk/by-id/usb-* path of a WHOLE disk (not a -partN link) whose
# /sys/block/<dev>/removable is 1, with nothing on it mounted, used as swap or held
# (device-mapper, RAID, a pool), at least as large as the base ISO plus the payloads plus
# slack; ISO_OUTDEV_CONFIRM must repeat its basename. It is checked before the build and again
# right before writing. Then every signature on its partitions and on the disk is wiped
# (wipefs), xorriso writes the image onto it, `xorriso -check_media` reads it back, and the
# sha256 of exactly the image's length (its ISO 9660 volume space size, isosize) is written to
# OUT_DIR/<name>.sha256. The whole disk is ERASED. Without ISO_OUTDEV nothing changes.
set -eu

die() { echo "iso-root-stage: $*" >&2; exit 1; }
log() { printf '\n== iso-root-stage: %s\n' "$*"; }

[ "$(id -u)" -eq 0 ] || die "run with sudo (this is the step that needs root)"
ENVF="${1:?usage: sudo sh build/iso-root-stage.sh <state>/root-stage-<profile>.env}"
[ -f "$ENVF" ] || die "no such file: $ENVF"
OWNER_UID="${SUDO_UID:?run through sudo, so the ISO can be handed back to you}"
OWNER_GID="${SUDO_GID:?run through sudo}"
[ "$(stat -c %u "$ENVF")" = "$OWNER_UID" ] || die "$ENVF is not owned by the invoking user"
case "$(stat -c %a "$ENVF")" in *[2367]?|*[2367]) die "$ENVF is writable by group or others" ;; esac
command -v podman >/dev/null 2>&1 || die "podman is required"

PROFILE="" REPO="" LOCALREPO="" OUT_DIR="" WORK_DIR="" BUILDER_ARCHIVE="" BUILDER_ID="" MODEL_PAYLOAD_DIR=""
PAYLOADS_DIR="" PUBLIC="" ISO_VARIANT="" KIND="" ISO_NAME="" PROFILE_DIR=""
while IFS= read -r line; do
	case "$line" in ''|\#*) continue ;; esac
	key="${line%%=*}" val="${line#*=}"
	case "$val" in *[!A-Za-z0-9_./:@+-]*) die "unsafe characters in $key" ;; esac
	case "$key" in
		PROFILE) case "$val" in *[!a-z0-9-]*|'') die "bad PROFILE" ;; esac; PROFILE="$val" ;;
		BUILDER_ID) case "$val" in *[!a-f0-9]*|'') die "bad BUILDER_ID" ;; esac; BUILDER_ID="$val" ;;
		PUBLIC) case "$val" in 0|1) PUBLIC="$val" ;; *) die "bad PUBLIC" ;; esac ;;
		ISO_VARIANT) case "$val" in *[!a-z0-9-]*|-*) die "bad ISO_VARIANT" ;; esac; ISO_VARIANT="$val" ;;
		KIND) case "$val" in server|workstation) KIND="$val" ;; *) die "bad KIND" ;; esac ;;
		ISO_NAME) case "$val" in *[!a-z0-9-]*|-*|'') die "bad ISO_NAME" ;; esac; ISO_NAME="$val" ;;
		REPO|LOCALREPO|OUT_DIR|WORK_DIR|BUILDER_ARCHIVE|MODEL_PAYLOAD_DIR|PAYLOADS_DIR|PROFILE_DIR)
			case "$val" in /*|'') ;; *) die "$key must be an absolute path" ;; esac
			case "$val" in *..*) die "$key must not contain '..'" ;; esac
			eval "$key=\$val" ;;
		*) die "unknown key $key in $ENVF" ;;
	esac
done < "$ENVF"
for k in PROFILE REPO LOCALREPO OUT_DIR WORK_DIR BUILDER_ARCHIVE BUILDER_ID; do
	eval "[ -n \"\$$k\" ]" || die "$k missing from $ENVF"
done
[ -f "$REPO/scripts/build-iso-box.sh" ] || die "$REPO is not a Runink River checkout"
# PROFILE_DIR: a staged profile (an external one, or one with edition branding; build/local-iso.sh
# step 4b), mounted into the build container in place of iso-profiles/<profile>. An .env
# written before external profiles has no KIND/ISO_NAME; the in-tree profile names imply them.
if [ -n "$PROFILE_DIR" ]; then
	[ -f "$PROFILE_DIR/profile.yaml" ] || die "no profile at $PROFILE_DIR"
	[ "$(stat -c %u "$PROFILE_DIR")" = "$OWNER_UID" ] || die "$PROFILE_DIR is not owned by the invoking user"
	{ [ -n "$KIND" ] && [ -n "$ISO_NAME" ]; } || die "a staged profile needs KIND and ISO_NAME in $ENVF (re-run build/local-iso.sh)"
else
	[ -d "$REPO/iso-profiles/$PROFILE" ] || die "no profile $PROFILE in $REPO"
	case "$PROFILE" in
		river) KIND="${KIND:-workstation}" ISO_NAME="${ISO_NAME:-runink-river}" ;;
		*) { [ -n "$KIND" ] && [ -n "$ISO_NAME" ]; } || die "profile $PROFILE needs KIND and ISO_NAME in $ENVF (re-run build/local-iso.sh)" ;;
	esac
fi
[ -f "$LOCALREPO/runink.db.tar.gz" ] || die "no [runink] repository at $LOCALREPO (run build/local-iso.sh)"
[ -f "$BUILDER_ARCHIVE" ] || die "no builder image archive at $BUILDER_ARCHIVE"
# A PUBLIC image carries no payload of any kind. An .env without PUBLIC predates the
# public/private split and is treated as public: re-run build/local-iso.sh.
[ -n "$PUBLIC" ] || PUBLIC=1
if [ "$PUBLIC" = 1 ]; then
	[ -z "$MODEL_PAYLOAD_DIR" ] || die "PUBLIC image: refusing to attach a model payload"
	[ -z "$PAYLOADS_DIR" ] || die "PUBLIC image: refusing to attach downstream payloads"
	[ -z "$ISO_VARIANT" ] || die "PUBLIC image: it has no variant name"
else
	[ "$KIND" = server ] || die "only a server profile has a private variant"
	[ -n "$ISO_VARIANT" ] || die "a private image needs ISO_VARIANT"
fi
if [ -n "$MODEL_PAYLOAD_DIR" ]; then
	[ "$KIND" = server ] || die "a model payload goes on a server ISO only"
	[ -f "$MODEL_PAYLOAD_DIR/MANIFEST" ] || die "no MANIFEST in $MODEL_PAYLOAD_DIR"
	# An incremental pack still in progress (river-modelpack pack --append) is not a payload.
	! grep -qx incomplete "$MODEL_PAYLOAD_DIR/MANIFEST" || die "$MODEL_PAYLOAD_DIR is an INCOMPLETE payload (finish it with river-modelpack pack --append)"
fi
# Each river-<kind>/ under PAYLOADS_DIR becomes /river-<kind> on the ISO.
if [ -n "$PAYLOADS_DIR" ]; then
	ls -d "$PAYLOADS_DIR"/river-*/*/MANIFEST >/dev/null 2>&1 || die "no river-<kind>/<group>/MANIFEST under $PAYLOADS_DIR"
	for d in "$PAYLOADS_DIR"/river-*; do
		case "$(basename "$d")" in river-models) die "$PAYLOADS_DIR must not hold river-models" ;; esac
	done
fi

# ISO_OUTDEV (see the header): the final image goes onto a removable USB disk, not a file.
OUTDEV="${ISO_OUTDEV:-}" DEV="" PAYLOAD_BYTES=0
dev_bytes() { echo $(( $(cat "/sys/block/$(basename "$DEV")/size") * 512 )); }
# outdev_guard: every check but the size. Run before the build and again right before
# writing: a desktop may have mounted the stick, or another one taken its name, in between.
outdev_guard() {
	DEV="$(readlink -f "$OUTDEV")"
	[ -b "$DEV" ] || die "ISO_OUTDEV=$OUTDEV does not resolve to a block device"
	_b="$(basename "$DEV")"
	[ -d "/sys/block/$_b" ] || die "ISO_OUTDEV=$OUTDEV ($DEV) is not a whole disk (a partition?)"
	[ "$(cat "/sys/block/$_b/removable" 2>/dev/null || true)" = 1 ] \
		|| die "$DEV is not a removable disk (/sys/block/$_b/removable is not 1); refusing to write it"
	_mp="$(lsblk -nrpo MOUNTPOINT "$DEV" | grep -v '^$' || true)"
	[ -z "$_mp" ] || die "$DEV (or a partition of it) is mounted or used as swap ($(echo "$_mp" | tr '\n' ' ')); unmount it first"
	for _h in /sys/block/"$_b"/holders/* /sys/block/"$_b"/"$_b"*/holders/*; do
		[ -e "$_h" ] && die "$DEV is held by $(basename "$_h") (device-mapper, RAID or a pool); release it first"
	done
	return 0
}
if [ -n "$OUTDEV" ]; then
	[ "$PUBLIC" = 0 ] || die "ISO_OUTDEV is for a private image with payloads; a public image is written as a file"
	[ -n "$MODEL_PAYLOAD_DIR$PAYLOADS_DIR" ] || die "ISO_OUTDEV needs a payload to attach (without one the image fits in a file)"
	case "$OUTDEV" in /dev/disk/by-id/usb-*) ;; *) die "ISO_OUTDEV must be a /dev/disk/by-id/usb-* path" ;; esac
	case "$OUTDEV" in *[!A-Za-z0-9_./:@+-]*|*..*) die "unsafe characters in ISO_OUTDEV" ;; esac
	case "$(basename "$OUTDEV")" in *-part[0-9]*) die "ISO_OUTDEV=$OUTDEV names a partition; give the whole disk" ;; esac
	[ "${ISO_OUTDEV_CONFIRM:-}" = "$(basename "$OUTDEV")" ] \
		|| die "ISO_OUTDEV will ERASE the whole disk; confirm with ISO_OUTDEV_CONFIRM=$(basename "$OUTDEV")"
	command -v wipefs >/dev/null 2>&1 && command -v lsblk >/dev/null 2>&1 && command -v isosize >/dev/null 2>&1 \
		|| die "ISO_OUTDEV needs wipefs, lsblk and isosize (util-linux)"
	outdev_guard
	set --
	[ -n "$MODEL_PAYLOAD_DIR" ] && set -- "$MODEL_PAYLOAD_DIR"
	[ -n "$PAYLOADS_DIR" ] && set -- "$@" "$PAYLOADS_DIR"/river-*
	PAYLOAD_BYTES="$(du -scb "$@" | tail -n 1 | cut -f1)"
	[ "$(dev_bytes)" -gt "$PAYLOAD_BYTES" ] || die "$DEV ($(dev_bytes) bytes) cannot hold the payloads ($PAYLOAD_BYTES bytes)"
	echo "iso-root-stage: the image will be written to $OUTDEV ($DEV, $(dev_bytes) bytes), erasing it"
fi

# --- 1. builder image ------------------------------------------------------------------
log "1/4 loading the builder image into root's podman"
podman load -q -i "$BUILDER_ARCHIVE" >/dev/null
IMG="$(podman images -q --no-trunc | grep -m1 "$BUILDER_ID" || true)"
[ -n "$IMG" ] || die "loaded image does not have ID $BUILDER_ID"
IMG="${IMG#sha256:}"

# --- 2. buildiso ------------------------------------------------------------------------
STAGE="$OUT_DIR/.stage-$PROFILE"
rm -rf "$STAGE" "$WORK_DIR"
mkdir -p "$STAGE" "$WORK_DIR"
# -v /dev:/dev: a --privileged container only gets a snapshot of /dev taken at start, so a
# loop device that losetup creates later (make_grub mounts efi.img over one) never appears
# inside it and the mount fails with "failed to set up loop device". Sharing the host /dev
# fixes that; the container is already --privileged, so it grants nothing new.
log "2/4 buildiso -p $PROFILE -i s6 (work dir $WORK_DIR; about 20-40 minutes)"
start=$(date +%s)
# A staged profile is mounted at /profile/<name> (writable: a LEAN build moves files inside it)
# and named to build-iso-box.sh by RIVER_PROFILE_DIR; without one it reads /os/iso-profiles/.
set --
[ -n "$PROFILE_DIR" ] && set -- -v "$PROFILE_DIR:/profile/$PROFILE" -e RIVER_PROFILE_DIR="/profile/$PROFILE"
podman run --rm --privileged --network=host -v /dev:/dev \
	-v "$REPO:/os" -v "$LOCALREPO:/os/localrepo:ro" -v "$WORK_DIR:/var/lib/artools" \
	-v "$STAGE:/os/iso-out" -e PROFILE="$PROFILE" "$@" --user root -w /os \
	"$IMG" bash /os/scripts/build-iso-box.sh
ISO="$(find "$STAGE" -name '*.iso' -type f | head -1)"
[ -n "$ISO" ] || die "buildiso produced no ISO under $STAGE"
echo "iso-root-stage: buildiso took $(( ($(date +%s) - start) / 60 )) min: $(basename "$ISO")"

# --- 3. payloads ---------------------------------------------------------------------------
LABEL="$(blkid -s LABEL -o value "$ISO" 2>/dev/null || true)"
[ -n "$LABEL" ] || die "cannot read the volume label of $ISO"
# A private image is named for its variant: <iso-name>-<variant>-<date>-x86_64.iso
# (a downstream server profile's ISO_NAME, then the variant).
NAME="$(basename "$ISO")"
if [ -n "$ISO_VARIANT" ]; then
	case "$NAME" in
		"$ISO_NAME"-*) NAME="$ISO_NAME-$ISO_VARIANT-${NAME#"$ISO_NAME"-}" ;;
		*) die "unexpected ISO name $NAME for a private variant (want $ISO_NAME-*)" ;;
	esac
fi
FINAL="$OUT_DIR/$NAME"
# One xorriso pass maps every payload directory (-map <dir> /river-<kind>).
set --
[ -n "$MODEL_PAYLOAD_DIR" ] && set -- "$@" -v "$MODEL_PAYLOAD_DIR:/p/river-models:ro"
if [ -n "$PAYLOADS_DIR" ]; then
	for d in "$PAYLOADS_DIR"/river-*; do
		[ -d "$d" ] && set -- "$@" -v "$d:/p/$(basename "$d"):ro"
	done
fi
if [ $# -gt 0 ]; then
	maps=""
	for a in "$@"; do
		case "$a" in *:/p/river-*:ro) k="${a#*:/p/}"; k="${k%:ro}"; maps="$maps -map /p/$k /$k" ;; esac
	done
	log "3/4 adding the encrypted payloads:$(echo "$maps" | sed 's| -map /p/[^ ]*||g')"
	# Where xorriso writes: a file in OUT_DIR, or (ISO_OUTDEV) the whole USB disk.
	OUT_OPT=-v OUT_RW="$OUT_DIR:/out" OUT_RO="$OUT_DIR:/out:ro" OUT_SPEC="/out/$NAME"
	if [ -n "$DEV" ]; then
		outdev_guard
		need=$(( $(stat -c %s "$ISO") + PAYLOAD_BYTES + PAYLOAD_BYTES / 100 + 64 * 1048576 ))
		[ "$(dev_bytes)" -ge "$need" ] || die "$DEV ($(dev_bytes) bytes) is smaller than the image bound ($need bytes: base ISO + payloads + slack)"
		echo "iso-root-stage: ERASING $OUTDEV ($DEV, $(dev_bytes) bytes; the image is at most $need bytes)"
		for p in $(lsblk -nrpo NAME,TYPE "$DEV" | awk '$2 == "part" { print $1 }'); do
			wipefs -a -q "$p"
		done
		wipefs -a -q "$DEV"
		blockdev --rereadpt "$DEV" 2>/dev/null || true
		OUT_OPT=--device OUT_RW="$DEV:/dev/river-outdev" OUT_RO="$DEV:/dev/river-outdev:r" OUT_SPEC="stdio:/dev/river-outdev"
		FINAL="$OUTDEV"
	else
		rm -f "$FINAL"
	fi
	# shellcheck disable=SC2086  # $maps is a list of -map pairs built above from safe names
	podman run --rm --network=none -v "$STAGE:/in:ro" "$OUT_OPT" "$OUT_RW" "$@" --user root "$IMG" \
		xorriso -indev "/in/${ISO#"$STAGE"/}" -outdev "$OUT_SPEC" \
			-volid "$LABEL" -boot_image any replay $maps -commit
	[ -z "$DEV" ] || { sync; blockdev --flushbufs "$DEV" 2>/dev/null || true; }
	found="$(podman run --rm --network=none "$OUT_OPT" "$OUT_RO" --user root "$IMG" \
		xorriso -indev "$OUT_SPEC" -find / -name MANIFEST 2>/dev/null | grep -c "/river-[a-z0-9-]*/" || true)"
	want=0
	[ -n "$MODEL_PAYLOAD_DIR" ] && want=1
	[ -n "$PAYLOADS_DIR" ] && want=$((want + $(ls "$PAYLOADS_DIR"/river-*/*/MANIFEST | wc -l)))
	[ "$found" -eq "$want" ] || die "$FINAL holds $found payload MANIFEST(s), expected $want"
	echo "iso-root-stage: $found payload(s) on the ISO"
	if [ -n "$DEV" ]; then
		# Read every block of the image back from the disk.
		cm="$(podman run --rm --network=none "$OUT_OPT" "$OUT_RO" --user root "$IMG" \
			xorriso -indev "$OUT_SPEC" -check_media -- 2>&1)" || die "xorriso -check_media failed on $DEV: $cm"
		echo "$cm" | grep -q '^Media region' || die "xorriso -check_media reported no media region on $DEV: $cm"
		! echo "$cm" | grep -E '^Media region.*, *-' || die "xorriso -check_media found unreadable or invalid blocks on $DEV"
		echo "iso-root-stage: xorriso -check_media: every region of $DEV good"
	fi
else
	log "3/4 no payload for this ISO"
	mv "$ISO" "$FINAL"
	# A public image must not carry one even by accident (e.g. a profile overlay).
	if podman run --rm --network=none -v "$OUT_DIR:/out:ro" --user root "$IMG" \
		xorriso -indev "/out/$NAME" -find / -name MANIFEST 2>/dev/null | grep -q "/river-[a-z0-9-]*/"; then
		die "$FINAL carries a /river-* payload but none was attached"
	fi
fi

# --- 4. checks and hand-back -------------------------------------------------------------
log "4/4 checks"
if [ -n "$DEV" ]; then
	got="$(blkid -p -s LABEL -o value "$DEV" 2>/dev/null || true)"
	[ "$got" = "$LABEL" ] || die "$DEV has label '$got', expected '$LABEL'"
	echo "iso-root-stage: label $got"
	# The image is the ISO 9660 volume space (it covers the appended boot partition too): hash
	# exactly that many bytes of the disk.
	SIZE="$(isosize "$DEV")"
	case "$SIZE" in ''|*[!0-9]*) die "cannot read the image size from $DEV" ;; esac
	{ [ "$SIZE" -gt 0 ] && [ "$SIZE" -le "$(dev_bytes)" ]; } || die "$DEV: image size $SIZE out of range"
	okd="$(mktemp -d)"
	SUM="$( { head -c "$SIZE" "$DEV" && : > "$okd/read"; } | sha256sum | cut -c1-64)"
	[ -f "$okd/read" ] || { rm -rf "$okd"; die "reading $SIZE bytes back from $DEV failed"; }
	rm -rf "$okd"
	echo "$SUM  $NAME" > "$OUT_DIR/$NAME.sha256"
	chown "$OWNER_UID:$OWNER_GID" "$OUT_DIR/$NAME.sha256"
	rm -rf "$STAGE" "$WORK_DIR"
	echo "iso-root-stage: image $NAME, $SIZE bytes, on $OUTDEV ($DEV)"
	cat "$OUT_DIR/$NAME.sha256"
	echo "iso-root-stage: DONE — $NAME is on $OUTDEV; the sha256 of its $SIZE bytes is in $OUT_DIR/$NAME.sha256"
	exit 0
fi
got="$(blkid -s LABEL -o value "$FINAL" 2>/dev/null || true)"
[ "$got" = "$LABEL" ] || die "$FINAL has label '$got', expected '$LABEL'"
echo "iso-root-stage: label $got"
( cd "$OUT_DIR" && sha256sum "$(basename "$FINAL")" > "$(basename "$FINAL").sha256" )
chown "$OWNER_UID:$OWNER_GID" "$FINAL" "$FINAL.sha256"
rm -rf "$STAGE" "$WORK_DIR"
ls -l "$FINAL"
cat "$FINAL.sha256"
echo "iso-root-stage: DONE — next, as yourself: sh $REPO/build/qemu-test.sh $FINAL"
