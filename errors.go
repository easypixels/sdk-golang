package easypixel

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors for errors.Is. They stand in for the exception subclasses of
// the PHP SDK: an *APIError reports itself as the sentinel matching its status.
var (
	// ErrAuthentication reports HTTP 401 and 403.
	ErrAuthentication = errors.New("easypixel: authentication failed")
	// ErrNotFound reports HTTP 404, which the webhook endpoint returns for an
	// unknown matrix API key.
	ErrNotFound = errors.New("easypixel: not found")
	// ErrValidation reports HTTP 422: no scenario is assigned to the matrix, or
	// the scene does not belong to the assigned scenario.
	ErrValidation = errors.New("easypixel: validation failed")
	// ErrRateLimit reports HTTP 429. See (*APIError).RetryAfter.
	ErrRateLimit = errors.New("easypixel: rate limited")
)

// APIError is any response with a status of 400 or above.
type APIError struct {
	// StatusCode is the HTTP status of the response.
	StatusCode int
	// Message is the "message" field of the response body, or a generic
	// "HTTP Error <status>" when the body carries none.
	Message string
	// ValidationErrors is the "errors" field of the response body, present on
	// some 422 responses.
	ValidationErrors map[string][]string
	// Body is the raw response body, kept for diagnostics.
	Body []byte

	retryAfter    time.Duration
	hasRetryAfter bool
}

func (e *APIError) Error() string {
	return fmt.Sprintf("easypixel: HTTP %d: %s", e.StatusCode, e.Message)
}

// Is maps the status code onto the package's sentinel errors.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrAuthentication:
		return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	case ErrValidation:
		return e.StatusCode == http.StatusUnprocessableEntity
	case ErrRateLimit:
		return e.StatusCode == http.StatusTooManyRequests
	default:
		return false
	}
}

// RetryAfter returns the delay from the Retry-After header. The second result
// is false when the server sent no such header or its value was unreadable; a
// date already in the past yields a zero delay and true.
func (e *APIError) RetryAfter() (time.Duration, bool) {
	return e.retryAfter, e.hasRetryAfter
}

// TransportError wraps a failure that happened before a response was read:
// DNS, connection, TLS, timeout.
type TransportError struct {
	Err error
}

func (e *TransportError) Error() string {
	return "easypixel: request failed: " + e.Err.Error()
}

func (e *TransportError) Unwrap() error { return e.Err }

func newAPIError(statusCode int, body []byte, header http.Header) *APIError {
	err := &APIError{
		StatusCode: statusCode,
		Message:    fmt.Sprintf("HTTP Error %d", statusCode),
		Body:       body,
	}

	// Тело может не быть JSON — например, страница прокси. Тогда остаётся
	// обобщённое сообщение со статусом, как в PHP SDK.
	var payload struct {
		Message string              `json:"message"`
		Errors  map[string][]string `json:"errors"`
	}
	if jsonErr := json.Unmarshal(body, &payload); jsonErr == nil {
		if payload.Message != "" {
			err.Message = payload.Message
		}
		err.ValidationErrors = payload.Errors
	}

	err.retryAfter, err.hasRetryAfter = parseRetryAfter(header)

	return err
}

// parseRetryAfter reads both forms allowed by RFC 7231: a number of seconds and
// an HTTP date. Delays in the past are clamped to zero.
func parseRetryAfter(header http.Header) (time.Duration, bool) {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0, false
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			seconds = 0
		}
		return time.Duration(seconds) * time.Second, true
	}

	if deadline, err := http.ParseTime(value); err == nil {
		delay := time.Until(deadline)
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}

	return 0, false
}
