package jev

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dept string

const (
	billing   dept = "billing"
	technical dept = "technical"
	sales     dept = "sales"
)

type severity int

const (
	cosmetic severity = iota
	degraded
	blocking
)

// Compile-time proof that every question type stays sealed, and that the typed
// ones announce their answer type.
var (
	_ Question                             = NoulQuestion{}
	_ Question                             = ChoiceQuestion[dept]{}
	_ Question                             = ScoreQuestion[severity]{}
	_ Question                             = RawQuestion(nil)
	_ TypedQuestion[NoulAnswer]            = NoulQuestion{}
	_ TypedQuestion[ChoiceAnswer[dept]]    = ChoiceQuestion[dept]{}
	_ TypedQuestion[ScoreAnswer[severity]] = ScoreQuestion[severity]{}
)

func manyOptions(n int) map[dept]Entry {
	opts := make(map[dept]Entry, n)
	for i := range n {
		opts[dept(fmt.Sprintf("o%d", i))] = nil
	}
	return opts
}

func manyLevels(n int) []Entry {
	levels := make([]Entry, n)
	for i := range levels {
		levels[i] = fmt.Sprintf("level %d", i)
	}
	return levels
}

func TestNoulQuestionMarshal(t *testing.T) {
	for _, tc := range []struct {
		name            string
		question        Question
		want            string
		wantErr         error
		wantErrContains string
	}{
		{
			name:     "instructions and both criteria",
			question: Noul("Does `body` convey urgency?").WithCriteria("Explicitly time-sensitive", "No urgency expressed"),
			want:     `{"type":"noul","instructions":"Does ` + "`body`" + ` convey urgency?","criteria":{"true":"Explicitly time-sensitive","false":"No urgency expressed"}}`,
		},
		{
			name:     "criteria omitted when unset",
			question: Noul("Urgent?"),
			want:     `{"type":"noul","instructions":"Urgent?"}`,
		},
		{
			name:     "instructions omitted when nil",
			question: Noul(nil),
			want:     `{"type":"noul"}`,
		},
		{
			name:     "only the true criterion",
			question: Noul("Urgent?").WithCriteria("Time-sensitive", nil),
			want:     `{"type":"noul","instructions":"Urgent?","criteria":{"true":"Time-sensitive"}}`,
		},
		{
			name:     "only the false criterion",
			question: Noul("Urgent?").WithCriteria(nil, "No urgency expressed"),
			want:     `{"type":"noul","instructions":"Urgent?","criteria":{"false":"No urgency expressed"}}`,
		},
		{
			name: "object instructions and criteria",
			question: Noul(map[string]any{"ask": "urgent?"}).
				WithCriteria(json.RawMessage(`{"hint":"deadline"}`), []string{"calm"}),
			want: `{"type":"noul","instructions":{"ask":"urgent?"},"criteria":{"true":{"hint":"deadline"},"false":["calm"]}}`,
		},
		{
			name:     "explicit null criterion",
			question: Noul("Urgent?").WithCriteria(json.RawMessage(`null`), nil),
			want:     `{"type":"noul","instructions":"Urgent?","criteria":{"true":null}}`,
		},
		{
			name:            "scalar instructions",
			question:        Noul(42),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "instructions",
		},
		{
			name:            "scalar true criterion",
			question:        Noul("Urgent?").WithCriteria(1, nil),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "criteria true",
		},
		{
			name:            "scalar false criterion",
			question:        Noul("Urgent?").WithCriteria(nil, 1),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "criteria false",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.question.marshalQuestion()
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				return
			}
			assert.JSONEq(t, tc.want, string(raw))
		})
	}
}

func TestChoiceQuestionMarshal(t *testing.T) {
	for _, tc := range []struct {
		name            string
		question        Question
		want            string
		wantErr         error
		wantErrContains string
	}{
		{
			name: "mixed descriptions",
			question: Choice("Which team should handle `body`?", map[dept]Entry{
				billing:   "Payments, invoicing, refunds",
				technical: map[string]any{"what": "Bugs, outages", "examples": []string{"500 errors"}},
				sales:     nil,
			}),
			want: `{"type":"choice","instructions":"Which team should handle ` + "`body`" + `?","criteria":{"billing":"Payments, invoicing, refunds","technical":{"examples":["500 errors"],"what":"Bugs, outages"},"sales":null}}`,
		},
		{
			name:     "options without description",
			question: Choice("Tone?", Options("calm", "angry")),
			want:     `{"type":"choice","instructions":"Tone?","criteria":{"angry":null,"calm":null}}`,
		},
		{
			name:     "instructions omitted when nil",
			question: Choice(nil, Options("only")),
			want:     `{"type":"choice","criteria":{"only":null}}`,
		},
		{
			name:            "empty label",
			question:        Choice("x", map[dept]Entry{"": "nope"}),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "option label must not be empty",
		},
		{
			name:            "scalar description",
			question:        Choice("x", map[dept]Entry{billing: 3}),
			wantErr:         ErrInvalidRequest,
			wantErrContains: `option "billing"`,
		},
		{
			name:            "scalar instructions",
			question:        Choice(42, Options("a")),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "instructions",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.question.marshalQuestion()
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				return
			}
			assert.JSONEq(t, tc.want, string(raw))
		})
	}
}

func TestScoreQuestionMarshal(t *testing.T) {
	for _, tc := range []struct {
		name            string
		question        Question
		want            string
		wantErr         error
		wantErrContains string
	}{
		{
			name:     "variadic levels",
			question: Score[severity]("How severe?", "Cosmetic; no impact", "Broken or degraded; workaround exists", "Blocking; no workaround"),
			want:     `{"type":"score","instructions":"How severe?","criteria":["Cosmetic; no impact","Broken or degraded; workaround exists","Blocking; no workaround"]}`,
		},
		{
			name: "levels from an enum map",
			question: ScoreMap("How severe?", map[severity]Entry{
				cosmetic: "Cosmetic; no impact",
				degraded: "Broken or degraded; workaround exists",
				blocking: "Blocking; no workaround",
			}),
			want: `{"type":"score","instructions":"How severe?","criteria":["Cosmetic; no impact","Broken or degraded; workaround exists","Blocking; no workaround"]}`,
		},
		{
			name:     "nil level becomes null",
			question: Score[int](nil, nil, "high"),
			want:     `{"type":"score","criteria":[null,"high"]}`,
		},
		{
			name:            "empty ScoreMap surfaces at marshal time",
			question:        ScoreMap[severity]("x", nil),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "score needs 2 to 10 levels, got 0",
		},
		{
			name:            "scalar level",
			question:        Score[int]("x", "low", true),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "level 1",
		},
		{
			name:            "scalar instructions",
			question:        Score[int](false, "low", "high"),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "instructions",
		},
		{
			name:            "deferred ScoreMap error",
			question:        ScoreMap("x", map[severity]Entry{cosmetic: "a", blocking: "b"}),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "score levels must be keyed 0..1, got 2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.question.marshalQuestion()
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				return
			}
			assert.JSONEq(t, tc.want, string(raw))
		})
	}
}

func TestRawQuestionMarshal(t *testing.T) {
	for _, tc := range []struct {
		name            string
		question        RawQuestion
		want            string
		wantErr         error
		wantErrContains string
	}{
		{
			name:     "forwarded untouched",
			question: RawQuestion(`{"type":"future","weird":[1,2]}`),
			want:     `{"type":"future","weird":[1,2]}`,
		},
		{
			name:            "not json",
			question:        RawQuestion(`{oops`),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "raw question is not a valid JSON object",
		},
		{
			name:            "array",
			question:        RawQuestion(`["noul"]`),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "raw question is not a valid JSON object",
		},
		{
			name:            "type is not a string",
			question:        RawQuestion(`{"type":5}`),
			wantErr:         ErrInvalidRequest,
			wantErrContains: "raw question is not a valid JSON object",
		},
		{
			name:            "without type",
			question:        RawQuestion(`{"instructions":"x"}`),
			wantErr:         ErrInvalidRequest,
			wantErrContains: `raw question needs a non-empty string "type" field`,
		},
		{
			name:            "empty type",
			question:        RawQuestion(`{"type":""}`),
			wantErr:         ErrInvalidRequest,
			wantErrContains: `raw question needs a non-empty string "type" field`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := tc.question.marshalQuestion()
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				return
			}
			assert.JSONEq(t, tc.want, string(raw))
		})
	}
}

func TestChoiceQuestionOptionLimits(t *testing.T) {
	for _, tc := range []struct {
		name            string
		options         int
		wantErr         error
		wantErrContains string
	}{
		{name: "one option", options: 1},
		{name: "255 options", options: 255},
		{
			name:            "no option",
			options:         0,
			wantErr:         ErrInvalidRequest,
			wantErrContains: "choice needs 1 to 255 options, got 0",
		},
		{
			name:            "256 options",
			options:         256,
			wantErr:         ErrInvalidRequest,
			wantErrContains: "choice needs 1 to 255 options, got 256",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := Choice("many", manyOptions(tc.options)).marshalQuestion()
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				assert.Nil(t, raw)
				return
			}
			var got struct {
				Criteria map[string]any `json:"criteria"`
			}
			require.NoError(t, json.Unmarshal(raw, &got))
			assert.Len(t, got.Criteria, tc.options)
		})
	}
}

func TestScoreQuestionLevelLimits(t *testing.T) {
	for _, tc := range []struct {
		name            string
		levels          int
		wantErr         error
		wantErrContains string
	}{
		{name: "two levels", levels: 2},
		{name: "10 levels", levels: 10},
		{
			name:            "one level",
			levels:          1,
			wantErr:         ErrInvalidRequest,
			wantErrContains: "score needs 2 to 10 levels, got 1",
		},
		{
			name:            "11 levels",
			levels:          11,
			wantErr:         ErrInvalidRequest,
			wantErrContains: "score needs 2 to 10 levels, got 11",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := Score[severity]("many", manyLevels(tc.levels)...).marshalQuestion()
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				assert.Nil(t, raw)
				return
			}
			var got struct {
				Criteria []any `json:"criteria"`
			}
			require.NoError(t, json.Unmarshal(raw, &got))
			assert.Len(t, got.Criteria, tc.levels)
		})
	}
}

func TestOptions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels []dept
		want   map[dept]Entry
	}{
		{name: "no label", labels: nil, want: map[dept]Entry{}},
		{name: "one label", labels: []dept{billing}, want: map[dept]Entry{billing: nil}},
		{name: "several labels", labels: []dept{billing, technical}, want: map[dept]Entry{billing: nil, technical: nil}},
		{name: "duplicate labels collapse", labels: []dept{billing, billing}, want: map[dept]Entry{billing: nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Options(tc.labels...))
		})
	}
}

func TestScoreMap(t *testing.T) {
	for _, tc := range []struct {
		name            string
		levels          map[severity]Entry
		want            []Entry
		wantErr         error
		wantErrContains string
	}{
		{
			name:   "map order does not matter",
			levels: map[severity]Entry{blocking: "c", cosmetic: "a", degraded: "b"},
			want:   []Entry{"a", "b", "c"},
		},
		{name: "empty map", levels: nil, want: []Entry{}},
		{
			name:            "gap in the keys",
			levels:          map[severity]Entry{cosmetic: "a", blocking: "b"},
			wantErr:         ErrInvalidRequest,
			wantErrContains: "score levels must be keyed 0..1, got 2",
		},
		{
			name:            "not starting at zero",
			levels:          map[severity]Entry{degraded: "a", blocking: "b"},
			wantErr:         ErrInvalidRequest,
			wantErrContains: "score levels must be keyed 0..1, got 2",
		},
		{
			name:            "negative key",
			levels:          map[severity]Entry{-1: "a", cosmetic: "b"},
			wantErr:         ErrInvalidRequest,
			wantErrContains: "score levels must be keyed 0..1, got -1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := ScoreMap("x", tc.levels)
			require.ErrorIs(t, q.err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, q.err.Error(), tc.wantErrContains)
				assert.Nil(t, q.Levels)
				return
			}
			assert.Equal(t, tc.want, q.Levels)
		})
	}
}

// TestScoreMapInt64 covers a 64-bit level type, whose keys must not be
// truncated to int on a 32-bit build.
func TestScoreMapInt64(t *testing.T) {
	for _, tc := range []struct {
		name            string
		levels          map[int64]Entry
		want            []Entry
		wantErr         error
		wantErrContains string
	}{
		{name: "contiguous", levels: map[int64]Entry{0: "a", 1: "b"}, want: []Entry{"a", "b"}},
		{
			name:            "key beyond 32 bits",
			levels:          map[int64]Entry{0: "a", 1 << 32: "b"},
			wantErr:         ErrInvalidRequest,
			wantErrContains: "score levels must be keyed 0..1, got 4294967296",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := ScoreMap("x", tc.levels)
			require.ErrorIs(t, q.err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, q.err.Error(), tc.wantErrContains)
				assert.Nil(t, q.Levels)
				return
			}
			assert.Equal(t, tc.want, q.Levels)
		})
	}
}
