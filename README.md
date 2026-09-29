# EasyPixel Go SDK

Go SDK for sending scenes to [EasyPixel](https://easypixel.ru) LED matrices.

[![Tests](https://github.com/easypixels/sdk-golang/actions/workflows/tests.yml/badge.svg)](https://github.com/easypixels/sdk-golang/actions/workflows/tests.yml)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-00ADD8.svg)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

Writing PHP? [`easypixels/sdk-php`](https://github.com/easypixels/sdk-php) is the same SDK for
PHP 7.2+, and [`easypixels/sdk-php-laravel`](https://github.com/easypixels/sdk-php-laravel)
wraps it for Laravel.

## Requirements

- Go 1.22 or higher
- No dependencies outside the standard library

## Installation

```bash
go get github.com/easypixels/sdk-golang
```

The import path ends in `sdk-golang`; the package is named `easypixel`.

## Quick Start

```go
package main

import (
	"context"
	"log"

	easypixel "github.com/easypixels/sdk-golang"
)

func main() {
	w := easypixel.New("matrix-api-key")

	// Show scene 42 on that matrix, filling in two of its variables
	res, err := w.Send(context.Background(), 42, map[string]string{
		"count":  "15",
		"status": "open",
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("accepted on matrix %d, updated %v", res.MatrixID, res.VariablesUpdated)
}
```

---

## Authentication

The SDK uses one credential: the **matrix API key**, a 32-character hex string you copy from
the EasyPixel web app in the settings of that matrix. It is scoped to a single matrix and can
do exactly one thing — show a scene from the scenario assigned to it.

The key travels in the URL (`POST /api/webhook/{api_key}`), so treat it like a password: keep
it out of the repository and out of client-side code. If it leaks, issue a new one with
`POST /api/matrices/{matrix}/regenerate-api-key`; the old key stops working immediately.

---

## Rate Limits

The webhook endpoint is throttled twice, and both limits apply at once:

| Limit | Scope |
|---|---|
| 120 requests / minute | per client IP address |
| 60 requests / minute | per matrix API key |

Driving several matrices from one host hits the IP limit first: 60 requests per key sounds
generous until three matrices share one server's address. Over either limit the API answers
`429`, which the SDK reports as an error matching `easypixel.ErrRateLimit`. The
`Retry-After` header is on `(*APIError).RetryAfter()`; see [Retries](#retries) to have the
SDK wait it out for you.

---

## Sending scenes

```go
w := easypixel.New("matrix-api-key")

res, err := w.Send(ctx, 42, nil)

res, err = w.Send(ctx, 42, map[string]string{
	"count": "100",
	"label": "Free spots",
})

// res is *easypixel.SendResult:
//   Message          "Webhook accepted."
//   SceneID          42
//   MatrixID         7
//   VariablesUpdated []string{"count", "label"}
```

The scene must belong to the scenario currently assigned to that matrix, otherwise the API
answers `422`.

**Important:** the webhook returns `202 Accepted` immediately. The scene is rendered and sent
to the hardware asynchronously. There is no delivery confirmation.

`VariablesUpdated` lists only the variables the matrix's scenario actually defines — unknown
names are ignored without an error, so compare that list against what you sent if a value
never shows up on the panel.

Variables are `map[string]string`, so format numbers yourself with `strconv` before sending.

---

## Error Handling

Use `errors.Is` to tell the cases apart and `errors.As` to reach the details:

```go
res, err := w.Send(ctx, 42, map[string]string{"count": "5"})

switch {
case err == nil:
	// accepted

case errors.Is(err, easypixel.ErrNotFound):
	// 404: invalid matrix API key
	log.Printf("invalid matrix API key: %v", err)

case errors.Is(err, easypixel.ErrValidation):
	// 422: no scenario assigned, or scene not found in the scenario
	var apiErr *easypixel.APIError
	errors.As(err, &apiErr)
	log.Printf("API error: %s %v", apiErr.Message, apiErr.ValidationErrors)

case errors.Is(err, easypixel.ErrRateLimit):
	// 429: throttled — see "Rate Limits" above
	var apiErr *easypixel.APIError
	errors.As(err, &apiErr)
	if wait, ok := apiErr.RetryAfter(); ok {
		log.Printf("throttled, retry in %s", wait)
	}

case errors.Is(err, easypixel.ErrAuthentication):
	// 401 / 403
	log.Printf("authentication failed: %v", err)

default:
	var apiErr *easypixel.APIError
	var transportErr *easypixel.TransportError
	switch {
	case errors.As(err, &apiErr):
		// other HTTP errors (500, etc.)
		log.Printf("HTTP %d: %s", apiErr.StatusCode, apiErr.Message)
	case errors.As(err, &transportErr):
		// DNS, connection, TLS, timeout
		log.Printf("request failed: %v", err)
	default:
		// context cancelled, malformed response
		log.Printf("%v", err)
	}
}
```

Error types:

| Type | Covers |
|---|---|
| `*APIError` | any response with status 400 or above |
| `*TransportError` | DNS, connection, TLS and timeout failures |

Sentinels for `errors.Is`, all satisfied by `*APIError`:

| Sentinel | Status |
|---|---|
| `ErrAuthentication` | 401, 403 |
| `ErrNotFound` | 404 |
| `ErrValidation` | 422 |
| `ErrRateLimit` | 429 |

---

## Retries

A request is sent exactly once by default, and the first `429` comes straight back to you.
`WithRetry` turns on waiting and retrying:

```go
w := easypixel.New("matrix-api-key", easypixel.WithRetry(easypixel.DefaultRetryPolicy()))
```

`DefaultRetryPolicy()` is three attempts, backoff from 500ms, never waiting longer than a
minute at a time, honouring `Retry-After` and spreading the backoff by ±25%. Every field is
yours to change:

```go
policy := easypixel.RetryPolicy{
	MaxAttempts:       5,
	BaseDelay:         time.Second,
	MaxDelay:          30 * time.Second,
	RespectRetryAfter: true,
	Jitter:            true,
}
```

What is retried:

| Result | Wait | Retried |
|---|---|---|
| `429` | `Retry-After` when present, otherwise the backoff | yes |
| `5xx` | backoff | yes |
| transport failure | backoff | yes |
| `4xx` other than `429` | — | no, the request itself is wrong |

Two things worth knowing. A `Retry-After` longer than `MaxDelay` is **not** waited out: the
`429` is returned instead, so a call never blocks longer than you allowed, and
`RetryAfter()` still tells you what the server asked for. And the wait runs on the context,
so a cancelled or expired context returns `context.Canceled` / `context.DeadlineExceeded`
right away rather than sleeping the rest out.

`Send` blocks while it waits. Run one goroutine per matrix if that matters:

```go
var wg sync.WaitGroup
for name, key := range apiKeys {
	wg.Add(1)
	go func(name, key string) {
		defer wg.Done()
		if _, err := easypixel.New(key, opts...).Send(ctx, sceneID, vars); err != nil {
			log.Printf("%s: %v", name, err)
		}
	}(name, key)
}
wg.Wait()
```

---

## Working with Multiple Matrices

Keep the API keys in your own configuration and walk them, so one failing matrix does not stop
the rest:

```go
apiKeys := map[string]string{"entrance": "abc…", "exit": "def…"}
vars := map[string]string{"free_spots": strconv.Itoa(freeSpots())}

for name, key := range apiKeys {
	if _, err := easypixel.New(key).Send(ctx, 42, vars); err != nil {
		log.Printf("%s: %v", name, err)
		continue
	}
	log.Printf("%s: sent", name)
}
```

Each `Webhook` carries its own `Client` with its own connection pool, so keep one instance per
matrix and reuse it. A `Webhook` is safe for concurrent use.

---

## Timeouts

The request timeout defaults to 30 seconds and the connection timeout to 10:

```go
w := easypixel.New("matrix-api-key",
	easypixel.WithTimeout(5*time.Second),
	easypixel.WithConnectTimeout(2*time.Second),
)
```

To control the transport yourself — a proxy, a custom TLS config, a shared connection pool —
pass a client. It is used as given, so both timeout options stop applying and redirects follow
whatever policy that client carries:

```go
w := easypixel.New("matrix-api-key", easypixel.WithHTTPClient(myClient))
```

Other options: `WithBaseURL` points the SDK at another installation, `WithUserAgent` and
`WithHeader` change what is sent with every request.

---

## Command line

```bash
go install github.com/easypixels/sdk-golang/cmd/easypixel-send@latest
```

```bash
export EASYPIXEL_API_KEY=abc…

easypixel-send -scene 42 -var count=15 -var status=open
# accepted: scene 42 on matrix 7
# variables updated: count, status
```

| Flag | Meaning |
|---|---|
| `-scene ID` | scene to show (required) |
| `-key KEY` | matrix API key; defaults to `EASYPIXEL_API_KEY` |
| `-var name=value` | scenario variable, repeatable |
| `-base-url URL` | another installation; defaults to `EASYPIXEL_BASE_URL` |
| `-timeout D` | request timeout, e.g. `5s` |
| `-retry N` | total attempts; above 1 waits out a `429` and tries again |
| `-json` | print the raw API response |

Exit codes: `0` accepted, `1` error, `2` bad usage, `3` rate limited — the wait from
`Retry-After` goes to stderr. Ctrl+C cancels the request and any wait between attempts.

---

## License

MIT © EasyPixel
