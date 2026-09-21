package jev

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withPolicy(p RetryPolicy, mutate func(*RetryPolicy)) RetryPolicy {
	mutate(&p)
	return p
}

func TestRetryPolicy_Validate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		policy  RetryPolicy
		wantErr error
	}{
		{name: "default is valid", policy: DefaultRetryPolicy()},
		{name: "no retry is valid", policy: NoRetry()},
		{name: "negative MaxRetries", policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.MaxRetries = -1 }), wantErr: ErrInvalidRequest},
		{name: "jitter above 1", policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.Jitter = 1.5 }), wantErr: ErrInvalidRequest},
		{name: "jitter below 0", policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.Jitter = -0.1 }), wantErr: ErrInvalidRequest},
		{name: "invalid status", policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.Statuses = []int{42} }), wantErr: ErrInvalidRequest},
		{name: "negative backoff", policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.BackoffInitial = -time.Second }), wantErr: ErrInvalidRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, tc.policy.validate(), tc.wantErr)
		})
	}
}

func TestRetryPolicy_RetryableStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy RetryPolicy
		status int
		want   bool
	}{
		{name: "default: 408 retryable", policy: DefaultRetryPolicy(), status: 408, want: true},
		{name: "default: 429 retryable", policy: DefaultRetryPolicy(), status: 429, want: true},
		{name: "default: 500 retryable", policy: DefaultRetryPolicy(), status: 500, want: true},
		{name: "default: 529 retryable", policy: DefaultRetryPolicy(), status: 529, want: true},
		{name: "default: 400 not retryable", policy: DefaultRetryPolicy(), status: 400, want: false},
		{name: "default: 404 not retryable", policy: DefaultRetryPolicy(), status: 404, want: false},
		{name: "default: 599 retryable", policy: DefaultRetryPolicy(), status: 599, want: true},
		{name: "default: 600 not retryable", policy: DefaultRetryPolicy(), status: 600, want: false},
		{
			name:   "custom statuses: listed status retryable",
			policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.Statuses = []int{503} }),
			status: 503,
			want:   true,
		},
		{
			name:   "custom statuses: unlisted status not retryable",
			policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.Statuses = []int{503} }),
			status: 500,
			want:   false,
		},
		{
			name:   "explicitly empty Statuses disables all status-based retry",
			policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.Statuses = []int{} }),
			status: 500,
			want:   false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.policy.retryableStatus(tc.status))
		})
	}
}

func TestRetryPolicy_Delay(t *testing.T) {
	now := time.Now()
	zeroRnd := func() float64 { return 0 }
	defaultInitial := DefaultRetryPolicy().BackoffInitial

	for _, tc := range []struct {
		name    string
		policy  RetryPolicy
		header  http.Header
		attempt int
		want    time.Duration
	}{
		{
			name:   "prefers Retry-After under the cap",
			policy: DefaultRetryPolicy(),
			header: http.Header{"Retry-After-Ms": {"1234"}},
			want:   1234 * time.Millisecond,
		},
		{
			name:   "falls back to backoff above the cap",
			policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.MaxRetryAfter = time.Second }),
			header: http.Header{"Retry-After": {"120"}},
			want:   defaultInitial,
		},
		{
			name:   "no header falls back to backoff",
			policy: DefaultRetryPolicy(),
			header: nil,
			want:   defaultInitial,
		},
		{
			name:   "IgnoreRetryAfter always uses backoff",
			policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.IgnoreRetryAfter = true }),
			header: http.Header{"Retry-After-Ms": {"1"}},
			want:   defaultInitial,
		},
		{
			name:   "invalid header falls back to backoff",
			policy: DefaultRetryPolicy(),
			header: http.Header{"Retry-After": {"not-a-value"}},
			want:   defaultInitial,
		},
		{
			name:   "Retry-After exactly at MaxRetryAfter is accepted",
			policy: DefaultRetryPolicy(),
			header: http.Header{"Retry-After-Ms": {"60000"}}, // == default MaxRetryAfter
			want:   60 * time.Second,
		},
		{
			name:   "MaxRetryAfter 0 rejects any positive Retry-After",
			policy: withPolicy(DefaultRetryPolicy(), func(p *RetryPolicy) { p.MaxRetryAfter = 0 }),
			header: http.Header{"Retry-After-Ms": {"1"}},
			want:   defaultInitial,
		},
		{
			name:    "attempt > 0 grows the fallback backoff",
			policy:  DefaultRetryPolicy(),
			header:  nil,
			attempt: 2,
			want:    2000 * time.Millisecond, // 500ms * 2^2, zero jitter
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.policy.delay(tc.attempt, tc.header, now, zeroRnd)
			assert.Equal(t, tc.want, got)
		})
	}
}
