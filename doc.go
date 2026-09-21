// Package jev is a Go client for the TypeSafe.ai System One API (the jev model
// family).
//
// # System One models
//
// A System One model answers closed questions instead of writing text. Every
// answer is a value the caller declared up front, a yes/no probability, one
// label out of a set, or a level on a scale, and it comes with calibrated
// probabilities: there is no prose to parse and no output format to enforce.
// Output tokens are billed at zero, so only the state and the questions are
// paid for, which makes asking a second question about the same state nearly
// free.
//
// # Primitives
//
// Three question shapes cover the API. Noul asks a yes/no question and returns
// the probability of yes; use it for a single binary predicate. Choice picks
// one label out of 1 to 255 options and returns a probability per label; use it
// for routing, classification and tagging. Score rates the state on an ordered
// scale of 2 to 10 levels and returns a probability-weighted mean; use it when
// the answers are ranked, such as severity, quality or risk. A question type
// this SDK does not know yet can still be sent with RawQuestion.
//
// # Typed heterogeneous batches
//
// One call carries one state and any mix of question shapes, each under a key
// of the caller's choosing. Ask registers a question and returns a Key typed by
// the answer it decodes to, so reading an answer needs no type assertion:
//
//	type Dept string
//	type Severity int
//
//	const (
//		Cosmetic Severity = iota
//		Degraded
//		Blocking
//	)
//
//	req := &jev.Request{State: ticket}
//	dept := jev.Ask(req, "department", jev.Choice("Which team should handle it?",
//		map[Dept]jev.Entry{"billing": "Payments and refunds", "technical": "Bugs and outages"}))
//	severity := jev.Ask(req, "severity", jev.ScoreMap("How severe is it?",
//		map[Severity]jev.Entry{Cosmetic: "No impact", Degraded: "Workaround exists", Blocking: "No workaround"}))
//	urgent := jev.Ask(req, "urgent", jev.Noul("Does it convey urgency?"))
//
//	res, err := client.Evaluate(ctx, req)
//	if err != nil {
//		return err
//	}
//	d, _ := dept.Answer(res)     // jev.ChoiceAnswer[Dept]
//	s, _ := severity.Answer(res) // jev.ScoreAnswer[Severity]
//	u, _ := urgent.Answer(res)   // jev.NoulAnswer
//
// A key identifies an answer inside the response only; it is never shown to the
// model, so naming it costs nothing. Requests built dynamically can skip Ask
// and read answers with ChoiceOf, ScoreOf and NoulOf.
//
// # Acting on probabilities
//
// The probabilities are the product, not a diagnostic. Choice and Score also
// carry a Confidence; a noul carries none, its probability being the whole
// signal. Gate on thresholds calibrated on what a wrong action costs, rather
// than on a round number:
//
//	switch {
//	case d.Confidence < 0.5:
//		routeToHuman(ticket, d.Ranked())
//	case s.Nearest() == Blocking && u.Yes(0.8):
//		page(ticket)
//	default:
//		assign(ticket, d.Choice)
//	}
//
// # Errors
//
// Local validation fails with ErrInvalidRequest before any network call. A
// non-2xx response becomes an *APIError, matched by status with errors.Is
// against ErrUnauthorized, ErrRateLimited, ErrOverloaded and the other
// sentinels, and opened with errors.As for the status, the body, the request id
// and RetryAfter. A call that never got a response becomes a *TransportError
// wrapping the network error or the caller's context error.
//
// # Retries and configuration
//
// New resolves each setting from the options first, then from the
// TYPESAFE_API_KEY, TYPESAFE_BASE_URL and TYPESAFE_DEFAULT_MODEL environment
// variables, then from the package defaults. Retries follow RetryPolicy: by
// default two extra attempts on 408, 429, 5xx and connection errors, with
// exponential backoff that honors Retry-After. Each attempt is bounded by
// DefaultTimeout, and a retry is abandoned rather than started when its delay
// would outlive the caller's context deadline.
package jev
