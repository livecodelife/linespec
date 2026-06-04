package base

import (
	"github.com/livecodelife/linespec/pkg/registry"
)

// Proxy manages database proxy connections using the MySQL pattern:
// 1. Connect to upstream FIRST
// 2. Start goroutine piping upstream->client transparently (includes startup)
// 3. In main goroutine, read client->upstream with selective interception
type Proxy struct {
	Registry      *registry.MockRegistry
	IsWhitelisted func(query string) bool
	ExtractTable  func(query string) string
}

