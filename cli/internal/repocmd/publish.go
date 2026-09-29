// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package repocmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
)

// PublishOptions are `river repo publish`'s inputs.
type PublishOptions struct {
	Dir     string
	KeyFpr  string
	KeyFile string

	GitHub     bool   // upload to the rolling GitHub release
	GitHubRepo string // owner/name
	Tag        string
	Prune      bool // delete release assets the repository no longer has

	PagesOut      string // write the Pages mirror subset here
	PagesMaxBytes int64
}

// gh runs the GitHub CLI; tests replace it.
var gh = func(ctx context.Context, args ...string) ([]byte, error) {
	return pipe.Output(ctx, nil, pipe.Cmd("gh", args...))
}

// Publish sends a verified repository to its hosts. It refuses unless Verify passes and every
// package passes the public guard again, and it never signs anything.
func Publish(ctx context.Context, o PublishOptions, w io.Writer) error {
	if o.Dir == "" {
		return fmt.Errorf("%s: publish needs --dir", tool)
	}
	if !o.GitHub && o.PagesOut == "" {
		return fmt.Errorf("%s: publish needs --github, --pages-out or both", tool)
	}
	if o.PagesMaxBytes <= 0 {
		o.PagesMaxBytes = PagesMaxBytes
	}
	if o.Tag == "" {
		o.Tag = ReleaseTag
	}
	if o.GitHubRepo == "" {
		o.GitHubRepo = GitHubRepo
	}
	if o.GitHub {
		if err := needTool("gh"); err != nil {
			return err
		}
	}
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return err
	}
	if dir, err = filepath.EvalSymlinks(dir); err != nil {
		return fmt.Errorf("%s: --dir: %w", tool, err)
	}
	if privatePath(dir) {
		return fmt.Errorf("%s: refusing to publish %s: a directory named for a private build or the server profile", tool, dir)
	}
	if err := Verify(ctx, dir, o.KeyFpr, o.KeyFile, w); err != nil {
		return fmt.Errorf("%s: refusing to publish: %w", tool, err)
	}
	names, resolved, err := listPackages(dir)
	if err != nil {
		return err
	}
	pkgs, problems, err := inspectAll(ctx, names, resolved)
	if err != nil {
		return fmt.Errorf("%s: %w", tool, err)
	}
	if len(problems) > 0 {
		return refusal("publish", problems)
	}
	files, err := repoFiles(dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.size > ReleaseAssetMaxBytes {
			return fmt.Errorf("%s: %s is %d bytes, over GitHub's 2 GiB release asset limit", tool, f.name, f.size)
		}
	}
	if o.GitHub {
		if err := publishGitHub(ctx, o, dir, files, w); err != nil {
			return err
		}
	}
	if o.PagesOut != "" {
		if err := writePages(dir, o.PagesOut, files, pkgs, o.PagesMaxBytes, w); err != nil {
			return err
		}
	}
	return nil
}

type repoFile struct {
	name string
	size int64 // of the content, through a symlink
}

// repoFiles lists every file in dir (Verify has already refused anything unexpected).
func repoFiles(dir string) ([]repoFile, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []repoFile
	for _, e := range ents {
		st, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: %s is not a file", tool, e.Name())
		}
		out = append(out, repoFile{e.Name(), st.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// isDBName is the database, file list and their signatures: uploaded after the packages, so
// a database never names a package the release does not have yet.
func isDBName(n string) bool {
	return strings.HasPrefix(n, RepoName+".db") || strings.HasPrefix(n, RepoName+".files") || strings.HasPrefix(n, "SHA256SUMS")
}

type ghRelease struct {
	IsPrerelease bool `json:"isPrerelease"`
	IsDraft      bool `json:"isDraft"`
	Assets       []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

func publishGitHub(ctx context.Context, o PublishOptions, dir string, files []repoFile, w io.Writer) error {
	R := []string{"-R", o.GitHubRepo}
	out, err := gh(ctx, append([]string{"release", "view", o.Tag, "--json", "isPrerelease,isDraft,assets"}, R...)...)
	var rel ghRelease
	switch {
	case err != nil && strings.Contains(err.Error(), "release not found"):
		fmt.Fprintf(w, "%s: creating the %s release on %s (a prerelease, never latest)\n", tool, o.Tag, o.GitHubRepo)
		_, err = gh(ctx, append([]string{"release", "create", o.Tag, "--prerelease", "--latest=false",
			"--title", "Runink River package repository [" + RepoName + "] (x86_64)",
			"--notes", "The signed pacman repository installed Runink River systems update from. " +
				"Its assets are replaced on every publish; see docs/REPOSITORY.md. Not a release of the image."}, R...)...)
		if err != nil {
			return fmt.Errorf("%s: gh release create: %w", tool, err)
		}
		rel.IsPrerelease = true
	case err != nil:
		return fmt.Errorf("%s: gh release view %s: %w", tool, o.Tag, err)
	default:
		if err := json.Unmarshal(out, &rel); err != nil {
			return fmt.Errorf("%s: gh release view %s: %w", tool, o.Tag, err)
		}
	}
	// A repository release that is not a prerelease could become "latest", and install.sh
	// downloads the image from the latest release.
	if !rel.IsPrerelease || rel.IsDraft {
		return fmt.Errorf("%s: the %s release must be a published prerelease (so it is never \"latest\"); fix it by hand before publishing", tool, o.Tag)
	}
	var pkgPaths, dbPaths []string
	local := map[string]bool{}
	for _, f := range files {
		local[f.name] = true
		if isDBName(f.name) {
			dbPaths = append(dbPaths, filepath.Join(dir, f.name))
		} else {
			pkgPaths = append(pkgPaths, filepath.Join(dir, f.name))
		}
	}
	// Packages first, the database last: pacman must never read a database naming a package
	// the release does not hold yet. gh reads a symlink's target under the link's own name.
	for _, set := range [][]string{pkgPaths, dbPaths} {
		if len(set) == 0 {
			continue
		}
		args := append(append([]string{"release", "upload", o.Tag, "--clobber"}, set...), R...)
		if _, err := gh(ctx, args...); err != nil {
			return fmt.Errorf("%s: gh release upload: %w", tool, err)
		}
	}
	fmt.Fprintf(w, "%s: uploaded %d file(s) to %s release %s\n", tool, len(files), o.GitHubRepo, o.Tag)
	if !o.Prune {
		return nil
	}
	// Assets the new database no longer names (superseded versions), removed only after the
	// new database is up.
	var stale []string
	for _, a := range rel.Assets {
		if !local[a.Name] {
			stale = append(stale, a.Name)
		}
	}
	sort.Strings(stale)
	for _, n := range stale {
		if _, err := gh(ctx, append([]string{"release", "delete-asset", o.Tag, n, "--yes"}, R...)...); err != nil {
			return fmt.Errorf("%s: gh release delete-asset %s: %w", tool, n, err)
		}
		fmt.Fprintf(w, "%s: removed stale asset %s\n", tool, n)
	}
	return nil
}

// writePages writes the Pages mirror subset into out: every file except the packages over
// max (and their signatures), as regular files (a symlink does not survive a site build).
// Repository files already in out that are not in the new set are removed; anything else in
// out makes it refuse, since out is meant to hold the mirror and nothing else.
func writePages(dir, out string, files []repoFile, pkgs []*Package, maxBytes int64, w io.Writer) error {
	big := map[string]bool{}
	for _, p := range pkgs {
		if p.Size > maxBytes {
			big[p.File] = true
		}
	}
	keep := map[string]bool{}
	var skipped []string
	for _, f := range files {
		base := strings.TrimSuffix(f.name, ".sig")
		if big[base] {
			if base == f.name {
				skipped = append(skipped, fmt.Sprintf("%s (%d bytes)", f.name, f.size))
			}
			continue
		}
		if f.size > maxBytes {
			return fmt.Errorf("%s: %s is %d bytes, over the Pages limit, and it is not a package the mirror may omit", tool, f.name, f.size)
		}
		keep[f.name] = true
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	ents, err := os.ReadDir(out)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !repoFileRE.MatchString(e.Name()) || e.IsDir() {
			return fmt.Errorf("%s: --pages-out %s holds %s, which is not a repository file; point it at the mirror's own directory", tool, out, e.Name())
		}
	}
	names := make([]string, 0, len(keep))
	for n := range keep {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := copyFile(filepath.Join(dir, n), filepath.Join(out, n)); err != nil {
			return err
		}
	}
	for _, e := range ents {
		if !keep[e.Name()] {
			if err := os.Remove(filepath.Join(out, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			fmt.Fprintf(w, "%s: pages: removed %s\n", tool, e.Name())
		}
	}
	fmt.Fprintf(w, "%s: pages: wrote %d file(s) to %s\n", tool, len(names), out)
	for _, s := range skipped {
		fmt.Fprintf(w, "%s: pages: NOT mirrored, over the Pages limit (GitHub release only): %s\n", tool, s)
	}
	return nil
}
