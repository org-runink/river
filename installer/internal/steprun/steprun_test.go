// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package steprun

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type rig struct {
	t      *testing.T
	dir    string
	c      *Config
	stdout bytes.Buffer
}

func newRig(t *testing.T) *rig {
	dir := t.TempDir()
	r := &rig{t: t, dir: dir}
	r.c = &Config{Log: filepath.Join(dir, "var-log", "install.log"), State: filepath.Join(dir, "state"),
		Target: filepath.Join(dir, "target"), Stdout: &r.stdout, Stdin: strings.NewReader("")}
	if err := r.c.Init(); err != nil {
		t.Fatal(err)
	}
	return r
}

// run executes a throwaway step and returns the failure report ("" on success), as the CLI
// prints it, and the log.
func (r *rig) run(step, body string) (Result, string, string) {
	r.t.Helper()
	f := filepath.Join(r.dir, step+".sh")
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.stdout.Reset()
	res := r.c.Run(step, f)
	rep := ""
	if res.RC != 0 {
		rep = r.c.Report(step, res)
	}
	b, _ := os.ReadFile(r.c.Log)
	return res, rep, string(b)
}

func has(t *testing.T, what, s string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Errorf("%s lacks %q:\n%s", what, w, s)
		}
	}
}

func TestFailedEarlyStepIsReportedInFull(t *testing.T) {
	r := newRig(t)
	res, rep, log := r.run("20-clone-rootfs", "echo copying\necho 'rsync: write failed: No space left on device (28)' >&2\nexit 23\n")
	if res.RC != 23 {
		t.Fatalf("rc = %d", res.RC)
	}
	has(t, "report", rep, "STOPPED at step 20-clone-rootfs (exit code 23)", "No space left on device",
		"Full log: "+r.c.Log, "What to do:", "NOT installed")
	has(t, "log", log, "No space left on device", "20-clone-rootfs exited 23")
	has(t, "console", r.stdout.String(), "copying")
}

func TestLateFailureIsPartlyInstalled(t *testing.T) {
	r := newRig(t)
	_, rep, _ := r.run("72-models-payload", "echo 'models-payload: FAILED: WRONG PASSPHRASE' >&2\nexit 1\n")
	has(t, "report", rep, "PARTLY installed", "WRONG PASSPHRASE")
}

func TestSilentFailureIsStillReported(t *testing.T) {
	r := newRig(t)
	_, rep, _ := r.run("50-runink-user", "exit 5\n")
	has(t, "report", rep, "exit code 5", "it printed nothing")
}

func TestSuccessReportsNoFailure(t *testing.T) {
	r := newRig(t)
	res, rep, log := r.run("30-target-config", "echo fine\n")
	if res.RC != 0 || rep != "" {
		t.Errorf("rc=%d report=%q", res.RC, rep)
	}
	has(t, "log", log, "30-target-config exited 0")
}

func TestStartFailureIsCodeOneAndSaid(t *testing.T) {
	r := newRig(t)
	r.c.Shell = filepath.Join(r.dir, "no-such-shell")
	res := r.c.Run("20-clone-rootfs", "x")
	if res.RC != 1 || !strings.Contains(strings.Join(res.Tail, "\n"), "cannot run step") {
		t.Errorf("%+v", res)
	}
}

func TestNoticesAreRepeatedAtTheEnd(t *testing.T) {
	r := newRig(t)
	if _, rep, _ := r.run("72-models-payload", "echo 'install-notice: models: DEFERRED: no passphrase'\n"); rep != "" {
		t.Errorf("a deferral is not a failure: %s", rep)
	}
	s := r.c.Summary()
	has(t, "summary", s, "Read this before you restart", "72-models-payload: models: DEFERRED")
	if (&Config{State: t.TempDir()}).Summary() != "" {
		t.Error("a run without notices printed a summary")
	}
}

// The recovery key reaches the console but is never written to the log, on success or failure.
func TestRecoveryKeyNeverReachesTheLog(t *testing.T) {
	key := strings.Repeat("ab", 32)
	r := newRig(t)
	_, _, log := r.run("10-disk-zfs", "echo 'RECOVERY KEY "+key+"'\n")
	if !strings.Contains(r.stdout.String(), key) || strings.Contains(log, key) || !strings.Contains(log, "withheld") {
		t.Errorf("console=%q log=%q", r.stdout.String(), log)
	}
	res, rep, log := r.run("10-disk-zfs", "echo 'RECOVERY KEY "+key+"'\necho 'zpool: cannot create' >&2\nexit 1\n")
	if res.RC != 1 || strings.Contains(rep, key) || strings.Contains(log, key) || !strings.Contains(rep, "STOPPED at step 10-disk-zfs") {
		t.Errorf("rc=%d report=%q log=%q", res.RC, rep, log)
	}
}

func TestLogIsCopiedOntoTheNewSystem(t *testing.T) {
	r := newRig(t)
	r.run("30-target-config", "echo early\n")
	if _, err := os.Stat(filepath.Join(r.c.Target, "var/log/install.log")); err == nil {
		t.Error("the log was copied before the new system existed")
	}
	if err := os.MkdirAll(filepath.Join(r.c.Target, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, rep, _ := r.run("40-boot-grub-zfs", "echo grub failed >&2\nexit 1\n")
	dst := filepath.Join(r.c.Target, "var/log/install.log")
	b, err := os.ReadFile(dst)
	st, _ := os.Stat(dst)
	if err != nil || !strings.Contains(string(b), "grub failed") || st.Mode().Perm() != 0o600 {
		t.Errorf("target log: %v %q", err, b)
	}
	has(t, "report", rep, "a copy is on the new system at /var/log/install.log")
	if st, _ := os.Stat(r.c.Log); st.Mode().Perm() != 0o600 {
		t.Errorf("log mode %v", st.Mode())
	}
}

// A prompt has no newline: it must reach the console before the step waits for input.
func TestOutputWithoutNewlineIsPassedThroughAtOnce(t *testing.T) {
	r := newRig(t)
	r.run("72-models-payload", "printf 'Model payload passphrase: '\n")
	if got := r.stdout.String(); got != "Model payload passphrase: " {
		t.Errorf("console = %q", got)
	}
}

func TestRsyncProgressStaysOutOfTheLog(t *testing.T) {
	r := newRig(t)
	_, _, log := r.run("20-clone-rootfs", "printf '    1,234,567,890  40%%   98MB/s    0:00:12\\r    1,234,567,890  80%%   98MB/s    0:00:12\\rdone copying\\n'\n")
	if strings.Contains(log, "98MB/s") || !strings.Contains(log, "done copying") {
		t.Errorf("log:\n%s", log)
	}
}

func TestSituation(t *testing.T) {
	for step, want := range map[string]string{"00-preflight": NotInstalled, "40-boot-grub-zfs": NotInstalled, "50-runink-user": Partial, "90-export": Partial} {
		if got := Situation(step); got != want {
			t.Errorf("%s: %s, want %s", step, got, want)
		}
	}
}
