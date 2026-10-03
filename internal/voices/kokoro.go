package voices

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

// Kokoro is the voice backend: Kokoro-FastAPI, an OpenAI-shaped speech API.
type Kokoro struct {
	BaseURL string
	HTTP    *http.Client
}

func (k *Kokoro) client() *http.Client {
	if k.HTTP != nil {
		return k.HTTP
	}
	// A long chapter takes minutes to read aloud; the request waits.
	return &http.Client{Timeout: 2 * time.Hour}
}

// Health is whether the voice backend answers.
func (k *Kokoro) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.BaseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("voices: %s", resp.Status)
	}
	return nil
}

// Voices lists the voices to choose from, by id ("af_heart").
func (k *Kokoro) Voices(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.BaseURL+"/v1/audio/voices", nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Voices []json.RawMessage `json:"voices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, err
	}
	var out []string
	for _, v := range body.Voices {
		// Either a name or {"id": name}, by version.
		var name string
		if json.Unmarshal(v, &name) != nil {
			var o struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(v, &o) != nil {
				continue
			}
			name = o.ID
		}
		if name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Speak reads text aloud in a voice, writing MP3 to w.
func (k *Kokoro) Speak(ctx context.Context, text, voice string, w io.Writer) error {
	body, _ := json.Marshal(map[string]any{
		"model": "kokoro", "input": text, "voice": voice, "response_format": "mp3", "speed": 1.0,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.BaseURL+"/v1/audio/speech", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := k.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("voices: %s %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	n, err := io.Copy(w, resp.Body)
	if err == nil && n == 0 {
		err = errors.New("voices: no sound came back")
	}
	return err
}

// VoiceName is a voice id as a person reads it: "af_heart" is "Heart".
func VoiceName(id string) string {
	name := id
	if i := strings.IndexByte(id, '_'); i >= 0 {
		name = id[i+1:]
	}
	name = strings.ReplaceAll(name, "_", " ")
	if name == "" {
		return id
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// VoiceAccent says what kind of voice an id is, from Kokoro's prefixes.
func VoiceAccent(id string) string {
	if len(id) < 2 {
		return ""
	}
	lang := map[byte]string{'a': "American", 'b': "British", 'e': "Spanish", 'f': "French", 'h': "Hindi", 'i': "Italian", 'j': "Japanese", 'p': "Portuguese", 'z': "Chinese"}[id[0]]
	who := map[byte]string{'f': "woman", 'm': "man"}[id[1]]
	return strings.TrimSpace(lang + " " + who)
}

// id3 is an ID3v2.3 tag of text frames, in UTF-16, for the front of an MP3:
// what tells the audiobook server the book, its author, the chapter and that
// the narrator is an AI voice (TCOM, which Audiobookshelf reads as narrator).
func id3(frames [][2]string) []byte {
	var body []byte
	for _, f := range frames {
		if f[1] == "" {
			continue
		}
		u := utf16.Encode([]rune(f[1]))
		data := []byte{1, 0xFF, 0xFE} // UTF-16 with a byte order mark
		for _, c := range u {
			data = binary.LittleEndian.AppendUint16(data, c)
		}
		body = append(body, f[0]...)
		body = binary.BigEndian.AppendUint32(body, uint32(len(data)))
		body = append(body, 0, 0)
		body = append(body, data...)
	}
	size := len(body)
	head := []byte{'I', 'D', '3', 3, 0, 0,
		byte(size >> 21 & 0x7f), byte(size >> 14 & 0x7f), byte(size >> 7 & 0x7f), byte(size & 0x7f)}
	return append(head, body...)
}
