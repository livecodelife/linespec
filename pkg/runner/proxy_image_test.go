package runner

import "testing"

// TestResolveProxyImage pins the precedence that decides which image proxy
// sidecars run. Getting rule 2 backwards is the expensive failure: a developer
// in a source checkout would be served the published image and see their local
// proxy changes silently have no effect.
func TestResolveProxyImage(t *testing.T) {
	cases := []struct {
		name          string
		configured    string
		version       string
		hasLocalImage bool
		want          string
	}{
		{"explicit config wins over local image", "registry.internal/linespec:pinned", "3.18.0", true, "registry.internal/linespec:pinned"},
		{"explicit config wins over published default", "registry.internal/linespec:pinned", "3.18.0", false, "registry.internal/linespec:pinned"},
		{"explicit config is not trimmed away when padded", "  registry.internal/linespec:pinned  ", "3.18.0", false, "registry.internal/linespec:pinned"},
		{"local image beats published default", "", "3.18.0", true, "linespec:latest"},
		{"published default is pinned to the release", "", "3.18.0", false, "ghcr.io/livecodelife/linespec:3.18.0"},
		{"published default strips a leading v", "", "v3.18.0", false, "ghcr.io/livecodelife/linespec:3.18.0"},
		{"dev binary falls back to the floating tag", "", "dev", false, "ghcr.io/livecodelife/linespec:latest"},
		{"empty version falls back to the floating tag", "", "", false, "ghcr.io/livecodelife/linespec:latest"},
		{"dev binary still prefers a local image", "", "dev", true, "linespec:latest"},
		{"whitespace-only config is treated as unset", "   ", "3.18.0", false, "ghcr.io/livecodelife/linespec:3.18.0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveProxyImage(tc.configured, tc.version, tc.hasLocalImage)
			if got != tc.want {
				t.Errorf("resolveProxyImage(%q, %q, %v) = %q, want %q",
					tc.configured, tc.version, tc.hasLocalImage, got, tc.want)
			}
		})
	}
}

// TestProxyImageTagNeverRequestsDev guards the one tag that is guaranteed not
// to exist in the registry: a dev binary must not ask for ":dev".
func TestProxyImageTagNeverRequestsDev(t *testing.T) {
	for _, v := range []string{"dev", "", "  ", "vdev"} {
		if got := proxyImageTag(v); got == "dev" {
			t.Errorf("proxyImageTag(%q) = %q, want a real published tag", v, got)
		}
	}
}
