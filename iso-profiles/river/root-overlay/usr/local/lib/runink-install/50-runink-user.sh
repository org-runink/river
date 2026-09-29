#!/bin/sh
# 50-runink-user — create the rootless service user + all rootless-podman prerequisites.
set -eu

TARGET="${RUNINK_TARGET:?}"
chroot_run() { chroot "$TARGET" /bin/sh -c "$1"; }

for fs in dev proc sys; do mount --rbind "/$fs" "$TARGET/$fs"; done

# Service user (uid 1000 to match XDG_RUNTIME_DIR=/run/user/1000 used everywhere).
echo "user: creating runink (uid 1000)"
chroot_run "id runink >/dev/null 2>&1 || useradd -m -u 1000 -U -s /bin/bash -G wheel,log runink"

# subuid/subgid come from the overlay (/etc/subuid,/etc/subgid). Verify.
grep -q '^runink:100000:65536' "$TARGET/etc/subuid" || echo 'runink:100000:65536' >> "$TARGET/etc/subuid"
grep -q '^runink:100000:65536' "$TARGET/etc/subgid" || echo 'runink:100000:65536' >> "$TARGET/etc/subgid"

# Lingering equivalent for s6: ensure /run/user/1000 exists at boot (rootless-boot.sh also
# creates it, but a tmpfiles entry makes it robust for other tools).
mkdir -p "$TARGET/etc/tmpfiles.d"
cat > "$TARGET/etc/tmpfiles.d/runink-runtimedir.conf" <<'EOF'
# Per-user runtime dir for rootless podman (uid 1000), created early at boot.
d /run/user/1000 0700 runink runink -
EOF

# cgroups v2 delegation for the runink user (rootless podman needs delegated controllers).
mkdir -p "$TARGET/etc/systemd/system" 2>/dev/null || true   # harmless if absent (s6 box)
# On s6 there is no systemd delegation; enable cgroup v2 + cpuset/memory delegation via
# the kernel cmdline default (systemd.unified_cgroup_hierarchy is systemd-only). cgroupfs
# mode in containers.conf handles rootless cgroups; ensure it is set.
mkdir -p "$TARGET/etc/containers"
if [ ! -f "$TARGET/etc/containers/containers.conf" ]; then
	cat > "$TARGET/etc/containers/containers.conf" <<'EOF'
[containers]
cgroups = "enabled"
[engine]
cgroup_manager = "cgroupfs"
events_logger = "file"
runtime = "crun"
EOF
fi

# The runink home lives on the $POOL/home dataset (created in 10-disk-zfs); models + the
# rootless container store go here, off the BE snapshot. Every level is created owned by
# runink: `install -d` makes missing parents as root, and a root-owned ~/.local left the
# desktop session unable to write ~/.local/share (SDDM's session log, Plasma's state).
chroot_run "install -d -o runink -g runink -m 0755 /home/runink/.local /home/runink/.local/share /home/runink/.local/share/containers"
# edge/ receives oidc.env at enrollment: shadow-grade from the start (river-perms re-asserts).
chroot_run "install -d -o runink -g runink -m 0700 /home/runink/edge"


# --- credentials: nothing from the live medium survives -------------------------------
# The rootfs is a clone of the live image, where root and runink carry the published live
# password ("runink"). An installed node must never keep it: root is locked outright (SSH
# already refuses root passwords and all passwords, sshd_config.d/10-runink.conf), and the
# admin gets either a password chosen now at the console, a pre-hashed one
# (RUNINK_ADMIN_PASSWORD_HASH: unattended installs, and the graphical installer, which hashes
# the password in its own memory), or no password at all (locked), in which case the only way
# in is the SSH key from RUNINK_ADMIN_AUTHORIZED_KEYS.
#
# The admin is `runink` unless RUNINK_ADMIN_USER names another account (the graphical
# installer's "Administrator user name"). Then that account is created as the admin (wheel,
# the password and the SSH keys go to it), and `runink` stays the node's SERVICE user (the
# keepalives, rootless state and river-perms paths name it) with a locked password.
chroot_run "usermod -p '!' root"
echo "user: root password locked"
ADMIN="${RUNINK_ADMIN_USER:-runink}"
case "$ADMIN" in
	*[!a-z0-9_-]*|[!a-z_]*|'') echo "user: RUNINK_ADMIN_USER '$ADMIN' is not a valid user name" >&2; exit 1 ;;
esac
if [ "$ADMIN" != runink ]; then
	chroot_run "id $ADMIN >/dev/null 2>&1 || useradd -m -U -s /bin/bash -G wheel,log $ADMIN"
	chroot_run "usermod -p '!' runink"
	echo "user: admin account $ADMIN created (wheel); runink stays the service user, password locked"
fi
admin_home="$(chroot "$TARGET" getent passwd "$ADMIN" | cut -d: -f6)"
admin_ids="$(chroot "$TARGET" id -u "$ADMIN"):$(chroot "$TARGET" id -g "$ADMIN")"
if [ -n "${RUNINK_ADMIN_AUTHORIZED_KEYS:-}" ]; then
	[ -r "$RUNINK_ADMIN_AUTHORIZED_KEYS" ] || { echo "user: cannot read $RUNINK_ADMIN_AUTHORIZED_KEYS" >&2; exit 1; }
	install -d -o "${admin_ids%:*}" -g "${admin_ids#*:}" -m 0700 "$TARGET$admin_home/.ssh"
	install -o "${admin_ids%:*}" -g "${admin_ids#*:}" -m 0600 "$RUNINK_ADMIN_AUTHORIZED_KEYS" "$TARGET$admin_home/.ssh/authorized_keys"
	echo "user: $ADMIN SSH authorized_keys installed ($(grep -c . "$RUNINK_ADMIN_AUTHORIZED_KEYS") key(s))"
fi
if [ -n "${RUNINK_ADMIN_PASSWORD_HASH:-}" ]; then
	printf '%s:%s\n' "$ADMIN" "$RUNINK_ADMIN_PASSWORD_HASH" | chroot "$TARGET" chpasswd -e
	echo "user: $ADMIN password set from RUNINK_ADMIN_PASSWORD_HASH"
elif [ -z "${RUNINK_UNATTENDED:-}" ] && [ -t 0 ]; then
	echo "user: choose the password for the '$ADMIN' admin (console login; SSH stays key-only)"
	until chroot "$TARGET" passwd "$ADMIN"; do echo "user: passwords did not match or were rejected; try again"; done
else
	chroot_run "usermod -p '!' $ADMIN"
	echo "user: $ADMIN password locked (unattended, no RUNINK_ADMIN_PASSWORD_HASH)"
	[ -n "${RUNINK_ADMIN_AUTHORIZED_KEYS:-}" ] || echo "user: WARNING: no password and no SSH key: this node has no interactive login until one is provisioned" >&2
fi

# The admin's login shell is fish when the image carries it: its greeting is the machine
# summary (etc/fish/conf.d/runink-greeting.fish). Only the account a person logs into; when
# the admin is not `runink`, the service user keeps bash. Nothing runs commands through a
# login shell (runuser and s6-setuidgid exec directly), so the change is only interactive.
if [ -x "$TARGET/usr/bin/fish" ]; then
	grep -qx /usr/bin/fish "$TARGET/etc/shells" 2>/dev/null || echo /usr/bin/fish >> "$TARGET/etc/shells"
	chroot_run "usermod -s /usr/bin/fish $ADMIN"
	echo "user: $ADMIN login shell is fish"
fi

for fs in sys proc dev; do umount -R "$TARGET/$fs" 2>/dev/null || true; done
echo "user: runink created with rootless prerequisites"
