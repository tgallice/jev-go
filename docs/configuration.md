# Configuration

For whoever wires the client into an application. It lists every option, the environment variables,
the resolution order, what the client logs, and the one security decision you can accidentally undo.

## Resolution order

`New` resolves each setting as **options > environment > defaults**. Options are applied after the
environment is read, so an explicit `With*` always wins. A blank or whitespace-only environment
variable is treated as unset.

```go
// TYPESAFE_BASE_URL is ignored here; the option wins.
client, err := jev.New(
	jev.WithAPIKey(os.Getenv("MY_KEY")),
	jev.WithBaseURL("https://api.staging.example"),
)
if err != nil {
	return err
}
_ = client
```

`New` returns an error rather than panicking, and never performs a network call.

## Environment variables

| Variable | Sets | Constant |
| --- | --- | --- |
| `TYPESAFE_API_KEY` | The bearer token | `jev.EnvAPIKey` |
| `TYPESAFE_BASE_URL` | The API root | `jev.EnvBaseURL` |
| `TYPESAFE_DEFAULT_MODEL` | The model used when `Request.Model` is empty | `jev.EnvModel` |

`TYPESAFE_LOG_LEVEL`, which the official Python and JavaScript SDKs read, is **not** read by this
SDK. Logging is configured with `WithLogger`.

## Defaults

| Setting | Default | Constant |
| --- | --- | --- |
| Base URL | `https://api.typesafe.ai` | `jev.DefaultBaseURL` |
| Model | `jev-latest` | `jev.DefaultModel` |
| Per-attempt timeout | 10s | `jev.DefaultTimeout` |
| Retry policy | `DefaultRetryPolicy()` | |
| HTTP client | a client that does not follow redirects | |
| Logger | `slog.DiscardHandler`: nothing is logged | |
| User-Agent | `jev-go/<Version> (go1.x; os/arch)` | `jev.Version` |

## Client options

| Option | Effect | Errors |
| --- | --- | --- |
| `WithAPIKey(key)` | Sets the bearer token; the value is trimmed | `ErrMissingAPIKey` from `New` if it ends up empty |
| `WithBaseURL(raw)` | Sets the API root; trailing slashes stripped, a path prefix kept | `ErrInvalidRequest` on an empty value, a scheme other than http/https, a missing host, embedded credentials, or a query or fragment |
| `WithModel(model)` | Model used when `Request.Model` is empty | `ErrInvalidRequest` on an empty or whitespace-only value |
| `WithTimeout(d)` | Per-attempt timeout; `0` leaves the context in charge | `ErrInvalidRequest` if negative |
| `WithRetryPolicy(p)` | Replaces the retry policy | `ErrInvalidRequest` if the policy is invalid |
| `WithHTTPClient(hc)` | Replaces the underlying `*http.Client` | `ErrInvalidRequest` if nil |
| `WithHeader(name, value)` | Adds a header to every request | Silently ignored for an empty name or a protected header |
| `WithUserAgent(ua)` | Replaces the `User-Agent` | |
| `WithLogger(l)` | Installs an `*slog.Logger` | A nil logger is ignored |

A nil `Option` in the variadic list is skipped, so configuration can be built conditionally:

```go
var opts []jev.Option
if debug {
	opts = append(opts, jev.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))))
}
client, err := jev.New(append(opts, jev.WithAPIKey("..."))...)
if err != nil {
	return err
}
_ = client
```

## Call options

| Option | Effect |
| --- | --- |
| `WithCallTimeout(d)` | Per-attempt timeout for this call; a negative `d` is ignored |
| `WithCallRetry(p)` | Retry policy for this call; not re-validated |
| `WithCallHeader(name, value)` | Extra header for this call; protected headers ignored |

Call options never mutate the client. A `*Client` is immutable after `New` and safe for concurrent
use; create one per process and share it.

## Protected headers

These are set by the client on every attempt and cannot be replaced with `WithHeader` or
`WithCallHeader`. An attempt to set one is silently ignored:

| Header | Value |
| --- | --- |
| `Authorization` | `Bearer <api key>` |
| `Content-Type` | `application/json`, on requests with a body |
| `Accept` | `application/json` |
| `X-TypeSafe-Retry-Count` | the attempt number, on attempts after the first |

Three more headers are written after your custom headers are merged, so a `WithHeader` value for them
is overwritten:

| Header | Value |
| --- | --- |
| `X-TypeSafe-SDK` | `jev-go/<Version>` |
| `X-TypeSafe-Runtime` | `go/<version> (os; arch)` |
| `User-Agent` | `WithUserAgent` if set, else a `WithHeader` `User-Agent`, else the default |

Anything else you add with `WithHeader` or `WithCallHeader` is sent as-is. Call headers override
client headers of the same name.

## Choosing the model

Three levels, most specific first:

```go
req := &jev.Request{State: ticket, Model: "jev-1.13.0"} // 1. per request
_ = req

client, err := jev.New(jev.WithAPIKey("..."), jev.WithModel("jev-preview")) // 2. per client
if err != nil {
	return err
}
fmt.Println(client.Model()) // 3. else TYPESAFE_DEFAULT_MODEL, else jev-latest
```

`jev-latest` and `jev-preview` are aliases that move when a release ships, so the answers behind them
can change without a change on your side. `Result.Model` always reports the versioned ID that
answered. If you have tuned confidence thresholds against a specific version, pin that version in
`Request.Model` and move on your own schedule.

`ListModels` returns what the API advertises, currently the aliases. Versioned IDs are accepted in
`Model` whether or not they appear in that list.

## Logging

`WithLogger` takes an `*slog.Logger`. By default nothing is logged at all.

| Level | Event | Attributes |
| --- | --- | --- |
| Debug | An attempt failed before a response was obtained | `method`, `url`, `attempt`, `error` |
| Info | A response was received | `method`, `url`, `status`, `duration`, `request_id`, `attempt` |
| Warn | A retry is about to be scheduled **after a retryable status** | `method`, `url`, `status`, `delay`, `attempt` |
| Warn | An answer arrived with a type the SDK does not know | `key`, `type` |

A retry that follows a connection failure or a per-attempt timeout produces only the `Debug` line, not
a `Warn`: there is no status to report. The `Info` line carries `request_id` when the response had
one; no other line does.

Never logged: the API key, the `Authorization` header, the request body, the response body. If you
need a body for debugging, take it from `(*APIError).Body` or `(*Result).RawBody()` and decide
yourself what is safe to record.

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
client, err := jev.New(jev.WithAPIKey("..."), jev.WithLogger(logger))
if err != nil {
	return err
}
_ = client
```

## Security: redirects are your responsibility once you replace the client

The default `*http.Client` is built with `CheckRedirect` returning `http.ErrUseLastResponse`, so it
**does not follow redirects**. That is deliberate. `net/http` replays custom headers on every hop and
keeps `Authorization` across subdomains and across an https-to-http downgrade. A followed 3xx would
therefore hand your API key to whatever host the redirect names. With the default client, a 3xx
surfaces as a non-retryable `*APIError` instead.

`WithHTTPClient` replaces that client wholesale. Its `Timeout` and its redirect policy both apply,
and the SDK does not patch either:

```go
// Unsafe: http.DefaultClient follows up to 10 redirects and replays Authorization.
client, err := jev.New(jev.WithAPIKey("..."), jev.WithHTTPClient(http.DefaultClient))
if err != nil {
	return err
}
_ = client
```

If you supply your own client, keep the same policy:

```go
hc := &http.Client{
	Transport:     myInstrumentedTransport(),
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}
client, err := jev.New(jev.WithAPIKey("..."), jev.WithHTTPClient(hc))
if err != nil {
	return err
}
_ = client
```

Also note that a `Timeout` set on your own client applies to the whole attempt in addition to the
SDK's per-attempt timeout, whichever is tighter. It surfaces as a `*TransportError` with `Timeout`
set to true, exactly like the SDK's own per-attempt timeout.

The base URL is validated for related reasons: credentials in the URL would leak into error messages
and logs, and a query or fragment would be mangled by the path concatenation. Both are rejected by
`WithBaseURL`.

A path prefix, on the other hand, is kept: `WithBaseURL("https://gateway.internal/typesafe")` sends
to `/typesafe/v1/systemone` and `/typesafe/v1/models`. That is what makes the SDK usable behind a
reverse proxy that mounts the API under a subpath.

## See also

- [retries-and-timeouts.md](retries-and-timeouts.md)
- [errors.md](errors.md)
- [testing.md](testing.md)
- [getting-started.md](getting-started.md)
