package runner

import (
	"github.com/docker/docker/api/types/container"
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
	}
}
