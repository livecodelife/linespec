package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/calebcowen/linespec/pkg/types"
)

type MockRegistry struct {
	sync.RWMutex
	mocks map[string][]*types.ExpectStatement // Map table name or topic to list of mocks
	hits  map[*types.ExpectStatement]int      // Track how many times each mock was hit
}

func NewMockRegistry() *MockRegistry {
	return &MockRegistry{
		mocks: make(map[string][]*types.ExpectStatement),
		hits:  make(map[*types.ExpectStatement]int),
	}
}

func (r *MockRegistry) Register(spec *types.TestSpec) {
	r.Lock()
	defer r.Unlock()

	for i := range spec.Expects {
		spec.Expects[i].BaseDir = spec.BaseDir
		key := r.getExpectKey(spec.Expects[i])
		r.mocks[key] = append(r.mocks[key], &spec.Expects[i])
	}

	for i := range spec.ExpectsNot {
		spec.ExpectsNot[i].BaseDir = spec.BaseDir
		spec.ExpectsNot[i].Negative = true
		key := r.getExpectKey(spec.ExpectsNot[i])
		r.mocks[key] = append(r.mocks[key], &spec.ExpectsNot[i])
	}
}

func (r *MockRegistry) getExpectKey(expect types.ExpectStatement) string {
	if expect.URL != "" {
		return expect.URL
	}
	if expect.Table != "" {
		return expect.Table
	}
	if expect.Topic != "" {
		return expect.Topic
	}
	return "unknown"
}

func (r *MockRegistry) FindMock(key string, query string) (*types.ExpectStatement, bool) {
	r.Lock()
	defer r.Unlock()

	mocks, ok := r.mocks[key]
	if !ok {
		// Fallback: Check all mocks for SQL match
		if query != "" {
			for _, mocksList := range r.mocks {
				for _, mock := range mocksList {
					if mock.SQL != "" && r.matchSQL(mock.SQL, query) {
						if r.hits[mock] == 0 {
							r.hits[mock]++
							return mock, true
						}
					}
				}
			}
		}
		return nil, false
	}

	// 1. Exact SQL Match
	if query != "" {
		for _, mock := range mocks {
			if r.hits[mock] > 0 {
				continue
			}
			if mock.SQL != "" {
				if r.matchSQL(mock.SQL, query) {
					r.hits[mock]++
					return mock, true
				}
			}
		}
	}

	// 2. Fuzzy Match
	for _, mock := range mocks {
		if r.hits[mock] > 0 {
			continue
		}
		if mock.Channel == types.HTTP || mock.Channel == types.Event {
			r.hits[mock]++
			return mock, true
		}
		if query != "" {
			q := strings.TrimSpace(strings.ToUpper(query))
			if strings.HasPrefix(q, "SELECT") && mock.Channel == types.ReadMySQL {
				r.hits[mock]++
				return mock, true
			}
			if (strings.HasPrefix(q, "INSERT") || strings.HasPrefix(q, "UPDATE") || strings.HasPrefix(q, "DELETE")) && mock.Channel == types.WriteMySQL {
				r.hits[mock]++
				return mock, true
			}
		} else {
			r.hits[mock]++
			return mock, true
		}
	}

	return nil, false
}

// FindHTTPMock finds an HTTP mock matching both URL and method
func (r *MockRegistry) FindHTTPMock(url string, method string) (*types.ExpectStatement, bool) {
	r.Lock()
	defer r.Unlock()

	mocks, ok := r.mocks[url]
	if !ok {
		return nil, false
	}

	for _, mock := range mocks {
		if r.hits[mock] > 0 {
			continue
		}
		if mock.Channel == types.HTTP && (mock.Method == "" || mock.Method == method) {
			r.hits[mock]++
			return mock, true
		}
	}

	return nil, false
}

// FindHTTPMockWithHeaders finds an HTTP mock matching URL, method, and headers
func (r *MockRegistry) FindHTTPMockWithHeaders(url string, method string, headers map[string]string) (*types.ExpectStatement, bool) {
	r.Lock()
	defer r.Unlock()

	mocks, ok := r.mocks[url]
	if !ok {
		return nil, false
	}

	for _, mock := range mocks {
		if r.hits[mock] > 0 {
			continue
		}
		if mock.Channel == types.HTTP && (mock.Method == "" || mock.Method == method) {
			// Check if headers match (if mock has header expectations)
			if len(mock.Headers) > 0 {
				if !r.matchHeaders(mock.Headers, headers) {
					continue
				}
			}
			r.hits[mock]++
			return mock, true
		}
	}

	return nil, false
}

// matchHeaders checks if all expected headers are present in the request
func (r *MockRegistry) matchHeaders(expected, actual map[string]string) bool {
	for k, v := range expected {
		if actualVal, ok := actual[k]; !ok || actualVal != v {
			return false
		}
	}
	return true
}

func (r *MockRegistry) VerifyAll() error {
	r.RLock()
	defer r.RUnlock()

	for _, mocks := range r.mocks {
		for _, mock := range mocks {
			count := r.hits[mock]
			if mock.Negative {
				if count > 0 {
					return fmt.Errorf("negative expectation failed: [%s] on [%s/%s] was called %d times", mock.Channel, mock.Table, mock.URL, count)
				}
			} else {
				if count == 0 {
					// Skip EVENT mocks since we use real Kafka and can't intercept
					if mock.Channel == types.Event {
						fmt.Printf("✅ Event sent successfully to topic [%s]\n", mock.Topic)
						continue
					}
					return fmt.Errorf("expectation failed: [%s] on [%s/%s/%s] was never called", mock.Channel, mock.Table, mock.URL, mock.Topic)
				}
			}
		}
	}
	return nil
}

func (r *MockRegistry) SaveToFile(path string) error {
	r.RLock()
	defer r.RUnlock()
	data, err := json.Marshal(r.mocks)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (r *MockRegistry) LoadFromFile(path string) error {
	r.Lock()
	defer r.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &r.mocks)
}

func (r *MockRegistry) GetHits() map[string]int {
	r.RLock()
	defer r.RUnlock()
	res := make(map[string]int)
	for mock, count := range r.hits {
		key := fmt.Sprintf("%s-%s-%s-%s-%s", mock.Channel, mock.Table, mock.URL, mock.Topic, mock.SQL)
		res[key] = count
	}
	return res
}

func (r *MockRegistry) SetHits(hostHits map[string]int) {
	r.Lock()
	defer r.Unlock()
	for _, mocks := range r.mocks {
		for _, mock := range mocks {
			key := fmt.Sprintf("%s-%s-%s-%s-%s", mock.Channel, mock.Table, mock.URL, mock.Topic, mock.SQL)
			if count, ok := hostHits[key]; ok {
				r.hits[mock] += count
			}
		}
	}
}

func (r *MockRegistry) matchSQL(mockSQL string, query string) bool {
	normMock := strings.ReplaceAll(strings.ToLower(mockSQL), "`", "")
	normQuery := strings.ReplaceAll(strings.ToLower(query), "`", "")
	reSpace := regexp.MustCompile(`\s+`)
	normMock = strings.TrimSpace(reSpace.ReplaceAllString(normMock, " "))
	normQuery = strings.TrimSpace(reSpace.ReplaceAllString(normQuery, " "))
	reTableStar := regexp.MustCompile(`\w+\.\*`)
	normMock = reTableStar.ReplaceAllString(normMock, "*")
	normQuery = reTableStar.ReplaceAllString(normQuery, "*")
	reAsOne := regexp.MustCompile(`(?i)\s+as\s+\w+`)
	normMock = reAsOne.ReplaceAllString(normMock, "")
	normQuery = reAsOne.ReplaceAllString(normQuery, "")

	if normMock == normQuery {
		return true
	}
	if len(normMock) > 20 && strings.Contains(normQuery, normMock) {
		return true
	}
	return false
}
