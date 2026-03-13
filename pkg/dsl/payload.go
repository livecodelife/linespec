package dsl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/livecodelife/linespec/pkg/interpolate"
	"gopkg.in/yaml.v3"
)

type PayloadLoader struct {
	BaseDir  string
	Resolver *interpolate.Resolver
}

func NewPayloadLoader(baseDir string) *PayloadLoader {
	return &PayloadLoader{BaseDir: baseDir}
}

// NewPayloadLoaderWithResolver creates a PayloadLoader with a specific resolver for variable substitution
func NewPayloadLoaderWithResolver(baseDir string, resolver *interpolate.Resolver) *PayloadLoader {
	return &PayloadLoader{BaseDir: baseDir, Resolver: resolver}
}

func (l *PayloadLoader) Load(filePath string) (interface{}, error) {
	fullPath := filepath.Join(l.BaseDir, filePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read payload file %s: %v", fullPath, err)
	}

	ext := filepath.Ext(filePath)
	var result interface{}

	switch ext {
	case ".json":
		// First, resolve any variables in the raw JSON string
		content := string(data)
		if l.Resolver != nil && interpolate.HasVariables(content) {
			content = l.Resolver.Resolve(content)
		}
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON payload %s: %v", fullPath, err)
		}
	case ".yaml", ".yml":
		// First, resolve any variables in the raw YAML string
		content := string(data)
		if l.Resolver != nil && interpolate.HasVariables(content) {
			content = l.Resolver.Resolve(content)
		}
		if err := yaml.Unmarshal([]byte(content), &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal YAML payload %s: %v", fullPath, err)
		}
	default:
		// Return raw string for unknown types (like .proto potentially, or just raw text)
		content := string(data)
		if l.Resolver != nil && interpolate.HasVariables(content) {
			content = l.Resolver.Resolve(content)
		}
		return content, nil
	}

	return result, nil
}
