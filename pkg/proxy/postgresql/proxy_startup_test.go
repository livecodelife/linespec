package postgresql

import (
	"fmt"
	"net"
	"testing"
	"time"
)

// dialPipe creates a connected pair of net.Conn via a local TCP listener.
func dialPipe(t *testing.T) (client, server net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("dialPipe: listen: %v", err)
	}
	defer ln.Close()

	done := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- nil
			return
		}
		done <- c
	}()

	client, err = net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dialPipe: dial: %v", err)
	}
	server = <-done
	if server == nil {
		t.Fatal("dialPipe: accept returned nil")
	}
	return client, server
}

// readyForQueryMsg returns a minimal valid ReadyForQuery message ('Z' + length 5 + 'I').
func readyForQueryMsg() []byte {
	return []byte{'Z', 0x00, 0x00, 0x00, 0x05, 'I'}
}

// newTestProxy creates a Proxy wired to a fake upstream address (not actually dialled here).
func newTestProxy(t *testing.T) *Proxy {
	t.Helper()
	return &Proxy{
		addr:         "127.0.0.1:0",
		upstreamAddr: "127.0.0.1:0",
		loader:       nil,
		startup:      NewStartupHandler(),
		result:       NewResultHandler(),
		debugLog:     nil,
	}
}

// TestStartupSuccess verifies that proxyWithStatefulRelay returns nil when the
// upstream sends a valid ReadyForQuery during the startup phase.
func TestStartupSuccess(t *testing.T) {
	// client <-> proxyClient  [proxy]  proxyUpstream <-> upstream
	proxyClient, clientSide := dialPipe(t)
	defer proxyClient.Close()
	defer clientSide.Close()

	proxyUpstream, upstreamSide := dialPipe(t)
	defer proxyUpstream.Close()
	defer upstreamSide.Close()

	p := newTestProxy(t)

	result := make(chan error, 1)
	go func() {
		result <- p.proxyWithStatefulRelay(proxyClient, proxyUpstream)
	}()

	// Upstream sends ReadyForQuery; proxy should proceed to Phase 2.
	if _, err := upstreamSide.Write(readyForQueryMsg()); err != nil {
		t.Fatalf("upstream write: %v", err)
	}

	// Give Phase 2 a moment to start, then close the client to end the test cleanly.
	time.Sleep(50 * time.Millisecond)
	clientSide.Close()
	proxyClient.Close()
	upstreamSide.Close()

	select {
	case err := <-result:
		if err != nil {
			t.Errorf("expected nil error on successful startup, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("proxyWithStatefulRelay did not return within timeout")
	}
}

// TestStartupUpstreamClose verifies that proxyWithStatefulRelay returns an error
// when the upstream closes the connection before sending ReadyForQuery.
func TestStartupUpstreamClose(t *testing.T) {
	proxyClient, clientSide := dialPipe(t)
	defer proxyClient.Close()
	defer clientSide.Close()

	proxyUpstream, upstreamSide := dialPipe(t)
	defer proxyUpstream.Close()

	p := newTestProxy(t)

	result := make(chan error, 1)
	go func() {
		result <- p.proxyWithStatefulRelay(proxyClient, proxyUpstream)
	}()

	// Upstream closes immediately without sending ReadyForQuery.
	upstreamSide.Close()

	select {
	case err := <-result:
		if err == nil {
			t.Error("expected error when upstream closes before ReadyForQuery, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("proxyWithStatefulRelay did not return within timeout")
	}
}

// TestStartupTimeout verifies that proxyWithStatefulRelay returns a descriptive
// error when ReadyForQuery is never received within the startup timeout.
//
// We temporarily replace the const with a short duration by running the relay
// with a mock upstream that stays silent. To keep the test fast we close the
// upstream after a brief pause that is well under the real 10-second timeout but
// long enough to distinguish from an instant close (which hits TestStartupUpstreamClose).
// The real timeout path is exercised by confirming that a silent-but-open upstream
// eventually triggers an error.
func TestStartupTimeoutPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}

	proxyClient, clientSide := dialPipe(t)
	defer proxyClient.Close()
	defer clientSide.Close()

	proxyUpstream, upstreamSide := dialPipe(t)
	defer proxyUpstream.Close()
	defer upstreamSide.Close()

	p := newTestProxy(t)

	result := make(chan error, 1)
	go func() {
		result <- p.proxyWithStatefulRelay(proxyClient, proxyUpstream)
	}()

	// Upstream stays silent. After 12 seconds the 10-second timer fires.
	select {
	case err := <-result:
		if err == nil {
			t.Error("expected timeout error, got nil")
		}
		t.Logf("received expected error: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("proxyWithStatefulRelay did not return within 15s after startup timeout")
	}
}

// TestStartupNoDataRace is a placeholder that exercises the happy path under the
// race detector (run with: go test -race ./pkg/proxy/postgresql/...).
func TestStartupNoDataRace(t *testing.T) {
	for i := 0; i < 5; i++ {
		t.Run(fmt.Sprintf("iter%d", i), func(t *testing.T) {
			proxyClient, clientSide := dialPipe(t)
			defer proxyClient.Close()
			defer clientSide.Close()

			proxyUpstream, upstreamSide := dialPipe(t)
			defer proxyUpstream.Close()
			defer upstreamSide.Close()

			p := newTestProxy(t)

			result := make(chan error, 1)
			go func() {
				result <- p.proxyWithStatefulRelay(proxyClient, proxyUpstream)
			}()

			if _, err := upstreamSide.Write(readyForQueryMsg()); err != nil {
				t.Fatalf("upstream write: %v", err)
			}
			time.Sleep(30 * time.Millisecond)
			clientSide.Close()
			proxyClient.Close()
			upstreamSide.Close()

			select {
			case <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for relay to exit")
			}
		})
	}
}
