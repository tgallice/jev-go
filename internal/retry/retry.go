// Package retry holds the pure delay computations; it must not import jev.
package retry

import (
	"context"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ParseRetryAfter reads the delay the server asked for, preferring
// Retry-After-Ms over Retry-After (decimal seconds or an HTTP date). The bool
// reports whether a usable value was found; a date already past yields 0, true.
func ParseRetryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	if h == nil {
		return 0, false
	}

	if raw := strings.TrimSpace(h.Get("Retry-After-Ms")); raw != "" {
		if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms >= 0 && ms <= math.MaxInt64/int64(time.Millisecond) {
			return time.Duration(ms) * time.Millisecond, true
		}
		// Invalid or overflowing retry-after-ms: fall through to Retry-After, like the official SDKs.
	}

	raw := strings.TrimSpace(h.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil {
		if secs < 0 || math.IsNaN(secs) || math.IsInf(secs, 0) || secs > float64(math.MaxInt64)/float64(time.Second) {
			return 0, false
		}
		return time.Duration(secs * float64(time.Second)), true
	}
	if t, err := http.ParseTime(raw); err == nil {
		if d := t.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// Backoff computes min(initial*2^attempt, maxDelay) minus a random fraction of
// up to jitter, and 0 when initial or maxDelay is not positive. A nil rnd draws
// from math/rand/v2.
func Backoff(attempt int, initial, maxDelay time.Duration, jitter float64, rnd func() float64) time.Duration {
	if initial <= 0 || maxDelay <= 0 {
		return 0
	}
	if attempt < 0 {
		attempt = 0
	}
	if rnd == nil {
		rnd = rand.Float64
	}

	// math.Pow keeps large attempts from overflowing a time.Duration before the cap applies.
	d := float64(initial) * math.Pow(2, float64(attempt))
	if d > float64(maxDelay) {
		d = float64(maxDelay)
	}

	factor := 1 - rnd()*jitter
	if factor < 0 {
		// Guards against a caller-supplied rnd or jitter outside [0,1].
		factor = 0
	}
	return time.Duration(d * factor)
}

// Sleep waits for d, or returns ctx.Err() if ctx is already done or becomes
// done first. A non-positive d returns immediately.
func Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
