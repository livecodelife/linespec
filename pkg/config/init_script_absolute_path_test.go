package config

import "testing"

// databases[].init_script must be relative to service_dir. An absolute path
// currently resolves (via filepath.Join(serviceDir, absPath)) to a
// nonexistent location, os.Stat fails, and the init script is silently
// dropped — the container starts with an empty database. This must instead
// fail validation with an explicit error. See prov-2026-14e58ba9.
func TestValidate_RejectsAbsoluteInitScript(t *testing.T) {
	t.Skip("pending implementation - see prov-2026-14e58ba9")
}
