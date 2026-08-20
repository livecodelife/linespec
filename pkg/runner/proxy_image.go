package runner

import (
	"strings"
)

const (
	// PublishedProxyImageRepo is the multi-arch image published by release CI
	// from Dockerfile.linespec. Protocol proxy sidecars run it.
	PublishedProxyImageRepo = "ghcr.io/livecodelife/linespec"

	// LocalProxyImage is the tag `linespec build` writes locally. A developer
	// working in a source checkout expects their freshly built proxy code to
	// be what runs, so its presence beats the published default.
	LocalProxyImage = "linespec:latest"
)

// resolveProxyImage picks the image protocol proxy sidecars run, in order:
//
//  1. infrastructure.proxy_image, used verbatim — private registries and
//     air-gapped CI depend on this winning outright (prov-2026-557f393c).
//  2. a locally built linespec:latest, when one is present — otherwise a
//     developer testing unreleased proxy changes would be silently served the
//     published image and conclude their changes did nothing.
//  3. the published image, pinned to this binary's version, so a given
//     linespec always talks to the image built from the same commit.
//
// Kept pure — the local-image probe is passed in — so the precedence can be
// tested without a Docker daemon.
func resolveProxyImage(configured, version string, hasLocalImage bool) string {
	if c := strings.TrimSpace(configured); c != "" {
		return c
	}
	if hasLocalImage {
		return LocalProxyImage
	}
	return PublishedProxyImageRepo + ":" + proxyImageTag(version)
}

// proxyImageTag maps a binary version onto a published tag. An unversioned
// binary (plain `go build`, no ldflags) has no release to pin to, so it falls
// back to the floating tag rather than requesting "ghcr.io/...:dev", which
// would never exist.
func proxyImageTag(version string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if v == "" || v == "dev" {
		return "latest"
	}
	return v
}
