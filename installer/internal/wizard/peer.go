// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// peerUID returns the uid owning the client end of a loopback TCP connection, the way identd
// does: the client socket appears in /proc/net/tcp6 (or tcp) with its local address equal to
// the connection's remote address, its remote address equal to our local one, and the uid
// of the process that created it.
//
// The installer's API serves secrets (the recovery key while it is shown, a setup page URL
// with its token) to the kiosk and to nobody else on the machine; this is how it tells.
func peerUID(procRoot string, local, remote *net.TCPAddr) (int, error) {
	for _, f := range []string{"net/tcp6", "net/tcp"} {
		uid, err := scanTCP(procRoot+"/"+f, local, remote)
		if err == nil {
			return uid, nil
		}
		if !errors.Is(err, errNoMatch) && !errors.Is(err, os.ErrNotExist) {
			return -1, err
		}
	}
	return -1, errNoMatch
}

var errNoMatch = errors.New("peer socket not found")

func scanTCP(path string, local, remote *net.TCPAddr) (int, error) {
	f, err := os.Open(path) // #nosec G304 -- a fixed /proc path
	if err != nil {
		return -1, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	first := true
	for s.Scan() {
		if first {
			first = false
			continue
		}
		fields := strings.Fields(s.Text())
		if len(fields) < 8 {
			continue
		}
		la, err1 := parseProcAddr(fields[1])
		ra, err2 := parseProcAddr(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		// The client's socket: its local end is our remote end, and vice versa.
		if sameAddr(la, remote) && sameAddr(ra, local) {
			uid, err := strconv.Atoi(fields[7])
			if err != nil {
				return -1, fmt.Errorf("bad uid field %q", fields[7])
			}
			return uid, nil
		}
	}
	if err := s.Err(); err != nil {
		return -1, err
	}
	return -1, errNoMatch
}

func sameAddr(a, b *net.TCPAddr) bool {
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

// parseProcAddr decodes "0100007F:1F90" (IPv4) or the 32-hex-digit IPv6 form: the address
// is stored as 32-bit words in host (little-endian) byte order, the port big-endian.
func parseProcAddr(s string) (*net.TCPAddr, error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return nil, errors.New("no port")
	}
	raw, err := hex.DecodeString(s[:i])
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, errors.New("bad address")
	}
	port, err := strconv.ParseUint(s[i+1:], 16, 16)
	if err != nil {
		return nil, errors.New("bad port")
	}
	ip := make(net.IP, len(raw))
	for w := 0; w < len(raw); w += 4 {
		ip[w], ip[w+1], ip[w+2], ip[w+3] = raw[w+3], raw[w+2], raw[w+1], raw[w]
	}
	return &net.TCPAddr{IP: ip, Port: int(port)}, nil
}
