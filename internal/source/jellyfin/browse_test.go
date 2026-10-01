package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// recordItems captures the query string /Items was called with.
func recordItems(t *testing.T) (*Source, *url.Values) {
	t.Helper()
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"Items": []any{}})
	}))
	t.Cleanup(srv.Close)

	s, err := New(Config{ID: "jellyfin", BaseURL: srv.URL, Token: "t", UserID: "u", ItemTypes: "Movie"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, &got
}

// An empty query must not be sent as searchTerm=.
//
// /Items with no searchTerm is Jellyfin's own browse and returns the library.
// Asking it to match the empty string is a different question, and not one it
// answers usefully - so the parameter is omitted rather than blanked.
func TestBrowseOmitsSearchTerm(t *testing.T) {
	s, got := recordItems(t)
	if _, err := s.Search(context.Background(), media.Query{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, present := (*got)["searchTerm"]; present {
		t.Errorf("searchTerm was sent on a browse: %v", (*got)["searchTerm"])
	}
	// Nothing to rank a browse by, so name order - and SortName is the field
	// Jellyfin itself sorts on, which ignores a leading "The".
	if (*got).Get("SortBy") != "SortName" {
		t.Errorf("SortBy = %q, want SortName", (*got).Get("SortBy"))
	}
}

// And a real search still sends it, without a sort that would override
// Jellyfin's own relevance ordering.
func TestSearchStillSendsSearchTerm(t *testing.T) {
	s, got := recordItems(t)
	if _, err := s.Search(context.Background(), media.Query{Text: "dune"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if (*got).Get("searchTerm") != "dune" {
		t.Errorf("searchTerm = %q, want dune", (*got).Get("searchTerm"))
	}
	if _, present := (*got)["SortBy"]; present {
		t.Error("a search should keep Jellyfin's own ordering, not force SortName")
	}
}

// An episode named after its file shows its title alone.
func TestFileNamedEpisodesShowTheirTitle(t *testing.T) {
	for name, want := range map[string]string{
		"Test Show - S01E02 - The Second One": "The Second One",
		"show.s02e10.Pilot":                   "Pilot",
		"The Real Title":                      "The Real Title",
	} {
		m := fileEpisodeName.FindStringSubmatch(name)
		got := name
		if m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%q shows as %q, want %q", name, got, want)
		}
	}
}

// Browsing television lists the shows; a search still finds episodes.
func TestTelevisionBrowsesShows(t *testing.T) {
	s, err := New(Config{ID: "jellyfin-tv", BaseURL: "http://jellyfin:8096", Token: "t", Kind: media.KindTV, ItemTypes: "Series,Episode"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.searchParams(media.Query{}).Get("IncludeItemTypes"); got != "Series" {
		t.Errorf("browse asks for %q, want Series", got)
	}
	if got := s.searchParams(media.Query{Text: "pilot"}).Get("IncludeItemTypes"); got != "Series,Episode" {
		t.Errorf("search asks for %q, want Series,Episode", got)
	}
}

// A file's audio languages are offered only when there is a choice.
func TestAudioTracksOnlyWhenThereIsAChoice(t *testing.T) {
	two := 1
	tracks := audioTracks([]mediaStream{
		{Index: 0, Type: "Video"},
		{Index: 1, Type: "Audio", Language: "eng", DisplayTitle: "English - AAC - Stereo"},
		{Index: 2, Type: "Audio", Language: "spa", Title: "Español"},
	}, &two)
	if len(tracks) != 2 || !tracks[0].Default || tracks[1].Label != "Spanish - Español" || tracks[1].Index != 2 {
		t.Errorf("tracks = %+v", tracks)
	}
	if one := audioTracks([]mediaStream{{Index: 1, Type: "Audio"}}, nil); one != nil {
		t.Errorf("one track offered as a choice: %+v", one)
	}
}

// A Blu-ray's own names for its tracks only repeat the channels, so a track
// is labelled by what it is, and says when it will come out with fewer
// channels than it has.
func TestAudioLabelsSayWhatATrackIs(t *testing.T) {
	streams := []mediaStream{
		{Index: 1, Type: "Audio", Language: "eng", Codec: "truehd", Profile: "Dolby TrueHD + Dolby Atmos", Channels: 8, Title: "Surround 7.1"},
		{Index: 3, Type: "Audio", Language: "fra", Codec: "dts", Profile: "DTS-HD MA", Channels: 6, Title: "Surround 5.1"},
		{Index: 6, Type: "Audio", Language: "eng", Codec: "ac3", Channels: 2, Title: "Commentary by the director"},
	}
	tracks := playsAs(audioTracks(streams, nil), streams, 6)
	want := []string{
		"English · Dolby TrueHD Atmos 7.1 (plays as 5.1)",
		"French · DTS-HD MA 5.1",
		"English · Dolby Digital stereo - Commentary by the director",
	}
	for i, w := range want {
		if tracks[i].Label != w {
			t.Errorf("track %d = %q, want %q", i, tracks[i].Label, w)
		}
	}
}
