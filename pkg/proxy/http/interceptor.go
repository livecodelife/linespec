package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/calebcowen/linespec/pkg/dsl"
	"github.com/calebcowen/linespec/pkg/registry"
	"github.com/calebcowen/linespec/pkg/types"
)

type Interceptor struct {
	addr     string
	registry *registry.MockRegistry
	loader   *dsl.PayloadLoader
}

func NewInterceptor(addr string, reg *registry.MockRegistry) *Interceptor {
	return &Interceptor{
		addr:     addr,
		registry: reg,
		loader:   &dsl.PayloadLoader{},
	}
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

	fmt.Printf("HTTP Interceptor listening on %s\n", i.addr)
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
	fmt.Printf("HTTP Interceptor: Intercepted %s %s (Host: %s)\n", method, path, r.Host)

	// Extract headers from request
	requestHeaders := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			requestHeaders[k] = v[0]
		}
	}
	fmt.Printf("HTTP Interceptor: Request headers: %v\n", requestHeaders)

	// Also extract authorization from request body (for Rails apps that send auth in body)
	bodyAuth := i.extractAuthFromBody(r)
	if bodyAuth != "" {
		fmt.Printf("HTTP Interceptor: Found authorization in body: %s\n", bodyAuth)
		// Add it to headers for matching purposes
		requestHeaders["Authorization"] = bodyAuth
	}

	// Try common variants of the key
	keys := []string{
		path,
		"http://" + r.Host + path,
		"http://user-service.local" + path, // Common alias
	}

	var mock *types.ExpectStatement
	var found bool
	for _, key := range keys {
		mock, found = i.registry.FindHTTPMockWithHeaders(key, method, requestHeaders)
		if found {
			break
		}
	}

	if !found {
		fmt.Printf("HTTP Interceptor: No mock found for %s %s (Tried keys: %v)\n", method, path, keys)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// 2. Load payload if needed
	if mock.ReturnsFile != "" {
		i.loader.BaseDir = mock.BaseDir
		payload, err := i.loader.Load(mock.ReturnsFile)
		if err != nil {
			fmt.Printf("HTTP Interceptor: Error loading payload %s: %v\n", mock.ReturnsFile, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		status := http.StatusOK
		if m, ok := payload.(map[string]interface{}); ok {
			if s, ok := m["status"].(float64); ok {
				status = int(s)
			} else if s, ok := m["status"].(int); ok {
				status = s
			}
		}

		data, _ := json.Marshal(payload)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(data)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// extractAuthFromBody extracts authorization from request body
// Rails apps often send auth as: { "authorization": "Bearer token" }
func (i *Interceptor) extractAuthFromBody(r *http.Request) string {
	// Only parse body for GET/POST/PATCH/PUT methods with Content-Type: application/json
	if r.Body == nil {
		return ""
	}

	contentType := r.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		return ""
	}

	// Read body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	// Restore body for potential future reads
	r.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))

	if len(bodyBytes) == 0 {
		return ""
	}

	// Try to parse as JSON
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
		return ""
	}

	// Look for authorization field
	if auth, ok := bodyMap["authorization"].(string); ok && auth != "" {
		return auth
	}

	return ""
}
