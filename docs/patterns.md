# Patterns

For developers designing the shape of a feature around the API. It transposes the four architectural
patterns from TypeSafe's documentation into Go that compiles against this SDK.

All four share one assumption: code owns the control flow and the model answers narrow, constrained
questions inside it. Read [batching.md](batching.md) and [confidence.md](confidence.md) first.

| Pattern | What it does | Buys you |
| --- | --- | --- |
| Speculative fan-out | Send every question the code might need, ignore the irrelevant answers | Cost, latency |
| Confidence-gated routing | Use confidence as a second axis: the answer says what, confidence says whether to act | Reliability, safety |
| Composite scoring | Score several dimensions separately, weight them in code | Cost, reliability, latency |
| Intent routing | Classify first, then dispatch to the cheapest handler that can cope | Cost, latency |

## Speculative fan-out

Ask everything up front, including questions whose answers only matter for some inputs. An extra
question costs its own tokens and barely changes the response time; a second round trip costs a
round trip.

```go
type Category string

const (
	BugReport      Category = "bug_report"
	BillingIssue   Category = "billing"
	FeatureRequest Category = "feature_request"
	AccountIssue   Category = "account"
)

// The scores here are only thresholded, so a named level enum buys nothing: int is enough.
func Triage(ctx context.Context, client *jev.Client, ticket any) error {
	req := &jev.Request{State: ticket}

	category := jev.Ask(req, "category", jev.Choice("What is the broad category of this ticket?",
		map[Category]jev.Entry{
			BugReport:      "The user reports something broken or producing errors",
			BillingIssue:   "Charges, invoices, refunds, subscriptions",
			FeatureRequest: "The user requests new functionality",
			AccountIssue:   "Login, permissions, profile, security",
		}))
	// Speculative: read only when the category is bug_report.
	severity := jev.Ask(req, "bug_severity", jev.Score[int]("How severe is the reported issue?",
		"Cosmetic; no impact to functionality",
		"Broken or degraded feature; workaround exists",
		"Blocking issue; no workaround exists"))
	repro := jev.Ask(req, "has_reproducible_steps",
		jev.Noul("The user describes specific steps to reproduce the issue"))
	// Speculative: read only for billing.
	refund := jev.Ask(req, "refund_requested",
		jev.Noul("The user is explicitly asking for a refund or credit"))
	// Read whatever the category turns out to be.
	frustration := jev.Ask(req, "frustration", jev.Score[int]("How frustrated does the user appear?",
		"Calm, matter-of-fact", "Frustrated but civil", "Very angry"))

	res, err := client.Evaluate(ctx, req)
	if err != nil {
		return err
	}
	cat, err := category.Answer(res)
	if err != nil {
		return err
	}

	switch cat.Choice {
	case BugReport:
		sev, err := severity.Answer(res)
		if err != nil {
			return err
		}
		steps, err := repro.Answer(res)
		if err != nil {
			return err
		}
		if sev.Score > 1.5 && steps.Yes(0.6) {
			escalateToEngineering()
		} else {
			addToBacklog()
		}
	case BillingIssue:
		wantsRefund, err := refund.Answer(res)
		if err != nil {
			return err
		}
		routeToBilling(wantsRefund.Yes(0.7))
	case FeatureRequest:
		logFeatureRequest()
	}

	f, err := frustration.Answer(res)
	if err != nil {
		return err
	}
	if f.Score > 1.5 {
		flagForPriorityResponse()
	}
	return nil
}
```

Five answers, one call, ordinary `switch` statements. A sixth question later does not add a request.

## Confidence-gated routing

The answer tells you what; confidence tells you whether to act on it. Give each action its own bar,
sized by the cost of being wrong.

```go
type Action string

const (
	CheckBalance    Action = "check_balance"
	ApproveTransfer Action = "approve_transfer"
	Other     Action = "other"
)

func HandleCommand(ctx context.Context, client *jev.Client, command string, accountID string) error {
	req := &jev.Request{State: command}
	action := jev.Ask(req, "intent", jev.Choice("What action is the user requesting?",
		map[Action]jev.Entry{
			CheckBalance:    "Check the balance of an account",
			ApproveTransfer: "Approve the pending transfer request",
			Other:     "Something else",
		}))

	res, err := client.Evaluate(ctx, req)
	if err != nil {
		return err
	}
	a, err := action.Answer(res)
	if err != nil {
		return err
	}

	const floor = 0.6

	switch {
	case a.Confidence < floor:
		// Genuinely uncertain about any action: a person decides.
		routeToSupportAgent(accountID)
	case a.Choice == CheckBalance:
		// Low stakes: the worst case is showing the wrong screen.
		showBalanceFor(accountID)
	case a.Choice == ApproveTransfer && a.Confidence > 0.85:
		approveTransferFor(accountID)
	case a.Choice == ApproveTransfer:
		// High stakes, moderate confidence: verify intent first.
		askToConfirm(accountID)
	default:
		routeToSupportAgent(accountID)
	}
	return nil
}
```

The floor and the per-action bar do different jobs. Removing the floor lets a coin-flip answer drive
a cheap action; removing the per-action bar lets a 0.61 answer move money.

## Composite scoring

Break a complex judgment into independent dimensions, score each one, and combine them with weights
that live in your code. When the ranking disagrees with your team, change a coefficient rather than
rewriting a prompt.

```go
type Level int

type Dimension struct {
	Key          string
	Instructions string
	Levels       []jev.Entry
}

var dimensions = []Dimension{
	{"python_depth", "How much depth of Python experience does this candidate have?", []jev.Entry{
		"No Python experience mentioned", "Mentioned but no detail",
		"Used in projects, some specifics", "Primary language, multiple projects",
		"Deep expertise: architecture, performance, libraries"}},
	{"team_leadership", "How much experience does this candidate have leading engineering teams?", []jev.Entry{
		"No management experience mentioned", "Informal mentorship or tech lead role",
		"Led a small team or project", "Managed a team with direct reports",
		"Managed multiple teams or an engineering org"}},
	{"system_design", "How much experience does this candidate have designing large-scale systems?", []jev.Entry{
		"No architecture work mentioned", "Contributed to design discussions",
		"Designed components of a larger system", "Owned architecture of a significant system",
		"Designed systems at scale across multiple domains"}},
	{"generalist", "How much evidence is there that this candidate picks up unfamiliar domains?", []jev.Entry{
		"Only one domain or role mentioned", "Some variety but within a narrow field",
		"Worked across a few different areas or stacks", "Regularly moved between domains",
		"Track record of ramping up in unfamiliar areas and delivering"}},
}

// Weights per role, keyed by dimension. They sum to 1 so the result stays on 0 to 1.
var roles = map[string]map[string]float64{
	"senior_ic": {"python_depth": 0.40, "team_leadership": 0.10, "system_design": 0.40, "generalist": 0.10},
	"manager":   {"python_depth": 0.15, "team_leadership": 0.40, "system_design": 0.20, "generalist": 0.25},
}

func ScoreResume(ctx context.Context, client *jev.Client, resume any) (map[string]float64, error) {
	req := &jev.Request{State: resume, Questions: map[string]jev.Question{}}
	for _, d := range dimensions {
		req.Questions[d.Key] = jev.Score[Level](d.Instructions, d.Levels...)
	}

	res, err := client.Evaluate(ctx, req)
	if err != nil {
		return nil, err
	}

	// Normalize every dimension to 0..1 so the weights mean what they say.
	norm := make(map[string]float64, len(dimensions))
	for _, d := range dimensions {
		a, err := jev.ScoreOf[Level](res, d.Key)
		if err != nil {
			return nil, err
		}
		if n := a.LevelCount(); n > 1 {
			norm[d.Key] = a.Score / float64(n-1)
		}
	}

	out := make(map[string]float64, len(roles))
	for role, weights := range roles {
		for key, w := range weights {
			out[role] += w * norm[key]
		}
	}
	return out, nil
}
```

Normalizing by `LevelCount()-1` is what makes the weights mean what they say: without it a five-level
score contributes up to 4 and a three-level score up to 2, and the weights stop being proportions.
Keep every dimension to one thing; a level reading "punctual and smart and experienced" cannot be
placed. Because the questions come from a slice, the reading side uses `ScoreOf` rather than handles;
see [batching.md](batching.md).

## Intent routing

Classify first with a fast, cheap call, then dispatch to the cheapest handler that can cope:
deterministic code, a specialist LLM, or a person.

```go
type Intent string

const (
	OrderStatus     Intent = "order_status"
	ProductQuestion Intent = "product_question"
	ReturnExchange  Intent = "return_exchange"
	Complaint       Intent = "complaint"
)

func Route(ctx context.Context, client *jev.Client, message string, ticketID string) error {
	req := &jev.Request{State: message}
	intent := jev.Ask(req, "intent", jev.Choice("What is the primary intent of this message?",
		map[Intent]jev.Entry{
			OrderStatus:     "Asking about an existing order",
			ProductQuestion: "Asking about a product before buying",
			ReturnExchange:  "Wants to return or exchange something",
			Complaint:       "Unhappy with the experience, wants resolution",
		}))
	complexity := jev.Ask(req, "complexity", jev.Score[int]("How complex is this request to resolve?",
		"Simple lookup or standard procedure",
		"Requires some judgment or a multi-step process",
		"Unusual situation, edge case, or escalation needed"))

	res, err := client.Evaluate(ctx, req)
	if err != nil {
		return err
	}
	i, err := intent.Answer(res)
	if err != nil {
		return err
	}
	if i.Confidence < 0.5 {
		return toHumanAgent(ticketID)
	}

	switch i.Choice {
	case OrderStatus:
		return lookupOrder(ticketID) // no model involved at all
	case ProductQuestion:
		return withLLM(ticketID, "product-specialist")
	case ReturnExchange:
		return withLLM(ticketID, "returns-specialist")
	case Complaint:
		c, err := complexity.Answer(res)
		if err != nil {
			return err
		}
		// Too complex to automate safely, or we are not sure how complex it is.
		if c.Score > 1 || c.Confidence < 0.5 {
			return toHumanAgent(ticketID)
		}
		return withLLM(ticketID, "complaint-resolution")
	}
	return toHumanAgent(ticketID)
}
```

The confidence check on `complexity` is not redundant with the one on `intent`. A confident intent
with an unreadable complexity is exactly the case where automating is risky.

## Combining them

These compose. A realistic triage path is one fan-out call whose answers feed a composite score,
gated on confidence before anything irreversible happens, with the intent choosing the handler. That
is still a single HTTP request.

## See also

- [batching.md](batching.md)
- [confidence.md](confidence.md)
- [primitives.md](primitives.md)
- [state.md](state.md)
