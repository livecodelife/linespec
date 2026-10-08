package config

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The user-facing docs must not present the old fixed Docker names as
// defaults (copying them reintroduces the cross-run collision), must describe
// the isolated per-run defaults, and the 3.25.0 release prep must be in place.

var oldFixedNames = []string{
	"linespec-shared-db",
	"linespec-shared-net",
	"linespec-shared-kafka",
	"linespec-migrate-",
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestDocsDoNotPresentFixedContainerNamesAsDefaults(t *testing.T) {
	for _, file := range []string{"../../README.md", "../../LINESPEC.md"} {
		content := readRepoFile(t, file)
		lower := strings.ToLower(content)
		lines := strings.Split(content, "\n")

		// Reasoning: the old names may legitimately remain as example values,
		// but only when presented as explicit overrides. Rather than parse the
		// markdown structure, require the word "explicit" within a +/-10 line
		// window of every occurrence (covers a comment above the YAML block
		// or prose just before/after the fence). "explicit" is chosen because
		// unrelated nearby text such as "Override the proxy image" does not
		// contain it.
		t.Run(file+"/old_names_only_as_explicit_overrides", func(t *testing.T) {
			for i, line := range lines {
				for _, name := range oldFixedNames {
					if !strings.Contains(line, name) {
						continue
					}
					lo, hi := max(0, i-10), min(len(lines), i+11)
					window := strings.ToLower(strings.Join(lines[lo:hi], "\n"))
					if !strings.Contains(window, "explicit") {
						t.Errorf("%s:%d mentions %q without introducing it as an explicit override nearby", file, i+1, name)
					}
				}
			}
		})

		t.Run(file+"/states_isolated_default_suffix", func(t *testing.T) {
			if !strings.Contains(lower, "sha256") && !strings.Contains(lower, "hash of the project root") {
				t.Error("docs do not mention the project-root hash in the default name suffix")
			}
			if !strings.Contains(lower, "per-process") && !strings.Contains(lower, "per-run") {
				t.Error("docs do not mention the per-process / per-run token in the default name suffix")
			}
		})

		t.Run(file+"/states_explicit_values_verbatim", func(t *testing.T) {
			if !strings.Contains(lower, "verbatim") {
				t.Error("docs do not say explicit container_naming values are used verbatim")
			}
		})

		t.Run(file+"/states_concurrent_runs_isolated", func(t *testing.T) {
			if !regexp.MustCompile(`concurrent[^\n]*isolat|isolat[^\n]*concurrent`).MatchString(lower) {
				t.Error("docs do not say concurrent runs are isolated")
			}
		})

		t.Run(file+"/documents_manual_orphan_cleanup", func(t *testing.T) {
			if !strings.Contains(content, "docker rm -f") {
				t.Error("docs do not mention `docker rm -f` for orphaned containers")
			}
			if !strings.Contains(content, "docker network rm") {
				t.Error("docs do not mention `docker network rm` for orphaned networks")
			}
		})
	}

	t.Run("VERSION_is_3.25.0", func(t *testing.T) {
		if got := strings.TrimSpace(readRepoFile(t, "../../VERSION")); got != "3.25.0" {
			t.Errorf("VERSION = %q, want 3.25.0", got)
		}
	})

	t.Run("CHANGELOG_has_3.25.0_entry", func(t *testing.T) {
		cl := readRepoFile(t, "../../CHANGELOG.md")
		if !regexp.MustCompile(`(?m)^## \[3\.25\.0\]`).MatchString(cl) {
			t.Fatal("CHANGELOG.md has no \"## [3.25.0]\" heading")
		}
		idx := strings.Index(cl, "## [3.25.0]")
		entry := cl[idx:]
		if next := strings.Index(entry[len("## [3.25.0]"):], "\n## ["); next >= 0 {
			entry = entry[:len("## [3.25.0]")+next]
		}
		lower := strings.ToLower(entry)
		if !strings.Contains(lower, "upgrade note") {
			t.Error("3.25.0 entry has no Upgrade note")
		}
		if !strings.Contains(lower, "per-run suffix") {
			t.Error("3.25.0 entry does not mention the per-run suffix")
		}
		if !strings.Contains(entry, "NO TRANSACTION") {
			t.Error("3.25.0 entry does not mention NO TRANSACTION (stricter parsing)")
		}
	})
}
