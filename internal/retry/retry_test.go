package retry

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name   string
		header http.Header
		want   time.Duration
		wantOK bool
	}{
		{
			name:   "ms header takes priority",
			header: http.Header{"Retry-After-Ms": {"1500"}, "Retry-After": {"30"}},
			want:   1500 * time.Millisecond,
			wantOK: true,
		},
		{
			name:   "decimal seconds",
			header: http.Header{"Retry-After": {"2.5"}},
			want:   2500 * time.Millisecond,
			wantOK: true,
		},
		{
			name:   "past HTTP date clamps to zero",
			header: http.Header{"Retry-After": {now.Add(-1 * time.Hour).Format(http.TimeFormat)}},
			want:   0,
			wantOK: true,
		},
		{
			name:   "future HTTP date",
			header: http.Header{"Retry-After": {now.Add(90 * time.Second).Format(http.TimeFormat)}},
			want:   90 * time.Second,
			wantOK: true,
		},
		{
			name:   "invalid value",
			header: http.Header{"Retry-After": {"not-a-value"}},
			wantOK: false,
		},
		{
			name:   "negative seconds",
			header: http.Header{"Retry-After": {"-5"}},
			wantOK: false,
		},
		{
			name:   "negative ms",
			header: http.Header{"Retry-After-Ms": {"-5"}},
			wantOK: false,
		},
		{
			name:   "absent",
			header: http.Header{},
			wantOK: false,
		},
		{
			name:   "nil header",
			header: nil,
			wantOK: false,
		},
		{
			name:   "seconds NaN is rejected",
			header: http.Header{"Retry-After": {"NaN"}},
			wantOK: false,
		},
		{
			name:   "seconds +Inf is rejected",
			header: http.Header{"Retry-After": {"+Inf"}},
			wantOK: false,
		},
		{
			name:   "seconds too large to fit a Duration is rejected",
			header: http.Header{"Retry-After": {"1e300"}},
			wantOK: false,
		},
		{
			name:   "ms overflowing a Duration is rejected",
			header: http.Header{"Retry-After-Ms": {"9223372036854775807"}},
			wantOK: false,
		},
		{
			name:   "invalid ms falls back to a valid Retry-After",
			header: http.Header{"Retry-After-Ms": {"abc"}, "Retry-After": {"5"}},
			want:   5 * time.Second,
			wantOK: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseRetryAfter(tc.header, now)
			assert.Equal(t, tc.wantOK, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestBackoff(t *testing.T) {
	initial := 500 * time.Millisecond
	maxDelay := 5 * time.Second
	zero := func() float64 { return 0 }
	one := func() float64 { return 1 }

	for _, tc := range []struct {
		name     string
		attempt  int
		initial  time.Duration
		maxDelay time.Duration
		jitter   float64
		rnd      func() float64
		wantMin  time.Duration
		wantMax  time.Duration
	}{
		{name: "attempt 0, no jitter", attempt: 0, initial: initial, maxDelay: maxDelay, jitter: 0.25, rnd: zero, wantMin: 500 * time.Millisecond, wantMax: 500 * time.Millisecond},
		{name: "attempt 1, no jitter", attempt: 1, initial: initial, maxDelay: maxDelay, jitter: 0.25, rnd: zero, wantMin: 1000 * time.Millisecond, wantMax: 1000 * time.Millisecond},
		{name: "attempt 2, no jitter", attempt: 2, initial: initial, maxDelay: maxDelay, jitter: 0.25, rnd: zero, wantMin: 2000 * time.Millisecond, wantMax: 2000 * time.Millisecond},
		{name: "attempt 3, no jitter", attempt: 3, initial: initial, maxDelay: maxDelay, jitter: 0.25, rnd: zero, wantMin: 4000 * time.Millisecond, wantMax: 4000 * time.Millisecond},
		{name: "attempt 4 caps at max", attempt: 4, initial: initial, maxDelay: maxDelay, jitter: 0.25, rnd: zero, wantMin: maxDelay, wantMax: maxDelay}, // 500*2^4 = 8s > 5s
		{name: "attempt 2000 caps at max instead of overflowing", attempt: 2000, initial: initial, maxDelay: maxDelay, jitter: 0.25, rnd: zero, wantMin: maxDelay, wantMax: maxDelay},
		{name: "jitter 0 keeps full value", attempt: 2, initial: initial, maxDelay: maxDelay, jitter: 0, rnd: one, wantMin: 2000 * time.Millisecond, wantMax: 2000 * time.Millisecond},
		{name: "jitter 1 with rnd=1 collapses to zero", attempt: 2, initial: initial, maxDelay: maxDelay, jitter: 1, rnd: one, wantMin: 0, wantMax: 0},
		{name: "jitter 1 with rnd=0 keeps full value", attempt: 2, initial: initial, maxDelay: maxDelay, jitter: 1, rnd: zero, wantMin: 2000 * time.Millisecond, wantMax: 2000 * time.Millisecond},
		{name: "negative attempt behaves like 0", attempt: -3, initial: initial, maxDelay: maxDelay, jitter: 0.25, rnd: zero, wantMin: 500 * time.Millisecond, wantMax: 500 * time.Millisecond},
		{name: "zero initial short-circuits to 0", attempt: 1, initial: 0, maxDelay: maxDelay, jitter: 0.25, rnd: zero, wantMin: 0, wantMax: 0},
		{name: "zero maxDelay short-circuits to 0", attempt: 1, initial: initial, maxDelay: 0, jitter: 0.25, rnd: zero, wantMin: 0, wantMax: 0},
		{name: "maxDelay below initial clamps down", attempt: 0, initial: initial, maxDelay: 100 * time.Millisecond, jitter: 0.25, rnd: zero, wantMin: 100 * time.Millisecond, wantMax: 100 * time.Millisecond},
		// nil rnd defaults to math/rand/v2, whose output is non-deterministic: bound it instead of pinning an exact value.
		{name: "nil rnd defaults to math/rand/v2, bounded by jitter", attempt: 1, initial: initial, maxDelay: maxDelay, jitter: 0.25, rnd: nil, wantMin: 750 * time.Millisecond, wantMax: 1000 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Backoff(tc.attempt, tc.initial, tc.maxDelay, tc.jitter, tc.rnd)
			assert.GreaterOrEqual(t, got, tc.wantMin)
			assert.LessOrEqual(t, got, tc.wantMax)
		})
	}
}

func TestSleep(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ctx     func() context.Context
		d       time.Duration
		wantErr error
	}{
		{
			name: "completes after duration",
			ctx:  func() context.Context { return context.Background() },
			d:    time.Millisecond,
		},
		{
			name: "zero duration returns immediately",
			ctx:  func() context.Context { return context.Background() },
			d:    0,
		},
		{
			name: "negative duration returns immediately",
			ctx:  func() context.Context { return context.Background() },
			d:    -time.Second,
		},
		{
			name: "cancelled context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			d:       time.Second,
			wantErr: context.Canceled,
		},
		{
			name: "cancelled context with zero duration still reports cancellation",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			d:       0,
			wantErr: context.Canceled,
		},
		{
			name: "context cancelled during the wait",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				go func() {
					time.Sleep(10 * time.Millisecond)
					cancel()
				}()
				return ctx
			},
			d:       time.Minute,
			wantErr: context.Canceled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Sleep(tc.ctx(), tc.d)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}
