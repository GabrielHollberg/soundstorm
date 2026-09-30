// Package flac decodes FLAC, just enough to hear a song's beats on the
// server: Navidrome converts any song to FLAC for it (a transcoding
// SoundStorm adds, subsonic/listen.go), and FLAC is simple enough to read
// with the standard library alone, where MP3 and AAC are not.
//
// It reads what ffmpeg writes - every subframe type, both residual codings,
// the three stereo decorrelations, wasted bits - and checks no CRCs: a
// damaged frame is an error, and for listening to a song that is enough.
package flac

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math/bits"
)

// ErrNotFLAC is a stream that does not start as FLAC does.
var ErrNotFLAC = errors.New("flac: not a FLAC stream")

// Info is what the stream says about itself.
type Info struct {
	SampleRate int
	Channels   int
	Bits       int // per sample
}

// Decode reads a FLAC stream, calling block with each frame's samples: one
// slice per channel, each value within Bits bits, signed. The slices are
// reused between calls.
func Decode(r io.Reader, block func(info Info, channels [][]int32) error) (Info, error) {
	br := &bitReader{r: bufio.NewReaderSize(r, 64<<10)}
	var magic [4]byte
	if _, err := io.ReadFull(br.r, magic[:]); err != nil {
		return Info{}, err
	}
	if string(magic[:]) != "fLaC" {
		return Info{}, ErrNotFLAC
	}
	var info Info
	for last := false; !last; {
		h, err := br.r.ReadByte()
		if err != nil {
			return info, err
		}
		last = h&0x80 != 0
		var l [3]byte
		if _, err := io.ReadFull(br.r, l[:]); err != nil {
			return info, err
		}
		n := int(l[0])<<16 | int(l[1])<<8 | int(l[2])
		body := make([]byte, n)
		if _, err := io.ReadFull(br.r, body); err != nil {
			return info, err
		}
		if h&0x7f == 0 && n >= 18 { // STREAMINFO
			info.SampleRate = int(body[10])<<12 | int(body[11])<<4 | int(body[12])>>4
			info.Channels = int(body[12]>>1&7) + 1
			info.Bits = int(body[12]&1)<<4 | int(body[13]>>4) + 1
		}
	}
	if info.SampleRate == 0 || info.Channels == 0 {
		return info, errors.New("flac: no stream information")
	}
	var bufs [][]int32
	for {
		ch, err := readFrame(br, &info, &bufs)
		if err == io.EOF {
			return info, nil
		}
		if err != nil {
			return info, err
		}
		if err := block(info, ch); err != nil {
			return info, err
		}
	}
}

var blockSizes = [16]int{0, 192, 576, 1152, 2304, 4608, -8, -16, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768}
var sampleRates = [12]int{0, 88200, 176400, 192000, 8000, 16000, 22050, 24000, 32000, 44100, 48000, 96000}
var sampleBits = [8]int{0, 8, 12, 0, 16, 20, 24, 32}

func readFrame(br *bitReader, info *Info, bufs *[][]int32) ([][]int32, error) {
	br.align()
	// Sync code: 14 bits of 11111111111110.
	b0, err := br.r.ReadByte()
	if err != nil {
		return nil, io.EOF
	}
	b1, err := br.r.ReadByte()
	if err != nil {
		return nil, io.EOF
	}
	if b0 != 0xFF || b1&0xFC != 0xF8 {
		return nil, fmt.Errorf("flac: lost frame sync")
	}
	h, err := br.bits(16)
	if err != nil {
		return nil, err
	}
	bsCode, srCode := int(h>>12), int(h>>8&15)
	chCode, bpsCode := int(h>>4&15), int(h>>1&7)
	// The frame or sample number, UTF-8 style.
	first, err := br.bits(8)
	if err != nil {
		return nil, err
	}
	for extra := 0; first&(0x80>>extra) != 0 && extra < 7; extra++ {
		if extra > 0 {
			if _, err := br.bits(8); err != nil {
				return nil, err
			}
		}
	}
	size := blockSizes[bsCode]
	switch size {
	case -8:
		v, err := br.bits(8)
		if err != nil {
			return nil, err
		}
		size = int(v) + 1
	case -16:
		v, err := br.bits(16)
		if err != nil {
			return nil, err
		}
		size = int(v) + 1
	case 0:
		return nil, errors.New("flac: reserved block size")
	}
	switch srCode {
	case 12:
		v, err := br.bits(8)
		if err != nil {
			return nil, err
		}
		info.SampleRate = int(v) * 1000
	case 13, 14:
		v, err := br.bits(16)
		if err != nil {
			return nil, err
		}
		info.SampleRate = int(v)
		if srCode == 14 {
			info.SampleRate *= 10
		}
	case 15:
		return nil, errors.New("flac: invalid sample rate")
	default:
		if srCode > 0 {
			info.SampleRate = sampleRates[srCode]
		}
	}
	bps := info.Bits
	if bpsCode != 0 {
		bps = sampleBits[bpsCode]
		if bps == 0 {
			return nil, errors.New("flac: reserved sample size")
		}
	}
	info.Bits = bps
	if _, err := br.bits(8); err != nil { // header CRC-8
		return nil, err
	}
	channels := chCode + 1
	if chCode >= 8 {
		if chCode > 10 {
			return nil, errors.New("flac: reserved channel assignment")
		}
		channels = 2
	}
	info.Channels = channels
	for len(*bufs) < channels {
		*bufs = append(*bufs, nil)
	}
	out := (*bufs)[:channels]
	for c := range out {
		if cap(out[c]) < size {
			out[c] = make([]int32, size)
		}
		out[c] = out[c][:size]
		// The side channel carries one bit more.
		cb := bps
		if (chCode == 8 && c == 1) || (chCode == 9 && c == 0) || (chCode == 10 && c == 1) {
			cb++
		}
		if err := readSubframe(br, out[c], cb); err != nil {
			return nil, err
		}
	}
	switch chCode {
	case 8: // left, side
		for i := range out[0] {
			out[1][i] = out[0][i] - out[1][i]
		}
	case 9: // side, right
		for i := range out[0] {
			out[0][i] += out[1][i]
		}
	case 10: // mid, side
		for i := range out[0] {
			side := out[1][i]
			mid := out[0][i]<<1 | side&1
			out[0][i] = (mid + side) >> 1
			out[1][i] = (mid - side) >> 1
		}
	}
	br.align()
	if _, err := br.bits(16); err != nil { // frame CRC-16
		return nil, err
	}
	return out, nil
}

var fixedCoeffs = [5][]int64{{}, {1}, {2, -1}, {3, -3, 1}, {4, -6, 4, -1}}

func readSubframe(br *bitReader, s []int32, bps int) error {
	h, err := br.bits(8)
	if err != nil {
		return err
	}
	kind := int(h >> 1 & 0x3f)
	wasted := 0
	if h&1 != 0 {
		for {
			bit, err := br.bits(1)
			if err != nil {
				return err
			}
			wasted++
			if bit == 1 {
				break
			}
		}
		bps -= wasted
	}
	switch {
	case kind == 0: // constant
		v, err := br.signed(bps)
		if err != nil {
			return err
		}
		for i := range s {
			s[i] = v
		}
	case kind == 1: // verbatim
		for i := range s {
			v, err := br.signed(bps)
			if err != nil {
				return err
			}
			s[i] = v
		}
	case kind >= 8 && kind <= 12: // fixed predictor
		order := kind - 8
		if err := warmup(br, s, order, bps); err != nil {
			return err
		}
		if err := residual(br, s, order); err != nil {
			return err
		}
		predict(s, order, fixedCoeffs[order], 0)
	case kind >= 32: // LPC
		order := kind - 31
		if err := warmup(br, s, order, bps); err != nil {
			return err
		}
		p, err := br.bits(4)
		if err != nil {
			return err
		}
		if p == 15 {
			return errors.New("flac: invalid LPC precision")
		}
		shift, err := br.signed(5)
		if err != nil {
			return err
		}
		if shift < 0 {
			return errors.New("flac: negative LPC shift")
		}
		coeffs := make([]int64, order)
		for i := range coeffs {
			c, err := br.signed(int(p) + 1)
			if err != nil {
				return err
			}
			coeffs[i] = int64(c)
		}
		if err := residual(br, s, order); err != nil {
			return err
		}
		predict(s, order, coeffs, uint(shift))
	default:
		return fmt.Errorf("flac: reserved subframe type %d", kind)
	}
	if wasted > 0 {
		for i := range s {
			s[i] <<= wasted
		}
	}
	return nil
}

func warmup(br *bitReader, s []int32, order, bps int) error {
	if order > len(s) {
		return errors.New("flac: predictor longer than its block")
	}
	for i := 0; i < order; i++ {
		v, err := br.signed(bps)
		if err != nil {
			return err
		}
		s[i] = v
	}
	return nil
}

// predict turns residuals (s[order:]) into samples in place.
func predict(s []int32, order int, coeffs []int64, shift uint) {
	for i := order; i < len(s); i++ {
		var sum int64
		for j, c := range coeffs {
			sum += c * int64(s[i-1-j])
		}
		s[i] += int32(sum >> shift)
	}
}

func residual(br *bitReader, s []int32, order int) error {
	method, err := br.bits(2)
	if err != nil {
		return err
	}
	if method > 1 {
		return errors.New("flac: reserved residual coding")
	}
	paramBits, escape := uint(4), uint64(15)
	if method == 1 {
		paramBits, escape = 5, 31
	}
	po, err := br.bits(4)
	if err != nil {
		return err
	}
	parts := 1 << po
	per := len(s) >> po
	if per<<po != len(s) || per < order {
		return errors.New("flac: bad residual partitions")
	}
	i := order
	for p := 0; p < parts; p++ {
		n := per
		if p == 0 {
			n -= order
		}
		k, err := br.bits(paramBits)
		if err != nil {
			return err
		}
		if k == escape {
			raw, err := br.bits(5)
			if err != nil {
				return err
			}
			for ; n > 0; n-- {
				v := int32(0)
				if raw > 0 {
					if v, err = br.signed(int(raw)); err != nil {
						return err
					}
				}
				s[i] = v
				i++
			}
			continue
		}
		for ; n > 0; n-- {
			q, err := br.unary()
			if err != nil {
				return err
			}
			low, err := br.bits(uint(k))
			if err != nil {
				return err
			}
			u := q<<k | low
			s[i] = int32(u>>1) ^ -int32(u&1)
			i++
		}
	}
	return nil
}

// bitReader reads bits most significant first.
type bitReader struct {
	r    *bufio.Reader
	acc  uint64
	have uint
}

func (b *bitReader) align() { b.acc, b.have = 0, 0 }

func (b *bitReader) bits(n uint) (uint64, error) {
	if n == 0 {
		return 0, nil
	}
	for b.have < n {
		c, err := b.r.ReadByte()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		b.acc = b.acc<<8 | uint64(c)
		b.have += 8
	}
	b.have -= n
	v := b.acc >> b.have & (1<<n - 1)
	b.acc &= 1<<b.have - 1
	return v, nil
}

func (b *bitReader) signed(n int) (int32, error) {
	if n <= 0 || n > 33 {
		return 0, errors.New("flac: bad sample size")
	}
	v, err := b.bits(uint(n))
	if err != nil {
		return 0, err
	}
	return int32(int64(v<<(64-uint(n))) >> (64 - uint(n))), nil
}

// unary counts zero bits up to the next one, a byte at a time.
func (b *bitReader) unary() (uint64, error) {
	var q uint64
	for {
		if b.have == 0 {
			c, err := b.r.ReadByte()
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return 0, err
			}
			b.acc, b.have = uint64(c), 8
		}
		if b.acc == 0 {
			q += uint64(b.have)
			b.have = 0
			if q > 1<<20 {
				return 0, errors.New("flac: runaway residual")
			}
			continue
		}
		zeros := b.have - uint(bits.Len64(b.acc))
		q += uint64(zeros)
		b.have -= zeros + 1
		b.acc &= 1<<b.have - 1
		return q, nil
	}
}
