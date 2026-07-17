package main

import (
	"runtime/debug"
	"testing"
)

// TestResolveDisplayVersion pins the version-resolution precedence that lets a
// plain `go install` binary report its release instead of "dev": ldflag wins
// when set to a real value, otherwise the build-info module version is used
// (with a +suffix stripped and a leading "v" removed), otherwise "dev".
func TestResolveDisplayVersion(t *testing.T) {
	info := func(v string) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}}
	}

	cases := []struct {
		name    string
		ldflag  string
		info    *debug.BuildInfo
		infoOK  bool
		want    string
	}{
		{"ldflag bare wins", "3.15.0", info("v9.9.9"), true, "3.15.0"},
		{"ldflag strips leading v", "v3.15.0", nil, false, "3.15.0"},
		{"go install falls back to build info", "dev", info("v3.15.0"), true, "3.15.0"},
		{"build info devel is ignored", "dev", info("(devel)"), true, "dev"},
		{"build info +suffix stripped", "dev", info("v3.15.0+dirty"), true, "3.15.0"},
		{"no build info stays dev", "dev", nil, false, "dev"},
		{"empty ldflag uses build info", "", info("v2.0.1"), true, "2.0.1"},
		{"empty ldflag no info is dev", "", nil, false, "dev"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveDisplayVersion(tc.ldflag, tc.info, tc.infoOK)
			if got != tc.want {
				t.Errorf("resolveDisplayVersion(%q, %+v, %v) = %q, want %q",
					tc.ldflag, tc.info, tc.infoOK, got, tc.want)
			}
		})
	}
}
