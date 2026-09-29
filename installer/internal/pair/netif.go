// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package pair

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
)

// NetStatePath is where the installer's network step records the network it set up
// (schema river.net-state/v1). Pairing runs after that step; when the file is absent it
// just needs an interface that is up.
const NetStatePath = "/run/river/net-state.json"

// Link is what interface selection needs to know about one interface.
type Link struct {
	Name      string
	Index     int
	Up        bool
	Loopback  bool
	LinkLocal netip.Addr // IPv6 link-local address with zone; invalid when there is none
}

type netState struct {
	Schema string `json:"schema"`
	Links  []struct {
		Name    string `json:"name"`
		Up      bool   `json:"up"`
		Carrier bool   `json:"carrier"`
	} `json:"links"`
}

// ErrNoLink says there is no usable interface.
var ErrNoLink = errors.New("no network interface is up with an IPv6 link-local address: run the network step first (or set the link up)")

// ChooseLink picks the interface to pair on. want (--iface) wins. Otherwise the first link
// the network step recorded as up with carrier, else the first up, non-loopback interface
// with an IPv6 link-local address. Pairing is link-local only, so a link without one is
// useless whatever else it has.
func ChooseLink(links []Link, stateJSON []byte, want string) (Link, error) {
	usable := func(l Link) bool { return l.Up && !l.Loopback && l.LinkLocal.IsValid() }
	byName := map[string]Link{}
	for _, l := range links {
		byName[l.Name] = l
	}
	if want != "" {
		l, ok := byName[want]
		if !ok {
			return Link{}, fmt.Errorf("no interface %q", want)
		}
		if !usable(l) {
			return Link{}, fmt.Errorf("interface %s is not up with an IPv6 link-local address", want)
		}
		return l, nil
	}
	if len(stateJSON) > 0 {
		var st netState
		if err := json.Unmarshal(stateJSON, &st); err == nil && strings.HasPrefix(st.Schema, "river.net-state/") {
			for _, sl := range st.Links {
				if l, ok := byName[sl.Name]; ok && sl.Up && sl.Carrier && usable(l) {
					return l, nil
				}
			}
		}
	}
	for _, l := range links {
		if usable(l) {
			return l, nil
		}
	}
	return Link{}, ErrNoLink
}

// SystemLinks lists this machine's interfaces.
func SystemLinks() ([]Link, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Link
	for _, i := range ifs {
		l := Link{Name: i.Name, Index: i.Index, Up: i.Flags&net.FlagUp != 0, Loopback: i.Flags&net.FlagLoopback != 0}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			pn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(pn.IP)
			if ok && ip.Is6() && !ip.Is4In6() && ip.IsLinkLocalUnicast() {
				l.LinkLocal = ip.WithZone(i.Name)
				break
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// PickLink is ChooseLink over this machine's interfaces and its recorded network state.
func PickLink(want string) (Link, error) {
	links, err := SystemLinks()
	if err != nil {
		return Link{}, err
	}
	st, _ := os.ReadFile(NetStatePath) // absent: fine
	return ChooseLink(links, st, want)
}
