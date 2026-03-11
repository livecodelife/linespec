package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	httpproxy "github.com/calebcowen/linespec/pkg/proxy/http"
	"github.com/calebcowen/linespec/pkg/proxy/kafka"
	"github.com/calebcowen/linespec/pkg/proxy/mysql"
	"github.com/calebcowen/linespec/pkg/proxy/postgresql"
	"github.com/calebcowen/linespec/pkg/registry"
	"github.com/calebcowen/linespec/pkg/runner"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: linespec <test|proxy> ...")
		os.Exit(1)
	}

	if os.Args[1] == "proxy" {
		runProxy()
		return
	}

	if len(os.Args) < 3 || os.Args[1] != "test" {
		fmt.Println("Usage: linespec test <path-to-linespec-or-dir>")
		os.Exit(1)
	}

	path := os.Args[2]
	ctx := context.Background()

	fileInfo, err := os.Stat(path)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	var testFiles []string
	if fileInfo.IsDir() {
		err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(p, ".linespec") {
				testFiles = append(testFiles, p)
			}
			return nil
		})
		if err != nil {
			fmt.Printf("Error walking path: %v\n", err)
			os.Exit(1)
		}
	} else {
		testFiles = append(testFiles, path)
	}

	if len(testFiles) == 0 {
		fmt.Println("No .linespec files found.")
		return
	}

	// Create test suite with shared infrastructure
	suite, err := runner.NewTestSuite()
	if err != nil {
		fmt.Printf("❌ Failed to create test suite: %v\n", err)
		os.Exit(1)
	}

	// Setup shared infrastructure once
	fmt.Println("🔧 Setting up shared infrastructure...")
	infraCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	if err := suite.SetupSharedInfrastructure(infraCtx); err != nil {
		cancel()
		fmt.Printf("❌ Failed to setup infrastructure: %v\n", err)
		os.Exit(1)
	}
	cancel()

	// Cleanup shared infrastructure when done
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		suite.CleanupSharedInfrastructure(cleanupCtx)
		cancel()
	}()

	passed := 0
	failed := 0

	for i, file := range testFiles {
		fmt.Printf("\n[%d/%d] Running Test: %s\n", i+1, len(testFiles), file)
		fmt.Println("--------------------------------------------------")

		testCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)

		func() {
			defer cancel()
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("❌ Test %s PANICKED: %v\n", file, r)
					failed++
				}
			}()

			if err := suite.RunTest(testCtx, file); err != nil {
				fmt.Printf("\n❌ Test %s FAILED: %v\n", file, err)
				failed++
			} else {
				fmt.Printf("\n✅ Test %s PASSED\n", file)
				passed++
			}
		}()
	}

	fmt.Printf("\n================Summary================\n")
	fmt.Printf("Total Tests: %d\n", len(testFiles))
	fmt.Printf("Passed:      %d\n", passed)
	fmt.Printf("Failed:      %d\n", failed)

	if failed > 0 {
		os.Exit(1)
	}
}

func runProxy() {
	if len(os.Args) < 5 {
		fmt.Println("Usage: linespec proxy <type> <listen-addr> <upstream-addr> [registry-file]")
		os.Exit(1)
	}

	// Change working directory to /app/project if it exists (inside container)
	if _, err := os.Stat("/app/project"); err == nil {
		os.Chdir("/app/project")
		fmt.Println("✅ Changed working directory to /app/project")
	}

	pType := os.Args[2]
	addr := os.Args[3]
	upstream := os.Args[4]

	reg := registry.NewMockRegistry()
	if len(os.Args) > 5 {
		regFile := os.Args[5]
		if err := reg.LoadFromFile(regFile); err != nil {
			fmt.Printf("❌ Failed to load registry: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Loaded registry from %s\n", regFile)

		// Debug: Print registry contents
		data, _ := os.ReadFile(regFile)
		fmt.Printf("📄 Registry file size: %d bytes\n", len(data))
	}

	// Start a sidecar HTTP server for verification
	srv := &http.Server{Addr: "0.0.0.0:8081"}
	http.HandleFunc("/verify", func(w http.ResponseWriter, r *http.Request) {
		hits := reg.GetHits()
		json.NewEncoder(w).Encode(hits)
	})

	go func() {
		fmt.Println("✅ Verification sidecar listening on 0.0.0.0:8081")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("❌ Verification sidecar error: %v\n", err)
		}
	}()

	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	var proxyErr error
	switch pType {
	case "mysql":
		p := mysql.NewProxy(addr, upstream, reg)
		proxyErr = p.Start(ctx)
	case "postgresql":
		p := postgresql.NewProxy(addr, upstream, reg)
		proxyErr = p.Start(ctx)
	case "http":
		p := httpproxy.NewInterceptor(addr, reg)
		proxyErr = p.Start(ctx)
	case "kafka":
		p := kafka.NewInterceptor(addr, reg)
		proxyErr = p.Start(ctx)
	default:
		fmt.Printf("Unknown proxy type: %s\n", pType)
		os.Exit(1)
	}

	if proxyErr != nil {
		fmt.Printf("Proxy error: %v\n", proxyErr)
		os.Exit(1)
	}

	// Block until context is cancelled
	<-ctx.Done()
}
