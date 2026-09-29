#!/bin/sh
# Assemble the pinned [runink] pacman repo from the built packages.
# Usage: make-localrepo.sh <localrepo-dir>
set -eu

REPO_DIR="${1:-localrepo}"
DBNAME="runink"

mkdir -p "$REPO_DIR"

# Gather packages produced by build/build-all.sh (build/artifacts/*.pkg.tar.zst)
found=0
for pkg in build/artifacts/*.pkg.tar.zst build/pkgbuilds/*/*.pkg.tar.zst; do
	[ -e "$pkg" ] || continue
	cp -f "$pkg" "$REPO_DIR/"
	found=1
done

if [ "$found" -eq 0 ]; then
	echo "make-localrepo: no *.pkg.tar.zst found — run 'make components' first" >&2
	exit 1
fi

if ! command -v repo-add >/dev/null 2>&1; then
	echo "make-localrepo: repo-add (pacman) not found — packages copied but DB not built" >&2
	echo "  run 'make localrepo' on Arch/Artix to generate $REPO_DIR/$DBNAME.db.tar.gz" >&2
	exit 1
fi

( cd "$REPO_DIR" && repo-add -q "$DBNAME.db.tar.gz" ./*.pkg.tar.zst )
echo "make-localrepo: $REPO_DIR/$DBNAME.db.tar.gz built with $(find "$REPO_DIR" -maxdepth 1 -name '*.pkg.tar.zst' | wc -l) packages"
