// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package netstate

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type fake struct {
	mu     sync.Mutex
	links  []Link
	routes []Route
	dns    []string
	dial   map[string]error // addr -> result; missing = timeout
	lookup []netip.Addr
	lkErr  error
	neigh  map[string]bool
	dialed []string
}

func (f *fake) env() Env {
	return Env{
		Links:     func() ([]Link, error) { return f.links, nil },
		Routes:    func() ([]Route, error) { return f.routes, nil },
		Resolvers: func() []string { return f.dns },
		Hostname:  func() string { return "runink" },
		Dial: func(ctx context.Context, network, addr string) error {
			f.mu.Lock()
			f.dialed = append(f.dialed, network+" "+addr)
			err, ok := f.dial[addr]
			f.mu.Unlock()
			if ok {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		},
		Lookup: func(ctx context.Context, host string) ([]netip.Addr, error) { return f.lookup, f.lkErr },
		Neighbour: func(a netip.Addr, iface string) bool {
			return f.neigh[a.String()]
		},
		Now: func() time.Time { return time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC) },
	}
}

// The qemu user-mode network, as the live system sees it.
func slirp() *fake {
	return &fake{
		links: []Link{{Name: "eth0", Up: true, Carrier: true,
			Addrs: []string{"10.0.2.15/24", "fec0::5054:ff:fe12:3456/64", "fe80::5054:ff:fe12:3456/64"}}},
		routes: []Route{{Family: "ipv4", Iface: "eth0", Gateway: "10.0.2.2"}, {Family: "ipv6", Iface: "eth0", Gateway: "fe80::2"}},
		dns:    []string{"10.0.2.3"},
		dial:   map[string]error{"10.0.2.2:443": syscall.ECONNREFUSED},
		neigh:  map[string]bool{"fe80::2": true},
	}
}

func TestRunOutcomes(t *testing.T) {
	ms := 50 * time.Millisecond
	t.Run("offline: no interface at all, nothing dialed, no wait", func(t *testing.T) {
		f := &fake{}
		start := time.Now()
		s := Run(context.Background(), f.env(), Options{Method: "dhcp", Timeout: time.Second})
		if s.State != Offline {
			t.Fatalf("state %s", s.State)
		}
		if el := time.Since(start); el > 200*time.Millisecond {
			t.Fatalf("an offline check took %v; it must not wait on anything", el)
		}
		if len(f.dialed) != 0 {
			t.Fatalf("dialed %v with no route", f.dialed)
		}
		if !s.Checks.Gateway4.Skipped || !s.Checks.Internet6.Skipped || s.Checks.DNS.OK {
			t.Fatalf("%+v", s.Checks)
		}
	})
	t.Run("lan-only: gateways answer, the internet does not", func(t *testing.T) {
		f := slirp()
		f.lkErr = errors.New("lookup kernel.org: i/o timeout")
		s := Run(context.Background(), f.env(), Options{Timeout: ms})
		if s.State != LANOnly || !s.Checks.Gateway4.OK || !s.Checks.Gateway6.OK {
			t.Fatalf("%s %+v", s.State, s.Checks)
		}
		if !strings.Contains(s.Checks.Gateway4.Detail, "reset") || !strings.Contains(s.Checks.Gateway6.Detail, "neighbour") {
			t.Fatalf("gateway evidence: %+v", s.Checks)
		}
		if s.Checks.Gateway6.Target != "fe80::2%eth0" {
			t.Fatalf("a link-local gateway needs its zone: %q", s.Checks.Gateway6.Target)
		}
	})
	t.Run("online dual-stack: the default host setup", func(t *testing.T) {
		f := slirp()
		f.lookup = []netip.Addr{netip.MustParseAddr("192.0.2.80"), netip.MustParseAddr("2001:db8::80")}
		f.dial["192.0.2.80:443"] = nil
		f.dial["[2001:db8::80]:443"] = nil
		s := Run(context.Background(), f.env(), Options{Method: "dhcp", Timeout: ms})
		c := s.Checks
		if s.State != Online || !c.Gateway4.OK || !c.Gateway6.OK || !c.DNS.OK || !c.Internet4.OK || !c.Internet6.OK {
			t.Fatalf("%s %+v", s.State, c)
		}
	})
	t.Run("online over IPv6 only", func(t *testing.T) {
		f := slirp()
		f.lookup = []netip.Addr{netip.MustParseAddr("192.0.2.80"), netip.MustParseAddr("2001:db8::80")}
		f.dial["[2001:db8::80]:443"] = nil
		s := Run(context.Background(), f.env(), Options{Timeout: ms})
		if s.State != Online || !s.Checks.Internet6.OK || s.Checks.Internet4.OK {
			t.Fatalf("%s %+v", s.State, s.Checks)
		}
	})
	t.Run("online through a proxy", func(t *testing.T) {
		f := slirp()
		f.lkErr = errors.New("no such host")
		f.dial["[2001:db8::3]:3128"] = nil
		s := Run(context.Background(), f.env(), Options{Timeout: ms, ProxyAddr: "[2001:db8::3]:3128", ProxyShow: "http://REDACTED@[2001:db8::3]:3128"})
		if s.State != Online || !s.Checks.Proxy.OK {
			t.Fatalf("%s %+v", s.State, s.Checks)
		}
	})
	t.Run("link up with a routable address but no route: lan-only", func(t *testing.T) {
		f := &fake{links: []Link{{Name: "eth0", Up: true, Carrier: true, Addrs: []string{"2001:db8::10/64"}}}}
		if s := Run(context.Background(), f.env(), Options{Timeout: ms}); s.State != LANOnly {
			t.Fatalf("%s", s.State)
		}
	})
	t.Run("link-local only is offline", func(t *testing.T) {
		f := &fake{links: []Link{{Name: "eth0", Up: true, Carrier: true, Addrs: []string{"fe80::1/64"}}}}
		if s := Run(context.Background(), f.env(), Options{Timeout: ms}); s.State != Offline {
			t.Fatalf("%s", s.State)
		}
	})
	t.Run("check host none disables the internet probes", func(t *testing.T) {
		f := slirp()
		s := Run(context.Background(), f.env(), Options{Timeout: ms, ProbeHost: "none"})
		if s.State != LANOnly || !s.Checks.Internet4.Skipped || !s.Checks.DNS.Skipped {
			t.Fatalf("%s %+v", s.State, s.Checks)
		}
		for _, d := range f.dialed {
			if !strings.Contains(d, "10.0.2.2") && !strings.Contains(d, "fe80::2") {
				t.Fatalf("dialed %s with the internet check disabled", d)
			}
		}
	})
}

func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run", "river")
	path := filepath.Join(dir, "net-state.json")
	envPath := filepath.Join(dir, "net.env")
	f := slirp()
	f.lookup = []netip.Addr{netip.MustParseAddr("192.0.2.80")}
	f.dial["192.0.2.80:443"] = nil
	s := Run(context.Background(), f.env(), Options{Method: "dhcp", Timeout: 50 * time.Millisecond, ProxyShow: "http://REDACTED@proxy.example.org:3128"})
	if err := Write(path, envPath, s, "node-a"); err != nil {
		t.Fatal(err)
	}
	// And again: the rename must replace, not fail.
	if err := Write(path, envPath, s, "node-a"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, envPath} {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != 0o644 {
			t.Fatalf("%s: %v %v", p, st.Mode(), err)
		}
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 {
		t.Fatalf("temp files left behind: %v", ents)
	}
	raw, _ := os.ReadFile(path)
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"schema": Schema, "state": Online, "method": "dhcp",
		"checked_at": "2026-09-25T12:00:00Z", "probe_host": DefaultProbeHost, "probe_port": 443.0} {
		if back[k] != want {
			t.Errorf("%s = %v, want %v", k, back[k], want)
		}
	}
	checks := back["checks"].(map[string]any)
	for _, k := range []string{"gateway4", "gateway6", "dns", "internet4", "internet6", "proxy"} {
		if _, ok := checks[k]; !ok {
			t.Errorf("checks.%s missing", k)
		}
	}
	if strings.Contains(string(raw), "pw") {
		t.Fatal("proxy credentials reached the state file")
	}
	env, _ := os.ReadFile(envPath)
	for _, want := range []string{"RIVER_NET_STATE='online'", "RIVER_NET_METHOD='dhcp'", "RIVER_NET_INTERNET4='1'",
		"RIVER_NET_INTERNET6='0'", "RIVER_NET_HOSTNAME='node-a'", "RIVER_NET_STATE_FILE='" + path + "'"} {
		if !strings.Contains(string(env), want+"\n") {
			t.Errorf("net.env lacks %s:\n%s", want, env)
		}
	}
	var rep strings.Builder
	Report(&rep, s)
	if !strings.Contains(rep.String(), "RESULT: online") || !strings.Contains(rep.String(), "route      IPv4 default via 10.0.2.2 dev eth0") {
		t.Fatalf("report:\n%s", rep.String())
	}
}

func TestWriteUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0o555)
	defer os.Chmod(dir, 0o755)
	if err := Write(filepath.Join(dir, "net-state.json"), "", State{Schema: Schema}, ""); err == nil {
		t.Fatal("wrote into a read-only directory")
	}
}

func TestParseRoutes(t *testing.T) {
	v4 := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0202000A	0003	0	0	100	00000000	0	0	0
eth0	0002000A	00000000	0001	0	0	100	00FFFFFF	0	0	0
`
	r := ParseRoute4(strings.NewReader(v4))
	if len(r) != 1 || r[0].Gateway != "10.0.2.2" || r[0].Iface != "eth0" {
		t.Fatalf("%+v", r)
	}
	v6 := `00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000002 00000400 00000001 00000000 00450003     eth0
fec00000000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001     eth0
00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200       lo
`
	r = ParseRoute6(strings.NewReader(v6))
	if len(r) != 1 || r[0].Gateway != "fe80::2" || r[0].Family != "ipv6" {
		t.Fatalf("%+v", r)
	}
}

func TestNdaAddr(t *testing.T) {
	// NDA_CACHEINFO-like padding attr, then NDA_DST (IPv4, 8 bytes).
	b := []byte{8, 0, 3, 0, 0, 0, 0, 0, 8, 0, 1, 0, 10, 0, 2, 2}
	a, ok := ndaAddr(b)
	if !ok || a.String() != "10.0.2.2" {
		t.Fatalf("%v %v", a, ok)
	}
	if _, ok := ndaAddr([]byte{2, 0}); ok {
		t.Fatal("truncated attribute accepted")
	}
}
