// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/org-runink/river/cli/internal/evidence"
	"github.com/org-runink/river/pkg/pipe"
	"github.com/spf13/viper"
)

// The release-evidence side of `river test` (--evidence FILE): the same run, also written as
// a runink.release-evidence/1 document (cli/internal/evidence, the one producer) for an
// autonomous harness to collect. Without --evidence nothing here runs and the command behaves
// exactly as before.
//
// Each subcommand is its own harness, named river-tier1/<subcommand> (sddm-theme, a developer
// check outside Tier 1, is river-dev/sddm-theme): one harness, one fixed declared list, so a
// consumer comparing runs of a harness compares like with like. The declared lists below are
// STATIC: never derived by running the subcommand, or a check that vanished would vanish from
// the list too and the run would still pass. A check that does not apply on a host is
// reported as a skip with its reason, never left out.

// tier1Harness is the evidence harness name of a Tier 1 subcommand (scripts/ci-tier1.sh).
func tier1Harness(sub string) string { return "river-tier1/" + sub }

// evidenceOpts are the --evidence flags.
type evidenceOpts struct {
	path     string // --evidence: where the document goes; "" = no evidence
	hostKind string // --evidence-host-kind
}

func evidenceOptsFrom(v *viper.Viper) evidenceOpts {
	return evidenceOpts{path: v.GetString("evidence"), hostKind: v.GetString("evidence-host-kind")}
}

// evidenceRun is what a subcommand's checks find in ctx: the run, and the redaction its
// details go through.
type evidenceRun struct {
	run    *evidence.Run
	redact func(string) string
}

type evidenceKey struct{}

func evidenceFrom(ctx context.Context) *evidenceRun {
	e, _ := ctx.Value(evidenceKey{}).(*evidenceRun)
	return e
}

// withEvidence runs fn and, when opts.path is set, records its checks and writes the document.
// A checkout with uncommitted changes, or a binary built from a modified tree, is refused BEFORE
// the test runs, with nothing run and nothing written. Evidence from a dirty tree
// cannot be re-derived from any commit, so it is not evidence.
//
// After the run, the test's own error is always returned as it was; a document that cannot be
// written (it would leak a host path) is an error too, so a run whose evidence was refused
// never exits 0.
func withEvidence(ctx context.Context, opts evidenceOpts, harness string, declared []string, repo string, fn func(context.Context) error) error {
	if opts.path == "" {
		return fn(ctx)
	}
	commit, version, env, err := evidenceOrigin(ctx, repo, opts.hostKind)
	if err != nil {
		return fmt.Errorf("evidence: refusing to run %s with --evidence: %w", harness, err)
	}
	run := evidence.New(harness, version, evidence.Subject{
		Kind:   "source",
		Name:   "river",
		Commit: commit,
	}, env, declared, nil) // nil: the human output stays the checks' own
	e := &evidenceRun{run: run, redact: redactor(repo)}
	terr := fn(context.WithValue(ctx, evidenceKey{}, e))
	doc := run.Finish()
	if terr != nil && doc.Verdict == evidence.Pass {
		// Every declared check passed, yet the test failed: a document saying "pass" would be
		// the exit-0-hides-exit-1 failure the contract exists for. Write nothing.
		return errors.Join(terr, fmt.Errorf("evidence: not written: %s failed after every declared check passed (a harness bug)", harness))
	}
	if werr := evidence.WriteFile(opts.path, doc); werr != nil {
		return errors.Join(terr, fmt.Errorf("evidence: %w", werr))
	}
	return terr
}

// evidenceOrigin is the subject commit, the harness version and the environment, or why the
// run cannot produce trustworthy evidence. It refuses only what the producer cannot see: a
// dirty checkout and a binary built from a modified tree. The shapes (40-hex commits, the
// architecture, which evidence.New maps from GOARCH itself, the host kind) are evidence.Lint's,
// checked once, when the document is written.
func evidenceOrigin(ctx context.Context, repo, hostKind string) (commit, version string, env evidence.Environment, err error) {
	commit = gitHead(ctx, repo)
	if commit != "" { // no commit at all: the document is refused when written, by evidence.Lint
		dirty, err := checkoutDirty(ctx, repo)
		if err != nil {
			return "", "", env, fmt.Errorf("cannot tell whether the checkout is clean: %w", err)
		}
		if dirty {
			return "", "", env, errors.New("the checkout under test has uncommitted changes: evidence from a dirty tree cannot be tied to a commit; commit or clean it first")
		}
	}
	var settings []debug.BuildSetting
	if bi, ok := debug.ReadBuildInfo(); ok {
		settings = bi.Settings
	}
	if version, err = harnessVersion(settings, commit); err != nil {
		return "", "", env, err
	}
	return commit, version, environment(hostKind), nil
}

// gitHead is the commit of the checkout under test, "" when it is not a git checkout.
func gitHead(ctx context.Context, repo string) string {
	out, err := capture(ctx, pipe.Cmd("git", "-C", repo, "rev-parse", "HEAD"), nil)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// checkoutDirty reports whether the checkout has uncommitted changes, untracked files
// included (an untracked file can change what a test sees). A variable so the package's own
// tests, which run in a checkout being edited, can say "clean".
var checkoutDirty = func(ctx context.Context, repo string) (bool, error) {
	out, err := capture(ctx, pipe.Cmd("git", "-C", repo, "status", "--porcelain"), nil)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// harnessVersion is the river commit this binary was built from, from its build settings. A
// binary built from a modified tree is refused: no commit names it. Tier 1 builds with
// -buildvcs=false from the same checkout it tests, so without a stamped revision the
// checkout's (clean) commit is the harness's.
func harnessVersion(settings []debug.BuildSetting, subjectCommit string) (string, error) {
	rev := ""
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				return "", errors.New("this river binary was built from a tree with uncommitted changes: its harness_version names no commit; rebuild it from a clean checkout")
			}
		}
	}
	if rev == "" {
		rev = subjectCommit
	}
	return rev, nil
}

// environment describes the host by category only: the document is public. Arch is left to
// evidence.New, which maps GOARCH onto the contract's names.
func environment(hostKind string) evidence.Environment {
	if hostKind == "" {
		hostKind = "dev-box"
		if os.Getenv("CI") != "" {
			hostKind = "ci"
		}
	}
	env := evidence.Environment{HostKind: hostKind, KVM: exists("/dev/kvm")}
	if os.Getenv("RIVER_TIER1_IN_CONTAINER") == "1" {
		env.ContainerImage = "river-tier1"
	}
	return env
}

// redactor rewrites what would place a detail on this machine: the checkout's path, scratch
// directories under the temporary directory, the home directory. Details are the checks' own
// descriptions and rarely hold a path; this is so the one that does is rewritten rather than
// refused (evidence.WriteFile refuses a document with a host path in it).
func redactor(repo string) func(string) string {
	type rule struct {
		re   *regexp.Regexp
		with string
	}
	var rules []rule
	add := func(path, suffix, with string) {
		if path == "" || path == "/" || path == "." {
			return
		}
		rules = append(rules, rule{regexp.MustCompile(regexp.QuoteMeta(filepath.Clean(path)) + suffix), with})
	}
	// Longest first, so the checkout inside a scratch directory reads as the checkout.
	var paths []string
	if abs, err := filepath.Abs(repo); err == nil {
		paths = append(paths, abs)
	}
	if filepath.IsAbs(repo) {
		paths = append(paths, repo)
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, p := range paths {
		add(p, "", "<repo>")
	}
	tmp := os.TempDir()
	add(tmp, `/[^/\s'"]+`, "<scratch>")
	add(tmp, "", "<tmp>")
	if home, err := os.UserHomeDir(); err == nil {
		add(home, "", "~")
	}
	return func(s string) string {
		for _, r := range rules {
			s = r.re.ReplaceAllString(s, r.with)
		}
		return s
	}
}
