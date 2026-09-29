// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package pair

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// BeaconMagic is the first line of every announcement.
const BeaconMagic = "RIVER-PAIR 1"

// MaxBeacon is the largest announcement a listener accepts.
const MaxBeacon = 512

// Beacon is one announcement: the minimum an operator needs to pick a target and check its
// identity, and nothing about the machine itself (no model, serial, disks or addresses).
type Beacon struct {
	Host string // river-install-<short id>
	FP   string // SHA256 fingerprint of the session's ephemeral SSH host key
}

var hostRe = regexp.MustCompile(`^river-install-[0-9a-z]{6}$`)

// ID is the session's short id (the part after river-install-).
func (b Beacon) ID() string { return strings.TrimPrefix(b.Host, "river-install-") }

// Marshal renders the announcement payload.
func (b Beacon) Marshal() []byte {
	return []byte(BeaconMagic + "\nhost " + b.Host + "\nfp " + b.FP + "\n")
}

// Beacon parse errors.
var (
	ErrNotLinkLocal = errors.New("source is not an IPv6 link-local address")
	ErrWrongLink    = errors.New("announcement arrived on another interface")
	ErrMalformed    = errors.New("malformed announcement")
)

// ParseBeacon validates one received datagram. src is the packet's source address and iface
// the interface the listener is bound to. Anything that is not a well-formed announcement
// from an IPv6 link-local address on that very link is rejected: routed traffic can never
// carry a link-local source, so a target is always on the operator's own segment, and the
// operator later connects to that source address and nowhere else.
func ParseBeacon(payload []byte, src netip.AddrPort, iface string) (Beacon, error) {
	a := src.Addr()
	if !a.Is6() || a.Is4In6() || !a.IsLinkLocalUnicast() {
		return Beacon{}, ErrNotLinkLocal
	}
	if iface != "" && a.Zone() != iface {
		return Beacon{}, ErrWrongLink
	}
	if len(payload) == 0 || len(payload) > MaxBeacon {
		return Beacon{}, ErrMalformed
	}
	s := string(payload)
	if !strings.HasSuffix(s, "\n") || strings.ContainsAny(s, "\r\x00") {
		return Beacon{}, ErrMalformed
	}
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) != 3 || lines[0] != BeaconMagic {
		return Beacon{}, ErrMalformed
	}
	var b Beacon
	for _, l := range lines[1:] {
		k, v, ok := strings.Cut(l, " ")
		if !ok {
			return Beacon{}, ErrMalformed
		}
		switch k {
		case "host":
			if b.Host != "" || !hostRe.MatchString(v) {
				return Beacon{}, ErrMalformed
			}
			b.Host = v
		case "fp":
			if b.FP != "" || !ValidFingerprint(v) {
				return Beacon{}, ErrMalformed
			}
			b.FP = v
		default:
			return Beacon{}, ErrMalformed
		}
	}
	if b.Host == "" || b.FP == "" {
		return Beacon{}, ErrMalformed
	}
	return b, nil
}

// Heard is an announcement and where it came from.
type Heard struct {
	Beacon
	Addr netip.Addr // link-local source, with zone
}

func (h Heard) String() string {
	return fmt.Sprintf("%s  %s  (%s)", h.Host, h.FP, h.Addr)
}
