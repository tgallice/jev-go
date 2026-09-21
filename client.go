package jev

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/tgallice/jev-go/internal/retry"
)

const (
	evaluatePath = "/v1/systemone"
	modelsPath   = "/v1/models"

	headerRetryCount = "X-TypeSafe-Retry-Count"
	headerRequestID  = "x-typesafe-request-id"

	// Bounds an unexpectedly large or hostile response body.
	maxResponseBody = 32 << 20
)

// errWaitSkipped marks a retry delay that would outlive the caller's deadline.
var errWaitSkipped = errors.New("jev: retry delay exceeds context deadline")

// Client is immutable after New and safe for concurrent use by any number of
// goroutines. It holds no resource that has to be released.
type Client struct {
	cfg config

	// Test hooks; nil rand means math/rand/v2.
	sleep func(context.Context, time.Duration) error
	rand  func() float64
	now   func() time.Time
}

// New builds a client, resolving each setting from the options first, then the
// environment, then the package defaults. It fails with ErrMissingAPIKey when
// no key is available, and with ErrInvalidRequest on an unusable base URL or
// retry policy.
//
//	client, err := jev.New(
//		jev.WithTimeout(8*time.Second),
//		jev.WithRetryPolicy(jev.DefaultRetryPolicy()),
//		jev.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))),
//	)
//	if err != nil {
//		return err // ErrMissingAPIKey when TYPESAFE_API_KEY is unset
//	}
func New(opts ...Option) (*Client, error) {
	cfg, err := newConfig(opts)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: cfg, sleep: retry.Sleep, now: time.Now}, nil
}

// Model is the model sent when Request.Model is empty, as resolved by New.
func (c *Client) Model() string { return c.cfg.model }

// BaseURL is the API root as resolved by New, without trailing slash.
func (c *Client) BaseURL() string { return c.cfg.baseURL }

// Evaluate posts req to /v1/systemone and decodes every answer. A malformed
// request fails with ErrInvalidRequest before any network call; then a non-2xx
// response fails with *APIError once retries are exhausted, a call that got no
// response with *TransportError, and an unexpected body with
// ErrInvalidResponse. An answer of a kind this SDK cannot decode is kept
// reachable through Result.RawAnswer instead of failing the call.
//
//	res, err := client.Evaluate(ctx, req, jev.WithCallTimeout(5*time.Second))
//	if err != nil {
//		return err
//	}
//	answer, err := key.Answer(res)
func (c *Client) Evaluate(ctx context.Context, req *Request, opts ...CallOption) (*Result, error) {
	model := c.cfg.model
	if req != nil && req.Model != "" {
		model = req.Model
	}
	body, err := marshalRequest(req, model)
	if err != nil {
		return nil, err
	}
	resp, raw, err := c.do(ctx, http.MethodPost, evaluatePath, body, opts)
	if err != nil {
		return nil, err
	}
	return parseResult(resp, raw, c.cfg.logger)
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, opts []CallOption) (*http.Response, []byte, error) {
	cc := c.resolveCall(opts)
	endpoint := c.cfg.baseURL + path
	header := c.header(cc, body != nil)
	log := c.cfg.logger

	for attempt := 0; ; attempt++ {
		start := c.now()
		resp, raw, err := c.attempt(ctx, cc, method, endpoint, body, header, attempt)
		if err != nil {
			if errors.Is(err, ErrInvalidResponse) {
				return nil, nil, err
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, nil, &TransportError{
					Method:   method,
					URL:      endpoint,
					Attempts: attempt + 1,
					Timeout:  errors.Is(ctxErr, context.DeadlineExceeded),
					Err:      ctxErr,
				}
			}
			log.Debug("jev: attempt failed", "method", method, "url", endpoint, "attempt", attempt, "error", err)
			if attempt >= cc.retry.MaxRetries || !cc.retry.ConnectionErrors {
				return nil, nil, err
			}
			if out := c.waitRetry(ctx, cc.retry.delay(attempt, nil, c.now(), c.rand), method, endpoint, attempt+1, err); out != nil {
				return nil, nil, out
			}
			continue
		}

		log.Info("jev: response", "method", method, "url", endpoint, "status", resp.StatusCode,
			"duration", c.now().Sub(start), "request_id", resp.Header.Get(headerRequestID), "attempt", attempt)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, raw, nil
		}

		apiErr := newAPIError(method, endpoint, resp, raw, attempt+1, c.now())
		if attempt >= cc.retry.MaxRetries || !cc.retry.retryableStatus(resp.StatusCode) {
			return nil, nil, apiErr
		}
		d := cc.retry.delay(attempt, resp.Header, c.now(), c.rand)
		log.Warn("jev: retrying", "method", method, "url", endpoint, "status", resp.StatusCode, "delay", d, "attempt", attempt)
		if out := c.waitRetry(ctx, d, method, endpoint, attempt+1, apiErr); out != nil {
			return nil, nil, out
		}
	}
}

func (c *Client) attempt(ctx context.Context, cc callConfig, method, url string, body []byte, header http.Header, n int) (*http.Response, []byte, error) {
	attemptCtx := ctx
	if cc.timeout > 0 {
		var cancel context.CancelFunc
		attemptCtx, cancel = context.WithTimeout(ctx, cc.timeout)
		defer cancel()
	}

	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(attemptCtx, method, url, rdr)
	if err != nil {
		return nil, nil, &TransportError{Method: method, URL: url, Attempts: n + 1, Err: err}
	}
	req.Header = header.Clone()
	if n > 0 {
		req.Header.Set(headerRetryCount, strconv.Itoa(n))
	}

	resp, err := c.cfg.httpClient.Do(req)
	if err != nil {
		return nil, nil, &TransportError{Method: method, URL: url, Attempts: n + 1, Timeout: isTimeout(attemptCtx, err), Err: err}
	}
	raw, err := readBody(resp)
	switch {
	case errors.Is(err, ErrInvalidResponse):
		return nil, nil, err
	case err != nil:
		return nil, nil, &TransportError{Method: method, URL: url, Attempts: n + 1, Timeout: isTimeout(attemptCtx, err), Err: err}
	}
	return resp, raw, nil
}

// waitRetry returns nil when the next attempt may start, else the error to surface.
func (c *Client) waitRetry(ctx context.Context, d time.Duration, method, url string, attempts int, last error) error {
	switch err := c.wait(ctx, d); {
	case err == nil:
		return nil
	case errors.Is(err, errWaitSkipped):
		return last
	default:
		return &TransportError{
			Method:   method,
			URL:      url,
			Attempts: attempts,
			Timeout:  errors.Is(err, context.DeadlineExceeded),
			Err:      err,
		}
	}
}

func (c *Client) wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && c.now().Add(d).After(deadline) {
		return errWaitSkipped
	}
	return c.sleep(ctx, d)
}

func (c *Client) resolveCall(opts []CallOption) callConfig {
	cc := callConfig{timeout: c.cfg.timeout, retry: c.cfg.retry}
	for _, opt := range opts {
		if opt != nil {
			opt(&cc)
		}
	}
	return cc
}

func (c *Client) header(cc callConfig, hasBody bool) http.Header {
	h := c.cfg.headers.Clone()
	for name, values := range cc.headers {
		h[name] = slices.Clone(values)
	}

	h.Set("Authorization", "Bearer "+c.cfg.apiKey)
	h.Set("Accept", "application/json")
	h.Set("X-TypeSafe-SDK", "jev-go/"+Version)
	h.Set("X-TypeSafe-Runtime", runtimeDescription())
	// WithUserAgent wins over a User-Agent passed through WithHeader.
	h.Set("User-Agent", userAgent(cmp.Or(c.cfg.userAgent, h.Get("User-Agent"))))
	if hasBody {
		h.Set("Content-Type", "application/json")
	}
	return h
}

// readBody always drains and closes, so the connection can be reused even
// when the response is an error. It reads one byte past the cap to tell a
// body that is exactly at the limit from one that was truncated.
func readBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxResponseBody {
		return nil, fmt.Errorf("%w: response body exceeds %d bytes", ErrInvalidResponse, maxResponseBody)
	}
	return raw, nil
}

func isTimeout(ctx context.Context, err error) bool {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var nerr net.Error
	return errors.As(err, &nerr) && nerr.Timeout()
}
