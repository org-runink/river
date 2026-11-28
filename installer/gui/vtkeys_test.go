// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import "testing"

// Ctrl+Alt+F<n> (either Ctrl, either Alt, held while F<n> is pressed) asks for VT n, F1..F6
// only; F<n> alone, a repeat, a release or a key held after the modifiers came up asks for nothing.
func TestVTKeys(t *testing.T) {
	type ev struct {
		code  uint16
		value int32
	}
	cases := []struct {
		name string
		evs  []ev
		want int
	}{
		{"ctrl+alt+f2", []ev{{keyLeftCtrl, 1}, {keyLeftAlt, 1}, {keyF1 + 1, 1}}, 2},
		{"right ctrl + right alt + f1", []ev{{keyRightCtrl, 1}, {keyRightAlt, 1}, {keyF1, 1}}, 1},
		{"ctrl+alt+f6", []ev{{keyLeftCtrl, 1}, {keyLeftAlt, 1}, {keyF1 + 5, 1}}, 6},
		{"f7 is not handled", []ev{{keyLeftCtrl, 1}, {keyLeftAlt, 1}, {keyF1 + 6, 1}}, 0},
		{"f2 alone", []ev{{keyF1 + 1, 1}}, 0},
		{"alt+f2 (no ctrl)", []ev{{keyLeftAlt, 1}, {keyF1 + 1, 1}}, 0},
		{"ctrl released first", []ev{{keyLeftCtrl, 1}, {keyLeftAlt, 1}, {keyLeftCtrl, 0}, {keyF1 + 1, 1}}, 0},
		{"repeat", []ev{{keyLeftCtrl, 1}, {keyLeftAlt, 1}, {keyF1 + 1, 2}}, 0},
		{"release", []ev{{keyLeftCtrl, 1}, {keyLeftAlt, 1}, {keyF1 + 1, 0}}, 0},
		{"modifiers held by repeat", []ev{{keyLeftCtrl, 2}, {keyLeftAlt, 2}, {keyF1 + 2, 1}}, 3},
	}
	for _, c := range cases {
		k := newVTKeys()
		got := 0
		for _, e := range c.evs {
			got = k.key(e.code, e.value)
		}
		if got != c.want {
			t.Errorf("%s: VT %d, want %d", c.name, got, c.want)
		}
	}
}

func TestEvioCGBitKey(t *testing.T) {
	// EVIOCGBIT(EV_KEY, 96) as linux/input.h computes it.
	if got := evioCGBitKey(96); got != 0x80604521 {
		t.Errorf("EVIOCGBIT(EV_KEY, 96) = %#x", got)
	}
}
