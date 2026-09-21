# State

For anyone deciding what to put in `Request.State`. It covers the shapes the SDK accepts, how to make
a structured state addressable from the instructions, what is rejected before the network call, and
the size limits the model announces.

## What state is

State is the content you ask the model to judge: a support message, a document, a record, the current
state of your application. Every question in a request sees the same state and is evaluated against it
independently.

```go
req := &jev.Request{State: "My card was charged twice."}
```

`Request.State` has type `jev.Entry`, which is an alias for `any`. The value is marshalled with
`encoding/json` and the result must be a JSON string, object or array.

## Accepted shapes

| Go value | JSON | Use it for |
| --- | --- | --- |
| `string` | string | One message, article or passage |
| `struct` with json tags | object | Named fields whose names the model can read |
| `map[string]any`, `map[string]jev.Entry` | object | A state assembled at runtime |
| `[]T`, `[]any` | array | A sequence of messages or records |
| `json.RawMessage` | passed through | JSON you already hold as bytes |

A named struct is the shape to reach for by default. The field names travel to the model as JSON keys,
so they become a vocabulary the instructions can point at.

```go
type Ticket struct {
	Subject  string   `json:"subject"`
	Body     string   `json:"body"`
	Messages []string `json:"messages"`
}

type State struct {
	Ticket       Ticket `json:"ticket"`
	RefundPolicy string `json:"refund_policy"`
}

req := &jev.Request{State: State{
	Ticket:       Ticket{Subject: "Duplicate charge", Body: "I was charged twice for order A-104."},
	RefundPolicy: "Duplicate charges are eligible for a refund.",
}}
_ = jev.Ask(req, "refund_requested", jev.Noul("Does `ticket.body` request a refund?"))
_ = jev.Ask(req, "policy_supports", jev.Noul("Does `refund_policy` support the request in `ticket.body`?"))
```

## Naming fields so questions can point at them

TypeSafe's documentation asks you to name a part of the state inside the instructions using a
dot-and-index path wrapped in backticks: `` `ticket.messages[0]` ``, `` `refund_policy` ``,
`` `order.charges` ``. The backticks are part of the convention, not decoration: they mark the path
as a reference to the state rather than ordinary prose.

That convention is the reason to prefer a named struct or a map with descriptive keys over a bare
string. A bare string has no addressable parts, so every question must describe what it is judging in
words. With an object, the question becomes short and unambiguous:

```go
// Bare string: the question has to restate what it is looking at.
_ = jev.Noul("Does the second customer message in this transcript ask for a refund?")

// Object: the question points at a path.
_ = jev.Noul("Does `ticket.messages[1]` ask for a refund?")
```

Keep related information in one state when the judgment requires comparing its parts. A ticket, the
matching order and the refund policy belong in the same object if a question has to weigh one against
the others. Leave out everything the current questions do not need: extra context costs tokens and
gives the model more to be distracted by.

## What is rejected before any network call

`Evaluate` validates and encodes the state before it builds the HTTP request, so all of these fail
locally with an error wrapping `jev.ErrInvalidRequest` and nothing is sent:

| State value | Why it is refused |
| --- | --- |
| `42`, `3.14` | A bare number is not a string, object or array |
| `true` | A bare boolean, same reason |
| `nil` | Encodes to `null`; the SDK refuses it at the top level |
| `[]byte("...")` | `encoding/json` would base64-encode it and the model would read the encoded form |
| a value `json.Marshal` cannot encode (a channel, a func, a cycle) | marshalling fails |

Numbers and booleans are fine **inside** an object or an array. Only the top-level value is
constrained:

```go
// Refused: bare scalar.
_ = &jev.Request{State: 42}

// Accepted: the number is nested.
_ = &jev.Request{State: map[string]any{"amount_usd": 42, "status": "captured"}}
```

`[]byte` deserves its own note. `json.Marshal([]byte("hello"))` produces `"aGVsbG8="`, which is a
valid JSON string, so the request would succeed and the model would be asked to reason about base64.
The SDK refuses it instead and tells you to pass a `string`.

If you already hold JSON bytes, wrap them in `json.RawMessage`, which is not a `[]byte` as far as the
check is concerned and is emitted verbatim:

```go
raw := json.RawMessage(`{"ticket":{"body":"I was charged twice."}}`)
req := &jev.Request{State: raw}
_ = jev.Ask(req, "refund", jev.Noul("Does `ticket.body` request a refund?"))
```

## Text only

Jev evaluates natural-language text. The state must be a string, a JSON object, or an array of text
values. Images, audio and video are not supported. Pre-process non-text input into text or structured
fields before putting it in the state.

English is Jev's primary training language and where accuracy is best. Other languages, including CJK
scripts, are accepted but the official documentation reports lower accuracy; test on your own content
and pay attention to [confidence](confidence.md) when routing non-English input.

## Size limits announced by the model

From the official model page for `jev-1.13.0`:

| Limit | Value |
| --- | --- |
| Context per request | 64k tokens for the state plus all questions combined |
| Context per question | 32k tokens for the state plus the single longest question |
| Rate limits | 250,000 tokens per second, 1,200 requests per minute |

The SDK does not enforce any of these. It sends what you give it; the API answers with `422` or `429`
and the SDK surfaces that as an `*APIError`. See [errors.md](errors.md).

The state is ingested once per request and every question is evaluated against it in parallel, so the
state's token cost is paid once no matter how many questions ride along. That is the arithmetic behind
[batching.md](batching.md).

## See also

- [primitives.md](primitives.md)
- [batching.md](batching.md)
- [errors.md](errors.md)
- [forward-compatibility.md](forward-compatibility.md)
