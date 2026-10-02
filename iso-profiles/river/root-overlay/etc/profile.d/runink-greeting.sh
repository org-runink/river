# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# Runink River: the greeting an interactive login shell prints — the Runink River mark and
# this machine's summary (fastfetch, /etc/xdg/fastfetch/config.jsonc). Nothing leaves the
# machine.
#
# STATIC, by design. This replaced a fish greeting that played a six-frame arrival animation
# (owner, 2026-10-02: "Do we really need the fish? Can we make it less dancy"). A login
# greeting is read once and then lived with; motion in it is a cost paid on every login.
#
#   RUNINK_GREETING=off   no greeting at all
#
# INTERACTIVE SHELLS ONLY. /etc/profile.d is sourced by login shells including the ones scp,
# sftp and `ssh host command` start, and printing a banner into those streams corrupts the
# transfer — scp fails with "protocol error" and rsync-over-SSH dies on the first byte. The
# $- check is what keeps that from happening: a non-interactive shell has no `i` in $-.
case $- in *i*) ;; *) return 0 ;; esac
[ "${RUNINK_GREETING:-}" = off ] && return 0
command -v fastfetch >/dev/null 2>&1 || return 0

# The Linux console (a text VT) skips the modules that open the GPU: Display and GPU probe
# /dev/dri, and on the live medium VT2's greeting ran them while the desktop compositor was
# taking the display over — the suspected cause of a black first Ctrl+Alt+F2 (river#134). A
# text console has no display to report anyway, and cannot draw the truecolor mark: it shows
# the ASCII mark instead (the one /etc/issue shows).
if [ "$TERM" = linux ]; then
	fastfetch --logo /usr/share/runink/fastfetch/river-mark.txt --logo-type file \
		--logo-color-1 white \
		--structure Title:Separator:OS:Host:Kernel:InitSystem:Uptime:Packages:Shell:CPU:Memory:Swap:Disk:LocalIp:Locale:Break:Colors
else
	fastfetch
fi
