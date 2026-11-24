// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"os"
	"path/filepath"
	"strings"
)

// NoticePrefix starts a line a step prints to tell the operator something that must not be
// lost in the scroll, whether the step succeeded or not ("install-notice: the models were
// deferred ..."). Both installers (this one and the text-mode runink-install, steprun.sh)
// collect these lines and show them again at the end of the install.
const NoticePrefix = "install-notice: "

// DefaultLogFile is where the install log is kept on the live system.
const DefaultLogFile = "/var/log/runink-install.log"

// tailLines is how many of a failed step's last lines the screen shows as the reason.
const tailLines = 12

// Situations a failed step leaves the machine in (Install.State).
const (
	// StateNotInstalled: the disks hold nothing that boots.
	StateNotInstalled = "not-installed"
	// StatePartial: the system and its boot loader are on the disk, but the steps from the
	// failed one on did not run, so it is not ready for use.
	StatePartial = "partial"
)

// stepState (see stepSituation) maps a step to the state its failure leaves. Steps up to
// 40-boot-grub-zfs (disk, copy, target configuration, boot loader) leave a machine that
// cannot boot; later steps (user, artifacts, models, plan, services) run on a system that
// can, so a failure there leaves it installed but unfinished.
func stepSituation(step string) string {
	if len(step) >= 2 && step[:2] >= "00" && step[:2] < "50" {
		return StateNotInstalled
	}
	return StatePartial
}

// lastLines returns up to n of the last non-empty lines.
func lastLines(lines []string, n int) []string {
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			out = append([]string{lines[i]}, out...)
		}
	}
	return out
}

// exitReason turns a step's error into plain words.
func exitReason(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if rest, ok := strings.CutPrefix(s, "exit status "); ok {
		return "exit code " + rest
	}
	return s
}

// appendFile adds one line to the install log. The log is 0600: it names disks and hosts.
// Errors are ignored on purpose; a log that cannot be written must not stop an install,
// and the same lines stay on the screen.
func appendFile(path, line string) {
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- the installer's own log path
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

// persistLog copies the log onto the new system once it exists (it has an /etc), so the
// operator can read it there after the reboot; it reports the path, or "" when it could not.
func persistLog(logFile, targetRoot string) string {
	if logFile == "" || targetRoot == "" {
		return ""
	}
	if st, err := os.Stat(filepath.Join(targetRoot, "etc")); err != nil || !st.IsDir() {
		return ""
	}
	b, err := os.ReadFile(logFile) // #nosec G304 -- the installer's own log path
	if err != nil {
		return ""
	}
	dst := filepath.Join(targetRoot, "var/log", filepath.Base(logFile))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return ""
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return ""
	}
	return filepath.Join("/var/log", filepath.Base(logFile))
}
