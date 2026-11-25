// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

// display.go -- a graphical session only counts when a display is actually being driven.
//
// The probes in main.go used to match on the PRESENCE of a compositor (its socket, its process).
// A compositor that starts and then cannot open any graphics device is present for a moment and
// leaves a socket behind, so the fallback decided "a desktop appeared; nothing to do" and the
// machine stayed on a dead screen with an error and no installer. The contract of this program
// is the opposite: a machine that boots the medium must always end up somewhere a person can
// install from.
//
// The kernel says directly whether a display is driven: every connector of a DRM device has
// /sys/class/drm/<card>-<connector>/enabled, "enabled" when a CRTC scans it out. That is true for
// any GPU, for the firmware framebuffer driver and for a virtual display, and false when no
// graphics device exists or nothing has set a mode. It needs no knowledge of the hardware.

import (
	"os"
	"path/filepath"
	"strings"
)

// displayDriven reports whether at least one DRM connector under root is enabled.
func displayDriven(root string) bool {
	ents, err := filepath.Glob(filepath.Join(root, "sys/class/drm/card*-*/enabled"))
	if err != nil {
		return false
	}
	for _, p := range ents {
		b, err := os.ReadFile(p)
		if err == nil && strings.TrimSpace(string(b)) == "enabled" {
			return true
		}
	}
	return false
}

// driven is the check the probes use; a test replaces it.
var driven = func() bool { return displayDriven("/") }
