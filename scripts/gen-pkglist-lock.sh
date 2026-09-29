#!/bin/sh
# Resolve Packages-Root against the pinned repos and record the full package set +
# versions into Pkglist.lock, for reproducibility. CI runs this twice and asserts the
# output is byte-identical.
#
# Usage: gen-pkglist-lock.sh <profile-dir>
set -eu

PROFILE_DIR="${1:?usage: gen-pkglist-lock.sh <profile-dir>}"
MANIFEST="$PROFILE_DIR/Packages-Root"
LOCK="$PROFILE_DIR/Pkglist.lock"
[ -f "$MANIFEST" ] || { echo "gen-pkglist-lock: no $MANIFEST" >&2; exit 1; }

pkgs="$(grep -vE '^\s*(#|$)' "$MANIFEST" | tr -s ' \t' '\n' | grep -vE '^$' || true)"

if ! command -v pacman >/dev/null 2>&1; then
	echo "gen-pkglist-lock: pacman unavailable — cannot resolve; run on Arch/Artix" >&2
	exit 1
fi

{
	echo "# Pkglist.lock — resolved closure of Packages-Root (generated; do not hand-edit)"
	echo "# regenerate: make lock"
	# -Sp prints the full dependency-resolved package list; strip URLs to name-version.
	# shellcheck disable=SC2086
	pacman -Sp --print-format '%n %v' $pkgs 2>/dev/null | sort -u
} > "$LOCK"

echo "gen-pkglist-lock: wrote $LOCK ($(grep -cvE '^#' "$LOCK") packages)"
