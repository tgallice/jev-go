# Forward compatibility

For developers who need an API feature this SDK does not type yet. It covers the three escape
hatches, when each one is the right answer, and how to retire it once the SDK catches up.

## Why they exist

The API ships on its own schedule. A new question type, a new top-level request field or a new answer
shape can appear before a release of this SDK types it. Without escape hatches, that means either
waiting or abandoning the SDK; with them, the untyped feature costs a little ceremony and the rest of
your code stays typed.

| Hatch | Direction | Replaces |
| --- | --- | --- |
| `RawQuestion` | request | A question type the SDK does not know |
| `Request.Extra` | request | A top-level request field the SDK does not know |
| `Result.Kind`, `RawAnswer`, `RawBody` | response | An answer shape the SDK does not decode |

All three are deliberate parallels of the `extra_body` and raw-response facilities in the official
Python and JavaScript SDKs.

## `RawQuestion`

`RawQuestion` is a `json.RawMessage` that is forwarded into the `questions` map untouched:

```go
req := &jev.Request{
	State: ticket,
	Questions: map[string]jev.Question{
		"multi_label": jev.RawQuestion(`{
			"type": "multichoice",
			"instructions": "Which topics does this ticket mention?",
			"criteria": {"billing": null, "shipping": null, "returns": null}
		}`),
	},
}

res, err := client.Evaluate(ctx, req)
if err != nil {
	return err
}

raw, ok := res.RawAnswer("multi_label")
if !ok {
	return fmt.Errorf("no answer for multi_label")
}
var answer struct {
	Type   string   `json:"type"`
	Labels []string `json:"labels"`
}
if err := json.Unmarshal(raw, &answer); err != nil {
	return err
}
fmt.Println(answer.Labels)
// [billing shipping]
```

Rules:

- The SDK validates only that the value is a JSON object carrying a non-empty string `type` field.
  Anything else is an `ErrInvalidRequest`. Everything past that is the server's business.
- A `RawQuestion` cannot be registered with `Ask`, because there is no answer type to infer. Put it
  in `Request.Questions` directly.
- Its answer is reachable only through `Result.RawAnswer`.
- Typed and raw questions mix freely in the same request. `Ask` allocates `Questions` when it is nil,
  and writing to the map yourself works equally well.

`RawQuestion` is also useful for a question shape you generate elsewhere, for instance one stored in
a configuration file as JSON, even when the SDK does support its type.

## `Request.Extra`

`Extra` is merged into the top level of the request body, after `state`, `questions` and `model`:

```go
req := &jev.Request{
	State: ticket,
	Extra: map[string]any{
		"experimental_flag": true,
		"tenant":            "acme",
	},
}
_ = jev.Ask(req, "is_urgent", jev.Noul("Does the ticket convey urgency?"))
```

Rules:

- `state` and `questions` are refused as keys with `ErrInvalidRequest`: overwriting them would
  silently corrupt the request.
- `model` is not refused, so an `Extra["model"]` overrides both `Request.Model` and the client
  default. That works, but `Request.Model` says what you mean.
- Values are marshalled with `encoding/json` and may be any JSON type, including bare numbers and
  booleans. The string/object/array restriction applies to `Entry` positions, not here.

## Reading unknown answers

`Evaluate` never fails on an answer whose `type` it does not recognize. It records the kind, keeps
the raw bytes, and logs a `Warn` line when a logger is installed. A missing or empty `type` is still
an `ErrInvalidResponse`: without a discriminant the response cannot be interpreted at all.

| Method | Returns | Use |
| --- | --- | --- |
| `Keys() []string` | Answered keys, sorted | Enumerate what came back |
| `Kind(key) (Kind, bool)` | The `type` field, including unknown ones | Branch before decoding |
| `RawAnswer(key) (json.RawMessage, bool)` | The answer as sent | Decode it yourself |
| `RawBody() []byte` | A copy of the whole response body | Logging, golden-file tests, debugging |

```go
for _, key := range res.Keys() {
	kind, _ := res.Kind(key)
	switch kind {
	case jev.KindNoul:
		a, err := jev.NoulOf(res, key)
		if err != nil {
			return err
		}
		fmt.Println(key, a.Noul)
	case jev.KindChoice, jev.KindScore:
		// handled by their typed keys elsewhere
	default:
		raw, _ := res.RawAnswer(key)
		fmt.Printf("%s: unsupported kind %q: %s\n", key, kind, raw)
	}
}
```

On a response carrying one noul and one answer of a type this SDK does not know, that loop prints:

```
is_urgent 0.93
multi_label: unsupported kind "multichoice": {"type":"multichoice","labels":["billing","shipping"]}
```

`Keys()` sorts, so the order is the keys', not the response's.

`RawAnswer` hands out the SDK's own slice: read it, do not modify it. `RawBody` returns a copy, so
it is safe to keep and mutate, at the cost of an allocation the size of the response.

## Migrating back to the typed API

When a release of the SDK types the feature you were forwarding by hand, the migration is mechanical.

| From | To |
| --- | --- |
| `jev.RawQuestion(...)` in `Questions` | the new constructor, registered with `jev.Ask` |
| `res.RawAnswer(key)` plus a local struct | `key.Answer(res)` or the new `XOf[T]` accessor |
| `Extra["some_field"]` | the new field on `Request` |

Two habits keep that migration small. Keep the question key in a constant, so the request side and
the reading side are already decoupled. Keep the hand-rolled decode in one function per question, so
switching it to the typed accessor is a one-function change and its tests carry over unchanged.

If the feature turns out to be something the SDK will not type, the escape hatch is a supported
permanent answer, not a stopgap. The official SDKs treat theirs the same way.

## See also

- [batching.md](batching.md)
- [primitives.md](primitives.md)
- [errors.md](errors.md)
- [testing.md](testing.md)
