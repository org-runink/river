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
		c.AddCommand(
			simpleCmd(v, "memtune", "the install plan's ZFS ARC and zram sizes, in a scratch root", Memtune),
			simpleCmd(v, "firstboot-hooks", "the first-boot hook contract (river-firstboot-hooks), in a scratch root", FirstbootHooks),
			simpleCmd(v, "external-profile", "a profile outside this repository: described, staged, linted", ExternalProfile),
			simpleCmd(v, "installed-hooks", "the profile-hook contract of qemu-gui-test (build/qemu-hooks.sh), without a VM", InstalledHooks),
			simpleCmd(v, "models-required", "the model payload step's required mode (72-models-payload), with fake zfs", ModelsRequired),
			simpleCmd(v, "models-fetch", "build/models-fetch.sh: MODELS_ONLY and HF_TOKEN_FILE, against a local upstream", ModelsFetch),
			sddmThemeCmd(v),
		)
		return c
	})
}

// A Test is one ported script: it runs against the checkout at repo, writing its progress to
// outw and its failures to errw, and returns an error when the script would have exited
// non-zero.
type Test func(ctx context.Context, repo string, outw, errw io.Writer) error

func simpleCmd(v *viper.Viper, name, short string, t Test) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return t(cmd.Context(), v.GetString("repo"), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

// checks counts results the way the scripts' ok/ko helpers did: "  ok    <what>" on stdout,
// "  FAIL  <what>" on stderr.
type checks struct {
	out, err  io.Writer
	n, failed int
}

func (c *checks) ok(d string) {
	c.n++
	fmt.Fprintf(c.out, "  ok    %s\n", d)
}

func (c *checks) ko(d string) {
	c.n++
	c.failed++
	fmt.Fprintf(c.err, "  FAIL  %s\n", d)
}

// expect is ok(d) when cond holds, else ko(d).
func (c *checks) expect(d string, cond bool) {
	if cond {
		c.ok(d)
	} else {
		c.ko(d)
	}
}

// eq compares a value with the one wanted and names both on a failure.
func (c *checks) eq(d, got, want string) {
	if got == want {
		c.ok(d)
	} else {
		c.ko(fmt.Sprintf("%s: got '%s', want '%s'", d, got, want))
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
