// Package mp4hls serves a long MP4 audiobook (an .m4b) as HLS: an init
// segment, a playlist and short fragments, made from the file as it is.
//
// Why: an MP4 carries an index of every sample (moov), and a player must have
// all of it before the first sound. An Audible book keeps it at the end, and a
// long one's is large - 33.5MB for a 47-hour book - so away from home a book
// took over half a minute to start (reported, measured: 33s for the index).
// Here the server reads the index, where it is instant, and the player is
// handed fragments of a few seconds each, a playlist naming them all.
//
// Nothing is transcoded: each fragment is a small moof (the samples' sizes and
// durations) and an mdat of the original bytes, byte for byte - a remux, as
// stream/slim.go rewrites a song's header, never Jellyfin's or ffmpeg's job.
// Only the first sound track is kept (an .m4b's chapter text track and cover
// are not needed to play), and its edit list is dropped: the AAC priming that
// shifts is about 50ms.
package mp4hls

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// SegmentSeconds is how long each fragment is meant to be.
const SegmentSeconds = 10

// MinIndex is the index size from which a file is worth serving this way;
// below it, the file plays at once as it is.
const MinIndex = 2 << 20

// maxIndex refuses a moov too large to keep in memory.
const maxIndex = 256 << 20

type box struct {
	typ  string
	data []byte // the payload, after the header
}

// Book is one file's track, ready to cut into fragments.
type Book struct {
	path      string
	timescale uint32
	trackID   uint32
	init      []byte

	// Per sample: sizes are read from the stsz table as needed (stszFixed
	// for a constant size), durations from the run-length stts.
	stsz      []byte
	stszFixed uint32
	samples   uint32
	stts      []sttsRun

	// Per chunk: where it is in the file and its first sample.
	chunkOffset []uint64
	chunkFirst  []uint32

	// Per segment: its first sample and start time; one more at the end.
	segSample []uint32
	segTime   []uint64
}

type sttsRun struct{ count, delta uint32 }

// Layout reads a file's top-level boxes and answers its index size, without
// reading the index: what decides whether a file is worth serving this way.
func Layout(path string) (moovSize int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	var pos int64
	hdr := make([]byte, 16)
	for i := 0; pos+8 <= st.Size() && i < 64; i++ {
		if _, err := f.ReadAt(hdr[:8], pos); err != nil {
			return 0, err
		}
		size := int64(binary.BigEndian.Uint32(hdr))
		typ := string(hdr[4:8])
		if size == 1 {
			if _, err := f.ReadAt(hdr[8:16], pos+8); err != nil {
				return 0, err
			}
			size = int64(binary.BigEndian.Uint64(hdr[8:16]))
		} else if size == 0 {
			size = st.Size() - pos
		}
		if size < 8 {
			return 0, errors.New("mp4: bad box")
		}
		if typ == "moov" {
			return size, nil
		}
		pos += size
	}
	return 0, errors.New("mp4: no moov")
}

// Open reads a file's index and plans its fragments.
func Open(path string) (*Book, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var moov []byte
	var pos int64
	hdr := make([]byte, 16)
	for i := 0; pos+8 <= st.Size() && i < 64 && moov == nil; i++ {
		if _, err := f.ReadAt(hdr[:8], pos); err != nil {
			return nil, err
		}
		size := int64(binary.BigEndian.Uint32(hdr))
		head := int64(8)
		if size == 1 {
			if _, err := f.ReadAt(hdr[8:16], pos+8); err != nil {
				return nil, err
			}
			size = int64(binary.BigEndian.Uint64(hdr[8:16]))
			head = 16
		} else if size == 0 {
			size = st.Size() - pos
		}
		if size < head || pos+size > st.Size() {
			return nil, errors.New("mp4: bad box")
		}
		if string(hdr[4:8]) == "moov" {
			if size > maxIndex {
				return nil, errors.New("mp4: index too large")
			}
			moov = make([]byte, size-head)
			if _, err := f.ReadAt(moov, pos+head); err != nil {
				return nil, err
			}
		}
		pos += size
	}
	if moov == nil {
		return nil, errors.New("mp4: no moov")
	}
	b, err := parse(moov)
	if err != nil {
		return nil, err
	}
	b.path = path
	return b, nil
}

// children splits a box payload into its child boxes.
func children(data []byte) ([]box, error) {
	var out []box
	for len(data) >= 8 {
		size := uint64(binary.BigEndian.Uint32(data))
		typ := string(data[4:8])
		head := uint64(8)
		if size == 1 {
			if len(data) < 16 {
				return nil, errors.New("mp4: bad box")
			}
			size = binary.BigEndian.Uint64(data[8:16])
			head = 16
		} else if size == 0 {
			size = uint64(len(data))
		}
		if size < head || size > uint64(len(data)) {
			return nil, errors.New("mp4: bad box")
		}
		out = append(out, box{typ, data[head:size]})
		data = data[size:]
	}
	return out, nil
}

func find(boxes []box, typ string) []byte {
	for _, b := range boxes {
		if b.typ == typ {
			return b.data
		}
	}
	return nil
}

func parse(moov []byte) (*Book, error) {
	top, err := children(moov)
	if err != nil {
		return nil, err
	}
	mvhd := find(top, "mvhd")
	if mvhd == nil {
		return nil, errors.New("mp4: no mvhd")
	}
	for _, t := range top {
		if t.typ != "trak" {
			continue
		}
		trak, err := children(t.data)
		if err != nil {
			return nil, err
		}
		mdia, err := children(find(trak, "mdia"))
		if err != nil {
			return nil, err
		}
		hdlr := find(mdia, "hdlr")
		if len(hdlr) < 12 || string(hdlr[8:12]) != "soun" {
			continue
		}
		return track(mvhd, find(trak, "tkhd"), mdia)
	}
	return nil, errors.New("mp4: no sound track")
}

func track(mvhd, tkhd []byte, mdia []box) (*Book, error) {
	b := &Book{}
	mdhd := find(mdia, "mdhd")
	if len(mdhd) < 24 {
		return nil, errors.New("mp4: bad mdhd")
	}
	if mdhd[0] == 1 {
		if len(mdhd) < 36 {
			return nil, errors.New("mp4: bad mdhd")
		}
		b.timescale = binary.BigEndian.Uint32(mdhd[20:24])
	} else {
		b.timescale = binary.BigEndian.Uint32(mdhd[12:16])
	}
	if b.timescale == 0 {
		return nil, errors.New("mp4: no timescale")
	}
	if len(tkhd) < 24 {
		return nil, errors.New("mp4: bad tkhd")
	}
	if tkhd[0] == 1 {
		if len(tkhd) < 28 {
			return nil, errors.New("mp4: bad tkhd")
		}
		b.trackID = binary.BigEndian.Uint32(tkhd[20:24])
	} else {
		b.trackID = binary.BigEndian.Uint32(tkhd[12:16])
	}
	minfData := find(mdia, "minf")
	minf, err := children(minfData)
	if err != nil {
		return nil, err
	}
	stbl, err := children(find(minf, "stbl"))
	if err != nil {
		return nil, err
	}
	stsd := find(stbl, "stsd")
	if stsd == nil {
		return nil, errors.New("mp4: no stsd")
	}

	// Durations.
	stts := find(stbl, "stts")
	if len(stts) < 8 {
		return nil, errors.New("mp4: no stts")
	}
	n := int(binary.BigEndian.Uint32(stts[4:8]))
	if n < 0 || len(stts) < 8+8*n {
		return nil, errors.New("mp4: bad stts")
	}
	var fromStts uint64
	for i := 0; i < n; i++ {
		r := sttsRun{binary.BigEndian.Uint32(stts[8+8*i:]), binary.BigEndian.Uint32(stts[12+8*i:])}
		b.stts = append(b.stts, r)
		fromStts += uint64(r.count)
	}

	// Sizes.
	stsz := find(stbl, "stsz")
	if len(stsz) < 12 {
		return nil, errors.New("mp4: no stsz")
	}
	b.stszFixed = binary.BigEndian.Uint32(stsz[4:8])
	b.samples = binary.BigEndian.Uint32(stsz[8:12])
	if b.stszFixed == 0 {
		if uint64(len(stsz)) < 12+4*uint64(b.samples) {
			return nil, errors.New("mp4: bad stsz")
		}
		b.stsz = stsz[12 : 12+4*uint64(b.samples)]
	}
	if b.samples == 0 || fromStts < uint64(b.samples) {
		return nil, errors.New("mp4: tables disagree")
	}

	// Chunks.
	var offsets []uint64
	if stco := find(stbl, "stco"); len(stco) >= 8 {
		c := int(binary.BigEndian.Uint32(stco[4:8]))
		if c < 0 || len(stco) < 8+4*c {
			return nil, errors.New("mp4: bad stco")
		}
		offsets = make([]uint64, c)
		for i := range offsets {
			offsets[i] = uint64(binary.BigEndian.Uint32(stco[8+4*i:]))
		}
	} else if co64 := find(stbl, "co64"); len(co64) >= 8 {
		c := int(binary.BigEndian.Uint32(co64[4:8]))
		if c < 0 || len(co64) < 8+8*c {
			return nil, errors.New("mp4: bad co64")
		}
		offsets = make([]uint64, c)
		for i := range offsets {
			offsets[i] = binary.BigEndian.Uint64(co64[8+8*i:])
		}
	} else {
		return nil, errors.New("mp4: no chunk offsets")
	}
	stsc := find(stbl, "stsc")
	if len(stsc) < 8 {
		return nil, errors.New("mp4: no stsc")
	}
	e := int(binary.BigEndian.Uint32(stsc[4:8]))
	if e <= 0 || len(stsc) < 8+12*e {
		return nil, errors.New("mp4: bad stsc")
	}
	b.chunkOffset = offsets
	b.chunkFirst = make([]uint32, len(offsets))
	var sample uint32
	for i := 0; i < e; i++ {
		first := binary.BigEndian.Uint32(stsc[8+12*i:])
		per := binary.BigEndian.Uint32(stsc[12+12*i:])
		last := uint32(len(offsets)) + 1
		if i+1 < e {
			last = binary.BigEndian.Uint32(stsc[8+12*(i+1):])
		}
		if first == 0 || last < first || last > uint32(len(offsets))+1 {
			return nil, errors.New("mp4: bad stsc")
		}
		for c := first; c < last; c++ {
			b.chunkFirst[c-1] = sample
			sample += per
		}
	}
	if sample < b.samples {
		return nil, errors.New("mp4: chunks hold too few samples")
	}

	// Segments: about SegmentSeconds each, cut between samples.
	target := uint64(SegmentSeconds) * uint64(b.timescale)
	var t, segStart uint64
	b.segSample = append(b.segSample, 0)
	b.segTime = append(b.segTime, 0)
	var s uint32
	for _, r := range b.stts {
		for k := uint32(0); k < r.count && s < b.samples; k++ {
			if t-segStart >= target {
				b.segSample = append(b.segSample, s)
				b.segTime = append(b.segTime, t)
				segStart = t
			}
			t += uint64(r.delta)
			s++
		}
	}
	b.segSample = append(b.segSample, b.samples)
	b.segTime = append(b.segTime, t)

	b.init = initSegment(mvhd, minfData, stsd, mdia, tkhd, b.trackID)
	return b, nil
}

// Segments is how many fragments the book has.
func (b *Book) Segments() int { return len(b.segSample) - 1 }

// Duration is the track's length in seconds.
func (b *Book) Duration() float64 {
	return float64(b.segTime[len(b.segTime)-1]) / float64(b.timescale)
}

// Playlist is the HLS playlist naming every fragment, relative to itself.
func (b *Book) Playlist() []byte {
	var sb strings.Builder
	longest := 0.0
	for i := 0; i < b.Segments(); i++ {
		longest = math.Max(longest, b.segDuration(i))
	}
	fmt.Fprintf(&sb, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:%d\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-MAP:URI=\"init.mp4\"\n",
		int(math.Ceil(longest)))
	for i := 0; i < b.Segments(); i++ {
		fmt.Fprintf(&sb, "#EXTINF:%.5f,\ns%d.m4s\n", b.segDuration(i), i)
	}
	sb.WriteString("#EXT-X-ENDLIST\n")
	return []byte(sb.String())
}

// SegmentStart is where fragment i starts, in seconds (i may be Segments(),
// the end).
func (b *Book) SegmentStart(i int) float64 {
	return float64(b.segTime[i]) / float64(b.timescale)
}

func (b *Book) segDuration(i int) float64 {
	return float64(b.segTime[i+1]-b.segTime[i]) / float64(b.timescale)
}

// Init is the init segment: the track's description, and no samples.
func (b *Book) Init() []byte { return b.init }

func (b *Book) size(s uint32) uint32 {
	if b.stszFixed != 0 {
		return b.stszFixed
	}
	return binary.BigEndian.Uint32(b.stsz[4*s:])
}

// durations walks the stts from sample a, calling f with each duration.
func (b *Book) durations(a, n uint32, f func(uint32)) {
	var s uint32
	for _, r := range b.stts {
		if s+r.count <= a {
			s += r.count
			continue
		}
		// Straight to sample a within this run.
		for k := a - min(a, s); k < r.count && n > 0; k++ {
			f(r.delta)
			n--
		}
		if n == 0 {
			return
		}
		s += r.count
		a = s
	}
}

// chunkOf is the chunk holding sample s.
func (b *Book) chunkOf(s uint32) int {
	lo, hi := 0, len(b.chunkFirst)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if b.chunkFirst[mid] <= s {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// WriteSegment writes fragment i: a moof describing its samples and an mdat
// of their bytes, copied from the file.
func (b *Book) WriteSegment(w io.Writer, i int) error {
	if i < 0 || i >= b.Segments() {
		return errors.New("no such segment")
	}
	a, z := b.segSample[i], b.segSample[i+1]
	n := z - a

	// trun: data offset, then per sample its duration and size.
	trun := make([]byte, 0, 12+8*n)
	trun = append(trun, 0, 0, 0x03, 0x01)
	trun = binary.BigEndian.AppendUint32(trun, n)
	trun = binary.BigEndian.AppendUint32(trun, 0) // data offset, set below
	var total uint64
	idx := 0
	sizes := make([]uint32, n)
	for s := a; s < z; s++ {
		sizes[s-a] = b.size(s)
		total += uint64(sizes[s-a])
	}
	b.durations(a, n, func(d uint32) {
		trun = binary.BigEndian.AppendUint32(trun, d)
		trun = binary.BigEndian.AppendUint32(trun, sizes[idx])
		idx++
	})
	if idx != int(n) {
		return errors.New("mp4: durations ran out")
	}
	tfhd := binary.BigEndian.AppendUint32([]byte{0, 0x02, 0, 0}, b.trackID)
	tfdt := binary.BigEndian.AppendUint64([]byte{1, 0, 0, 0}, b.segTime[i])
	traf := concat(mkbox("tfhd", tfhd), mkbox("tfdt", tfdt), mkbox("trun", trun))
	mfhd := binary.BigEndian.AppendUint32([]byte{0, 0, 0, 0}, uint32(i+1))
	moof := mkbox("moof", concat(mkbox("mfhd", mfhd), mkbox("traf", traf)))
	// The data offset is from the moof's start to the first sample: past the
	// moof and the mdat's header. Its place in the trun is fixed: moof header
	// (8), mfhd (16), traf header (8), tfhd (16), tfdt (20), trun header (8),
	// version and flags (4), count (4).
	at := 8 + 16 + 8 + 16 + 20 + 8 + 4 + 4
	mdatHead := 8
	if total+8 > math.MaxUint32 {
		mdatHead = 16
	}
	binary.BigEndian.PutUint32(moof[at:], uint32(len(moof)+mdatHead))
	if _, err := w.Write(moof); err != nil {
		return err
	}
	if mdatHead == 8 {
		_, err := w.Write(binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, uint32(total+8)), 0x6d646174))
		if err != nil {
			return err
		}
	} else {
		head := binary.BigEndian.AppendUint32(nil, 1)
		head = append(head, "mdat"...)
		head = binary.BigEndian.AppendUint64(head, total+16)
		if _, err := w.Write(head); err != nil {
			return err
		}
	}

	f, err := os.Open(b.path)
	if err != nil {
		return err
	}
	defer f.Close()
	// Samples follow one another within a chunk: each chunk's run is one read.
	s := a
	for s < z {
		c := b.chunkOf(s)
		off := b.chunkOffset[c]
		for k := b.chunkFirst[c]; k < s; k++ {
			off += uint64(b.size(k))
		}
		end := z
		if c+1 < len(b.chunkFirst) && b.chunkFirst[c+1] < end {
			end = b.chunkFirst[c+1]
		}
		var length uint64
		for k := s; k < end; k++ {
			length += uint64(sizes[k-a])
		}
		if _, err := io.Copy(w, io.NewSectionReader(f, int64(off), int64(length))); err != nil {
			return err
		}
		s = end
	}
	return nil
}

func mkbox(typ string, payload []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(payload)))
	out = append(out, typ...)
	return append(out, payload...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// initSegment is ftyp and a moov with the track's description and no
// samples, plus mvex saying its samples come in fragments.
func initSegment(mvhd, minfData, stsd []byte, mdia []box, tkhd []byte, trackID uint32) []byte {
	ftyp := mkbox("ftyp", concat([]byte("iso6"), []byte{0, 0, 0, 0}, []byte("iso6mp41dashM4A ")))
	empty := func(typ string, extra int) []byte { return mkbox(typ, make([]byte, 8+extra)) }
	stbl := concat(mkbox("stsd", stsd), empty("stts", 0), empty("stsc", 0), mkbox("stsz", make([]byte, 12)), empty("stco", 0))
	minfKids, _ := children(minfData)
	var minf []byte
	for _, k := range minfKids {
		if k.typ == "stbl" {
			continue
		}
		minf = append(minf, mkbox(k.typ, k.data)...)
	}
	minf = append(minf, mkbox("stbl", stbl)...)
	var mdiaOut []byte
	for _, k := range mdia {
		if k.typ == "minf" {
			mdiaOut = append(mdiaOut, mkbox("minf", minf)...)
			continue
		}
		mdiaOut = append(mdiaOut, mkbox(k.typ, k.data)...)
	}
	trak := mkbox("trak", concat(mkbox("tkhd", tkhd), mkbox("mdia", mdiaOut)))
	trex := binary.BigEndian.AppendUint32([]byte{0, 0, 0, 0}, trackID)
	trex = binary.BigEndian.AppendUint32(trex, 1) // sample description 1
	trex = append(trex, make([]byte, 12)...)
	mvex := mkbox("mvex", mkbox("trex", trex))
	moov := mkbox("moov", concat(mkbox("mvhd", mvhd), trak, mvex))
	return concat(ftyp, moov)
}
