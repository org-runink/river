// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package repocmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
)

const pkgSuffix = ".pkg.tar.zst"

// dbFile and filesFile are the archives repo-add writes; runink.db and runink.files are its
// symlinks to them, and the names pacman fetches.
var (
	dbFile    = RepoName + ".db.tar.zst"
	filesFile = RepoName + ".files.tar.zst"
)

// repoAdd runs repo-add in dir; tests replace it when repo-add is not installed.
var repoAdd = func(ctx context.Context, dir string, pkgs []string, w io.Writer) error {
	args := append([]string{"--quiet", "--nocolor", dbFile}, pkgs...)
	return pipe.Run(ctx, pipe.IO{Stdout: w, Stderr: w},
		pipe.Command{Name: "repo-add", Args: args, Dir: dir, Env: []string{"LC_ALL=C"}})
}

// listPackages returns the *.pkg.tar.zst in dir (sorted), each resolved through symlinks.
func listPackages(dir string) (names, resolved []string, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), pkgSuffix) {
			continue
		}
		r, err := filepath.EvalSymlinks(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, nil, err
		}
		names = append(names, e.Name())
		resolved = append(resolved, r)
	}
	return names, resolved, nil
}

// inspectAll reads every package (in parallel, one zstd each) and checks each is public.
// It returns the packages and one line per problem.
func inspectAll(ctx context.Context, names, resolved []string) ([]*Package, []string, error) {
	type job struct{ name, path string }
	jobs := make([]job, len(names))
	for i := range names {
		jobs[i] = job{names[i], resolved[i]}
	}
	p := pipe.New(ctx)
	out := pipe.ParallelMap(p, pipe.Slice(p, jobs...), max(1, runtime.NumCPU()/2),
		func(ctx context.Context, j job) (*Package, error) {
			pk, err := OpenPackage(ctx, j.path)
			if err != nil {
				return nil, err
			}
			pk.File = j.name
			return pk, nil
		})
	pkgs, err := pipe.Collect(p, out)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].File < pkgs[j].File })
	var problems []string
	seen := map[string]string{}
	for _, pk := range pkgs {
		for _, why := range CheckPublic(pk) {
			problems = append(problems, pk.File+": "+why)
		}
		if prev, dup := seen[pk.Name]; dup {
			problems = append(problems, fmt.Sprintf("%s: a second %s (also %s); a repository holds one version", pk.File, pk.Name, prev))
		}
		seen[pk.Name] = pk.File
	}
	return pkgs, problems, nil
}

func refusal(what string, problems []string) error {
	return fmt.Errorf("%s: refusing to %s: %d problem(s):\n  %s", tool, what, len(problems), strings.Join(problems, "\n  "))
}

// Assemble builds the unsigned repository in out from the packages in from: it refuses the
// whole set if any package is not public, then copies them, runs repo-add and writes the
// SHA256SUMS that base/river-sign signs from.
func Assemble(ctx context.Context, from, out string, w io.Writer) error {
	if from == "" || out == "" {
		return fmt.Errorf("%s: assemble needs --from and --out", tool)
	}
	fromAbs, err := filepath.Abs(from)
	if err != nil {
		return err
	}
	if fromAbs, err = filepath.EvalSymlinks(fromAbs); err != nil {
		return fmt.Errorf("%s: --from: %w", tool, err)
	}
	if privatePath(fromAbs) {
		return fmt.Errorf("%s: refusing %s: a directory named for a private build or the server profile is never a public repository's source", tool, fromAbs)
	}
	if err := emptyOrAbsent(out); err != nil {
		return err
	}
	if err := needTool("zstd", "repo-add"); err != nil {
		return err
	}
	names, resolved, err := listPackages(fromAbs)
	if err != nil {
		return fmt.Errorf("%s: --from: %w", tool, err)
	}
	if len(names) == 0 {
		return fmt.Errorf("%s: no *%s in %s", tool, pkgSuffix, fromAbs)
	}
	pkgs, problems, err := inspectAll(ctx, names, resolved)
	if err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}
	if len(problems) > 0 {
		return refusal("assemble a public repository", problems)
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	var files []string
	for _, pk := range pkgs {
		if err := copyFile(pk.Path, filepath.Join(out, pk.File)); err != nil {
			return err
		}
		files = append(files, pk.File)
	}
	if err := repoAdd(ctx, out, files, w); err != nil {
		return fmt.Errorf("%s: repo-add: %w", tool, err)
	}
	for _, f := range []string{dbFile, filesFile} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			return fmt.Errorf("%s: repo-add wrote no %s: %w", tool, f, err)
		}
	}
	if err := writeSums(out, append(files, dbFile, filesFile)); err != nil {
		return err
	}
	for _, pk := range pkgs {
		note := ""
		if pk.Size > PagesMaxBytes {
			note = "  (over 100 MiB: GitHub release only, not on the Pages mirror)"
		}
		fmt.Fprintf(w, "%s: %12d  %s%s\n", tool, pk.Size, pk.File, note)
	}
	fmt.Fprintf(w, "%s: assembled %d public package(s) into %s (UNSIGNED)\n", tool, len(pkgs), out)
	fmt.Fprintf(w, "%s: next, the owner signs it offline: base/river-sign %s\n", tool, out)
	return nil
}

func emptyOrAbsent(dir string) error {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(ents) > 0 {
		return fmt.Errorf("%s: %s is not empty; assemble writes a fresh repository", tool, dir)
	}
	return nil
}

// copyFile copies src's content to dst (a regular file, 0644), through a temporary name.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tmp-"+filepath.Base(dst)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeSums writes dir/SHA256SUMS in sha256sum's format, which base/river-sign checks.
func writeSums(dir string, names []string) error {
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		s, err := sha256File(filepath.Join(dir, n))
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", s, n)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(b.String()), 0o644)
}
