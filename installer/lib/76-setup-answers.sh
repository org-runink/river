#!/bin/sh
# 76-setup-answers — hand a scripted install's downstream setup answers to the first boot.
#
# A downstream platform may take its first configuration from an answers file instead of an
# operator at a console (docs/PAYLOADS.md, "First-boot hand-off"). `runink-autoinstall
# --setup-answers FILE` sets RUNINK_SETUP_ANSWERS; this step copies the file onto the target
# at /var/lib/runink/firstboot.d/setup-answers (root, 0600 in a 0700 directory: it may hold
# secrets). river-firstboot-hooks passes its path to the first-boot hooks as
# RIVER_SETUP_ANSWERS and shreds it once every hook has succeeded. Runink River itself never
# reads it. No RUNINK_SETUP_ANSWERS: nothing to do.
set -eu

TARGET="${RUNINK_TARGET:?}"
[ -n "${RUNINK_SETUP_ANSWERS:-}" ] || { echo "setup-answers: none given"; exit 0; }
[ -f "$RUNINK_SETUP_ANSWERS" ] && [ -r "$RUNINK_SETUP_ANSWERS" ] \
	|| { echo "setup-answers: cannot read $RUNINK_SETUP_ANSWERS" >&2; exit 1; }
[ -s "$RUNINK_SETUP_ANSWERS" ] || { echo "setup-answers: $RUNINK_SETUP_ANSWERS is empty" >&2; exit 1; }
D="$TARGET/var/lib/runink/firstboot.d"
( umask 077; mkdir -p "$D" )
chmod 0700 "$TARGET/var/lib/runink" "$D"
( umask 077; cp "$RUNINK_SETUP_ANSWERS" "$D/setup-answers.part" )
chown 0:0 "$D/setup-answers.part"
chmod 0600 "$D/setup-answers.part"
mv "$D/setup-answers.part" "$D/setup-answers"
echo "setup-answers: staged for the first boot ($(wc -c < "$D/setup-answers") bytes, 0600; shredded after use)"
