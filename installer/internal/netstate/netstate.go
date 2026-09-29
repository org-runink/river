// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package netstate checks what network the live session has and records it.
//
// The check reports the links and their addresses, the default routes, the DNS servers,
// and whether the default gateways answer, DNS resolves, and the internet is reachable
// over IPv4 and IPv6. The internet check is a plain TCP connect to port 443 of a
// well-known host (no HTTP, no curl). Every probe has a short timeout and is skipped when
// it cannot succeed (no route, no resolver), so an offline machine gets its answer at once.
//
// The outcome is one of online, lan-only or offline. All three are valid: the install
// itself needs no network. It is written to /run/river/net-state.json (schema
// river.net-state/v1) for the later install steps, plus an sh-sourceable net.env.
package netstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Schema is the net-state.json schema identifier.
const Schema = "river.net-state/v1"

// Outcomes.
const (
	Online  = "online"
	LANOnly = "lan-only"
	Offline = "offline"
)

// DefaultProbeHost is the well-known host the internet check connects to (port 443).
// Override with --host, RIVER_NET_PROBE_HOST or [check] host= in a config file.
const DefaultProbeHost = "kernel.org"

var errDisabled = errors.New("internet check disabled")

// Link is one non-loopback network interface.
type Link struct {
	Name    string   `json:"name"`
	Up      bool     `json:"up"`
	Carrier bool     `json:"carrier"`
	Addrs   []string `json:"addrs"`
}

// Route is a default route.
type Route struct {
	Family  string `json:"family"` // ipv4 | ipv6
	Iface   string `json:"iface"`
	Gateway string `json:"gateway"` // empty for an on-link default route
}

// Check is the result of one probe.
type Check struct {
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"`
	Target  string `json:"target,omitempty"`
	Detail  string `json:"detail"`
}

// Checks are the reachability probes, in report order.
type Checks struct {
	Gateway4  Check `json:"gateway4"`
	Gateway6  Check `json:"gateway6"`
	DNS       Check `json:"dns"`
	Internet4 Check `json:"internet4"`
	Internet6 Check `json:"internet6"`
	Proxy     Check `json:"proxy"`
}

// State is the recorded result (net-state.json).
type State struct {
	Schema    string   `json:"schema"`
	State     string   `json:"state"`
	Method    string   `json:"method"`
	CheckedAt string   `json:"checked_at"`
	Hostname  string   `json:"hostname"`
	ProbeHost string   `json:"probe_host"`
	ProbePort int      `json:"probe_port"`
	Proxy     string   `json:"proxy,omitempty"` // credentials redacted
	Links     []Link   `json:"links"`
	Routes    []Route  `json:"routes"`
	DNS       []string `json:"dns"`
	Checks    Checks   `json:"checks"`
}

// Env is what the check reads from the machine. System() is the real one; tests fake it.
type Env struct {
	Links     func() ([]Link, error)
	Routes    func() ([]Route, error)
	Resolvers func() []string
	Hostname  func() string
	// Dial connects and closes; nil means connected.
	Dial func(ctx context.Context, network, addr string) error
	// Lookup resolves host to addresses.
	Lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	// Neighbour reports whether addr (on iface) is in the kernel's neighbour table as
	// answering (reachable, stale, delay, probe or permanent).
	Neighbour func(addr netip.Addr, iface string) bool
	Now       func() time.Time
}

// Options control one check.
type Options struct {
	Method    string        // how the network was set up (dhcp, static, wifi, off, ...)
	ProbeHost string        // default DefaultProbeHost
	ProbePort int           // default 443
	Proxy     string        // optional proxy URL (host:port is probed)
	ProxyAddr string        // host:port of Proxy, already validated
	ProxyShow string        // Proxy with credentials redacted
	Hostname  string        // the hostname the operator chose, if any
	Timeout   time.Duration // per probe; default 3s
}

// Run performs the check. It never fails: a probe that cannot run is recorded as such.
func Run(ctx context.Context, env Env, o Options) State {
	if o.ProbeHost == "" {
		o.ProbeHost = DefaultProbeHost
	}
	if o.ProbePort == 0 {
		o.ProbePort = 443
	}
	if o.Timeout == 0 {
		o.Timeout = 3 * time.Second
	}
	if o.Method == "" {
		o.Method = "unknown"
	}
	s := State{Schema: Schema, Method: o.Method, ProbeHost: o.ProbeHost, ProbePort: o.ProbePort,
		Proxy: o.ProxyShow, Links: []Link{}, Routes: []Route{}, DNS: []string{}}
	s.CheckedAt = env.Now().UTC().Format(time.RFC3339)
	s.Hostname = o.Hostname
	if s.Hostname == "" {
		s.Hostname = env.Hostname()
	}
	if l, err := env.Links(); err == nil && l != nil {
		s.Links = l
	}
	if r, err := env.Routes(); err == nil && r != nil {
		s.Routes = r
	}
	if d := env.Resolvers(); d != nil {
		s.DNS = d
	}

	var wg sync.WaitGroup
	var addrs []netip.Addr
	var dnsErr error
	probeIP, perr := netip.ParseAddr(o.ProbeHost)
	disabled := o.ProbeHost == "none"
	gw := func(fam string, c *Check) {
		defer wg.Done()
		*c = checkGateway(ctx, env, s.Routes, fam, o.Timeout)
	}
	wg.Add(2)
	go gw("ipv4", &s.Checks.Gateway4)
	go gw("ipv6", &s.Checks.Gateway6)
	wg.Add(1)
	go func() {
		defer wg.Done()
		switch {
		case disabled:
			dnsErr = errDisabled
			s.Checks.DNS = Check{Skipped: true, Detail: "internet check disabled (check host none)"}
		case perr == nil:
			addrs = []netip.Addr{probeIP}
			s.Checks.DNS = Check{Skipped: true, Detail: "the check host is an address; nothing to resolve"}
		case len(s.DNS) == 0:
			dnsErr = errors.New("no DNS server configured")
			s.Checks.DNS = Check{Target: o.ProbeHost, Detail: dnsErr.Error()}
		default:
			c, cancel := context.WithTimeout(ctx, o.Timeout)
			defer cancel()
			addrs, dnsErr = env.Lookup(c, o.ProbeHost)
			if dnsErr != nil {
				s.Checks.DNS = Check{Target: o.ProbeHost, Detail: "resolution failed: " + short(dnsErr)}
			} else {
				s.Checks.DNS = Check{OK: true, Target: o.ProbeHost, Detail: fmt.Sprintf("%d address(es)", len(addrs))}
			}
		}
	}()
	if o.ProxyAddr != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Checks.Proxy = dialCheck(ctx, env, "tcp", o.ProxyAddr, o.Timeout)
		}()
	} else {
		s.Checks.Proxy = Check{Skipped: true, Detail: "no proxy configured"}
	}
	wg.Wait()

	wg.Add(2)
	inet := func(v4 bool, c *Check) {
		defer wg.Done()
		*c = checkInternet(ctx, env, s.Routes, addrs, dnsErr, v4, o.ProbePort, o.Timeout)
	}
	go inet(true, &s.Checks.Internet4)
	go inet(false, &s.Checks.Internet6)
	wg.Wait()

	s.State = Classify(s)
	return s
}

func hasDefault(routes []Route, fam string) []Route {
	var out []Route
	for _, r := range routes {
		if r.Family == fam {
			out = append(out, r)
		}
	}
	return out
}

func checkGateway(ctx context.Context, env Env, routes []Route, fam string, to time.Duration) Check {
	rs := hasDefault(routes, fam)
	if len(rs) == 0 {
		return Check{Skipped: true, Detail: "no " + famName(fam) + " default route"}
	}
	r := rs[0]
	if r.Gateway == "" {
		return Check{OK: true, Target: "dev " + r.Iface, Detail: "on-link default route"}
	}
	gw, err := netip.ParseAddr(r.Gateway)
	if err != nil {
		return Check{Target: r.Gateway, Detail: "unparseable gateway"}
	}
	target := gw.String()
	host := target
	if gw.Is6() && gw.IsLinkLocalUnicast() {
		host += "%" + r.Iface
		target = host
	}
	network := "tcp4"
	if gw.Is6() {
		network = "tcp6"
	}
	// A router rarely listens on 443, but a TCP reset proves it answered as surely as a
	// handshake does. A router that drops the probe still had to resolve its link-layer
	// address first, so the neighbour table is the second witness.
	c, cancel := context.WithTimeout(ctx, to)
	err = env.Dial(c, network, net.JoinHostPort(host, "443"))
	cancel()
	switch {
	case err == nil:
		return Check{OK: true, Target: target, Detail: "answered (tcp connect)"}
	case errors.Is(err, syscall.ECONNREFUSED):
		return Check{OK: true, Target: target, Detail: "answered (tcp reset)"}
	case env.Neighbour(gw, r.Iface):
		return Check{OK: true, Target: target, Detail: "answered (neighbour table)"}
	}
	return Check{Target: target, Detail: "no answer: " + short(err)}
}

func checkInternet(ctx context.Context, env Env, routes []Route, addrs []netip.Addr, dnsErr error, v4 bool, port int, to time.Duration) Check {
	fam, network := "ipv6", "tcp6"
	if v4 {
		fam, network = "ipv4", "tcp4"
	}
	if len(hasDefault(routes, fam)) == 0 {
		return Check{Skipped: true, Detail: "no " + famName(fam) + " default route"}
	}
	if errors.Is(dnsErr, errDisabled) {
		return Check{Skipped: true, Detail: "disabled (check host none)"}
	}
	if dnsErr != nil {
		return Check{Skipped: true, Detail: "no address to try (DNS failed)"}
	}
	var cand []netip.Addr
	for _, a := range addrs {
		if a.Unmap().Is4() == v4 {
			cand = append(cand, a.Unmap())
		}
	}
	if len(cand) == 0 {
		return Check{Skipped: true, Detail: "the check host has no " + famName(fam) + " address"}
	}
	if len(cand) > 2 {
		cand = cand[:2]
	}
	var last Check
	for _, a := range cand {
		last = dialCheck(ctx, env, network, net.JoinHostPort(a.String(), fmt.Sprint(port)), to)
		if last.OK {
			return last
		}
	}
	return last
}

func dialCheck(ctx context.Context, env Env, network, addr string, to time.Duration) Check {
	c, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	if err := env.Dial(c, network, addr); err != nil {
		return Check{Target: addr, Detail: "tcp connect failed: " + short(err)}
	}
	return Check{OK: true, Target: addr, Detail: "tcp connect ok"}
}

func famName(fam string) string {
	if fam == "ipv4" {
		return "IPv4"
	}
	return "IPv6"
}

// short trims Go's wrapped network errors to their last, useful part.
func short(err error) string {
	if err == nil {
		return "timeout"
	}
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && i+2 < len(msg) {
		msg = msg[i+2:]
	}
	return msg
}

// Classify derives the outcome from a checked state:
//
//	online    the check host answered on 443 over IPv4 or IPv6 (DNS resolved it, or it is
//	          an address), or the configured proxy answered
//	lan-only  not online, but a link has a routable (non-link-local) address or a gateway
//	          answered
//	offline   neither
func Classify(s State) string {
	c := s.Checks
	dnsOK := c.DNS.OK || c.DNS.Skipped
	if (dnsOK && (c.Internet4.OK || c.Internet6.OK)) || c.Proxy.OK {
		return Online
	}
	if (c.Gateway4.OK && !c.Gateway4.Skipped) || (c.Gateway6.OK && !c.Gateway6.Skipped) {
		return LANOnly
	}
	for _, l := range s.Links {
		if !l.Up {
			continue
		}
		for _, a := range l.Addrs {
			p, err := netip.ParsePrefix(a)
			if err == nil && p.Addr().IsGlobalUnicast() {
				return LANOnly
			}
		}
	}
	return Offline
}

// Report writes the human-readable summary.
func Report(w io.Writer, s State) {
	fmt.Fprintf(w, "Runink River network check (%s, method %s)\n", s.CheckedAt, s.Method)
	if len(s.Links) == 0 {
		fmt.Fprintln(w, "  link       (no network interface besides loopback)")
	}
	for _, l := range s.Links {
		st := "down"
		if l.Up {
			st = "up"
		}
		if l.Carrier {
			st += ", carrier"
		} else {
			st += ", no carrier"
		}
		addrs := strings.Join(l.Addrs, " ")
		if addrs == "" {
			addrs = "(no address)"
		}
		fmt.Fprintf(w, "  link       %-10s %s  %s\n", l.Name, st, addrs)
	}
	if len(s.Routes) == 0 {
		fmt.Fprintln(w, "  route      (no default route)")
	}
	for _, r := range s.Routes {
		via := "on-link"
		if r.Gateway != "" {
			via = "via " + r.Gateway
		}
		fmt.Fprintf(w, "  route      %s default %s dev %s\n", famName(r.Family), via, r.Iface)
	}
	dns := strings.Join(s.DNS, " ")
	if dns == "" {
		dns = "(none)"
	}
	fmt.Fprintf(w, "  dns        %s\n", dns)
	fmt.Fprintf(w, "  hostname   %s\n", s.Hostname)
	if s.Proxy != "" {
		fmt.Fprintf(w, "  proxy      %s\n", s.Proxy)
	}
	fmt.Fprintf(w, "  checks (host %s, tcp/%d):\n", s.ProbeHost, s.ProbePort)
	for _, row := range []struct {
		name string
		c    Check
	}{{"gateway4", s.Checks.Gateway4}, {"gateway6", s.Checks.Gateway6}, {"dns", s.Checks.DNS},
		{"internet4", s.Checks.Internet4}, {"internet6", s.Checks.Internet6}, {"proxy", s.Checks.Proxy}} {
		mark := "FAIL"
		switch {
		case row.c.Skipped:
			mark = "-"
		case row.c.OK:
			mark = "OK"
		}
		tgt := ""
		if row.c.Target != "" {
			tgt = row.c.Target + ": "
		}
		fmt.Fprintf(w, "    %-10s %-5s %s%s\n", row.name, mark, tgt, row.c.Detail)
	}
	fmt.Fprintf(w, "RESULT: %s\n", s.State)
}

// Write records s as JSON at path and as sh assignments at envPath (skipped when empty),
// each atomically (temp file + rename, mode 0644). The parent directory is created 0755:
// the state names no secret (a proxy's credentials are redacted before it gets here).
func Write(path, envPath string, s State, chosenHostname string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := writeAtomic(path, append(b, '\n')); err != nil {
		return err
	}
	if envPath == "" {
		return nil
	}
	yes := func(c Check) string {
		if c.OK && !c.Skipped {
			return "1"
		}
		return "0"
	}
	var e strings.Builder
	for _, kv := range [][2]string{
		{"RIVER_NET_STATE", s.State},
		{"RIVER_NET_METHOD", s.Method},
		{"RIVER_NET_STATE_FILE", path},
		{"RIVER_NET_INTERNET4", yes(s.Checks.Internet4)},
		{"RIVER_NET_INTERNET6", yes(s.Checks.Internet6)},
		{"RIVER_NET_HOSTNAME", chosenHostname},
	} {
		fmt.Fprintf(&e, "%s='%s'\n", kv[0], strings.ReplaceAll(kv[1], "'", `'\''`))
	}
	return writeAtomic(envPath, []byte(e.String()))
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	// #nosec G301 -- /run/river is world-readable on purpose: the unprivileged guide reads
	// net.env, and the state names no secret (proxy credentials are redacted before here).
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once renamed
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sortLinks orders links by name for a stable report.
func sortLinks(l []Link) {
	sort.Slice(l, func(i, j int) bool { return l[i].Name < l[j].Name })
}
