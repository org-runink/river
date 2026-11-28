// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

// activity.go -- the installer backend's own log, for the UI's "Show details" pane.
//
// The install log (Log) starts when the install does. Before that the backend probes the
// hardware, sets up the network and checks the plan, and a person whose screen stops on
// "Checking this computer" had no way to see why without a terminal. Every line the backend
// logs (river-installer serve's Logf) is kept here, in memory for the pane (/api/activity) and
// appended to a file on /run (installer.log in the state directory), which the hardware report
// names and copies onto the stick. The backend never logs a secret (passwords, the recovery key
// and Wi-Fi passphrases are never passed to Logf).

import (
	"os"
	"sync"
	"time"
)

// activityMax is how many lines the pane keeps.
const activityMax = 2000

// Activity is the backend's log: a ring for the UI and an append-only file.
type Activity struct {
	mu    sync.Mutex
	lines []string
	file  string
	now   func() time.Time
}

// NewActivity keeps lines in memory and, when file is not "", appends them to file (0600).
func NewActivity(file string) *Activity {
	return &Activity{file: file, now: time.Now}
}

// Add records one line, with the time.
func (a *Activity) Add(line string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	line = a.now().UTC().Format("15:04:05") + "  " + line
	a.lines = append(a.lines, line)
	if len(a.lines) > activityMax {
		a.lines = a.lines[len(a.lines)-activityMax:]
	}
	if a.file != "" {
		if f, err := os.OpenFile(a.file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil { // #nosec G304 -- the backend's own state directory
			_, _ = f.WriteString(line + "\n")
			_ = f.Close()
		}
	}
}

// Lines returns a copy of what is kept.
func (a *Activity) Lines() []string {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.lines...)
}
