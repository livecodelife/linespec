package runner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"

	"github.com/livecodelife/linespec/v3/pkg/docker"
)

// The app container must declare its port as exposed, not only bind it: a
// daemon that publishes only exposed ports (Docker 28.0.4) maps nothing for an
// image without its own EXPOSE, and the wait for the host port times out.
func TestAppContainerConfigExposesTheAppPort(t *testing.T) {
	cfg := appContainerConfig("svc:latest", []string{"PORT=3000"}, []string{"sh", "-c", "start"}, "3000")
	if _, ok := cfg.ExposedPorts[nat.Port("3000/tcp")]; !ok {
		t.Fatalf("ExposedPorts = %v, want 3000/tcp", cfg.ExposedPorts)
	}
	if cfg.Image != "svc:latest" || len(cfg.Env) != 1 || len(cfg.Cmd) != 3 {
		t.Fatalf("image, env and command were not carried over: %+v", cfg)
	}
}

// Against a real daemon: an image with no EXPOSE of its own must still get a
// published host port when it is started with the app container configuration.
// Without the exposed port a daemon that publishes only exposed ports (Docker
// 28 on GitHub runners) maps nothing and the wait for the host port times out.
// Skipped when no Docker daemon is reachable.
func TestAppContainerConfigPublishesAPortForAnImageWithoutExpose(t *testing.T) {
	orch, err := docker.NewDockerOrchestrator()
	if err != nil {
		t.Skipf("no Docker: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	const image = "busybox:1.36"
	if err := orch.EnsureImage(ctx, image); err != nil {
		t.Skipf("cannot get %s: %v", image, err)
	}
	name := fmt.Sprintf("linespec-expose-test-%d", time.Now().UnixNano())
	_, err = orch.StartContainer(ctx,
		appContainerConfig(image, nil, []string{"sleep", "60"}, "3000"),
		&container.HostConfig{PortBindings: map[nat.Port][]nat.PortBinding{
			"3000/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
		}}, nil, name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = orch.StopAndRemoveContainer(context.Background(), name) }()
	s := &TestSuite{orch: orch}
	if port, err := s.waitForContainerPort(ctx, name, "3000/tcp", appPortTimeout); err != nil || port == "" {
		t.Fatalf("no host port for an image without EXPOSE: %q, %v", port, err)
	}
}
