package runner

import (
	"testing"

	"github.com/docker/go-connections/nat"
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
