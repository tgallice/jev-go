package jev

import (
	"encoding/json"
	"fmt"
)

// Request is one evaluation: the state to examine and the questions asked about
// it. The state is billed once however many questions accompany it, so batching
// is what makes a further question nearly free.
type Request struct {
	State     Entry               // required: a string, an object or an array, never nil or a bare scalar
	Model     string              // overrides the client default when set
	Questions map[string]Question // required, non-empty; allocated by Ask when nil, and a nil value in it is refused
	Extra     map[string]any      // extra top-level fields, merged last; "state" and "questions" are refused
}

// Key is a typed handle on the answer to a question registered with Ask. Its
// zero value is unusable: Answer then fails with ErrInvalidRequest.
type Key[A any] struct {
	name   string
	decode func(json.RawMessage) (A, error)
}

// Name is the question key. It identifies the answer in the response and is
// never shown to the model, so it can be named for the calling code alone.
func (k Key[A]) Name() string { return k.name }

// Answer decodes this question's answer out of r. It fails with
// ErrAnswerMissing when the API answered nothing under this key, ErrAnswerKind
// when the answer is of another kind, and ErrInvalidResponse when it lacks a
// field the kind requires.
func (k Key[A]) Answer(r *Result) (A, error) {
	var zero A
	if k.decode == nil {
		return zero, fmt.Errorf("%w: zero Key, use the one returned by Ask", ErrInvalidRequest)
	}
	raw, err := answerOf(r, k.name)
	if err != nil {
		return zero, err
	}
	return k.decode(raw)
}

// Ask registers q under name in req and returns a handle typed by the answer q
// decodes to, so reading that answer needs no type assertion. A name already in
// use is replaced. A is inferred from q, which must therefore be a concrete
// question (NoulQuestion, ChoiceQuestion, ScoreQuestion), not a Question value;
// a nil req panics.
//
//	req := &jev.Request{State: ticket}
//	urgent := jev.Ask(req, "urgent", jev.Noul("Is it urgent?"))
//	tone := jev.Ask(req, "tone", jev.Choice("Tone?", jev.Options("calm", "angry")))
//
//	res, err := client.Evaluate(ctx, req)
//	// urgent.Answer(res) yields a NoulAnswer, tone.Answer(res) a ChoiceAnswer[string].
func Ask[A any](req *Request, name string, q TypedQuestion[A]) Key[A] {
	if req.Questions == nil {
		req.Questions = make(map[string]Question)
	}
	req.Questions[name] = q
	return Key[A]{name: name, decode: q.decodeAnswer}
}

// MarshalJSON encodes the request on its own, so "model" is omitted when
// Request.Model is empty; what Evaluate sends carries the client default
// instead. It fails with ErrInvalidRequest on an unusable state, an empty
// question set or a question that cannot be encoded. The receiver is a value so
// that a Request embedded or passed by value still encodes through it.
func (r Request) MarshalJSON() ([]byte, error) { return marshalRequest(&r, r.Model) }

func marshalRequest(req *Request, model string) ([]byte, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: nil request", ErrInvalidRequest)
	}
	state, err := marshalEntry(req.State, false)
	if err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	if len(req.Questions) == 0 {
		return nil, fmt.Errorf("%w: at least one question is required", ErrInvalidRequest)
	}
	questions := make(map[string]json.RawMessage, len(req.Questions))
	for name, q := range req.Questions {
		if name == "" {
			return nil, fmt.Errorf("%w: question key must not be empty", ErrInvalidRequest)
		}
		if q == nil {
			return nil, fmt.Errorf("%w: question %q is nil", ErrInvalidRequest, name)
		}
		raw, err := q.marshalQuestion()
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", name, err)
		}
		questions[name] = raw
	}
	rawQuestions, err := json.Marshal(questions)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	top := map[string]json.RawMessage{"state": state, "questions": rawQuestions}
	if model != "" {
		rawModel, err := json.Marshal(model)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
		top["model"] = rawModel
	}
	for name, v := range req.Extra {
		if name == "state" || name == "questions" {
			return nil, fmt.Errorf("%w: extra field %q would overwrite the request body", ErrInvalidRequest, name)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("%w: extra field %q: %v", ErrInvalidRequest, name, err)
		}
		top[name] = raw
	}
	return json.Marshal(top)
}
