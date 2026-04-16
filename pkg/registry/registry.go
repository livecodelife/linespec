package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/livecodelife/linespec/pkg/logger"
	"github.com/livecodelife/linespec/pkg/types"
)

type MockRegistry struct {
	sync.RWMutex
	mocks        map[string][]*types.ExpectStatement // Map table name or topic to list of mocks
	orderedMocks []*types.ExpectStatement            // All mocks in registration order (for deterministic fallback)
	hits         map[*types.ExpectStatement]int      // Track how many times each mock was hit
	seeds        map[string][][]byte                 // Kafka seed messages: topic -> ordered list of raw payloads
	passthroughs []string                            // Descriptions of requests that bypassed the mock layer
	verifyErrors []string                            // VERIFY rule failures recorded by proxies
}

func NewMockRegistry() *MockRegistry {
	return &MockRegistry{
		mocks:        make(map[string][]*types.ExpectStatement),
		orderedMocks: make([]*types.ExpectStatement, 0),
		hits:         make(map[*types.ExpectStatement]int),
		seeds:        make(map[string][][]byte),
		passthroughs: make([]string, 0),
	}
}

// SeedTopic adds a raw payload to be served to Kafka consumers on a given topic.
func (r *MockRegistry) SeedTopic(topic string, value []byte) {
	r.Lock()
	defer r.Unlock()
	r.seeds[topic] = append(r.seeds[topic], value)
}

// GetSeeds returns a copy of all seeded Kafka messages.
func (r *MockRegistry) GetSeeds() map[string][][]byte {
	r.RLock()
	defer r.RUnlock()
	out := make(map[string][][]byte, len(r.seeds))
	for topic, msgs := range r.seeds {
		cp := make([][]byte, len(msgs))
		copy(cp, msgs)
		out[topic] = cp
	}
	return out
}

// ResetHits resets the hit count for all mocks (useful for testing)
func (r *MockRegistry) ResetHits() {
	r.Lock()
	defer r.Unlock()
	r.hits = make(map[*types.ExpectStatement]int)
}

// ClearState resets hits, passthroughs, and verifyErrors without touching mocks.
// Used after LoadFromBytes/LoadFromFile when hot-reloading between test runs.
func (r *MockRegistry) ClearState() {
	r.Lock()
	defer r.Unlock()
	r.hits = make(map[*types.ExpectStatement]int)
	r.passthroughs = make([]string, 0)
	r.verifyErrors = make([]string, 0)
}

// ToBytes serialises the registry mocks and seeds to JSON.
func (r *MockRegistry) ToBytes() ([]byte, error) {
	r.RLock()
	defer r.RUnlock()
	return json.Marshal(registryFile{Mocks: r.mocks, Seeds: r.seeds})
}

// LoadFromBytes replaces the registry contents from JSON bytes (same format as SaveToFile).
func (r *MockRegistry) LoadFromBytes(data []byte) error {
	r.Lock()
	defer r.Unlock()
	var rf registryFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return err
	}
	if rf.Mocks != nil {
		r.mocks = rf.Mocks
	} else {
		if err := json.Unmarshal(data, &r.mocks); err != nil {
			return err
		}
	}
	if rf.Seeds != nil {
		r.seeds = rf.Seeds
	}
	r.orderedMocks = make([]*types.ExpectStatement, 0)
	for _, mocksList := range r.mocks {
		r.orderedMocks = append(r.orderedMocks, mocksList...)
	}
	return nil
}

// RecordPassthrough records that a request bypassed the mock layer (no matching mock found).
// description should be a short human-readable string identifying the request, e.g. "SELECT users".
func (r *MockRegistry) RecordPassthrough(description string) {
	r.Lock()
	defer r.Unlock()
	r.passthroughs = append(r.passthroughs, description)
}

// GetPassthroughs returns a copy of all recorded passthrough descriptions.
func (r *MockRegistry) GetPassthroughs() []string {
	r.RLock()
	defer r.RUnlock()
	cp := make([]string, len(r.passthroughs))
	copy(cp, r.passthroughs)
	return cp
}

// AddPassthroughs merges passthrough records from a proxy container into this registry.
func (r *MockRegistry) AddPassthroughs(passthroughs []string) {
	r.Lock()
	defer r.Unlock()
	r.passthroughs = append(r.passthroughs, passthroughs...)
}

// RecordVerifyError records a VERIFY rule failure. Called by proxies when a VERIFY check fails.
func (r *MockRegistry) RecordVerifyError(msg string) {
	r.Lock()
	defer r.Unlock()
	r.verifyErrors = append(r.verifyErrors, msg)
}

// GetVerifyErrors returns a copy of all recorded VERIFY failures.
func (r *MockRegistry) GetVerifyErrors() []string {
	r.RLock()
	defer r.RUnlock()
	cp := make([]string, len(r.verifyErrors))
	copy(cp, r.verifyErrors)
	return cp
}

// AddVerifyErrors merges VERIFY failure records from a proxy container into this registry.
func (r *MockRegistry) AddVerifyErrors(errors []string) {
	r.Lock()
	defer r.Unlock()
	r.verifyErrors = append(r.verifyErrors, errors...)
}

// IncrementHit increments the hit count for a specific mock (thread-safe)
func (r *MockRegistry) IncrementHit(mock *types.ExpectStatement) {
	r.Lock()
	defer r.Unlock()
	r.hits[mock]++
}

func (r *MockRegistry) Register(spec *types.TestSpec) {
	r.Lock()
	defer r.Unlock()

	for i := range spec.Expects {
		spec.Expects[i].BaseDir = spec.BaseDir
		key := r.getExpectKey(spec.Expects[i])
		r.mocks[key] = append(r.mocks[key], &spec.Expects[i])
		r.orderedMocks = append(r.orderedMocks, &spec.Expects[i])
	}

	for i := range spec.ExpectsNot {
		spec.ExpectsNot[i].BaseDir = spec.BaseDir
		spec.ExpectsNot[i].Negative = true
		key := r.getExpectKey(spec.ExpectsNot[i])
		r.mocks[key] = append(r.mocks[key], &spec.ExpectsNot[i])
		r.orderedMocks = append(r.orderedMocks, &spec.ExpectsNot[i])
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
	if expect.Service != "" && expect.RPCMethod != "" {
		return expect.Service + "/" + expect.RPCMethod
	}
	if expect.Command != "" || expect.RedisKey != "" {
		return expect.Command + ":" + expect.RedisKey
	}
	return "unknown"
}

// GetEventTopics returns all distinct Kafka topic names from EVENT channel mocks.
func (r *MockRegistry) GetEventTopics() []string {
	r.RLock()
	defer r.RUnlock()
	seen := make(map[string]struct{})
	for _, mocks := range r.mocks {
		for _, mock := range mocks {
			if mock.Channel == types.Event && mock.Topic != "" {
				seen[mock.Topic] = struct{}{}
			}
		}
	}
	topics := make([]string, 0, len(seen))
	for t := range seen {
		topics = append(topics, t)
	}
	return topics
}

// GetTables returns a list of unique table names registered in the registry
func (r *MockRegistry) GetTables() []string {
	r.RLock()
	defer r.RUnlock()

	tableSet := make(map[string]bool)
	for key, mocks := range r.mocks {
		// Check if any mock for this key is a database operation
		for _, mock := range mocks {
			if mock.Channel == types.ReadMySQL || mock.Channel == types.WriteMySQL ||
				mock.Channel == types.ReadPostgreSQL || mock.Channel == types.WritePostgreSQL ||
				mock.Channel == types.ReadMongoDB || mock.Channel == types.WriteMongoDB {
				tableSet[key] = true
				break
			}
		}
	}

	tables := make([]string, 0, len(tableSet))
	for table := range tableSet {
		tables = append(tables, table)
	}
	return tables
}

// PeekMock checks if a mock exists without incrementing hit count (used for testing intercept)
func (r *MockRegistry) PeekMock(key string, query string) (*types.ExpectStatement, bool) {
	r.RLock()
	defer r.RUnlock()

	mocks, ok := r.mocks[key]
	if !ok {
		// Fallback: scan all mocks in registration order for an SQL match.
		// Uses orderedMocks (a slice) instead of the map to guarantee
		// deterministic results — first declared match wins.
		if query != "" {
			for _, mock := range r.orderedMocks {
				if mock.Negative {
					continue
				}
				if mock.SQL != "" && r.matchSQL(mock.SQL, query) {
					if r.hits[mock] == 0 {
						return mock, true
					}
				}
			}
		}
		return nil, false
	}

	// 1. Exact SQL Match
	if query != "" {
		for _, mock := range mocks {
			if mock.Negative {
				continue
			}
			if r.hits[mock] > 0 {
				continue
			}
			if mock.SQL != "" {
				if r.matchSQL(mock.SQL, query) {
					return mock, true
				}
			}
		}
	}

	// 2. Fuzzy Match
	for _, mock := range mocks {
		if mock.Negative {
			continue
		}
		if r.hits[mock] > 0 {
			continue
		}
		if mock.Channel == types.HTTP || mock.Channel == types.Event {
			return mock, true
		}
		if query != "" {
			q := strings.TrimSpace(strings.ToUpper(query))
			if strings.HasPrefix(q, "SELECT") && (mock.Channel == types.ReadMySQL || mock.Channel == types.ReadPostgreSQL) {
				return mock, true
			}
			if (strings.HasPrefix(q, "INSERT") || strings.HasPrefix(q, "UPDATE") || strings.HasPrefix(q, "DELETE")) && (mock.Channel == types.WriteMySQL || mock.Channel == types.WritePostgreSQL) {
				return mock, true
			}
			if isMongoReadCommand(q) && mock.Channel == types.ReadMongoDB {
				return mock, true
			}
			if isMongoWriteCommand(q) && mock.Channel == types.WriteMongoDB {
				return mock, true
			}
		} else {
			return mock, true
		}
	}

	return nil, false
}

func (r *MockRegistry) FindMock(key string, query string) (*types.ExpectStatement, bool) {
	r.Lock()
	defer r.Unlock()

	mocks, ok := r.mocks[key]
	if !ok {
		// Fallback: scan all mocks in registration order for an SQL match.
		// Uses orderedMocks (a slice) instead of the map to guarantee
		// deterministic results — first declared match wins.
		if query != "" {
			for _, mock := range r.orderedMocks {
				if mock.Negative {
					continue
				}
				if mock.SQL != "" && r.matchSQL(mock.SQL, query) {
					if r.hits[mock] == 0 {
						r.hits[mock]++
						return mock, true
					}
				}
			}
		}
		return nil, false
	}

	// 1. Exact SQL Match
	if query != "" {
		for _, mock := range mocks {
			if mock.Negative {
				continue
			}
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
		if mock.Negative {
			continue
		}
		if r.hits[mock] > 0 {
			continue
		}
		if mock.Channel == types.HTTP || mock.Channel == types.Event {
			r.hits[mock]++
			return mock, true
		}
		if query != "" {
			q := strings.TrimSpace(strings.ToUpper(query))
			if strings.HasPrefix(q, "SELECT") && (mock.Channel == types.ReadMySQL || mock.Channel == types.ReadPostgreSQL) {
				r.hits[mock]++
				return mock, true
			}
			if (strings.HasPrefix(q, "INSERT") || strings.HasPrefix(q, "UPDATE") || strings.HasPrefix(q, "DELETE")) && (mock.Channel == types.WriteMySQL || mock.Channel == types.WritePostgreSQL) {
				r.hits[mock]++
				return mock, true
			}
			if isMongoReadCommand(q) && mock.Channel == types.ReadMongoDB {
				r.hits[mock]++
				return mock, true
			}
			if isMongoWriteCommand(q) && mock.Channel == types.WriteMongoDB {
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
		if mock.Negative {
			continue
		}
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
		if mock.Negative {
			continue
		}
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

	if len(r.verifyErrors) > 0 {
		return fmt.Errorf("VERIFY failed: %s", r.verifyErrors[0])
	}

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
						logger.Debug("Event sent successfully to topic [%s]", mock.Topic)
						continue
					}
					return fmt.Errorf("expectation failed: [%s] on [%s/%s/%s] was never called", mock.Channel, mock.Table, mock.URL, mock.Topic)
				}
			}
		}
	}
	return nil
}

// CheckNegativeMocks checks incoming DB/Kafka requests against negative expectations
// and increments their hit counters if matched. Called by proxies alongside FindMock
// so that EXPECT_NOT violations are detected at verification time.
func (r *MockRegistry) CheckNegativeMocks(key string, query string) {
	r.Lock()
	defer r.Unlock()

	if mocks, ok := r.mocks[key]; ok {
		for _, mock := range mocks {
			if !mock.Negative {
				continue
			}
			if query != "" && mock.SQL != "" {
				// Explicit SQL constraint: use proper SQL matching.
				if r.matchSQL(mock.SQL, query) {
					r.hits[mock]++
				}
			} else if query != "" {
				// No SQL constraint on the mock: match on channel type.
				// Also require the table name (key) to appear in the query to
				// prevent a JOIN or other multi-table query from triggering a
				// negative mock that is not related to the table being accessed.
				q := strings.TrimSpace(strings.ToUpper(query))
				keyUpper := strings.ToUpper(key)
				if strings.HasPrefix(q, "SELECT") &&
					(mock.Channel == types.ReadMySQL || mock.Channel == types.ReadPostgreSQL) &&
					strings.Contains(q, keyUpper) {
					r.hits[mock]++
				} else if (strings.HasPrefix(q, "INSERT") || strings.HasPrefix(q, "UPDATE") || strings.HasPrefix(q, "DELETE")) &&
					(mock.Channel == types.WriteMySQL || mock.Channel == types.WritePostgreSQL) &&
					strings.Contains(q, keyUpper) {
					r.hits[mock]++
				} else if isMongoReadCommand(q) && mock.Channel == types.ReadMongoDB {
					r.hits[mock]++
				} else if isMongoWriteCommand(q) && mock.Channel == types.WriteMongoDB {
					r.hits[mock]++
				}
			} else {
				// Empty query: only fire for non-SQL channel types (e.g. Kafka events).
				// SQL-type and MongoDB negative mocks require a command name to match against.
				if mock.Channel != types.ReadMySQL && mock.Channel != types.ReadPostgreSQL &&
					mock.Channel != types.WriteMySQL && mock.Channel != types.WritePostgreSQL &&
					mock.Channel != types.ReadMongoDB && mock.Channel != types.WriteMongoDB {
					r.hits[mock]++
				}
			}
		}
	}

	// Fallback: scan orderedMocks for SQL matches when key is not found
	if query != "" {
		if _, ok := r.mocks[key]; !ok {
			for _, mock := range r.orderedMocks {
				if !mock.Negative {
					continue
				}
				if mock.SQL != "" && r.matchSQL(mock.SQL, query) {
					r.hits[mock]++
				}
			}
		}
	}
}

// CheckNegativeHTTPMocks checks incoming HTTP requests against negative expectations
// and increments their hit counters if matched. Called by the HTTP proxy alongside
// FindHTTPMock so that EXPECT_NOT violations are detected at verification time.
func (r *MockRegistry) CheckNegativeHTTPMocks(url string, method string) {
	r.Lock()
	defer r.Unlock()

	if mocks, ok := r.mocks[url]; ok {
		for _, mock := range mocks {
			if !mock.Negative {
				continue
			}
			if mock.Channel == types.HTTP && (mock.Method == "" || mock.Method == method) {
				r.hits[mock]++
			}
		}
	}
}

// FindGRPCMock finds a gRPC mock matching the service and method.
func (r *MockRegistry) FindGRPCMock(service, method string) (*types.ExpectStatement, bool) {
	r.Lock()
	defer r.Unlock()

	key := service + "/" + method
	mocks, ok := r.mocks[key]
	if !ok {
		return nil, false
	}

	for _, mock := range mocks {
		if mock.Negative {
			continue
		}
		if r.hits[mock] > 0 {
			continue
		}
		if mock.Channel == types.GRPC {
			r.hits[mock]++
			return mock, true
		}
	}
	return nil, false
}

// CheckNegativeGRPCMocks checks incoming gRPC requests against negative expectations.
func (r *MockRegistry) CheckNegativeGRPCMocks(service, method string) {
	r.Lock()
	defer r.Unlock()

	key := service + "/" + method
	if mocks, ok := r.mocks[key]; ok {
		for _, mock := range mocks {
			if !mock.Negative {
				continue
			}
			if mock.Channel == types.GRPC {
				r.hits[mock]++
			}
		}
	}
}

// FindRedisMock finds a Redis mock matching the command and key.
func (r *MockRegistry) FindRedisMock(command, key string) (*types.ExpectStatement, bool) {
	r.Lock()
	defer r.Unlock()

	mockKey := command + ":" + key
	mocks, ok := r.mocks[mockKey]
	if !ok {
		return nil, false
	}

	for _, mock := range mocks {
		if mock.Negative {
			continue
		}
		if r.hits[mock] > 0 {
			continue
		}
		if mock.Channel == types.ReadRedis || mock.Channel == types.WriteRedis {
			r.hits[mock]++
			return mock, true
		}
	}
	return nil, false
}

// CheckNegativeRedisMocks checks incoming Redis commands against negative expectations.
func (r *MockRegistry) CheckNegativeRedisMocks(command, key string) {
	r.Lock()
	defer r.Unlock()

	mockKey := command + ":" + key
	if mocks, ok := r.mocks[mockKey]; ok {
		for _, mock := range mocks {
			if !mock.Negative {
				continue
			}
			if mock.Channel == types.ReadRedis || mock.Channel == types.WriteRedis {
				r.hits[mock]++
			}
		}
	}
}

type registryFile struct {
	Mocks map[string][]*types.ExpectStatement `json:"mocks"`
	Seeds map[string][][]byte                 `json:"seeds,omitempty"`
}

func (r *MockRegistry) SaveToFile(path string) error {
	r.RLock()
	defer r.RUnlock()
	data, err := json.Marshal(registryFile{Mocks: r.mocks, Seeds: r.seeds})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// rebaseDir rewrites an absolute BaseDir path from a host filesystem path to
// its equivalent path inside a Docker container, where the host CWD is mounted
// at containerMount. If baseDir is not under hostCwd, it is returned unchanged.
func rebaseDir(baseDir, hostCwd, containerMount string) string {
	rel, err := filepath.Rel(hostCwd, baseDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return baseDir
	}
	return filepath.Join(containerMount, rel)
}

// ToBytesForContainer serialises the registry with all BaseDir fields rewritten
// from absolute host paths to container-internal paths. hostCwd is the host
// working directory; containerProjectMount is where it is bind-mounted inside
// the proxy sidecar container (e.g. "/app/project").
func (r *MockRegistry) ToBytesForContainer(hostCwd, containerProjectMount string) ([]byte, error) {
	r.RLock()
	defer r.RUnlock()

	rebasedMocks := make(map[string][]*types.ExpectStatement, len(r.mocks))
	for key, mocks := range r.mocks {
		rebased := make([]*types.ExpectStatement, len(mocks))
		for i, m := range mocks {
			cp := *m
			cp.BaseDir = rebaseDir(m.BaseDir, hostCwd, containerProjectMount)
			rebased[i] = &cp
		}
		rebasedMocks[key] = rebased
	}

	return json.Marshal(registryFile{Mocks: rebasedMocks, Seeds: r.seeds})
}

// SaveToFileForContainer saves the registry to path with BaseDir fields rewritten
// for use inside a Docker container. See ToBytesForContainer for parameter docs.
func (r *MockRegistry) SaveToFileForContainer(path, hostCwd, containerProjectMount string) error {
	data, err := r.ToBytesForContainer(hostCwd, containerProjectMount)
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
	// Try new format first (with seeds).
	var rf registryFile
	if err := json.Unmarshal(data, &rf); err != nil {
		return err
	}
	if rf.Mocks != nil {
		r.mocks = rf.Mocks
	} else {
		// Legacy format: the file was a bare map[string][]*ExpectStatement.
		if err := json.Unmarshal(data, &r.mocks); err != nil {
			return err
		}
	}
	if rf.Seeds != nil {
		r.seeds = rf.Seeds
	}
	r.orderedMocks = make([]*types.ExpectStatement, 0)
	for _, mocksList := range r.mocks {
		r.orderedMocks = append(r.orderedMocks, mocksList...)
	}
	return nil
}

func (r *MockRegistry) GetHits() map[string]int {
	r.RLock()
	defer r.RUnlock()
	res := make(map[string]int)
	for mock, count := range r.hits {
		// Use consistent key format:
		// For HTTP: Channel-URL (Table/Topic/SQL are empty)
		// For DB: Channel-Table-SQL (only for READ operations with explicit SQL)
		// For WRITE: Channel-Table only (SQL is auto-generated at runtime)
		var key string
		switch mock.Channel {
		case types.HTTP:
			key = fmt.Sprintf("%s-%s", mock.Channel, mock.URL)
		case types.ReadMySQL, types.ReadPostgreSQL:
			// READ operations: include SQL to distinguish different queries
			key = fmt.Sprintf("%s-%s-%s", mock.Channel, mock.Table, mock.SQL)
		case types.GRPC:
			key = fmt.Sprintf("%s-%s/%s", mock.Channel, mock.Service, mock.RPCMethod)
		case types.ReadRedis, types.WriteRedis:
			key = fmt.Sprintf("%s-%s:%s", mock.Channel, mock.Command, mock.RedisKey)
		default:
			// WRITE and other operations: use Channel-Table only
			key = fmt.Sprintf("%s-%s", mock.Channel, mock.Table)
		}
		res[key] = count
	}
	return res
}

func (r *MockRegistry) SetHits(hostHits map[string]int) {
	r.Lock()
	defer r.Unlock()
	for _, mocks := range r.mocks {
		for _, mock := range mocks {
			// Use same key format as GetHits
			var key string
			switch mock.Channel {
			case types.HTTP:
				key = fmt.Sprintf("%s-%s", mock.Channel, mock.URL)
			case types.ReadMySQL, types.ReadPostgreSQL:
				key = fmt.Sprintf("%s-%s-%s", mock.Channel, mock.Table, mock.SQL)
			case types.GRPC:
				key = fmt.Sprintf("%s-%s/%s", mock.Channel, mock.Service, mock.RPCMethod)
			case types.ReadRedis, types.WriteRedis:
				key = fmt.Sprintf("%s-%s:%s", mock.Channel, mock.Command, mock.RedisKey)
			default:
				key = fmt.Sprintf("%s-%s", mock.Channel, mock.Table)
			}
			if count, ok := hostHits[key]; ok {
				r.hits[mock] += count
			}
		}
	}
}

func (r *MockRegistry) matchSQL(mockSQL string, query string) bool {
	normMock := strings.ReplaceAll(strings.ToLower(mockSQL), "`", "")
	normQuery := strings.ReplaceAll(strings.ToLower(query), "`", "")

	// Normalize table prefixes like `users`.`id` to `users.id`
	reTablePrefix := regexp.MustCompile(`(\w+)\.(\w+)`)
	normMock = reTablePrefix.ReplaceAllString(normMock, "$1.$2")
	normQuery = reTablePrefix.ReplaceAllString(normQuery, "$1.$2")

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

// VerifyPassthroughs logs a warning for every recorded passthrough. If strict is true and
// any passthroughs were recorded, it returns an error so the test fails.
func (r *MockRegistry) VerifyPassthroughs(strict bool) error {
	r.RLock()
	defer r.RUnlock()
	if len(r.passthroughs) == 0 {
		return nil
	}
	for _, desc := range r.passthroughs {
		logger.Info("WARNING: passthrough — request bypassed mock layer: %s", desc)
	}
	if strict {
		return fmt.Errorf("%d request(s) bypassed the mock layer (strict_passthrough is enabled); see warnings above", len(r.passthroughs))
	}
	return nil
}

// isMongoReadCommand reports whether the command name (already uppercased) is a MongoDB read operation.
func isMongoReadCommand(cmd string) bool {
	switch cmd {
	case "FIND", "AGGREGATE", "COUNT", "DISTINCT", "LISTCOLLECTIONS", "LISTINDEXES":
		return true
	}
	return false
}

// isMongoWriteCommand reports whether the command name (already uppercased) is a MongoDB write operation.
func isMongoWriteCommand(cmd string) bool {
	switch cmd {
	case "INSERT", "UPDATE", "DELETE", "FINDANDMODIFY", "CREATEINDEXES", "DROP":
		return true
	}
	return false
}
