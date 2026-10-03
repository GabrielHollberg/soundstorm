package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/source"
)

// Photos by who is in them and where they were taken, and the photos from
// this day in earlier years. The photo backend does the recognizing
// (source.PhotoBrowser); this asks it, through the registry, so an account
// that may not see photos is refused like any other shelf.

const (
	photosDeadline  = 10 * time.Second
	groupPhotoLimit = 2000
	onThisDayEach   = 30
	maxPersonName   = 100
)

func (s *Server) photoBrowser(ctx context.Context) (source.PhotoBrowser, string, bool) {
	for _, src := range s.reg.All(ctx) {
		if src.Kind() != media.KindPicture {
			continue
		}
		if b, ok := src.(source.PhotoBrowser); ok {
			return b, src.ID(), true
		}
	}
	return nil, "", false
}

type photoGroupOut struct {
	source.PhotoGroup
	SourceID string `json:"sourceId"`
}

func groupsOut(groups []source.PhotoGroup, sourceID string) []photoGroupOut {
	out := make([]photoGroupOut, len(groups))
	for i, g := range groups {
		out[i] = photoGroupOut{PhotoGroup: g, SourceID: sourceID}
	}
	return out
}

// handlePeople lists the people found in the pictures, or with ?id= one
// person's pictures.
func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), photosDeadline)
	defer cancel()
	b, src, ok := s.photoBrowser(ctx)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"people": []photoGroupOut{}})
		return
	}
	if id := r.URL.Query().Get("id"); id != "" {
		items, err := b.PersonPhotos(ctx, id, groupPhotoLimit)
		if err != nil {
			s.photosError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
		return
	}
	people, err := b.People(ctx)
	if err != nil {
		s.photosError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"people": groupsOut(people, src)})
}

// handleNamePerson names somebody found in the pictures. Anybody who can see
// the photos may: a name is for the household, like the pictures themselves.
func (s *Server) handleNamePerson(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "expected a name")
		return
	}
	name := strings.TrimSpace(body.Name)
	id := r.URL.Query().Get("id")
	if id == "" || utf8.RuneCountInString(name) > maxPersonName {
		writeError(w, http.StatusBadRequest, "that name is too long")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), photosDeadline)
	defer cancel()
	b, _, ok := s.photoBrowser(ctx)
	if !ok {
		writeError(w, http.StatusNotFound, "no photos")
		return
	}
	if err := b.NamePerson(ctx, id, name); err != nil {
		s.photosError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// handlePlaces lists the towns the pictures were taken in, or with ?id= one
// town's pictures.
func (s *Server) handlePlaces(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), photosDeadline)
	defer cancel()
	b, src, ok := s.photoBrowser(ctx)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"places": []photoGroupOut{}})
		return
	}
	if id := r.URL.Query().Get("id"); id != "" {
		items, err := b.PlacePhotos(ctx, id, groupPhotoLimit)
		if err != nil {
			s.photosError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
		return
	}
	places, err := b.Places(ctx)
	if err != nil {
		s.photosError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"places": groupsOut(places, src)})
}

// handlePhotosOfType is the Photos tab's Videos and Live photos: ?type=video
// or ?type=live, newest first.
func (s *Server) handlePhotosOfType(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("type")
	if kind != "video" && kind != "live" {
		writeError(w, http.StatusBadRequest, "expected type=video or type=live")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), photosDeadline)
	defer cancel()
	b, _, ok := s.photoBrowser(ctx)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	items, err := b.PhotosOfType(ctx, kind, groupPhotoLimit)
	if err != nil {
		s.photosError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}

// onThisDayCache keeps a day's answer for an hour: Home asks on every visit,
// and the answer is twenty-five searches.
type onThisDayCache struct {
	mu   sync.Mutex
	key  string
	at   time.Time
	days []source.PhotoDay
}

// handleOnThisDay is the photos from this day in earlier years, newest year
// first. ?date=YYYY-MM-DD asks about another day.
func (s *Server) handleOnThisDay(w http.ResponseWriter, r *http.Request) {
	day := time.Now()
	if d := r.URL.Query().Get("date"); d != "" {
		parsed, err := time.Parse("2006-01-02", d)
		if err != nil {
			writeError(w, http.StatusBadRequest, "expected a date like 2024-06-01")
			return
		}
		day = parsed
	}
	ctx, cancel := context.WithTimeout(r.Context(), photosDeadline)
	defer cancel()
	b, src, ok := s.photoBrowser(ctx)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"days": []source.PhotoDay{}})
		return
	}
	// Per person: a member's photos are their own.
	key := src + "|" + source.UserID(ctx) + "|" + day.Format("2006-01-02")
	c := &s.onThisDay
	c.mu.Lock()
	if c.key == key && time.Since(c.at) < time.Hour {
		days := c.days
		c.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"days": days})
		return
	}
	c.mu.Unlock()
	days, err := b.OnThisDay(ctx, day, onThisDayEach)
	if err != nil {
		s.photosError(w, err)
		return
	}
	if days == nil {
		days = []source.PhotoDay{}
	}
	c.mu.Lock()
	c.key, c.at, c.days = key, time.Now(), days
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
}

func (s *Server) photosError(w http.ResponseWriter, err error) {
	s.log.Warn("photos", "err", err)
	writeError(w, http.StatusBadGateway, "the photo library did not answer")
}

var photoMonthRE = regexp.MustCompile(`^[12][0-9]{3}-(0[1-9]|1[0-2])$`)

// photoTimeline is the picture source as a timeline, when it can be one.
func (s *Server) photoTimeline(ctx context.Context) (source.PhotoTimeline, bool) {
	b, _, ok := s.photoBrowser(ctx)
	if !ok {
		return nil, false
	}
	t, ok := b.(source.PhotoTimeline)
	return t, ok
}

// GET /api/photos/months: every month of the asker's photos and how many,
// newest first - the timeline's outline and its scroll handle's years.
func (s *Server) handlePhotoMonths(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), photosDeadline)
	defer cancel()
	t, ok := s.photoTimeline(ctx)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"months": []any{}})
		return
	}
	months, err := t.PhotoMonths(ctx)
	if err != nil {
		s.photosError(w, err)
		return
	}
	if months == nil {
		months = []source.PhotoMonth{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"months": months})
}

// GET /api/photos/month?m=2024-01: that month's photos, newest first.
func (s *Server) handlePhotoMonth(w http.ResponseWriter, r *http.Request) {
	m := r.URL.Query().Get("m")
	if !photoMonthRE.MatchString(m) {
		writeError(w, http.StatusBadRequest, "expected m=YYYY-MM")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), photosDeadline)
	defer cancel()
	t, ok := s.photoTimeline(ctx)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	items, err := t.MonthPhotos(ctx, m)
	if err != nil {
		s.photosError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": nonNil(items)})
}
