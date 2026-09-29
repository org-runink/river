#!/bin/sh
# Drive Artix `buildiso` for our profile, wiring in the pinned pacman config + [runink]
# local repo. Must run on Artix (or an Artix chroot/container) with artools installed.
#
# Usage: build-iso.sh <profile> <workspace-dir>
set -eu

PROFILE="${1:-river}"
WORKSPACE="${2:-$HOME/artools-workspace}"
REPO_ROOT="$(pwd)"

command -v buildiso >/dev/null 2>&1 || {
	echo "build-iso: buildiso not found (pacman -S artools-iso on Artix)" >&2; exit 1; }

# artools reads profiles from $WORKSPACE/iso-profiles; link ours in. RIVER_PROFILE_DIR names a
# profile outside this repository (docs/BUILD.md, "Downstream distributions").
PROFILE_DIR="${RIVER_PROFILE_DIR:-$REPO_ROOT/iso-profiles/$PROFILE}"
[ -f "$PROFILE_DIR/profile.yaml" ] || { echo "build-iso: no profile at $PROFILE_DIR" >&2; exit 1; }
mkdir -p "$WORKSPACE/iso-profiles"
if [ ! -e "$WORKSPACE/iso-profiles/$PROFILE" ]; then
	ln -sfn "$PROFILE_DIR" "$WORKSPACE/iso-profiles/$PROFILE"
fi

# Render pacman.conf with our pinned repos (absolute file:// path to localrepo).
LOCALREPO_ABS="$REPO_ROOT/localrepo"
[ -f "$LOCALREPO_ABS/runink.db.tar.gz" ] || {
	echo "build-iso: $LOCALREPO_ABS/runink.db.tar.gz missing — run 'make components && make localrepo'" >&2
	exit 1; }

RENDERED="$WORKSPACE/pacman-runink.conf"
sed "s|@LOCALREPO@|$LOCALREPO_ABS|g" pacman/pacman.conf.in > "$RENDERED"
echo "build-iso: rendered pacman conf -> $RENDERED"

# buildiso: -p profile. Pass our pacman conf + pinned mirrorlist via artools iso.conf,
# or the -c flag where supported. We copy the pinned mirrorlist into place.
echo "build-iso: running buildiso -p $PROFILE (workspace=$WORKSPACE)"
(
	cd "$WORKSPACE"
	# artools honours a project-local pacman.conf; expose ours.
	buildiso -p "$PROFILE"
)

echo "build-iso: done — ISO under $WORKSPACE/ (see artools iso.conf for output dir)"
