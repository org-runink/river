# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# qemu-lib.sh — helpers SOURCED by build/cloud-image.sh and build/cloud-image-test.sh to
# drive a QEMU guest as the calling user: FIFO serial and monitor, typing a line on the
# guest keyboard, waiting for a marker on the serial log. The same mechanics as
# build/qemu-test.sh (which keeps its own copy).
#
# The caller sets WORK (the work directory) and QEMU_ARGS (the argument list, word-split),
# and may set QPID/CATS to empty before the first start_vm.
# shellcheck shell=sh

QPID="" CATS=""

qemu_cleanup() {
	[ -n "$QPID" ] && kill "$QPID" 2>/dev/null || true
	for p in $CATS; do kill "$p" 2>/dev/null || true; done
	CATS=""
}

# A FIFO write blocks forever once QEMU has exited, so check it is alive and bound the wait.
mon() { [ -n "$QPID" ] && kill -0 "$QPID" 2>/dev/null || return 0; timeout 5 sh -c 'printf "%s\n" "$1" > "$2"' _ "$*" "$WORK/mon.in" || true; }
shot() { mon "screendump $WORK/$1.png -f png"; sleep 1; }

# type_line TEXT — type TEXT and Enter on the guest keyboard (US layout).
type_line() {
	s="$1"
	while [ -n "$s" ]; do
		c="${s%"${s#?}"}"; s="${s#?}"
		case "$c" in
			[a-z0-9]) k="$c" ;;
			[A-Z]) k="shift-$(printf '%s' "$c" | tr 'A-Z' 'a-z')" ;;
			' ') k='spc' ;; '-') k='minus' ;; '/') k='slash' ;; '.') k='dot' ;; ';') k='semicolon' ;;
			'"') k=shift-apostrophe ;; '=') k=equal ;; '_') k=shift-minus ;;
			*) echo "qemu-lib: cannot type '$c'" >&2; return 1 ;;
		esac
		mon "sendkey $k"
		sleep 0.03
	done
	mon "sendkey ret"
}

# start_vm NAME [extra args...] — QEMU with $QEMU_ARGS plus the extras; the serial port is
# appended to $WORK/serial-NAME.log.
start_vm() {
	name="$1"; shift
	for f in ser mon; do rm -f "$WORK/$f.in" "$WORK/$f.out"; mkfifo "$WORK/$f.in" "$WORK/$f.out"; done
	# shellcheck disable=SC2086 # QEMU_ARGS is a word list by design
	qemu-system-x86_64 $QEMU_ARGS \
		-chardev pipe,id=ser0,path="$WORK/ser" -serial chardev:ser0 \
		-chardev pipe,id=mon0,path="$WORK/mon" -mon chardev=mon0,mode=readline \
		"$@" > "$WORK/qemu-$name.log" 2>&1 &
	QPID=$!
	cat "$WORK/ser.out" >> "$WORK/serial-$name.log" & CATS="$CATS $!"
	cat "$WORK/mon.out" > /dev/null & CATS="$CATS $!"
}

seen() { grep -aq "$1" "$WORK/serial-$2.log" 2>/dev/null; }

# wait_for PATTERN PHASE SECONDS — true once PATTERN is on the phase's serial log.
wait_for() {
	i=0
	while [ "$i" -lt "$3" ]; do
		seen "$1" "$2" && return 0
		kill -0 "$QPID" 2>/dev/null || return 1
		sleep 5; i=$((i + 5))
	done
	return 1
}

# wait_exit SECONDS — wait for QEMU to exit; kill it after SECONDS.
wait_exit() {
	i=0
	while kill -0 "$QPID" 2>/dev/null; do
		[ "$i" -lt "$1" ] || { kill "$QPID" 2>/dev/null; sleep 2; QPID=""; return 1; }
		sleep 5; i=$((i + 5))
	done
	QPID=""
	for p in $CATS; do kill "$p" 2>/dev/null || true; done
	CATS=""
}

# ovmf_check — sets OVMF_CODE / OVMF_VARS (edk2-ovmf) or fails.
ovmf_check() {
	OVMF_CODE="${OVMF_CODE:-/usr/share/edk2/x64/OVMF_CODE.4m.fd}"
	OVMF_VARS="${OVMF_VARS:-/usr/share/edk2/x64/OVMF_VARS.4m.fd}"
	[ -f "$OVMF_CODE" ] && [ -f "$OVMF_VARS" ]
}
