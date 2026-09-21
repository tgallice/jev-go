package jev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Model is one entry of GET /v1/models.
type Model struct {
	Name        string `json:"name"` // what to pass as Request.Model or to WithModel
	Description string `json:"description"`
	ReleaseDate string `json:"release_date"` // YYYY-MM-DD, kept as sent
}

// ListModels returns the models the API advertises, currently the aliases; a
// versioned ID may be accepted without appearing here. It fails with
// ErrInvalidResponse when the body carries no models array, and otherwise like
// Evaluate.
func (c *Client) ListModels(ctx context.Context, opts ...CallOption) ([]Model, error) {
	_, raw, err := c.do(ctx, http.MethodGet, modelsPath, nil, opts)
	if err != nil {
		return nil, err
	}
	var wire struct {
		Models []Model `json:"models"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if wire.Models == nil {
		return nil, fmt.Errorf("%w: response without %q array", ErrInvalidResponse, "models")
	}
	return wire.Models, nil
}
