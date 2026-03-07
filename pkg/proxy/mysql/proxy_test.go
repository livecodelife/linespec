package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/calebcowen/linespec/pkg/registry"
	"github.com/calebcowen/linespec/pkg/types"
	_ "github.com/go-sql-driver/mysql"
)

func TestProxy_Passthrough(t *testing.T) {
	// This test requires a running MySQL container on localhost:3307 (as per user-service compose)
	// Or we can skip if not available.
	dbAddr := "localhost:3307"
	dbUser := "todo_user"
	dbPass := "todo_password"
	dbName := "todo_api_development"

	// Check if DB is available
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/%s", dbUser, dbPass, dbAddr, dbName))
	if err != nil {
		t.Skip("MySQL not available on localhost:3307")
		return
	}
	if err := db.Ping(); err != nil {
		t.Skip("MySQL not reachable on localhost:3307")
		return
	}
	db.Close()

	reg := registry.NewMockRegistry()
	proxyAddr := "localhost:3308"
	proxy := NewProxy(proxyAddr, dbAddr, reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := proxy.Start(ctx); err != nil {
			fmt.Printf("Proxy start error: %v\n", err)
		}
	}()

	// Wait for proxy to start
	time.Sleep(1 * time.Second)

	// Connect to proxy
	proxyDB, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/%s", dbUser, dbPass, proxyAddr, dbName))
	if err != nil {
		t.Fatalf("Failed to connect to proxy: %v", err)
	}
	defer proxyDB.Close()

	// Run whitelisted query
	var val int
	err = proxyDB.QueryRow("SELECT 1").Scan(&val)
	if err != nil {
		t.Fatalf("Passthrough SELECT 1 failed: %v", err)
	}
	if val != 1 {
		t.Errorf("Expected 1, got %d", val)
	}

	// Run migration-like query
	rows, err := proxyDB.Query("SHOW TABLES")
	if err != nil {
		t.Fatalf("Passthrough SHOW TABLES failed: %v", err)
	}
	rows.Close()
}

func TestProxy_Mocking(t *testing.T) {
	dbAddr := "localhost:3307"
	dbUser := "todo_user"
	dbPass := "todo_password"
	dbName := "todo_api_development"

	reg := registry.NewMockRegistry()
	// Register a mock for users table
	spec := &types.TestSpec{
		Expects: []types.ExpectStatement{
			{
				Channel: types.WriteMySQL,
				Table:   "users",
				SQL:     "INSERT INTO users (name) VALUES ('mocked')",
			},
		},
	}
	reg.Register(spec)

	proxyAddr := "localhost:3309"
	proxy := NewProxy(proxyAddr, dbAddr, reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = proxy.Start(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	proxyDB, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/%s", dbUser, dbPass, proxyAddr, dbName))
	if err != nil {
		t.Fatalf("Failed to connect to proxy: %v", err)
	}
	defer proxyDB.Close()

	// Run mocked query
	_, err = proxyDB.Exec("INSERT INTO users (name) VALUES ('mocked')")
	if err != nil {
		t.Fatalf("Mocked INSERT failed: %v", err)
	}

	// Verify it wasn't actually inserted in real DB
	realDB, _ := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/%s", dbUser, dbPass, dbAddr, dbName))
	defer realDB.Close()
	var count int
	_ = realDB.QueryRow("SELECT COUNT(*) FROM users WHERE name = 'mocked'").Scan(&count)
	if count > 0 {
		t.Errorf("Expected 0 records in real DB, found %d. Mocking failed!", count)
	}
}

func TestProxy_MockingSelect(t *testing.T) {
	dbAddr := "localhost:3307"
	dbUser := "todo_user"
	dbPass := "todo_password"
	dbName := "todo_api_development"

	reg := registry.NewMockRegistry()
	spec := &types.TestSpec{
		Expects: []types.ExpectStatement{
			{
				Channel:      types.ReadMySQL,
				Table:        "users",
				ReturnsEmpty: true,
			},
		},
	}
	reg.Register(spec)

	proxyAddr := "localhost:3310"
	proxy := NewProxy(proxyAddr, dbAddr, reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = proxy.Start(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	proxyDB, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/%s", dbUser, dbPass, proxyAddr, dbName))
	if err != nil {
		t.Fatalf("Failed to connect to proxy: %v", err)
	}
	defer proxyDB.Close()

	// Run mocked SELECT
	rows, err := proxyDB.Query("SELECT * FROM users LIMIT 1")
	if err != nil {
		t.Fatalf("Mocked SELECT failed: %v", err)
	}
	defer rows.Close()

	if rows.Next() {
		t.Errorf("Expected 0 rows for mocked SELECT")
	}
}
