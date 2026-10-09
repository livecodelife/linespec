package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Proof tests for prov-2026-06d7d70b: the per-spec PostgreSQL, Oracle (main,
// seed, reset) and MongoDB containers must carry the same per-run isolation
// suffix (DefaultNamingSuffix) as the other default container names.
//
// Assumed API (code-author must implement exactly these in package config):
//
//	func PerSpecDatabaseContainerName(root, engine, host, specName string) string
//	    engine is one of "postgresql", "oracle", "mongodb".
//	    -> "linespec-<engine>-<host>-<SanitizeContainerName(specName)>-" + DefaultNamingSuffix(root)
//	func OracleSeedContainerName(root, host string) string
//	    -> "linespec-oracle-seed-<host>-" + DefaultNamingSuffix(root)
//	func OracleResetContainerName(root, host string) string
//	    -> "linespec-oracle-reset-<host>-" + DefaultNamingSuffix(root)

const (
	perSpecRootA = "/work/project-a"
	perSpecRootB = "/work/project-b"
	perSpecHost  = "db-host"
	perSpecSpec  = "create-user"
)

var dockerNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// perSpecNames resolves every per-spec/auxiliary DB container name for a root.
func perSpecNames(root, spec string) map[string]string {
	return map[string]string{
		"postgresql":   PerSpecDatabaseContainerName(root, "postgresql", perSpecHost, spec),
		"oracle":       PerSpecDatabaseContainerName(root, "oracle", perSpecHost, spec),
		"mongodb":      PerSpecDatabaseContainerName(root, "mongodb", perSpecHost, spec),
		"oracle-seed":  OracleSeedContainerName(root, perSpecHost),
		"oracle-reset": OracleResetContainerName(root, perSpecHost),
	}
}

func TestPerSpecDatabaseContainerNamesCarryRunSuffix(t *testing.T) {
	sfx := DefaultNamingSuffix(perSpecRootA)
	// Same suffix the other defaults use.
	if !strings.HasSuffix(DefaultContainerNaming(perSpecRootA).GetDatabaseContainer(ContainerNameParams{}), sfx) {
		t.Fatalf("precondition: shared db default should end with %q", sfx)
	}

	for role, name := range perSpecNames(perSpecRootA, perSpecSpec) {
		if !strings.HasSuffix(name, "-"+sfx) {
			t.Errorf("%s name %q does not end with the per-run suffix %q", role, name, sfx)
		}
		if !strings.HasPrefix(name, "linespec-"+role+"-") {
			t.Errorf("%s name %q lost its linespec-%s- prefix", role, name, role)
		}
		if !strings.Contains(name, perSpecHost) {
			t.Errorf("%s name %q does not contain the db host %q", role, name, perSpecHost)
		}
	}
	for _, role := range []string{"postgresql", "oracle", "mongodb"} {
		if n := perSpecNames(perSpecRootA, perSpecSpec)[role]; !strings.Contains(n, perSpecSpec) {
			t.Errorf("%s name %q does not contain the spec name", role, n)
		}
	}
}

func TestPerSpecDatabaseContainerNamesDifferAcrossProjectRoots(t *testing.T) {
	a := perSpecNames(perSpecRootA, perSpecSpec)
	b := perSpecNames(perSpecRootB, perSpecSpec)
	for role, na := range a {
		if na == "" {
			t.Errorf("%s resolved to an empty name", role)
		}
		if na == b[role] {
			t.Errorf("%s name %q is identical across two project roots; parallel runs would collide", role, na)
		}
	}
}

func TestPerSpecDatabaseContainerNamesStableAndDistinctWithinRun(t *testing.T) {
	first := perSpecNames(perSpecRootA, perSpecSpec)
	second := perSpecNames(perSpecRootA, perSpecSpec)
	seen := map[string]string{}
	for role, n := range first {
		// Cleanup removes containers by the exact recorded name, so the
		// derivation must be deterministic within a run.
		if n != second[role] {
			t.Errorf("%s name not stable within one run: %q vs %q", role, n, second[role])
		}
		if other, dup := seen[n]; dup {
			t.Errorf("%s and %s resolve to the same name %q", role, other, n)
		}
		seen[n] = role
	}
	// Different specs in the same run stay distinct (per-spec containers).
	if PerSpecDatabaseContainerName(perSpecRootA, "postgresql", perSpecHost, "spec-one") ==
		PerSpecDatabaseContainerName(perSpecRootA, "postgresql", perSpecHost, "spec-two") {
		t.Error("different specs must yield different per-spec container names")
	}
}

func TestPerSpecDatabaseContainerNamesAreDockerValid(t *testing.T) {
	for role, n := range perSpecNames(perSpecRootA, "Create User/42 (edge)") {
		if !dockerNameRE.MatchString(n) {
			t.Errorf("%s name %q is not a valid Docker container name", role, n)
		}
	}
	n := PerSpecDatabaseContainerName(perSpecRootA, "postgresql", perSpecHost, "Create User/42 (edge)")
	if !strings.Contains(n, SanitizeContainerName("Create User/42 (edge)")) {
		t.Errorf("name %q should embed the sanitised spec name", n)
	}
}

func TestExplicitContainerNamingOverridesStayVerbatim(t *testing.T) {
	cfg := loadIsolationConfig(t, isolationMinimalConfig+`
container_naming:
  database_container: my-db
  network_name: my-net
`)
	p := ContainerNameParams{ServiceName: "my-api", SpecName: perSpecSpec, Type: "db"}
	if got := cfg.ContainerNaming.GetDatabaseContainer(p); got != "my-db" {
		t.Errorf("database_container override not verbatim: %q", got)
	}
	if got := cfg.ContainerNaming.GetNetworkName(p); got != "my-net" {
		t.Errorf("network_name override not verbatim: %q", got)
	}
	// The per-spec names have no override field and stay suffixed regardless.
	for role, n := range perSpecNames(perSpecRootA, perSpecSpec) {
		if !strings.HasSuffix(n, DefaultNamingSuffix(perSpecRootA)) {
			t.Errorf("%s name %q must remain suffixed when other overrides exist", role, n)
		}
	}
}

// The runner must not build any Docker name from a hardcoded linespec-* literal
// outside ContainerNaming (constraint 5). Cleanup removes by the recorded name,
// so routing every site through the helpers keeps by-name cleanup correct.
func TestRunnerHasNoHardcodedPerSpecContainerLiterals(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "runner", "runner.go"))
	if err != nil {
		t.Fatalf("read runner.go: %v", err)
	}
	for _, lit := range []string{
		`"linespec-postgresql-`, `"linespec-oracle-`, `"linespec-oracle-seed-`,
		`"linespec-oracle-reset-`, `"linespec-mongodb-`,
	} {
		if strings.Contains(string(src), lit) {
			t.Errorf("pkg/runner/runner.go still builds a container name from hardcoded literal %s", lit)
		}
	}
}
