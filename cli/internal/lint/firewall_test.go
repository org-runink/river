// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fwFixtureBase = `#!/usr/sbin/nft -f
# the host firewall
table inet runink_fw
delete table inet runink_fw

table inet runink_fw {
	include "/usr/local/lib/runink-net/runink-fw-common.nft"

	chain input {
		type filter hook input priority filter; policy drop;
		jump common_in
	}
}
`

const fwFixtureCommon = `# the shared part of inet runink_fw
	set open_tcp { type inet_service; }
	set open_udp { type inet_service; }
	set pair_tcp { type inet_service; }
	set pair_udp { type inet_service; }
	set forward_if { type ifname; }

	chain common_in {
		iif "lo" accept
		ct state established,related accept
		ct state invalid drop
		meta l4proto ipv6-icmp accept
		udp sport 67 udp dport 68 accept
		udp dport 546 accept
		udp dport 5353 accept
		iifname @forward_if accept
		tcp dport @open_tcp accept
		udp dport @open_udp accept
		ip6 saddr fe80::/10 tcp dport @pair_tcp accept
		ip6 saddr fe80::/10 udp dport @pair_udp accept
	}

	chain forward {
		type filter hook forward priority filter; policy drop;
		ct state established,related accept
		ct state invalid drop
		iifname @forward_if accept
		oifname @forward_if accept
	}

	chain output {
		type filter hook output priority filter; policy accept;
	}
`

const fwFixtureTool = `#!/bin/sh
seed_pair() {
	[ -f "$PAIR" ] || return 0
	if ! is_live; then
		return 0
	fi
	lines "$PAIR" | while read -r proto port _rest; do
		nft add element $TABLE "pair_$proto" "{ $port }"
	done
}
`

func firewallFixture(t *testing.T) string {
	t.Helper()
	dir := fixtureRepo(t)
	writeTree(t, dir, map[string]string{
		fwBase:   fwFixtureBase,
		fwCommon: fwFixtureCommon,
		fwTool:   fwFixtureTool,
		fwOverlay + "/etc/s6/sv/NetworkManager-srv/dependencies.d/runink-fw": "",
		fwOverlay + "/etc/s6/sv/sshd-srv/dependencies.d/runink-fw":           "",
		fwOverlay + "/etc/s6/adminsv/rc-local/dependencies.d/runink-fw":      "",
		fwOverlay + "/etc/s6/sv/runink-fw/up":                                "runink-fw apply\n",
		fwProfile + "/profile.yaml":                                          "live:\n  services:\n    - runink-fw\n",
		fwPairF:                                                              "# ports\nudp 47653\t# beacon\ntcp 47654\ntcp 47655 # ssh\n",
		fwProto:                                                              "package pair\n\nconst (\n\tBeaconPort  = 47653 // UDP\n\tPairPort    = 47654\n\tSSHPort     = 47655\n)\n",
		"installer/lib/20-clone-rootfs.sh":                                   "rsync --exclude='/usr/local/lib/river-pair' / /mnt\n",
		fwOverlay + "/usr/local/lib/runink-install/20-clone-rootfs.sh":       "rsync --exclude='/usr/local/lib/river-pair' / /mnt\n",
	})
	return dir
}

// staticOnly runs the lint without loading the ruleset.
func staticOnly(t *testing.T) {
	t.Helper()
	saved := fwLoadable
	t.Cleanup(func() { fwLoadable = saved })
	fwLoadable = func(context.Context) bool { return false }
}

func runFirewall(t *testing.T, dir string) (string, error) {
	t.Helper()
	var errb, outb bytes.Buffer
	err := Firewall(context.Background(), dir, &errb, &outb)
	return outb.String(), err
}

func TestFirewallClean(t *testing.T) {
	staticOnly(t)
	out, err := runFirewall(t, firewallFixture(t))
	if err != nil {
		t.Fatalf("clean tree failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "pairing ports (47653/udp 47654,47655/tcp) link-local and live-only; ruleset loaded: skipped ✓") {
		t.Fatalf("unexpected OK line: %q", out)
	}
}

// With nft and user namespaces (a workstation, not the Tier 1 container), the fixture
// ruleset loads, with and without its sets filled; a broken one does not.
func TestFirewallLoads(t *testing.T) {
	if !fwCanLoad(context.Background()) {
		t.Skip("nft or unprivileged user namespaces unavailable")
	}
	dir := firewallFixture(t)
	out, err := runFirewall(t, dir)
	if err != nil || !strings.Contains(out, "ruleset loaded: yes ✓") {
		t.Fatalf("fixture ruleset did not load: %v\n%s", err, out)
	}
	editFixture(t, dir, fwCommon, "set pair_udp { type inet_service; }\n", "")
	editFixture(t, dir, fwCommon, "ip6 saddr fe80::/10 udp dport @pair_udp accept", "ip6 saddr fe80::/10 udp dport @pair_tcp accept")
	out, err = runFirewall(t, dir)
	if err == nil || !strings.Contains(out, "lint-firewall: the ruleset does not take its sets") {
		t.Fatalf("a ruleset without pair_udp took its sets: %v\n%s", err, out)
	}
}

func TestFirewallFailures(t *testing.T) {
	staticOnly(t)
	cases := []struct {
		name   string
		break_ func(t *testing.T, dir string)
		want   string
	}{
		{"common not included", func(t *testing.T, d string) {
			editFixture(t, d, fwBase, `include "/usr/local/lib/runink-net/runink-fw-common.nft"`, "")
		}, "does not include the common file"},
		{"input accept", func(t *testing.T, d string) {
			editFixture(t, d, fwBase, "policy drop;", "policy accept;")
		}, "input is not policy drop"},
		{"forward accept", func(t *testing.T, d string) {
			editFixture(t, d, fwCommon, "hook forward priority filter; policy drop;", "hook forward priority filter; policy accept;")
		}, "forward is not policy drop"},
		{"second forward chain", func(t *testing.T, d string) {
			editFixture(t, d, fwBase, "jump common_in\n", "jump common_in\n\t}\n\tchain fwd2 {\n\t\ttype filter hook forward priority 0;\n")
		}, "defines a second forward chain"},
		{"output drop", func(t *testing.T, d string) {
			editFixture(t, d, fwCommon, "policy accept;", "policy drop;")
		}, "output is not policy accept"},
		{"flush ruleset", func(t *testing.T, d string) {
			editFixture(t, d, fwBase, "table inet runink_fw\ndelete", "flush ruleset\ntable inet runink_fw\ndelete")
		}, "flushes the whole ruleset"},
		{"mDNS dropped", func(t *testing.T, d string) {
			editFixture(t, d, fwCommon, "\t\tudp dport 5353 accept\n", "")
		}, "no 'udp dport 5353 accept' in " + fwCommon},
		{"ssh open", func(t *testing.T, d string) {
			editFixture(t, d, fwCommon, "\t\tudp dport 5353 accept\n", "\t\tudp dport 5353 accept\n\t\ttcp dport 22 accept\n")
		}, "a fixed port is open by default (open ports on purpose in /etc/runink/fw-open): \t\ttcp dport 22 accept"},
		{"forward by address", func(t *testing.T, d string) {
			editFixture(t, d, fwCommon, "\t\toifname @forward_if accept\n", "\t\toifname @forward_if accept\n\t\tip saddr 192.0.2.0/24 accept\n")
		}, "the forward chain accepts more than established traffic and the forward_if interfaces: ct state established,related accept;iifname @forward_if accept;oifname @forward_if accept;ip saddr 192.0.2.0/24 accept"},
		{"forward_if baked in", func(t *testing.T, d string) {
			editFixture(t, d, fwCommon, "set forward_if { type ifname; }", `set forward_if { type ifname; elements = { "br0" } }`)
		}, "forward_if has baked-in elements"},
		{"sshd before the firewall", func(t *testing.T, d string) {
			if err := os.Remove(filepath.Join(d, fwOverlay+"/etc/s6/sv/sshd-srv/dependencies.d/runink-fw")); err != nil {
				t.Fatal(err)
			}
		}, "sshd-srv does not depend on runink-fw"},
		{"rc-local before the firewall", func(t *testing.T, d string) {
			if err := os.Remove(filepath.Join(d, fwOverlay+"/etc/s6/adminsv/rc-local/dependencies.d/runink-fw")); err != nil {
				t.Fatal(err)
			}
		}, "rc-local does not depend on runink-fw"},
		{"oneshot does nothing", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{fwOverlay + "/etc/s6/sv/runink-fw/up": "true\n"})
		}, "the runink-fw oneshot does not run 'runink-fw apply'"},
		{"not enabled", func(t *testing.T, d string) {
			editFixture(t, d, fwProfile+"/profile.yaml", "    - runink-fw", "    - other")
		}, "runink-fw is not among profile.yaml's live-session services"},
		{"missing input", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{fwTool: ""})
		}, fwTool + " missing or empty (nothing to check against)"},
		{"missing pairing input", func(t *testing.T, d string) {
			if err := os.Remove(filepath.Join(d, fwProto)); err != nil {
				t.Fatal(err)
			}
		}, fwProto + " missing or empty (nothing to check the pairing ports against)"},
		{"ports not in protocol.go", func(t *testing.T, d string) {
			editFixture(t, d, fwProto, "SSHPort", "SshPort")
		}, "BeaconPort/PairPort/SSHPort not found in " + fwProto},
		{"fw-pair drifted", func(t *testing.T, d string) {
			editFixture(t, d, fwPairF, "tcp 47655", "tcp 22")
		}, "fw-pair lists { tcp 22;tcp 47654;udp 47653;} but protocol.go says { tcp 47654;tcp 47655;udp 47653;}"},
		{"pair set used elsewhere", func(t *testing.T, d string) {
			editFixture(t, d, fwCommon, "\t\ttcp dport @open_tcp accept\n", "\t\ttcp dport @open_tcp accept\n\t\ttcp dport @pair_tcp accept\n")
		}, "the pair sets are used by 3 rules"},
		{"pair rule not link-local", func(t *testing.T, d string) {
			editFixture(t, d, fwCommon, "ip6 saddr fe80::/10 tcp dport @pair_tcp", "tcp dport @pair_tcp")
		}, "no 'ip6 saddr fe80::/10 tcp dport @pair_tcp accept'"},
		{"base uses a pair set", func(t *testing.T, d string) {
			editFixture(t, d, fwBase, "jump common_in\n", "jump common_in\n\t\ttcp dport @pair_tcp drop\n")
		}, "the ruleset uses the pair sets itself"},
		{"pair set baked in", func(t *testing.T, d string) {
			editFixture(t, d, fwCommon, "set pair_tcp { type inet_service; }", "set pair_tcp { type inet_service; elements = { 47654 } }")
		}, "a pair set has baked-in elements"},
		{"fw-pair installed", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{fwOverlay + "/usr/local/lib/river-pair/fw-pair": "tcp 47654\n"})
		}, fwOverlay + " carries fw-pair"},
		{"clone copies fw-pair", func(t *testing.T, d string) {
			writeTree(t, d, map[string]string{"installer/lib/20-clone-rootfs.sh": "rsync / /mnt\n"})
		}, "installer/lib/20-clone-rootfs.sh does not exclude /usr/local/lib/river-pair from the clone"},
		{"no clone step", func(t *testing.T, d string) {
			for _, f := range []string{"installer/lib/20-clone-rootfs.sh", fwOverlay + "/usr/local/lib/runink-install/20-clone-rootfs.sh"} {
				if err := os.Remove(filepath.Join(d, f)); err != nil {
					t.Fatal(err)
				}
			}
		}, "no 20-clone-rootfs.sh found"},
		{"seeded before the live check", func(t *testing.T, d string) {
			editFixture(t, d, fwTool, "\tif ! is_live; then\n\t\treturn 0\n\tfi\n", "")
		}, "runink-fw's seed_pair does not check is_live"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := firewallFixture(t)
			c.break_(t, dir)
			out, err := runFirewall(t, dir)
			if err == nil {
				t.Fatalf("passed:\n%s", out)
			}
			if !strings.Contains(out, "lint-firewall: ") || !strings.Contains(out, c.want) {
				t.Fatalf("want %q in:\n%s", c.want, out)
			}
		})
	}
}
