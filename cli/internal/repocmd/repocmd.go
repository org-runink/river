// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package repocmd is `river repo`: it assembles, verifies and publishes Runink River's public,
// signed pacman repository [runink] (docs/REPOSITORY.md).
//
// The three steps sit either side of the one step it never does, signing:
//
//	river repo assemble --from BUILT --out DIR   public packages only, repo-add, SHA256SUMS
//	base/river-sign DIR                          the owner, offline, with the release key
//	river repo verify --dir DIR                  every package and the database signed by it
//	river repo publish --dir DIR --github --pages-out SITE/static/river/repo/x86_64
//
// assemble is the guard that keeps a downstream payload out of a public mirror, and publish
// runs that guard again, and verify, before it sends anything anywhere.
package repocmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/org-runink/river/cli/internal/rootcmd"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	// RepoName is the pacman repository name: [runink], files runink.db and runink.files.
	RepoName = "runink"
	// ReleaseTag is the rolling GitHub release that holds the repository's files.
	ReleaseTag = "repo-x86_64"
	// GitHubRepo is where that release lives.
	GitHubRepo = "org-runink/river"
	// PagesMaxBytes is GitHub's per-file limit for a git repository, and so for the Pages
	// mirror: 100 MiB. A package over it is served by the GitHub release only.
	PagesMaxBytes int64 = 100 << 20
	// ReleaseAssetMaxBytes is GitHub's limit for one release asset (2 GiB).
	ReleaseAssetMaxBytes int64 = 2 << 30
)

const tool = "river-repo"

func init() {
	rootcmd.Register(func(v *viper.Viper) *cobra.Command {
		c := &cobra.Command{
			Use:   "repo",
			Short: "The public signed pacman repository [runink]: assemble, verify, publish",
			Long: "The public signed pacman repository [runink] (docs/REPOSITORY.md).\n" +
				"assemble builds it from River's own packages, the owner signs it offline with\n" +
				"base/river-sign, verify checks every signature, and publish uploads it. Nothing\n" +
				"here signs or ever reads a private key.",
		}
		c.AddCommand(assembleCmd(v), verifyCmd(v), publishCmd(v), installTestCmd(v))
		return c
	})
}

func assembleCmd(v *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:   "assemble",
		Short: "Build the repository directory from River's public packages (refuses anything else)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Assemble(cmd.Context(), v.GetString("from"), v.GetString("out"), cmd.OutOrStdout())
		},
	}
	c.Flags().String("from", "", "directory of built *.pkg.tar.zst (required)")
	c.Flags().String("out", "", "the repository directory to create; must not exist or be empty (required)")
	return c
}

func keyFlags(c *cobra.Command) {
	c.Flags().String("repo", ".", "the river checkout (for the default key fingerprint and public key)")
	c.Flags().String("key-fpr", "", "fingerprint of the release key's PRIMARY key (default: base/keys/owner/fingerprint)")
	c.Flags().String("key-file", "", "the release key's armored public key (default: base/keys/owner/<fpr>.asc)")
}

func verifyCmd(v *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:   "verify",
		Short: "Check every package and the database carry a valid signature by the release key",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fpr, keyFile, err := resolveKey(v.GetString("repo"), v.GetString("key-fpr"), v.GetString("key-file"))
			if err != nil {
				return err
			}
			return Verify(cmd.Context(), v.GetString("dir"), fpr, keyFile, cmd.OutOrStdout())
		},
	}
	c.Flags().String("dir", "", "the signed repository directory (required)")
	keyFlags(c)
	return c
}

func publishCmd(v *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:   "publish",
		Short: "Upload a verified repository to the GitHub release and/or write the Pages mirror subset",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fpr, keyFile, err := resolveKey(v.GetString("repo"), v.GetString("key-fpr"), v.GetString("key-file"))
			if err != nil {
				return err
			}
			return Publish(cmd.Context(), PublishOptions{
				Dir:           v.GetString("dir"),
				KeyFpr:        fpr,
				KeyFile:       keyFile,
				GitHub:        v.GetBool("github"),
				GitHubRepo:    v.GetString("gh-repo"),
				Tag:           v.GetString("tag"),
				Prune:         v.GetBool("prune"),
				PagesOut:      v.GetString("pages-out"),
				PagesMaxBytes: v.GetInt64("pages-max-bytes"),
			}, cmd.OutOrStdout())
		},
	}
	c.Flags().String("dir", "", "the signed repository directory (required)")
	c.Flags().Bool("github", false, "upload to the GitHub release (gh release upload --clobber)")
	c.Flags().String("gh-repo", GitHubRepo, "the GitHub repository holding the release")
	c.Flags().String("tag", ReleaseTag, "the rolling release tag")
	c.Flags().Bool("prune", true, "delete release assets the repository no longer has, after the upload")
	c.Flags().String("pages-out", "", "write the Pages mirror subset here (the site's static/river/repo/x86_64)")
	c.Flags().Int64("pages-max-bytes", PagesMaxBytes, "largest file the Pages mirror takes")
	keyFlags(c)
	return c
}

// resolveKey fills in the fingerprint and the public key from the checkout when not given.
func resolveKey(repo, fpr, keyFile string) (string, string, error) {
	if fpr == "" {
		b, err := os.ReadFile(filepath.Join(repo, "base/keys/owner/fingerprint"))
		if err != nil {
			return "", "", fmt.Errorf("%s: no --key-fpr and no base/keys/owner/fingerprint under --repo %s: %w", tool, repo, err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				fpr = line
				break
			}
		}
	}
	fpr, err := normFpr(fpr)
	if err != nil {
		return "", "", err
	}
	if keyFile == "" {
		keyFile = filepath.Join(repo, "base/keys/owner", fpr+".asc")
	}
	return fpr, keyFile, nil
}

func normFpr(s string) (string, error) {
	f := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	if len(f) != 40 || strings.Trim(f, "0123456789ABCDEF") != "" {
		return "", fmt.Errorf("%s: key fingerprint must be 40 hex digits, got %q", tool, s)
	}
	return f, nil
}
