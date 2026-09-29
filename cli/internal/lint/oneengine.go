// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// one-engine — mistral.rs is the only inference engine (decided 2026-09-27): no second engine
// creeps back into the tree as a build pin, a package, a run config or a claim.
//
// It fails when a tracked text file names llama.cpp (llama.cpp, llama-cpp, LLAMA_CPP,
// llama-server, llama-cli, llama-bench, runink-llama-*) outside oneEngineAllow. The allowed
// files may name it only for the reasons listed there, and each reason is re-checked: an
// allow-list entry whose reason is gone fails.
//
// What this does NOT prove: that the image carries no such binary (tests/assert-golden.sh,
// on a booted target), or that an untracked file is clean (build/.out/ is not ours).

const oneEnginePattern = `llama[._-]?cpp|llama-(server|cli|bench)|runink-llama`

// oneEngineAllow maps each allowed file to why it may name llama.cpp.
var oneEngineAllow = map[string]string{
	"tests/assert-golden.sh":              "the guard that fails an image carrying llama.cpp binaries",
	"tests/README.md":                     "describes that guard",
	"CHANGELOG.md":                        "history: the removal itself",
	"validation/results.json":             "historical runs, each marked not_evidence",
	"validation/SELECTION.md":             "historical results, each labelled NOT EVIDENCE",
	"models.evidence.json":                "historical evidence marked not_evidence, and upstream facts",
	"cli/internal/lint/oneengine.go":      "this lint",
	"cli/internal/lint/oneengine_test.go": "this lint's tests",
}

// oneEngineReasons are the facts the allow-list depends on: file, pattern that must match,
// and the message when it no longer does.
var oneEngineReasons = []struct{ file, re, msg string }{
	{"tests/assert-golden.sh", `/usr/local/bin/llama-server(\s|$)`, "tests/assert-golden.sh no longer checks for llama.cpp binaries (the guard for this decision)"},
	{"validation/results.json", `"not_evidence"`, "validation/results.json names llama.cpp runs but carries no not_evidence mark"},
	{"validation/SELECTION.md", `NOT EVIDENCE`, "validation/SELECTION.md names llama.cpp but labels nothing NOT EVIDENCE"},
	{"models.evidence.json", `"not_evidence"`, "models.evidence.json names llama.cpp but carries no not_evidence mark"},
}

func oneEngineCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "one-engine",
		Short: "mistral.rs is the only inference engine: llama.cpp named only where allowed",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return OneEngine(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

// OneEngine runs the lint over repo, writing problems to errw and the OK line to outw.
func OneEngine(ctx context.Context, repo string, errw, outw interface{ Write([]byte) (int, error) }) error {
	r := &report{name: "lint-one-engine", w: errw}

	files, err := gitLines(ctx, repo, "ls-files")
	if err != nil {
		return fmt.Errorf("lint-one-engine: git ls-files: %w", err)
	}
	if len(files) < 100 {
		return fmt.Errorf("lint-one-engine: only %d tracked files; not looking at this repository", len(files))
	}

	// 1. No mention outside the allow-list. -I skips binaries (artwork, fonts).
	hits, err := gitLines(ctx, repo, "grep", "-I", "-l", "-i", "-E", oneEnginePattern, "--", ".")
	if err != nil {
		return fmt.Errorf("lint-one-engine: git grep: %w", err)
	}
	for _, f := range hits {
		if _, ok := oneEngineAllow[f]; ok {
			continue
		}
		if st, err := os.Stat(filepath.Join(repo, f)); err != nil || !st.Mode().IsRegular() {
			continue
		}
		lines, _ := gitLines(ctx, repo, "grep", "-I", "-n", "-i", "-E", oneEnginePattern, "--", f)
		r.fail("%s names llama.cpp; mistral.rs is the only inference engine:\n    %s", f, strings.Join(lines, "\n    "))
	}

	// 2. The allow-list reasons still hold.
	for _, reason := range oneEngineReasons {
		b, err := os.ReadFile(filepath.Join(repo, reason.file))
		if err != nil || !regexp.MustCompile(`(?m)`+reason.re).Match(b) {
			r.fail("%s", reason.msg)
		}
	}

	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-one-engine: OK (%d tracked files; mistral.rs is the only engine)\n", len(files))
	return nil
}

// oneEngineAllowed lists the allow-list, sorted, for tests and --help output.
func oneEngineAllowed() []string {
	ks := make([]string, 0, len(oneEngineAllow))
	for k := range oneEngineAllow {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}
