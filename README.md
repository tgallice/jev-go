# jev-go

[![CI](https://github.com/tgallice/jev-go/actions/workflows/ci.yml/badge.svg)](https://github.com/tgallice/jev-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/tgallice/jev-go.svg)](https://pkg.go.dev/github.com/tgallice/jev-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

`jev-go` is a Go client for the [TypeSafe.ai](https://typesafe.ai) System One API (model family `jev`). It asks a batch of typed questions about a piece of state and gets back typed decisions with calibrated probabilities, not generated text.

## Why jev-go

Use it when a piece of state needs a decision: routing to a queue, classifying into a fixed set of categories, triaging by severity or urgency, moderation, re-ranking candidates. The API returns a probability (or a distribution over your own enum), so the decision comes with a confidence you can gate on.

Do not reach for it to generate prose, summaries or freeform text: System One answers typed questions, it does not produce open-ended output.

## Installation

```
go get github.com/tgallice/jev-go
```

Requires Go 1.26. Zero production dependencies: the client is built entirely on the standard library.

You need a TypeSafe.ai API key. Pass it with `jev.WithAPIKey` or set it in the `TYPESAFE_API_KEY` environment variable; `jev.New` fails with `jev.ErrMissingAPIKey` if neither is set.

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/tgallice/jev-go"
)

func main() {
	client, err := jev.New()
	if err != nil {
		log.Fatal(err)
	}

	req := &jev.Request{State: "Our integration returns 500 on every request."}
	urgent := jev.Ask(req, "is_urgent", jev.Noul("Does this convey urgency?"))

	res, err := client.Evaluate(context.Background(), req)
	if err != nil {
		log.Fatal(err)
	}

	answer, err := urgent.Answer(res)
	if err != nil {
		log.Fatal(err)
	}
	if answer.Yes(0.8) {
		fmt.Println("urgent")
	}
}
```

`State` is the content the model reasons about: a string, a struct, a map or a slice. Each question is registered on the request with `Ask`, which hands back a typed `Key[A]` used to read the matching answer once `Evaluate` returns.

## Heterogeneous batches

Several differently typed questions, each with its own user-defined enum, can leave in one `Evaluate` call and come back typed, with no type assertion at the call site.

```go
type Dept string

const (
	Billing   Dept = "billing"
	Technical Dept = "technical"
)

type Severity int

const (
	Cosmetic Severity = iota
	Degraded
	Blocking
)

req := &jev.Request{State: "Our integration returns 500 on every request."}

dept := jev.Ask(req, "department", jev.Choice(
	"Which team should handle this?",
	map[Dept]jev.Entry{
		Billing:   "Payments, invoicing, refunds",
		Technical: "Bugs, outages, integrations",
	},
))
severity := jev.Ask(req, "severity", jev.ScoreMap(
	"How severe is the issue?",
	map[Severity]jev.Entry{
		Cosmetic: "Cosmetic; no impact",
		Degraded: "Broken or degraded; workaround exists",
		Blocking: "Blocking; no workaround",
	},
))
urgent := jev.Ask(req, "is_urgent", jev.Noul("Is this urgent?"))

res, err := client.Evaluate(ctx, req)
if err != nil {
	return err
}

d, err := dept.Answer(res)     // jev.ChoiceAnswer[Dept], no type assertion
s, err := severity.Answer(res) // jev.ScoreAnswer[Severity]
u, err := urgent.Answer(res)   // jev.NoulAnswer

fmt.Println(d.Choice, d.Confidence)           // technical 0.82
fmt.Println(s.Score, s.Nearest() == Blocking) // 1.8 true
fmt.Println(u.Noul, u.Yes(0.8))               // 0.93 true
```

What those three answers actually say:

| Answer | Reads as |
| --- | --- |
| `d` | `Technical` is the pick, with 0.82 confidence. `d.Probabilities` holds `map[billing:0.18 technical:0.82]`, and `d.Ranked()` gives `[technical billing]`, runner-up included. |
| `s` | 1.8 on a 0..2 scale: between `Degraded` and `Blocking`, closer to `Blocking`. `s.Nearest()` rounds it to `Blocking`, a `Severity` to compare or switch on; `s.Legend[s.Nearest()]` is what gives back the description. |
| `u` | A 0.93 probability that the ticket conveys urgency. A noul carries no confidence: the probability is the whole signal, and `u.Yes(0.8)` is the gate. |

`Ask[A any](req *Request, name string, q TypedQuestion[A]) Key[A]` infers `A` from the question passed in, so each `Key` carries its own answer type. For requests built without `Ask` (e.g. from a dynamic set of keys), the free functions `NoulOf`, `ChoiceOf[T]` and `ScoreOf[L]` read an answer by key against a `*Result` directly. See [docs/batching.md](docs/batching.md).

## Primitives

System One has three question primitives: `Noul`, `Choice`, `Score`. See [docs/primitives.md](docs/primitives.md) for the full reference.

### Noul

A yes/no question, answered as a probability of "yes". Use it for a single boolean signal: is this urgent, does this need review, is this spam.

```go
urgent := jev.Ask(req, "is_urgent", jev.Noul("Does this convey urgency?").
	WithCriteria("Explicitly time-sensitive", "No urgency expressed"))

res, err := client.Evaluate(ctx, req)
if err != nil {
	return err
}
answer, err := urgent.Answer(res) // jev.NoulAnswer
if err != nil {
	return err
}
if answer.Yes(0.8) {
	// ...
}
```

### Choice

A single pick among a labeled set of options, answered with a probability distribution over all of them. Use it when the state must be routed to exactly one of several known categories.

```go
dept := jev.Ask(req, "department", jev.Choice(
	"Which team should handle this?",
	map[Dept]jev.Entry{
		Billing:   "Payments, invoicing, refunds",
		Technical: "Bugs, outages, integrations",
	},
))

res, err := client.Evaluate(ctx, req)
if err != nil {
	return err
}
answer, err := dept.Answer(res) // jev.ChoiceAnswer[Dept]
if err != nil {
	return err
}
fmt.Println(answer.Choice, answer.Confidence, answer.Ranked())
// technical 0.82 [technical billing]
```

`jev.Options(labels...)` builds an option set when none of the labels need a description.

### Score

A rating on an ordered scale, answered as a probability-weighted mean that may fall between two levels. Use it for severity, quality or any graded judgment where "how much" matters more than "which one".

```go
severity := jev.Ask(req, "severity", jev.ScoreMap(
	"How severe is the issue?",
	map[Severity]jev.Entry{
		Cosmetic: "Cosmetic; no impact",
		Degraded: "Broken or degraded; workaround exists",
		Blocking: "Blocking; no workaround",
	},
))

res, err := client.Evaluate(ctx, req)
if err != nil {
	return err
}
answer, err := severity.Answer(res) // jev.ScoreAnswer[Severity]
if err != nil {
	return err
}
fmt.Println(answer.Score, answer.Legend[answer.Nearest()])
// 1.8 Blocking; no workaround

switch answer.Nearest() {
case Blocking:
	page()
case Degraded:
	openTicket()
}
```

`Score` is the number to threshold and sort on; `Nearest()` is the level to switch on, a `Severity` rather than a label, which is why `Legend[Nearest()]` is what prints as text.

`ScoreMap` infers the level type from the map and requires keys `0..len-1`. `Score[L](instructions, levels...)` builds the same question from an ordered slice when a map is less convenient.

## Confidence gating

TypeSafe's recommended usage: act on an answer only above a confidence threshold set by the cost of a wrong decision, and fall back otherwise.

```go
switch {
case answer.Confidence < 0.5:
	routeToHuman(answer.Ranked())
case answer.Choice == Technical:
	openBug()
default:
	assign(answer.Choice)
}
```

`ChoiceAnswer.Confidence` and `ScoreAnswer.Confidence` come straight from the API; `NoulAnswer` has no confidence field, gate on `Yes(threshold)` instead. `ChoiceAnswer.Probabilities` and `ChoiceAnswer.Ranked()` let you inspect the runner-up before committing to the top choice. See [docs/confidence.md](docs/confidence.md).

## Errors

`Evaluate` fails with `ErrInvalidRequest` before any network call. Both `Evaluate` and `ListModels` can then fail with `*jev.APIError` (a non-2xx response, once retries are exhausted) or `*jev.TransportError` (no HTTP response: DNS, TLS, connection reset, a per-attempt timeout, or ctx cancellation).

| HTTP status | Sentinel | Retryable by default |
| --- | --- | --- |
| 400 | `ErrBadRequest` | no |
| 401 | `ErrUnauthorized` | no |
| 403 | `ErrForbidden` | no |
| 404 | `ErrNotFound` | no |
| 408 | (none, use `errors.As`) | yes |
| 422 | `ErrUnprocessable` | no |
| 429 | `ErrRateLimited` | yes |
| 529 | `ErrOverloaded` (also matches `ErrServer`) | yes |
| 500-599 | `ErrServer` | yes |

```go
res, err := client.Evaluate(ctx, req)
if err != nil {
	var apiErr *jev.APIError
	switch {
	case errors.Is(err, jev.ErrRateLimited):
		errors.As(err, &apiErr)
		fmt.Println("retry after", apiErr.RetryAfter)
	case errors.As(err, &apiErr):
		fmt.Println("API error", apiErr.StatusCode, apiErr.Message, apiErr.RequestID)
	case errors.Is(err, jev.ErrInvalidRequest):
		fmt.Println("local bug:", err)
	default:
		fmt.Println(err)
	}
	return
}
fmt.Println(res.RequestID, res.StatusCode)
```

`*jev.APIError` also carries `Body`, `Header` and `Attempts`; `*jev.TransportError` wraps the underlying network error (`Unwrap`) and reports whether it was a per-attempt timeout. See [docs/errors.md](docs/errors.md).

A successful `*Result` carries its own status and headers alongside the decoded answers:

| Field | Meaning |
| --- | --- |
| `Model` | Model that produced the answers |
| `Usage` | Input/output token counts |
| `RequestID` | `x-typesafe-request-id`, empty when absent |
| `StatusCode` | HTTP status of the response (200 on success) |
| `Header` | Response headers; must not be modified |

## Configuration

| Option | Effect |
| --- | --- |
| `WithAPIKey(key)` | Bearer token, overrides `TYPESAFE_API_KEY` |
| `WithBaseURL(url)` | API root, trailing slashes stripped |
| `WithModel(model)` | Default model used when `Request.Model` is empty |
| `WithHTTPClient(hc)` | Replaces the underlying `*http.Client`; its own `Timeout` and redirect policy both apply |
| `WithTimeout(d)` | Per-attempt timeout, not per call; `0` leaves the caller's context in charge |
| `WithUserAgent(ua)` | Replaces the default `User-Agent` |
| `WithHeader(name, value)` | Adds a header to every request; protected headers are ignored |
| `WithLogger(l)` | `*slog.Logger`; nothing is logged by default |
| `WithRetryPolicy(p)` | Replaces the retry policy, validated by `New` |

| Call option | Effect |
| --- | --- |
| `WithCallTimeout(d)` | Overrides the per-attempt timeout for one call |
| `WithCallRetry(p)` | Overrides the retry policy for one call |
| `WithCallHeader(name, value)` | Adds a header to one call; protected headers are ignored |

Protected headers (`Authorization`, `Content-Type`, `Accept`, `X-TypeSafe-Retry-Count`) are always set by the client and cannot be overridden through `WithHeader` or `WithCallHeader`.

| Environment variable | Effect |
| --- | --- |
| `TYPESAFE_API_KEY` | Bearer token, overridden by `WithAPIKey` |
| `TYPESAFE_BASE_URL` | API root, overridden by `WithBaseURL` |
| `TYPESAFE_DEFAULT_MODEL` | Default model, overridden by `WithModel` |

A blank environment variable is treated as unset. `New` resolves settings in this order: options, then environment, then defaults (`DefaultBaseURL`, `DefaultModel`, `DefaultTimeout`). See [docs/configuration.md](docs/configuration.md).

## Retries

The default policy (`DefaultRetryPolicy`, matching the official Python and JS SDKs) retries `408`, `429` and `5xx` responses plus connection errors, up to 2 extra attempts, with exponential backoff and jitter, honoring `Retry-After` up to a ceiling.

| Field | Meaning |
| --- | --- |
| `MaxRetries` | Extra attempts after the first; `0` disables retries |
| `BackoffInitial` | Delay before the first retry |
| `BackoffMax` | Backoff ceiling |
| `Jitter` | Fraction of the backoff randomly subtracted, in `[0,1]` |
| `MaxRetryAfter` | Beyond this, a server `Retry-After` is ignored in favor of backoff |
| `IgnoreRetryAfter` | Never honor `Retry-After`, always use backoff |
| `Statuses` | `nil` means the default set `{408, 429, 500..599}` |
| `ConnectionErrors` | Retry network errors and per-attempt timeouts |

`RetryPolicy` has no magic zero value: start from `DefaultRetryPolicy()` and override fields, or use `NoRetry()`. `MaxRetries: 0` truly disables retries. See [docs/retries-and-timeouts.md](docs/retries-and-timeouts.md).

## Security note

The default HTTP client never follows redirects. If you supply your own `*http.Client` through `WithHTTPClient`, its redirect policy applies instead, and following redirects is then your responsibility: a 3xx response followed automatically would replay the `Authorization` header against whatever host the redirect points to.

## Forward compatibility

The API may add question types, response fields or top-level request fields before this SDK catches up. `jev.RawQuestion` forwards an unknown question type untouched, `Request.Extra` adds top-level request fields, and `Result.RawAnswer` / `Result.Kind` / `Result.RawBody` read back what the SDK does not decode. See [docs/forward-compatibility.md](docs/forward-compatibility.md).

## Documentation

| Guide | Covers |
| --- | --- |
| [docs/getting-started.md](docs/getting-started.md) | A longer walkthrough from installation to first evaluation |
| [docs/state.md](docs/state.md) | What `Request.State` accepts and how to shape it |
| [docs/primitives.md](docs/primitives.md) | `Noul`, `Choice`, `Score` in full, including edge cases |
| [docs/batching.md](docs/batching.md) | Heterogeneous batches, `Ask`, and the `*Of` free functions |
| [docs/confidence.md](docs/confidence.md) | Reading and gating on `Confidence`, `Probabilities`, `Ranked` |
| [docs/errors.md](docs/errors.md) | The error hierarchy, sentinels, and `errors.Is`/`errors.As` patterns |
| [docs/retries-and-timeouts.md](docs/retries-and-timeouts.md) | `RetryPolicy`, `Retry-After`, per-attempt vs per-call timeouts |
| [docs/configuration.md](docs/configuration.md) | Every `Option` and `CallOption`, and environment variable resolution |
| [docs/patterns.md](docs/patterns.md) | Routing, triage and moderation patterns built on the primitives |
| [docs/forward-compatibility.md](docs/forward-compatibility.md) | `RawQuestion`, `Request.Extra`, `Result.RawAnswer`/`Kind`/`RawBody` |
| [docs/testing.md](docs/testing.md) | Testing code that calls this SDK, without hitting the real API |

## Development

```
make test    # go test -race ./...
make cover   # coverage profile and function-level report
make lint    # golangci-lint run ./...
```

Zero production dependencies: the client is built entirely on the standard library. `testify` is used only in tests.

## License

MIT, see [LICENSE](LICENSE).
