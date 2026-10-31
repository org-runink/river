// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// encodePNG encodes img as tightly as image/png can.
func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// padPNG grows a PNG to exactly size bytes with a tEXt chunk before IEND. The result is still a
// valid PNG with the same pixels: padding changes its size, never what it shows.
func padPNG(t *testing.T, p []byte, size int) []byte {
	t.Helper()
	const iend = 12 // length, "IEND", crc
	data := []byte("Comment\x00")
	n := size - len(p) - 12 - len(data)
	if n < 0 {
		t.Fatalf("PNG is %d bytes, cannot pad it to %d", len(p), size)
	}
	data = append(data, bytes.Repeat([]byte{'x'}, n)...)
	var c bytes.Buffer
	_ = binary.Write(&c, binary.BigEndian, uint32(len(data)))
	c.WriteString("tEXt")
	c.Write(data)
	_ = binary.Write(&c, binary.BigEndian, crc32.ChecksumIEEE(append([]byte("tEXt"), data...)))
	out := append(append(append([]byte{}, p[:len(p)-iend]...), c.Bytes()...), p[len(p)-iend:]...)
	if len(out) != size {
		t.Fatalf("padded to %d bytes, want %d", len(out), size)
	}
	return out
}

// placeholder is QEMU's "Guest has not initialized the display": a line of light text on black,
// 640x480, 752 bytes. Its grey mean is above zero, which is how it passed the old mean check.
func placeholder(t *testing.T) []byte {
	img := image.NewPaletted(image.Rect(0, 0, 640, 480), color.Palette{color.Black, color.Gray{Y: 0xaa}})
	for x := 160; x < 480; x++ {
		for y := 236; y < 244; y++ {
			if (x/3+y)%4 != 0 { // glyph-like strokes
				img.SetColorIndex(x, y, 1)
			}
		}
	}
	return padPNG(t, encodePNG(t, img), 752)
}

// drawn is a real-size screen: a dark gradient (a wallpaper) under varied detail (windows, text).
func drawn(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1280, 800))
	r := rand.New(rand.NewSource(1))
	for y := 0; y < 800; y++ {
		for x := 0; x < 1280; x++ {
			g := uint8(0x21 + (x+y)*0x30/2080)
			if (x/40+y/40)%5 == 0 {
				g = uint8(r.Intn(256))
			}
			img.Set(x, y, color.RGBA{g, g + uint8(x%7), g, 0xff})
		}
	}
	return encodePNG(t, img)
}

func writeShot(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestScreenRejectsQEMUsPlaceholder(t *testing.T) {
	p := writeShot(t, "placeholder.png", placeholder(t))
	f, err := ScreenCheck(p, screenMinBytes, screenMinBits)
	if f.Bytes != 752 || f.Width != 640 || f.Height != 480 {
		t.Fatalf("fixture is not the 752-byte 640x480 placeholder: %+v", f)
	}
	if err == nil {
		t.Fatalf("the placeholder passed the gate: %+v", f)
	}
	for _, want := range []string{"752 bytes", "luma entropy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestScreenPassesADrawnScreen(t *testing.T) {
	p := writeShot(t, "desk.png", drawn(t))
	f, err := ScreenCheck(p, screenMinBytes, screenMinBits)
	if err != nil {
		t.Fatalf("a drawn screen failed: %v", err)
	}
	if f.Bytes < screenMinBytes || f.Bits < 2 {
		t.Fatalf("fixture is not a real-size screen: %+v", f)
	}
}

// Each rule fails on its own: a black frame padded past the size floor is still blank.
func TestScreenEntropyRuleStandsAlone(t *testing.T) {
	black := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 1280, 800)))
	p := writeShot(t, "black-padded.png", padPNG(t, black, screenMinBytes+1024))
	_, err := ScreenCheck(p, screenMinBytes, screenMinBits)
	if err == nil || !strings.Contains(err.Error(), "luma entropy") || strings.Contains(err.Error(), "bytes <") {
		t.Fatalf("a large blank frame: got %v, want only the entropy rule", err)
	}
}

func TestScreenRejectsNonPNGAndCountsEveryFile(t *testing.T) {
	bad := writeShot(t, "screen.png", []byte("not a png"))
	good := writeShot(t, "desk.png", drawn(t))
	var out, errw bytes.Buffer
	err := Screens([]string{good, bad}, screenMinBytes, screenMinBits, &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "1 of 2") {
		t.Fatalf("got %v, want 1 of 2 failed", err)
	}
	if !strings.Contains(errw.String(), "not a PNG") || !strings.Contains(out.String(), "desk.png") {
		t.Fatalf("out=%q err=%q", out.String(), errw.String())
	}
	if err := Screens([]string{good}, screenMinBytes, screenMinBits, io.Discard, io.Discard); err != nil {
		t.Fatalf("one drawn screen: %v", err)
	}
}
