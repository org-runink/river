// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"
)

// waitUp: river-installer wait [--url URL] [--timeout D] — exit 0 once the UI answers. The kiosk
// launcher uses it so the web view never opens on a "cannot connect" page.
func waitUp(args []string) int {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	url := fs.String("url", "http://[::1]:47660/api/state", "what to poll")
	timeout := fs.Duration("timeout", 2*time.Minute, "give up after")
	if fs.Parse(args) != nil {
		return 2
	}
	c := http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(*timeout)
	for time.Now().Before(deadline) {
		if r, err := c.Get(*url); err == nil {
			_ = r.Body.Close()
			if r.StatusCode < 500 {
				return 0
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "river-installer wait: no answer from", *url)
	return 1
}

// Linux VT ioctls (linux/vt.h).
const (
	vtActivate   = 0x5606
	vtWaitActive = 0x5607
)

// switchVT: river-installer vt N — make virtual terminal N the visible one (what chvt does,
// without needing kbd on the image). The first-boot kiosk runs on a VT of its own.
func switchVT(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: river-installer vt N")
		return 2
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n < 1 || n > 63 {
		fmt.Fprintln(os.Stderr, "river-installer vt: N is 1..63")
		return 2
	}
	f, err := os.OpenFile("/dev/tty0", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "river-installer vt:", err)
		return 1
	}
	defer f.Close()
	for _, req := range []uintptr{vtActivate, vtWaitActive} {
		if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(n)); e != 0 {
			fmt.Fprintln(os.Stderr, "river-installer vt:", e)
			return 1
		}
	}
	return 0
}
