// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

// watch.go -- a compositor that is alive but never draws, the text-installer option, and getting
// the console back from a session that took it.
//
// THE FAILURE THIS FILE EXISTS FOR (owner's photographs, 2026-11-27): the live session is
// greetd's initial_session running `weston`. weston 15 opens the display through libseat and
// nothing else; libseat tried seatd (no daemon, no socket), then logind, and the log simply
// stopped. The process stayed alive for ever. No Wayland socket appeared, so nothing "came up";
// weston never EXITED, so nothing "failed". The fallback chain only moves on when a compositor
// appears or the 6 minute ceiling passes, and meanwhile greetd had put VT 1 into graphics mode
// with the keyboard off, so the person saw a dead screen, and Ctrl+Alt+F2 did nothing.
//
// Three things follow, and each is here:
//
//   - a compositor that is alive for compositorBudget with no socket is HUNG, not slow, and the
//     fallback acts on it at once instead of waiting out the ceiling (compositorWatch);
//   - taking the console back means more than starting something on it: the session left the
//     VT in KD_GRAPHICS with the keyboard off (resetConsole);
//   - the text installer must be reachable WITHOUT any of seatd, logind, DRM or a compositor, by
//     a boot-menu entry, the way every other live medium offers one (textRequested).

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// compositorBudget is how long a compositor may be alive with no Wayland socket before it is
// called hung. weston creates its socket as soon as its backend is open; what comes before is
// reading a few megabytes of its own libraries off the medium and probing the display, which
// takes seconds even from a slow stick. 60 s is an order of magnitude over that, and still short
// enough that a person has not given up and pulled the stick. It is NOT measured on the
// owner's hardware (there is no log of a healthy start from it yet): the number is a bound with
// margin, and river-live-fallback --report on a machine that reaches the desktop shows when the
// socket appeared, so it can be tightened from evidence.
var compositorBudget = 60 * time.Second

// compositorWatch tracks one compositor across the polls of waitForSession.
type compositorWatch struct {
	since time.Time // when it was first seen alive with no socket; zero when it is not
}

// observe records one poll and reports whether the compositor is now hung: alive, with no
// socket, for at least compositorBudget. A compositor that is gone, or that has made its socket,
// resets the clock, so a restart by greetd starts a new budget rather than inheriting the old.
func (w *compositorWatch) observe(now time.Time, alive, socket bool) bool {
	if !alive || socket {
		w.since = time.Time{}
		return false
	}
	if w.since.IsZero() {
		w.since = now
		return false
	}
	return now.Sub(w.since) >= compositorBudget
}

// textRequested reports whether the kernel command line asks for the text installer: the
// "Text installer (no graphics)" GRUB entry passes river.text (or river.text=1, =yes, =on,
// =true). Anything else after the = (0, no, off) means no. It MUST agree with the greetd
// service's own check (build/pkgbuilds/runink-greetd-s6): one declines to start greetd, the other
// opens the text installer, and a disagreement leaves a medium with neither. A dotted name is
// ignored by the kernel as a module parameter, so it is safe to pass; userspace reads
// /proc/cmdline itself.
func textRequested(cmdline string) bool {
	for _, f := range strings.Fields(cmdline) {
		if f == "river.text" {
			return true
		}
		if v, ok := strings.CutPrefix(f, "river.text="); ok {
			switch strings.ToLower(v) {
			case "1", "yes", "on", "true":
				return true
			}
		}
	}
	return false
}

// readCmdline returns the kernel command line, or "" when /proc is not there.
func readCmdline() string {
	b, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return ""
	}
	return string(b)
}

// compositorNames are the processes whose presence without a socket means "a compositor that
// has not come up". The live medium's session is weston; the installed machine's greeter is cage,
// but this program runs on the live medium only.
var compositorNames = []string{"weston"}

// anyAlive reports whether a process with one of these exact names is running.
func anyAlive(names []string) bool {
	for _, n := range names {
		if pgrep(n) {
			return true
		}
	}
	return false
}

// pidsOf returns the pids of processes with exactly this name, read from /proc.
func pidsOf(name string) []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		pid := 0
		for _, c := range e.Name() {
			if c < '0' || c > '9' {
				pid = -1
				break
			}
			pid = pid*10 + int(c-'0')
		}
		if pid <= 0 {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err == nil && strings.TrimSpace(string(b)) == name {
			out = append(out, pid)
		}
	}
	return out
}

// killCompositors stops every process in names: SIGTERM, a short wait, then SIGKILL for what is
// left. A compositor stuck in a library call does not always honour SIGTERM. It reports how many
// processes it signalled. Best effort: a process that cannot be killed (uninterruptible sleep)
// is left, and the chain moves on regardless.
func killCompositors(names []string) int {
	n := 0
	for _, name := range names {
		for _, pid := range pidsOf(name) {
			if syscall.Kill(pid, syscall.SIGTERM) == nil {
				n++
			}
		}
	}
	for i := 0; i < 6 && anyAlive(names); i++ {
		time.Sleep(500 * time.Millisecond)
	}
	for _, name := range names {
		for _, pid := range pidsOf(name) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	return n
}

// Console ioctls (linux/kd.h and linux/vt.h). The values are part of the kernel's ABI.
const (
	kdSetMode  = 0x4B3A // KDSETMODE
	kdText     = 0x00   // KD_TEXT: the kernel draws the console
	kdSkbMode  = 0x4B45 // KDSKBMODE
	kUnicode   = 0x03   // K_UNICODE: the keyboard produces characters again
	vtSetMode  = 0x5602 // VT_SETMODE
	vtAuto     = 0x00   // VT_AUTO: the kernel switches VTs, nobody is asked
	vtActivate = 0x5606 // VT_ACTIVATE
)

// vtModeT is struct vt_mode: mode, waitv, relsig, acqsig, frsig.
type vtModeT struct {
	mode   uint8
	waitv  uint8
	relsig int16
	acqsig int16
	frsig  int16
}

func ioctl(fd, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// resetConsole gives a virtual console back to the kernel after a session that took it.
//
// A display manager or compositor puts its VT in KD_GRAPHICS (the kernel stops drawing text on
// it) and turns the keyboard off (the compositor reads the devices itself), and VT_PROCESS mode
// makes the kernel ask it before switching away. Killed, it never undoes any of that. The result
// is the owner's photograph: a console that prints nothing and a keyboard that does not answer
// Ctrl+Alt+F2. Putting the console back in text mode, the keyboard in Unicode mode and the VT in
// automatic switching is what makes everything the fallback prints, and every key a person
// presses, work. Best effort, and a no-op on a path that is not a console.
func resetConsole(ttyPath string) {
	f, err := os.OpenFile(ttyPath, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer f.Close()
	fd := f.Fd()
	_ = ioctl(fd, kdSetMode, kdText)
	_ = ioctl(fd, kdSkbMode, kUnicode)
	vm := vtModeT{mode: vtAuto}
	_ = ioctl(fd, vtSetMode, uintptr(unsafe.Pointer(&vm))) // #nosec G103 -- the kernel's struct vt_mode, passed by pointer as the ioctl requires
	if n := vtOf(ttyPath); n != "" {
		v := uintptr(0)
		for _, c := range n {
			if c < '0' || c > '9' {
				return
			}
			v = v*10 + uintptr(c-'0')
		}
		_ = ioctl(fd, vtActivate, v)
	}
}

// hungProbe is what waitForSession returns when a compositor is alive but silent: the fallback
// treats it like "no session after the timeout", only sooner, and first takes the console back.
const hungProbe = "hung compositor"

// The two facts the hang check reads, as variables so a test can set them.
var (
	compositorAlive = func() bool { return anyAlive(compositorNames) }
	socketPresent   = func() bool { return globAny("/run/user/*/wayland-[0-9]") }
)

// sessionProcs are everything the live session's display manager may have started and that
// would keep the display or the VT: the compositor and the greeter's pair.
var sessionProcs = []string{"weston", "cage", "gtkgreet"}

// hungNotice is what a person sees when the compositor was alive but never drew.
const hungNotice = `
================================================================================
  Runink River — the graphical session started but never put anything on screen.

  The display could not be opened (no seat for the compositor). That is a
  software or driver problem, not a fault in your computer, and it does not
  stop you installing. The console has been taken back. Starting the installer
  here instead: the same installer, with the same steps and the same result.
================================================================================

`

// textBanner is the one screen the text installer opens with: why this is a text console, how
// to get to the graphical installer instead, and where the hardware report went. saved is
// saveReport's own sentence (where on the stick, or why it could not be saved).
func textBanner(saved string) string {
	return `
================================================================================
  Runink River — text installer

  This is the plain text console. It needs no graphics, no seat service and no
  desktop, so it works on any computer that can boot this stick.

  To use the graphical installer instead:
    * restart and choose "Runink River — Install / Live" (or "safe graphics"
      if that blanks the screen); or
    * press Alt+F2 for a shell and run:  sudo river-kiosk

  Hardware report (what this computer has, and why graphics may have failed):
    ` + saved + `
    It is also in /run/river-report.txt. Send it with a bug report.
================================================================================

`
}

// savedNote carries saveReport's one-line answer out of the bounded goroutine that writes the
// report, without a data race if that goroutine outlives its budget.
type savedNote struct {
	mu sync.Mutex
	s  string
}

func (n *savedNote) set(s string) { n.mu.Lock(); n.s = s; n.mu.Unlock() }
func (n *savedNote) get() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.s == "" {
		return "(the report was not written: it did not finish in time)"
	}
	return n.s
}

// runReportPath is where the full report is also left, so it can be read from any shell on the
// medium even when no stick could be written.
const runReportPath = "/run/river-report.txt"

// saveFullReport writes the full hardware report to the stick and to runReportPath and returns
// the one line for the person. Both writes are best effort.
func saveFullReport(note *savedNote) {
	kmsg := readKmsg("/dev/kmsg", 200)
	full := hardwareReport("/", kmsg, true)
	_ = os.WriteFile(runReportPath, []byte(full), 0o600)
	note.set(saveReport("river-report.txt", full))
}

// runTextMode is the text installer by request: the console is reset (a session may have left
// it in graphics mode), one screen says how to reach the graphical installer and where the
// report went, then the text installer runs on the console. It needs nothing but the kernel's
// console: no seatd, no logind, no DRM, no compositor.
func runTextMode(ttyPath string, pause time.Duration) int {
	tty, err := os.OpenFile(ttyPath, os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "river-live-fallback: cannot open %s: %v\n", ttyPath, err)
		return 1
	}
	defer tty.Close()
	resetConsole(ttyPath)
	activate(ttyPath)
	var note savedNote
	bounded(reportBudget, func() { saveFullReport(&note) })
	say(tty, textBanner(note.get()))
	// Long enough to read or photograph, short enough that someone who asked for the text
	// installer is not made to wait: 15 s at most whatever --report-pause says.
	if pause > 15*time.Second {
		pause = 15 * time.Second
	}
	if pause > 0 {
		say(tty, fmt.Sprintf("  The installer starts in %s.\n\n", pause))
		time.Sleep(pause)
	}
	return textLoop(tty)
}

// textLoop runs the text installer on the console and runs it again when it returns: the
// console has no getty (tty1 is the installer's), so an installer that ended would leave a
// blank screen. A keystroke starts it again; Alt+F2 is a shell.
func textLoop(tty *os.File) int {
	rd := bufio.NewReader(tty)
	for {
		if err := run(textBin, tty); err != nil {
			fmt.Fprintf(os.Stderr, "river-live-fallback: %s: %v\n", textBin, err)
			say(tty, fmt.Sprintf("\n  The text installer stopped: %v\n", err))
		}
		say(tty, "\n  Press Enter to run the text installer again, or Alt+F2 for a shell.\n")
		if _, err := rd.ReadString('\n'); err != nil {
			return 1
		}
	}
}

// releaseSeat stops the live medium's seat service once its session is gone, so the console
// kiosk's own private seatd (seatd-launch) starts on a free socket instead of beside a daemon
// that still holds the devices. Best effort: a medium without s6-rc, or without the service,
// loses nothing.
func releaseSeat() {
	if _, err := exec.LookPath("s6-rc"); err != nil {
		return
	}
	cmd := exec.Command("s6-rc", "-d", "change", "seatd") // #nosec G204 -- fixed tool and arguments
	cmd.Env = withBinDirs(os.Environ())
	_ = cmd.Run()
}
