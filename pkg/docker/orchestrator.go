package docker

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

type DockerOrchestrator struct {
	cli *client.Client
}

func NewDockerOrchestrator() (*DockerOrchestrator, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &DockerOrchestrator{cli: cli}, nil
}

func (d *DockerOrchestrator) CreateNetwork(ctx context.Context, name string) (string, error) {
	res, err := d.cli.NetworkCreate(ctx, name, types.NetworkCreate{})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

func (d *DockerOrchestrator) RemoveNetwork(ctx context.Context, id string) error {
	return d.cli.NetworkRemove(ctx, id)
}

func (d *DockerOrchestrator) PullImage(ctx context.Context, imageName string) error {
	reader, err := d.cli.ImagePull(ctx, imageName, types.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

func (d *DockerOrchestrator) StartContainer(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, containerName string) (string, error) {
	resp, err := d.cli.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, containerName)
	if err != nil {
		return "", err
	}

	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", err
	}

	return resp.ID, nil
}

func (d *DockerOrchestrator) StopAndRemoveContainer(ctx context.Context, id string) error {
	timeout := 10
	if err := d.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout}); err != nil {
		fmt.Printf("Warning: failed to stop container %s: %v\n", id, err)
	}
	return d.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true, RemoveVolumes: true})
}

func (d *DockerOrchestrator) GetContainerInspect(ctx context.Context, id string) (types.ContainerJSON, error) {
	return d.cli.ContainerInspect(ctx, id)
}

func (d *DockerOrchestrator) GetNetworkGateway(ctx context.Context, id string) (string, error) {
	inspect, err := d.cli.NetworkInspect(ctx, id, types.NetworkInspectOptions{})
	if err != nil {
		return "", err
	}
	if len(inspect.IPAM.Config) > 0 {
		return inspect.IPAM.Config[0].Gateway, nil
	}
	return "", fmt.Errorf("no gateway found for network %s", id)
}

func (d *DockerOrchestrator) GetContainerIP(ctx context.Context, id string, networkName string) (string, error) {
	inspect, err := d.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", err
	}
	netSettings := inspect.NetworkSettings.Networks[networkName]
	if netSettings == nil {
		return "", fmt.Errorf("container not in network %s", networkName)
	}
	return netSettings.IPAddress, nil
}

// Prober methods

func (d *DockerOrchestrator) WaitTCPInternal(ctx context.Context, networkName, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	config := &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sh", "-c", fmt.Sprintf("until nc -w 1 %s; do sleep 1; done", strings.Replace(address, ":", " ", 1))},
	}

	for time.Now().Before(deadline) {
		id, err := d.StartContainer(ctx, config, &container.HostConfig{}, &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{networkName: {}},
		}, "waiter-"+fmt.Sprintf("%d", time.Now().UnixNano()))
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		// Wait for waiter to exit
		statusCh, errCh := d.cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)
		select {
		case <-statusCh:
			d.StopAndRemoveContainer(context.Background(), id)
			return nil
		case <-errCh:
			d.StopAndRemoveContainer(context.Background(), id)
		case <-time.After(5 * time.Second):
			d.StopAndRemoveContainer(context.Background(), id)
		}
	}
	return fmt.Errorf("timeout waiting for internal TCP %s", address)
}

func (d *DockerOrchestrator) WaitTCP(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 1*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for TCP %s", address)
}

func (d *DockerOrchestrator) WaitHTTP(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	httpClient := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
		resp, err := httpClient.Do(req)
		if err == nil {
			resp.Body.Close()
			fmt.Printf("WaitHTTP: %s returned %d\n", url, resp.StatusCode)
			if resp.StatusCode < 500 { // 200 or 404 is usually fine for Rails boot
				return nil
			}
		} else {
			// fmt.Printf("WaitHTTP: %s error: %v\n", url, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return fmt.Errorf("timeout waiting for HTTP %s", url)
}

func (d *DockerOrchestrator) StreamLogs(ctx context.Context, id string, stdout io.Writer, stderr io.Writer) error {
	options := container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: false,
	}
	reader, err := d.cli.ContainerLogs(ctx, id, options)
	if err != nil {
		return err
	}
	defer reader.Close()

	_, err = stdcopy.StdCopy(stdout, stderr, reader)
	if err != nil && err != io.EOF {
		return err
	}
	return nil
}
