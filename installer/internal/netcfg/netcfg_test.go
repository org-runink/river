// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package netcfg

import (
	"strings"
	"testing"
)

func TestFromCmdline(t *testing.T) {
	for _, tc := range []struct {
		name, cmdline string
		found         bool
		method        Method
		env           []string // NET_* lines that must appear
		err           string
	}{
		{name: "absent", cmdline: "BOOT_IMAGE=/vmlinuz quiet label=RIVER", method: None,
			env: []string{"NET_METHOD=''"}},
		{name: "dhcp is dual-stack", cmdline: "quiet river.net=dhcp", found: true, method: DHCP,
			env: []string{"NET_METHOD='dhcp'", "NET_V6ONLY='0'"}},
		{name: "dhcp v6only and iface", cmdline: "river.net=dhcp,v6only,iface=enp1s0", found: true, method: DHCP,
			env: []string{"NET_V6ONLY='1'", "NET_IFACE='enp1s0'"}},
		{name: "the old ipv4 option is gone (dhcp is dual-stack)", cmdline: "river.net=dhcp,ipv4", found: true, err: "unknown option"},
		{name: "off", cmdline: "river.net=off", found: true, method: Off, env: []string{"NET_METHOD='off'"}},
		{name: "static dual stack", found: true, method: Static,
			cmdline: "river.net=static:192.0.2.10/24,gw=192.0.2.1,dns=192.0.2.53,2001:db8::10/64,gw=2001:db8::1,dns=2001:db8::53",
			env: []string{"NET_ADDR4='192.0.2.10/24'", "NET_ADDR6='2001:db8::10/64'", "NET_GW4='192.0.2.1'",
				"NET_GW6='2001:db8::1'", "NET_DNS4='192.0.2.53'", "NET_DNS6='2001:db8::53'"}},
		{name: "static v6 only, link-local gateway", found: true, method: Static,
			cmdline: "river.net=static:2001:db8::10/64,gw=fe80::1,iface=eth0",
			env:     []string{"NET_GW6='fe80::1'", "NET_ADDR4=''"}},
		{name: "last one wins", cmdline: "river.net=off river.net=dhcp", found: true, method: DHCP},
		{name: "check host", cmdline: "river.net.check=example.org", method: None,
			env: []string{"NET_PROBE_HOST='example.org'"}},
		{name: "check disabled", cmdline: "river.net=dhcp river.net.check=none", found: true, method: DHCP,
			env: []string{"NET_PROBE_HOST='none'"}},

		{name: "empty", cmdline: "river.net=", found: true, err: "empty value"},
		{name: "unknown method", cmdline: "river.net=pppoe", found: true, err: "unknown method"},
		{name: "wifi refused", cmdline: "river.net=wifi:home,psk=secret123", found: true, err: "kernel command line"},
		{name: "static needs address", cmdline: "river.net=static:,gw=192.0.2.1", found: true, err: "ADDR/PREFIX is required"},
		{name: "static without prefix", cmdline: "river.net=static:192.0.2.10", found: true, err: "ADDR/PREFIX"},
		{name: "gateway family without address", cmdline: "river.net=static:192.0.2.10/24,gw=2001:db8::1", found: true, err: "no IPv6 address"},
		{name: "two gateways", cmdline: "river.net=static:192.0.2.10/24,gw=192.0.2.1,gw=192.0.2.2", found: true, err: "one gateway"},
		{name: "bad gateway", cmdline: "river.net=static:192.0.2.10/24,gw=router", found: true, err: "not an IP"},
		{name: "loopback address", cmdline: "river.net=static:127.0.0.2/8", found: true, err: "unicast"},
		{name: "off takes nothing", cmdline: "river.net=off,ipv4", found: true, err: "no options"},
		{name: "dhcp unknown option", cmdline: "river.net=dhcp,gw=192.0.2.1", found: true, err: "unknown option"},
		{name: "bad iface", cmdline: "river.net=dhcp,iface=eth0;reboot", found: true, err: "interface"},
		{name: "bad check host", cmdline: "river.net.check=exa$mple", err: "check host"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, found, err := FromCmdline(tc.cmdline)
			if found != tc.found {
				t.Fatalf("found=%v, want %v", found, tc.found)
			}
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err %v, want it to mention %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Method != tc.method {
				t.Fatalf("method %q, want %q", c.Method, tc.method)
			}
			env := c.Env()
			for _, want := range tc.env {
				if !strings.Contains(env, want+"\n") {
					t.Errorf("env lacks %s:\n%s", want, env)
				}
			}
		})
	}
}

func TestParseKeyfile(t *testing.T) {
	good := `# a comment
[network]
method = wifi
interface = wlan0
hostname = node-a
dns = 2001:db8::53, 192.0.2.53
; another comment
[wifi]
ssid = Lab Net
psk = it's a secret
[proxy]
http = http://user:pw@proxy.example.org:3128
no-proxy = localhost,.example.org
[check]
host = example.org
`
	c, err := ParseKeyfile(strings.NewReader(good))
	if err != nil {
		t.Fatal(err)
	}
	if c.Method != WiFi || c.SSID != "Lab Net" || c.PSK != "it's a secret" || c.Iface != "wlan0" ||
		c.Hostname != "node-a" || len(c.DNS) != 2 || c.ProbeHost != "example.org" {
		t.Fatalf("%+v", c)
	}
	env := c.Env()
	// The quote in the passphrase must stay data: '\'' is the only way out of a '...' string.
	if !strings.Contains(env, `NET_PSK='it'\''s a secret'`+"\n") {
		t.Fatalf("psk not safely quoted:\n%s", env)
	}
	if !strings.Contains(env, "NET_DNS6='2001:db8::53'\n") || !strings.Contains(env, "NET_DNS4='192.0.2.53'\n") {
		t.Fatalf("dns split:\n%s", env)
	}

	static := "[network]\nmethod=static\naddress=192.0.2.10/24\naddress=2001:db8::10/64\ngateway=192.0.2.1\nipv6-only=false\n"
	if c, err = ParseKeyfile(strings.NewReader(static)); err != nil || len(c.Addrs) != 2 || len(c.Gateways) != 1 {
		t.Fatalf("static: %+v %v", c, err)
	}

	for name, bad := range map[string]string{
		"no method":                   "[network]\ninterface=eth0\n",
		"unknown section":             "[network]\nmethod=dhcp\n[vpn]\nkey=1\n",
		"unknown key":                 "[network]\nmethod=dhcp\ngatway=192.0.2.1\n",
		"outside section":             "method=dhcp\n",
		"not key=value":               "[network]\nmethod dhcp\n",
		"wifi without ssid":           "[network]\nmethod=wifi\n",
		"short psk":                   "[network]\nmethod=wifi\n[wifi]\nssid=x\npsk=short\n",
		"proxy with path":             "[network]\nmethod=dhcp\n[proxy]\nhttp=http://proxy.example.org:3128/x\n",
		"proxy scheme":                "[network]\nmethod=dhcp\n[proxy]\nhttp=socks5://proxy.example.org:1080\n",
		"hostname":                    "[network]\nmethod=dhcp\nhostname=Node_A\n",
		"bad bool":                    "[network]\nmethod=dhcp\nipv6-only=maybe\n",
		"v6only with an IPv4 address": "[network]\nmethod=static\nipv6-only=true\naddress=192.0.2.10/24\n",
		"dhcp with address":           "[network]\nmethod=dhcp\naddress=192.0.2.10/24\n",
	} {
		if _, err := ParseKeyfile(strings.NewReader(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestProxy(t *testing.T) {
	hp, err := ProxyHostPort("http://user:pw@[2001:db8::3]:3128")
	if err != nil || hp != "[2001:db8::3]:3128" {
		t.Fatalf("%q %v", hp, err)
	}
	if hp, _ := ProxyHostPort("https://proxy.example.org"); hp != "proxy.example.org:443" {
		t.Fatalf("default port: %q", hp)
	}
	if r := Redact("http://user:pw@proxy.example.org:3128"); strings.Contains(r, "pw") || !strings.Contains(r, "proxy.example.org:3128") {
		t.Fatalf("redact: %q", r)
	}
	if _, err := ProxyHostPort("http://user:pw@proxy.example.org:99999"); err == nil || strings.Contains(err.Error(), "pw") {
		t.Fatalf("bad port error must not leak the password: %v", err)
	}
}

func TestShQuote(t *testing.T) {
	for in, want := range map[string]string{
		"":          "''",
		"plain":     "'plain'",
		"a'b":       `'a'\''b'`,
		"$(reboot)": "'$(reboot)'",
	} {
		if got := ShQuote(in); got != want {
			t.Errorf("ShQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
