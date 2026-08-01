package config

import (
	"os"
	"path/filepath"
	"testing"
)

// database.port must default per db.Type (5432/postgresql, 3306/mysql,
// 27017/mongodb) even when database.image is set explicitly. Before the fix,
// the defaulting logic in parser.go only ran when db.Image == "", so an
// explicit image with no port left db.Port == 0, which Docker later
// rejects as "invalid port specification: 0/tcp".
func TestDatabasePortDefaultsWithExplicitImage(t *testing.T) {
	cases := []struct {
		name         string
		dbType       string
		image        string
		expectedPort int
	}{
		{"postgresql explicit image", "postgresql", "postgres:16-alpine", 5432},
		{"mysql explicit image", "mysql", "mysql:8.4", 3306},
		{"mongodb explicit image", "mongodb", "mongo:7", 27017},
		{"postgresql non-default explicit image", "postgresql", "postgres:15-alpine", 5432},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			configContent := `
service:
  name: my-api
  port: 3000
  framework: rails
infrastructure:
  database: true
database:
  type: ` + tc.dbType + `
  image: ` + tc.image + `
  database: my_db
  username: my_user
  password: my_pass
`
			configPath := filepath.Join(tempDir, ".linespec.yml")
			if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
				t.Fatalf("Failed to write test config: %v", err)
			}

			config, err := LoadConfigFile(configPath)
			if err != nil {
				t.Fatalf("LoadConfigFile() failed: %v", err)
			}

			if config.Database == nil {
				t.Fatal("Database config should not be nil")
			}
			if config.Database.Image != tc.image {
				t.Errorf("Database.Image = %q, expected explicit image %q to be preserved", config.Database.Image, tc.image)
			}
			if config.Database.Port != tc.expectedPort {
				t.Errorf("Database.Port = %d, expected %d (defaulted despite explicit image)", config.Database.Port, tc.expectedPort)
			}
		})
	}
}
