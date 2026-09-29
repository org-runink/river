#!/bin/sh
# 70-secrets-models — install the first-boot enrollment hook (models + secrets).
#
# NOTHING secret is written into the image here. If the operator supplied an enrollment.env
# at install (RUNINK_ENROLL), it is copied to /etc/runink/enrollment.env on the target so
# runink-firstboot.sh consumes (and shreds) it on first boot. Otherwise the node boots
# un-enrolled and degrades until enrollment.env is dropped in later.
set -eu

TARGET="${RUNINK_TARGET:?}"
ENROLL_SRC="${RUNINK_ENROLL:-}"

install -d -m 0700 "$TARGET/etc/runink"

if [ -n "$ENROLL_SRC" ] && [ -f "$ENROLL_SRC" ]; then
	echo "secrets: staging enrollment.env for first boot (will be shredded after use)"
	install -m 0600 "$ENROLL_SRC" "$TARGET/etc/runink/enrollment.env"
else
	echo "secrets: no enrollment.env provided — node boots un-enrolled (models+secrets deferred)"
fi

# Wire runink-firstboot.sh to run once on first boot, BEFORE the rc.local stack settles.
# On s6 we add a oneshot that runs it; simplest robust hook is to have rc.local invoke it
# first. Patch rc.local to call firstboot ahead of the stack if not already wired.
RCLOCAL="$TARGET/etc/s6/rc.local"
# Only wire it if the script is actually ON THE TARGET. This step is shared by every
# profile, but runink-firstboot.sh is a server distribution's enrollment hook and ships only
# in such a profile's overlay. Without this guard a Runink River install patches its
# rc.local to call a file that does not exist: the `|| echo` on the injected line keeps it
# non-fatal, so it does not break the boot — it just prints "firstboot deferred" on every
# boot forever, which is the kind of harmless-looking noise nobody traces back.
FIRSTBOOT="$TARGET/usr/local/bin/runink-firstboot.sh"
if [ ! -f "$FIRSTBOOT" ]; then
	echo "secrets: no runink-firstboot.sh on target — skipping rc.local wiring (expected on Runink River and other non-server profiles)"
elif [ -f "$RCLOCAL" ] && ! grep -q runink-firstboot "$RCLOCAL"; then
	# Insert the firstboot call right after the shebang/set line block.
	tmp="$RCLOCAL.new"
	awk '
		/^set -eu/ && !done {
			print;
			print "";
			print "# First-boot enrollment (models + secrets); no-op once the sentinel exists.";
			print "/usr/local/bin/runink-firstboot.sh || echo \"rc.local: firstboot deferred\"";
			done=1; next
		}
		{ print }
	' "$RCLOCAL" > "$tmp" && mv "$tmp" "$RCLOCAL"
	chmod +x "$RCLOCAL"
	echo "secrets: wired runink-firstboot.sh into rc.local"
fi

echo "secrets: first-boot enrollment configured"
