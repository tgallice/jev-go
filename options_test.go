package jev

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireNilClient(t *testing.T, c *Client) {
	t.Helper()
	require.Nil(t, c)
}

func TestNew(t *testing.T) {
	customHTTP := &http.Client{Timeout: 3 * time.Second}
	customLogger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, tc := range []struct {
		name         string
		env          map[string]string
		opts         []Option
		wantErr      error
		assertClient func(t *testing.T, c *Client)
	}{
		{
			name:         "no API key anywhere",
			wantErr:      ErrMissingAPIKey,
			assertClient: requireNilClient,
		},
		{
			name:         "API key from environment",
			env:          map[string]string{EnvAPIKey: "env-key"},
			assertClient: func(t *testing.T, c *Client) { assert.Equal(t, "env-key", c.cfg.apiKey) },
		},
		{
			name: "environment sets all three values",
			env: map[string]string{
				EnvAPIKey:  "env-key",
				EnvBaseURL: "https://env.example.com/",
				EnvModel:   "jev-preview",
			},
			assertClient: func(t *testing.T, c *Client) {
				assert.Equal(t, "env-key", c.cfg.apiKey)
				assert.Equal(t, "https://env.example.com", c.BaseURL())
				assert.Equal(t, "jev-preview", c.Model())
			},
		},
		{
			name: "options win over environment",
			env: map[string]string{
				EnvAPIKey:  "env-key",
				EnvBaseURL: "https://env.example.com",
				EnvModel:   "jev-preview",
			},
			opts: []Option{
				WithAPIKey("opt-key"),
				WithBaseURL("https://opt.example.com"),
				WithModel("jev-1.13.0"),
			},
			assertClient: func(t *testing.T, c *Client) {
				assert.Equal(t, "opt-key", c.cfg.apiKey)
				assert.Equal(t, "https://opt.example.com", c.BaseURL())
				assert.Equal(t, "jev-1.13.0", c.Model())
			},
		},
		{
			name: "blank environment values are ignored",
			env: map[string]string{
				EnvAPIKey:  "  ",
				EnvBaseURL: "   ",
				EnvModel:   "\t",
			},
			opts: []Option{WithAPIKey("opt-key")},
			assertClient: func(t *testing.T, c *Client) {
				assert.Equal(t, DefaultBaseURL, c.BaseURL())
				assert.Equal(t, DefaultModel, c.Model())
			},
		},
		{
			name:         "environment values are trimmed",
			env:          map[string]string{EnvAPIKey: "  env-key\n", EnvModel: " jev-preview "},
			assertClient: func(t *testing.T, c *Client) { assert.Equal(t, "jev-preview", c.Model()) },
		},
		{
			name: "defaults",
			opts: []Option{WithAPIKey("k")},
			assertClient: func(t *testing.T, c *Client) {
				assert.Equal(t, DefaultBaseURL, c.BaseURL())
				assert.Equal(t, DefaultModel, c.Model())
				assert.Equal(t, DefaultTimeout, c.cfg.timeout)
				assert.Equal(t, DefaultRetryPolicy(), c.cfg.retry)
				assert.NotNil(t, c.cfg.httpClient)
				assert.NotNil(t, c.cfg.logger)
				assert.NotNil(t, c.now)
				assert.NotNil(t, c.sleep)
			},
		},
		{
			name:         "empty model option",
			opts:         []Option{WithAPIKey("k"), WithModel("")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "blank model option",
			opts:         []Option{WithAPIKey("k"), WithModel("   ")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "explicit model",
			opts:         []Option{WithAPIKey("k"), WithModel(" jev-1.13.0 ")},
			assertClient: func(t *testing.T, c *Client) { assert.Equal(t, "jev-1.13.0", c.Model()) },
		},
		{
			name:         "an empty model environment variable is ignored",
			env:          map[string]string{EnvAPIKey: "k", EnvModel: ""},
			assertClient: func(t *testing.T, c *Client) { assert.Equal(t, DefaultModel, c.Model()) },
		},
		{
			name:         "a blank model environment variable is ignored",
			env:          map[string]string{EnvAPIKey: "k", EnvModel: "   "},
			assertClient: func(t *testing.T, c *Client) { assert.Equal(t, DefaultModel, c.Model()) },
		},
		{
			name:         "base URL trailing slashes stripped",
			opts:         []Option{WithAPIKey("k"), WithBaseURL("https://example.com/api///")},
			assertClient: func(t *testing.T, c *Client) { assert.Equal(t, "https://example.com/api", c.BaseURL()) },
		},
		{
			name:         "base URL with unsupported scheme",
			opts:         []Option{WithAPIKey("k"), WithBaseURL("ftp://example.com")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "unparsable base URL",
			opts:         []Option{WithAPIKey("k"), WithBaseURL("http://exa\x7fmple.com")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "base URL without host",
			opts:         []Option{WithAPIKey("k"), WithBaseURL("/v1")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "empty base URL",
			opts:         []Option{WithAPIKey("k"), WithBaseURL("   ")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "base URL with no host at all",
			opts:         []Option{WithAPIKey("k"), WithBaseURL("https:///v1")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "base URL carrying credentials",
			opts:         []Option{WithAPIKey("k"), WithBaseURL("https://user:pass@example.com")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "base URL carrying a query",
			opts:         []Option{WithAPIKey("k"), WithBaseURL("https://example.com/api?x=1")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "base URL carrying an empty query",
			opts:         []Option{WithAPIKey("k"), WithBaseURL("https://example.com/api?")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "base URL carrying a fragment",
			opts:         []Option{WithAPIKey("k"), WithBaseURL("https://example.com/#frag")},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "invalid base URL in environment",
			env:          map[string]string{EnvAPIKey: "k", EnvBaseURL: "ftp://example.com"},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "an invalid environment base URL is overridden by the option",
			env:          map[string]string{EnvAPIKey: "k", EnvBaseURL: "ftp://example.com"},
			opts:         []Option{WithBaseURL("https://opt.example.com")},
			assertClient: func(t *testing.T, c *Client) { assert.Equal(t, "https://opt.example.com", c.BaseURL()) },
		},
		{
			name: "the default http client refuses to follow redirects",
			opts: []Option{WithAPIKey("k")},
			assertClient: func(t *testing.T, c *Client) {
				require.NotNil(t, c.cfg.httpClient.CheckRedirect)
				assert.ErrorIs(t, c.cfg.httpClient.CheckRedirect(nil, nil), http.ErrUseLastResponse)
			},
		},
		{
			name:         "empty API key option clears the environment value",
			env:          map[string]string{EnvAPIKey: "env-key"},
			opts:         []Option{WithAPIKey("  ")},
			wantErr:      ErrMissingAPIKey,
			assertClient: requireNilClient,
		},
		{
			name:         "negative timeout",
			opts:         []Option{WithAPIKey("k"), WithTimeout(-time.Second)},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "zero timeout is accepted",
			opts:         []Option{WithAPIKey("k"), WithTimeout(0)},
			assertClient: func(t *testing.T, c *Client) { assert.Zero(t, c.cfg.timeout) },
		},
		{
			name:         "nil http client",
			opts:         []Option{WithAPIKey("k"), WithHTTPClient(nil)},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "custom http client",
			opts:         []Option{WithAPIKey("k"), WithHTTPClient(customHTTP)},
			assertClient: func(t *testing.T, c *Client) { assert.Same(t, customHTTP, c.cfg.httpClient) },
		},
		{
			name:         "custom logger",
			opts:         []Option{WithAPIKey("k"), WithLogger(customLogger)},
			assertClient: func(t *testing.T, c *Client) { assert.Same(t, customLogger, c.cfg.logger) },
		},
		{
			name:         "nil logger keeps the discarding one",
			opts:         []Option{WithAPIKey("k"), WithLogger(nil)},
			assertClient: func(t *testing.T, c *Client) { assert.NotNil(t, c.cfg.logger) },
		},
		{
			name:         "nil option is skipped",
			opts:         []Option{WithAPIKey("k"), nil},
			assertClient: func(t *testing.T, c *Client) { assert.Equal(t, "k", c.cfg.apiKey) },
		},
		{
			name:         "custom user agent",
			opts:         []Option{WithAPIKey("k"), WithUserAgent(" acme/2.0 ")},
			assertClient: func(t *testing.T, c *Client) { assert.Equal(t, "acme/2.0", c.cfg.userAgent) },
		},
		{
			name: "protected headers are ignored, others kept",
			opts: []Option{
				WithAPIKey("k"),
				WithHeader("X-Trace", "abc"),
				WithHeader("authorization", "Bearer nope"),
				WithHeader("Content-Type", "text/plain"),
				WithHeader("accept", "text/plain"),
				WithHeader("x-typesafe-retry-count", "9"),
				WithHeader("", "ignored"),
			},
			assertClient: func(t *testing.T, c *Client) {
				assert.Equal(t, "abc", c.cfg.headers.Get("X-Trace"))
				assert.Empty(t, c.cfg.headers.Get("Authorization"))
				assert.Empty(t, c.cfg.headers.Get("Content-Type"))
				assert.Empty(t, c.cfg.headers.Get("Accept"))
				assert.Empty(t, c.cfg.headers.Get(headerRetryCount))
			},
		},
		{
			name:         "invalid retry policy",
			opts:         []Option{WithAPIKey("k"), WithRetryPolicy(RetryPolicy{Jitter: 1.5})},
			wantErr:      ErrInvalidRequest,
			assertClient: requireNilClient,
		},
		{
			name:         "custom retry policy",
			opts:         []Option{WithAPIKey("k"), WithRetryPolicy(NoRetry())},
			assertClient: func(t *testing.T, c *Client) { assert.Equal(t, NoRetry(), c.cfg.retry) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{EnvAPIKey, EnvBaseURL, EnvModel} {
				t.Setenv(name, tc.env[name])
			}

			c, err := New(tc.opts...)

			require.ErrorIs(t, err, tc.wantErr)
			tc.assertClient(t, c)
		})
	}
}

func TestResolveCall(t *testing.T) {
	for _, tc := range []struct {
		name       string
		opts       []CallOption
		assertCall func(t *testing.T, cc callConfig)
	}{
		{
			name: "no option keeps the client settings",
			assertCall: func(t *testing.T, cc callConfig) {
				assert.Equal(t, 4*time.Second, cc.timeout)
				assert.Equal(t, DefaultRetryPolicy(), cc.retry)
				assert.Nil(t, cc.headers)
			},
		},
		{
			name:       "call timeout",
			opts:       []CallOption{WithCallTimeout(time.Second)},
			assertCall: func(t *testing.T, cc callConfig) { assert.Equal(t, time.Second, cc.timeout) },
		},
		{
			name:       "negative call timeout is ignored",
			opts:       []CallOption{WithCallTimeout(-time.Second)},
			assertCall: func(t *testing.T, cc callConfig) { assert.Equal(t, 4*time.Second, cc.timeout) },
		},
		{
			name:       "call retry policy",
			opts:       []CallOption{WithCallRetry(NoRetry())},
			assertCall: func(t *testing.T, cc callConfig) { assert.Equal(t, NoRetry(), cc.retry) },
		},
		{
			name: "call headers, protected ones ignored",
			opts: []CallOption{
				WithCallHeader("X-Trace", "abc"),
				WithCallHeader("Authorization", "Bearer nope"),
				WithCallHeader("", "ignored"),
				nil,
			},
			assertCall: func(t *testing.T, cc callConfig) {
				assert.Equal(t, "abc", cc.headers.Get("X-Trace"))
				assert.Empty(t, cc.headers.Get("Authorization"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{cfg: config{timeout: 4 * time.Second, retry: DefaultRetryPolicy()}}

			tc.assertCall(t, c.resolveCall(tc.opts))
		})
	}
}
