// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package netcfg parses the network settings river-netsetup applies: the kernel command
// line (river.net=...), the same grammar given on its own (--net SPEC), and a keyfile-style
// config file (--config FILE). It only parses and validates; river-netsetup applies the
// result with nmcli. Env renders it as sh assignments for that wrapper.
//
// Grammar of river.net= (no spaces; one kernel parameter):
//
//	dhcp[,v6only][,iface=NAME]
//	    automatic addressing, dual-stack: DHCPv4 plus IPv6 SLAAC/DHCPv6 (the host is
//	    dual-stack; only the k0s cluster network is IPv6-only). "v6only" turns IPv4 off,
//	    for IPv6-only sites
//	static:ADDR/PREFIX[,ADDR/PREFIX...][,gw=ADDR]...[,dns=ADDR]...[,iface=NAME]
//	    fixed addresses; IPv4 and IPv6 may be mixed, gw= and dns= repeat
//	off
//	    networking off for the live session
//
// Wi-Fi is config-file only: a passphrase does not belong on the kernel command line,
// which every local user can read in /proc/cmdline.
package netcfg

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Method is how the live session gets its network.
type Method string

// The methods river-netsetup knows.
const (
	None   Method = ""       // nothing to apply (no river.net= on the command line)
	DHCP   Method = "dhcp"   // automatic addressing
	Static Method = "static" // fixed addresses
	WiFi   Method = "wifi"   // a Wi-Fi network (config file or interactive)
	Off    Method = "off"    // networking off
)

// Config is one network configuration to apply.
type Config struct {
	Method    Method
	Iface     string         // empty: the first wired (or Wi-Fi) device
	V6Only    bool           // dhcp or wifi: no IPv4 (IPv6-only sites)
	Addrs     []netip.Prefix // static
	Gateways  []netip.Addr   // static; at most one per family
	DNS       []netip.Addr   // static (or dhcp: extra servers)
	SSID      string         // wifi
	PSK       string         // wifi; empty for an open network
	Proxy     string         // optional HTTP(S) proxy URL
	NoProxy   string         // optional no_proxy list
	Hostname  string         // optional
	ProbeHost string         // optional host for the internet check
}

var (
	ifaceRE    = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)
	hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	probeRE    = regexp.MustCompile(`^[A-Za-z0-9.:-]{1,253}$`)
	noProxyRE  = regexp.MustCompile(`^[A-Za-z0-9.,:/*_-]*$`)
	pskHexRE   = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)
)

// FromCmdline returns the river.net= setting of a kernel command line. found is false when
// the parameter is absent (nothing to apply). The last river.net= wins, as for the kernel.
// river.net.check=HOST (or none) sets the internet-check host, with or without river.net=.
func FromCmdline(cmdline string) (c Config, found bool, err error) {
	spec, probe := "", ""
	for _, f := range strings.Fields(cmdline) {
		if v, ok := strings.CutPrefix(f, "river.net="); ok {
			spec, found = v, true
		}
		if v, ok := strings.CutPrefix(f, "river.net.check="); ok {
			probe = v
		}
	}
	if found {
		if c, err = ParseSpec(spec); err != nil {
			return c, true, err
		}
	}
	c.ProbeHost = probe
	return c, found, c.Validate()
}

// ParseSpec parses one river.net value (see the package comment).
func ParseSpec(spec string) (Config, error) {
	var c Config
	head, rest, _ := strings.Cut(spec, ",")
	var opts []string
	if rest != "" {
		opts = strings.Split(rest, ",")
	}
	switch {
	case head == "dhcp":
		c.Method = DHCP
	case head == "off":
		c.Method = Off
		if len(opts) > 0 {
			return c, fmt.Errorf("river.net=off takes no options (got %q)", rest)
		}
	case strings.HasPrefix(head, "static:"):
		c.Method = Static
		opts = append([]string{strings.TrimPrefix(head, "static:")}, opts...)
	case head == "wifi" || strings.HasPrefix(head, "wifi:"):
		return c, errors.New("river.net: Wi-Fi is set up interactively or with --config FILE; a passphrase does not belong on the kernel command line")
	case head == "":
		return c, errors.New("river.net: empty value (want dhcp, static:ADDR/PREFIX,... or off)")
	default:
		return c, fmt.Errorf("river.net: unknown method %q (want dhcp, static:ADDR/PREFIX,... or off)", head)
	}
	for _, o := range opts {
		k, v, hasEq := strings.Cut(o, "=")
		var err error
		switch {
		case !hasEq && k == "v6only" && c.Method == DHCP:
			c.V6Only = true
		case !hasEq && c.Method == Static:
			err = c.addAddr(k)
		case hasEq && k == "iface":
			c.Iface = v
		case hasEq && k == "gw" && c.Method == Static:
			err = c.addGateway(v)
		case hasEq && k == "dns":
			err = c.addDNS(v)
		case hasEq && k == "hostname":
			c.Hostname = v
		default:
			err = fmt.Errorf("river.net: unknown option %q for %s", o, c.Method)
		}
		if err != nil {
			return c, err
		}
	}
	return c, c.Validate()
}

func (c *Config) addAddr(s string) error {
	for _, one := range splitList(s) {
		p, err := netip.ParsePrefix(one)
		if err != nil {
			return fmt.Errorf("address %q: want ADDR/PREFIX (e.g. 192.0.2.10/24 or 2001:db8::10/64)", one)
		}
		c.Addrs = append(c.Addrs, p)
	}
	return nil
}

func (c *Config) addGateway(s string) error {
	for _, one := range splitList(s) {
		a, err := netip.ParseAddr(one)
		if err != nil {
			return fmt.Errorf("gateway %q is not an IP address", one)
		}
		c.Gateways = append(c.Gateways, a)
	}
	return nil
}

func (c *Config) addDNS(s string) error {
	for _, one := range splitList(s) {
		a, err := netip.ParseAddr(one)
		if err != nil {
			return fmt.Errorf("dns %q is not an IP address", one)
		}
		c.DNS = append(c.DNS, a)
	}
	return nil
}

// splitList splits a config value on commas and blanks, dropping empty items.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '+' })
}

// ParseKeyfile reads a keyfile-style config (see docs/INSTALL.md, "Network first"):
//
//	[network]  method=dhcp|static|wifi|off  interface=  ipv6-only=true|false
//	           address=ADDR/PREFIX (repeats)  gateway=ADDR (repeats)  dns=ADDR (repeats)
//	           hostname=NAME
//	[wifi]     ssid=  psk=
//	[proxy]    http=URL  no-proxy=LIST
//	[check]    host=HOST
//
// Unknown sections and keys are errors: a typo must not silently fall back to DHCP.
func ParseKeyfile(r io.Reader) (Config, error) {
	var c Config
	sec := ""
	sc := bufio.NewScanner(r)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec = strings.TrimSpace(line[1 : len(line)-1])
			switch sec {
			case "network", "wifi", "proxy", "check":
			default:
				return c, fmt.Errorf("line %d: unknown section [%s]", n, sec)
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return c, fmt.Errorf("line %d: want key=value", n)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if sec == "" {
			return c, fmt.Errorf("line %d: %s= outside a section", n, k)
		}
		var err error
		switch sec + "." + k {
		case "network.method":
			switch Method(v) {
			case DHCP, Static, WiFi, Off:
				c.Method = Method(v)
			default:
				err = fmt.Errorf("method %q (want dhcp, static, wifi or off)", v)
			}
		case "network.interface":
			c.Iface = v
		case "network.ipv6-only":
			c.V6Only, err = strconv.ParseBool(v)
		case "network.address":
			err = c.addAddr(v)
		case "network.gateway":
			err = c.addGateway(v)
		case "network.dns":
			err = c.addDNS(v)
		case "network.hostname":
			c.Hostname = v
		case "wifi.ssid":
			c.SSID = v
		case "wifi.psk":
			c.PSK = v
		case "proxy.http":
			c.Proxy = v
		case "proxy.no-proxy":
			c.NoProxy = v
		case "check.host":
			c.ProbeHost = v
		default:
			err = fmt.Errorf("unknown key %s in [%s]", k, sec)
		}
		if err != nil {
			return c, fmt.Errorf("line %d: %w", n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return c, err
	}
	if c.Method == None {
		return c, errors.New("no [network] method= (want dhcp, static, wifi or off)")
	}
	if c.Method == WiFi && c.SSID == "" {
		return c, errors.New("method=wifi needs [wifi] ssid=")
	}
	return c, c.Validate()
}

// Validate checks the settings river-netsetup would hand to nmcli.
func (c Config) Validate() error {
	if c.Iface != "" && !ifaceRE.MatchString(c.Iface) {
		return fmt.Errorf("interface %q is not a network interface name", c.Iface)
	}
	if c.Hostname != "" && !ValidHostname(c.Hostname) {
		return fmt.Errorf("hostname %q: want lowercase letters, digits and '-', 1-63 characters", c.Hostname)
	}
	if c.ProbeHost != "" && !probeRE.MatchString(c.ProbeHost) {
		return fmt.Errorf("check host %q is not a host name or address", c.ProbeHost)
	}
	if c.Proxy != "" {
		if _, err := ProxyHostPort(c.Proxy); err != nil {
			return err
		}
	}
	if len(c.NoProxy) > 1024 || !noProxyRE.MatchString(c.NoProxy) {
		return fmt.Errorf("no-proxy %q has characters a host list does not", c.NoProxy)
	}
	switch c.Method {
	case Static:
		if len(c.Addrs) == 0 {
			return errors.New("static: at least one ADDR/PREFIX is required")
		}
		if c.V6Only && c.hasFamily(true) {
			return errors.New("static: ipv6-only with an IPv4 address")
		}
		for _, p := range c.Addrs {
			if !p.Addr().IsGlobalUnicast() {
				return fmt.Errorf("static address %s is not a unicast host address", p)
			}
			if p.Bits() < 1 {
				return fmt.Errorf("static address %s: bad prefix length", p)
			}
		}
		var gw4, gw6 int
		for _, g := range c.Gateways {
			if g.Is4() {
				gw4++
				if !c.hasFamily(true) {
					return fmt.Errorf("gateway %s is IPv4 but no IPv4 address is configured", g)
				}
			} else {
				gw6++
				if !c.hasFamily(false) && !g.IsLinkLocalUnicast() {
					return fmt.Errorf("gateway %s is IPv6 but no IPv6 address is configured", g)
				}
			}
		}
		if gw4 > 1 || gw6 > 1 {
			return errors.New("static: at most one gateway per address family")
		}
	case WiFi:
		if c.SSID == "" || len(c.SSID) > 32 {
			return errors.New("wifi: ssid must be 1-32 bytes")
		}
		for _, r := range c.SSID {
			if r < 0x20 || r == 0x7f {
				return errors.New("wifi: ssid has a control character")
			}
		}
		if c.PSK != "" && !pskHexRE.MatchString(c.PSK) && (len(c.PSK) < 8 || len(c.PSK) > 63) {
			return errors.New("wifi: psk must be 8-63 characters (or 64 hex digits); leave it empty for an open network")
		}
	case DHCP, Off, None:
		if len(c.Addrs) > 0 || len(c.Gateways) > 0 {
			return fmt.Errorf("%s takes no address or gateway", c.Method)
		}
	default:
		return fmt.Errorf("unknown method %q", c.Method)
	}
	return nil
}

func (c Config) hasFamily(v4 bool) bool {
	for _, p := range c.Addrs {
		if p.Addr().Is4() == v4 {
			return true
		}
	}
	return false
}

// ValidHostname reports whether s is a single lowercase RFC 1123 label.
func ValidHostname(s string) bool { return hostnameRE.MatchString(s) }

// ProxyHostPort validates an http:// or https:// proxy URL and returns its host:port.
func ProxyHostPort(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("proxy %q: want http://HOST:PORT", Redact(raw))
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("proxy %q: want http://HOST:PORT with no path", Redact(raw))
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return "", fmt.Errorf("proxy %q: bad port", Redact(raw))
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}

// Redact drops any user:password from a proxy URL, for logs and the state file.
func Redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		if err != nil {
			return "(unparseable)"
		}
		return raw
	}
	u.User = url.User("REDACTED")
	return u.String()
}

// Env renders c as sh assignments (NET_*) for river-netsetup to eval. Every value is
// single-quoted, so nothing in a config file can reach the shell as code.
func (c Config) Env() string {
	var a4, a6, g4, g6, d4, d6 []string
	for _, p := range c.Addrs {
		if p.Addr().Is4() {
			a4 = append(a4, p.String())
		} else {
			a6 = append(a6, p.String())
		}
	}
	for _, g := range c.Gateways {
		if g.Is4() {
			g4 = append(g4, g.String())
		} else {
			g6 = append(g6, g.String())
		}
	}
	for _, d := range c.DNS {
		if d.Is4() {
			d4 = append(d4, d.String())
		} else {
			d6 = append(d6, d.String())
		}
	}
	v6only := "0"
	if c.V6Only {
		v6only = "1"
	}
	var b strings.Builder
	for _, kv := range [][2]string{
		{"NET_METHOD", string(c.Method)},
		{"NET_IFACE", c.Iface},
		{"NET_V6ONLY", v6only},
		{"NET_ADDR4", strings.Join(a4, ",")},
		{"NET_ADDR6", strings.Join(a6, ",")},
		{"NET_GW4", strings.Join(g4, ",")},
		{"NET_GW6", strings.Join(g6, ",")},
		{"NET_DNS4", strings.Join(d4, ",")},
		{"NET_DNS6", strings.Join(d6, ",")},
		{"NET_SSID", c.SSID},
		{"NET_PSK", c.PSK},
		{"NET_PROXY", c.Proxy},
		{"NET_NO_PROXY", c.NoProxy},
		{"NET_HOSTNAME", c.Hostname},
		{"NET_PROBE_HOST", c.ProbeHost},
	} {
		fmt.Fprintf(&b, "%s=%s\n", kv[0], ShQuote(kv[1]))
	}
	return b.String()
}

// ShQuote single-quotes s for a POSIX shell.
func ShQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
