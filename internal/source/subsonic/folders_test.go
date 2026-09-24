package subsonic

import "testing"

// The folders decide, whatever the tags say: a featured artist does not
// become an artist of their own, an album whose tracks disagree about the
// year stays one album, disc folders stay inside their album, and a loose
// song is one of its artist's singles.
func TestGroupByFolderFollowsTheFolders(t *testing.T) {
	lib := groupByFolder([]song{
		{ID: "1", Path: "Aurora Lane/Tidewater/01 - Harbour.mp3", Artist: "Aurora Lane", Year: 2022, Track: 1, CoverArt: "al-a"},
		{ID: "2", Path: "Aurora Lane/Tidewater/02 - Undertow.mp3", Artist: "Aurora Lane feat. Guest", Year: 2021, Track: 2, CoverArt: "al-b"},
		{ID: "3", Path: "Aurora Lane/Tidewater/03 - Low Tide.mp3", Artist: "Aurora Lane", Year: 2022, Track: 3, CoverArt: "al-a"},
		{ID: "4", Path: "The Parallels/Signal/CD2/01 - Static.mp3", Track: 1, DiscNumber: 2},
		{ID: "5", Path: "The Parallels/Signal/CD1/02 - Carrier Wave.mp3", Track: 2, DiscNumber: 1},
		{ID: "6", Path: "The Parallels/Signal/CD1/01 - Broadcast.mp3", Track: 1, DiscNumber: 1},
		{ID: "7", Path: "The Parallels/Loose Single.mp3"},
		{ID: "8", Path: "AC-DC/Live 1992/01 - Thunderstruck.mp3", AlbumArtist: "AC/DC", Artist: "AC/DC", Album: "Live: 1992"},
		{ID: "9", Path: "Digital Booklet - Midnight Memories.itlp/audio/intro.m4a"},
	})

	if ac := lib.byName["AC-DC"]; ac == nil || ac.name != "AC/DC" || ac.albums[0].title != "Live: 1992" {
		t.Errorf("AC-DC folder = %+v, want shown as the tags spell it: AC/DC, Live: 1992", ac)
	}
	if tide := lib.byAlbum["Aurora Lane/Tidewater"]; tide != nil && tide.artist != "Aurora Lane" {
		t.Errorf("a featured artist's spelling won: %q", tide.artist)
	}
	delete(lib.byName, "AC-DC")
	for i, a := range lib.artists {
		if a.folder == "AC-DC" {
			lib.artists = append(lib.artists[:i], lib.artists[i+1:]...)
			break
		}
	}
	if len(lib.artists) != 2 {
		names := []string{}
		for _, a := range lib.artists {
			names = append(names, a.name)
		}
		t.Fatalf("artists = %v, want the two folders", names)
	}
	tide := lib.byAlbum["Aurora Lane/Tidewater"]
	if tide == nil || len(tide.songs) != 3 {
		t.Fatalf("Tidewater = %+v, want one album of three songs", tide)
	}
	if tide.year != 2022 || tide.art != "al-a" {
		t.Errorf("Tidewater year %d art %q, want the most common: 2022, al-a", tide.year, tide.art)
	}

	signal := lib.byAlbum["The Parallels/Signal"]
	if signal == nil {
		t.Fatal("no Signal album: disc folders made albums of their own")
	}
	var order []string
	for _, sg := range signal.songs {
		order = append(order, sg.ID)
	}
	if got := order; len(got) != 3 || got[0] != "6" || got[1] != "5" || got[2] != "4" {
		t.Errorf("Signal plays %v, want disc 1 in track order, then disc 2: [6 5 4]", got)
	}
	if lib.byAlbum["The Parallels/"+singles] == nil {
		t.Error("the loose song is not among The Parallels' singles")
	}

	// Albums sort by name without "The", and ids round-trip.
	if sortKey("The Parallels") != "parallels" {
		t.Errorf("sortKey kept the article: %q", sortKey("The Parallels"))
	}
	if p, ok := folderPath(folderID("AC-DC/Live [1992]")); !ok || p != "AC-DC/Live [1992]" {
		t.Errorf("folder id did not round-trip: %q %v", p, ok)
	}
	if _, ok := folderPath("7jOXlI9zgqBM3037eOU2qO"); ok {
		t.Error("a Navidrome album id was taken for a folder id")
	}
}
