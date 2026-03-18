package runner

import (
	"fmt"
	"net"
	"sync"
)

// PortAllocator manages dynamic port allocation for test containers
// to avoid port conflicts when running multiple tests in parallel
type PortAllocator struct {
	mu        sync.Mutex
	allocated map[int]bool
	minPort   int
	maxPort   int
}

// NewPortAllocator creates a new port allocator with the given port range
// If minPort and maxPort are 0, uses default range 10000-65535
func NewPortAllocator(minPort, maxPort int) *PortAllocator {
	if minPort == 0 {
		minPort = 10000
	}
	if maxPort == 0 {
		maxPort = 65535
	}
	return &PortAllocator{
		allocated: make(map[int]bool),
		minPort:   minPort,
		maxPort:   maxPort,
	}
}

// Allocate finds an available port and marks it as allocated
// Returns the port number or an error if no ports are available
func (pa *PortAllocator) Allocate() (int, error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	for port := pa.minPort; port <= pa.maxPort; port++ {
		if !pa.allocated[port] {
			// Check if the port is actually available on the system
			if isPortAvailable(port) {
				pa.allocated[port] = true
				return port, nil
			}
		}
	}

	return 0, fmt.Errorf("no available ports in range %d-%d", pa.minPort, pa.maxPort)
}

// AllocateMany allocates multiple ports atomically
// Returns the allocated ports or an error if not enough ports are available
func (pa *PortAllocator) AllocateMany(count int) ([]int, error) {
	pa.mu.Lock()
	defer pa.mu.Unlock()

	var ports []int
	for port := pa.minPort; port <= pa.maxPort && len(ports) < count; port++ {
		if !pa.allocated[port] && isPortAvailable(port) {
			pa.allocated[port] = true
			ports = append(ports, port)
		}
	}

	if len(ports) < count {
		// Release allocated ports on failure
		for _, p := range ports {
			delete(pa.allocated, p)
		}
		return nil, fmt.Errorf("could not allocate %d ports, only found %d available", count, len(ports))
	}

	return ports, nil
}

// Release marks a port as no longer allocated
func (pa *PortAllocator) Release(port int) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	delete(pa.allocated, port)
}

// isPortAvailable checks if a port is available on the system
func isPortAvailable(port int) bool {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	listener.Close()
	return true
}

// DefaultPortAllocator is a package-level default allocator
// Used when no custom allocator is provided
var DefaultPortAllocator = NewPortAllocator(10000, 65535)
