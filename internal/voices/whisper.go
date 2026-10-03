package voices

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Whisper is the backend that writes speech down: whisper-asr-webservice
// (MIT, run unmodified) with faster-whisper's base.en, the model Storyteller
// uses. Each word comes back with where it is said.
type Whisper struct {
	BaseURL string
	HTTP    *http.Client
}

// Word is one word heard, and where, in seconds from the start of the piece.
type Word struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"word"`
}

// Health is whether the backend answers.
func (w *Whisper) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.BaseURL+"/docs", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("whisper: %s", resp.Status)
	}
	return nil
}

// Transcribe writes down a piece of audio (any format ffmpeg reads), word by
// word.
func (w *Whisper) Transcribe(ctx context.Context, audio []byte, name string) ([]Word, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("audio_file", name)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}
	mw.Close()
	url := w.BaseURL + "/asr?task=transcribe&language=en&output=json&word_timestamps=true&encode=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	client := w.HTTP
	if client == nil {
		client = &http.Client{Timeout: time.Hour}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("whisper: %s %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	var out struct {
		Segments []struct {
			Words []Word `json:"words"`
		} `json:"segments"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("whisper: %w", err)
	}
	var words []Word
	for _, s := range out.Segments {
		for _, wd := range s.Words {
			wd.Text = strings.TrimSpace(wd.Text)
			if wd.Text != "" {
				words = append(words, wd)
			}
		}
	}
	return words, nil
}
