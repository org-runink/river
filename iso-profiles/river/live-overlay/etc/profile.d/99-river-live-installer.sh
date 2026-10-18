# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# A live login on tty1 OPENS THE INSTALLER. Nothing is typed.
#
# WHY THIS EXISTS. The medium already has a fallback chain -- rc.local starts
# river-live-fallback, which waits for a desktop, then tries the weston kiosk,
# then the text installer on tty1. On the owner's hardware (2026-10-18) none of
# it was reached: the medium came up on a bare console and the only way in was
# knowing to type `sudo runink-install`. That is the exact failure
# river-live-fallback's own header says it exists to prevent, so the chain is
# correct and something upstream of it did not run or did not finish.
#
# This does not replace that chain and does not try to diagnose it. It is a
# last line: by the time a login shell is running on tty1 of a live medium, the
# machine is demonstrably far enough up to run a dialog, so the installer can
# be offered from here whatever happened earlier.
#
# GUARDED NARROWLY, because a profile.d file runs for every login shell:
#   - live medium only      (overlay=livefs on the kernel cmdline)
#   - tty1 only             (tty2 stays a plain console, and qemu-test.sh drives it)
#   - interactive only      ($- has i; scp/rsync/subshells are untouched)
#   - once per boot         (a stamp in /run, so Ctrl-C does not re-arm it)
#   - never when the kiosk or the graphical installer is already up
#
# It is INTERRUPTIBLE on purpose: 5 seconds to press a key, then the console is
# yours. The owner asked for zero-knowledge install, not for a captive shell.
case $- in *i*) ;; *) return 0 ;; esac
[ -r /proc/cmdline ] && grep -qw 'overlay=livefs' /proc/cmdline 2>/dev/null || return 0
[ "$(tty 2>/dev/null)" = /dev/tty1 ] || return 0
[ -e /run/river-live-installer.offered ] && return 0
pgrep -x weston >/dev/null 2>&1 && return 0
pgrep -f river-installer-desktop >/dev/null 2>&1 && return 0
: > /run/river-live-installer.offered 2>/dev/null

printf '\n  Runink River — this medium installs the system.\n\n'
printf '  Starting the installer. Press any key within 5 seconds for a shell instead.\n\n'
# `read -t`/`-n` are bash, not POSIX (shellcheck SC3045). The live user's login
# shell IS bash (profile.yaml: "the live user's shell is bash", matching the
# installed admin), so the timed read is available in practice -- but profile.d
# is sourced by whatever reads it, and a dash or busybox sh would error here
# rather than fall through. So the bashism is GUARDED instead of assumed, and
# the non-bash path still gives the same 5 seconds and the same escape.
_ri_wait() {
	if [ -n "${BASH_VERSION:-}" ]; then
		# shellcheck disable=SC3045 # reached only when BASH_VERSION is set
		read -r -t 5 -n 1 _ 2>/dev/null
	else
		read -r _ 2>/dev/null &
		_ri_pid=$!
		( sleep 5; kill "$_ri_pid" 2>/dev/null ) 2>/dev/null &
		wait "$_ri_pid" 2>/dev/null
	fi
}
if _ri_wait; then
	printf '\n  Shell it is. Run `sudo runink-install` when you want the installer.\n\n'
	unset -f _ri_wait
	return 0
fi
unset -f _ri_wait
exec sudo -n /usr/local/bin/runink-install 2>/dev/null || {
	printf '\n  Could not start the installer automatically. Run: sudo runink-install\n\n'
	return 0
}
