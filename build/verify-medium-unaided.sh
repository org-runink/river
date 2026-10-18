#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# verify-medium-unaided.sh — boot an ISO under QEMU with NOTHING injected and decide whether
# the MEDIUM reaches the graphical installer on its own.
#
#   build/verify-medium-unaided.sh ISO
#
# This is the check build/qemu-gui-test.sh tells you to write and is not. That harness
# overlays repo-built binaries over the image's own river-installer and river-kiosk, and
# starts the wizard when the image did not; its own header says so. A green gui-desktop
# there means the INSTALLER works. It cannot mean the medium boots to the wizard unaided,
# because it repairs the thing it is measuring.
#
# This script injects nothing, starts nothing, and overlays nothing. It boots the ISO, waits,
# and asks the running system five questions. RELEASE.md criterion 3 depends on it.
#
# WHY IT EXISTS (2026-10-18). The shipped 2026-10-18 ISO booted to a text console. SDDM puts
# its greeter on VT 1 and ignores MinimumVT in Wayland mode; the live medium also ran an
# autologin getty there. sddm-helper could not take the TTY, logged
# "Greeter stopped. SDDM::Auth::HELPER_TTY_ERROR / Jumping to VT 1" and respawned forever --
# 194 crashes, screen black, nothing on the console saying the display manager was dying. No
# existing check could see it: the GUI harness started the wizard itself, and CI never boots
# a medium. Measured against that ISO this script fails on four of its five questions.
#
# RIG. -cpu host, and virtio-vga so /dev/dri/renderD128 exists. With QEMU's default bochs VGA
# there is no render node, kwin_wayland quits with "Found no render device!", and EVERY image
# fails for a reason that belongs to the rig. A disk is attached because the installer plan
# wants one; nothing is written to it.
set -eu

ISO="${1:-}"
[ -n "$ISO" ] && [ -f "$ISO" ] || { sed -n '5,8p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

OVMF_CODE="${OVMF_CODE:-/usr/share/edk2/x64/OVMF_CODE.4m.fd}"
OVMF_VARS="${OVMF_VARS:-/usr/share/edk2/x64/OVMF_VARS.4m.fd}"
for f in "$OVMF_CODE" "$OVMF_VARS"; do
	[ -r "$f" ] || { echo "verify-medium: no OVMF firmware at $f (set OVMF_CODE/OVMF_VARS)" >&2; exit 2; }
done

D="$(mktemp -d)"
LOG="$D/serial.log"; SER="$D/ser.sock"; FIFO="$D/in.fifo"
QPID=""
cleanup() { [ -n "$QPID" ] && kill "$QPID" 2>/dev/null; rm -rf "$D"; }
trap cleanup EXIT INT TERM
mkfifo "$FIFO"
cp -f "$OVMF_VARS" "$D/vars.fd"
qemu-img create -f qcow2 "$D/scratch.qcow2" 128G >/dev/null

qemu-system-x86_64 -enable-kvm -cpu host -m 8G -smp 4 -machine q35 \
	-drive if=pflash,format=raw,readonly=on,file="$OVMF_CODE" \
	-drive if=pflash,format=raw,file="$D/vars.fd" \
	-drive file="$D/scratch.qcow2",if=virtio,format=qcow2 \
	-cdrom "$ISO" \
	-netdev user,id=n0 -device virtio-net-pci,netdev=n0 \
	-device virtio-vga -display none \
	-chardev socket,id=s0,path="$SER",server=on,wait=off,logfile="$LOG" -serial chardev:s0 &
QPID=$!
sleep 6
tail -f "$FIFO" | socat - unix-connect:"$SER" >/dev/null 2>&1 &

# Wait for ANY prompt. A console shell IS the failure mode, so waiting only for a success
# marker would hang a broken medium until the timeout and report nothing useful.
i=0
while [ "$i" -lt 72 ]; do
	grep -qa -E 'login:|\$ |# ' "$LOG" 2>/dev/null && break
	i=$((i + 1)); sleep 5
done
sleep 45   # let the graphical session come up, or crash-loop

# The probe goes in base64: a multi-line script with nested quotes does not survive a serial
# line intact, and a mangled probe that yields empty values must never read as a verdict.
cat > "$D/probe.sh" <<'PEOF'
#!/bin/sh
# Settle first, then sample twice. A fixed delay is not enough: sampled too early the log is
# absent (which `grep -c` reports as nothing, indistinguishable from zero errors) and a
# respawning kwin_wayland is briefly in `ps`, so an early probe PASSES a crash-looping image.
# Observed doing exactly that on the 2026-10-18 ISO: one run OK, the next FAIL, same image.
n=0
while [ "$n" -lt 36 ]; do
	[ -e /var/log/sddm.log ] && break
	n=$((n + 1)); sleep 5
done
if [ -e /var/log/sddm.log ]; then L=1; else L=0; fi
sleep 30
T=$(sudo grep -c HELPER_TTY_ERROR /var/log/sddm.log 2>/dev/null); [ -n "$T" ] || T=0
A=$(sudo s6-svstat -p /run/service/sddm-srv 2>/dev/null)
PA=$(ps -eo comm | grep -cE '^(plasma_session|kwin_wayland)$')
sleep 12
B=$(sudo s6-svstat -p /run/service/sddm-srv 2>/dev/null)
PB=$(ps -eo comm | grep -cE '^(plasma_session|kwin_wayland)$')
echo "RV_DMLOG=${L:-}"
echo "RV_TTYERR=${T:-}"
echo "RV_PIDA=${A:-}"
echo "RV_PIDB=${B:-}"
echo "RV_PLASMAA=${PA:-}"
echo "RV_PLASMAB=${PB:-}"
echo "RV_INST=$(ps -eo comm | grep -c '^river-installer$')"
echo "RV_GETTY=$(sudo s6-rc -a list 2>/dev/null | grep -cx tty1)"
echo RV_DONE
PEOF
: > "$LOG"
printf 'echo %s | base64 -d > /tmp/rvprobe.sh; sh /tmp/rvprobe.sh\n' "$(base64 -w0 "$D/probe.sh")" > "$FIFO"
sleep 45
OUT="$(sed 's/\x1b\[[0-9;?]*[a-zA-Z]//g' "$LOG" | tr -d '\r')"

val() { printf '%s\n' "$OUT" | sed -n "s/^RV_$1=\(.*\)$/\1/p" | tail -1 | tr -dc '0-9-'; }
dmlog="$(val DMLOG)"; ttyerr="$(val TTYERR)"; pida="$(val PIDA)"; pidb="$(val PIDB)"
plasmaa="$(val PLASMAA)"; plasmab="$(val PLASMAB)"; inst="$(val INST)"; getty="$(val GETTY)"

echo "--- verify-medium-unaided: $(basename "$ISO") ---"
printf '%s\n' "$OUT" | grep '^RV_' | sed 's/^/  /'
if ! printf '%s\n' "$OUT" | grep -q RV_DONE; then
	echo "RIVERTEST FAIL medium-probe   the probe did not complete; no verdict. Serial tail:" >&2
	printf '%s\n' "$OUT" | tail -25 >&2
	exit 1
fi

fail=0
say() { if [ "$2" = ok ]; then echo "RIVERTEST OK   $1"; else echo "RIVERTEST FAIL $1   $3"; fail=1; fi; }

# An unparsed value is a broken probe, not a verdict. Say which.
for v in dmlog ttyerr pida pidb plasmaa plasmab inst getty; do
	eval "x=\${$v}"
	[ -n "$x" ] || say "medium-probe" ko "value $v did not parse (broken harness, not a verdict)"
done
[ "$fail" = 0 ] || exit 1

[ "$getty" = 0 ] && say medium-vt-free ok \
	|| say medium-vt-free ko "a tty1 getty is active; it starves the display manager's greeter"
# "No log" is not "no errors" -- it means sddm never got far enough to write one.
[ "$dmlog" = 1 ] && say medium-dm-started ok \
	|| say medium-dm-started ko "no /var/log/sddm.log after 3 min: the display manager never started"
[ "$ttyerr" = 0 ] && say medium-dm-tty ok \
	|| say medium-dm-tty ko "$ttyerr HELPER_TTY_ERROR in sddm.log: the greeter is losing the TTY"
# s6-svstat -p prints -1 when the service is down; equal-and-negative is not stable.
{ [ "$pida" = "$pidb" ] && [ "$pida" -gt 0 ] 2>/dev/null; } && say medium-dm-stable ok \
	|| say medium-dm-stable ko "sddm-srv pid $pida -> $pidb across 12s: not a stable supervisor"
# Two samples: a respawning kwin_wayland is in ps often enough for one sample to catch it.
{ [ "$plasmaa" -ge 1 ] && [ "$plasmab" -ge 1 ]; } 2>/dev/null && say medium-desktop ok \
	|| say medium-desktop ko "no sustained Plasma session (samples: $plasmaa, $plasmab)"
[ "$inst" -ge 1 ] 2>/dev/null && say medium-installer ok \
	|| say medium-installer ko "river-installer is not running"

[ "$fail" = 0 ] && echo "verify-medium-unaided: the medium reaches the graphical installer unaided"
exit "$fail"
