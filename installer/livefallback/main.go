// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// river-live-fallback — make sure a live medium ALWAYS reaches an installer.
//
// The live medium logs the live user straight into the desktop (SDDM autologin), and the
// graphical installer starts inside that session. When the desktop cannot start on a given
// machine — a GPU the kernel cannot drive, a compositor that dies, a display manager that
// never comes up — nothing else offered the installer: the screen stayed blank or on a bare
// console, and the only way in was knowing to type `sudo runink-install`. A machine that
// boots the medium must always end up somewhere a person can install from.
//
// So: wait for a graphical session; if none appears within the timeout, fall back, in order,
// to the console kiosk (the same web UI drawn straight through KMS, no desktop needed) and
// then to the text installer. Both are started from the init context as root, so nobody has
// to find a terminal or type a privileged command.
//
//	river-live-fallback [--timeout 150s] [--tty /dev/tty1] [--check]
//
//	--check   report what it would do and exit; starts nothing (tests, and a live shell)
//
// It exits 0 when a desktop appeared (it did nothing) and when a fallback finished. It is
// started in the background by /etc/s6/rc.local on the live medium only, and must never hold
// up the boot.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// sessionProbe is one way of telling that a graphical session exists. The first that
// matches wins; none of them needs the session to be healthy, only present, because a
// desktop that draws anything at all can open the installer itself.
type sessionProbe struct {
	name string
	find func() bool
}

func probes() []sessionProbe {
	return []sessionProbe{
		// A Wayland compositor publishes its socket under the user's runtime directory.
		{"wayland socket", func() bool { return globAny("/run/user/*/wayland-*") }},
		// An X server publishes its socket here. Runink River is Wayland, but a
		// downstream profile may not be.
		{"X socket", func() bool { return globAny("/tmp/.X11-unix/X*") }},
		// The desktop shell itself, in case the sockets live somewhere unusual.
		{"plasmashell", func() bool { return pgrep("plasmashell") }},
	}
}

func globAny(pattern string) bool {
	m, err := filepath.Glob(pattern)
	return err == nil && len(m) > 0
}

// pgrep reports whether a process with this exact name is running. It reads /proc rather
// than shelling out, so it works on a medium with no pgrep.
func pgrep(name string) bool {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil {
			continue
		}
		if len(b) > 0 && string(b[:len(b)-1]) == name {
			return true
		}
	}
	return false
}

// waitForSession polls until a probe matches or the deadline passes. It returns the probe
// that matched, or "" when none did.
func waitForSession(timeout, every time.Duration, tick func(time.Duration)) string {
	start := time.Now()
	deadline := start.Add(timeout)
	lastTick := time.Duration(-1)
	for {
		for _, p := range probes() {
			if p.find() {
				return p.name
			}
		}
		if !time.Now().Before(deadline) {
			return ""
		}
		// Say something while waiting. A console that sits silent for minutes looks
		// identical to one that has hung, and the person in front of it has no way to
		// tell "Plasma is still loading off a USB stick" from "this is never going to
		// work". Once every 30 s is enough to show progress without filling the screen.
		if tick != nil {
			if el := time.Since(start).Truncate(30 * time.Second); el > 0 && el != lastTick {
				lastTick = el
				tick(el)
			}
		}
		time.Sleep(every)
	}
}

// graphicsReport is the state of the display hardware at the moment the desktop was given up
// on. Everything here is a plain file read, so it works on a medium with no tools and cannot
// hang: no lspci, no dmesg binary, no shelling out.
func graphicsReport() string {
	var b strings.Builder
	b.WriteString("  Graphics state (please photograph this if you report the problem):\n")

	// DRM device nodes. None at all means no driver bound and nothing can draw.
	if ents, err := os.ReadDir("/dev/dri"); err == nil && len(ents) > 0 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		b.WriteString("    /dev/dri        " + strings.Join(names, " ") + "\n")
	} else {
		b.WriteString("    /dev/dri        EMPTY or missing - no DRM device, nothing can draw\n")
	}

	// Which driver claimed each card. simpledrm here means the EFI framebuffer is all we got
	// and the real GPU driver never bound.
	if cards, err := filepath.Glob("/sys/class/drm/card*/device/driver"); err == nil {
		for _, c := range cards {
			if dst, err := os.Readlink(c); err == nil {
				card := filepath.Base(filepath.Dir(filepath.Dir(c)))
				b.WriteString("    " + card + " driver   " + filepath.Base(dst) + "\n")
			}
		}
	}

	// Connector status, so a panel that is present but reported disconnected is visible.
	if sts, err := filepath.Glob("/sys/class/drm/card*-*/status"); err == nil {
		for _, st := range sts {
			if v, err := os.ReadFile(st); err == nil {
				b.WriteString("    " + filepath.Base(filepath.Dir(st)) + "  " +
					strings.TrimSpace(string(v)) + "\n")
			}
		}
	}

	// Did the GPU modules load at all?
	if mods, err := os.ReadFile("/proc/modules"); err == nil {
		var found []string
		for _, want := range []string{"i915", "amdgpu", "nouveau", "xe", "simpledrm"} {
			for _, line := range strings.Split(string(mods), "\n") {
				if strings.HasPrefix(line, want+" ") {
					found = append(found, want)
					break
				}
			}
		}
		if len(found) == 0 {
			b.WriteString("    modules         none of i915/amdgpu/nouveau/xe loaded\n")
		} else {
			b.WriteString("    modules         " + strings.Join(found, " ") + "\n")
		}
	}

	// The display manager's own account. On a live medium /var/log is not writable, so SDDM
	// writes under /run -- which is exactly why nobody has ever read this file.
	for _, p := range []string{"/run/log/sddm", "/var/log/sddm.log", "/run/sddm.log"} {
		if v, err := os.ReadFile(p); err == nil && len(v) > 0 {
			lines := strings.Split(strings.TrimSpace(string(v)), "\n")
			if len(lines) > 6 {
				lines = lines[len(lines)-6:]
			}
			b.WriteString("    " + p + " (last " + fmt.Sprint(len(lines)) + "):\n")
			for _, l := range lines {
				b.WriteString("      | " + l + "\n")
			}
			break
		}
	}
	b.WriteString("\n")
	return b.String()
}

// notice is what a person sees on the console when the desktop did not start. It says what
// happened, that this is the same installer, and that nothing is wrong with their machine
// beyond the graphics.
const notice = `
================================================================================
  Runink River — the desktop did not start on this computer.

  That is usually the graphics driver, and it does not stop you installing.
  Starting the installer here instead. It is the same installer, with the same
  steps and the same result.
================================================================================

`

func main() {
	// 150 s was not enough and it cost the owner a failed install. This is a CEILING, not a
	// delay: the loop polls every 3 s and returns the instant a session appears, so raising it
	// costs exactly nothing on a machine where the desktop comes up normally. What it buys is a
	// machine where the desktop is merely SLOW -- Plasma starting off a USB stick reads a lot of
	// small files out of a compressed squashfs, which is an order of magnitude slower than the
	// SSD-backed ISO a QEMU test reads from. Falling back early does not just give up early, it
	// TAKES THE CONSOLE AWAY from a desktop that was still coming, and then the kiosk owns a
	// display the compositor wanted.
	timeout := flag.Duration("timeout", 6*time.Minute, "how long to wait for a graphical session")
	every := flag.Duration("poll", 3*time.Second, "how often to look")
	ttyPath := flag.String("tty", "/dev/tty1", "console to fall back on")
	check := flag.Bool("check", false, "report and exit; start nothing")
	// --report: print the graphics state and exit. The same block the fallback puts on the
	// console, available on demand, so a machine that DID reach a desktop can still be asked
	// what its display hardware looks like without reproducing a failure first.
	report := flag.Bool("report", false, "print the graphics state and exit")
	flag.Parse()

	if *report {
		fmt.Print(graphicsReport())
		return
	}

	if *check {
		if p := waitForSession(0, *every, nil); p != "" {
			fmt.Printf("river-live-fallback: a graphical session is present (%s); nothing to do\n", p)
			return
		}
		k, t := resolve(kioskBin), resolve(textBin)
		fmt.Printf("river-live-fallback: no graphical session; would fall back on %s\n", *ttyPath)
		for _, b := range []struct{ name, path string }{{kioskBin, k}, {textBin, t}} {
			if b.path == "" {
				fmt.Printf("  %-16s NOT FOUND in %v or $PATH — this fallback CANNOT run\n", b.name, binDirs)
			} else {
				fmt.Printf("  %-16s %s\n", b.name, b.path)
			}
		}
		if k == "" && t == "" {
			fmt.Printf("river-live-fallback: neither tool is reachable; a machine with no desktop would reach NO installer\n")
			os.Exit(1)
		}
		return
	}

	// Open the console FIRST, so the wait can report progress on it. Opening it changes
	// nothing for a desktop that does come up: tty1 is a text console sitting behind the
	// graphical session, and writing to it is harmless.
	var progress *os.File
	if f, err := os.OpenFile(*ttyPath, os.O_WRONLY, 0); err == nil {
		progress = f
		defer progress.Close()
	}
	if p := waitForSession(*timeout, *every, func(el time.Duration) {
		if progress != nil {
			fmt.Fprintf(progress, "  Runink River: waiting for the desktop (%s of %s)...\n",
				el, *timeout)
		}
	}); p != "" {
		fmt.Printf("river-live-fallback: a graphical session appeared (%s); nothing to do\n", p)
		return
	}
	fmt.Fprintf(os.Stderr, "river-live-fallback: no graphical session after %s; falling back on %s\n",
		*timeout, *ttyPath)

	tty, err := os.OpenFile(*ttyPath, os.O_RDWR, 0)
	if err != nil {
		// Without a console there is nothing a person could use anyway; say so where
		// the boot log will keep it.
		fmt.Fprintf(os.Stderr, "river-live-fallback: cannot open %s: %v\n", *ttyPath, err)
		os.Exit(1)
	}
	defer tty.Close()
	say(tty, notice)
	// WHY the desktop did not start, on the screen, now. The owner's machine has no serial
	// port, no network (it reports no interface besides loopback) and no desktop -- so every
	// previous failure was diagnosed from a photograph of a console that said only THAT it had
	// failed. One screen with the graphics state on it turns the next report into an answer
	// instead of another round trip. It is six cheap reads and it cannot fail the boot.
	say(tty, graphicsReport())
	activate(*ttyPath)

	// The console kiosk first: it draws the same graphical installer straight through
	// KMS, with no desktop and no display manager, so a machine whose compositor failed
	// can still get the full UI. Give it the display before asking it to draw on one.
	releaseDisplay(tty)
	if err := run(kioskBin, tty, "--vt", vtOf(*ttyPath)); err == nil {
		return
	} else {
		fmt.Fprintf(os.Stderr, "river-live-fallback: %s did not start (%v); using the text installer\n", kioskBin, err)
		say(tty, "\n  The graphical installer could not start either. Using the text installer.\n\n")
	}

	if err := run(textBin, tty); err != nil {
		fmt.Fprintf(os.Stderr, "river-live-fallback: %s failed: %v\n", textBin, err)
		os.Exit(1)
	}
}

const (
	kioskBin = "river-kiosk"
	textBin  = "runink-install"
)

// binDirs are searched BEFORE $PATH. This program is started by /etc/s6/rc.local, by absolute
// path, in the init context -- which has a minimal environment and, on this image, no
// /usr/local/bin in $PATH. Both tools it hands over to live there. Resolving them through
// exec.LookPath alone meant every fallback failed with "executable file not found", on a
// machine that had both binaries sitting in /usr/local/bin, and the operator was left at a
// bare login prompt with no installer at all.
var binDirs = []string{"/usr/local/bin", "/usr/bin", "/bin"}

// resolve returns a runnable absolute path for a tool, or "" when there is none. The explicit
// directories come first precisely because $PATH cannot be trusted here.
func resolve(name string) string {
	for _, d := range binDirs {
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// say writes a message to the console. A console that cannot be written to is reported and
// then ignored: the installer still has to start, and stderr goes to the boot log either way.
func say(tty *os.File, msg string) {
	if _, err := io.WriteString(tty, msg); err != nil {
		fmt.Fprintf(os.Stderr, "river-live-fallback: cannot write to the console: %v\n", err)
	}
}

// run starts a command with the console as its terminal and waits for it. A missing binary
// is an error like any other, so the caller moves on to the next fallback.
func run(name string, tty *os.File, args ...string) error {
	bin := resolve(name)
	if bin == "" {
		return fmt.Errorf("%s not found in %v or $PATH", name, binDirs)
	}
	cmd := exec.Command(bin, args...) // #nosec G204 -- fixed tool names from this file; arguments are passed as argv, never through a shell
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.Env = withBinDirs(os.Environ())
	return cmd.Run()
}

// withBinDirs puts binDirs at the FRONT of the child's PATH.
//
// Resolving our own two tools by absolute path was only half the problem. runink-install is a
// shell script that looks ITS OWN helpers up with `command -v` -- installer/lib/hwplan.sh for
// river-hwprobe and river-plan, runink-install itself for river-netsetup -- and a child inherits
// this process's environment, which, started from /etc/s6/rc.local, carries an init-context
// $PATH without /usr/local/bin. runink-installer installs every one of those helpers there.
//
// On the owner's hardware, 2026-10-03, that produced a text installer reporting
// "river-hwprobe not found (runink-installer package missing from the live image?)" and
// "river-netsetup is not on this image" on a medium where the package was present and COMPLETE
// -- all eleven binaries in /usr/local/bin, verified in the shipped package. The diagnostic
// blamed packaging because `command -v` cannot tell "absent" from "not on $PATH", and that sent
// the investigation after a missing package that was never missing.
//
// Fixed here rather than in each script on purpose: this process is the one that knows it was
// started from an init context, and it is the boundary where that context leaks into everything
// it runs.
func withBinDirs(env []string) []string {
	want := strings.Join(binDirs, ":")
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			found = true
			if v == "" {
				kv = "PATH=" + want
			} else {
				kv = "PATH=" + want + ":" + v
			}
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+want)
	}
	return out
}

// releaseDisplay stops the display manager, because the kiosk cannot draw while it is running.
//
// THE KIOSK IS A KMS CLIENT. It renders through wpe-display-drm, straight to /dev/dri, which
// means it has to become DRM MASTER. The kernel allows exactly one master per device, and a
// second client asking for it is refused -- measured on this project's own hardware,
// 2026-10-03: DRM_IOCTL_SET_MASTER returns EACCES ("Permission denied") while a compositor
// holds the device.
//
// A DISPLAY MANAGER HOLDS THAT DEVICE EVEN WHEN IT HAS PRODUCED NOTHING. On the owner's laptop
// SDDM was up for 836 seconds with one pid and no restarts, and no session ever appeared: no
// Wayland socket, no X socket, no plasmashell. From the outside that looks like an idle
// service. To the kernel it is the owner of the screen. So when the fallback handed over to the
// kiosk, the web view asked for DRM master, was refused, and aborted in under a second -- five
// times, deterministically, which is exactly the shape of a resource that is held rather than a
// race that is lost.
//
// Stopping it here is safe BECAUSE OF WHERE WE ARE. This code runs only after waitForSession
// has looked for a compositor and found none; the display manager has already failed to produce
// one. It is not a working desktop being killed, it is a service holding a device it never used.
// If the kiosk then fails too, the text installer gets a console that nothing else is driving.
//
// Best effort: a medium without s6-rc still reaches the installer, just without the display.
func releaseDisplay(tty *os.File) {
	if _, err := exec.LookPath("s6-rc"); err != nil {
		return
	}
	// "sddm" is the bundle name this image enables (iso-profiles/river/profile.yaml services,
	// re-asserted by installer/lib/80-enable-s6.sh), not the sddm-srv servicedir.
	cmd := exec.Command("s6-rc", "-d", "change", "sddm") // #nosec G204 -- fixed tool and arguments
	cmd.Env = withBinDirs(os.Environ())
	if err := cmd.Run(); err != nil {
		// Not fatal: it may already be down, or not be this image's display manager.
		fmt.Fprintf(os.Stderr, "river-live-fallback: could not stop the display manager (%v); "+
			"the kiosk may not get the display\n", err)
		return
	}
	say(tty, "  Freeing the display from the display manager, which never started a session.\n")
	// The device is not released the instant the service is told to stop. A short wait costs
	// nothing next to five failed view starts.
	time.Sleep(2 * time.Second)
}

// activate brings the console to the front, so the message is on the screen the person is
// looking at rather than behind a splash, and then QUIETENS it so the installer is the only
// thing drawing on it. Best effort throughout: a medium without these tools still works.
func activate(ttyPath string) {
	if _, err := exec.LookPath("chvt"); err == nil {
		_ = exec.Command("chvt", vtOf(ttyPath)).Run() // #nosec G204 -- a fixed tool name; the argument is the console number, passed as argv, never through a shell
	}
	quieten(ttyPath)
}

// quieten stops everything else writing over the installer.
//
// WHY. The installer here is a full-screen dialog program on a text console, and on a live
// medium that console is also where the kernel prints and where boot-time services log. On the
// owner's hardware, 2026-10-03, the result was an ERASE confirmation and a ZFS pool-name prompt
// being overwritten faster than a person could read them -- the installer was running and
// unusable, which looks far worse than it not starting. A crash-looping web view supplied most
// of that noise and is fixed separately, but it was only the loudest writer, not the only one:
// kernel messages, rc.local, the firewall and the installer backend all land here too.
//
// So the console is quietened rather than each writer silenced one at a time, which is the
// difference between fixing this failure and fixing this CLASS of failure. Nothing is lost:
// kernel messages stay in dmesg and the services keep logging to s6, so the evidence is still
// there for anyone diagnosing afterwards. It is only the screen that stops being shared.
func quieten(ttyPath string) {
	// Kernel messages to the console: off. They remain readable with `dmesg`.
	if _, err := exec.LookPath("dmesg"); err == nil {
		_ = exec.Command("dmesg", "--console-off").Run()
	}
	// The kernel's own printk level, for anything that bypasses the above. 1 = emergencies
	// only, so a genuine panic still reaches the person in front of the machine.
	if f, err := os.OpenFile("/proc/sys/kernel/printk", os.O_WRONLY, 0); err == nil {
		_, _ = f.WriteString("1 4 1 7\n")
		_ = f.Close()
	}
	// setterm's console messages, on the console we are about to use.
	if _, err := exec.LookPath("setterm"); err == nil {
		c := exec.Command("setterm", "--msg", "off") // #nosec G204 -- fixed arguments
		if tty, err := os.OpenFile(ttyPath, os.O_WRONLY, 0); err == nil {
			c.Stdout = tty
			_ = c.Run()
			_ = tty.Close()
		}
	}
	// Start from a clean screen, so whatever was already printed is not mistaken for part of
	// the installer.
	if tty, err := os.OpenFile(ttyPath, os.O_WRONLY, 0); err == nil {
		_, _ = tty.WriteString("\033[H\033[2J\033[3J")
		_ = tty.Close()
	}
}

// vtOf turns /dev/tty1 into "1". Anything unexpected falls back to the first console.
func vtOf(ttyPath string) string {
	base := filepath.Base(ttyPath)
	if n := len("tty"); len(base) > n && base[:n] == "tty" {
		return base[n:]
	}
	return "1"
}
