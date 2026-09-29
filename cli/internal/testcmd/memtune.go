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
	"regexp"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
)

// Memtune — the install plan's memory sizes on the target, in a scratch root, no root needed
// (docs/INSTALLER-HARDWARE.md, "Swap, ARC, GPU, TPM, network"). Was tests/memtune.sh.
//
//	arc      the plan's RUNINK_ZFS_ARC_MAX becomes `options zfs zfs_arc_max=N` in
//	         etc/modprobe.d/zfs.conf; other options and lines are kept; a re-run is a no-op
//	fallback no plan: clamp(MemTotal/16, 1 GiB, 16 GiB) — the planner's numbers for 93 GiB
//	         (~5.8 GiB) and 16 GiB (1 GiB), pinned in planner TestMemorySizesInEnv too
//	zram     the plan's RUNINK_ZRAM_SIZE lands in etc/runink/zram.conf (0600), and
//	         runink-zram.sh --print-size reads it; the environment overrides it, the
//	         image default applies without it, and a malformed value is ignored
//
// It drives the shell under test (installer/lib/memtune.sh, sourced, and the image's
// runink-zram.sh) as programs. Tier 1 runs it (scripts/ci-tier1.sh).
func Memtune(ctx context.Context, repo string, outw, errw io.Writer) error {
	lib := filepath.Join(repo, "installer/lib/memtune.sh")
	zram := filepath.Join(repo, "iso-profiles/river/root-overlay/usr/local/bin/runink-zram.sh")
	for _, f := range []string{lib, zram} {
		if !isFile(f) {
			fmt.Fprintf(errw, "memtune: missing %s\n", f)
			return errors.New("memtune: missing the code under test")
		}
	}
	t, err := os.MkdirTemp("", "memtune.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(t)
	c := &checks{out: outw, err: errw}

	meminfo := filepath.Join(t, "meminfo")
	writeMeminfo := func(gib int64) error {
		return os.WriteFile(meminfo, fmt.Appendf(nil, "MemTotal:       %d kB\nMemFree:        1024 kB\n", gib*1024*1024), 0o644)
	}
	memPath := meminfo
	// arc is $(RUNINK_ZFS_ARC_MAX=plan memtune_arc_bytes); quiet discards its stderr.
	arc := func(plan string, quiet bool) (string, error) {
		cmd := libCall(lib, "memtune_arc_bytes")
		cmd.Env = []string{"MEMTUNE_MEMINFO=" + memPath, "RUNINK_ZFS_ARC_MAX=" + plan}
		var stderr io.Writer = errw
		if quiet {
			stderr = nil
		}
		return capture(ctx, cmd, stderr)
	}
	call := func(fn string, args ...string) error {
		return pipe.Run(ctx, pipe.IO{Stdout: outw, Stderr: errw}, libCall(lib, fn, args...))
	}

	// --- zfs_arc_max -------------------------------------------------------------------------
	if err := writeMeminfo(93); err != nil {
		return err
	}
	got, _ := arc("123456789", false)
	c.eq("plan value wins over meminfo", got, "123456789")
	got, _ = arc("", false)
	c.eq("93 GiB, no plan: RAM/16 = 5952 MiB", got, "6241124352")
	got, _ = arc("6G", true)
	c.eq("malformed plan value falls back to RAM/16", got, "6241124352")
	if err := writeMeminfo(16); err != nil {
		return err
	}
	got, _ = arc("", false)
	c.eq("16 GiB, no plan: the 1 GiB floor", got, "1073741824")
	if err := writeMeminfo(512); err != nil {
		return err
	}
	got, _ = arc("", false)
	c.eq("512 GiB, no plan: the 16 GiB ceiling", got, "17179869184")
	memPath = filepath.Join(t, "absent")
	if _, err := arc("", true); err == nil {
		c.ko("no plan and no meminfo must fail")
	} else {
		c.ok("no plan and no meminfo fails (ZFS default left alone)")
	}
	memPath = meminfo

	r := filepath.Join(t, "root")
	if err := os.MkdirAll(filepath.Join(r, "etc/modprobe.d"), 0o755); err != nil {
		return err
	}
	conf := filepath.Join(r, "etc/modprobe.d/zfs.conf")
	if err := os.WriteFile(conf, []byte("# local\noptions zfs zfs_arc_max=1 zfs_txg_timeout=10\noptions spl spl_hostid=1\n"), 0o644); err != nil {
		return err
	}
	for range 2 {
		if err := call("memtune_write_arc", r, "6241124352"); err != nil {
			return fmt.Errorf("memtune: memtune_write_arc: %w", err)
		}
	}
	zc := readTrim(conf)
	c.eq("one zfs_arc_max after two runs", countMatching(zc, regexp.MustCompile(`zfs_arc_max=`)), "1")
	c.eq("zfs_arc_max is the plan's", arcValue(zc), "6241124352")
	c.eq("other zfs options kept", countMatching(zc, regexp.MustCompile(`^options zfs zfs_txg_timeout=10$`)), "1")
	c.eq("other modules' lines kept", countMatching(zc, regexp.MustCompile(`^options spl spl_hostid=1$`)), "1")
	c.eq("comments kept", countMatching(zc, regexp.MustCompile(`^# local$`)), "1")
	if err := os.RemoveAll(r); err != nil {
		return err
	}
	if err := call("memtune_write_arc", r, "1073741824"); err != nil {
		return fmt.Errorf("memtune: memtune_write_arc: %w", err)
	}
	c.eq("fresh target: the file is created", strings.Join(matching(readTrim(conf), regexp.MustCompile(`^options`)), "\n"), "options zfs zfs_arc_max=1073741824")

	// --- zram --------------------------------------------------------------------------------
	if err := call("memtune_write_zram", r, "16384M"); err != nil {
		return fmt.Errorf("memtune: memtune_write_zram: %w", err)
	}
	z := filepath.Join(r, "etc/runink/zram.conf")
	sizeLine := func() string {
		return strings.Join(matching(readTrim(z), regexp.MustCompile(`^RUNINK_ZRAM_SIZE=`)), "\n")
	}
	c.eq("zram.conf holds the plan's size", sizeLine(), "RUNINK_ZRAM_SIZE=16384M")
	c.eq("zram.conf is 0600", mode(z), "600")
	c.eq("etc/runink is 0700", mode(filepath.Join(r, "etc/runink")), "700")
	if err := pipe.Run(ctx, pipe.IO{Stdout: outw}, libCall(lib, "memtune_write_zram", r, "16G; reboot")); err == nil {
		c.ko("a malformed zram size must be refused")
	} else {
		c.ok("a malformed zram size is refused")
	}
	c.eq("a refused size leaves the file alone", sizeLine(), "RUNINK_ZRAM_SIZE=16384M")

	// zsize is `env -u RUNINK_ZRAM_SIZE -u RUNINK_ZRAM_DEFAULT VARS... sh runink-zram.sh
	// --print-size 2>/dev/null`: the zram script with only the given variables of its own.
	zsize := func(vars ...string) string {
		args := append([]string{"-u", "RUNINK_ZRAM_SIZE", "-u", "RUNINK_ZRAM_DEFAULT"}, vars...)
		args = append(args, "sh", zram, "--print-size")
		out, _ := capture(ctx, pipe.Cmd("env", args...), nil)
		return out
	}
	absent := filepath.Join(t, "absent")
	c.eq("runink-zram reads the plan's size", zsize("RUNINK_ZRAM_CONF="+z), "16384M")
	c.eq("the environment overrides the plan", zsize("RUNINK_ZRAM_CONF="+z, "RUNINK_ZRAM_SIZE=4G"), "4G")
	c.eq("no zram.conf: the image default", zsize("RUNINK_ZRAM_CONF="+absent, "RUNINK_ZRAM_DEFAULT=8G"), "8G")
	c.eq("no zram.conf, no default: 16G", zsize("RUNINK_ZRAM_CONF="+absent), "16G")
	quoted := filepath.Join(t, "quoted.conf")
	if err := os.WriteFile(quoted, []byte("RUNINK_ZRAM_SIZE=\"12G\"\n"), 0o644); err != nil {
		return err
	}
	c.eq("a quoted value is read", zsize("RUNINK_ZRAM_CONF="+quoted), "12G")
	bad := filepath.Join(t, "bad.conf")
	if err := os.WriteFile(bad, []byte("RUNINK_ZRAM_SIZE=$(reboot)\n"), 0o644); err != nil {
		return err
	}
	c.eq("a malformed zram.conf falls back to the default", zsize("RUNINK_ZRAM_CONF="+bad, "RUNINK_ZRAM_DEFAULT=2G"), "2G")

	if c.n == 0 {
		fmt.Fprintln(errw, "memtune: no checks ran")
		return errors.New("memtune: no checks ran")
	}
	if c.failed != 0 {
		fmt.Fprintln(errw, "memtune: FAILED")
		return fmt.Errorf("memtune: %d of %d checks failed", c.failed, c.n)
	}
	fmt.Fprintf(outw, "memtune: %d checks ok\n", c.n)
	return nil
}

// matching is `grep RE` over the lines of s.
func matching(s string, re *regexp.Regexp) []string {
	var out []string
	for _, l := range lines(s) {
		if re.MatchString(l) {
			out = append(out, l)
		}
	}
	return out
}

// countMatching is `grep -c RE`, as the string the script compared.
func countMatching(s string, re *regexp.Regexp) string {
	return fmt.Sprint(len(matching(s, re)))
}

var arcLine = regexp.MustCompile(`^options zfs zfs_arc_max=([0-9]*)$`)

// arcValue is `sed -n 's/^options zfs zfs_arc_max=\([0-9]*\)$/\1/p'`.
func arcValue(s string) string {
	var out []string
	for _, l := range lines(s) {
		if m := arcLine.FindStringSubmatch(l); m != nil {
			out = append(out, m[1])
		}
	}
	return strings.Join(out, "\n")
}
