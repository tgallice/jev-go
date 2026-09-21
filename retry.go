package jev

import (
	"fmt"
	"net/http"
	"time"

	"github.com/tgallice/jev-go/internal/retry"
)

// RetryPolicy governs how a call is retried. It has no magic zero values: a
// zero RetryPolicy retries nothing, so start from DefaultRetryPolicy and
// override fields. Statuses is the one exception, nil meaning the default set
// rather than an empty one. New validates a policy; WithCallRetry does not.
//
//	p := jev.DefaultRetryPolicy()
//	p.MaxRetries = 5
//	p.Statuses = []int{429, 503, 529}
//	client, err := jev.New(jev.WithRetryPolicy(p))
//
//	// Or, for one call only:
//	res, err := client.Evaluate(ctx, req, jev.WithCallRetry(jev.NoRetry()))
type RetryPolicy struct {
	MaxRetries       int           // extra attempts after the first; 0 disables retries
	BackoffInitial   time.Duration // delay before the first retry, doubled at each attempt
	BackoffMax       time.Duration // backoff ceiling, before jitter
	Jitter           float64       // fraction of the backoff randomly subtracted, in [0,1]
	MaxRetryAfter    time.Duration // beyond this, ignore Retry-After and back off; 0 disables it, only a zero delay passing
	IgnoreRetryAfter bool          // never read Retry-After, always back off
	Statuses         []int         // statuses worth retrying; nil means the default set: {408, 429, 500..599}
	ConnectionErrors bool          // also retry network failures and per-attempt timeouts
}

// DefaultRetryPolicy matches the official Python and JS SDKs: 2 extra attempts,
// backoff from 500ms to 5s with 0.25 jitter, Retry-After honored up to 60s, on
// statuses 408, 429 and 5xx plus connection errors.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:       2,
		BackoffInitial:   500 * time.Millisecond,
		BackoffMax:       5 * time.Second,
		Jitter:           0.25,
		MaxRetryAfter:    60 * time.Second,
		ConnectionErrors: true,
	}
}

// NoRetry makes every call a single attempt, connection errors included. Use it
// where the caller already retries at its own level.
func NoRetry() RetryPolicy {
	p := DefaultRetryPolicy()
	p.MaxRetries = 0
	p.ConnectionErrors = false
	return p
}

func (p RetryPolicy) validate() error {
	if p.MaxRetries < 0 {
		return fmt.Errorf("%w: MaxRetries must be >= 0, got %d", ErrInvalidRequest, p.MaxRetries)
	}
	if p.BackoffInitial < 0 || p.BackoffMax < 0 || p.MaxRetryAfter < 0 {
		return fmt.Errorf("%w: retry durations must be >= 0", ErrInvalidRequest)
	}
	if p.Jitter < 0 || p.Jitter > 1 {
		return fmt.Errorf("%w: Jitter must be in [0,1], got %v", ErrInvalidRequest, p.Jitter)
	}
	for _, s := range p.Statuses {
		if s < 100 || s > 999 {
			return fmt.Errorf("%w: invalid status code %d", ErrInvalidRequest, s)
		}
	}
	return nil
}

func (p RetryPolicy) retryableStatus(code int) bool {
	if p.Statuses == nil {
		return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
	}
	for _, s := range p.Statuses {
		if s == code {
			return true
		}
	}
	return false
}

// delay bounds worst-case wait time: a Retry-After beyond MaxRetryAfter is ignored in favor of backoff.
func (p RetryPolicy) delay(attempt int, h http.Header, now time.Time, rnd func() float64) time.Duration {
	if !p.IgnoreRetryAfter && h != nil {
		if d, ok := retry.ParseRetryAfter(h, now); ok && d >= 0 && d <= p.MaxRetryAfter {
			return d
		}
	}
	return retry.Backoff(attempt, p.BackoffInitial, p.BackoffMax, p.Jitter, rnd)
}
