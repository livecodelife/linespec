package initcmd

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// runWizard feeds answers through the wizard and returns stdout + config path.
func runWizard(t *testing.T, projectDir, outputDir string, answers []string, force bool) (string, string) {
	t.Helper()
	input := strings.NewReader(strings.Join(answers, "\n") + "\n")
	var out bytes.Buffer
	opts := Options{
		ProjectPath: projectDir,
		OutputPath:  outputDir,
		Force:       force,
		Stdin:       input,
		Stdout:      &out,
	}
	if err := Run(opts); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	configPath := filepath.Join(outputDir, ".linespec.yml")
	return out.String(), configPath
}

func readConfig(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read config: %v", err)
	}
	var out map[string]interface{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("cannot parse config: %v", err)
	}
	return out
}

// TestScanProject_FrameworkDetection verifies indicator-file → framework mapping.
func TestScanProject_FrameworkDetection(t *testing.T) {
	cases := []struct {
		file      string
		wantFW    string
	}{
		{"Gemfile", "rails"},
		{"manage.py", "django"},
		{"requirements.txt", "fastapi"},
		{"go.mod", "chi"},
		{"package.json", "express"},
	}
	for _, tc := range cases {
		t.Run(tc.wantFW, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.file), []byte{}, 0644); err != nil {
				t.Fatal(err)
			}
			got := scanProject(dir)
			if got.Framework != tc.wantFW {
				t.Errorf("framework: got %q, want %q", got.Framework, tc.wantFW)
			}
		})
	}
}

// TestScanProject_DatabaseDetection verifies database detection from indicator files.
func TestScanProject_DatabaseDetection(t *testing.T) {
	t.Run("database_yml_implies_mysql", func(t *testing.T) {
		dir := t.TempDir()
		configDir := filepath.Join(dir, "config")
		if err := os.Mkdir(configDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configDir, "database.yml"), []byte("adapter: mysql2"), 0644); err != nil {
			t.Fatal(err)
		}
		got := scanProject(dir)
		if got.Database != "mysql" {
			t.Errorf("database: got %q, want %q", got.Database, "mysql")
		}
	})

	t.Run("docker_compose_postgres", func(t *testing.T) {
		dir := t.TempDir()
		compose := "services:\n  db:\n    image: postgres:16\n"
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0644); err != nil {
			t.Fatal(err)
		}
		got := scanProject(dir)
		if got.Database != "postgresql" {
			t.Errorf("database: got %q, want %q", got.Database, "postgresql")
		}
	})

	t.Run("docker_compose_mysql", func(t *testing.T) {
		dir := t.TempDir()
		compose := "services:\n  db:\n    image: mysql:8.4\n"
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(compose), 0644); err != nil {
			t.Fatal(err)
		}
		got := scanProject(dir)
		if got.Database != "mysql" {
			t.Errorf("database: got %q, want %q", got.Database, "mysql")
		}
	})

	t.Run("no_indicators_returns_none", func(t *testing.T) {
		dir := t.TempDir()
		got := scanProject(dir)
		if got.Database != "none" {
			t.Errorf("database: got %q, want %q", got.Database, "none")
		}
	})
}

// TestBuildConfig_TestingMode verifies testing-only config has no provenance block.
func TestBuildConfig_TestingMode(t *testing.T) {
	cfg := buildConfig("testing", "my-svc", "express", "mysql")
	if cfg.Provenance != nil {
		t.Error("testing mode should not set provenance")
	}
	if cfg.Database == nil {
		t.Fatal("testing mode with mysql should set database")
	}
	if cfg.Database.Type != "mysql" {
		t.Errorf("database type: got %q, want %q", cfg.Database.Type, "mysql")
	}
	if cfg.Service.Name != "my-svc" {
		t.Errorf("service name: got %q, want %q", cfg.Service.Name, "my-svc")
	}
}

// TestBuildConfig_ProvenanceMode verifies provenance-only config has no service block.
func TestBuildConfig_ProvenanceMode(t *testing.T) {
	cfg := buildConfig("provenance", "", "", "")
	if cfg.Provenance == nil {
		t.Fatal("provenance mode should set provenance block")
	}
	if cfg.Provenance.Enforcement != "strict" {
		t.Errorf("enforcement: got %q, want %q", cfg.Provenance.Enforcement, "strict")
	}
	if cfg.Provenance.Dir != "provenance" {
		t.Errorf("dir: got %q, want %q", cfg.Provenance.Dir, "provenance")
	}
	if !cfg.Provenance.CommitTagRequired {
		t.Error("commit_tag_required should be true")
	}
}

// TestBuildConfig_BothMode verifies both mode includes service and provenance.
func TestBuildConfig_BothMode(t *testing.T) {
	cfg := buildConfig("both", "svc", "rails", "postgresql")
	if cfg.Provenance == nil {
		t.Fatal("both mode should set provenance block")
	}
	if cfg.Service.Name != "svc" {
		t.Errorf("service name: got %q, want %q", cfg.Service.Name, "svc")
	}
	if cfg.Database == nil {
		t.Fatal("both mode with postgresql should set database")
	}
	if cfg.Database.Type != "postgresql" {
		t.Errorf("database type: got %q, want %q", cfg.Database.Type, "postgresql")
	}
}

// TestBuildConfig_NoDatabaseOption verifies "none" database clears any default.
func TestBuildConfig_NoDatabaseOption(t *testing.T) {
	cfg := buildConfig("testing", "svc", "rails", "none")
	if cfg.Database != nil {
		t.Errorf("expected nil database for 'none', got %+v", cfg.Database)
	}
}

// TestRun_WritesFile verifies that the wizard writes a parseable .linespec.yml.
func TestRun_WritesFile(t *testing.T) {
	projectDir := t.TempDir()
	outputDir := t.TempDir()

	// Answers: mode=testing, service=my-app, framework=express, database=none
	_, configPath := runWizard(t, projectDir, outputDir,
		[]string{"testing", "my-app", "express", "none"},
		false,
	)

	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config not written: %v", err)
	}
	cfg := readConfig(t, configPath)
	svc, _ := cfg["service"].(map[string]interface{})
	if svc["name"] != "my-app" {
		t.Errorf("service.name: got %v, want %q", svc["name"], "my-app")
	}
}

// TestRun_DefaultsAccepted verifies empty input accepts all displayed defaults.
func TestRun_DefaultsAccepted(t *testing.T) {
	projectDir := t.TempDir()
	// Add a Gemfile so framework defaults to rails
	if err := os.WriteFile(filepath.Join(projectDir, "Gemfile"), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	outputDir := t.TempDir()

	// All empty answers → accept all defaults (mode=both, service=basename, framework=rails, database=none)
	_, configPath := runWizard(t, projectDir, outputDir,
		[]string{"", "", "", ""},
		false,
	)

	cfg := readConfig(t, configPath)
	svc, _ := cfg["service"].(map[string]interface{})
	if svc["framework"] != "rails" {
		t.Errorf("framework: got %v, want %q", svc["framework"], "rails")
	}
	if cfg["provenance"] == nil {
		t.Error("default mode=both should include provenance block")
	}
}

// TestRun_OverwriteRefused verifies that "N" at the overwrite prompt aborts without writing.
func TestRun_OverwriteRefused(t *testing.T) {
	projectDir := t.TempDir()
	outputDir := t.TempDir()

	// Create an existing config
	existing := filepath.Join(outputDir, ".linespec.yml")
	if err := os.WriteFile(existing, []byte("existing: true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	input := strings.NewReader("testing\nmy-app\nexpress\nnone\nN\n")
	var out bytes.Buffer
	err := Run(Options{
		ProjectPath: projectDir,
		OutputPath:  outputDir,
		Stdin:       input,
		Stdout:      &out,
	})
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// The existing file must be unchanged
	data, _ := os.ReadFile(existing)
	if string(data) != "existing: true\n" {
		t.Errorf("file was modified despite refusal; content: %s", data)
	}
	if !strings.Contains(out.String(), "Aborted") {
		t.Error("expected 'Aborted' in output")
	}
}

// TestRun_ForceOverwrites verifies --force skips the confirmation prompt.
func TestRun_ForceOverwrites(t *testing.T) {
	projectDir := t.TempDir()
	outputDir := t.TempDir()

	existing := filepath.Join(outputDir, ".linespec.yml")
	if err := os.WriteFile(existing, []byte("existing: true\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, configPath := runWizard(t, projectDir, outputDir,
		[]string{"provenance"},
		true, // force
	)

	data, _ := os.ReadFile(configPath)
	if strings.Contains(string(data), "existing: true") {
		t.Error("force should have overwritten the file")
	}
}

// TestAsk_DefaultOnEmpty verifies ask() returns defaultVal on empty input.
func TestAsk_DefaultOnEmpty(t *testing.T) {
	sc := newScanner("\n")
	var out bytes.Buffer
	got := ask(sc, &out, "question", "mydefault")
	if got != "mydefault" {
		t.Errorf("got %q, want %q", got, "mydefault")
	}
}

// TestAskChoice_AcceptsValidChoice verifies askChoice() accepts a valid entry.
func TestAskChoice_AcceptsValidChoice(t *testing.T) {
	sc := newScanner("postgresql\n")
	var out bytes.Buffer
	got := askChoice(sc, &out, "Database", []string{"mysql", "postgresql", "none"}, "mysql")
	if got != "postgresql" {
		t.Errorf("got %q, want %q", got, "postgresql")
	}
}

// TestAskChoice_RejectsInvalidThenAccepts verifies askChoice() loops on invalid input.
func TestAskChoice_RejectsInvalidThenAccepts(t *testing.T) {
	sc := newScanner("bad\nmysql\n")
	var out bytes.Buffer
	got := askChoice(sc, &out, "Database", []string{"mysql", "postgresql", "none"}, "none")
	if got != "mysql" {
		t.Errorf("got %q, want %q", got, "mysql")
	}
	if !strings.Contains(out.String(), "Invalid choice") {
		t.Error("expected 'Invalid choice' message for bad input")
	}
}

func newScanner(s string) *bufio.Scanner {
	return bufio.NewScanner(strings.NewReader(s))
}
