package stream

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/httpx"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Songs sent without the pictures inside them.
//
// Reported as songs taking 15-40 seconds to start on a phone away from home,
// where Apple Music starts at once. Measured at 0.6 Mbps in Chrome: an iTunes
// M4A took 34s to start, and the same song with the tags and artwork taken out
// of its header - the audio byte for byte the same - 2.5s. A browser's media
// engine takes a picture embedded in a song for a second stream and reads on,
// about 2.3MB of an iTunes song, before it will play (the phone's own report
// agreed: 75 seconds of music downloaded before the first note). An MP3 with a
// large embedded picture took 75s. FLAC was not affected. At home the extra
// megabytes take a fraction of a second, which is why it never showed.
//
// The files stay exactly as they are - the pictures are most songs' only
// cover, and other players read them - so the picture is left out on the way
// out instead. The song is sent as a slimmer file: a header built here
// (without the picture), then the original's audio, fetched from the backend
// as it is by byte range. For an M4A the header is the original's `moov`
// without its `udta` and `meta` (tags and artwork), with the empty padding
// after it dropped too and every chunk offset moved to match; for an MP3 it
// is the original from its first audio frame, the ID3 tag skipped. Seeking
// works because every range asked of the slim file maps onto the original.
//
// Only original-quality streams: a converted one (?kbps=) is made by the
// backend and carries no picture already.

const (
	slimProbe     = 64 << 10 // read first: ftyp and the start of moov, or an ID3 tag's header
	slimMaxMoov   = 16 << 20 // a song's index is a few hundred KB
	slimMaxPrefix = 1 << 20  // what sits before moov (ftyp) is tiny
	slimMinSaving = 16 << 10 // under this, send the original as it is
	slimKeep      = time.Hour
	slimCacheSize = 128
)

// slimLayout describes the slim file: head, from memory, then the original
// from tailStart to its end. ok false means send the original.
type slimLayout struct {
	ok          bool
	head        []byte
	tailStart   int64
	size        int64
	contentType string
	kbps        float64 // the audio's own bitrate, for pacing; 0 if not known
	etag        string
	lastMod     string
	made        time.Time
}

func (l *slimLayout) virtualSize() int64 { return int64(len(l.head)) + l.size - l.tailStart }

type slimCache struct {
	mu    sync.Mutex
	m     map[string]*slimLayout
	order []string
}

func (c *slimCache) get(key string) *slimLayout {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.m[key]
	if l != nil && time.Since(l.made) > slimKeep {
		delete(c.m, key)
		return nil
	}
	return l
}

func (c *slimCache) put(key string, l *slimLayout) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]*slimLayout{}
	}
	if _, ok := c.m[key]; !ok {
		c.order = append(c.order, key)
	}
	c.m[key] = l
	for len(c.order) > slimCacheSize {
		delete(c.m, c.order[0])
		c.order = c.order[1:]
	}
}

func (c *slimCache) drop(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

// slimmable reports whether a stream request may be sent slim: an original
// song from a backend, not a converted one or a local file.
func slimmable(r *http.Request, target source.Target) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		target.URL != "" && target.OnDone == nil && source.MaxBitRate(r.Context()) == 0
}

// serveSlim sends the slim file, or reports false for the caller to send the
// original. Once a song has been sent slim every request for it is answered
// slim, since a player mixing ranges of the two files would get garbage.
func (p *Proxy) serveSlim(w http.ResponseWriter, r *http.Request, target source.Target, key, what string) bool {
	l := p.slim.get(key)
	if l == nil {
		var err error
		l, err = p.slimLayoutFor(r.Context(), target)
		if err != nil {
			if r.Context().Err() == nil {
				p.log.Debug("slim layout", "what", what, "err", err)
			}
			return false // not cached: the next request looks again
		}
		p.slim.put(key, l)
	}
	if !l.ok {
		return false
	}

	vsize := l.virtualSize()
	start, end, partial, satisfiable := parseRange(r.Header.Get("Range"), vsize)
	if ir := r.Header.Get("If-Range"); ir != "" && ir != l.etag && ir != l.lastMod {
		start, end, partial, satisfiable = 0, vsize-1, false, true
	}
	if !satisfiable {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", vsize))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return true
	}

	headLen := int64(len(l.head))
	var tail io.ReadCloser
	if end >= headLen && r.Method == http.MethodGet {
		from := l.tailStart + max(start, headLen) - headLen
		to := l.tailStart + end - headLen
		resp, err := p.fetchRange(r.Context(), target, from, to)
		if err != nil {
			if r.Context().Err() == nil {
				p.log.Warn("upstream fetch failed", "what", what, "err", err)
				http.Error(w, "upstream unavailable", http.StatusBadGateway)
			}
			return true
		}
		if total, ok := rangeTotal(resp); resp.StatusCode != http.StatusPartialContent || !ok || total != l.size {
			// The file changed under the layout: look again next time.
			resp.Body.Close()
			p.slim.drop(key)
			http.Error(w, "song changed, try again", http.StatusServiceUnavailable)
			return true
		}
		tail = resp.Body
		defer tail.Close()
	}

	h := w.Header()
	h.Set("Content-Type", l.contentType)
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	if l.etag != "" {
		h.Set("ETag", l.etag)
	}
	if l.lastMod != "" {
		h.Set("Last-Modified", l.lastMod)
	}
	h.Set("Cache-Control", "private, max-age=604800")
	h.Set("X-Content-Type-Options", "nosniff")
	GuardActiveContent(h)
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, vsize))
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return true
	}

	var body io.Reader = bytes.NewReader(nil)
	if start < headLen {
		body = bytes.NewReader(l.head[start:min(end+1, headLen)])
	}
	if tail != nil {
		body = io.MultiReader(body, io.LimitReader(tail, end-max(start, headLen)+1))
	}
	var err error
	if burst, rate := paceFor(r, l.contentType, l.kbps); rate > 0 && awayFromHome(r) {
		if start < headLen {
			burst += headLen - start // the header, then eight seconds of music
		}
		_, err = pacedCopy(r.Context(), w, body, burst, rate)
	} else {
		_, err = io.Copy(w, body)
	}
	if err != nil && r.Context().Err() == nil {
		p.log.Debug("stream copy ended early", "what", what, "err", err)
	}
	return true
}

// fetchRange asks the backend for bytes from..to of the original.
func (p *Proxy) fetchRange(ctx context.Context, target source.Target, from, to int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range target.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, httpx.Redact(err)
	}
	return resp, nil
}

// readRange reads bytes from..to of the original in full.
func (p *Proxy) readRange(ctx context.Context, target source.Target, from, to int64) ([]byte, *http.Response, error) {
	resp, err := p.fetchRange(ctx, target, from, to)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return nil, resp, fmt.Errorf("upstream answered %d to a range", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, to-from+1))
	if err != nil {
		return nil, resp, err
	}
	return b, resp, nil
}

// slimLayoutFor looks at the start of a song and works out its slim file. An
// error means it could not tell (the backend did not answer); a layout with ok
// false means the song is sent as it is.
func (p *Proxy) slimLayoutFor(ctx context.Context, target source.Target) (*slimLayout, error) {
	first, resp, err := p.readRange(ctx, target, 0, slimProbe-1)
	if resp == nil {
		return nil, err
	}
	no := &slimLayout{made: time.Now()}
	if err != nil {
		return no, nil // no ranges: send it as it is
	}
	size, ok := rangeTotal(resp)
	if !ok || size <= 0 {
		return no, nil
	}
	l := &slimLayout{
		size:        size,
		contentType: resp.Header.Get("Content-Type"),
		lastMod:     resp.Header.Get("Last-Modified"),
		made:        time.Now(),
	}
	if et := resp.Header.Get("ETag"); et != "" {
		l.etag = `"slim-` + strings.Trim(strings.TrimPrefix(et, "W/"), `"`) + `"`
	}
	ct := strings.ToLower(l.contentType)
	switch {
	case strings.HasPrefix(ct, "audio/mpeg") || strings.HasPrefix(ct, "audio/mp3"):
		err = p.slimMP3(ctx, target, first, l)
	case strings.HasPrefix(ct, "audio/mp4") || strings.HasPrefix(ct, "audio/x-m4a") ||
		strings.HasPrefix(ct, "audio/m4a") || strings.HasPrefix(ct, "audio/aac"):
		err = p.slimMP4(ctx, target, first, l)
	default:
		return no, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return no, nil
	}
	if l.tailStart-int64(len(l.head)) < slimMinSaving {
		return no, nil
	}
	l.ok = true
	return l, nil
}

// slimMP3 skips the ID3v2 tags in front of an MP3's first audio frame. The
// song's own frames, its Xing/LAME header (gapless playback) included, are
// sent as they are.
func (p *Proxy) slimMP3(ctx context.Context, target source.Target, first []byte, l *slimLayout) error {
	var at int64
	for i := 0; i < 4; i++ { // tags can be chained
		hdr := first
		if at != 0 {
			var err error
			if hdr, _, err = p.readRange(ctx, target, at, at+9); err != nil {
				return err
			}
		} else if len(hdr) < 10 {
			return errors.New("short")
		}
		if string(hdr[:3]) != "ID3" {
			break
		}
		n := int64(hdr[6]&0x7f)<<21 | int64(hdr[7]&0x7f)<<14 | int64(hdr[8]&0x7f)<<7 | int64(hdr[9]&0x7f)
		at += 10 + n
		if hdr[5]&0x10 != 0 { // footer
			at += 10
		}
		if at >= l.size {
			return errors.New("tag runs past the file")
		}
	}
	if at == 0 {
		return errors.New("no tag")
	}
	// A frame must start where the tag ends, or the tag's size was wrong.
	frame, _, err := p.readRange(ctx, target, at, at+255)
	if err != nil || len(frame) < 4 || frame[0] != 0xff || frame[1]&0xe0 != 0xe0 {
		return errors.New("no frame after the tag")
	}
	l.tailStart = at
	l.kbps = mp3FrameKbps(frame)
	return nil
}

// mp3FrameKbps is the bitrate a constant-bitrate MP3's first frame names, or
// 0 when it cannot be trusted: a variable-bitrate file announces itself with
// a Xing or Info frame whose own bitrate says nothing about the rest.
func mp3FrameKbps(frame []byte) float64 {
	if bytes.Contains(frame, []byte("Xing")) || bytes.Contains(frame, []byte("VBRI")) {
		return 0
	}
	version := frame[1] >> 3 & 3 // 3 is MPEG-1
	layer := frame[1] >> 1 & 3   // 1 is Layer III
	index := frame[2] >> 4
	if layer != 1 || index == 0 || index == 15 {
		return 0
	}
	mpeg1 := [...]float64{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	mpeg2 := [...]float64{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	if version == 3 {
		return mpeg1[index]
	}
	return mpeg2[index]
}

type mp4Box struct {
	typ        string
	start, end int64 // end is exclusive
}

// slimMP4 builds an M4A's slim header: what comes before moov, then moov
// without udta and meta, with chunk offsets moved to where the audio now is.
func (p *Proxy) slimMP4(ctx context.Context, target source.Target, first []byte, l *slimLayout) error {
	// The top-level boxes, reading headers past the probe as needed.
	var boxes []mp4Box
	var off int64
	for len(boxes) < 32 && off < l.size {
		var hdr []byte
		if off+16 <= int64(len(first)) {
			hdr = first[off : off+16]
		} else {
			var err error
			if hdr, _, err = p.readRange(ctx, target, off, min(off+15, l.size-1)); err != nil {
				return err
			}
		}
		if len(hdr) < 8 {
			return errors.New("short box")
		}
		n := int64(binary.BigEndian.Uint32(hdr))
		switch n {
		case 0:
			n = l.size - off
		case 1:
			if len(hdr) < 16 {
				return errors.New("short box")
			}
			n = int64(binary.BigEndian.Uint64(hdr[8:]))
		}
		if n < 8 || off+n > l.size {
			return errors.New("bad box")
		}
		b := mp4Box{string(hdr[4:8]), off, off + n}
		boxes = append(boxes, b)
		if b.typ == "mdat" {
			break
		}
		off += n
	}
	mi, di := -1, -1
	for i, b := range boxes {
		switch b.typ {
		case "moov":
			if mi < 0 {
				mi = i
			}
		case "mdat":
			di = i
		}
	}
	// Only the shape iTunes and most taggers write: moov, then the audio.
	if mi < 0 || di < 0 || di < mi {
		return errors.New("moov is not before the audio")
	}
	moovBox := boxes[mi]
	if moovBox.start > slimMaxPrefix || moovBox.end-moovBox.start > slimMaxMoov {
		return errors.New("header too large")
	}
	var head []byte
	if moovBox.end <= int64(len(first)) {
		head = first[:moovBox.end]
	} else {
		var err error
		if head, _, err = p.readRange(ctx, target, 0, moovBox.end-1); err != nil {
			return err
		}
		if int64(len(head)) != moovBox.end {
			return errors.New("short header")
		}
	}
	moov := head[moovBox.start:moovBox.end]
	if int64(binary.BigEndian.Uint32(moov)) != moovBox.end-moovBox.start {
		return errors.New("moov size is not plain") // a 64-bit moov: leave it
	}

	// The padding between moov and the audio goes too; anything else there stays.
	tailStart := moovBox.end
	for _, b := range boxes[mi+1 : di] {
		if b.typ != "free" && b.typ != "skip" {
			tailStart = moovBox.end
			break
		}
		tailStart = b.end
	}

	slimMoov, err := stripMoov(moov)
	if err != nil {
		return err
	}
	newHead := make([]byte, 0, int(moovBox.start)+len(slimMoov))
	newHead = append(newHead, head[:moovBox.start]...)
	newHead = append(newHead, slimMoov...)
	shift := tailStart - int64(len(newHead))
	if shift < 0 {
		return errors.New("slim header is larger")
	}
	if err := shiftChunkOffsets(newHead[moovBox.start:], tailStart, shift); err != nil {
		return err
	}
	l.head = newHead
	l.tailStart = tailStart
	if secs := mp4Seconds(slimMoov); secs > 0 {
		kbps := float64(l.size-tailStart) * 8 / secs / 1000
		if kbps >= 32 && kbps <= 2000 {
			l.kbps = kbps
		}
	}
	return nil
}

// mp4Seconds is the length moov's mvhd gives, or 0.
func mp4Seconds(moov []byte) float64 {
	for off := 8; off+8 <= len(moov); {
		n := int(binary.BigEndian.Uint32(moov[off:]))
		if n < 8 || off+n > len(moov) {
			return 0
		}
		if string(moov[off+4:off+8]) == "mvhd" {
			b := moov[off : off+n]
			var scale, dur float64
			switch {
			case len(b) >= 32 && b[8] == 0:
				scale, dur = float64(binary.BigEndian.Uint32(b[20:])), float64(binary.BigEndian.Uint32(b[24:]))
			case len(b) >= 44 && b[8] == 1:
				scale, dur = float64(binary.BigEndian.Uint32(b[28:])), float64(binary.BigEndian.Uint64(b[32:]))
			}
			if scale > 0 {
				return dur / scale
			}
			return 0
		}
		off += n
	}
	return 0
}

// stripMoov copies moov without its udta and meta children. A fragmented
// file (mvex) is refused: its fragments carry offsets of their own.
func stripMoov(moov []byte) ([]byte, error) {
	out := make([]byte, 8, len(moov))
	copy(out, moov[:8])
	for off := 8; off < len(moov); {
		if off+8 > len(moov) {
			return nil, errors.New("short child")
		}
		n := int(binary.BigEndian.Uint32(moov[off:]))
		if n < 8 || off+n > len(moov) {
			return nil, errors.New("bad child")
		}
		switch string(moov[off+4 : off+8]) {
		case "mvex":
			return nil, errors.New("fragmented")
		case "udta", "meta":
		default:
			out = append(out, moov[off:off+n]...)
		}
		off += n
	}
	binary.BigEndian.PutUint32(out, uint32(len(out)))
	return out, nil
}

// shiftChunkOffsets moves every stco and co64 entry in moov back by shift.
// Every entry must point at or after from - into the audio that follows -
// or the file is not the shape this expects.
func shiftChunkOffsets(moov []byte, from, shift int64) error {
	found := false
	var walk func(b []byte) error
	walk = func(b []byte) error {
		for off := 0; off+8 <= len(b); {
			n := int(binary.BigEndian.Uint32(b[off:]))
			if n < 8 || off+n > len(b) {
				return errors.New("bad box")
			}
			box := b[off : off+n]
			switch string(box[4:8]) {
			case "trak", "mdia", "minf", "stbl":
				if err := walk(box[8:]); err != nil {
					return err
				}
			case "stco", "co64":
				wide := string(box[4:8]) == "co64"
				if len(box) < 16 {
					return errors.New("short offsets")
				}
				count := int(binary.BigEndian.Uint32(box[12:]))
				w := 4
				if wide {
					w = 8
				}
				if 16+count*w > len(box) {
					return errors.New("short offsets")
				}
				for i := 0; i < count; i++ {
					p := box[16+i*w:]
					var v int64
					if wide {
						v = int64(binary.BigEndian.Uint64(p))
					} else {
						v = int64(binary.BigEndian.Uint32(p))
					}
					if v < from {
						return errors.New("an offset points before the audio")
					}
					if wide {
						binary.BigEndian.PutUint64(p, uint64(v-shift))
					} else {
						binary.BigEndian.PutUint32(p, uint32(v-shift))
					}
				}
				found = true
			}
			off += n
		}
		return nil
	}
	if err := walk(moov[8:]); err != nil {
		return err
	}
	if !found {
		return errors.New("no chunk offsets")
	}
	return nil
}

// parseRange reads a single-range Range header against size. No header, or
// several ranges, is the whole file. satisfiable false means a 416.
func parseRange(v string, size int64) (start, end int64, partial, satisfiable bool) {
	whole := func() (int64, int64, bool, bool) { return 0, size - 1, false, true }
	if v == "" || !strings.HasPrefix(v, "bytes=") || strings.Contains(v, ",") {
		return whole()
	}
	a, b, ok := strings.Cut(strings.TrimSpace(v[len("bytes="):]), "-")
	if !ok {
		return whole()
	}
	if a == "" { // the last b bytes
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return whole()
		}
		return max(size-n, 0), size - 1, true, true
	}
	s, err := strconv.ParseInt(a, 10, 64)
	if err != nil || s < 0 {
		return whole()
	}
	if s >= size {
		return 0, 0, false, false
	}
	e := size - 1
	if b != "" {
		if e2, err := strconv.ParseInt(b, 10, 64); err == nil && e2 >= s {
			e = min(e2, size-1)
		} else if err != nil || e2 < s {
			return whole()
		}
	}
	return s, e, true, true
}

// rangeTotal is the full length a 206's Content-Range names.
func rangeTotal(resp *http.Response) (int64, bool) {
	cr := resp.Header.Get("Content-Range")
	_, total, ok := strings.Cut(cr, "/")
	if !ok || total == "*" {
		return 0, false
	}
	n, err := strconv.ParseInt(total, 10, 64)
	return n, err == nil
}
