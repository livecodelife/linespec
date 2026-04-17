package grpc

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/interpolate"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/verify"
)

// Interceptor is a mock gRPC server that serves responses from the registry.
// It speaks HTTP/2 cleartext (h2c) and encodes payloads as application/grpc+json.
// Only unary RPCs are supported.
type Interceptor struct {
	addr     string
	registry *registry.MockRegistry
	resolver *interpolate.Resolver
}

// NewInterceptor creates a new gRPC interceptor listening on addr.
func NewInterceptor(addr string, reg *registry.MockRegistry) *Interceptor {
	return &Interceptor{
		addr:     addr,
		registry: reg,
	}
}

// SetResolver stores a resolver so that ${VAR} tokens in RETURNS payload files
// are resolved at runtime.
func (i *Interceptor) SetResolver(resolver *interpolate.Resolver) {
	i.resolver = resolver
}

// Start begins listening for gRPC connections. It blocks until ctx is cancelled.
func (i *Interceptor) Start(ctx context.Context) error {
	logger.Debug("gRPC Interceptor: Starting on %s", i.addr)

	h2s := &http2.Server{}
	handler := h2c.NewHandler(http.HandlerFunc(i.handleRequest), h2s)

	srv := &http.Server{
		Addr:    i.addr,
		Handler: handler,
	}

	// Shut down when context is cancelled.
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// handleRequest handles a single gRPC unary RPC.
func (i *Interceptor) handleRequest(w http.ResponseWriter, r *http.Request) {
	// gRPC path format: /package.Service/Method
	path := r.URL.Path
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
	if len(parts) != 2 {
		writeGRPCError(w, 12, fmt.Sprintf("invalid gRPC path: %s", path))
		return
	}
	service := parts[0]
	method := parts[1]

	logger.Debug("gRPC Interceptor: %s/%s", service, method)

	// Read and decode the request body (5-byte gRPC frame prefix + message bytes).
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeGRPCError(w, 2, "failed to read request body")
		return
	}
	var requestBody string
	if len(bodyBytes) >= 5 {
		// First byte is compressed flag, next 4 bytes are message length.
		msgLen := binary.BigEndian.Uint32(bodyBytes[1:5])
		if int(msgLen) <= len(bodyBytes)-5 {
			requestBody = string(bodyBytes[5 : 5+msgLen])
		}
	}

	// Check negative expectations.
	i.registry.CheckNegativeGRPCMocks(service, method)

	// Find a matching mock.
	mock, found := i.registry.FindGRPCMock(service, method)
	if !found {
		logger.Debug("gRPC Interceptor: No mock found for %s/%s", service, method)
		i.registry.RecordPassthrough("gRPC " + service + "/" + method)
		writeGRPCError(w, 12, fmt.Sprintf("no mock registered for %s/%s", service, method))
		return
	}

	// Run VERIFY rules.
	if len(mock.Verify) > 0 {
		metadata := make(map[string]string)
		for key, vals := range r.Header {
			if len(vals) > 0 {
				metadata[key] = vals[0]
			}
		}
		req := &verify.GRPCRequest{
			Service:  service,
			Method:   method,
			Body:     requestBody,
			Metadata: metadata,
		}
		rules := verify.ExtractVerifyRulesForTarget(mock.Verify, "grpc")
		if err := verify.VerifyGRPC(req, rules); err != nil {
			i.registry.RecordVerifyError("GRPC [" + service + "/" + method + "]: " + err.Error())
			writeGRPCError(w, 2, err.Error())
			return
		}
	}

	// Load the response payload.
	var responseBody []byte
	if mock.ReturnsFile != "" {
		loader := dsl.NewPayloadLoaderWithResolver(mock.BaseDir, i.resolver)
		raw, _, err := loader.LoadRaw(mock.ReturnsFile)
		if err != nil {
			writeGRPCError(w, 2, fmt.Sprintf("failed to load response payload: %v", err))
			return
		}
		responseBody = raw
	}

	// Write the gRPC response.
	w.Header().Set("Content-Type", "application/grpc+json")
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	w.WriteHeader(http.StatusOK)

	if len(responseBody) > 0 {
		frame := encodeGRPCFrame(responseBody)
		_, _ = w.Write(frame)
	}

	w.Header().Set("Grpc-Status", "0")
	w.Header().Set("Grpc-Message", "OK")
}

// encodeGRPCFrame prepends the 5-byte gRPC length-prefix frame header to msg.
func encodeGRPCFrame(msg []byte) []byte {
	frame := make([]byte, 5+len(msg))
	frame[0] = 0 // not compressed
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(msg)))
	copy(frame[5:], msg)
	return frame
}

// writeGRPCError writes a gRPC error response with the given status code and message.
// gRPC status codes: 0=OK, 2=UNKNOWN, 12=UNIMPLEMENTED.
func writeGRPCError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/grpc+json")
	w.Header().Set("Grpc-Status", fmt.Sprintf("%d", statusCode))
	w.Header().Set("Grpc-Message", message)
	w.WriteHeader(http.StatusOK)
}
