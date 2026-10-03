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

// modelsRequiredChecks is every check ModelsRequired reports, by its stable evidence name
// (static: see testevidence.go). The medium-* pair is skipped, with its reason, on a host that
// has a model payload mounted.
var modelsRequiredChecks = []string{
	"default-skip-models", "default-no-passphrase", "default-blank-passphrase", "default-no-manifest",
	"default-unpacked", "default-no-room-fails",
	"required-skip-models", "required-no-passphrase", "required-blank-passphrase", "required-no-manifest",
	"required-unpacked", "required-no-room-fails",
	"medium-default-defers", "medium-required-fails",
}

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
	c := newChecks(ctx, outw, errw)

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
create) mkdir -p "$RUNINK_TARGET/var/lib/core/models/shared" ;;
destroy) echo destroyed >> "$FAKE_LOG" ;;
get) case "$5" in
	encryption) echo aes-256-gcm ;;
	available) echo "$FAKE_AVAIL" ;;
	mountpoint) echo /var/lib/core/models/shared ;;
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

	run := func(env ...string) (string, bool) {
		_ = os.Remove(flog)
		cmd := pipe.Cmd("sh", step)
		cmd.Env = append([]string{
			"PATH=" + bin + ":/usr/bin:/bin",
			"RUNINK_TARGET=" + filepath.Join(t, "target"), "RUNINK_POOL=tank",
			"RUNINK_MODELS_LOCK=" + lock, "RUNINK_MODELS_MANIFEST=" + filepath.Join(t, "none"),
			"RUNINK_MODELS_RUNDIR=" + filepath.Join(t, "run"), "FAKE_LOG=" + flog, "FAKE_AVAIL=999999999",
		}, env...)
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
		want := func(check, d string, out string, ok bool) {
			if req == "1" {
				c.expect(name+"-"+check, name+": "+d+" FAILS the step", !ok && strings.Contains(out, "REQUIRED") && !strings.Contains(out, "unpacked"))
			} else {
				c.expect(name+"-"+check, name+": "+d+" defers the models", ok && !strings.Contains(out, "unpacked"))
			}
		}
		out, ok := run(reqEnv, "RUNINK_SKIP_MODELS=1")
		want("skip-models", "RUNINK_SKIP_MODELS=1", out, ok)
		out, ok = run(reqEnv, withPayload)
		want("no-passphrase", "an unattended install without a passphrase", out, ok)
		out, ok = run(reqEnv, withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+blank)
		want("blank-passphrase", "a blank passphrase file", out, ok)
		out, ok = run(reqEnv, noPayload)
		want("no-manifest", "a RUNINK_MODELS_PAYLOAD without a MANIFEST", out, ok)
		out, ok = run(reqEnv, withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass)
		c.expect(name+"-unpacked", name+": a payload with a passphrase is unpacked", ok && strings.Contains(out, "unpacked") && strings.Contains(out, "installed and verified"))
		out, ok = run(reqEnv, withPayload, "RUNINK_MODELS_PASSPHRASE_FILE="+pass, "FAKE_AVAIL=1000")
		c.expect(name+"-no-room-fails", name+": a pool without room for the set fails before unpacking", !ok && !strings.Contains(out, "unpacked") && strings.Contains(out, "free"))
	}
	// The medium search: with nothing mounted where a live medium would be, the default mode
	// defers and the required mode fails. (/proc/mounts of the test host is searched too; a
	// host with a river-models payload on an iso9660 mount would skew this, so it is checked.)
	if hostHasPayload() {
		c.skipped("medium search: skipped (this host has a river-models payload mounted)",
			"this host has a river-models payload mounted, which the step would find",
			"medium-default-defers", "medium-required-fails")
	} else {
		out, ok := run()
		c.expect("medium-default-defers", "default: no payload on the medium defers the models", ok && strings.Contains(out, "no model payload"))
		out, ok = run("RUNINK_MODELS_REQUIRED=1")
		c.expect("medium-required-fails", "required: no payload on the medium FAILS the step", !ok && strings.Contains(out, "REQUIRED"))
	}
	if c.failed > 0 {
		return fmt.Errorf("models-required: %d of %d check(s) failed", c.failed, c.n)
	}
	fmt.Fprintf(outw, "models-required: %d check(s) passed\n", c.n)
	return nil
}

// hostHasPayload reports whether a live-medium mount point or an iso9660 mount of this host
// carries river-models/MANIFEST (the step would find it). A variable so a test can take the
// skip branch.
var hostHasPayload = func() bool {
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
