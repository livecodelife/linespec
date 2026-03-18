package runner

import (
	"fmt"
	"net"
	"testing"
)

func TestPortAllocator_Allocate(t *testing.T) {
	// Use a small port range for testing to make it faster
	pa := NewPortAllocator(30000, 30010)

	// Allocate a port
	port, err := pa.Allocate()
	if err != nil {
		t.Fatalf("Failed to allocate port: %v", err)
	}

	// Check that the port is within range
	if port < 30000 || port > 30010 {
		t.Errorf("Allocated port %d is outside range 30000-30010", port)
	}

	// Allocate another port
	port2, err := pa.Allocate()
	if err != nil {
		t.Fatalf("Failed to allocate second port: %v", err)
	}

	// Check that ports are different
	if port == port2 {
		t.Error("Allocated ports should be different")
	}

	// Release the first port and allocate again
	pa.Release(port)

	// The allocator might give us the same port back or a different one
	// Just verify we can still allocate
	_, err = pa.Allocate()
	if err != nil {
		t.Fatalf("Failed to allocate after release: %v", err)
	}
}

func TestPortAllocator_AllocateMany(t *testing.T) {
	// Use a small port range
	pa := NewPortAllocator(30000, 30010)

	// Allocate multiple ports
	ports, err := pa.AllocateMany(3)
	if err != nil {
		t.Fatalf("Failed to allocate multiple ports: %v", err)
	}

	// Check that we got the right number
	if len(ports) != 3 {
		t.Errorf("Expected 3 ports, got %d", len(ports))
	}

	// Check that all ports are within range
	for _, port := range ports {
		if port < 30000 || port > 30010 {
			t.Errorf("Port %d is outside range", port)
		}
	}

	// Check that all ports are unique
	seen := make(map[int]bool)
	for _, port := range ports {
		if seen[port] {
			t.Errorf("Duplicate port %d allocated", port)
		}
		seen[port] = true
	}
}

func TestPortAllocator_AllocateMany_Insufficient(t *testing.T) {
	// Use a very small port range
	pa := NewPortAllocator(30000, 30002)

	// Try to allocate more ports than available
	_, err := pa.AllocateMany(10)
	if err == nil {
		t.Error("Expected error when allocating more ports than available")
	}
}

func TestPortAllocator_Defaults(t *testing.T) {
	// Test default allocator
	pa := NewPortAllocator(0, 0)

	// Check default values
	if pa.minPort != 10000 {
		t.Errorf("Expected default minPort 10000, got %d", pa.minPort)
	}
	if pa.maxPort != 65535 {
		t.Errorf("Expected default maxPort 65535, got %d", pa.maxPort)
	}

	// Should be able to allocate with defaults
	port, err := pa.Allocate()
	if err != nil {
		t.Fatalf("Failed to allocate with default range: %v", err)
	}

	if port < 10000 || port > 65535 {
		t.Errorf("Port %d outside default range", port)
	}
}

func TestIsPortAvailable(t *testing.T) {
	// Test with a very high port number (unlikely to be in use)
	// Using a random high port to reduce collision chance
	testPort := 54321

	// First check should pass (port available)
	if !isPortAvailable(testPort) {
		t.Skipf("Port %d is not available, skipping test", testPort)
	}

	// Actually bind to the port to make it unavailable
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", testPort))
	if err != nil {
		t.Fatalf("Failed to bind to port %d: %v", testPort, err)
	}
	defer listener.Close()

	// Now the port should not be available
	if isPortAvailable(testPort) {
		t.Error("Port should not be available after binding")
	}
}
