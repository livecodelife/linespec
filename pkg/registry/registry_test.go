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
