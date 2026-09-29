#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# 32-locale-keyboard — give the installed machine the language and keyboard the operator chose
# in the graphical installer, so the console (and, on a workstation, the login screen and the
# desktop) type what the keys say from the first boot on. The password was typed with this
# layout; a machine that boots with another could refuse the operator's own password.
#
#   RUNINK_LANG          en_US.UTF-8 | es_ES.UTF-8 | fr_FR.UTF-8 | pt_BR.UTF-8
#   RUNINK_KEYMAP        the console keymap (kbd), e.g. fr-latin9
#   RUNINK_XKB_LAYOUT    the XKB layout, e.g. fr   (RUNINK_XKB_VARIANT optional)
#
# Nothing set (runink-autoinstall, the TUI installer): nothing changes. Writes
#   /etc/locale.conf, /etc/vconsole.conf                        every image
#   /etc/X11/xorg.conf.d/00-keyboard.conf, /etc/xdg/kxkbrc      when the image has a desktop
# and generates the locale when glibc's locale sources are on the image. Never fatal: a
# missing locale leaves English, it does not stop an install.
set -eu

TARGET="${RUNINK_TARGET:?}"
LANG_SEL="${RUNINK_LANG:-}"
KEYMAP="${RUNINK_KEYMAP:-}"
XKB="${RUNINK_XKB_LAYOUT:-}"
VARIANT="${RUNINK_XKB_VARIANT:-}"

if [ -z "$LANG_SEL$KEYMAP$XKB" ]; then
	echo "locale-keyboard: nothing chosen; the image defaults stay"
	exit 0
fi
case "$LANG_SEL$KEYMAP$XKB$VARIANT" in
	*[!A-Za-z0-9_.@-]*) echo "locale-keyboard: refusing unexpected characters in the settings" >&2; exit 1 ;;
esac

if [ -n "$LANG_SEL" ]; then
	printf 'LANG=%s\n' "$LANG_SEL" > "$TARGET/etc/locale.conf"
	echo "locale-keyboard: LANG=$LANG_SEL"
	if [ -f "$TARGET/etc/locale.gen" ] && [ -d "$TARGET/usr/share/i18n/locales" ]; then
		if ! grep -q "^${LANG_SEL} UTF-8" "$TARGET/etc/locale.gen"; then
			printf '%s UTF-8\n' "$LANG_SEL" >> "$TARGET/etc/locale.gen"
		fi
		chroot "$TARGET" locale-gen >/dev/null 2>&1 \
			&& echo "locale-keyboard: locale generated" \
			|| echo "locale-keyboard: WARN: locale-gen failed; messages stay in English" >&2
	fi
fi
if [ -n "$KEYMAP" ]; then
	printf 'KEYMAP=%s\n' "$KEYMAP" > "$TARGET/etc/vconsole.conf"
	echo "locale-keyboard: console keymap $KEYMAP"
fi
# The desktop: the X11 keyboard (SDDM's greeter) and Plasma's default layout list.
if [ -n "$XKB" ] && { [ -d "$TARGET/usr/share/sddm" ] || [ -d "$TARGET/usr/share/plasma" ]; }; then
	mkdir -p "$TARGET/etc/X11/xorg.conf.d" "$TARGET/etc/xdg"
	cat > "$TARGET/etc/X11/xorg.conf.d/00-keyboard.conf" <<-EOF
		# Written by the Runink River installer (32-locale-keyboard).
		Section "InputClass"
		        Identifier "system-keyboard"
		        MatchIsKeyboard "on"
		        Option "XkbLayout" "$XKB"
		        Option "XkbVariant" "$VARIANT"
		EndSection
	EOF
	printf '[Layout]\nLayoutList=%s\nVariantList=%s\nUse=true\n' "$XKB" "$VARIANT" > "$TARGET/etc/xdg/kxkbrc"
	echo "locale-keyboard: desktop keyboard $XKB${VARIANT:+ ($VARIANT)}"
fi
