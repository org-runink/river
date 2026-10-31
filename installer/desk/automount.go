// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"io"
	"regexp"
	"strings"
	"time"
)

// UDisks2, as gdbus prints it. GetManagedObjects (what is in at login) and every
// InterfacesAdded signal (what is put in later) both print one section per object:
//
//	objectpath '/org/freedesktop/UDisks2/block_devices/sdb1', {'org.freedesktop.UDisks2.Block':
//	  {'Device': <b'/dev/sdb1'>, ..., 'HintAuto': <true>, 'HintIgnore': <false>, ...},
//	 'org.freedesktop.UDisks2.Filesystem': {'MountPoints': <@aay []>, ...}}
//
// A section names a medium to mount when it is a block device with a filesystem that udisks
// marks for automounting (HintAuto: removable media), not a system device (HintSystem), not
// hidden (HintIgnore), and not mounted yet (MountPoints empty). The decision is udisks's own,
// the one desktops' automounters follow; nothing here guesses from device names.
var (
	udObjectRe = regexp.MustCompile(`objectpath '/org/freedesktop/UDisks2/block_devices/[^']+'`)
	udDeviceRe = regexp.MustCompile(`'Device': <b'(/dev/[^']+)'>`)
)

const udisksPath = "/org/freedesktop/UDisks2"

// toMount returns the devices in gdbus's text that udisks would have a desktop mount.
func toMount(text string) []string {
	var devs []string
	locs := udObjectRe.FindAllStringIndex(text, -1)
	for i, l := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		s := text[l[0]:end]
		// the next object (a drive, a job) ends this section too
		if j := strings.Index(s[len("objectpath '"):], "objectpath '"); j >= 0 {
			s = s[:j+len("objectpath '")]
		}
		m := udDeviceRe.FindStringSubmatch(s)
		if m == nil || !strings.Contains(s, "'org.freedesktop.UDisks2.Filesystem'") ||
			!strings.Contains(s, "'HintAuto': <true>") || strings.Contains(s, "'HintSystem': <true>") ||
			strings.Contains(s, "'HintIgnore': <true>") || !strings.Contains(s, "'MountPoints': <@aay []>") {
			continue
		}
		devs = append(devs, m[1])
	}
	return devs
}

// automount mounts what is in now, then what is put in, until ctx ends. gdbus monitor ends
// when the bus or udisks goes away; it is started again after a pause.
func automount(ctx context.Context, r runner) error {
	mount := func(text string) {
		for _, dev := range toMount(text) {
			out, err := r.output(ctx, "udisksctl", "mount", "--no-user-interaction", "--block-device", dev)
			if err != nil {
				notify("Could not open "+dev, out)
				continue
			}
			// "Mounted /dev/sdb1 at /run/media/runink/STICK"
			notify("Opened "+dev, strings.TrimSuffix(out, "."))
		}
	}
	for ctx.Err() == nil {
		if objs, err := r.output(ctx, "gdbus", "call", "--system", "--dest", "org.freedesktop.UDisks2",
			"--object-path", udisksPath, "--method", "org.freedesktop.DBus.ObjectManager.GetManagedObjects"); err == nil {
			mount(objs)
		}
		_ = r.lines(ctx, func(l string) {
			if strings.Contains(l, "InterfacesAdded") {
				mount(l)
			}
		}, "gdbus", "monitor", "--system", "--dest", "org.freedesktop.UDisks2", "--object-path", udisksPath)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
	return ctx.Err()
}

// readLines hands each line of r to each; gdbus prints a whole object on one long line.
func readLines(r io.Reader, each func(string)) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64<<10), 4<<20)
	for s.Scan() {
		each(s.Text())
	}
}
