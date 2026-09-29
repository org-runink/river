#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# qemu-lan-test.sh — two-VM test of opt-in LAN install pairing (docs/INSTALL.md, "LAN
# installs") against a built Runink River ISO (or a downstream one), unattended, as the calling
# user (no root; needs a writable /dev/kvm).
#
#   build/qemu-lan-test.sh [options] ISO
#
# Two guests boot the ISO on one private L2 segment (QEMU `-netdev socket,mcast=` on the
# host loopback: no bridge, no host network, no other machine can see it):
#
#   B  the TARGET, with a scratch qcow2 (serial RIVERQEMU0002). Its kit runs the real menu
#      entry, `runink-install --mode target`; the pairing code and host key it shows are read
#      off its console (the serial port), and "y" is typed on its own keyboard.
#   A  the OPERATOR, no disk. Its kit first runs `runink-install --mode operator` once with
#      no input (the menu entry lists B, passively), then `river-pair install --lab
#      --payload ...`; every answer (target, code, "the fingerprint matches", the disk
#      serial, hostname, the admin key file, YES, reboot) is typed on its keyboard.
#
# What the harness checks, and fails on:
#   pairing       A lists B with the host key fingerprint B's own screen shows; a mistyped
#                 code is refused on A without reaching B; a WRONG code reaches B and is
#                 counted there ("4 left"); the right code pairs only after B's local "y",
#                 and the operator key B shows is the one A shows
#   payload       rsync over the restricted channel + `stage-payload` called the payload
#                 tool with --src/--dest (a TEST STUB river-payloadpack on B, standing in for
#                 the unmerged one), same digest on both sides
#   install       runink-autoinstall ran on B through the channel: AUTOINSTALL-DONE, and the
#                 SAME recovery key shown once on A and on B's console
#   reboot        B rebooted on A's command (B runs with -no-reboot, so its QEMU exits)
#   firewall      both live systems load the default-deny host firewall (runink-fw) with the
#                 LAN-install pairing ports in its link-local-only pair sets, and pair through it
#   teardown      nothing of the session is left on B's live system after it ends (checked
#                 by B's kit before the reboot), and the pairing ports are closed
#   installed     B boots the installed system from its disk: ZFS root, unlocked with the key
#                 A showed, hostname and admin key from A, no pairing user, assert-golden.sh
#
# How B's installed system is checked: like build/qemu-test.sh, a second LIVE boot of B
# imports the pool with the recovery key, adds a one-line rc.local hook and a serial console
# to the SCRATCH disk only, and exports it; the third boot runs the checks from the kit.
#
# New binaries are injected the way build/qemu-test.sh --kit-overlay does it: DIR/root/ is
# copied over each live system before the checks. Without --kit-overlay the harness builds
# one from this checkout (river-pair, river-pair-announce, runink-install, pair-menu.sh,
# 20-clone-rootfs.sh and the session service template).
#
# Options:
#   --kit-overlay DIR   overlay to inject (DIR/root/...), instead of building one
#   --mem MiB           guest RAM, at most 3072 (default 3072)
#   --disk-size SIZE    B's scratch disk, sparse (default 80G; the planner wants >= 64 GiB)
#   --work DIR          work directory (default ~/.cache/river-build/qemu-lan-test/<time>)
#   --keep              keep the work directory (default: keep only on failure)
#   --dry-run           print what would run; boot nothing
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}"
MEM=3072 DISK_SIZE=80G WORK="" KEEP=0 DRY=0 ISO="" OVERLAY=""
SERIAL_ID="RIVERQEMU0002"
POOL=zriver
NEWHOST=lanpaired

die() { echo "qemu-lan-test: $*" >&2; exit 2; }
usage() { sed -n '5,/^set -eu/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; exit 2; }
while [ $# -gt 0 ]; do
	case "$1" in
		--kit-overlay) OVERLAY="${2:?}"; shift ;;
		--mem) MEM="${2:?}"; shift ;;
		--disk-size) DISK_SIZE="${2:?}"; shift ;;
		--work) WORK="${2:?}"; shift ;;
		--keep) KEEP=1 ;;
		--dry-run) DRY=1 ;;
		-h|--help) usage ;;
		-*) die "unknown option $1" ;;
		*) [ -z "$ISO" ] || die "one ISO only"; ISO="$1" ;;
	esac
	shift
done
[ -n "$ISO" ] || usage
[ -f "$ISO" ] || die "no ISO at $ISO"
case "$MEM" in *[!0-9]*|'') die "--mem is a number" ;; esac
[ "$MEM" -le 3072 ] || die "--mem is at most 3072 (each guest stays within 3 GiB)"
OVMF_CODE="${OVMF_CODE:-/usr/share/edk2/x64/OVMF_CODE.4m.fd}"
OVMF_VARS="${OVMF_VARS:-/usr/share/edk2/x64/OVMF_VARS.4m.fd}"
missing=""
for t in qemu-system-x86_64 qemu-img mkfs.vfat mcopy blkid go sha256sum; do command -v "$t" >/dev/null 2>&1 || missing="$missing $t"; done
[ -f "$OVMF_CODE" ] && [ -f "$OVMF_VARS" ] || missing="$missing OVMF(edk2-ovmf)"
[ -w /dev/kvm ] || missing="$missing /dev/kvm(writable)"
[ -z "$missing" ] || die "missing prerequisites:$missing"
[ -n "$WORK" ] || WORK="$CACHE/river-build/qemu-lan-test/$(date +%Y%m%d-%H%M%S)"
# A private segment: a multicast group on the host loopback, a port unlikely to be shared.
MPORT=$((20000 + $$ % 20000))
NET="-netdev socket,id=lan,mcast=230.0.0.1:$MPORT,localaddr=127.0.0.1"
MEDIUM="-device qemu-xhci,id=xhci -drive if=none,id=medium,format=raw,readonly=on,file=$ISO -device usb-storage,bus=xhci.0,drive=medium,bootindex=0"
base() { # vm-name
	echo "-enable-kvm -machine q35 -cpu host -m $MEM -smp 2 -display none
 -drive if=pflash,format=raw,readonly=on,file=$OVMF_CODE -drive if=pflash,format=raw,file=$WORK/$1/vars.fd
 -drive if=none,id=kit,format=raw,file=$WORK/$1/kit.img -device virtio-blk-pci,drive=kit,serial=RIVERTESTKIT
 $NET -device virtio-net-pci,netdev=lan,mac=52:54:00:4c:41:$2
 -chardev pipe,id=ser0,path=$WORK/$1/ser -serial chardev:ser0
 -chardev pipe,id=mon0,path=$WORK/$1/mon -mon chardev=mon0,mode=readline"
}
TARGET_DISK="-drive if=none,id=target,format=qcow2,file=$WORK/B/target.qcow2 -device virtio-blk-pci,drive=target,serial=$SERIAL_ID,bootindex=1"

echo "qemu-lan-test: ISO $ISO; two guests of $MEM MiB on a private segment (mcast port $MPORT); work dir $WORK"
if [ "$DRY" -eq 1 ]; then
	echo "  A: qemu-system-x86_64 $(base A 0a | tr '\n' ' ') $MEDIUM"
	echo "  B: qemu-system-x86_64 $(base B 0b | tr '\n' ' ') $TARGET_DISK $MEDIUM -no-reboot"
	exit 0
fi

mkdir -p "$WORK/A" "$WORK/B"
PIDS="" CATS=""
cleanup() {
	for p in $PIDS $CATS; do kill "$p" 2>/dev/null || true; done
}
trap cleanup EXIT INT TERM

# --- the kit overlay ---------------------------------------------------------------------------
if [ -z "$OVERLAY" ]; then
	OVERLAY="$WORK/overlay"
	R="$OVERLAY/root"
	mkdir -p "$R/usr/local/bin" "$R/usr/local/lib/runink-install" "$R/usr/local/lib/river-pair/sv/river-pair-announce"
	for b in pair:river-pair pairannounce:river-pair-announce; do
		( cd "$REPO/installer" && CGO_ENABLED=0 GOAMD64=v1 go build -trimpath -ldflags='-s -w' -o "$R/usr/local/bin/${b#*:}" "./${b%%:*}" )
	done
	cp "$REPO/iso-profiles/river/root-overlay/usr/local/bin/runink-install" "$R/usr/local/bin/"
	cp "$REPO/installer/lib/pair-menu.sh" "$REPO/installer/lib/20-clone-rootfs.sh" "$R/usr/local/lib/runink-install/"
	cp "$REPO/iso-profiles/river/live-overlay/usr/local/lib/river-pair/sv/river-pair-announce/run" \
		"$R/usr/local/lib/river-pair/sv/river-pair-announce/run"
	# The default-deny host firewall and the live medium's pairing ports (runink-fw seed_pair).
	mkdir -p "$R/usr/local/lib/runink-net"
	cp "$REPO/iso-profiles/river/root-overlay/usr/local/bin/runink-fw" "$R/usr/local/bin/"
	cp "$REPO/iso-profiles/river/root-overlay/usr/local/lib/runink-net/"*.nft "$R/usr/local/lib/runink-net/"
	cp "$REPO/iso-profiles/river/live-overlay/usr/local/lib/river-pair/fw-pair" "$R/usr/local/lib/river-pair/fw-pair"
fi
[ -d "$OVERLAY/root" ] || die "--kit-overlay $OVERLAY has no root/"

# --- kits ----------------------------------------------------------------------------------------
mkkit() { # vm role
	k="$WORK/$1/kit"
	rm -rf "$k"; mkdir -p "$k"
	cat > "$k/config" <<EOF
ROLE=$2
TARGET_SERIAL=$SERIAL_ID
POOL=$POOL
NEWHOST=$NEWHOST
EOF
	cp -r "$OVERLAY" "$k/overlay"
	cp "$REPO/tests/assert-golden.sh" "$k/assert-golden.sh"
	cat > "$k/live.sh" <<'EOF'
#!/bin/sh
# Runs as root on a LIVE guest; stdin is its second console (the harness types there),
# results go to the serial port.
exec >/dev/ttyS0 2>&1
set -u
export PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin
. /run/rt/config
ok() { echo "RIVERTEST OK $1"; }
ko() { echo "RIVERTEST FAIL $1${2:+ ($2)}"; }
echo "RIVERTEST BEGIN $ROLE"
# The kit overlay: fixed files tested against this (existing) ISO, each listed.
( cd /run/rt/overlay/root && find . -type f ) | while read -r f; do
	f="${f#./}"
	mkdir -p "/$(dirname "$f")" && cp "/run/rt/overlay/root/$f" "/$f"
	case "$f" in */bin/*|*/sv/*/run) chmod 0755 "/$f" ;; esac
	echo "RIVERTEST NOTE overlay /$f"
done
digest() { ( cd "$1" && find . -type f | sort | xargs sha256sum | sha256sum | cut -c1-16 ); }

if [ "$ROLE" = wire ]; then
	# Second live boot of B: import the installed pool with the recovery key A showed and
	# wire the harness onto the SCRATCH disk (rc.local hook + serial console), as
	# build/qemu-test.sh does after its own install.
	KEY="$(cat /run/rt/recovery-key)"
	tdisk="$(lsblk -dno NAME,SERIAL | awk -v s="$TARGET_SERIAL" '$2 == s { print $1; exit }')"
	if [ -n "$tdisk" ] && zpool import -N -R /mnt "$POOL" && printf '%s' "$KEY" | zfs load-key "$POOL"; then
		ok unlock-live
		be="$(zfs list -H -o name -r "$POOL/ROOT" | sed -n 2p)"
		zfs mount "$be" && zfs mount -a
		echo '[ -e /dev/disk/by-label/RIVERTEST ] && ( mkdir -p /run/rt && mount -r -L RIVERTEST /run/rt && sh /run/rt/installed.sh ) >/dev/null 2>&1 &' >> /mnt/etc/s6/rc.local
		esp="$(lsblk -rno PATH,PARTTYPE "/dev/$tdisk" | awk 'tolower($2) == "c12a7328-f81f-11d2-ba4b-00a0c93ec93b" { print $1; exit }')"
		mkdir -p /run/rt-esp && mount "$esp" /run/rt-esp && \
			sed -i '/^[[:space:]]*linux[[:space:]]/ s/$/ console=tty0 console=ttyS0,115200/' /run/rt-esp/grub/grub.cfg && \
			grep -q 'console=ttyS0' /run/rt-esp/grub/grub.cfg && ok harness-wired || ko harness-wired "esp=$esp"
		umount /run/rt-esp || true
		zfs unmount -a || true
		zpool export "$POOL" && ok export || ko export
	else
		ko unlock-live "disk=$tdisk"
	fi
	echo "RIVERTEST END $ROLE"
	sync; poweroff -f
fi

# Both live guests: free the guide model's memory (3 GiB guests), then the network step
# this image does not have yet: a link-local-only connection on the one NIC.
s6-rc -d change river-guide-model >/dev/null 2>&1 || true
nic="$(ip -o link show | awk -F': ' '$2 != "lo" { print $2; exit }')"
if command -v nmcli >/dev/null 2>&1; then
	nmcli con add type ethernet ifname "$nic" con-name river-lan ipv4.method disabled ipv6.method link-local >/dev/null 2>&1
	nmcli con up river-lan >/dev/null 2>&1
else
	ip link set "$nic" up
fi
i=0
until ip -6 addr show dev "$nic" scope link | grep -v tentative | grep -q 'inet6 fe80'; do
	sleep 1; i=$((i + 1)); [ "$i" -lt 60 ] || break
done
ll="$(ip -6 -o addr show dev "$nic" scope link | awk '{print $4}' | head -1)"
[ -n "$ll" ] && ok "net" || ko net "$nic has no link-local address"
echo "RIVERTEST NOTE $ROLE nic $nic $ll"
# The live medium's default-deny firewall, as its s6 oneshot runink-fw loads it at boot (an
# older ISO gets it from the kit overlay): pairing must work THROUGH it, so its pair sets
# must carry the pairing ports, which the common rules accept from link-local sources only.
if [ -x /usr/local/bin/runink-fw ]; then
	/usr/local/bin/runink-fw apply
	pairs="$(nft -n list set inet runink_fw pair_tcp 2>/dev/null; nft -n list set inet runink_fw pair_udp 2>/dev/null)"
	printf '%s\n' "$pairs" | grep elements | sed "s/^/RIVERTEST NOTE $ROLE fw /"
	if nft list chain inet runink_fw input 2>/dev/null | grep -q 'policy drop' \
		&& printf '%s\n' "$pairs" | grep -q '47654' && printf '%s\n' "$pairs" | grep -q '47655' \
		&& printf '%s\n' "$pairs" | grep -q '47653'; then ok "fw-pair-$ROLE"
	else ko "fw-pair-$ROLE" "$(printf '%s' "$pairs" | tr -s ' \t\n' ' ')"; fi
else
	ko "fw-pair-$ROLE" "no runink-fw on this live system"
fi

if [ "$ROLE" = target ]; then
	# The TEST STUB for the payload tool (the airgap work's river-payloadpack is unmerged):
	# it only proves the call and its arguments, and what arrived.
	cat > /usr/local/bin/river-payloadpack <<'STUB'
#!/bin/sh
[ "$1" = stage ] && [ "$2" = --src ] && [ "$4" = --dest ] || { echo "stub river-payloadpack: bad args: $*" >&2; exit 2; }
mkdir -p "$5" && cp -r "$3"/. "$5"/ || exit 1
echo "STUB-PAYLOADPACK staged $3 -> $5 digest $(cd "$5" && find . -type f | sort | xargs sha256sum | sha256sum | cut -c1-16)"
STUB
	chmod 0755 /usr/local/bin/river-payloadpack
	echo "RIVERTEST READY target"
	# The real menu entry. The local operator (the harness, on this console) answers "y".
	runink-install --skip-network --mode target
	echo "RIVERTEST NOTE runink-install exited $?"
	echo "RIVERTEST END target"
	sync
	sleep 30
	poweroff -f
fi

# operator
mkdir -p /run/rt-admin /run/rt-payload/testpayload/sub
ssh-keygen -q -t ed25519 -N '' -C lan-test-admin -f /run/rt-admin/id
echo "RIVERTEST NOTE adminkey $(cut -d' ' -f1,2 /run/rt-admin/id.pub)"
echo "LAN install test payload" > /run/rt-payload/testpayload/README
head -c 1048576 /dev/urandom > /run/rt-payload/testpayload/sub/blob.bin
echo "RIVERTEST NOTE payload-digest $(digest /run/rt-payload/testpayload)"
echo "RIVERTEST READY operator"
read -r _go
# 1. The menu entry, with no input: it listens (passively), lists what announces, then
#    stops at the first question.
timeout 40 runink-install --skip-network --mode operator --lab </dev/null > /run/rt-menu.log 2>&1
cat /run/rt-menu.log
grep -q 'listening for Runink River installers' /run/rt-menu.log && grep -q 'river-install-' /run/rt-menu.log \
	&& ok operator-menu || ko operator-menu
echo "RIVERTEST READY operator-drive"
read -r _go
# 2. The install, driven from here.
river-pair install --lab --payload /run/rt-payload/testpayload
echo "RIVERTEST NOTE river-pair exited $?"
echo "RIVERTEST END operator"
sync
poweroff -f
EOF
	cat > "$k/installed.sh" <<'EOF'
#!/bin/sh
# Runs as root on B's INSTALLED system (rc.local hook); results go to the serial port.
exec >/dev/ttyS0 2>&1
set -u
export PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin
. /run/rt/config
ok() { echo "RIVERTEST OK $1"; }
ko() { echo "RIVERTEST FAIL $1${2:+ ($2)}"; }
echo "RIVERTEST BEGIN installed"
src="$(findmnt -n -o SOURCE /)"
case "$src" in "$POOL"/ROOT/*) ok zfs-root ;; *) ko zfs-root "$src" ;; esac
[ "$(zfs get -H -o value keystatus "$POOL")" = available ] && ok unlocked || ko unlocked
[ "$(cat /etc/hostname)" = "$NEWHOST" ] && ok hostname || ko hostname "$(cat /etc/hostname)"
grep -qF "$(cat /run/rt/adminkey)" /home/runink/.ssh/authorized_keys 2>/dev/null && ok admin-key || ko admin-key
grep -q '^river-pair:' /etc/passwd /etc/group && ko no-pair-user || ok no-pair-user
[ -e /usr/local/lib/river-pair ] && ko no-pair-service || ok no-pair-service
echo "kernel $(uname -r)"
sh /run/rt/assert-golden.sh && ok golden || ko golden
echo "RIVERTEST END installed"
sync
poweroff -f
EOF
	rm -f "$WORK/$1/kit.img"
	mkfs.vfat -C -n RIVERTEST "$WORK/$1/kit.img" 65536 >/dev/null
	for f in "$k"/*; do mcopy -s -i "$WORK/$1/kit.img" "$f" ::/; done
}
kit_add() { # vm file-name content
	printf '%s\n' "$3" > "$WORK/$1/kit/$2"
	mcopy -o -i "$WORK/$1/kit.img" "$WORK/$1/kit/$2" ::/
}

qemu-img create -q -f qcow2 "$WORK/B/target.qcow2" "$DISK_SIZE"
cp "$OVMF_VARS" "$WORK/A/vars.fd"; cp "$OVMF_VARS" "$WORK/B/vars.fd"
mkkit A operator
mkkit B target

# --- helpers (per VM) ---------------------------------------------------------------------
pid_of() { cat "$WORK/$1/pid" 2>/dev/null; }
alive() { p="$(pid_of "$1")"; [ -n "$p" ] && kill -0 "$p" 2>/dev/null; }
mon() { alive "$1" || return 0; v="$1"; shift; timeout 5 sh -c 'printf "%s\n" "$1" > "$2"' _ "$*" "$WORK/$v/mon.in" || true; }
shot() { mon "$1" "screendump $WORK/$1-$2.png -f png"; sleep 1; }
type_line() { # vm text
	v="$1"; s="$2"
	while [ -n "$s" ]; do
		c="${s%"${s#?}"}"; s="${s#?}"
		case "$c" in
			[a-z0-9]) k="$c" ;;
			[A-Z]) k="shift-$(printf '%s' "$c" | tr 'A-Z' 'a-z')" ;;
			' ') k='spc' ;; '-') k='minus' ;; '/') k='slash' ;; '.') k='dot' ;; ';') k='semicolon' ;;
			'"') k=shift-apostrophe ;; '=') k=equal ;; '_') k=shift-minus ;;
			*) echo "qemu-lan-test: cannot type '$c'" >&2; return 1 ;;
		esac
		mon "$v" "sendkey $k"
		sleep 0.03
	done
	mon "$v" "sendkey ret"
}
start_vm() { # vm phase args...
	v="$1"; ph="$2"; shift 2
	for f in ser mon; do rm -f "$WORK/$v/$f.in" "$WORK/$v/$f.out"; mkfifo "$WORK/$v/$f.in" "$WORK/$v/$f.out"; done
	: > "$WORK/$v/serial-$ph.log"
	# shellcheck disable=SC2046,SC2086 # word lists by design
	qemu-system-x86_64 $(base "$v" "$(printf '%s' "$v" | tr AB ab | sed 's/^/0/')") "$@" > "$WORK/$v/qemu-$ph.log" 2>&1 &
	echo $! > "$WORK/$v/pid"; PIDS="$PIDS $!"
	cat "$WORK/$v/ser.out" >> "$WORK/$v/serial-$ph.log" & CATS="$CATS $!"
	cat "$WORK/$v/mon.out" > /dev/null & CATS="$CATS $!"
	echo "$ph" > "$WORK/$v/phase"
}
log_of() { echo "$WORK/$1/serial-$(cat "$WORK/$1/phase").log"; }
count() { tr -d '\r' < "$(log_of "$1")" | grep -a -c -- "$2" || true; }
wait_n() { # vm pattern n seconds — wait for the n-th occurrence
	i=0
	while [ "$i" -lt "$4" ]; do
		[ "$(count "$1" "$2")" -ge "$3" ] && return 0
		alive "$1" || { [ "$(count "$1" "$2")" -ge "$3" ]; return; }
		sleep 2; i=$((i + 2))
	done
	echo "qemu-lan-test: $1 never showed \"$2\" (#$3)" >&2
	return 1
}
wait_for() { wait_n "$1" "$2" 1 "$3"; }
field() { tr -d '\r' < "$(log_of "$1")" | grep -a -- "$2" | tail -1 | sed "s/.*$2 *//"; }
wait_exit() { # vm seconds
	i=0
	while alive "$1"; do [ "$i" -lt "$2" ] || return 1; sleep 2; i=$((i + 2)); done
}
result() { echo "RIVERTEST $1 $2" >> "$WORK/results.txt"; echo "qemu-lan-test: $1 $2"; }
boot_kit() { # vm — log in on the second console and start the kit
	tries=0
	until [ "$(count "$1" "RIVERTEST BEGIN")" -ge 1 ]; do
		tries=$((tries + 1))
		[ "$tries" -le 5 ] || { echo "qemu-lan-test: $1 never ran the kit" >&2; return 1; }
		sleep 60
		mon "$1" "sendkey alt-f2"; sleep 2
		type_line "$1" runink; sleep 2; type_line "$1" runink; sleep 3
		type_line "$1" "sudo sh -c \"mkdir -p /run/rt; mount -r -L RIVERTEST /run/rt; sh /run/rt/live.sh\""
		sleep 20
		shot "$1" "tty2-try$tries"
	done
}
: > "$WORK/results.txt"
fail() { result FAIL "$1"; FAILED=1; }
FAILED=0

# --- phase 1: both live, B opts in, A pairs and installs --------------------------------------
echo "qemu-lan-test: phase 1 — A (operator) and B (target) boot the live ISO"
# shellcheck disable=SC2086
start_vm B live $TARGET_DISK $MEDIUM -no-reboot
# shellcheck disable=SC2086
start_vm A live $MEDIUM
boot_kit B & bk=$!
boot_kit A
wait "$bk" || true

phase1() {
	wait_for B "RIVERTEST READY target" 300 || return 1
	# Both live systems run the default-deny firewall with the pairing ports open to link-local
	# sources (runink-fw), so everything below goes THROUGH it.
	tr -d '\r' < "$(log_of B)" | grep -aq "RIVERTEST OK fw-pair-target" && result OK fw-pair-target || fail fw-pair-target
	wait_for B "PAIRING CODE:" 120 || return 1
	CODE="$(field B 'PAIRING CODE:' | tr -d ' |')"
	BFP="$(field B 'Host key:' | tr -d ' |')"
	BHOST="$(field B 'This machine:' | tr -d ' |')"
	echo "qemu-lan-test: B shows $BHOST, code $CODE, host key $BFP"
	[ -n "$CODE" ] && [ -n "$BFP" ] && result OK target-opt-in || { fail target-opt-in; return 1; }

	wait_for A "RIVERTEST READY operator" 300 || return 1
	tr -d '\r' < "$(log_of A)" | grep -aq "RIVERTEST OK fw-pair-operator" && result OK fw-pair-operator || fail fw-pair-operator
	type_line A go
	wait_for A "RIVERTEST READY operator-drive" 120 || return 1
	tr -d '\r' < "$(log_of A)" | grep -aq "RIVERTEST OK operator-menu" && result OK operator-menu || fail operator-menu
	tr -d '\r' < "$(log_of A)" | grep -a "$BHOST" | grep -aq "$BFP" && result OK passive-listing || fail passive-listing

	# Baselines: the menu run above already printed some of the prompts waited for below.
	nsel=$(count A "Select a target") ncode=$(count A "Type the pairing code") nfp=$(count A "exactly this host key")
	type_line A go

	# First try: a code with a broken check character is refused ON A (never sent).
	wait_n A "Select a target" $((nsel + 1)) 60 || return 1
	type_line A 1
	wait_n A "Type the pairing code" $((ncode + 1)) 30 || return 1
	d="$(printf '%s' "$CODE" | tr -d '-')"
	last="$(printf '%s' "$d" | cut -c8)"
	bad="$(printf '%s' "$d" | cut -c1-7)$( [ "$last" = Z ] && echo Y || echo Z)"
	type_line A "$bad"
	wait_for A "check character does not match" 20 && [ "$(count B 'WRONG code')" -eq 0 ] \
		&& result OK typo-refused-locally || fail typo-refused-locally
	# Then a well-formed but WRONG code: it reaches B, which counts it.
	type_line A "$(wrong_code "$d")"
	wait_n A "exactly this host key" $((nfp + 1)) 20 || return 1
	type_line A y
	if wait_for B "WRONG code (4 left" 30 && wait_for A "rejected the code" 20; then result OK wrong-code-counted; else fail wrong-code-counted; fi
	nmore=$(count A "Install another machine on this network")
	wait_n A "Install another machine on this network" $((nmore > 0 ? nmore : 1)) 20 || return 1
	type_line A y

	# The real pairing.
	wait_n A "Select a target" $((nsel + 2)) 60 || return 1
	type_line A 1
	wait_n A "Type the pairing code" $((ncode + 3)) 30 || return 1
	type_line A "$CODE"
	wait_n A "exactly this host key" $((nfp + 2)) 20 || return 1
	AFP="$(tr -d '\r' < "$(log_of A)" | grep -a -A1 "announces this host key" | tail -1 | tr -d ' ')"
	[ "$AFP" = "$BFP" ] && result OK fingerprint-matches-target-screen || { fail fingerprint-matches-target-screen; return 1; }
	type_line A y
	wait_for B "Allow this operator to install this machine" 30 || return 1
	sleep 3
	OPFP="$(tr -d '\r' < "$(log_of A)" | grep -a -A1 "This operator's key is" | tail -1 | tr -d ' ')"
	BOP="$(field B 'Paired with' | sed 's/ (from.*//')"
	[ -n "$OPFP" ] && [ "$BOP" = "$OPFP" ] && result OK operator-key-shown-on-target || fail operator-key-shown-on-target
	[ "$(count A 'paired with river-install-')" -eq 0 ] && result OK waits-for-local-y || fail waits-for-local-y
	type_line B y
	wait_for A "paired with river-install-" 30 && result OK paired || { fail paired; return 1; }

	wait_for A "ERASE vd" 180 || return 1
	type_line A "$SERIAL_ID"
	wait_for A "Hostname for this machine" 20 || return 1
	type_line A "$NEWHOST"
	wait_for A "Model payload passphrase" 20 || return 1
	type_line A ""
	wait_for A "runink admin (blank: none)" 20 || return 1
	type_line A /run/rt-admin/id.pub
	wait_for A "Type YES to proceed" 20 || return 1
	type_line A YES

	wait_for A "STUB-PAYLOADPACK staged" 120 || true
	PD="$(field A 'payload-digest')"
	SD="$(tr -d '\r' < "$(log_of A)" | grep -a 'STUB-PAYLOADPACK staged' | sed 's/.* digest //' | tail -1)"
	[ -n "$PD" ] && [ "$PD" = "$SD" ] && result OK payload-staged || fail payload-staged

	wait_for A "AUTOINSTALL-DONE" 3600 && result OK install || { fail install; return 1; }
	KA="$(tr -d '\r' < "$(log_of A)" | grep -aEo '([0-9a-f]{8} ){7}[0-9a-f]{8}' | head -1)"
	KB="$(tr -d '\r' < "$(log_of B)" | grep -aEo '([0-9a-f]{8} ){7}[0-9a-f]{8}' | head -1)"
	[ "$(tr -d '\r' < "$(log_of A)" | grep -aEc '([0-9a-f]{8} ){7}[0-9a-f]{8}')" -eq 1 ] && result OK key-shown-once-on-operator || fail key-shown-once-on-operator
	[ -n "$KA" ] && [ "$KA" = "$KB" ] && result OK recovery-key-on-both || fail recovery-key-on-both
	RECOVERY="$(printf '%s' "$KA" | tr -d ' ')"
	wait_for A "Reboot river-install-" 60 || return 1
	type_line A y
	if wait_exit B 180; then result OK target-rebooted; else fail target-rebooted; fi
	# The console prints this only after the session's teardown has finished.
	tr -d '\r' < "$(log_of B)" | grep -aq "Rebooting into the installed system" && result OK target-teardown || fail target-teardown
	nmore=$(count A "Install another machine on this network")
	wait_n A "Install another machine on this network" 2 60 && type_line A ""
	wait_for A "RIVERTEST END operator" 60 || true
	tr -d '\r' < "$(log_of A)" | grep -aq "river-pair exited 0" && result OK operator-exit || fail operator-exit
	ADMINKEY="$(field A 'adminkey')"
	return 0
}

# wrong_code DATA8 — a well-formed code (valid check character) that is not DATA8.
wrong_code() {
	alpha=0123456789ABCDEFGHJKMNPQRSTVWXYZ
	first="$(printf '%s' "$1" | cut -c1)"
	for c in 2 3 4; do
		[ "$c" = "$first" ] && continue
		data="$c$(printf '%s' "$1" | cut -c2-7)"
		h="$(printf 'river-pair/v1 check\n%s' "$data" | sha256sum | cut -c1-2)"
		idx=$(( 0x$h & 31 ))
		printf '%s%s\n' "$data" "$(printf '%s' "$alpha" | cut -c$((idx + 1)))"
		return
	done
}

CODE="" RECOVERY="" ADMINKEY=""
phase1 || fail phase1
shot A end; shot B end
mon A quit; mon B quit
sleep 3

# --- phase 2 + 3: B's installed system ---------------------------------------------------------
if [ -n "$RECOVERY" ] && [ "$FAILED" -eq 0 ]; then
	echo "qemu-lan-test: phase 2 — B live again: wire the harness onto its scratch disk"
	kit_add B recovery-key "$RECOVERY"
	kit_add B adminkey "$ADMINKEY"
	sed -i 's/^ROLE=.*/ROLE=wire/' "$WORK/B/kit/config"; mcopy -o -i "$WORK/B/kit.img" "$WORK/B/kit/config" ::/
	# shellcheck disable=SC2086
	start_vm B wire $TARGET_DISK $MEDIUM
	boot_kit B
	wait_for B "RIVERTEST END wire" 600 || true
	wait_exit B 120 || true
	for c in unlock-live harness-wired export; do
		tr -d '\r' < "$(log_of B)" | grep -aq "RIVERTEST OK $c\$" || fail "wire-$c"
	done
	echo "qemu-lan-test: phase 3 — B boots the installed Runink River from its disk"
	# shellcheck disable=SC2086
	start_vm B installed $TARGET_DISK
	if wait_for B "passphrase" 300; then
		sleep 2
		timeout 5 sh -c 'printf "%s\n" "$1" > "$2"' _ "$RECOVERY" "$WORK/B/ser.in" || true
	fi
	wait_for B "RIVERTEST END installed" 900 || shot B installed-end
	wait_exit B 120 || true
	for c in zfs-root unlocked hostname admin-key no-pair-user no-pair-service golden; do
		if tr -d '\r' < "$(log_of B)" | grep -aq "RIVERTEST OK $c\$"; then result OK "installed-$c"; else fail "installed-$c"; fi
	done
fi

echo
echo "qemu-lan-test: results ($WORK)"
cat "$WORK/results.txt"
if [ "$FAILED" -eq 0 ]; then
	echo "qemu-lan-test: PASS"
	[ "$KEEP" -eq 1 ] || rm -f "$WORK/B/target.qcow2"
	exit 0
fi
echo "qemu-lan-test: FAIL — serial logs and screenshots kept in $WORK" >&2
exit 1
