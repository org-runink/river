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
func waitForSession(timeout, every time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		for _, p := range probes() {
			if p.find() {
				return p.name
			}
		}
		if !time.Now().Before(deadline) {
			return ""
		}
		time.Sleep(every)
	}
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
	timeout := flag.Duration("timeout", 150*time.Second, "how long to wait for a graphical session")
	every := flag.Duration("poll", 3*time.Second, "how often to look")
	ttyPath := flag.String("tty", "/dev/tty1", "console to fall back on")
	check := flag.Bool("check", false, "report and exit; start nothing")
	flag.Parse()

	if *check {
		if p := waitForSession(0, *every); p != "" {
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

	if p := waitForSession(*timeout, *every); p != "" {
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
	activate(*ttyPath)

	// The console kiosk first: it draws the same graphical installer straight through
	// KMS, with no desktop and no display manager, so a machine whose compositor failed
	// can still get the full UI.
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

// activate brings the console to the front, so the message is on the screen the person is
// looking at rather than behind a splash. Best effort: a medium without chvt still works,
// the text just waits on that console.
func activate(ttyPath string) {
	if _, err := exec.LookPath("chvt"); err != nil {
		return
	}
	_ = exec.Command("chvt", vtOf(ttyPath)).Run() // #nosec G204 -- a fixed tool name; the argument is the console number, passed as argv, never through a shell
}

// vtOf turns /dev/tty1 into "1". Anything unexpected falls back to the first console.
func vtOf(ttyPath string) string {
	base := filepath.Base(ttyPath)
	if n := len("tty"); len(base) > n && base[:n] == "tty" {
		return base[n:]
	}
	return "1"
}
