package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/types"
)

func TestMockRegistry_RegisterAndFind(t *testing.T) {
	reg := NewMockRegistry()

	spec := &types.TestSpec{
		Name: "create_user_success",
		Expects: []types.ExpectStatement{
			{
				Channel:  types.WriteMySQL,
				Table:    "users",
				WithFile: "payloads/user_db_write_record.json",
				Verify: []types.VerifyRule{
					{Type: "CONTAINS", Pattern: "password_digest"},
				},
			},
		},
	}

	reg.Register(spec)

	// Simulate an incoming query
	mock, found := reg.FindMock("users", "INSERT INTO users (name, email) VALUES ('John', 'john@example.com')")
	if !found {
		t.Fatalf("Expected to find mock for table 'users'")
	}

	if mock.Table != "users" {
		t.Errorf("Expected table 'users', got %s", mock.Table)
	}

	if len(mock.Verify) != 1 || mock.Verify[0].Pattern != "password_digest" {
		t.Errorf("Verify rules not preserved")
	}
}

func TestMockRegistry_DeterministicFallbackOrder(t *testing.T) {
	// Run many iterations to catch non-determinism from Go map randomization.
	// With 3+ keys and the old map-iteration fallback, the wrong mock would be
	// returned on most runs. With orderedMocks, "alpha" is always returned.
	for attempt := 0; attempt < 50; attempt++ {
		reg := NewMockRegistry()

		spec := &types.TestSpec{
			Name: "deterministic_test",
			Expects: []types.ExpectStatement{
				{
					Channel:     types.ReadMySQL,
					Table:       "alpha",
					SQL:         "SELECT * FROM users WHERE id = 1",
					ReturnsFile: "alpha_response.json",
				},
				{
					Channel:     types.ReadMySQL,
					Table:       "beta",
					SQL:         "SELECT * FROM users WHERE id = 1",
					ReturnsFile: "beta_response.json",
				},
				{
					Channel:     types.ReadMySQL,
					Table:       "gamma",
					SQL:         "SELECT * FROM users WHERE id = 1",
					ReturnsFile: "gamma_response.json",
				},
			},
		}
		reg.Register(spec)

		// PeekMock with a key that doesn't exist — triggers the fallback path
		mock, found := reg.PeekMock("nonexistent", "SELECT * FROM users WHERE id = 1")
		if !found {
			t.Fatalf("attempt %d: expected fallback match in PeekMock", attempt)
		}
		if mock.Table != "alpha" {
			t.Fatalf("attempt %d: PeekMock fallback returned %q, want %q (first declared)", attempt, mock.Table, "alpha")
		}

		// FindMock with the same nonexistent key
		reg.ResetHits()
		mock, found = reg.FindMock("nonexistent", "SELECT * FROM users WHERE id = 1")
		if !found {
			t.Fatalf("attempt %d: expected fallback match in FindMock", attempt)
		}
		if mock.Table != "alpha" {
			t.Fatalf("attempt %d: FindMock fallback returned %q, want %q (first declared)", attempt, mock.Table, "alpha")
		}
	}
}

func TestMockRegistry_SQLMatching(t *testing.T) {
	reg := NewMockRegistry()

	spec := &types.TestSpec{
		Name: "get_user_success",
		Expects: []types.ExpectStatement{
			{
				Channel: types.ReadMySQL,
				Table:   "users",
				SQL:     "SELECT * FROM users WHERE id = 42 LIMIT 1",
			},
		},
	}

	reg.Register(spec)

	// Exact match
	_, found := reg.FindMock("users", "SELECT * FROM users WHERE id = 42 LIMIT 1")
	if !found {
		t.Errorf("Expected exact SQL match to work")
	}

	// Backtick normalization: `users` and `id` are stripped
	reg.ResetHits()
	_, found = reg.FindMock("users", "SELECT * FROM `users` WHERE `id` = 42 LIMIT 1")
	if !found {
		t.Errorf("Expected backtick-normalized SQL match to work")
	}

	// Table-qualified column (users.id) does NOT match mock that uses bare `id`.
	// Engineers must write USING_SQL to match what the ORM actually produces, or use
	// USING_SQL_CONTAINS for a fragment match.
	reg.ResetHits()
	_, found = reg.FindMock("users", "SELECT * FROM `users` WHERE `users`.`id` = 42 LIMIT 1")
	if found {
		t.Errorf("USING_SQL should not match when mock uses 'id' but query uses 'users.id' — use USING_SQL_CONTAINS for fragment matching")
	}
}

func TestMockRegistry_SQLContainsMatching(t *testing.T) {
	reg := NewMockRegistry()

	spec := &types.TestSpec{
		Name: "get_user_contains",
		Expects: []types.ExpectStatement{
			{
				Channel:     types.ReadMySQL,
				Table:       "users",
				SQLContains: "WHERE users.id = 42",
				ReturnsFile: "payloads/user.json",
			},
		},
	}
	reg.Register(spec)

	// Full ORM-generated query with table prefix and backtick quoting
	_, found := reg.FindMock("users", "SELECT * FROM `users` WHERE `users`.`id` = 42 LIMIT 1")
	if !found {
		t.Errorf("USING_SQL_CONTAINS should match when fragment is present in query")
	}

	// Fragment not present — should not match
	reg.ResetHits()
	_, found = reg.FindMock("users", "SELECT * FROM `users` WHERE `users`.`token` = 'abc' LIMIT 1")
	if found {
		t.Errorf("USING_SQL_CONTAINS should not match when fragment is absent from query")
	}
}

func TestMockRegistry_SQLContains_DoesNotMatchSQL(t *testing.T) {
	// A mock with SQL (exact) and one with SQLContains must not cross-match.
	reg := NewMockRegistry()

	spec := &types.TestSpec{
		Name: "disambiguation_test",
		Expects: []types.ExpectStatement{
			{
				Channel:     types.ReadMySQL,
				Table:       "users",
				SQL:         "SELECT * FROM users WHERE users.id = 1",
				ReturnsFile: "payloads/user_one.json",
			},
			{
				Channel:     types.ReadMySQL,
				Table:       "users",
				SQLContains: "WHERE users.id = 2",
				ReturnsFile: "payloads/user_two.json",
			},
		},
	}
	reg.Register(spec)

	// Query for id=1 should hit the exact-match mock
	mock, found := reg.FindMock("users", "SELECT * FROM users WHERE users.id = 1")
	if !found {
		t.Fatal("Expected exact SQL mock to match id=1 query")
	}
	if mock.ReturnsFile != "payloads/user_one.json" {
		t.Errorf("Wrong mock returned for id=1: got %q", mock.ReturnsFile)
	}

	// Query for id=2 should hit the contains mock
	mock, found = reg.FindMock("users", "SELECT * FROM `users` WHERE `users`.`id` = 2 LIMIT 1")
	if !found {
		t.Fatal("Expected USING_SQL_CONTAINS mock to match id=2 query")
	}
	if mock.ReturnsFile != "payloads/user_two.json" {
		t.Errorf("Wrong mock returned for id=2: got %q", mock.ReturnsFile)
	}
}

func TestMockRegistry_NegativeMockNotReturnedByFindMock(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "negative_test",
		ExpectsNot: []types.ExpectStatement{
			{
				Channel: types.WriteMySQL,
				Table:   "users",
			},
		},
	}
	reg.Register(spec)

	_, found := reg.FindMock("users", "INSERT INTO users (name) VALUES ('John')")
	if found {
		t.Fatal("FindMock must not return negative mocks as interceptors")
	}
}

func TestMockRegistry_NegativeMockNotReturnedByPeekMock(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "negative_peek_test",
		ExpectsNot: []types.ExpectStatement{
			{
				Channel: types.ReadMySQL,
				Table:   "users",
			},
		},
	}
	reg.Register(spec)

	_, found := reg.PeekMock("users", "SELECT * FROM users WHERE id = 1")
	if found {
		t.Fatal("PeekMock must not return negative mocks as interceptors")
	}
}

func TestMockRegistry_NegativeMockNotReturnedByFindHTTPMock(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "negative_http_test",
		ExpectsNot: []types.ExpectStatement{
			{
				Channel: types.HTTP,
				URL:     "/api/users",
				Method:  "GET",
			},
		},
	}
	reg.Register(spec)

	_, found := reg.FindHTTPMock("/api/users", "GET")
	if found {
		t.Fatal("FindHTTPMock must not return negative mocks as interceptors")
	}
}

func TestMockRegistry_CheckNegativeMocksIncrementsHits(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "negative_check_test",
		ExpectsNot: []types.ExpectStatement{
			{
				Channel: types.WriteMySQL,
				Table:   "users",
			},
		},
	}
	reg.Register(spec)

	reg.CheckNegativeMocks("users", "INSERT INTO users (name) VALUES ('John')")

	err := reg.VerifyAll()
	if err == nil {
		t.Fatal("VerifyAll should fail when a negative expectation is violated")
	}
}

func TestMockRegistry_NegativeExpectationPassesWhenNotCalled(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "negative_pass_test",
		ExpectsNot: []types.ExpectStatement{
			{
				Channel: types.WriteMySQL,
				Table:   "users",
			},
		},
	}
	reg.Register(spec)

	// Don't call CheckNegativeMocks — the negative expectation should pass
	err := reg.VerifyAll()
	if err != nil {
		t.Fatalf("VerifyAll should pass when negative expectation is not violated, got: %v", err)
	}
}

func TestMockRegistry_MixedPositiveAndNegative(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "mixed_test",
		Expects: []types.ExpectStatement{
			{
				Channel: types.WriteMySQL,
				Table:   "todos",
			},
		},
		ExpectsNot: []types.ExpectStatement{
			{
				Channel: types.WriteMySQL,
				Table:   "users",
			},
		},
	}
	reg.Register(spec)

	// FindMock should only return the positive mock
	mock, found := reg.FindMock("todos", "INSERT INTO todos (title) VALUES ('test')")
	if !found {
		t.Fatal("FindMock should find the positive mock for 'todos'")
	}
	if mock.Table != "todos" {
		t.Fatalf("Expected positive mock for 'todos', got %q", mock.Table)
	}

	_, found = reg.FindMock("users", "INSERT INTO users (name) VALUES ('John')")
	if found {
		t.Fatal("FindMock must not return the negative mock for 'users'")
	}

	// Simulate the service calling the forbidden endpoint — negative hit recorded
	reg.CheckNegativeMocks("users", "INSERT INTO users (name) VALUES ('John')")

	// Positive mock was never hit, so VerifyAll should fail on the positive expectation first
	// but negative is also violated — either way it should fail
	err := reg.VerifyAll()
	if err == nil {
		t.Fatal("VerifyAll should fail when negative expectation is violated")
	}
}

func TestMockRegistry_VerifyErrorCausesVerifyAllFailure(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "verify_error_test",
		Expects: []types.ExpectStatement{
			{
				Channel: types.WritePostgreSQL,
				Table:   "orders",
			},
		},
	}
	reg.Register(spec)

	// Simulate the mock being hit (hit count incremented)
	mock, found := reg.FindMock("orders", "INSERT INTO orders (customer_name) VALUES ('Alice')")
	if !found {
		t.Fatal("FindMock should find the mock for 'orders'")
	}
	_ = mock

	// VerifyAll should pass because the mock was hit
	if err := reg.VerifyAll(); err != nil {
		t.Fatalf("VerifyAll should pass with hit mock and no verify errors, got: %v", err)
	}

	// Now record a VERIFY rule failure (as a proxy would after FindMock increments hit)
	reg.RecordVerifyError("WRITE:POSTGRESQL [orders]: query does not contain 'something_invalid'")

	// VerifyAll should now fail due to the recorded VERIFY error
	err := reg.VerifyAll()
	if err == nil {
		t.Fatal("VerifyAll should fail when a VERIFY error was recorded")
	}
	if !contains(err.Error(), "VERIFY failed") {
		t.Errorf("Expected error to mention 'VERIFY failed', got: %v", err)
	}
}

func TestMockRegistry_AddVerifyErrorsMergesFromSidecar(t *testing.T) {
	reg := NewMockRegistry()

	reg.AddVerifyErrors([]string{"WRITE:POSTGRESQL [orders]: query does not contain 'x'"})
	reg.AddVerifyErrors([]string{"HTTP [POST /api]: header missing"})

	errs := reg.GetVerifyErrors()
	if len(errs) != 2 {
		t.Fatalf("Expected 2 verify errors, got %d", len(errs))
	}

	if err := reg.VerifyAll(); err == nil {
		t.Fatal("VerifyAll should fail when verify errors exist")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestMockRegistry_SeedTopicAndGetSeeds(t *testing.T) {
	reg := NewMockRegistry()
	reg.SeedTopic("orders", []byte(`{"id":1}`))
	reg.SeedTopic("orders", []byte(`{"id":2}`))
	reg.SeedTopic("events", []byte(`{"type":"created"}`))

	seeds := reg.GetSeeds()
	if len(seeds["orders"]) != 2 {
		t.Errorf("Expected 2 seeds for 'orders', got %d", len(seeds["orders"]))
	}
	if len(seeds["events"]) != 1 {
		t.Errorf("Expected 1 seed for 'events', got %d", len(seeds["events"]))
	}
	if string(seeds["orders"][0]) != `{"id":1}` {
		t.Errorf("Unexpected first seed value: %s", seeds["orders"][0])
	}
}

func TestMockRegistry_SeedsPersistThroughFile(t *testing.T) {
	reg := NewMockRegistry()
	reg.SeedTopic("todo-events", []byte(`{"title":"buy milk"}`))

	// Also register a mock so the file has both sections.
	reg.Register(&types.TestSpec{
		Expects: []types.ExpectStatement{
			{Channel: types.WriteMySQL, Table: "todos"},
		},
	})

	tmpFile := filepath.Join(t.TempDir(), "registry.json")
	if err := reg.SaveToFile(tmpFile); err != nil {
		t.Fatalf("SaveToFile failed: %v", err)
	}

	reg2 := NewMockRegistry()
	if err := reg2.LoadFromFile(tmpFile); err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	seeds := reg2.GetSeeds()
	if len(seeds["todo-events"]) != 1 {
		t.Errorf("Expected 1 seeded message for 'todo-events', got %d", len(seeds["todo-events"]))
	}
	if string(seeds["todo-events"][0]) != `{"title":"buy milk"}` {
		t.Errorf("Unexpected seed value: %s", seeds["todo-events"][0])
	}
}

func TestMockRegistry_LegacyFileFormat(t *testing.T) {
	// Write a registry file in the old bare-map format (no "mocks"/"seeds" wrapper).
	legacy := map[string][]*types.ExpectStatement{
		"users": {
			{Channel: types.WriteMySQL, Table: "users"},
		},
	}
	data, _ := json.Marshal(legacy)
	tmpFile := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("Failed to write legacy file: %v", err)
	}

	reg := NewMockRegistry()
	if err := reg.LoadFromFile(tmpFile); err != nil {
		t.Fatalf("LoadFromFile failed for legacy format: %v", err)
	}

	mock, found := reg.FindMock("users", "INSERT INTO users")
	if !found || mock == nil {
		t.Error("Expected to find mock from legacy file")
	}
}

func TestMockRegistry_FindGRPCMock(t *testing.T) {
	reg := NewMockRegistry()

	spec := &types.TestSpec{
		Name: "grpc_get_user",
		Expects: []types.ExpectStatement{
			{
				Channel:     types.GRPC,
				Service:     "users.UserService",
				RPCMethod:   "GetUser",
				ReturnsFile: "payloads/user.json",
			},
			{
				Channel:   types.GRPC,
				Service:   "users.UserService",
				RPCMethod: "ListUsers",
			},
		},
	}
	reg.Register(spec)

	// Find the GetUser mock
	mock, found := reg.FindGRPCMock("users.UserService", "GetUser")
	if !found {
		t.Fatal("Expected to find GetUser mock")
	}
	if mock.Service != "users.UserService" {
		t.Errorf("Expected service 'users.UserService', got %q", mock.Service)
	}
	if mock.RPCMethod != "GetUser" {
		t.Errorf("Expected method 'GetUser', got %q", mock.RPCMethod)
	}

	// GetUser mock is consumed — second call should not find it
	_, found = reg.FindGRPCMock("users.UserService", "GetUser")
	if found {
		t.Error("Mock should be consumed after first hit")
	}

	// ListUsers mock should still be findable
	_, found = reg.FindGRPCMock("users.UserService", "ListUsers")
	if !found {
		t.Error("Expected to find ListUsers mock")
	}

	// Non-existent mock
	_, found = reg.FindGRPCMock("users.UserService", "DeleteUser")
	if found {
		t.Error("Should not find DeleteUser mock")
	}
}

func TestMockRegistry_FindRedisMock(t *testing.T) {
	reg := NewMockRegistry()

	spec := &types.TestSpec{
		Name: "redis_ops",
		Expects: []types.ExpectStatement{
			{
				Channel:      types.ReadRedis,
				Command:      "GET",
				RedisKey:     "user:123",
				ReturnsFile:  "payloads/user.json",
			},
			{
				Channel:  types.WriteRedis,
				Command:  "SET",
				RedisKey: "session:abc",
			},
		},
	}
	reg.Register(spec)

	// Find the GET mock
	mock, found := reg.FindRedisMock("GET", "user:123")
	if !found {
		t.Fatal("Expected to find GET user:123 mock")
	}
	if mock.Command != "GET" {
		t.Errorf("Expected command 'GET', got %q", mock.Command)
	}
	if mock.RedisKey != "user:123" {
		t.Errorf("Expected key 'user:123', got %q", mock.RedisKey)
	}

	// GET mock is consumed
	_, found = reg.FindRedisMock("GET", "user:123")
	if found {
		t.Error("Mock should be consumed after first hit")
	}

	// SET mock should still be findable
	_, found = reg.FindRedisMock("SET", "session:abc")
	if !found {
		t.Error("Expected to find SET session:abc mock")
	}

	// Non-existent mock
	_, found = reg.FindRedisMock("DEL", "user:123")
	if found {
		t.Error("Should not find DEL user:123 mock")
	}
}

func TestMockRegistry_GetHits_GRPC(t *testing.T) {
	reg := NewMockRegistry()

	spec := &types.TestSpec{
		Name: "grpc_test",
		Expects: []types.ExpectStatement{
			{
				Channel:   types.GRPC,
				Service:   "users.UserService",
				RPCMethod: "GetUser",
			},
		},
	}
	reg.Register(spec)

	reg.FindGRPCMock("users.UserService", "GetUser")

	hits := reg.GetHits()
	key := "GRPC-users.UserService/GetUser"
	if hits[key] != 1 {
		t.Errorf("Expected hit count 1 for key %q, got %d", key, hits[key])
	}
}

func TestMockRegistry_GetHits_Redis(t *testing.T) {
	reg := NewMockRegistry()

	spec := &types.TestSpec{
		Name: "redis_test",
		Expects: []types.ExpectStatement{
			{
				Channel:  types.ReadRedis,
				Command:  "GET",
				RedisKey: "user:123",
			},
		},
	}
	reg.Register(spec)

	reg.FindRedisMock("GET", "user:123")

	hits := reg.GetHits()
	key := "READ_REDIS-GET:user:123"
	if hits[key] != 1 {
		t.Errorf("Expected hit count 1 for key %q, got %d", key, hits[key])
	}
}

// ── Semantic SQL matching registry tests ─────────────────────────────────────

func TestSemanticRegistry_BasicAccessingTables(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "semantic_basic",
		Expects: []types.ExpectStatement{
			{
				Channel:        types.ReadMySQL,
				AccessingTables: []string{"users"},
			},
		},
	}
	reg.Register(spec)

	// Should match a SELECT on users
	mock, ok := reg.FindMockByTables([]string{"users"}, "SELECT", nil, nil, nil)
	if !ok {
		t.Fatal("Expected FindMockByTables to find a mock")
	}
	if mock.Channel != types.ReadMySQL {
		t.Errorf("Expected READ_MYSQL channel, got %s", mock.Channel)
	}

	// Second call should not match (already consumed)
	_, ok = reg.FindMockByTables([]string{"users"}, "SELECT", nil, nil, nil)
	if ok {
		t.Error("Expected no mock on second call (already consumed)")
	}
}

func TestSemanticRegistry_VerifyOperationFilter(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "semantic_op_filter",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.ReadMySQL,
				AccessingTables: []string{"users"},
				VerifyOperation: "SELECT",
			},
			{
				Channel:         types.WriteMySQL,
				AccessingTables: []string{"users"},
				VerifyOperation: "INSERT",
			},
		},
	}
	reg.Register(spec)

	// SELECT should match the first mock
	mock, ok := reg.FindMockByTables([]string{"users"}, "SELECT", nil, nil, nil)
	if !ok {
		t.Fatal("Expected SELECT mock to match")
	}
	if mock.VerifyOperation != "SELECT" {
		t.Errorf("Expected SELECT mock, got %s", mock.VerifyOperation)
	}

	// INSERT should match the second mock
	mock, ok = reg.FindMockByTables([]string{"users"}, "INSERT", nil, nil, nil)
	if !ok {
		t.Fatal("Expected INSERT mock to match")
	}
	if mock.VerifyOperation != "INSERT" {
		t.Errorf("Expected INSERT mock, got %s", mock.VerifyOperation)
	}
}

func TestSemanticRegistry_VerifyWhereDisambiguation(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "semantic_where",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.ReadMySQL,
				AccessingTables: []string{"users"},
				VerifyWhere:     map[string]string{"token": "PRESENT"},
			},
			{
				Channel:         types.ReadMySQL,
				AccessingTables: []string{"users"},
				VerifyWhere:     map[string]string{"id": "42"},
			},
		},
	}
	reg.Register(spec)

	// Auth query: WHERE token = '...' → matches first mock
	mock, ok := reg.FindMockByTables(
		[]string{"users"}, "SELECT",
		[]string{"token"}, map[string]string{"token": "abc123"},
		nil,
	)
	if !ok {
		t.Fatal("Expected token mock to match")
	}
	if mock.VerifyWhere["token"] != "PRESENT" {
		t.Errorf("Expected token:PRESENT mock, got %v", mock.VerifyWhere)
	}

	// Get-by-id query: WHERE id = 42 → matches second mock
	mock, ok = reg.FindMockByTables(
		[]string{"users"}, "SELECT",
		[]string{"id"}, map[string]string{"id": "42"},
		nil,
	)
	if !ok {
		t.Fatal("Expected id mock to match")
	}
	if mock.VerifyWhere["id"] != "42" {
		t.Errorf("Expected id:42 mock, got %v", mock.VerifyWhere)
	}
}

func TestSemanticRegistry_VerifyWrittenValues(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "semantic_written",
		Expects: []types.ExpectStatement{
			{
				Channel:             types.WriteMySQL,
				AccessingTables:     []string{"users"},
				VerifyOperation:     "INSERT",
				VerifyWrittenValues: map[string]string{"email": "john@example.com", "name": "John Doe"},
			},
		},
	}
	reg.Register(spec)

	// Should match when written values match
	_, ok := reg.FindMockByTables(
		[]string{"users"}, "INSERT",
		nil, nil,
		map[string]string{"email": "john@example.com", "name": "John Doe"},
	)
	if !ok {
		t.Fatal("Expected mock to match with correct written values")
	}
}

func TestSemanticRegistry_CallNOrdering(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "semantic_call_n",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.ReadMySQL,
				AccessingTables: []string{"users"},
				VerifyOperation: "SELECT",
				CallN:           1,
			},
			{
				Channel:         types.ReadMySQL,
				AccessingTables: []string{"users"},
				VerifyOperation: "SELECT",
				CallN:           2,
				ReturnsEmpty:    true,
			},
		},
	}
	reg.Register(spec)

	// First call should consume CALL 1
	mock1, ok := reg.FindMockByTables([]string{"users"}, "SELECT", nil, nil, nil)
	if !ok {
		t.Fatal("Expected first mock (CALL 1)")
	}
	if mock1.CallN != 1 {
		t.Errorf("Expected CallN=1, got %d", mock1.CallN)
	}

	// Second call should consume CALL 2
	mock2, ok := reg.FindMockByTables([]string{"users"}, "SELECT", nil, nil, nil)
	if !ok {
		t.Fatal("Expected second mock (CALL 2)")
	}
	if mock2.CallN != 2 {
		t.Errorf("Expected CallN=2, got %d", mock2.CallN)
	}
	if !mock2.ReturnsEmpty {
		t.Error("Expected CALL 2 mock to have ReturnsEmpty=true")
	}

	// Third call should find nothing
	_, ok = reg.FindMockByTables([]string{"users"}, "SELECT", nil, nil, nil)
	if ok {
		t.Error("Expected no mock on third call")
	}
}

func TestSemanticRegistry_SpecificityWins(t *testing.T) {
	reg := NewMockRegistry()
	// Mock 1: lower specificity (only table set)
	// Mock 2: higher specificity (table set + VERIFY_OPERATION)
	spec := &types.TestSpec{
		Name: "semantic_specificity",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.ReadMySQL,
				AccessingTables: []string{"orders"},
			},
			{
				Channel:         types.ReadMySQL,
				AccessingTables: []string{"orders"},
				VerifyOperation: "SELECT",
			},
		},
	}
	reg.Register(spec)

	// Should prefer the more specific mock (VerifyOperation=SELECT)
	mock, ok := reg.FindMockByTables([]string{"orders"}, "SELECT", nil, nil, nil)
	if !ok {
		t.Fatal("Expected mock to match")
	}
	if mock.VerifyOperation != "SELECT" {
		t.Errorf("Specificity-wins should prefer SELECT mock, got %q", mock.VerifyOperation)
	}
}

func TestSemanticRegistry_JoinTwoTables(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "semantic_join",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.ReadPostgreSQL,
				AccessingTables: []string{"orders", "users"},
				VerifyOperation: "SELECT",
			},
		},
	}
	reg.Register(spec)

	// Both tables must match (exact set)
	_, ok := reg.FindMockByTables([]string{"orders", "users"}, "SELECT", nil, nil, nil)
	if !ok {
		t.Fatal("Expected JOIN mock to match")
	}

	// Single-table queries should NOT match the two-table mock
	reg2 := NewMockRegistry()
	reg2.Register(spec)
	_, ok2 := reg2.FindMockByTables([]string{"orders"}, "SELECT", nil, nil, nil)
	if ok2 {
		t.Error("Single-table query should not match two-table ACCESSING_TABLES mock")
	}
}

func TestSemanticRegistry_ExactTableSetRequired(t *testing.T) {
	reg := NewMockRegistry()
	spec := &types.TestSpec{
		Name: "semantic_exact_tables",
		Expects: []types.ExpectStatement{
			{
				Channel:         types.ReadMySQL,
				AccessingTables: []string{"users"},
			},
		},
	}
	reg.Register(spec)

	// Query touching users AND orders should NOT match the users-only mock
	_, ok := reg.FindMockByTables([]string{"users", "orders"}, "SELECT", nil, nil, nil)
	if ok {
		t.Error("Multi-table query should not match single-table ACCESSING_TABLES mock")
	}
}

// alwaysMatch is a bodyMatch callback that always returns true (no WITH file constraint).
func alwaysMatch(_, _ string) bool { return true }

// neverMatch is a bodyMatch callback that always returns false (body never matches).
func neverMatch(_, _ string) bool { return false }

func TestFindHTTPMockWithBody_NoWithFile(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Expects: []types.ExpectStatement{
			{Channel: types.HTTP, URL: "/api/users", Method: "POST"},
		},
	})

	// Mock has no WithFile — alwaysMatch and neverMatch both should find it
	mock, found := reg.FindHTTPMockWithBody("/api/users", "POST", nil, alwaysMatch)
	if !found || mock == nil {
		t.Fatal("expected to find mock when no WithFile constraint")
	}
}

func TestFindHTTPMockWithBody_BodyMatchSkipsNonMatching(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Expects: []types.ExpectStatement{
			{Channel: types.HTTP, URL: "/api/users", Method: "POST", WithFile: "body.json"},
		},
	})

	_, found := reg.FindHTTPMockWithBody("/api/users", "POST", nil, neverMatch)
	if found {
		t.Fatal("expected no match when bodyMatch returns false")
	}
}

func TestFindHTTPMockWithBody_DisambiguatesByBody(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Expects: []types.ExpectStatement{
			{Channel: types.HTTP, URL: "/api/users", Method: "POST", WithFile: "alice.json", ReturnsFile: "alice_resp.json"},
			{Channel: types.HTTP, URL: "/api/users", Method: "POST", WithFile: "bob.json", ReturnsFile: "bob_resp.json"},
		},
	})

	// Simulate: first call matches "alice.json" body, second matches "bob.json" body
	aliceMatcher := func(withFile, _ string) bool { return withFile == "alice.json" }
	bobMatcher := func(withFile, _ string) bool { return withFile == "bob.json" }

	m1, found1 := reg.FindHTTPMockWithBody("/api/users", "POST", nil, aliceMatcher)
	if !found1 || m1.ReturnsFile != "alice_resp.json" {
		t.Fatalf("expected alice mock, got %v", m1)
	}
	m2, found2 := reg.FindHTTPMockWithBody("/api/users", "POST", nil, bobMatcher)
	if !found2 || m2.ReturnsFile != "bob_resp.json" {
		t.Fatalf("expected bob mock, got %v", m2)
	}
}

func TestFindKafkaMockWithBody_NoWithFile(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Expects: []types.ExpectStatement{
			{Channel: types.Event, Topic: "orders"},
		},
	})

	mock, found := reg.FindKafkaMockWithBody("orders", alwaysMatch)
	if !found || mock == nil {
		t.Fatal("expected to find Kafka mock with no WithFile")
	}
}

func TestFindKafkaMockWithBody_BodyMatchSkipsNonMatching(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Expects: []types.ExpectStatement{
			{Channel: types.Event, Topic: "orders", WithFile: "order.json"},
		},
	})

	_, found := reg.FindKafkaMockWithBody("orders", neverMatch)
	if found {
		t.Fatal("expected no match when bodyMatch returns false")
	}
}

func TestFindGRPCMockWithBody_NoWithFile(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Expects: []types.ExpectStatement{
			{Channel: types.GRPC, Service: "UserService", RPCMethod: "GetUser"},
		},
	})

	mock, found := reg.FindGRPCMockWithBody("UserService", "GetUser", alwaysMatch)
	if !found || mock == nil {
		t.Fatal("expected to find gRPC mock with no WithFile")
	}
}

func TestFindGRPCMockWithBody_BodyMatchSkipsNonMatching(t *testing.T) {
	reg := NewMockRegistry()
	reg.Register(&types.TestSpec{
		Expects: []types.ExpectStatement{
			{Channel: types.GRPC, Service: "UserService", RPCMethod: "GetUser", WithFile: "req.json"},
		},
	})

	_, found := reg.FindGRPCMockWithBody("UserService", "GetUser", neverMatch)
	if found {
		t.Fatal("expected no match when bodyMatch returns false")
	}
}
