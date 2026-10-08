package runner

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeDockerDaemon is a minimal Docker Engine API stand-in. NewTestSuite builds
// its client from the environment (client.FromEnv), so pointing DOCKER_HOST at
// this server lets the tests observe exactly which containers and networks a
// suite asks the daemon to remove, with no real Docker and no production seam.
type fakeDockerDaemon struct {
	mu       sync.Mutex
	requests []string // "METHOD /path" with the API version prefix stripped
}

func newFakeDockerDaemon(t *testing.T) *fakeDockerDaemon {
	t.Helper()
	d := &fakeDockerDaemon{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/v") {
			if i := strings.Index(path[1:], "/"); i >= 0 {
				path = path[i+1:]
			}
		}
		if path == "/_ping" {
			w.Header().Set("API-Version", "1.45")
			w.WriteHeader(http.StatusOK)
			return
		}
		d.mu.Lock()
		d.requests = append(d.requests, r.Method+" "+path)
		d.mu.Unlock()

		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/logs"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && path == "/networks/create":
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "fake-network-id"})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	return d
}

func (d *fakeDockerDaemon) deletes() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, r := range d.requests {
		if strings.HasPrefix(r, "DELETE ") {
			out = append(out, r)
		}
	}
	return out
}

// Before the fix, CleanupSharedInfrastructure force-removed the shared kafka
// and db containers and the shared network by their fixed default names
// without checking that this run created them, so a second run in another repo
// deleted the first run's network mid-run. A suite that has created nothing
// must therefore ask the daemon to remove nothing.
func TestCleanupSharedInfrastructureOnlyRemovesOwnResources(t *testing.T) {
	t.Run("a suite that created nothing removes nothing", func(t *testing.T) {
		daemon := newFakeDockerDaemon(t)
		suite, err := NewTestSuite()
		if err != nil {
			t.Fatalf("NewTestSuite failed: %v", err)
		}

		_ = suite.CleanupSharedInfrastructure(context.Background())

		if got := daemon.deletes(); len(got) != 0 {
			t.Errorf("cleanup removed resources this run never created: %v", got)
		}
	})

	t.Run("default shared names are not the machine-wide fixed names", func(t *testing.T) {
		newFakeDockerDaemon(t)
		suite, err := NewTestSuite()
		if err != nil {
			t.Fatalf("NewTestSuite failed: %v", err)
		}

		if suite.networkName == "linespec-shared-net" {
			t.Errorf("default network name is still the shared fixed name %q", suite.networkName)
		}
		if suite.containerNaming.DatabaseContainer == "linespec-shared-db" {
			t.Errorf("default db container is still the shared fixed name %q", suite.containerNaming.DatabaseContainer)
		}
		if suite.containerNaming.KafkaContainer == "linespec-shared-kafka" {
			t.Errorf("default kafka container is still the shared fixed name %q", suite.containerNaming.KafkaContainer)
		}
	})

	t.Run("a suite that created shared resources removes exactly those", func(t *testing.T) {
		daemon := newCreatingFakeDockerDaemon(t)
		// Run from an empty directory: no services are discovered, so setup
		// creates the shared network and the shared Kafka container and nothing
		// else (no MySQL, no migrations).
		t.Chdir(t.TempDir())

		suite, err := NewTestSuite()
		if err != nil {
			t.Fatalf("NewTestSuite failed: %v", err)
		}
		if err := suite.SetupSharedInfrastructure(context.Background()); err != nil {
			t.Fatalf("SetupSharedInfrastructure against the fake daemon failed: %v", err)
		}
		created := daemon.createdResources()
		if len(created) == 0 {
			t.Fatal("setup created no resources against the fake daemon; test needs updating")
		}
		daemon.reset()

		_ = suite.CleanupSharedInfrastructure(context.Background())

		removed := daemon.removedResources()
		for _, want := range created {
			if !containsString(removed, want) {
				t.Errorf("cleanup did not remove %s that this run created; removed: %v", want, removed)
			}
		}
		for _, got := range removed {
			if !containsString(created, got) {
				t.Errorf("cleanup removed %s that this run did not create; created: %v", got, created)
			}
		}
	})
}

// The health-failure and verification-failure paths streamed logs from
// "app-"+spec.Name instead of the app_container template, so a custom or
// namespaced app_container name streamed nothing. Reaching those paths needs a
// live app container and a 120s health wait, so this checks the call sites
// structurally: no StreamLogs call in runner.go may build a hardcoded "app-"
// container name (the app container must come from GetAppContainer). Other
// StreamLogs calls stream proxy/db containers and are not constrained.
func TestAppLogStreamUsesAppContainerTemplate(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "runner.go", nil, 0)
	if err != nil {
		t.Fatalf("failed to parse runner.go: %v", err)
	}

	calls := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "StreamLogs" || len(call.Args) < 2 {
			return true
		}
		calls++
		pos := fset.Position(call.Pos())

		hardcodedApp := false
		ast.Inspect(call.Args[1], func(m ast.Node) bool {
			switch v := m.(type) {
			case *ast.BasicLit:
				if v.Kind == token.STRING && strings.HasPrefix(strings.Trim(v.Value, `"`), "app-") {
					hardcodedApp = true
				}
			}
			return true
		})
		if hardcodedApp {
			t.Errorf("%s: StreamLogs builds a hardcoded \"app-\" container name instead of using the app_container template", pos)
		}
		return true
	})

	if calls == 0 {
		t.Fatal("found no StreamLogs calls in runner.go; test needs updating")
	}
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// creatingFakeDockerDaemon extends the fake with just enough of the Engine API
// for SetupSharedInfrastructure to bring up the shared network and Kafka
// container, tracking which resources were created and which were removed.
// Resources are reported as "container NAME" / "network NAME" regardless of
// whether the caller addressed them by name or by the ID the fake handed out.
type creatingFakeDockerDaemon struct {
	mu        sync.Mutex
	created   []string
	removed   []string
	byID      map[string]string // id or name -> canonical "kind NAME"
	kafkaPort string
}

func newCreatingFakeDockerDaemon(t *testing.T) *creatingFakeDockerDaemon {
	t.Helper()
	// WaitTCPInternal dials localhost:<published port> to decide Kafka is ready.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	d := &creatingFakeDockerDaemon{
		byID:      map[string]string{},
		kafkaPort: ln.Addr().String()[strings.LastIndex(ln.Addr().String(), ":")+1:],
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/v") {
			if i := strings.Index(path[1:], "/"); i >= 0 {
				path = path[i+1:]
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()

		switch {
		case path == "/_ping":
			w.Header().Set("API-Version", "1.45")
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "sha256:fake"})
		case r.Method == http.MethodPost && path == "/networks/create":
			var body struct{ Name string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			key := "network " + body.Name
			d.byID["net-id-"+body.Name], d.byID[body.Name] = key, key
			d.created = append(d.created, key)
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "net-id-" + body.Name})
		case r.Method == http.MethodPost && path == "/containers/create":
			name := r.URL.Query().Get("name")
			key := "container " + name
			d.byID["ctr-id-"+name], d.byID[name] = key, key
			d.created = append(d.created, key)
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "ctr-id-" + name})
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":    id,
				"State": map[string]string{"Status": "running"},
				"NetworkSettings": map[string]any{"Ports": map[string]any{
					"9092/tcp": []map[string]string{{"HostIp": "0.0.0.0", "HostPort": d.kafkaPort}},
				}},
			})
		case r.Method == http.MethodDelete && (strings.HasPrefix(path, "/containers/") || strings.HasPrefix(path, "/networks/")):
			parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
			key, ok := d.byID[parts[1]]
			if !ok { // never created by this run: record it under the raw name
				key = strings.TrimSuffix(parts[0], "s") + " " + parts[1]
			}
			d.removed = append(d.removed, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	return d
}

func (d *creatingFakeDockerDaemon) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.removed = nil
}

func (d *creatingFakeDockerDaemon) createdResources() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.created...)
}

func (d *creatingFakeDockerDaemon) removedResources() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := append([]string(nil), d.removed...)
	sort.Strings(out)
	return out
}
