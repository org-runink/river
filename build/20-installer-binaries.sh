#!/bin/sh
# 20-installer-binaries.sh — build the installer's hardware probe and planner.
#
# river-hwprobe and river-plan come from THIS repository (installer/, Go standard library
# only), so this step needs no sibling checkout and no downstream payload: it runs for a
# base image and for a payload image alike. They are packaged by runink-installer, not by
# runink-runtime, which carries only what a downstream payload stages in $OUT_DIR/bin.
#
# GOAMD64=v1 ON PURPOSE: these two must run on the machines the installer REFUSES. A
# pre-AVX2 CPU has to get "x86-64-v2, missing avx2 ..." from river-plan, not SIGILL from
# river-hwprobe. See docs/INSTALLER-HARDWARE.md.
#
# river-modelpack and river-payloadpack (same module, same flags) open the encrypted payloads a server
# medium may carry (docs/PAYLOADS.md); the live installer runs them.
#
# river-cloud-init (same module, same flags) brings a cloud image up on its instance
# (docs/CLOUD-IMAGES.md); it ships on every image and runs only where 78-cloud-target enabled it.
#
# river-netcheck (same module, same flags) is the connectivity check behind river-netsetup,
# the first step of every live session (docs/INSTALL.md, "Network first").
#
# river-pair and river-pair-announce (same module, same flags) are the two sides of opt-in LAN
# install pairing (docs/INSTALL.md, "LAN installs"): the operator machine and the target.
#
# Output: $OUT_DIR/installer-bin/{river-hwprobe,river-plan,river-modelpack,river-payloadpack,
#   river-cloud-init,river-netcheck,river-netsetup,river-pair,river-pair-announce,river-installer}
#
# river-installer (same module, same flags) is the graphical installer's backend and web UI, and
# the graphical first boot of an installed server (docs/INSTALL.md, "Installing with the
# graphical installer").
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
# shellcheck disable=SC1091
. "$HERE/config.env"

DEST="$OUT_DIR/installer-bin"
mkdir -p "$DEST"

command -v go >/dev/null 2>&1 || { echo "20-installer-binaries: go toolchain required (builder only)" >&2; exit 1; }

LDFLAGS="-s -w"

for b in hwprobe:river-hwprobe plan:river-plan modelpack:river-modelpack payloadpack:river-payloadpack \
	cloudinit:river-cloud-init netcheck:river-netcheck pair:river-pair pairannounce:river-pair-announce \
	gui:river-installer; do
	echo "  building ${b#*:} from installer/${b%%:*} (GOAMD64=v1)"
	( cd "$HERE/../installer" && CGO_ENABLED=0 GOAMD64=v1 \
		go build -trimpath -ldflags="$LDFLAGS" -o "$DEST/${b#*:}" "./${b%%:*}" )
done

# river-netsetup, the network-first step (a POSIX sh wrapper over nmcli and river-netcheck),
# ships beside them from the same package.
install -m 0755 "$HERE/../installer/netsetup/river-netsetup" "$DEST/river-netsetup"

echo "20-installer-binaries: built:"
ls -la "$DEST"
