// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ci-runners — every job of every workflow runs on the project's own `public-ci` runners and
// only for the owner (docs/governance/CI.md, "Runners"):
//
//  1. its `runs-on:` is exactly `[self-hosted, public-ci]`. A GitHub-hosted label
//     (ubuntu-*, windows-*, macos-*), an expression (`${{ matrix.os }}`) or any other
//     self-hosted label fails;
//  2. its job-level `if:` is the owner-only condition, alone or as the last `&& (...)` term,
//     so a pull request whose head is a fork never starts a job.
//
// A job that calls a reusable workflow (job-level `uses:`) has no `runs-on` of its own; it
// still needs the `if:`.
//
// What this does NOT prove: that a runner with that label exists, or that a fork cannot
// change the workflow in its own pull request. The `if:` is read from the pull request's
// merge commit; what keeps fork code off the runners is that they are registered to this
// repository only and fork runs need a maintainer's approval (CI.md).

// ownerOnlyIf is the job condition: a push or dispatch, or a pull request from this repository.
const ownerOnlyIf = "github.event_name != 'pull_request' || github.event.pull_request.head.repo.full_name == github.repository"

// publicRunsOn is the one runs-on value a job may carry.
const publicRunsOn = "[self-hosted, public-ci]"

var (
	ciJobsRE    = regexp.MustCompile(`^jobs:\s*(#.*)?$`)
	ciJobRE     = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*(#.*)?$`)
	ciTopRE     = regexp.MustCompile(`^[A-Za-z]`)
	ciRunsOnRE  = regexp.MustCompile(`^    runs-on:\s*(.*?)\s*(#.*)?$`)
	ciIfRE      = regexp.MustCompile(`^    if:\s*(.*?)\s*$`)
	ciUsesRE    = regexp.MustCompile(`^    uses:\s`)
	ciRunsOnSep = regexp.MustCompile(`\s*,\s*`)
)

func init() { extraLints = append(extraLints, ciRunnersCmd) }

func ciRunnersCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "ci-runners",
		Short: "every workflow job runs on [self-hosted, public-ci], never GitHub-hosted, and only for the owner",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return CIRunners(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

type ciJob struct {
	name, runsOn, cond string
	line               int
	hasRunsOn, reusing bool
}

// ciJobs reads the jobs of one workflow, line by line (two-space job ids, four-space keys).
func ciJobs(body string) []ciJob {
	var jobs []ciJob
	in := false
	for i, l := range strings.Split(body, "\n") {
		switch {
		case ciJobsRE.MatchString(l):
			in = true
			continue
		case ciTopRE.MatchString(l):
			in = false
			continue
		case !in:
			continue
		}
		if m := ciJobRE.FindStringSubmatch(l); m != nil {
			jobs = append(jobs, ciJob{name: m[1], line: i + 1})
			continue
		}
		if len(jobs) == 0 {
			continue
		}
		j := &jobs[len(jobs)-1]
		if m := ciRunsOnRE.FindStringSubmatch(l); m != nil {
			j.hasRunsOn, j.runsOn = true, m[1]
		} else if m := ciIfRE.FindStringSubmatch(l); m != nil {
			j.cond = m[1]
		} else if ciUsesRE.MatchString(l) {
			j.reusing = true
		}
	}
	return jobs
}

// ciOwnerOnly reports whether cond is the owner-only condition, alone or ANDed last.
func ciOwnerOnly(cond string) bool {
	return cond == ownerOnlyIf || strings.HasSuffix(cond, ") && ("+ownerOnlyIf+")") && strings.HasPrefix(cond, "(")
}

// ciRunsOnOK reports whether a runs-on value is exactly the public-ci pool.
func ciRunsOnOK(v string) bool {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return false
	}
	return strings.Join(ciRunsOnSep.Split(strings.TrimSpace(v[1:len(v)-1]), -1), ", ") == publicRunsOn[1:len(publicRunsOn)-1]
}

// CIRunners runs the lint over the tracked workflows of repo.
func CIRunners(ctx context.Context, repo string, errw, outw io.Writer) error {
	r := &report{name: "lint-ci-runners", w: errw}
	files, err := gitLines(ctx, repo, "ls-files", "--", ".github/workflows/*.yml", ".github/workflows/*.yaml")
	if err != nil {
		return fmt.Errorf("lint-ci-runners: git ls-files: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("lint-ci-runners: no tracked workflow under .github/workflows; not looking at this repository")
	}
	jobs := 0
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(repo, f))
		if err != nil {
			return fmt.Errorf("lint-ci-runners: %w", err)
		}
		js := ciJobs(string(b))
		if len(js) == 0 {
			r.fail("%s: no job found under `jobs:`; the lint cannot read this workflow", f)
		}
		for _, j := range js {
			jobs++
			switch {
			case j.reusing && !j.hasRunsOn:
			case !j.hasRunsOn:
				r.fail("%s:%d: job %s has no runs-on; use `runs-on: %s`", f, j.line, j.name, publicRunsOn)
			case !ciRunsOnOK(j.runsOn):
				r.fail("%s:%d: job %s runs on %q; only `runs-on: %s` is allowed (no GitHub-hosted runner, no other pool)", f, j.line, j.name, j.runsOn, publicRunsOn)
			}
			if !ciOwnerOnly(j.cond) {
				r.fail("%s:%d: job %s lacks the owner-only condition; use `if: %s`, or `if: (<yours>) && (%s)`", f, j.line, j.name, ownerOnlyIf, ownerOnlyIf)
			}
		}
	}
	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-ci-runners: OK (%d workflows, %d jobs)\n", len(files), jobs)
	return nil
}
