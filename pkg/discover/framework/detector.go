package framework

import (
	"os"
	"path/filepath"
	"strings"
)

// DetectResult holds the outcome of auto-detection.
type DetectResult struct {
	Language  string // "go" or "ruby"
	Framework string // e.g. "chi", "rails", "sinatra"
}

// Detect scans dir for project files and matches them against the detection rules
// in the provided descriptions map. Returns the first match found.
// Returns nil if no framework is detected.
func Detect(dir string, descs map[string]*Description) *DetectResult {
	for _, d := range descs {
		for _, rule := range d.Detection {
			if matchesRule(dir, rule) {
				return &DetectResult{Language: d.Language, Framework: d.Name}
			}
		}
	}
	return nil
}

func matchesRule(dir string, rule DetectionRule) bool {
	path := filepath.Join(dir, rule.Manifest)
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	for _, needle := range rule.Contains {
		if !strings.Contains(content, needle) {
			return false
		}
	}
	return true
}
