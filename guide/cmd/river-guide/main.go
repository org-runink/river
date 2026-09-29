// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// river-guide — the Runink River install guide agent.
//
//	river-guide [flags]                 interactive guide on this terminal (tty1 on the medium)
//	river-guide ask "question"          one grounded answer, then exit
//	river-guide steps                   list the built-in install steps
//	river-guide eval --cases FILE       score the guide model against fixed questions
//	river-guide verify-bundle --pubkey ed25519:… BUNDLE [SIG]
//
// It answers from guide bundles (the built-in public Runink River guide, plus any verified
// private platform bundle), walks the install steps, and optionally mirrors the install
// to one GitHub issue ("tags": @river_install comments). It runs fully offline; the
// network is touched only by `login`/`remote` and only when the operator asks.
//
// Settings come from flags, then the kernel command line (river.guide.*), so the boot
// menu can switch modes without a config file on the medium:
//
//	river.guide=0                   do not start the guide on tty1 (read by the tty1 wrapper)
//	river.guide.remote=1            offer the remote channel at start-up
//	river.guide.issue=OWNER/REPO/N  the install-tracking issue (or OWNER/REPO to create one)
//	river.guide.client_id=ID        GitHub App client ID for the device flow (not a secret)
//	river.guide.proxy=URL           HTTPS proxy for GitHub on IPv6-only networks
//	river.guide.model=0             degraded mode even when the model is available
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/org-runink/river/guide/agent"
	"github.com/org-runink/river/guide/audit"
	"github.com/org-runink/river/guide/bundle"
	"github.com/org-runink/river/guide/bundles"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "verify-bundle":
			return cmdVerify(args[1:], stdout, stderr)
		case "steps":
			return cmdSteps(stdout, stderr)
		case "eval":
			return cmdEval(args[1:], stdout, stderr)
		case "version", "--version":
			fmt.Fprintln(stdout, "river-guide", version)
			return 0
		}
	}
	cfg, rest, err := parseFlags(args, stderr)
	if err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := newApp(ctx, cfg, stdin, stdout)
	if err != nil {
		fmt.Fprintln(stderr, "river-guide:", err)
		return 1
	}
	defer a.close()
	if len(rest) > 0 && rest[0] == "ask" {
		q := strings.Join(rest[1:], " ")
		a.waitModel(ctx, cfg.modelWait)
		a.answer(ctx, q)
		return 0
	}
	if len(rest) > 0 {
		fmt.Fprintf(stderr, "river-guide: unknown command %q\n", rest[0])
		return 2
	}
	return a.repl(ctx)
}

func cmdSteps(stdout, stderr io.Writer) int {
	b, err := bundles.River()
	if err != nil {
		fmt.Fprintln(stderr, "river-guide:", err)
		return 1
	}
	for i, s := range b.Steps {
		fmt.Fprintf(stdout, "%2d. %-13s %-11s %s\n", i+1, s.ID, s.Kind, s.Title)
	}
	return 0
}

func cmdVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify-bundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	pub := fs.String("pubkey", "", "ed25519:<base64> verification key")
	sum := fs.String("sha256", "", "optional pinned sha256 of the bundle file")
	if err := fs.Parse(args); err != nil || fs.NArg() < 1 {
		fmt.Fprintln(stderr, "usage: river-guide verify-bundle --pubkey ed25519:… BUNDLE [SIG]")
		return 2
	}
	k, err := bundle.ParsePublicKey(*pub)
	if err != nil {
		fmt.Fprintln(stderr, "river-guide:", err)
		return 2
	}
	sigPath := fs.Arg(0) + ".sig"
	if fs.NArg() > 1 {
		sigPath = fs.Arg(1)
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "river-guide:", err)
		return 1
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		fmt.Fprintln(stderr, "river-guide:", err)
		return 1
	}
	b, err := bundle.VerifyAndDecode(k, raw, sig, *sum)
	if err != nil {
		fmt.Fprintln(stderr, "river-guide: REFUSED:", err)
		return 1
	}
	fmt.Fprintf(stdout, "OK %s %s: %d sections, %d steps, sha256 %s\n", b.Name, b.Version, len(b.Sections), len(b.Steps), bundle.SHA256(raw))
	return 0
}

// auditOrStderr opens the audit log, falling back to stderr so events are never lost
// silently.
func auditOrStderr(path string, stderr io.Writer) *audit.Log {
	if path == "" || path == "-" {
		return audit.To(stderr)
	}
	l, err := audit.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "river-guide: audit log %s: %v (logging to stderr)\n", path, err)
		return audit.To(stderr)
	}
	return l
}

type lineReader struct {
	sc *bufio.Scanner
}

func (l *lineReader) read() (string, error) {
	if !l.sc.Scan() {
		if err := l.sc.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	return strings.TrimSpace(l.sc.Text()), nil
}

func stepLine(s agent.Step) string {
	tag := ""
	if s.Private {
		tag = " [platform]"
	}
	return fmt.Sprintf("%-10s %-13s %-11s %s%s", s.State, s.ID, s.Kind, s.Title, tag)
}

func isEOF(err error) bool { return errors.Is(err, io.EOF) }

func since(t time.Time) string { return time.Since(t).Round(time.Second).String() }
