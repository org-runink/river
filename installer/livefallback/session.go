// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

// session.go -- the live medium's first session: try the desktop, install from it.
//
//	river-live-fallback --session
//
// greetd's initial_session runs this as the live user (live-overlay/etc/greetd/live.toml). It
// starts the live DEsk desktop, so the stick can be used and tested without installing
// anything, and the installer is one launcher away ("Install Runink River"). When DEsk does not
// start, the installer opens full screen in weston's kiosk shell instead, the session the
// medium used to start first. The "Install only" GRUB entry (river.installer=1) goes straight
// to that kiosk.
//
// Nothing here is silent. Every decision, and everything DEsk and weston print, goes to
// <runtime dir>/river-live/session.log (the hardware report names it and copies its tail onto
// the stick), and the decisions go to stderr too, which greetd's service log keeps. Behind this
// the rest of the chain is unchanged: river-live-fallback (started by rc.local) takes over with
// the console kiosk and then the text installer when no compositor ever draws.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// cmdlineFlag reports whether a dotted boolean flag is on in the kernel command line: the bare
// name, or name=1|yes|on|true. Same rule as textRequested.
func cmdlineFlag(cmdline, name string) bool {
	for _, f := range strings.Fields(cmdline) {
		if f == name {
			return true
		}
		if v, ok := strings.CutPrefix(f, name+"="); ok {
			switch strings.ToLower(v) {
			case "1", "yes", "on", "true":
				return true
			}
		}
	}
	return false
}

// installerOnly reports whether the "Install only" GRUB entry was chosen.
func installerOnly(cmdline string) bool { return cmdlineFlag(cmdline, "river.installer") }

// sessionLogName is the session log's name under the live user's runtime directory.
const sessionLogName = "river-live/session.log"

// sessionLogMax caps the log, so a chatty browser cannot fill the runtime directory (a tmpfs).
const sessionLogMax = 8 << 20

// desktopStartBudget: a DEsk session that ends sooner than this, with an error or within
// desktopQuickExit, did not start; the installer kiosk takes its place. A session that ran
// longer ended on purpose (the person logged out) and greetd's login screen follows.
const (
	desktopStartBudget = 45 * time.Second
	desktopQuickExit   = 10 * time.Second
)

// desktopFailed decides whether a DEsk session that ended counts as "did not start".
func desktopFailed(rc int, ran time.Duration) bool {
	if ran >= desktopStartBudget {
		return false
	}
	return rc != 0 || ran < desktopQuickExit
}

// cappedWriter writes until max bytes, then once says it stopped.
type cappedWriter struct {
	mu   sync.Mutex
	w    io.Writer
	n    int
	max  int
	full bool
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.full {
		return len(p), nil
	}
	if c.n+len(p) > c.max {
		c.full = true
		_, _ = io.WriteString(c.w, fmt.Sprintf("\n[session log stopped at %d bytes]\n", c.max))
		return len(p), nil
	}
	n, err := c.w.Write(p)
	c.n += n
	if err != nil {
		return n, err
	}
	return len(p), nil
}

// sessionLogPath returns where this user's session log goes.
func sessionLogPath() string {
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		rt = filepath.Join(os.TempDir(), fmt.Sprintf("river-live-%d", os.Getuid()))
	}
	return filepath.Join(rt, sessionLogName)
}

// runSession is --session. It returns the exit code for greetd.
func runSession() int {
	path := sessionLogPath()
	var out io.Writer = io.Discard
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil { // #nosec G302 G304 -- the live user's own runtime directory; the report (root) reads it
			defer f.Close()
			out = &cappedWriter{w: f, max: sessionLogMax}
		} else {
			fmt.Fprintf(os.Stderr, "river-live-session: cannot write %s: %v\n", path, err)
		}
	}
	say := func(format string, a ...any) {
		line := fmt.Sprintf("river-live-session: "+format, a...)
		fmt.Fprintln(os.Stderr, line)
		fmt.Fprintf(out, "%s %s\n", time.Now().UTC().Format(time.RFC3339), line)
	}
	cmdline := strings.TrimSpace(readCmdline())
	say("log %s; kernel command line: %s", path, cmdline)

	if !installerOnly(cmdline) {
		desk, err := exec.LookPath("desk-session")
		if err != nil {
			say("DEsk is not on this medium (desk-session: %v); opening the installer full screen", err)
		} else {
			say("starting the live desktop (%s)", desk)
			rc, ran := runLogged(out, desk)
			if !desktopFailed(rc, ran) {
				say("the desktop session ended (exit %d after %s)", rc, ran.Round(time.Second))
				return 0
			}
			say("the desktop did NOT start: exit %d after %s (its own output is above). "+
				"Opening the installer full screen instead.", rc, ran.Round(time.Second))
		}
	} else {
		say("river.installer is set (\"Install only\"): opening the installer full screen")
	}
	weston, err := exec.LookPath("weston")
	if err != nil {
		say("weston is not on this medium (%v): no graphical installer from this session; "+
			"river-live-fallback opens the text installer", err)
		return 1
	}
	rc, ran := runLogged(out, weston, "--continue-without-input")
	say("the installer session (weston) ended: exit %d after %s", rc, ran.Round(time.Second))
	return rc
}

// runLogged runs a session program with its output in the log, passes greetd's SIGTERM and
// SIGHUP on to it, and returns its exit code and how long it ran.
func runLogged(out io.Writer, name string, args ...string) (int, time.Duration) {
	cmd := exec.Command(name, args...) // #nosec G204 -- fixed session programs resolved by LookPath
	cmd.Stdout, cmd.Stderr = out, out
	// A program the session started (a browser) can outlive it and hold the log pipe; do not
	// wait for it.
	cmd.WaitDelay = 2 * time.Second
	start := time.Now()
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(out, "river-live-session: %s did not start: %v\n", name, err)
		return 127, 0
	}
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sig:
				_ = cmd.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	signal.Stop(sig)
	ran := time.Since(start)
	if cmd.ProcessState != nil {
		if code := cmd.ProcessState.ExitCode(); code >= 0 {
			return code, ran
		}
		return 128, ran // killed by a signal
	}
	if err != nil {
		return 1, ran
	}
	return 0, ran
}
