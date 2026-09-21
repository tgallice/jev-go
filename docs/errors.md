# Errors

For developers writing the failure branch. It maps HTTP statuses onto sentinels, shows what
`errors.Is` and `errors.As` give you, and is explicit about what the SDK does not know.

## Four families

| Family | Type | When |
| --- | --- | --- |
| Local | sentinel errors only | Before any network call: bad configuration or an invalid request |
| HTTP | `*APIError` | A non-2xx response was received, after retries were exhausted |
| Transport | `*TransportError` | No HTTP response at all: DNS, TLS, reset, per-attempt timeout, cancelled context |
| Decoding | sentinel errors only | After a 2xx response: the body or one answer could not be read as expected |

Every error from `Evaluate` and `ListModels` falls into exactly one of these.

## HTTP status to sentinel

| Status | Sentinel | Retried by default | Meaning |
| --- | --- | --- | --- |
| 400 | `ErrBadRequest` | no | Malformed request. Not documented by the API. |
| 401 | `ErrUnauthorized` | no | Key missing or invalid |
| 403 | `ErrForbidden` | no | Access denied. Not documented by the API. |
| 404 | `ErrNotFound` | no | Unknown resource. Not documented by the API. |
| 408 | none | yes | Server-side timeout. Not documented by the API. |
| 422 | `ErrUnprocessable` | no | Body validation failed; the body details the offending field |
| 429 | `ErrRateLimited` | yes | Rate limit exceeded; honor `Retry-After` |
| 500 to 599 | `ErrServer` | yes | Server error. Not documented by the API as a range. |
| 529 | `ErrOverloaded` and `ErrServer` | yes | Temporary overload |

The API's own reference documents 401, 422, 429 and 529. The other rows are generic HTTP semantics
that the SDK maps for convenience, marked above; do not read them as a promise about what the API
returns.

529 satisfies both `ErrOverloaded` and `ErrServer`, so test for `ErrOverloaded` first if you want to
tell them apart. 408 has no sentinel of its own: match it on `apiErr.StatusCode` or let the retry
policy deal with it.

The mapping lives in `(*APIError).Is`, so `errors.Is` works through any wrapping you add.

## Local and decoding sentinels

| Sentinel | Raised by |
| --- | --- |
| `ErrMissingAPIKey` | `New`, when neither `WithAPIKey` nor `TYPESAFE_API_KEY` supplies a key |
| `ErrInvalidRequest` | `New` (bad base URL, empty model, negative timeout, invalid retry policy) and `Evaluate` (nil request, no questions, an empty question key, a nil `Question` value, bad state or criteria, option and level bounds, `Extra` colliding with `state` or `questions`) |
| `ErrInvalidResponse` | A 2xx body that is not valid JSON, has no `answers` object, has an answer without a `type`, or exceeds 32 MiB |
| `ErrAnswerMissing` | `Key.Answer`, `NoulOf`, `ChoiceOf`, `ScoreOf` when the key is not in the result |
| `ErrAnswerKind` | The same accessors when the answer is of a different kind |

The first two are raised before a single byte goes out: reaching one in production means a bug in
your code, not a bad day for the API. The last three are the decoding family and can only happen
once a 2xx response has arrived, so they say something about the payload, not about your call.

A zero `Key[A]` (one not returned by `Ask`) returns `ErrInvalidRequest` from `Answer`.

## Inspecting an `*APIError`

```go
res, err := client.Evaluate(ctx, req)
if err != nil {
	var apiErr *jev.APIError
	switch {
	case errors.Is(err, jev.ErrInvalidRequest), errors.Is(err, jev.ErrMissingAPIKey):
		return fmt.Errorf("jev misconfigured: %w", err) // our bug, do not retry

	case errors.Is(err, jev.ErrRateLimited):
		errors.As(err, &apiErr)
		return fmt.Errorf("rate limited, back off %s (request %s): %w",
			apiErr.RetryAfter, apiErr.RequestID, err)

	case errors.Is(err, jev.ErrUnauthorized):
		return fmt.Errorf("check TYPESAFE_API_KEY: %w", err)

	case errors.Is(err, jev.ErrUnprocessable):
		errors.As(err, &apiErr)
		return fmt.Errorf("request rejected: %s (body %q)", apiErr.Message, apiErr.Body)

	case errors.Is(err, jev.ErrServer):
		errors.As(err, &apiErr)
		return fmt.Errorf("upstream unavailable after %d attempts: %w", apiErr.Attempts, err)

	default:
		return err
	}
}
_ = res
return nil
```

Fields on `*APIError`:

| Field | Type | Notes |
| --- | --- | --- |
| `StatusCode` | `int` | The status as received |
| `Method`, `URL` | `string` | The attempted request |
| `Message` | `string` | Extracted from the body by heuristic; may be `""` |
| `Body` | `[]byte` | The raw body, uncapped |
| `Header` | `http.Header` | The full response headers |
| `RequestID` | `string` | `x-typesafe-request-id`, `""` when absent |
| `RetryAfter` | `time.Duration` | Parsed from `retry-after-ms` or `Retry-After`; 0 when neither is present or parseable |
| `Attempts` | `int` | Total attempts made, including the first |

`Error()` truncates the message (or, when there is none, the body) to 200 bytes so a large or binary
body cannot dominate a log line. `Message` itself is never truncated, so `errors.As` gives you the
full text. The API key is never part of the message.

Logged or printed, a rate-limited call reads:

```
jev: POST https://api.typesafe.ai/v1/systemone: 429 Too Many Requests: slow down (request id req_abc, 3 attempts)
```

The request id is dropped from the suffix when the response carried none, leaving `(3 attempts)`.

## The error body format is undocumented

TypeSafe does not publish the shape of an error response body. `Message` is extracted by a heuristic
copied from the official JavaScript SDK, which tries, in order:

1. the raw text, when the body is not valid JSON
2. `error` as a string
3. `error.message`
4. `message`
5. `detail` as a string
6. `detail.message`
7. `detail` as an array of `{loc, msg}` objects (FastAPI style), joined with `; `, with `body`
   removed from each `loc` path

If none of those match, `Message` is `""` and the full body is still in `Body`. Log `Body` when you
need to know what actually came back, and do not build control flow on the exact text of `Message`.

## Transport errors

```go
res, err := client.Evaluate(ctx, req)
if err != nil {
	var tErr *jev.TransportError
	if errors.As(err, &tErr) {
		switch {
		case errors.Is(err, context.Canceled):
			return err // the caller gave up; do not retry
		case errors.Is(err, context.DeadlineExceeded), tErr.Timeout:
			return fmt.Errorf("jev timed out after %d attempts: %w", tErr.Attempts, err)
		default:
			return fmt.Errorf("jev unreachable: %w", err)
		}
	}
}
_ = res
return nil
```

| Field | Notes |
| --- | --- |
| `Method`, `URL` | The attempted request |
| `Attempts` | Total attempts made |
| `Timeout` | True for a per-attempt timeout, a context deadline, or any `net.Error` reporting a timeout |
| `Err` | The wrapped cause, reachable through `Unwrap` |

There is no status to report, so the cause takes its place:

```
jev: POST https://api.typesafe.ai/v1/systemone: dial tcp 203.0.113.10:443: i/o timeout (after 3 attempts)
```

Because `Unwrap` returns `Err`, `errors.Is(err, context.Canceled)` and
`errors.Is(err, context.DeadlineExceeded)` both work on a `*TransportError`, as does `errors.As` for
`*net.OpError` or `*tls.CertificateVerificationError`.

The distinction that matters operationally: `context.Canceled` means the caller walked away and
nothing should be retried; `context.DeadlineExceeded` means the budget ran out. Neither is a reason
to blame the API.

## Redirects surface as errors

The default HTTP client does not follow redirects. A 3xx therefore arrives as a non-retryable
`*APIError` with that status rather than being replayed against another host with the `Authorization`
header attached. If you install your own `*http.Client` with `WithHTTPClient`, its redirect policy is
yours to own; see the warning in [configuration.md](configuration.md).

## Oversized responses

A response body over 32 MiB is refused with `ErrInvalidResponse` rather than being buffered. This is
a guard against a hostile or broken upstream, not a documented API limit.

## See also

- [retries-and-timeouts.md](retries-and-timeouts.md)
- [configuration.md](configuration.md)
- [testing.md](testing.md)
- [getting-started.md](getting-started.md)
