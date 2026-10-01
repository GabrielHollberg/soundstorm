package httpapi

import (
	"net/http"

	"github.com/GabrielHollberg/soundstorm/internal/source"
	"github.com/GabrielHollberg/soundstorm/internal/stream"
)

// Film qualities, chosen per device in Playback on this device (?vq=).
//
// A film used to be sent at 20 Mbit at most, which is under what a Blu-ray
// rip carries (about 40), so Jellyfin re-encoded every frame of the picture
// on the fly, at a fast, lossy setting - a copy of a copy - when the picture
// was already one any browser plays. Above the file's own bitrate it copies
// the picture untouched, which is also far less work for the server; only
// sound a browser cannot play (TrueHD, DTS) is still converted.
var (
	// filmOriginal lets any picture through untouched.
	filmOriginal = source.VideoQuality{MaxBitrate: 200_000_000, MaxAudioChannels: 6}
	// filmStandard is the old cap: enough for 1080p, light on a connection.
	filmStandard = source.VideoQuality{MaxBitrate: 20_000_000, MaxAudioChannels: 6}
	// filmSaver is for a phone on mobile data: 720p at 4 Mbit, stereo.
	filmSaver = source.VideoQuality{MaxBitrate: 4_000_000, MaxAudioChannels: 2, MaxWidth: 1280}
)

// filmQuality is the quality a playback request asked for. "smart", the
// default (and what a client that does not ask gets), is the original at home
// and the standard copy from an away-from-home name, where few connections
// carry a Blu-ray's bitrate.
func filmQuality(r *http.Request) source.VideoQuality {
	switch r.URL.Query().Get("vq") {
	case "original":
		return filmOriginal
	case "standard":
		return filmStandard
	case "saver":
		return filmSaver
	}
	if stream.AwayFromHome(r) {
		return filmStandard
	}
	return filmOriginal
}
