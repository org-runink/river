#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# k0s-airgap.sh — build the k0s airgap image bundle for the pinned k0s and a k0s config (the
# reference build/k0s/k0s.yaml, or K0S_CONFIG), so a downstream server distribution brings its
# cluster up with NO network.
#
# k0s itself is one static binary (runink-k0s), but on start it runs its system components
# as pods (CoreDNS, kube-router, kube-proxy, metrics-server, konnectivity, the pause image),
# and a fresh node would pull those from quay.io and registry.k8s.io. k0s imports every
# image archive it finds in /var/lib/k0s/images/ into its containerd (namespace k8s.io,
# pinned against garbage collection) BEFORE it starts kubelet, so an archive there means
# kubelet never has to pull one of them. This script produces that archive:
#
#   1. `k0s airgap list-images -c <k0s.yaml>` for the pinned binary (sha256-checked
#      against build/pkgbuilds/runink-k0s/PKGBUILD) is the image list. k0s 1.31 lists the
#      calico images whatever the provider; they are dropped when the config's provider is
#      kuberouter (the only provider this image uses).
#   2. build/k0s-images.lock pins every image: the reference, the manifest digest pulled
#      (linux/amd64), and the image config digest (the image ID, which is what containerd
#      imports and what the archive is checked against). The list and the lock must agree:
#      a k0s or config change that alters the list fails here until the lock is updated
#      (--update-lock resolves the new digests; review the diff).
#   3. Each image is pulled rootless BY DIGEST, its ID compared with the lock, and tagged with
#      the plain reference k0s uses; `podman save --multi-image-archive` writes one
#      docker-archive (shared layers stored once); `river-payloadpack imagecheck` then
#      re-reads it: every reference present with its pinned config digest, no extra image,
#      every layer matching the config's diff_ids.
#
# Output (packaged by build/pkgbuilds/runink-k0s-airgap):
#   $OUT_DIR/k0s-airgap/k0s-airgap-bundle-<k0s version>-amd64.tar
#   $OUT_DIR/k0s-airgap/k0s-images.lock
#   $OUT_DIR/k0s-airgap/NOTICE
# A bundle already there that verifies against the lock is reused.
#
# Usage: build/k0s-airgap.sh [--update-lock]
# Environment: K0S_BIN (default: the cached, verified release binary; fetched once into
# ~/.cache/river-build/sources), K0S_CONFIG (default build/k0s/k0s.yaml), OUT_DIR (default
# build/artifacts).
# Needs: podman (rootless), go, sha256sum, and network for the first pull only.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}"
OUT_DIR="${OUT_DIR:-$REPO/build/artifacts}"
PKGBUILD="$REPO/build/pkgbuilds/runink-k0s/PKGBUILD"
CFG="${K0S_CONFIG:-$REPO/build/k0s/k0s.yaml}"
[ -f "$CFG" ] || { echo "k0s-airgap: no k0s config at $CFG" >&2; exit 1; }
LOCK="$REPO/build/k0s-images.lock"
UPDATE=0

die() { echo "k0s-airgap: $*" >&2; exit 1; }
log() { echo "k0s-airgap: $*"; }

case "${1:-}" in
	--update-lock) UPDATE=1 ;;
	'') ;;
	*) die "usage: build/k0s-airgap.sh [--update-lock]" ;;
esac
for t in podman go sha256sum; do command -v "$t" >/dev/null 2>&1 || die "$t is required"; done

# --- the pinned k0s binary ------------------------------------------------------------------
ver="$(sed -n 's/^_k0sver="\(.*\)"$/\1/p' "$PKGBUILD")"
sum="$(sed -n "s/^sha256sums=('\([0-9a-f]\{64\}\)')$/\1/p" "$PKGBUILD")"
[ -n "$ver" ] && [ -n "$sum" ] || die "cannot read _k0sver / sha256sums from $PKGBUILD"
K0S_BIN="${K0S_BIN:-$CACHE/river-build/sources/k0s-$ver-amd64}"
if [ ! -f "$K0S_BIN" ]; then
	command -v curl >/dev/null 2>&1 || die "no $K0S_BIN and no curl to fetch it"
	mkdir -p "$(dirname "$K0S_BIN")"
	log "fetching k0s $ver"
	curl -fsSL -o "$K0S_BIN.part" "https://github.com/k0sproject/k0s/releases/download/$ver/k0s-$ver-amd64"
	mv "$K0S_BIN.part" "$K0S_BIN"
fi
[ "$(sha256sum "$K0S_BIN" | cut -c1-64)" = "$sum" ] || die "$K0S_BIN does not match the PKGBUILD's sha256 $sum"
chmod +x "$K0S_BIN"

# --- the image list ---------------------------------------------------------------------------
TMP="$(mktemp -d "$CACHE/k0s-airgap.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT INT TERM
"$K0S_BIN" airgap list-images -c "$CFG" > "$TMP/listed"
[ -s "$TMP/listed" ] || die "k0s airgap list-images printed nothing"
provider="$(awk '/^  network:/{n=1} n && /^    provider:/{print $2; exit}' "$CFG")"
[ -n "$provider" ] || die "cannot read spec.network.provider from $CFG"
if [ "$provider" = kuberouter ]; then
	grep -v '/calico-' "$TMP/listed" | sort -u > "$TMP/want"
else
	sort -u "$TMP/listed" > "$TMP/want"
fi
[ -s "$TMP/want" ] || die "empty image list"

if [ "$UPDATE" -eq 1 ]; then
	log "resolving digests for $(wc -l < "$TMP/want") image(s) (linux/amd64)"
	{
		echo "# k0s-images.lock — the k0s $ver system images for build/k0s/k0s.yaml."
		echo "# Written by build/k0s-airgap.sh --update-lock; checked on every build."
		echo "# <reference> <manifest digest pulled (linux/amd64)> <config digest = image ID>"
		while read -r ref; do
			podman pull -q --arch amd64 --os linux "docker://$ref" >/dev/null
			d="$(podman image inspect --format '{{.Digest}}' "$ref")"
			id="$(podman image inspect --format '{{.Id}}' "$ref")"
			echo "$ref $d sha256:$id"
		done < "$TMP/want"
	} > "$TMP/lock"
	mv "$TMP/lock" "$LOCK"
	log "wrote $LOCK; review and commit it"
fi

[ -f "$LOCK" ] || die "no $LOCK (run with --update-lock once, and commit it)"
grep -v '^#' "$LOCK" | awk 'NF' > "$TMP/lockrows"
awk '{print $1}' "$TMP/lockrows" | sort -u > "$TMP/locked"
if ! cmp -s "$TMP/want" "$TMP/locked"; then
	echo "k0s-airgap: k0s $ver lists a different image set than $LOCK:" >&2
	diff "$TMP/locked" "$TMP/want" >&2 || true
	die "update the lock (build/k0s-airgap.sh --update-lock) and review it"
fi
awk 'NF != 3 || $2 !~ /^sha256:[0-9a-f]{64}$/ || $3 !~ /^sha256:[0-9a-f]{64}$/ { bad=1 } END { exit bad }' \
	"$TMP/lockrows" || die "$LOCK has a malformed row"
awk '{print $1, $3}' "$TMP/lockrows" > "$TMP/refs"

# --- the verifier -------------------------------------------------------------------------------
PP="$TMP/river-payloadpack"
( cd "$REPO/installer" && CGO_ENABLED=0 go build -trimpath -o "$PP" ./payloadpack )

DEST="$OUT_DIR/k0s-airgap"
BUNDLE="$DEST/k0s-airgap-bundle-$ver-amd64.tar"
mkdir -p "$DEST"
if [ -f "$BUNDLE" ] && cmp -s "$LOCK" "$DEST/k0s-images.lock" && "$PP" imagecheck --archive "$BUNDLE" --refs "$TMP/refs"; then
	log "reusing $BUNDLE (verified against the lock)"
else
	rm -f "$BUNDLE"
	set --
	while read -r ref digest id; do
		repo="${ref%:*}"
		have="$(podman image inspect --format '{{.Id}}' "$ref" 2>/dev/null || true)"
		if [ "sha256:$have" != "$id" ]; then
			log "pull $repo@$digest"
			podman pull -q --arch amd64 --os linux "docker://$repo@$digest" >/dev/null
			got="$(podman image inspect --format '{{.Id}}' "$repo@$digest")"
			[ "sha256:$got" = "$id" ] || die "$repo@$digest has image ID sha256:$got, the lock pins $id"
			podman tag "$got" "$ref"
		fi
		set -- "$@" "$ref"
	done < "$TMP/lockrows"
	log "saving $# image(s) into one docker-archive"
	podman save -q --multi-image-archive --format docker-archive -o "$BUNDLE.part" "$@"
	"$PP" imagecheck --archive "$BUNDLE.part" --refs "$TMP/refs"
	mv "$BUNDLE.part" "$BUNDLE"
fi
install -m644 "$LOCK" "$DEST/k0s-images.lock"

# --- notices ------------------------------------------------------------------------------------------
{
	echo "k0s airgap image bundle for k0s $ver (for a server image built from Runink River)."
	echo
	echo "These are unmodified upstream container images, redistributed as pulled (by digest,"
	echo "pinned in k0s-images.lock). Each image's own software is under the licence below;"
	echo "the operating-system packages inside an image are under their own licences (for"
	echo "example GPL-2.0 iptables and BusyBox in the kube-router image), and their"
	echo "corresponding source is available from the image's upstream project and base"
	echo "distribution at the version the image was built from."
	echo
	while read -r ref digest _id; do
		case "$ref" in
			*/coredns:*)                      lic="Apache-2.0"; src="https://github.com/coredns/coredns" ;;
			*/kube-router:*)                  lic="Apache-2.0"; src="https://github.com/cloudnativelabs/kube-router" ;;
			*/kube-proxy:*)                   lic="Apache-2.0"; src="https://github.com/kubernetes/kubernetes" ;;
			*/metrics-server:*)               lic="Apache-2.0"; src="https://github.com/kubernetes-sigs/metrics-server" ;;
			*/pause:*)                        lic="Apache-2.0"; src="https://github.com/kubernetes/kubernetes" ;;
			*/apiserver-network-proxy-agent:*) lic="Apache-2.0"; src="https://github.com/kubernetes-sigs/apiserver-network-proxy" ;;
			*/cni-node:*)                     lic="Apache-2.0"; src="https://github.com/k0sproject/k0s (containernetworking/plugins)" ;;
			*/calico-*)                       lic="Apache-2.0"; src="https://github.com/projectcalico/calico" ;;
			*/envoy-distroless:*)             lic="Apache-2.0"; src="https://github.com/envoyproxy/envoy" ;;
			*) die "no licence recorded for $ref: add it to the NOTICE table in $0" ;;
		esac
		printf '%s\n    %s\n    licence: %s\n    source: %s\n' "$ref" "$digest" "$lic" "$src"
	done < "$TMP/lockrows"
} > "$DEST/NOTICE"

ls -l "$BUNDLE" | awk '{ printf "k0s-airgap: %s  %d bytes\n", $NF, $5 }'
