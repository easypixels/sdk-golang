package easypixel

import (
	"net"
	"net/http"
	"time"
)

// Defaults matching the PHP SDK.
const (
	// DefaultBaseURL is the production EasyPixel API.
	DefaultBaseURL = "https://api.easypixel.ru"
	// DefaultTimeout caps a whole request, including reading the response.
	DefaultTimeout = 30 * time.Second
	// DefaultConnectTimeout caps establishing the connection.
	DefaultConnectTimeout = 10 * time.Second
)

// Version is the SDK version reported in the User-Agent header.
const Version = "1.0.0"

const defaultUserAgent = "easypixel-go/" + Version

type config struct {
	baseURL        string
	httpClient     *http.Client
	header         http.Header
	timeout        time.Duration
	connectTimeout time.Duration
	retry          RetryPolicy
}

// Option configures a Webhook or a Client.
type Option func(*config)

// WithBaseURL points the SDK at another installation, e.g. a local backend.
func WithBaseURL(baseURL string) Option {
	return func(c *config) {
		if baseURL != "" {
			c.baseURL = baseURL
		}
	}
}

// WithHTTPClient supplies the HTTP client to send with. It is used as given,
// so WithTimeout and WithConnectTimeout no longer apply and redirects follow
// whatever policy that client carries.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *config) { c.httpClient = hc }
}

// WithTimeout caps a whole request. Ignored together with WithHTTPClient.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithConnectTimeout caps establishing the connection. Ignored together with
// WithHTTPClient.
func WithConnectTimeout(d time.Duration) Option {
	return func(c *config) { c.connectTimeout = d }
}

// WithHeader sets a header sent with every request, replacing any previous
// value under that name.
func WithHeader(name, value string) Option {
	return func(c *config) { c.header.Set(name, value) }
}

// WithUserAgent replaces the default User-Agent.
func WithUserAgent(ua string) Option {
	return func(c *config) { c.header.Set("User-Agent", ua) }
}

// WithRetry enables retries. Without it a request is sent exactly once and the
// first 429 comes back to the caller, as it does in the PHP SDK.
func WithRetry(p RetryPolicy) Option {
	return func(c *config) { c.retry = p }
}

func newConfig(opts ...Option) *config {
	cfg := &config{
		baseURL:        DefaultBaseURL,
		timeout:        DefaultTimeout,
		connectTimeout: DefaultConnectTimeout,
		header:         http.Header{},
	}
	cfg.header.Set("Accept", "application/json")
	cfg.header.Set("User-Agent", defaultUserAgent)

	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}

	return cfg
}

func (c *config) buildHTTPClient() *http.Client {
	if c.httpClient != nil {
		return c.httpClient
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   c.connectTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext

	return &http.Client{
		Timeout:   c.timeout,
		Transport: transport,
		// Редиректы не выполняются: ключ матрицы лежит в пути, и следовать за
		// чужим Location значит отдать его постороннему хосту.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
