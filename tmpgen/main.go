package main

import (
	"context"
	"fmt"
	"os"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source/jellyfin"
)

func main() {
	base, tok, uid := os.Args[1], os.Args[2], os.Args[3]
	sources := []jellyfin.Config{
		{ID: "jellyfin", BaseURL: base, Token: tok, UserID: uid, Kind: media.KindVideo, ItemTypes: "Movie"},
		{ID: "jellyfin-tv", BaseURL: base, Token: tok, UserID: uid, Kind: media.KindTV, ItemTypes: "Series,Episode"},
	}
	for _, cfg := range sources {
		s, err := jellyfin.New(cfg)
		if err != nil {
			panic(err)
		}
		for _, q := range []media.Query{{}, {Text: "Dune"}, {Text: "Sandworms"}, {Text: "Blade"}} {
			label := q.Text
			if label == "" {
				label = "(browse)"
			}
			items, err := s.Search(context.Background(), q)
			if err != nil {
				fmt.Printf("%-12s %-12s error: %v\n", cfg.ID, label, err)
				continue
			}
			fmt.Printf("%-12s %-12s %d item(s)", cfg.ID, label, len(items))
			for _, it := range items {
				fmt.Printf("  [%s]", it.Title)
			}
			fmt.Println()
		}
	}
}
