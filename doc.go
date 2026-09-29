// Package easypixel sends scenes to EasyPixel LED matrices.
//
// The SDK wraps a single endpoint, POST /api/webhook/{api_key}, which shows a
// scene from the scenario assigned to one matrix and optionally fills in that
// scenario's variables. The matrix API key is the only credential; it travels
// in the URL, so treat it like a password.
//
// A minimal send:
//
//	w := easypixel.New("matrix-api-key")
//	res, err := w.Send(context.Background(), 42, map[string]string{"count": "15"})
//
// The endpoint answers 202 Accepted: the scene is rendered and pushed to the
// hardware asynchronously, so a successful call means the request was accepted,
// not that the panel has changed.
//
// Failures come back as typed errors. Use errors.Is with [ErrNotFound],
// [ErrValidation], [ErrRateLimit] or [ErrAuthentication] to discriminate, and
// errors.As with [*APIError] to reach the status code, the validation details
// or the Retry-After delay. Network failures are reported as [*TransportError].
//
// Retries are off by default, matching the PHP SDK. Pass [WithRetry] to have
// Send wait out a 429 and try again; the wait is interruptible through the
// context, so a goroutine per matrix stays cancellable.
package easypixel
