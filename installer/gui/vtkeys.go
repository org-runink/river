// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

// vtkeys.go -- river-installer vtkeys: Ctrl+Alt+F1..F6 always reach a text console on the live
// medium, whatever the graphical session is doing.
//
// WHY. On a VT that a compositor owns, the kernel's keyboard handler is OFF (the seat service
// sets K_OFF and VT_PROCESS), so Ctrl+Alt+F2 does something only if the compositor itself turns
// the key into a VT switch: weston binds it in its DRM backend, a wlroots desktop does it only
// if it implements it, a kiosk that never got the keyboard, or that is stuck, does nothing.
// That is the one key a person in front of a white or frozen screen knows, and on the live
// medium the console on tty2 is the way to the installer's logs and the text installer.
//
// WHAT. A small root process (started by rc.local on the live medium only) reads the keyboards'
// evdev nodes BESIDE the compositor -- evdev allows several readers, and nothing here grabs a
// device -- and on Ctrl+Alt+F<n> asks the kernel to activate VT n (VT_ACTIVATE on /dev/tty0,
// what chvt does). Under seatd that request goes through the seat service exactly as the
// compositor's own would: the kernel signals seatd, seatd disables the session (drops its DRM
// master and revokes its input) and acknowledges, and the text console takes the screen. When
// the compositor also handles the key, both ask for the same VT, which is harmless.
//
// It reads key codes only to follow Ctrl, Alt and F1..F6. It never records, prints or forwards
// any other key: the only thing it logs is the switch it made.

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Linux input constants (linux/input-event-codes.h).
const (
	evKey        = 0x01
	keyLeftCtrl  = 29
	keyRightCtrl = 97
	keyLeftAlt   = 56
	keyRightAlt  = 100
	keyF1        = 59 // F1..F10 are 59..68
	keyMax       = 0x2ff
	vtGetState   = 0x5603
)

// evioCGBitKey is EVIOCGBIT(EV_KEY, len): _IOC(_IOC_READ, 'E', 0x20 + EV_KEY, len).
func evioCGBitKey(n int) uintptr { return uintptr(2<<30 | n<<16 | 'E'<<8 | (0x20 + evKey)) }

// vtKeys tracks the modifiers across every keyboard and turns Ctrl+Alt+F<n> into a VT number.
type vtKeys struct {
	mu   sync.Mutex
	ctrl map[uint16]bool
	alt  map[uint16]bool
}

func newVTKeys() *vtKeys { return &vtKeys{ctrl: map[uint16]bool{}, alt: map[uint16]bool{}} }

// key takes one key event (code, value: 1 press, 0 release, 2 repeat) and returns the VT to
// switch to, or 0.
func (k *vtKeys) key(code uint16, value int32) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	switch code {
	case keyLeftCtrl, keyRightCtrl:
		k.ctrl[code] = value != 0
		return 0
	case keyLeftAlt, keyRightAlt:
		k.alt[code] = value != 0
		return 0
	}
	if value != 1 || code < keyF1 || code > keyF1+5 {
		return 0
	}
	if (k.ctrl[keyLeftCtrl] || k.ctrl[keyRightCtrl]) && (k.alt[keyLeftAlt] || k.alt[keyRightAlt]) {
		return int(code-keyF1) + 1
	}
	return 0
}

// isKeyboard reports whether an evdev device has Ctrl, Alt and F2 keys.
func isKeyboard(f *os.File) bool {
	bits := make([]byte, keyMax/8+1)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), evioCGBitKey(len(bits)), uintptr(unsafe.Pointer(&bits[0]))); e != 0 {
		return false
	}
	has := func(c int) bool { return bits[c/8]&(1<<(c%8)) != 0 }
	return has(keyLeftCtrl) && has(keyLeftAlt) && has(keyF1+1)
}

// activeVT returns the VT the kernel shows, or 0.
func activeVT(tty0 *os.File) int {
	var st struct{ active, signal, state uint16 }
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, tty0.Fd(), vtGetState, uintptr(unsafe.Pointer(&st))); e != 0 {
		return 0
	}
	return int(st.active)
}

// vtKeysMain: river-installer vtkeys. It runs until killed.
func vtKeysMain(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: river-installer vtkeys")
		return 2
	}
	tty0, err := os.OpenFile("/dev/tty0", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "river-installer vtkeys:", err)
		return 1
	}
	defer tty0.Close()
	keys := newVTKeys()
	evSize := int(unsafe.Sizeof(syscall.Timeval{})) + 8 // struct input_event
	var mu sync.Mutex
	open := map[string]bool{}
	read := func(path string, f *os.File) {
		defer func() { _ = f.Close(); mu.Lock(); delete(open, path); mu.Unlock() }()
		buf := make([]byte, evSize*64)
		for {
			n, err := f.Read(buf)
			if err != nil {
				return // unplugged
			}
			for off := 0; off+evSize <= n; off += evSize {
				ev := buf[off+evSize-8 : off+evSize]
				if binary.LittleEndian.Uint16(ev[0:2]) != evKey {
					continue
				}
				vt := keys.key(binary.LittleEndian.Uint16(ev[2:4]), int32(binary.LittleEndian.Uint32(ev[4:8])))
				if vt == 0 || vt == activeVT(tty0) {
					continue
				}
				if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, tty0.Fd(), vtActivate, uintptr(vt)); e != 0 {
					fmt.Fprintf(os.Stderr, "river-installer vtkeys: Ctrl+Alt+F%d: VT_ACTIVATE %d: %v\n", vt, vt, e)
				} else {
					fmt.Fprintf(os.Stderr, "river-installer vtkeys: Ctrl+Alt+F%d: switched to VT %d\n", vt, vt)
				}
			}
		}
	}
	skip := map[string]time.Time{} // non-keyboards, by when they were looked at (this loop only)
	fmt.Fprintln(os.Stderr, "river-installer vtkeys: watching the keyboards for Ctrl+Alt+F1..F6")
	for {
		paths, _ := filepath.Glob("/dev/input/event*")
		for _, p := range paths {
			mu.Lock()
			seen := open[p]
			mu.Unlock()
			if seen || time.Since(skip[p]) < 30*time.Second {
				continue
			}
			f, err := os.Open(p) // #nosec G304 -- /dev/input/event* only
			if err != nil {
				continue
			}
			if !isKeyboard(f) {
				_ = f.Close()
				skip[p] = time.Now() // not a keyboard: look again in a while (the node may be reused)
				continue
			}
			mu.Lock()
			open[p] = true
			mu.Unlock()
			go read(p, f)
		}
		time.Sleep(2 * time.Second)
	}
}
