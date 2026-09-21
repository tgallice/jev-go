package jev

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
)

// Kind is the discriminant carried by every question and answer. An answer of
// any other kind is kept raw by Result rather than failing the call.
type Kind string

// The answer kinds this SDK decodes.
const (
	KindNoul   Kind = "noul"
	KindChoice Kind = "choice"
	KindScore  Kind = "score"
)

// NoulAnswer is the answer to a yes/no question. It carries no confidence, the
// API sending none for this kind, so Noul is the only value to act on.
type NoulAnswer struct {
	Noul float64 // probability of yes, 0..1
}

// Yes reports whether the probability of yes is at least threshold. The
// threshold is the caller's to pick, from what a wrong yes costs.
func (a NoulAnswer) Yes(threshold float64) bool { return a.Noul >= threshold }

// ChoiceAnswer is the answer to a single-pick question. Choice is only the
// highest-probability option, so a near tie still yields one; Confidence and
// Ranked are what tell the two apart.
type ChoiceAnswer[T ~string] struct {
	Choice        T             // the most probable option
	Probabilities map[T]float64 // one entry per option offered, summing to about 1
	Confidence    float64       // calibrated confidence in Choice, 0..1
}

// Ranked lists every option offered by decreasing probability, ties broken by
// label so the order is stable. It is what a fallback path needs when
// Confidence is too low to act on Choice alone.
func (a ChoiceAnswer[T]) Ranked() []T {
	ranked := make([]T, 0, len(a.Probabilities))
	for label := range a.Probabilities {
		ranked = append(ranked, label)
	}
	slices.SortFunc(ranked, func(x, y T) int {
		if c := cmp.Compare(a.Probabilities[y], a.Probabilities[x]); c != 0 {
			return c
		}
		return cmp.Compare(x, y)
	})
	return ranked
}

// ScoreAnswer is the answer to a scale question. Score, being a mean, is
// usually a better input to a decision than the level Nearest rounds it to.
type ScoreAnswer[L Level] struct {
	Score         float64       // probability-weighted mean, may fall between two levels
	Confidence    float64       // calibrated confidence in Score, 0..1
	Probabilities map[L]float64 // one entry per level, summing to about 1
	Legend        map[L]Entry   // the level descriptions echoed back by the API
}

// Nearest rounds Score to the closest level, halves going up, clamped to the
// scale when LevelCount knows its size. A NaN Score yields level 0. Rounding
// discards the distance between levels, so a 1.5 and a clean 2 become the same
// answer.
func (a ScoreAnswer[L]) Nearest() L {
	if math.IsNaN(a.Score) {
		return 0
	}
	level := math.Round(a.Score)
	if level < 0 {
		level = 0
	}
	if n := a.LevelCount(); n > 0 && level > float64(n-1) {
		level = float64(n - 1)
	}
	return L(level)
}

// LevelCount is the size of the scale as the API reported it, 0 when the answer
// carries neither legend nor probabilities.
func (a ScoreAnswer[L]) LevelCount() int {
	if len(a.Legend) > 0 {
		return len(a.Legend)
	}
	return len(a.Probabilities)
}

type answerWire struct {
	Type          Kind               `json:"type"`
	Noul          *float64           `json:"noul"`
	Choice        *string            `json:"choice"`
	Score         *float64           `json:"score"`
	Confidence    *float64           `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities"`
	Legend        map[string]Entry   `json:"legend"`
}

func decodeWire(raw json.RawMessage, want Kind) (answerWire, error) {
	var w answerWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return w, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if w.Type != want {
		return w, fmt.Errorf("%w: want %s, got %s", ErrAnswerKind, want, w.Type)
	}
	return w, nil
}

func missingField(k Kind, field string) error {
	return fmt.Errorf("%w: %s answer without %q field", ErrInvalidResponse, k, field)
}

func decodeNoul(raw json.RawMessage) (NoulAnswer, error) {
	w, err := decodeWire(raw, KindNoul)
	if err != nil {
		return NoulAnswer{}, err
	}
	if w.Noul == nil {
		return NoulAnswer{}, missingField(KindNoul, "noul")
	}
	return NoulAnswer{Noul: *w.Noul}, nil
}

func decodeChoice[T ~string](raw json.RawMessage) (ChoiceAnswer[T], error) {
	w, err := decodeWire(raw, KindChoice)
	if err != nil {
		return ChoiceAnswer[T]{}, err
	}
	switch {
	case w.Choice == nil:
		return ChoiceAnswer[T]{}, missingField(KindChoice, "choice")
	case w.Confidence == nil:
		return ChoiceAnswer[T]{}, missingField(KindChoice, "confidence")
	case w.Probabilities == nil:
		return ChoiceAnswer[T]{}, missingField(KindChoice, "probabilities")
	}
	probabilities := make(map[T]float64, len(w.Probabilities))
	for label, p := range w.Probabilities {
		probabilities[T(label)] = p
	}
	return ChoiceAnswer[T]{
		Choice:        T(*w.Choice),
		Probabilities: probabilities,
		Confidence:    *w.Confidence,
	}, nil
}

func decodeScore[L Level](raw json.RawMessage) (ScoreAnswer[L], error) {
	w, err := decodeWire(raw, KindScore)
	if err != nil {
		return ScoreAnswer[L]{}, err
	}
	switch {
	case w.Score == nil:
		return ScoreAnswer[L]{}, missingField(KindScore, "score")
	case w.Confidence == nil:
		return ScoreAnswer[L]{}, missingField(KindScore, "confidence")
	case w.Probabilities == nil:
		return ScoreAnswer[L]{}, missingField(KindScore, "probabilities")
	case w.Legend == nil:
		return ScoreAnswer[L]{}, missingField(KindScore, "legend")
	}
	probabilities, err := byLevel[L](w.Probabilities)
	if err != nil {
		return ScoreAnswer[L]{}, fmt.Errorf("probabilities: %w", err)
	}
	legend, err := byLevel[L](w.Legend)
	if err != nil {
		return ScoreAnswer[L]{}, fmt.Errorf("legend: %w", err)
	}
	return ScoreAnswer[L]{
		Score:         *w.Score,
		Confidence:    *w.Confidence,
		Probabilities: probabilities,
		Legend:        legend,
	}, nil
}

// byLevel converts the wire maps, whose keys are level indexes written as
// strings ("0", "1", ...), into maps keyed by the caller's level type.
func byLevel[L Level, V any](src map[string]V) (map[L]V, error) {
	out := make(map[L]V, len(src))
	for key, v := range src {
		i, err := strconv.Atoi(key)
		if err != nil {
			return nil, fmt.Errorf("%w: level key %q is not an integer", ErrInvalidResponse, key)
		}
		// Out-of-range keys would wrap around and collide with a valid level.
		if i < 0 || int(L(i)) != i {
			return nil, fmt.Errorf("%w: level key %q is out of range for the level type", ErrInvalidResponse, key)
		}
		out[L(i)] = v
	}
	return out, nil
}

func (q NoulQuestion) decodeAnswer(raw json.RawMessage) (NoulAnswer, error) {
	return decodeNoul(raw)
}

func (q ChoiceQuestion[T]) decodeAnswer(raw json.RawMessage) (ChoiceAnswer[T], error) {
	return decodeChoice[T](raw)
}

func (q ScoreQuestion[L]) decodeAnswer(raw json.RawMessage) (ScoreAnswer[L], error) {
	return decodeScore[L](raw)
}

// NoulOf reads a yes/no answer by key, for requests built without Ask. It fails
// with ErrAnswerMissing when nothing was answered under key, ErrAnswerKind when
// the answer is of another kind, and ErrInvalidResponse when it lacks a field.
func NoulOf(r *Result, key string) (NoulAnswer, error) {
	raw, err := answerOf(r, key)
	if err != nil {
		return NoulAnswer{}, err
	}
	return decodeNoul(raw)
}

// ChoiceOf reads a single-pick answer by key, for requests built without Ask. T
// is not checked against the labels that were sent: whatever the API returns is
// converted to T. Errors: ErrAnswerMissing, ErrAnswerKind, ErrInvalidResponse.
func ChoiceOf[T ~string](r *Result, key string) (ChoiceAnswer[T], error) {
	raw, err := answerOf(r, key)
	if err != nil {
		return ChoiceAnswer[T]{}, err
	}
	return decodeChoice[T](raw)
}

// ScoreOf reads a scale answer by key, for requests built without Ask. A level
// index that does not fit L is refused rather than wrapped around. Errors:
// ErrAnswerMissing, ErrAnswerKind, ErrInvalidResponse.
func ScoreOf[L Level](r *Result, key string) (ScoreAnswer[L], error) {
	raw, err := answerOf(r, key)
	if err != nil {
		return ScoreAnswer[L]{}, err
	}
	return decodeScore[L](raw)
}

func answerOf(r *Result, key string) (json.RawMessage, error) {
	if r != nil {
		if raw, ok := r.answers[key]; ok {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrAnswerMissing, key)
}
