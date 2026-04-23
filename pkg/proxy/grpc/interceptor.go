package grpc

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
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

type Interceptor struct {
	addr       string
	upstream   string
	registry   *registry.MockRegistry
	resolver   *interpolate.Resolver
	descriptor *DescriptorResolver
	client     *http.Client
}

func NewInterceptor(addr string, upstreamAddr string, reg *registry.MockRegistry) *Interceptor {
	i := &Interceptor{
		addr:     addr,
		upstream: upstreamAddr,
		registry: reg,
	}
	if upstreamAddr != "" {
		transport := &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				dialer := &net.Dialer{}
				return dialer.DialContext(ctx, network, addr)
			},
		}
		i.client = &http.Client{Transport: transport}
	}
	return i
}

func (i *Interceptor) SetResolver(resolver *interpolate.Resolver) {
	i.resolver = resolver
}

func (i *Interceptor) SetDescriptor(d *DescriptorResolver) {
	i.descriptor = d
}

func (i *Interceptor) Start(ctx context.Context) error {
	logger.Debug("gRPC Interceptor: Starting on %s (upstream: %s)", i.addr, i.upstream)

	h2s := &http2.Server{}
	handler := h2c.NewHandler(http.HandlerFunc(i.handleRequest), h2s)

	srv := &http.Server{
		Addr:    i.addr,
		Handler: handler,
	}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (i *Interceptor) handleRequest(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
	if len(parts) != 2 {
		writeGRPCError(w, 12, fmt.Sprintf("invalid gRPC path: %s", path), "")
		return
	}
	service := parts[0]
	method := parts[1]

	logger.Debug("gRPC Interceptor: %s/%s", service, method)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeGRPCError(w, 2, "failed to read request body", "")
		return
	}
	var requestBody string
	if len(bodyBytes) >= 5 {
		msgLen := binary.BigEndian.Uint32(bodyBytes[1:5])
		if msgLen <= uint32(len(bodyBytes)-5) {
			requestBody = string(bodyBytes[5 : 5+msgLen])
		}
	}

	i.registry.CheckNegativeGRPCMocks(service, method)

	mock, found := i.registry.FindGRPCMock(service, method)
	if !found {
		if i.upstream != "" {
			i.forwardToUpstream(w, r, bodyBytes, service, method)
			return
		}
		logger.Debug("gRPC Interceptor: No mock found for %s/%s", service, method)
		i.registry.RecordPassthrough("gRPC " + service + "/" + method)
		writeGRPCError(w, 12, fmt.Sprintf("no mock registered for %s/%s", service, method), "")
		return
	}

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
			writeGRPCError(w, 2, err.Error(), r.Header.Get("Content-Type"))
			return
		}
	}

	var responseBody []byte
	if mock.ReturnsFile != "" {
		loader := dsl.NewPayloadLoaderWithResolver(mock.BaseDir, i.resolver)
		raw, _, err := loader.LoadRaw(mock.ReturnsFile)
		if err != nil {
			writeGRPCError(w, 2, fmt.Sprintf("failed to load response payload: %v", err), r.Header.Get("Content-Type"))
			return
		}
		responseBody = raw
	}

	requestCT := r.Header.Get("Content-Type")
	responseCT := requestCT
	if responseCT == "" {
		responseCT = "application/grpc+json"
	}

	if requestCT == "application/grpc" && i.descriptor != nil && i.descriptor.HasDescriptor(service, method) {
		protoBytes, err := i.descriptor.JSONToProtobuf(service, method, responseBody)
		if err != nil {
			logger.Debug("gRPC Interceptor: JSON-to-protobuf conversion failed for %s/%s: %v", service, method, err)
		} else {
			responseBody = protoBytes
		}
	}

	w.Header().Set("Content-Type", responseCT)
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	w.WriteHeader(http.StatusOK)

	if len(responseBody) > 0 {
		frame := encodeGRPCFrame(responseBody)
		_, _ = w.Write(frame)
	}

	w.Header().Set("Grpc-Status", "0")
	w.Header().Set("Grpc-Message", "OK")
}

func (i *Interceptor) forwardToUpstream(w http.ResponseWriter, r *http.Request, bodyBytes []byte, service, method string) {
	upstreamURL := "http://" + i.upstream + r.URL.Path
	logger.Debug("gRPC Interceptor: Forwarding %s/%s to upstream %s", service, method, upstreamURL)

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, io.NopCloser(strings.NewReader(string(bodyBytes))))
	if err != nil {
		writeGRPCError(w, 13, fmt.Sprintf("failed to create upstream request: %v", err), r.Header.Get("Content-Type"))
		return
	}

	for key, vals := range r.Header {
		for _, val := range vals {
			upstreamReq.Header.Add(key, val)
		}
	}
	upstreamReq.Header.Set("Content-Type", r.Header.Get("Content-Type"))

	resp, err := i.client.Do(upstreamReq)
	if err != nil {
		logger.Debug("gRPC Interceptor: Upstream request failed for %s/%s: %v", service, method, err)
		writeGRPCError(w, 14, fmt.Sprintf("upstream unreachable: %v", err), r.Header.Get("Content-Type"))
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		writeGRPCError(w, 2, fmt.Sprintf("failed to read upstream response: %v", err), r.Header.Get("Content-Type"))
		return
	}

	for key, vals := range resp.Header {
		if strings.HasPrefix(key, "Grpc-") || strings.EqualFold(key, "Content-Type") || strings.EqualFold(key, "Trailer") {
			for _, val := range vals {
				w.Header().Add(key, val)
			}
		}
	}

	w.WriteHeader(resp.StatusCode)
	if len(respBody) > 0 {
		_, _ = w.Write(respBody)
	}

	i.registry.RecordPassthrough("gRPC " + service + "/" + method)
	logger.Debug("gRPC Interceptor: Forwarded %s/%s to upstream (status %d)", service, method, resp.StatusCode)
}

func encodeGRPCFrame(msg []byte) []byte {
	msgLen := len(msg)
	if msgLen > (1<<32)-1-5 {
		msgLen = (1 << 32) - 1 - 5
	}
	frame := make([]byte, 5+msgLen)
	frame[0] = 0
	binary.BigEndian.PutUint32(frame[1:5], uint32(msgLen))
	copy(frame[5:], msg)
	return frame
}

func writeGRPCError(w http.ResponseWriter, statusCode int, message string, requestCT string) {
	ct := requestCT
	if ct == "" {
		ct = "application/grpc+json"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Grpc-Status", fmt.Sprintf("%d", statusCode))
	w.Header().Set("Grpc-Message", message)
	w.WriteHeader(http.StatusOK)
}
