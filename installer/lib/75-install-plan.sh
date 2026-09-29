#!/bin/sh
# SPDX-FileCopyrightText: 2026 The RIVER Authors
# SPDX-License-Identifier: MIT
#
# 75-install-plan — record the hardware install plan on the installed node.
#
# /etc/runink/install-plan.json (schema river.install-plan/v1, docs/INSTALLER-HARDWARE.md)
# is the per-node record of what this hardware was planned for: enabled model tiers,
# context lengths, KV budgets, thread counts, the RAM budget and the pool layout. A
# downstream platform reads it to size its inference deployment on this node.
#
# root:root 0600 in the 0700 /etc/runink tree, like every file there: it names disk
# serials and the machine's inventory. river-perms re-asserts the mode on every boot.
set -eu

TARGET="${RUNINK_TARGET:?}"
PLAN="${RUNINK_PLAN_FILE:-}"

if [ -z "$PLAN" ]; then
	echo "install-plan: no plan (RUNINK_PLAN_FILE unset) — nothing recorded"
	exit 0
fi
[ -f "$PLAN" ] || { echo "install-plan: $PLAN is missing" >&2; exit 1; }

install -d -o root -g root -m 0700 "$TARGET/etc/runink"
install -o root -g root -m 0600 "$PLAN" "$TARGET/etc/runink/install-plan.json"
echo "install-plan: wrote /etc/runink/install-plan.json (0600)"
