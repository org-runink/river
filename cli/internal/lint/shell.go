// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// shell — run shellcheck across all shell scripts in the repo (installer, build, scripts,
// tests, base, bench, and the overlay usr/local/bin scripts). Non-fatal if shellcheck is
// absent (prints a note), so `make lint` still runs the other lints on minimal environments;
// --strict (Tier 1) makes a missing shellcheck, or a search that finds no script, a failure.
//
// It is the gate for the shell not yet ported to Go (scripts/shell-ratchet.txt).
//
// Excluded checks:
//
//	SC1091 — can't follow non-constant source (overlay scripts source runtime paths)
//	SC2015 — "A && B || C is not if-then-else": intentional in the ok/bad test helpers,
//	         where the success branch (ok) always returns 0, so C never runs spuriously.
//	SC2209, SC2024 — as the script excluded them.

// shellRoots are where scripts are looked for, relative to the repository; the profile
// entries are globs.
var shellRoots = []string{
	"installer", "build", "scripts", "tests", "base", "bench", "install.sh",
	"iso-profiles/*/root-overlay/usr/local/bin",
	"iso-profiles/*/live-overlay/usr/local/bin",
}

var shellcheckExcludes = []string{"SC1091", "SC2015", "SC2209", "SC2024"}

// shellShebang is the script's `head -1 | grep -qE '^#!.*(sh|bash)'`.
var shellShebang = regexp.MustCompile(`^#!.*(sh|bash)`)

func init() { extraLints = append(extraLints, shellCmd) }

func shellCmd(v *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:   "shell",
		Short: "shellcheck every shell script still in the tree",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Shell(cmd.Context(), v.GetString("repo"), ShellOptions{
				Severity: v.GetString("severity"),
				Strict:   v.GetBool("strict"),
			}, cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
	c.Flags().String("severity", "", "shellcheck --severity (empty: shellcheck's default, style)")
	c.Flags().Bool("strict", false, "fail, rather than skip, when shellcheck is missing or no script is found")
	return c
}

// ShellOptions tune the shell lint.
type ShellOptions struct {
	Severity string // passed to shellcheck --severity when set
	Strict   bool   // a missing shellcheck or an empty search fails instead of skipping
}

// Shell runs shellcheck over every shell script under the repo's script directories.
func Shell(ctx context.Context, repo string, o ShellOptions, errw, outw io.Writer) error {
	sc, err := exec.LookPath("shellcheck")
	if err != nil {
		if o.Strict {
			return fmt.Errorf("lint-shell: shellcheck not installed")
		}
		fmt.Fprintln(outw, "lint-shell: shellcheck not installed — skipping (install it for full linting)")
		return nil
	}

	files, err := shellScripts(repo)
	if err != nil {
		return fmt.Errorf("lint-shell: %w", err)
	}
	if len(files) == 0 {
		if o.Strict {
			return fmt.Errorf("lint-shell: no shell scripts found — the search paths are stale")
		}
		fmt.Fprintln(outw, "lint-shell: no shell scripts found")
		return nil
	}
	fmt.Fprintf(outw, "lint-shell: checking %d scripts\n", len(files))

	// -x follows sourced files.
	args := []string{"-x"}
	if o.Severity != "" {
		args = append(args, "--severity="+o.Severity)
	}
	for _, e := range shellcheckExcludes {
		args = append(args, "-e", e)
	}
	args = append(args, files...)
	if err := pipe.Run(ctx, pipe.IO{Stdout: outw, Stderr: errw}, pipe.Command{Name: sc, Args: args, Dir: repo}); err != nil {
		fmt.Fprintln(errw, "lint-shell: shellcheck reported issues")
		return fmt.Errorf("lint-shell: shellcheck reported issues")
	}
	fmt.Fprintln(outw, "lint-shell: OK")
	return nil
}

// shellScripts finds the scripts shellcheck covers, as sorted repo-relative paths: files
// ending in .sh, or whose first line is a sh/bash shebang. Build outputs, upstream checkouts
// and makepkg's src/ and pkg/ are pruned.
func shellScripts(repo string) ([]string, error) {
	var roots []string
	for _, r := range shellRoots {
		if strings.Contains(r, "*") {
			m, err := filepath.Glob(filepath.Join(repo, r))
			if err != nil {
				return nil, err
			}
			for _, p := range m {
				rel, _ := filepath.Rel(repo, p)
				roots = append(roots, filepath.ToSlash(rel))
			}
			continue
		}
		roots = append(roots, r)
	}
	pruneName := map[string]bool{".out": true, ".upstream": true, "node_modules": true, ".git": true}
	prunePath := func(rel string) bool {
		if rel == "build/artifacts" {
			return true
		}
		if m, _ := filepath.Match("build/pkgbuilds/*/src", rel); m {
			return true
		}
		m, _ := filepath.Match("build/pkgbuilds/*/pkg", rel)
		return m
	}

	seen := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(filepath.Join(repo, root), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil // a root that does not exist (find's error went to /dev/null)
				}
				return err
			}
			rel, _ := filepath.Rel(repo, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if pruneName[d.Name()] || prunePath(rel) {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // find -type f: no symlinks
			}
			if strings.HasSuffix(rel, ".sh") {
				seen[rel] = true
				return nil
			}
			ok, err := firstLineMatches(p, shellShebang)
			if err != nil {
				return err
			}
			if ok {
				seen[rel] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	files := make([]string, 0, len(seen))
	for f := range seen {
		files = append(files, f)
	}
	slices.Sort(files)
	return files, nil
}

// firstLineMatches reports whether the first line of the file at p matches re.
func firstLineMatches(p string, re *regexp.Regexp) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	head := make([]byte, 2)
	if n, _ := io.ReadFull(br, head); n < 2 || string(head) != "#!" {
		return false, nil
	}
	line, err := br.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	return re.MatchString("#!" + strings.TrimRight(line, "\n")), nil
}
