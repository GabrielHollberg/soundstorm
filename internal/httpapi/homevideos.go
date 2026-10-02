package httpapi

// Home videos go with the photos (the owner's choice). A video dropped where
// the plan said "film" - a renamed clip, "Jack's 5th birthday.mov", looks
// exactly like one - is looked inside once it has arrived: a phone or a
// camera writes its make and model, or where it was filmed, into every video,
// and a film never carries those (tags.VideoCamera). Such a video goes to
// the person's own photo folder by when it was filmed, as a clip dropped by
// its camera name always has. Anything else stays a film.

import (
	"io"
	"path"
	"strings"

	"github.com/GabrielHollberg/soundstorm/internal/library"
	"github.com/GabrielHollberg/soundstorm/internal/media"
	"github.com/GabrielHollberg/soundstorm/internal/state"
	"github.com/GabrielHollberg/soundstorm/internal/tags"
)

// videoFilmed reads what a filming device wrote inside a video; nothing for
// anything that is not an MP4 or a MOV.
func videoFilmed(staged, name string) tags.Camera {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4", ".mov", ".m4v", ".3gp":
		return tags.VideoCamera(staged)
	}
	return tags.Camera{}
}

// saveFilmOrHomeVideo saves a video dropped as a film: to Films, or, when a
// phone or camera filmed it, to the person's photos. It says which.
func (s *Server) saveFilmOrHomeVideo(u state.User, dropped string, body io.Reader, hint, size int64, opts library.SaveOptions) (string, media.Kind, error) {
	var pl *photoPlace
	kind := media.KindVideo
	dest, err := s.library.SaveWith(media.KindVideo, dropped, body, func(staged string) (media.Kind, string, error) {
		if !videoFilmed(staged, path.Base(dropped)).Filmed {
			return media.KindVideo, "", nil
		}
		if _, err := s.library.EnsurePersonalFolder(u.Name); err != nil {
			return "", "", err
		}
		if !u.IsOwner() {
			if err := s.photoRoom(u, size); err != nil {
				return "", "", err
			}
		}
		p, err := s.datedPhoto(u, staged, dropped, hint)
		if err != nil {
			return "", "", err
		}
		pl, kind = p, media.KindPicture
		return media.KindPicture, p.rel, nil
	}, opts)
	if err != nil {
		return "", kind, err
	}
	if pl != nil {
		s.photoSaved(u, dest, pl)
	}
	return dest, kind, nil
}
