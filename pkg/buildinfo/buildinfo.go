// Package buildinfo holds the version string injected at build time.
//
// It exists so that the injected value has exactly one home. The version used
// to live in package main as `main.version`, which package main alone could
// read — pkg/runner needs it too, to pin the default proxy image to the
// release it was built from (prov-2026-f57f1570). Injecting a second variable
// alongside main.version would let the two drift, so every -X ldflag points
// here instead: Makefile, .goreleaser.yml (both build ids), and
// Dockerfile.linespec.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

// Version is injected at build time via
// -X github.com/livecodelife/linespec/v3/pkg/buildinfo.Version=<version>.
// It stays "dev" for an ldflag-less `go build`.
var Version = "dev"

// Current returns the best available version string for this binary, resolving
// the build-info fallback against the real runtime build info.
func Current() string {
	info, ok := debug.ReadBuildInfo()
	return Resolve(Version, info, ok)
}

// Resolve picks the best available version string. Preference: an explicit
// ldflag value, then the build-info module version (with any +dirty/local
// suffix stripped), then the raw ldflag, then "dev". The result is returned
// without a leading "v" so the "LineSpec v{{.Version}}" template never
// double-prints it (build info reports "v3.15.0" while the ldflag is bare).
// Kept pure so it can be unit-tested without a real build info.
func Resolve(ldflagVersion string, info *debug.BuildInfo, infoOK bool) string {
	if ldflagVersion != "" && ldflagVersion != "dev" {
		return strings.TrimPrefix(ldflagVersion, "v")
	}
	if infoOK && info != nil {
		v := info.Main.Version
		if idx := strings.Index(v, "+"); idx >= 0 {
			v = v[:idx]
		}
		if v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	if ldflagVersion != "" {
		return strings.TrimPrefix(ldflagVersion, "v")
	}
	return "dev"
}
