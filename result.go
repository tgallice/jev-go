package jev

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
)

// Usage counts the tokens of an evaluation. Only InputTokens is billed. Both
// read 0 when the API omits them, which is indistinguishable from a real 0.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Result is the decoded response of one evaluation. It is safe for concurrent
// reads and for those only: Header and the slice returned by RawAnswer point at
// internal state, so writing through either races with every other reader.
type Result struct {
	Model      string // the versioned model that answered, even when an alias was sent
	Usage      Usage
	RequestID  string // x-typesafe-request-id, "" when absent
	StatusCode int
	Header     http.Header // response headers; must not be modified

	body    []byte
	answers map[string]json.RawMessage
	kinds   map[string]Kind
}

// Keys lists the keys the API answered, sorted. A question that got no answer
// does not appear.
func (r *Result) Keys() []string {
	keys := make([]string, 0, len(r.answers))
	for key := range r.answers {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// Kind reports an answer's type, including types this SDK cannot decode. The
// bool is false when nothing was answered under key.
func (r *Result) Kind(key string) (Kind, bool) {
	k, ok := r.kinds[key]
	return k, ok
}

// RawAnswer returns an answer as sent by the API, for types this SDK does not
// decode. The slice is the Result's own and must not be modified; copy it
// before keeping it past the Result.
func (r *Result) RawAnswer(key string) (json.RawMessage, bool) {
	raw, ok := r.answers[key]
	return raw, ok
}

// RawBody returns a fresh copy of the whole response body, safe to keep and to
// modify.
func (r *Result) RawBody() []byte { return slices.Clone(r.body) }

func parseResult(resp *http.Response, body []byte, logger *slog.Logger) (*Result, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	var wire struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   Usage                      `json:"usage"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if wire.Answers == nil {
		return nil, fmt.Errorf("%w: response without %q object", ErrInvalidResponse, "answers")
	}
	res := &Result{
		Model:   wire.Model,
		Usage:   wire.Usage,
		body:    body,
		answers: wire.Answers,
		kinds:   make(map[string]Kind, len(wire.Answers)),
	}
	if resp != nil {
		res.StatusCode = resp.StatusCode
		res.Header = resp.Header
		res.RequestID = resp.Header.Get("x-typesafe-request-id")
	}
	for key, raw := range wire.Answers {
		var probe struct {
			Type Kind `json:"type"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil || probe.Type == "" {
			return nil, fmt.Errorf("%w: answer %q has no %q field", ErrInvalidResponse, key, "type")
		}
		res.kinds[key] = probe.Type
		switch probe.Type {
		case KindNoul, KindChoice, KindScore:
		default:
			// Forward compatibility: keep the raw answer reachable instead of failing.
			logger.Warn("jev: unknown answer type", "key", key, "type", string(probe.Type))
		}
	}
	return res, nil
}
