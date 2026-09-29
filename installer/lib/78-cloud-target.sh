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
	# virtio-scsi, virtio-pci and virtio-blk are built into linux-runink (config.require).
	INITRAMFS_MODULES="nvme virtio_net gve"
	METADATA_BASE="http://169.254.169.254/computeMetadata/v1"
}

case "$CLOUD" in
	gce) cloud_gce ;;
	*) echo "cloud-target: RUNINK_CLOUD=$CLOUD is not supported yet (gce only)" >&2; exit 1 ;;
esac

# --- what river-cloud-init reads ------------------------------------------------------------
install -d -m 0755 "$TARGET/etc/river-cloud"
cat > "$TARGET/etc/river-cloud/cloud.env" <<EOF
# Written by installer step 78-cloud-target. Public configuration, no secrets.
RIVER_CLOUD=$CLOUD
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

# --- k0s: advertise the node address --------------------------------------------------------
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
echo "cloud-target: $CLOUD configured (serial $SERIAL_TTY, initramfs +$INITRAMFS_MODULES, node address $NODE_ADDR)"
