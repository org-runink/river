#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# steprun.sh — shared by the text-mode install drivers (runink-install, runink-autoinstall);
# SOURCED, never run as a step. It runs one installer step so that a failure is never silent:
#
#   * the step's output still reaches the console as it is written (a passphrase prompt has
#     no newline, so the output is teed, never read line by line);
#   * the whole output is appended to the install log, $STEPRUN_LOG, which is 0600 and, once
#     the new system exists, copied onto it (/var/log/runink-install.log);
#   * the step's exit code is kept (a pipe would hide it);
#   * on a failure steprun_fail prints, in plain language, which step failed, why (the
#     step's own last lines and its exit code), where the full log is, what state the
#     machine is left in and what to do next;
#   * a line a step prints that starts with "install-notice: " (a deferred step, say) is
#     collected and repeated by steprun_summary at the very end, so it is not lost in the
#     scroll.
#
# 10-disk-zfs prints the ZFS recovery key, which is never written to a file: its output
# reaches the console only and the log records that it was withheld. The graphical
# installer keeps the same log (installer/internal/wizard/installreport.go) and uses the
# same wording for the states.
#
#   steprun_init              once, before the first step
#   steprun_run STEP FILE     run sh FILE; returns the step's exit code (STEPRUN_RC)
#   steprun_fail STEP RC      print the failure report (to stderr, and a dialog if there is one)
#   steprun_summary           print the collected notices (stdout); prints nothing without any

STEPRUN_LOG="${RUNINK_INSTALL_LOG:-/var/log/runink-install.log}"
STEPRUN_DIR=""
STEPRUN_RC=0

steprun_init() {
	_base="${RUNINK_STEPRUN_TMP:-${TMPDIR:-/tmp}}"
	_umask="$(umask)"
	umask 077
	STEPRUN_DIR="$(mktemp -d "$_base/steprun.XXXXXX")"
	mkdir -p "$(dirname "$STEPRUN_LOG")" 2>/dev/null || true
	: > "$STEPRUN_DIR/notices"
	{ echo "=== install started $(date -u +%Y-%m-%dT%H:%M:%SZ)"; } >> "$STEPRUN_LOG" 2>/dev/null || STEPRUN_LOG="$STEPRUN_DIR/install.log"
	umask "$_umask"
}

# steprun_persist — copy the log onto the new system once it has an /etc (best effort).
steprun_persist() {
	[ -d "${RUNINK_TARGET:-/nonexistent}/etc" ] || return 0
	mkdir -p "$RUNINK_TARGET/var/log" 2>/dev/null || return 0
	( umask 077; cp "$STEPRUN_LOG" "$RUNINK_TARGET/var/log/$(basename "$STEPRUN_LOG")" ) 2>/dev/null || return 0
	return 0
}

steprun_run() {
	_s="$1" _f="$2"
	_raw="$STEPRUN_DIR/raw" _rcf="$STEPRUN_DIR/rc"
	: > "$_raw"
	rm -f "$_rcf"
	echo "=== $_s $(date -u +%H:%M:%S)" >> "$STEPRUN_LOG" 2>/dev/null || true
	if [ "$_s" = 10-disk-zfs ]; then
		# The recovery key is on the console only.
		{ sh "$_f" 2>&1; echo "$?" > "$_rcf"; } | cat
		echo "(output withheld: it carries the recovery key)" >> "$STEPRUN_LOG" 2>/dev/null || true
	else
		{ sh "$_f" 2>&1; echo "$?" > "$_rcf"; } | tee "$_raw"
		# rsync's progress rewrites its line with \r: one line per update, but not in the log.
		tr '\r' '\n' < "$_raw" | grep -v '^[[:space:]]*[0-9][0-9,]*[[:space:]]\{1,\}[0-9]\{1,3\}%' >> "$STEPRUN_LOG" 2>/dev/null || true
		tr '\r' '\n' < "$_raw" | grep -v '^[[:space:]]*$' | tail -n 12 > "$STEPRUN_DIR/tail"
		tr '\r' '\n' < "$_raw" | sed -n "s/^install-notice: \{0,1\}/$_s: /p" >> "$STEPRUN_DIR/notices"
	fi
	STEPRUN_RC="$(cat "$_rcf" 2>/dev/null)"
	# A step that could not even start leaves no code: that is a failure too.
	case "$STEPRUN_RC" in '' | *[!0-9]*) STEPRUN_RC=1 ;; esac
	echo "=== $_s exited $STEPRUN_RC" >> "$STEPRUN_LOG" 2>/dev/null || true
	[ "$_s" = 10-disk-zfs ] && [ "$STEPRUN_RC" -ne 0 ] && : > "$STEPRUN_DIR/tail"
	steprun_persist
	return "$STEPRUN_RC"
}

# steprun_state STEP — the state a failure at STEP leaves: through 40-boot-grub-zfs nothing
# bootable exists; after it the system is on the disk but unfinished.
steprun_state() {
	case "$1" in
	[0-4][0-9]-*) echo not-installed ;;
	*) echo partial ;;
	esac
}

steprun_fail() {
	_s="$1" _rc="$2"
	_state="$(steprun_state "$_s")"
	_msg="$STEPRUN_DIR/failure.txt"
	{
		echo "THE INSTALLATION STOPPED at step $_s (exit code $_rc)."
		echo
		echo "Why: the step's last lines were:"
		if [ -s "$STEPRUN_DIR/tail" ]; then
			sed 's/^/    /' "$STEPRUN_DIR/tail"
		else
			echo "    (it printed nothing; see the log)"
		fi
		echo
		if [ "$_state" = not-installed ]; then
			echo "State of this computer: NOT installed. Nothing on it can start yet."
		else
			echo "State of this computer: only PARTLY installed. The system is on the disk, but step $_s"
			echo "and the steps after it did not run, so it is not ready to use."
		fi
		echo
		echo "Full log: $STEPRUN_LOG (this live session; it is lost when you restart)"
		if [ -f "${RUNINK_TARGET:-/nonexistent}/var/log/$(basename "$STEPRUN_LOG")" ]; then
			echo "          a copy is on the new system at /var/log/$(basename "$STEPRUN_LOG")"
		fi
		echo
		echo "What to do: read the reason above and fix it (free disk space, check the cable or the"
		echo "stick, check the passphrase), then run the installer again. If it fails again, keep the"
		echo "log text and ask for help."
	} > "$_msg"
	echo "installer: step $_s FAILED (exit $_rc)" >> "$STEPRUN_LOG" 2>/dev/null || true
	echo >&2
	echo "======================================================================" >&2
	cat "$_msg" >&2
	echo "======================================================================" >&2
	if command -v dialog >/dev/null 2>&1 && [ -t 1 ] && [ -z "${RUNINK_UNATTENDED:-}" ]; then
		dialog --title "Installation stopped" --exit-label "OK" --textbox "$_msg" 0 0 || true
		clear 2>/dev/null || true
	fi
}

steprun_summary() {
	[ -s "$STEPRUN_DIR/notices" ] || return 0
	echo
	echo "Read this before you restart:"
	sed 's/^/  - /' "$STEPRUN_DIR/notices"
	cp "$STEPRUN_DIR/notices" "$STEPRUN_DIR/notices.shown"
}
