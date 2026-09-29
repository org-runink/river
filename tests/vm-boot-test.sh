#!/bin/sh
# vm-boot-test.sh — boot the built ISO in qemu, install to a virtual ZFS disk, run asserts.
#
# Local stand-in for the P6 gate. Requires qemu + KVM. Creates a scratch disk, boots the ISO
# with a serial console + a kickstart-ish enrollment file, then (after install + reboot)
# runs tests/assert-golden.sh over the guest's serial/ssh.
#
# This is intentionally a scaffold: full automation needs an unattended path through
# runink-install. Use it as the manual harness until the unattended installer lands.
set -eu

ISO="${1:-$(find "${HOME}/artools-workspace" -maxdepth 3 -name 'runink-river-*.iso' 2>/dev/null | head -1)}"
[ -n "$ISO" ] && [ -f "$ISO" ] || { echo "vm-boot-test: ISO not found — run 'make iso' first (pass path as arg1)" >&2; exit 1; }

command -v qemu-system-x86_64 >/dev/null 2>&1 || { echo "vm-boot-test: qemu required" >&2; exit 1; }
OVMF="${OVMF:-/usr/share/edk2/x64/OVMF_CODE.4m.fd}"
[ -f "$OVMF" ] || { echo "vm-boot-test: OVMF firmware not found at $OVMF (set OVMF=)" >&2; exit 1; }

WORK="$(mktemp -d)"
DISK="$WORK/target.qcow2"
qemu-img create -f qcow2 "$DISK" 40G >/dev/null

echo "vm-boot-test: booting $ISO (serial console; install with 'runink-install')"
echo "  after install + reboot, run: sh tests/assert-golden.sh   (inside the guest)"
qemu-system-x86_64 \
	-enable-kvm -m 12288 -smp "$(nproc)" \
	-machine q35 \
	-drive if=pflash,format=raw,readonly=on,file="$OVMF" \
	-drive file="$DISK",if=virtio,format=qcow2 \
	-cdrom "$ISO" \
	-boot d \
	-netdev user,id=n0,hostfwd=tcp::2222-:22 -device virtio-net,netdev=n0 \
	-nographic

echo "vm-boot-test: qemu exited. Scratch disk: $DISK"
