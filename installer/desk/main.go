// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// river-desk — the small session jobs a DEsk session on Runink River needs, where the program
// DEsk's session helper would start is written in Python (udiskie, blueman,
// system-config-printer) or is not packaged (poweralertd, batsignal):
//
//	river-desk automount   mount removable media for the session's user as it is put in (and
//	                       what is already in at login), through udisks2: `gdbus monitor` on
//	                       UDisks2's object manager, `udisksctl mount`, a letter in DEsk's post
//	                       box (notify-send) for each
//	river-desk battery     a letter when a battery that is discharging falls to 15% and again at
//	                       5% (read from /sys/class/power_supply every minute)
//	river-desk bluetooth   bluetuith (Bluetooth devices) in a foot terminal; also as river-bluetooth
//	river-desk printers    CUPS's own pages, http://localhost:631/, in the default browser; also as
//	                       river-printers
//
// automount and battery start from /etc/xdg/autostart (OnlyShowIn=DEsk); bluetooth and printers
// are the programs DEsk's Shell Settings rows and its Bluetooth lever open (the runink-desk
// fork's patch 0005), which run a program with no arguments, hence the two names.
//
// Exit codes: 0 ok; 1 error; 2 usage. Standard library only; nothing here needs root.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

func main() {
	args := os.Args[1:]
	switch filepath.Base(os.Args[0]) {
	case "river-bluetooth":
		args = append([]string{"bluetooth"}, args...)
	case "river-printers":
		args = append([]string{"printers"}, args...)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, args, os.Stderr))
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: river-desk automount|battery|bluetooth|printers")
		return 2
	}
	var err error
	switch args[0] {
	case "automount":
		err = automount(ctx, execRunner{})
	case "battery":
		err = battery(ctx, "/sys/class/power_supply", notify)
	case "bluetooth":
		err = become("foot", "--title=Bluetooth", "bluetuith")
	case "printers":
		err = become("xdg-open", printersURL)
	default:
		fmt.Fprintf(stderr, "river-desk: unknown job %q\n", args[0])
		return 2
	}
	if err != nil && ctx.Err() == nil {
		fmt.Fprintln(stderr, "river-desk:", err)
		return 1
	}
	return 0
}

// printersURL is CUPS's web interface, on the loopback address cupsd listens on by default.
const printersURL = "http://localhost:631/"

// programs is every program river-desk starts. Anything else is refused, so a name that
// reaches exec here is always one of these, and arguments always go as argv, never a shell.
var programs = map[string]bool{"foot": true, "xdg-open": true, "udisksctl": true, "gdbus": true, "notify-send": true}

func allowed(prog string) error {
	if !programs[prog] {
		return fmt.Errorf("river-desk does not start %q", prog)
	}
	return nil
}

// become replaces this process with the program, so DEsk's launcher sees it directly.
func become(prog string, args ...string) error {
	if err := allowed(prog); err != nil {
		return err
	}
	p, err := exec.LookPath(prog)
	if err != nil {
		return err
	}
	return syscall.Exec(p, append([]string{prog}, args...), os.Environ()) // #nosec G204 -- prog is one of `programs`; argv, never a shell
}

// notify puts a letter in DEsk's post box (DEsk is the session's notification server). A
// variable, so the tests read the letters instead.
var notify = func(summary, body string) {
	_ = exec.Command("notify-send", "--app-name=Runink River", summary, body).Run() // #nosec G204 -- fixed program; the letter's text is argv, never a shell
}

// runner runs a program and returns its standard output (the seam the tests replace).
type runner interface {
	output(ctx context.Context, name string, args ...string) (string, error)
	lines(ctx context.Context, each func(string), name string, args ...string) error
}

type execRunner struct{}

func (execRunner) output(ctx context.Context, name string, args ...string) (string, error) {
	if err := allowed(name); err != nil {
		return "", err
	}
	b, err := exec.CommandContext(ctx, name, args...).Output() // #nosec G204 -- name is one of `programs`; argv, never a shell
	return strings.TrimSpace(string(b)), err
}

// lines runs a long-lived program and hands each line of its output to each, until it exits.
func (execRunner) lines(ctx context.Context, each func(string), name string, args ...string) error {
	if err := allowed(name); err != nil {
		return err
	}
	c := exec.CommandContext(ctx, name, args...) // #nosec G204 -- name is one of `programs`; argv, never a shell
	out, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		return err
	}
	readLines(out, each)
	return c.Wait()
}
