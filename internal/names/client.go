package names

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultService is where installs register unless told otherwise.
const DefaultService = "https://names.soundstorm.dev"

// Client talks to the name service from an install.
type Client struct {
	Base string // e.g. DefaultService
	HTTP *http.Client
}

// Register asks for a new name.
func (c *Client) Register(ctx context.Context) (Registration, error) {
	var reg Registration
	err := c.do(ctx, http.MethodPost, "/v1/register", "", nil, &reg)
	if err == nil && (!validID(reg.ID) || reg.Token == "" || reg.Name == "") {
		err = fmt.Errorf("name service returned an incomplete registration")
	}
	return reg, err
}

// SetAddress points the registration's name at ip.
func (c *Client) SetAddress(ctx context.Context, reg Registration, ip string) error {
	return c.do(ctx, http.MethodPut, "/v1/address", reg.Credential(), map[string]string{"ip": ip}, nil)
}

// SetChallenge publishes an ACME DNS-01 value, returning once the service
// reports it visible - which can take minutes.
func (c *Client) SetChallenge(ctx context.Context, reg Registration, value string) error {
	return c.do(ctx, http.MethodPut, "/v1/challenge", reg.Credential(), map[string]string{"value": value}, nil)
}

// ClearChallenge takes a challenge down.
func (c *Client) ClearChallenge(ctx context.Context, reg Registration) error {
	return c.do(ctx, http.MethodDelete, "/v1/challenge", reg.Credential(), nil, nil)
}

// StatusError is a refusal from the service, with its reason.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("name service: %s (status %d)", e.Message, e.Status)
}

func (c *Client) do(ctx context.Context, method, path, credential string, body, into any) error {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		req.Header.Set("Authorization", credential)
	}
	client := c.HTTP
	if client == nil {
		// Long enough for a challenge to become visible, which the service
		// waits for before answering.
		client = &http.Client{Timeout: 6 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("name service: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = http.StatusText(resp.StatusCode)
		}
		return &StatusError{Status: resp.StatusCode, Message: e.Error}
	}
	if into != nil {
		if err := json.Unmarshal(data, into); err != nil {
			return fmt.Errorf("name service: unreadable reply: %w", err)
		}
	}
	return nil
}
