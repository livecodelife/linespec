package dsl

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/livecodelife/linespec/pkg/interpolate"
	"gopkg.in/yaml.v3"
)

// PayloadParser defines the interface for parsing different payload formats
type PayloadParser interface {
	CanParse(extension string) bool
	Parse(data []byte) (interface{}, error)
}

// JSONParser implements PayloadParser for JSON files
type JSONParser struct{}

func (p *JSONParser) CanParse(ext string) bool {
	return ext == ".json"
}

func (p *JSONParser) Parse(data []byte) (interface{}, error) {
	var result interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}
	return result, nil
}

// YAMLParser implements PayloadParser for YAML files
type YAMLParser struct{}

func (p *YAMLParser) CanParse(ext string) bool {
	return ext == ".yaml" || ext == ".yml"
}

func (p *YAMLParser) Parse(data []byte) (interface{}, error) {
	var result interface{}
	if err := yaml.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}
	return result, nil
}

// XMLParser implements PayloadParser for XML files
type XMLParser struct{}

func (p *XMLParser) CanParse(ext string) bool {
	return ext == ".xml"
}

func (p *XMLParser) Parse(data []byte) (interface{}, error) {
	var result interface{}
	if err := xml.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse XML: %w", err)
	}
	return result, nil
}

// RawParser implements PayloadParser for raw/unknown file types
type RawParser struct{}

func (p *RawParser) CanParse(ext string) bool {
	return true // Catch-all for unknown extensions
}

func (p *RawParser) Parse(data []byte) (interface{}, error) {
	return string(data), nil
}

// PayloadLoader loads and parses payload files
type PayloadLoader struct {
	BaseDir  string
	Resolver *interpolate.Resolver
	Parsers  []PayloadParser
}

func NewPayloadLoader(baseDir string) *PayloadLoader {
	return &PayloadLoader{
		BaseDir: baseDir,
		Parsers: []PayloadParser{
			&JSONParser{},
			&YAMLParser{},
			&XMLParser{},
			&RawParser{}, // Must be last as catch-all
		},
	}
}

// NewPayloadLoaderWithResolver creates a PayloadLoader with a specific resolver for variable substitution
func NewPayloadLoaderWithResolver(baseDir string, resolver *interpolate.Resolver) *PayloadLoader {
	loader := NewPayloadLoader(baseDir)
	loader.Resolver = resolver
	return loader
}

// SetParsers allows customizing the list of parsers
func (l *PayloadLoader) SetParsers(parsers []PayloadParser) {
	l.Parsers = parsers
}

// Load loads and parses a payload file
func (l *PayloadLoader) Load(filePath string) (interface{}, error) {
	fullPath := filepath.Join(l.BaseDir, filePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read payload file %s: %v", fullPath, err)
	}

	ext := strings.ToLower(filepath.Ext(filePath))

	// Apply variable resolution if resolver is configured
	content := data
	if l.Resolver != nil {
		contentStr := string(data)
		if interpolate.HasVariables(contentStr) {
			resolved := l.Resolver.Resolve(contentStr)
			content = []byte(resolved)
		}
	}

	// Find appropriate parser
	for _, parser := range l.Parsers {
		if parser.CanParse(ext) {
			return parser.Parse(content)
		}
	}

	// Should never reach here due to RawParser catch-all
	return string(content), nil
}
