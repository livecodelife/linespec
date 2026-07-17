package postgresql

import (
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/registry"
	"github.com/livecodelife/linespec/v3/pkg/types"
)

func TestCreateCommandCompleteTag(t *testing.T) {
	// Create a proxy instance to test the method
	proxy := NewProxy("localhost:5432", "localhost:5433", registry.NewMockRegistry())

	tests := []struct {
		name     string
		sql      string
		rowCount int
		want     string
	}{
		// INSERT operations
		{
			name:     "INSERT with 1 row",
			sql:      "INSERT INTO orders (id, customer_name) VALUES ($1, $2)",
			rowCount: 1,
			want:     "INSERT 0 1",
		},
		{
			name:     "INSERT with multiple rows",
			sql:      "INSERT INTO users (name, email) VALUES ($1, $2), ($3, $4)",
			rowCount: 2,
			want:     "INSERT 0 2",
		},
		{
			name:     "INSERT with whitespace",
			sql:      "  INSERT INTO products (name) VALUES ($1)  ",
			rowCount: 1,
			want:     "INSERT 0 1",
		},
		{
			name:     "INSERT lowercase",
			sql:      "insert into items (id) values ($1)",
			rowCount: 1,
			want:     "INSERT 0 1",
		},
		{
			name:     "INSERT with RETURNING clause",
			sql:      "INSERT INTO orders (id) VALUES ($1) RETURNING id",
			rowCount: 1,
			want:     "INSERT 0 1",
		},

		// UPDATE operations
		{
			name:     "UPDATE single row",
			sql:      "UPDATE orders SET status = 'completed' WHERE id = $1",
			rowCount: 1,
			want:     "UPDATE 1",
		},
		{
			name:     "UPDATE multiple rows",
			sql:      "UPDATE users SET active = true WHERE created_at > $1",
			rowCount: 5,
			want:     "UPDATE 5",
		},
		{
			name:     "UPDATE lowercase",
			sql:      "update products set name = $1 where id = $2",
			rowCount: 1,
			want:     "UPDATE 1",
		},
		{
			name:     "UPDATE with whitespace",
			sql:      "  UPDATE items SET count = count + 1  ",
			rowCount: 3,
			want:     "UPDATE 3",
		},

		// DELETE operations
		{
			name:     "DELETE single row",
			sql:      "DELETE FROM orders WHERE id = $1",
			rowCount: 1,
			want:     "DELETE 1",
		},
		{
			name:     "DELETE multiple rows",
			sql:      "DELETE FROM users WHERE inactive = true",
			rowCount: 10,
			want:     "DELETE 10",
		},
		{
			name:     "DELETE lowercase",
			sql:      "delete from products where id = $1",
			rowCount: 1,
			want:     "DELETE 1",
		},
		{
			name:     "DELETE with whitespace",
			sql:      "  DELETE FROM items WHERE expired = true  ",
			rowCount: 2,
			want:     "DELETE 2",
		},

		// SELECT operations
		{
			name:     "SELECT with rows",
			sql:      "SELECT * FROM orders WHERE customer_id = $1",
			rowCount: 5,
			want:     "SELECT 5",
		},
		{
			name:     "SELECT empty result",
			sql:      "SELECT * FROM users WHERE id = 99999",
			rowCount: 0,
			want:     "SELECT 0",
		},
		{
			name:     "SELECT lowercase",
			sql:      "select * from products",
			rowCount: 3,
			want:     "SELECT 3",
		},

		// Edge cases
		{
			name:     "Empty SQL defaults to SELECT",
			sql:      "",
			rowCount: 1,
			want:     "SELECT 1",
		},
		{
			name:     "Unknown operation defaults to SELECT",
			sql:      "CREATE TABLE users (id INT)",
			rowCount: 0,
			want:     "SELECT 0",
		},
		{
			name:     "BEGIN transaction defaults to SELECT",
			sql:      "BEGIN",
			rowCount: 0,
			want:     "SELECT 0",
		},
		{
			name:     "COMMIT defaults to SELECT",
			sql:      "COMMIT",
			rowCount: 0,
			want:     "SELECT 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := proxy.createCommandCompleteTag(tt.sql, tt.rowCount)
			if got != tt.want {
				t.Errorf("createCommandCompleteTag(%q, %d) = %q, want %q",
					tt.sql, tt.rowCount, got, tt.want)
			}
		})
	}
}

func TestCreateCommandCompleteTagWithRealQueries(t *testing.T) {
	// Test with the actual query that the order-service uses
	proxy := NewProxy("localhost:5432", "localhost:5433", registry.NewMockRegistry())

	orderServiceQuery := "INSERT INTO orders (id, customer_name, total_amount, status, created_at) VALUES ($1, $2, $3, $4, $5)"

	tag := proxy.createCommandCompleteTag(orderServiceQuery, 1)
	expected := "INSERT 0 1"

	if tag != expected {
		t.Errorf("Order service query tag = %q, want %q", tag, expected)
	}
}

func TestCommandCompleteTagIntegrationWithMock(t *testing.T) {
	// Test that createCommandCompleteTag works with actual mock expectations
	proxy := NewProxy("localhost:5432", "localhost:5433", registry.NewMockRegistry())

	// Simulate different types of write operations
	testCases := []struct {
		channel  types.ExpectChannel
		sql      string
		expected string
	}{
		{types.WritePostgreSQL, "INSERT INTO orders (id) VALUES ($1)", "INSERT 0 1"},
		{types.WritePostgreSQL, "UPDATE orders SET status = $1 WHERE id = $2", "UPDATE 1"},
		{types.WritePostgreSQL, "DELETE FROM orders WHERE id = $1", "DELETE 1"},
		{types.ReadPostgreSQL, "SELECT * FROM orders", "SELECT 1"}, // Default row count for reads
	}

	for _, tc := range testCases {
		tag := proxy.createCommandCompleteTag(tc.sql, 1)
		if tag != tc.expected {
			t.Errorf("Channel %v with SQL %q: got %q, want %q",
				tc.channel, tc.sql, tag, tc.expected)
		}
	}
}

func TestExtractTable(t *testing.T) {
	proxy := NewProxy("localhost:5432", "localhost:5433", registry.NewMockRegistry())

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "INSERT into orders",
			query: "INSERT INTO orders (id, customer_name) VALUES ($1, $2)",
			want:  "orders",
		},
		{
			name:  "SELECT from orders",
			query: "SELECT * FROM orders WHERE id = $1",
			want:  "orders",
		},
		{
			name:  "UPDATE orders",
			query: "UPDATE orders SET status = 'completed' WHERE id = $1",
			want:  "orders",
		},
		{
			name:  "DELETE from orders",
			query: "DELETE FROM orders WHERE id = $1",
			want:  "orders",
		},
		{
			name:  "INSERT into users (known table)",
			query: "INSERT INTO users (name) VALUES ($1)",
			want:  "users",
		},
		{
			name:  "SELECT from todos (known table)",
			query: "SELECT * FROM todos WHERE user_id = $1",
			want:  "todos",
		},
		{
			name:  "Complex INSERT with multiple columns",
			query: "INSERT INTO orders (id, customer_name, total_amount, status, created_at) VALUES ($1, $2, $3, $4, $5)",
			want:  "orders",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := proxy.extractTable(tt.query)
			if got != tt.want {
				t.Errorf("extractTable(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}
