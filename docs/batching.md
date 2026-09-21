# Batching

For developers who have one working question and are about to write a second `Evaluate` call. It
explains why that second call is almost always the wrong move, how `Ask` keeps a heterogeneous batch
type-safe, and how to read answers when the questions were built at runtime.

## One call, many questions

A request carries one state and a map of named questions. The model ingests the state once and
evaluates every question against it in parallel and in isolation. Adding a question costs the tokens
of that question and barely changes the response time.

| | N separate calls | One batch of N |
| --- | --- | --- |
| State tokens billed | N times | once |
| Round trips | N | 1 |
| Latency | sum of N calls | one call, questions evaluated in parallel |
| Retry surface | N independent failure points | 1 |
| Rate limit consumption | N requests | 1 request |

TypeSafe's parallel-questions cookbook measures this: 13 questions about the GDPR Wikipedia article,
batched into one call, come out 12.2x cheaper and 10.0x faster than the same 13 questions sent one
per call, with no change in the answers.

The practical consequence: ask every question your code *might* need, including the ones that matter
only for some inputs, and let the code ignore the rest. That is the
[speculative fan-out](patterns.md) pattern.

## `Ask` and `Key[A].Answer`

`Ask` registers a question under a name and returns a typed handle:

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

req := &jev.Request{State: ticket}

dept := jev.Ask(req, "department", jev.Choice("Which team should handle `body`?", map[Dept]jev.Entry{
	Billing:   "Payments, invoicing, refunds",
	Technical: "Bugs, outages, integrations",
}))
severity := jev.Ask(req, "severity", jev.Score[Severity]("How severe is `body`?",
	"Cosmetic", "Degraded, workaround exists", "Blocking"))
urgent := jev.Ask(req, "is_urgent", jev.Noul("Does `body` convey urgency?"))

res, err := client.Evaluate(ctx, req)
if err != nil {
	return err
}

d, err := dept.Answer(res)     // jev.ChoiceAnswer[Dept]
if err != nil {
	return err
}
s, err := severity.Answer(res) // jev.ScoreAnswer[Severity]
if err != nil {
	return err
}
u, err := urgent.Answer(res)   // jev.NoulAnswer
if err != nil {
	return err
}
fmt.Println(d.Choice, s.Legend[s.Nearest()], u.Yes(0.8))
// technical Blocking true
```

Three differently typed answers come back from one call with no type assertion anywhere: the handle
carries the answer type, so `dept.Answer` can only produce a `ChoiceAnswer[Dept]`.

| Signature | Notes |
| --- | --- |
| `Ask[A any](req *Request, name string, q TypedQuestion[A]) Key[A]` | `A` is inferred from `q` |
| `(Key[A]) Answer(r *Result) (A, error)` | decodes that question's answer |
| `(Key[A]) Name() string` | the question key, never shown to the model |

Rules worth knowing:

- **`req` must not be nil.** `Ask` writes into `req.Questions`, allocating the map when it is nil. A
  nil `*Request` panics.
- **A duplicate name replaces the previous question.** The earlier handle then decodes the new
  question's answer, which will fail with `ErrAnswerKind` if the kinds differ.
- **`q` must be a concrete question type.** `A` is inferred from `NoulQuestion`, `ChoiceQuestion[T]`
  or `ScoreQuestion[L]`. A value stored in a `jev.Question` interface variable has lost that
  information and will not compile with `Ask`.
- **Question names are yours.** They identify the answer in the result and are never sent to the
  model. Write the whole question in the instructions even when the name looks self-explanatory.

## Type inference

| Constructor | Type parameter | How it is determined |
| --- | --- | --- |
| `jev.Noul(instructions)` | none | `NoulAnswer` |
| `jev.Choice(instructions, map[T]jev.Entry{...})` | `T ~string` | inferred from the options map |
| `jev.Options[T](labels...)` | `T ~string` | explicit when the labels are untyped constants |
| `jev.Score[L](instructions, levels...)` | `L` integer | must be written explicitly |
| `jev.ScoreMap(instructions, map[L]jev.Entry{...})` | `L` integer | inferred from the map keys |

Choice is parameterized on `~string`, so your own named string type works directly and the answer
comes back in that type:

```go
type Intent string

const (
	OrderStatus Intent = "order_status"
	Complaint   Intent = "complaint"
)

intent := jev.Ask(req, "intent", jev.Choice("What is the customer asking for?", map[Intent]jev.Entry{
	OrderStatus: "Asking about an existing order",
	Complaint:   "Unhappy with the experience, wants resolution",
}))

a, err := intent.Answer(res)
if err != nil {
	return err
}
switch a.Choice { // a.Choice is an Intent, not a string
case OrderStatus:
	handleOrderStatus()
case Complaint:
	handleComplaint()
}
```

Score is parameterized on any integer type, so an `iota` enum starting at 0 lines up with the wire
level numbers:

```go
type Complexity int

const (
	Simple Complexity = iota
	Judgment
	EdgeCase
)

complexity := jev.Ask(req, "complexity", jev.ScoreMap("How complex is this request to resolve?",
	map[Complexity]jev.Entry{
		Simple:   "Simple lookup or standard procedure",
		Judgment: "Requires some judgment or a multi-step process",
		EdgeCase: "Unusual situation, edge case, or escalation needed",
	}))

c, err := complexity.Answer(res)
if err != nil {
	return err
}
if c.Nearest() >= Judgment { // comparison on your own enum
	escalateToHuman()
}
```

When `jev.Options` is given untyped string constants it cannot infer your named type, so spell it
out: `jev.Options[Dept]("billing", "technical")`. With typed constants, `jev.Options(Billing,
Technical)` infers `Dept`.

## Dynamic accessors

`Ask` requires a handle to be in scope at the point where you read the answer. When it is not, read
by key with one of the free functions:

| Function | Returns |
| --- | --- |
| `jev.NoulOf(r *Result, key string)` | `NoulAnswer` |
| `jev.ChoiceOf[T ~string](r *Result, key string)` | `ChoiceAnswer[T]` |
| `jev.ScoreOf[L Level](r *Result, key string)` | `ScoreAnswer[L]` |

All three return `ErrAnswerMissing` when the key is absent and `ErrAnswerKind` when the answer is not
of the requested kind, so a mistake surfaces as an error rather than a wrong value.

Three situations call for them.

**Questions built from configuration.** The set of questions is not known at compile time, so there
is no place to keep a handle:

```go
type Rubric struct {
	Key          string
	Instructions string
	Levels       []jev.Entry
}

type Grade int

func score(client *jev.Client, ctx context.Context, state any, rubrics []Rubric) (map[string]float64, error) {
	req := &jev.Request{State: state, Questions: map[string]jev.Question{}}
	for _, r := range rubrics {
		req.Questions[r.Key] = jev.Score[Grade](r.Instructions, r.Levels...)
	}
	res, err := client.Evaluate(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(rubrics))
	for _, r := range rubrics {
		a, err := jev.ScoreOf[Grade](res, r.Key)
		if err != nil {
			return nil, err
		}
		out[r.Key] = a.Score / float64(a.LevelCount()-1)
	}
	return out, nil
}
```

Note that `Questions` is a plain map: `Ask` is a convenience over it, not the only way in.

**A `*Result` that crosses a boundary.** When the call site that builds the request and the one that
reads the answers are in different packages, the handles cannot travel with the result. Pass the
`*Result` and read by key, with the key as an exported constant so both sides agree:

```go
const KeyDepartment = "department"

func Route(res *jev.Result) error {
	dept, err := jev.ChoiceOf[Dept](res, KeyDepartment)
	if err != nil {
		return err
	}
	fmt.Println(dept.Choice)
	return nil
}
```

**Opportunistic reads.** When you want an answer only if it is there, check first:

```go
if kind, ok := res.Kind("severity"); ok && kind == jev.KindScore {
	sev, err := jev.ScoreOf[Severity](res, "severity")
	if err != nil {
		return err
	}
	fmt.Println(sev.Score, sev.Legend[sev.Nearest()]) // 1.8 Blocking
}
```

`Result.Keys()` lists the answered keys, sorted, and `Result.Kind(key)` reports an answer's type
including types this SDK does not decode. See [forward-compatibility.md](forward-compatibility.md).

## Concurrency

A `*Client` is immutable after `New` and safe for concurrent use: create one per process and share
it. A `*Result` is read-only once returned and safe for concurrent reads, so fanning out over its
answers needs no lock; `RawAnswer` and `Header` hand out internal state, so treat both as read-only.
A `*Request` is a plain struct with a map inside, so building one from several goroutines is your
own synchronization problem.

## When a second call is genuinely needed

Questions in one request are independent, so an answer cannot feed another question in the same
request. Make a second call only when your code cannot build the second request until it has the
first answer: it needs the answer to fetch more data for the state, to decide what the state is made
of, or to choose the next question's options (walking a taxonomy level by level, for instance).

If the second request's questions could have been asked against the original state, they belong in
the first request.

## See also

- [primitives.md](primitives.md)
- [confidence.md](confidence.md)
- [patterns.md](patterns.md)
- [forward-compatibility.md](forward-compatibility.md)
