// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package steprun runs one installer step so that a failure is never silent. It is the engine
// of river-steprun, which the text-mode installers (runink-install, runink-autoinstall) call
// for every step; the graphical installer shares its helpers (Situation, LastLines, Persist).
//
//   - the step's output reaches the console as it is written (a passphrase prompt has no
//     newline, so output is passed through in chunks, never line by line);
//   - the whole output is appended to the install log (0600) and, once the new system exists,
//     copied onto it (/var/log/<log name>);
//   - the exit code is kept and returned;
//   - on a failure Report prints, in plain language, which step failed, why (the step's own last
//     lines and exit code), where the log is, the state the computer is left in and what to do
//     next;
//   - a line a step prints that starts with NoticePrefix (a deferred step, say) is collected and
//     repeated by Summary at the end of the install.
//
// 10-disk-zfs prints the ZFS recovery key, which is never written to a file: its output reaches
// the console only and the log records that it was withheld.
package steprun

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// NoticePrefix starts a line a step prints to tell the operator something that must not be
// lost in the scroll.
const NoticePrefix = "install-notice: "

// DefaultLog is where the install log is kept on the live system.
const DefaultLog = "/var/log/runink-install.log"

// TailLines is how many of a step's last lines a failure report shows as the reason.
const TailLines = 12

// WithheldStep is the step whose output carries the recovery key and is never logged.
const WithheldStep = "10-disk-zfs"

// Situations a failed step leaves the machine in.
const (
	// NotInstalled: the disks hold nothing that boots.
	NotInstalled = "not-installed"
	// Partial: the system and its boot loader are on the disk, but the steps from the failed
	// one on did not run, so it is not ready for use.
	Partial = "partial"
)

// Situation maps a step to the state its failure leaves. Steps up to 40-boot-grub-zfs (disk,
// copy, target configuration, boot loader) leave a machine that cannot boot; later steps (user,
// artifacts, models, plan, services) run on a system that can, so a failure there leaves it
// installed but unfinished.
func Situation(step string) string {
	if len(step) >= 2 && step[:2] >= "00" && step[:2] < "50" {
		return NotInstalled
	}
	return Partial
}

// LastLines returns up to n of the last non-empty lines.
func LastLines(lines []string, n int) []string {
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			out = append([]string{lines[i]}, out...)
		}
	}
	return out
}

// AppendFile adds one line to a log (0600: it names disks and hosts). Errors are ignored on
// purpose: a log that cannot be written must not stop an install, and the same lines stay on
// the screen.
func AppendFile(path, line string) {
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- the installer's own log path
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

// Persist copies the log onto the new system once it exists (it has an /etc), so the operator
// can read it there after the reboot. It returns the path on the new system, or "".
func Persist(logFile, targetRoot string) string {
	if logFile == "" || targetRoot == "" {
		return ""
	}
	if st, err := os.Stat(filepath.Join(targetRoot, "etc")); err != nil || !st.IsDir() {
		return ""
	}
	b, err := os.ReadFile(logFile) // #nosec G304 -- the installer's own log path
	if err != nil {
		return ""
	}
	dst := filepath.Join(targetRoot, "var/log", filepath.Base(logFile))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return ""
	}
	return filepath.Join("/var/log", filepath.Base(logFile))
}

// Config is one runner's environment.
type Config struct {
	Log    string // the install log (default DefaultLog)
	State  string // scratch dir holding the notices between invocations
	Target string // where the new system is mounted (RUNINK_TARGET)
	Shell  string // default "sh"
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
}

func (c *Config) log() string {
	if c.Log == "" {
		return DefaultLog
	}
	return c.Log
}

func (c *Config) noticesFile() string { return filepath.Join(c.State, "notices") }

// Init starts a run: it creates the state dir, empties the notices and opens the log.
func (c *Config) Init() error {
	if err := os.MkdirAll(c.State, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(c.noticesFile(), nil, 0o600); err != nil {
		return err
	}
	AppendFile(c.log(), "=== install started "+time.Now().UTC().Format(time.RFC3339))
	return nil
}

var progressRe = regexp.MustCompile(`^\s*[0-9][0-9,]*\s+[0-9]{1,3}%`)

// sink passes the step's output through to the console at once and feeds a line splitter that
// keeps the log, the tail and the notices. rsync rewrites its progress line with \r: it is
// split like a newline for the tail but kept out of the log.
type sink struct {
	mu      sync.Mutex
	out     io.Writer
	step    string
	logFile string
	partial []byte
	tail    []string
	notices []string
	logged  bool
}

func (s *sink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.out.Write(p)
	if s.step == WithheldStep {
		return n, err
	}
	s.partial = append(s.partial, p...)
	for {
		i := bytes.IndexAny(s.partial, "\r\n")
		if i < 0 {
			break
		}
		s.line(string(s.partial[:i]))
		s.partial = s.partial[i+1:]
	}
	return n, err
}

func (s *sink) line(l string) {
	if strings.TrimSpace(l) == "" {
		return
	}
	s.tail = append(s.tail, l)
	if len(s.tail) > TailLines {
		s.tail = s.tail[len(s.tail)-TailLines:]
	}
	if n, ok := strings.CutPrefix(l, NoticePrefix); ok {
		s.notices = append(s.notices, s.step+": "+strings.TrimSpace(n))
	}
	if !progressRe.MatchString(l) {
		AppendFile(s.logFile, l)
	}
}

func (s *sink) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.partial) > 0 && s.step != WithheldStep {
		s.line(string(s.partial))
	}
	s.partial = nil
}

// Result is what Run learned.
type Result struct {
	RC   int
	Tail []string
}

// Run runs `sh file` as step, returns its exit code. A step that cannot even start is code 1.
// The failure report is NOT printed here (see Report): the caller decides.
func (c *Config) Run(step, file string) Result {
	stdout := c.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	s := &sink{out: stdout, step: step, logFile: c.log()}
	AppendFile(c.log(), "=== "+step+" "+time.Now().UTC().Format("15:04:05"))
	shell := c.Shell
	if shell == "" {
		shell = "sh"
	}
	cmd := exec.Command(shell, file) // #nosec G204 -- the image's own steps
	cmd.Stdin = c.Stdin
	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	cmd.Stdout, cmd.Stderr = s, s
	rc := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() >= 0 {
			rc = ee.ExitCode()
		} else {
			rc = 1
			fmt.Fprintf(s, "river-steprun: cannot run step %s: %v\n", step, err)
		}
	}
	s.flush()
	if step == WithheldStep {
		AppendFile(c.log(), "(output withheld: it carries the recovery key)")
		s.tail = nil
	}
	AppendFile(c.log(), fmt.Sprintf("=== %s exited %d", step, rc))
	if len(s.notices) > 0 {
		if f, err := os.OpenFile(c.noticesFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			for _, n := range s.notices {
				_, _ = f.WriteString(n + "\n")
			}
			_ = f.Close()
		}
	}
	Persist(c.log(), c.Target)
	return Result{RC: rc, Tail: append([]string(nil), s.tail...)}
}

// Report is the failure report, in plain language.
func (c *Config) Report(step string, r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "THE INSTALLATION STOPPED at step %s (exit code %d).\n\n", step, r.RC)
	b.WriteString("Why: the step's last lines were:\n")
	if len(r.Tail) == 0 {
		b.WriteString("    (it printed nothing; see the log)\n")
	}
	for _, l := range r.Tail {
		b.WriteString("    " + l + "\n")
	}
	b.WriteString("\n")
	if Situation(step) == NotInstalled {
		b.WriteString("State of this computer: NOT installed. Nothing on it can start yet.\n")
	} else {
		fmt.Fprintf(&b, "State of this computer: only PARTLY installed. The system is on the disk, but step %s\nand the steps after it did not run, so it is not ready to use.\n", step)
	}
	fmt.Fprintf(&b, "\nFull log: %s (this live session; it is lost when you restart)\n", c.log())
	if c.Target != "" {
		if _, err := os.Stat(filepath.Join(c.Target, "var/log", filepath.Base(c.log()))); err == nil {
			fmt.Fprintf(&b, "          a copy is on the new system at /var/log/%s\n", filepath.Base(c.log()))
		}
	}
	b.WriteString("\nWhat to do: read the reason above and fix it (free disk space, check the cable or the\n" +
		"stick, check the passphrase), then run the installer again. If it fails again, keep the\n" +
		"log text and ask for help.\n")
	return b.String()
}

// Summary returns the notices steps printed, as text for the end of the install ("" when none).
func (c *Config) Summary() string {
	b, err := os.ReadFile(c.noticesFile())
	if err != nil || len(bytes.TrimSpace(b)) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("\nRead this before you restart:\n")
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		out.WriteString("  - " + l + "\n")
	}
	AppendFile(c.log(), strings.TrimRight(out.String(), "\n"))
	return out.String()
}
