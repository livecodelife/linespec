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
