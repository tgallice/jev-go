package jev

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUserAgent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		custom   string
		assertUA func(t *testing.T, ua string)
	}{
		{
			name:     "custom value wins",
			custom:   "acme/2.0",
			assertUA: func(t *testing.T, ua string) { assert.Equal(t, "acme/2.0", ua) },
		},
		{
			name: "default advertises the SDK and the runtime",
			assertUA: func(t *testing.T, ua string) {
				assert.Contains(t, ua, "jev-go/"+Version)
				assert.Contains(t, ua, runtime.Version())
				assert.Contains(t, ua, runtime.GOOS+"/"+runtime.GOARCH)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.assertUA(t, userAgent(tc.custom))
		})
	}
}

func TestRuntimeDescription(t *testing.T) {
	got := runtimeDescription()

	assert.True(t, strings.HasPrefix(got, "go/"), got)
	assert.NotContains(t, got, "go/go")
	assert.Contains(t, got, runtime.GOOS+"; "+runtime.GOARCH)
}
