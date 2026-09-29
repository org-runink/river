#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# verify-kernel-zfs.sh — check a built linux-runink / runink-zfs set before it goes into an
# image. Runs as an ordinary user on the packages alone (no install, no root):
#
#   1. the four packages are present: linux-runink, linux-runink-headers, runink-zfs,
#      runink-zfs-utils, and exactly one of each;
#   2. the kernel's module directory is the pinned kernelrelease (from the PKGBUILD);
#   3. the resolved .config (shipped in the headers) meets every line of config.require;
#   4. spl.ko and zfs.ko carry a module signature that verifies against the certificate in
#      runink_signing.pem, and so does a module from the kernel package itself, i.e. the
#      out-of-tree modules are signed with the SAME per-build key as the in-tree ones;
#   5. runink-zfs's modules are built for that same kernelrelease and pin
#      runink-zfs-utils of the same OpenZFS version.
#
# Usage: build/verify-kernel-zfs.sh KERNEL_OUT
#   KERNEL_OUT holds pkgs/*.pkg.tar.zst and runink_signing.pem (build/build-kernel-zfs.sh).
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${1:?usage: verify-kernel-zfs.sh KERNEL_OUT}"
PKGS="$OUT/pkgs"
KEY="$OUT/runink_signing.pem"
REQUIRE="$HERE/pkgbuilds/runink-kernel/config.require"

fail=0
ok() { echo "verify-kernel-zfs: ok    $*"; }
bad() { echo "verify-kernel-zfs: FAIL  $*" >&2; fail=1; }
for t in bsdtar zstd openssl sha256sum od; do
	command -v "$t" >/dev/null 2>&1 || { echo "verify-kernel-zfs: $t required" >&2; exit 1; }
done
[ -d "$PKGS" ] || { echo "verify-kernel-zfs: no $PKGS" >&2; exit 1; }
[ -f "$KEY" ] || { echo "verify-kernel-zfs: no $KEY" >&2; exit 1; }

# The pinned release, from the PKGBUILD (so this cannot drift from what the build enforces).
pkv() { sed -n "s/^$1=\([0-9.]*\).*/\1/p" "$HERE/pkgbuilds/runink-kernel/PKGBUILD"; }
_major="$(pkv _major)"; _minor="$(pkv _minor)"; _zenrel="$(pkv _zenrel)"; pkgrel="$(pkv pkgrel)"
KREL="${_major}.${_minor}-zen${_zenrel}-${pkgrel}-runink"
ZVER="$(sed -n 's/^pkgver=//p' "$HERE/pkgbuilds/runink-zfs/PKGBUILD")"

one() { # the single package file for a name, or empty
	n=0; f=""
	for p in "$PKGS/$1"-[0-9]*.pkg.tar.zst; do
		[ -e "$p" ] || continue
		n=$((n + 1)); f="$p"
	done
	[ "$n" -eq 1 ] && echo "$f"
}
K="$(one linux-runink)" || true
H="$(one linux-runink-headers)" || true
Z="$(one runink-zfs)" || true
U="$(one runink-zfs-utils)" || true
for v in "linux-runink:$K" "linux-runink-headers:$H" "runink-zfs:$Z" "runink-zfs-utils:$U"; do
	[ -n "${v#*:}" ] && ok "package ${v#*:}" || bad "exactly one ${v%%:*} package expected in $PKGS"
done
[ "$fail" -eq 0 ] || exit 1

TMP="$(mktemp -d "${TMPDIR:-/tmp}/verify-kzfs.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT INT TERM

# 2. kernelrelease
if bsdtar -tf "$K" | grep -qx "usr/lib/modules/$KREL/vmlinuz"; then
	ok "kernel package installs usr/lib/modules/$KREL/vmlinuz"
else
	bad "no usr/lib/modules/$KREL/vmlinuz in $(basename "$K")"
fi

# 3. config.require against the shipped .config
bsdtar -xOf "$H" "usr/lib/modules/$KREL/build/.config" > "$TMP/config" 2>/dev/null || true
if [ ! -s "$TMP/config" ]; then
	bad "no .config in $(basename "$H")"
else
	miss=0; lines=0
	while IFS= read -r line; do
		case "$line" in ''|\#*) continue ;; esac
		lines=$((lines + 1))
		sym="${line%%=*}"; want="${line#*=}"
		have="$(grep -m1 "^CONFIG_${sym}=" "$TMP/config" | cut -d= -f2-)"
		have="${have:-n}"
		match=0
		oldifs="$IFS"; IFS='|'
		for alt in $want; do [ "$have" = "$alt" ] && match=1; done
		IFS="$oldifs"
		[ "$match" -eq 1 ] || { echo "  CONFIG_${sym} is '$have', need '$want'" >&2; miss=1; }
	done < "$REQUIRE"
	[ "$lines" -gt 0 ] || bad "config.require has no lines"
	[ "$miss" -eq 0 ] && ok "config.require: all $lines lines met by the built .config" \
		|| bad "config.require not met by the built .config"
fi

# 4. module signatures
openssl x509 -in "$KEY" -out "$TMP/cert.pem" 2>/dev/null || bad "no certificate in $KEY"
MAGIC="~Module signature appended~"
# check_sig FILE.ko LABEL — verify the appended PKCS#7 signature against cert.pem.
check_sig() {
	f="$1"
	[ "$(tail -c 28 "$f" | head -c 27)" = "$MAGIC" ] || { bad "$2: no module signature"; return; }
	total=$(wc -c < "$f")
	# struct module_signature (12 bytes) precedes the 28-byte magic; sig_len is its last
	# four bytes, big-endian.
	siglen=$(tail -c 32 "$f" | head -c 4 | od -An -tu1 | awk '{ print ($1*16777216)+($2*65536)+($3*256)+$4 }')
	content=$((total - 40 - siglen))
	[ "$content" -gt 0 ] || { bad "$2: malformed signature trailer"; return; }
	head -c "$content" "$f" > "$TMP/content"
	tail -c $((siglen + 40)) "$f" | head -c "$siglen" > "$TMP/sig.der"
	if openssl cms -verify -binary -inform DER -in "$TMP/sig.der" -content "$TMP/content" \
		-certfile "$TMP/cert.pem" -nointern -noverify -out /dev/null 2>"$TMP/err"; then
		ok "$2: signature verifies against runink_signing.pem"
	else
		bad "$2: signature does NOT verify against runink_signing.pem ($(head -1 "$TMP/err"))"
	fi
}
for m in spl zfs; do
	p="usr/lib/modules/$KREL/extra/$m.ko.zst"
	if bsdtar -xOf "$Z" "$p" 2>/dev/null | zstd -dq > "$TMP/$m.ko" && [ -s "$TMP/$m.ko" ]; then
		check_sig "$TMP/$m.ko" "runink-zfs $m.ko ($KREL)"
	else
		bad "no $p in $(basename "$Z") (not built for $KREL?)"
	fi
done
# One in-tree module, to prove both halves use the one key.
intree="$(bsdtar -tf "$K" | grep -m1 "^usr/lib/modules/$KREL/kernel/fs/.*\.ko\.zst$" || true)"
if [ -n "$intree" ] && bsdtar -xOf "$K" "$intree" | zstd -dq > "$TMP/intree.ko"; then
	check_sig "$TMP/intree.ko" "in-tree ${intree##*/}"
else
	bad "no in-tree module found in $(basename "$K")"
fi

# 5. ZFS pairing
dep="$(bsdtar -xOf "$Z" .PKGINFO | sed -n 's/^depend = //p' | tr '\n' ' ')"
case " $dep " in *" runink-zfs-utils=$ZVER "*) ok "runink-zfs depends on runink-zfs-utils=$ZVER" ;;
	*) bad "runink-zfs depends on '$dep', want runink-zfs-utils=$ZVER" ;; esac
uver="$(bsdtar -xOf "$U" .PKGINFO | sed -n 's/^pkgver = //p')"
[ "${uver%-*}" = "$ZVER" ] && ok "runink-zfs-utils is $uver" || bad "runink-zfs-utils is $uver, want $ZVER"

# 6. No interpreter the image does not ship. Every script in the userland must run under sh
#    or bash. OpenZFS installs zarcstat, zarcsummary, dbufstat and zilstat as scripts for an
#    interpreter the image does not carry unless it is configured --with-python=no, and
#    runink-zfs-utils 2.4.4-2 shipped all four.
nscripts=0 foreign=""
for f in $(bsdtar -tf "$U" | grep -E '^usr/(bin|lib)/[^.]' | grep -v '/$'); do
	line="$(bsdtar -xOf "$U" "$f" 2>/dev/null | head -c 128 | head -n 1 | tr -d '\0')"
	case "$line" in
		'#!'*) nscripts=$((nscripts + 1)) ;;
		*) continue ;;
	esac
	case "$line" in
		'#!/bin/sh'*|'#!/usr/bin/sh'*|'#!/bin/bash'*|'#!/usr/bin/bash'*|'#!/usr/bin/env bash'*|'#!/usr/bin/env sh'*) ;;
		*) foreign="$foreign ${f#usr/}(${line#\#!})" ;;
	esac
done
[ "$nscripts" -gt 0 ] || bad "found no scripts in $(basename "$U"); the interpreter check examined nothing"
[ -z "$foreign" ] && ok "runink-zfs-utils: all $nscripts scripts run under sh/bash" \
	|| bad "runink-zfs-utils ships scripts for an interpreter the image lacks:$foreign"

echo "verify-kernel-zfs: sha256 and size:"
for p in "$K" "$H" "$Z" "$U"; do
	printf '  %s  %s  %s\n' "$(sha256sum "$p" | cut -c1-64)" "$(wc -c < "$p" | tr -d ' ')" "$(basename "$p")"
done
[ "$fail" -eq 0 ] || { echo "verify-kernel-zfs: FAILED" >&2; exit 1; }
echo "verify-kernel-zfs: OK ($KREL, OpenZFS $ZVER)"
