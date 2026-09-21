# Testing

For developers writing tests for code that calls this SDK. It covers the fake server pattern, how to
obtain a `*Result` in a test, how to make failures deterministic, and what is not worth mocking.

## The shape of a test

`WithBaseURL` plus `net/http/httptest` is the whole technique. You get the real client, the real
encoder and decoder, the real retry loop, and full control over the wire.

```go
func newTestClient(t *testing.T, h http.HandlerFunc) *jev.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	client, err := jev.New(
		jev.WithAPIKey("test-key"),
		jev.WithBaseURL(srv.URL),
		jev.WithRetryPolicy(jev.NoRetry()),
	)
	if err != nil {
		t.Fatalf("jev.New: %v", err)
	}
	return client
}
```

Three deliberate choices there:

- `WithBaseURL(srv.URL)` points the client at the fake. `httptest` URLs have no trailing slash, no
  query and no credentials, so they pass the base URL validation unchanged.
- `WithAPIKey("test-key")` keeps the test independent of `TYPESAFE_API_KEY`. Without it, the test
  passes or fails depending on the developer's shell.
- `NoRetry()` keeps failure tests fast. With the default policy, a test asserting on a 500 waits out
  two backoffs, 1.1s to 1.5s, before it finishes.

## Asserting on the request

The handler sees exactly what the SDK sends, which is the cheapest way to test question construction:

```go
func TestSendsBothQuestions(t *testing.T) {
	var got struct {
		State     map[string]any             `json:"state"`
		Model     string                     `json:"model"`
		Questions map[string]json.RawMessage `json:"questions"`
	}

	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("Authorization = %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"jev-1.13.0","answers":{
			"is_urgent":{"type":"noul","noul":0.93}
		}}`)
	})

	req := &jev.Request{State: map[string]any{"body": "everything is on fire"}}
	urgent := jev.Ask(req, "is_urgent", jev.Noul("Does `body` convey urgency?"))

	res, err := client.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got.Model != "jev-latest" {
		t.Errorf("model = %q, want the client default", got.Model)
	}
	if _, ok := got.Questions["is_urgent"]; !ok {
		t.Errorf("questions = %v, want an is_urgent entry", got.Questions)
	}

	a, err := urgent.Answer(res)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if !a.Yes(0.8) {
		t.Errorf("Noul = %v, want a strong yes", a.Noul)
	}
}
```

## Getting a `*Result` in a test

`Result` carries its answers in unexported fields, so a struct literal outside the package cannot
hold any. `&jev.Result{Model: "x"}` compiles, but every accessor reports the answer as missing.

That is intentional: a result is whatever the API sent, and the only honest way to build one is to
send that JSON through the client. Wrap it in a helper and the ceremony disappears:

```go
// resultFrom turns a canned response body into a *jev.Result, through the real decoder.
func resultFrom(t *testing.T, body string, build func(*jev.Request)) *jev.Result {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-typesafe-request-id", "req_test")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	client, err := jev.New(jev.WithAPIKey("test-key"), jev.WithBaseURL(srv.URL),
		jev.WithRetryPolicy(jev.NoRetry()))
	if err != nil {
		t.Fatalf("jev.New: %v", err)
	}
	req := &jev.Request{State: "test"}
	build(req)
	res, err := client.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return res
}
```

Now a unit test of your own routing function takes a result built from a fixture and asserts on the
decision, with no live call and no coupling to how the request was assembled:

```go
func TestRoutesLowConfidenceToHuman(t *testing.T) {
	const body = `{"model":"jev-1.13.0","answers":{
		"department":{"type":"choice","choice":"technical",
			"probabilities":{"technical":0.52,"billing":0.48},"confidence":0.48}
	}}`

	res := resultFrom(t, body, func(req *jev.Request) {
		jev.Ask(req, "department", jev.Choice("Which team?", jev.Options[Dept]("billing", "technical")))
	})

	if decision := decide(res); decision != "human" {
		t.Errorf("decide() = %q, want human review below the confidence floor", decision)
	}
}
```

Keep canned bodies in `testdata/*.json` once there are more than a handful; `os.ReadFile` in the
helper beats a wall of raw strings.

## Testing failure paths

The fake server is where you produce statuses the API would otherwise never give you on demand:

```go
func TestSurfacesRateLimit(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.Header().Set("x-typesafe-request-id", "req_abc")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"slow down"}}`)
	})

	req := &jev.Request{State: "test"}
	jev.Ask(req, "q", jev.Noul("Is this urgent?"))

	_, err := client.Evaluate(context.Background(), req)
	if !errors.Is(err, jev.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var apiErr *jev.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an *jev.APIError", err)
	}
	if apiErr.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", apiErr.RetryAfter)
	}
	if apiErr.RequestID != "req_abc" {
		t.Errorf("RequestID = %q", apiErr.RequestID)
	}
	if apiErr.Message != "slow down" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}
```

Other failures worth a test, and how to produce them:

| Failure | How |
| --- | --- |
| Retry then success | A handler counting calls, with a policy whose `MaxRetries` is at least 1 and a tiny `BackoffInitial` |
| Transport error | `srv.Close()` before the call, or a base URL pointing at a closed port |
| Timeout | A handler that sleeps, plus `WithCallTimeout(10*time.Millisecond)` |
| Cancellation | `context.WithCancel` cancelled from inside the handler |
| Malformed body | Write `not json` with a 200 and assert `ErrInvalidResponse` |
| Wrong answer kind | Return a `score` answer for a key registered as a Noul and assert `ErrAnswerKind` |
| Unknown answer kind | Return `{"type":"multichoice",...}` and assert `Kind` and `RawAnswer` |

When a test needs a retry to actually happen, shrink the backoff so the suite stays fast:

```go
p := jev.DefaultRetryPolicy()
p.MaxRetries = 2
p.BackoffInitial = time.Millisecond
p.Jitter = 0
_ = p
```

## Injecting a transport

For anything below the HTTP response, replace the client instead of the server:

```go
type recordingTransport struct {
	inner    http.RoundTripper
	requests []*http.Request
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.requests = append(rt.requests, r)
	return rt.inner.RoundTrip(r)
}
```

Install it with `WithHTTPClient(&http.Client{Transport: rt, CheckRedirect: noRedirects})`. Keep the
redirect policy: replacing the client also replaces the SDK's, and the default one exists to stop the
`Authorization` header being replayed at a redirect target. See [configuration.md](configuration.md).

A `RoundTripper` is also how you test connection-level failures without opening a socket: return an
error from `RoundTrip` and assert on `*jev.TransportError`.

## Parallel and race testing

A `*Client` is immutable after `New` and safe for concurrent use; a `*Result` is safe for concurrent
reads. Both are fine under `t.Parallel()` and `go test -race`. A `*Request` is a plain struct with a
map inside and is not safe to build from several goroutines.

## What not to mock

| Do not | Instead |
| --- | --- |
| Define your own interface over `*jev.Client` only to stub `Evaluate` | Point the real client at `httptest`: you then test the encoding and decoding too |
| Construct `*jev.Result` by hand | Run a canned body through the client, as above |
| Reimplement the retry loop in a fake | Drive it with status codes from the fake server |
| Assert on the exact text of an error | Use `errors.Is` with a sentinel and `errors.As` for the fields |
| Assert on `(*APIError).Message` for control flow | The error body format is undocumented; branch on `StatusCode` or a sentinel |
| Call the live API from unit tests | Keep live calls in a separate, opt-in integration test guarded by `testing.Short()` or a build tag |

An interface over the client is still worth defining at the boundary of *your* domain: a
`Classifier` interface with the one method your service actually needs, implemented once over the SDK
and once as a stub. That stub returns your own types, not `*jev.Result`, and the SDK stays tested at
the wire.

## See also

- [errors.md](errors.md)
- [retries-and-timeouts.md](retries-and-timeouts.md)
- [configuration.md](configuration.md)
- [forward-compatibility.md](forward-compatibility.md)
