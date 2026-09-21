package jev

import (
	"encoding/json"
	"fmt"
)

// Entry is any JSON-serializable value the API accepts for state, instructions
// and criteria descriptions: a string, an object, an array, or null where a
// description is optional. A bare number or boolean is refused with
// ErrInvalidRequest, and so is a []byte, which encoding/json would send
// base64-encoded; a json.RawMessage is forwarded as is.
type Entry = any

// marshalEntry rejects bare numbers and booleans: the API only accepts them
// nested inside an object or an array.
func marshalEntry(v Entry, allowNil bool) (json.RawMessage, error) {
	// encoding/json would base64-encode a []byte into a valid-looking string,
	// and the model would read the encoded form.
	if _, ok := v.([]byte); ok {
		return nil, fmt.Errorf("%w: []byte would be sent base64-encoded, pass a string", ErrInvalidRequest)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	switch {
	case raw[0] == '"' || raw[0] == '{' || raw[0] == '[':
		return raw, nil
	case allowNil && string(raw) == "null":
		return raw, nil
	}
	return nil, fmt.Errorf("%w: entry must be a string, object or array, got %s", ErrInvalidRequest, snippet(raw))
}

// snippet bounds error messages: an Entry can be an arbitrarily large document.
func snippet(raw []byte) string {
	const limit = 64
	if len(raw) > limit {
		return string(raw[:limit]) + "..."
	}
	return string(raw)
}
