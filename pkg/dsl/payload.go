package dsl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type PayloadLoader struct {
	BaseDir string
}

func NewPayloadLoader(baseDir string) *PayloadLoader {
	return &PayloadLoader{BaseDir: baseDir}
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
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON payload %s: %v", fullPath, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal YAML payload %s: %v", fullPath, err)
		}
	default:
		// Return raw string for unknown types (like .proto potentially, or just raw text)
		return string(data), nil
	}

	return result, nil
}
