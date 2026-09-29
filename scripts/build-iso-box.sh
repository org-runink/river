#!/bin/bash
set -e
# The profile: iso-profiles/<PROFILE> in this checkout, or a staged one (an external profile
# or one with edition branding) that build/iso-root-stage.sh mounts and names in
# RIVER_PROFILE_DIR. What it says about itself (kind, ISO label and name, live menu title)
# comes from build/profile-lib.sh: known by name in-tree, river-profile.env otherwise.
PROFILE="${PROFILE:-river}"
PROFILE_DIR="${RIVER_PROFILE_DIR:-/os/iso-profiles/${PROFILE}}"
# shellcheck source=build/profile-lib.sh
. /os/build/profile-lib.sh
river_profile_read "$PROFILE_DIR" "$PROFILE" || exit 1
CONF=/usr/share/artools/pacman.conf.d/iso-x86_64.conf
# One Go helper does every in-place patch this script needs (pacman conf + artools).
# Built once as the invoking user; run as root at each site — `sudo go run` would need
# a root GOCACHE. The build host already has Go (see build/20-installer-binaries.sh).
go build -o /tmp/patch-artools /os/scripts/patch-artools.go
echo "[iso] injecting [runink] BEFORE [system] (priority for our pinned linux-runink/runink-zfs)"
# [runink] stays TrustAll -- it's our own file:// local repo (packages we just built in this
# same container), not a remote third party; nothing to verify against.
#
# [archzfs] is NO LONGER injected (2026-09-22). It was here for zfs-dkms, then for zfs-utils
# alone, and the zfs-utils it served was 2.3.3 against our 2.4.4 modules. The userland is
# now runink-zfs-utils, split from build/pkgbuilds/runink-zfs and built from the same signed
# tarball, so no profile installs anything from archzfs. Dropping the repo also drops a
# remote, kernel-privileged third party (and its two signing keys, which this script used to
# fetch from a keyserver and lsign) from the build's trust set. If a profile ever needs a
# package only archzfs carries, re-add both here AND in scripts/patch-artools.go, with the
# key verification that used to live here (git log -S ARCHZFS_KEYS).
# Artix's own repos still need an initialised keyring (this used to happen as a side effect
# of the archzfs key import that stood here).
sudo pacman-key --init >/dev/null 2>&1 || true
if ! grep -q '\[runink\]' "$CONF"; then
  sudo /tmp/patch-artools pacman-conf "$CONF"
fi

echo "[iso] patching artools for the live boot (ZFS needs it)"
# RUNINK_PROFILE selects the ISO volume label and the live GRUB titles. Passed explicitly on
# the sudo command line, NOT exported — sudo scrubs the environment by default, so an
# `export` would silently not reach the tool and every ISO would keep the server's label.
# That label is also the live `root=LABEL` kopt, so getting it wrong means a stick that can
# mount the WRONG image's rootfs — not a cosmetic name.
#
# RESTORED 2026-09-06. This line shipped in #68 and was then silently clobbered: #69, #70
# and #73 each branched off main in parallel and all edit this file, so the last merge won
# and took this with it. The Go half (brandFor, which READS the variable) survived, leaving
# a half-live fix — nothing set the variable, patch-artools fell back to its (pre-RIVER) server-profile
# default, and BOTH 2026-09-06 ISOs were built with label=RUNINK_SERVER. Found with blkid
# while preparing a dual-boot USB, which would have been ambiguous in exactly the way #68
# was written to prevent.
sudo RUNINK_PROFILE="${PROFILE:-river}" RIVER_PROFILE_DIR="$PROFILE_DIR" RIVER_ISO_LABEL="$RP_ISO_LABEL" \
	RIVER_ISO_NAME="$RP_ISO_NAME" RIVER_GRUB_TITLE="$RP_GRUB_TITLE" /tmp/patch-artools

# ── skip mirrors measured as degraded ──────────────────────────────────────────────────
# Two consecutive workstation builds (33977370927, 33978541291) died in make_rootfs on the
# same mirror, on DIFFERENT packages:
#
#   error: failed retrieving 'linux-firmware-nvidia-...sig' from mirrors.dotsrc.org :
#     Operation too slow. Less than 1 bytes/sec transferred the last 10 seconds
#   error: failed retrieving 'plasma-desktop-...sig'        from mirrors.dotsrc.org : ...
#
# pacman does not fail over to the next mirror for a stalled transfer — it aborts the whole
# transaction. The workstation profile pulls ~800 packages against the server's ~200, so it
# is far more exposed to any one bad mirror; the server profile has been getting away with it.
#
# NOTE: pacman/mirrorlist.pin exists and pins two Artix mirrors, NEITHER of them dotsrc —
# but nothing reads it on this path (same as pacman/pacman.conf.in, per the comment at the
# top of this script). Wiring that file in wholesale is NOT the fix here: it also pins an
# Arch snapshot of PIN_DATE, which would change which package VERSIONS every build installs.
# That is a deliberate decision, not a side effect of unbreaking CI.
# FIRST ATTEMPT AT THIS COMMENTED OUT ONE BAD MIRROR AND MADE THINGS WORSE. Recorded
# because the failure mode is the interesting part: the container's mirrorlist is an
# ORDERED list of 12, dotsrc is #1, and #2 is ftp.ntua.gr — frozen at 12 Sep 2025, a year
# stale. Removing #1 silently promoted a one-year-old package snapshot. Nothing lost all
# its mirrors, so a "do we still have mirrors?" guard passed, and the rootfs was built from
# 2025 packages: s6-base 2.7, which predates the s6-frontend package that provides
# /usr/bin/s6. The HOST meanwhile had current artools-iso 0.39.1, which calls
# `chroot "$mnt" s6 set enable` — hence:
#     chroot: failed to run command 's6': No such file or directory
# (run 33980437854). Checking that mirrors EXIST is not the same as checking they are
# CURRENT, and only the second one was ever the question.
#
# So: use an explicit allowlist instead of deleting from an ordered list whose tail is
# unknown, and then PROVE the result is fresh.
#
# The allowlist is the Artix half of pacman/mirrorlist.pin — a file this repo already
# curates and which nothing on this path read. Its ARTIX_MIRRORS are used; its
# ARCH_SNAPSHOT is deliberately NOT, because adopting that would pin every build to
# PIN_DATE's package versions, which is a reproducibility decision and not something to
# smuggle in while unbreaking CI.
echo "[iso] pinning Artix mirrors from pacman/mirrorlist.pin (container default is an"
echo "[iso] ordered list whose #2 is a year stale — see the comment in this script)"
# shellcheck disable=SC1091  # repo-relative, present in the cloned tree
. /os/pacman/mirrorlist.pin
printf '%s\n' "$ARTIX_MIRRORS" | sudo tee /etc/pacman.d/mirrorlist >/dev/null

_srv="$(grep -cE "^[[:space:]]*Server[[:space:]]*=" /etc/pacman.d/mirrorlist 2>/dev/null || echo 0)"
echo "[iso] effective Artix mirrors: ${_srv}"
grep -E "^[[:space:]]*Server[[:space:]]*=" /etc/pacman.d/mirrorlist 2>/dev/null | sed 's/^/[iso]     /'
[ "${_srv}" -gt 0 ] || { echo "[iso] mirrorlist is empty — refusing to build" >&2; exit 1; }

# FRESHNESS ASSERTION — the check that would have caught the failure above.
# A mirror that answers is not a mirror that is current. Fail loudly on a stale snapshot
# rather than silently building a year-old rootfs that only explodes later, somewhere that
# reads as an unrelated bug.
_dburl="$(sed -n 's|^[[:space:]]*Server[[:space:]]*=[[:space:]]*\(.*\)$|\1|p' /etc/pacman.d/mirrorlist | head -1 \
	| sed 's|\$repo|system|; s|\$arch|x86_64|')/system.db"
echo "[iso] freshness probe: ${_dburl}"
_lm="$(curl -fsSI --max-time 30 "$_dburl" 2>/dev/null | sed -n 's/^[Ll]ast-[Mm]odified:[[:space:]]*//p' | tr -d '\r')"
if [ -z "$_lm" ]; then
	echo "[iso] WARNING: could not read Last-Modified for the primary mirror — continuing"
else
	_age=$(( ( $(date -u +%s) - $(date -u -d "$_lm" +%s 2>/dev/null || echo 0) ) / 86400 ))
	echo "[iso] primary mirror last modified: ${_lm} (${_age} days old)"
	[ "$_age" -lt 30 ] || {
		echo "[iso] primary Artix mirror is ${_age} days stale — refusing to build a rootfs" >&2
		echo "[iso] from an outdated snapshot (this is how run 33980437854 produced a 2025" >&2
		echo "[iso] userland with no /usr/bin/s6). Update pacman/mirrorlist.pin." >&2
		exit 1
	}
fi
echo "[iso] tmpfs work dir (overlayfs upperdir must not be ZFS)"
sudo mkdir -p /var/lib/artools
# Size is overridable but keeps the proven default. It was hardcoded, so a caller
# passing a tmpfs size got 28G regardless — the knob existed and did nothing.
mountpoint -q /var/lib/artools || sudo mount -t tmpfs -o "size=${TMPFS:-28G}" tmpfs /var/lib/artools
# Link the profile that was ASKED for. Both the symlink name and its target used to be the
# literal name of the pre-RIVER server profile, while the build below is `buildiso -p "$PROFILE"` — so any
# other PROFILE linked the SERVER profile and then asked buildiso for
# one that had never been linked, giving `buildiso: profile not found`. The PROFILE input
# was inert in exactly the case it exists for. Unreachable until a second profile arrived.
PROFILE="${PROFILE:-river}"
echo "[iso] link profile: ${PROFILE}"
[ -d "${PROFILE_DIR}" ] || {
	echo "[iso] no such profile: ${PROFILE_DIR}" >&2
	echo "[iso] available: $(ls /os/iso-profiles 2>/dev/null | tr '\n' ' ')" >&2
	exit 1
}
mkdir -p ~/artools-workspace/iso-profiles
ln -sfn "${PROFILE_DIR}" ~/artools-workspace/iso-profiles/"${PROFILE}"
# A profile with its own common.yaml (the server) replaces artools' shared one, which installs
# Artix's desktop-live package lists into every profile's rootfs (see
# iso-profiles/river/common.yaml). artools reads the WORKSPACE iso-profiles/common/ instead of
# its own when that directory exists; it is removed first so a reused container never hands
# one profile's list to another.
rm -rf ~/artools-workspace/iso-profiles/common
if [ -f "${PROFILE_DIR}/common.yaml" ]; then
	echo "[iso] common.yaml: the profile's own (iso-profiles/${PROFILE}/common.yaml)"
	mkdir -p ~/artools-workspace/iso-profiles/common
	cp "${PROFILE_DIR}/common.yaml" ~/artools-workspace/iso-profiles/common/common.yaml
else
	echo "[iso] common.yaml: artools' own (patched: linux-runink)"
fi
mkdir -p /os/iso-out

# LEAN mode (RUNINK_LEAN=1): the DOWNLOADABLE installer excludes the ~2.7GB baked app
# images so the ISO fits GitHub Releases' 2 GiB asset limit. The apps sync at enrollment.
# The fat (images-baked) ISO is the air-gap/preloaded variant. Move the tar aside within
# /os (same fs = instant rename), restore after.
# Profile-relative, for the same reason as the symlink above: hardcoding river here
# meant a workstation build would hide the SERVER's image tar and leave its own in place —
# LEAN would silently not be lean. (The workstation profile ships no baked app images at
# all, so this is a no-op there; being a no-op for the right reason still matters.)
IMGTAR="${PROFILE_DIR}/root-overlay/usr/local/share/runink/images/runink-apps.tar"
HIDDEN=/os/build/.runink-apps.tar.hidden
if [ "${RUNINK_LEAN:-0}" = 1 ] && [ -f "$IMGTAR" ]; then
  echo "[iso] LEAN build: excluding baked app images"; mv "$IMGTAR" "$HIDDEN"
fi
# The `return 0` is load-bearing, not tidiness. As written before, the function's LAST
# command was the `[ -f "$HIDDEN" ]` test. When there is nothing to restore — the normal
# case, since $HIDDEN only exists if LEAN mode actually moved a tar — that test is FALSE,
# `&&` short-circuits, and the function returns 1. Running as an EXIT trap under `set -e`,
# that 1 became the SCRIPT's exit status, so a completely successful build reported
# failure. Measured on run 33968636833: a 1.5 GB ISO was produced, copied to /os-out,
# sha256'd, "[iso] DONE" printed — and the pod still exited 1 with the Job marked Failed.
# The river-guide model (~1.1 GB, build/25-river-guide.sh) is hidden the same way: a LEAN
# stick has no guide model, and river-guide runs degraded (guide sections, no model).
GUIDE_MODEL="${PROFILE_DIR}/live-overlay/usr/share/river-guide/model"
GUIDE_HIDDEN=/os/build/.river-guide-model.hidden
if [ "${RUNINK_LEAN:-0}" = 1 ] && [ -d "$GUIDE_MODEL" ]; then
  echo "[iso] LEAN build: excluding the river-guide model (guide runs degraded)"; mv "$GUIDE_MODEL" "$GUIDE_HIDDEN"
fi
restore_images() {
  [ -f "$HIDDEN" ] && mv "$HIDDEN" "$IMGTAR"
  [ -d "$GUIDE_HIDDEN" ] && mv "$GUIDE_HIDDEN" "$GUIDE_MODEL"
  return 0
}
trap restore_images EXIT

echo "[iso] buildiso (s6) ..."
# PROFILE is overridable but defaults to the only profile this repo ships. It used
# to be hardcoded, which made the callers' `profile` input silently inert — the
# workflow accepted a value, ignored it, and reported success for a build of
# something else.
# Output is tee'd so the ZFS assertion below can inspect it. PIPESTATUS, not $?, because
# the pipe's exit status is tee's.
BUILDLOG=/tmp/buildiso.log
set +e
buildiso -p "${PROFILE:-river}" -i s6 -t /os/iso-out 2>&1 | tee "$BUILDLOG"
_bi=${PIPESTATUS[0]}
set -e
[ "$_bi" -eq 0 ] || { echo "[iso] buildiso exited $_bi" >&2; exit "$_bi"; }

# ── the ZFS kernel module MUST have built ───────────────────────────────────────────────
# This is a ZFS-ROOT image. If zfs-dkms did not build, the live installer cannot
# `zpool create` and the ISO cannot do the one thing it exists for — but pacman's DKMS
# hook only WARNS, and buildiso happily carries on and produces an ISO that looks fine:
#
#   ==> WARNING: `dkms install --no-depmod zfs/2.3.3 -k 6.18.49-2-lts' exited 1
#
# Both ISOs built on 2026-09-05 (runs 33969319317 server, 33974322050 workstation) shipped
# this way and were reported GREEN. Nobody would have found out until an install failed at
# a console. A warning is the wrong severity for "the root filesystem driver is missing";
# it is the same shape as every other check in this repo that could not fail.
if grep -qE "dkms install.*exited [1-9]" "$BUILDLOG"; then
	echo "::error::zfs-dkms FAILED to build — this is a ZFS-root image and the module is missing." >&2
	grep -E "dkms install|DKMS|zfs" "$BUILDLOG" | tail -20 >&2
	echo "  The live installer would be unable to create or import the pool." >&2
	exit 1
fi

# The DKMS check above is now a LEGACY GUARD and cannot fire: since the linux-runink swap
# the profiles ship the PREBUILT runink-zfs, so no `dkms install` line is ever emitted. It
# is kept only to catch an accidental reintroduction of zfs-dkms. On its own it would be a
# check that cannot fail — precisely the defect os#70 existed to remove — so the assertion
# below is the one that actually holds now.
#
# Assert the ZFS module is IN THE BUILT ROOTFS. buildiso leaves its work tree behind, so
# this reads the real filesystem the ISO was made from instead of trusting a log line.
_rootfs="/var/lib/artools/buildiso/${PROFILE:-river}/artix/rootfs"
if [ -d "$_rootfs" ]; then
	if find "$_rootfs"/usr/lib/modules/*-runink/extra -name 'zfs.ko*' 2>/dev/null | grep -q .; then
		echo "[iso] zfs module present in rootfs"
	else
		echo "::error::no zfs.ko under ${_rootfs}/usr/lib/modules/*-runink/extra" >&2
		echo "  This is a ZFS-ROOT image; without the module the live installer cannot" >&2
		echo "  zpool create, and the failure would surface at a console mid-install." >&2
		echo "  Check that runink-zfs reached localrepo and installed cleanly." >&2
		exit 1
	fi
	# The initcpio hook ships in runink-zfs-utils (vendored from archzfs into the runink-zfs
	# split PKGBUILD), NOT in runink-zfs, which is modules-only. Without it `mkinitcpio -P`
	# fails with "Hook 'zfs' cannot be found" and the image has no initramfs.
	if [ ! -e "$_rootfs/usr/lib/initcpio/install/zfs" ]; then
		echo "::error::zfs initcpio hook missing from the rootfs (usr/lib/initcpio/install/zfs)" >&2
		echo "  It ships in runink-zfs-utils, which runink-zfs depends on. If it was dropped" >&2
		echo "  or replaced, the image cannot build an initramfs and will not boot." >&2
		exit 1
	fi
	echo "[iso] zfs initcpio hook present"
	# Userland and module must be the SAME OpenZFS release. This is the skew the split
	# exists to prevent (archzfs's zfs-utils 2.3.3 against 2.4.4 modules); read the rootfs's
	# own pacman db rather than trusting the dependency resolver got it right.
	_zmod="$(sudo pacman --root "$_rootfs" -Q runink-zfs 2>/dev/null | awk '{print $2}')"
	_zutl="$(sudo pacman --root "$_rootfs" -Q runink-zfs-utils 2>/dev/null | awk '{print $2}')"
	if [ -z "$_zmod" ] || [ -z "$_zutl" ] || [ "${_zmod%-*}" != "${_zutl%-*}" ]; then
		echo "::error::ZFS userland/module mismatch in rootfs: runink-zfs=${_zmod:-ABSENT} runink-zfs-utils=${_zutl:-ABSENT}" >&2
		exit 1
	fi
	echo "[iso] zfs userland matches module: ${_zutl}"
else
	echo "[iso] WARNING: rootfs not found at ${_rootfs} — could not verify the ZFS module" >&2
fi

# ── the live session's services MUST have been enabled ─────────────────────────────────
# artools only WARNS when a live-session service is missing and when s6's repository sync
# fails, and it still produces an ISO. The first server ISO (2026-09-25) did both: its
# live-overlay was never applied (no `livefs:` key), so river-guide-model was "not found",
# and a hand-made adminsv/default bundle without a `type` file made `s6 repository sync`
# fatal, so NO enabled service reached the boot database: the live system booted without
# NetworkManager or sshd and without the guide on tty1. Either line fails the build now.
if grep -nE "WARNING: Service .* not found|s6-rc-repo-sync: fatal|s6-rc-compile: fatal|s6-rc-set-[a-z]+: fatal" "$BUILDLOG" >&2; then
	echo "::error::the live session's s6 services were not all enabled (see the lines above)." >&2
	echo "  A live-session service must exist in root-overlay/ or live-overlay/ (with a" >&2
	echo "  livefs: key in profile.yaml), and every etc/s6/adminsv/* entry needs its type file." >&2
	exit 1
fi
echo "[iso] live-session services enabled (no missing service, s6 repository in sync)"

# ── ...and they must be in the boot database the live medium boots on ─────────────────────
# The log check above only sees what artools printed. Read the compiled database itself, in
# the live system's own view (the livefs layer over the rootfs, where artools configured the
# live session), and require every live-session service in its default bundle. Also: nothing
# may depend on artix-live, which no Runink River medium installs. The first workstation ISO
# (2026-09-25) had sddm-srv depend on it, and no service at all reached the database.
_livefs="/var/lib/artools/buildiso/${PROFILE:-river}/artix/livefs"
_view="$_rootfs"
if [ -d "$_livefs" ]; then
	_view=/tmp/runink-live-view
	sudo mkdir -p "$_view"
	sudo mount -t overlay overlay -o "ro,lowerdir=$_livefs:$_rootfs" "$_view"
fi
_db="$(sudo chroot "$_view" s6-rc-db -c /etc/s6/rc/compiled contents default 2>&1)" || {
	echo "::error::cannot read the live boot database (/etc/s6/rc/compiled): $_db" >&2; exit 1; }
_bad=""
for _svc in $(yq -P '.live-session.services[]' "${PROFILE_DIR}/profile.yaml"); do
	if [ -d "$_view/etc/s6/sv/$_svc-srv" ]; then _want="$_svc-srv"; else _want="$_svc"; fi
	printf '%s\n' "$_db" | grep -qx "$_want" || _bad="$_bad $_want"
done
_artix="$(find "$_view/etc/s6/sv" "$_view/etc/s6/adminsv" -path '*/dependencies.d/artix-live' 2>/dev/null || true)"
[ "$_view" = "$_rootfs" ] || { sudo umount "$_view"; sudo rmdir "$_view"; }
if [ -n "$_bad" ]; then
	echo "::error::live-session service(s) missing from the live boot database's default bundle:$_bad" >&2
	exit 1
fi
if [ -n "$_artix" ]; then
	echo "::error::service(s) depend on artix-live, which this medium does not install:" >&2
	printf '  %s\n' "$_artix" >&2
	exit 1
fi
echo "[iso] live boot database holds every live-session service; nothing depends on artix-live"

echo "[iso] ISOs:"; find /os/iso-out -name '*.iso' -exec ls -lh {} \;

# Assert the produced ISO carries the label for THIS profile.
#
# The label is the live `root=LABEL` kopt, so two images sharing one label can mount each
# other's rootfs — the hazard #68 fixed. That fix then silently half-reverted (the shell
# line that sets RUNINK_PROFILE was clobbered by a parallel merge while the Go side that
# reads it survived), and nothing noticed until a dual-boot USB forced someone to run blkid.
# Reading the label back off the artifact is the check that would have caught it the same day.
_want_label="$(RUNINK_PROFILE="${PROFILE:-river}" RIVER_ISO_LABEL="$RP_ISO_LABEL" /tmp/patch-artools --print-label 2>/dev/null || true)"
if [ -n "$_want_label" ] && command -v blkid >/dev/null 2>&1; then
	for _iso in /os/iso-out/*/*.iso /os/iso-out/*.iso; do
		[ -e "$_iso" ] || continue
		_got="$(blkid -s LABEL -o value "$_iso" 2>/dev/null || true)"
		if [ "$_got" != "$_want_label" ]; then
			echo "::error::$(basename "$_iso") has label '${_got}', expected '${_want_label}'" >&2
			echo "  The label is the live root=LABEL kopt. A wrong or shared label means the" >&2
			echo "  installer can mount the wrong image's rootfs. Check that build-iso-box.sh" >&2
			echo "  still passes RUNINK_PROFILE to patch-artools." >&2
			exit 1
		fi
		echo "[iso] label verified: $(basename "$_iso") = ${_got}"
	done
fi

# Copy out to the caller's mount when there is one. /os is the clone and dies with
# the container, so an ISO left only there is the artifact the build exists to
# produce, discarded at exit. ISO_OUT_DIR is set by the k8s Job (a hostPath); when
# it is unset — a plain on-box `make` run — this is a no-op and /os/iso-out stands.
if [ -n "${ISO_OUT_DIR:-}" ] && [ -d "${ISO_OUT_DIR}" ]; then
	echo "[iso] copying to ${ISO_OUT_DIR} (survives the build container)"
	find /os/iso-out -name '*.iso' -exec cp -v {} "${ISO_OUT_DIR}/" \;
	# Record the BASENAME, not the container path. `sha256sum "$1"` writes the path the ISO
	# had inside this container (/os/iso-out/<profile>/x.iso), which does not exist anywhere
	# the ISO is actually used — so `sha256sum -c x.iso.sha256` fails with "No such file or
	# directory" on a perfectly good image, both on the node and for anyone verifying a
	# downloaded copy. Found 2026-09-06 when the USB-preparation script refused to write a
	# stick over it: the hashes were correct all along, only the filename was unusable.
	#
	# Generating it from a `cd` into the destination makes the file self-describing and
	# portable — `sha256sum -c` now works wherever the ISO and its .sha256 sit together.
	find /os/iso-out -name '*.iso' -exec sh -c \
		'cd "$2" && sha256sum "$(basename "$1")" > "$(basename "$1").sha256"' _ {} "${ISO_OUT_DIR}" \;
fi
echo "[iso] DONE"
