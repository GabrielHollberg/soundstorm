// Package qr draws a QR code: enough of ISO/IEC 18004 to put an address on a
// screen for a phone to scan - byte mode, error correction level M, versions
// 1 to 10 (up to 213 bytes). A TV signing in shows one, so somebody can sign
// it in from their phone instead of typing on a remote.
//
// No dependency, as everywhere in this module. The layout follows the
// standard as Project Nayuki's reference implementation reads it; the test
// draws codes of every version and has them read back.
package qr

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
)

// ErrTooLong is returned for text past what version 10 holds at level M.
var ErrTooLong = errors.New("qr: text too long")

// Code is a drawn QR code: Size modules square, true for dark.
type Code struct {
	Size    int
	modules [][]bool
}

// Dark reports whether the module at column x, row y is dark.
func (c *Code) Dark(x, y int) bool { return c.modules[y][x] }

// block is a version's layout at level M: how many codewords of error
// correction each block has, and how many blocks of each data length.
type blocks struct {
	ec     int
	groups [][2]int // {count, data codewords per block}
}

var levelM = [11]blocks{
	1:  {10, [][2]int{{1, 16}}},
	2:  {16, [][2]int{{1, 28}}},
	3:  {26, [][2]int{{1, 44}}},
	4:  {18, [][2]int{{2, 32}}},
	5:  {24, [][2]int{{2, 43}}},
	6:  {16, [][2]int{{4, 27}}},
	7:  {18, [][2]int{{4, 31}}},
	8:  {22, [][2]int{{2, 38}, {2, 39}}},
	9:  {22, [][2]int{{3, 36}, {2, 37}}},
	10: {26, [][2]int{{4, 43}, {1, 44}}},
}

var alignment = [11][]int{
	2: {6, 18}, 3: {6, 22}, 4: {6, 26}, 5: {6, 30}, 6: {6, 34},
	7: {6, 22, 38}, 8: {6, 24, 42}, 9: {6, 26, 46}, 10: {6, 28, 50},
}

func (b blocks) data() int {
	n := 0
	for _, g := range b.groups {
		n += g[0] * g[1]
	}
	return n
}

// Encode draws text in the smallest version that holds it.
func Encode(text string) (*Code, error) {
	payload := []byte(text)
	version := 0
	for v := 1; v <= 10; v++ {
		countBits := 8
		if v >= 10 {
			countBits = 16
		}
		if 4+countBits+8*len(payload) <= 8*levelM[v].data() {
			version = v
			break
		}
	}
	if version == 0 {
		return nil, ErrTooLong
	}
	data := encodeData(payload, version)
	codewords := interleave(data, levelM[version])

	q := newGrid(version)
	q.drawFunctions(version)
	q.drawCodewords(codewords)
	best, bestPenalty := 0, -1
	for mask := 0; mask < 8; mask++ {
		q.applyMask(mask)
		q.drawFormat(mask)
		if p := q.penalty(); bestPenalty < 0 || p < bestPenalty {
			best, bestPenalty = mask, p
		}
		q.applyMask(mask) // undone: the mask is its own inverse
	}
	q.applyMask(best)
	q.drawFormat(best)
	return &Code{Size: q.size, modules: q.dark}, nil
}

// encodeData is the byte-mode bit stream, terminated and padded.
func encodeData(payload []byte, version int) []byte {
	var bits []bool
	put := func(v, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, v>>i&1 == 1)
		}
	}
	put(0b0100, 4)
	if version >= 10 {
		put(len(payload), 16)
	} else {
		put(len(payload), 8)
	}
	for _, b := range payload {
		put(int(b), 8)
	}
	capacity := 8 * levelM[version].data()
	for i := 0; i < 4 && len(bits) < capacity; i++ {
		bits = append(bits, false)
	}
	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}
	for pad := 0; len(bits) < capacity; pad++ {
		put([]int{0xEC, 0x11}[pad%2], 8)
	}
	out := make([]byte, len(bits)/8)
	for i, b := range bits {
		if b {
			out[i/8] |= 0x80 >> (i % 8)
		}
	}
	return out
}

// interleave splits the data into its blocks, adds each block's error
// correction, and takes them a codeword from each block in turn.
func interleave(data []byte, layout blocks) []byte {
	var dataBlocks, ecBlocks [][]byte
	for _, g := range layout.groups {
		for i := 0; i < g[0]; i++ {
			d := data[:g[1]]
			data = data[g[1]:]
			dataBlocks = append(dataBlocks, d)
			ecBlocks = append(ecBlocks, reedSolomon(d, layout.ec))
		}
	}
	var out []byte
	for i := 0; ; i++ {
		any := false
		for _, d := range dataBlocks {
			if i < len(d) {
				out = append(out, d[i])
				any = true
			}
		}
		if !any {
			break
		}
	}
	for i := 0; i < layout.ec; i++ {
		for _, e := range ecBlocks {
			out = append(out, e[i])
		}
	}
	return out
}

// --- Reed-Solomon over GF(256), polynomial 0x11D ---------------------------------

func gfMul(a, b byte) byte {
	var r byte
	for b != 0 {
		if b&1 != 0 {
			r ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1D
		}
		b >>= 1
	}
	return r
}

func reedSolomon(data []byte, degree int) []byte {
	// The generator: the product of (x - 2^i) for i below degree.
	gen := make([]byte, degree)
	gen[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := range gen {
			gen[j] = gfMul(gen[j], root)
			if j+1 < len(gen) {
				gen[j] ^= gen[j+1]
			}
		}
		root = gfMul(root, 2)
	}
	rem := make([]byte, degree)
	for _, b := range data {
		factor := b ^ rem[0]
		copy(rem, rem[1:])
		rem[degree-1] = 0
		for i := range rem {
			rem[i] ^= gfMul(gen[i], factor)
		}
	}
	return rem
}

// --- the grid ----------------------------------------------------------------

type grid struct {
	size     int
	dark     [][]bool
	function [][]bool
}

func newGrid(version int) *grid {
	size := version*4 + 17
	g := &grid{size: size, dark: make([][]bool, size), function: make([][]bool, size)}
	for i := range g.dark {
		g.dark[i] = make([]bool, size)
		g.function[i] = make([]bool, size)
	}
	return g
}

func (g *grid) setFunction(x, y int, dark bool) {
	g.dark[y][x] = dark
	g.function[y][x] = true
}

func (g *grid) drawFunctions(version int) {
	// Timing patterns.
	for i := 0; i < g.size; i++ {
		g.setFunction(6, i, i%2 == 0)
		g.setFunction(i, 6, i%2 == 0)
	}
	// Finders, with their light separators.
	for _, c := range [][2]int{{3, 3}, {g.size - 4, 3}, {3, g.size - 4}} {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := c[0]+dx, c[1]+dy
				if x < 0 || y < 0 || x >= g.size || y >= g.size {
					continue
				}
				d := max(abs(dx), abs(dy))
				g.setFunction(x, y, d != 2 && d != 4)
			}
		}
	}
	// Alignment patterns, except where a finder is.
	pos := alignment[version]
	for i, cx := range pos {
		for j, cy := range pos {
			if (i == 0 && j == 0) || (i == 0 && j == len(pos)-1) || (i == len(pos)-1 && j == 0) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					g.setFunction(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	// Reserved for the format, drawn for real once the mask is chosen.
	g.drawFormat(0)
	if version >= 7 {
		rem := version
		for i := 0; i < 12; i++ {
			rem = rem<<1 ^ (rem>>11)*0x1F25
		}
		bits := version<<12 | rem
		for i := 0; i < 18; i++ {
			dark := bits>>i&1 == 1
			a, b := g.size-11+i%3, i/3
			g.setFunction(a, b, dark)
			g.setFunction(b, a, dark)
		}
	}
}

// drawFormat writes the level (M, whose bits are 00) and mask, twice.
func (g *grid) drawFormat(mask int) {
	data := mask // level M's two bits are zero
	rem := data
	for i := 0; i < 10; i++ {
		rem = rem<<1 ^ (rem>>9)*0x537
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return bits>>i&1 == 1 }
	for i := 0; i <= 5; i++ {
		g.setFunction(8, i, bit(i))
	}
	g.setFunction(8, 7, bit(6))
	g.setFunction(8, 8, bit(7))
	g.setFunction(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		g.setFunction(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		g.setFunction(g.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		g.setFunction(8, g.size-15+i, bit(i))
	}
	g.setFunction(8, g.size-8, true) // always dark
}

// drawCodewords lays the bits up and down column pairs from the bottom
// right, skipping the vertical timing pattern.
func (g *grid) drawCodewords(data []byte) {
	i := 0
	for right := g.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < g.size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = g.size - 1 - vert
				}
				if g.function[y][x] || i >= len(data)*8 {
					continue
				}
				g.dark[y][x] = data[i>>3]>>(7-i&7)&1 == 1
				i++
			}
		}
	}
}

func (g *grid) applyMask(mask int) {
	for y := 0; y < g.size; y++ {
		for x := 0; x < g.size; x++ {
			if g.function[y][x] {
				continue
			}
			var flip bool
			switch mask {
			case 0:
				flip = (x+y)%2 == 0
			case 1:
				flip = y%2 == 0
			case 2:
				flip = x%3 == 0
			case 3:
				flip = (x+y)%3 == 0
			case 4:
				flip = (x/3+y/2)%2 == 0
			case 5:
				flip = x*y%2+x*y%3 == 0
			case 6:
				flip = (x*y%2+x*y%3)%2 == 0
			case 7:
				flip = ((x+y)%2+x*y%3)%2 == 0
			}
			if flip {
				g.dark[y][x] = !g.dark[y][x]
			}
		}
	}
}

// penalty scores how hard a mask makes the code to read: long runs, blocks
// of one colour, things that look like a finder, and too much of one colour.
func (g *grid) penalty() int {
	n := g.size
	p := 0
	at := func(x, y int, rows bool) bool {
		if rows {
			return g.dark[y][x]
		}
		return g.dark[x][y]
	}
	for _, rows := range []bool{true, false} {
		for a := 0; a < n; a++ {
			run := 1
			for b := 1; b < n; b++ {
				if at(b, a, rows) == at(b-1, a, rows) {
					run++
					continue
				}
				if run >= 5 {
					p += run - 2
				}
				run = 1
			}
			if run >= 5 {
				p += run - 2
			}
			// 1:1:3:1:1 with four light either side.
			for b := 0; b+10 < n; b++ {
				pattern := [11]bool{}
				for k := range pattern {
					pattern[k] = at(b+k, a, rows)
				}
				if pattern == [11]bool{true, false, true, true, true, false, true, false, false, false, false} ||
					pattern == [11]bool{false, false, false, false, true, false, true, true, true, false, true} {
					p += 40
				}
			}
		}
	}
	dark := 0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if g.dark[y][x] {
				dark++
			}
			if x+1 < n && y+1 < n {
				c := g.dark[y][x]
				if g.dark[y][x+1] == c && g.dark[y+1][x] == c && g.dark[y+1][x+1] == c {
					p += 3
				}
			}
		}
	}
	percent := dark * 100 / (n * n)
	p += abs(percent-50) / 5 * 10
	return p
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// PNG draws the code at scale pixels a module, with the four-module light
// border scanners need.
func (c *Code) PNG(scale int) ([]byte, error) {
	const border = 4
	side := (c.Size + 2*border) * scale
	img := image.NewGray(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if !c.Dark(x, y) {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetGray((x+border)*scale+dx, (y+border)*scale+dy, color.Gray{})
				}
			}
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
