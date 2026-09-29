// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package netstate

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// System reads the running machine: net.Interfaces, /proc/net/{route,ipv6_route},
// /etc/resolv.conf, the kernel neighbour table (netlink, read-only) and real TCP dials.
// Nothing here needs root.
func System(root string) Env {
	if root == "" {
		root = "/"
	}
	p := func(rel string) string { return filepath.Join(root, rel) }
	return Env{
		Links:     func() ([]Link, error) { return sysLinks(p("sys/class/net")) },
		Routes:    func() ([]Route, error) { return sysRoutes(p("proc/net/route"), p("proc/net/ipv6_route")) },
		Resolvers: func() []string { return Resolvers(p("etc/resolv.conf")) },
		Hostname: func() string {
			h, _ := os.Hostname()
			return h
		},
		Dial: func(ctx context.Context, network, addr string) error {
			var d net.Dialer
			c, err := d.DialContext(ctx, network, addr)
			if err == nil {
				_ = c.Close()
			}
			return err
		},
		Lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			r := &net.Resolver{PreferGo: true}
			ips, err := r.LookupNetIP(ctx, "ip", host)
			return ips, err
		},
		Neighbour: sysNeighbour,
		Now:       time.Now,
	}
}

func sysLinks(sysNet string) ([]Link, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Link
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		l := Link{Name: i.Name, Up: i.Flags&net.FlagUp != 0, Addrs: []string{}}
		if b, err := os.ReadFile(filepath.Join(sysNet, i.Name, "carrier")); err == nil {
			l.Carrier = strings.TrimSpace(string(b)) == "1"
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if pfx, err := netip.ParsePrefix(a.String()); err == nil {
				l.Addrs = append(l.Addrs, pfx.String())
			}
		}
		out = append(out, l)
	}
	sortLinks(out)
	return out, nil
}

// sysRoutes returns the default routes from the kernel's /proc tables.
func sysRoutes(v4, v6 string) ([]Route, error) {
	var out []Route
	if f, err := os.Open(v4); err == nil {
		out = append(out, ParseRoute4(f)...)
		_ = f.Close()
	}
	if f, err := os.Open(v6); err == nil {
		out = append(out, ParseRoute6(f)...)
		_ = f.Close()
	}
	return out, nil
}

const (
	rtfUp     = 0x0001
	rtfReject = 0x0200
)

// ParseRoute4 reads /proc/net/route: the default routes (destination and mask 0).
func ParseRoute4(r io.Reader) []Route {
	var out []Route
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 8 || f[0] == "Iface" || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, _ := strconv.ParseUint(f[3], 16, 32)
		if flags&rtfUp == 0 || flags&rtfReject != 0 {
			continue
		}
		rt := Route{Family: "ipv4", Iface: f[0]}
		if g, err := strconv.ParseUint(f[2], 16, 32); err == nil && g != 0 {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], uint32(g))
			rt.Gateway = netip.AddrFrom4(b).String()
		}
		out = append(out, rt)
	}
	return out
}

// ParseRoute6 reads /proc/net/ipv6_route: the default routes (::/0), minus the kernel's
// unreachable placeholders on lo.
func ParseRoute6(r io.Reader) []Route {
	var out []Route
	zero := strings.Repeat("0", 32)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || f[0] != zero || f[1] != "00" || f[9] == "lo" {
			continue
		}
		flags, _ := strconv.ParseUint(f[8], 16, 32)
		if flags&rtfUp == 0 || flags&rtfReject != 0 {
			continue
		}
		rt := Route{Family: "ipv6", Iface: f[9]}
		if f[4] != zero {
			if b, err := hex.DecodeString(f[4]); err == nil && len(b) == 16 {
				rt.Gateway = netip.AddrFrom16([16]byte(b)).String()
			}
		}
		out = append(out, rt)
	}
	return out
}

// Resolvers returns the nameserver lines of a resolv.conf.
func Resolvers(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return []string{}
	}
	defer f.Close()
	out := []string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) >= 2 && fl[0] == "nameserver" {
			out = append(out, fl[1])
		}
	}
	return out
}

// Neighbour states that mean the neighbour answered (include/uapi/linux/neighbour.h).
const (
	nudReachable = 0x02
	nudStale     = 0x04
	nudDelay     = 0x08
	nudProbe     = 0x10
	nudPermanent = 0x80
	ndaDst       = 1
	sizeofNdmsg  = 12
)

// sysNeighbour dumps the kernel neighbour table over netlink (read-only, unprivileged).
func sysNeighbour(addr netip.Addr, iface string) bool {
	fam := syscall.AF_INET
	if addr.Is6() && !addr.Is4In6() {
		fam = syscall.AF_INET6
	}
	idx := 0
	if i, err := net.InterfaceByName(iface); err == nil {
		idx = i.Index
	}
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETNEIGH, fam)
	if err != nil {
		return false
	}
	msgs, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return false
	}
	want := addr.WithZone("").Unmap()
	for _, m := range msgs {
		if m.Header.Type != syscall.RTM_NEWNEIGH || len(m.Data) < sizeofNdmsg {
			continue
		}
		ifindex := int(int32(binary.NativeEndian.Uint32(m.Data[4:8]))) // #nosec G115 -- ndm_ifindex is a C int
		state := binary.NativeEndian.Uint16(m.Data[8:10])
		if idx != 0 && ifindex != idx {
			continue
		}
		if state&(nudReachable|nudStale|nudDelay|nudProbe|nudPermanent) == 0 {
			continue
		}
		if dst, ok := ndaAddr(m.Data[sizeofNdmsg:]); ok && dst == want {
			return true
		}
	}
	return false
}

// ndaAddr finds NDA_DST in a run of rtattrs.
func ndaAddr(b []byte) (netip.Addr, bool) {
	for len(b) >= 4 {
		l := int(binary.NativeEndian.Uint16(b[0:2]))
		t := binary.NativeEndian.Uint16(b[2:4])
		if l < 4 || l > len(b) {
			break
		}
		if t == ndaDst {
			if a, ok := netip.AddrFromSlice(b[4:l]); ok {
				return a.Unmap(), true
			}
		}
		al := (l + 3) &^ 3
		if al > len(b) {
			break
		}
		b = b[al:]
	}
	return netip.Addr{}, false
}
