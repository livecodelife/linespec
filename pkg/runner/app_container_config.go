package runner

import (
	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
)

// appContainerConfig is the configuration of the app container. It declares
// the application port as exposed as well as binding it: a daemon that
// publishes only exposed ports maps nothing for an image without its own
// EXPOSE, and the wait for the host port then times out.
func appContainerConfig(image string, env, cmd []string, appPort string) *container.Config {
	return &container.Config{
		Image: image,
		Env:   env,
		Cmd:   cmd,
		ExposedPorts: map[nat.Port]struct{}{
			nat.Port(appPort + "/tcp"): {},
		},
	}
}
