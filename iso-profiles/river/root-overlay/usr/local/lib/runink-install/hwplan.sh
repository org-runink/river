#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# hwplan.sh — shared by the install drivers (runink-install, runink-autoinstall); SOURCED,
# never run as a step. It probes the machine, makes or loads the install plan, and resolves
# the plan's disks against a fresh probe once the operator has confirmed them by serial.
#
# The work directory lives under /run (tmpfs, 0700): the probe and plan name disk serials
# and MAC addresses, which are node inventory, not something to leave in /tmp.
#
# Binaries: river-hwprobe and river-plan (installer/hwprobe, installer/plan; shipped in the
# runink-installer package). Contract: docs/INSTALLER-HARDWARE.md.

HWPLAN_MANIFEST="${RUNINK_MODELS_TIERS:-/usr/local/share/runink/models.tiers}"
HWPLAN_LOCK="${RUNINK_MODELS_LOCK:-/usr/local/share/runink/models.lock}"
# server (default) or workstation: the planner profile (docs/INSTALLER-HARDWARE.md, "Profiles").
# A workstation plans against desktop minimums and plans no model tiers.
HWPLAN_PROFILE="${RUNINK_PLAN_PROFILE:-server}"

# hwplan_init — create the private work dir; sets HWPLAN_DIR.
hwplan_init() {
	for _b in river-hwprobe river-plan; do
		command -v "$_b" >/dev/null 2>&1 || {
			echo "hwplan: $_b not found (runink-installer package missing from the live image?)" >&2
			return 1
		}
	done
	_base=/run/runink-install
	[ -w /run ] || _base="${TMPDIR:-/tmp}/runink-install.$$"
	_umask="$(umask)"
	umask 077 # only for these two; the install steps keep the caller's umask
	mkdir -p "$_base"
	# shellcheck disable=SC2034 # read by the sourcing driver
	HWPLAN_DIR="$(mktemp -d "$_base/hwplan.XXXXXX")"
	umask "$_umask"
}

# hwplan_probe OUT — a fresh hwprobe document.
hwplan_probe() {
	river-hwprobe --json > "$1"
}

# hwplan_make PROBE OUT [--lab] — plan from the probe and the image's models manifest.
# Returns river-plan's status: 0 ok/degraded, 3 refused (OUT still holds the plan).
hwplan_make() {
	_probe="$1"; _out="$2"; shift 2
	if [ "$HWPLAN_PROFILE" = workstation ]; then
		set -- --profile workstation --no-models "$@"
	elif [ -f "$HWPLAN_MANIFEST" ]; then
		if [ -f "$HWPLAN_LOCK" ]; then
			set -- --manifest "$HWPLAN_MANIFEST" --lock "$HWPLAN_LOCK" "$@"
		else
			set -- --manifest "$HWPLAN_MANIFEST" "$@"
		fi
	else
		echo "hwplan: no models manifest at $HWPLAN_MANIFEST — planning hardware and storage only" >&2
		set -- --no-models "$@"
	fi
	river-plan --probe "$_probe" "$@" --json > "$_out"
}

# hwplan_resolve PLAN PROBE ENVOUT CONFIRMED_IDS — CONFIRMED_IDS is newline-separated.
# Writes sh assignments (RUNINK_DISK, RUNINK_POOL_*, ...) to ENVOUT. Fails unless every
# disk the plan wipes was confirmed, is present in PROBE and is still eligible.
hwplan_resolve() {
	_plan="$1"; _probe="$2"; _env="$3"; _ids="$4"
	set --
	_oifs="$IFS"
	IFS='
'
	for _id in $_ids; do
		[ -n "$_id" ] && set -- "$@" --confirm "$_id"
	done
	IFS="$_oifs"
	river-plan --plan-file "$_plan" --probe "$_probe" --env "$@" > "$_env"
}
