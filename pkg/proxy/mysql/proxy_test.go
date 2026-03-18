//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/types"
)

func TestProxy_Passthrough(t *testing.T) {
	// This test requires a running MySQL container on localhost:3307
	// Run with: make test-integration
	// Or manually: docker run -d -p 3307:3306 -e MYSQL_ROOT_PASSWORD=rootpassword \
	//   -e MYSQL_DATABASE=todo_api_development -e MYSQL_USER=todo_user \
	//   -e MYSQL_PASSWORD=todo_password mysql:8.4
	dbAddr := "localhost:3307"
	dbUser := "todo_user"
	dbPass := "todo_password"
	dbName := "todo_api_development"

	reg := registry.NewMockRegistry()
	proxyAddr := "localhost:3308"
	proxy := NewProxy(proxyAddr, dbAddr, reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := proxy.Start(ctx); err != nil {
			logger.Debug("Proxy start error: %v", err)
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
	// Integration test: requires MySQL container running on localhost:3307
	dbAddr := "localhost:3307"
	dbUser := "todo_user"
	dbPass := "todo_password"
	dbName := "todo_api_development"

	// Verify DB is available - fail fast if not
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/%s", dbUser, dbPass, dbAddr, dbName))
	if err != nil {
		t.Fatalf("Failed to open MySQL connection: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("MySQL not reachable on localhost:3307: %v", err)
	}
	db.Close()

	reg := registry.NewMockRegistry()
	// Register a mock expectation that matches our INSERT
	spec := &types.TestSpec{
		Expects: []types.ExpectStatement{
			{
				Channel: types.WriteMySQL,
				Table:   "users",
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
	// Integration test: requires MySQL container running on localhost:3307
	dbAddr := "localhost:3307"
	dbUser := "todo_user"
	dbPass := "todo_password"
	dbName := "todo_api_development"

	// Verify DB is available
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s)/%s", dbUser, dbPass, dbAddr, dbName))
	if err != nil {
		t.Fatalf("Failed to open MySQL connection: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("MySQL not reachable on localhost:3307: %v", err)
	}
	db.Close()

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
