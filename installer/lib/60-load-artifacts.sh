#!/bin/sh
# 60-load-artifacts — the baked app images + stack manifest ship in the golden image at
# /usr/local/share/runink/ (baked in from the profile root-overlay at ISO build time, and
# carried onto the target by the 20-clone-rootfs live-rootfs clone), so they're already on
# the target. The actual image load + apply
# happens at FIRST BOOT (runink-firstboot.sh), where the runink user has a real rootless
# session and enrollment has supplied the deployment config — far more reliable than a
# rootless podman load inside the installer chroot. This step just confirms staging.
set -eu

TARGET="${RUNINK_TARGET:?}"
SHARE="$TARGET/usr/local/share/runink"

if [ -f "$SHARE/images/runink-apps.tar" ]; then
	sz="$(du -h "$SHARE/images/runink-apps.tar" 2>/dev/null | cut -f1)"
	echo "load: app images staged on target ($sz) — loaded + played at first boot"
else
	echo "load: no baked app images (lean ISO / enrollment-load variant)"
fi

# Go host binaries come from the runink-* packages, which are installed into the golden
# rootfs at ISO build time and cloned onto the target by 20-clone-rootfs.
echo "load: artifacts staged (app images + stack manifest load at first boot)"
