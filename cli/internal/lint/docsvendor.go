// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// docs-vendor — the documentation website (website/) builds OFFLINE from vendored copies of
// its two third-party inputs. This asserts they are exactly the pinned ones:
//
//   - the Hextra theme, a Hugo module pinned in website/go.mod + go.sum and vendored into
//     website/_vendor/ by `hugo mod vendor` (plus its upstream LICENSE, copied in by hand
//     because `hugo mod vendor` copies only the mounted directories). The whole vendored tree
//     is pinned by one digest: sha256 over the sorted `sha256sum` listing of its files (the
//     listing `cd website/_vendor && find . -type f -print0 | LC_ALL=C sort -z | xargs -0
//     sha256sum` prints, so the digest can still be checked by hand).
//   - FlexSearch, vendored as one file under website/assets/lib/flexsearch/ (see SOURCE there).
//
// TO BUMP HEXTRA: in website/, `hugo mod get github.com/imfing/hextra@vX.Y.Z`, then
// `hugo mod vendor`, copy the release's LICENSE into _vendor/github.com/imfing/hextra/, and
// update hextraVersion and hextraTreeSHA256 below (this lint prints the new digest).

// The pins. Variables, not constants, only so the tests can point them at a fixture.
var (
	hextraVersion    = "v0.12.3"
	hextraTreeSHA256 = "587a0529502cf2c0f5c62aee14aec72c0118ec8b1bfb32e19fd92eef9aebd72e"
	hextraMinFiles   = 100
	flexsearchFile   = "website/assets/lib/flexsearch/flexsearch.bundle.min.js"
	flexsearchSHA256 = "433e941a8a573ebb9931fc16fc75266ab6b93f569ac2fb4f3dc66882e0416f4c"
)

func init() { extraLints = append(extraLints, docsVendorCmd) }

func docsVendorCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "docs-vendor",
		Short: "the docs website's vendored Hextra theme and FlexSearch are the pinned copies",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return DocsVendor(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

// DocsVendor runs the lint over repo.
func DocsVendor(_ context.Context, repo string, errw, outw io.Writer) error {
	r := &report{name: "lint-docs-vendor", w: errw}
	path := func(p string) string { return filepath.Join(repo, p) }

	// 1. go.mod and the vendor listing name the same, exact release.
	if !fileHasMatch(path("website/go.mod"), `(?m)^`+regexp.QuoteMeta("require github.com/imfing/hextra "+hextraVersion)+`$`) {
		r.fail("website/go.mod does not require github.com/imfing/hextra %s", hextraVersion)
	}
	if !fileHasMatch(path("website/go.sum"), `(?m)^`+regexp.QuoteMeta("github.com/imfing/hextra "+hextraVersion+" h1:")) {
		r.fail("website/go.sum has no hash for github.com/imfing/hextra %s", hextraVersion)
	}
	if !fileHasMatch(path("website/_vendor/modules.txt"), `(?m)^`+regexp.QuoteMeta("# github.com/imfing/hextra "+hextraVersion)+`$`) {
		r.fail("website/_vendor/modules.txt is not github.com/imfing/hextra %s", hextraVersion)
	}
	if st, err := os.Stat(path("website/_vendor/github.com/imfing/hextra/LICENSE")); err != nil || !st.Mode().IsRegular() {
		r.fail("the vendored Hextra has no LICENSE (MIT requires the notice to travel with it)")
	}

	// 2. The vendored tree is byte-for-byte the one that was reviewed. Fail on an empty tree.
	n, got, err := vendorTreeDigest(path("website/_vendor"))
	if err != nil {
		return fmt.Errorf("lint-docs-vendor: %w", err)
	}
	if n < hextraMinFiles {
		r.fail("website/_vendor has only %d files (expected >= %d)", n, hextraMinFiles)
	}
	if got != hextraTreeSHA256 {
		r.fail("website/_vendor digest %s != pinned %s (edited, or bumped without updating the pin)", got, hextraTreeSHA256)
	}

	// 3. FlexSearch.
	if st, err := os.Stat(path(flexsearchFile)); err == nil && st.Mode().IsRegular() {
		got, err := sha256OfFile(path(flexsearchFile))
		if err != nil {
			return fmt.Errorf("lint-docs-vendor: %w", err)
		}
		if got != flexsearchSHA256 {
			r.fail("%s sha256 %s != pinned %s", flexsearchFile, got, flexsearchSHA256)
		}
	} else {
		r.fail("%s is missing", flexsearchFile)
	}

	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-docs-vendor: OK (Hextra %s, %d files; FlexSearch pinned)\n", hextraVersion, n)
	return nil
}

// vendorTreeDigest counts the regular files under dir and returns the sha256 of their
// `sha256sum` listing, "<hex>  ./<path>\n" per file in byte order of the path. A missing dir
// is zero files and the digest of an empty listing.
func vendorTreeDigest(dir string) (int, string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == dir {
				return filepath.SkipAll
			}
			return err
		}
		if d.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, "./"+filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return 0, "", err
	}
	slices.Sort(files) // LC_ALL=C sort: bytewise
	h := sha256.New()
	for _, f := range files {
		sum, err := sha256OfFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			return 0, "", err
		}
		fmt.Fprintf(h, "%s  %s\n", sum, f)
	}
	return len(files), hex.EncodeToString(h.Sum(nil)), nil
}

func sha256OfFile(p string) (string, error) {
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

// fileHasMatch reports whether the file at p exists and matches re. A missing file does not.
func fileHasMatch(p, re string) bool {
	b, err := os.ReadFile(p)
	return err == nil && regexp.MustCompile(re).Match(b)
}
