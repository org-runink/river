#!/bin/sh
# 73-downstream-payloads — copy the medium's downstream payloads onto the node, STILL
# ENCRYPTED, into their own natively encrypted dataset <pool>/payloads, mounted at
# /var/lib/runink/payloads (docs/PAYLOADS.md).
#
# A medium MAY carry, next to the model payload, any number of downstream payloads at
# /river-<kind>/<group>/ (MANIFEST + LOCK + parts; a downstream platform decides what kinds
# and groups mean). The installer never opens them: it does not know their passphrase and
# does not deploy anything. A downstream tool on the installed node (handed control through
# /etc/runink/firstboot.d/) decrypts what the operator selects, later. This step only:
#
#   - finds them: RUNINK_PAYLOADS_SRC (a directory holding river-<kind>/ directories), else
#     every place a live stack mounts the medium, as 72-models-payload does;
#   - creates <pool>/payloads (encryption inherited from the pool root, never off;
#     compression off: ciphertext does not compress), root-only;
#   - runs `river-payloadpack stage --src SRC --dest DIR`, which copies each payload while
#     hashing every part against its MANIFEST, checks each LOCK against the MANIFEST's
#     lock-sha256, writes files 0600 in 0700 directories and an INDEX of what it copied.
#
# No payload on the medium (a public image): nothing is created, the step succeeds.
# RUNINK_SKIP_PAYLOADS=1 (runink-autoinstall --skip-payloads) skips it on purpose.
# Any failure destroys <pool>/payloads: a node never keeps a partial or altered payload set.
# An existing <pool>/payloads (re-install into an imported pool) is replaced by the medium's.
#
# The same command works outside the installer (e.g. run over SSH on a target), given a
# source directory and an empty destination:
#   river-payloadpack stage --src <dir holding river-*/> --dest /var/lib/runink/payloads
set -eu

TARGET="${RUNINK_TARGET:?}"
POOL="${RUNINK_POOL:?}"
PAYLOADS_MP="/var/lib/runink/payloads"
DS="$POOL/payloads"

log() { echo "payloads: $*"; }

if [ "${RUNINK_SKIP_PAYLOADS:-0}" = 1 ]; then
	log "skipped (RUNINK_SKIP_PAYLOADS=1); nothing copied"
	exit 0
fi

# A directory holds downstream payloads when it has river-<kind>/<group>/MANIFEST for a kind
# other than models.
has_payloads() {
	for m in "$1"/river-*/*/MANIFEST; do
		[ -f "$m" ] || continue
		case "$m" in "$1"/river-models/*) continue ;; esac
		return 0
	done
	return 1
}
find_src() {
	if [ -n "${RUNINK_PAYLOADS_SRC:-}" ]; then
		has_payloads "$RUNINK_PAYLOADS_SRC" && { echo "$RUNINK_PAYLOADS_SRC"; return 0; }
		log "RUNINK_PAYLOADS_SRC=$RUNINK_PAYLOADS_SRC holds no river-<kind>/<group>/MANIFEST" >&2
		return 2
	fi
	for d in /run/initramfs/live /run/archiso/bootmnt /run/artix/bootmnt /run/miso/bootmnt /bootmnt \
		$(awk '$3 == "iso9660" { print $2 }' /proc/mounts); do
		has_payloads "$d" && { echo "$d"; return 0; }
	done
	return 1
}

rc=0
SRC="$(find_src)" || rc=$?
[ "$rc" -ne 2 ] || exit 1
if [ "$rc" -ne 0 ]; then
	log "no downstream payload on the install medium (nothing to copy)"
	exit 0
fi
log "downstream payloads found under $SRC"
command -v river-payloadpack >/dev/null 2>&1 || { log "river-payloadpack missing from the live image" >&2; exit 1; }

# A CLOUD IMAGE (RUNINK_CLOUD; docs/CLOUD-IMAGES.md) has no encryption root yet: it is created
# per instance at first boot. The payloads are ciphertext, so they go into the image's
# /var/lib/runink as a plain directory, and river-cloud-init moves /var/lib/runink into the
# instance's encrypted node-state dataset before anything can open them.
if [ -n "${RUNINK_CLOUD:-}" ]; then
	DEST="$TARGET$PAYLOADS_MP"
	[ ! -e "$DEST" ] || rm -rf "$DEST"
	( umask 077; mkdir -p "$DEST" )
	chmod 0700 "$TARGET/var/lib/runink" "$DEST"
	log "cloud image: copying (still encrypted) into $DEST; moved into the encrypted node-state dataset per instance"
	river-payloadpack stage --src "$SRC" --dest "$DEST" || { rm -rf "$DEST"; log "copy/verification FAILED; nothing kept" >&2; exit 1; }
	chown -R 0:0 "$DEST"
	sed 's/^/payloads:   /' "$DEST/INDEX"
	exit 0
fi
enc="$(zfs get -H -o value encryption "$POOL")"
if [ "$enc" = off ]; then
	log "pool $POOL is not encrypted; refusing to put the payloads on it" >&2
	exit 1
fi
if zfs list -H "$DS" >/dev/null 2>&1; then
	log "$DS exists (re-install); replacing it with the medium's payloads"
	zfs destroy -r "$DS"
fi
zfs create -o mountpoint="$PAYLOADS_MP" -o com.sun:auto-snapshot=false -o atime=off \
	-o recordsize=1M -o compression=off "$DS"
fail() {
	log "$1" >&2
	zfs destroy -r "$DS" 2>/dev/null || true
	exit 1
}
[ "$(zfs get -H -o value encryption "$DS")" != off ] || fail "$DS came out unencrypted; destroyed"
DEST="$(zfs get -H -o value mountpoint "$DS")"
case "$DEST" in "$TARGET"*) ;; *) DEST="$TARGET$PAYLOADS_MP" ;; esac
[ -d "$DEST" ] || fail "$DS is not mounted at $DEST"

log "copying (still encrypted) into $DS"
river-payloadpack stage --src "$SRC" --dest "$DEST" || fail "copy/verification FAILED; $DS destroyed"
chown -R 0:0 "$DEST"
chmod 0700 "$DEST"
sed 's/^/payloads:   /' "$DEST/INDEX"
log "downstream payloads staged and verified: $(zfs get -H -o value used "$DS") in $DS (never decrypted here)"
