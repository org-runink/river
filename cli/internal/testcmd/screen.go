// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

package testcmd

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"

	"github.com/spf13/cobra"
)

// screen judges a QEMU screendump: did the guest draw anything?
//
// WHY NOT THE MEAN. The VM gates used the grey mean of a screendump (magick %[fx:mean]): a
// black screen is 0. On 2026-10-30 a frame of QEMU's own "Guest has not initialized the
// display" placeholder passed that check -- white text on black has a mean above 0 -- although
// the guest had drawn nothing at all. That placeholder is a 752-byte PNG. So the gate asks two
// things the placeholder, a black screen and a text console all fail, and a drawn desktop,
// greeter or installer page passes:
//
//	bytes  the PNG's size. A drawn 1280x800 screen is tens or hundreds of KiB; QEMU's
//	       placeholder is 752 bytes and a black frame a few KiB.
//	bits   the Shannon entropy of the frame's 8-bit luma histogram. Text on black is a fraction
//	       of a bit; a wallpaper, a greeter or the installer's page is well above one.
//
// Both must pass: a large file can still be flat (padding, metadata), and a small one cannot
// be a drawn screen whatever its pixels are.
const (
	screenMinBytes = 16 << 10
	screenMinBits  = 1.0
)

// ScreenFacts is what the gate measured in one screendump.
type ScreenFacts struct {
	Bytes         int64
	Width, Height int
	Bits          float64
}

// ScreenCheck measures the PNG at path and returns an error naming every rule it breaks.
func ScreenCheck(path string, minBytes int64, minBits float64) (ScreenFacts, error) {
	var f ScreenFacts
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	f.Bytes = int64(len(b))
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return f, fmt.Errorf("%s: not a PNG (%v)", path, err)
	}
	r := img.Bounds()
	f.Width, f.Height = r.Dx(), r.Dy()
	f.Bits = lumaEntropy(img)
	var why []string
	if f.Bytes < minBytes {
		why = append(why, fmt.Sprintf("%d bytes < %d (QEMU's no-display placeholder is 752)", f.Bytes, minBytes))
	}
	if f.Bits < minBits {
		why = append(why, fmt.Sprintf("luma entropy %.2f bits < %.2f (a blank screen or bare text)", f.Bits, minBits))
	}
	if len(why) > 0 {
		return f, fmt.Errorf("%s: nothing drawn: %v", path, why)
	}
	return f, nil
}

// lumaEntropy is the entropy, in bits, of the image's 256-bin luma (BT.601) histogram.
func lumaEntropy(img image.Image) float64 {
	var hist [256]int
	r := img.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			hist[(299*cr+587*cg+114*cb)/1000>>8]++
		}
	}
	n := float64(r.Dx() * r.Dy())
	h := 0.0
	for _, c := range hist {
		if c > 0 {
			p := float64(c) / n
			h -= p * math.Log2(p)
		}
	}
	return h
}

func screenCmd() *cobra.Command {
	var minBytes int64
	var minBits float64
	c := &cobra.Command{
		Use:   "screen <png>...",
		Short: "the VM gates' screenshot check: a screendump shows something drawn (size and entropy, not the mean)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return Screens(args, minBytes, minBits, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	c.Flags().Int64Var(&minBytes, "min-bytes", screenMinBytes, "smallest PNG that can be a drawn screen")
	c.Flags().Float64Var(&minBits, "min-bits", screenMinBits, "lowest luma entropy, in bits, of a drawn screen")
	return c
}

// Screens checks every PNG and fails when any of them shows nothing drawn.
func Screens(paths []string, minBytes int64, minBits float64, outw, errw io.Writer) error {
	c := checks{out: outw, err: errw}
	for _, p := range paths {
		f, err := ScreenCheck(p, minBytes, minBits)
		if err != nil {
			c.ko(err.Error())
			continue
		}
		c.ok(fmt.Sprintf("%s: %d bytes, %dx%d, luma entropy %.2f bits", p, f.Bytes, f.Width, f.Height, f.Bits))
	}
	if c.failed > 0 {
		return fmt.Errorf("screen: %d of %d screendump(s) show nothing drawn", c.failed, c.n)
	}
	return nil
}
