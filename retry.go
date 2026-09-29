package easypixel

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"time"
)

// RetryPolicy decides whether a failed request is sent again and how long to
// wait first. The zero value performs no retries.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts including the first one.
	// Values below 2 disable retries.
	MaxAttempts int
	// BaseDelay is the first backoff step; it doubles on every further attempt.
	BaseDelay time.Duration
	// MaxDelay caps a single wait. A Retry-After longer than this is not waited
	// out: the 429 is returned instead, so a caller never blocks longer than it
	// asked for.
	MaxDelay time.Duration
	// RespectRetryAfter waits exactly as long as a 429 response asks, instead of
	// using the backoff.
	RespectRetryAfter bool
	// Jitter spreads the backoff by ±25% so that several senders that were
	// throttled together do not come back in lockstep.
	Jitter bool
}

// DefaultRetryPolicy is a sensible starting point: three attempts, backoff from
// half a second, never waiting longer than a minute at a time.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:       3,
		BaseDelay:         500 * time.Millisecond,
		MaxDelay:          60 * time.Second,
		RespectRetryAfter: true,
		Jitter:            true,
	}
}

func (p RetryPolicy) attempts() int {
	if p.MaxAttempts < 1 {
		return 1
	}
	return p.MaxAttempts
}

// retryable reports whether sending the same request again has a chance of a
// different outcome. A 4xx other than 429 does not: the request itself is wrong.
func (p RetryPolicy) retryable(err error) bool {
	var transportErr *TransportError
	if errors.As(err, &transportErr) {
		return true
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}

	return false
}

// delay returns how long to wait before attempt number attempt+1. The second
// result is false when the wait would exceed MaxDelay and the caller should get
// the error instead.
func (p RetryPolicy) delay(attempt int, err error) (time.Duration, bool) {
	maxDelay := p.MaxDelay
	if maxDelay <= 0 {
		maxDelay = DefaultRetryPolicy().MaxDelay
	}

	if p.RespectRetryAfter {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			if wait, ok := apiErr.RetryAfter(); ok {
				if wait > maxDelay {
					return 0, false
				}
				// Пауза из Retry-After не размывается джиттером: сервер назвал
				// момент, раньше которого запрос снова получит 429.
				return wait, true
			}
		}
	}

	base := p.BaseDelay
	if base <= 0 {
		base = DefaultRetryPolicy().BaseDelay
	}

	wait := base
	for i := 1; i < attempt; i++ {
		if wait >= maxDelay {
			break
		}
		wait *= 2
	}
	if wait > maxDelay {
		wait = maxDelay
	}

	if p.Jitter {
		wait = applyJitter(wait)
	}

	return wait, true
}

func applyJitter(d time.Duration) time.Duration {
	spread := int64(d) / 2
	if spread <= 0 {
		return d
	}
	return d - time.Duration(spread/2) + time.Duration(rand.Int63n(spread+1))
}

// sleep waits out d, or returns early with the context's error.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
