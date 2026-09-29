#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# qemu-test.sh — boot a built ISO under QEMU/KVM with UEFI (OVMF) and check it, unattended,
# as the calling user (no root; needs a writable /dev/kvm). The default is Runink River
# (--profile workstation); --profile server checks a downstream server distribution's ISO
# (this repository builds none since Runink River became the workstation).
#
#   build/qemu-test.sh [options] ISO
#
# --profile server, without --lab (non-destructive to nothing but a scratch disk):
#   grub      a screenshot of the GRUB menu, and /proc/cmdline of the booted live system
#             carries the ISO's own label (the server live entry booted, not another image's)
#   guide     river-guide runs on tty1
#   hwprobe   river-hwprobe produces an inventory; river-plan makes a plan (--lab waivers)
#             whose --list-disks names the scratch disk by its serial
#   payload   the medium carries /river-models (when --expect-models)
#   net-*     network first: river-netsetup --auto records a state. With --net none (the
#             default: no NIC) it must say offline within 15 s (net-offline); with --net user
#             (a QEMU user-mode NIC) it must obtain an automatic address (net-address) and
#             find the default gateway answering (net-gateway); river-netsetup --net dhcp must
#             apply through the real nmcli and come up DUAL-STACK and online (net-apply; the
#             host running the test must be online), runink-node-ip6 must name the IPv6
#             address as the k0s node address (net-node-ip6), and --net dhcp,v6only must
#             leave no IPv4 address (net-v6only).
#             net-check-user: the unprivileged --check the guide runs prints a RESULT and
#             records nothing.
#   fw-*      the host firewall: runink-fw loads the base posture (SSH, default-drop
#             forward) (fw-base), and a LAN peer in a throwaway namespace cannot reach an
#             arbitrary destination through this host over IPv4 or IPv6, while it can with
#             the table removed (fw-forward-drop); on this LIVE medium the LAN-install pairing
#             ports are in the firewall's pair sets, and with a real target session
#             (river-pair-announce) a link-local peer reaches TCP 47654 while a global source
#             on 47654 and a link-local one on another port are dropped (fw-pair-live)
# With --lab, additionally:
#   install   runink-autoinstall --plan-file ... --yes-i-have-checked-serial=<scratch serial>
#             installs to the scratch qcow2 (and unpacks the model payload when
#             --models-passphrase-file is given)
#   unlock    the installed system reboots and its encrypted ZFS root is unlocked with the
#             recovery key the installer printed (fed over the serial console)
#   golden    tests/assert-golden.sh passes on the installed system
#   models    <pool>/models exists, is encrypted, and every file matches models.manifest
# With --offline (needs --lab), the guests get a NIC with NO route out (QEMU user networking,
# restrict=on, an IPv6 ULA prefix: an address, no egress) instead of no NIC, and the
# installed system must additionally prove it needs nothing from any network:
#   net-offline      the guest cannot resolve or reach anything outside
#   k0s-ready        the k0s node reports Ready
#   k0s-images       every image of k0s-images.lock is in containerd, imported and pinned
#   pods-running     every pod in the cluster is Running (or Succeeded), none waiting on an
#                    image (ErrImagePull, ImagePullBackOff, ErrImageNeverPull)
#   no-pulls         kubelet recorded no image pull at all (no "Pulling" event)
# Payload checks (docs/PAYLOADS.md):
#   --expect-payloads  (a private medium) medium-payloads: the medium carries downstream
#                    payloads; payloads (installed): <pool>/payloads is encrypted, root-only,
#                    holds exactly MANIFEST/LOCK/parts, and every payload of its INDEX passes
#                    river-payloadpack check (parts + LOCK against the MANIFEST);
#                    firstboot-hooks: every /usr/local/lib/runink/firstboot.d hook succeeded
#   --public         (a public medium) public-medium: no /river-* payload on the medium;
#                    public-node: no <pool>/payloads, no <pool>/models on the node
#
# How it drives the guest without a network or SSH (the live image has password SSH off,
# and the default -nic none gives it no network at all): a small FAT image labelled RIVERTEST carries the check scripts.
# QEMU's monitor types one bootstrap line on the live system's second console (sendkey);
# the scripts write their results to the serial port, which this script reads. For the
# installed system, the live phase adds a one-line rc.local hook and a serial console to
# the SCRATCH disk only. Nothing touches a host block device.
#
# --profile workstation (the default) boots a Runink River ISO and checks its live desktop (no
# install phase here: build/qemu-gui-test.sh drives the graphical install and the login):
#   grub-entry  as above
#   sddm        SDDM runs; autologin  the live user is logged in through SDDM's drop-in
#   plasma      the live Plasma session runs (plasmashell + kwin_wayland as the live user);
#               screenshots desktop-early.png and desktop.png show it
#   s6-*        s6-rc has NetworkManager-srv, sddm-srv, bluetoothd-srv and cupsd up
#   no-artix-live  no artix-live service or dependency on the medium
#   net-*       network first, as for the server; net-autostart  the live Plasma session opened
#               the graphical installer (older images: river-netsetup in Konsole)
#
# Options:
#   --profile workstation|server  which image the ISO is (default workstation)
#   --lab                       run the destructive install phases on the scratch disk
#   --models-passphrase-file F  unpack the model payload during the install (with --lab)
#   --expect-models             fail if the medium carries no model payload
#   --offline                   NIC without a route out + the offline checks above (with --lab)
#   --expect-payloads           the private-medium checks above
#   --setup-answers F           pass F to runink-autoinstall --setup-answers (with --lab): the
#                               downstream first-boot hooks get it; check them with --expect-payloads
#   --public                    the public-medium checks above (excludes the two options above
#                               that expect payloads, and --models-passphrase-file)
#   --disk-size SIZE            scratch qcow2 size (default 80G)
#   --mem MiB  --cpus N         guest size (default 8192 MiB, 6144 for a workstation; 4 CPUs)
#   --medium usb|cdrom          how the ISO is attached (default usb, as on a real stick)
#   --net none|user             the guest NIC: none (default; the harness needs no network;
#                               --offline replaces it with its own restricted NIC)
#                               or a QEMU user-mode NIC (NAT to the host: DHCPv4 10.0.2.15,
#                               SLAAC fd52:4956::/64, gateway 10.0.2.2 / fe80::2). The IPv6
#                               prefix is a ULA, not QEMU's default fec0::/64: the kernel
#                               gives fec0:: (deprecated site-local) scope "site", so it is
#                               never a global address and cannot stand in for a real one.
#   --target-bus virtio|scsi    how the scratch disk is attached: virtio-blk (default; the
#                               serial is /sys/block/vda/serial, as on cloud servers) or a
#                               scsi-hd on virtio-scsi (the serial is VPD page 0x80)
#   --kit-overlay DIR           test fixed files against an EXISTING ISO: DIR/root/ is copied
#                               over the live system (binaries under */bin/ made executable)
#                               and DIR/pre.sh, if present, runs as root, both before the
#                               checks. Every overlaid file is listed on the serial log.
#   --work DIR                  work directory (default ~/.cache/river-build/qemu-test/<time>)
#   --keep                      keep the work directory (default: keep only on failure)
#   --dry-run                   check the arguments and print what would run; boot nothing
#
# Prerequisites: qemu-system-x86_64, qemu-img, OVMF (edk2-ovmf: OVMF_CODE.4m.fd and
# OVMF_VARS.4m.fd; set OVMF_CODE / OVMF_VARS to override), mkfs.vfat + mcopy (dosfstools,
# mtools).
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/.." && pwd)"
CACHE="${XDG_CACHE_HOME:-$HOME/.cache}"

LAB=0 PASSF="" EXPECT_MODELS=0 DISK_SIZE=80G MEM="" CPUS=4 MEDIUM=usb WORK="" KEEP=0 DRY=0 ISO=""
BUS=virtio OVERLAY="" PROFILE=workstation OFFLINE=0 EXPECT_PAYLOADS=0 PUBLIC=0 ANSWERS="" NET=none
SERIAL_ID="RIVERQEMU0001"
POOL=zriver

die() { echo "qemu-test: $*" >&2; exit 2; }
usage() { sed -n '5,/^set -eu/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'; exit 2; }

while [ $# -gt 0 ]; do
	case "$1" in
		--profile) PROFILE="${2:?}"; shift ;;
		--profile=*) PROFILE="${1#*=}" ;;
		--lab) LAB=1 ;;
		--models-passphrase-file) PASSF="${2:?}"; shift ;;
		--models-passphrase-file=*) PASSF="${1#*=}" ;;
		--expect-models) EXPECT_MODELS=1 ;;
		--offline) OFFLINE=1 ;;
		--expect-payloads) EXPECT_PAYLOADS=1 ;;
		--public) PUBLIC=1 ;;
		--setup-answers) ANSWERS="${2:?}"; shift ;;
		--disk-size) DISK_SIZE="${2:?}"; shift ;;
		--mem) MEM="${2:?}"; shift ;;
		--cpus) CPUS="${2:?}"; shift ;;
		--medium) MEDIUM="${2:?}"; shift ;;
		--net) NET="${2:?}"; shift ;;
		--net=*) NET="${1#*=}" ;;
		--target-bus) BUS="${2:?}"; shift ;;
		--kit-overlay) OVERLAY="${2:?}"; shift ;;
		--work) WORK="${2:?}"; shift ;;
		--keep) KEEP=1 ;;
		--dry-run) DRY=1 ;;
		-h|--help) usage ;;
		-*) die "unknown option $1 (see --help)" ;;
		*) [ -z "$ISO" ] || die "one ISO only"; ISO="$1" ;;
	esac
	shift
done
[ -n "$ISO" ] || usage
[ -f "$ISO" ] || die "no ISO at $ISO"
case "$DISK_SIZE" in *[!0-9GM]*|'') die "--disk-size like 80G" ;; esac
case "$MEDIUM" in usb|cdrom) ;; *) die "--medium usb or cdrom" ;; esac
case "$BUS" in virtio|scsi) ;; *) die "--target-bus virtio or scsi" ;; esac
# --offline: a NIC with an address and no egress (slirp restrict=on: nothing is routed to the
# host or beyond), IPv6-only, so k0s finds a node address as on a real air-gapped LAN. It is
# its own NIC, so it replaces --net (the live network checks then only require a state).
if [ "$OFFLINE" -eq 1 ]; then
	[ "$NET" = none ] || die "--offline brings its own NIC (no route out); drop --net $NET"
	NET=restricted
fi
case "$NET" in
	none) NIC="-nic none" ;;
	user) NIC="-nic user,model=virtio-net-pci,ipv6-net=fd52:4956::/64" ;;
	restricted) NIC="-nic user,model=virtio-net-pci,restrict=on,ipv4=off,ipv6=on,ipv6-prefix=fd00:5eed:0:1::,ipv6-prefixlen=64" ;;
	*) die "--net none or user" ;;
esac
case "$PROFILE" in
	server) : "${MEM:=8192}" ;;
	# The install phases (--lab) drive the server installer's plan and golden checks; the
	# workstation install is not in this harness yet.
	workstation) : "${MEM:=6144}"; [ "$LAB" -eq 0 ] || die "--lab is server-only for now (the workstation mode checks the live desktop)" ;;
	*) die "--profile server or workstation" ;;
esac
case "$MEM$CPUS" in *[!0-9]*|'') die "--mem and --cpus are numbers" ;; esac
if [ -n "$OVERLAY" ]; then
	[ -d "$OVERLAY/root" ] || [ -f "$OVERLAY/pre.sh" ] || die "--kit-overlay $OVERLAY has neither root/ nor pre.sh"
fi
if [ "$OFFLINE" -eq 1 ] && [ "$LAB" -eq 0 ]; then die "--offline needs --lab (it checks the installed system)"; fi
if [ -n "$ANSWERS" ]; then
	[ "$LAB" -eq 1 ] || die "--setup-answers needs --lab"
	[ -r "$ANSWERS" ] || die "cannot read $ANSWERS"
fi
if [ "$PUBLIC" -eq 1 ]; then
	[ "$EXPECT_PAYLOADS" -eq 0 ] && [ "$EXPECT_MODELS" -eq 0 ] && [ -z "$PASSF" ] \
		|| die "--public excludes --expect-payloads, --expect-models and --models-passphrase-file"
fi
[ "$PROFILE" = server ] || [ "$OFFLINE$EXPECT_PAYLOADS$PUBLIC" = 000 ] || die "--offline/--expect-payloads/--public are server checks"
if [ -n "$PASSF" ]; then
	[ "$LAB" -eq 1 ] || die "--models-passphrase-file needs --lab"
	[ -r "$PASSF" ] || die "cannot read $PASSF"
fi
OVMF_CODE="${OVMF_CODE:-/usr/share/edk2/x64/OVMF_CODE.4m.fd}"
OVMF_VARS="${OVMF_VARS:-/usr/share/edk2/x64/OVMF_VARS.4m.fd}"
missing=""
for t in qemu-system-x86_64 qemu-img mkfs.vfat mcopy blkid; do command -v "$t" >/dev/null 2>&1 || missing="$missing $t"; done
[ -f "$OVMF_CODE" ] && [ -f "$OVMF_VARS" ] || missing="$missing OVMF(edk2-ovmf)"
[ -w /dev/kvm ] || missing="$missing /dev/kvm(writable)"
[ -z "$missing" ] || die "missing prerequisites:$missing"
LABEL="$(blkid -s LABEL -o value "$ISO" 2>/dev/null || true)"
[ -n "$LABEL" ] || die "cannot read the ISO volume label of $ISO"
[ -n "$WORK" ] || WORK="$CACHE/river-build/qemu-test/$(date +%Y%m%d-%H%M%S)"
case "$WORK" in /tmp/*) echo "qemu-test: WARNING: $WORK is probably tmpfs; the scratch disk grows to ~25 GB" >&2 ;; esac

if [ "$MEDIUM" = usb ]; then
	MEDIUM_ARGS="-device qemu-xhci,id=xhci -drive if=none,id=medium,format=raw,readonly=on,file=$ISO -device usb-storage,bus=xhci.0,drive=medium,bootindex=0"
else
	MEDIUM_ARGS="-drive if=none,id=medium,format=raw,readonly=on,media=cdrom,file=$ISO -device ide-cd,drive=medium,bootindex=0"
fi
# The scratch disk carries the serial the operator confirms. virtio-blk keeps it on the disk
# (/sys/block/vda/serial, udev ID_SERIAL); a scsi-hd reports it in VPD page 0x80 (udev
# ID_SERIAL_SHORT). river-hwprobe reads both; --target-bus picks which one is tested.
if [ "$BUS" = virtio ]; then
	TARGET_DEV="-device virtio-blk-pci,drive=target,serial=$SERIAL_ID,bootindex=1"
else
	TARGET_DEV="-device virtio-scsi-pci,id=scsi0 -device scsi-hd,drive=target,bus=scsi0.0,serial=$SERIAL_ID,bootindex=1"
fi
COMMON="-enable-kvm -machine q35 -cpu host -m $MEM -smp $CPUS $NIC -display none
 -drive if=pflash,format=raw,readonly=on,file=$OVMF_CODE -drive if=pflash,format=raw,file=$WORK/vars.fd
 -drive if=none,id=target,format=qcow2,file=$WORK/target.qcow2 $TARGET_DEV
 -drive if=none,id=kit,format=raw,file=$WORK/kit.img -device virtio-blk-pci,drive=kit,serial=RIVERTESTKIT
 -chardev pipe,id=ser0,path=$WORK/ser -serial chardev:ser0
 -chardev pipe,id=mon0,path=$WORK/mon -mon chardev=mon0,mode=readline"
# The workstation draws a desktop: a virtio GPU (KMS for kwin_wayland; screendump reads it).
[ "$PROFILE" = workstation ] && COMMON="$COMMON -vga virtio"

echo "qemu-test: ISO $ISO (label $LABEL), profile=$PROFILE, lab=$LAB, unpack models=$([ -n "$PASSF" ] && echo yes || echo no), net=$NET, offline=$OFFLINE, expect-payloads=$EXPECT_PAYLOADS, public=$PUBLIC"
echo "qemu-test: work dir $WORK; scratch disk $DISK_SIZE qcow2 on $BUS, serial $SERIAL_ID, net $NET${OVERLAY:+; kit overlay $OVERLAY}"
if [ "$DRY" -eq 1 ]; then
	echo "qemu-test: DRY RUN. Would run:"
	echo "  phase 1 (live):      qemu-system-x86_64 $(echo "$COMMON" | tr '\n' ' ') $MEDIUM_ARGS"
	[ "$LAB" -eq 1 ] && echo "  phase 2 (installed): qemu-system-x86_64 $(echo "$COMMON" | tr '\n' ' ')"
	exit 0
fi

mkdir -p "$WORK"
QPID="" CATS=""
cleanup() {
	[ -n "$QPID" ] && kill "$QPID" 2>/dev/null || true
	for p in $CATS; do kill "$p" 2>/dev/null || true; done
	rm -f "$WORK/kit/models-passphrase" "$WORK/kit/setup-answers" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

# --- scratch disk, firmware vars, kit --------------------------------------------------
qemu-img create -q -f qcow2 "$WORK/target.qcow2" "$DISK_SIZE"
cp "$OVMF_VARS" "$WORK/vars.fd"
mkdir -p "$WORK/kit"
cat > "$WORK/kit/config" <<EOF
LAB=$LAB
EXPECT_LABEL=$LABEL
TARGET_SERIAL=$SERIAL_ID
POOL=$POOL
EXPECT_MODELS=$EXPECT_MODELS
UNPACK_MODELS=$([ -n "$PASSF" ] && echo 1 || echo 0)
NET=$NET
OFFLINE=$OFFLINE
EXPECT_PAYLOADS=$EXPECT_PAYLOADS
PUBLIC=$PUBLIC
EOF
cp "$REPO/tests/assert-golden.sh" "$WORK/kit/assert-golden.sh"
[ -n "$PASSF" ] && ( umask 077; head -n 1 "$PASSF" > "$WORK/kit/models-passphrase" )
[ -n "$ANSWERS" ] && ( umask 077; cp "$ANSWERS" "$WORK/kit/setup-answers" )
if [ -n "$OVERLAY" ]; then
	mkdir -p "$WORK/kit/overlay"
	[ -d "$OVERLAY/root" ] && cp -r "$OVERLAY/root" "$WORK/kit/overlay/root"
	[ -f "$OVERLAY/pre.sh" ] && cp "$OVERLAY/pre.sh" "$WORK/kit/overlay/pre.sh"
fi

# Network first (both profiles; sourced by live.sh after any --kit-overlay is applied).
cat > "$WORK/kit/net.sh" <<'EOF'
# river-netsetup --auto is what the live session runs first. It must record a state and never
# hang: with no NIC (--net none) it says offline at once; on a QEMU user-mode NIC (--net user)
# it finds an automatic address (DHCPv4 10.0.2.15 and/or SLAAC fd52:4956::/64) and a default
# gateway that answers (10.0.2.2 or fe80::2). The host is DUAL-STACK by default (only the
# k0s cluster network is IPv6-only): river-netsetup --net dhcp must give IPv4 + IPv6, both
# gateways answering and the internet reachable (net-apply; this needs the host running
# the test to be online), and --net dhcp,v6only must give no IPv4 at all (net-v6only).
if command -v river-netsetup >/dev/null 2>&1; then
	t0="$(date +%s)"
	nrc=0
	timeout 150 river-netsetup --auto --wait 30 > /run/rt-net.txt 2>&1 || nrc=$?
	el=$(($(date +%s) - t0))
	sed 's/^/net: /' /run/rt-net.txt
	echo "net: river-netsetup --auto exit $nrc after ${el}s"
	RIVER_NET_STATE=""
	[ -r /run/river/net.env ] && . /run/river/net.env
	if [ "$nrc" -eq 0 ] && [ -s /run/river/net-state.json ] && [ -n "$RIVER_NET_STATE" ]; then ok net-setup
	else ko net-setup "exit $nrc, state ${RIVER_NET_STATE:-none}"; fi
	if [ "$NET" = none ]; then
		if [ "$RIVER_NET_STATE" = offline ] && [ "$el" -le 15 ]; then ok net-offline
		else ko net-offline "state ${RIVER_NET_STATE:-none} after ${el}s"; fi
	elif [ "$NET" = restricted ]; then
		# --offline: an address and no route out. Any recorded state is valid; the installed
		# system proves it needs no network (net-offline, k0s-ready, ... after the install).
		echo "net: --offline NIC (no egress): state $RIVER_NET_STATE"
	else
		grep -Eq '^  link .* (10\.0\.2\.[0-9]+/24|fd52:4956:[0-9a-f:]+/64)' /run/rt-net.txt && ok net-address || ko net-address
		grep -Eq '^    gateway[46] +OK ' /run/rt-net.txt && ok net-gateway || ko net-gateway
		echo "net: state $RIVER_NET_STATE"
		# The apply path with the real nmcli: what river.net=dhcp does (a river-<iface> connection,
		# dual-stack), and the network answers over both families after it.
		arc=0
		timeout 150 river-netsetup --net dhcp --wait 30 > /run/rt-net2.txt 2>&1 || arc=$?
		sed 's/^/net-apply: /' /run/rt-net2.txt
		if [ "$arc" -eq 0 ] && nmcli -t -f NAME connection show --active 2>/dev/null | grep -q '^river-' \
			&& grep -Eq '^  link .* 10\.0\.2\.[0-9]+/24 .*fd52:4956:' /run/rt-net2.txt \
			&& grep -Eq '^    gateway4 +OK ' /run/rt-net2.txt && grep -Eq '^    gateway6 +OK ' /run/rt-net2.txt \
			&& grep -q '^RESULT: online' /run/rt-net2.txt; then ok net-apply
		else ko net-apply "exit $arc, active: $(nmcli -t -f NAME connection show --active 2>/dev/null | tr '\n' ' ')"; fi
		# The k0s node address on this dual-stack host: IPv6 (the SLAAC fd52:4956:: one), never the IPv4.
		n6="$(runink-node-ip6 2>/dev/null)"; echo "net: runink-node-ip6 -> ${n6:-none}"
		if runink-node-ip6 --has-ipv4 && case "$n6" in fd52:4956:*) true ;; *) false ;; esac; then ok net-node-ip6
		else ko net-node-ip6 "got ${n6:-none}; $(ip -6 -o addr show scope global | awk '{ print $2, $4, $NF }' | tr '\n' ';')"; fi
		# IPv6-only sites: river.net=dhcp,v6only leaves no IPv4 address on the link.
		vrc=0
		timeout 150 river-netsetup --net dhcp,v6only --wait 30 > /run/rt-net3.txt 2>&1 || vrc=$?
		sed 's/^/net-v6only: /' /run/rt-net3.txt
		sleep 3
		# From the check's own link report: eth0 is listed, carries the ULA, and has no 10.0.2.x.
		if [ "$vrc" -eq 0 ] && grep -Eq '^  link +eth0 .*fd52:4956:' /run/rt-net3.txt \
			&& ! grep -Eq '^  link +eth0 .*10\.0\.2\.' /run/rt-net3.txt \
			&& grep -Eq '^    gateway6 +OK ' /run/rt-net3.txt; then ok net-v6only
		else ko net-v6only "exit $vrc, v4: $(ip -4 -o addr show scope global | awk '{ print $4 }' | tr '\n' ' ')"; fi
		# Back to the default (dual-stack) for whatever runs next.
		timeout 150 river-netsetup --net dhcp --wait 30 >/dev/null 2>&1 || true
	fi
	# The host firewall (server): the base posture is loaded, and the host routes NOTHING for a
	# LAN peer. Two throwaway namespaces: rt-peer (a LAN host, 192.0.2.2 / 2001:db8:1::2) whose
	# gateway is this host, and rt-far (an arbitrary destination, 198.51.100.2 / 2001:db8:2::2)
	# behind it. With runink_fw loaded, peer -> far must fail for both families; with the table
	# removed it must work (so the drop is the firewall's, not a broken test rig).
	if [ -x /usr/local/bin/runink-fw ]; then
		/usr/local/bin/runink-fw apply
		if nft list chain inet runink_fw input 2>/dev/null | grep -q 'tcp dport 22 accept' \
			&& nft list chain inet runink_fw forward 2>/dev/null | grep -q 'policy drop'; then ok fw-base
		else ko fw-base "$(nft list table inet runink_fw 2>&1 | head -3 | tr '\n' ' ')"; fi
		for n in rt-peer rt-far; do ip netns add $n; done
		ip link add rt-p type veth peer name eth0 netns rt-peer
		ip link add rt-f type veth peer name eth0 netns rt-far
		ip addr add 192.0.2.1/24 dev rt-p; ip addr add 2001:db8:1::1/64 dev rt-p nodad
		ip addr add 198.51.100.1/24 dev rt-f; ip addr add 2001:db8:2::1/64 dev rt-f nodad
		ip link set rt-p up; ip link set rt-f up
		ip -n rt-peer addr add 192.0.2.2/24 dev eth0; ip -n rt-peer addr add 2001:db8:1::2/64 dev eth0 nodad
		ip -n rt-far addr add 198.51.100.2/24 dev eth0; ip -n rt-far addr add 2001:db8:2::2/64 dev eth0 nodad
		for n in rt-peer rt-far; do ip -n $n link set eth0 up; ip -n $n link set lo up; done
		ip -n rt-peer route add default via 192.0.2.1; ip -n rt-peer -6 route add default via 2001:db8:1::1
		ip -n rt-far route add default via 198.51.100.1; ip -n rt-far -6 route add default via 2001:db8:2::1
		sysctl -qw net.ipv4.ip_forward=1 net.ipv6.conf.all.forwarding=1
		reach() { ip netns exec rt-peer ping -c 2 -W 2 "$1" >/dev/null 2>&1; }
		v4=0; v6=0; reach 198.51.100.2 && v4=1; reach 2001:db8:2::2 && v6=1
		nft delete table inet runink_fw
		c4=0; c6=0; reach 198.51.100.2 && c4=1; reach 2001:db8:2::2 && c6=1
		/usr/local/bin/runink-fw apply >/dev/null
		echo "fw: forwarded with runink_fw: v4=$v4 v6=$v6; without it (control): v4=$c4 v6=$c6"
		if [ "$v4$v6" = 00 ] && [ "$c4$c6" = 11 ]; then ok fw-forward-drop
		else ko fw-forward-drop "with v4=$v4 v6=$v6, control v4=$c4 v6=$c6"; fi
		# LAN-install pairing on the LIVE medium: runink-fw put the pairing ports (fw-pair) in
		# pair_tcp/pair_udp, and with a real target session (river-pair-announce, on this host's
		# side of the peer link) the peer reaches the pairing endpoint, TCP 47654, from its
		# link-local address through the default-deny input chain. Controls: the same port from
		# the peer's GLOBAL address, and a non-pairing port from its link-local one, are both
		# DROPPED (a timeout; accepted, the kernel would refuse them at once: nothing listens
		# there). When the session ends, nothing listens on 47654 any more.
		why=""
		pt="$(nft -n list set inet runink_fw pair_tcp 2>/dev/null | tr -s ' \t\n' ' ')"
		pu="$(nft -n list set inet runink_fw pair_udp 2>/dev/null | tr -s ' \t\n' ' ')"
		echo "fw: pair_tcp: $pt"; echo "fw: pair_udp: $pu"
		case "$pt" in *47654*) ;; *) why="47654 not in pair_tcp;" ;; esac
		case "$pt" in *47655*) ;; *) why="$why 47655 not in pair_tcp;" ;; esac
		case "$pu" in *47653*) ;; *) why="$why 47653 not in pair_udp;" ;; esac
		if ! command -v river-pair-announce >/dev/null 2>&1 || ! command -v bash >/dev/null 2>&1; then
			why="$why no river-pair-announce or bash on this image;"
		else
			i=0
			until ip -6 addr show dev rt-p scope link | grep -v tentative | grep -q 'inet6 fe80'; do
				sleep 1; i=$((i + 1)); [ "$i" -lt 15 ] || break
			done
			hll="$(ip -6 -o addr show dev rt-p scope link | awk '{ sub(/\/.*/, "", $4); print $4; exit }')"
			river-pair-announce --iface rt-p </dev/null > /run/rt-pair.txt 2>&1 &
			apid=$!
			i=0
			until ss -Hltn 'sport = :47654' 2>/dev/null | grep -q .; do
				sleep 1; i=$((i + 1)); [ "$i" -lt 30 ] || break
			done
			ss -Hltn 'sport = :47654' 2>/dev/null | sed 's/^/fw: listening: /'
			tcp() { ip netns exec rt-peer timeout 6 bash -c "exec 3<>/dev/tcp/$1/$2" 2>/dev/null; }
			lrc=0; tcp "$hll%eth0" 47654 || lrc=$?
			grc=0; tcp 2001:db8:1::1 47654 || grc=$?
			nrc=0; tcp "$hll%eth0" 47656 || nrc=$?
			echo "fw: peer -> [$hll]:47654 rc=$lrc (want 0), [2001:db8:1::1]:47654 rc=$grc (want 124, dropped), [$hll]:47656 rc=$nrc (want 124, dropped)"
			[ "$lrc" -eq 0 ] || why="$why link-local peer cannot reach 47654 (rc $lrc);"
			[ "$grc" -eq 124 ] || why="$why a global source was not dropped on 47654 (rc $grc);"
			[ "$nrc" -eq 124 ] || why="$why a non-pairing port was not dropped (rc $nrc);"
			kill "$apid" 2>/dev/null; wait "$apid" 2>/dev/null
			i=0
			while ss -Hltn 'sport = :47654' 2>/dev/null | grep -q .; do
				sleep 1; i=$((i + 1)); [ "$i" -lt 20 ] || { why="$why 47654 still listening after the session ended;"; break; }
			done
			sed 's/^/fw: pair-target: /' /run/rt-pair.txt | grep -v 'PAIRING CODE'
		fi
		[ -z "$why" ] && ok fw-pair-live || ko fw-pair-live "$why"
		ip link del rt-p 2>/dev/null; ip link del rt-f 2>/dev/null
		for n in rt-peer rt-far; do ip netns del $n 2>/dev/null; done
	else
		echo "RIVERTEST SKIP fw (no runink-fw on this image; the server's want list still requires fw-base)"
	fi
	# The guide's first step runs the check unprivileged: a RESULT, and nothing recorded.
	before="$(cksum < /run/river/net-state.json 2>/dev/null)"
	if s6-setuidgid runink river-netsetup --check > /run/rt-netcheck.txt 2>&1 && grep -q '^RESULT: ' /run/rt-netcheck.txt \
		&& [ "$before" = "$(cksum < /run/river/net-state.json 2>/dev/null)" ]; then ok net-check-user
	else ko net-check-user "$(tail -1 /run/rt-netcheck.txt)"; fi
else
	ko net-setup "river-netsetup is not on this image"
fi
EOF

cat > "$WORK/kit/live.sh" <<'EOF'
#!/bin/sh
# Runs as root on the LIVE system; results go to the serial port.
exec >/dev/ttyS0 2>&1
set -u
export PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin
. /run/rt/config
ok() { echo "RIVERTEST OK $1"; }
ko() { echo "RIVERTEST FAIL $1${2:+ ($2)}"; }
echo "RIVERTEST BEGIN live"
# --kit-overlay: fixed files tested against this (existing) ISO. Listed, so a result is never
# mistaken for the ISO's own.
if [ -d /run/rt/overlay/root ]; then
	( cd /run/rt/overlay/root && find . -type f ) | while read -r f; do
		f="${f#./}"
		mkdir -p "/$(dirname "$f")" && cp "/run/rt/overlay/root/$f" "/$f"
		case "$f" in */bin/*|*/sv/*/run) chmod 0755 "/$f" ;; esac
		echo "RIVERTEST NOTE overlay /$f"
	done
fi
[ -f /run/rt/overlay/pre.sh ] && { echo "RIVERTEST NOTE overlay pre.sh"; sh /run/rt/overlay/pre.sh; }
. /run/rt/net.sh
grep -q "label=$EXPECT_LABEL" /proc/cmdline && ok grub-entry || ko grub-entry "$(cat /proc/cmdline)"
# river-guide runs on tty3 since the graphical installer took tty1 (tty1 on older images).
ps -eo tty=,args= | awk '$1 == "tty1" || $1 == "tty3"' | grep -q river-guide && ok guide-tty1 || ko guide-tty1
river-hwprobe --json > /run/rt-probe.json && ok hwprobe || ko hwprobe
rc=0
river-plan --probe /run/rt-probe.json --manifest /usr/local/share/runink/models.tiers --lab --json > /run/rt-plan.json || rc=$?
{ [ "$rc" -eq 0 ] || [ "$rc" -eq 3 ]; } && ok plan || ko plan "exit $rc"
river-plan --plan-file /run/rt-plan.json || true
# The disk the plan will wipe under the harness's serial (tab-separated: name, confirm_id, ...).
tdisk="$(river-plan --plan-file /run/rt-plan.json --list-disks | awk -F '\t' -v s="$TARGET_SERIAL" '$2 == s { print $1; exit }')"
[ -n "$tdisk" ] && ok plan-target || ko plan-target
P=""
for d in /run/initramfs/live /run/archiso/bootmnt /run/artix/bootmnt /run/miso/bootmnt /bootmnt; do
	[ -f "$d/river-models/MANIFEST" ] && P="$d/river-models"
done
if [ -n "$P" ]; then ok payload; head -6 "$P/MANIFEST"
elif [ "$EXPECT_MODELS" = 1 ]; then ko payload "no river-models on the medium"
else echo "RIVERTEST SKIP payload"; fi
# Downstream payloads on the medium (docs/PAYLOADS.md): /river-<kind>/<group>/MANIFEST.
M=""
for d in /run/initramfs/live /run/archiso/bootmnt /run/artix/bootmnt /run/miso/bootmnt /bootmnt \
	$(awk '$3 == "iso9660" { print $2 }' /proc/mounts); do
	[ -d "$d" ] && ls "$d"/river-*/MANIFEST "$d"/river-*/*/MANIFEST >/dev/null 2>&1 && { M="$d"; break; }
done
if [ "$EXPECT_PAYLOADS" = 1 ]; then
	n="$( [ -n "$M" ] && ls "$M"/river-*/*/MANIFEST 2>/dev/null | grep -vc "/river-models/" )"
	[ "${n:-0}" -gt 0 ] && ok medium-payloads || ko medium-payloads "no /river-<kind>/<group>/MANIFEST"
	[ -n "$M" ] && ls -d "$M"/river-*/* 2>/dev/null
fi
if [ "$PUBLIC" = 1 ]; then
	[ -z "$M" ] && ok public-medium || ko public-medium "payloads on the medium: $(ls -d "$M"/river-* 2>/dev/null | tr '\n' ' ')"
fi
if [ "$LAB" = 1 ]; then
	set -- --plan-file /run/rt-plan.json --yes-i-have-checked-serial="$TARGET_SERIAL"
	[ "$UNPACK_MODELS" = 1 ] && { cp /run/rt/models-passphrase /run/rt-pass; chmod 600 /run/rt-pass
		set -- "$@" --models-passphrase-file /run/rt-pass; }
	[ -f /run/rt/setup-answers ] && { cp /run/rt/setup-answers /run/rt-answers; chmod 600 /run/rt-answers
		set -- "$@" --setup-answers /run/rt-answers; }
	runink-autoinstall "$@" 2>&1 | tee /run/rt-install.log
	rm -f /run/rt-answers
	rm -f /run/rt-pass
	grep -q AUTOINSTALL-DONE /run/rt-install.log && ok install || ko install
	KEY="$(grep -Eo '([0-9a-f]{8} ){7}[0-9a-f]{8}' /run/rt-install.log | head -1 | tr -d ' ')"
	# Test-harness wiring on the SCRATCH disk only: an rc.local hook that runs installed.sh
	# from the kit, and the serial console on the installed kernel command line.
	if [ -n "$KEY" ] && zpool import -N -R /mnt "$POOL" && printf '%s' "$KEY" | zfs load-key "$POOL"; then
		be="$(zfs list -H -o name -r "$POOL/ROOT" | sed -n 2p)"
		zfs mount "$be" && zfs mount -a
		# Appended, so the checks start once rc.local has done its work (sysctl, zram, the
		# stack launch). At the top of the file they raced it and reported unapplied sysctls.
		echo '[ -e /dev/disk/by-label/RIVERTEST ] && ( mkdir -p /run/rt && mount -r -L RIVERTEST /run/rt && sh /run/rt/installed.sh ) >/dev/null 2>&1 &' >> /mnt/etc/s6/rc.local
		esp="$(lsblk -rno PATH,PARTTYPE "/dev/$tdisk" | awk 'tolower($2) == "c12a7328-f81f-11d2-ba4b-00a0c93ec93b" { print $1; exit }')"
		mkdir -p /run/rt-esp && mount "$esp" /run/rt-esp && \
			sed -i '/^[[:space:]]*linux[[:space:]]/ s/$/ console=tty0 console=ttyS0,115200/' /run/rt-esp/grub/grub.cfg && \
			grep -q 'console=ttyS0' /run/rt-esp/grub/grub.cfg && ok harness-wired || ko harness-wired "esp=$esp"
		umount /run/rt-esp || true
		zfs unmount -a || true
		zpool export "$POOL" && ok export || ko export
	else
		ko harness-wired "no key captured or import failed"
	fi
fi
echo "RIVERTEST END live"
sync
poweroff -f 2>/dev/null || poweroff
EOF

# --profile workstation: the live DESKTOP checks replace live.sh (no guide, no installer run).
if [ "$PROFILE" = workstation ]; then
cat > "$WORK/kit/live.sh" <<'EOF'
#!/bin/sh
# Runs as root on the LIVE Runink River medium; results go to the serial port.
exec >/dev/ttyS0 2>&1
set -u
export PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin
. /run/rt/config
ok() { echo "RIVERTEST OK $1"; }
ko() { echo "RIVERTEST FAIL $1${2:+ ($2)}"; }
echo "RIVERTEST BEGIN live"
if [ -d /run/rt/overlay/root ]; then
	( cd /run/rt/overlay/root && find . -type f ) | while read -r f; do
		f="${f#./}"
		mkdir -p "/$(dirname "$f")" && cp "/run/rt/overlay/root/$f" "/$f"
		case "$f" in */bin/*|*/sv/*/run) chmod 0755 "/$f" ;; esac
		echo "RIVERTEST NOTE overlay /$f"
	done
fi
[ -f /run/rt/overlay/pre.sh ] && { echo "RIVERTEST NOTE overlay pre.sh"; sh /run/rt/overlay/pre.sh; }
grep -q "label=$EXPECT_LABEL" /proc/cmdline && ok grub-entry || ko grub-entry "$(cat /proc/cmdline)"
# Plasma on software rendering can take a while after SDDM's autologin: give it 4 minutes.
i=0
until pgrep -u runink -x plasmashell >/dev/null 2>&1 || [ "$i" -ge 240 ]; do sleep 5; i=$((i + 5)); done
pgrep -x sddm >/dev/null && ok sddm || ko sddm
# Autologin: the live drop-in names the user, no greeter is waiting, and the session is up.
if grep -rqs '^User=runink' /etc/sddm.conf.d && ! pgrep -f sddm-greeter >/dev/null && pgrep -u runink -x plasmashell >/dev/null; then
	ok autologin
else ko autologin "greeter=$(pgrep -f sddm-greeter | head -1) conf=$(grep -rhs '^User=' /etc/sddm.conf.d | tr '\n' ' ')"; fi
if pgrep -u runink -x plasmashell >/dev/null && pgrep -u runink -x kwin_wayland >/dev/null; then ok plasma
else ko plasma "plasmashell=$(pgrep -u runink -x plasmashell | head -1) kwin_wayland=$(pgrep -u runink -x kwin_wayland | head -1)"; fi
active="$(s6-rc -a list 2>&1)"
for s in NetworkManager-srv sddm-srv bluetoothd-srv cupsd; do
	printf '%s\n' "$active" | grep -qx "$s" && ok "s6-${s%-srv}" || ko "s6-${s%-srv}" "not up"
done
if [ -d /etc/s6/sv/artix-live ] || find /etc/s6/sv /etc/s6/adminsv -path '*/dependencies.d/artix-live' | grep -q .; then
	ko no-artix-live
else ok no-artix-live; fi
# Network first, now through the graphical installer: the live session opened the installer
# (whose network screen comes first) through its autostart entry. Images from before the
# graphical installer opened river-netsetup in Konsole instead; either passes.
if { [ -f /etc/xdg/autostart/river-installer.desktop ] && pgrep -f 'firefox.*47660' >/dev/null; } \
	|| { [ -f /etc/xdg/autostart/river-netsetup.desktop ] && pgrep -f 'river-netsetup --desktop' >/dev/null; }; then ok net-autostart
else ko net-autostart "entries=$(ls /etc/xdg/autostart/river-installer.desktop /etc/xdg/autostart/river-netsetup.desktop 2>/dev/null | tr '\n' ' ')"; fi
. /run/rt/net.sh
echo "s6-rc active: $(printf '%s\n' "$active" | tr '\n' ' ')"
# Back to the desktop's virtual terminal for the harness's screenshot.
pid="$(pgrep -u runink -x kwin_wayland | head -1)"
vt="$( [ -n "$pid" ] && tr '\0' '\n' < "/proc/$pid/environ" | sed -n 's/^XDG_VTNR=//p' | head -1)"
if [ -n "$vt" ]; then chvt "$vt"; sleep 8; echo "RIVERTEST SHOT desktop"; sleep 15; fi
echo "RIVERTEST END live"
sync
poweroff -f 2>/dev/null || poweroff
EOF
fi

cat > "$WORK/kit/installed.sh" <<'EOF'
#!/bin/sh
# Runs as root on the INSTALLED system (from the rc.local hook); results go to the serial port.
exec >/dev/ttyS0 2>&1
set -u
# rc.local runs with s6's PATH, which has no /usr/local/bin (river-modelpack, river-perms).
export PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/usr/sbin:/bin:/sbin
. /run/rt/config
ok() { echo "RIVERTEST OK $1"; }
ko() { echo "RIVERTEST FAIL $1${2:+ ($2)}"; }
echo "RIVERTEST BEGIN installed"
src="$(findmnt -n -o SOURCE /)"
case "$src" in "$POOL"/ROOT/*) ok zfs-root ;; *) ko zfs-root "$src" ;; esac
[ "$(zfs get -H -o value keystatus "$POOL")" = available ] && ok unlocked || ko unlocked
echo "kernel $(uname -r)"
sh /run/rt/assert-golden.sh && ok golden || ko golden
if [ "$UNPACK_MODELS" = 1 ]; then
	if zfs list -H "$POOL/models" >/dev/null 2>&1 && [ "$(zfs get -H -o value encryption "$POOL/models")" != off ]; then
		mp="$(zfs get -H -o value mountpoint "$POOL/models")"
		( cd "$mp" && sha256sum --quiet -c /usr/local/share/runink/models.manifest ) \
			&& river-modelpack verify --dir "$mp" --lock /usr/local/share/runink/models.lock >/dev/null \
			&& ok models || ko models "files do not match"
		zfs list -o name,used,encryption,keystatus,mountpoint "$POOL/models"
	else
		ko models "no encrypted $POOL/models dataset"
	fi
fi
if [ "$PUBLIC" = 1 ]; then
	if zfs list -H "$POOL/payloads" >/dev/null 2>&1 || zfs list -H "$POOL/models" >/dev/null 2>&1 || [ -e /var/lib/runink/payloads/INDEX ]; then
		ko public-node "a payload dataset exists"
	else ok public-node; fi
fi
if [ "$EXPECT_PAYLOADS" = 1 ]; then
	P=/var/lib/runink/payloads
	why=""
	zfs list -H "$POOL/payloads" >/dev/null 2>&1 || why="no $POOL/payloads"
	[ -z "$why" ] && [ "$(zfs get -H -o value encryption "$POOL/payloads")" = off ] && why="$POOL/payloads is not encrypted"
	[ -z "$why" ] && [ "$(zfs get -H -o value mountpoint "$POOL/payloads")" != "$P" ] && why="mountpoint"
	[ -z "$why" ] && [ "$(stat -c '%a %U' "$P")" != "700 root" ] && why="$P is $(stat -c '%a %U' "$P")"
	[ -z "$why" ] && [ -s "$P/INDEX" ] || why="${why:-no INDEX}"
	# Only ciphertext and its plaintext descriptions: never a decrypted file.
	bad="$(find "$P" -mindepth 1 \( -type d ! -perm 0700 -o -type f ! -perm 0600 -o ! -type d ! -type f \) -print | head -3)"
	[ -z "$why" ] && [ -n "$bad" ] && why="loose modes: $bad"
	stray="$(find "$P" -mindepth 3 -type f ! -name MANIFEST ! -name LOCK ! -name 'payload.rmp.[0-9][0-9][0-9]' -print | head -3)"
	[ -z "$why" ] && [ -n "$stray" ] && why="not ciphertext: $stray"
	n=0
	if [ -z "$why" ]; then
		while read -r tag kind group lsha _rest; do
			[ "$tag" = payload ] || continue
			n=$((n + 1))
			d="$P/$kind/$group"
			[ "$(sha256sum "$d/LOCK" | cut -c1-64)" = "$lsha" ] || { why="$kind/$group LOCK does not match INDEX"; break; }
			river-payloadpack check --payload "$d" || { why="$kind/$group does not verify"; break; }
		done < "$P/INDEX"
		[ -n "$why" ] || [ "$n" -gt 0 ] || why="INDEX lists no payload"
	fi
	[ -z "$why" ] && ok payloads || ko payloads "$why"
	zfs list -o name,used,encryption,keystatus,mountpoint "$POOL/payloads" 2>/dev/null
	cat "$P/INDEX" 2>/dev/null
fi
if [ "$OFFLINE" = 1 ]; then
	K="k0s kubectl --kubeconfig /var/lib/k0s/pki/admin.conf"
	ip -6 addr show scope global; ip -6 route
	why=""
	getent ahosts quay.io >/dev/null 2>&1 && why="quay.io resolves"
	if [ -z "$why" ] && command -v bash >/dev/null 2>&1; then
		timeout 8 bash -c 'exec 3<>/dev/tcp/2606:4700:4700::1111/443' 2>/dev/null && why="reached 2606:4700:4700::1111:443"
	fi
	[ -z "$why" ] && ok net-offline || ko net-offline "$why"
	# k0s starts from rc.local; bring-up (bundle import first, then kubelet) takes minutes.
	i=0
	until $K get nodes --no-headers 2>/dev/null | awk '$2 == "Ready" { f=1 } END { exit !f }' || [ "$i" -ge 1200 ]; do
		sleep 10; i=$((i + 10))
	done
	$K get nodes -o wide 2>&1
	$K get nodes --no-headers 2>/dev/null | awk '$2 == "Ready" { f=1 } END { exit !f }' && ok k0s-ready || ko k0s-ready "not Ready after ${i}s"
	imgs="$(k0s ctr -n k8s.io images ls 2>/dev/null)"
	why=""
	for ref in $(grep -v '^#' /usr/local/share/runink/k0s-images.lock | awk 'NF { print $1 }'); do
		printf '%s\n' "$imgs" | awk -v r="$ref" '$1 == r && /io.cri-containerd.pinned=pinned/ { f=1 } END { exit !f }' \
			|| why="$why $ref"
	done
	[ -s /usr/local/share/runink/k0s-images.lock ] || why="no k0s-images.lock"
	[ -z "$why" ] && ok k0s-images || ko k0s-images "missing or unpinned:$why"
	# Every pod settles: Running (all containers ready) or Succeeded; none stuck on an image.
	i=0
	while [ "$i" -lt 900 ]; do
		pending="$($K get pods -A --no-headers 2>/dev/null | awk '$4 != "Running" && $4 != "Completed" { print $1 "/" $2 ":" $4 } $4 == "Running" { split($3, r, "/"); if (r[1] != r[2]) print $1 "/" $2 ":notready" }')"
		n="$($K get pods -A --no-headers 2>/dev/null | wc -l)"
		[ "$n" -gt 0 ] && [ -z "$pending" ] && break
		sleep 15; i=$((i + 15))
	done
	$K get pods -A -o wide 2>&1
	imgwait="$($K get pods -A -o jsonpath='{range .items[*]}{range .status.containerStatuses[*]}{.state.waiting.reason}{"\n"}{end}{range .status.initContainerStatuses[*]}{.state.waiting.reason}{"\n"}{end}{end}' 2>/dev/null | grep -E 'ErrImagePull|ImagePullBackOff|ErrImageNeverPull' | sort | uniq -c | tr '\n' ' ')"
	if [ "${n:-0}" -gt 0 ] && [ -z "$pending" ] && [ -z "$imgwait" ]; then ok pods-running
	else ko pods-running "pods=$n pending=[$(echo "$pending" | tr '\n' ' ')] image-waits=[$imgwait]"; fi
	pulls="$($K get events -A --no-headers 2>/dev/null | awk '$3 == "Pulling" || ($3 == "Failed" && /[Pp]ull/)' | head -5)"
	[ -z "$pulls" ] && ok no-pulls || ko no-pulls "$(echo "$pulls" | tr '\n' ' ')"
fi
if [ "$EXPECT_PAYLOADS" = 1 ]; then
	# The downstream's first-boot hand-off (river-firstboot-hooks) ran each hook to success.
	H=/usr/local/lib/runink/firstboot.d
	if ls "$H"/* >/dev/null 2>&1; then
		i=0 missing="x"
		while [ "$i" -lt 900 ] && [ -n "$missing" ]; do
			missing=""
			for h in "$H"/*; do [ -e "/var/lib/runink/firstboot.d/$(basename "$h").done" ] || missing="$missing $(basename "$h")"; done
			[ -n "$missing" ] && { sleep 15; i=$((i + 15)); }
		done
		# Once every hook succeeded, setup answers (if any were given) are gone. The shred runs
		# right after the last hook, so allow it a moment.
		sleep 5
		if [ -z "$missing" ] && [ -e /var/lib/runink/firstboot.d/setup-answers ]; then missing=" (setup-answers not shredded)"; fi
		[ -z "$missing" ] && ok firstboot-hooks || ko firstboot-hooks "not done:$missing"
		tail -n 20 /var/log/runink/firstboot.d/*.log 2>/dev/null
	else
		echo "RIVERTEST SKIP firstboot-hooks (none installed)"
	fi
fi
echo "RIVERTEST END installed"
sync
poweroff -f 2>/dev/null || poweroff
EOF

rm -f "$WORK/kit.img"
# 64 MiB, or the kit plus 32 MiB when a --kit-overlay carries more (e.g. packages to test).
kit_kib=$(($(du -sk "$WORK/kit" | cut -f1) + 32768))
[ "$kit_kib" -ge 65536 ] || kit_kib=65536
mkfs.vfat -C -n RIVERTEST "$WORK/kit.img" "$kit_kib" >/dev/null
for f in "$WORK"/kit/*; do mcopy -s -i "$WORK/kit.img" "$f" ::/; done
rm -f "$WORK/kit/models-passphrase" "$WORK/kit/setup-answers"

# --- helpers ------------------------------------------------------------------------------
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
			*) echo "qemu-test: cannot type '$c'" >&2; return 1 ;;
		esac
		mon "sendkey $k"
		sleep 0.03
	done
	mon "sendkey ret"
}
start_vm() { # phase-name, extra args...
	name="$1"; shift
	for f in ser mon; do rm -f "$WORK/$f.in" "$WORK/$f.out"; mkfifo "$WORK/$f.in" "$WORK/$f.out"; done
	# shellcheck disable=SC2086 # COMMON and the extras are word lists by design
	qemu-system-x86_64 $COMMON "$@" > "$WORK/qemu-$name.log" 2>&1 &
	QPID=$!
	cat "$WORK/ser.out" >> "$WORK/serial-$name.log" & CATS="$CATS $!"
	cat "$WORK/mon.out" > /dev/null & CATS="$CATS $!"
}
seen() { grep -q "$1" "$WORK/serial-$2.log" 2>/dev/null; }
wait_for() { # pattern phase seconds
	i=0
	while [ "$i" -lt "$3" ]; do
		seen "$1" "$2" && return 0
		kill -0 "$QPID" 2>/dev/null || return 1
		sleep 5; i=$((i + 5))
	done
	return 1
}
wait_exit() { # seconds
	i=0
	while kill -0 "$QPID" 2>/dev/null; do
		[ "$i" -lt "$1" ] || { kill "$QPID" 2>/dev/null; return 1; }
		sleep 5; i=$((i + 5))
	done
	QPID=""
}

# --- phase 1: live ---------------------------------------------------------------------------
echo "qemu-test: phase 1 — live boot from the $MEDIUM medium"
: > "$WORK/serial-live.log"
# shellcheck disable=SC2086
start_vm live $MEDIUM_ARGS
sleep 6; shot grub-menu
sleep 20; shot live-boot
# The workstation autologins into Plasma through SDDM: capture the desktop it reaches.
[ "$PROFILE" = workstation ] && { sleep 90; shot desktop-early; }
# Ctrl+Alt+F2 from a graphical session (Alt+F2 there is KRunner); plain Alt+F2 on a text console.
if [ "$PROFILE" = workstation ]; then VTKEY=ctrl-alt-f2; else VTKEY=alt-f2; fi
BOOT="sudo sh -c \"mkdir -p /run/rt; mount -r -L RIVERTEST /run/rt; sh /run/rt/live.sh\""
tries=0
until seen "RIVERTEST BEGIN live" live; do
	tries=$((tries + 1))
	[ "$tries" -le 4 ] || { echo "qemu-test: the live system never ran the kit (see $WORK/*.png)" >&2; break; }
	sleep 40
	# The second console logs in automatically on the live image; typing the credentials
	# first covers an image where it does not (on a shell they are two harmless commands).
	mon "sendkey $VTKEY"; sleep 2
	type_line runink; sleep 2; type_line runink; sleep 3
	type_line "$BOOT"
	sleep 20
	shot "tty2-try$tries"
done
if [ "$LAB" -eq 1 ]; then limit=5400; else limit=900; fi
if [ "$PROFILE" = workstation ] && wait_for "RIVERTEST SHOT desktop" live 600; then shot desktop; fi
wait_for "RIVERTEST END live" live "$limit" || echo "qemu-test: live phase did not finish" >&2
shot live-end
wait_exit 120 || true

# --- phase 2: installed -------------------------------------------------------------------------
if [ "$LAB" -eq 1 ] && seen "RIVERTEST OK export" live; then
	KEY="$(grep -Eo '([0-9a-f]{8} ){7}[0-9a-f]{8}' "$WORK/serial-live.log" | head -1 | tr -d ' ')"
	echo "qemu-test: phase 2 — boot the installed system from the scratch disk"
	: > "$WORK/serial-installed.log"
	start_vm installed
	if wait_for "passphrase" installed 300; then
		sleep 2
		timeout 5 sh -c 'printf "%s\n" "$1" > "$2"' _ "$KEY" "$WORK/ser.in" || true
		echo "qemu-test: recovery key sent over the serial console"
	else
		echo "qemu-test: no passphrase prompt on the serial console" >&2
		shot installed-noprompt
	fi
	if [ "$OFFLINE$EXPECT_PAYLOADS" != 00 ]; then ilimit=3600; else ilimit=900; fi
	wait_for "RIVERTEST END installed" installed "$ilimit" || { echo "qemu-test: installed phase did not finish" >&2; shot installed-end; }
	wait_exit 120 || true
fi

# --- verdict ---------------------------------------------------------------------------------------
cat "$WORK"/serial-*.log 2>/dev/null | tr -d '\r' | grep -a 'RIVERTEST ' > "$WORK/results.txt" || true
if [ "$PROFILE" = workstation ]; then
	want="grub-entry sddm autologin plasma s6-NetworkManager s6-sddm s6-bluetoothd s6-cupsd no-artix-live net-autostart"
else
	want="grub-entry guide-tty1 hwprobe plan plan-target fw-base fw-forward-drop fw-pair-live"
fi
# Network first, on every profile.
if [ "$NET" = none ]; then want="$want net-setup net-offline net-check-user"
elif [ "$NET" = restricted ]; then want="$want net-setup net-check-user"
else want="$want net-setup net-address net-gateway net-apply net-node-ip6 net-v6only net-check-user"; fi
[ "$EXPECT_MODELS" -eq 1 ] && want="$want payload"
[ "$LAB" -eq 1 ] && want="$want install harness-wired export zfs-root unlocked golden"
[ -n "$PASSF" ] && want="$want models"
[ "$EXPECT_PAYLOADS" -eq 1 ] && want="$want medium-payloads"
[ "$EXPECT_PAYLOADS" -eq 1 ] && [ "$LAB" -eq 1 ] && want="$want payloads firstboot-hooks"
[ "$PUBLIC" -eq 1 ] && want="$want public-medium"
[ "$PUBLIC" -eq 1 ] && [ "$LAB" -eq 1 ] && want="$want public-node"
[ "$OFFLINE" -eq 1 ] && want="$want net-offline k0s-ready k0s-images pods-running no-pulls"
fail=0
echo
echo "qemu-test: results ($WORK)"
for c in $want; do
	if grep -q "RIVERTEST OK $c\$" "$WORK/results.txt"; then echo "  PASS  $c"
	else echo "  FAIL  $c $(grep "RIVERTEST FAIL $c" "$WORK/results.txt" | head -1 | sed 's/^RIVERTEST FAIL [^ ]*//')"; fail=1; fi
done
echo "  screenshots: $(cd "$WORK" && ls ./*.png 2>/dev/null | tr '\n' ' ')"
if [ "$fail" -eq 0 ]; then
	echo "qemu-test: PASS"
	[ "$KEEP" -eq 1 ] || rm -f "$WORK/target.qcow2"
else
	echo "qemu-test: FAIL — serial logs and screenshots kept in $WORK" >&2
fi
exit "$fail"
