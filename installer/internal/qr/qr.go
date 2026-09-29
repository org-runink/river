// SPDX-FileCopyrightText: 2026 The RIVER Authors
// SPDX-License-Identifier: MIT

// Package qr encodes short byte strings as QR codes (ISO/IEC 18004), standard library only.
//
// It covers what the graphical installer needs and no more: byte mode, error correction
// level M, versions 1 to 10 (up to 213 bytes), all eight masks scored by the standard
// penalty rules. The installer draws the recovery key with it, so the key never leaves the
// machine to be rendered: the matrix goes to the local UI, which paints it as SVG.
package qr

import "errors"

// ErrTooLong is returned for input that does not fit version 10 at level M.
var ErrTooLong = errors.New("qr: data too long for version 10-M")

// Code is a square matrix of modules; true is dark. Size = 17 + 4*Version. The quiet zone
// (4 modules on every side) is not included.
type Code struct {
	Version int
	Size    int
	Modules [][]bool
}

// block layout for level M: {blocks, data codewords} groups and EC codewords per block.
type ecInfo struct {
	ecPerBlock int
	groups     [][2]int // {number of blocks, data codewords per block}
}

var levelM = [11]ecInfo{
	{},
	{10, [][2]int{{1, 16}}},
	{16, [][2]int{{1, 28}}},
	{26, [][2]int{{1, 44}}},
	{18, [][2]int{{2, 32}}},
	{24, [][2]int{{2, 43}}},
	{16, [][2]int{{4, 27}}},
	{18, [][2]int{{4, 31}}},
	{22, [][2]int{{2, 38}, {2, 39}}},
	{22, [][2]int{{3, 36}, {2, 37}}},
	{26, [][2]int{{4, 43}, {1, 44}}},
}

var alignment = [11][]int{
	{}, {}, {6, 18}, {6, 22}, {6, 26}, {6, 30}, {6, 34},
	{6, 22, 38}, {6, 24, 42}, {6, 26, 46}, {6, 28, 50},
}

func dataCapacity(v int) int {
	n := 0
	for _, g := range levelM[v].groups {
		n += g[0] * g[1]
	}
	return n
}

// Encode returns the smallest level-M QR code (version 1..10) holding data in byte mode.
func Encode(data []byte) (*Code, error) {
	for v := 1; v <= 10; v++ {
		countBits := 8
		if v >= 10 {
			countBits = 16
		}
		need := 4 + countBits + 8*len(data)
		if need <= 8*dataCapacity(v) {
			return build(v, data, countBits), nil
		}
	}
	return nil, ErrTooLong
}

type bitBuf struct {
	b []byte
	n int
}

func (w *bitBuf) put(val uint, bits int) {
	for i := bits - 1; i >= 0; i-- {
		if w.n%8 == 0 {
			w.b = append(w.b, 0)
		}
		if val>>uint(i)&1 == 1 {
			w.b[len(w.b)-1] |= 0x80 >> uint(w.n%8)
		}
		w.n++
	}
}

func build(v int, data []byte, countBits int) *Code {
	capBytes := dataCapacity(v)
	var w bitBuf
	w.put(0x4, 4) // byte mode
	w.put(uint(len(data)), countBits)
	for _, c := range data {
		w.put(uint(c), 8)
	}
	// Terminator (up to 4 zero bits), pad to a byte, then the 0xEC/0x11 pad codewords.
	t := 8*capBytes - w.n
	if t > 4 {
		t = 4
	}
	w.put(0, t)
	if w.n%8 != 0 {
		w.put(0, 8-w.n%8)
	}
	for i := 0; len(w.b) < capBytes; i++ {
		if i%2 == 0 {
			w.b = append(w.b, 0xEC)
		} else {
			w.b = append(w.b, 0x11)
		}
	}
	codewords := interleave(v, w.b)

	size := 17 + 4*v
	c := &Code{Version: v, Size: size, Modules: make([][]bool, size)}
	fn := make([][]bool, size) // function-pattern modules: never masked or overwritten
	for i := range c.Modules {
		c.Modules[i] = make([]bool, size)
		fn[i] = make([]bool, size)
	}
	placeFunctionPatterns(c, fn)
	placeData(c, fn, codewords)

	best, bestScore := -1, 0
	for m := 0; m < 8; m++ {
		t := clone(c)
		applyMask(t, fn, m)
		placeFormat(t, m)
		if s := penalty(t); best < 0 || s < bestScore {
			best, bestScore = m, s
		}
	}
	applyMask(c, fn, best)
	placeFormat(c, best)
	return c
}

func clone(c *Code) *Code {
	t := &Code{Version: c.Version, Size: c.Size, Modules: make([][]bool, c.Size)}
	for i := range c.Modules {
		t.Modules[i] = append([]bool(nil), c.Modules[i]...)
	}
	return t
}

// interleave splits data into the version's blocks, appends each block's Reed-Solomon
// codewords and interleaves data then EC codewords column by column.
func interleave(v int, data []byte) []byte {
	info := levelM[v]
	var blocks [][]byte
	off := 0
	for _, g := range info.groups {
		for i := 0; i < g[0]; i++ {
			blocks = append(blocks, data[off:off+g[1]])
			off += g[1]
		}
	}
	gen := rsGenerator(info.ecPerBlock)
	ecs := make([][]byte, len(blocks))
	maxData := 0
	for i, b := range blocks {
		ecs[i] = rsRemainder(b, gen)
		if len(b) > maxData {
			maxData = len(b)
		}
	}
	var out []byte
	for i := 0; i < maxData; i++ {
		for _, b := range blocks {
			if i < len(b) {
				out = append(out, b[i])
			}
		}
	}
	for i := 0; i < info.ecPerBlock; i++ {
		for _, e := range ecs {
			out = append(out, e[i])
		}
	}
	return out
}

// --- GF(256) with the QR polynomial x^8+x^4+x^3+x^2+1 ------------------------------------

var gfExp [512]byte
var gfLog [256]int

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[gfLog[a]+gfLog[b]]
}

// rsGenerator returns the generator polynomial of the given degree, highest term first,
// without its leading 1.
func rsGenerator(degree int) []byte {
	g := []byte{1}
	for i := 0; i < degree; i++ {
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= gfMul(c, gfExp[i])
		}
		g = next
	}
	return g[1:]
}

func rsRemainder(data, gen []byte) []byte {
	rem := make([]byte, len(gen))
	for _, d := range data {
		f := d ^ rem[0]
		copy(rem, rem[1:])
		rem[len(rem)-1] = 0
		for i, g := range gen {
			rem[i] ^= gfMul(g, f)
		}
	}
	return rem
}

// --- matrix -------------------------------------------------------------------------------

func set(c *Code, fn [][]bool, r, col int, dark bool) {
	c.Modules[r][col] = dark
	fn[r][col] = true
}

func placeFunctionPatterns(c *Code, fn [][]bool) {
	n := c.Size
	finder := func(r0, c0 int) {
		for dr := -1; dr <= 7; dr++ {
			for dc := -1; dc <= 7; dc++ {
				r, col := r0+dr, c0+dc
				if r < 0 || r >= n || col < 0 || col >= n {
					continue
				}
				dark := dr >= 0 && dr <= 6 && dc >= 0 && dc <= 6 &&
					(dr == 0 || dr == 6 || dc == 0 || dc == 6 || (dr >= 2 && dr <= 4 && dc >= 2 && dc <= 4))
				set(c, fn, r, col, dark)
			}
		}
	}
	finder(0, 0)
	finder(0, n-7)
	finder(n-7, 0)
	for i := 8; i < n-8; i++ { // timing patterns
		set(c, fn, 6, i, i%2 == 0)
		set(c, fn, i, 6, i%2 == 0)
	}
	pos := alignment[c.Version]
	for i, r := range pos {
		for j, col := range pos {
			last := len(pos) - 1
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) { // a finder
				continue
			}
			for dr := -2; dr <= 2; dr++ {
				for dc := -2; dc <= 2; dc++ {
					d := dr == -2 || dr == 2 || dc == -2 || dc == 2 || (dr == 0 && dc == 0)
					set(c, fn, r+dr, col+dc, d)
				}
			}
		}
	}
	// Reserve the format areas (written per mask later) and the dark module.
	for i := 0; i < 9; i++ {
		fn[8][i], fn[i][8] = true, true
	}
	for i := 0; i < 8; i++ {
		fn[8][n-1-i], fn[n-1-i][8] = true, true
	}
	set(c, fn, n-8, 8, true)
	if c.Version >= 7 {
		bits := versionBits(c.Version)
		for i := 0; i < 18; i++ {
			d := bits>>uint(i)&1 == 1
			a, b := i/3, n-11+i%3
			set(c, fn, a, b, d)
			set(c, fn, b, a, d)
		}
	}
}

func placeData(c *Code, fn [][]bool, cw []byte) {
	n := c.Size
	bit := 0
	total := len(cw) * 8
	up := true
	for right := n - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for i := 0; i < n; i++ {
			r := i
			if up {
				r = n - 1 - i
			}
			for dc := 0; dc < 2; dc++ {
				col := right - dc
				if fn[r][col] {
					continue
				}
				if bit < total {
					c.Modules[r][col] = cw[bit/8]>>uint(7-bit%8)&1 == 1
				}
				bit++
			}
		}
		up = !up
	}
}

func maskBit(m, r, c int) bool {
	switch m {
	case 0:
		return (r+c)%2 == 0
	case 1:
		return r%2 == 0
	case 2:
		return c%3 == 0
	case 3:
		return (r+c)%3 == 0
	case 4:
		return (r/2+c/3)%2 == 0
	case 5:
		return r*c%2+r*c%3 == 0
	case 6:
		return (r*c%2+r*c%3)%2 == 0
	default:
		return ((r+c)%2+r*c%3)%2 == 0
	}
}

func applyMask(c *Code, fn [][]bool, m int) {
	for r := 0; r < c.Size; r++ {
		for col := 0; col < c.Size; col++ {
			if !fn[r][col] && maskBit(m, r, col) {
				c.Modules[r][col] = !c.Modules[r][col]
			}
		}
	}
}

// formatBits returns the 15-bit format information for level M and mask m.
func formatBits(m int) int {
	data := (0x0 << 3) | m // level M = 00
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	return (data<<10 | rem) ^ 0x5412
}

func versionBits(v int) int {
	rem := v
	for i := 0; i < 12; i++ {
		rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
	}
	return v<<12 | rem
}

func placeFormat(c *Code, m int) {
	n := c.Size
	bits := formatBits(m)
	bit := func(i int) bool { return bits>>uint(i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		c.Modules[i][8] = bit(i)
	}
	c.Modules[7][8] = bit(6)
	c.Modules[8][8] = bit(7)
	c.Modules[8][7] = bit(8)
	for i := 9; i < 15; i++ {
		c.Modules[8][14-i] = bit(i)
	}
	for i := 0; i < 8; i++ {
		c.Modules[8][n-1-i] = bit(i)
	}
	for i := 8; i < 15; i++ {
		c.Modules[n-15+i][8] = bit(i)
	}
	c.Modules[n-8][8] = true
}

// penalty scores a masked matrix by the four rules of ISO/IEC 18004 §8.8.2.
func penalty(c *Code) int {
	n := c.Size
	p := 0
	at := func(r, col int, horiz bool) bool {
		if horiz {
			return c.Modules[r][col]
		}
		return c.Modules[col][r]
	}
	for _, horiz := range []bool{true, false} {
		for r := 0; r < n; r++ {
			run := 1
			for col := 1; col < n; col++ {
				if at(r, col, horiz) == at(r, col-1, horiz) {
					run++
				} else {
					if run >= 5 {
						p += run - 2
					}
					run = 1
				}
			}
			if run >= 5 {
				p += run - 2
			}
			// 1:1:3:1:1 finder-like patterns with 4 light modules on one side.
			for col := 0; col+10 < n; col++ {
				pat := [11]bool{true, false, true, true, true, false, true, false, false, false, false}
				m1, m2 := true, true
				for k := 0; k < 11; k++ {
					v := at(r, col+k, horiz)
					if v != pat[k] {
						m1 = false
					}
					if v != pat[10-k] {
						m2 = false
					}
				}
				if m1 {
					p += 40
				}
				if m2 {
					p += 40
				}
			}
		}
	}
	for r := 0; r+1 < n; r++ {
		for col := 0; col+1 < n; col++ {
			v := c.Modules[r][col]
			if v == c.Modules[r][col+1] && v == c.Modules[r+1][col] && v == c.Modules[r+1][col+1] {
				p += 3
			}
		}
	}
	dark := 0
	for r := 0; r < n; r++ {
		for col := 0; col < n; col++ {
			if c.Modules[r][col] {
				dark++
			}
		}
	}
	pct := dark * 100 / (n * n)
	d := pct - 50
	if d < 0 {
		d = -d
	}
	p += d / 5 * 10
	return p
}

// Rows renders the matrix as strings of '1' (dark) and '0', one per row, the form the
// installer's API hands to its UI.
func (c *Code) Rows() []string {
	out := make([]string, c.Size)
	for r, row := range c.Modules {
		b := make([]byte, c.Size)
		for i, d := range row {
			b[i] = '0'
			if d {
				b[i] = '1'
			}
		}
		out[r] = string(b)
	}
	return out
}
