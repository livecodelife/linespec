package registry

import (
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
