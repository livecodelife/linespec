package provenance

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSpecCommand_KnownTypes(t *testing.T) {
	tests := []struct {
		name        string
		spec        AssociatedSpec
		wantContain string
		wantSkip    bool
	}{
		{
			name:        "linespec type",
			spec:        AssociatedSpec{Path: "tests/foo.linespec", Type: "linespec"},
			wantContain: "linespec test tests/foo.linespec",
			wantSkip:    false,
		},
		{
			name:        "rspec type",
			spec:        AssociatedSpec{Path: "spec/foo_spec.rb", Type: "rspec"},
			wantContain: "bundle exec rspec spec/foo_spec.rb",
			wantSkip:    false,
		},
		{
			name:        "pytest type",
			spec:        AssociatedSpec{Path: "tests/test_foo.py", Type: "pytest"},
			wantContain: "pytest tests/test_foo.py",
			wantSkip:    false,
		},
		{
			name:        "jest type",
			spec:        AssociatedSpec{Path: "src/foo.test.js", Type: "jest"},
			wantContain: "npx jest src/foo.test.js",
			wantSkip:    false,
		},
		{
			name:     "unknown type is skipped",
			spec:     AssociatedSpec{Path: "some/file.txt", Type: "unknown"},
			wantSkip: true,
		},
		{
			name:     "empty type is skipped",
			spec:     AssociatedSpec{Path: "some/file.txt"},
			wantSkip: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmdStr, skip, err := buildSpecCommand(tc.spec)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if skip != tc.wantSkip {
				t.Errorf("skip=%v, want %v", skip, tc.wantSkip)
			}
			if !tc.wantSkip && !strings.Contains(cmdStr, tc.wantContain) {
				t.Errorf("command %q does not contain %q", cmdStr, tc.wantContain)
			}
		})
	}
}

func TestBuildSpecCommand_RunCommand(t *testing.T) {
	t.Run("run_command with {{path}} placeholder", func(t *testing.T) {
		spec := AssociatedSpec{
			Path:       "tests/foo_spec.rb",
			Type:       "rspec",
			RunCommand: "bundle exec rspec --format documentation {{path}}",
		}
		cmdStr, skip, err := buildSpecCommand(spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skip {
			t.Fatal("expected not skipped")
		}
		want := "bundle exec rspec --format documentation tests/foo_spec.rb"
		if cmdStr != want {
			t.Errorf("got %q, want %q", cmdStr, want)
		}
	})

	t.Run("run_command without placeholder appends path", func(t *testing.T) {
		spec := AssociatedSpec{
			Path:       "tests/foo_spec.rb",
			RunCommand: "bundle exec rspec --format documentation",
		}
		cmdStr, skip, err := buildSpecCommand(spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skip {
			t.Fatal("expected not skipped")
		}
		want := "bundle exec rspec --format documentation tests/foo_spec.rb"
		if cmdStr != want {
			t.Errorf("got %q, want %q", cmdStr, want)
		}
	})

	t.Run("run_command overrides type", func(t *testing.T) {
		spec := AssociatedSpec{
			Path:       "tests/test_foo.py",
			Type:       "pytest",
			RunCommand: "python -m pytest --tb=short",
		}
		cmdStr, skip, err := buildSpecCommand(spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skip {
			t.Fatal("expected not skipped")
		}
		// run_command takes precedence — should append path
		want := "python -m pytest --tb=short tests/test_foo.py"
		if cmdStr != want {
			t.Errorf("got %q, want %q", cmdStr, want)
		}
	})
}

func TestRunSpecs_DisabledByConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &ProvenanceConfig{
		Dir:                          tmpDir,
		Enforcement:                  "none",
		RunAssociatedSpecsOnComplete: false,
	}

	var buf bytes.Buffer
	cmds, err := NewCommands(cfg, tmpDir, os.Stdout, false)
	if err != nil {
		t.Fatalf("failed to create commands: %v", err)
	}
	_ = buf

	// Should succeed silently — feature disabled
	err = cmds.RunSpecs(RunSpecsOptions{RecordID: "nonexistent"})
	if err != nil {
		t.Errorf("expected nil error when feature disabled, got %v", err)
	}
}

func TestRunSpecs_RecordNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &ProvenanceConfig{
		Dir:                          tmpDir,
		Enforcement:                  "none",
		RunAssociatedSpecsOnComplete: true,
	}

	cmds, err := NewCommands(cfg, tmpDir, os.Stdout, false)
	if err != nil {
		t.Fatalf("failed to create commands: %v", err)
	}

	err = cmds.RunSpecs(RunSpecsOptions{RecordID: "prov-2026-nonexistent"})
	if err == nil {
		t.Error("expected error for nonexistent record, got nil")
	}
}

func TestRunSpecs_NoSpecs(t *testing.T) {
	tmpDir := t.TempDir()

	record := &Record{
		ID:              "prov-2026-a1b2c3d4",
		Title:           "Test record",
		Status:          StatusOpen,
		CreatedAt:       "2026-04-15",
		Author:          "test@test.com",
		Intent:          "test",
		AssociatedSpecs: []AssociatedSpec{},
	}
	if err := writeTestRecord(tmpDir, record); err != nil {
		t.Fatalf("failed to write test record: %v", err)
	}

	cfg := &ProvenanceConfig{
		Dir:                          tmpDir,
		Enforcement:                  "none",
		RunAssociatedSpecsOnComplete: true,
	}
	cmds, err := NewCommands(cfg, tmpDir, os.Stdout, false)
	if err != nil {
		t.Fatalf("failed to create commands: %v", err)
	}

	if err := cmds.RunSpecs(RunSpecsOptions{RecordID: "prov-2026-a1b2c3d4"}); err != nil {
		t.Errorf("expected nil for empty specs, got %v", err)
	}
}

func TestRunSpecs_SkipsUnknownType(t *testing.T) {
	tmpDir := t.TempDir()

	record := &Record{
		ID:        "prov-2026-a1b2c3d4",
		Title:     "Test record",
		Status:    StatusOpen,
		CreatedAt: "2026-04-15",
		Author:    "test@test.com",
		Intent:    "test",
		AssociatedSpecs: []AssociatedSpec{
			{Path: "some/config.yml", Type: "config"},
		},
	}
	if err := writeTestRecord(tmpDir, record); err != nil {
		t.Fatalf("failed to write test record: %v", err)
	}

	cfg := &ProvenanceConfig{
		Dir:                          tmpDir,
		Enforcement:                  "none",
		RunAssociatedSpecsOnComplete: true,
	}
	cmds, err := NewCommands(cfg, tmpDir, os.Stdout, false)
	if err != nil {
		t.Fatalf("failed to create commands: %v", err)
	}

	// Should succeed — unknown type is skipped, not an error
	if err := cmds.RunSpecs(RunSpecsOptions{RecordID: "prov-2026-a1b2c3d4"}); err != nil {
		t.Errorf("expected nil for skipped spec, got %v", err)
	}
}

// writeTestRecord writes a minimal record YAML file to tmpDir for use in tests.
func writeTestRecord(dir string, record *Record) error {
	loader := NewLoader(dir, nil)
	record.FilePath = filepath.Join(dir, record.ID+".yml")
	loader.Records = append(loader.Records, record)
	return loader.SaveRecord(record)
}
