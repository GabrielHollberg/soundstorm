package source

import "testing"

func TestRelativeTo(t *testing.T) {
	cases := []struct {
		root, reported, want string
		ok                   bool
	}{
		{"/media/movies", "/media/movies/Dune (2021)/Dune.mkv", "Dune (2021)/Dune.mkv", true},
		{"/pictures/", "/pictures/2022/IMG_1.jpg", "2022/IMG_1.jpg", true},
		{"/media/movies", "/media/movies", "", false},           // the shelf itself
		{"/media/movies", "/media/movies-old/x.mkv", "", false}, // a sibling, not inside
		{"/media/movies", "/config/data/library.db", "", false},
		{"", "/anything", "", false},
	}
	for _, c := range cases {
		got, err := RelativeTo(c.root, c.reported)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("RelativeTo(%q, %q) = %q, %v", c.root, c.reported, got, err)
		}
	}
}
