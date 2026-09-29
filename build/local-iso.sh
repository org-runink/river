#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# local-iso.sh — build a Runink River ISO on a workstation, doing everything that does not
# need root as the calling user, and print the ONE command that does.
#
# Only artools' buildiso needs root (mount, chroot, loop devices). Everything before it runs
# here, rootless. Every component this project builds is built FROM SOURCE on every run, with
# makepkg: nothing is reused from an earlier image (third-party inputs that are pinned by hash,
# the builder image, the k0s airgap images and the model weights, are the only things cached):
#
#   0. the profile copy, for an external profile (RIVER_PROFILE_DIR) or edition branding
#      (RIVER_BRANDING_DIR): docs/BUILD.md "Downstream distributions"
#   1. the Artix builder image                  (podman, `make builder`)
#   2. linux-runink + runink-zfs                (build/build-kernel-zfs.sh, rootless podman;
#                                                rebuilt every run, then build/verify-kernel-zfs.sh)
#   3. the downstream payload, if any           ("$RIVER_PAYLOAD_DIR/build.sh", on this host)
#      and the k0s airgap bundle                (build/k0s-airgap.sh, rootless podman)
#   4. the component packages                   (build/build-all.sh in the builder, rootless)
#   5. the [runink] local repository            (per profile, under $STATE)
#   6. the encrypted payloads (PRIVATE server) (models: river-modelpack; the downstream's
#                                                RIVER_PAYLOAD_STAGE: river-payloadpack;
#                                                docs/PAYLOADS.md)
#   7. the pre-flight lints                     (Tier 1, closure lint for the in-tree profile
#                                                and a staged one, installer/branding sync,
#                                                go tests)
#   8. the builder image as an archive root's podman can load, and the root-stage file
#
# Then it prints:   sudo sh <repo>/build/iso-root-stage.sh <state>/root-stage-<profile>.env
# which runs buildiso in a privileged container, attaches the model payload to a private
# server ISO (a downstream profile), checks the result and hands the ISO back to you.
#
# Usage:  build/local-iso.sh [--no-lint]
# PUBLIC and PRIVATE images. A build is PUBLIC unless it has a downstream payload
# (RIVER_PAYLOAD_DIR): the public Runink River ISO is the OS, the desktop and the installer,
# nothing else, and this script and iso-root-stage.sh REFUSE to attach any payload (models or
# downstream) to it. A private build (RIVER_PUBLIC=0, implied by RIVER_PAYLOAD_DIR) may carry
# the encrypted payloads and is named <iso-name>-<RIVER_ISO_VARIANT>-<date>-x86_64.iso. Only a server profile, which
# lives downstream, has a private variant.
#
# Environment (all optional):
#   PROFILE            river (Runink River, the only in-tree profile; the default); with
#                      RIVER_PROFILE_DIR, the external profile's name (default: its basename)
#   RIVER_PROFILE_DIR  a profile OUTSIDE this repository (a downstream distribution; it needs a
#                      river-profile.env, build/profile-lib.sh and docs/BUILD.md "Downstream
#                      distributions"). It is copied to $STATE/profiles/ and built from there.
#   RIVER_BRANDING_DIR an edition's branding: <dir>/overlay/ is layered onto the (copied)
#                      profile, <dir>/remove lists profile-relative paths deleted from the copy
#   RIVER_PUBLIC       1 (default without RIVER_PAYLOAD_DIR) | 0 (default with it)
#   RIVER_ISO_VARIANT  private builds: the ISO name suffix (a lower-case word; default "private")
#   RIVER_PAYLOAD_STAGE
#                      private builds: plaintext downstream payloads, <stage>/<kind>/<group>/
#                      holding the files and a generic LOCK (river-payloadpack lock); each is
#                      packed with the medium passphrase to /river-<kind>/<group>/ on the ISO
#   RIVER_PAYLOAD_DIR  a downstream payload (docs/BUILD.md); server profile only
#   MODELS_DIR         the models.lock cache (default ~/.cache/river-build/models)
#   MODEL_PAYLOAD      auto (default: a PRIVATE server build with a MODELS_DIR) | yes | no
#   RIVER_MODELS_PASSPHRASE_FILE
#                      the medium passphrase, for the models AND every downstream payload (default ~/.cache/river-build/secrets/
#                      models-passphrase; generated, 0600 in a 0700 directory, if absent).
#                      It is NEVER copied to the ISO; keep it off the stick.
#   OUT_DIR            where the finished ISO lands (default ~/.cache/river-build/iso-out)
#   KERNEL_OUT         kernel/ZFS packages (default ~/.cache/river-build/kernel)
#   BUILDER_IMAGE      default runink-os-builder
#   STATE              working state (default ~/.cache/river-build/local-iso)
# Big files go under ~/.cache on purpose: /tmp is often a tmpfs, and filling it kills
# processes without a disk-full error.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}"
RIVER_PROFILE_DIR="${RIVER_PROFILE_DIR:-}"
RIVER_BRANDING_DIR="${RIVER_BRANDING_DIR:-}"
if [ -n "$RIVER_PROFILE_DIR" ]; then
	RIVER_PROFILE_DIR="$(cd "$RIVER_PROFILE_DIR" 2>/dev/null && pwd)" || { echo "local-iso: RIVER_PROFILE_DIR is not a directory" >&2; exit 1; }
	PROFILE="${PROFILE:-$(basename "$RIVER_PROFILE_DIR")}"
fi
PROFILE="${PROFILE:-river}"
RIVER_PAYLOAD_DIR="${RIVER_PAYLOAD_DIR:-}"
MODELS_DIR="${MODELS_DIR:-$CACHE/river-build/models}"
MODEL_PAYLOAD="${MODEL_PAYLOAD:-auto}"
PASSFILE="${RIVER_MODELS_PASSPHRASE_FILE:-$CACHE/river-build/secrets/models-passphrase}"
OUT_DIR="${OUT_DIR:-$CACHE/river-build/iso-out}"
KERNEL_OUT="${KERNEL_OUT:-$CACHE/river-build/kernel}"
BUILDER_IMAGE="${BUILDER_IMAGE:-runink-os-builder}"
STATE="${STATE:-$CACHE/river-build/local-iso}"
RIVER_PAYLOAD_STAGE="${RIVER_PAYLOAD_STAGE:-}"
RIVER_ISO_VARIANT="${RIVER_ISO_VARIANT:-}"
if [ -n "$RIVER_PAYLOAD_DIR" ]; then RIVER_PUBLIC="${RIVER_PUBLIC:-0}"; else RIVER_PUBLIC="${RIVER_PUBLIC:-1}"; fi
LINT=1

log() { printf '\n== local-iso: %s\n' "$*"; }
die() { echo "local-iso: $*" >&2; exit 1; }

case "${1:-}" in
	--no-lint) LINT=0 ;;
	'') ;;
	-h|--help) sed -n '5,65p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) die "unknown argument $1 (see --help)" ;;
esac

[ "$(id -u)" -ne 0 ] || die "run this as your user; it prints the single command that needs root"
case "$PROFILE" in *[!a-z0-9-]*|-*|'') die "PROFILE must be a lower-case name ([a-z0-9-])" ;; esac
# The profile source: in this repository, or a downstream's (RIVER_PROFILE_DIR).
if [ -n "$RIVER_PROFILE_DIR" ]; then
	PROFILE_SRC="$RIVER_PROFILE_DIR"
	[ -f "$PROFILE_SRC/river-profile.env" ] || die "$PROFILE_SRC has no river-profile.env (docs/BUILD.md, Downstream distributions)"
else
	PROFILE_SRC="$REPO/iso-profiles/$PROFILE"
fi
[ -f "$PROFILE_SRC/profile.yaml" ] || die "no profile at $PROFILE_SRC (no profile.yaml)"
if [ -n "$RIVER_BRANDING_DIR" ]; then
	RIVER_BRANDING_DIR="$(cd "$RIVER_BRANDING_DIR" 2>/dev/null && pwd)" || die "RIVER_BRANDING_DIR is not a directory"
	[ -d "$RIVER_BRANDING_DIR/overlay" ] || die "RIVER_BRANDING_DIR=$RIVER_BRANDING_DIR has no overlay/"
fi
# shellcheck source=build/profile-lib.sh
. "$HERE/profile-lib.sh"
for t in podman go git sha256sum zstd repo-add bsdtar openssl; do
	command -v "$t" >/dev/null 2>&1 || die "$t is required"
done
case "$MODELS_DIR$OUT_DIR$KERNEL_OUT$STATE$REPO$PROFILE_SRC$RIVER_BRANDING_DIR" in *' '*) die "paths with spaces are not supported" ;; esac
mkdir -p "$STATE" "$OUT_DIR"

# Which extras this profile takes. The workstation carries no platform payload and no models.
# An in-tree profile is known by name; an external one says so in river-profile.env.
river_profile_read "$PROFILE_SRC" "$PROFILE" || die "cannot use profile $PROFILE"
SERVER=0
[ "$RP_KIND" = server ] && SERVER=1
if [ "$SERVER" -eq 0 ] && [ -n "$RIVER_PAYLOAD_DIR" ]; then
	echo "local-iso: $PROFILE takes no downstream payload; ignoring RIVER_PAYLOAD_DIR" >&2
	RIVER_PAYLOAD_DIR=""
fi
# PUBLIC artifacts carry NO payload of any kind (no models, no images, no apps): refuse,
# rather than silently drop, anything that asks for one.
case "$RIVER_PUBLIC" in
	1)
		[ -z "$RIVER_PAYLOAD_STAGE" ] || die "RIVER_PUBLIC=1: a public image carries no downstream payload (unset RIVER_PAYLOAD_STAGE)"
		[ "$MODEL_PAYLOAD" != yes ] || die "RIVER_PUBLIC=1: a public image carries no model payload (MODEL_PAYLOAD=yes refused)"
		[ -z "$RIVER_ISO_VARIANT" ] || die "RIVER_PUBLIC=1: the public image has no variant name"
		MODEL_PAYLOAD=no
		;;
	0)
		[ "$SERVER" -eq 1 ] || die "RIVER_PUBLIC=0: only a server profile has a private variant"
		RIVER_ISO_VARIANT="${RIVER_ISO_VARIANT:-private}"
		case "$RIVER_ISO_VARIANT" in *[!a-z0-9-]*|-*) die "RIVER_ISO_VARIANT must be a lower-case word" ;; esac
		if [ -n "$RIVER_PAYLOAD_STAGE" ]; then
			[ -d "$RIVER_PAYLOAD_STAGE" ] || die "RIVER_PAYLOAD_STAGE=$RIVER_PAYLOAD_STAGE is not a directory"
			case "$RIVER_PAYLOAD_STAGE" in *' '*) die "paths with spaces are not supported" ;; esac
		fi
		;;
	*) die "RIVER_PUBLIC must be 0 or 1" ;;
esac
case "$MODEL_PAYLOAD" in
	auto) if [ "$SERVER" -eq 1 ] && [ "$RIVER_PUBLIC" = 0 ] && { [ -d "$MODELS_DIR" ] || [ -f "$STATE/model-payload/river-models/MANIFEST" ]; }; then
		MODEL_PAYLOAD=yes; else MODEL_PAYLOAD=no; fi ;;
	yes) [ "$SERVER" -eq 1 ] || die "MODEL_PAYLOAD=yes is for the server profile only" ;;
	no) ;;
	*) die "MODEL_PAYLOAD must be auto, yes or no" ;;
esac
echo "local-iso: PROFILE=$PROFILE ($RP_KIND, label $RP_ISO_LABEL${RIVER_PROFILE_DIR:+, from $RIVER_PROFILE_DIR}${RIVER_BRANDING_DIR:+, branding $RIVER_BRANDING_DIR}) RIVER_PUBLIC=$RIVER_PUBLIC${RIVER_ISO_VARIANT:+ RIVER_ISO_VARIANT=$RIVER_ISO_VARIANT} MODEL_PAYLOAD=$MODEL_PAYLOAD${RIVER_PAYLOAD_STAGE:+ RIVER_PAYLOAD_STAGE=$RIVER_PAYLOAD_STAGE}"

# A private variant keeps its own repository, work dir and stage file, so the public and the
# private server image can be prepared side by side (their runink-core packages differ).
FLAVOR="$PROFILE${RIVER_ISO_VARIANT:+-$RIVER_ISO_VARIANT}"

# --- 0. staged profile (external profile and/or edition branding) ---------------------------
# An external profile, or any profile with a RIVER_BRANDING_DIR, is built from a COPY under
# $STATE: the source is never written to (the LEAN build moves files inside the profile, and
# the guide model is staged into it). The in-tree profile without branding is built in place,
# exactly as before.
PROFILE_DIR=""
if [ -n "$RIVER_PROFILE_DIR" ] || [ -n "$RIVER_BRANDING_DIR" ]; then
	PROFILE_DIR="$STATE/profiles/$FLAVOR/$PROFILE"
	log "0/8 profile copy ($PROFILE_DIR)"
	river_profile_stage "$PROFILE_SRC" "$RIVER_BRANDING_DIR" "$PROFILE_DIR" || die "cannot stage the profile"
	if [ -n "$RIVER_BRANDING_DIR" ]; then
		echo "local-iso: branding overlay $RIVER_BRANDING_DIR/overlay applied"
		# The overlay may carry the profile's own river-profile.env (label, ISO name, title).
		river_profile_read "$PROFILE_DIR" "$PROFILE" || die "the branded profile does not describe itself"
		[ "$RP_KIND" = "$([ "$SERVER" -eq 1 ] && echo server || echo workstation)" ] \
			|| die "the branding overlay changes KIND; a branding overlay cannot turn a server into a workstation"
	fi
fi
# river-guide (the install guide agent) is on a medium only when its profile says so with a
# guide-model.lock; Runink River's own profile does not carry it. river-guide and its model are
# built from guide/model.lock, so a profile that ships the guide must pin the same model.
PDIR="${PROFILE_DIR:-$PROFILE_SRC}"
GUIDE=0
if [ -f "$PDIR/guide-model.lock" ]; then
	cmp -s "$PDIR/guide-model.lock" "$REPO/guide/model.lock" \
		|| die "$PROFILE_SRC/guide-model.lock differs from guide/model.lock (river-guide is built from the latter)"
	GUIDE=1
fi
# The graphical installer must find itself among the profile's edition descriptors.
river_profile_self_edition "${PROFILE_DIR:-$PROFILE_SRC}" "$RP_ISO_LABEL" "$RP_EDITION" || die "fix the profile's edition descriptors"

# --- 1. builder image ------------------------------------------------------------------
log "1/8 builder image ($BUILDER_IMAGE)"
podman image exists "$BUILDER_IMAGE" || podman build -t "$BUILDER_IMAGE" "$REPO/builder"

# --- 2. kernel + ZFS ---------------------------------------------------------------------
log "2/8 linux-runink + runink-zfs ($KERNEL_OUT)"
# Always from source: packages from an earlier run never reach this image.
rm -rf "${KERNEL_OUT:?}/pkgs"
echo "local-iso: building the kernel and ZFS from source (about an hour; nice 19)"
KERNEL_OUT="$KERNEL_OUT" BUILDER_IMAGE="$BUILDER_IMAGE" "$HERE/build-kernel-zfs.sh"
"$HERE/verify-kernel-zfs.sh" "$KERNEL_OUT" || die "the kernel/ZFS packages just built do not verify"

# --- 3. downstream payload -----------------------------------------------------------------
ART="$REPO/build/artifacts"
PREBUILT=0
log "3/8 downstream payload"
rm -rf "${ART:?}/bin" "$ART/core-tree" "$ART/models.tiers" "$ART/models.lock" "$ART/models.manifest"
if [ -n "$RIVER_PAYLOAD_DIR" ]; then
	[ -x "$RIVER_PAYLOAD_DIR/build.sh" ] || die "RIVER_PAYLOAD_DIR=$RIVER_PAYLOAD_DIR has no executable build.sh"
	"$RIVER_PAYLOAD_DIR/build.sh" --check
	mkdir -p "$ART"
	"$RIVER_PAYLOAD_DIR/build.sh" "$ART"
	PREBUILT=1
else
	echo "local-iso: no downstream payload (a base image)"
fi
# The k0s airgap bundle (runink-k0s-airgap): every image k0s itself runs, pinned by digest in
# build/k0s-images.lock, so a downstream server brings its cluster up with no network. Built
# here on the host (rootless podman pulls them); reused when it still verifies. Runink River does
# not install the package, but build-all packages every component. The image list comes from
# the profile's own /etc/k0s/k0s.yaml when it has one, else from the reference build/k0s/k0s.yaml.
echo "local-iso: k0s airgap bundle"
K0S_CFG="$REPO/build/k0s/k0s.yaml"
[ -f "$PDIR/root-overlay/etc/k0s/k0s.yaml" ] && K0S_CFG="$PDIR/root-overlay/etc/k0s/k0s.yaml"
OUT_DIR="$ART" K0S_CONFIG="$K0S_CFG" "$HERE/k0s-airgap.sh"
# The lock the image ships (runink-installer takes a payload-staged one first) is the lock
# the model payload must be packed from: the installer refuses a payload from another lock.
LOCK="$REPO/models.lock"
[ -f "$ART/models.lock" ] && LOCK="$ART/models.lock"

# --- 4. component packages -------------------------------------------------------------------
log "4/8 component packages (build/build-all.sh in $BUILDER_IMAGE, rootless)"
# Step 5 copies every package under build/pkgbuilds/; clear the ones an earlier run left, so
# only what makepkg builds now can reach the repository.
find "$REPO/build/pkgbuilds" -name '*.pkg.tar.zst' -delete
# A FINISHED model payload of this lock stands in for the plaintext cache, which an incremental
# pack (river-modelpack pack --append --consume) deletes as it goes: step 6 checks the payload's
# parts and passphrase before the image uses it, and repacks from a fresh fetch if they fail.
MODELS_IN_PAYLOAD=0
MPAY="$STATE/model-payload/river-models"
if [ "$MODEL_PAYLOAD" = yes ] && [ -f "$MPAY/MANIFEST" ] && ! grep -qx incomplete "$MPAY/MANIFEST" \
	&& grep -qx "lock-sha256 $(sha256sum "$LOCK" | cut -c1-64)" "$MPAY/MANIFEST"; then
	MODELS_IN_PAYLOAD=1
	echo "local-iso: the model set is in the finished payload $MPAY (checked at step 6)"
fi
set -- -v "$REPO:/os" -w /os -e RIVER_PAYLOAD_PREBUILT="$PREBUILT" -e RIVER_GUIDE_MODEL="$GUIDE" -e MODEL_PAYLOAD="$MODEL_PAYLOAD" \
	-e RIVER_MODELS_IN_PAYLOAD="$MODELS_IN_PAYLOAD"
# A no-model image (MODEL_PAYLOAD=no) carries no models.manifest either: a manifest would
# describe weights that are not on the medium.
if [ "$SERVER" -eq 1 ] && [ "$MODEL_PAYLOAD" != no ] && [ -d "$MODELS_DIR" ]; then
	set -- "$@" -v "$MODELS_DIR:/models:ro" -e MODELS_DIR=/models
else
	set -- "$@" -e MODELS_DIR=/nonexistent
fi
[ "$LOCK" = "$REPO/models.lock" ] || set -- "$@" -e MODELS_LOCK=/os/build/artifacts/models.lock
# A worktree keeps its git metadata outside the checkout; the build does not need git.
podman run --rm --userns=keep-id --user builder "$@" "$BUILDER_IMAGE" build/build-all.sh

# The live guide: a profile that ships river-guide (its lock was checked above) gets the model
# build-all.sh just staged under build/artifacts/river-guide/model.
if [ "$GUIDE" = 1 ]; then
	gm=live-overlay/usr/share/river-guide/model
	[ -d "$ART/river-guide/model" ] || die "build-all.sh staged no guide model ($ART/river-guide/model)"
	rm -rf "${PDIR:?}/$gm"
	mkdir -p "$(dirname "$PDIR/$gm")"
	# Hard links when the profile is on the same filesystem (1 GB, read-only use).
	if ! cp -al "$ART/river-guide/model" "$PDIR/$gm" 2>/dev/null; then
		rm -rf "${PDIR:?}/$gm"
		cp -a "$ART/river-guide/model" "$PDIR/$gm"
	fi
fi

# --- 5. local repository ----------------------------------------------------------------------
REPODIR="$STATE/localrepo-$FLAVOR"
# The root stage mounts REPODIR at /os/localrepo; the mount point must exist and be yours.
mkdir -p "$REPO/localrepo"
log "5/8 [runink] repository ($REPODIR)"
rm -rf "$REPODIR"
mkdir -p "$REPODIR"
cp "$KERNEL_OUT"/pkgs/*.pkg.tar.zst "$REPODIR/"
for p in "$REPO"/build/pkgbuilds/*/*.pkg.tar.zst; do
	case "$p" in */runink-kernel/*|*/runink-zfs/*) continue ;; esac   # only the verified set
	[ -e "$p" ] && cp "$p" "$REPODIR/"
done
( cd "$REPODIR" && repo-add -q runink.db.tar.gz ./*.pkg.tar.zst )
ls -l "$REPODIR"/*.pkg.tar.zst | awk '{ printf "  %10d  %s\n", $5, $NF }'

# --- 6. model payload --------------------------------------------------------------------------
PAYLOAD_DIR=""
log "6/8 model payload (MODEL_PAYLOAD=$MODEL_PAYLOAD)"
if [ "$MODEL_PAYLOAD" = yes ]; then
	MP="$STATE/bin/river-modelpack"
	mkdir -p "$STATE/bin"
	( cd "$REPO/installer" && CGO_ENABLED=0 GOAMD64=v1 go build -trimpath -ldflags='-s -w' -o "$MP" ./modelpack )
	if [ ! -s "$PASSFILE" ]; then
		( umask 077; mkdir -p "$(dirname "$PASSFILE")"; chmod 0700 "$(dirname "$PASSFILE")"
		  "$MP" genpass > "$PASSFILE" )
		cat >&2 <<-EOF

			  NEW MODEL PAYLOAD PASSPHRASE written to $PASSFILE (0600).
			  The installer asks for it to unpack the models. It is not on the ISO. Put it in
			  your password manager (or on paper) and keep this file OFF the USB stick.

		EOF
	fi
	[ "$(stat -c %a "$PASSFILE")" = 600 ] || die "$PASSFILE must be mode 0600"
	PAYLOAD_DIR="$STATE/model-payload/river-models"
	want="$(sha256sum "$LOCK" | cut -c1-64)"
	have="$(sed -n 's/^lock-sha256 //p' "$PAYLOAD_DIR/MANIFEST" 2>/dev/null || true)"
	# An incremental pack of this lock still in progress is never reused, and never thrown away:
	# finish it (docs/MODEL-PAYLOAD.md, "Packing a large set incrementally").
	if [ "$have" = "$want" ] && grep -qx incomplete "$PAYLOAD_DIR/MANIFEST"; then
		die "$PAYLOAD_DIR is an INCOMPLETE model payload of this lock ($("$MP" pending --payload "$PAYLOAD_DIR" --lock "$LOCK" | wc -l) file(s) to go); finish it with river-modelpack pack --append, or remove it to pack from scratch"
	fi
	if [ "$have" = "$want" ] && "$MP" check --payload "$PAYLOAD_DIR" --passphrase-file "$PASSFILE"; then
		echo "local-iso: reusing the model payload (same lock, parts verified, passphrase opens it)"
	else
		rm -rf "$PAYLOAD_DIR"
		# Fetches only what is missing or wrong (every file is re-hashed while packing anyway).
		MODELS_DIR="$MODELS_DIR" MODELS_LOCK="$LOCK" "$HERE/models-fetch.sh"
		nice -n 19 "$MP" pack --lock "$LOCK" --models-dir "$MODELS_DIR" --out "$PAYLOAD_DIR" \
			--passphrase-file "$PASSFILE" --zstd-level 12 --zstd-threads "$(( $(nproc) / 2 ))"
	fi
	du -sh "$PAYLOAD_DIR" | sed 's/^/  payload size: /'
fi

# Downstream payloads (private builds): every <stage>/<kind>/<group>/ with a LOCK becomes an
# encrypted payload at <state>/payloads/river-<kind>/<group>/ (docs/PAYLOADS.md), sealed with
# the SAME medium passphrase as the models. It is packed afresh on every run, from what step 3
# just built: a payload from an earlier image is never reused.
PAYLOADS_DIR=""
if [ -n "$RIVER_PAYLOAD_STAGE" ]; then
	log "6/8 downstream payloads ($RIVER_PAYLOAD_STAGE)"
	PP="$STATE/bin/river-payloadpack"
	mkdir -p "$STATE/bin"
	( cd "$REPO/installer" && CGO_ENABLED=0 GOAMD64=v1 go build -trimpath -ldflags='-s -w' -o "$PP" ./payloadpack )
	if [ ! -s "$PASSFILE" ]; then
		( umask 077; mkdir -p "$(dirname "$PASSFILE")"; chmod 0700 "$(dirname "$PASSFILE")"
		  "$PP" genpass > "$PASSFILE" )
		echo "local-iso: NEW MEDIUM PASSPHRASE written to $PASSFILE (0600); keep it OFF the stick" >&2
	fi
	[ "$(stat -c %a "$PASSFILE")" = 600 ] || die "$PASSFILE must be mode 0600"
	PAYLOADS_DIR="$STATE/payloads"
	mkdir -p "$PAYLOADS_DIR"
	n=0
	for lk in "$RIVER_PAYLOAD_STAGE"/*/*/LOCK; do
		[ -f "$lk" ] || continue
		g="$(dirname "$lk")"; group="$(basename "$g")"; kind="$(basename "$(dirname "$g")")"
		[ "$kind" != models ] || die "$g: the model payload is built from models.lock, not from the stage"
		grep -qx "kind $kind" "$lk" && grep -qx "group $group" "$lk" \
			|| die "$lk must say kind $kind and group $group (the installer checks the LOCK against its place)"
		out="$PAYLOADS_DIR/river-$kind/$group"
		rm -rf "$out"
		"$PP" verify --dir "$g" --lock "$lk" >/dev/null || die "$g does not match its LOCK"
		nice -n 19 "$PP" pack --lock "$lk" --dir "$g" --out "$out" --passphrase-file "$PASSFILE" \
			--zstd-level 12 --zstd-threads "$(( $(nproc) / 2 ))"
		n=$((n + 1))
	done
	[ "$n" -gt 0 ] || die "RIVER_PAYLOAD_STAGE=$RIVER_PAYLOAD_STAGE holds no <kind>/<group>/LOCK"
	# A payload no longer staged must not ride along from an earlier build.
	for d in "$PAYLOADS_DIR"/river-*/*; do
		[ -d "$d" ] || continue
		k="$(basename "$(dirname "$d")")"; k="${k#river-}"
		[ -f "$RIVER_PAYLOAD_STAGE/$k/$(basename "$d")/LOCK" ] || { echo "local-iso: dropping stale $d"; rm -rf "$d"; }
	done
	du -sh "$PAYLOADS_DIR"/river-* | sed 's/^/  downstream payload: /'
fi

# --- 7. lints ------------------------------------------------------------------------------------
if [ "$LINT" -eq 1 ]; then
	log "7/8 pre-flight lints"
	sh "$REPO/scripts/ci-tier1.sh"
	( cd "$REPO/installer" && test -z "$(gofmt -l .)" && go vet ./... && go test -count=1 ./... )
	( cd "$REPO/guide" && test -z "$(gofmt -l .)" && go vet ./... && go test -count=1 ./... )
	# The lints ported to Go run from one river binary (docs/GO-CLI.md), static so the builder
	# container below can run it too.
	RIVER="$STATE/river"
	( cd "$REPO/cli" && CGO_ENABLED=0 go build -buildvcs=false -o "$RIVER" ./cmd/river )
	LOCALREPO="$REPODIR" "$RIVER" lint k0s-pin --repo "$REPO"
	# A staged profile gets the profile checks Tier 1 runs on the in-tree ones: its manifest,
	# its installer steps and driver against installer/lib.
	set --
	if [ -n "$PROFILE_DIR" ]; then
		"$RIVER" lint profile-manifest --repo "$REPO" "$PROFILE_DIR"
		"$RIVER" lint installer-sync --repo "$REPO" "$PROFILE_DIR"
		set -- -v "$PROFILE_DIR:/staged/$PROFILE:ro"
	fi
	# The closure lint needs pactree and every repo the manifests resolve from, including this
	# [runink] repo, so it runs in the builder with the repo injected ahead of [system]. The
	# in-tree profile is checked, whichever one is being built, and the staged one if any.
	podman run --rm --user root -v "$REPO:/os:ro" -v "$REPODIR:/localrepo:ro" -v "$RIVER:/usr/local/bin/river:ro" "$@" "$BUILDER_IMAGE" sh -euc '
		pacman -Sy --noconfirm --needed pacman-contrib >/dev/null
		sed -i "0,/^\[system\]/s||[runink]\nSigLevel = Optional TrustAll\nServer = file:///localrepo\n\n[system]|" /etc/pacman.conf
		pacman -Sy >/dev/null
		for p in /os/iso-profiles/*/Packages-Root /staged/*/Packages-Root; do
			[ -f "$p" ] || continue
			echo "== lint-closure $p"; river lint closure "$p"
		done'
else
	log "7/8 lints SKIPPED (--no-lint): do not ship this image"
fi

# --- 8. root stage -----------------------------------------------------------------------------------
log "8/8 root stage"
ARCHIVE="$STATE/builder-image.oci.tar"
ID="$(podman image inspect --format '{{.Id}}' "$BUILDER_IMAGE")"
if [ "$(cat "$ARCHIVE.id" 2>/dev/null)" != "$ID" ]; then
	rm -f "$ARCHIVE"
	podman save --format oci-archive -o "$ARCHIVE" "$BUILDER_IMAGE"
	echo "$ID" > "$ARCHIVE.id"
fi
ENVF="$STATE/root-stage-$FLAVOR.env"
{
	echo "# written by build/local-iso.sh $(date -u +%Y-%m-%dT%H:%M:%SZ); read by build/iso-root-stage.sh"
	echo "PROFILE=$PROFILE"
	echo "REPO=$REPO"
	echo "LOCALREPO=$REPODIR"
	echo "OUT_DIR=$OUT_DIR"
	echo "WORK_DIR=$STATE/artools-$FLAVOR"
	echo "BUILDER_ARCHIVE=$ARCHIVE"
	echo "BUILDER_ID=$ID"
	echo "MODEL_PAYLOAD_DIR=$PAYLOAD_DIR"
	echo "PAYLOADS_DIR=$PAYLOADS_DIR"
	echo "PUBLIC=$RIVER_PUBLIC"
	echo "ISO_VARIANT=$RIVER_ISO_VARIANT"
	echo "KIND=$RP_KIND"
	echo "ISO_NAME=$RP_ISO_NAME"
	echo "PROFILE_DIR=$PROFILE_DIR"
} > "$ENVF"
chmod 0644 "$ENVF"
cat <<EOF

================================================================================
 Everything that runs without root is done for PROFILE=$PROFILE.
 Now run, as yourself (it asks for your password once):

   sudo sh $HERE/iso-root-stage.sh $ENVF

 It loads the builder image into root's podman, runs buildiso in a privileged
 container (work dir $STATE/artools-$FLAVOR, removed after),
$( [ -n "$PAYLOAD_DIR" ] && echo " attaches the encrypted model payload ($(du -sh "$PAYLOAD_DIR" | cut -f1)) under /river-models," )
$( [ -n "$PAYLOADS_DIR" ] && echo " attaches the downstream payloads ($(du -sh "$PAYLOADS_DIR" | cut -f1)) under /river-<kind>/," )
$( [ "$RIVER_PUBLIC" = 1 ] && echo " (PUBLIC image: it refuses to attach any payload)" || echo " names it $RP_ISO_NAME-$RIVER_ISO_VARIANT-<date>-x86_64.iso," )
 checks the label and the ZFS module, and leaves the ISO + .sha256 in
 $OUT_DIR, owned by you.
================================================================================
EOF
