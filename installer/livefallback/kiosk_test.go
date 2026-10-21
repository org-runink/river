// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kioskScript is the river-kiosk the live medium ships. The fallback is only as good as the
// thing it hands the console to, so the hand-over is tested here, beside the fallback.
const kioskScript = "../../iso-profiles/river/root-overlay/usr/local/bin/river-kiosk"

// runKiosk runs the real river-kiosk script with stub tools in a scratch directory and returns
// what it printed and what the stubs recorded.
//
//	withSeatdLaunch   whether a seatd-launch exists on the (fake) image
//
// The stubs: weston records that it ran and whether it ran UNDER seatd-launch; seatd-launch
// records itself, marks its child, and execs the command after `--` like the real one.
func runKiosk(t *testing.T, withSeatdLaunch bool) (out, rec string) {
	t.Helper()
	return runKioskArgs(t, withSeatdLaunch)
}

// runKioskArgs is runKiosk with extra arguments for river-kiosk, as the fallback passes them
// (`river-kiosk --vt 1`).
func runKioskArgs(t *testing.T, withSeatdLaunch bool, extra ...string) (out, rec string) {
	t.Helper()
	return runKioskOpts(t, kioskOpts{seatd: withSeatdLaunch, extra: extra})
}

// kioskOpts: seatd = a seatd-launch exists; asRoot = the script believes it runs as root and as
// the kiosk user "kioskuser" (stubbed `id` and `chown`, so a test run as an ordinary user can
// observe what root would do).
type kioskOpts struct {
	seatd  bool
	asRoot bool
	extra  []string
}

func runKioskOpts(t *testing.T, o kioskOpts) (out, rec string) {
	t.Helper()
	withSeatdLaunch, extra := o.seatd, o.extra
	if _, err := os.Stat(kioskScript); err != nil {
		t.Fatalf("river-kiosk not found at %s: %v", kioskScript, err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	recFile := filepath.Join(dir, "rec")
	put := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	put("weston", "#!/bin/sh\necho \"weston $* under_seatd_launch=${UNDER_SEATD_LAUNCH:-no}\" >> \""+recFile+"\"\n")
	put("river-installer", "#!/bin/sh\nexit 0\n")
	if o.asRoot {
		put("id", "#!/bin/sh\n[ \"$1\" = -u ] && { echo 0; exit 0; }\nexit 0\n")
		put("chown", "#!/bin/sh\necho \"chown $*\" >> \""+recFile+"\"\n")
	}
	if withSeatdLaunch {
		put("seatd-launch", "#!/bin/sh\necho \"seatd-launch $*\" >> \""+recFile+"\"\n"+
			"[ \"$1\" = -- ] && shift\nUNDER_SEATD_LAUNCH=yes\nexport UNDER_SEATD_LAUNCH\nexec \"$@\"\n")
	}
	launch := filepath.Join(dir, "launch")
	if err := os.WriteFile(launch, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", append([]string{kioskScript, "--wait", "1"}, extra...)...)
	cmd.Env = []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"RIVER_KIOSK_PATH=" + bin + ":/usr/bin:/bin",
		"RIVER_KIOSK_RUN=" + filepath.Join(dir, "run"),
		"RIVER_KIOSK_LAUNCH=" + launch,
		"RIVER_KIOSK_USER=" + map[bool]string{false: "root", true: "kioskuser"}[o.asRoot],
		"RIVER_KIOSK_DRMWAIT=0",
		"RIVER_KIOSK_MINRUN=0", // a stub that exits at once still counts as a session that ran
	}
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("river-kiosk failed: %v\n%s", err, b)
	}
	r, _ := os.ReadFile(recFile)
	return string(b), string(r)
}

// On the owner's hardware weston, started as root, died with
//
//	Could not connect to socket /run/seatd.sock: No such file or directory
//	Could not get primary session for user: No data available
//	No backend was able to open a seat
//
// because nothing ran seatd and root has no logind session. The image ships seatd-launch for
// exactly this. weston must be started UNDER it.
func TestKioskStartsWestonUnderSeatdLaunch(t *testing.T) {
	out, rec := runKiosk(t, true)
	if !strings.Contains(rec, "seatd-launch -- weston --continue-without-input") {
		t.Errorf("weston was not started through seatd-launch; recorded:\n%s\nscript said:\n%s", rec, out)
	}
	if !strings.Contains(rec, "under_seatd_launch=yes") {
		t.Errorf("weston ran, but not as a child of seatd-launch; recorded:\n%s", rec)
	}
	if !strings.Contains(out, "seat: a private seatd for weston") {
		t.Errorf("the script did not say which seat it was using:\n%s", out)
	}
}

// A medium without seatd-launch must behave as it did before, not differently: weston is
// started directly and the script says plainly that it will probably fail.
func TestKioskWithoutSeatdLaunchStartsWestonDirectly(t *testing.T) {
	out, rec := runKiosk(t, false)
	if strings.Contains(rec, "seatd-launch") {
		t.Errorf("seatd-launch was invoked although it does not exist; recorded:\n%s", rec)
	}
	if !strings.Contains(rec, "weston --continue-without-input") || !strings.Contains(rec, "under_seatd_launch=no") {
		t.Errorf("weston was not started directly; recorded:\n%s", rec)
	}
	if !strings.Contains(out, "seatd-launch is not installed") {
		t.Errorf("the script did not warn that there is no seat helper:\n%s", out)
	}
}

// The fallback starts the kiosk as `river-kiosk --vt N`. The script used to forward that to weston
// as `--tty=N`. weston 15 under libseat does not accept it: after loading its shell it printed
// "fatal: unhandled option: --tty=1" and exited, so on the real ISO the kiosk never drew even once
// it had its seat (found booting runink-river-20261021). The script already switches to the console
// itself (`river-installer vt`); the seat daemon binds the seat to the active one.
func TestKioskNeverPassesTtyToWeston(t *testing.T) {
	_, rec := runKioskArgs(t, true, "--vt", "1")
	if !strings.Contains(rec, "weston --continue-without-input") {
		t.Fatalf("weston did not run; recorded:\n%s", rec)
	}
	if strings.Contains(rec, "--tty") {
		t.Errorf("weston was handed --tty, which weston 15 rejects as an unhandled option; recorded:\n%s", rec)
	}
}

// river-installer-desktop drops to the kiosk user with setpriv and gives it XDG_RUNTIME_DIR=/run/river-kiosk,
// where Firefox keeps its profile and dconf. river-kiosk created that directory as root, so on the real
// ISO Firefox could not write there and showed "Your Firefox profile cannot be loaded" (dconf and the
// profile: "unable to create directory '/run/river-kiosk/dconf': Permission denied"). As root, the script
// must hand the directory to the kiosk user. weston stays root and can use any directory.
func TestKioskHandsItsRuntimeDirectoryToTheKioskUser(t *testing.T) {
	_, rec := runKioskOpts(t, kioskOpts{seatd: true, asRoot: true, extra: []string{"--vt", "1"}})
	if !strings.Contains(rec, "chown kioskuser ") || !strings.HasSuffix(strings.Fields(strings.Split(rec, "chown kioskuser ")[1])[0], "run") {
		t.Errorf("the runtime directory was not handed to the kiosk user; recorded:\n%s", rec)
	}
}

// An ordinary user (the tests, a developer) cannot chown, and does not need to.
func TestKioskDoesNotChownWhenNotRoot(t *testing.T) {
	_, rec := runKioskOpts(t, kioskOpts{seatd: true})
	if strings.Contains(rec, "chown") {
		t.Errorf("chown ran although the script is not root; recorded:\n%s", rec)
	}
}
