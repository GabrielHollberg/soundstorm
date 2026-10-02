package library

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/GabrielHollberg/soundstorm/internal/media"
)

// Adding a file whose name is already taken (the owner's design, after
// copying files in Windows): an exact copy is skipped without asking, and a
// different file with the same name is asked about - keep both, skip, or
// (the owner) replace. Asked before anything is sent: the client asks
// which of its files would land on a taken name (CheckDest), compares the
// file's Sample with the one already there, and sends only what it should
// with what was decided (SaveOptions.Conflict).

// sampleChunk is how much of each part of a file a Sample reads.
const sampleChunk = 1 << 20

// Sample fingerprints a file cheaply enough to do on a phone before sending
// a film: SHA-256 over its length (8 bytes, big-endian) and its first, middle
// and last megabyte - the whole file when it is three megabytes or less. Two
// files of the same length and the same Sample are taken for the same file.
// The page computes it the same way (fileSample in app.js).
func Sample(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	return sampleOf(f, info.Size())
}

func sampleOf(r io.ReaderAt, size int64) (string, error) {
	h := sha256.New()
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(size))
	h.Write(n[:])
	part := func(from, length int64) error {
		_, err := io.Copy(h, io.NewSectionReader(r, from, length))
		return err
	}
	if size <= 3*sampleChunk {
		if err := part(0, size); err != nil {
			return "", err
		}
	} else {
		for _, from := range []int64{0, size/2 - sampleChunk/2, size - sampleChunk} {
			if err := part(from, sampleChunk); err != nil {
				return "", err
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DestState is what is at a planned destination: nothing, or a file of some
// size and Sample.
type DestState struct {
	Taken  bool   `json:"taken"`
	Size   int64  `json:"size,omitempty"`
	Sample string `json:"sample,omitempty"`
}

// CheckDest looks at a destination as a plan gave it ("movies/Dune/Dune.mkv",
// relative to the library root), which must be on the given shelf. The
// Sample is worked out only when the size matches the one coming, as only
// then can the two be the same.
func (l *Library) CheckDest(kind media.Kind, dest string, size int64) (DestState, error) {
	abs, err := l.shelfFile(kind, dest)
	if err != nil {
		return DestState{}, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return DestState{}, nil
	}
	st := DestState{Taken: true, Size: info.Size()}
	if info.Mode().IsRegular() && info.Size() == size {
		if s, err := Sample(abs); err == nil {
			st.Sample = s
		}
	}
	return st, nil
}

// shelfFile turns a library-relative path into where it is, refusing one
// that is not on the given shelf or leaves it.
func (l *Library) shelfFile(kind media.Kind, dest string) (string, error) {
	rel, err := cleanRelPath(dest)
	if err != nil {
		return "", err
	}
	name := folderName(kind)
	if name == "" || !strings.HasPrefix(rel, name+"/") {
		return "", fmt.Errorf("that is not on the %s shelf", kind)
	}
	folder := l.PathFor(kind)
	if folder == "" {
		return "", fmt.Errorf("there is no %s library", kind)
	}
	abs := filepath.Join(folder, filepath.FromSlash(strings.TrimPrefix(rel, name+"/")))
	if !within(folder, abs) {
		return "", fmt.Errorf("that path does not stay inside the library")
	}
	return abs, nil
}

// Conflict is what to do when a different file already has the name.
type Conflict string

const (
	// ConflictRefuse leaves what is there and refuses the new one.
	ConflictRefuse Conflict = ""
	// ConflictKeep keeps both: the new one is named for what makes it
	// different (distinct.go), or as the person typed.
	ConflictKeep Conflict = "keep"
	// ConflictReplace puts what is there in the bin and saves the new one.
	ConflictReplace Conflict = "replace"
)

// SaveOptions are how a save treats a taken name. An exact copy is always
// refused as already there, whatever was decided.
type SaveOptions struct {
	Conflict Conflict
	// Name is the file name somebody chose for the new one, kept both; ""
	// for one made from what makes it different.
	Name string
	// Replace puts the file at a library-relative path in the bin; needed
	// for ConflictReplace (the caller decides who may).
	Replace func(rel string) error
}

// sameFile is whether two files are the same by length and Sample.
func sameFile(a, b string) bool {
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	if err1 != nil || err2 != nil || !ib.Mode().IsRegular() || ia.Size() != ib.Size() {
		return false
	}
	sa, err1 := Sample(a)
	sb, err2 := Sample(b)
	return err1 == nil && err2 == nil && sa == sb
}
