#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# 05-hwplan-verify — re-probe the machine and re-resolve the confirmed install plan
# IMMEDIATELY before 10-disk-zfs writes to any disk.
#
# The drivers (runink-install, runink-autoinstall) resolved the plan when the operator
# confirmed each disk by serial. Minutes can pass before this step — a disk can be pulled,
# hot-plugged or renamed in between. So this step takes a FRESH probe, resolves the plan
# again with the same confirmed serials, and refuses unless it lands on exactly the disks
# the drivers exported. Nothing has been written to any disk when it fails.
#
# Inputs: RUNINK_PLAN_FILE, RUNINK_CONFIRMED_IDS (newline-separated serials), and the
# RUNINK_DISK / RUNINK_POOL_DISKS / RUNINK_SPECIAL_DISKS the drivers exported.
set -eu

PLAN="${RUNINK_PLAN_FILE:?05-hwplan-verify: no install plan (run the installer through runink-install or runink-autoinstall)}"
IDS="${RUNINK_CONFIRMED_IDS:?05-hwplan-verify: no disk was confirmed by serial}"
[ -f "$PLAN" ] || { echo "hwplan-verify: plan $PLAN is missing" >&2; exit 1; }

LIB="$(dirname "$0")"
# shellcheck source=installer/lib/hwplan.sh
. "$LIB/hwplan.sh"
hwplan_init
hwplan_probe "$HWPLAN_DIR/probe.json"
hwplan_resolve "$PLAN" "$HWPLAN_DIR/probe.json" "$HWPLAN_DIR/plan.env" "$IDS" || {
	echo "hwplan-verify: REFUSING — the confirmed plan no longer resolves on this machine" >&2
	exit 1
}

want="$RUNINK_DISK|${RUNINK_POOL_DISKS:-$RUNINK_DISK}|${RUNINK_SPECIAL_DISKS:-}"
got="$(
	# shellcheck disable=SC1091
	. "$HWPLAN_DIR/plan.env"
	printf '%s|%s|%s' "$RUNINK_DISK" "$RUNINK_POOL_DISKS" "$RUNINK_SPECIAL_DISKS"
)"
if [ "$got" != "$want" ]; then
	echo "hwplan-verify: REFUSING — the disks changed since they were confirmed" >&2
	echo "  confirmed: $want" >&2
	echo "  now:       $got" >&2
	exit 1
fi
rm -rf "$HWPLAN_DIR"
echo "hwplan-verify: plan re-resolved on a fresh probe — boot $RUNINK_DISK, pool ${RUNINK_POOL_DISKS:-$RUNINK_DISK}${RUNINK_SPECIAL_DISKS:+, special $RUNINK_SPECIAL_DISKS}"
