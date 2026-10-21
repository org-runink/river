// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBoundedReturnsTrueWhenTheFunctionFinishes(t *testing.T) {
	ran := false
	if !bounded(time.Second, func() { ran = true }) || !ran {
		t.Fatal("a quick function must finish inside its budget")
	}
}

// The structural guarantee: a step that never returns costs the budget, not the installer.
func TestBoundedGivesUpOnAFunctionThatNeverReturns(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	start := time.Now()
	if bounded(100*time.Millisecond, func() { <-block }) {
		t.Fatal("a stuck function was reported as finished")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("bounded waited %s for a 100ms budget", d)
	}
}

// Arguments of a `go` statement are evaluated by the CALLER. The early save used to be
// `go saveReport(name, hardwareReport(..., readKmsg()))`, so a blocked kernel-log read blocked the
// wait loop that calls it. saveEarly must return at once even when the kernel-log read never does.
func TestSaveEarlyDoesNotRunTheKernelLogReadOnTheCallersGoroutine(t *testing.T) {
	block := make(chan struct{})
	saved := make(chan string, 1)
	returned := make(chan struct{})
	go func() {
		saveEarly(func() []string { <-block; return nil }, func(name, content string) { saved <- name })
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("saveEarly blocked its caller on the kernel-log read")
	}
	close(block) // the read finishes late: the save must still happen, under the right name
	select {
	case name := <-saved:
		if name != "river-report-early.txt" {
			t.Errorf("saved as %q", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the early report was never saved once the read finished")
	}
}

// What /dev/kmsg delivers: one record per read, "prio,seq,usec,flags;message" plus indented
// key=value continuation lines. A FIFO that holds records and stays open stands in for it.
func TestReadKmsgParsesRecordsAndThenStops(t *testing.T) {
	p, closeW := emptyPollableFile(t)
	defer closeW()
	w, err := os.OpenFile(p, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rec := "6,1,1000,-;usb 1-1: new device\n" +
		"3,2,2000000,-;iwlwifi 0000:02:00.0: no suitable firmware\n SUBSYSTEM=pci\n" +
		"6,3,3000000,-;i915 0000:00:02.0: [drm] probed\n"
	if _, err := w.WriteString(rec); err != nil {
		t.Fatal(err)
	}
	done := make(chan []string, 1)
	go func() { done <- readKmsg(p, 10) }()
	select {
	case got := <-done:
		j := strings.Join(got, "\n")
		if len(got) != 2 || !strings.Contains(j, "iwlwifi") || !strings.Contains(j, "[drm]") || strings.Contains(j, "usb 1-1") {
			t.Errorf("want the two relevant records and not the noise, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readKmsg did not stop after the records it was given")
	}
}

const syscallNonblock = 0x800 // O_NONBLOCK on linux; keeps the writer from blocking on a FIFO
