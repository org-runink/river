// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A compositor alive with no socket is hung once compositorBudget has passed, and not before.
// One that makes its socket, or that is gone, resets the clock: a restart by greetd gets a new
// budget instead of inheriting the old one, and a healthy compositor is never called hung.
func TestCompositorWatch(t *testing.T) {
	t0 := time.Unix(1000, 0)
	var w compositorWatch
	if w.observe(t0, true, false) {
		t.Fatal("hung on the very first sighting")
	}
	if w.observe(t0.Add(compositorBudget-time.Second), true, false) {
		t.Fatal("hung before the budget passed")
	}
	if !w.observe(t0.Add(compositorBudget), true, false) {
		t.Fatal("not hung when alive with no socket for the whole budget")
	}

	// A compositor that made its socket is left alone, however long it has been alive.
	w = compositorWatch{}
	w.observe(t0, true, false)
	if w.observe(t0.Add(10*compositorBudget), true, true) {
		t.Fatal("a compositor with a socket was called hung")
	}
	// ...and it does not inherit the clock from before the socket appeared.
	if w.observe(t0.Add(10*compositorBudget+time.Second), true, false) {
		t.Fatal("the budget was not restarted after the socket appeared")
	}

	// A compositor that exits resets the clock too.
	w = compositorWatch{}
	w.observe(t0, true, false)
	w.observe(t0.Add(compositorBudget/2), false, false)
	if w.observe(t0.Add(compositorBudget), true, false) {
		t.Fatal("the budget survived the compositor exiting")
	}
}

// waitForSession returns the hung verdict for a compositor that is alive and silent, and does not
// for one that is not alive. The display probes are stubbed off so only the hang check can answer.
func TestWaitForSessionHungCompositor(t *testing.T) {
	oldDriven, oldAlive, oldSock, oldBudget := driven, compositorAlive, socketPresent, compositorBudget
	defer func() {
		driven, compositorAlive, socketPresent, compositorBudget = oldDriven, oldAlive, oldSock, oldBudget
	}()
	driven = func() bool { return false }
	compositorBudget = 60 * time.Millisecond

	compositorAlive = func() bool { return true }
	socketPresent = func() bool { return false }
	if got := waitForSession(5*time.Second, 5*time.Millisecond, nil); got != hungProbe {
		t.Fatalf("an alive, silent compositor: waitForSession = %q, want %q", got, hungProbe)
	}

	compositorAlive = func() bool { return false }
	if got := waitForSession(300*time.Millisecond, 5*time.Millisecond, nil); got != "" {
		t.Fatalf("no compositor at all: waitForSession = %q, want the plain timeout (empty)", got)
	}

	compositorAlive = func() bool { return true }
	socketPresent = func() bool { return true }
	if got := waitForSession(300*time.Millisecond, 5*time.Millisecond, nil); got == hungProbe {
		t.Fatal("a compositor with a socket was called hung")
	}
}

func TestTextRequested(t *testing.T) {
	for cmdline, want := range map[string]bool{
		"BOOT_IMAGE=/boot/vmlinuz label=RIVER overlay=livefs": false,
		"label=RIVER overlay=livefs nomodeset river.text=1":   true,
		"label=RIVER river.text overlay=livefs":               true,
		"river.text=yes":                                      true,
		"river.text=ON":                                       true,
		"river.text=0":                                        false,
		"river.text=no":                                       false,
		"river.textual=1":                                     false,
		"xriver.text=1":                                       false,
		"":                                                    false,
		"label=RIVER river.edition=river nomodeset river.text=1 plymouth.enable=0": true,
	} {
		if got := textRequested(cmdline); got != want {
			t.Errorf("textRequested(%q) = %v, want %v", cmdline, got, want)
		}
	}
}

// The greetd service declines to start on exactly the command lines textRequested accepts: one
// without the other leaves a medium with a compositor and no text installer, or the reverse.
// The package's own pattern is read from the PKGBUILD and checked against both.
func TestGreetdServiceAgreesWithTextRequested(t *testing.T) {
	b, err := os.ReadFile("../../build/pkgbuilds/runink-greetd-s6/PKGBUILD")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"(^| )river[.]text(=(1|yes|on|true))?( |$)" /proc/cmdline`) {
		t.Fatal("the greetd service's river.text check changed; update textRequested and this test together")
	}
	for _, tc := range []struct {
		cmd  string
		want bool
	}{
		{"nomodeset river.text=1", true}, {"river.text", true}, {"river.text=no", false}, {"river.textual", false},
	} {
		if got := textRequested(tc.cmd); got != tc.want {
			t.Errorf("textRequested(%q) = %v", tc.cmd, got)
		}
	}
}

// killCompositors really signals a process by name and reports it; a name with no process is
// zero. The victim is a copy of sleep under a name nothing else has, so the test can never touch
// another process on the machine.
func TestKillCompositors(t *testing.T) {
	if n := killCompositors([]string{"rv-no-such-process"}); n != 0 {
		t.Fatalf("signalled %d processes for a name nobody has", n)
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatalf("no sleep to copy: %v", err)
	}
	b, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "rv-victim")
	if err := os.WriteFile(victim, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(victim, "60")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot run a copy of sleep (a multi-call binary?): %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// Wait for /proc to show the new name before asking.
	for i := 0; i < 100 && !pgrep("rv-victim"); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !pgrep("rv-victim") {
		_ = cmd.Process.Kill()
		t.Skip("the copy of sleep did not keep its name in /proc (a multi-call binary)")
	}
	if n := killCompositors([]string{"rv-victim"}); n != 1 {
		t.Fatalf("killCompositors reported %d, want 1", n)
	}
	select {
	case err := <-done:
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("the victim did not die from a signal: %v", err)
		}
		if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() {
			t.Fatalf("the victim was not signalled: %v", err)
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the victim was still alive after killCompositors")
	}
}

// resetConsole is a no-op on something that is not a console: no panic, no change to the file.
func TestResetConsoleOnANonConsole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tty1")
	if err := os.WriteFile(p, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	resetConsole(p)
	resetConsole(filepath.Join(t.TempDir(), "does-not-exist"))
	if b, _ := os.ReadFile(p); string(b) != "keep" {
		t.Fatalf("resetConsole changed a regular file: %q", b)
	}
}

// The one screen the text console opens with says how to reach the graphical installer and where
// the report went. Each fact is asserted, because the screen exists to carry them.
func TestTextBannerCarriesTheFacts(t *testing.T) {
	saved := "Report saved to the stick as /mnt/river-report.txt - plug the stick into another computer."
	b := textBanner(saved)
	for _, want := range []string{
		"text installer",             // what this is
		"Try or Install",             // the menu entry for graphics
		"river-kiosk",                // the command to try graphics from a shell
		"Alt+F2",                     // how to get a shell
		saved,                        // where the report went, verbatim
		runReportPath[len("/run/"):], // the copy that is always there
		"no seat service",            // why it works on any machine
	} {
		if !strings.Contains(b, want) {
			t.Errorf("the text installer's opening screen lacks %q:\n%s", want, b)
		}
	}
	if n := strings.Count(b, "\n"); n > 24 {
		t.Errorf("the opening screen is %d lines; it has to fit one 24-line screen", n)
	}
}

// textLoop runs the installer, then asks before running it again, and ends when the console
// cannot be read. The installer is a stub that records each run. The "console" is a regular file:
// it is written to by the child and, at EOF, ends the loop after one run.
func TestTextLoopRunsTheInstaller(t *testing.T) {
	dir := t.TempDir()
	rec := filepath.Join(dir, "ran")
	stub := filepath.Join(dir, textBin)
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho ran >> "+rec+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := binDirs
	binDirs = []string{dir}
	defer func() { binDirs = old }()

	tty, err := os.CreateTemp(dir, "console")
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	if code := textLoop(tty); code != 1 {
		t.Fatalf("textLoop on a console that cannot be read = %d, want 1", code)
	}
	if b, _ := os.ReadFile(rec); strings.Count(string(b), "ran") != 1 {
		t.Fatalf("the text installer ran %q, want once", b)
	}
	out, _ := os.ReadFile(tty.Name())
	if !strings.Contains(string(out), "Press Enter to run the text installer again") {
		t.Errorf("no prompt after the installer ended: %q", out)
	}
}

// A missing installer is reported on the console and does not end the loop by itself: the person
// is still told what happened and can reach a shell.
func TestTextLoopReportsAMissingInstaller(t *testing.T) {
	old := binDirs
	binDirs = []string{t.TempDir()}
	defer func() { binDirs = old }()
	t.Setenv("PATH", "")
	tty, err := os.CreateTemp(t.TempDir(), "console")
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()
	textLoop(tty)
	out, _ := os.ReadFile(tty.Name())
	if !strings.Contains(string(out), "The text installer stopped") {
		t.Errorf("a missing installer was not reported: %q", out)
	}
}

// The hardware report names the seat provider and the compositor, so the next failure names itself.
func TestReportSessionNamesWhatIsMissing(t *testing.T) {
	root := t.TempDir()
	write := func(p, body string) {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing: no seatd, no elogind, no socket, no compositor.
	got := strings.Join(rpt{root: root}.session(), "\n")
	for _, want := range []string{"NO SOCKET at /run/seatd.sock", "seatd        NOT RUNNING", "elogind      NOT RUNNING", "NO SOCKET under /run/user"} {
		if !strings.Contains(got, want) {
			t.Errorf("an empty system's session report lacks %q:\n%s", want, got)
		}
	}
	// weston alive and waiting, seatd absent: the owner's 2026-11-27 state.
	write("proc/4242/comm", "weston\n")
	write("proc/4242/wchan", "do_sys_poll\n")
	write("proc/4242/syscall", "7 0x7ffd 0x1 0xffffffff\n")
	got = strings.Join(rpt{root: root}.session(), "\n")
	for _, want := range []string{"weston", "pid 4242", "waiting in do_sys_poll", "syscall 7"} {
		if !strings.Contains(got, want) {
			t.Errorf("a hung weston is not named in the session report (want %q):\n%s", want, got)
		}
	}
	// The healthy state reads as healthy.
	write("run/seatd.sock", "")
	write("proc/100/comm", "seatd\n")
	write("proc/101/comm", "elogind\n")
	write("run/user/1000/wayland-0", "")
	got = strings.Join(rpt{root: root}.session(), "\n")
	for _, want := range []string{"socket /run/seatd.sock present", "running, pid 100", "running, pid 101", "socket "} {
		if !strings.Contains(got, want) {
			t.Errorf("the healthy session report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "NO SOCKET") {
		t.Errorf("the healthy report still says NO SOCKET:\n%s", got)
	}
}
