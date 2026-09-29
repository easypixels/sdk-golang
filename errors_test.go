package easypixel

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestStatusMapsToSentinel(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrAuthentication},
		{http.StatusForbidden, ErrAuthentication},
		{http.StatusNotFound, ErrNotFound},
		{http.StatusUnprocessableEntity, ErrValidation},
		{http.StatusTooManyRequests, ErrRateLimit},
	} {
		err := newAPIError(tc.status, nil, http.Header{})
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d does not match %v", tc.status, tc.want)
		}
	}
}

func TestUnrelatedStatusMatchesNoSentinel(t *testing.T) {
	err := newAPIError(http.StatusInternalServerError, nil, http.Header{})

	for _, sentinel := range []error{ErrAuthentication, ErrNotFound, ErrValidation, ErrRateLimit} {
		if errors.Is(err, sentinel) {
			t.Errorf("status 500 unexpectedly matches %v", sentinel)
		}
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("errors.As lost the status: %v", err)
	}
}

func TestValidationErrors(t *testing.T) {
	body := []byte(`{"message":"The given data was invalid.","errors":{"scene_id":["The scene id field is required."]}}`)
	err := newAPIError(http.StatusUnprocessableEntity, body, http.Header{})

	if err.Message != "The given data was invalid." {
		t.Errorf("Message = %q", err.Message)
	}
	messages := err.ValidationErrors["scene_id"]
	if len(messages) != 1 || messages[0] != "The scene id field is required." {
		t.Errorf("ValidationErrors = %v", err.ValidationErrors)
	}
}

func TestValidationErrorsAbsent(t *testing.T) {
	err := newAPIError(http.StatusUnprocessableEntity, []byte(`{"message":"No scenario assigned to this matrix."}`), http.Header{})

	if err.ValidationErrors != nil {
		t.Errorf("ValidationErrors = %v, want nil", err.ValidationErrors)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "42")

	wait, ok := newAPIError(http.StatusTooManyRequests, nil, header).RetryAfter()
	if !ok {
		t.Fatal("RetryAfter() reported no header")
	}
	if wait != 42*time.Second {
		t.Errorf("RetryAfter() = %v, want 42s", wait)
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", time.Now().Add(90*time.Second).UTC().Format(http.TimeFormat))

	wait, ok := newAPIError(http.StatusTooManyRequests, nil, header).RetryAfter()
	if !ok {
		t.Fatal("RetryAfter() reported no header")
	}
	// Дата отдаётся с точностью до секунды, поэтому сравниваем с допуском.
	if wait < 88*time.Second || wait > 90*time.Second {
		t.Errorf("RetryAfter() = %v, want about 90s", wait)
	}
}

func TestRetryAfterInThePastIsZero(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))

	wait, ok := newAPIError(http.StatusTooManyRequests, nil, header).RetryAfter()
	if !ok {
		t.Fatal("RetryAfter() reported no header")
	}
	if wait != 0 {
		t.Errorf("RetryAfter() = %v, want 0", wait)
	}
}

func TestRetryAfterAbsentOrUnreadable(t *testing.T) {
	for _, value := range []string{"", "   ", "soon"} {
		header := http.Header{}
		if value != "" {
			header.Set("Retry-After", value)
		}

		if wait, ok := newAPIError(http.StatusTooManyRequests, nil, header).RetryAfter(); ok {
			t.Errorf("Retry-After %q gave %v, want no delay", value, wait)
		}
	}
}

func TestTransportErrorUnwraps(t *testing.T) {
	cause := errors.New("no such host")
	err := &TransportError{Err: cause}

	if !errors.Is(err, cause) {
		t.Error("errors.Is did not reach the cause")
	}
	if err.Error() != "easypixel: request failed: no such host" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestAPIErrorMessage(t *testing.T) {
	err := newAPIError(http.StatusNotFound, []byte(`{"message":"Invalid API key."}`), http.Header{})

	if err.Error() != "easypixel: HTTP 404: Invalid API key." {
		t.Errorf("Error() = %q", err.Error())
	}
}
