// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// shell-ratchet — River's internals move from shell to Go (docs/GO-CLI.md). The shell that
// exists today is listed in scripts/shell-ratchet.txt; this lint keeps that list honest in
// both directions:
//
//   - a tracked shell script that is NOT listed fails: new logic goes in Go;
//   - a listed path that is no longer a tracked shell script fails: a port removes its line,
//     so the list only ever shrinks.
//
// Shell a tool demands is exempt, as long as it stays a thin call into `river`:
//
//   - PKGBUILDs and pacman .install files (makepkg and pacman read bash);
//   - s6 `run`/`finish`/`up`/`down` files that are at most two lines of code, one of them
//     calling `river` (a new s6 service is `exec river service run <name>`);
//   - a profile drop-in, `*/etc/profile.d/*.sh` in a profile overlay. It is SOURCED INTO the
//     user's login shell, so it cannot be a Go binary, and bash only reads files matching
//     `*.sh` there, so it cannot be renamed out of the way either. The shell is the interface,
//     not an implementation choice -- the same reason PKGBUILDs are exempt.
//
// CI `run:` lines and the one sudo line are not files and are not scanned here.

const ratchetFile = "scripts/shell-ratchet.txt"

var shebangShell = regexp.MustCompile(`^#!\s*(/usr)?/bin/(env\s+)?(sh|bash|dash|ash|execlineb)(\s|$)`)

func init() { extraLints = append(extraLints, shellRatchetCmd) }

func shellRatchetCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "shell-ratchet",
		Short: "no new shell: every shell script is listed in " + ratchetFile + ", and the list only shrinks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ShellRatchet(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

// ShellRatchet runs the lint over repo.
func ShellRatchet(ctx context.Context, repo string, errw, outw io.Writer) error {
	r := &report{name: "lint-shell-ratchet", w: errw}

	listed, err := readRatchet(filepath.Join(repo, ratchetFile))
	if err != nil {
		return fmt.Errorf("lint-shell-ratchet: %w", err)
	}
	files, err := gitLines(ctx, repo, "ls-files")
	if err != nil {
		return fmt.Errorf("lint-shell-ratchet: git ls-files: %w", err)
	}
	if len(files) < 100 {
		return fmt.Errorf("lint-shell-ratchet: only %d tracked files; not looking at this repository", len(files))
	}

	shell := map[string]bool{}
	for _, f := range files {
		if strings.HasPrefix(f, "cli/vendor/") {
			continue
		}
		p := filepath.Join(repo, f)
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue // deleted in the working tree, a symlink, or a submodule
		}
		kind, err := shellKind(p, f)
		if err != nil {
			return fmt.Errorf("lint-shell-ratchet: %s: %w", f, err)
		}
		switch kind {
		case notShell, toolShell:
		case thinS6:
		case fatS6, script:
			shell[f] = true
		}
	}

	for f := range shell {
		if !listed[f] {
			r.fail("%s is a new shell script; write it in Go as a `river` subcommand (docs/GO-CLI.md)", f)
		}
	}
	for f := range listed {
		if !shell[f] {
			r.fail("%s is listed in %s but is no longer a tracked shell script; delete its line", f, ratchetFile)
		}
	}

	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-shell-ratchet: OK (%d shell scripts left to port)\n", len(listed))
	return nil
}

type kind int

const (
	notShell  kind = iota
	toolShell      // PKGBUILD, pacman .install or a profile.d drop-in: the tool demands shell
	thinS6         // an s6 control file that only calls river
	fatS6          // an s6 control file with logic in it: still to port
	script         // any other shell script
)

var s6Names = []string{"run", "finish", "up", "down"}

func shellKind(p, rel string) (kind, error) {
	base := path.Base(rel)
	if base == "PKGBUILD" || strings.HasSuffix(base, ".install") {
		return toolShell, nil
	}
	// A profile drop-in: sourced into the login shell, and bash reads only *.sh from there.
	if strings.Contains(rel, "/etc/profile.d/") && strings.HasSuffix(base, ".sh") {
		return toolShell, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return notShell, err
	}
	first, _, _ := bytes.Cut(b, []byte("\n"))
	isShell := strings.HasSuffix(base, ".sh") || shebangShell.Match(first)
	if !isShell {
		return notShell, nil
	}
	if !slices.Contains(s6Names, base) {
		return script, nil
	}
	code, callsRiver := 0, false
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		code++
		if regexp.MustCompile(`(^|[\s/])river(\s|$)`).MatchString(line) {
			callsRiver = true
		}
	}
	if code <= 2 && callsRiver {
		return thinS6, nil
	}
	return fatS6, nil
}

// readRatchet reads the list: one path per line, # comments and blank lines ignored.
func readRatchet(p string) (map[string]bool, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m[line] {
			return nil, fmt.Errorf("%s: %s listed twice", ratchetFile, line)
		}
		m[line] = true
	}
	return m, nil
}
