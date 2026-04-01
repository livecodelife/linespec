package config

import (
	"strings"
	"testing"
	"time"
)

func TestFrameworkDefaults(t *testing.T) {
	tests := []struct {
		framework       string
		startContains   string
		expectMigration bool
		expectWarmup    bool
		expectEndpoint  string
		expectDelay     time.Duration
	}{
		{"rails", "bundle exec rails", true, true, "/up", 100 * time.Millisecond},
		{"fastapi", "uvicorn", false, false, "/health", 0},
		{"django", "manage.py runserver", true, true, "/health", 100 * time.Millisecond},
		{"express", "npm start", false, false, "/health", 0},
		{"unknown", "", false, false, "/", 0},
	}

	for _, tt := range tests {
		t.Run(tt.framework, func(t *testing.T) {
			cfg := GetFrameworkConfig(tt.framework, "", "", nil, "", 0)
			if cfg == nil {
				t.Fatal("GetFrameworkConfig() returned nil")
			}

			if warmup := cfg.NeedsWarmup(); warmup != tt.expectWarmup {
				t.Errorf("NeedsWarmup() = %v, expected %v", warmup, tt.expectWarmup)
			}

			if endpoint := cfg.GetWarmupEndpoint(); endpoint != tt.expectEndpoint {
				t.Errorf("GetWarmupEndpoint() = %q, expected %q", endpoint, tt.expectEndpoint)
			}

			if delay := cfg.GetWarmupDelay(); delay != tt.expectDelay {
				t.Errorf("GetWarmupDelay() = %v, expected %v", delay, tt.expectDelay)
			}

			hasMigration := cfg.GetMigrationCommand() != nil
			if hasMigration != tt.expectMigration {
				t.Errorf("GetMigrationCommand() != nil = %v, expected %v", hasMigration, tt.expectMigration)
			}

			if tt.startContains != "" {
				startCmd := cfg.GetStartCommand("3000")
				full := strings.Join(startCmd, " ")
				if !strings.Contains(full, tt.startContains) {
					t.Errorf("GetStartCommand() = %v, expected it to contain %q", startCmd, tt.startContains)
				}
			}
		})
	}
}

func TestFrameworkDefaultsPortSubstitution(t *testing.T) {
	// Verify ${PORT} is replaced in framework default start commands
	for _, framework := range []string{"rails", "fastapi", "django"} {
		t.Run(framework, func(t *testing.T) {
			cfg := GetFrameworkConfig(framework, "", "", nil, "", 0)
			startCmd := cfg.GetStartCommand("9999")
			full := strings.Join(startCmd, " ")
			if strings.Contains(full, "${PORT}") {
				t.Errorf("GetStartCommand(9999) still contains literal ${PORT}: %v", startCmd)
			}
			if !strings.Contains(full, "9999") {
				t.Errorf("GetStartCommand(9999) does not contain port 9999: %v", startCmd)
			}
		})
	}
}

func TestGetFrameworkConfigAllReturnGeneric(t *testing.T) {
	// All framework names should return *GenericFrameworkConfig
	frameworks := []string{"rails", "fastapi", "django", "express", "unknown", ""}
	for _, fw := range frameworks {
		t.Run(fw, func(t *testing.T) {
			cfg := GetFrameworkConfig(fw, "", "", nil, "", 0)
			if _, ok := cfg.(*GenericFrameworkConfig); !ok {
				t.Errorf("GetFrameworkConfig(%q) returned %T, expected *GenericFrameworkConfig", fw, cfg)
			}
		})
	}
}

func TestGetFrameworkConfigOverridesApplyToAllFrameworks(t *testing.T) {
	// Overrides must take precedence over framework defaults for all frameworks,
	// including known ones like rails.
	frameworks := []string{"rails", "fastapi", "django", "express", "custom-framework"}
	for _, fw := range frameworks {
		t.Run(fw, func(t *testing.T) {
			needsWarmup := false
			cfg := GetFrameworkConfig(fw, "custom-start", "custom-migrate", &needsWarmup, "/custom", 200)

			if cfg.NeedsWarmup() {
				t.Errorf("%s: NeedsWarmup() = true, expected false (override)", fw)
			}
			if endpoint := cfg.GetWarmupEndpoint(); endpoint != "/custom" {
				t.Errorf("%s: GetWarmupEndpoint() = %q, expected /custom", fw, endpoint)
			}
			if delay := cfg.GetWarmupDelay(); delay != 200*time.Millisecond {
				t.Errorf("%s: GetWarmupDelay() = %v, expected 200ms", fw, delay)
			}
			startCmd := cfg.GetStartCommand("3000")
			if !strings.Contains(strings.Join(startCmd, " "), "custom-start") {
				t.Errorf("%s: GetStartCommand() = %v, expected custom-start", fw, startCmd)
			}
			migrationCmd := cfg.GetMigrationCommand()
			if migrationCmd == nil || !strings.Contains(strings.Join(migrationCmd, " "), "custom-migrate") {
				t.Errorf("%s: GetMigrationCommand() = %v, expected custom-migrate", fw, migrationCmd)
			}
		})
	}
}

func TestGetFrameworkConfigNilNeedsWarmupUsesDefault(t *testing.T) {
	// When needsWarmup is nil, the framework default should be used
	railsCfg := GetFrameworkConfig("rails", "", "", nil, "", 0)
	if !railsCfg.NeedsWarmup() {
		t.Error("rails: NeedsWarmup() = false with nil override, expected true (framework default)")
	}

	fastAPICfg := GetFrameworkConfig("fastapi", "", "", nil, "", 0)
	if fastAPICfg.NeedsWarmup() {
		t.Error("fastapi: NeedsWarmup() = true with nil override, expected false (framework default)")
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
