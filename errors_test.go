package jev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractMessage(t *testing.T) {
	detailArrBody, err := os.ReadFile("testdata/error_422_detail.json")
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "raw text, not JSON",
			body: "internal server error",
			want: "internal server error",
		},
		{
			name: "error string",
			body: `{"error":"invalid model"}`,
			want: "invalid model",
		},
		{
			name: "error.message",
			body: `{"error":{"message":"invalid model","code":"bad_model"}}`,
			want: "invalid model",
		},
		{
			name: "message",
			body: `{"message":"rate limit exceeded"}`,
			want: "rate limit exceeded",
		},
		{
			name: "detail string",
			body: `{"detail":"not found"}`,
			want: "not found",
		},
		{
			name: "detail.message",
			body: `{"detail":{"message":"forbidden","code":"acl"}}`,
			want: "forbidden",
		},
		{
			name: "detail[] FastAPI style",
			body: string(detailArrBody),
			want: "questions.severity.criteria: field required; model: extra fields not permitted",
		},
		{
			name: "empty body",
			body: "",
			want: "",
		},
		{
			name: "JSON object with none of the known fields",
			body: `{"code":"unknown"}`,
			want: "",
		},
		{
			name: "truncated invalid JSON is treated as raw text",
			body: `{"error":"incomplete`,
			want: `{"error":"incomplete`,
		},
		{
			name: "JSON array body falls back to raw text",
			body: `[1,2,3]`,
			want: `[1,2,3]`,
		},
		{
			name: "JSON string body falls back to raw text",
			body: `"hello"`,
			want: `"hello"`,
		},
		{
			name: "empty detail[] yields no message",
			body: `{"detail":[]}`,
			want: "",
		},
		{
			name: "detail[] with non-object entries yields no message",
			body: `{"detail":[1,2,3]}`,
			want: "",
		},
		{
			name: "detail[] with numeric loc segments",
			body: `{"detail":[{"loc":["body",0,"field"],"msg":"required"}]}`,
			want: "0.field: required",
		},
		{
			name: "detail[] entry without msg is skipped (regression lock for bug 8)",
			body: `{"detail":[{"loc":["body","a"]},{"loc":["body","b"],"msg":"required"}]}`,
			want: "b: required",
		},
		{
			name: "whitespace-only body",
			body: "   \n\t",
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, extractMessage([]byte(tc.body)))
		})
	}
}

func TestAPIError_Is(t *testing.T) {
	cases := []struct {
		name   string
		status int
		target error
		want   bool
	}{
		{name: "400 matches ErrBadRequest", status: 400, target: ErrBadRequest, want: true},
		{name: "400 does not match ErrServer", status: 400, target: ErrServer, want: false},
		{name: "401 matches ErrUnauthorized", status: 401, target: ErrUnauthorized, want: true},
		{name: "403 matches ErrForbidden", status: 403, target: ErrForbidden, want: true},
		{name: "404 matches ErrNotFound", status: 404, target: ErrNotFound, want: true},
		{name: "422 matches ErrUnprocessable", status: 422, target: ErrUnprocessable, want: true},
		{name: "429 matches ErrRateLimited", status: 429, target: ErrRateLimited, want: true},
		{name: "500 matches ErrServer", status: 500, target: ErrServer, want: true},
		{name: "503 matches ErrServer", status: 503, target: ErrServer, want: true},
		{name: "529 matches ErrOverloaded", status: 529, target: ErrOverloaded, want: true},
		{name: "529 also matches ErrServer", status: 529, target: ErrServer, want: true},
		{name: "200 does not match ErrServer", status: 200, target: ErrServer, want: false},
	}

	// 402 and 405 aren't covered by any sentinel: check that explicitly against every one of them.
	allSentinels := []error{
		ErrBadRequest, ErrUnauthorized, ErrForbidden, ErrNotFound,
		ErrUnprocessable, ErrRateLimited, ErrOverloaded, ErrServer,
	}
	for _, status := range []int{402, 405} {
		for _, target := range allSentinels {
			cases = append(cases, struct {
				name   string
				status int
				target error
				want   bool
			}{
				name:   fmt.Sprintf("%d matches no sentinel (checked against %v)", status, target),
				status: status,
				target: target,
				want:   false,
			})
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &APIError{StatusCode: tc.status}
			assert.Equal(t, tc.want, e.Is(tc.target))
			assert.Equal(t, tc.want, errors.Is(e, tc.target))
		})
	}
}

func TestAPIError_Error(t *testing.T) {
	for _, tc := range []struct {
		name   string
		apiErr *APIError
		want   string
	}{
		{
			name: "with request id and message",
			apiErr: &APIError{
				StatusCode: 422,
				Method:     "POST",
				URL:        "https://api.typesafe.ai/v1/systemone",
				Message:    "questions.severity.criteria: field required",
				RequestID:  "req_123",
				Attempts:   1,
			},
			want: "jev: POST https://api.typesafe.ai/v1/systemone: 422 Unprocessable Entity: " +
				"questions.severity.criteria: field required (request id req_123, 1 attempts)",
		},
		{
			name: "without request id",
			apiErr: &APIError{
				StatusCode: 401,
				Method:     "POST",
				URL:        "https://api.typesafe.ai/v1/systemone",
				Message:    "invalid API key",
				Attempts:   1,
			},
			want: "jev: POST https://api.typesafe.ai/v1/systemone: 401 Unauthorized: invalid API key (1 attempts)",
		},
		{
			name: "no message falls back to body, capped",
			apiErr: &APIError{
				StatusCode: 500,
				Method:     "POST",
				URL:        "https://api.typesafe.ai/v1/systemone",
				Body:       []byte("boom"),
				Attempts:   3,
			},
			want: "jev: POST https://api.typesafe.ai/v1/systemone: 500 Internal Server Error: boom (3 attempts)",
		},
		{
			name: "no message falls back to body, capped at maxBodyEcho (regression lock for bug 7)",
			apiErr: &APIError{
				StatusCode: 502,
				Method:     "POST",
				URL:        "https://api.typesafe.ai/v1/systemone",
				Body:       []byte(strings.Repeat("x", 300)),
				Attempts:   1,
			},
			want: "jev: POST https://api.typesafe.ai/v1/systemone: 502 Bad Gateway: " + strings.Repeat("x", 200) + " (1 attempts)",
		},
		{
			name: "long Message field (not Body) is capped the same way",
			apiErr: &APIError{
				StatusCode: 502,
				Method:     "POST",
				URL:        "https://api.typesafe.ai/v1/systemone",
				Message:    strings.Repeat("x", 5000),
				Attempts:   1,
			},
			want: "jev: POST https://api.typesafe.ai/v1/systemone: 502 Bad Gateway: " + strings.Repeat("x", 200) + " (1 attempts)",
		},
		{
			name: "529 uses Overloaded status text (http.StatusText returns empty)",
			apiErr: &APIError{
				StatusCode: 529,
				Method:     "POST",
				URL:        "https://api.typesafe.ai/v1/systemone",
				Attempts:   1,
			},
			want: "jev: POST https://api.typesafe.ai/v1/systemone: 529 Overloaded:  (1 attempts)",
		},
		{
			name: "Authorization header is never echoed",
			apiErr: &APIError{
				StatusCode: 401,
				Method:     "POST",
				URL:        "https://api.typesafe.ai/v1/systemone",
				Header:     http.Header{"Authorization": {"Bearer super-secret-key"}},
				Attempts:   1,
			},
			want: "jev: POST https://api.typesafe.ai/v1/systemone: 401 Unauthorized:  (1 attempts)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.apiErr.Error())
		})
	}
}

func TestTransportError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     *TransportError
		wantErr error
		wantMsg string
	}{
		{
			name:    "wraps context.Canceled",
			err:     &TransportError{Method: "POST", URL: "https://api.typesafe.ai/v1/systemone", Attempts: 3, Err: context.Canceled},
			wantErr: context.Canceled,
			wantMsg: "jev: POST https://api.typesafe.ai/v1/systemone: context canceled (after 3 attempts)",
		},
		{
			name:    "wraps a per-attempt timeout",
			err:     &TransportError{Method: "GET", URL: "https://api.typesafe.ai/v1/models", Attempts: 1, Timeout: true, Err: context.DeadlineExceeded},
			wantErr: context.DeadlineExceeded,
			wantMsg: "jev: GET https://api.typesafe.ai/v1/models: context deadline exceeded (after 1 attempts)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, tc.err, tc.wantErr)
			assert.Equal(t, tc.wantErr, tc.err.Unwrap())
			assert.Equal(t, tc.wantMsg, tc.err.Error())
		})
	}
}

func TestNewAPIError(t *testing.T) {
	now := time.Now()

	for _, tc := range []struct {
		name           string
		resp           *http.Response
		body           []byte
		attempts       int
		wantStatus     int
		wantRequestID  string
		wantMessage    string
		wantRetryAfter time.Duration
		wantIs         error
	}{
		{
			name: "rate limited with retry-after-ms",
			resp: &http.Response{
				StatusCode: 429,
				Header: http.Header{
					"X-Typesafe-Request-Id": {"req_abc"},
					"Retry-After-Ms":        {"1500"},
				},
			},
			body:           []byte(`{"message":"slow down"}`),
			attempts:       2,
			wantStatus:     429,
			wantRequestID:  "req_abc",
			wantMessage:    "slow down",
			wantRetryAfter: 1500 * time.Millisecond,
			wantIs:         ErrRateLimited,
		},
		{
			name: "no request id, no retry-after",
			resp: &http.Response{
				StatusCode: 500,
				Header:     http.Header{},
			},
			body:           []byte("boom"),
			attempts:       1,
			wantStatus:     500,
			wantRequestID:  "",
			wantMessage:    "boom",
			wantRetryAfter: 0,
			wantIs:         ErrServer,
		},
		{
			name: "nil header",
			resp: &http.Response{
				StatusCode: 503,
				Header:     nil,
			},
			body:           []byte("unavailable"),
			attempts:       1,
			wantStatus:     503,
			wantRequestID:  "",
			wantMessage:    "unavailable",
			wantRetryAfter: 0,
			wantIs:         ErrServer,
		},
		{
			name: "invalid Retry-After yields RetryAfter 0",
			resp: &http.Response{
				StatusCode: 429,
				Header:     http.Header{"Retry-After": {"not-a-value"}},
			},
			body:           []byte(`{"message":"slow down"}`),
			attempts:       1,
			wantStatus:     429,
			wantRequestID:  "",
			wantMessage:    "slow down",
			wantRetryAfter: 0,
			wantIs:         ErrRateLimited,
		},
		{
			name: "Retry-After overflowing a Duration yields RetryAfter 0, never negative",
			resp: &http.Response{
				StatusCode: 429,
				Header:     http.Header{"Retry-After": {"1e300"}},
			},
			body:           []byte(`{"message":"slow down"}`),
			attempts:       1,
			wantStatus:     429,
			wantRequestID:  "",
			wantMessage:    "slow down",
			wantRetryAfter: 0,
			wantIs:         ErrRateLimited,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newAPIError("POST", "https://api.typesafe.ai/v1/systemone", tc.resp, tc.body, tc.attempts, now)
			assert.Equal(t, tc.wantStatus, e.StatusCode)
			assert.Equal(t, tc.wantRequestID, e.RequestID)
			assert.Equal(t, tc.wantMessage, e.Message)
			assert.Equal(t, tc.wantRetryAfter, e.RetryAfter)
			assert.Equal(t, tc.attempts, e.Attempts)
			require.ErrorIs(t, e, tc.wantIs)
		})
	}
}
