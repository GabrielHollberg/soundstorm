package httpapi

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/GabrielHollberg/soundstorm/internal/collections"
	"github.com/GabrielHollberg/soundstorm/internal/discover"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
	"strings"
)

// Music discovery: an artist's bio and the artists like them, from
// MusicBrainz, ListenBrainz and Wikipedia (internal/discover), and what that
// makes possible - an artist's radio of artists like them, and "More like"
// mixes around the artists somebody plays most. Only ever with the owner's
// say-so: it sends artist names out of the house. And only artists in the
// library are ever offered - discovery here is of what you already have.

const discoverDeadline = 15 * time.Second

func (s *Server) discovering() bool {
	return s.discover != nil && s.store.OnlineDiscovery()
}

// handleSetOnlineDiscovery turns music discovery on or off. Owner-only: it
// decides whether artist names leave the house.
func (s *Server) handleSetOnlineDiscovery(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1024)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a JSON body with enabled")
		return
	}
	if s.discover == nil {
		writeError(w, http.StatusNotImplemented, "music discovery is not available")
		return
	}
	if err := s.store.SetOnlineDiscovery(body.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "could not save the setting")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enabled})
}

// libraryArtists is which of these artists are in the library, in their
// order, up to limit, leaving out the one called except.
func libraryArtists(ctx context.Context, browser source.MusicBrowser, similar []discover.Artist, except string, limit int) []source.Artist {
	all, err := browser.Artists(ctx)
	if err != nil {
		return nil
	}
	byName := map[string]source.Artist{}
	for _, a := range all {
		byName[nameKey(a.Name)] = a
	}
	skip := nameKey(except)
	var out []source.Artist
	seen := map[string]bool{}
	for _, sim := range similar {
		key := nameKey(sim.Name)
		a, ok := byName[key]
		if !ok || key == skip || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, a)
		if len(out) == limit {
			break
		}
	}
	return out
}

// handleArtistAbout is an artist's bio and the artists like them that are in
// the library - or, with music discovery off, only that it is off.
func (s *Server) handleArtistAbout(w http.ResponseWriter, r *http.Request) {
	src, ok := s.reg.ByID(r.Context(), r.PathValue("source"))
	if !ok {
		writeError(w, http.StatusNotFound, "no such shelf")
		return
	}
	browser, ok := src.(source.MusicBrowser)
	if !ok {
		writeError(w, http.StatusNotFound, "that shelf has no artists")
		return
	}
	if !s.discovering() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), discoverDeadline)
	defer cancel()
	artist, _, err := browser.Artist(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such artist")
		return
	}
	info, found, err := s.discover.Artist(ctx, artist.Name)
	if err != nil {
		s.log.Warn("music discovery", "err", err)
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "error": "could not be looked up just now"})
		return
	}
	similar := libraryArtists(ctx, browser, info.Similar, artist.Name, 12)
	if similar == nil {
		similar = []source.Artist{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true, "found": found, "bio": info.Bio, "bioUrl": info.BioURL, "similar": similar,
	})
}

// similarSongs is a few songs from each of up to maxArtists artists like
// this one that are in the library, shuffled - nil with discovery off.
func (s *Server) similarSongs(r *http.Request, browser source.MusicBrowser, name string, maxArtists, perArtist int) []media.Item {
	if !s.discovering() {
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), discoverDeadline)
	defer cancel()
	info, _, err := s.discover.Artist(ctx, name)
	if err != nil {
		return nil
	}
	var songs []media.Item
	for _, a := range libraryArtists(ctx, browser, info.Similar, name, maxArtists) {
		_, albums, err := browser.Artist(ctx, a.ID)
		if err != nil || len(albums) == 0 {
			continue
		}
		rand.Shuffle(len(albums), func(i, j int) { albums[i], albums[j] = albums[j], albums[i] })
		var theirs []media.Item
		for _, al := range albums[:min(2, len(albums))] {
			if _, tracks, err := browser.Album(ctx, al.ID); err == nil {
				theirs = append(theirs, tracks...)
			}
		}
		theirs = shuffleItems(theirs)
		songs = append(songs, theirs[:min(perArtist, len(theirs))]...)
	}
	return shuffleItems(songs)
}

// moreLike is a mix of the artists like this one that are in the library.
func (s *Server) moreLike(r *http.Request, src source.Source, artistID string) ([]media.Item, error) {
	browser, ok := src.(source.MusicBrowser)
	if !ok {
		return nil, nil
	}
	artist, _, err := browser.Artist(r.Context(), artistID)
	if err != nil {
		return nil, err
	}
	return s.similarSongs(r, browser, artist.Name, 10, 5), nil
}

// moreLikeCards are "More like" mixes around the artists this person plays
// most, for those already looked up - never waiting on the network to show
// the mixes. The rest are looked up in the background, so they appear the
// next time the mixes are opened.
func (s *Server) moreLikeCards(r *http.Request, src source.Source, byCount []collections.Play) []mixCard {
	if !s.discovering() {
		return nil
	}
	browser, ok := src.(source.MusicBrowser)
	if !ok {
		return nil
	}
	all, err := browser.Artists(r.Context())
	if err != nil {
		return nil
	}
	byName := map[string]source.Artist{}
	for _, a := range all {
		byName[nameKey(a.Name)] = a
	}
	var cards []mixCard
	seen := map[string]bool{}
	for _, p := range byCount {
		if len(p.Item.Creators) == 0 || len(seen) == 3 {
			continue
		}
		name := p.Item.Creators[0]
		artist, inLibrary := byName[nameKey(name)]
		if !inLibrary || seen[artist.ID] {
			continue
		}
		seen[artist.ID] = true
		info, known := s.discover.Cached(artist.Name)
		if !known {
			go s.lookUpLater(artist.Name)
			continue
		}
		similar := libraryArtists(r.Context(), browser, info.Similar, artist.Name, 8)
		if len(similar) < 2 {
			continue
		}
		var art []string
		for _, a := range similar {
			if a.ArtID != "" && len(art) < 4 {
				art = append(art, a.ArtID)
			}
		}
		cards = append(cards, mixCard{ID: "like:" + artist.ID, Title: "More like " + artist.Name,
			Subtitle: "Artists like them in your library", Covers: art, SourceID: src.ID()})
	}
	return cards
}

// lookUpLater asks about an artist with nobody waiting on the answer.
func (s *Server) lookUpLater(name string) {
	// One at a time per artist, and a few in all: every page view asked
	// again for each artist not yet known, and they piled up waiting on the
	// one-a-second limit (a security review).
	key := strings.ToLower(name)
	s.lookingUpMu.Lock()
	if s.lookingUp == nil {
		s.lookingUp = map[string]bool{}
	}
	if s.lookingUp[key] || len(s.lookingUp) >= 32 {
		s.lookingUpMu.Unlock()
		return
	}
	s.lookingUp[key] = true
	s.lookingUpMu.Unlock()
	defer func() {
		s.lookingUpMu.Lock()
		delete(s.lookingUp, key)
		s.lookingUpMu.Unlock()
	}()
	defer func() { _ = recover() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, _, err := s.discover.Artist(ctx, name); err != nil {
		s.log.Debug("music discovery in the background", "err", err)
	}
}
