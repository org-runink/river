#!/bin/sh
# tier2-boot-smoke.sh — Tier 2 (docs/governance/CI.md): boot a built ISO in a short-lived,
# throwaway QEMU/KVM microVM and require the live session to reach a login prompt on the
# serial console. Unattended; the VM, its disk and its firmware vars live in a temp dir that
# is removed on exit.
#
# STATUS: scaffold. It has not yet run on a registered Tier 2 runner. The full gate (install
# to a virtual ZFS disk, reboot, run tests/assert-golden.sh) is tests/vm-boot-test.sh, which
# is still interactive. If the live ISO does not print to the serial console, add
# console=ttyS0 to the live kernel command line before relying on this check.
#
# Usage: tests/tier2-boot-smoke.sh <iso> [timeout-seconds]
set -eu

ISO="${1:?usage: tier2-boot-smoke.sh <iso> [timeout-seconds]}"
TIMEOUT="${2:-600}"
[ -f "$ISO" ] || { echo "tier2: no ISO at $ISO" >&2; exit 1; }
command -v qemu-system-x86_64 >/dev/null 2>&1 || { echo "tier2: qemu required" >&2; exit 1; }
[ -w /dev/kvm ] || { echo "tier2: /dev/kvm not writable — Tier 2 needs KVM" >&2; exit 1; }
OVMF="${OVMF:-/usr/share/edk2/x64/OVMF_CODE.4m.fd}"
[ -f "$OVMF" ] || { echo "tier2: OVMF firmware not found at $OVMF (set OVMF=)" >&2; exit 1; }

WORK="$(mktemp -d)"
QPID=""
cleanup() { [ -n "$QPID" ] && kill "$QPID" 2>/dev/null; rm -rf "$WORK"; }
trap cleanup EXIT INT TERM

# No NIC at all: the boot smoke needs no network, and the VM gets none.
qemu-system-x86_64 -enable-kvm -m 4096 -smp 2 -machine q35 \
	-drive if=pflash,format=raw,readonly=on,file="$OVMF" \
	-cdrom "$ISO" -boot d -nic none -display none \
	-serial "file:$WORK/serial.log" -monitor none &
QPID=$!

i=0
while [ "$i" -lt "$TIMEOUT" ]; do
	if grep -q 'login:' "$WORK/serial.log" 2>/dev/null; then
		echo "tier2: live session reached a login prompt after ${i}s"
		exit 0
	fi
	kill -0 "$QPID" 2>/dev/null || { echo "tier2: qemu exited early" >&2; tail -40 "$WORK/serial.log" >&2; exit 1; }
	sleep 5; i=$((i + 5))
done
echo "tier2: no login prompt within ${TIMEOUT}s; last serial output:" >&2
tail -40 "$WORK/serial.log" >&2
exit 1
