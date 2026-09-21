# Retries and timeouts

For developers tuning the client's behavior under load or building a latency budget. It documents
the default policy field by field, the difference between a per-attempt timeout and a context
deadline, and the cases where the client deliberately stops trying.

## The default policy

`DefaultRetryPolicy()` matches the official Python and JavaScript SDKs:

| Field | Default | Meaning |
| --- | --- | --- |
| `MaxRetries` | 2 | Extra attempts after the first. 3 attempts total. |
| `BackoffInitial` | 500ms | Delay before the first retry |
| `BackoffMax` | 5s | Ceiling on the exponential backoff |
| `Jitter` | 0.25 | Fraction of the backoff randomly subtracted; must be in [0,1] |
| `MaxRetryAfter` | 60s | Beyond this, `Retry-After` is ignored and backoff is used. `0` means only a zero delay qualifies, which disables the header in practice. |
| `IgnoreRetryAfter` | false | When true, the header is never consulted |
| `Statuses` | nil | nil means the default set `{408, 429, 500..599}` |
| `ConnectionErrors` | true | Retry transport failures and per-attempt timeouts |

`RetryPolicy` has no magic zero values. `MaxRetries: 0` genuinely disables retries rather than
meaning "unset", so build a custom policy by starting from `DefaultRetryPolicy()` and overriding
fields:

```go
p := jev.DefaultRetryPolicy()
p.MaxRetries = 5
p.BackoffMax = 30 * time.Second
p.Statuses = []int{429, 503, 529}

client, err := jev.New(jev.WithAPIKey("..."), jev.WithRetryPolicy(p))
if err != nil {
	return err
}
_ = client
```

`New` validates the policy: a negative `MaxRetries`, a negative duration, a `Jitter` outside [0,1] or
a status outside 100 to 999 all return an error wrapping `ErrInvalidRequest`.

## Which failures are retried

| Failure | Retried |
| --- | --- |
| Status in `Statuses` (or the default set when nil) | yes, up to `MaxRetries` |
| DNS, TLS, connection reset, refused | only when `ConnectionErrors` is true |
| Per-attempt timeout | only when `ConnectionErrors` is true |
| Caller's context cancelled or expired | never |
| `ErrInvalidResponse` (unparseable or oversized body) | never |
| Any other non-2xx status | never |
| 3xx redirect | never (the default client does not follow redirects) |

Setting `Statuses` replaces the default set rather than adding to it. An empty non-nil slice
(`[]int{}`) therefore disables status-based retries while leaving connection retries on.

## Backoff

The delay before retry number `n` (zero-based) is:

```text
min(BackoffInitial * 2^n, BackoffMax) * (1 - rand[0,1) * Jitter)
```

With the defaults, that is roughly 375ms to 500ms, then 750ms to 1s, capped at 5s. Jitter is
subtracted, never added, so the computed delay is always at or below the nominal backoff. If
`BackoffInitial` or `BackoffMax` is 0, the delay is 0 and retries happen immediately.

## `Retry-After`

When a retryable response carries a retry hint, it wins over the computed backoff:

| Header | Format | Precedence |
| --- | --- | --- |
| `retry-after-ms` | integer milliseconds | checked first |
| `Retry-After` | seconds (integer or decimal) or an HTTP date | fallback |

Details that matter:

- An HTTP date in the past clamps to 0 rather than being rejected.
- A malformed or overflowing `retry-after-ms` falls through to `Retry-After`, as in the official SDKs.
- A negative, `NaN` or infinite `Retry-After` is ignored and backoff is used.
- A value above `MaxRetryAfter` is ignored and backoff is used, so a hostile or mistaken header
  cannot park your goroutine for an hour.
- `IgnoreRetryAfter: true` skips the header entirely.

`retry-after-ms` is not part of the published HTTP API reference; it is supported because the
official SDKs honor it when the server sends it.

Whatever the client decides to do, `(*APIError).RetryAfter` carries the parsed value so you can
implement your own backoff on top.

## Per-attempt timeout versus context deadline

These are two different budgets and both apply:

| | Set by | Bounds |
| --- | --- | --- |
| Per-attempt timeout | `WithTimeout`, `WithCallTimeout`, default 10s | One HTTP attempt, headers plus body read |
| Context deadline | the `ctx` you pass to `Evaluate` | The whole call: every attempt plus every wait between them |

The per-attempt timeout is applied with `context.WithTimeout` derived from your context, so the
deadline always wins when it is the tighter of the two. `WithTimeout(0)` removes the per-attempt
bound and leaves the context solely in charge.

Budget the context for the worst case, not the typical one. With the defaults and no retry hint from
the server, that is three attempts of up to 10s each plus at most 1.5s of backoff (500ms then 1s,
before jitter), a little over 30s:

```go
ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
defer cancel()

res, err := client.Evaluate(ctx, req)
if err != nil {
	return err
}
_ = res
```

That 30s covers the backoff path only. When the server answers 429 or 529 with a `Retry-After`, the
client honors it up to `MaxRetryAfter`, so the default worst case is three attempts of up to 10s
plus two waits of up to 60s: 150s. Lower `MaxRetryAfter`, or set `IgnoreRetryAfter`, if a call must
stay inside a tighter budget than the server asks for.

There is no total retry budget field. The context deadline plays that role, and the client never
sleeps past it: if the next delay would end after the deadline, it stops immediately and returns the
error from the last attempt rather than burning the remaining time on a wait it knows will fail.

## A cancelled context is never retried

When the caller's context is cancelled or expires, the client stops and returns a `*TransportError`
wrapping `context.Canceled` or `context.DeadlineExceeded`. It does not count as a connection error
and no further attempt is made, whatever `ConnectionErrors` says. See [errors.md](errors.md).

## Disabling retries

```go
client, err := jev.New(jev.WithAPIKey("..."), jev.WithRetryPolicy(jev.NoRetry()))
if err != nil {
	return err
}
_ = client
```

`NoRetry()` sets `MaxRetries: 0` and `ConnectionErrors: false`, so exactly one attempt is made.
Use it when a caller above you already retries, when you are inside a request path with a hard
latency budget, and in tests, where it removes several seconds of sleeping.

## Per-call overrides

`Evaluate` and `ListModels` accept `CallOption` values that override client settings for that call
only:

| Option | Effect |
| --- | --- |
| `WithCallTimeout(d)` | Replaces the per-attempt timeout. A negative `d` is ignored. |
| `WithCallRetry(p)` | Replaces the retry policy. Not re-validated, so build it from `DefaultRetryPolicy()`. |
| `WithCallHeader(name, value)` | Adds a header for this call. Protected headers are ignored. |

```go
// A health check must fail fast and must not amplify an outage.
models, err := client.ListModels(ctx,
	jev.WithCallRetry(jev.NoRetry()),
	jev.WithCallTimeout(2*time.Second))
if err != nil {
	return err
}
_ = models

// A background job can afford to be patient.
res, err := client.Evaluate(ctx, req,
	jev.WithCallTimeout(60*time.Second),
	jev.WithCallRetry(patientPolicy()))
if err != nil {
	return err
}
_ = res
```

## Observing retries

Every attempt after the first carries an `X-TypeSafe-Retry-Count` header with the attempt number, so
the server side can see them. On your side, `Attempts` on both `*APIError` and `*TransportError`
reports how many attempts were made, and a logger installed with `WithLogger` records a `Warn` line
per status-driven retry carrying the method, URL, status, delay and attempt number. See
[configuration.md](configuration.md).

## See also

- [errors.md](errors.md)
- [configuration.md](configuration.md)
- [testing.md](testing.md)
- [batching.md](batching.md)
