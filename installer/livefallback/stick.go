// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

// stick.go — leave the hardware report on the stick it booted from.
//
// The person testing a stick on a machine that will not start the desktop has no serial port,
// no network and no second screen. What they do have is the stick, which they can carry to
// another computer. The hybrid image written to it has a small FAT partition (label RIVER_EFI,
// the EFI system partition) that is NOT part of the read-only ISO9660 volume, so it can be
// written. The report goes there, and the stick becomes the way the machine tells us about
// itself: no typing, no photograph needed, nothing to read off a screen.
//
// Best effort and never a reason for the boot to fail. Every step can fail on some medium (no
// such partition because the image was burned to a disc, a read-only stick, a full partition),
// and each failure becomes one line of text, not an error that stops the fallback.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	espLabel = "RIVER_EFI"
	// maxReportBytes keeps a runaway report from filling a 4 MB partition that also holds the
	// boot loader.
	maxReportBytes = 96 * 1024
	// espHeadroom is what must remain free after the write, beyond the report itself.
	espHeadroom = 64 * 1024
)

// stick is the part of the system the save touches, as functions, so a test can supply fakes.
type stick struct {
	byLabel   string
	mount     func(src, dst, fstype string, flags uintptr, data string) error
	umount    func(dst string) error
	freeBytes func(dir string) (int64, error)
	pause     func(time.Duration) // between unmount retries
}

func realStick() stick {
	return stick{
		byLabel: "/dev/disk/by-label/" + espLabel,
		mount: func(src, dst, fstype string, flags uintptr, data string) error {
			return syscall.Mount(src, dst, fstype, flags, data)
		},
		umount: func(dst string) error { return syscall.Unmount(dst, 0) },
		freeBytes: func(dir string) (int64, error) {
			var s syscall.Statfs_t
			if err := syscall.Statfs(dir, &s); err != nil {
				return 0, err
			}
			return int64(s.Bavail) * int64(s.Bsize), nil
		},
		pause: time.Sleep,
	}
}

// save writes data to <ESP>/name and returns where it went. The partition is mounted only for
// the write and ALWAYS unmounted afterwards, including when the write fails, because a FAT
// filesystem left mounted and dirty on a stick the person is about to pull out is the one way
// this could do real harm.
func (s stick) save(name string, data []byte) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return "", fmt.Errorf("refusing to write a file called %q", name)
	}
	if len(data) > maxReportBytes {
		data = append(data[:maxReportBytes:maxReportBytes], []byte("\n[report truncated]\n")...)
	}
	dev, err := filepath.EvalSymlinks(s.byLabel)
	if err != nil {
		return "", fmt.Errorf("no %s partition on this medium (%v)", espLabel, err)
	}
	dir, err := os.MkdirTemp("", "river-esp-")
	if err != nil {
		return "", err
	}
	defer os.Remove(dir)

	// nosuid, nodev, noexec: this partition is for data, and nothing on it is ever run from here.
	flags := uintptr(syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC | syscall.MS_NOATIME)
	if err := s.mount(dev, dir, "vfat", flags, ""); err != nil {
		return "", fmt.Errorf("mounting %s as vfat: %w", dev, err)
	}

	where, werr := s.write(dir, name, data)
	uerr := s.unmountRetry(dir)
	switch {
	case werr != nil:
		return "", werr
	case uerr != nil:
		return "", fmt.Errorf("written, but unmounting %s failed: %w", dev, uerr)
	}
	return where, nil
}

func (s stick) write(dir, name string, data []byte) (string, error) {
	free, err := s.freeBytes(dir)
	if err != nil {
		return "", fmt.Errorf("cannot tell how much room %s has: %w", espLabel, err)
	}
	if free < int64(len(data))+espHeadroom {
		return "", fmt.Errorf("only %d bytes free on %s, need %d", free, espLabel, int64(len(data))+espHeadroom)
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil { // the stick is pulled soon after; make sure it is on the stick
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return espLabel + ":/" + name, nil
}

// unmountRetry tries a few times: a unmount straight after a sync can report busy for a moment.
func (s stick) unmountRetry(dir string) error {
	var err error
	for i := 0; i < 5; i++ {
		if err = s.umount(dir); err == nil {
			return nil
		}
		s.pause(200 * time.Millisecond)
	}
	return err
}

// saveReport writes a report to the stick and returns ONE line saying what happened.
func saveReport(name, content string) string {
	where, err := realStick().save(name, []byte(content))
	if err != nil {
		return "Report NOT saved to the stick: " + err.Error()
	}
	return "Report saved to the stick as " + where + " - plug the stick into another computer to read it."
}
