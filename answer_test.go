package jev

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeNoul(t *testing.T) {
	for _, tc := range []struct {
		name            string
		raw             string
		want            NoulAnswer
		wantErr         error
		wantErrContains string
	}{
		{name: "probability", raw: `{"type":"noul","noul":0.91}`, want: NoulAnswer{Noul: 0.91}},
		{name: "zero probability", raw: `{"type":"noul","noul":0}`, want: NoulAnswer{}},
		{
			name:            "missing noul field",
			raw:             `{"type":"noul"}`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: `noul answer without "noul" field`,
		},
		{
			name:            "crossed kind",
			raw:             `{"type":"choice","choice":"a"}`,
			wantErr:         ErrAnswerKind,
			wantErrContains: "want noul, got choice",
		},
		{
			name:            "broken json",
			raw:             `{"type":`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: "unexpected end of JSON input",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NoulQuestion{}.decodeAnswer(json.RawMessage(tc.raw))
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				assert.Zero(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDecodeChoice(t *testing.T) {
	for _, tc := range []struct {
		name            string
		raw             string
		want            ChoiceAnswer[dept]
		wantErr         error
		wantErrContains string
	}{
		{
			name: "full answer",
			raw:  `{"type":"choice","choice":"technical","probabilities":{"billing":0.03,"technical":0.96,"sales":0.01},"confidence":0.93}`,
			want: ChoiceAnswer[dept]{
				Choice:        technical,
				Probabilities: map[dept]float64{billing: 0.03, technical: 0.96, sales: 0.01},
				Confidence:    0.93,
			},
		},
		{
			name:            "missing choice",
			raw:             `{"type":"choice","probabilities":{"a":1},"confidence":1}`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: `"choice" field`,
		},
		{
			name:            "missing confidence",
			raw:             `{"type":"choice","choice":"a","probabilities":{"a":1}}`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: `"confidence" field`,
		},
		{
			name:            "missing probabilities",
			raw:             `{"type":"choice","choice":"a","confidence":1}`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: `"probabilities" field`,
		},
		{
			name:            "crossed kind",
			raw:             `{"type":"noul","noul":0.5}`,
			wantErr:         ErrAnswerKind,
			wantErrContains: "want choice, got noul",
		},
		{
			name:            "broken json",
			raw:             `nope`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: "invalid character",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChoiceQuestion[dept]{}.decodeAnswer(json.RawMessage(tc.raw))
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				assert.Zero(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDecodeScore(t *testing.T) {
	for _, tc := range []struct {
		name            string
		raw             string
		want            ScoreAnswer[severity]
		wantErr         error
		wantErrContains string
	}{
		{
			name: "full answer",
			raw:  `{"type":"score","score":1.87,"legend":{"0":"low","1":"mid","2":"high"},"probabilities":{"0":0.02,"1":0.09,"2":0.89},"confidence":0.81}`,
			want: ScoreAnswer[severity]{
				Score:         1.87,
				Confidence:    0.81,
				Probabilities: map[severity]float64{cosmetic: 0.02, degraded: 0.09, blocking: 0.89},
				Legend:        map[severity]Entry{cosmetic: "low", degraded: "mid", blocking: "high"},
			},
		},
		{
			name: "object legend entries",
			raw:  `{"type":"score","score":0,"legend":{"0":{"label":"low"},"1":null},"probabilities":{"0":1,"1":0},"confidence":1}`,
			want: ScoreAnswer[severity]{
				Score:         0,
				Confidence:    1,
				Probabilities: map[severity]float64{cosmetic: 1, degraded: 0},
				Legend:        map[severity]Entry{cosmetic: map[string]any{"label": "low"}, degraded: nil},
			},
		},
		{
			name:            "missing score",
			raw:             `{"type":"score","legend":{},"probabilities":{},"confidence":1}`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: `"score" field`,
		},
		{
			name:            "missing confidence",
			raw:             `{"type":"score","score":1,"legend":{},"probabilities":{}}`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: `"confidence" field`,
		},
		{
			name:            "missing probabilities",
			raw:             `{"type":"score","score":1,"legend":{},"confidence":1}`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: `"probabilities" field`,
		},
		{
			name:            "missing legend",
			raw:             `{"type":"score","score":1,"probabilities":{},"confidence":1}`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: `"legend" field`,
		},
		{
			name:            "non integer probability key",
			raw:             `{"type":"score","score":1,"legend":{"0":"low"},"probabilities":{"low":0.5},"confidence":1}`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: `probabilities: jev: invalid response: level key "low" is not an integer`,
		},
		{
			name:            "non integer legend key",
			raw:             `{"type":"score","score":1,"legend":{"low":"x"},"probabilities":{"0":0.5},"confidence":1}`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: `legend: jev: invalid response: level key "low" is not an integer`,
		},
		{
			name:            "crossed kind",
			raw:             `{"type":"choice","choice":"a"}`,
			wantErr:         ErrAnswerKind,
			wantErrContains: "want score, got choice",
		},
		{
			name:            "not an object",
			raw:             `[]`,
			wantErr:         ErrInvalidResponse,
			wantErrContains: "cannot unmarshal array",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ScoreQuestion[severity]{}.decodeAnswer(json.RawMessage(tc.raw))
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				assert.Zero(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestByLevel(t *testing.T) {
	for _, tc := range []struct {
		name            string
		src             map[string]float64
		want            map[severity]float64
		wantErr         error
		wantErrContains string
	}{
		{name: "empty", src: map[string]float64{}, want: map[severity]float64{}},
		{
			name: "string keys become levels",
			src:  map[string]float64{"0": 0.1, "2": 0.9},
			want: map[severity]float64{cosmetic: 0.1, blocking: 0.9},
		},
		{
			name:            "negative key",
			src:             map[string]float64{"-1": 1},
			wantErr:         ErrInvalidResponse,
			wantErrContains: `level key "-1" is out of range`,
		},
		{
			name:            "non integer key",
			src:             map[string]float64{"high": 1},
			wantErr:         ErrInvalidResponse,
			wantErrContains: `level key "high" is not an integer`,
		},
		{
			name:            "float key",
			src:             map[string]float64{"1.5": 1},
			wantErr:         ErrInvalidResponse,
			wantErrContains: `level key "1.5" is not an integer`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := byLevel[severity](tc.src)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestByLevelUint8 covers the levels that only a narrow level type can reject.
func TestByLevelUint8(t *testing.T) {
	for _, tc := range []struct {
		name            string
		src             map[string]float64
		want            map[uint8]float64
		wantErr         error
		wantErrContains string
	}{
		{name: "in range", src: map[string]float64{"0": 0.4, "255": 0.6}, want: map[uint8]float64{0: 0.4, 255: 0.6}},
		{
			name:            "above the type range",
			src:             map[string]float64{"256": 1},
			wantErr:         ErrInvalidResponse,
			wantErrContains: `level key "256" is out of range`,
		},
		{
			name:            "wrapping collision with an existing level",
			src:             map[string]float64{"0": 0.5, "256": 0.5},
			wantErr:         ErrInvalidResponse,
			wantErrContains: `level key "256" is out of range`,
		},
		{
			name:            "negative key",
			src:             map[string]float64{"-1": 1},
			wantErr:         ErrInvalidResponse,
			wantErrContains: `level key "-1" is out of range`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := byLevel[uint8](tc.src)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestNoulAnswerYes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		answer    NoulAnswer
		threshold float64
		want      bool
	}{
		{name: "above the threshold", answer: NoulAnswer{Noul: 0.8}, threshold: 0.5, want: true},
		{name: "on the threshold", answer: NoulAnswer{Noul: 0.8}, threshold: 0.8, want: true},
		{name: "below the threshold", answer: NoulAnswer{Noul: 0.8}, threshold: 0.81, want: false},
		{name: "zero probability", answer: NoulAnswer{}, threshold: 0, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.answer.Yes(tc.threshold))
		})
	}
}

func TestChoiceAnswerRanked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer ChoiceAnswer[dept]
		want   []dept
	}{
		{
			name:   "by decreasing probability",
			answer: ChoiceAnswer[dept]{Probabilities: map[dept]float64{billing: 0.03, technical: 0.96, sales: 0.01}},
			want:   []dept{technical, billing, sales},
		},
		{
			name:   "ties broken by label",
			answer: ChoiceAnswer[dept]{Probabilities: map[dept]float64{sales: 0.25, billing: 0.25, technical: 0.5}},
			want:   []dept{technical, billing, sales},
		},
		{name: "no probability", answer: ChoiceAnswer[dept]{}, want: []dept{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Map iteration order varies: the ranking must not.
			for range 20 {
				assert.Equal(t, tc.want, tc.answer.Ranked())
			}
		})
	}
}

func TestScoreAnswerNearest(t *testing.T) {
	scale := map[severity]Entry{cosmetic: "a", degraded: "b", blocking: "c"}

	for _, tc := range []struct {
		name   string
		answer ScoreAnswer[severity]
		want   severity
	}{
		{name: "exact level", answer: ScoreAnswer[severity]{Score: 0}, want: cosmetic},
		{name: "rounds down", answer: ScoreAnswer[severity]{Score: 1.49}, want: degraded},
		{name: "half rounds up", answer: ScoreAnswer[severity]{Score: 1.5}, want: blocking},
		{name: "close to the top level", answer: ScoreAnswer[severity]{Score: 1.87}, want: blocking},
		{name: "top level", answer: ScoreAnswer[severity]{Score: 2}, want: blocking},
		{name: "NaN falls back on the first level", answer: ScoreAnswer[severity]{Score: math.NaN(), Legend: scale}, want: cosmetic},
		{name: "NaN without a known scale", answer: ScoreAnswer[severity]{Score: math.NaN()}, want: cosmetic},
		{name: "negative score clamps down", answer: ScoreAnswer[severity]{Score: -5, Legend: scale}, want: cosmetic},
		{name: "score above the scale clamps up", answer: ScoreAnswer[severity]{Score: 99, Legend: scale}, want: blocking},
		{name: "clamps on the probabilities when there is no legend", answer: ScoreAnswer[severity]{Score: 9, Probabilities: map[severity]float64{cosmetic: 1, degraded: 0}}, want: degraded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.answer.Nearest())
		})
	}
}

func TestScoreAnswerLevelCount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer ScoreAnswer[severity]
		want   int
	}{
		{
			name: "counted from the legend",
			answer: ScoreAnswer[severity]{
				Legend:        map[severity]Entry{cosmetic: "a", degraded: "b", blocking: "c"},
				Probabilities: map[severity]float64{cosmetic: 0.1},
			},
			want: 3,
		},
		{
			name:   "falls back on the probabilities",
			answer: ScoreAnswer[severity]{Probabilities: map[severity]float64{cosmetic: 0.5, degraded: 0.5}},
			want:   2,
		},
		{name: "zero answer", answer: ScoreAnswer[severity]{}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.answer.LevelCount())
		})
	}
}

func TestNoulOf(t *testing.T) {
	mixed := loadResult(t, "response_mixed.json", nil)

	for _, tc := range []struct {
		name    string
		result  *Result
		key     string
		want    NoulAnswer
		wantErr error
	}{
		{name: "known key", result: mixed, key: "is_urgent", want: NoulAnswer{Noul: 0.91}},
		{name: "unknown key", result: mixed, key: "nope", wantErr: ErrAnswerMissing},
		{name: "nil result", result: nil, key: "is_urgent", wantErr: ErrAnswerMissing},
		{name: "crossed kind", result: mixed, key: "department", wantErr: ErrAnswerKind},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NoulOf(tc.result, tc.key)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Zero(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestChoiceOf(t *testing.T) {
	mixed := loadResult(t, "response_mixed.json", nil)

	for _, tc := range []struct {
		name    string
		result  *Result
		key     string
		want    ChoiceAnswer[dept]
		wantErr error
	}{
		{
			name:   "known key",
			result: mixed,
			key:    "department",
			want: ChoiceAnswer[dept]{
				Choice:        technical,
				Probabilities: map[dept]float64{billing: 0.03, technical: 0.96, sales: 0.01},
				Confidence:    0.93,
			},
		},
		{name: "unknown key", result: mixed, key: "nope", wantErr: ErrAnswerMissing},
		{name: "nil result", result: nil, key: "department", wantErr: ErrAnswerMissing},
		{name: "crossed kind", result: mixed, key: "severity", wantErr: ErrAnswerKind},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChoiceOf[dept](tc.result, tc.key)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Zero(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestScoreOf(t *testing.T) {
	mixed := loadResult(t, "response_mixed.json", nil)

	for _, tc := range []struct {
		name    string
		result  *Result
		key     string
		want    ScoreAnswer[severity]
		wantErr error
	}{
		{
			name:   "known key",
			result: mixed,
			key:    "severity",
			want: ScoreAnswer[severity]{
				Score:         1.87,
				Confidence:    0.81,
				Probabilities: map[severity]float64{cosmetic: 0.02, degraded: 0.09, blocking: 0.89},
				Legend: map[severity]Entry{
					cosmetic: "Cosmetic; no impact",
					degraded: "Broken or degraded; workaround exists",
					blocking: "Blocking; no workaround",
				},
			},
		},
		{name: "unknown key", result: mixed, key: "nope", wantErr: ErrAnswerMissing},
		{name: "nil result", result: nil, key: "severity", wantErr: ErrAnswerMissing},
		{name: "crossed kind", result: mixed, key: "is_urgent", wantErr: ErrAnswerKind},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ScoreOf[severity](tc.result, tc.key)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Zero(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}
