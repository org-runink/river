// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Conn is what the checklist runner needs from QEMU's QMP: one command and its reply.
type Conn interface {
	Exec(cmd string, args any) (json.RawMessage, error)
}

// QMP is a client of QEMU's machine protocol on a unix socket (-qmp unix:PATH,server=on,wait=off).
type QMP struct {
	c        net.Conn
	r        *bufio.Reader
	Shutdown bool // a SHUTDOWN event went by
}

// DialQMP connects, reads the greeting and negotiates capabilities. QEMU takes one client at a
// time, so callers close it when done.
func DialQMP(path string, wait time.Duration) (*QMP, error) {
	var c net.Conn
	var err error
	for end := time.Now().Add(wait); ; {
		if c, err = net.Dial("unix", path); err == nil {
			break
		}
		if time.Now().After(end) {
			return nil, err
		}
		time.Sleep(500 * time.Millisecond)
	}
	q := &QMP{c: c, r: bufio.NewReader(c)}
	if _, err := q.readLine(10 * time.Second); err != nil { // the greeting
		c.Close()
		return nil, fmt.Errorf("qmp greeting: %w", err)
	}
	if _, err := q.Exec("qmp_capabilities", nil); err != nil {
		c.Close()
		return nil, err
	}
	return q, nil
}

func (q *QMP) Close() error { return q.c.Close() }

func (q *QMP) readLine(d time.Duration) (map[string]json.RawMessage, error) {
	q.c.SetReadDeadline(time.Now().Add(d))
	line, err := q.r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(line, &m); err != nil {
		return nil, fmt.Errorf("qmp: bad line %q: %w", line, err)
	}
	return m, nil
}

// Exec runs one command and returns its "return" value; events that arrive first are noted.
func (q *QMP) Exec(cmd string, args any) (json.RawMessage, error) {
	req := map[string]any{"execute": cmd}
	if args != nil {
		req["arguments"] = args
	}
	b, _ := json.Marshal(req)
	q.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := q.c.Write(append(b, '\n')); err != nil {
		return nil, err
	}
	for {
		m, err := q.readLine(60 * time.Second)
		if err != nil {
			return nil, err
		}
		if ev, ok := m["event"]; ok {
			if string(ev) == `"SHUTDOWN"` {
				q.Shutdown = true
			}
			continue
		}
		if e, ok := m["error"]; ok {
			var qe struct{ Desc string }
			json.Unmarshal(e, &qe)
			return nil, fmt.Errorf("qmp %s: %s", cmd, qe.Desc)
		}
		if r, ok := m["return"]; ok {
			return r, nil
		}
	}
}

// WaitShutdown waits until QEMU reports SHUTDOWN or closes the socket (the VM powered off).
func (q *QMP) WaitShutdown(d time.Duration) bool {
	if q.Shutdown {
		return true
	}
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		m, err := q.readLine(time.Until(end))
		if err != nil {
			return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)
		}
		if ev, ok := m["event"]; ok && string(ev) == `"SHUTDOWN"` {
			return true
		}
	}
	return false
}

// --- input -------------------------------------------------------------------------------------

func qcodes(keys []string) []map[string]any {
	var out []map[string]any
	for _, k := range keys {
		out = append(out, map[string]any{"type": "qcode", "data": k})
	}
	return out
}

// SendKeys presses the keys together and releases them after hold (send-key).
func SendKeys(c Conn, keys []string, hold time.Duration) error {
	_, err := c.Exec("send-key", map[string]any{"keys": qcodes(keys), "hold-time": hold.Milliseconds()})
	return err
}

// KeyEvents presses (down) or releases keys one by one in order, so a chord can be held across
// other actions (Alt+Tab held while the screen is photographed).
func KeyEvents(c Conn, keys []string, down bool) error {
	var evs []map[string]any
	list := keys
	if !down { // release in reverse order
		list = make([]string, len(keys))
		for i, k := range keys {
			list[len(keys)-1-i] = k
		}
	}
	for _, k := range list {
		evs = append(evs, map[string]any{"type": "key", "data": map[string]any{
			"down": down, "key": map[string]any{"type": "qcode", "data": k}}})
	}
	_, err := c.Exec("input-send-event", map[string]any{"events": evs})
	return err
}

// absMax is the range of an absolute pointer axis (usb-tablet, virtio-tablet).
const absMax = 32767

// MoveTo puts the absolute pointer at fx,fy, fractions of the screen (0..1).
func MoveTo(c Conn, fx, fy float64) error {
	if fx < 0 || fx > 1 || fy < 0 || fy > 1 {
		return fmt.Errorf("pointer position %.3f,%.3f outside the screen (fractions 0..1)", fx, fy)
	}
	_, err := c.Exec("input-send-event", map[string]any{"events": []map[string]any{
		{"type": "abs", "data": map[string]any{"axis": "x", "value": int(fx * absMax)}},
		{"type": "abs", "data": map[string]any{"axis": "y", "value": int(fy * absMax)}},
	}})
	return err
}

// Button presses or releases a mouse button ("left", "right", "middle").
func Button(c Conn, button string, down bool) error {
	_, err := c.Exec("input-send-event", map[string]any{"events": []map[string]any{
		{"type": "btn", "data": map[string]any{"down": down, "button": button}},
	}})
	return err
}

// Click moves to fx,fy and clicks.
func Click(c Conn, fx, fy float64, button string) error {
	if err := MoveTo(c, fx, fy); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond)
	if err := Button(c, button, true); err != nil {
		return err
	}
	time.Sleep(80 * time.Millisecond)
	return Button(c, button, false)
}

// TypeText types text on the guest keyboard, one chord at a time.
func TypeText(c Conn, text string) error {
	chords, err := TypeChords(text)
	if err != nil {
		return err
	}
	for _, ch := range chords {
		if err := SendKeys(c, ch, 25*time.Millisecond); err != nil {
			return err
		}
		time.Sleep(40 * time.Millisecond)
	}
	return nil
}

// --- screen and USB ----------------------------------------------------------------------------

// Screendump writes the screen (head 0, or the given head of the display device) as a PNG. A
// QEMU that cannot write PNG itself (before 7.1) gives a PPM, converted here.
func Screendump(c Conn, path string, head int) error {
	args := map[string]any{"filename": path, "format": "png"}
	if head > 0 {
		args["head"] = head
		args["device"] = "video0"
	}
	if _, err := c.Exec("screendump", args); err == nil {
		return nil
	}
	ppm := path + ".ppm"
	delete(args, "format")
	args["filename"] = ppm
	if _, err := c.Exec("screendump", args); err != nil {
		return err
	}
	defer os.Remove(ppm)
	return PPMFileToPNG(ppm, path)
}

// UsbAdd hot-plugs a USB mass-storage device backed by the raw image file.
func UsbAdd(c Conn, image, bus string) error {
	abs, err := filepath.Abs(image)
	if err != nil {
		return err
	}
	if _, err := c.Exec("blockdev-add", map[string]any{"driver": "raw", "node-name": "rtstick",
		"file": map[string]any{"driver": "file", "filename": abs}}); err != nil {
		return err
	}
	_, err = c.Exec("device_add", map[string]any{"driver": "usb-storage", "id": "rtstick0",
		"drive": "rtstick", "bus": bus})
	return err
}
