package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Every linespec run used the same fixed default Docker names
// (linespec-shared-net, linespec-shared-db, linespec-shared-kafka, ...), so a
// second run in another repo force-removed the first run's network and
// containers mid-run. Defaults must now be unique per run or per project root,
// while explicit container_naming overrides are honoured verbatim.

const isolationMinimalConfig = `
service:
  name: my-api
  port: 3000
  framework: rails
`

func loadIsolationConfig(t *testing.T, yaml string) *LineSpecConfig {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".linespec.yml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	cfg, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile failed: %v", err)
	}
	if cfg.ContainerNaming == nil {
		t.Fatal("expected ContainerNaming to be populated with defaults")
	}
	return cfg
}

// resolvedDefaults returns the resolved default names keyed by role, using the
// same params for every config so differences can only come from the config.
func resolvedDefaults(cn *ContainerNaming) map[string]string {
	p := ContainerNameParams{ServiceName: "my-api", SpecName: "create-user", Type: "db"}
	return map[string]string{
		"network": cn.GetNetworkName(p),
		"db":      cn.GetDatabaseContainer(p),
		"kafka":   cn.GetKafkaContainer(p),
		"migrate": cn.GetMigrateContainer(p),
		"app":     cn.GetAppContainer(p),
		"proxy":   cn.GetProxyContainer(p),
	}
}

func TestContainerNamingDefaultsAreIsolatedPerRun(t *testing.T) {
	t.Run("defaults differ between two distinct project roots", func(t *testing.T) {
		a := resolvedDefaults(loadIsolationConfig(t, isolationMinimalConfig).ContainerNaming)
		b := resolvedDefaults(loadIsolationConfig(t, isolationMinimalConfig).ContainerNaming)

		for role, nameA := range a {
			if nameA == b[role] {
				t.Errorf("%s default is identical across two project roots (%q); concurrent runs would collide", role, nameA)
			}
		}
	})

	t.Run("shared infrastructure defaults no longer use the fixed legacy names", func(t *testing.T) {
		got := resolvedDefaults(loadIsolationConfig(t, isolationMinimalConfig).ContainerNaming)

		legacy := map[string]string{
			"network": "linespec-shared-net",
			"db":      "linespec-shared-db",
			"kafka":   "linespec-shared-kafka",
		}
		for role, fixed := range legacy {
			if got[role] == fixed {
				t.Errorf("%s default is still the machine-wide fixed name %q", role, fixed)
			}
		}
	})

	t.Run("defaults within one config are distinct per role", func(t *testing.T) {
		got := resolvedDefaults(loadIsolationConfig(t, isolationMinimalConfig).ContainerNaming)
		seen := map[string]string{}
		for role, name := range got {
			if name == "" {
				t.Errorf("%s default resolved to an empty name", role)
			}
			if other, dup := seen[name]; dup {
				t.Errorf("%s and %s resolve to the same name %q", role, other, name)
			}
			seen[name] = role
		}
	})

	t.Run("explicit container_naming overrides are honoured verbatim", func(t *testing.T) {
		cfg := loadIsolationConfig(t, isolationMinimalConfig+`
container_naming:
  network_name: my-net
  database_container: my-db
  kafka_container: my-kafka
  migrate_container: my-migrate
  app_container: my-app
  proxy_container: my-proxy
`)
		want := map[string]string{
			"network": "my-net",
			"db":      "my-db",
			"kafka":   "my-kafka",
			"migrate": "my-migrate",
			"app":     "my-app",
			"proxy":   "my-proxy",
		}
		got := resolvedDefaults(cfg.ContainerNaming)
		for role, w := range want {
			if got[role] != w {
				t.Errorf("%s: explicit override not honoured verbatim: want %q, got %q", role, w, got[role])
			}
		}
	})

	t.Run("a partial override leaves only the other defaults isolated", func(t *testing.T) {
		yaml := isolationMinimalConfig + `
container_naming:
  network_name: my-net
`
		a := loadIsolationConfig(t, yaml).ContainerNaming
		b := loadIsolationConfig(t, yaml).ContainerNaming
		ra, rb := resolvedDefaults(a), resolvedDefaults(b)

		if ra["network"] != "my-net" || rb["network"] != "my-net" {
			t.Errorf("explicit network_name must be verbatim, got %q and %q", ra["network"], rb["network"])
		}
		if ra["db"] == rb["db"] {
			t.Errorf("unset database_container should still default to a per-root name, got %q in both roots", ra["db"])
		}
		if ra["kafka"] == rb["kafka"] {
			t.Errorf("unset kafka_container should still default to a per-root name, got %q in both roots", ra["kafka"])
		}
	})
}
