// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package lint

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/org-runink/river/pkg/pipe"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// firewall — the host firewall (inet runink_fw, iso-profiles/river) keeps the properties
// AGENTS.md invariant 7 promises for a workstation:
//
//   - input is `policy drop`, and the ruleset includes the common file;
//   - forward is `policy drop`, and only the forward_if set (/etc/runink/fw-forward) opens it;
//   - output is `policy accept` (a laptop keeps its LAN: replies come back as established);
//   - loopback, established/related, ICMPv6, DHCPv4 and DHCPv6 replies and mDNS are accepted;
//   - NO fixed port is open, SSH included (a port is opened on purpose: /etc/runink/fw-open);
//   - it never flushes the whole ruleset;
//   - runink-fw loads it before NetworkManager and sshd (their s6 dependencies), and the
//     service is enabled on the live medium;
//   - the LAN-install pairing ports: see below.
//
// With nft and unprivileged user namespaces available (a workstation), the ruleset is also
// LOADED into a throwaway network namespace; in the Tier 1 container that part is skipped and
// says so. Every other check runs everywhere and fails on a missing input.
//
// LAN-install pairing (docs/INSTALL.md, "LAN installs"): the live medium's firewall must let
// the pairing ports in, from IPv6 link-local sources only, and an installed machine must not.
//
//   - fw-pair (live overlay) lists exactly installer/internal/pair/protocol.go's ports:
//     udp BeaconPort, tcp PairPort, tcp SSHPort;
//   - the only rules using the pair sets are the two fe80::/10 ones, and the sets are empty
//     in the ruleset (filled at runtime only);
//   - fw-pair is NOT in any root-overlay, every 20-clone-rootfs copy excludes its directory,
//     and runink-fw seeds the sets only behind its live-medium check.
//
// Problems go to standard output, as the script printed them.

func init() { extraLints = append(extraLints, firewallCmd) }

func firewallCmd(v *viper.Viper) *cobra.Command {
	return &cobra.Command{
		Use:   "firewall",
		Short: "the host firewall is default-deny, opens no fixed port and keeps the pairing ports live-only",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return Firewall(cmd.Context(), v.GetString("repo"), cmd.ErrOrStderr(), cmd.OutOrStdout())
		},
	}
}

const (
	fwProfile = "iso-profiles/river"
	fwOverlay = fwProfile + "/root-overlay"
	fwNet     = fwOverlay + "/usr/local/lib/runink-net"
	fwCommon  = fwNet + "/runink-fw-common.nft"
	fwBase    = fwNet + "/runink-firewall-base.nft"
	fwTool    = fwOverlay + "/usr/local/bin/runink-fw"
	fwPairF   = fwProfile + "/live-overlay/usr/local/lib/river-pair/fw-pair"
	fwProto   = "installer/internal/pair/protocol.go"
)

// Firewall runs the lint over repo. Its problems and its OK line go to outw; errw receives
// what nft itself prints to stderr.
func Firewall(ctx context.Context, repo string, errw, outw io.Writer) error {
	r := &report{name: "lint-firewall", w: outw}
	path := func(p string) string { return filepath.Join(repo, p) }
	read := func(p string) string { b, _ := os.ReadFile(path(p)); return string(b) }
	exists := func(p string) bool { _, err := os.Stat(path(p)); return err == nil }
	nonEmpty := func(p string) bool { st, err := os.Stat(path(p)); return err == nil && st.Size() > 0 }

	for _, f := range []string{fwCommon, fwBase, fwTool, fwProfile + "/profile.yaml"} {
		if !nonEmpty(f) {
			r.fail("%s missing or empty (nothing to check against)", f)
			return r.err()
		}
	}
	common, base := read(fwCommon), read(fwBase)
	commonLines, baseLines := strings.Split(common, "\n"), strings.Split(base, "\n")

	// rule TEXT: TEXT is a whole (tab-indented) line of the common file.
	rule := func(text string) {
		if !slices.Contains(commonLines, "\t\t"+text) {
			r.fail("no '%s' in %s", text, fwCommon)
		}
	}

	if !strings.Contains(base, `include "/usr/local/lib/runink-net/runink-fw-common.nft"`) {
		r.fail("%s does not include the common file", fwBase)
	}
	if !strings.Contains(base, "hook input priority filter; policy drop;") {
		r.fail("input is not policy drop")
	}
	if !strings.Contains(common, "hook forward priority filter; policy drop;") {
		r.fail("forward is not policy drop")
	}
	if strings.Contains(base, "hook forward") {
		r.fail("the ruleset defines a second forward chain (it belongs in the common file)")
	}
	if !strings.Contains(common, "hook output priority filter; policy accept;") {
		r.fail("output is not policy accept (a laptop would lose its LAN)")
	}
	commentLine := regexp.MustCompile(`^[ \t\v\f\r]*#`)
	for _, l := range append(slices.Clone(baseLines), commonLines...) {
		if !commentLine.MatchString(l) && strings.Contains(l, "flush ruleset") {
			r.fail("the firewall flushes the whole ruleset (only inet runink_fw may be replaced)")
			break
		}
	}
	rule(`iif "lo" accept`)
	rule(`ct state established,related accept`)
	rule(`meta l4proto ipv6-icmp accept`)
	rule(`udp sport 67 udp dport 68 accept`)
	rule(`udp dport 546 accept`)
	rule(`udp dport 5353 accept`)

	fixedPort := regexp.MustCompile(`dport [0-9]+ accept`)
	var fixed []string
	for _, l := range append(slices.Clone(baseLines), commonLines...) {
		if fixedPort.MatchString(l) &&
			!strings.Contains(l, "udp sport 67 udp dport 68") &&
			!strings.Contains(l, "udp dport 546 accept") &&
			!strings.Contains(l, "udp dport 5353 accept") {
			fixed = append(fixed, l)
		}
	}
	if len(fixed) > 0 {
		r.fail("a fixed port is open by default (open ports on purpose in /etc/runink/fw-open): %s", strings.Join(fixed, "\n"))
	}

	// Forward: only the listed interfaces, both directions, nothing by address.
	var fwd []string
	on := false
	for _, l := range commonLines {
		if strings.Contains(l, "chain forward") {
			on = true
		}
		if on && strings.Contains(l, "accept") {
			fwd = append(fwd, strings.TrimLeft(l, " \t\v\f\r"))
		}
		if on && strings.HasPrefix(l, "\t}") {
			break
		}
	}
	wantFwd := []string{"ct state established,related accept", "iifname @forward_if accept", "oifname @forward_if accept"}
	if !slices.Equal(fwd, wantFwd) {
		r.fail("the forward chain accepts more than established traffic and the forward_if interfaces: %s", strings.Join(fwd, ";"))
	}
	if anyLineMatches(commonLines, `set forward_if \{[^}]*elements`) {
		r.fail("forward_if has baked-in elements (it is seeded from /etc/runink/fw-forward)")
	}

	// Loaded before the network comes up, and enabled.
	for _, s := range []string{"NetworkManager-srv", "sshd-srv"} {
		if !exists(fwOverlay + "/etc/s6/sv/" + s + "/dependencies.d/runink-fw") {
			r.fail("%s does not depend on runink-fw (the network would come up unfiltered)", s)
		}
	}
	if !exists(fwOverlay + "/etc/s6/adminsv/rc-local/dependencies.d/runink-fw") {
		r.fail("rc-local does not depend on runink-fw (what rc.local starts would come up unfiltered)")
	}
	if !strings.Contains(read(fwOverlay+"/etc/s6/sv/runink-fw/up"), "runink-fw apply") {
		r.fail("the runink-fw oneshot does not run 'runink-fw apply'")
	}
	if !slices.Contains(strings.Split(read(fwProfile+"/profile.yaml"), "\n"), "    - runink-fw") {
		r.fail("runink-fw is not among profile.yaml's live-session services")
	}

	// LAN-install pairing.
	for _, f := range []string{fwPairF, fwProto} {
		if !nonEmpty(f) {
			r.fail("%s missing or empty (nothing to check the pairing ports against)", f)
			return r.err()
		}
	}
	proto := read(fwProto)
	gport := func(name string) string {
		m := regexp.MustCompile(`(?m)^[ \t]*` + name + `[ \t]*=[ \t]*([0-9]+)`).FindStringSubmatch(proto)
		if m == nil {
			return ""
		}
		return m[1]
	}
	bp, pp, sp := gport("BeaconPort"), gport("PairPort"), gport("SSHPort")
	if bp == "" || pp == "" || sp == "" {
		r.fail("BeaconPort/PairPort/SSHPort not found in %s", fwProto)
	}
	want := []string{"udp " + bp, "tcp " + pp, "tcp " + sp}
	var got []string
	for _, l := range strings.Split(read(fwPairF), "\n") {
		code, _, _ := strings.Cut(l, "#")
		// awk 'NF { print $1, $2 }': a one-field line prints as "x ".
		switch f := strings.Fields(code); len(f) {
		case 0:
		case 1:
			got = append(got, f[0]+" ")
		default:
			got = append(got, f[0]+" "+f[1])
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	wantP, gotP := strings.Join(want, ";")+";", strings.Join(got, ";")+";"
	if len(got) == 0 {
		gotP = ""
	}
	if wantP != gotP {
		r.fail("fw-pair lists { %s} but protocol.go says { %s}", gotP, wantP)
	}
	uses := 0
	for _, l := range commonLines {
		if strings.Contains(l, "@pair_") {
			uses++
		}
	}
	if uses != 2 {
		r.fail("the pair sets are used by %d rules in %s (want exactly the two link-local ones)", uses, fwCommon)
	}
	rule(`ip6 saddr fe80::/10 tcp dport @pair_tcp accept`)
	rule(`ip6 saddr fe80::/10 udp dport @pair_udp accept`)
	if strings.Contains(base, "@pair_") {
		r.fail("the ruleset uses the pair sets itself (they belong to the common file's link-local rules)")
	}
	if anyLineMatches(commonLines, `set pair_(tcp|udp) \{[^}]*elements`) {
		r.fail("a pair set has baked-in elements (they are filled on the live medium only)")
	}
	overlays, _ := filepath.Glob(path("iso-profiles/*/root-overlay"))
	for _, p := range overlays {
		if _, err := os.Stat(filepath.Join(p, "usr/local/lib/river-pair/fw-pair")); err == nil {
			rel, _ := filepath.Rel(repo, p)
			r.fail("%s carries fw-pair: an installed machine would open the pairing ports", rel)
		}
	}
	clones := 0
	cloneCopies, _ := filepath.Glob(path("iso-profiles/*/root-overlay/usr/local/lib/runink-install/20-clone-rootfs.sh"))
	for _, c := range append([]string{path("installer/lib/20-clone-rootfs.sh")}, cloneCopies...) {
		st, err := os.Stat(c)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		clones++
		b, _ := os.ReadFile(c)
		if !strings.Contains(string(b), "--exclude='/usr/local/lib/river-pair'") {
			rel, _ := filepath.Rel(repo, c)
			r.fail("%s does not exclude /usr/local/lib/river-pair from the clone", rel)
		}
	}
	if clones == 0 {
		r.fail("no 20-clone-rootfs.sh found")
	}
	if !seedPairChecksLive(read(fwTool)) {
		r.fail("runink-fw's seed_pair does not check is_live before it adds to the pair sets")
	}

	loaded := "skipped"
	if fwLoadable(ctx) {
		loaded = "yes"
		if err := fwLoad(ctx, repo, bp, pp, sp, r, errw, outw); err != nil {
			return fmt.Errorf("lint-firewall: %w", err)
		}
	}

	if err := r.err(); err != nil {
		return err
	}
	fmt.Fprintf(outw, "lint-firewall: default-deny input (no fixed port, SSH closed), forward default-drop but forward_if, output open, loaded before NetworkManager and sshd; LAN-install pairing ports (%s/udp %s,%s/tcp) link-local and live-only; ruleset loaded: %s ✓\n", bp, pp, sp, loaded)
	return nil
}

func anyLineMatches(lines []string, re string) bool {
	rx := regexp.MustCompile(re)
	return slices.ContainsFunc(lines, rx.MatchString)
}

// seedPairChecksLive is the script's awk over runink-fw: inside seed_pair() (from its
// opening line to the first line starting with '}'), the last line naming is_live comes
// before the last line naming pair_$proto, and both exist.
func seedPairChecksLive(tool string) bool {
	g, s, on := 0, 0, false
	for i, l := range strings.Split(tool, "\n") {
		n := i + 1
		if strings.HasPrefix(l, "seed_pair() {") {
			on = true
		}
		if !on {
			continue
		}
		if strings.Contains(l, "is_live") {
			g = n
		}
		if strings.Contains(l, "pair_$proto") {
			s = n
		}
		if strings.HasPrefix(l, "}") {
			break
		}
	}
	return g > 0 && s > 0 && g < s
}

// fwLoadable is fwCanLoad, a variable so a test can run the static checks alone.
var fwLoadable = fwCanLoad

// fwCanLoad: nft and unshare are installed and an unprivileged user+network namespace can
// be made (`unshare -rn true`).
func fwCanLoad(ctx context.Context) bool {
	if _, err := exec.LookPath("nft"); err != nil {
		return false
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		return false
	}
	return pipe.Run(ctx, pipe.IO{}, pipe.Cmd("unshare", "-rn", "true")) == nil
}

// fwLoad loads the ruleset into a throwaway network namespace, from a copy whose include
// points at a scratch directory; then loads it again with its sets filled the way runink-fw
// fills them (an open port, a forwarded interface, the live medium's pairing ports).
func fwLoad(ctx context.Context, repo, bp, pp, sp string, r *report, errw, outw io.Writer) error {
	tmp, err := os.MkdirTemp("", "lint-firewall-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, f := range []string{fwCommon, fwBase} {
		b, err := os.ReadFile(filepath.Join(repo, f))
		if err != nil {
			return err
		}
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			lines[i] = strings.Replace(l, "/usr/local/lib/runink-net", tmp, 1)
		}
		if err := os.WriteFile(filepath.Join(tmp, filepath.Base(f)), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
			return err
		}
	}
	baseNft := filepath.Join(tmp, "runink-firewall-base.nft")
	stdio := pipe.IO{Stdout: outw, Stderr: errw}
	if pipe.Run(ctx, stdio, pipe.Cmd("unshare", "-rn", "nft", "-f", baseNft)) != nil {
		r.fail("the ruleset does not load")
	}
	// The script ran `nft -f` and then each `nft add element` in one namespace through
	// `sh -c`; one nft file does the same in one transaction, with no shell.
	filled := fmt.Sprintf("include %q\n"+
		"add element inet runink_fw open_tcp { 22 }\n"+
		"add element inet runink_fw forward_if { \"virbr0\" }\n"+
		"add element inet runink_fw pair_udp { %s }\n"+
		"add element inet runink_fw pair_tcp { %s, %s }\n", baseNft, bp, pp, sp)
	filledNft := filepath.Join(tmp, "filled.nft")
	if err := os.WriteFile(filledNft, []byte(filled), 0o600); err != nil {
		return err
	}
	if pipe.Run(ctx, stdio, pipe.Cmd("unshare", "-rn", "nft", "-f", filledNft)) != nil {
		r.fail("the ruleset does not take its sets (open ports, forward interfaces, pairing ports)")
	}
	return nil
}
