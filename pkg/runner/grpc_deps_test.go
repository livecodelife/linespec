package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/livecodelife/linespec/v3/pkg/config"
)

func TestGetGRPCProxyDependencies(t *testing.T) {
	cfg := &config.LineSpecConfig{
		Dependencies: []config.DependencyConfig{
			{Name: "temporal", Type: "grpc", Host: "temporal:7233", Port: 7233, Proxy: true},
			{Name: "users-svc", Type: "http", Host: "users-svc", Port: 80, Proxy: true},
			{Name: "kafka-svc", Type: "grpc", Host: "kafka:9092", Port: 9092, Proxy: false},
			{Name: "etcd", Type: "grpc", Host: "etcd:2379", Port: 2379, Proxy: true, GRPCDescriptorSet: "proto/etcd.pb"},
		},
	}

	deps := (&testRunner{}).getGRPCProxyDependencies(cfg)
	if len(deps) != 2 {
		t.Fatalf("Expected 2 gRPC proxy deps, got %d", len(deps))
	}
	if deps[0].Name != "temporal" {
		t.Errorf("Expected first dep name 'temporal', got %s", deps[0].Name)
	}
	if deps[1].Name != "etcd" {
		t.Errorf("Expected second dep name 'etcd', got %s", deps[1].Name)
	}
}

func TestGetGRPCProxyDependencies_None(t *testing.T) {
	cfg := &config.LineSpecConfig{
		Dependencies: []config.DependencyConfig{
			{Name: "users-svc", Type: "http", Host: "users-svc", Port: 80, Proxy: true},
		},
	}
	deps := (&testRunner{}).getGRPCProxyDependencies(cfg)
	if len(deps) != 0 {
		t.Errorf("Expected 0 gRPC proxy deps, got %d", len(deps))
	}
}

func TestMergeGRPCDescriptorSets_None(t *testing.T) {
	r := &testRunner{tempDir: t.TempDir()}
	cfg := &config.LineSpecConfig{}
	path, err := r.mergeGRPCDescriptorSets(cfg, "/project")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if path != "" {
		t.Errorf("Expected empty path for no descriptor sets, got %s", path)
	}
}

func TestMergeGRPCDescriptorSets_SingleServiceLevel(t *testing.T) {
	tmpDir := t.TempDir()
	pbData := []byte("fake descriptor data")
	pbPath := filepath.Join(tmpDir, "service.pb")
	if err := os.WriteFile(pbPath, pbData, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	r := &testRunner{tempDir: t.TempDir()}
	cfg := &config.LineSpecConfig{
		GRPCDescriptorSet: filepath.Base(pbPath),
	}
	path, err := r.mergeGRPCDescriptorSets(cfg, tmpDir)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if path != pbPath {
		t.Errorf("Expected path %s, got %s", pbPath, path)
	}
}

func TestMergeGRPCDescriptorSets_Multiple(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "base.pb"), []byte("base"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "dep.pb"), []byte("dep"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	r := &testRunner{tempDir: t.TempDir()}
	cfg := &config.LineSpecConfig{
		GRPCDescriptorSet: "base.pb",
		Dependencies: []config.DependencyConfig{
			{Name: "svc", Type: "grpc", Proxy: true, GRPCDescriptorSet: "dep.pb"},
		},
	}
	path, err := r.mergeGRPCDescriptorSets(cfg, projectDir)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if path == "" {
		t.Fatal("Expected merged path, got empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read merged file: %v", err)
	}
	if string(data) != "basedep" {
		t.Errorf("Expected merged content 'basedep', got %s", string(data))
	}
}
