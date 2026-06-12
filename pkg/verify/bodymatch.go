package verify

import (
	"encoding/json"

	"github.com/livecodelife/linespec/pkg/dsl"
	"github.com/livecodelife/linespec/pkg/interpolate"
	"github.com/livecodelife/linespec/pkg/logger"
)

// MakeJSONBodyMatcher returns a registry body-match callback shared by the HTTP
// and Kafka interceptors. When the mock has no WithFile the callback always
// returns true (match-any). Otherwise it loads the expected payload from
// withFile, parses actualBody as JSON, and delegates to CompareJSON. sourceLabel
// names the body source in the parse-failure debug log (e.g. "request body" or
// "Kafka message value").
func MakeJSONBodyMatcher(actualBody, sourceLabel string, resolver *interpolate.Resolver) func(withFile, baseDir string) bool {
	return func(withFile, baseDir string) bool {
		if withFile == "" {
			return true
		}
		loader := dsl.NewPayloadLoaderWithResolver(baseDir, resolver)
		expected, err := loader.Load(withFile)
		if err != nil {
			logger.Debug("WITH body match: failed to load %s: %v", withFile, err)
			return false
		}
		var actual interface{}
		if jsonErr := json.Unmarshal([]byte(actualBody), &actual); jsonErr != nil {
			logger.Debug("WITH body match: failed to parse %s as JSON: %v", sourceLabel, jsonErr)
			return false
		}
		return CompareJSON(expected, actual) == nil
	}
}
