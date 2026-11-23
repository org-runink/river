// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GuestExec runs a shell command inside the guest and returns its exit status and output.
type GuestExec interface {
	// Run runs cmd as user ("root" or "runink") and waits for it. With wait false it only sends
	// the command (for one that ends the guest, like a power off).
	Run(user, cmd string, timeout time.Duration, wait bool) (rc int, out string, err error)
}

// Guest is the command channel: the guest's second serial port (ttyS1), which QEMU exposes as a
// unix socket (-chardev socket,...,server=on). A small agent in the guest (the harness's
// installed.sh) reads lines from it:
//
//	RIVERPING <n>                    answered with RIVERPONG <n>
//	RIVERCMD <id> <user> <base64>    run the command, answer RIVERRES <id> <rc> <base64 output>
//
// Lines of any other kind (kernel messages, a getty) are ignored.
type Guest struct {
	conn  net.Conn
	lines chan string
	mu    sync.Mutex
	seq   int
}

// DialGuest connects to the socket and waits for the agent to answer a ping.
func DialGuest(path string, wait time.Duration) (*Guest, error) {
	end := time.Now().Add(wait)
	var c net.Conn
	var err error
	for {
		if c, err = net.Dial("unix", path); err == nil {
			break
		}
		if time.Now().After(end) {
			return nil, err
		}
		time.Sleep(time.Second)
	}
	g := &Guest{conn: c, lines: make(chan string, 256)}
	go func() {
		sc := bufio.NewScanner(c)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			g.lines <- strings.TrimRight(sc.Text(), "\r")
		}
		close(g.lines)
	}()
	for n := 1; ; n++ {
		fmt.Fprintf(c, "RIVERPING %d\n", n)
		deadline := time.After(3 * time.Second)
	wait:
		for {
			select {
			case l, ok := <-g.lines:
				if !ok {
					return nil, fmt.Errorf("guest channel closed")
				}
				if strings.HasPrefix(l, "RIVERPONG ") {
					return g, nil
				}
			case <-deadline:
				break wait
			}
		}
		if time.Now().After(end) {
			c.Close()
			return nil, fmt.Errorf("the guest agent never answered on %s", path)
		}
	}
}

func (g *Guest) Close() error { return g.conn.Close() }

func (g *Guest) Run(user, cmd string, timeout time.Duration, wait bool) (int, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	id := strconv.Itoa(g.seq)
	if _, err := fmt.Fprintf(g.conn, "RIVERCMD %s %s %s\n", id, user, base64.StdEncoding.EncodeToString([]byte(cmd))); err != nil {
		return -1, "", err
	}
	if !wait {
		return 0, "", nil
	}
	deadline := time.After(timeout)
	for {
		select {
		case l, ok := <-g.lines:
			if !ok {
				return -1, "", fmt.Errorf("guest channel closed")
			}
			if rc, out, ok, err := ParseResult(l, id); ok {
				return rc, out, err
			}
		case <-deadline:
			return -1, "", fmt.Errorf("no answer from the guest in %s", timeout)
		}
	}
}

// ParseResult reads a RIVERRES line for request id.
func ParseResult(line, id string) (rc int, out string, ok bool, err error) {
	f := strings.SplitN(line, " ", 4)
	if len(f) < 3 || f[0] != "RIVERRES" || f[1] != id {
		return 0, "", false, nil
	}
	rc, err = strconv.Atoi(f[2])
	if err != nil {
		return -1, "", true, fmt.Errorf("bad result line %q", line)
	}
	if len(f) == 4 {
		b, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(f[3]))
		if derr != nil {
			return -1, "", true, fmt.Errorf("bad output encoding in %q", line)
		}
		out = string(b)
	}
	return rc, out, true, nil
}
