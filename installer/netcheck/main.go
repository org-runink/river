// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// river-netcheck — the Go half of river-netsetup: the connectivity check and the parsers.
//
//	river-netcheck check [--method M] [--host H] [--port 443] [--timeout 3s] [--proxy URL]
//	                     [--hostname NAME] [--state FILE] [--env FILE] [--json] [--no-write]
//	    report links, addresses, default routes, DNS, and whether the default gateways,
//	    DNS resolution and the internet (a TCP connect to H:443, IPv4 and IPv6) answer;
//	    record the outcome (online | lan-only | offline) in FILE (default
//	    /run/river/net-state.json, schema river.net-state/v1) and ENV (default net.env
//	    beside it). Every outcome is valid: the exit status is 0 whichever it is.
//	river-netcheck cmdline [--file /proc/cmdline]
//	    parse river.net= / river.net.check= and print NET_* sh assignments (NET_METHOD is
//	    empty when river.net= is absent)
//	river-netcheck spec SPEC       parse one river.net value, print NET_* assignments
//	river-netcheck config FILE     parse a keyfile-style config, print NET_* assignments
//
// Exit codes: 0 ok; 1 error (a parse error names the problem); 2 usage.
// Standard library only; nothing here needs root, and check writes only its state files.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/org-runink/river/installer/internal/netcfg"
	"github.com/org-runink/river/installer/internal/netstate"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: river-netcheck check|cmdline|spec|config ... (see --help of each)")
		return 2
	}
	switch args[0] {
	case "check":
		return cmdCheck(args[1:], stdout, stderr)
	case "cmdline":
		fs := flag.NewFlagSet("cmdline", flag.ContinueOnError)
		fs.SetOutput(stderr)
		file := fs.String("file", "/proc/cmdline", "kernel command line to read")
		if fs.Parse(args[1:]) != nil || fs.NArg() != 0 {
			return 2
		}
		b, err := os.ReadFile(*file)
		if err != nil {
			fmt.Fprintln(stderr, "river-netcheck:", err)
			return 1
		}
		c, _, err := netcfg.FromCmdline(string(b))
		return emit(c, err, stdout, stderr)
	case "spec":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: river-netcheck spec SPEC")
			return 2
		}
		c, err := netcfg.ParseSpec(args[1])
		return emit(c, err, stdout, stderr)
	case "config":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: river-netcheck config FILE")
			return 2
		}
		f, err := os.Open(args[1]) // #nosec G703 -- an operator CLI: reading the file it is given is its job
		if err != nil {
			fmt.Fprintln(stderr, "river-netcheck:", err)
			return 1
		}
		defer f.Close()
		c, err := netcfg.ParseKeyfile(f)
		if err != nil {
			err = fmt.Errorf("%s: %w", args[1], err)
		}
		return emit(c, err, stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprintln(stdout, "river-netcheck check|cmdline|spec|config — see the header of installer/netcheck/main.go")
		return 0
	}
	fmt.Fprintf(stderr, "river-netcheck: unknown command %q\n", args[0])
	return 2
}

func emit(c netcfg.Config, err error, stdout, stderr io.Writer) int {
	if err != nil {
		fmt.Fprintln(stderr, "river-netcheck:", err)
		return 1
	}
	fmt.Fprint(stdout, c.Env())
	return 0
}

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	method := fs.String("method", "unknown", "how the network was set up (recorded)")
	host := fs.String("host", envOr("RIVER_NET_PROBE_HOST", netstate.DefaultProbeHost), "well-known host for the internet check (tcp/443); none disables it")
	port := fs.Int("port", 443, "TCP port of the internet check")
	timeout := fs.Duration("timeout", 3*time.Second, "per-probe timeout")
	proxy := fs.String("proxy", "", "HTTP(S) proxy URL to probe (host:port answered = online)")
	hostname := fs.String("hostname", "", "the hostname the operator chose (recorded)")
	state := fs.String("state", "/run/river/net-state.json", "state file to write")
	envF := fs.String("env", "", "sh-sourceable state (default: net.env beside --state)")
	asJSON := fs.Bool("json", false, "print the state JSON instead of the report")
	noWrite := fs.Bool("no-write", false, "report only; record nothing")
	root := fs.String("root", "/", "filesystem root for /proc, /sys and /etc (tests)")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return 2
	}
	if *host != "none" {
		probe := netcfg.Config{Method: netcfg.None, ProbeHost: *host}
		if err := probe.Validate(); err != nil {
			fmt.Fprintln(stderr, "river-netcheck:", err)
			return 2
		}
	}
	if *port < 1 || *port > 65535 || *timeout <= 0 || *timeout > time.Minute {
		fmt.Fprintln(stderr, "river-netcheck: --port 1-65535 and --timeout up to 1m")
		return 2
	}
	o := netstate.Options{Method: *method, ProbeHost: *host, ProbePort: *port, Timeout: *timeout, Hostname: *hostname}
	if *hostname != "" && !netcfg.ValidHostname(*hostname) {
		fmt.Fprintf(stderr, "river-netcheck: --hostname %q is not a hostname\n", *hostname)
		return 2
	}
	if *proxy != "" {
		hp, err := netcfg.ProxyHostPort(*proxy)
		if err != nil {
			fmt.Fprintln(stderr, "river-netcheck:", err)
			return 2
		}
		o.Proxy, o.ProxyAddr, o.ProxyShow = *proxy, hp, netcfg.Redact(*proxy)
	}
	// The whole check is bounded: parallel probes of at most two rounds of --timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 4**timeout+2*time.Second)
	defer cancel()
	s := netstate.Run(ctx, netstate.System(*root), o)
	if *asJSON {
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Fprintln(stdout, string(b))
	} else {
		netstate.Report(stdout, s)
	}
	if *noWrite {
		return 0
	}
	env := *envF
	if env == "" {
		env = filepath.Join(filepath.Dir(*state), "net.env")
	}
	if err := netstate.Write(*state, env, s, *hostname); err != nil {
		// Not fatal: the check itself worked (an unprivileged run cannot write /run/river).
		fmt.Fprintf(stderr, "river-netcheck: state not recorded (%v)\n", short(err))
		return 0
	}
	if !*asJSON {
		fmt.Fprintf(stdout, "state recorded in %s\n", *state)
	}
	return 0
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func short(err error) string {
	if pe, ok := err.(*os.PathError); ok {
		return pe.Op + " " + pe.Path + ": " + pe.Err.Error()
	}
	return err.Error()
}
