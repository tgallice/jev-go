package jev

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonObject and jsonNumber cover Entry values that decide their own JSON.
type jsonObject struct{}

func (jsonObject) MarshalJSON() ([]byte, error) { return []byte(`{"kind":"custom"}`), nil }

type jsonNumber struct{}

func (jsonNumber) MarshalJSON() ([]byte, error) { return []byte(`7`), nil }

type ticket struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

func TestMarshalEntry(t *testing.T) {
	for _, tc := range []struct {
		name            string
		entry           Entry
		allowNil        bool
		want            string
		wantErr         error
		wantErrContains string
	}{
		{name: "string", entry: "hello", want: `"hello"`},
		{name: "empty string", entry: "", want: `""`},
		{name: "struct", entry: ticket{Subject: "API down", Body: "500s"}, want: `{"subject":"API down","body":"500s"}`},
		{name: "map", entry: map[string]any{"what": "outage", "count": 3}, want: `{"count":3,"what":"outage"}`},
		{name: "slice", entry: []string{"500 errors", "webhook failing"}, want: `["500 errors","webhook failing"]`},
		{name: "nested scalars allowed", entry: map[string]any{"ok": true, "n": 1.5}, want: `{"n":1.5,"ok":true}`},
		{name: "raw message", entry: json.RawMessage(`{"a": 1}`), want: `{"a":1}`},
		{name: "marshaler producing an object", entry: jsonObject{}, want: `{"kind":"custom"}`},
		{
			name:            "marshaler producing a number",
			entry:           jsonNumber{},
			wantErr:         ErrInvalidRequest,
			wantErrContains: "got 7",
		},
		{
			name:            "byte slice would be base64",
			entry:           []byte("ab"),
			allowNil:        true,
			wantErr:         ErrInvalidRequest,
			wantErrContains: "pass a string",
		},
		{name: "nil allowed", entry: nil, allowNil: true, want: `null`},
		{name: "nil rejected", entry: nil, wantErr: ErrInvalidRequest, wantErrContains: "got null"},
		{name: "integer rejected", entry: 42, wantErr: ErrInvalidRequest, wantErrContains: "got 42"},
		{name: "float rejected", entry: 1.5, allowNil: true, wantErr: ErrInvalidRequest, wantErrContains: "got 1.5"},
		{name: "bool rejected", entry: true, wantErr: ErrInvalidRequest, wantErrContains: "got true"},
		{name: "unmarshalable", entry: make(chan int), wantErr: ErrInvalidRequest, wantErrContains: "unsupported type"},
		{
			name:            "long scalar truncated",
			entry:           json.RawMessage(strings.Repeat("1", 100)),
			wantErr:         ErrInvalidRequest,
			wantErrContains: strings.Repeat("1", 64) + "...",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := marshalEntry(tc.entry, tc.allowNil)
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

func TestSnippet(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
		want string
	}{
		{name: "empty", raw: nil, want: ""},
		{name: "short", raw: []byte("abc"), want: "abc"},
		{name: "exactly at the limit", raw: []byte(strings.Repeat("x", 64)), want: strings.Repeat("x", 64)},
		{name: "over the limit", raw: []byte(strings.Repeat("x", 100)), want: strings.Repeat("x", 64) + "..."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, snippet(tc.raw))
		})
	}
}
