package jev

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tgallice/jev-go/internal/retry"
)

// Sentinels for the failures this SDK detects itself, without or before an HTTP
// status. Match them with errors.Is.
var (
	ErrMissingAPIKey   = errors.New("jev: missing API key (WithAPIKey or TYPESAFE_API_KEY)") // New found no key
	ErrInvalidRequest  = errors.New("jev: invalid request")                                  // local validation failed, nothing was sent
	ErrInvalidResponse = errors.New("jev: invalid response")                                 // the body is not the shape the API documents
	ErrAnswerMissing   = errors.New("jev: answer missing")                                   // no answer came back under that key
	ErrAnswerKind      = errors.New("jev: answer type mismatch")                             // the answer is of another kind than the question
)

// Sentinels for HTTP statuses, matched with errors.Is against an *APIError.
// Status 529 satisfies both ErrOverloaded and ErrServer. Of these, 429 and the
// 5xx family are the ones the default RetryPolicy retries.
var (
	ErrBadRequest    = errors.New("jev: bad request (400)")
	ErrUnauthorized  = errors.New("jev: unauthorized (401)")
	ErrForbidden     = errors.New("jev: forbidden (403)")
	ErrNotFound      = errors.New("jev: not found (404)")
	ErrUnprocessable = errors.New("jev: unprocessable entity (422)")
	ErrRateLimited   = errors.New("jev: rate limited (429)")
	ErrOverloaded    = errors.New("jev: overloaded (529)")
	ErrServer        = errors.New("jev: server error (5xx)")
)

// maxBodyEcho bounds how much of a body without an extracted message ends
// up in Error(), so a large or binary body never dominates the log line.
const maxBodyEcho = 200

// APIError is a non-2xx HTTP response, returned once retries are exhausted.
// Match its status with errors.Is against the sentinels above, and open it with
// errors.As for the body, the request id and RetryAfter. The API does not
// document its error body format, so Message is extracted heuristically and may
// well be empty even when Body is not.
//
//	var apiErr *jev.APIError
//	switch {
//	case errors.Is(err, jev.ErrRateLimited):
//		errors.As(err, &apiErr)
//		backOffFor(apiErr.RetryAfter)
//	case errors.As(err, &apiErr):
//		log.Printf("api %d: %s (request %s)", apiErr.StatusCode, apiErr.Message, apiErr.RequestID)
//	}
type APIError struct {
	StatusCode int
	Method     string
	URL        string // the endpoint called, never carrying credentials
	Message    string // extracted from the body, "" when none could be found
	Body       []byte // the body as received
	Header     http.Header
	RequestID  string        // x-typesafe-request-id, "" when absent
	RetryAfter time.Duration // from the Retry-After headers, 0 when absent or unparsable
	Attempts   int           // attempts made, the first one included
}

// Error renders the method, endpoint, status, message and request id. It never
// includes the API key, and truncates the message to 200 bytes so a large or
// binary body cannot dominate a log line; Message itself stays uncapped for
// inspection through errors.As.
func (e *APIError) Error() string {
	status := http.StatusText(e.StatusCode)
	if status == "" && e.StatusCode == 529 {
		// http.StatusText(529) returns "" since 529 isn't in the IANA registry.
		status = "Overloaded"
	}

	msg := e.Message
	if msg == "" {
		msg = string(e.Body)
	}
	if len(msg) > maxBodyEcho {
		msg = msg[:maxBodyEcho]
	}

	var suffix string
	if e.RequestID != "" {
		suffix = fmt.Sprintf(" (request id %s, %d attempts)", e.RequestID, e.Attempts)
	} else {
		suffix = fmt.Sprintf(" (%d attempts)", e.Attempts)
	}

	return fmt.Sprintf("jev: %s %s: %d %s: %s%s", e.Method, e.URL, e.StatusCode, status, msg, suffix)
}

// Is maps the status code onto this package's HTTP sentinels, so
// errors.Is(err, ErrRateLimited) answers without an errors.As first.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrBadRequest:
		return e.StatusCode == http.StatusBadRequest
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized
	case ErrForbidden:
		return e.StatusCode == http.StatusForbidden
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	case ErrUnprocessable:
		return e.StatusCode == http.StatusUnprocessableEntity
	case ErrRateLimited:
		return e.StatusCode == http.StatusTooManyRequests
	case ErrOverloaded:
		return e.StatusCode == 529
	case ErrServer:
		return e.StatusCode >= 500 && e.StatusCode <= 599
	default:
		return false
	}
}

// TransportError is returned when no HTTP response was obtained: DNS, TLS,
// connection reset, a per-attempt timeout, or the caller's ctx being cancelled.
// It unwraps to the cause, so errors.Is against context.Canceled and
// context.DeadlineExceeded tells a cancelled call from a failed one.
type TransportError struct {
	Method   string
	URL      string
	Attempts int   // attempts made, the first one included
	Timeout  bool  // a deadline fired, rather than the connection failing outright
	Err      error // the network or context error
}

func (e *TransportError) Error() string {
	return fmt.Sprintf("jev: %s %s: %s (after %d attempts)", e.Method, e.URL, e.Err, e.Attempts)
}

func (e *TransportError) Unwrap() error {
	return e.Err
}

func newAPIError(method, url string, resp *http.Response, body []byte, attempts int, now time.Time) *APIError {
	retryAfter, _ := retry.ParseRetryAfter(resp.Header, now)
	return &APIError{
		StatusCode: resp.StatusCode,
		Method:     method,
		URL:        url,
		Message:    extractMessage(body),
		Body:       body,
		Header:     resp.Header,
		RequestID:  resp.Header.Get(headerRequestID),
		RetryAfter: retryAfter,
		Attempts:   attempts,
	}
}

// Mirrors the JS SDK errors.ts: the error body format is undocumented.
func extractMessage(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return ""
	}

	var v any
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return string(trimmed)
	}

	obj, ok := v.(map[string]any)
	if !ok {
		return string(trimmed)
	}

	if s, ok := obj["error"].(string); ok {
		return s
	}
	if errObj, ok := obj["error"].(map[string]any); ok {
		if s, ok := errObj["message"].(string); ok {
			return s
		}
	}
	if s, ok := obj["message"].(string); ok {
		return s
	}
	if s, ok := obj["detail"].(string); ok {
		return s
	}
	if detObj, ok := obj["detail"].(map[string]any); ok {
		if s, ok := detObj["message"].(string); ok {
			return s
		}
	}
	if detArr, ok := obj["detail"].([]any); ok {
		if s, ok := joinDetailEntries(detArr); ok {
			return s
		}
	}

	return ""
}

func joinDetailEntries(entries []any) (string, bool) {
	var parts []string
	for _, entry := range entries {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		msg, _ := m["msg"].(string)
		if msg == "" {
			continue
		}

		var loc []string
		if locArr, ok := m["loc"].([]any); ok {
			for _, l := range locArr {
				s := fmt.Sprint(l)
				if s == "body" {
					continue
				}
				loc = append(loc, s)
			}
		}

		if len(loc) > 0 {
			parts = append(parts, strings.Join(loc, ".")+": "+msg)
		} else {
			parts = append(parts, msg)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "; "), true
}
