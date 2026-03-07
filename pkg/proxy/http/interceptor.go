package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/calebcowen/linespec/pkg/dsl"
	"github.com/calebcowen/linespec/pkg/registry"
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
	fullURL := fmt.Sprintf("http://%s%s", r.Host, r.URL.Path)
	fmt.Printf("HTTP Interceptor: Intercepted %s %s\n", r.Method, fullURL)

	mock, found := i.registry.FindMock(fullURL, "")
	if !found {
		mock, found = i.registry.FindMock(r.URL.Path, "")
	}

	if !found {
		fmt.Printf("HTTP Interceptor: No mock found for %s\n", fullURL)
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
