package runner

import (
	"strings"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/config"
)

func oracleTestDB() config.DatabaseConfig {
	return config.DatabaseConfig{
		Name:       "primary",
		Type:       "oracle",
		Image:      "gvenzl/oracle-free:23-slim-faststart",
		Port:       1521,
		Host:       "db",
		Database:   "FREEPDB1",
		Username:   "erp",
		Password:   "linespec",
		InitScript: "init/vendor_types.sql",
	}
}

func TestOracleServiceDefaultsToTheImagesPluggableDatabase(t *testing.T) {
	db := oracleTestDB()
	db.Database = ""
	if got := oracleService(db); got != "FREEPDB1" {
		t.Fatalf("service = %q, want FREEPDB1", got)
	}
	db.Database = "ERPTEST"
	if got := oracleService(db); got != "ERPTEST" {
		t.Fatalf("service = %q, want ERPTEST", got)
	}
}

// Readiness is the reason this container carries a healthcheck at all: the image ships
// the script but no HEALTHCHECK instruction, and the listener accepts TCP long before
// the pluggable database opens, so a port probe reports ready about a minute early.
func TestOracleContainerDeclaresTheImagesHealthcheck(t *testing.T) {
	cfg, _ := oracleContainerSpec(oracleTestDB())
	if cfg.Healthcheck == nil {
		t.Fatal("no healthcheck declared; readiness would fall back to a port probe")
	}
	joined := strings.Join(cfg.Healthcheck.Test, " ")
	if !strings.Contains(joined, "/opt/oracle/healthcheck.sh") {
		t.Fatalf("healthcheck = %q, want the image's healthcheck.sh", joined)
	}
}

func TestOracleContainerCarriesAdminAndAppCredentials(t *testing.T) {
	cfg, _ := oracleContainerSpec(oracleTestDB())
	env := strings.Join(cfg.Env, "\n")
	for _, want := range []string{
		"ORACLE_PASSWORD=linespec",
		"APP_USER=erp",
		"APP_USER_PASSWORD=linespec",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("env missing %q; got:\n%s", want, env)
		}
	}
	// FREEPDB1 is what the image creates on its own, so asking for it again would
	// make the image build a second pluggable database for no reason.
	if strings.Contains(env, "ORACLE_DATABASE=") {
		t.Errorf("ORACLE_DATABASE set for the image's own default database:\n%s", env)
	}
}

func TestOracleContainerRequestsANonDefaultPluggableDatabase(t *testing.T) {
	db := oracleTestDB()
	db.Database = "ERPTEST"
	cfg, _ := oracleContainerSpec(db)
	if !strings.Contains(strings.Join(cfg.Env, "\n"), "ORACLE_DATABASE=ERPTEST") {
		t.Errorf("env does not ask the image to create ERPTEST: %v", cfg.Env)
	}
}

// The database container must not carry the seed at all. gvenzl runs the scripts in
// its init directory BEFORE it creates APP_USER, so a seed that grants privileges to
// the application account fails there with ORA-01917 while the container still reports
// a clean start — and the failure resurfaces as ORA-00942 from the application, which
// names the wrong cause. The seed is applied after the database opens instead.
func TestOracleContainerDoesNotRunTheSeedItself(t *testing.T) {
	_, hostCfg := oracleContainerSpec(oracleTestDB())
	joined := strings.Join(hostCfg.Binds, "\n")
	if strings.Contains(joined, "container-entrypoint-initdb.d") {
		t.Fatalf("seed mounted into the image's init directory, which runs before APP_USER exists:\n%s", joined)
	}
	if len(hostCfg.Binds) != 0 {
		t.Errorf("binds = %v, want none", hostCfg.Binds)
	}
}

// A reset is setup, not traffic: it must reach the database directly, because a reset
// routed through the proxy would register as interactions the spec never asked for.
func TestOracleResetDialsTheDatabaseAndNotTheProxy(t *testing.T) {
	cfg, _ := oracleResetSpec(oracleTestDB(), "real-db", "/host/init.sql")
	script := strings.Join(cfg.Cmd, " ")
	if !strings.Contains(script, "real-db:1521/FREEPDB1") {
		t.Fatalf("reset does not dial real-db: %s", script)
	}
	if strings.Contains(script, "@//db:") {
		t.Fatalf("reset dials the proxy alias: %s", script)
	}
}

// Without this, a seed that fails leaves sqlplus exiting zero and the spec runs
// against an empty table, reporting a mock-never-called failure that names the wrong
// thing.
func TestOracleResetFailsLoudlyOnASqlError(t *testing.T) {
	cfg, _ := oracleResetSpec(oracleTestDB(), "real-db", "/host/init.sql")
	script := strings.Join(cfg.Cmd, " ")
	if !strings.Contains(script, "WHENEVER SQLERROR EXIT SQL.SQLCODE") {
		t.Fatalf("reset does not propagate a SQL error to its exit status: %s", script)
	}
}

// No Oracle driver enters this module (prov-2026-37958093 kept one out deliberately),
// so the reset runs sqlplus from the database image itself.
func TestOracleResetRunsFromTheDatabaseImage(t *testing.T) {
	db := oracleTestDB()
	cfg, hostCfg := oracleResetSpec(db, "real-db", "/host/init.sql")
	if cfg.Image != db.Image {
		t.Errorf("reset image = %q, want the database image %q", cfg.Image, db.Image)
	}
	if !strings.Contains(strings.Join(hostCfg.Binds, "\n"), oracleResetScript) {
		t.Errorf("reset container cannot see the seed script: %v", hostCfg.Binds)
	}
}
