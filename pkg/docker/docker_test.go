package docker

import (
	"context"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

func TestDockerOrchestrator_NetworkAndContainer(t *testing.T) {
	ctx := context.Background()
	orch, err := NewDockerOrchestrator()
	if err != nil {
		t.Skip("Docker daemon not available")
		return
	}

	// Test Network
	netName := "test-network-linespec"
	netID, err := orch.CreateNetwork(ctx, netName)
	if err != nil {
		t.Fatalf("Failed to create network: %v", err)
	}
	defer orch.RemoveNetwork(ctx, netID)

	// Test Container
	config := &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"echo", "hello"},
	}

	// Pull image first
	err = orch.PullImage(ctx, "alpine:latest")
	if err != nil {
		t.Fatalf("Failed to pull image: %v", err)
	}

	containerID, err := orch.StartContainer(ctx, config, &container.HostConfig{}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			netName: {},
		},
	}, "test-alpine")
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer orch.StopAndRemoveContainer(ctx, containerID)

	if containerID == "" {
		t.Errorf("Expected non-empty container ID")
	}
}
