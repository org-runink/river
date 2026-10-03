// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package repocmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/org-runink/river/cli/internal/evidence"
	"github.com/org-runink/river/pkg/pipe"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// InstallTest answers the one question `river repo verify` cannot: CAN A NODE ACTUALLY INSTALL
// FROM [runink]?
//
// verify checks the database's shape and every signature. Both can be perfect while the
// repository is still unusable -- an unsatisfiable dependency, a package built against a version
// that is gone from the upstream repos, a database that lists a file it does not contain. The
// owner's complaint on 2026-10-02 was exactly this gap: the repository had never been published
// at all, so "an installed node can update from [runink]" was an assumption nobody had tested.
// It is still an assumption today, because nothing ever installs from the assembled directory.
//
// So this runs pacman against the real repository in a throwaway container: refresh the
// database, install every package it offers, and compare the installed versions with the
// filenames. No host is modified and nothing needs root on the host -- which is what makes it
// runnable by a CORE agent with no human present.
//
// It emits a release-evidence document (internal/evidence) so the result is consumable rather
// than prose in a terminal.

// checks this harness promises to report. They ship in the document as declared_checks and the
// consumer recomputes the verdict against them, so a run that dies halfway cannot read as a pass.
var installTestChecks = []string{
	"repo-readable",
	"db-resolves",
	"install-all",
	"versions-match",
	"signature-chain",
}

// InstallTestOptions configures one run.
type InstallTestOptions struct {
	Dir       string // the assembled repository directory
	Image     string // base container image (an Artix userland with pacman)
	Commit    string // the commit the packages were built from; required for the evidence
	Version   string // e.g. runink-os-2026.10
	HostKind  string // server-node | dev-box | ci
	Runner    string
	Evidence  string // where to write the evidence document; empty writes none
	RunID     string // the orchestrator's run id, written through unchanged; empty generates one
	RiverRepo string // the river checkout, for pacman/mirrorlist.pin
}

// artixMirrors reads ARTIX_MIRRORS from the repository's pacman/mirrorlist.pin. Empty when the
// checkout is not there: the run then uses the image's own mirrors and is no worse off than
// before, rather than failing on a file that is only a hardening.
func artixMirrors(repo string) string {
	b, err := os.ReadFile(filepath.Join(repo, "pacman", "mirrorlist.pin"))
	if err != nil {
		return ""
	}
	_, rest, ok := strings.Cut(string(b), "ARTIX_MIRRORS='")
	if !ok {
		return ""
	}
	v, _, ok := strings.Cut(rest, "'")
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// pkgFile is one package in the repository.
type pkgFile struct {
	file, name, version string
}

// parsePkgFile splits name-version-rel-arch.pkg.tar.zst. A package name may itself contain
// dashes (linux-runink-headers), so the split is from the RIGHT: arch, then rel, then version,
// and whatever remains is the name. Splitting from the left gets every one of our packages wrong.
func parsePkgFile(base string) (pkgFile, bool) {
	s := strings.TrimSuffix(base, ".pkg.tar.zst")
	if s == base {
		return pkgFile{}, false
	}
	i := strings.LastIndex(s, "-") // -arch
	if i < 0 {
		return pkgFile{}, false
	}
	s = s[:i]
	j := strings.LastIndex(s, "-") // -rel
	if j < 0 {
		return pkgFile{}, false
	}
	k := strings.LastIndex(s[:j], "-") // -version
	if k < 0 {
		return pkgFile{}, false
	}
	return pkgFile{file: base, name: s[:k], version: s[k+1:]}, true
}

// readRepo lists the packages in dir, and whether any detached signature is present.
func readRepo(dir string) (pkgs []pkgFile, signed bool, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, false, err
	}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".sig") {
			signed = true
			continue
		}
		if p, ok := parsePkgFile(n); ok {
			pkgs = append(pkgs, p)
		}
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].name < pkgs[j].name })
	return pkgs, signed, nil
}

// the script run inside the container. It takes no interpolated values: the repository is a
// mount, and the package list arrives on stdin, so nothing from the filesystem is spliced into
// shell text.
const installScript = `set -eu
# The base image ships an ORDERED mirrorlist whose first entry has repeatedly degraded, and
# pacman does NOT fail over on a stalled or 404 transfer -- it aborts the whole transaction.
# The first run of this harness failed exactly there: inetutils could not be retrieved, from one
# mirror that was unreachable and a second that 404d, and it reported a FAILURE THAT WAS ITS OWN
# ENVIRONMENT rather than a defect in [runink]. A test whose result depends on someone elses
# mirror weather says nothing about the repository, so the mirrors are pinned to the
# repositorys own allowlist (pacman/mirrorlist.pin) -- the same two that build-iso-box.sh and
# the kernel builder pin. RIVER_MIRRORS arrives as an environment variable, so no value is
# spliced into shell text.
[ -n "${RIVER_MIRRORS:-}" ] && printf '%s\n' "$RIVER_MIRRORS" >/etc/pacman.d/mirrorlist
cat >/etc/pacman.d/runink-test <<'EOF'
[runink]
SigLevel = Optional TrustAll
Server = file:///repo
EOF
sed -i '0,/^\[/{s|^\[|Include = /etc/pacman.d/runink-test\n\n[|}' /etc/pacman.conf
echo "== pacman -Sy"
pacman -Sy --noconfirm
echo "== pacman -Sl runink"
pacman -Sl runink
echo "== install"
xargs -r pacman -S --noconfirm --needed --assume-installed initramfs
echo "== installed versions"
pacman -Q
`

// InstallTest runs the harness and returns an error when the run's verdict is not a pass.
func InstallTest(ctx context.Context, o InstallTestOptions, harnessVersion string, out io.Writer) error {
	if o.Dir == "" {
		return fmt.Errorf("--dir is required")
	}
	if o.Commit == "" {
		return fmt.Errorf("--commit is required: evidence that cannot be tied to a commit cannot be re-derived")
	}
	if o.Image == "" {
		o.Image = "runink-os-builder"
	}
	if o.HostKind == "" {
		o.HostKind = "dev-box"
	}
	abs, err := filepath.Abs(o.Dir)
	if err != nil {
		return err
	}

	mirrors := artixMirrors(o.RiverRepo)

	run := evidence.NewWithRunID(o.RunID, "river-packages", harnessVersion,
		evidence.Subject{
			Kind:         "pacman-repo",
			Name:         "runink",
			Version:      o.Version,
			Commit:       o.Commit,
			ArtifactPath: filepath.Base(abs),
		},
		evidence.Environment{
			HostKind:       o.HostKind,
			Runner:         o.Runner,
			ContainerImage: o.Image,
		},
		installTestChecks, out)

	fmt.Fprintf(out, "%s: install-test run %s\n", tool, run.RunID())

	pkgs, signed, err := readRepo(abs)
	switch {
	case err != nil:
		run.Fail("repo-readable", fmt.Sprintf("cannot read the repository directory: %v", redact(err.Error(), abs)))
	case len(pkgs) == 0:
		run.Fail("repo-readable", "no *.pkg.tar.zst in the repository directory")
	default:
		run.Pass("repo-readable", fmt.Sprintf("%d package(s)", len(pkgs)))
	}

	// signature-chain is REPORTED, never performed here. An autonomous run cannot sign, and the
	// owner signs offline, so an unsigned assembled repository is the expected state and must be
	// stated rather than passed over in silence.
	if signed {
		run.Skip("signature-chain", "signatures present: `river repo verify` checks them against the release key; this harness does not")
	} else {
		run.Skip("signature-chain", "unsigned: the release key is the owner's and offline, so an autonomous run cannot verify a chain that does not exist yet")
	}

	if len(pkgs) == 0 {
		return finish(run, o.Evidence, out)
	}

	names := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		names = append(names, p.name)
	}

	var buf strings.Builder
	err = pipe.Run(ctx,
		pipe.IO{Stdin: strings.NewReader(strings.Join(names, "\n") + "\n"), Stdout: &buf, Stderr: &buf},
		pipe.Cmd("podman", "run", "--rm", "-i", "--user", "root",
			"--network", "bridge",
			"-e", "RIVER_MIRRORS="+mirrors,
			"-v", abs+":/repo:ro",
			o.Image, "sh", "-c", installScript))
	log := buf.String()

	// db-resolves: pacman saw the repository and listed what it contains.
	listed := countListed(log, "runink")
	switch {
	case strings.Contains(log, "error: failed to synchronize"), strings.Contains(log, "could not open file"):
		run.Fail("db-resolves", "pacman could not synchronise the repository database: "+lastError(log))
	case listed == 0:
		run.Fail("db-resolves", "pacman listed no packages in [runink] after -Sy")
	case listed != len(pkgs):
		run.Fail("db-resolves", fmt.Sprintf("the database lists %d package(s) but the directory holds %d: the db and the files disagree", listed, len(pkgs)))
	default:
		run.Pass("db-resolves", fmt.Sprintf("pacman -Sy saw %d package(s)", listed))
	}

	// install-all: every package actually installed.
	if err != nil {
		run.Fail("install-all", "pacman could not install every package: "+lastError(log))
	} else {
		run.Pass("install-all", fmt.Sprintf("%d package(s) installed, no conflicts", len(pkgs)))
	}

	// versions-match: what pacman reports installed equals the filenames. This is the check that
	// catches a database pointing at a different build than the files on disk.
	installed := parseQuery(log)
	var wrong []string
	for _, p := range pkgs {
		got, ok := installed[p.name]
		if !ok {
			wrong = append(wrong, p.name+": not installed")
			continue
		}
		if got != p.version {
			wrong = append(wrong, fmt.Sprintf("%s: installed %s, file says %s", p.name, got, p.version))
		}
	}
	sort.Strings(wrong)
	if len(wrong) == 0 {
		run.Pass("versions-match", fmt.Sprintf("all %d installed version(s) match their package file", len(pkgs)))
	} else {
		run.Fail("versions-match", strings.Join(wrong, "; "))
	}

	return finish(run, o.Evidence, out)
}

func finish(run *evidence.Run, path string, out io.Writer) error {
	d := run.Finish()
	fmt.Fprintf(out, "%s: %s (%d pass, %d fail, %d skip)\n", tool, strings.ToUpper(d.Verdict), d.Totals.Pass, d.Totals.Fail, d.Totals.Skip)
	if path != "" {
		if err := evidence.WriteFile(path, d); err != nil {
			return fmt.Errorf("writing evidence: %w", err)
		}
		fmt.Fprintf(out, "%s: evidence %s\n", tool, filepath.Base(path))
	}
	if d.Verdict != evidence.Pass {
		return fmt.Errorf("install test failed: %d check(s) failed", d.Totals.Fail)
	}
	return nil
}

// countListed counts `pacman -Sl <repo>` lines, which are "<repo> <name> <version>".
func countListed(log, repo string) int {
	n := 0
	for _, l := range strings.Split(log, "\n") {
		if f := strings.Fields(l); len(f) >= 3 && f[0] == repo {
			n++
		}
	}
	return n
}

// parseQuery reads `pacman -Q` output ("<name> <version>") into a map.
func parseQuery(log string) map[string]string {
	m := map[string]string{}
	in := false
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(l, "== installed versions") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if f := strings.Fields(l); len(f) == 2 {
			m[f[0]] = f[1]
		}
	}
	return m
}

// lastError returns the last error line, trimmed, so a failure detail says what went wrong
// without pasting a whole pacman log into a published document.
func lastError(log string) string {
	var last string
	for _, l := range strings.Split(log, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "error:") {
			last = strings.TrimSpace(l)
		}
	}
	if last == "" {
		// Not an error pacman named: say so rather than inventing a cause.
		return "no error line in the output; see the harness log"
	}
	if len(last) > 200 {
		last = last[:200] + "…"
	}
	return last
}

// redact keeps a host path out of a detail that will be published. The evidence linter refuses
// such a document anyway; this makes the message useful instead of merely refused.
func redact(s, dir string) string {
	return strings.ReplaceAll(s, dir, "<repo>")
}

// installTestCmd registers `river repo install-test`.
func installTestCmd(v *viper.Viper) *cobra.Command {
	c := &cobra.Command{
		Use:   "install-test",
		Short: "Install every package from the assembled repository in a container, and emit evidence",
		Long: "Prove a node can actually install from [runink].\n\n" +
			"`river repo verify` checks the database's shape and every signature. Both can be\n" +
			"perfect while the repository is unusable: an unsatisfiable dependency, a database\n" +
			"that lists a file it does not contain, a package built against an upstream version\n" +
			"that is gone. Nothing has ever installed FROM the assembled directory, so \"an\n" +
			"installed node can update from [runink]\" has been an assumption.\n\n" +
			"This runs pacman against the real repository in a throwaway container and writes a\n" +
			"release-evidence document. No host is modified and nothing needs root on the host,\n" +
			"which is what makes it runnable with no human present.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return InstallTest(cmd.Context(), InstallTestOptions{
				Dir:       v.GetString("dir"),
				Image:     v.GetString("image"),
				Commit:    v.GetString("commit"),
				Version:   v.GetString("version"),
				HostKind:  v.GetString("host-kind"),
				Runner:    v.GetString("runner"),
				Evidence:  v.GetString("evidence"),
				RunID:     v.GetString("run-id"),
				RiverRepo: v.GetString("river-repo"),
			}, v.GetString("commit"), cmd.OutOrStdout())
		},
	}
	c.Flags().String("dir", "", "the assembled repository directory (required)")
	c.Flags().String("image", "runink-os-builder", "base container image with pacman")
	c.Flags().String("commit", "", "the commit the packages were built from (required)")
	c.Flags().String("version", "", "the release version, e.g. runink-os-2026.10")
	c.Flags().String("host-kind", "dev-box", "where this ran: server-node | dev-box | ci")
	c.Flags().String("runner", "", "the runner's name, if any")
	c.Flags().String("evidence", "", "write the release-evidence document here")
	c.Flags().String("run-id", "", "the orchestrator's run id (a ULID), written through unchanged")
	c.Flags().String("river-repo", ".", "the river checkout, for pacman/mirrorlist.pin")
	return c
}
