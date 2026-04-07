package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/livecodelife/linespec/pkg/types"
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

	// Test exact match
	_, found := reg.FindMock("users", "SELECT * FROM users WHERE id = 42 LIMIT 1")
	if !found {
		t.Errorf("Expected exact SQL match to work")
	}

	// Reset hits to test backtick normalization
	reg.ResetHits()

	// Test backtick-normalized match
	_, found = reg.FindMock("users", "SELECT * FROM `users` WHERE id = 42 LIMIT 1")
	if !found {
		t.Errorf("Expected backtick-normalized SQL match to work")
	}

	// Reset hits to test table prefix normalization
	reg.ResetHits()

	// Test table prefix match (like `users`.`id` -> users.id)
	_, found = reg.FindMock("users", "SELECT * FROM `users` WHERE `users`.`id` = 42 LIMIT 1")
	if !found {
		t.Errorf("Expected table prefix normalized SQL match to work")
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
