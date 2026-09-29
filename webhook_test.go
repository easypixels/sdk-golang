package easypixel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// recordedRequest is one request as the test server saw it.
type recordedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// recorder collects requests from the server's goroutines for the test's one.
type recorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (r *recorder) add(req recordedRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
}

func (r *recorder) all() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.requests...)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// newTestServer answers every request with handler and records what arrived.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *recorder) {
	t.Helper()

	rec := &recorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.add(recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   body,
		})
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	return server, rec
}

func acceptedHandler(sceneID, matrixID int, updated []string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if updated == nil {
			updated = []string{}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":           "Webhook accepted.",
			"scene_id":          sceneID,
			"matrix_id":         matrixID,
			"variables_updated": updated,
		})
	}
}

func errorHandler(status int, message string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": message})
	}
}

func TestSendPutsAPIKeyInPath(t *testing.T) {
	server, rec := newTestServer(t, acceptedHandler(42, 7, nil))

	_, err := New("secret-key", WithBaseURL(server.URL)).Send(context.Background(), 42, nil)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	requests := rec.all()
	if len(requests) != 1 {
		t.Fatalf("got %d requests, want 1", len(requests))
	}
	got := requests[0]
	if got.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", got.Method)
	}
	if want := "/api/webhook/secret-key"; got.Path != want {
		t.Errorf("path = %q, want %q", got.Path, want)
	}
	if ct := got.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestSendOmitsVariablesWhenEmpty(t *testing.T) {
	for _, tc := range []struct {
		name      string
		variables map[string]string
		wantBody  string
	}{
		{"nil", nil, `{"scene_id":42}`},
		{"empty", map[string]string{}, `{"scene_id":42}`},
		{"filled", map[string]string{"count": "15"}, `{"scene_id":42,"variables":{"count":"15"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, rec := newTestServer(t, acceptedHandler(42, 7, nil))

			_, err := New("key", WithBaseURL(server.URL)).Send(context.Background(), 42, tc.variables)
			if err != nil {
				t.Fatalf("Send: %v", err)
			}

			if got := string(rec.all()[0].Body); got != tc.wantBody {
				t.Errorf("body = %s, want %s", got, tc.wantBody)
			}
		})
	}
}

func TestSendParsesResult(t *testing.T) {
	server, _ := newTestServer(t, acceptedHandler(42, 7, []string{"count", "label"}))

	result, err := New("key", WithBaseURL(server.URL)).
		Send(context.Background(), 42, map[string]string{"count": "15", "label": "Free"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if result.Message != "Webhook accepted." {
		t.Errorf("Message = %q", result.Message)
	}
	if result.SceneID != 42 || result.MatrixID != 7 {
		t.Errorf("SceneID = %d, MatrixID = %d, want 42 and 7", result.SceneID, result.MatrixID)
	}
	if len(result.VariablesUpdated) != 2 {
		t.Errorf("VariablesUpdated = %v, want two names", result.VariablesUpdated)
	}
}

func TestSendInvalidAPIKey(t *testing.T) {
	server, _ := newTestServer(t, errorHandler(http.StatusNotFound, "Invalid API key."))

	_, err := New("bad-key", WithBaseURL(server.URL)).Send(context.Background(), 42, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Message != "Invalid API key." {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

func TestSendSceneNotInScenario(t *testing.T) {
	server, _ := newTestServer(t, errorHandler(http.StatusUnprocessableEntity, "Scene not found in assigned scenario."))

	_, err := New("key", WithBaseURL(server.URL)).Send(context.Background(), 999, nil)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestSendHonoursCancelledContext(t *testing.T) {
	server, rec := newTestServer(t, acceptedHandler(42, 7, nil))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New("key", WithBaseURL(server.URL)).Send(ctx, 42, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := rec.count(); got != 0 {
		t.Errorf("server saw %d requests, want none", got)
	}
}

func TestAccessors(t *testing.T) {
	w := New("key", WithBaseURL("https://example.test/"))

	if w.APIKey() != "key" {
		t.Errorf("APIKey() = %q", w.APIKey())
	}
	if got := w.Client().BaseURL(); got != "https://example.test" {
		t.Errorf("BaseURL() = %q, want the trailing slash trimmed", got)
	}
	if w.Client().HTTPClient() == nil {
		t.Error("HTTPClient() = nil")
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if got := New("key").Client().BaseURL(); got != DefaultBaseURL {
		t.Errorf("BaseURL() = %q, want %q", got, DefaultBaseURL)
	}
}
