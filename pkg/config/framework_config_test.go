package config

import (
	"testing"
	"time"
)

func TestRailsFrameworkConfig(t *testing.T) {
	config := &RailsFrameworkConfig{}

	// Test GetStartCommand
	startCmd := config.GetStartCommand("3000")
	expected := []string{"bash", "-c", "rm -f tmp/pids/server.pid && bundle exec rails server -b 0.0.0.0 -p 3000"}
	if len(startCmd) != len(expected) {
		t.Errorf("GetStartCommand() returned %d args, expected %d", len(startCmd), len(expected))
	}
	for i := range expected {
		if startCmd[i] != expected[i] {
			t.Errorf("GetStartCommand()[%d] = %q, expected %q", i, startCmd[i], expected[i])
		}
	}

	// Test GetMigrationCommand
	migrationCmd := config.GetMigrationCommand()
	expectedMigration := []string{"bundle", "exec", "rails", "db:migrate"}
	if len(migrationCmd) != len(expectedMigration) {
		t.Errorf("GetMigrationCommand() returned %d args, expected %d", len(migrationCmd), len(expectedMigration))
	}
	for i := range expectedMigration {
		if migrationCmd[i] != expectedMigration[i] {
			t.Errorf("GetMigrationCommand()[%d] = %q, expected %q", i, migrationCmd[i], expectedMigration[i])
		}
	}

	// Test NeedsWarmup
	if !config.NeedsWarmup() {
		t.Error("RailsFrameworkConfig.NeedsWarmup() should return true")
	}

	// Test GetWarmupEndpoint
	if endpoint := config.GetWarmupEndpoint(); endpoint != "/up" {
		t.Errorf("GetWarmupEndpoint() = %q, expected /up", endpoint)
	}

	// Test GetWarmupDelay
	if delay := config.GetWarmupDelay(); delay != 100*time.Millisecond {
		t.Errorf("GetWarmupDelay() = %v, expected 100ms", delay)
	}
}

func TestFastAPIFrameworkConfig(t *testing.T) {
	config := &FastAPIFrameworkConfig{}

	// Test GetStartCommand
	startCmd := config.GetStartCommand("8000")
	expected := []string{"python", "-m", "uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"}
	if len(startCmd) != len(expected) {
		t.Errorf("GetStartCommand() returned %d args, expected %d", len(startCmd), len(expected))
	}
	for i := range expected {
		if startCmd[i] != expected[i] {
			t.Errorf("GetStartCommand()[%d] = %q, expected %q", i, startCmd[i], expected[i])
		}
	}

	// Test GetMigrationCommand (should be nil)
	if migrationCmd := config.GetMigrationCommand(); migrationCmd != nil {
		t.Errorf("GetMigrationCommand() should be nil for FastAPI, got %v", migrationCmd)
	}

	// Test NeedsWarmup
	if config.NeedsWarmup() {
		t.Error("FastAPIFrameworkConfig.NeedsWarmup() should return false")
	}

	// Test GetWarmupEndpoint
	if endpoint := config.GetWarmupEndpoint(); endpoint != "/health" {
		t.Errorf("GetWarmupEndpoint() = %q, expected /health", endpoint)
	}

	// Test GetWarmupDelay
	if delay := config.GetWarmupDelay(); delay != 0 {
		t.Errorf("GetWarmupDelay() = %v, expected 0", delay)
	}
}

func TestDjangoFrameworkConfig(t *testing.T) {
	config := &DjangoFrameworkConfig{}

	// Test GetStartCommand
	startCmd := config.GetStartCommand("8000")
	expected := []string{"python", "manage.py", "runserver", "0.0.0.0:8000"}
	if len(startCmd) != len(expected) {
		t.Errorf("GetStartCommand() returned %d args, expected %d", len(startCmd), len(expected))
	}
	for i := range expected {
		if startCmd[i] != expected[i] {
			t.Errorf("GetStartCommand()[%d] = %q, expected %q", i, startCmd[i], expected[i])
		}
	}

	// Test GetMigrationCommand
	migrationCmd := config.GetMigrationCommand()
	expectedMigration := []string{"python", "manage.py", "migrate"}
	if len(migrationCmd) != len(expectedMigration) {
		t.Errorf("GetMigrationCommand() returned %d args, expected %d", len(migrationCmd), len(expectedMigration))
	}
	for i := range expectedMigration {
		if migrationCmd[i] != expectedMigration[i] {
			t.Errorf("GetMigrationCommand()[%d] = %q, expected %q", i, migrationCmd[i], expectedMigration[i])
		}
	}

	// Test NeedsWarmup
	if !config.NeedsWarmup() {
		t.Error("DjangoFrameworkConfig.NeedsWarmup() should return true")
	}

	// Test GetWarmupEndpoint
	if endpoint := config.GetWarmupEndpoint(); endpoint != "/health" {
		t.Errorf("GetWarmupEndpoint() = %q, expected /health", endpoint)
	}
}

func TestExpressFrameworkConfig(t *testing.T) {
	config := &ExpressFrameworkConfig{}

	// Test GetStartCommand
	startCmd := config.GetStartCommand("3000")
	expected := []string{"npm", "start"}
	if len(startCmd) != len(expected) {
		t.Errorf("GetStartCommand() returned %d args, expected %d", len(startCmd), len(expected))
	}
	for i := range expected {
		if startCmd[i] != expected[i] {
			t.Errorf("GetStartCommand()[%d] = %q, expected %q", i, startCmd[i], expected[i])
		}
	}

	// Test GetMigrationCommand (should be nil)
	if migrationCmd := config.GetMigrationCommand(); migrationCmd != nil {
		t.Errorf("GetMigrationCommand() should be nil for Express, got %v", migrationCmd)
	}

	// Test NeedsWarmup
	if config.NeedsWarmup() {
		t.Error("ExpressFrameworkConfig.NeedsWarmup() should return false")
	}
}

func TestGenericFrameworkConfig(t *testing.T) {
	// Test with custom start command
	config := &GenericFrameworkConfig{
		CustomStartCommand:  "node server.js",
		CustomMigrationCmd:  "npm run migrate",
		NeedsWarmupFlag:     true,
		WarmupEndpointValue: "/ready",
		WarmupDelayMs:       500,
	}

	// Test GetStartCommand
	startCmd := config.GetStartCommand("3000")
	if len(startCmd) != 3 || startCmd[0] != "sh" || startCmd[1] != "-c" || startCmd[2] != "node server.js" {
		t.Errorf("GetStartCommand() = %v, expected [sh -c node server.js]", startCmd)
	}

	// Test GetMigrationCommand
	migrationCmd := config.GetMigrationCommand()
	if len(migrationCmd) != 3 || migrationCmd[2] != "npm run migrate" {
		t.Errorf("GetMigrationCommand() = %v, expected [sh -c npm run migrate]", migrationCmd)
	}

	// Test NeedsWarmup
	if !config.NeedsWarmup() {
		t.Error("GenericFrameworkConfig.NeedsWarmup() should return true")
	}

	// Test GetWarmupEndpoint
	if endpoint := config.GetWarmupEndpoint(); endpoint != "/ready" {
		t.Errorf("GetWarmupEndpoint() = %q, expected /ready", endpoint)
	}

	// Test GetWarmupDelay
	if delay := config.GetWarmupDelay(); delay != 500*time.Millisecond {
		t.Errorf("GetWarmupDelay() = %v, expected 500ms", delay)
	}

	// Test default start command (empty)
	config2 := &GenericFrameworkConfig{
		CustomStartCommand: "",
	}
	startCmd2 := config2.GetStartCommand("3000")
	if len(startCmd2) != 3 || startCmd2[2] != "echo 'No start command specified'" {
		t.Errorf("Empty GetStartCommand() = %v, expected echo message", startCmd2)
	}
}

func TestGetFrameworkConfig(t *testing.T) {
	tests := []struct {
		framework       string
		expectedType    string
		expectWarmup    bool
		expectMigration bool
	}{
		{"rails", "*config.RailsFrameworkConfig", true, true},
		{"fastapi", "*config.FastAPIFrameworkConfig", false, false},
		{"django", "*config.DjangoFrameworkConfig", true, true},
		{"express", "*config.ExpressFrameworkConfig", false, false},
		{"unknown", "*config.GenericFrameworkConfig", false, false},
		{"", "*config.GenericFrameworkConfig", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.framework, func(t *testing.T) {
			config := GetFrameworkConfig(tt.framework, "", "", false, "", 0)
			if config == nil {
				t.Fatal("GetFrameworkConfig() returned nil")
			}

			// Check warmup
			if warmup := config.NeedsWarmup(); warmup != tt.expectWarmup {
				t.Errorf("NeedsWarmup() = %v, expected %v", warmup, tt.expectWarmup)
			}

			// Check migration command
			hasMigration := config.GetMigrationCommand() != nil
			if hasMigration != tt.expectMigration {
				t.Errorf("GetMigrationCommand() != nil = %v, expected %v", hasMigration, tt.expectMigration)
			}
		})
	}
}

func TestGetFrameworkConfigWithOverrides(t *testing.T) {
	// Test overrides with generic framework (since known frameworks have fixed configs)
	needsWarmup := true
	config := GetFrameworkConfig("custom-framework", "custom-start", "custom-migrate", needsWarmup, "/custom", 200)

	// For generic frameworks, overrides should be respected
	if !config.NeedsWarmup() {
		t.Error("NeedsWarmup() should be true with override")
	}

	if endpoint := config.GetWarmupEndpoint(); endpoint != "/custom" {
		t.Errorf("GetWarmupEndpoint() = %q, expected /custom", endpoint)
	}

	if delay := config.GetWarmupDelay(); delay != 200*time.Millisecond {
		t.Errorf("GetWarmupDelay() = %v, expected 200ms", delay)
	}

	// Test custom commands are used
	startCmd := config.GetStartCommand("3000")
	if len(startCmd) != 3 || startCmd[2] != "custom-start" {
		t.Errorf("GetStartCommand() = %v, expected custom-start command", startCmd)
	}

	migrationCmd := config.GetMigrationCommand()
	if len(migrationCmd) != 3 || migrationCmd[2] != "custom-migrate" {
		t.Errorf("GetMigrationCommand() = %v, expected custom-migrate command", migrationCmd)
	}
}

func TestGetFrameworkConfigRailsOverridesNotUsed(t *testing.T) {
	// Test that Rails uses its own fixed values, not the overrides passed to GetFrameworkConfig
	needsWarmup := false // Rails uses true by default
	config := GetFrameworkConfig("rails", "custom-start", "custom-migrate", needsWarmup, "/custom", 200)

	// Rails config uses its own fixed values
	if !config.NeedsWarmup() {
		t.Error("RailsFrameworkConfig.NeedsWarmup() should always return true regardless of override")
	}

	// Rails uses its own endpoint, not the custom one
	if endpoint := config.GetWarmupEndpoint(); endpoint != "/up" {
		t.Errorf("Rails GetWarmupEndpoint() = %q, expected /up (Rails fixed value)", endpoint)
	}

	// Rails uses its own delay, not the custom one
	if delay := config.GetWarmupDelay(); delay != 100*time.Millisecond {
		t.Errorf("Rails GetWarmupDelay() = %v, expected 100ms (Rails fixed value)", delay)
	}

	// Rails uses its own start command
	startCmd := config.GetStartCommand("3000")
	if len(startCmd) != 3 || !contains(startCmd[2], "bundle exec rails") {
		t.Errorf("Rails GetStartCommand() = %v, expected bundle exec rails command", startCmd)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr || len(s) > len(substr) && containsSubstring(s, substr)
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
