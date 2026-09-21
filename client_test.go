package jev

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tgallice/jev-go/internal/retry"
)

const testKey = "sk-test-key"

// zeroes feeds an arbitrarily long response body.
type zeroes struct{}

func (zeroes) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

type recordedRequest struct {
	method string
	path   string
	header http.Header
	body   []byte
}

type stub struct {
	status int
	body   string
	header map[string]string
	delay  time.Duration
	abort  bool // truncate the response mid-body
}

// serve replays stubs in order; the last one answers every further attempt.
func serve(stubs ...stub) func(t *testing.T) http.HandlerFunc {
	return func(t *testing.T) http.HandlerFunc {
		var n atomic.Int64
		return func(w http.ResponseWriter, r *http.Request) {
			s := stubs[min(int(n.Add(1))-1, len(stubs)-1)]
			if s.delay > 0 {
				time.Sleep(s.delay)
			}
			if s.abort {
				truncate(t, w, s)
				return
			}
			for name, value := range s.header {
				w.Header().Set(name, value)
			}
			w.WriteHeader(cmp.Or(s.status, http.StatusOK))
			io.WriteString(w, s.body)
		}
	}
}

// truncate hijacks the connection to announce more bytes than it sends, then
// closes: the client always fails mid-body with io.ErrUnexpectedEOF, with no
// race between the flush and the close.
func truncate(t *testing.T, w http.ResponseWriter, s stub) {
	t.Helper()
	conn, buf, err := w.(http.Hijacker).Hijack()
	require.NoError(t, err)
	defer conn.Close()

	status := cmp.Or(s.status, http.StatusOK)
	fmt.Fprintf(buf, "HTTP/1.1 %d %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		status, http.StatusText(status), len(s.body)+100, s.body)
	require.NoError(t, buf.Flush())
}

// check turns an optional case expectation into a callable one.
func check[T any](f func(*testing.T, T)) func(*testing.T, T) {
	if f == nil {
		return func(*testing.T, T) {}
	}
	return f
}

func requestOf(f func() *Request) *Request {
	if f == nil {
		return testRequest()
	}
	return f()
}

func testRequest() *Request {
	req := &Request{State: map[string]any{"subject": "API down", "body": "500 on every call"}}
	Ask(req, "department", Choice("Which team?", Options[dept](billing, technical, sales)))
	Ask(req, "severity", ScoreMap("How severe?", map[severity]Entry{
		cosmetic: "no impact",
		degraded: "workaround exists",
		blocking: "no workaround",
	}))
	Ask(req, "is_urgent", Noul("Urgent?"))
	Ask(req, "tone", Choice("Tone?", Options("calm", "frustrated", "angry")))
	return req
}

func fastRetry() RetryPolicy {
	p := DefaultRetryPolicy()
	p.BackoffInitial = time.Millisecond
	p.BackoffMax = 2 * time.Millisecond
	return p
}

// callFixture bundles what a case hook may rewire before the call.
type callFixture struct {
	client *Client
	cancel context.CancelFunc
}

func TestClientEvaluate(t *testing.T) {
	mixed := string(fixture(t, "response_mixed.json"))
	detail422 := string(fixture(t, "error_422_detail.json"))

	for _, tc := range []struct {
		name           string
		newHandler     func(t *testing.T) http.HandlerFunc
		newRequest     func() *Request
		opts           []Option
		callOpts       []CallOption
		ctxTimeout     time.Duration
		hooks          func(t *testing.T, f *callFixture)
		wantErr        error
		wantCalls      int
		assertErr      func(t *testing.T, err error)
		assertResult   func(t *testing.T, res *Result)
		assertRequests func(t *testing.T, reqs []recordedRequest)
		assertDelays   func(t *testing.T, delays []time.Duration)
	}{
		{
			name:       "200 decodes a mixed batch",
			newHandler: serve(stub{body: mixed, header: map[string]string{headerRequestID: "req-1"}}),
			wantCalls:  1,
			assertResult: func(t *testing.T, res *Result) {
				require.NotNil(t, res)
				assert.Equal(t, "jev-1.13.0", res.Model)
				assert.Equal(t, Usage{InputTokens: 412, OutputTokens: 37}, res.Usage)
				assert.Equal(t, "req-1", res.RequestID)
				assert.Equal(t, http.StatusOK, res.StatusCode)
				assert.Equal(t, []string{"department", "is_urgent", "severity", "tone"}, res.Keys())

				d, err := ChoiceOf[dept](res, "department")
				require.NoError(t, err)
				assert.Equal(t, technical, d.Choice)
				assert.InDelta(t, 0.93, d.Confidence, 1e-9)

				s, err := ScoreOf[severity](res, "severity")
				require.NoError(t, err)
				assert.Equal(t, blocking, s.Nearest())

				u, err := NoulOf(res, "is_urgent")
				require.NoError(t, err)
				assert.True(t, u.Yes(0.9))
			},
			assertRequests: func(t *testing.T, reqs []recordedRequest) {
				require.Len(t, reqs, 1)
				assert.Equal(t, http.MethodPost, reqs[0].method)
				assert.Equal(t, "/v1/systemone", reqs[0].path)
				assert.Equal(t, "Bearer "+testKey, reqs[0].header.Get("Authorization"))
				assert.Equal(t, "application/json", reqs[0].header.Get("Accept"))
				assert.Equal(t, "application/json", reqs[0].header.Get("Content-Type"))
				assert.Equal(t, userAgent(""), reqs[0].header.Get("User-Agent"))
				assert.Equal(t, "jev-go/"+Version, reqs[0].header.Get("X-TypeSafe-SDK"))
				assert.Equal(t, runtimeDescription(), reqs[0].header.Get("X-TypeSafe-Runtime"))
				assert.Empty(t, reqs[0].header.Get(headerRetryCount))

				var sent map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(reqs[0].body, &sent))
				assert.JSONEq(t, `"`+DefaultModel+`"`, string(sent["model"]))
				assert.NotEmpty(t, sent["state"])
				assert.NotEmpty(t, sent["questions"])
			},
		},
		{
			name:       "request model overrides the client model",
			newHandler: serve(stub{body: mixed}),
			opts:       []Option{WithModel("jev-preview")},
			newRequest: func() *Request {
				req := testRequest()
				req.Model = "jev-1.13.0"
				return req
			},
			wantCalls: 1,
			assertRequests: func(t *testing.T, reqs []recordedRequest) {
				var sent map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(reqs[0].body, &sent))
				assert.JSONEq(t, `"jev-1.13.0"`, string(sent["model"]))
			},
		},
		{
			name:       "client model is used when the request has none",
			newHandler: serve(stub{body: mixed}),
			opts:       []Option{WithModel("jev-preview")},
			wantCalls:  1,
			assertRequests: func(t *testing.T, reqs []recordedRequest) {
				var sent map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(reqs[0].body, &sent))
				assert.JSONEq(t, `"jev-preview"`, string(sent["model"]))
			},
		},
		{
			name:       "local validation happens before any call",
			newHandler: serve(stub{body: mixed}),
			newRequest: func() *Request { return &Request{} },
			wantErr:    ErrInvalidRequest,
			wantCalls:  0,
		},
		{
			name:       "200 without answers",
			newHandler: serve(stub{body: `{"model":"jev-1.13.0"}`}),
			wantErr:    ErrInvalidResponse,
			wantCalls:  1,
		},
		{
			name:       "401 is never retried",
			newHandler: serve(stub{status: http.StatusUnauthorized, body: `{"error":"invalid api key"}`}),
			wantErr:    ErrUnauthorized,
			wantCalls:  1,
			assertErr: func(t *testing.T, err error) {
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
				assert.Equal(t, "invalid api key", apiErr.Message)
				assert.Equal(t, 1, apiErr.Attempts)
			},
			assertDelays: func(t *testing.T, delays []time.Duration) { assert.Empty(t, delays) },
		},
		{
			name: "422 formats the FastAPI detail list",
			newHandler: serve(stub{
				status: http.StatusUnprocessableEntity,
				body:   detail422,
				header: map[string]string{headerRequestID: "req-422"},
			}),
			wantErr:   ErrUnprocessable,
			wantCalls: 1,
			assertErr: func(t *testing.T, err error) {
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, "questions.severity.criteria: field required; model: extra fields not permitted", apiErr.Message)
				assert.Equal(t, "req-422", apiErr.RequestID)
				assert.Contains(t, apiErr.Error(), "422 Unprocessable Entity")
			},
		},
		{
			name: "429 waits retry-after-ms then succeeds",
			newHandler: serve(
				stub{status: http.StatusTooManyRequests, header: map[string]string{"retry-after-ms": "10"}},
				stub{body: mixed},
			),
			wantCalls: 2,
			assertResult: func(t *testing.T, res *Result) {
				require.NotNil(t, res)
				assert.Equal(t, "jev-1.13.0", res.Model)
			},
			assertRequests: func(t *testing.T, reqs []recordedRequest) {
				require.Len(t, reqs, 2)
				assert.Empty(t, reqs[0].header.Get(headerRetryCount))
				assert.Equal(t, "1", reqs[1].header.Get(headerRetryCount))
				assert.Equal(t, reqs[0].body, reqs[1].body)
			},
			assertDelays: func(t *testing.T, delays []time.Duration) {
				assert.Equal(t, []time.Duration{10 * time.Millisecond}, delays)
			},
		},
		{
			name:       "529 fails after MaxRetries+1 attempts",
			newHandler: serve(stub{status: 529, body: "overloaded"}),
			wantErr:    ErrOverloaded,
			wantCalls:  3,
			assertErr: func(t *testing.T, err error) {
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, 3, apiErr.Attempts)
				assert.ErrorIs(t, err, ErrServer)
				assert.Contains(t, apiErr.Error(), "529 Overloaded")
			},
			assertRequests: func(t *testing.T, reqs []recordedRequest) {
				require.Len(t, reqs, 3)
				assert.Equal(t, "1", reqs[1].header.Get(headerRetryCount))
				assert.Equal(t, "2", reqs[2].header.Get(headerRetryCount))
			},
			assertDelays: func(t *testing.T, delays []time.Duration) {
				assert.Equal(t, []time.Duration{time.Millisecond, 2 * time.Millisecond}, delays)
			},
		},
		{
			name: "Retry-After above MaxRetryAfter falls back to backoff",
			newHandler: serve(
				stub{status: http.StatusTooManyRequests, header: map[string]string{"Retry-After": "120"}},
				stub{body: mixed},
			),
			wantCalls: 2,
			assertDelays: func(t *testing.T, delays []time.Duration) {
				assert.Equal(t, []time.Duration{time.Millisecond}, delays)
			},
		},
		{
			name:       "per-attempt timeout is retried",
			newHandler: serve(stub{body: mixed, delay: 80 * time.Millisecond}),
			callOpts:   []CallOption{WithCallTimeout(20 * time.Millisecond)},
			wantErr:    context.DeadlineExceeded,
			wantCalls:  3,
			assertErr: func(t *testing.T, err error) {
				var terr *TransportError
				require.ErrorAs(t, err, &terr)
				assert.True(t, terr.Timeout)
				assert.Equal(t, 3, terr.Attempts)
			},
		},
		{
			name:       "connection errors are not retried when disabled",
			newHandler: serve(stub{body: mixed, delay: 80 * time.Millisecond}),
			callOpts:   []CallOption{WithCallTimeout(20 * time.Millisecond), WithCallRetry(NoRetry())},
			wantErr:    context.DeadlineExceeded,
			wantCalls:  1,
			assertErr: func(t *testing.T, err error) {
				var terr *TransportError
				require.ErrorAs(t, err, &terr)
				assert.Equal(t, 1, terr.Attempts)
			},
		},
		{
			name:       "a truncated body is a transport error and is retried",
			newHandler: serve(stub{body: `{"model":`, abort: true}),
			wantErr:    io.ErrUnexpectedEOF,
			wantCalls:  3,
			assertErr: func(t *testing.T, err error) {
				var terr *TransportError
				require.ErrorAs(t, err, &terr)
				assert.False(t, terr.Timeout)
				assert.Equal(t, 3, terr.Attempts)
			},
		},
		{
			name:       "caller cancellation in flight is not retried",
			newHandler: serve(stub{body: mixed, delay: 200 * time.Millisecond}),
			hooks: func(t *testing.T, f *callFixture) {
				go func() {
					time.Sleep(20 * time.Millisecond)
					f.cancel()
				}()
			},
			wantErr:   context.Canceled,
			wantCalls: 1,
			assertErr: func(t *testing.T, err error) {
				var terr *TransportError
				require.ErrorAs(t, err, &terr)
				assert.Equal(t, 1, terr.Attempts)
			},
			assertDelays: func(t *testing.T, delays []time.Duration) { assert.Empty(t, delays) },
		},
		{
			name:       "caller cancellation during the wait is immediate",
			newHandler: serve(stub{status: http.StatusTooManyRequests}),
			hooks: func(t *testing.T, f *callFixture) {
				f.client.sleep = func(ctx context.Context, _ time.Duration) error {
					f.cancel()
					return ctx.Err()
				}
			},
			wantErr:   context.Canceled,
			wantCalls: 1,
			assertErr: func(t *testing.T, err error) {
				var terr *TransportError
				require.ErrorAs(t, err, &terr)
				assert.False(t, terr.Timeout)
				assert.Equal(t, 1, terr.Attempts)
			},
		},
		{
			name:       "a delay beyond the context deadline is not awaited",
			newHandler: serve(stub{status: http.StatusTooManyRequests}),
			opts:       []Option{WithRetryPolicy(DefaultRetryPolicy())},
			ctxTimeout: 300 * time.Millisecond,
			wantErr:    ErrRateLimited,
			wantCalls:  1,
			assertErr: func(t *testing.T, err error) {
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, 1, apiErr.Attempts)
			},
			assertDelays: func(t *testing.T, delays []time.Duration) { assert.Empty(t, delays) },
		},
		{
			name:       "NoRetry stops at the first response",
			newHandler: serve(stub{status: 529}),
			callOpts:   []CallOption{WithCallRetry(NoRetry())},
			wantErr:    ErrOverloaded,
			wantCalls:  1,
			assertDelays: func(t *testing.T, delays []time.Duration) {
				assert.Empty(t, delays)
			},
		},
		{
			name:       "nil request is rejected before any call",
			newHandler: serve(stub{body: mixed}),
			newRequest: func() *Request { return nil },
			wantErr:    ErrInvalidRequest,
			wantCalls:  0,
		},
		{
			name: "408 is retried",
			newHandler: serve(
				stub{status: http.StatusRequestTimeout},
				stub{body: mixed},
			),
			wantCalls: 2,
			assertResult: func(t *testing.T, res *Result) {
				require.NotNil(t, res)
				assert.Equal(t, "jev-1.13.0", res.Model)
			},
		},
		{
			name: "a body past the cap is reported, not truncated",
			newHandler: func(t *testing.T) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					_, err := io.CopyN(w, zeroes{}, maxResponseBody+1)
					assert.NoError(t, err)
				}
			},
			wantErr:   ErrInvalidResponse,
			wantCalls: 1,
			assertErr: func(t *testing.T, err error) {
				assert.Contains(t, err.Error(), "exceeds")
				var terr *TransportError
				assert.NotErrorAs(t, err, &terr)
			},
		},
		{
			name:       "the client timeout bounds each attempt",
			newHandler: serve(stub{body: mixed, delay: 80 * time.Millisecond}),
			opts:       []Option{WithTimeout(20 * time.Millisecond)},
			wantErr:    context.DeadlineExceeded,
			wantCalls:  3,
			assertErr: func(t *testing.T, err error) {
				var terr *TransportError
				require.ErrorAs(t, err, &terr)
				assert.True(t, terr.Timeout)
			},
		},
		{
			name:       "a zero call timeout disables the client one",
			newHandler: serve(stub{body: mixed, delay: 60 * time.Millisecond}),
			opts:       []Option{WithTimeout(20 * time.Millisecond)},
			callOpts:   []CallOption{WithCallTimeout(0)},
			wantCalls:  1,
			assertResult: func(t *testing.T, res *Result) {
				require.NotNil(t, res)
				assert.Equal(t, "jev-1.13.0", res.Model)
			},
		},
		{
			name:       "the http client timeout is honored as a transport timeout",
			newHandler: serve(stub{body: mixed, delay: 80 * time.Millisecond}),
			opts: []Option{
				WithTimeout(0),
				WithHTTPClient(&http.Client{Timeout: 20 * time.Millisecond}),
				WithRetryPolicy(NoRetry()),
			},
			wantErr:   context.DeadlineExceeded,
			wantCalls: 1,
			assertErr: func(t *testing.T, err error) {
				var terr *TransportError
				require.ErrorAs(t, err, &terr)
				assert.True(t, terr.Timeout)
				assert.Equal(t, 1, terr.Attempts)
			},
		},
		{
			name:       "a transport failure stops when the deadline is too close",
			newHandler: serve(stub{body: mixed, delay: 200 * time.Millisecond}),
			opts:       []Option{WithRetryPolicy(DefaultRetryPolicy()), WithTimeout(20 * time.Millisecond)},
			ctxTimeout: 300 * time.Millisecond,
			wantErr:    context.DeadlineExceeded,
			wantCalls:  1,
			assertErr: func(t *testing.T, err error) {
				var terr *TransportError
				require.ErrorAs(t, err, &terr)
				assert.Equal(t, 1, terr.Attempts)
			},
			assertDelays: func(t *testing.T, delays []time.Duration) { assert.Empty(t, delays) },
		},
		{
			name:       "a call header overrides a client header of the same name",
			newHandler: serve(stub{body: mixed}),
			opts:       []Option{WithHeader("X-Trace", "from-client")},
			callOpts:   []CallOption{WithCallHeader("X-Trace", "from-call")},
			wantCalls:  1,
			assertRequests: func(t *testing.T, reqs []recordedRequest) {
				assert.Equal(t, []string{"from-call"}, reqs[0].header.Values("X-Trace"))
			},
		},
		{
			name:       "call headers cannot override protected ones",
			newHandler: serve(stub{body: mixed}),
			opts:       []Option{WithHeader("X-Client", "client-value"), WithUserAgent("acme/1.0")},
			callOpts: []CallOption{
				WithCallHeader("Authorization", "Bearer stolen"),
				WithCallHeader("Accept", "text/plain"),
				WithCallHeader("Content-Type", "text/plain"),
				WithCallHeader(headerRetryCount, "42"),
				WithCallHeader("X-Trace", "trace-1"),
			},
			wantCalls: 1,
			assertRequests: func(t *testing.T, reqs []recordedRequest) {
				require.Len(t, reqs, 1)
				assert.Equal(t, "Bearer "+testKey, reqs[0].header.Get("Authorization"))
				assert.Equal(t, "application/json", reqs[0].header.Get("Accept"))
				assert.Equal(t, "application/json", reqs[0].header.Get("Content-Type"))
				assert.Empty(t, reqs[0].header.Get(headerRetryCount))
				assert.Equal(t, "trace-1", reqs[0].header.Get("X-Trace"))
				assert.Equal(t, "client-value", reqs[0].header.Get("X-Client"))
				assert.Equal(t, "acme/1.0", reqs[0].header.Get("User-Agent"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{EnvAPIKey, EnvBaseURL, EnvModel} {
				t.Setenv(name, "")
			}

			var mu sync.Mutex
			var requests []recordedRequest
			var delays []time.Duration

			handler := tc.newHandler(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, recordedRequest{
					method: r.Method,
					path:   r.URL.Path,
					header: r.Header.Clone(),
					body:   body,
				})
				mu.Unlock()
				handler(w, r)
			}))
			defer srv.Close()

			c, err := New(append([]Option{
				WithAPIKey(testKey),
				WithBaseURL(srv.URL),
				WithRetryPolicy(fastRetry()),
			}, tc.opts...)...)
			require.NoError(t, err)

			c.rand = func() float64 { return 0 }
			c.sleep = func(ctx context.Context, d time.Duration) error {
				mu.Lock()
				delays = append(delays, d)
				mu.Unlock()
				return retry.Sleep(ctx, d)
			}

			ctx, cancel := context.WithTimeout(context.Background(), cmp.Or(tc.ctxTimeout, 30*time.Second))
			defer cancel()
			check(tc.hooks)(t, &callFixture{client: c, cancel: cancel})

			res, err := c.Evaluate(ctx, requestOf(tc.newRequest), tc.callOpts...)

			require.ErrorIs(t, err, tc.wantErr)
			check(tc.assertErr)(t, err)
			check(tc.assertResult)(t, res)

			mu.Lock()
			defer mu.Unlock()
			assert.Len(t, requests, tc.wantCalls)
			check(tc.assertRequests)(t, requests)
			check(tc.assertDelays)(t, delays)
		})
	}
}

// Guards the documented invariant that a Client is immutable and shareable.
func TestClientConcurrentUse(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	mixed := string(fixture(t, "response_mixed.json"))

	// One 429 exercises the retry path while every goroutine still succeeds.
	var throttled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if throttled.CompareAndSwap(false, true) {
			w.Header().Set("retry-after-ms", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, mixed)
	}))
	defer srv.Close()

	c, err := New(WithAPIKey(testKey), WithBaseURL(srv.URL), WithRetryPolicy(fastRetry()))
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.Evaluate(context.Background(), testRequest())
			assert.NoError(t, err)
			if assert.NotNil(t, res) {
				assert.Equal(t, "jev-1.13.0", res.Model)
			}
		}()
	}
	wg.Wait()
}

func TestClientWait(t *testing.T) {
	for _, tc := range []struct {
		name      string
		newCtx    func(t *testing.T) context.Context
		delay     time.Duration
		wantErr   error
		wantSlept bool
		assertErr func(t *testing.T, err error)
	}{
		{
			name:      "sleeps when the deadline is far enough",
			newCtx:    func(t *testing.T) context.Context { return context.Background() },
			delay:     time.Millisecond,
			wantSlept: true,
		},
		{
			name: "already cancelled context",
			newCtx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			delay:   time.Millisecond,
			wantErr: context.Canceled,
		},
		{
			name: "delay beyond the deadline is skipped",
			newCtx: func(t *testing.T) context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				t.Cleanup(cancel)
				return ctx
			},
			delay:   time.Hour,
			wantErr: errWaitSkipped,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var slept bool
			c := &Client{now: time.Now, sleep: func(context.Context, time.Duration) error {
				slept = true
				return nil
			}}

			err := c.wait(tc.newCtx(t), tc.delay)

			require.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, tc.wantSlept, slept)
		})
	}
}

type fakeNetError struct{ timeout bool }

func (e fakeNetError) Error() string   { return "fake net error" }
func (e fakeNetError) Timeout() bool   { return e.timeout }
func (e fakeNetError) Temporary() bool { return false }

func TestIsTimeout(t *testing.T) {
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{name: "expired attempt context", ctx: expired, err: errors.New("closed"), want: true},
		{name: "error wrapping DeadlineExceeded", ctx: context.Background(), err: fmt.Errorf("get: %w", context.DeadlineExceeded), want: true},
		{name: "net.Error that timed out", ctx: context.Background(), err: fakeNetError{timeout: true}, want: true},
		{name: "net.Error that did not time out", ctx: context.Background(), err: fakeNetError{}, want: false},
		{name: "plain error", ctx: context.Background(), err: errors.New("reset by peer"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTimeout(tc.ctx, tc.err))
		})
	}
}

// Guards the invariant that credentials and custom headers are never replayed
// to a redirect target chosen by the server.
func TestClientDoesNotFollowRedirects(t *testing.T) {
	t.Setenv(EnvAPIKey, "")

	var targetHits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
	}))
	defer target.Close()

	var originHits atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		w.Header().Set("Location", target.URL+"/v1/systemone")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	c, err := New(
		WithAPIKey(testKey),
		WithBaseURL(origin.URL),
		WithHeader("X-Client-Secret", "top-secret"),
		WithRetryPolicy(fastRetry()),
	)
	require.NoError(t, err)

	res, err := c.Evaluate(context.Background(), testRequest())

	assert.Nil(t, res)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusTemporaryRedirect, apiErr.StatusCode)
	assert.Equal(t, 1, apiErr.Attempts)
	assert.Equal(t, int64(1), originHits.Load())
	assert.Zero(t, targetHits.Load(), "the redirect target must never be contacted")
}

// Guards the invariant that no credential, header or payload reaches the logs.
func TestClientLoggerNeverLeaksSecrets(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	mixed := string(fixture(t, "response_mixed.json"))

	var attempts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("retry-after-ms", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set(headerRequestID, "req-log")
		io.WriteString(w, mixed)
	}))
	defer srv.Close()

	var logged bytes.Buffer
	c, err := New(
		WithAPIKey(testKey),
		WithBaseURL(srv.URL),
		WithHeader("X-Client-Secret", "top-secret"),
		WithRetryPolicy(fastRetry()),
		WithLogger(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))),
	)
	require.NoError(t, err)

	_, err = c.Evaluate(context.Background(), testRequest())
	require.NoError(t, err)

	out := logged.String()
	assert.Contains(t, out, "req-log", "the logger must still report the request id")
	for _, secret := range []string{testKey, "Bearer", "Authorization", "top-secret", "API down", "500 on every call", "is_urgent"} {
		assert.NotContains(t, out, secret)
	}
}
