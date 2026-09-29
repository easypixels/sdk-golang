package easypixel

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// fastPolicy keeps the waits in the microsecond range so the suite stays quick.
func fastPolicy(attempts int) RetryPolicy {
	return RetryPolicy{
		MaxAttempts:       attempts,
		BaseDelay:         time.Millisecond,
		MaxDelay:          50 * time.Millisecond,
		RespectRetryAfter: true,
	}
}

// throttleThen answers 429 for the first n requests and 202 afterwards.
func throttleThen(n int32, retryAfter string) (http.HandlerFunc, *int32) {
	var calls int32
	handler := func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= n {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"Too Many Attempts."}`))
			return
		}
		acceptedHandler(42, 7, nil)(w, r)
	}
	return handler, &calls
}

func TestNoRetryByDefault(t *testing.T) {
	handler, calls := throttleThen(10, "1")
	server, _ := newTestServer(t, handler)

	_, err := New("key", WithBaseURL(server.URL)).Send(context.Background(), 42, nil)
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("err = %v, want ErrRateLimit", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("server saw %d requests, want 1", got)
	}
}

func TestRetryWaitsOutRateLimit(t *testing.T) {
	handler, calls := throttleThen(1, "0")
	server, rec := newTestServer(t, handler)

	result, err := New("key", WithBaseURL(server.URL), WithRetry(fastPolicy(3))).
		Send(context.Background(), 42, map[string]string{"count": "15"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.MatrixID != 7 {
		t.Errorf("MatrixID = %d", result.MatrixID)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("server saw %d requests, want 2", got)
	}

	// Вторая попытка обязана нести то же тело, а не пустое.
	requests := rec.all()
	if len(requests) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(requests))
	}
	want := `{"scene_id":42,"variables":{"count":"15"}}`
	for i, req := range requests {
		if string(req.Body) != want {
			t.Errorf("request %d body = %s, want %s", i+1, req.Body, want)
		}
	}
}

func TestRetryOnServerError(t *testing.T) {
	var calls int32
	server, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		acceptedHandler(42, 7, nil)(w, r)
	})

	if _, err := New("key", WithBaseURL(server.URL), WithRetry(fastPolicy(3))).
		Send(context.Background(), 42, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("server saw %d requests, want 2", got)
	}
}

func TestValidationIsNotRetried(t *testing.T) {
	var calls int32
	server, _ := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		errorHandler(http.StatusUnprocessableEntity, "Scene not found in assigned scenario.")(w, nil)
	})

	_, err := New("key", WithBaseURL(server.URL), WithRetry(fastPolicy(5))).
		Send(context.Background(), 999, nil)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("server saw %d requests, want 1 — a 422 must not be retried", got)
	}
}

func TestRetriesExhaustedKeepsLastError(t *testing.T) {
	handler, calls := throttleThen(10, "0")
	server, _ := newTestServer(t, handler)

	_, err := New("key", WithBaseURL(server.URL), WithRetry(fastPolicy(3))).
		Send(context.Background(), 42, nil)
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("err = %v, want ErrRateLimit", err)
	}
	if got := atomic.LoadInt32(calls); got != 3 {
		t.Errorf("server saw %d requests, want 3", got)
	}
}

func TestRetryAfterBeyondMaxDelayReturnsError(t *testing.T) {
	handler, calls := throttleThen(10, "600")
	server, _ := newTestServer(t, handler)

	policy := fastPolicy(5)
	policy.MaxDelay = time.Second

	start := time.Now()
	_, err := New("key", WithBaseURL(server.URL), WithRetry(policy)).
		Send(context.Background(), 42, nil)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("err = %v, want ErrRateLimit", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("server saw %d requests, want 1", got)
	}
	if elapsed > time.Second {
		t.Errorf("Send blocked for %v; a Retry-After beyond MaxDelay must return at once", elapsed)
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if wait, ok := apiErr.RetryAfter(); !ok || wait != 600*time.Second {
			t.Errorf("RetryAfter() = %v, %v; want the server's 600s left for the caller", wait, ok)
		}
	}
}

func TestContextCancelledDuringBackoff(t *testing.T) {
	handler, calls := throttleThen(10, "5")
	server, _ := newTestServer(t, handler)

	policy := fastPolicy(5)
	policy.MaxDelay = time.Minute

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := New("key", WithBaseURL(server.URL), WithRetry(policy)).Send(ctx, 42, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("server saw %d requests, want 1", got)
	}
}

func TestRetryOnTransportError(t *testing.T) {
	var calls int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, errors.New("connection reset by peer")
	})}

	_, err := New("key",
		WithBaseURL("https://example.test"),
		WithHTTPClient(client),
		WithRetry(fastPolicy(3)),
	).Send(context.Background(), 42, nil)

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v, want *TransportError", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("transport saw %d attempts, want 3", got)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 10, BaseDelay: time.Second, MaxDelay: 4 * time.Second}
	err := newAPIError(http.StatusInternalServerError, nil, http.Header{})

	for _, tc := range []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 4 * time.Second},
		{9, 4 * time.Second},
	} {
		got, ok := policy.delay(tc.attempt, err)
		if !ok {
			t.Fatalf("attempt %d: delay refused", tc.attempt)
		}
		if got != tc.want {
			t.Errorf("attempt %d: delay = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestJitterStaysWithinQuarter(t *testing.T) {
	policy := RetryPolicy{MaxAttempts: 2, BaseDelay: time.Second, MaxDelay: time.Minute, Jitter: true}
	err := newAPIError(http.StatusInternalServerError, nil, http.Header{})

	for i := 0; i < 100; i++ {
		got, ok := policy.delay(1, err)
		if !ok {
			t.Fatal("delay refused")
		}
		if got < 750*time.Millisecond || got > 1250*time.Millisecond {
			t.Fatalf("delay = %v, want within ±25%% of 1s", got)
		}
	}
}

func TestZeroPolicyMeansSingleAttempt(t *testing.T) {
	if got := (RetryPolicy{}).attempts(); got != 1 {
		t.Errorf("attempts() = %d, want 1", got)
	}
	if got := (RetryPolicy{MaxAttempts: -3}).attempts(); got != 1 {
		t.Errorf("attempts() = %d, want 1", got)
	}
}
