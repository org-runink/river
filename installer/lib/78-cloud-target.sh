#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# 78-cloud-target — configure a cloud image's target for its cloud (RUNINK_CLOUD). Runs after
# 40-boot-grub-zfs and 50-runink-user, before 80-enable-s6. docs/CLOUD-IMAGES.md.
#
# Generic for every cloud: the serial console, storage and NIC drivers in the initramfs, a
# NIC that takes whatever the VPC gives (IPv4, IPv6 or both), key-only SSH from metadata,
# the k0s node address, and the enrollment hook. The per-cloud values are in cloud_<name>
# below; `gce` is the only one today (aws and azure are planned, with the same hooks).
set -eu

TARGET="${RUNINK_TARGET:?}"
CLOUD="${RUNINK_CLOUD:?78-cloud-target: cloud images only}"

# The cluster address of every cloud node: a ULA on the river0 dummy interface. The node's
# IPv6-only k0s cluster (the downstream server's rule) advertises it, so it runs on an instance whose NIC has
# no IPv6 at all (GCE's default). Host-local: every instance uses the same one, and it is
# never routed off the instance. "fd52:6976:6572" is "Rivr" in ASCII.
NODE_ADDR="fd52:6976:6572:1::1"

cloud_gce() {
	# docs.cloud.google.com/compute/docs/import/import-existing-image: "Add
	# console=ttyS0,38400n8d". GCE's serial port 1 is ttyS0 (16550A).
	SERIAL_TTY=ttyS0
	SERIAL_KERNEL="console=tty0 console=ttyS0,38400n8d"
	SERIAL_GRUB="serial --unit=0 --speed=38400 --word=8 --parity=no --stop=1"
	SERIAL_BAUD="38400,115200"
	# Modules the build machine did not load, so mkinitcpio's autodetect would drop them:
	# NVMe (third-generation machine series boot from NVMe), gVNIC (gve) and virtio-net.
	# virtio-scsi, virtio-pci and virtio-blk are built into linux-runink (config.base); the
	# check below reads them from the running kernel, which is the one the target boots.
	INITRAMFS_MODULES="nvme virtio_net gve"
	# docs.cloud.google.com/compute/docs/images/building-custom-os: the options the kernel must
	# be built with (=y). VIRTIO_NET is a module here and goes in the initramfs above.
	KERNEL_BUILTIN="KVM_GUEST VIRTIO_PCI SCSI_VIRTIO PCI_MSI SERIAL_8250_CONSOLE"
	METADATA_BASE="http://169.254.169.254/computeMetadata/v1"
	# docs.cloud.google.com/compute/docs/images/configuring-imported-images: "include only the
	# `server metadata.google.internal iburst` Google NTP server".
	NTP_SERVER="metadata.google.internal"
}

case "$CLOUD" in
	gce) cloud_gce ;;
	*) echo "cloud-target: RUNINK_CLOUD=$CLOUD is not supported yet (gce only)" >&2; exit 1 ;;
esac

# The image profile: `node` (default) is a cluster node with encrypted data datasets; `base` is
# a general-purpose image with neither a cluster address nor an enrollment hook
# (docs/cloud/gcp.md). river-cloud-init reads it back from cloud.env.
PROFILE="${RUNINK_CLOUD_PROFILE:-node}"
case "$PROFILE" in
	node|base) ;;
	*) echo "cloud-target: RUNINK_CLOUD_PROFILE=$PROFILE (want node or base)" >&2; exit 1 ;;
esac
# The base edition is headless and serial-only: one console, at 115200 (the cloud edition, docs/cloud/edition.md).
if [ "$PROFILE" = base ]; then
	SERIAL_KERNEL="console=ttyS0,115200n8"
	SERIAL_GRUB="serial --unit=0 --speed=115200 --word=8 --parity=no --stop=1"
	SERIAL_BAUD="115200,38400"
fi

# The running kernel is the target's (the same linux-runink package), so its config is the
# target's. A kernel without /proc/config.gz cannot be checked, and that fails the build too.
[ -r /proc/config.gz ] || { echo "cloud-target: no /proc/config.gz to check the kernel against" >&2; exit 1; }
for o in $KERNEL_BUILTIN; do
	zcat /proc/config.gz | grep -qx "CONFIG_$o=y" || { echo "cloud-target: the kernel lacks CONFIG_$o=y" >&2; exit 1; }
done

# --- what river-cloud-init reads ------------------------------------------------------------
install -d -m 0755 "$TARGET/etc/river-cloud"
cat > "$TARGET/etc/river-cloud/cloud.env" <<EOF
# Written by installer step 78-cloud-target. Public configuration, no secrets.
RIVER_CLOUD=$CLOUD
RIVER_CLOUD_PROFILE=$PROFILE
RIVER_METADATA_BASE=$METADATA_BASE
RIVER_NODE_ADDR=$NODE_ADDR
RIVER_CLUSTER_DNS=fd00:10:96::a
EOF
command -v river-cloud-init >/dev/null 2>&1 && [ -x "$TARGET/usr/local/bin/river-cloud-init" ] \
	|| { echo "cloud-target: river-cloud-init is not on the target (runink-installer too old?)" >&2; exit 1; }

# --- serial console -----------------------------------------------------------------------------
# Kernel + GRUB on the serial port, no graphical theme: nobody sees a framebuffer in a cloud.
# Sourced after 10-runink-zfs.cfg, so these win.
cat > "$TARGET/etc/default/grub.d/20-river-cloud.cfg" <<EOF
# Cloud image ($CLOUD): serial console, no theme (installer step 78-cloud-target).
GRUB_CMDLINE_LINUX="\$GRUB_CMDLINE_LINUX $SERIAL_KERNEL"
# No "quiet" (the base default is "loglevel=3 quiet"): Google asks for full boot output.
GRUB_CMDLINE_LINUX_DEFAULT=""
GRUB_TERMINAL="serial console"
GRUB_SERIAL_COMMAND="$SERIAL_GRUB"
GRUB_THEME=""
GRUB_BACKGROUND=""
GRUB_GFXPAYLOAD_LINUX=text
GRUB_TIMEOUT=1
GRUB_TIMEOUT_STYLE=menu
EOF
# A login prompt on the serial console (the s6 ttyS service; enabled in 85-cloud-finalize).
cat > "$TARGET/etc/s6/config/ttyS.conf" <<EOF
# Cloud image ($CLOUD): getty on the serial console (installer step 78-cloud-target).
SPAWN="yes"
ARGS=""
GETTY="agetty"
TTY="$SERIAL_TTY"
BAUD_RATE="$SERIAL_BAUD"
EOF

# --- time ---------------------------------------------------------------------------------------
# The cloud's own NTP server and nothing else: an instance with no external address reaches no
# public server, and the cloud's server smears leap seconds the way its hosts do. The image's
# public servers stay in the file, commented out.
CHRONY="$TARGET/etc/chrony.conf"
if [ -f "$CHRONY" ]; then
	{
		echo "# Cloud image ($CLOUD): the cloud's NTP server only (installer step 78-cloud-target)."
		echo "server $NTP_SERVER iburst"
		sed -E 's/^(server|pool|peer) /# cloud: &/' "$CHRONY"
	} > "$CHRONY.new" && mv "$CHRONY.new" "$CHRONY"
	chmod 0644 "$CHRONY"
	[ "$(grep -cE '^(server|pool|peer) ' "$CHRONY")" = 1 ] && grep -qx "server $NTP_SERVER iburst" "$CHRONY" \
		|| { echo "cloud-target: $CHRONY does not name $NTP_SERVER as its only source" >&2; exit 1; }
fi

# --- initramfs: the cloud's disk and NIC drivers --------------------------------------------
# zz-: sourced after zfs.conf, whose MODULES=(zfs) would otherwise reset the list.
cat > "$TARGET/etc/mkinitcpio.conf.d/zz-river-cloud.conf" <<EOF
# Cloud image ($CLOUD): drivers the instance may boot from that the build machine did not use.
MODULES+=($INITRAMFS_MODULES)
EOF

# --- network ------------------------------------------------------------------------------------
# The VPC decides the address families: IPv4 on every GCE subnet, IPv6 on dual-stack ones.
# Take both (as the image default, 10-dual-stack.conf, does; stated here so the cloud image
# does not depend on it), keep the NIC's own MAC
# (the VPC drops frames from any other; 20-mac-spoof.conf would spoof it).
cat > "$TARGET/etc/NetworkManager/conf.d/90-river-cloud.conf" <<EOF
# Cloud image ($CLOUD) — installer step 78-cloud-target. docs/CLOUD-IMAGES.md, "Network".
[connection]
ipv4.method=auto
ipv6.method=auto
ethernet.cloned-mac-address=permanent
EOF
if [ "$PROFILE" = node ]; then
install -d -m 0755 "$TARGET/etc/NetworkManager/system-connections"
cat > "$TARGET/etc/NetworkManager/system-connections/river0.nmconnection" <<EOF
# The node's host-local cluster address (installer step 78-cloud-target).
[connection]
id=river0
type=dummy
interface-name=river0
autoconnect=true

[ipv4]
method=disabled

[ipv6]
method=manual
addresses=$NODE_ADDR/64
EOF
chmod 0600 "$TARGET/etc/NetworkManager/system-connections/river0.nmconnection"
fi

# --- SSH ----------------------------------------------------------------------------------------
# 05-: read before 10-runink-*.conf, and sshd keeps the FIRST value of each keyword. The
# instance's NIC may be IPv4-only, so listen on both families. Keys from metadata land in
# /etc/ssh/authorized_keys.d/runink (river-cloud-init), readable before the encrypted /home
# is unlocked, so the admin can always get in to repair a node.
cat > "$TARGET/etc/ssh/sshd_config.d/05-river-cloud.conf" <<'EOF'
# Cloud image — installer step 78-cloud-target. Key-only, no root login.
AddressFamily any
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
AuthorizedKeysFile .ssh/authorized_keys /etc/ssh/authorized_keys.d/%u
ClientAliveInterval 420
EOF
install -d -m 0755 "$TARGET/etc/ssh/authorized_keys.d"

# --- node profile only: the k0s node address and the enrollment hook ------------------------
if [ "$PROFILE" = node ]; then
# Quoted: an IPv6 literal is otherwise a YAML mapping hazard (see runink-firstboot.sh).
K0S="$TARGET/etc/k0s/k0s.yaml"
if [ -f "$K0S" ] && ! grep -q '^    address:' "$K0S"; then
	sed -i "/^  api:/a\\    address: \"$NODE_ADDR\"" "$K0S"
	sed -i "/^    sans:/a\\      - \"$NODE_ADDR\"" "$K0S"
fi
grep -q "address: \"$NODE_ADDR\"" "$K0S" || { echo "cloud-target: could not set api.address in $K0S" >&2; exit 1; }

# --- enrollment hook ------------------------------------------------------------------------------
# As 70-secrets-models wires it on an installed node (runink-autoinstall skips 70): the first
# boot runs runink-firstboot.sh from rc.local; river-cloud-init stages enrollment.env from
# the `river-enrollment` metadata attribute when one is given, and it no-ops otherwise.
RCLOCAL="$TARGET/etc/s6/rc.local"
if [ -f "$TARGET/usr/local/bin/runink-firstboot.sh" ] && ! grep -q runink-firstboot "$RCLOCAL"; then
	awk '
		/^set -eu/ && !done {
			print; print "";
			print "# First-boot enrollment (models + secrets); no-op once the sentinel exists.";
			print "/usr/local/bin/runink-firstboot.sh || echo \"rc.local: firstboot deferred\"";
			done=1; next
		}
		{ print }
	' "$RCLOCAL" > "$RCLOCAL.new" && mv "$RCLOCAL.new" "$RCLOCAL"
	chmod +x "$RCLOCAL"
fi
fi

# --- base profile only: the guest bootstrap, SSH reachable, accounts ----------------------------
if [ "$PROFILE" = base ]; then
# The s6 services of river-cloud-init (docs/cloud/gcp.md section 2.9). A downstream profile that
# ships its own keeps them (nothing is overwritten).
svc() { # svc NAME TYPE DEPENDENCIES... ; the script (up or run) is on stdin
	_n="$1" _t="$2"; shift 2
	_d="$TARGET/etc/s6/sv/$_n"
	[ -f "$_d/type" ] && return 0
	mkdir -p "$_d/dependencies.d"
	echo "$_t" > "$_d/type"
	for _x in "$@"; do : > "$_d/dependencies.d/$_x"; done
	if [ "$_t" = oneshot ]; then cat > "$_d/up"; else cat > "$_d/run"; chmod 0755 "$_d/run"; fi
}
detach() { printf '/bin/sh -c "setsid /usr/local/bin/river-cloud-init %s </dev/null >/dev/null 2>&1 &"\n' "$1"; }
printf '%s\n' "/bin/sh -c \"/usr/local/bin/river-cloud-init identity || echo 'river-cloud-identity: failed' >&2; exit 0\"" \
	| svc river-cloud-identity oneshot mount-filesystems remount-root
echo "/usr/local/bin/river-cloud-init boot" | svc river-cloud-init oneshot river-cloud-identity NetworkManager-srv
detach late | svc river-cloud-late oneshot river-cloud-init
detach startup | svc river-cloud-startup oneshot river-cloud-init
printf '#!/bin/execlineb -P\nfdmove -c 2 1\n/usr/local/bin/river-cloud-init watch\n' | svc river-cloud-watch longrun river-cloud-init

# SSH is closed by the host firewall until /etc/runink/fw-open lists it (invariant 7). An
# instance with no way in is useless, so the base image opens tcp 22 and nothing else.
install -d -m 0700 "$TARGET/etc/runink"
printf '# Cloud edition: SSH (installer step 78-cloud-target). Nothing else is opened.\ntcp 22\n' > "$TARGET/etc/runink/fw-open"
chmod 0600 "$TARGET/etc/runink/fw-open"

# The accounts named in metadata `ssh-keys` are administrators, as with Compute Engine's guest
# environment: wheel, without a password (they have none; login is by key only).
cat > "$TARGET/etc/sudoers.d/20-river-cloud" <<'SUDO'
# Cloud edition (installer step 78-cloud-target): metadata-listed accounts have no password.
%wheel ALL=(ALL:ALL) NOPASSWD: ALL
SUDO
chmod 0440 "$TARGET/etc/sudoers.d/20-river-cloud"
# The NIC takes its MTU from DHCP (Compute Engine sends 1460); nothing may force another.
if grep -rqs '^[[:space:]]*\(ethernet\.\)\?mtu[[:space:]]*=' "$TARGET/etc/NetworkManager"; then
	echo "cloud-target: a NetworkManager file forces an MTU; the VPC's 1460 must come from DHCP" >&2; exit 1
fi
fi

# --- rebuild the initramfs and grub.cfg with all of the above ------------------------------
mounted=""
cleanup() { for fs in $mounted; do umount -R "$TARGET/$fs" 2>/dev/null || true; done; }
trap cleanup EXIT
for fs in dev proc sys; do mount --rbind "/$fs" "$TARGET/$fs"; mounted="$fs $mounted"; done
chroot "$TARGET" /bin/sh -c "mkinitcpio -P"
chroot "$TARGET" /bin/sh -c "grub-mkconfig -o /boot/grub/grub.cfg"
grep -q 'console=ttyS0' "$TARGET/boot/grub/grub.cfg" || { echo "cloud-target: grub.cfg has no serial console" >&2; exit 1; }
if grep -E '^\s*linux\s' "$TARGET/boot/grub/grub.cfg" | grep -qw quiet; then
	echo "cloud-target: grub.cfg still boots with quiet" >&2; exit 1
fi
for m in $INITRAMFS_MODULES; do
	chroot "$TARGET" /bin/sh -c "lsinitcpio /boot/initramfs-linux-runink.img" | grep -q "/$m\.ko" \
		|| { echo "cloud-target: $m is not in the initramfs" >&2; exit 1; }
done
if [ "$PROFILE" = node ]; then
	echo "cloud-target: $CLOUD configured, profile node (serial $SERIAL_TTY, initramfs +$INITRAMFS_MODULES, NTP $NTP_SERVER, node address $NODE_ADDR)"
else
	echo "cloud-target: $CLOUD configured, profile base (serial $SERIAL_TTY, initramfs +$INITRAMFS_MODULES, NTP $NTP_SERVER)"
fi
