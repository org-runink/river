// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Command river is the one command line for Runink River's internals: the image build, the
// installer steps, first boot, the s6 services and the repository checks. See docs/GO-CLI.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/org-runink/river/cli/internal/rootcmd"

	// Command groups register themselves with the root.
	_ "github.com/org-runink/river/cli/internal/lint"
	_ "github.com/org-runink/river/cli/internal/repocmd"
	_ "github.com/org-runink/river/cli/internal/testcmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rootcmd.New().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "river:", err)
		os.Exit(1)
	}
}
