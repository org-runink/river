// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package testcmd holds the host-side checks that were tests/*.sh, as `river test <name>`:
// the Tier 1 contract tests (scripts/ci-tier1.sh) and the developer's SDDM render check.
//
// Most of them test SHELL code that is not ported yet (a sourced library, a first-boot hook
// runner, the image's zram script). They drive that code exactly as the scripts did, as a
// program run through pkg/pipe, and check what it did with the standard library. The scripts
// that run on a booted target or inside a VM (tests/assert-golden.sh, tests/smoke-k0s.sh, the
// boot tests) are not here; they belong to a later phase (docs/GO-CLI.md, "Order").
package testcmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/org-runink/river/cli/internal/evidence"
	"github.com/org-runink/river/cli/internal/rootcmd"
	"github.com/org-runink/river/pkg/pipe"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootcmd.Register(func(v *viper.Viper) *cobra.Command {
		c := &cobra.Command{
			Use:   "test",
			Short: "Host-side contract tests (were tests/*.sh)",
		}
		c.PersistentFlags().String("repo", ".", "the river checkout to test")
		c.PersistentFlags().String("evidence", "", "also write the run's release-evidence document (JSON) to this file")
		c.PersistentFlags().String("evidence-host-kind", "", "environment.host_kind in the evidence: server-node, dev-box or ci (default: ci when $CI is set, else dev-box)")
		c.AddCommand(
			simpleCmd(v, "memtune", "the install plan's ZFS ARC and zram sizes, in a scratch root", Memtune, memtuneChecks),
			simpleCmd(v, "firstboot-hooks", "the first-boot hook contract (river-firstboot-hooks), in a scratch root", FirstbootHooks, firstbootChecksDeclared),
			simpleCmd(v, "external-profile", "a profile outside this repository: described, staged, linted", ExternalProfile, externalProfileChecks),
			simpleCmd(v, "installed-hooks", "the profile-hook contract of qemu-gui-test (build/qemu-hooks.sh), without a VM", InstalledHooks, installedHooksDeclared),
			simpleCmd(v, "models-required", "the model payload step's required mode (72-models-payload), with fake zfs", ModelsRequired, modelsRequiredChecks),
			simpleCmd(v, "models-fetch", "build/models-fetch.sh: MODELS_ONLY and HF_TOKEN_FILE, against a local upstream", ModelsFetch, modelsFetchChecks),
			sddmThemeCmd(v),
		)
		return c
	})
}

// A Test is one ported script: it runs against the checkout at repo, writing its progress to
// outw and its failures to errw, and returns an error when the script would have exited
// non-zero. When ctx carries an evidence run (--evidence), every check it reports is also
// recorded there, under its stable machine name.
type Test func(ctx context.Context, repo string, outw, errw io.Writer) error

func simpleCmd(v *viper.Viper, name, short string, t Test, declared []string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repo := v.GetString("repo")
			return withEvidence(cmd.Context(), evidenceOptsFrom(v), tier1Harness(name), declared, repo,
				func(ctx context.Context) error {
					return t(ctx, repo, cmd.OutOrStdout(), cmd.ErrOrStderr())
				})
		},
	}
}

// checks counts results the way the scripts' ok/ko helpers did: "  ok    <what>" on stdout,
// "  FAIL  <what>" on stderr. That text is unchanged by evidence: scripts and CI parse it.
//
// Every result also carries a stable machine NAME, which is what the evidence document
// records (the human text embeds values, so it cannot be a name). Each subcommand declares
// its names up front in a static list; a name reported but not declared, or declared but
// never reported, fails the evidence run.
type checks struct {
	out, err  io.Writer
	n, failed int
	run       *evidence.Run // nil unless the run writes evidence
	redact    func(string) string
}

// newChecks is the one way a subcommand gets its checks: they record into the evidence run
// ctx carries, if any.
func newChecks(ctx context.Context, outw, errw io.Writer) *checks {
	c := &checks{out: outw, err: errw}
	if e := evidenceFrom(ctx); e != nil {
		c.run, c.redact = e.run, e.redact
	}
	return c
}

func (c *checks) record(name, verdict, d string) {
	if c.run == nil {
		return
	}
	d = c.redact(d)
	switch verdict {
	case evidence.Pass:
		c.run.Pass(name, d)
	case evidence.Skip:
		c.run.Skip(name, d)
	default:
		c.run.Fail(name, d)
	}
}

func (c *checks) ok(name, d string) {
	c.n++
	fmt.Fprintf(c.out, "  ok    %s\n", d)
	c.record(name, evidence.Pass, d)
}

func (c *checks) ko(name, d string) {
	c.n++
	c.failed++
	fmt.Fprintf(c.err, "  FAIL  %s\n", d)
	c.record(name, evidence.Fail, d)
}

// expect is ok(d) when cond holds, else ko(d).
func (c *checks) expect(name, d string, cond bool) {
	if cond {
		c.ok(name, d)
	} else {
		c.ko(name, d)
	}
}

// eq compares a value with the one wanted and names both on a failure.
func (c *checks) eq(name, d, got, want string) {
	if got == want {
		c.ok(name, d)
	} else {
		c.ko(name, fmt.Sprintf("%s: got '%s', want '%s'", d, got, want))
	}
}

// skipped is a conditional check that does not apply on this host. The human output keeps
// the single "ok" line the script printed; the evidence records every named check as a skip
// with the reason, because a skip is never a pass.
func (c *checks) skipped(d, reason string, names ...string) {
	c.n++
	fmt.Fprintf(c.out, "  ok    %s\n", d)
	for _, n := range names {
		c.record(n, evidence.Skip, reason)
	}
}

// sourceCall is the one fixed piece of shell this package runs with -c. It sources a shell
// library (the code under test, named by path in $1) and calls one of its functions with the
// remaining arguments. Everything reaches it as positional parameters, so no value is ever
// spliced into shell text.
const sourceCall = `. "$1" && shift && "$@"`

// libCall is `. LIB; FN ARGS...` as a command.
func libCall(lib, fn string, args ...string) pipe.Command {
	return pipe.Cmd("sh", append([]string{"-c", sourceCall, "sh", lib, fn}, args...)...)
}

// capture runs c and returns its stdout with trailing newlines removed, like $(...). The
// command's stderr goes to stderr (nil: discarded, like 2>/dev/null).
func capture(ctx context.Context, c pipe.Command, stderr io.Writer) (string, error) {
	var out bytes.Buffer
	err := pipe.Run(ctx, pipe.IO{Stdout: &out, Stderr: stderr}, c)
	return strings.TrimRight(out.String(), "\n"), err
}

// combined runs c with stdout and stderr in one buffer, like `> file 2>&1`.
func combined(ctx context.Context, c pipe.Command) (string, error) {
	var b lockedBuffer
	err := pipe.Run(ctx, pipe.IO{Stdout: &b, Stderr: &b}, c)
	return b.String(), err
}

// lockedBuffer is a bytes.Buffer two copying goroutines (stdout and stderr) may write at once.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// lines splits s into its lines, without the empty one after a final newline.
func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// hasLine reports whether s has a line exactly equal to want (grep -qx with a fixed string).
func hasLine(s, want string) bool {
	for _, l := range lines(s) {
		if l == want {
			return true
		}
	}
	return false
}

// exists is `[ -e path ]`: true for anything, including a dangling symlink's target missing.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// mode is `stat -c %a path`, or "" when path is missing.
func mode(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return strconv.FormatUint(uint64(st.Mode().Perm()), 8)
}

// readTrim is `$(cat path)`: the file with trailing newlines removed, "" when unreadable.
func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

// touch is `: > path`.
func touch(path string) error { return os.WriteFile(path, nil, 0o644) }

// writeExec writes a script and sets its mode exactly (the scripts' printf + chmod).
func writeExec(path, body string, perm fs.FileMode) error {
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}

// appendFile is `printf ... >> path`.
func appendFile(path, s string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(s); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// copyTree is `cp -a src dst` for what a profile holds: directories, regular files and
// symlinks, with their permission bits. dst must not exist.
func copyTree(src, dst string) error {
	type dirMode struct {
		path string
		perm fs.FileMode
	}
	var dirs []dirMode
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			// Writable while it is filled; its own mode is set once everything is in.
			dirs = append(dirs, dirMode{to, info.Mode().Perm()})
			return os.Mkdir(to, 0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(target, to)
		case info.Mode().IsRegular():
			return copyFile(p, to, info.Mode().Perm())
		default:
			return fmt.Errorf("copy %s: not a file, directory or symlink", p)
		}
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i].path, dirs[i].perm); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, perm)
}

// listTree is `cd dir && find . | sort`: every path under dir, relative, sorted.
func listTree(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out = append(out, rel)
		return err
	})
	return out, err
}
