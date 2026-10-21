// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

// bounded.go -- nothing in the hardware report may hold up the installer.
//
// The report is a convenience for whoever reports a problem; the installer is the product. The
// first build of the report read /dev/kmsg in a way that blocked forever as root, and because it ran
// on the fallback's own goroutine the fallback never got as far as the kiosk. That one cause is
// fixed in readKmsg; this is the structural guarantee behind it: every report step runs on its own
// goroutine and the fallback stops WAITING for it after a fixed budget, whatever it is stuck on.

import (
	"time"
)

// reportBudget is the longest the fallback waits for the on-screen summary and the save to the
// stick before it moves on to the installer.
const reportBudget = 20 * time.Second

// bounded runs fn on its own goroutine and waits for it at most d. It reports whether fn finished.
// A function that is still running when bounded returns keeps running in the background; it can no
// longer delay anything.
func bounded(d time.Duration, fn func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// saveEarly saves a first report in the background. EVERYTHING it needs, the kernel log included,
// is gathered inside the goroutine: arguments of a `go` statement are evaluated by the caller, so
// `go save(name, report(readKmsg()))` would run readKmsg on the wait loop's goroutine.
func saveEarly(kmsg func() []string, save func(name, content string)) {
	go func() {
		save("river-report-early.txt", hardwareReport("/", kmsg(), true))
	}()
}
