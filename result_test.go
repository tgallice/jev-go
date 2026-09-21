package jev

import (
	"bytes"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, file string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + file)
	require.NoError(t, err)
	return body
}

func loadResult(t *testing.T, file string, logger *slog.Logger) *Result {
	t.Helper()
	res, err := parseResult(okResponse(), fixture(t, file), logger)
	require.NoError(t, err)
	return res
}

func okResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Typesafe-Request-Id": []string{"req_01HZX"}},
	}
}

func TestParseResult(t *testing.T) {
	unknownLogs := &bytes.Buffer{}
	warn := slog.New(slog.NewTextHandler(unknownLogs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	mixedBody := fixture(t, "response_mixed.json")
	unknownBody := fixture(t, "response_unknown_kind.json")

	for _, tc := range []struct {
		name            string
		body            []byte
		resp            *http.Response
		logger          *slog.Logger
		logs            *bytes.Buffer
		wantModel       string
		wantUsage       Usage
		wantRequestID   string
		wantStatus      int
		wantKinds       map[string]Kind
		wantLog         []string
		wantErr         error
		wantErrContains string
	}{
		{
			name:          "mixed batch",
			body:          mixedBody,
			resp:          okResponse(),
			wantModel:     "jev-1.13.0",
			wantUsage:     Usage{InputTokens: 412, OutputTokens: 37},
			wantRequestID: "req_01HZX",
			wantStatus:    http.StatusOK,
			wantKinds: map[string]Kind{
				"department": KindChoice,
				"severity":   KindScore,
				"is_urgent":  KindNoul,
				"tone":       KindChoice,
			},
		},
		{
			name:          "unknown kind is kept and logged",
			body:          unknownBody,
			resp:          okResponse(),
			logger:        warn,
			logs:          unknownLogs,
			wantModel:     "jev-1.13.0",
			wantUsage:     Usage{InputTokens: 118, OutputTokens: 12},
			wantRequestID: "req_01HZX",
			wantStatus:    http.StatusOK,
			wantKinds:     map[string]Kind{"is_urgent": KindNoul, "ordering": Kind("rank")},
			wantLog:       []string{"unknown answer type", "key=ordering", "type=rank"},
		},
		{
			name:      "without http response",
			body:      []byte(`{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":1}}}`),
			wantModel: "jev-1.13.0",
			wantKinds: map[string]Kind{"a": KindNoul},
		},
		{
			name:            "broken json",
			body:            []byte(`{`),
			wantErr:         ErrInvalidResponse,
			wantErrContains: "unexpected end",
		},
		{
			name:            "answers missing",
			body:            []byte(`{"model":"m"}`),
			wantErr:         ErrInvalidResponse,
			wantErrContains: `response without "answers" object`,
		},
		{
			name:            "answers not an object",
			body:            []byte(`{"answers":[]}`),
			wantErr:         ErrInvalidResponse,
			wantErrContains: "cannot unmarshal",
		},
		{
			name:            "answer without type",
			body:            []byte(`{"answers":{"a":{"noul":1}}}`),
			wantErr:         ErrInvalidResponse,
			wantErrContains: `answer "a" has no "type" field`,
		},
		{
			name:            "answer with an empty type",
			body:            []byte(`{"answers":{"a":{"type":""}}}`),
			wantErr:         ErrInvalidResponse,
			wantErrContains: `answer "a" has no "type" field`,
		},
		{
			name:            "answer not an object",
			body:            []byte(`{"answers":{"a":42}}`),
			wantErr:         ErrInvalidResponse,
			wantErrContains: `answer "a" has no "type" field`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := parseResult(tc.resp, tc.body, tc.logger)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Contains(t, err.Error(), tc.wantErrContains)
				assert.Nil(t, res)
				return
			}

			assert.Equal(t, tc.wantModel, res.Model)
			assert.Equal(t, tc.wantUsage, res.Usage)
			assert.Equal(t, tc.wantRequestID, res.RequestID)
			assert.Equal(t, tc.wantStatus, res.StatusCode)
			assert.Equal(t, tc.wantKinds, res.kinds)
			assert.Equal(t, tc.body, res.RawBody())
			for _, want := range tc.wantLog {
				assert.Contains(t, tc.logs.String(), want)
			}
		})
	}
}

func TestResultKeys(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *Result
		want   []string
	}{
		{
			name:   "sorted",
			result: loadResult(t, "response_mixed.json", nil),
			want:   []string{"department", "is_urgent", "severity", "tone"},
		},
		{
			name:   "unknown kinds are listed too",
			result: loadResult(t, "response_unknown_kind.json", nil),
			want:   []string{"is_urgent", "ordering"},
		},
		{name: "no answer", result: &Result{}, want: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.result.Keys())
		})
	}
}

func TestResultKind(t *testing.T) {
	mixed := loadResult(t, "response_mixed.json", nil)
	unknown := loadResult(t, "response_unknown_kind.json", nil)

	for _, tc := range []struct {
		name   string
		result *Result
		key    string
		want   Kind
		wantOK bool
	}{
		{name: "choice", result: mixed, key: "department", want: KindChoice, wantOK: true},
		{name: "score", result: mixed, key: "severity", want: KindScore, wantOK: true},
		{name: "noul", result: mixed, key: "is_urgent", want: KindNoul, wantOK: true},
		{name: "unknown to the sdk", result: unknown, key: "ordering", want: Kind("rank"), wantOK: true},
		{name: "unknown key", result: mixed, key: "nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.result.Kind(tc.key)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResultRawAnswer(t *testing.T) {
	mixed := loadResult(t, "response_mixed.json", nil)
	unknown := loadResult(t, "response_unknown_kind.json", nil)

	for _, tc := range []struct {
		name   string
		result *Result
		key    string
		want   string
		wantOK bool
	}{
		{name: "known kind", result: mixed, key: "is_urgent", want: `{"type":"noul","noul":0.91}`, wantOK: true},
		{
			name:   "unknown kind stays reachable",
			result: unknown,
			key:    "ordering",
			want:   `{"type":"rank","rank":["b","a","c"],"confidence":0.66}`,
			wantOK: true,
		},
		{name: "unknown key", result: mixed, key: "nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, ok := tc.result.RawAnswer(tc.key)
			require.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				assert.Nil(t, raw)
				return
			}
			assert.JSONEq(t, tc.want, string(raw))
		})
	}
}

func TestResultRawBody(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *Result
		want   []byte
	}{
		{name: "full body", result: loadResult(t, "response_mixed.json", nil), want: fixture(t, "response_mixed.json")},
		{name: "no body", result: &Result{}, want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.result.RawBody()
			assert.Equal(t, tc.want, got)

			// The copy must not be shared with the result.
			for i := range got {
				got[i] = 'X'
			}
			assert.Equal(t, tc.want, tc.result.RawBody())
		})
	}
}

// TestResultConcurrentReads is a single scenario, meant to be run under -race.
func TestResultConcurrentReads(t *testing.T) {
	res := loadResult(t, "response_mixed.json", nil)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.Len(t, res.Keys(), 4)
			_, _ = res.Kind("severity")
			_, _ = res.RawAnswer("severity")
			_ = res.RawBody()
			_, err := ScoreOf[severity](res, "severity")
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
}
