#!/bin/sh
# 72-models-payload — unpack the encrypted model payload from the install medium into its
# own natively encrypted dataset, <pool>/models, mounted at /var/lib/core/models/shared.
#
# A server medium MAY carry the model set pinned in models.lock as an encrypted payload
# (`river-models/` at the root of the ISO; format and threat model in docs/MODEL-PAYLOAD.md).
# Without one this step does nothing and the models arrive at enrollment (MODEL_STORE_URL),
# as before.
#
# The payload's passphrase is NEVER on the medium. It comes from
#   RUNINK_MODELS_PASSPHRASE_FILE   a file (runink-autoinstall --models-passphrase-file), or
#   the console                     typed with echo off (runink-install), up to 3 attempts.
# No passphrase (unattended without the file, or an empty answer) defers the models: the
# step says so and succeeds, and nothing is written. RUNINK_SKIP_MODELS=1
# (runink-autoinstall --skip-models) skips the step outright.
#
# REQUIRED mode (RUNINK_MODELS_REQUIRED=1: an edition descriptor's "models_required", or set by a
# downstream's own front end): a node that must carry its models never installs without them.
# The step then FAILS, instead of deferring, when the boot medium carries no model payload,
# when the passphrase is blank or an unattended install has no passphrase source, and when
# RUNINK_SKIP_MODELS=1 asks to skip it. The default (unset) is unchanged.
#
# The payload is read from the boot medium itself, however large it is (a single ISO can carry
# a model set of well over 100 GB: parts stay below 2 GiB, so ISO 9660 holds them). Before
# unpacking, the pool must have room for the set the MANIFEST declares.
#
# The dataset inherits the pool's aes-256-gcm encryption root (never encryption=off), so the
# weights are encrypted at rest under the node's own key from the first byte written.
# river-modelpack authenticates every 4 MiB chunk (AES-256-GCM), checks each ciphertext part
# against the payload MANIFEST, refuses any file that is not a models.lock row, and checks
# every file's size and sha256 against the lock as it writes it. This step then re-checks the
# whole directory with sha256sum against models.manifest (generated from the same lock at
# build time). Any failure destroys the dataset, so a node never keeps a partial or altered
# model set.
set -eu

TARGET="${RUNINK_TARGET:?}"
POOL="${RUNINK_POOL:?}"
LOCK="${RUNINK_MODELS_LOCK:-/usr/local/share/runink/models.lock}"
SUMS="${RUNINK_MODELS_MANIFEST:-/usr/local/share/runink/models.manifest}"
MODELS_MP="/var/lib/core/models/shared"
DS="$POOL/models"

log() { echo "models-payload: $*"; }
REQUIRED="${RUNINK_MODELS_REQUIRED:-0}"
# required_or_defer MESSAGE: in required mode, fail with it; otherwise defer the models.
required_or_defer() {
	if [ "$REQUIRED" = 1 ]; then
		log "REQUIRED (RUNINK_MODELS_REQUIRED=1): $1; this node must carry its models, so the install stops" >&2
		exit 1
	fi
	log "$1; models DEFERRED, nothing written"
	exit 0
}

# --- find the payload on the medium ----------------------------------------------------
# The live stack decides where the medium is mounted (it differs between initramfs
# implementations), so look at every place one of them uses, then at any mounted iso9660.
find_payload() {
	if [ -n "${RUNINK_MODELS_PAYLOAD:-}" ]; then
		[ -f "$RUNINK_MODELS_PAYLOAD/MANIFEST" ] && { echo "$RUNINK_MODELS_PAYLOAD"; return 0; }
		log "RUNINK_MODELS_PAYLOAD=$RUNINK_MODELS_PAYLOAD has no MANIFEST" >&2
		return 1
	fi
	for d in /run/initramfs/live /run/archiso/bootmnt /run/artix/bootmnt /run/miso/bootmnt /bootmnt \
		$(awk '$3 == "iso9660" { print $2 }' /proc/mounts); do
		[ -f "$d/river-models/MANIFEST" ] && { echo "$d/river-models"; return 0; }
	done
	return 1
}

if [ "${RUNINK_SKIP_MODELS:-0}" = 1 ]; then
	[ "$REQUIRED" = 1 ] && required_or_defer "RUNINK_SKIP_MODELS=1 asks to skip the model payload"
	log "skipped (RUNINK_SKIP_MODELS=1): no model set written; enrollment can still sync them"
	exit 0
fi
if ! PAYLOAD="$(find_payload)"; then
	[ "$REQUIRED" = 1 ] && required_or_defer "no model payload (river-models/MANIFEST) on the boot medium"
	log "no model payload on the install medium; the models arrive at enrollment (MODEL_STORE_URL)"
	exit 0
fi
# content <files> <bytes> of the MANIFEST: what the unpack writes.
SET_BYTES="$(awk '$1 == "content" { print $3 }' "$PAYLOAD/MANIFEST")"
case "$SET_BYTES" in ''|*[!0-9]*) log "$PAYLOAD/MANIFEST has no content line" >&2; exit 1 ;; esac
log "model payload found at $PAYLOAD ($(awk '$1 == "content" { print $2 }' "$PAYLOAD/MANIFEST") file(s), $SET_BYTES bytes)"
command -v river-modelpack >/dev/null 2>&1 || { log "river-modelpack missing from the live image" >&2; exit 1; }
[ -f "$LOCK" ] || { log "no $LOCK on the live image: cannot verify the payload, refusing" >&2; exit 1; }

# --- an existing dataset (re-install into an imported pool) ----------------------------
if zfs list -H "$DS" >/dev/null 2>&1; then
	mp="$(zfs get -H -o value mountpoint "$DS")"
	log "$DS already exists (mounted at $mp); verifying it instead of unpacking"
	if river-modelpack verify --dir "$mp" --lock "$LOCK" >/dev/null; then
		log "$DS matches $LOCK; keeping it"
		exit 0
	fi
	log "$DS does NOT match $LOCK. It is left untouched; destroy it (zfs destroy -r $DS) and re-run to unpack the medium's set." >&2
	exit 1
fi

# --- the passphrase ----------------------------------------------------------------------
# Held in a file on tmpfs that only root can read, removed on exit (RUNINK_MODELS_RUNDIR, default
# /run, only moves it for the contract test). river-modelpack reads it
# once; it never reaches the target or the medium.
PASSDIR="$(mktemp -d "${RUNINK_MODELS_RUNDIR:-/run}/river-models.XXXXXX")"
chmod 0700 "$PASSDIR"
PASS="$PASSDIR/passphrase"
cleanup() { rm -rf "$PASSDIR"; }
trap cleanup EXIT INT TERM

if [ -n "${RUNINK_MODELS_PASSPHRASE_FILE:-}" ]; then
	[ -r "$RUNINK_MODELS_PASSPHRASE_FILE" ] || { log "cannot read $RUNINK_MODELS_PASSPHRASE_FILE" >&2; exit 1; }
	( umask 077; head -n 1 "$RUNINK_MODELS_PASSPHRASE_FILE" > "$PASS" )
	attempts=1
elif [ -t 0 ]; then
	attempts=3
else
	required_or_defer "no passphrase (unattended, no RUNINK_MODELS_PASSPHRASE_FILE)"
fi
if [ -n "${RUNINK_MODELS_PASSPHRASE_FILE:-}" ] && [ -z "$(tr -d '\r\n' < "$PASS")" ]; then
	# A blank first line is no passphrase: say so, rather than a failed unpack.
	required_or_defer "the passphrase file is blank"
fi

# --- the dataset ---------------------------------------------------------------------------
enc="$(zfs get -H -o value encryption "$POOL")"
if [ "$enc" = off ]; then
	log "pool $POOL is not encrypted; refusing to put the model set on it in the clear" >&2
	exit 1
fi
# Room for the whole set (plus 2%), checked before hours of reading rather than after.
avail="$(zfs get -Hp -o value available "$POOL")"
case "$avail" in ''|*[!0-9]*) log "cannot read the free space of $POOL" >&2; exit 1 ;; esac
if [ "$avail" -lt $((SET_BYTES + SET_BYTES / 50)) ]; then
	log "pool $POOL has $avail bytes free; the model set needs $SET_BYTES (+2%); nothing written" >&2
	exit 1
fi
zfs create -o mountpoint="$MODELS_MP" -o com.sun:auto-snapshot=false -o atime=off \
	-o recordsize=1M -o compression=off "$DS"
[ "$(zfs get -H -o value encryption "$DS")" != off ] \
	|| { zfs destroy -r "$DS"; log "$DS came out unencrypted; destroyed" >&2; exit 1; }
DEST="$(zfs get -H -o value mountpoint "$DS")"
case "$DEST" in "$TARGET"*) ;; *) DEST="$TARGET$MODELS_MP" ;; esac
[ -d "$DEST" ] || { zfs destroy -r "$DS"; log "$DS is not mounted at $DEST" >&2; exit 1; }

fail() {
	log "$1" >&2
	zfs destroy -r "$DS" 2>/dev/null || true
	exit 1
}

n=0
while :; do
	n=$((n + 1))
	if [ -z "${RUNINK_MODELS_PASSPHRASE_FILE:-}" ]; then
		stty -echo 2>/dev/null || true
		if [ "$REQUIRED" = 1 ]; then
			printf 'Model payload passphrase (required): ' >&2
		else
			printf 'Model payload passphrase (blank = defer the models): ' >&2
		fi
		IFS= read -r _p || _p=""
		stty echo 2>/dev/null || true
		printf '\n' >&2
		if [ -z "$_p" ]; then
			zfs destroy -r "$DS"
			required_or_defer "a blank passphrase"
		fi
		( umask 077; printf '%s\n' "$_p" > "$PASS" )
		unset _p
	fi
	log "unpacking into $DS (this reads the whole payload from the medium: $SET_BYTES bytes before compression)"
	rc=0
	river-modelpack unpack --payload "$PAYLOAD" --lock "$LOCK" --dest "$DEST" --passphrase-file "$PASS" || rc=$?
	[ "$rc" -eq 0 ] && break
	# Start clean for the next attempt (or leave nothing behind).
	zfs destroy -r "$DS"
	if [ "$n" -ge "$attempts" ]; then
		log "unpacking FAILED (see above); $DS destroyed, no model set on this node" >&2
		exit 1
	fi
	log "unpacking failed; try again ($n/$attempts)" >&2
	zfs create -o mountpoint="$MODELS_MP" -o com.sun:auto-snapshot=false -o atime=off \
		-o recordsize=1M -o compression=off "$DS"
done

# Independent re-check with coreutils against the build-time manifest of the same lock.
if [ -f "$SUMS" ]; then
	( cd "$DEST" && sha256sum --quiet -c "$SUMS" ) || fail "files do not match $SUMS; $DS destroyed"
	log "sha256sum -c $SUMS: every file OK"
fi
chmod -R a+rX "$DEST"
log "model set installed and verified: $(zfs get -H -o value used "$DS") in $DS"
