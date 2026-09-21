package jev

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Defaults applied when neither an option nor an environment variable sets a value.
const (
	DefaultBaseURL = "https://api.typesafe.ai"
	DefaultModel   = "jev-latest"     // alias; Result.Model names the version that answered
	DefaultTimeout = 10 * time.Second // per attempt, not per call: a retried call takes longer
)

// Environment variables read by New. A blank or whitespace-only value is
// ignored, and the matching option always wins over them.
const (
	EnvAPIKey  = "TYPESAFE_API_KEY"
	EnvBaseURL = "TYPESAFE_BASE_URL"
	EnvModel   = "TYPESAFE_DEFAULT_MODEL"
)

// Callers must not override these: the client sets them on every attempt.
var protectedHeaders = []string{"Authorization", "Content-Type", "Accept", "X-TypeSafe-Retry-Count"}

// Option configures a Client at construction time. New applies options in
// order and returns on the first one that fails, so the later ones are not
// applied.
type Option func(*config) error

// CallOption overrides client settings for a single call, leaving the Client
// itself untouched and usable concurrently.
type CallOption func(*callConfig)

type config struct {
	apiKey    string
	baseURL   string
	model     string
	userAgent string

	httpClient *http.Client
	timeout    time.Duration
	headers    http.Header
	logger     *slog.Logger
	retry      RetryPolicy
}

type callConfig struct {
	timeout time.Duration
	retry   RetryPolicy
	headers http.Header
}

// WithAPIKey sets the bearer token, overriding TYPESAFE_API_KEY. The value is
// trimmed, and a key left empty makes New fail with ErrMissingAPIKey.
func WithAPIKey(key string) Option {
	return func(c *config) error {
		c.apiKey = strings.TrimSpace(key)
		return nil
	}
}

// WithBaseURL sets the API root, overriding TYPESAFE_BASE_URL; trailing slashes
// are stripped. It fails with ErrInvalidRequest unless the URL is http or
// https, has a host, and carries no credentials, query or fragment. A path
// prefix is kept, so "https://host/prefix" calls "/prefix/v1/systemone".
func WithBaseURL(raw string) Option {
	return func(c *config) error {
		normalized, err := normalizeBaseURL(raw)
		if err != nil {
			return err
		}
		c.baseURL = normalized
		return nil
	}
}

// WithModel sets the model used when Request.Model is empty, overriding
// TYPESAFE_DEFAULT_MODEL. An alias or a versioned ID both work; the SDK does
// not check the name against ListModels. A blank model fails with ErrInvalidRequest.
func WithModel(model string) Option {
	return func(c *config) error {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" {
			return fmt.Errorf("%w: model must not be empty", ErrInvalidRequest)
		}
		c.model = trimmed
		return nil
	}
}

// WithHTTPClient replaces the underlying client, to reuse a transport or to
// instrument it. Its own Timeout and its own redirect policy both apply, so
// following redirects, and thereby replaying the Authorization header onto
// whatever the redirect points at, becomes the caller's responsibility; the
// default client follows none. A nil client fails with ErrInvalidRequest.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *config) error {
		if hc == nil {
			return fmt.Errorf("%w: nil http.Client", ErrInvalidRequest)
		}
		c.httpClient = hc
		return nil
	}
}

// WithTimeout bounds each attempt, not the whole call: with retries a call can
// run several times as long. 0 leaves the caller's context in sole charge, and
// a negative duration fails with ErrInvalidRequest.
func WithTimeout(d time.Duration) Option {
	return func(c *config) error {
		if d < 0 {
			return fmt.Errorf("%w: timeout must be >= 0, got %s", ErrInvalidRequest, d)
		}
		c.timeout = d
		return nil
	}
}

// WithUserAgent replaces the default User-Agent, which names the SDK version
// and the Go runtime. It wins over a User-Agent set through WithHeader.
func WithUserAgent(ua string) Option {
	return func(c *config) error {
		c.userAgent = strings.TrimSpace(ua)
		return nil
	}
}

// WithHeader sets a header on every request. Authorization, Content-Type,
// Accept and X-TypeSafe-Retry-Count are written by the client on each attempt
// and cannot be overridden: setting one of them is silently ignored.
func WithHeader(name, value string) Option {
	return func(c *config) error {
		if name == "" || protectedHeader(name) {
			return nil
		}
		c.headers.Set(name, value)
		return nil
	}
}

// WithLogger sets the logger: Debug per failed attempt, Info per response, Warn
// on a retry or an unknown answer type. Nothing is logged by default, and
// neither the API key nor any request or response body is ever logged. A nil
// logger is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) error {
		if l != nil {
			c.logger = l
		}
		return nil
	}
}

// WithRetryPolicy replaces the retry policy. New validates it and fails with
// ErrInvalidRequest on a negative MaxRetries or duration, a Jitter outside
// [0,1], or a status outside 100..999.
func WithRetryPolicy(p RetryPolicy) Option {
	return func(c *config) error {
		c.retry = p
		return nil
	}
}

// WithCallTimeout overrides the per-attempt timeout for one call; 0 hands the
// call to its context alone, and a negative duration is ignored.
func WithCallTimeout(d time.Duration) CallOption {
	return func(cc *callConfig) {
		if d >= 0 {
			cc.timeout = d
		}
	}
}

// WithCallRetry overrides the retry policy for one call. Unlike
// WithRetryPolicy it is not validated, so an out-of-range field is used as
// given.
func WithCallRetry(p RetryPolicy) CallOption {
	return func(cc *callConfig) { cc.retry = p }
}

// WithCallHeader sets a header on one call, over anything WithHeader set.
// Protected headers are ignored, as with WithHeader.
func WithCallHeader(name, value string) CallOption {
	return func(cc *callConfig) {
		if name == "" || protectedHeader(name) {
			return
		}
		if cc.headers == nil {
			cc.headers = http.Header{}
		}
		cc.headers.Set(name, value)
	}
}

// defaultHTTPClient never follows redirects: net/http replays custom headers
// on every hop and keeps Authorization across subdomains and https-to-http
// downgrades. A 3xx surfaces as a non-retryable *APIError instead.
func defaultHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func newConfig(opts []Option) (config, error) {
	cfg := config{
		baseURL:    DefaultBaseURL,
		model:      DefaultModel,
		httpClient: defaultHTTPClient(),
		timeout:    DefaultTimeout,
		headers:    http.Header{},
		logger:     slog.New(slog.DiscardHandler),
		retry:      DefaultRetryPolicy(),
	}

	if v, ok := envValue(EnvAPIKey); ok {
		cfg.apiKey = v
	}
	if v, ok := envValue(EnvBaseURL); ok {
		cfg.baseURL = v
	}
	if v, ok := envValue(EnvModel); ok {
		cfg.model = v
	}

	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return config{}, err
		}
	}

	if cfg.apiKey == "" {
		return config{}, ErrMissingAPIKey
	}
	// Normalized last: an unusable environment value must not outweigh WithBaseURL.
	baseURL, err := normalizeBaseURL(cfg.baseURL)
	if err != nil {
		return config{}, err
	}
	cfg.baseURL = baseURL
	if err := cfg.retry.validate(); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func envValue(name string) (string, bool) {
	v := strings.TrimSpace(os.Getenv(name))
	return v, v != ""
}

func normalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return "", fmt.Errorf("%w: empty base URL", ErrInvalidRequest)
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: base URL %q: %v", ErrInvalidRequest, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: base URL %q must use http or https", ErrInvalidRequest, raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: base URL %q has no host", ErrInvalidRequest, raw)
	}
	// Credentials would end up in error messages and logs; a query or a
	// fragment would be mangled by the path concatenation.
	if u.User != nil {
		return "", fmt.Errorf("%w: base URL must not carry credentials", ErrInvalidRequest)
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("%w: base URL %q must not carry a query or a fragment", ErrInvalidRequest, raw)
	}
	return trimmed, nil
}

func protectedHeader(name string) bool {
	canonical := http.CanonicalHeaderKey(name)
	for _, p := range protectedHeaders {
		if http.CanonicalHeaderKey(p) == canonical {
			return true
		}
	}
	return false
}
