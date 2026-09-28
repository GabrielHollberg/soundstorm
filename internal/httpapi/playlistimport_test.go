package httpapi

import (
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

func importSong(id, artist, title, path string, secs float64) source.SongFile {
	return source.SongFile{Path: path, Item: media.Item{ID: id, SourceID: "navidrome", Kind: media.KindMusic,
		Title: title, Creators: []string{artist}, DurationSeconds: secs}}
}

// A Plex export names songs by where they sat on the Plex server and by
// #EXTINF; each is found by its path's tail, then by artist and title, the
// length choosing between versions - and a song not in the library is
// reported, not guessed.
func TestImportMatchesByPathThenTags(t *testing.T) {
	ix := newSongIndex([]source.SongFile{
		importSong("1", "Radiohead", "Airbag", "Radiohead/OK Computer/01 Airbag.flac", 284),
		importSong("2", "Radiohead", "Creep", "Radiohead/Pablo Honey/02 Creep.mp3", 238),
		importSong("3", "Radiohead", "Creep", "Radiohead/Live/Creep (Live).mp3", 262),
		importSong("4", "The Beatles", "Let It Be", "The Beatles/Let It Be/06 Let It Be.m4a", 243),
		importSong("5", "Somebody", "Intro", "Somebody/First/01 Intro.mp3", 60),
	})
	name, entries := parseM3U("\ufeff#EXTM3U\r\n#PLAYLIST:Road trip\r\n" +
		"#EXTINF:284,Radiohead - Airbag\r\n/data/Music/Radiohead/OK Computer/01 Airbag.flac\r\n" +
		"#EXTINF:238,Radiohead - Creep\r\n" + `D:\Plex\Other\Creep.mp3` + "\r\n" +
		"#EXTINF:244,Beatles - Let It Be (Remastered 2009)\r\nfile:///srv/x/Let%20It%20Be.m4a\r\n" +
		"#EXTINF:243,The Beatles - Let It Be (Remastered)\r\n/srv/y/z.m4a\r\n" +
		"#EXTINF:200,Nobody - Missing Song\r\n/srv/missing.mp3\r\n" +
		"#EXTINF:300,Else - Intro\r\n/elsewhere/01 Intro.mp3\r\n")
	if name != "Road trip" {
		t.Errorf("name = %q, want the #PLAYLIST name", name)
	}
	want := []string{"1", "2", "4", "4", "", ""}
	if len(entries) != len(want) {
		t.Fatalf("parsed %d entries, want %d", len(entries), len(want))
	}
	for i, e := range entries {
		it, ok := ix.find(e)
		got := ""
		if ok {
			got = it.ID
		}
		if got != want[i] {
			t.Errorf("entry %d (%s) matched %q, want %q", i, e.label(), got, want[i])
		}
	}
}
