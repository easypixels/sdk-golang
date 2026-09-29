package easypixel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client is the HTTP transport underneath Webhook. It speaks JSON, turns
// non-2xx responses into *APIError and applies the retry policy.
type Client struct {
	baseURL    string
	httpClient *http.Client
	header     http.Header
	retry      RetryPolicy
}

// NewClient builds a transport for baseURL. An empty baseURL falls back to the
// one from the options, and ultimately to DefaultBaseURL.
func NewClient(baseURL string, opts ...Option) *Client {
	cfg := newConfig(opts...)
	if baseURL != "" {
		cfg.baseURL = baseURL
	}
	return newClient(cfg)
}

func newClient(cfg *config) *Client {
	return &Client{
		baseURL:    strings.TrimRight(cfg.baseURL, "/"),
		httpClient: cfg.buildHTTPClient(),
		header:     cfg.header.Clone(),
		retry:      cfg.retry,
	}
}

// BaseURL is the API root every path is appended to, without a trailing slash.
func (c *Client) BaseURL() string { return c.baseURL }

// HTTPClient is the underlying client, exposed for inspection.
func (c *Client) HTTPClient() *http.Client { return c.httpClient }

// Get sends a GET request and returns the raw response body.
func (c *Client) Get(ctx context.Context, path string) (json.RawMessage, error) {
	return c.Do(ctx, http.MethodGet, path, nil)
}

// Post sends body as JSON and returns the raw response body.
func (c *Client) Post(ctx context.Context, path string, body any) (json.RawMessage, error) {
	return c.Do(ctx, http.MethodPost, path, body)
}

// Do sends one request, retrying it according to the policy. A nil body sends
// no payload at all.
func (c *Client) Do(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("easypixel: encode request body: %w", err)
		}
		payload = encoded
	}

	url := c.baseURL + path
	maxAttempts := c.retry.attempts()

	for attempt := 1; ; attempt++ {
		raw, err := c.attempt(ctx, method, url, payload)
		if err == nil {
			return raw, nil
		}

		if attempt >= maxAttempts || !c.retry.retryable(err) {
			return nil, err
		}

		wait, ok := c.retry.delay(attempt, err)
		if !ok {
			return nil, err
		}
		if waitErr := sleep(ctx, wait); waitErr != nil {
			return nil, waitErr
		}
	}
}

func (c *Client) attempt(ctx context.Context, method, url string, payload []byte) (json.RawMessage, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("easypixel: build request: %w", err)
	}

	req.Header = c.header.Clone()
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(payload))
		// Каждая попытка читает тело с нуля, поэтому payload держим в памяти
		// целиком, а не стримим из вызывающего кода.
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(payload)), nil
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, &TransportError{Err: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, &TransportError{Err: err}
	}

	if resp.StatusCode >= 400 {
		return nil, newAPIError(resp.StatusCode, respBody, resp.Header)
	}

	return json.RawMessage(respBody), nil
}
