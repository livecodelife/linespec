package config

import "testing"

// Placeholder proof artifact for prov-2026-a0bac0d9.
//
// database.port must default per db.Type (5432/postgresql, 3306/mysql,
// 27017/mongodb) even when database.image is set explicitly. Today the
// defaulting logic in parser.go only runs when db.Image == "", so an
// explicit image with no port yields db.Port == 0, which Docker later
// rejects as "invalid port specification: 0/tcp".
func TestDatabasePortDefaultsWithExplicitImage(t *testing.T) {
	t.Skip("pending implementation - see prov-2026-a0bac0d9")
}
