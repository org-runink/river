#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# pair-menu.sh — SOURCED by runink-install (never run as a step). It asks what this live
# session is for, after the network step and before anything else:
#
#   local     Install this machine                      (the ordinary install; returns)
#   target    Let another machine install this one      (river-pair-announce; never returns)
#   operator  Install other machines on this network    (river-pair; never returns)
#
# LAN installs are opt-in on BOTH machines (docs/INSTALL.md, "LAN installs"): nothing
# announces itself or listens unless the operator at that machine chose "target" here, and
# the operator machine only listens. An entry whose tools are not on this image is not
# offered; with only the local install available the menu is skipped.
#
#   pair_menu MODE LAB   MODE: "" (ask) | local | target | operator; LAB: "" or --lab

pair_menu() {
	_mode="$1"
	_lab="$2"
	_can_target=""
	_can_operator=""
	command -v river-pair-announce >/dev/null 2>&1 && command -v runink-autoinstall >/dev/null 2>&1 \
		&& [ -d /usr/local/lib/river-pair/sv/river-pair-announce ] && _can_target=1
	command -v river-pair >/dev/null 2>&1 && command -v ssh >/dev/null 2>&1 && _can_operator=1
	if [ -z "$_mode" ]; then
		if [ -z "$_can_target$_can_operator" ]; then
			_mode=local
		elif command -v dialog >/dev/null 2>&1; then
			set -- local "Install this machine"
			[ -n "$_can_target" ] && set -- "$@" target "Let another machine install this one"
			[ -n "$_can_operator" ] && set -- "$@" operator "Install other machines on this network"
			_mode="$(dialog --stdout --title "Runink River installer" --menu \
				"What should this machine do?" 12 70 3 "$@")" || exit 1
			clear 2>/dev/null || true
		else
			echo "What should this machine do?"
			echo "  1) Install this machine"
			[ -n "$_can_target" ] && echo "  2) Let another machine install this one"
			[ -n "$_can_operator" ] && echo "  3) Install other machines on this network"
			printf 'Choice [1]: '
			read -r _c || _c=1
			case "$_c" in
				2) _mode=target ;;
				3) _mode=operator ;;
				*) _mode=local ;;
			esac
		fi
	fi
	case "$_mode" in
		local) return 0 ;;
		target)
			[ -n "$_can_target" ] || { echo "runink-install: LAN install (target) is not available on this image" >&2; exit 1; }
			echo "Let another machine install this one: this machine announces itself on the local link"
			echo "and waits for an operator who has the code shown below AND your \"y\" on this screen."
			exec river-pair-announce
			;;
		operator)
			[ -n "$_can_operator" ] || { echo "runink-install: LAN install (operator) is not available on this image" >&2; exit 1; }
			echo "Install other machines on this network: listening for machines whose owners chose"
			echo "\"Let another machine install this one\" (nothing is scanned or probed)."
			# shellcheck disable=SC2086 # $_lab is empty or the single word --lab
			exec river-pair install $_lab
			;;
		*) echo "runink-install: unknown mode $_mode (local, target or operator)" >&2; exit 2 ;;
	esac
}
