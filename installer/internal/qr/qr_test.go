// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package qr

import (
	"strings"
	"testing"
)

func TestFormatBits(t *testing.T) {
	// ISO/IEC 18004 Table C.1: level M, masks 0..7.
	want := []int{0x5412, 0x5125, 0x5E7C, 0x5B4B, 0x45F9, 0x40CE, 0x4F97, 0x4AA0}
	for m, w := range want {
		if got := formatBits(m); got != w {
			t.Errorf("formatBits(%d) = %#x, want %#x", m, got, w)
		}
	}
}

func TestVersionBits(t *testing.T) {
	// Annex D: version 7 = 0x07C94, version 10 = 0x0A4D3.
	if got := versionBits(7); got != 0x07C94 {
		t.Errorf("versionBits(7) = %#x", got)
	}
	if got := versionBits(10); got != 0x0A4D3 {
		t.Errorf("versionBits(10) = %#x", got)
	}
}

func TestRSRemainder(t *testing.T) {
	// The worked example of ISO/IEC 18004 Annex I ("01234567", version 1-M).
	data := []byte{0x10, 0x20, 0x0C, 0x56, 0x61, 0x80, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11, 0xEC, 0x11}
	want := []byte{0xA5, 0x24, 0xD4, 0xC1, 0xED, 0x36, 0xC7, 0x87, 0x2C, 0x55}
	got := rsRemainder(data, rsGenerator(10))
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("EC codewords %x, want %x", got, want)
		}
	}
}

func TestVersionsAndStructure(t *testing.T) {
	for _, tc := range []struct {
		n, version int
	}{{1, 1}, {14, 1}, {15, 2}, {64, 5}, {71, 5}, {84, 5}, {85, 6}, {213, 10}} {
		c, err := Encode([]byte(strings.Repeat("A", tc.n)))
		if err != nil {
			t.Fatalf("%d bytes: %v", tc.n, err)
		}
		if c.Version != tc.version || c.Size != 17+4*tc.version || len(c.Rows()) != c.Size {
			t.Errorf("%d bytes: version %d size %d, want version %d", tc.n, c.Version, c.Size, tc.version)
		}
		// Finder pattern corners and the dark module.
		m := c.Modules
		if !m[0][0] || !m[0][6] || !m[6][0] || m[1][1] || !m[3][3] || !m[c.Size-8][8] {
			t.Errorf("%d bytes: finder/dark module wrong", tc.n)
		}
	}
	if _, err := Encode(make([]byte, 214)); err != ErrTooLong {
		t.Fatalf("214 bytes: %v, want ErrTooLong", err)
	}
}
