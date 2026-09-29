package easypixel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// roundTripFunc turns a function into an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDefaultHeaders(t *testing.T) {
	server, rec := newTestServer(t, acceptedHandler(1, 1, nil))

	if _, err := New("key", WithBaseURL(server.URL)).Send(context.Background(), 1, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}

	header := rec.all()[0].Header
	if got := header.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q", got)
	}
	if got := header.Get("User-Agent"); !strings.HasPrefix(got, "easypixel-go/") {
		t.Errorf("User-Agent = %q, want the SDK default", got)
	}
}

func TestWithHeaderAndUserAgentOverride(t *testing.T) {
	server, rec := newTestServer(t, acceptedHandler(1, 1, nil))

	client := New("key",
		WithBaseURL(server.URL),
		WithUserAgent("parking-bridge/2.1"),
		WithHeader("X-Request-Id", "abc123"),
	)
	if _, err := client.Send(context.Background(), 1, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}

	header := rec.all()[0].Header
	if got := header.Get("User-Agent"); got != "parking-bridge/2.1" {
		t.Errorf("User-Agent = %q", got)
	}
	if got := header.Get("X-Request-Id"); got != "abc123" {
		t.Errorf("X-Request-Id = %q", got)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	server, rec := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			acceptedHandler(1, 1, nil)(w, r)
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	})

	// Ключ матрицы лежит в пути, поэтому SDK не идёт за Location и на 302
	// не получает ничего разбираемого.
	_, err := New("key", WithBaseURL(server.URL)).Send(context.Background(), 1, nil)
	if err == nil {
		t.Error("Send succeeded; the redirect must not be followed to the 202 behind it")
	}
	if got := rec.count(); got != 1 {
		t.Errorf("server saw %d requests, want 1", got)
	}
	if got := rec.all()[0].Path; got == "/elsewhere" {
		t.Errorf("the SDK followed the redirect to %q", got)
	}
}

func TestNonJSONErrorBody(t *testing.T) {
	server, _ := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	})

	_, err := New("key", WithBaseURL(server.URL)).Send(context.Background(), 1, nil)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d", apiErr.StatusCode)
	}
	if apiErr.Message != "HTTP Error 502" {
		t.Errorf("Message = %q, want the generic fallback", apiErr.Message)
	}
	if !strings.Contains(string(apiErr.Body), "Bad Gateway") {
		t.Errorf("Body = %q, want the raw response kept", apiErr.Body)
	}
}

func TestTransportFailure(t *testing.T) {
	boom := errors.New("dial tcp: connection refused")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, boom
	})}

	_, err := New("key", WithBaseURL("https://example.test"), WithHTTPClient(client)).
		Send(context.Background(), 1, nil)

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v, want *TransportError", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("Error() = %q, want the underlying cause", err.Error())
	}
}

func TestTimeout(t *testing.T) {
	// Обработчик держит ответ, пока тест не отпустит его сам: клиент уходит по
	// таймауту молча, контекст запроса на сервере об этом не узнаёт, и
	// Server.Close ждал бы обработчик вечно.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	// Cleanup исполняется в обратном порядке: сначала отпускаем обработчик,
	// потом закрываем сервер.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	_, err := New("key", WithBaseURL(server.URL), WithTimeout(50*time.Millisecond)).
		Send(context.Background(), 1, nil)
	if err == nil {
		t.Fatal("Send: want a timeout error")
	}

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v, want *TransportError", err)
	}
}

func TestNewClientBaseURLArgumentWins(t *testing.T) {
	client := NewClient("https://explicit.test", WithBaseURL("https://option.test"))
	if got := client.BaseURL(); got != "https://explicit.test" {
		t.Errorf("BaseURL() = %q", got)
	}

	client = NewClient("", WithBaseURL("https://option.test"))
	if got := client.BaseURL(); got != "https://option.test" {
		t.Errorf("BaseURL() = %q, want the option to fill an empty argument", got)
	}
}

func TestGet(t *testing.T) {
	server, rec := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	raw, err := NewClient(server.URL).Get(context.Background(), "/ping")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Errorf("body = %s", raw)
	}

	got := rec.all()[0]
	if got.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", got.Method)
	}
	if len(got.Body) != 0 {
		t.Errorf("body = %q, want none", got.Body)
	}
}
