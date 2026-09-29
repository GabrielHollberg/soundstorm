package stream

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Pacing music sent away from home.
//
// Reported from the Android app on a phone away from home: songs sat at 0:00
// for a minute or more, and the next song after a skip the same. Measured with
// a playback report: the link from the house's upload to the phone ran at about
// 0.3 Mbps, and the server handed each song to the network in full the moment
// it was asked - milliseconds at home, where the network drains it as fast as
// it arrives. Over the slow link those megabytes sit queued in the connection
// (HTTP/2 lets a browser take several MB per stream before it pushes back, and
// the house's router and Docker Desktop's port forwarding buffer more), so
// every request after them - the song that replaced a skipped one, the quieter
// copy a stuck one falls back to, even the covers - waited a minute behind
// bytes nobody would ever play.
//
// So audio for a device reaching the server by its away-from-home name is
// sent at a steady pace after a first burst: never more than a few seconds of
// music queued ahead of the listener, so a new request's first bytes arrive in
// seconds. At home nothing is paced. Films are not either: Jellyfin sends
// them as short HLS pieces the player asks for one at a time.

const (
	// paceBurst goes out at once: enough to start any song, an iTunes M4A's
	// ~600KB of index, art and padding before its first note included.
	paceBurst = 1 << 20
	// paceAhead is how many times faster than it plays music is sent after
	// the burst: comfortably ahead of playback on a link that can carry it,
	// never megabytes queued on one that cannot.
	paceAhead = 2.5
)

// awayFromHome reports whether a request came in by one of the names used
// from outside the house: remote access (<id>.net.soundstorm.dev) or a
// Tailscale address (<machine>.<tailnet>.ts.net).
func awayFromHome(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return strings.HasSuffix(host, ".net.soundstorm.dev") || strings.HasSuffix(host, ".ts.net")
}

// paceRate is how fast, in bytes a second, to send a piece of audio after the
// burst: paceAhead times its bitrate. The bitrate is the one asked for
// (?kbps=, a converted stream), else a generous guess from its type - 320 kbps
// for compressed audio, CD quality for lossless - since an original file's
// bitrate is not known here. Zero means not to pace at all.
func paceRate(r *http.Request, contentType string) float64 {
	ct := strings.ToLower(contentType)
	if !strings.HasPrefix(ct, "audio/") {
		return 0
	}
	kbps := 320.0
	if k, err := strconv.Atoi(r.URL.Query().Get("kbps")); err == nil && k > 0 {
		kbps = float64(k)
	} else {
		for _, lossless := range []string{"flac", "wav", "aiff", "x-aiff", "alac"} {
			if strings.Contains(ct, lossless) {
				kbps = 1411
			}
		}
	}
	return paceAhead * kbps * 1000 / 8
}

// pacedCopy copies src to dst: the first burst bytes at once, then no faster
// than rate bytes a second, measured from the start. It stops when ctx is
// done (the listener skipped, stopped or closed the app).
func pacedCopy(ctx context.Context, dst io.Writer, src io.Reader, burst int64, rate float64) (int64, error) {
	buf := make([]byte, 32<<10)
	start := time.Now()
	var sent int64
	for {
		if sent > burst {
			// Where the pace would have us by now; wait until we are no
			// further ahead than that.
			allowed := float64(burst) + rate*time.Since(start).Seconds()
			if ahead := float64(sent) - allowed; ahead > 0 {
				wait := time.Duration(ahead / rate * float64(time.Second))
				select {
				case <-ctx.Done():
					return sent, ctx.Err()
				case <-time.After(wait):
				}
			}
		}
		n, err := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			sent += int64(w)
			if werr != nil {
				return sent, werr
			}
			// Each piece reaches the network now, not when a buffer fills:
			// the pace is only real if the writes are.
			if rw, ok := dst.(http.ResponseWriter); ok {
				_ = http.NewResponseController(rw).Flush()
			}
		}
		if err == io.EOF {
			return sent, nil
		}
		if err != nil {
			return sent, err
		}
	}
}
