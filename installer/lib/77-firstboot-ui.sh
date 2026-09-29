#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# 77-firstboot-ui — arrange the graphical first boot of an installed server, and hand the
# installer's role choice to the first-boot hooks.
#
# RUNINK_ROLE (an edition that offers roles): written to /etc/runink/role (0600); the hooks get
# it as RIVER_ROLE (river-firstboot-hooks).
#
# RUNINK_FIRSTBOOT_UI=1 (the graphical installer, for an edition with "firstboot_ui"): the node
# starts with the graphical first boot (river-installer firstboot, docs/INSTALL.md). It writes
# the marker /var/lib/runink/firstboot-ui (root 0600), KEY=VALUE lines the first boot reads:
#   EDITION, EDITION_TITLE, ADMIN_USER, LANG      what the screens say
#   KIOSK_PKGS                                    the kiosk packages 20-clone-rootfs kept on the
#                                                 node for the first boot; the first boot's
#                                                 Finish removes them (pacman -Rns)
# The first boot needs the kiosk: if the packages are not on the target, no marker is written
# and the node starts headless (its hooks get no RIVER_PAGE_DIR).
# Anything else (runink-autoinstall, the TUI installer): nothing happens.
set -eu

TARGET="${RUNINK_TARGET:?}"
LIST=/usr/share/river/installer/kiosk-packages

if [ -n "${RUNINK_ROLE:-}" ]; then
	case "$RUNINK_ROLE" in *[!a-z0-9-]*) echo "firstboot-ui: bad role '$RUNINK_ROLE'" >&2; exit 1 ;; esac
	install -d -m 0700 "$TARGET/etc/runink"
	( umask 077; printf '%s\n' "$RUNINK_ROLE" > "$TARGET/etc/runink/role" )
	echo "firstboot-ui: role $RUNINK_ROLE"
fi

[ "${RUNINK_FIRSTBOOT_UI:-0}" = 1 ] || { echo "firstboot-ui: no graphical first boot requested"; exit 0; }

pkgs=""
if [ -r "$LIST" ]; then
	for p in $(sed 's/#.*//' "$LIST"); do
		if chroot "$TARGET" pacman -Q "$p" >/dev/null 2>&1; then pkgs="$pkgs $p"; fi
	done
fi
pkgs="${pkgs# }"
if [ -z "$pkgs" ] || [ ! -x "$TARGET/usr/local/bin/river-installer" ]; then
	echo "firstboot-ui: WARN: no kiosk or no river-installer on the target; the node starts headless" >&2
	exit 0
fi
clean() { printf '%s' "$1" | tr -d '\n\r'; }
install -d -m 0700 "$TARGET/var/lib/runink"
umask 077
{
	echo "# Written by 77-firstboot-ui; removed when the graphical first boot finishes."
	echo "EDITION=$(clean "${RUNINK_EDITION:-}")"
	echo "EDITION_TITLE=$(clean "${RUNINK_EDITION_TITLE:-}")"
	echo "ADMIN_USER=$(clean "${RUNINK_ADMIN_USER:-runink}")"
	echo "LANG=$(clean "${RUNINK_UI_LANG:-en}")"
	echo "KIOSK_PKGS=$pkgs"
} > "$TARGET/var/lib/runink/firstboot-ui"
chmod 0600 "$TARGET/var/lib/runink/firstboot-ui"
echo "firstboot-ui: graphical first boot armed (kiosk: $pkgs)"
