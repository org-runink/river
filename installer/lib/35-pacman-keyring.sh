#!/bin/sh
# 35-pacman-keyring — initialise AND populate the target's pacman keyring.
#
# WHY THIS EXISTS
# A freshly installed node could not install a single package. Measured on the live
# box 2026-08-15, two days after install:
#
#	pacman-key --list-keys | grep -c '^pub'   ->  3
#	pacman -S --needed fakeroot               ->  error: required key missing from keyring
#	                                              error: failed to commit transaction
#
# `pacman-key --init` creates an empty keyring; it is `--populate` that imports the
# distribution's packager keys from the artix-keyring package. Nothing in the profile
# or the ISO build ever ran --populate, so every installed node shipped with a
# keyring that exists, looks initialised, and can verify nothing. After running
# --populate by hand the same box went 3 -> 30 pubkeys and installed normally.
#
# This is the platform's dominant failure shape: a foundation procedure that is
# present, reports success, and is structurally unable to do its job. The symptom
# only appears the first time someone needs a package on a remote node — which, at a
# farm we cannot drive to, is the worst possible moment to discover it.
#
# NOT A LICENCE TO BUILD ON NODES. The image ships prebuilt packages (including
# runink-tayga) precisely so nodes never need a toolchain; base-devel and fakeroot
# stay off the image deliberately. This hook exists so that pacman can VERIFY what
# it installs, not so nodes can compile.
set -eu

TARGET="${RUNINK_TARGET:?}"
chroot_run() { chroot "$TARGET" /bin/sh -c "$1"; }

# gpg needs /dev (entropy, /dev/null) and /proc inside the chroot. Mount only what is
# not already mounted, and undo exactly that on the way out: 40-boot-grub-zfs, 50 and 80
# rbind the same paths again, and a mount left here would sit under theirs, never unwound.
# pacman-key starts a gpg-agent inside the target that outlives it; stop it too, so no
# process keeps the target's files open when 90-export exports the pool.
mounted=""
cleanup() {
	if [ -x "$TARGET/usr/bin/gpgconf" ]; then
		chroot "$TARGET" gpgconf --homedir /etc/pacman.d/gnupg --kill all >/dev/null 2>&1 || true
	fi
	for fs in $mounted; do umount -R "$TARGET/$fs" 2>/dev/null || true; done
}
trap cleanup EXIT
for fs in dev proc sys; do
	mountpoint -q "$TARGET/$fs" 2>/dev/null && continue
	mount --rbind "/$fs" "$TARGET/$fs"
	mount --make-rslave "$TARGET/$fs" 2>/dev/null || true
	mounted="$fs $mounted"
done

echo "pacman-key: initialising + populating the target keyring"
chroot_run "pacman-key --init"
# Populate every keyring the target actually has installed. Naming a keyring that is
# absent makes pacman-key exit non-zero, so ask the filesystem rather than guessing:
# an Artix base has artix, an Arch-derived one may also carry archlinux.
# Single quotes deliberate: this block must expand inside the chroot, not here.
# shellcheck disable=SC2016
chroot_run '
	set -eu
	kr=""
	for k in artix archlinux; do
		[ -f "/usr/share/pacman/keyrings/${k}.gpg" ] && kr="$kr $k"
	done
	if [ -z "$kr" ]; then
		echo "pacman-key: FATAL no keyring files under /usr/share/pacman/keyrings" >&2
		exit 1
	fi
	echo "pacman-key: populating:$kr"
	# shellcheck disable=SC2086
	pacman-key --populate $kr
'

# VERIFY, do not assume. The whole point of this hook is that the previous code path
# "succeeded" while leaving the keyring useless, so failing loudly here is the
# feature. 3 keys is what an unpopulated keyring has; a populated one has ~30.
keys=$(chroot_run "pacman-key --list-keys 2>/dev/null | grep -c '^pub'" || echo 0)
echo "pacman-key: $keys public keys in the target keyring"
if [ "$keys" -lt 10 ]; then
	echo "pacman-key: FATAL keyring still unpopulated ($keys keys) — the node would be unable to install or verify any package" >&2
	exit 1
fi

# --- [runink]: Runink River's own signed package repository (docs/REPOSITORY.md) ------------
# The image ships the repository's mirrorlist (/etc/pacman.d/mirrorlist-runink) and the release
# key as a pacman keyring (/usr/share/pacman/keyrings/runink.gpg + runink-trusted). Here the key
# is trusted in the target keyring and the [runink] stanza added to its pacman.conf, AHEAD of
# [system] as at build time. Both or neither: [runink] requires a signed database and signed
# packages, so a stanza whose key is not trusted would fail every `pacman -Syu`, and a trusted
# key with no stanza vouches for nothing. An image that ships neither (a downstream profile
# without the repository) is left as it is.
CONF="$TARGET/etc/pacman.conf"
ML="$TARGET/etc/pacman.d/mirrorlist-runink"
KR="$TARGET/usr/share/pacman/keyrings/runink"
# The build-time [runink] is a file:// directory on the build host. It must never reach a node.
if grep -Eq '^[[:space:]]*Server[[:space:]]*=[[:space:]]*file:' "$CONF"; then
	echo "pacman-key: FATAL $CONF names a file:// repository (the build-time [runink] leaked into the image)" >&2
	exit 1
fi
if [ -f "$ML" ] || [ -f "$KR.gpg" ]; then
	if [ ! -f "$ML" ] || [ ! -f "$KR.gpg" ] || [ ! -s "$KR-trusted" ]; then
		echo "pacman-key: FATAL the image ships only part of [runink] (need $ML, $KR.gpg and $KR-trusted)" >&2
		exit 1
	fi
	echo "pacman-key: populating: runink (the Runink River release key)"
	chroot_run "pacman-key --populate runink"
	# Every trusted fingerprint must now be VALID in the target keyring (locally signed), or
	# pacman would refuse the repository's signatures on the node's first update.
	for fpr in $(sed -e 's/#.*//' -e 's/:.*//' "$KR-trusted"); do
		printf '%s\n' "$fpr" | grep -Eqx '[0-9A-F]{40}' || {
			echo "pacman-key: FATAL $KR-trusted: '$fpr' is not a 40-hex fingerprint" >&2; exit 1; }
		v=$(chroot_run "gpg --homedir /etc/pacman.d/gnupg --batch --with-colons --list-keys $fpr 2>/dev/null" \
			| awk -F: '$1 == "pub" { print $2; exit }') || v=
		case "$v" in
			f|u) echo "pacman-key: [runink] key $fpr trusted (validity $v)" ;;
			*) echo "pacman-key: FATAL [runink] key $fpr is not valid in the target keyring (validity '${v:-none}')" >&2; exit 1 ;;
		esac
	done
	if ! grep -q '^\[runink\]' "$CONF"; then
		sed -i '0,/^\[system\]/s//[runink]\nSigLevel = Required DatabaseRequired\nInclude = \/etc\/pacman.d\/mirrorlist-runink\n\n[system]/' "$CONF"
		grep -q '^\[runink\]' "$CONF" || {
			echo "pacman-key: FATAL $CONF has no [system] stanza to put [runink] ahead of" >&2; exit 1; }
		echo "pacman-key: [runink] added to /etc/pacman.conf (SigLevel = Required DatabaseRequired)"
	fi
fi
