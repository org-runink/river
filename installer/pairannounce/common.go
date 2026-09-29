// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Layout of one session. Everything is on /run (tmpfs): nothing of a session survives a
// reboot, and 20-clone-rootfs never copies /run to an installed node.
const (
	stateDir    = "/run/river-pair"          // 0711 root: traversable, nothing listable
	privDir     = stateDir + "/private"      // 0700 root: host key, sshd config, plan, probe
	sshDir      = stateDir + "/ssh"          // 0755 root: authorized_keys (0644), read by sshd as the user
	homeDir     = stateDir + "/home"         // 0700 river-pair: payload/NAME staging (RAM)
	consoleSock = stateDir + "/console.sock" // 0600 root: the local console <-> the session
	rpcSock     = stateDir + "/rpc.sock"     // 0660 root:river-pair: the ForceCommand wrapper -> the session
	svRoot      = "/run/river-pair-sv"       // the s6 service directory copied from svTemplate
	svName      = "river-pair-announce"      // its name in the scan directory
	svTemplate  = "/usr/local/lib/river-pair/sv/" + svName
	scanDir     = "/run/service"        // s6-linux-init's scan directory
	logDir      = "/var/log/river-pair" // 0700 root, live RAM overlay; excluded from the clone
	selfPath    = "/usr/local/bin/river-pair-announce"
	payloadDest = "/run/river/payloads" // where stage-payload hands payloads to the installer
)

// event is one line on the console socket, session -> console.
type event struct {
	Ev      string `json:"ev"`
	Host    string `json:"host,omitempty"`
	Code    string `json:"code,omitempty"`
	FP      string `json:"fp,omitempty"`
	Iface   string `json:"iface,omitempty"`
	Addr    string `json:"addr,omitempty"`
	Expires string `json:"expires,omitempty"`
	Line    string `json:"line,omitempty"`
	Left    int    `json:"left,omitempty"`
	Why     string `json:"why,omitempty"`
}

// answer is one line on the console socket, console -> session.
type answer struct {
	Allow bool `json:"allow"`
}

// isLive reports whether this system runs from the live installer medium. The target side
// refuses to run anywhere else: an installed node never offers itself for installation.
func isLive() bool {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		mp := f[1]
		// artix live (/run/artix/bootmnt), archiso, miso, dracut live.
		if strings.HasPrefix(mp, "/run/artix/") || strings.HasPrefix(mp, "/run/archiso/") || strings.HasPrefix(mp, "/run/miso/") ||
			mp == "/bootmnt" || mp == "/run/initramfs/live" || mp == "/run/rootfsbase" {
			return true
		}
	}
	return false
}

func needRootLive() error {
	if os.Geteuid() != 0 {
		return errors.New("must run as root (sudo river-pair-announce)")
	}
	if !isLive() {
		return errors.New("this is not the Runink River live installer: only a machine booted from the install medium can be installed over the LAN")
	}
	return nil
}

// logger writes the session log: /var/log/river-pair/<id>.log (0600, live RAM only) and
// stderr (the s6 catch-all log). It never receives the code, a secret or the recovery key.
type logger struct {
	mu sync.Mutex
	f  io.Writer
}

func newLogger(id string) *logger {
	l := &logger{f: os.Stderr}
	if err := os.MkdirAll(logDir, 0o700); err == nil {
		if f, err := os.OpenFile(filepath.Join(logDir, id+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			l.f = io.MultiWriter(f, os.Stderr)
		}
	}
	return l
}

func (l *logger) Printf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.f, "%s river-pair-announce: %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, a...))
}

// readLine reads one '\n'-terminated line of at most max bytes.
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var out []byte
	for {
		b, err := r.ReadSlice('\n')
		out = append(out, b...)
		if len(out) > max {
			return nil, errors.New("line too long")
		}
		if err == nil {
			return out, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
