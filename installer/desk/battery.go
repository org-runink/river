// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The battery letters: one when a discharging battery falls to low, one more at critical.
// Charging (or rising above low again) clears them, so the next fall is announced again.
const (
	batteryLow      = 15
	batteryCritical = 5
	batteryEvery    = time.Minute
)

// batteryState is one battery as /sys/class/power_supply/<name> shows it.
type batteryState struct {
	name        string
	capacity    int
	discharging bool
}

// readBatteries returns every supply of type Battery under root that reports a capacity.
// Mains adapters, USB supplies and a peripheral's battery that reports no capacity are skipped.
func readBatteries(root string) []batteryState {
	ents, _ := os.ReadDir(root)
	var out []batteryState
	for _, e := range ents {
		d := filepath.Join(root, e.Name())
		if read(d, "type") != "Battery" || read(d, "scope") == "Device" {
			continue
		}
		c, err := strconv.Atoi(read(d, "capacity"))
		if err != nil {
			continue
		}
		out = append(out, batteryState{e.Name(), c, read(d, "status") == "Discharging"})
	}
	return out
}

func read(dir, f string) string {
	b, _ := os.ReadFile(filepath.Join(dir, f))
	return strings.TrimSpace(string(b))
}

// batteryWatch remembers, per battery, the lowest level already announced (0: none).
type batteryWatch map[string]int

// check reads root once and sends the letters that are due.
func (w batteryWatch) check(root string, say func(summary, body string)) {
	for _, b := range readBatteries(root) {
		if !b.discharging || b.capacity > batteryLow {
			delete(w, b.name)
			continue
		}
		level := batteryLow
		if b.capacity <= batteryCritical {
			level = batteryCritical
		}
		if told, ok := w[b.name]; ok && told <= level {
			continue
		}
		w[b.name] = level
		if level == batteryCritical {
			say("Battery critically low", fmt.Sprintf("%s is at %d%%. Plug the computer in now, or save your work.", b.name, b.capacity))
		} else {
			say("Battery low", fmt.Sprintf("%s is at %d%%. Plug the computer in soon.", b.name, b.capacity))
		}
	}
}

// battery checks root every minute until ctx ends.
func battery(ctx context.Context, root string, say func(summary, body string)) error {
	w := batteryWatch{}
	for {
		w.check(root, say)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(batteryEvery):
		}
	}
}
