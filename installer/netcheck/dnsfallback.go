// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

// dns-compose — the IPv4 fallback resolvers (docs: book/docs/configuration/networking,
// "IPv4 fallback resolvers").
//
// Internal traffic may be IPv6-only; external names must still resolve when the IPv6
// resolvers a network hands out stop answering. NetworkManager cannot add resolvers as a
// connection default, so this rewrites /etc/resolv.conf from NetworkManager's own copy
// (/run/NetworkManager/resolv.conf), keeping the network's resolvers and adding the
// fallbacks named in /etc/river/dns-fallback.conf:
//
//   - the network's FIRST resolver stays first: it answers the local names (a search domain
//     such as home.arpa), and a public resolver's "no such name" does not fall through;
//   - then the network's other IPv4 resolvers, then the fallbacks, then its IPv6 resolvers;
//   - fallbacks are added only when the host has an IPv4 default route (dual-stack mode):
//     in IPv6-only mode there is no IPv4 path, and the file is passed through unchanged;
//   - DNS_OPTIONS (timeout/attempts) replace the same keys in NetworkManager's options.
//
// It runs as a NetworkManager dispatcher (a link named *dns-fallback in dispatcher.d to
// this binary) and by hand: river-netcheck dns-compose [--src F] [--out F] [--conf F].
// An empty FALLBACK_DNS, a missing config, or an /etc/resolv.conf that is a link to
// anything but NetworkManager's file leaves resolv.conf as NetworkManager wrote it.

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	nmResolv       = "/run/NetworkManager/resolv.conf"
	etcResolv      = "/etc/resolv.conf"
	fallbackConfig = "/etc/river/dns-fallback.conf"
)

// dispatcherActions are the NetworkManager events after which the resolvers may differ.
var dispatcherActions = map[string]bool{
	"up": true, "down": true, "dhcp4-change": true, "dhcp6-change": true,
	"dns-change": true, "reapply": true, "connectivity-change": true,
}

var optionToken = regexp.MustCompile(`^[a-z][a-z0-9-]*(:[0-9]+)?$`)

type fallbackConf struct {
	servers []string // IPv4 only
	options []string // resolver options, e.g. timeout:2
}

// readFallbackConf reads KEY=value lines (FALLBACK_DNS, DNS_OPTIONS; values may be quoted,
// space- or comma-separated). A missing file is an empty configuration.
func readFallbackConf(path string) (fallbackConf, error) {
	var c fallbackConf
	b, err := os.ReadFile(path) // #nosec G304 -- the operator's config path
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("%s:%d: want KEY=value", path, n)
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		fields := strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
		switch strings.TrimSpace(k) {
		case "FALLBACK_DNS":
			for _, f := range fields {
				ip := net.ParseIP(f)
				if ip == nil || ip.To4() == nil {
					return c, fmt.Errorf("%s:%d: FALLBACK_DNS takes IPv4 addresses, got %q", path, n, f)
				}
				c.servers = append(c.servers, ip.To4().String())
			}
		case "DNS_OPTIONS":
			for _, f := range fields {
				if !optionToken.MatchString(f) {
					return c, fmt.Errorf("%s:%d: bad resolver option %q", path, n, f)
				}
				c.options = append(c.options, f)
			}
		default:
			return c, fmt.Errorf("%s:%d: unknown key %q", path, n, k)
		}
	}
	return c, sc.Err()
}

// composeResolv returns the resolv.conf for NetworkManager's file src, the fallback
// configuration and whether the host has an IPv4 path.
func composeResolv(src string, c fallbackConf, ipv4Path bool) string {
	var keep, servers, opts []string
	for _, line := range strings.Split(src, "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 0 || strings.HasPrefix(f[0], "#") || strings.HasPrefix(f[0], ";"):
		case f[0] == "nameserver" && len(f) >= 2:
			servers = append(servers, f[1])
		case f[0] == "options":
			opts = append(opts, f[1:]...)
		default:
			keep = append(keep, strings.Join(f, " "))
		}
	}
	ordered := servers
	if ipv4Path && len(c.servers) > 0 {
		ordered = nil
		seen := map[string]bool{}
		add := func(s string) {
			if !seen[s] {
				seen[s] = true
				ordered = append(ordered, s)
			}
		}
		var v6 []string
		for i, s := range servers {
			ip := net.ParseIP(strings.SplitN(s, "%", 2)[0])
			if i == 0 || (ip != nil && ip.To4() != nil) {
				add(s)
			} else {
				v6 = append(v6, s)
			}
		}
		for _, s := range c.servers {
			add(s)
		}
		for _, s := range v6 {
			add(s)
		}
	}
	if len(c.options) > 0 {
		ours := map[string]bool{}
		for _, o := range c.options {
			k, _, _ := strings.Cut(o, ":")
			ours[k] = true
		}
		var merged []string
		for _, o := range opts {
			if k, _, _ := strings.Cut(o, ":"); !ours[k] {
				merged = append(merged, o)
			}
		}
		opts = append(merged, c.options...)
	}
	var b strings.Builder
	b.WriteString("# Generated by NetworkManager; IPv4 fallback resolvers by river-netcheck dns-compose\n")
	b.WriteString("# (" + fallbackConfig + "). Edit that file, not this one.\n")
	for _, l := range keep {
		b.WriteString(l + "\n")
	}
	for _, s := range ordered {
		b.WriteString("nameserver " + s + "\n")
	}
	if len(opts) > 0 {
		b.WriteString("options " + strings.Join(opts, " ") + "\n")
	}
	return b.String()
}

// hasIPv4Default reports whether /proc/net/route (under root) lists an IPv4 default route.
func hasIPv4Default(root string) bool {
	b, err := os.ReadFile(filepath.Join(root, "proc/net/route"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 8 && f[1] == "00000000" && f[7] == "00000000" && f[0] != "lo" {
			return true
		}
	}
	return false
}

func cmdDNSCompose(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dns-compose", flag.ContinueOnError)
	fs.SetOutput(stderr)
	src := fs.String("src", nmResolv, "NetworkManager's resolv.conf")
	out := fs.String("out", etcResolv, "resolv.conf to write")
	conf := fs.String("conf", fallbackConfig, "fallback configuration")
	root := fs.String("root", "/", "filesystem root for /proc (tests)")
	dryRun := fs.Bool("print", false, "print the result instead of writing it")
	if fs.Parse(args) != nil || fs.NArg() != 0 {
		return 2
	}
	return dnsCompose(*src, *out, *conf, *root, *dryRun, stdout, stderr)
}

func dnsCompose(src, out, conf, root string, dryRun bool, stdout, stderr io.Writer) int {
	c, err := readFallbackConf(conf)
	if err != nil {
		fmt.Fprintln(stderr, "river-netcheck dns-compose:", err)
		return 1
	}
	raw, err := os.ReadFile(src) // #nosec G304 -- NetworkManager's file, or the test's
	if err != nil {
		return 0 // NetworkManager manages no resolv.conf (dns=none): nothing to add to
	}
	if fi, err := os.Lstat(out); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if t, _ := os.Readlink(out); filepath.Clean(t) != filepath.Clean(src) {
			return 0 // the operator points resolv.conf elsewhere: leave it alone
		}
	}
	text := composeResolv(string(raw), c, hasIPv4Default(root))
	if dryRun {
		fmt.Fprint(stdout, text)
		return 0
	}
	if cur, err := os.ReadFile(out); err == nil && string(cur) == text { // #nosec G304
		return 0
	}
	tmp, err := os.CreateTemp(filepath.Dir(out), ".resolv.conf.")
	if err != nil {
		fmt.Fprintln(stderr, "river-netcheck dns-compose:", err)
		return 1
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		fmt.Fprintln(stderr, "river-netcheck dns-compose:", err)
		return 1
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		fmt.Fprintln(stderr, "river-netcheck dns-compose:", err)
		return 1
	}
	if err := tmp.Close(); err != nil {
		fmt.Fprintln(stderr, "river-netcheck dns-compose:", err)
		return 1
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		fmt.Fprintln(stderr, "river-netcheck dns-compose:", err)
		return 1
	}
	return 0
}

// asDispatcher handles a run through a NetworkManager dispatcher.d link whose name ends in
// "dns-fallback": NetworkManager passes the interface and the action.
func asDispatcher(argv0 string, args []string, stdout, stderr io.Writer) (int, bool) {
	if !strings.HasSuffix(filepath.Base(argv0), "dns-fallback") {
		return 0, false
	}
	if len(args) < 2 || !dispatcherActions[args[1]] {
		return 0, true
	}
	return dnsCompose(nmResolv, etcResolv, fallbackConfig, "/", false, stdout, stderr), true
}
