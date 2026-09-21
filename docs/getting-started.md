# Getting started

For Go developers who have never called the TypeSafe.ai System One API. It takes you from an empty
directory to a running program that sends a state and three typed questions in one HTTP call.

## 1. Get an API key

Keys are created in the TypeSafe console at `https://console.typesafe.ai/keys`. The key is a bearer
token sent on every request. Keep it out of source control.

```sh
export TYPESAFE_API_KEY='ts-...'
```

`New` reads `TYPESAFE_API_KEY` when no `WithAPIKey` option is given. Without either, it returns
`jev.ErrMissingAPIKey` and never touches the network.

## 2. Install

```sh
go get github.com/tgallice/jev-go@latest
```

The module path is `github.com/tgallice/jev-go` and the package is called `jev`, so an ordinary
import binds the name `jev`:

```go
import "github.com/tgallice/jev-go"
```

Because the last path segment (`jev-go`) differs from the package name, many editors and import
organisers write the alias explicitly. That is a readability choice, not a requirement; the examples
in these guides use it:

```go
import jev "github.com/tgallice/jev-go"
```

Production code in the SDK depends on the standard library only.

## 3. The first program

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	jev "github.com/tgallice/jev-go"
)

type Dept string

const (
	Billing   Dept = "billing"
	Technical Dept = "technical"
	Sales     Dept = "sales"
)

type Frustration int

const (
	Calm Frustration = iota
	Annoyed
	Angry
)

func main() {
	client, err := jev.New() // reads TYPESAFE_API_KEY
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ticket := map[string]any{
		"subject": "Integration down",
		"body":    "Our integration returns 500 on every request since 20 minutes. We cannot process orders.",
	}

	req := &jev.Request{State: ticket}

	dept := jev.Ask(req, "department", jev.Choice("Which team should handle `body`?", map[Dept]jev.Entry{
		Billing:   "Payments, invoicing, refunds",
		Technical: "Bugs, outages, integrations",
		Sales:     "Pricing and account questions",
	}))
	frustration := jev.Ask(req, "frustration", jev.ScoreMap("How frustrated is the customer in `body`?",
		map[Frustration]jev.Entry{
			Calm:    "Calm, just stating facts",
			Annoyed: "Frustrated but civil",
			Angry:   "Very angry, strong language",
		}))
	// WithCriteria is optional; it pins down what yes and no mean.
	urgent := jev.Ask(req, "is_urgent", jev.Noul("Does `body` convey urgency?").
		WithCriteria("Explicitly time-sensitive", "No urgency expressed"))
	// jev.Options builds an option set when the labels need no description.
	tone := jev.Ask(req, "tone", jev.Choice("What is the tone of `body`?",
		jev.Options("calm", "frustrated", "angry")))

	// The 10s default timeout is per attempt, and there can be three attempts.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	res, err := client.Evaluate(ctx, req)
	if err != nil {
		switch {
		case errors.Is(err, jev.ErrUnauthorized):
			fmt.Fprintln(os.Stderr, "check TYPESAFE_API_KEY:", err)
		case errors.Is(err, jev.ErrRateLimited), errors.Is(err, jev.ErrServer):
			fmt.Fprintln(os.Stderr, "transient, safe to try again later:", err)
		case errors.Is(err, jev.ErrInvalidRequest):
			fmt.Fprintln(os.Stderr, "bug in how the request was built:", err)
		default:
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}

	d, err := dept.Answer(res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	f, err := frustration.Answer(res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	u, err := urgent.Answer(res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tn, err := tone.Answer(res)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("model=%s request=%s input_tokens=%d\n", res.Model, res.RequestID, res.Usage.InputTokens)
	fmt.Printf("department=%s (confidence %.2f)\n", d.Choice, d.Confidence)
	fmt.Printf("frustration=%v (score %.2f)\n", f.Legend[f.Nearest()], f.Score)
	fmt.Printf("urgent=%v (p=%.2f)\n", u.Yes(0.8), u.Noul)
	fmt.Printf("tone=%s\n", tn.Choice)
}
```

It prints one line per answer, the numbers being the model's own:

```
model=jev-1.13.0 request=req_abc input_tokens=328
department=technical (confidence 0.78)
frustration=Frustrated but civil (score 1.00)
urgent=true (p=0.97)
tone=frustrated
```

`frustration` prints its legend entry and not a level number because `Nearest()` returns a
`Frustration`, a plain integer type: it is the value to switch on, and `Legend[Nearest()]` is the
value to show. The response those lines were read from is in [section 4](#4-what-the-api-sends-back).

Five points about that program:

- One `Evaluate` call carries all four questions. The model evaluates them in parallel against the
  same state. See [batching.md](batching.md).
- `jev.Ask` returns a typed handle. `dept.Answer(res)` is a `jev.ChoiceAnswer[Dept]`,
  `frustration.Answer(res)` a `jev.ScoreAnswer[Frustration]`. No type assertion at the call site.
- A question name must be unique within a request: calling `jev.Ask` twice with the same name
  silently replaces the first question, and the handle you kept then decodes the second one's answer.
- The backticks around `` `body` `` in the instructions point the model at a field of the state
  object. See [state.md](state.md).
- Descriptions are optional. `jev.Options` builds an option set whose labels carry none, and
  `WithCriteria` adds descriptions of yes and no to a Noul that has none by default.

## 4. What the API sends back

`Evaluate` posts to `POST /v1/systemone` and decodes a body of this shape:

```json
{
  "model": "jev-1.13.0",
  "answers": {
    "department": {
      "type": "choice",
      "choice": "technical",
      "confidence": 0.78,
      "probabilities": {"technical": 0.85, "billing": 0.15, "sales": 0.0}
    },
    "frustration": {
      "type": "score",
      "score": 1.0,
      "confidence": 1.0,
      "legend": {"0": "Calm, just stating facts", "1": "Frustrated but civil", "2": "Very angry, strong language"},
      "probabilities": {"0": 0.0, "1": 1.0, "2": 0.0}
    },
    "is_urgent": {"type": "noul", "noul": 0.97},
    "tone": {
      "type": "choice",
      "choice": "frustrated",
      "confidence": 0.71,
      "probabilities": {"calm": 0.08, "frustrated": 0.71, "angry": 0.21}
    }
  },
  "usage": {"input_tokens": 328, "output_tokens": 34}
}
```

| Response field | Go | Type | Notes |
| --- | --- | --- | --- |
| `model` | `Result.Model` | `string` | The versioned ID that answered, even when an alias was sent. |
| `answers.<key>` | `Key[A].Answer(res)` | depends on the question | One entry per question key. |
| `usage.input_tokens` | `Result.Usage.InputTokens` | `int` | Billed. `0` when the API omits the field. |
| `usage.output_tokens` | `Result.Usage.OutputTokens` | `int` | Free. `0` when the API omits the field. |
| `x-typesafe-request-id` header | `Result.RequestID` | `string` | `""` when absent. Quote it in support requests. |
| the HTTP status | `Result.StatusCode` | `int` | The 2xx status the answers came back on. |
| all response headers | `Result.Header` | `http.Header` | Exposed as-is; read it, do not modify it. |

Answers by kind:

| Kind | Go type | Fields and their types |
| --- | --- | --- |
| `noul` | `NoulAnswer` | `Noul float64` (probability of yes, 0 to 1). No confidence. |
| `choice` | `ChoiceAnswer[T]` | `Choice T`, `Probabilities map[T]float64`, `Confidence float64`. |
| `score` | `ScoreAnswer[L]` | `Score float64`, `Probabilities map[L]float64`, `Legend map[L]jev.Entry` (and `jev.Entry` is an alias for `any`), `Confidence float64`. |

## 5. Defaults you inherit

| Setting | Default | Override |
| --- | --- | --- |
| Base URL | `https://api.typesafe.ai` | `WithBaseURL`, `TYPESAFE_BASE_URL` |
| Model | `jev-latest` | `WithModel`, `TYPESAFE_DEFAULT_MODEL`, `Request.Model` |
| Timeout | 10s, **per attempt**, not per call | `WithTimeout`, `WithCallTimeout` |
| Retries | 2 extra attempts, exponential backoff with jitter | `WithRetryPolicy`, `WithCallRetry` |
| Logging | nothing | `WithLogger` |
| Redirects | not followed | see the warning in [configuration.md](configuration.md) |

Those two rows combine: with the defaults a single `Evaluate` can make three attempts of up to 10s
each, so give the context more than 30s. [retries-and-timeouts.md](retries-and-timeouts.md) works
out the real worst case, which is larger again when the server sends a `Retry-After`.

## Where to go next

| You want to | Read |
| --- | --- |
| Structure the content being evaluated | [state.md](state.md) |
| Choose between Noul, Choice and Score | [primitives.md](primitives.md) |
| Ask many questions in one call | [batching.md](batching.md) |
| Decide when to act on an answer | [confidence.md](confidence.md) |
| Handle failures | [errors.md](errors.md) |
| Tune retries and deadlines | [retries-and-timeouts.md](retries-and-timeouts.md) |
| Configure the client | [configuration.md](configuration.md) |
| Copy a working architecture | [patterns.md](patterns.md) |
| Use API features the SDK does not type yet | [forward-compatibility.md](forward-compatibility.md) |
| Test your own code | [testing.md](testing.md) |

## See also

Every guide above, starting with [state.md](state.md) and [primitives.md](primitives.md).
