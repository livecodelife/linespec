package provenance

import (
	"strings"
	"testing"
)

// TestIsPathExcluded verifies that all four exclusion pattern types work correctly.
func TestIsPathExcluded(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		patterns []string
		want     bool
	}{
		// Exact file path
		{
			name:     "exact match",
			filePath: "CHANGELOG.md",
			patterns: []string{"CHANGELOG.md"},
			want:     true,
		},
		{
			name:     "exact no match",
			filePath: "README.md",
			patterns: []string{"CHANGELOG.md"},
			want:     false,
		},

		// Directory prefix
		{
			name:     "directory prefix with trailing slash",
			filePath: "docs/api.md",
			patterns: []string{"docs/"},
			want:     true,
		},
		{
			name:     "directory prefix without trailing slash",
			filePath: "docs/api.md",
			patterns: []string{"docs"},
			want:     true,
		},
		{
			name:     "directory prefix does not match sibling",
			filePath: "docs-old/file.md",
			patterns: []string{"docs"},
			want:     false,
		},
		{
			name:     "directory prefix does not match bare filename that equals prefix",
			filePath: "docs",
			patterns: []string{"docs/"},
			want:     false,
		},
		{
			name:     "nested directory under prefix",
			filePath: "migrations/v2/001_add_users.sql",
			patterns: []string{"migrations/"},
			want:     true,
		},

		// Glob patterns
		{
			name:     "double-star glob matches generated file",
			filePath: "pkg/foo/foo_gen.go",
			patterns: []string{"**/*_gen.go"},
			want:     true,
		},
		{
			name:     "glob does not match non-generated file",
			filePath: "pkg/foo/foo.go",
			patterns: []string{"**/*_gen.go"},
			want:     false,
		},
		{
			name:     "single-star glob",
			filePath: "CHANGELOG.md",
			patterns: []string{"CHANGELOG*"},
			want:     true,
		},

		// Regex patterns (delimited by leading and trailing /)
		{
			name:     "regex matches generated file",
			filePath: "pkg/foo/service_gen.go",
			patterns: []string{`/.*_gen\.go$/`},
			want:     true,
		},
		{
			name:     "regex does not match non-generated file",
			filePath: "pkg/foo/service.go",
			patterns: []string{`/.*_gen\.go$/`},
			want:     false,
		},
		{
			name:     "regex anchored to suffix",
			filePath: "docs/index.html",
			patterns: []string{`/\.html$/`},
			want:     true,
		},

		// Invalid patterns should not panic or match
		{
			name:     "invalid regex is skipped",
			filePath: "some/file.go",
			patterns: []string{`/[invalid/`},
			want:     false,
		},

		// Empty inputs
		{
			name:     "empty exclude list",
			filePath: "any/file.go",
			patterns: []string{},
			want:     false,
		},
		{
			name:     "empty pattern string",
			filePath: "any/file.go",
			patterns: []string{""},
			want:     false,
		},

		// First matching pattern wins
		{
			name:     "first match wins among multiple patterns",
			filePath: "vendor/lib/file.go",
			patterns: []string{"vendor/", "other/"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPathExcluded(tt.filePath, tt.patterns)
			if got != tt.want {
				t.Errorf("IsPathExcluded(%q, %v) = %v, want %v",
					tt.filePath, tt.patterns, got, tt.want)
			}
		})
	}
}

// TestCommitCheckerExcludePathsField verifies the field is present on CommitChecker.
func TestCommitCheckerExcludePathsField(t *testing.T) {
	checker := NewCommitChecker(&Git{}, &Loader{})
	if checker.ExcludePaths != nil {
		t.Errorf("expected ExcludePaths to be nil by default, got %v", checker.ExcludePaths)
	}

	checker.ExcludePaths = []string{"docs/", "vendor/"}
	if len(checker.ExcludePaths) != 2 {
		t.Errorf("expected 2 ExcludePaths, got %d", len(checker.ExcludePaths))
	}
}

// TestCheckStagedExcludedFileFiltering verifies the exclusion filtering logic used inside CheckStaged.
func TestCheckStagedExcludedFileFiltering(t *testing.T) {
	excludePaths := []string{"CHANGELOG.md", "docs/"}

	t.Run("all staged files excluded means no non-excluded files remain", func(t *testing.T) {
		staged := []string{"CHANGELOG.md", "docs/api.md"}
		var nonExcluded []string
		for _, f := range staged {
			if !IsPathExcluded(f, excludePaths) {
				nonExcluded = append(nonExcluded, f)
			}
		}
		if len(nonExcluded) != 0 {
			t.Errorf("expected all files excluded, got: %v", nonExcluded)
		}
	})

	t.Run("mixed files: non-excluded files remain for enforcement", func(t *testing.T) {
		staged := []string{"CHANGELOG.md", "pkg/foo/foo.go"}
		var nonExcluded []string
		for _, f := range staged {
			if !IsPathExcluded(f, excludePaths) {
				nonExcluded = append(nonExcluded, f)
			}
		}
		if len(nonExcluded) != 1 || nonExcluded[0] != "pkg/foo/foo.go" {
			t.Errorf("expected [pkg/foo/foo.go] to remain, got %v", nonExcluded)
		}
	})
}

// TestBuildContextResultExemptFiles verifies that buildContextResult populates ExemptFiles.
func TestBuildContextResultExemptFiles(t *testing.T) {
	var buf strings.Builder
	cmds := &Commands{
		Loader: &Loader{Records: []*Record{}},
		Config: &ProvenanceConfig{
			ExcludePaths: []string{"CHANGELOG.md", "docs/"},
		},
		Formatter: NewFormatter(&buf, false),
	}

	files := []string{"CHANGELOG.md", "docs/api.md", "pkg/foo/foo.go"}
	result := cmds.buildContextResult(files)

	wantExempt := map[string]bool{"CHANGELOG.md": true, "docs/api.md": true}
	for _, f := range result.ExemptFiles {
		if !wantExempt[f] {
			t.Errorf("unexpected file in ExemptFiles: %q", f)
		}
		delete(wantExempt, f)
	}
	if len(wantExempt) != 0 {
		t.Errorf("missing expected exempt files: %v", wantExempt)
	}

	for _, f := range result.ExemptFiles {
		if f == "pkg/foo/foo.go" {
			t.Error("pkg/foo/foo.go should not appear in ExemptFiles")
		}
	}
}

// TestProvenanceConfigExcludePaths verifies the ProvenanceConfig struct exposes ExcludePaths.
func TestProvenanceConfigExcludePaths(t *testing.T) {
	cfg := &ProvenanceConfig{
		ExcludePaths: []string{"vendor/", `/.*_gen\.go$/`},
	}

	if len(cfg.ExcludePaths) != 2 {
		t.Fatalf("expected 2 ExcludePaths, got %d", len(cfg.ExcludePaths))
	}
	if cfg.ExcludePaths[0] != "vendor/" {
		t.Errorf("ExcludePaths[0]: got %q, want %q", cfg.ExcludePaths[0], "vendor/")
	}
}

// TestLinterExcludePathsField verifies the Linter struct exposes ExcludePaths.
func TestLinterExcludePathsField(t *testing.T) {
	linter := NewLinter(&Loader{}, "warn")
	linter.ExcludePaths = []string{"docs/", "CHANGELOG.md"}

	if len(linter.ExcludePaths) != 2 {
		t.Errorf("expected 2 ExcludePaths, got %d", len(linter.ExcludePaths))
	}
}

// TestFormatContextCompactExemptFiles verifies the compact formatter outputs an EXEMPT section.
func TestFormatContextCompactExemptFiles(t *testing.T) {
	var buf strings.Builder
	f := NewFormatter(&buf, false)

	result := &ContextResult{
		Files:         []string{"CHANGELOG.md"},
		DirectMatches: []*ContextRecord{},
		ExemptFiles:   []string{"CHANGELOG.md"},
	}

	f.FormatContextCompact(result)

	output := buf.String()
	if !strings.Contains(output, "EXEMPT:") {
		t.Errorf("expected EXEMPT: section in output, got:\n%s", output)
	}
	if !strings.Contains(output, "CHANGELOG.md") {
		t.Errorf("expected CHANGELOG.md in output, got:\n%s", output)
	}
	if !strings.Contains(output, "exempt") {
		t.Errorf("expected 'exempt' status in output, got:\n%s", output)
	}
}

// TestFormatContextJSONExemptFiles verifies the JSON formatter includes exempt_files.
func TestFormatContextJSONExemptFiles(t *testing.T) {
	var buf strings.Builder
	f := NewFormatter(&buf, false)

	result := &ContextResult{
		Files:         []string{"CHANGELOG.md"},
		DirectMatches: []*ContextRecord{},
		ExemptFiles:   []string{"CHANGELOG.md"},
	}

	if err := f.FormatContextJSON(result); err != nil {
		t.Fatalf("FormatContextJSON returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"exempt_files"`) {
		t.Errorf("expected exempt_files key in JSON output, got:\n%s", output)
	}
	if !strings.Contains(output, "CHANGELOG.md") {
		t.Errorf("expected CHANGELOG.md in JSON output, got:\n%s", output)
	}
}
