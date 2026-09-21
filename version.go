package jev

import (
	"runtime"
	"strings"
)

// Version is the SDK version, reported in the User-Agent and X-TypeSafe-SDK headers.
const Version = "0.1.0"

func userAgent(custom string) string {
	if custom != "" {
		return custom
	}
	return "jev-go/" + Version + " (" + runtime.Version() + "; " + runtime.GOOS + "/" + runtime.GOARCH + ")"
}

func runtimeDescription() string {
	return "go/" + strings.TrimPrefix(runtime.Version(), "go") + " (" + runtime.GOOS + "; " + runtime.GOARCH + ")"
}
