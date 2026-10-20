package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/go-connections/nat"

	"github.com/livecodelife/linespec/v3/pkg/docker"
)

// fakeDocker answers /_ping and a container inspect that has no port binding
// for the first `empty` calls, then publishes hostPort.
func fakeDocker(t *testing.T, empty int32, hostPort string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("Api-Version", "1.43")
			_, _ = w.Write([]byte("OK"))
			return
		}
		n := atomic.AddInt32(&calls, 1)
		ports := nat.PortMap{}
		if n > empty {
			ports["8080/tcp"] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: hostPort}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.ContainerJSON{
			ContainerJSONBase: &types.ContainerJSONBase{State: &types.ContainerState{Status: "running"}},
			NetworkSettings:   &types.NetworkSettings{NetworkSettingsBase: types.NetworkSettingsBase{Ports: ports}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func suiteOn(t *testing.T, srv *httptest.Server) *TestSuite {
	t.Helper()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	orch, err := docker.NewDockerOrchestrator()
	if err != nil {
		t.Fatal(err)
	}
	return &TestSuite{orch: orch}
}

// The host port is published a moment after StartContainer returns; a single
// inspect used to read an empty port and every spec then polled
// http://localhost:/healthz. The wait must keep polling until it appears.
func TestAppHostPortIsPolledUntilPublished(t *testing.T) {
	srv, calls := fakeDocker(t, 3, "49153")
	s := suiteOn(t, srv)
	got, err := s.waitForContainerPort(context.Background(), "app", "8080/tcp", appPortTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if got != "49153" || atomic.LoadInt32(calls) != 4 {
		t.Fatalf("port %q after %d inspects, want 49153 after 4", got, atomic.LoadInt32(calls))
	}
}

// A port that never appears is a clear error, not an empty string.
func TestAppHostPortNeverPublishedIsAnError(t *testing.T) {
	srv, _ := fakeDocker(t, 1<<30, "")
	s := suiteOn(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got, err := s.waitForContainerPort(ctx, "app", "8080/tcp", 300*time.Millisecond); err == nil {
		t.Fatalf("got port %q, want an error", got)
	}
}

// The app container's port must be read through the polling wait, never from a
// lone inspect right after StartContainer.
func TestAppContainerPortUsesThePollingWait(t *testing.T) {
	src, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "GetContainerInspect(ctx, appContainerName)") {
		t.Fatal("runner.go inspects the app container once for its host port; use waitForContainerPort")
	}
	if !strings.Contains(string(src), "waitForContainerPort(ctx, appContainerName") {
		t.Fatal("runner.go no longer waits for the app container's host port")
	}
}
