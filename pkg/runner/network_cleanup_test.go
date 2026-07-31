package runner

import "testing"

// Placeholder proof artifact for prov-2026-a0bac0d9.
//
// CleanupSharedInfrastructure discards the error from orch.RemoveNetwork
// (runner.go), so a network left with dangling endpoints after a failed
// run is never reported and never retried. The next run's
// SetupSharedInfrastructure then fails with "network with name
// linespec-shared-net already exists", masking the original failure.
func TestCleanupSharedInfrastructureSurfacesNetworkRemovalFailure(t *testing.T) {
	t.Skip("pending implementation - see prov-2026-a0bac0d9")
}
