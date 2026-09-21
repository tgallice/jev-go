package jev

import (
	"encoding/json"
	"fmt"
)

const (
	maxChoiceOptions = 255
	minScoreLevels   = 2
	maxScoreLevels   = 10
)

// Level is the level type of a Score question: any integer type, typically an
// iota enum. The API indexes levels from 0, so the enum must start at 0 and be
// contiguous for Legend and Probabilities to line up with it.
type Level interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// Question is sealed: only this package implements it, so Request.Questions
// can hold nothing but shapes the API accepts. Its implementations are
// NoulQuestion, ChoiceQuestion, ScoreQuestion and RawQuestion.
type Question interface {
	marshalQuestion() (json.RawMessage, error)
}

// TypedQuestion ties a question to the answer type it decodes to. Ask takes a
// TypedQuestion rather than a Question precisely so it can infer A and hand
// back a typed Key.
type TypedQuestion[A any] interface {
	Question
	decodeAnswer(raw json.RawMessage) (A, error)
}

type questionWire struct {
	Type         Kind            `json:"type"`
	Instructions json.RawMessage `json:"instructions,omitempty"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

// NoulQuestion asks a yes/no question and yields the probability of yes. True
// and False are optional; when both are nil, no criteria object is sent and the
// question rests on Instructions alone.
type NoulQuestion struct {
	Instructions Entry // what is being asked; omitted from the request when nil
	True         Entry // what a yes means
	False        Entry // what a no means
}

// Noul asks a yes/no question and returns the probability of yes. Unlike
// Choice and Score, a noul answer carries no confidence: the probability is the
// whole signal. instructions may be nil, in which case the field is omitted;
// api.md marks it required, so the API may still refuse such a question.
//
//	urgent := jev.Ask(req, "urgent", jev.Noul("Does the message convey urgency?").
//		WithCriteria("Explicitly time-sensitive", "No urgency expressed"))
//
//	a, err := urgent.Answer(res)
//	if err == nil && a.Yes(0.8) {
//		page()
//	}
func Noul(instructions Entry) NoulQuestion {
	return NoulQuestion{Instructions: instructions}
}

// WithCriteria returns a copy of q describing what yes and no mean. Either side
// may be nil.
func (q NoulQuestion) WithCriteria(yes, no Entry) NoulQuestion {
	q.True, q.False = yes, no
	return q
}

func (q NoulQuestion) marshalQuestion() (json.RawMessage, error) {
	w := questionWire{Type: KindNoul}
	var err error
	if w.Instructions, err = marshalInstructions(q.Instructions); err != nil {
		return nil, err
	}
	if q.True != nil || q.False != nil {
		var c struct {
			True  json.RawMessage `json:"true,omitempty"`
			False json.RawMessage `json:"false,omitempty"`
		}
		if q.True != nil {
			if c.True, err = marshalEntry(q.True, true); err != nil {
				return nil, fmt.Errorf("criteria true: %w", err)
			}
		}
		if q.False != nil {
			if c.False, err = marshalEntry(q.False, true); err != nil {
				return nil, fmt.Errorf("criteria false: %w", err)
			}
		}
		if w.Criteria, err = json.Marshal(c); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
	}
	return json.Marshal(w)
}

// ChoiceQuestion picks exactly one option out of a labeled set. The labels are
// what the model reads and what comes back in ChoiceAnswer.Choice, so they
// carry meaning; their order does not.
type ChoiceQuestion[T ~string] struct {
	Instructions Entry       // what is being asked; omitted from the request when nil
	Options      map[T]Entry // 1 to 255 labels; a nil value means an option without description
}

// Choice asks for one option out of options and returns a probability per
// label. T is inferred from the map, so a named string type flows through to
// ChoiceAnswer.Choice unchanged. An empty label, or a set outside 1 to 255
// options, is refused with ErrInvalidRequest when the request is encoded.
//
//	type Dept string
//
//	dept := jev.Ask(req, "department", jev.Choice("Which team should handle it?", map[Dept]jev.Entry{
//		"billing":   "Payments, invoicing, refunds",
//		"technical": map[string]any{"what": "Bugs and outages", "examples": []string{"500 errors"}},
//		"sales":     nil,
//	}))
//	a, err := dept.Answer(res) // jev.ChoiceAnswer[Dept]
func Choice[T ~string](instructions Entry, options map[T]Entry) ChoiceQuestion[T] {
	return ChoiceQuestion[T]{Instructions: instructions, Options: options}
}

// Options builds the option set of a Choice where no label carries a
// description, leaving the labels themselves to convey the meaning.
//
//	tone := jev.Ask(req, "tone", jev.Choice("Tone of the message?",
//		jev.Options("calm", "frustrated", "angry")))
func Options[T ~string](labels ...T) map[T]Entry {
	opts := make(map[T]Entry, len(labels))
	for _, label := range labels {
		opts[label] = nil
	}
	return opts
}

func (q ChoiceQuestion[T]) marshalQuestion() (json.RawMessage, error) {
	if n := len(q.Options); n < 1 || n > maxChoiceOptions {
		return nil, fmt.Errorf("%w: choice needs 1 to %d options, got %d", ErrInvalidRequest, maxChoiceOptions, n)
	}
	w := questionWire{Type: KindChoice}
	var err error
	if w.Instructions, err = marshalInstructions(q.Instructions); err != nil {
		return nil, err
	}
	criteria := make(map[string]json.RawMessage, len(q.Options))
	for label, desc := range q.Options {
		if label == "" {
			return nil, fmt.Errorf("%w: choice option label must not be empty", ErrInvalidRequest)
		}
		raw, err := marshalEntry(desc, true)
		if err != nil {
			return nil, fmt.Errorf("option %q: %w", string(label), err)
		}
		criteria[string(label)] = raw
	}
	if w.Criteria, err = json.Marshal(criteria); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return json.Marshal(w)
}

// ScoreQuestion rates the state on an ordered scale. Levels runs from the
// lowest level to the highest and its index is the level value, so Levels[0]
// describes level 0.
type ScoreQuestion[L Level] struct {
	Instructions Entry   // what is being rated; omitted from the request when nil
	Levels       []Entry // 2 to 10 level descriptions, lowest first

	// err defers a ScoreMap contiguity failure to marshal time, since a
	// constructor cannot report one.
	err error
}

// Score asks for a rating on the scale described by levels, lowest first, and
// returns a probability-weighted mean. L cannot be inferred from the arguments
// and must be given explicitly; ScoreMap infers it instead. Fewer than 2 or
// more than 10 levels is refused with ErrInvalidRequest when the request is
// encoded.
//
//	type Severity int
//
//	sev := jev.Ask(req, "severity", jev.Score[Severity]("How severe is the issue?",
//		"Cosmetic, no impact", "Degraded, workaround exists", "Blocking, no workaround"))
//	a, err := sev.Answer(res) // jev.ScoreAnswer[Severity]
func Score[L Level](instructions Entry, levels ...Entry) ScoreQuestion[L] {
	return ScoreQuestion[L]{Instructions: instructions, Levels: levels}
}

// ScoreMap builds the same question as Score from an enum-keyed map, inferring
// L from the keys. They must be exactly 0..len(levels)-1; a gap or an
// out-of-range key cannot be reported by a constructor, so it surfaces as
// ErrInvalidRequest from Evaluate.
//
//	type Severity int
//
//	const (
//		Cosmetic Severity = iota
//		Degraded
//		Blocking
//	)
//
//	sev := jev.Ask(req, "severity", jev.ScoreMap("How severe is the issue?", map[Severity]jev.Entry{
//		Cosmetic: "No impact",
//		Degraded: "Workaround exists",
//		Blocking: "No workaround",
//	}))
func ScoreMap[L Level](instructions Entry, levels map[L]Entry) ScoreQuestion[L] {
	q := ScoreQuestion[L]{Instructions: instructions, Levels: make([]Entry, len(levels))}
	for level, desc := range levels {
		// int64, not int: a 64-bit L would silently truncate on a 32-bit build.
		i := int64(level)
		if i < 0 || i >= int64(len(levels)) {
			q.Levels = nil
			q.err = fmt.Errorf("%w: score levels must be keyed 0..%d, got %d", ErrInvalidRequest, len(levels)-1, i)
			return q
		}
		q.Levels[i] = desc
	}
	return q
}

func (q ScoreQuestion[L]) marshalQuestion() (json.RawMessage, error) {
	if q.err != nil {
		return nil, q.err
	}
	if n := len(q.Levels); n < minScoreLevels || n > maxScoreLevels {
		return nil, fmt.Errorf("%w: score needs %d to %d levels, got %d", ErrInvalidRequest, minScoreLevels, maxScoreLevels, n)
	}
	w := questionWire{Type: KindScore}
	var err error
	if w.Instructions, err = marshalInstructions(q.Instructions); err != nil {
		return nil, err
	}
	criteria := make([]json.RawMessage, len(q.Levels))
	for i, desc := range q.Levels {
		if criteria[i], err = marshalEntry(desc, true); err != nil {
			return nil, fmt.Errorf("level %d: %w", i, err)
		}
	}
	if w.Criteria, err = json.Marshal(criteria); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	return json.Marshal(w)
}

// RawQuestion forwards a JSON question object untouched, for question types
// this SDK does not know yet. It must be a JSON object carrying a non-empty
// string "type" field, or the request is refused with ErrInvalidRequest. Its
// answer is only reachable through Result.RawAnswer, no Go type describing it.
type RawQuestion json.RawMessage

func (q RawQuestion) marshalQuestion() (json.RawMessage, error) {
	var probe struct {
		Type *string `json:"type"`
	}
	if err := json.Unmarshal(q, &probe); err != nil {
		return nil, fmt.Errorf("%w: raw question is not a valid JSON object: %v", ErrInvalidRequest, err)
	}
	if probe.Type == nil || *probe.Type == "" {
		return nil, fmt.Errorf("%w: raw question needs a non-empty string \"type\" field", ErrInvalidRequest)
	}
	return json.RawMessage(q), nil
}

func marshalInstructions(v Entry) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := marshalEntry(v, false)
	if err != nil {
		return nil, fmt.Errorf("instructions: %w", err)
	}
	return raw, nil
}
