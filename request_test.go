package jev

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAskInfersAnswerType is a compile-time proof, kept outside the table
// pattern: Ask must infer the answer type from the question alone, with no type
// argument at the call site, for a user-defined ~string and ~int.
func TestAskInfersAnswerType(t *testing.T) {
	req := &Request{State: ticket{Subject: "API down", Body: "500s"}}

	department := Ask(req, "department", Choice("Which team?", map[dept]Entry{
		billing:   "Payments",
		technical: "Bugs",
	}))
	sev := Ask(req, "severity", ScoreMap("How severe?", map[severity]Entry{
		cosmetic: "Cosmetic",
		degraded: "Degraded",
		blocking: "Blocking",
	}))
	urgent := Ask(req, "is_urgent", Noul("Urgent?").WithCriteria("yes", "no"))
	tone := Ask(req, "tone", Choice("Tone?", Options("calm", "angry")))
	scale := Ask(req, "scale", Score[severity]("How severe?", "low", "high"))

	var _ Key[ChoiceAnswer[dept]] = department
	var _ Key[ScoreAnswer[severity]] = sev
	var _ Key[NoulAnswer] = urgent
	var _ Key[ChoiceAnswer[string]] = tone
	var _ Key[ScoreAnswer[severity]] = scale

	require.Len(t, req.Questions, 5)
}

func TestAsk(t *testing.T) {
	for _, tc := range []struct {
		name         string
		req          *Request
		key          string
		question     TypedQuestion[NoulAnswer]
		wantQuestion Question
		wantLen      int
	}{
		{
			name:         "allocates the map",
			req:          &Request{State: "x"},
			key:          "urgent",
			question:     Noul("Urgent?"),
			wantQuestion: Noul("Urgent?"),
			wantLen:      1,
		},
		{
			name:         "adds to an existing map",
			req:          &Request{State: "x", Questions: map[string]Question{"tone": Noul("Calm?")}},
			key:          "urgent",
			question:     Noul("Urgent?"),
			wantQuestion: Noul("Urgent?"),
			wantLen:      2,
		},
		{
			name:         "replaces an existing key",
			req:          &Request{State: "x", Questions: map[string]Question{"urgent": Noul("Calm?")}},
			key:          "urgent",
			question:     Noul("Urgent?"),
			wantQuestion: Noul("Urgent?"),
			wantLen:      1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := Ask(tc.req, tc.key, tc.question)
			assert.Equal(t, tc.key, key.Name())
			assert.Len(t, tc.req.Questions, tc.wantLen)
			assert.Equal(t, tc.wantQuestion, tc.req.Questions[tc.key])
		})
	}
}

func TestKeyAnswer(t *testing.T) {
	mixed := loadResult(t, "response_mixed.json", nil)
	emptyKeyed, err := parseResult(nil, []byte(`{"answers":{"":{"type":"noul","noul":1}}}`), nil)
	require.NoError(t, err)
	typed := Ask(&Request{State: "x"}, "department", Choice("Which team?", map[dept]Entry{billing: nil, technical: nil}))

	for _, tc := range []struct {
		name    string
		key     Key[NoulAnswer]
		result  *Result
		want    NoulAnswer
		wantErr error
	}{
		{
			name:   "registered key",
			key:    Ask(&Request{State: "x"}, "is_urgent", Noul("Urgent?")),
			result: mixed,
			want:   NoulAnswer{Noul: 0.91},
		},
		{
			name:    "key absent from the result",
			key:     Ask(&Request{State: "x"}, "absent", Noul("Urgent?")),
			result:  mixed,
			wantErr: ErrAnswerMissing,
		},
		{
			name:    "nil result",
			key:     Ask(&Request{State: "x"}, "is_urgent", Noul("Urgent?")),
			result:  nil,
			wantErr: ErrAnswerMissing,
		},
		{
			name:    "crossed kind",
			key:     Ask(&Request{State: "x"}, "department", Noul("Urgent?")),
			result:  mixed,
			wantErr: ErrAnswerKind,
		},
		{
			name:    "zero key on a result holding that answer",
			key:     Key[NoulAnswer]{},
			result:  emptyKeyed,
			wantErr: ErrInvalidRequest,
		},
		{
			name:    "zero key on an unrelated result",
			key:     Key[NoulAnswer]{},
			result:  mixed,
			wantErr: ErrInvalidRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.key.Answer(tc.result)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Zero(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}

	// A typed key decodes through the question it was built from.
	answer, err := typed.Answer(mixed)
	require.NoError(t, err)
	assert.Equal(t, technical, answer.Choice)
}

// quickstartRequest mirrors the end-to-end example of the architecture document.
func quickstartRequest() *Request {
	req := &Request{State: ticket{Subject: "API down", Body: "500s since 20 minutes"}}
	Ask(req, "department", Choice("Which team should handle `body`?", map[dept]Entry{
		billing:   "Payments, invoicing, refunds",
		technical: map[string]any{"what": "Bugs, outages, integrations", "examples": []string{"500 errors", "webhook failing"}},
		sales:     nil,
	}))
	Ask(req, "severity", ScoreMap("How severe is the issue described in `body`?", map[severity]Entry{
		cosmetic: "Cosmetic; no impact",
		degraded: "Broken or degraded; workaround exists",
		blocking: "Blocking; no workaround",
	}))
	Ask(req, "is_urgent", Noul("Does `body` convey urgency?").
		WithCriteria("Explicitly time-sensitive", "No urgency expressed"))
	Ask(req, "tone", Choice("Tone of `body`?", Options("calm", "frustrated", "angry")))
	return req
}

func noulRequest(req *Request) *Request {
	Ask(req, "urgent", Noul("Urgent?"))
	return req
}

func TestMarshalRequest(t *testing.T) {
	const quickstartGolden = `{
	  "state": {"subject":"API down","body":"500s since 20 minutes"},
	  "model": "jev-latest",
	  "questions": {
	    "department": {"type":"choice","instructions":"Which team should handle ` + "`body`" + `?","criteria":{"billing":"Payments, invoicing, refunds","sales":null,"technical":{"examples":["500 errors","webhook failing"],"what":"Bugs, outages, integrations"}}},
	    "is_urgent": {"type":"noul","instructions":"Does ` + "`body`" + ` convey urgency?","criteria":{"true":"Explicitly time-sensitive","false":"No urgency expressed"}},
	    "severity": {"type":"score","instructions":"How severe is the issue described in ` + "`body`" + `?","criteria":["Cosmetic; no impact","Broken or degraded; workaround exists","Blocking; no workaround"]},
	    "tone": {"type":"choice","instructions":"Tone of ` + "`body`" + `?","criteria":{"angry":null,"calm":null,"frustrated":null}}
	  }
	}`

	for _, tc := range []struct {
		name            string
		req             *Request
		model           string
		want            string
		wantErr         error
		wantErrContains string
	}{
		{name: "quickstart batch", req: quickstartRequest(), model: "jev-latest", want: quickstartGolden},
		{
			name:  "model argument wins over the request field",
			req:   noulRequest(&Request{State: "x", Model: "jev-preview"}),
			model: "jev-1.13.0",
			want:  `{"state":"x","model":"jev-1.13.0","questions":{"urgent":{"type":"noul","instructions":"Urgent?"}}}`,
		},
		{
			name: "model omitted when empty",
			req:  noulRequest(&Request{State: "x"}),
			want: `{"state":"x","questions":{"urgent":{"type":"noul","instructions":"Urgent?"}}}`,
		},
		{
			name:  "extra fields are merged over the body",
			req:   noulRequest(&Request{State: "x", Extra: map[string]any{"model": "jev-preview", "experimental_ab": true, "tags": []string{"support"}}}),
			model: "jev-latest",
			want:  `{"state":"x","model":"jev-preview","experimental_ab":true,"tags":["support"],"questions":{"urgent":{"type":"noul","instructions":"Urgent?"}}}`,
		},
		{
			name:  "raw question forwarded untouched",
			req:   &Request{State: "x", Questions: map[string]Question{"future": RawQuestion(`{"type":"rank","items":["a","b"]}`)}},
			model: "jev-latest",
			want:  `{"state":"x","model":"jev-latest","questions":{"future":{"type":"rank","items":["a","b"]}}}`,
		},
		{
			name:            "extra field overwriting state",
			req:             noulRequest(&Request{State: "x", Extra: map[string]any{"state": 1}}),
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: `extra field "state" would overwrite the request body`,
		},
		{
			name:            "extra field overwriting questions",
			req:             noulRequest(&Request{State: "x", Extra: map[string]any{"questions": "x"}}),
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: `extra field "questions" would overwrite the request body`,
		},
		{
			name:            "nil request",
			req:             nil,
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: "nil request",
		},
		{
			name:            "nil state",
			req:             noulRequest(&Request{}),
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: "state: ",
		},
		{
			name:            "scalar state",
			req:             noulRequest(&Request{State: 42}),
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: "state: ",
		},
		{
			name:            "no question",
			req:             &Request{State: "x"},
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: "at least one question is required",
		},
		{
			name:            "empty question key",
			req:             &Request{State: "x", Questions: map[string]Question{"": Noul("Urgent?")}},
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: "question key must not be empty",
		},
		{
			name:            "nil question",
			req:             &Request{State: "x", Questions: map[string]Question{"urgent": nil}},
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: `question "urgent" is nil`,
		},
		{
			name:            "invalid question",
			req:             &Request{State: "x", Questions: map[string]Question{"tone": Choice[dept]("x", nil)}},
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: `question "tone": jev: invalid request: choice needs 1 to 255 options, got 0`,
		},
		{
			name: "deferred score map error",
			req: &Request{State: "x", Questions: map[string]Question{
				"severity": ScoreMap("x", map[severity]Entry{cosmetic: "a", blocking: "b"}),
			}},
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: `question "severity": jev: invalid request: score levels must be keyed 0..1, got 2`,
		},
		{
			name:            "unmarshalable extra field",
			req:             noulRequest(&Request{State: "x", Extra: map[string]any{"bad": make(chan int)}}),
			model:           "jev-latest",
			wantErr:         ErrInvalidRequest,
			wantErrContains: `extra field "bad"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := marshalRequest(tc.req, tc.model)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				assert.Nil(t, raw)
				return
			}
			assert.JSONEq(t, tc.want, string(raw))
		})
	}
}

func TestRequestMarshalJSON(t *testing.T) {
	const golden = `{"state":"x","model":"jev-preview","questions":{"urgent":{"type":"noul","instructions":"Urgent?"}}}`

	for _, tc := range []struct {
		name    string
		value   any
		want    string
		wantErr error
	}{
		{
			name:  "pointer uses the request model field",
			value: noulRequest(&Request{State: "x", Model: "jev-preview"}),
			want:  golden,
		},
		{
			name:  "value receiver keeps the custom encoding",
			value: *noulRequest(&Request{State: "x", Model: "jev-preview"}),
			want:  golden,
		},
		{
			name: "embedded by value keeps the custom encoding",
			value: struct {
				Request
			}{*noulRequest(&Request{State: "x", Model: "jev-preview"})},
			want: golden,
		},
		{
			name:    "reports validation errors",
			value:   &Request{State: "x"},
			wantErr: ErrInvalidRequest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Nil(t, raw)
				return
			}
			assert.JSONEq(t, tc.want, string(raw))
		})
	}
}
