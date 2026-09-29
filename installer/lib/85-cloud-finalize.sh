#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# 85-cloud-finalize — the last step before 90-export on a cloud image: enable the cloud
# services in the boot database, then clear EVERY per-machine identity, so that no two
# instances from the image share a host key, a machine id, a ZFS hostid, a random seed or
# enrollment state, and no password works anywhere. docs/CLOUD-IMAGES.md, "Before capture".
#
# The image is captured without ever having booted, so most of this is belt and braces over
# 20-clone-rootfs; each item is still asserted, because the image is copied to every
# instance and a leak here is a leak everywhere.
set -eu

TARGET="${RUNINK_TARGET:?}"
: "${RUNINK_CLOUD:?85-cloud-finalize: cloud images only}"
chroot_run() { chroot "$TARGET" /bin/sh -c "$1"; }

mounted=""
cleanup() { for fs in $mounted; do umount -R "$TARGET/$fs" 2>/dev/null || true; done; }
trap cleanup EXIT
for fs in dev proc sys; do mount --rbind "/$fs" "$TARGET/$fs"; mounted="$fs $mounted"; done
mount -t tmpfs -o mode=0755 tmpfs "$TARGET/run"; mounted="run $mounted"

# --- s6: the cloud services, and rc-local waits for the data datasets --------------------
CLOUD_SERVICES="river-cloud-identity river-cloud-init river-cloud-late ttyS"
for s in river-cloud-identity river-cloud-init river-cloud-late; do
	[ -f "$TARGET/etc/s6/sv/$s/type" ] || { echo "cloud-finalize: s6 service $s missing from the target" >&2; exit 1; }
done
# On a cloud image the stack (k0s on /var/lib/k0s, the payload on /var/lib/core) must not
# start before river-cloud-init has unlocked and mounted the encrypted data datasets.
: > "$TARGET/etc/s6/adminsv/rc-local/dependencies.d/river-cloud-init"
chroot_run "s6 repository sync"
# shellcheck disable=SC2086 # a word list by construction
chroot_run "s6 set enable --pull-dependencies $CLOUD_SERVICES"
chroot_run "s6 set commit"
chroot_run "s6 live install --init"
db="$(chroot_run "s6-rc-db -c /etc/s6/rc/compiled contents default")"
for s in $CLOUD_SERVICES; do
	printf '%s\n' "$db" | grep -qx "$s" || { echo "cloud-finalize: $s is not in the boot database" >&2; exit 1; }
done
chroot_run "s6-rc-db -c /etc/s6/rc/compiled dependencies rc-local" | grep -qx river-cloud-init \
	|| { echo "cloud-finalize: rc-local does not depend on river-cloud-init" >&2; exit 1; }
echo "cloud-finalize: s6 boot database has $CLOUD_SERVICES; rc-local waits for river-cloud-init"

# --- per-machine identity -----------------------------------------------------------------
: > "$TARGET/etc/machine-id"                      # river-cloud-identity writes a new one
rm -f "$TARGET/var/lib/dbus/machine-id"
rm -f "$TARGET"/etc/ssh/ssh_host_*                # per-instance host keys (ssh-keygen -A)
rm -f "$TARGET/etc/hostid"                        # per-instance ZFS hostid (zgenhostid)
rm -f "$TARGET/var/lib/random-seed" "$TARGET/var/lib/systemd/random-seed"
rm -rf "$TARGET/var/lib/NetworkManager"/*         # leases, and secret_key (per-host)
find "$TARGET/etc/NetworkManager/system-connections" -type f ! -name river0.nmconnection -delete 2>/dev/null || true
rm -f "$TARGET/etc/udev/rules.d/70-persistent-net.rules"
# Enrollment state and anything that described the BUILD machine.
find "$TARGET/etc/runink" -mindepth 1 -maxdepth 1 ! -name '*.example' -exec rm -rf {} + 2>/dev/null || true
rm -rf "$TARGET/var/lib/runink"/* "$TARGET/var/lib/river-cloud/keys" "$TARGET/var/lib/river-cloud/instance-id"
rm -f "$TARGET/etc/runink/enrollment.env" "$TARGET/etc/k0s/join-token"
# Credentials: keys come from instance metadata, never from the image.
rm -f "$TARGET/home/runink/.ssh/authorized_keys" "$TARGET/root/.ssh/authorized_keys"
rm -f "$TARGET"/etc/ssh/authorized_keys.d/* 2>/dev/null || true
rm -f "$TARGET/root/.bash_history" "$TARGET/home/runink/.bash_history" "$TARGET/root/.lesshst"
chroot_run "usermod -p '!' root && usermod -p '!' runink"
# Logs of the build session.
find "$TARGET/var/log" -type f -exec truncate -s 0 {} + 2>/dev/null || true
rm -rf "$TARGET/tmp"/* "$TARGET/var/tmp"/* "$TARGET/var/cache/pacman/pkg"/*

# --- source offer -------------------------------------------------------------------------------
# A cloud image is a binary distribution of GPL/LGPL software (the kernel, glibc, coreutils,
# GRUB, ...), so it carries, ON the image, the list of every package with its version,
# licence and where its complete corresponding source is, plus a written offer. The sources
# themselves (several GB) are not on the image. docs/CLOUD-IMAGES.md, "Source offer".
SRC="$TARGET/usr/share/river/sources"
REF="${RUNINK_SOURCE_REF:-main}"
install -d -m 0755 "$SRC"
{
	printf 'package\tversion\tpkgbase\tlicense\tupstream\tsource\n'
	for d in "$TARGET"/var/lib/pacman/local/*/; do
		[ -f "$d/desc" ] || continue
		awk -v ref="$REF" '
			/^%[A-Z]+%$/ { sec = $0; next }
			/^$/ { sec = ""; next }
			sec == "%NAME%" { name = $0 }
			sec == "%VERSION%" { ver = $0 }
			sec == "%BASE%" { base = $0 }
			sec == "%URL%" { url = $0 }
			sec == "%LICENSE%" { lic = lic (lic == "" ? "" : " AND ") $0 }
			END {
				if (base == "") base = name
				if (base ~ /^linux-runink/) dir = "runink-kernel"
				else if (base ~ /^runink-zfs/) dir = "runink-zfs"
				else if (base ~ /^(runink-|river-)/) dir = base
				if (dir != "")
					src = "https://github.com/org-runink/river/tree/" ref "/build/pkgbuilds/" dir
				else
					src = "https://gitea.artixlinux.org/packages/" base " (tag " ver "), upstream tarballs pinned in its PKGBUILD"
				printf "%s\t%s\t%s\t%s\t%s\t%s\n", name, ver, base, lic, url, src
			}' "$d/desc"
	done
} > "$SRC/MANIFEST.tsv"
cat > "$SRC/WRITTEN-OFFER.txt" <<EOF
Runink River Server — source offer

This image contains software licensed under the GNU GPL, the GNU LGPL and other licences
that require the complete corresponding source code to be made available. MANIFEST.tsv in
this directory lists every package on the image: its exact version, its licence, its
upstream URL, and where its complete corresponding source is:

  - Runink River's own packages (the kernel linux-runink, OpenZFS runink-zfs, the installer
    and the other runink-* packaging): https://github.com/org-runink/river at revision
    $REF, directory build/pkgbuilds/<package>. The PKGBUILDs pin every upstream tarball
    and patch by URL and sha256.
  - Every other package: the Artix Linux package source at
    https://gitea.artixlinux.org/packages/<pkgbase>, at the tag of the listed version, whose
    PKGBUILD pins the upstream source tarballs and patches.

For at least three years after you received this image, the maintainers of Runink River
(MAINTAINERS.md in the repository above) will also provide, on request, a copy of the
complete corresponding source code for any GPL- or LGPL-licensed component of it, on a
medium customarily used for software interchange, for no more than the cost of physically
performing the distribution. Open an issue at https://github.com/org-runink/river/issues
titled "source request" naming the image (/etc/runink-os-version) and the packages.
EOF
chmod 0644 "$SRC/MANIFEST.tsv" "$SRC/WRITTEN-OFFER.txt"
n_pkgs="$(($(wc -l < "$SRC/MANIFEST.tsv") - 1))"
[ "$n_pkgs" -gt 50 ] || { echo "cloud-finalize: the source manifest lists only $n_pkgs packages" >&2; exit 1; }
awk -F '\t' 'NR > 1 && $4 == "" { n++ } END { exit n > 0 }' "$SRC/MANIFEST.tsv" \
	|| echo "cloud-finalize: WARN — some packages declare no licence (see $SRC/MANIFEST.tsv)" >&2
echo "cloud-finalize: source offer at /usr/share/river/sources ($n_pkgs packages, revision $REF)"

# --- assert -----------------------------------------------------------------------------------
fail=0
bad() { echo "cloud-finalize: $*" >&2; fail=1; }
[ -s "$TARGET/etc/machine-id" ] && bad "machine-id is not empty"
ls "$TARGET"/etc/ssh/ssh_host_* >/dev/null 2>&1 && bad "SSH host keys remain"
[ -e "$TARGET/etc/hostid" ] && bad "/etc/hostid remains"
[ -e "$TARGET/var/lib/random-seed" ] && bad "random seed remains"
[ -e "$TARGET/var/lib/NetworkManager/secret_key" ] && bad "NetworkManager secret_key remains"
for u in root runink; do
	h="$(awk -F: -v u="$u" '$1 == u { print $2 }' "$TARGET/etc/shadow")"
	case "$h" in '!'*|'*'*) ;; *) bad "$u has a usable password" ;; esac
done
find "$TARGET/home" "$TARGET/root" -name authorized_keys 2>/dev/null | grep -q . && bad "an authorized_keys file remains"
[ -n "$(find "$TARGET/etc/runink" -mindepth 1 ! -name '*.example' 2>/dev/null)" ] && bad "/etc/runink holds node state"
[ "$fail" -eq 0 ] || exit 1
echo "cloud-finalize: per-machine identity cleared (machine-id, host keys, hostid, random seed, NM state, enrollment, passwords locked)"
