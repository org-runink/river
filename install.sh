#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# install.sh — the one-line entry point for Runink River.
#
#   curl -fsSL https://raw.githubusercontent.com/org-runink/river/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- [--dry-run] [--out DIR] [--write /dev/sdX]
#                                        [--yes-i-have-checked-serial=SERIAL] [--lab]
#
# It detects where it runs:
#   * on any Linux host: download the latest Runink River release ISO, verify the release
#     signature (the Runink River Release Engineering OpenPGP key, see KEYS) and the ISO's
#     sha256, and optionally write it to a USB stick once you confirm the stick's serial;
#   * inside the Runink River live environment: probe the hardware, show the install plan, and
#     hand over to the installer (runink-install).
#
# It never escalates privileges: a step that needs root prints the exact command instead.
# It sends nothing anywhere: the only requests are the release files and the public key.
# Verification FAILS CLOSED: no key fingerprint, no signature or a bad checksum = no image.
#
#   --dry-run      verify the release metadata, print what would be done, change nothing
#   --out DIR      where to put the ISO (default: current directory)
#   --write DEV    after verifying, write the ISO to the USB stick DEV (erases it)
#   --yes-i-have-checked-serial=S   confirm DEV's serial without a terminal prompt
#   --lab          (live environment) plan below the documented minimums: VMs/test rigs only
#
# Environment (mirrors and testing): RIVER_RELEASE_URL (default: the latest GitHub
# release), RIVER_KEY_URL, RIVER_KEY_FPR (overrides the pinned fingerprint — you are then
# choosing what to trust).
set -eu

KEY_FPR_PINNED="95C0A7B97D547413E42660DDB06FE75626F15BF3" # the release-signing key (see KEYS)
RELEASE_URL="${RIVER_RELEASE_URL:-https://github.com/org-runink/river/releases/latest/download}"
KEY_URL="${RIVER_KEY_URL:-https://runink.org/.well-known/gpg-key.txt}"
KEY_FPR="${RIVER_KEY_FPR:-$KEY_FPR_PINNED}"

DRY=0; OUT=.; DEV=""; SERIAL=""; LAB=""
while [ $# -gt 0 ]; do
	case "$1" in
		--dry-run) DRY=1 ;;
		--out) OUT="${2:?--out needs a directory}"; shift ;;
		--write) DEV="${2:?--write needs a device}"; shift ;;
		--yes-i-have-checked-serial=?*) SERIAL="${1#*=}" ;;
		--lab) LAB=--lab ;;
		-h|--help) if [ -f "$0" ]; then sed -n '5,32p' "$0" | sed 's/^# \{0,1\}//'; else echo "usage: see the header of install.sh"; fi; exit 0 ;;
		*) echo "install.sh: unknown argument: $1" >&2; exit 2 ;;
	esac
	shift
done

say() { printf '==> %s\n' "$*"; }
die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }
run() { if [ "$DRY" = 1 ]; then say "(dry run) would run: $*"; else "$@"; fi; }
[ "$(uname -s)" = Linux ] || die "Runink River installs from Linux only."
is_root() { [ "$(id -u)" = 0 ]; }

# ---- (b) inside the Runink River live environment ------------------------------------------------
if [ "${RIVER_CONTEXT:-}" = live ] || { [ -z "${RIVER_CONTEXT:-}" ] && [ -d /run/archiso/bootmnt ] &&
	command -v river-hwprobe >/dev/null 2>&1 && command -v runink-install >/dev/null 2>&1; }; then
	say "Runink River live environment detected: probing the hardware"
	w="$(mktemp -d)"; trap 'rm -rf "$w"' EXIT
	river-hwprobe --json > "$w/probe.json"
	set -- --probe "$w/probe.json"
	tiers="${RUNINK_MODELS_TIERS:-/usr/local/share/runink/models.tiers}"
	if [ -f "$tiers" ]; then set -- "$@" --manifest "$tiers"
	else set -- "$@" --no-models; fi
	[ -z "$LAB" ] || set -- "$@" "$LAB"
	rc=0; river-plan "$@" || rc=$?
	case $rc in
		0) ;;
		3) die "this machine is REFUSED by the plan above (docs/INSTALLER-HARDWARE.md)." ;;
		*) die "planning failed (river-plan exit $rc)" ;;
	esac
	if [ "$DRY" = 1 ]; then say "(dry run) would run: runink-install $LAB"; exit 0; fi
	is_root || { say "The installer needs root. Run:"; echo "  sudo runink-install $LAB"; exit 1; }
	# stdin may be the pipe this script came from; the installer needs the console.
	# shellcheck disable=SC2086 # $LAB is empty or the single word --lab
	exec runink-install $LAB < /dev/tty
fi

# ---- (a) any Linux host: fetch + verify --------------------------------------------------
for t in curl gpg sha256sum; do
	command -v "$t" >/dev/null 2>&1 || die "$t is required (install it with your package manager)."
done
[ -n "$KEY_FPR" ] || die "the Runink River Release Engineering key fingerprint is not published yet (see KEYS).
  Refusing to download an image that cannot be verified. Nothing was downloaded."
KEY_FPR="$(printf '%s' "$KEY_FPR" | tr -d ' ' | tr '[:lower:]' '[:upper:]')"

w="$(mktemp -d)"; trap 'rm -rf "$w"' EXIT
fetch() { curl -fsSL --proto '=https,file' --tlsv1.2 -o "$2" "$1" || die "download failed: $1"; }

say "Fetching the release checksums, signature and key"
fetch "$RELEASE_URL/SHA256SUMS" "$w/SHA256SUMS"
fetch "$RELEASE_URL/SHA256SUMS.asc" "$w/SHA256SUMS.asc"
fetch "$KEY_URL" "$w/key.asc"

export GNUPGHOME="$w/gnupg"; mkdir -m 700 "$GNUPGHOME"
gpg --batch --quiet --import "$w/key.asc" 2>/dev/null || die "cannot import the release key from $KEY_URL"
gpg --batch --with-colons --fingerprint 2>/dev/null | grep -q "^fpr:::::::::$KEY_FPR:\$" \
	|| die "the key at $KEY_URL is not $KEY_FPR — refusing."
# VALIDSIG's last field is the PRIMARY key fingerprint, whichever subkey signed.
gpg --batch --status-fd 1 --verify "$w/SHA256SUMS.asc" "$w/SHA256SUMS" 2>/dev/null \
	| grep -q "^\[GNUPG:\] VALIDSIG .* $KEY_FPR\$" || die "SHA256SUMS is NOT validly signed by $KEY_FPR — refusing."
say "SHA256SUMS signature: good (Runink River Release Engineering, $KEY_FPR)"

# The Runink River image is runink-river-<date>-x86_64.iso; prefer it when a release lists
# more than one ISO, else take the first one listed.
line="$(grep -E '^[0-9a-f]{64}  \*?runink-river-[0-9]{8}-x86_64\.iso$' "$w/SHA256SUMS" | head -1)"
[ -n "$line" ] || line="$(grep -E '^[0-9a-f]{64}  \*?[A-Za-z0-9._-]+\.iso$' "$w/SHA256SUMS" | head -1)"
[ -n "$line" ] || die "no .iso listed in SHA256SUMS"
sum="${line%% *}"; iso="${line##* }"; iso="${iso#\*}"
say "Release image: $iso"

dest="$OUT/$iso"
# An ISO larger than a release asset may be (GitHub: 2 GiB) is published as numbered parts,
# <iso>.part-00, -01, … each listed in the signed SHA256SUMS next to the whole ISO: every part
# is verified, the parts are joined in order, and the result is verified against the ISO's sum.
parts="$(grep -E "^[0-9a-f]{64}  \*?$(printf '%s' "$iso" | sed 's/[.]/\\./g')\.part-[0-9]{2}\$" "$w/SHA256SUMS" | sed 's/^[0-9a-f]\{64\}  \*\{0,1\}//' | sort)"
if [ -f "$dest" ] && [ "$(sha256sum "$dest" | cut -d' ' -f1)" = "$sum" ]; then
	say "$dest is already downloaded and verified"
elif [ "$DRY" = 1 ]; then
	if [ -n "$parts" ]; then
		say "(dry run) would download $(printf '%s\n' "$parts" | wc -l) parts of $iso from $RELEASE_URL, join them into $dest and check sha256 $sum"
	else
		say "(dry run) would download $RELEASE_URL/$iso to $dest and check sha256 $sum"
	fi
else
	mkdir -p "$OUT"
	: > "$dest.part"
	if [ -n "$parts" ]; then
		for p in $parts; do
			psum="$(grep -E "  \*?$(printf '%s' "$p" | sed 's/[.]/\\./g')\$" "$w/SHA256SUMS" | cut -d' ' -f1)"
			say "Downloading $p"
			curl -fL --proto '=https,file' --tlsv1.2 -o "$dest.p" "$RELEASE_URL/$p" || { rm -f "$dest.part" "$dest.p"; die "download failed: $p"; }
			[ "$(sha256sum "$dest.p" | cut -d' ' -f1)" = "$psum" ] || { rm -f "$dest.part" "$dest.p"; die "sha256 MISMATCH for $p — deleted."; }
			cat "$dest.p" >> "$dest.part"; rm -f "$dest.p"
		done
	else
		say "Downloading $iso"
		curl -fL --proto '=https,file' --tlsv1.2 -o "$dest.part" "$RELEASE_URL/$iso" || die "download failed"
	fi
	[ "$(sha256sum "$dest.part" | cut -d' ' -f1)" = "$sum" ] || { rm -f "$dest.part"; die "sha256 MISMATCH for $iso — deleted."; }
	mv "$dest.part" "$dest"
	say "Verified: $dest"
fi

[ -n "$DEV" ] || { say "Done. Write it to a USB stick with: sh install.sh --write /dev/sdX"; exit 0; }

# ---- optional: write the verified ISO to a USB stick ---------------------------------------
name="${DEV#/dev/}"
[ -b "$DEV" ] && [ -d "/sys/block/$name" ] || die "$DEV is not a whole-disk block device."
tran="$(lsblk -dno TRAN "$DEV" 2>/dev/null | tr -d ' ')"
[ "$tran" = usb ] || [ "$(cat "/sys/block/$name/removable" 2>/dev/null)" = 1 ] \
	|| die "$DEV is not a USB/removable device (transport '${tran:-?}') — refusing."
grep -q "^$DEV" /proc/mounts && die "$DEV (or a partition of it) is mounted — unmount it first."
ser="$(lsblk -dno SERIAL "$DEV" 2>/dev/null | tr -d ' ')"
[ -n "$ser" ] || die "$DEV reports no serial; cannot confirm it — write it yourself with dd."
say "Target: $DEV — $(lsblk -dno MODEL,SIZE "$DEV" 2>/dev/null | tr -s ' '), serial $ser. IT WILL BE ERASED."
if [ -z "$SERIAL" ]; then
	[ -r /dev/tty ] || die "no terminal to confirm on: pass --yes-i-have-checked-serial=$ser"
	printf 'Type the serial of %s to confirm: ' "$DEV" > /dev/tty; read -r SERIAL < /dev/tty
fi
[ "$SERIAL" = "$ser" ] || die "serial does not match $DEV — nothing was written."
cmd="dd if=$dest of=$DEV bs=4M conv=fsync oflag=direct status=progress"
if [ "$DRY" = 1 ]; then say "(dry run) would run as root: $cmd"; exit 0; fi
is_root || { say "Writing a device needs root. Run exactly:"; echo "  sudo $cmd"; exit 1; }
run dd if="$dest" of="$DEV" bs=4M conv=fsync oflag=direct status=progress
sync
say "Written. Boot the target machine from $DEV (UEFI)."
