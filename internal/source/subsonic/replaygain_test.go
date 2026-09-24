package subsonic

import (
	"encoding/json"
	"testing"
)

// Navidrome 0.64.1's getSong for a tagged MP3 carries OpenSubsonic's
// replayGain; the player reads it from Extra.
func TestReplayGainReachesTheItem(t *testing.T) {
	var sg song
	if err := json.Unmarshal([]byte(`{"id":"1","title":"Harbour","replayGain":{"trackGain":-5,"albumGain":-4.5,"trackPeak":0.9,"albumPeak":0.95}}`), &sg); err != nil {
		t.Fatal(err)
	}
	s := &Source{id: "navidrome"}
	it := s.songItem(sg)
	for key, want := range map[string]string{"trackGain": "-5.00", "albumGain": "-4.50", "trackPeak": "0.90", "albumPeak": "0.95"} {
		if it.Extra[key] != want {
			t.Errorf("%s = %q, want %q", key, it.Extra[key], want)
		}
	}
	var untagged song
	_ = json.Unmarshal([]byte(`{"id":"2","title":"Untagged"}`), &untagged)
	if _, ok := s.songItem(untagged).Extra["trackGain"]; ok {
		t.Error("an untagged song reported a gain")
	}
}
