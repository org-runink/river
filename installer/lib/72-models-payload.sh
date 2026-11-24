#!/bin/sh
# 72-models-payload — unpack the encrypted model payload from the install medium into its
# own natively encrypted dataset, <pool>/models, mounted at /var/lib/tide/models/shared.
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
MODELS_MP="/var/lib/tide/models/shared"
DS="$POOL/models"

log() { echo "models-payload: $*"; }
REQUIRED="${RUNINK_MODELS_REQUIRED:-0}"
# Nothing here may fail silently. Every way out prints, in plain words, what happened, the
# exact reason and what to do next (die), or an install-notice line the install drivers repeat
# at the end (notice). SAID records that a message was printed; the EXIT trap below reports
# anything that stopped the step without one (an unexpected tool failure under `set -e`).
SAID=""
die() { # die REASON [NEXT-LINE...]: print and fail the step
	SAID=1
	log "FAILED: $1" >&2
	shift
	for _l in "$@"; do echo "models-payload:   $_l" >&2; done
	echo "models-payload:   Full log: $STEPLOG_HINT" >&2
	exit 1
}
STEPLOG_HINT="${RUNINK_INSTALL_LOG:-/var/log/runink-install.log}"
notice() { echo "install-notice: models: $*"; }
PASSDIR=""
cleanup() {
	_rc=$?
	[ -z "$PASSDIR" ] || rm -rf "$PASSDIR"
	if [ "$_rc" -ne 0 ] && [ -z "$SAID" ]; then
		log "FAILED unexpectedly (exit $_rc): a command above failed and printed the reason." >&2
		echo "models-payload:   What to do: run the installer again; if it fails the same way, keep the log ($STEPLOG_HINT) and ask for help." >&2
	fi
}
trap cleanup EXIT INT TERM
# gib BYTES: "12.3 GiB" for a message.
gib() { awk -v b="$1" 'BEGIN { printf "%.1f GiB", b / 1073741824 }'; }
# required_or_defer MESSAGE: in required mode, fail with it; otherwise defer the models.
required_or_defer() {
	if [ "$REQUIRED" = 1 ]; then
		die "REQUIRED (RUNINK_MODELS_REQUIRED=1): $1. This node must carry its models, so the install stops and the computer is NOT ready." \
			"What to do: fix the cause above and run the installer again; the model passphrase is on the medium's label or from whoever made it."
	fi
	log "$1; models DEFERRED, nothing written"
	notice "DEFERRED: $1. The computer installs WITHOUT models; they arrive at enrollment (MODEL_STORE_URL) or when the install is run again with the passphrase."
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
	notice "SKIPPED on request (RUNINK_SKIP_MODELS=1). The computer installs WITHOUT models; enrollment can still sync them."
	exit 0
fi
if ! PAYLOAD="$(find_payload)"; then
	[ "$REQUIRED" = 1 ] && required_or_defer "no model payload (river-models/MANIFEST) on the boot medium"
	log "no model payload on the install medium; the models arrive at enrollment (MODEL_STORE_URL)"
	notice "this medium carries no model payload. The computer installs WITHOUT models; they arrive at enrollment (MODEL_STORE_URL)."
	exit 0
fi
# content <files> <bytes> of the MANIFEST: what the unpack writes.
SET_BYTES="$(awk '$1 == "content" { print $3 }' "$PAYLOAD/MANIFEST")"
case "$SET_BYTES" in ''|*[!0-9]*) die "$PAYLOAD/MANIFEST has no content line (the payload's manifest is unreadable or damaged)." "What to do: re-create the install medium from a verified download." ;; esac
log "model payload found at $PAYLOAD ($(awk '$1 == "content" { print $2 }' "$PAYLOAD/MANIFEST") file(s), $SET_BYTES bytes)"
command -v river-modelpack >/dev/null 2>&1 || die "river-modelpack is missing from the live image, so the model payload cannot be opened." \
	"The install medium is incomplete or was not made by the official build." "What to do: re-create the install medium from a verified download and run the installer again."
[ -f "$LOCK" ] || die "no $LOCK on the live image: the payload cannot be verified, so it is refused." \
	"What to do: re-create the install medium from a verified download and run the installer again."

# --- an existing dataset (re-install into an imported pool) ----------------------------
if zfs list -H "$DS" >/dev/null 2>&1; then
	mp="$(zfs get -H -o value mountpoint "$DS")"
	log "$DS already exists (mounted at $mp); verifying it instead of unpacking"
	if river-modelpack verify --dir "$mp" --lock "$LOCK" >/dev/null; then
		log "$DS matches $LOCK; keeping it"
		exit 0
	fi
	die "$DS already exists and does NOT match $LOCK. It is left untouched." \
		"What to do: if its contents are not needed, destroy it (zfs destroy -r $DS) and run the installer again to unpack the medium's set."
fi

# --- the passphrase ----------------------------------------------------------------------
# Held in a file on tmpfs that only root can read, removed on exit (RUNINK_MODELS_RUNDIR, default
# /run, only moves it for the contract test). river-modelpack reads it
# once; it never reaches the target or the medium.
PASSDIR="$(mktemp -d "${RUNINK_MODELS_RUNDIR:-/run}/river-models.XXXXXX")"
chmod 0700 "$PASSDIR"
PASS="$PASSDIR/passphrase"

if [ -n "${RUNINK_MODELS_PASSPHRASE_FILE:-}" ]; then
	[ -r "$RUNINK_MODELS_PASSPHRASE_FILE" ] || die "cannot read the passphrase file $RUNINK_MODELS_PASSPHRASE_FILE." "What to do: check that the file exists and is readable by root."
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
	die "pool $POOL is not encrypted; refusing to put the model set on it in the clear." \
		"What to do: the pool must be created by this installer (encrypted); an existing unencrypted pool cannot be used."
fi
# Room for the whole set (plus 2%), checked before hours of reading rather than after.
avail="$(zfs get -Hp -o value available "$POOL")"
case "$avail" in ''|*[!0-9]*) die "cannot read the free space of pool $POOL." "What to do: run the installer again; if it repeats, check the disk with zpool status $POOL." ;; esac
if [ "$avail" -lt $((SET_BYTES + SET_BYTES / 50)) ]; then
	die "NOT ENOUGH FREE SPACE for the models: pool $POOL has $(gib "$avail") free ($avail bytes); the model set needs $(gib $((SET_BYTES + SET_BYTES / 50))) ($SET_BYTES bytes + 2%); $(gib $((SET_BYTES + SET_BYTES / 50 - avail))) more is needed. Nothing was written." \
		"What to do: install onto a bigger disk (or add one to the pool), or run the installer again and skip the models (they can be added at enrollment)."
fi
zfs create -o mountpoint="$MODELS_MP" -o com.sun:auto-snapshot=false -o atime=off \
	-o recordsize=1M -o compression=off "$DS" \
	|| die "cannot create the encrypted dataset $DS for the models (zfs said the line above)." "What to do: check the pool with zpool status $POOL and run the installer again."
[ "$(zfs get -H -o value encryption "$DS")" != off ] \
	|| { zfs destroy -r "$DS"; die "$DS came out unencrypted; it was destroyed and no model set was written." "What to do: run the installer again; if it repeats, the pool's encryption is broken: ask for help."; }
DEST="$(zfs get -H -o value mountpoint "$DS")"
case "$DEST" in "$TARGET"*) ;; *) DEST="$TARGET$MODELS_MP" ;; esac
[ -d "$DEST" ] || { zfs destroy -r "$DS"; die "$DS is not mounted at $DEST." "What to do: run the installer again."; }

fail() { # fail MESSAGE: destroy the partial dataset, then die
	zfs destroy -r "$DS" 2>/dev/null || true
	die "$1" "No model set is left on this computer: the dataset was destroyed." \
		"What to do: run the installer again; if it repeats, re-create the install medium from a verified download."
}
# classify ERRFILE ATTEMPT ATTEMPTS: print the reason river-modelpack failed, in plain words, and
# set CLASS (auth | medium | other). The names come from river-modelpack's own messages.
classify() {
	if grep -q 'authentication failed' "$1"; then
		CLASS=auth
		REASON="WRONG PASSPHRASE (or the payload was altered): the model payload did not open."
	elif grep -q 'medium is damaged\|does not match MANIFEST' "$1"; then
		CLASS=medium
		_part="$(sed -n 's/.*part \([^ ]*\) does not match MANIFEST.*/\1/p' "$1" | head -n 1)"
		REASON="PAYLOAD READ ERROR / sha256 MISMATCH in ${_part:+the file $_part of }the payload on the install medium ($PAYLOAD)."
	else
		CLASS=other
		REASON="river-modelpack failed: $(tail -n 3 "$1" | tr '\n' ' ')"
	fi
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
	ERRF="$PASSDIR/modelpack.err"
	river-modelpack unpack --payload "$PAYLOAD" --lock "$LOCK" --dest "$DEST" --passphrase-file "$PASS" 2> "$ERRF" || rc=$?
	cat "$ERRF" >&2
	[ "$rc" -eq 0 ] && break
	classify "$ERRF"
	# Start clean for the next attempt (or leave nothing behind).
	zfs destroy -r "$DS"
	if [ "$n" -ge "$attempts" ]; then
		case "$CLASS" in
		auth) _next="What to do: run the installer again and type the right passphrase (it is on the medium's label or from whoever made it), or skip the models and add them at enrollment." ;;
		medium) _next="What to do: the stick or disc is damaged or was written wrongly. Re-create the install medium from a verified download and run the installer again." ;;
		*) _next="What to do: run the installer again; if it repeats, keep the log and ask for help." ;;
		esac
		die "$REASON No attempts left; $DS was destroyed and no model set is on this computer." "$_next"
	fi
	SAID=1
	if [ "$CLASS" = auth ]; then
		log "$REASON Attempts left: $((attempts - n)) of $attempts. Type the passphrase again." >&2
	else
		log "$REASON Attempts left: $((attempts - n)) of $attempts; trying again." >&2
	fi
	SAID=""
	zfs create -o mountpoint="$MODELS_MP" -o com.sun:auto-snapshot=false -o atime=off \
		-o recordsize=1M -o compression=off "$DS"
done

# Independent re-check with coreutils against the build-time manifest of the same lock.
if [ -f "$SUMS" ]; then
	if ! ( cd "$DEST" && sha256sum -c "$SUMS" > "$PASSDIR/sums.out" 2>&1 ); then
		_bad="$(grep -v ': OK$' "$PASSDIR/sums.out" | head -n 5 | tr '\n' ';')"
		fail "SHA256 MISMATCH after unpacking against $SUMS: $_bad the files named here are missing or wrong."
	fi
	log "sha256sum -c $SUMS: every file OK"
fi
chmod -R a+rX "$DEST"
log "model set installed and verified: $(zfs get -H -o value used "$DS") in $DS"
notice "installed and verified ($(zfs get -H -o value used "$DS") in $DS)."
