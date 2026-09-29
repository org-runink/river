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
fi
# Each river-<kind>/ under PAYLOADS_DIR becomes /river-<kind> on the ISO.
if [ -n "$PAYLOADS_DIR" ]; then
	ls -d "$PAYLOADS_DIR"/river-*/*/MANIFEST >/dev/null 2>&1 || die "no river-<kind>/<group>/MANIFEST under $PAYLOADS_DIR"
	for d in "$PAYLOADS_DIR"/river-*; do
		case "$(basename "$d")" in river-models) die "$PAYLOADS_DIR must not hold river-models" ;; esac
	done
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
	rm -f "$FINAL"
	# shellcheck disable=SC2086  # $maps is a list of -map pairs built above from safe names
	podman run --rm --network=none -v "$STAGE:/in:ro" -v "$OUT_DIR:/out" "$@" --user root "$IMG" \
		xorriso -indev "/in/${ISO#"$STAGE"/}" -outdev "/out/$NAME" \
			-volid "$LABEL" -boot_image any replay $maps -commit
	found="$(podman run --rm --network=none -v "$OUT_DIR:/out:ro" --user root "$IMG" \
		xorriso -indev "/out/$NAME" -find / -name MANIFEST 2>/dev/null | grep -c "/river-[a-z0-9-]*/" || true)"
	want=0
	[ -n "$MODEL_PAYLOAD_DIR" ] && want=1
	[ -n "$PAYLOADS_DIR" ] && want=$((want + $(ls "$PAYLOADS_DIR"/river-*/*/MANIFEST | wc -l)))
	[ "$found" -eq "$want" ] || die "$FINAL holds $found payload MANIFEST(s), expected $want"
	echo "iso-root-stage: $found payload(s) on the ISO"
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
got="$(blkid -s LABEL -o value "$FINAL" 2>/dev/null || true)"
[ "$got" = "$LABEL" ] || die "$FINAL has label '$got', expected '$LABEL'"
echo "iso-root-stage: label $got"
( cd "$OUT_DIR" && sha256sum "$(basename "$FINAL")" > "$(basename "$FINAL").sha256" )
chown "$OWNER_UID:$OWNER_GID" "$FINAL" "$FINAL.sha256"
rm -rf "$STAGE" "$WORK_DIR"
ls -l "$FINAL"
cat "$FINAL.sha256"
echo "iso-root-stage: DONE — next, as yourself: sh $REPO/build/qemu-test.sh $FINAL"
