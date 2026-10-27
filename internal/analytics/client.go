package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const ingestRequestTimeout = 2 * time.Minute

var ErrUnknownInstall = errors.New("attn-ingest does not know this install's token")

type RefusalError struct {
	Status  int
	Refusal Refusal
	Body    string
}

func (e *RefusalError) Error() string {
	if e.Refusal.Limit == "" {
		return fmt.Sprintf("attn-ingest refused the batch with %d: %s", e.Status, e.Body)
	}
	message := fmt.Sprintf("attn-ingest refused the batch: %s is %d, and this batch would make it %d", e.Refusal.Limit, e.Refusal.Value, e.Refusal.Observed)
	if e.Refusal.RetryAfterS > 0 {
		message += fmt.Sprintf("; retrying in %s", time.Duration(e.Refusal.RetryAfterS)*time.Second)
	}
	return message
}

type Ingest struct {
	URL  string
	HTTP *http.Client
}

func NewIngest(url string) Ingest {
	return Ingest{URL: url, HTTP: &http.Client{Timeout: ingestRequestTimeout, Transport: &http.Transport{DisableKeepAlives: true, Proxy: http.ProxyFromEnvironment}}}
}

func (c Ingest) Register(ctx context.Context) (Registration, error) {
	var registration Registration
	status, body, err := c.do(ctx, http.MethodPost, "/v1/installs", "", nil)
	if err != nil {
		return registration, err
	}
	if status != http.StatusOK {
		return registration, refusal(status, body)
	}
	if err := json.Unmarshal(body, &registration); err != nil || registration.Token == "" {
		return Registration{}, fmt.Errorf("attn-ingest answered the registration with %q", body)
	}
	return registration, nil
}

func (c Ingest) Send(ctx context.Context, token string, batch Batch) (Accepted, error) {
	var payload bytes.Buffer
	if err := WriteBatch(&payload, batch); err != nil {
		return Accepted{}, err
	}
	status, body, err := c.do(ctx, http.MethodPost, "/v1/events", token, &payload)
	if err != nil {
		return Accepted{}, err
	}
	switch status {
	case http.StatusOK:
		var accepted Accepted
		if err := json.Unmarshal(body, &accepted); err != nil {
			return Accepted{}, fmt.Errorf("attn-ingest answered the batch with %q", body)
		}
		return accepted, nil
	case http.StatusUnauthorized:
		return Accepted{}, ErrUnknownInstall
	default:
		return Accepted{}, refusal(status, body)
	}
}

func (c Ingest) Delete(ctx context.Context, token string) error {
	status, body, err := c.do(ctx, http.MethodDelete, "/v1/installs/me", token, nil)
	switch {
	case err != nil:
		return err
	case status == http.StatusNoContent:
		return nil
	case status == http.StatusUnauthorized:
		return ErrUnknownInstall
	default:
		return fmt.Errorf("attn-ingest answered the delete with %d: %s", status, strings.TrimSpace(string(body)))
	}
}

func (c Ingest) do(ctx context.Context, method, path, token string, payload io.Reader) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, payload)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("could not reach attn-ingest at %s: %w", c.URL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, body, err
}

func refusal(status int, body []byte) error {
	refused := &RefusalError{Status: status, Body: strings.TrimSpace(string(body))}
	_ = json.Unmarshal(body, &refused.Refusal)
	var reason struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &reason) == nil && reason.Error != "" {
		refused.Body = reason.Error
	}
	return refused
}
