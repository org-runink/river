// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"sort"
	"strings"
)

// Region is a rectangle of the screen as fractions 0..1 (x0 y0 x1 y1). The zero Region is the
// whole screen, so a check never depends on the resolution QEMU picked.
type Region [4]float64

func (r Region) rect(b image.Rectangle) image.Rectangle {
	if r == (Region{}) {
		return b
	}
	w, h := float64(b.Dx()), float64(b.Dy())
	return image.Rect(b.Min.X+int(r[0]*w), b.Min.Y+int(r[1]*h), b.Min.X+int(r[2]*w), b.Min.Y+int(r[3]*h)).Intersect(b)
}

// Bound is an inclusive range; a missing end is open.
type Bound struct {
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

func (b Bound) ok(v float64) bool {
	return (b.Min == nil || v >= *b.Min) && (b.Max == nil || v <= *b.Max)
}

func (b Bound) String() string {
	lo, hi := "-inf", "+inf"
	if b.Min != nil {
		lo = fmt.Sprintf("%g", *b.Min)
	}
	if b.Max != nil {
		hi = fmt.Sprintf("%g", *b.Max)
	}
	return "[" + lo + ", " + hi + "]"
}

// Metrics of a region of a frame. Every one is a fraction or a statistic that does not depend on
// exact pixel values or on the screen's size, so a theme tweak or another resolution does not
// move it, while a black, flat or crashed screen moves all of them.
//
//	luma_mean     mean BT.601 luma, 0..1
//	entropy       Shannon entropy of the 8-bit luma histogram, bits (a flat or black frame is
//	              near 0, text on black about 1, a drawn scene above 3)
//	colors        distinct colours at 4 bits per channel (a gradient or a lit room has hundreds)
//	nonblack_frac pixels with luma >= 16
//	dark_frac     pixels with luma < 40
//	bright_frac   pixels with luma >= 200
//	warm_frac     lamp-light and wood pixels: R >= 110, R-G >= 8, G-B >= 8
//	paper_frac    paper and light windows: luma >= 190 and channel spread <= 40
//	red_frac      red marks (the clock counting the trace): R >= 150, G <= 90, B <= 90
func Measure(img image.Image, reg Region) map[string]float64 {
	rc := reg.rect(img.Bounds())
	n := rc.Dx() * rc.Dy()
	m := map[string]float64{}
	if n <= 0 {
		return m
	}
	var hist [256]int
	var sum float64
	var nonblack, dark, bright, warm, paper, red int
	seen := map[uint16]struct{}{}
	for y := rc.Min.Y; y < rc.Max.Y; y++ {
		for x := rc.Min.X; x < rc.Max.X; x++ {
			r, g, b := rgb8(img.At(x, y))
			l := (299*int(r) + 587*int(g) + 114*int(b)) / 1000
			hist[l]++
			sum += float64(l)
			if l >= 16 {
				nonblack++
			}
			if l < 40 {
				dark++
			}
			if l >= 200 {
				bright++
			}
			if r >= 110 && int(r)-int(g) >= 8 && int(g)-int(b) >= 8 {
				warm++
			}
			hi, lo := max3(r, g, b), min3(r, g, b)
			if l >= 190 && int(hi)-int(lo) <= 40 {
				paper++
			}
			if r >= 150 && g <= 90 && b <= 90 {
				red++
			}
			seen[uint16(r>>4)<<8|uint16(g>>4)<<4|uint16(b>>4)] = struct{}{}
		}
	}
	f := float64(n)
	var ent float64
	for _, c := range hist {
		if c > 0 {
			p := float64(c) / f
			ent -= p * math.Log2(p)
		}
	}
	m["luma_mean"] = sum / f / 255
	m["entropy"] = ent
	m["colors"] = float64(len(seen))
	m["nonblack_frac"] = float64(nonblack) / f
	m["dark_frac"] = float64(dark) / f
	m["bright_frac"] = float64(bright) / f
	m["warm_frac"] = float64(warm) / f
	m["paper_frac"] = float64(paper) / f
	m["red_frac"] = float64(red) / f
	return m
}

// ChangedFrac is the fraction of the region's pixels that differ between two frames by more than
// 24 in some channel (so a clock hand or a blinking cursor is noise, a window opening is not).
// Frames of different sizes are compared on the smaller one.
func ChangedFrac(a, b image.Image, reg Region) float64 {
	ab, bb := a.Bounds(), b.Bounds()
	w, h := min(ab.Dx(), bb.Dx()), min(ab.Dy(), bb.Dy())
	rc := reg.rect(image.Rect(0, 0, w, h))
	n := rc.Dx() * rc.Dy()
	if n <= 0 {
		return 0
	}
	ch := 0
	for y := rc.Min.Y; y < rc.Max.Y; y++ {
		for x := rc.Min.X; x < rc.Max.X; x++ {
			r1, g1, b1 := rgb8(a.At(ab.Min.X+x, ab.Min.Y+y))
			r2, g2, b2 := rgb8(b.At(bb.Min.X+x, bb.Min.Y+y))
			if absd(r1, r2) > 24 || absd(g1, g2) > 24 || absd(b1, b2) > 24 {
				ch++
			}
		}
	}
	return float64(ch) / float64(n)
}

func absd(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}
func max3(a, b, c uint8) uint8 { return max(a, max(b, c)) }
func min3(a, b, c uint8) uint8 { return min(a, min(b, c)) }
func rgb8(c color.Color) (uint8, uint8, uint8) {
	r, g, b, _ := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

// Evaluate checks metrics against bounds and says what failed, in a stable order.
func Evaluate(m map[string]float64, want map[string]Bound) (bool, string) {
	names := make([]string, 0, len(want))
	for k := range want {
		names = append(names, k)
	}
	sort.Strings(names)
	var bad, good []string
	for _, k := range names {
		v, ok := m[k]
		if !ok {
			bad = append(bad, fmt.Sprintf("%s: unknown metric", k))
		} else if !want[k].ok(v) {
			bad = append(bad, fmt.Sprintf("%s=%.4g outside %s", k, v, want[k]))
		} else {
			good = append(good, fmt.Sprintf("%s=%.4g", k, v))
		}
	}
	if len(bad) > 0 {
		return false, strings.Join(bad, "; ")
	}
	return true, strings.Join(good, " ")
}

// Presets are the expectations the harness shares between the default run and the checklist, so
// one number lives in one place. They are first-run values for a CPU-drawn 2D Fallback room and
// are meant to be tuned from the first real boot's frames (docs/DESK-PROOF.md).
var Presets = map[string]map[string]Bound{
	// the login screen: a dark wallpaper with something drawn on it, not a black placeholder
	"greeter": {"luma_mean": bnd(0.02, 0.6), "entropy": bnd(1.0, -1), "colors": bnd(12, -1), "dark_frac": bnd(0.25, -1)},
	// the room while it is being traced: dark, but alive (lamp, clock) and not a flat black
	"dark-alive": {"entropy": bnd(0.4, -1), "colors": bnd(8, -1), "dark_frac": bnd(0.3, -1), "nonblack_frac": bnd(0.01, -1)},
	// the lit room: wood and lamp light (warm), a drawn scene (entropy, colours), not dim
	"lit": {"luma_mean": bnd(0.1, -1), "entropy": bnd(3.0, -1), "colors": bnd(150, -1), "warm_frac": bnd(0.05, -1)},
}

func bnd(lo, hi float64) Bound {
	var b Bound
	if lo >= 0 {
		b.Min = &lo
	}
	if hi >= 0 {
		b.Max = &hi
	}
	return b
}

// --- image files -------------------------------------------------------------------------------

func LoadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return img, nil
}

// ReadPPM decodes a binary PPM (P6, 8 bit), what QEMU's screendump writes.
func ReadPPM(r io.Reader) (image.Image, error) {
	br := bufio.NewReader(r)
	var magic string
	var w, h, maxv int
	if _, err := fmt.Fscan(br, &magic, &w, &h, &maxv); err != nil {
		return nil, fmt.Errorf("ppm header: %w", err)
	}
	if magic != "P6" || maxv != 255 || w <= 0 || h <= 0 || w*h > 64<<20 {
		return nil, fmt.Errorf("ppm: unsupported header %s %dx%d max %d", magic, w, h, maxv)
	}
	if _, err := br.ReadByte(); err != nil { // the single whitespace after maxval
		return nil, err
	}
	pix := make([]byte, w*h*3)
	if _, err := io.ReadFull(br, pix); err != nil {
		return nil, fmt.Errorf("ppm pixels: %w", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = pix[i*3], pix[i*3+1], pix[i*3+2], 255
	}
	return img, nil
}

// PPMFileToPNG converts a PPM file to a PNG file.
func PPMFileToPNG(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	img, err := ReadPPM(f)
	if err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := png.Encode(out, img); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
