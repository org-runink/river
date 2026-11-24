// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
)

// ModelsRequired — the model payload step's required mode, without ZFS or a medium
// (installer/lib/72-models-payload.sh; docs/MODEL-PAYLOAD.md, "Installing from it").
//
//	default   no payload, a skip, an unattended install without a passphrase and a blank
//	          passphrase all DEFER the models: the step succeeds and writes nothing
//	required  RUNINK_MODELS_REQUIRED=1 turns each of those into a FAILED step
//	both      a payload with a passphrase file is unpacked (a fake river-modelpack), and a
//	          pool without room for the MANIFEST's content fails before unpacking
//
// The step runs as a program with fake zfs and river-modelpack first on PATH, in a scratch
// directory. Tier 1 runs it (scripts/ci-tier1.sh).
func ModelsRequired(ctx context.Context, repo string, outw, errw io.Writer) error {
	step := filepath.Join(repo, "installer/lib/72-models-payload.sh")
	if !isFile(step) {
		fmt.Fprintf(errw, "models-required: missing %s\n", step)
		return errors.New("models-required: missing the code under test")
	}
	t, err := os.MkdirTemp("", "models-required.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(t)
	c := &checks{out: outw, err: errw}

	bin := filepath.Join(t, "bin")
	payload := filepath.Join(t, "medium", "river-models")
	for _, d := range []string{bin, payload, filepath.Join(t, "target"), filepath.Join(t, "run")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// zfs: no existing dataset, an encrypted pool with FAKE_AVAIL bytes free.
	if err := writeExec(filepath.Join(bin, "zfs"), `#!/bin/sh
case "$1" in
list) exit 1 ;;
create) mkdir -p "$RUNINK_TARGET/var/lib/tide/models/shared" ;;
destroy) echo destroyed >> "$FAKE_LOG" ;;
get) [ -z "${FAKE_ZFS_BREAK:-}" ] || { echo "cannot open pool" >&2; exit 1; }
	case "$5" in
	encryption) echo aes-256-gcm ;;
	available) echo "$FAKE_AVAIL" ;;
	mountpoint) echo /var/lib/tide/models/shared ;;
	used) echo 1G ;;
	*) exit 1 ;;
	esac ;;
*) exit 1 ;;
esac
`, 0o755); err != nil {
		return err
	}
	if err := writeExec(filepath.Join(bin, "river-modelpack"), `#!/bin/sh
[ "$1" = unpack ] || exit 1
case "${FAKE_MP:-ok}" in
auth) echo "river-modelpack unpack: modelpack: authentication failed (wrong passphrase, or the payload was altered) at chunk 0" >&2; exit 1 ;;
medium) echo "river-modelpack unpack: modelpack: the medium is damaged: part models.part003 does not match MANIFEST (size or sha256)" >&2; exit 1 ;;
other) echo "river-modelpack unpack: boom" >&2; exit 1 ;;
esac
echo unpacked >> "$FAKE_LOG"
`, 0o755); err != nil {
		return err
	}
	lock := filepath.Join(t, "models.lock")
	manifest := "river-modelpack 1\ncontent 3 1000000\n"
	pass := filepath.Join(t, "pass")
	blank := filepath.Join(t, "blank")
	for p, s := range map[string]string{lock: "# lock\n", filepath.Join(payload, "MANIFEST"): manifest,
		pass: strings.Repeat("ab", 32) + "\n", blank: "\n"} {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			return err
		}
	}
	flog := filepath.Join(t, "log")
	// A PATH without river-modelpack (zfs only), for the missing-tool case.
	nomp := filepath.Join(t, "nomp")
	if err := os.MkdirAll(nomp, 0o755); err != nil {
		return err
	}
	if err := os.Symlink(filepath.Join(bin, "zfs"), filepath.Join(nomp, "zfs")); err != nil {
		return err
	}
	runEnv := func() []string {
		return []string{
			"PATH=" + bin + ":/usr/bin:/bin",
			"RUNINK_TARGET=" + filepath.Join(t, "target"), "RUNINK_POOL=tank",
			"RUNINK_MODELS_LOCK=" + lock, "RUNINK_MODELS_MANIFEST=" + filepath.Join(t, "none"),
			"RUNINK_MODELS_RUNDIR=" + filepath.Join(t, "run"), "FAKE_LOG=" + flog, "FAKE_AVAIL=999999999",
			"RUNINK_INSTALL_LOG=" + filepath.Join(t, "install.log"),
		}
	}

	run := func(env ...string) (string, bool) {
		_ = os.Remove(flog)
		cmd := pipe.Cmd("sh", step)
		cmd.Env = append(runEnv(), env...)
		out, err := combined(ctx, cmd)
		return out + readTrim(flog), err == nil
	}
	withPayload := "RUNINK_MODELS_PAYLOAD=" + payload
	noPayload := "RUNINK_MODELS_PAYLOAD=" + filepath.Join(t, "nothing")

	for _, req := range []string{"", "1"} {
		name := "default"
		if req == "1" {
			name = "required"
		}
		reqEnv := "RUNINK_MODELS_REQUIRED=" + req
		want := func(d string, out string, ok bool) {
			if req == "1" {
				c.expect(name+": "+d+" FAILS the step", !ok && strings.Contains(out, "REQUIRED") && !strings.Contains(out, "unpacked"))
			} else {
				c.expect(name+": "+d+" defers the models", ok && !strings.Contains(out, "unpacked"))
			}
		}
		out, ok := run(reqEnv, "RUNINK_SKIP_MODELS=1")
		want("RUNINK_SKIP_MODELS=1", out, ok)
		out, ok = run(reqEnv, withPayload)
		want("an unattended install without a passphrase", out, ok)
		out, ok = run(reqEnv, withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+blank)
		want("a blank passphrase file", out, ok)
		out, ok = run(reqEnv, noPayload)
		want("a RUNINK_MODELS_PAYLOAD without a MANIFEST", out, ok)
		out, ok = run(reqEnv, withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass)
		c.expect(name+": a payload with a passphrase is unpacked", ok && strings.Contains(out, "unpacked") && strings.Contains(out, "installed and verified"))
		out, ok = run(reqEnv, withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass, "FAKE_AVAIL=1000")
		c.expect(name+": a pool without room for the set fails before unpacking", !ok && !strings.Contains(out, "unpacked") && strings.Contains(out, "free"))
	}
	// Nothing fails silently: each way the unpack step can stop says what failed, why, the
	// numbers or the file, and what to do next; a deferral leaves an install-notice line the
	// drivers repeat at the end (docs/MODEL-PAYLOAD.md).
	out, ok := run(withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass, "FAKE_AVAIL=1000")
	c.expect("a pool short of space says so with the numbers and what to do",
		!ok && strings.Contains(out, "NOT ENOUGH FREE SPACE") && strings.Contains(out, "(1000 bytes)") &&
			strings.Contains(out, "1000000 bytes + 2%") && strings.Contains(out, "What to do:") && strings.Contains(out, "Full log:"))
	out, ok = run(withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass, "FAKE_MP=auth")
	c.expect("a wrong passphrase file is named as such, with no attempts left, and the dataset is destroyed",
		!ok && strings.Contains(out, "WRONG PASSPHRASE") && strings.Contains(out, "No attempts left") && strings.Contains(out, "destroyed") && strings.Contains(out, "What to do:"))
	out, ok = run(withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass, "FAKE_MP=medium")
	c.expect("a damaged payload names the file and says read error / sha256 mismatch",
		!ok && strings.Contains(out, "PAYLOAD READ ERROR / sha256 MISMATCH") && strings.Contains(out, "models.part003") && strings.Contains(out, "install medium"))
	out, ok = run(withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass, "FAKE_MP=other")
	c.expect("any other river-modelpack failure shows its own last lines", !ok && strings.Contains(out, "boom") && strings.Contains(out, "FAILED"))
	out, ok = run(withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass, "PATH="+nomp+":/usr/bin:/bin")
	c.expect("a missing river-modelpack is named, with what to do", !ok && strings.Contains(out, "river-modelpack is missing") && strings.Contains(out, "What to do:"))
	out, ok = run(withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+filepath.Join(t, "no-such-file"))
	c.expect("an unreadable passphrase file is reported", !ok && strings.Contains(out, "cannot read the passphrase file"))
	out, ok = run(withPayload, "RUNINK_SKIP_MODELS=1")
	c.expect("a skip leaves an install-notice saying the computer has no models", ok && strings.Contains(out, "install-notice: models: SKIPPED"))
	out, ok = run(withPayload)
	c.expect("a deferral leaves an install-notice saying the computer has no models", ok && strings.Contains(out, "install-notice: models: DEFERRED") && strings.Contains(out, "WITHOUT models"))
	out, ok = run(withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass)
	c.expect("success leaves an install-notice too", ok && strings.Contains(out, "install-notice: models: installed and verified"))
	// An unexpected tool failure (here: zfs cannot read the pool) is reported, not silent.
	out, ok = run(withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass, "FAKE_ZFS_BREAK=1")
	c.expect("an unexpected failure under set -e still says it failed and what to do", !ok && strings.Contains(out, "FAILED unexpectedly") && strings.Contains(out, "What to do:"))
	// Interactive: three attempts, each wrong one says how many are left. Needs a terminal,
	// which util-linux script(1) provides.
	if _, err := exec.LookPath("script"); err != nil {
		c.ok("interactive attempts: skipped (script(1) not installed)")
	} else {
		cmd := pipe.Cmd("script", "-qec", "sh "+step, "/dev/null")
		cmd.Env = append(runEnv(), "FAKE_MP=auth", withPayload)
		var b lockedBuffer
		err := pipe.Run(ctx, pipe.IO{Stdin: strings.NewReader("one\ntwo\nthree\n"), Stdout: &b, Stderr: &b}, cmd)
		o := b.String()
		c.expect("interactive: a wrong passphrase shows attempts left (2 of 3, 1 of 3) and then stops",
			err != nil && strings.Contains(o, "Attempts left: 2 of 3") && strings.Contains(o, "Attempts left: 1 of 3") && strings.Contains(o, "No attempts left"))
	}
	// The medium search: with nothing mounted where a live medium would be, the default mode
	// defers and the required mode fails. (/proc/mounts of the test host is searched too; a
	// host with a river-models payload on an iso9660 mount would skew this, so it is checked.)
	if hostHasPayload() {
		c.ok("medium search: skipped (this host has a river-models payload mounted)")
	} else {
		out, ok := run()
		c.expect("default: no payload on the medium defers the models", ok && strings.Contains(out, "no model payload"))
		out, ok = run("RUNINK_MODELS_REQUIRED=1")
		c.expect("required: no payload on the medium FAILS the step", !ok && strings.Contains(out, "REQUIRED"))
	}
	if c.failed > 0 {
		return fmt.Errorf("models-required: %d of %d check(s) failed", c.failed, c.n)
	}
	fmt.Fprintf(outw, "models-required: %d check(s) passed\n", c.n)
	return nil
}

// hostHasPayload reports whether a live-medium mount point or an iso9660 mount of this host
// carries river-models/MANIFEST (the step would find it).
func hostHasPayload() bool {
	dirs := []string{"/run/initramfs/live", "/run/archiso/bootmnt", "/run/artix/bootmnt", "/run/miso/bootmnt", "/bootmnt"}
	if b, err := os.ReadFile("/proc/mounts"); err == nil {
		for _, l := range lines(string(b)) {
			if f := strings.Fields(l); len(f) > 2 && f[2] == "iso9660" {
				dirs = append(dirs, f[1])
			}
		}
	}
	for _, d := range dirs {
		if isFile(filepath.Join(d, "river-models", "MANIFEST")) {
			return true
		}
	}
	return false
}
