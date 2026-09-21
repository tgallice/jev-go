# Primitives

For developers choosing the shape of a question. It describes the three question types the API
exposes, the Go constructors for each, the bounds the SDK validates locally, and the traps that make
an answer hard to read.

## The three types

| Type | Question it answers | Answer fields | Go constructor |
| --- | --- | --- | --- |
| Noul | Is this true? | `Noul` (0 to 1) | `jev.Noul(instructions)` |
| Choice | Which of these options? | `Choice`, `Probabilities`, `Confidence` | `jev.Choice(instructions, options)` |
| Score | Which level on this scale? | `Score`, `Probabilities`, `Legend`, `Confidence` | `jev.Score[L](instructions, levels...)`, `jev.ScoreMap(instructions, levels)` |

All three can be mixed in one request. Every question is evaluated independently against the same
state, so one answer never becomes hidden context for another.

## Decision table

| What you need | Primitive | Why |
| --- | --- | --- |
| A boolean your code branches on | Noul | The probability of yes is the whole answer |
| One of N unordered categories | Choice | Maps straight onto N code paths |
| A position on a spectrum you can describe in steps | Score | The levels are yours; the answer lands on or between them |
| A ranking key | Score | `Score` is a float and sorts |
| A checklist of independent conditions | Several Nouls | One condition per question, combined in code |
| A degree, phrased as yes/no | Score, not Noul | A Noul of 0.5 means "equally likely yes or no", not "medium" |
| Categories with an order that matters | Score if you can describe each step, else Choice | |
| Something the option list may not cover | Choice with an explicit `other` option | The model can say none of the others fit |

A judgment weighing several independent factors is not one question: ask one per factor and combine
the answers in code, as in [patterns.md](patterns.md).

## Noul

A Noul returns one number: the probability of yes. There is no confidence, because a two-outcome
distribution is fully described by a single probability.

```go
req := &jev.Request{State: "I have asked three times now. Can I please just talk to a real person?"}

escalate := jev.Ask(req, "is_human_escalation", jev.Noul("Is the customer asking for a human agent?"))

repeat := jev.Ask(req, "is_repeat_contact",
	jev.Noul("Has the customer contacted support about this before?").
		WithCriteria(
			"Mentions a prior attempt, ticket, or that they have asked before",
			"No sign of any previous contact",
		))
_, _ = escalate, repeat
```

| Wire field | Go |
| --- | --- |
| `type: "noul"` | set by the SDK |
| `instructions` | `NoulQuestion.Instructions`, first argument of `jev.Noul` |
| `criteria.true` / `criteria.false` | `WithCriteria(yes, no)`, optional |

Reading it:

```go
a, err := escalate.Answer(res)
if err != nil {
	return err
}
switch {
case a.Yes(0.8):
	routeToAgent()
case a.Noul < 0.2:
	routeToBot()
default:
	sendToReview() // the model is not sure either way
}
```

`Yes(threshold)` is `a.Noul >= threshold`. Where the threshold sits depends on the cost of being
wrong: raise it when acting on a false yes is expensive, lower it when missing a true yes is.

Traps:

- **Two conditions in one question.** "Is the customer angry and asking for a refund?" makes the model
  judge both at once and the number stops meaning anything. Ask two Nouls.
- **Inverted phrasing.** Phrase it so a high value means yes; "Is the message free of personal data?"
  reads backwards at the call site.
- **A degree dressed up as a boolean.** "Is this candidate strong in Python?" has no crisp boundary.
  Use a Score, or sharpen it: "Does the resume state that the candidate used Python at work?".

## Choice

A Choice picks one option from a labeled set and returns the full distribution over the set.

```go
type Dept string

const (
	Returns  Dept = "returns"
	Shipping Dept = "shipping"
	Billing  Dept = "billing"
)

req := &jev.Request{State: "My running shoes arrived in the wrong size. Can I swap them for a size 10?"}

dept := jev.Ask(req, "department", jev.Choice("Which team should handle this?", map[Dept]jev.Entry{
	Returns:  "Exchanges, wrong or damaged items",
	Shipping: "Delivery status, delays, lost packages",
	Billing:  "Charges, invoices, payment problems",
}))
_ = dept
```

`T` is inferred from the options map, so `dept` is a `jev.Key[jev.ChoiceAnswer[Dept]]`. When the
labels need no description, `jev.Options` builds the map for you:

```go
tone := jev.Ask(req, "tone", jev.Choice("What is the customer's tone?",
	jev.Options("calm", "frustrated", "angry")))
_ = tone // jev.Key[jev.ChoiceAnswer[string]]
```

| Wire field | Go | Bounds |
| --- | --- | --- |
| `criteria` | `ChoiceQuestion[T].Options`, a `map[T]jev.Entry` | 1 to 255 options, checked locally |
| option key | the map key | must not be empty |
| option value | the description | `nil` means an option with no description; sent as JSON `null` |

Reading it:

`dept.Answer(res)` gives a `jev.ChoiceAnswer[Dept]`: `Choice` (the highest-probability option),
`Probabilities` (a `map[Dept]float64`, `0` for a label the API did not list), `Confidence`, and
`Ranked()` (the options by decreasing probability, ties broken by label). See
[confidence.md](confidence.md).

Traps:

- **A short option list.** Options cost a few tokens each. Give the model every team, category or
  product rather than a shortlist.
- **No escape hatch.** Add an `other` option when the list might not cover every input, otherwise the
  probability is forced onto an option that does not fit.
- **Options that overlap.** When two keep getting confused, describe each with an object saying what
  it covers and what it does not; see [Structured descriptions](#structured-descriptions).
- **`Probabilities` is a map.** A label the API did not return reads back as `0`, not as "missing".
- **Option ordering.** `criteria` is a JSON object built from a Go map, so there is no order to carry
  meaning in the first place, and `encoding/json` sorts the keys on the wire.

## Score

A Score rates the state on an ordered scale. Levels are described, lowest first, and are numbered by
their position starting at 0. The answer is a probability-weighted mean and can fall between two
levels.

Two constructors. `Score` takes the levels positionally and needs the level type spelled out:

```go
type Severity int

const (
	Cosmetic Severity = iota
	Degraded
	Blocking
)

req := &jev.Request{State: "The export button crashes the settings page in Safari."}

severity := jev.Ask(req, "severity", jev.Score[Severity](
	"How severe is the reported issue?",
	"Cosmetic; no impact to functionality",
	"Broken or degraded feature, but workaround exists",
	"Blocking issue; no workaround exists",
))
_ = severity
```

`ScoreMap` takes an enum-keyed map and infers the level type from it. The keys must be exactly
`0..len-1`; a gap or an out-of-range key is reported as an `ErrInvalidRequest` when the request is
marshalled, not by the constructor:

```go
sev := jev.Ask(req, "severity", jev.ScoreMap("How severe is the reported issue?",
	map[Severity]jev.Entry{
		Cosmetic: "Cosmetic; no impact to functionality",
		Degraded: "Broken or degraded feature, but workaround exists",
		Blocking: "Blocking issue; no workaround exists",
	}))
_ = sev
```

`ScoreMap` is the safer of the two: the enum constant sits next to its description, so inserting a
level cannot silently shift the meaning of the others.

| Wire field | Go | Bounds |
| --- | --- | --- |
| `criteria` | `ScoreQuestion[L].Levels`, an ordered `[]jev.Entry` | 2 to 10 levels, checked locally |
| level number | index in the slice, or the map key | starts at 0 |
| `legend` in the answer | `ScoreAnswer[L].Legend` | the level descriptions, echoed back by level |

`L` may be any integer type (`jev.Level` covers all signed and unsigned integer kinds). An `iota`
enum starting at 0 lines up exactly with the wire numbering.

Reading it:

`severity.Answer(res)` gives a `jev.ScoreAnswer[Severity]`: `Score` (a float that may fall between
two levels, 1.43 for instance), `Nearest()` (the closest level, halves up, clamped to the scale),
`Legend` and `Probabilities` keyed by `Severity`, `LevelCount()`, and `Confidence`. See
[confidence.md](confidence.md).

Traps:

- **Degrees instead of situations.** "Moderately severe" gives the model nothing to match. Describe a
  situation per level.
- **Numbers as levels.** Each level is judged on its own; the model never sees a level's number or
  its neighbours, so `"0"`, `"1"`, `"2"` perform badly.
- **More than one dimension.** "Punctual and smart and experienced" measures three things and nothing
  can be placed on it. Split into one Score per dimension.
- **Same score, different distribution.** A score of 1.0 can be all the probability on level 1, or
  half on level 0 and half on level 2. Read `Probabilities` and `Confidence` alongside it.
- **Scales of different lengths.** Divide by `LevelCount()-1` before combining; see
  [patterns.md](patterns.md).

## Structured descriptions

`instructions` and every criteria description accept a JSON object or array, not only a string. This
is the API's `EntryType`, and in Go it is `jev.Entry`, an alias for `any`.

| Field | Applies to | Accepted |
| --- | --- | --- |
| `instructions` | Noul, Choice, Score | string, object, array, or omitted when `nil` (see the note below) |
| option description | Choice | string, object, array, or `null` |
| level description | Score | string, object, array, or `null` |
| `criteria.true` / `criteria.false` | Noul | string, object, array |

Structured instructions put the question in one field and the data it refers to in others:

```go
type Duplicate struct {
	Name         string `json:"name"`
	Location     string `json:"location"`
	LastEmployer string `json:"last_employer"`
}

q := jev.Noul(map[string]any{
	"potential_duplicate": Duplicate{Name: "Jon Smith", Location: "Oakland, CA", LastEmployer: "Google"},
	"question":            "Is the resume for the same person as `potential_duplicate`?",
})
_ = q
```

Structured option descriptions sharpen the boundary between similar options:

```go
q := jev.Choice("Which returns topic is the customer asking about?", map[string]jev.Entry{
	"return_policy": map[string]any{
		"what":    "Whether and how an item can be returned",
		"not_for": "Progress of a return already sent",
	},
	"return_status": map[string]any{
		"what":    "Progress of a return already sent",
		"not_for": "Whether and how an item can be returned",
	},
})
_ = q
```

The field names (`question`, `focus`, `what`, `not_for`, `examples`) are not part of the API and none
are reserved. You choose them the way you choose option names; the model sees the names alongside the
values, so keep them short and descriptive.

Omitting `instructions` is an open question. The SDK sends no field when it is `nil`, and the
official Python and JavaScript SDKs accept a missing or null value, but the API reference marks
`instructions` as required and only the advanced-structure page shows `null` as an accepted shape.
If the API rejects a question without instructions, that is why. Supply them unless you are
deliberately testing the boundary.

Start with plain strings. Reach for structure when two options or two neighboring levels keep getting
confused on inputs you consider clear. The official docs warn that examples only help when they
resemble your real inputs, and that higher confidence alone does not prove a description is better.
Bare numbers and booleans are refused here for the same reason as in the state: wrap them in an
object.

## Local validation

These are checked when the request is marshalled, before any network call, and return an error
wrapping `jev.ErrInvalidRequest`:

| Rule | Error text |
| --- | --- |
| At least one question | `at least one question is required` |
| Non-empty question key | `question key must not be empty` |
| 1 to 255 choice options | `choice needs 1 to 255 options, got N` |
| Non-empty choice option label | `choice option label must not be empty` |
| 2 to 10 score levels | `score needs 2 to 10 levels, got N` |
| `ScoreMap` keys contiguous from 0 | `score levels must be keyed 0..N, got M` |
| Entries are string, object or array | `entry must be a string, object or array, got ...` |

Everything else is left to the server's `422`; the SDK does not reproduce undocumented validation.

## See also

- [state.md](state.md)
- [batching.md](batching.md)
- [confidence.md](confidence.md)
- [patterns.md](patterns.md)
