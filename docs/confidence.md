# Confidence

For developers deciding whether to act on an answer. It separates probability from confidence, lists
the Go accessors for both, and gives a method for picking thresholds instead of guessing them.

## Two different numbers

| | What it measures | Where it lives |
| --- | --- | --- |
| Probability | How likely one specific outcome is | `ChoiceAnswer.Probabilities[label]`, `ScoreAnswer.Probabilities[level]`, `NoulAnswer.Noul` |
| Confidence | How concentrated the whole distribution is | `ChoiceAnswer.Confidence`, `ScoreAnswer.Confidence` |

A probability answers "how likely is `billing`?". Confidence answers "did the model have a clear read
at all?". They agree on clear-cut inputs and diverge on the interesting ones.

Confidence is computed by the API from the probability distribution it already returns; it is not a
separate judgment. TypeSafe describes it as a convenient default measure and returns the full
distribution precisely so you can compute your own if it suits you better.

A flat distribution means low confidence. For a Choice, that usually means no option is a clear
winner. For a Score, it usually means the levels overlap for this state, the question is measuring
more than one thing, or the state does not say enough to place it.

Two cautions the official documentation is explicit about:

- Confidence 1.0 describes the model's answer, not its correctness. All the probability landed on one
  outcome; that is all it says.
- Two different distributions can produce the same `Score`. A score of 1.0 on a three-level scale can
  be all the probability on level 1, or half on level 0 and half on level 2. Read `Probabilities`
  alongside `Score` when the difference matters.

## Choice

`dept.Answer(res)` yields a `jev.ChoiceAnswer[Dept]`:

| Member | Type | Meaning |
| --- | --- | --- |
| `Choice` | `Dept` | The option with the highest probability |
| `Probabilities` | `map[Dept]float64` | The full distribution, summing to about 1 |
| `Confidence` | `float64` | 0 to 1 |
| `Ranked()` | `[]Dept` | Options by decreasing probability, ties broken by label |

`Ranked()` is what you hand a human when the model is unsure: it turns the distribution into a
shortlist ordered the way a reviewer would want to see it.

On a near tie between two of three departments, the four members read:

| Member | Value |
| --- | --- |
| `a.Choice` | `Technical` |
| `a.Confidence` | `0.58` |
| `a.Probabilities` | `map[billing:0.42 sales:0 technical:0.58]` |
| `a.Ranked()` | `[technical billing sales]` |

`Choice` alone would hide that `Billing` was almost as likely; `Confidence` and `Ranked()` are what
tell the two apart.

```go
const autoAssign = 0.7

a, err := dept.Answer(res)
if err != nil {
	return err
}
if a.Confidence < autoAssign {
	sendToReviewWith(a.Ranked()) // best candidate first
	return nil
}
assign(a.Choice)

// A runner-up with a real share of the probability still deserves a copy.
for label, p := range a.Probabilities {
	if label != a.Choice && p > 0.25 {
		notify(label)
	}
}
return nil
```

`Probabilities` is a map, so a label the API did not return reads back as `0`. That is what you want
for a threshold test, but it means you cannot distinguish "probability zero" from "not in the
response".

## Score

`severity.Answer(res)` yields a `jev.ScoreAnswer[Severity]`:

| Member | Type | Meaning |
| --- | --- | --- |
| `Score` | `float64` | Probability-weighted mean of the level numbers; may fall between two levels |
| `Nearest()` | `Severity` | The closest level, halves rounding up, clamped to the scale |
| `LevelCount()` | `int` | Size of the scale, taken from `Legend` or `Probabilities` |
| `Legend` | `map[Severity]jev.Entry` | The level descriptions, echoed back by the API |
| `Probabilities` | `map[Severity]float64` | The distribution across levels |
| `Confidence` | `float64` | 0 to 1 |

`Score` is the number to sort and threshold on. `Nearest()` is the number to switch on.

`LevelCount()` is what makes scales of different lengths comparable. A four-level score runs 0 to 3
and a three-level score 0 to 2, so normalize before combining:

```go
func normalized[L jev.Level](a jev.ScoreAnswer[L]) float64 {
	if n := a.LevelCount(); n > 1 {
		return a.Score / float64(n-1)
	}
	return 0
}
```

`LevelCount()` returns the size of `Legend` when it is populated, otherwise the size of
`Probabilities`, and `0` when the answer carries neither. `Nearest()` clamps to the scale only when
the size is known, returns `0` for a `NaN` score, and never returns a negative level.

## Noul

A Noul has no confidence, and that is not an omission. Its distribution has two outcomes, so the
single probability describes it completely.

`urgent.Answer(res)` yields a `jev.NoulAnswer`:

| Member | Type | Meaning |
| --- | --- | --- |
| `Noul` | `float64` | Probability of yes, 0 to 1 |
| `Yes(threshold)` | `bool` | `Noul >= threshold` |

Near 1 is a strong yes, near 0 a strong no, near 0.5 means the model gives yes and no similar
probability. The middle is the equivalent of low confidence on the other two types: it is a reason to
route to a person, not a reason to pick a side.

```go
const (
	yes = 0.8
	no  = 0.2
)

u, err := urgent.Answer(res)
if err != nil {
	return err
}
switch {
case u.Noul >= yes:
	page()
case u.Noul <= no:
	backlog()
default:
	sendToReview()
}
```

A middling Noul does not mean "medium". If the underlying question is really about degree, it is the
wrong primitive; use a Score with described levels. See [primitives.md](primitives.md).

## Choosing a threshold

A threshold is not a property of the model, it is a property of the decision. Set it from the cost of
being wrong:

| Cost of a wrong action | Threshold | Behavior below it |
| --- | --- | --- |
| Cheap and reversible (showing a screen, adding a label) | low, around 0.5 to 0.6 | act anyway, or pick a safe default |
| Ordinary (routing a ticket, opening a bug) | 0.6 to 0.8 | send to a reviewer |
| Expensive or irreversible (refund, transfer, paging on-call) | 0.85 and up | ask the user to confirm, or hand to a person |
| Missing a true positive is the expensive side (safety flags) | low, and act on the positive | let false positives through to review |

The three-band shape that comes out of this is the one TypeSafe recommends:

```go
a, err := intent.Answer(res)
if err != nil {
	return err
}

const floor = 0.6

switch {
case a.Confidence < floor:
	// Genuinely unsure. Do not guess.
	routeToHuman()
case a.Choice == "check_balance":
	// Low stakes: the floor is enough.
	showBalance()
case a.Choice == "approve_transfer":
	if a.Confidence > 0.85 {
		approveTransfer()
	} else {
		// High stakes, moderate confidence: verify first.
		askUserToConfirm()
	}
default:
	routeToHuman()
}
```

One floor catches everything the model reports as uncertain. Above the floor, each action carries its
own bar. Different actions in the same system should not share a threshold.

Start conservative, measure against your own labeled data, and move the numbers. The correct values
depend on your domain and on how the model performs on your inputs; neither this SDK nor the official
documentation can supply them.

## See also

- [primitives.md](primitives.md)
- [batching.md](batching.md)
- [patterns.md](patterns.md)
- [errors.md](errors.md)
