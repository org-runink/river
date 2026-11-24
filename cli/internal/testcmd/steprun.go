// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
)

// Steprun — the text-mode installer's step runner (installer/lib/steprun.sh), with throwaway
// steps in a scratch dir. A step must never fail silently:
//
//	failure   the report names the step and its exit code, shows the step's own last lines,
//	          says where the log is, says what state the machine is in (before the boot loader:
//	          NOT installed; after it: only PARTLY installed) and what to do next
//	silent    a step that fails without printing is still reported
//	log       the whole output is in the log (0600), the recovery key of 10-disk-zfs is not,
//	          and the log is copied onto the new system once it has an /etc
//	notice    an install-notice line a step prints is repeated by steprun_summary
func Steprun(ctx context.Context, repo string, outw, errw io.Writer) error {
	lib := filepath.Join(repo, "installer/lib/steprun.sh")
	if !isFile(lib) {
		fmt.Fprintf(errw, "steprun: missing %s\n", lib)
		return errors.New("steprun: missing the code under test")
	}
	t, err := os.MkdirTemp("", "steprun.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(t)
	c := &checks{out: outw, err: errw}

	driver := filepath.Join(t, "driver.sh")
	if err := writeExec(driver, `#!/bin/sh
. "$1"
steprun_init
steprun_run "$2" "$3" || { steprun_fail "$2" "$STEPRUN_RC"; echo "DRIVER-RC=$STEPRUN_RC"; }
steprun_summary
`, 0o755); err != nil {
		return err
	}
	step := func(name, body string) string {
		p := filepath.Join(t, name+".sh")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			panic(err)
		}
		return p
	}
	logf := filepath.Join(t, "var-log", "install.log")
	target := filepath.Join(t, "target")
	run := func(name, body string) (string, string) {
		_ = os.RemoveAll(logf)
		cmd := pipe.Cmd("sh", driver, lib, name, step(name, body))
		cmd.Env = []string{"PATH=/usr/bin:/bin", "RUNINK_INSTALL_LOG=" + logf, "RUNINK_STEPRUN_TMP=" + t,
			"RUNINK_TARGET=" + target, "RUNINK_UNATTENDED=1"}
		out, _ := combined(ctx, cmd)
		return out, readTrim(logf)
	}

	out, log := run("20-clone-rootfs", "echo copying\necho 'rsync: write failed: No space left on device (28)' >&2\nexit 23\n")
	c.expect("a failed early step is reported with its exit code, its own last lines and the log path",
		strings.Contains(out, "STOPPED at step 20-clone-rootfs (exit code 23)") && strings.Contains(out, "No space left on device") &&
			strings.Contains(out, "Full log: "+logf) && strings.Contains(out, "DRIVER-RC=23") && strings.Contains(out, "What to do:"))
	c.expect("an early failure says the computer is NOT installed", strings.Contains(out, "NOT installed"))
	c.expect("the log has the step's output and its exit code", strings.Contains(log, "No space left on device") && strings.Contains(log, "20-clone-rootfs exited 23"))

	out, _ = run("72-models-payload", "echo 'models-payload: FAILED: WRONG PASSPHRASE' >&2\nexit 1\n")
	c.expect("a failure after the boot loader says PARTLY installed", strings.Contains(out, "PARTLY installed") && strings.Contains(out, "WRONG PASSPHRASE"))

	out, _ = run("50-runink-user", "exit 5\n")
	c.expect("a step that fails silently is still reported, and says it printed nothing",
		strings.Contains(out, "exit code 5") && strings.Contains(out, "it printed nothing"))

	out, log = run("30-target-config", "echo fine\n")
	c.expect("a step that succeeds reports no failure", !strings.Contains(out, "STOPPED") && strings.Contains(out, "fine") && strings.Contains(log, "30-target-config exited 0"))

	out, log = run("72-models-payload", "echo 'install-notice: models: DEFERRED: no passphrase'\n")
	c.expect("an install-notice is repeated at the end", strings.Contains(out, "Read this before you restart") && strings.Contains(out, "72-models-payload: models: DEFERRED") && !strings.Contains(out, "STOPPED"))

	key := strings.Repeat("ab", 32)
	out, log = run("10-disk-zfs", "echo 'RECOVERY KEY "+key+"'\nexit 0\n")
	c.expect("the recovery key reaches the console but never the log file", strings.Contains(out, key) && !strings.Contains(log, key) && strings.Contains(log, "withheld"))
	out, _ = run("10-disk-zfs", "echo 'RECOVERY KEY "+key+"'\necho 'zpool: cannot create' >&2\nexit 1\n")
	c.expect("a failed 10-disk-zfs is reported without echoing the key a second time",
		strings.Contains(out, "STOPPED at step 10-disk-zfs") && strings.Count(out, key) == 1)

	if err := os.MkdirAll(filepath.Join(target, "etc"), 0o755); err != nil {
		return err
	}
	_, _ = run("40-boot-grub-zfs", "echo grub failed >&2\nexit 1\n")
	c.expect("once the new system exists the log is copied onto it, 0600",
		strings.Contains(readTrim(filepath.Join(target, "var/log/install.log")), "grub failed") && modeOf(filepath.Join(target, "var/log/install.log")) == 0o600)

	if c.failed > 0 {
		return fmt.Errorf("steprun: %d of %d check(s) failed", c.failed, c.n)
	}
	fmt.Fprintf(outw, "steprun: %d check(s) passed\n", c.n)
	return nil
}

func modeOf(p string) os.FileMode {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Mode().Perm()
}
