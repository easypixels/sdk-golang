// Command easypixel-send shows a scene on an EasyPixel matrix from the shell.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	easypixel "github.com/easypixels/sdk-golang"
)

// Exit codes.
const (
	exitOK        = 0
	exitError     = 1
	exitUsage     = 2
	exitRateLimit = 3
)

// variableFlag collects repeated -var name=value pairs.
type variableFlag map[string]string

func (v variableFlag) String() string {
	pairs := make([]string, 0, len(v))
	for name, value := range v {
		pairs = append(pairs, name+"="+value)
	}
	return strings.Join(pairs, ",")
}

func (v variableFlag) Set(raw string) error {
	name, value, found := strings.Cut(raw, "=")
	if !found || name == "" {
		return fmt.Errorf("expected name=value, got %q", raw)
	}
	v[name] = value
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("easypixel-send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: easypixel-send -scene ID [options]\n\n")
		fmt.Fprintf(stderr, "Shows a scene on the matrix the API key belongs to.\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(stderr, "\nEnvironment:\n")
		fmt.Fprintf(stderr, "  EASYPIXEL_API_KEY   default for -key\n")
		fmt.Fprintf(stderr, "  EASYPIXEL_BASE_URL  default for -base-url\n")
		fmt.Fprintf(stderr, "\nExit codes: 0 accepted, 1 error, 2 usage, 3 rate limited\n")
	}

	var (
		sceneID   = fs.Int("scene", 0, "scene `ID` to show (required)")
		apiKey    = fs.String("key", os.Getenv("EASYPIXEL_API_KEY"), "matrix API `key`")
		baseURL   = fs.String("base-url", os.Getenv("EASYPIXEL_BASE_URL"), "API base `URL`")
		timeout   = fs.Duration("timeout", easypixel.DefaultTimeout, "request `timeout`")
		retries   = fs.Int("retry", 1, "total attempts; above 1 waits out 429 and retries")
		asJSON    = fs.Bool("json", false, "print the raw API response")
		variables = variableFlag{}
	)
	fs.Var(variables, "var", "scenario variable as `name=value`, repeatable")

	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *sceneID <= 0 {
		fmt.Fprintln(stderr, "easypixel-send: -scene is required")
		fs.Usage()
		return exitUsage
	}
	if *apiKey == "" {
		fmt.Fprintln(stderr, "easypixel-send: no API key: pass -key or set EASYPIXEL_API_KEY")
		return exitUsage
	}

	opts := []easypixel.Option{
		easypixel.WithBaseURL(*baseURL),
		easypixel.WithTimeout(*timeout),
	}
	if *retries > 1 {
		policy := easypixel.DefaultRetryPolicy()
		policy.MaxAttempts = *retries
		opts = append(opts, easypixel.WithRetry(policy))
	}

	// SIGINT снимает и сам запрос, и паузу между попытками.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := easypixel.New(*apiKey, opts...).Send(ctx, *sceneID, variables)
	if err != nil {
		return report(stderr, err)
	}

	if *asJSON {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if encodeErr := encoder.Encode(result); encodeErr != nil {
			fmt.Fprintf(stderr, "easypixel-send: %v\n", encodeErr)
			return exitError
		}
		return exitOK
	}

	fmt.Fprintf(stdout, "accepted: scene %d on matrix %d\n", result.SceneID, result.MatrixID)
	if len(result.VariablesUpdated) > 0 {
		fmt.Fprintf(stdout, "variables updated: %s\n", strings.Join(result.VariablesUpdated, ", "))
	} else if len(variables) > 0 {
		fmt.Fprintln(stdout, "variables updated: none — the scenario defines no variable by those names")
	}

	return exitOK
}

func report(stderr *os.File, err error) int {
	var apiErr *easypixel.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 429 {
		if wait, ok := apiErr.RetryAfter(); ok {
			fmt.Fprintf(stderr, "easypixel-send: rate limited, retry in %s\n", wait.Round(time.Second))
		} else {
			fmt.Fprintln(stderr, "easypixel-send: rate limited, no Retry-After given")
		}
		return exitRateLimit
	}

	fmt.Fprintf(stderr, "easypixel-send: %v\n", err)
	if errors.As(err, &apiErr) && len(apiErr.ValidationErrors) > 0 {
		for field, messages := range apiErr.ValidationErrors {
			fmt.Fprintf(stderr, "  %s: %s\n", field, strings.Join(messages, "; "))
		}
	}

	return exitError
}
