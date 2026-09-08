package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/livecodelife/linespec/v3/pkg/config"
)

// oracleNetwork attaches a container to the suite network under no alias. The
// database and its proxy take aliases; the reset container only needs to reach them.
func oracleNetwork(networkName string, aliases ...string) *network.NetworkingConfig {
	return &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			networkName: {Aliases: aliases},
		},
	}
}

// Oracle support in the runner is the other half of prov-2026-37958093, which taught
// the proxy to frame TNS and pull statements off the wire but left the runner unable
// to bring a database up in front of it.
//
// Two things here are Oracle's and not shared with the PostgreSQL branch this
// otherwise mirrors. Readiness is the image's own healthcheck script rather than a
// TCP probe, because the listener accepts connections roughly a minute before the
// pluggable database opens and a TCP probe reports ready that early. And reset runs
// as a container rather than over a connection, because prov-2026-37958093
// deliberately kept an Oracle driver out of this module and re-adding one to run a
// seed script would undo that; the database image already carries sqlplus.

const (
	// oracleDefaultService is the pluggable database gvenzl's images create by
	// default. A configured database name other than this one is passed as
	// ORACLE_DATABASE so the image creates it.
	oracleDefaultService = "FREEPDB1"

	// oracleHealthcheck is the readiness script gvenzl ships at a fixed path. It
	// exits zero only once the pluggable database is open, which is the state a
	// client needs and the one a TCP probe cannot see.
	oracleHealthcheck = "/opt/oracle/healthcheck.sh"

	// oracleResetScript is where the configured seed script is mounted. It is
	// deliberately NOT the image's /container-entrypoint-initdb.d: gvenzl runs user
	// scripts there BEFORE it creates APP_USER, so a seed that grants privileges to
	// the application account fails with ORA-01917 while the container still reports
	// a clean start, and what surfaces later is ORA-00942 from the application — the
	// error Oracle gives for a table you cannot see, naming the wrong cause. The
	// script is applied after the database opens instead, by the same reset container
	// that replays it between specs, so there is one moment and one code path.
	oracleResetScript = "/linespec-seed/init.sql"
)

// oracleService returns the service name a client should connect to.
func oracleService(db config.DatabaseConfig) string {
	if db.Database == "" {
		return oracleDefaultService
	}
	return db.Database
}

// oracleContainerSpec builds the database container. The seed script is never run by
// the image itself; see oracleResetScript for why.
//
// ORACLE_PASSWORD is the administrative password (SYS/SYSTEM) and APP_USER/
// APP_USER_PASSWORD create the application account the service connects as. Both are
// taken from the single username/password pair the config already states, because a
// throwaway test database has no reason to hold two secrets.
func oracleContainerSpec(db config.DatabaseConfig) (*container.Config, *container.HostConfig) {
	port := fmt.Sprintf("%d", db.Port)

	env := []string{"ORACLE_PASSWORD=" + db.Password}
	if db.Username != "" {
		env = append(env,
			"APP_USER="+db.Username,
			"APP_USER_PASSWORD="+db.Password,
		)
	}
	if svc := oracleService(db); svc != oracleDefaultService {
		env = append(env, "ORACLE_DATABASE="+svc)
	}

	return &container.Config{
			Image: db.Image,
			Env:   env,
			// Declared here rather than relied upon from the image: gvenzl's images
			// ship the script but no HEALTHCHECK instruction, so without this the
			// container has no health state to poll.
			Healthcheck: &container.HealthConfig{
				Test:        []string{"CMD-SHELL", oracleHealthcheck},
				Interval:    5 * time.Second,
				Timeout:     10 * time.Second,
				Retries:     120,
				StartPeriod: 10 * time.Second,
			},
		}, &container.HostConfig{
			PortBindings: map[nat.Port][]nat.PortBinding{
				nat.Port(port + "/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
			},
		}
}

// oracleResetSpec builds the short-lived container that replays the seed script
// between specs. It runs from the database image, so sqlplus is already present, and
// dials the real database by its network alias rather than through the proxy — a
// reset is setup, not traffic the spec under test should see.
func oracleResetSpec(db config.DatabaseConfig, realAlias, initScriptPath string) (*container.Config, *container.HostConfig) {
	dsn := fmt.Sprintf("system/%s@//%s:%d/%s", db.Password, realAlias, db.Port, oracleService(db))
	// EXIT SQL.SQLCODE makes sqlplus's exit status carry the script's failure, so a
	// broken seed surfaces as a failed container rather than a quietly empty table.
	script := fmt.Sprintf(
		"sqlplus -S -L %s <<'EOF'\nWHENEVER SQLERROR EXIT SQL.SQLCODE\n@%s\nEXIT\nEOF",
		dsn, oracleResetScript)

	return &container.Config{
			Image:      db.Image,
			Entrypoint: []string{"bash", "-lc"},
			Cmd:        []string{script},
		}, &container.HostConfig{
			Binds:      []string{initScriptPath + ":" + oracleResetScript + ":ro"},
			AutoRemove: false,
		}
}

// waitForOracle polls the container's health state until the pluggable database is
// open. It reads the healthcheck declared in oracleContainerSpec rather than probing
// the port itself, which is the whole reason that healthcheck is declared.
func (s *TestSuite) waitForOracle(ctx context.Context, containerName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		inspect, err := s.orch.GetContainerInspect(ctx, containerName)
		if err == nil && inspect.State != nil {
			if inspect.State.Health != nil {
				last = inspect.State.Health.Status
				if last == "healthy" {
					return nil
				}
			}
			if !inspect.State.Running && inspect.State.ExitCode != 0 {
				return fmt.Errorf("oracle container exited with code %d", inspect.State.ExitCode)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if last == "" {
		last = "no health state reported"
	}
	return fmt.Errorf("oracle did not become healthy within %s (last: %s)", timeout, last)
}

// resetOracle replays the seed script. A missing script is not an error — a suite
// whose specs only read has nothing to reset.
func (s *TestSuite) resetOracle(ctx context.Context, db config.DatabaseConfig, realAlias, initScriptPath, containerName string) error {
	if initScriptPath == "" {
		return nil
	}
	cfg, hostCfg := oracleResetSpec(db, realAlias, initScriptPath)
	if _, err := s.orch.StartContainer(ctx, cfg, hostCfg, oracleNetwork(s.networkName), containerName); err != nil {
		return fmt.Errorf("failed to start Oracle reset container: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.orch.StopAndRemoveContainer(cleanupCtx, containerName)
	}()

	statusCh, errCh := s.orch.WaitForContainer(ctx, containerName)
	select {
	case err := <-errCh:
		return fmt.Errorf("oracle reset container failed: %w", err)
	case status := <-statusCh:
		if status.StatusCode != 0 {
			return fmt.Errorf("oracle reset script exited %d: %s",
				status.StatusCode, s.captureContainerLogs(containerName))
		}
	case <-time.After(2 * time.Minute):
		return fmt.Errorf("oracle reset container timed out")
	}
	return nil
}
