package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/interpolate"
	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/registry"
	"github.com/livecodelife/linespec/pkg/types"
	"github.com/livecodelife/linespec/pkg/verify"
)

type Interceptor struct {
	addr     string
	registry *registry.MockRegistry
	loader   *dsl.PayloadLoader
	resolver *interpolate.Resolver
}

func NewInterceptor(addr string, reg *registry.MockRegistry) *Interceptor {
	return &Interceptor{
		addr:     addr,
		registry: reg,
		loader:   dsl.NewPayloadLoader(""), // BaseDir will be set per-request from mock.BaseDir
	}
}

// SetResolver wires an interpolate.Resolver into the payload loader so that
// ${VAR} tokens in RETURNS payload files are resolved at runtime.
func (i *Interceptor) SetResolver(resolver *interpolate.Resolver) {
	i.resolver = resolver
	i.loader = dsl.NewPayloadLoaderWithResolver("", resolver)
}

func (i *Interceptor) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", i.handleRequest)

	server := &http.Server{
		Addr:    i.addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		server.Shutdown(context.Background())
	}()

	logger.Debug("HTTP Interceptor listening on %s", i.addr)
	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (i *Interceptor) handleRequest(w http.ResponseWriter, r *http.Request) {
	// 1. Find mock in registry
	path := r.URL.Path
	method := r.Method
	logger.Debug("Intercepted %s %s (Host: %s)", method, path, r.Host)

	// Extract headers from request
	requestHeaders := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			requestHeaders[k] = v[0]
		}
	}
	logger.Debug("Request headers: %v", requestHeaders)

	// Read request body (we need it for verification)
	var body string
	if r.Body != nil {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			logger.Error("Error reading request body: %v", err)
		} else {
			body = string(bodyBytes)
			// Restore body for potential future reads
			r.Body = io.NopCloser(strings.NewReader(body))
		}
	}

	// Try common variants of the key
	keys := []string{
		path,
		"http://" + r.Host + path,
	}

	for _, key := range keys {
		i.registry.CheckNegativeHTTPMocks(key, method)
	}
	bodyMatcher := makeBodyMatcher(body, i.resolver)

	var mock *types.ExpectStatement
	var found bool
	for _, key := range keys {
		mock, found = i.registry.FindHTTPMockWithBody(key, method, requestHeaders, bodyMatcher)
		if found {
			break
		}
	}

	if !found {
		logger.Debug("No mock found for %s %s (Tried keys: %v)", method, path, keys)
		i.registry.RecordPassthrough("HTTP " + method + " " + path)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// 2. Execute VERIFY rules if any
	if len(mock.Verify) > 0 {
		// Filter rules for HTTP targets only
		httpRules := verify.ExtractVerifyRulesForTarget(mock.Verify, "http")
		if len(httpRules) > 0 {
			req := &verify.HTTPRequest{
				Method:  method,
				URL:     r.URL.String(),
				Path:    path,
				Headers: requestHeaders,
				Body:    body,
			}
			if err := verify.VerifyHTTP(req, httpRules); err != nil {
				logger.Error("VERIFY failed for HTTP %s %s: %v", method, path, err)
				i.registry.RecordVerifyError(fmt.Sprintf("HTTP [%s %s]: %v", method, path, err))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				response := map[string]string{
					"error": fmt.Sprintf("VERIFY failed: %v", err),
				}
				if encodeErr := json.NewEncoder(w).Encode(response); encodeErr != nil {
					logger.Error("Failed to encode error response: %v", encodeErr)
				}
				return
			}
			logger.Debug("VERIFY passed for HTTP %s %s", method, path)
		}
	}

	// 3. Handle RETURNS ERROR (simulate network/connection failure)
	if mock.ReturnsError {
		logger.Debug("RETURNS ERROR: simulating connection failure for %s %s", method, path)
		// Hijack the connection to simulate a network failure
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, _, err := hijacker.Hijack()
			if err == nil {
				conn.Close()
				return
			}
		}
		// Fallback: return 503 Service Unavailable if hijack fails
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	// 4. Handle RETURNS HTTP:NNN (return specific HTTP status from dependency)
	if mock.ReturnsHTTPStatus > 0 {
		for k, v := range mock.ResponseHeaders {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(mock.ReturnsHTTPStatus)
		// If there's also a ReturnsFile, serve its body
		if mock.ReturnsFile != "" {
			i.loader.BaseDir = mock.BaseDir
			rawData, inferredContentType, err := i.loader.LoadRaw(mock.ReturnsFile)
			if err == nil {
				responseBody := rawData
				if inferredContentType == "application/json" || inferredContentType == "application/yaml" {
					if payload, loadErr := i.loader.Load(mock.ReturnsFile); loadErr == nil {
						if encoded, encErr := json.Marshal(payload); encErr == nil {
							responseBody = encoded
						}
					}
				}
				w.Write(responseBody)
			}
		}
		return
	}

	// 5. Load payload if needed
	if mock.ReturnsFile != "" {
		i.loader.BaseDir = mock.BaseDir

		rawData, inferredContentType, err := i.loader.LoadRaw(mock.ReturnsFile)
		if err != nil {
			logger.Error("Error loading payload %s: %v", mock.ReturnsFile, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// For JSON and YAML payloads, parse and re-encode as JSON so HTTP clients receive
		// a proper JSON response regardless of the source file format.
		responseBody := rawData
		contentType := inferredContentType
		if inferredContentType == "application/json" || inferredContentType == "application/yaml" {
			payload, loadErr := i.loader.Load(mock.ReturnsFile)
			if loadErr == nil {
				if encoded, encErr := json.Marshal(payload); encErr == nil {
					responseBody = encoded
					contentType = "application/json"
				}
			}
		}

		// Extract status code from parsed payload if present
		status := http.StatusOK
		if inferredContentType == "application/json" || inferredContentType == "application/yaml" {
			if payload, loadErr := i.loader.Load(mock.ReturnsFile); loadErr == nil {
				if m, ok := payload.(map[string]interface{}); ok {
					if s, ok := m["status"].(float64); ok {
						status = int(s)
					} else if s, ok := m["status"].(int); ok {
						status = s
					}
				}
			}
		}

		// Explicit ResponseHeaders may override Content-Type
		for k, v := range mock.ResponseHeaders {
			if strings.ToLower(k) == "content-type" {
				contentType = v
			} else {
				w.Header().Set(k, v)
			}
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		w.Write(responseBody)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// makeBodyMatcher returns a registry body-match callback for HTTP requests. When the
// mock has no WithFile the callback always returns true (match-any). Otherwise it loads
// the file, parses the actual body as JSON, and delegates to verify.CompareJSON.
func makeBodyMatcher(actualBody string, resolver *interpolate.Resolver) func(withFile, baseDir string) bool {
	return verify.MakeJSONBodyMatcher(actualBody, "request body", resolver)
}

// Note: Rails-specific auth extraction has been removed.
// Use custom auth extraction rules via .linespec.yml payload.auth_extraction configuration
// if you need to extract authentication from request bodies.
