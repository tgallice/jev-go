package jev

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientListModels(t *testing.T) {
	catalog := string(fixture(t, "models.json"))

	for _, tc := range []struct {
		name           string
		newHandler     func(t *testing.T) http.HandlerFunc
		callOpts       []CallOption
		wantErr        error
		wantCalls      int
		assertModels   func(t *testing.T, models []Model)
		assertErr      func(t *testing.T, err error)
		assertRequests func(t *testing.T, reqs []recordedRequest)
	}{
		{
			name:       "200 lists the catalog",
			newHandler: serve(stub{body: catalog}),
			wantCalls:  1,
			assertModels: func(t *testing.T, models []Model) {
				require.Len(t, models, 3)
				assert.Equal(t, Model{
					Name:        "jev-latest",
					Description: "Alias for the current stable jev model",
					ReleaseDate: "2025-11-04",
				}, models[0])
			},
			assertRequests: func(t *testing.T, reqs []recordedRequest) {
				require.Len(t, reqs, 1)
				assert.Equal(t, http.MethodGet, reqs[0].method)
				assert.Equal(t, "/v1/models", reqs[0].path)
				assert.Equal(t, "Bearer "+testKey, reqs[0].header.Get("Authorization"))
				assert.Equal(t, "application/json", reqs[0].header.Get("Accept"))
				assert.Empty(t, reqs[0].header.Get("Content-Type"))
				assert.Empty(t, reqs[0].body)
			},
		},
		{
			name:       "empty catalog",
			newHandler: serve(stub{body: `{"models":[]}`}),
			wantCalls:  1,
			assertModels: func(t *testing.T, models []Model) {
				assert.Empty(t, models)
			},
		},
		{
			name:       "response without models",
			newHandler: serve(stub{body: `{"data":[]}`}),
			wantErr:    ErrInvalidResponse,
			wantCalls:  1,
		},
		{
			name:       "malformed body",
			newHandler: serve(stub{body: `not json`}),
			wantErr:    ErrInvalidResponse,
			wantCalls:  1,
		},
		{
			name:       "500 is retried then fails",
			newHandler: serve(stub{status: http.StatusInternalServerError, body: `{"error":"boom"}`}),
			wantErr:    ErrServer,
			wantCalls:  3,
			assertErr: func(t *testing.T, err error) {
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, 3, apiErr.Attempts)
				assert.Equal(t, "boom", apiErr.Message)
			},
		},
		{
			name:       "call retry policy disables retries",
			newHandler: serve(stub{status: http.StatusInternalServerError}),
			callOpts:   []CallOption{WithCallRetry(NoRetry())},
			wantErr:    ErrServer,
			wantCalls:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{EnvAPIKey, EnvBaseURL, EnvModel} {
				t.Setenv(name, "")
			}

			var mu sync.Mutex
			var requests []recordedRequest
			handler := tc.newHandler(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, recordedRequest{
					method: r.Method,
					path:   r.URL.Path,
					header: r.Header.Clone(),
					body:   body,
				})
				handler(w, r)
			}))
			defer srv.Close()

			c, err := New(WithAPIKey(testKey), WithBaseURL(srv.URL), WithRetryPolicy(fastRetry()))
			require.NoError(t, err)
			c.rand = func() float64 { return 0 }

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			models, err := c.ListModels(ctx, tc.callOpts...)

			require.ErrorIs(t, err, tc.wantErr)
			check(tc.assertErr)(t, err)
			check(tc.assertModels)(t, models)
			mu.Lock()
			defer mu.Unlock()
			assert.Len(t, requests, tc.wantCalls)
			check(tc.assertRequests)(t, requests)
		})
	}
}
