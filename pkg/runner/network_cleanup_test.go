package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/docker/docker/errdefs"
)

// Before the fix, CleanupSharedInfrastructure discarded the error from
// orch.RemoveNetwork (`_ = s.orch.RemoveNetwork(...)`), so a network left with
// dangling endpoints after a failed run (or, per the sibling bug fixed in the
// same record, a proxy container that outlived its cleanup context) was never
// reported and never retried. The next run's SetupSharedInfrastructure then
// failed with "network with name linespec-shared-net already exists", masking
// the original failure. CleanupSharedInfrastructure now delegates network
// removal to removeNetworkWithRetry and returns its error instead of
// discarding it; these tests exercise that helper directly since it requires
// no Docker daemon, unlike CleanupSharedInfrastructure itself (which needs a
// live *docker.DockerOrchestrator).
func TestCleanupSharedInfrastructureSurfacesNetworkRemovalFailure(t *testing.T) {
	t.Run("surfaces the error when every attempt fails", func(t *testing.T) {
		wantErr := errors.New("network has active endpoints")
		calls := 0
		err := removeNetworkWithRetry(context.Background(), 3, time.Millisecond, func(context.Context) error {
			calls++
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected the underlying RemoveNetwork error to be surfaced, got: %v", err)
		}
		if calls != 3 {
			t.Errorf("expected 3 attempts, got %d", calls)
		}
	})

	t.Run("retries transient failures and succeeds", func(t *testing.T) {
		// Models the real-world race: Docker reports active endpoints for a
		// brief window after a container is removed, before the daemon
		// finishes detaching it from the network.
		calls := 0
		err := removeNetworkWithRetry(context.Background(), 5, time.Millisecond, func(context.Context) error {
			calls++
			if calls < 3 {
				return errors.New("network has active endpoints")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected eventual success, got error: %v", err)
		}
		if calls != 3 {
			t.Errorf("expected exactly 3 attempts (stop retrying once it succeeds), got %d", calls)
		}
	})

	t.Run("succeeds immediately without retrying", func(t *testing.T) {
		calls := 0
		err := removeNetworkWithRetry(context.Background(), 5, time.Millisecond, func(context.Context) error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if calls != 1 {
			t.Errorf("expected exactly 1 attempt when the first succeeds, got %d", calls)
		}
	})

	t.Run("treats not-found as success without retrying", func(t *testing.T) {
		// SetupSharedInfrastructure runs this as opportunistic pre-cleanup before
		// every suite, including the very first run when the network has never
		// existed. That must not be reported as a failure or retried — there is
		// nothing to remove.
		calls := 0
		err := removeNetworkWithRetry(context.Background(), 5, time.Millisecond, func(context.Context) error {
			calls++
			return errdefs.NotFound(errors.New("network linespec-shared-net not found"))
		})
		if err != nil {
			t.Fatalf("expected not-found to be treated as success, got error: %v", err)
		}
		if calls != 1 {
			t.Errorf("expected exactly 1 attempt for a not-found error (no point retrying), got %d", calls)
		}
	})
}
