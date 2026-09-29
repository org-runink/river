// Package lint holds the repository lints that were scripts/lint-*.sh, as `river lint <name>`.
// Each lint reads the tracked tree through git and fails with a message per violation, the
// way the scripts did, so CI output stays readable.
package lint

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/org-runink/river/cli/internal/rootcmd"
	"github.com/org-runink/river/pkg/pipe"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootcmd.Register(func(v *viper.Viper) *cobra.Command {
		c := &cobra.Command{
			Use:   "lint",
			Short: "Repository lints (were scripts/lint-*.sh)",
		}
		c.PersistentFlags().String("repo", ".", "the river checkout to lint")
		c.AddCommand(oneEngineCmd(v))
		for _, l := range extraLints {
			c.AddCommand(l(v))
		}
		return c
	})
}

// extraLints are lint commands other files of this package add from their init.
var extraLints []func(*viper.Viper) *cobra.Command

// report collects violations; a lint fails when it holds any.
type report struct {
	name string
	w    io.Writer
	n    int
}

func (r *report) fail(format string, args ...any) {
	r.n++
	fmt.Fprintf(r.w, "%s: %s\n", r.name, fmt.Sprintf(format, args...))
}

func (r *report) err() error {
	if r.n == 0 {
		return nil
	}
	return fmt.Errorf("%s: %d problem(s)", r.name, r.n)
}

// gitLines runs git in repo and returns its stdout lines. A git that exits 1 with no output
// (git grep finding nothing) is an empty answer, not an error.
func gitLines(ctx context.Context, repo string, args ...string) ([]string, error) {
	out, err := pipe.Output(ctx, nil, pipe.Command{Name: "git", Args: append([]string{"-C", repo}, args...)})
	if err != nil {
		if len(bytes.TrimSpace(out)) == 0 && isExit1(err) {
			return nil, nil
		}
		return nil, err
	}
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return nil, nil
	}
	return strings.Split(s, "\n"), nil
}

func isExit1(err error) bool {
	type exitCoder interface{ ExitCode() int }
	for e := err; e != nil; {
		if ec, ok := e.(exitCoder); ok {
			return ec.ExitCode() == 1
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
