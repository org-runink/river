// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package wizard

import (
	"strings"

	"github.com/org-runink/river/installer/internal/steprun"
)

// The graphical installer shares its failure reporting with the text installers
// (installer/internal/steprun, river-steprun): the same log, the same notice lines and the
// same states.

// NoticePrefix starts a line a step prints to tell the operator something that must not be lost
// in the scroll ("install-notice: the models were deferred ..."). Both installers collect these
// lines and show them again at the end of the install.
const NoticePrefix = steprun.NoticePrefix

// DefaultLogFile is where the install log is kept on the live system.
const DefaultLogFile = steprun.DefaultLog

// tailLines is how many of a failed step's last lines the screen shows as the reason.
const tailLines = steprun.TailLines

// Situations a failed step leaves the machine in (Install.State).
const (
	StateNotInstalled = steprun.NotInstalled
	StatePartial      = steprun.Partial
)

func stepSituation(step string) string         { return steprun.Situation(step) }
func lastLines(l []string, n int) []string     { return steprun.LastLines(l, n) }
func appendFile(path, line string)             { steprun.AppendFile(path, line) }
func persistLog(logFile, target string) string { return steprun.Persist(logFile, target) }

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
