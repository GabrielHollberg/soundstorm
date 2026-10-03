package voices

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Job is one book being read aloud.
type Job struct {
	ID     string `json:"id"`
	UserID string `json:"userId"`
	// The ebook, by its shelf and id (for the page) and its file.
	Source string `json:"source"`
	Item   string `json:"item"`
	Ebook  string `json:"ebook"`
	File   string `json:"file"`
	Title  string `json:"title"`
	Author string `json:"author"`
	Voice  string `json:"voice"`

	// State is waiting, working, done, failed or cancelled.
	State     string    `json:"state"`
	Chapters  int       `json:"chapters"`
	ChapDone  int       `json:"chaptersDone"`
	Words     int       `json:"words"`
	WordsDone int       `json:"wordsDone"`
	Added     time.Time `json:"added"`
	Started   time.Time `json:"started,omitempty"`
	Finished  time.Time `json:"finished,omitempty"`
	// Seconds of speech made per second of work, for the time left.
	Pace    float64 `json:"pace,omitempty"`
	Problem string  `json:"problem,omitempty"`
	// Where it went, relative to the audiobook shelf.
	Dest string `json:"dest,omitempty"`
}

// Placer puts a finished book's folder of files on the audiobook shelf and
// answers where, relative to the shelf.
type Placer func(job *Job, staged string) (string, error)

// Manager is the queue: one book at a time, kept on disk so a restart carries
// on (the chapters already made are kept).
type Manager struct {
	dir    string
	voice  *Kokoro
	place  Placer
	log    *slog.Logger
	mu     sync.Mutex
	jobs   []*Job
	wake   chan struct{}
	cancel context.CancelFunc
	runID  string
}

// NewManager opens the queue kept in dir.
func NewManager(dir string, voice *Kokoro, place Placer, log *slog.Logger) *Manager {
	m := &Manager{dir: dir, voice: voice, place: place, log: log, wake: make(chan struct{}, 1)}
	if data, err := os.ReadFile(filepath.Join(dir, "jobs.json")); err == nil {
		_ = json.Unmarshal(data, &m.jobs)
	}
	for _, j := range m.jobs {
		if j.State == "working" {
			j.State = "waiting" // carried on after a restart
		}
	}
	return m
}

// Voice is the backend, for listing voices and samples.
func (m *Manager) Voice() *Kokoro { return m.voice }

func (m *Manager) save() {
	_ = os.MkdirAll(m.dir, 0o700)
	data, _ := json.MarshalIndent(m.jobs, "", "  ")
	tmp := filepath.Join(m.dir, "jobs.json.new")
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, filepath.Join(m.dir, "jobs.json"))
	}
}

// Jobs is the queue, newest finished last; finished ones are kept a week.
func (m *Manager) Jobs() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	return out
}

// Add queues a book. Ebook is its file; title and author are the book's.
func (m *Manager) Add(userID, source, item, ebook, file, title, author, voice string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.Source == source && j.Item == item && (j.State == "waiting" || j.State == "working") {
			return Job{}, errors.New("this book is already being read aloud")
		}
	}
	waiting := 0
	for _, j := range m.jobs {
		if j.State == "waiting" {
			waiting++
		}
	}
	if waiting >= 20 {
		return Job{}, errors.New("twenty books are already waiting")
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	j := &Job{ID: hex.EncodeToString(b), UserID: userID, Source: source, Item: item, Ebook: ebook, File: file,
		Title: title, Author: author, Voice: voice, State: "waiting", Added: time.Now()}
	m.jobs = append(m.jobs, j)
	m.prune()
	m.save()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return *j, nil
}

// Cancel stops a book: waiting, it is taken off; being made, it stops and its
// chapters so far are deleted.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ID != id || (j.State != "waiting" && j.State != "working") {
			continue
		}
		if j.State == "working" && m.cancel != nil && m.runID == id {
			m.cancel()
		}
		j.State = "cancelled"
		j.Finished = time.Now()
		_ = os.RemoveAll(m.work(j))
		m.save()
		return true
	}
	return false
}

func (m *Manager) prune() {
	keep := m.jobs[:0]
	for _, j := range m.jobs {
		if (j.State == "done" || j.State == "failed" || j.State == "cancelled") && time.Since(j.Finished) > 7*24*time.Hour {
			continue
		}
		keep = append(keep, j)
	}
	m.jobs = keep
}

func (m *Manager) work(j *Job) string { return filepath.Join(m.dir, "work", j.ID) }

// Run works through the queue until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	for {
		j := m.next()
		if j == nil {
			select {
			case <-ctx.Done():
				return
			case <-m.wake:
			case <-time.After(time.Minute):
			}
			continue
		}
		jctx, cancel := context.WithCancel(ctx)
		m.mu.Lock()
		m.cancel, m.runID = cancel, j.ID
		m.mu.Unlock()
		err := m.make(jctx, j)
		cancel()
		m.mu.Lock()
		m.cancel, m.runID = nil, ""
		if j.State == "working" {
			j.Finished = time.Now()
			if err != nil {
				j.State = "failed"
				j.Problem = err.Error()
				m.log.Warn("making an audiobook failed", "book", j.Title, "err", err)
			} else {
				j.State = "done"
				m.log.Info("made an audiobook", "book", j.Title, "dest", j.Dest)
			}
			m.save()
		}
		m.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
	}
}

func (m *Manager) next() *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.State == "waiting" {
			j.State = "working"
			if j.Started.IsZero() {
				j.Started = time.Now()
			}
			m.save()
			return j
		}
	}
	return nil
}

// make reads one book aloud, a file per chapter, then has it placed.
func (m *Manager) make(ctx context.Context, j *Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("stopped unexpectedly: %v", r)
		}
	}()
	title, author, chapters, err := Chapters(j.Ebook)
	if err != nil {
		return fmt.Errorf("could not read the ebook")
	}
	if len(chapters) == 0 {
		return errors.New("the ebook has no text to read")
	}
	if j.Title == "" {
		j.Title = title
	}
	if j.Author == "" {
		j.Author = author
	}
	words := 0
	for _, c := range chapters {
		words += len(strings.Fields(c.Text))
	}
	m.mu.Lock()
	j.Chapters, j.Words = len(chapters), words
	m.save()
	m.mu.Unlock()

	work := m.work(j)
	if err := os.MkdirAll(work, 0o700); err != nil {
		return err
	}
	narrator := "AI voice: " + VoiceName(j.Voice)
	digits := len(itoa(len(chapters)))
	done := 0
	for i, c := range chapters {
		name := fmt.Sprintf("%0*d - %s.mp3", digits, i+1, fileSafe(c.Title))
		final := filepath.Join(work, name)
		cw := len(strings.Fields(c.Text))
		if _, err := os.Stat(final); err == nil {
			done += cw
			m.progress(j, i+1, done, 0, 0)
			continue // made before a restart
		}
		f, err := os.Create(final + ".part")
		if err != nil {
			return err
		}
		tag := id3([][2]string{
			{"TIT2", c.Title}, {"TALB", j.Title}, {"TPE1", j.Author}, {"TPE2", j.Author},
			{"TCOM", narrator}, {"TRCK", fmt.Sprintf("%d/%d", i+1, len(chapters))}, {"TCON", "Audiobook"},
		})
		start := time.Now()
		_, err = f.Write(tag)
		if err == nil {
			err = m.voice.Speak(ctx, c.Title+".\n"+c.Text, j.Voice, f)
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(final + ".part")
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("the voice could not read chapter %d: %w", i+1, err)
		}
		if err := os.Rename(final+".part", final); err != nil {
			return err
		}
		done += cw
		// About 150 words a minute of speech: the pace is speech per work.
		m.progress(j, i+1, done, float64(cw)/2.5, time.Since(start).Seconds())
	}
	if cover, ext := coverOf(j.Ebook); cover != nil {
		_ = os.WriteFile(filepath.Join(work, "cover"+ext), cover, 0o600)
	}
	dest, err := m.place(j, work)
	if err != nil {
		return fmt.Errorf("could not put it on the shelf: %w", err)
	}
	m.mu.Lock()
	j.Dest = dest
	m.mu.Unlock()
	_ = os.RemoveAll(work)
	return nil
}

func (m *Manager) progress(j *Job, chapters, words int, speech, took float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.ChapDone, j.WordsDone = chapters, words
	if took > 0 && speech > 0 {
		p := speech / took
		if j.Pace == 0 {
			j.Pace = p
		} else {
			j.Pace = j.Pace*0.7 + p*0.3
		}
	}
	m.save()
}

// fileSafe makes a chapter title a file name.
func fileSafe(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		if r < 32 {
			return -1
		}
		return r
	}, s)
	s = strings.Trim(strings.TrimSpace(s), ". ")
	if len(s) > 80 {
		s = strings.TrimSpace(s[:80])
	}
	if s == "" {
		s = "Chapter"
	}
	return s
}
