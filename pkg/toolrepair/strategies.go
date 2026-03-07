package toolrepair

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"

	"github.com/kaptinlin/jsonrepair"
)

// Pre-compiled reasoning patterns (compiled once using sync.Once)
var (
	reasoningPatterns     []*regexp.Regexp
	reasoningPatternsOnce sync.Once
)

// initReasoningPatterns compiles the reasoning patterns once using sync.Once
func initReasoningPatterns() {
	patterns := []string{
		"(?i)Summary:.*",
		"(?i)Approach:.*",
		"(?i)Recommended:.*",
		"(?i)Let me.*",
		"(?i)I'll.*",
		"(?i)First,.*",
		"(?i)Next,.*",
		"(?i)Finally,.*",
	}
	reasoningPatterns = make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		reasoningPatterns[i] = regexp.MustCompile(p)
	}
}

// Strategy is a function that attempts to repair malformed JSON
type Strategy func(string) (string, error)

// getStrategy returns a repair strategy by name
func getStrategy(name string) Strategy {
	switch name {
	case "extract_json":
		return extractJSONBlock
	case "library_repair":
		return libraryRepair
	case "remove_reasoning":
		return removeReasoningLeakage
	default:
		return nil
	}
}

// extractJSONBlock extracts JSON from mixed content (removes surrounding text)
func extractJSONBlock(input string) (string, error) {
	input = strings.TrimSpace(input)

	// If already valid, return as-is
	if isValidJSON(input) {
		return input, nil
	}

	// Try to find JSON object
	objStart := strings.Index(input, "{")
	objEnd := strings.LastIndex(input, "}")

	if objStart >= 0 && objEnd > objStart {
		extracted := input[objStart : objEnd+1]
		if isValidJSON(extracted) {
			return extracted, nil
		}
	}

	// Try to find JSON array
	arrStart := strings.Index(input, "[")
	arrEnd := strings.LastIndex(input, "]")

	if arrStart >= 0 && arrEnd > arrStart {
		extracted := input[arrStart : arrEnd+1]
		if isValidJSON(extracted) {
			return extracted, nil
		}
	}

	// Return original if we can't extract
	return input, nil
}

// libraryRepair uses the jsonrepair library to fix common JSON issues
func libraryRepair(input string) (string, error) {
	repaired, err := jsonrepair.Repair(input)
	if err != nil {
		return input, err
	}
	return repaired, nil
}

// removeReasoningLeakage removes common reasoning patterns from tool arguments
func removeReasoningLeakage(input string) (string, error) {
	// Initialize reasoning patterns on first use (lazy compilation)
	reasoningPatternsOnce.Do(initReasoningPatterns)

	result := input
	for _, re := range reasoningPatterns {
		result = re.ReplaceAllString(result, "")
	}

	result = strings.TrimSpace(result)

	// If the result is still invalid, return original
	if !isValidJSON(result) {
		return input, nil
	}

	return result, nil
}

// validateBasicSchema performs basic schema validation
// This checks if required fields are present but doesn't validate against a full schema
func validateBasicSchema(input string, schema map[string]interface{}) error {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(input), &data); err != nil {
		return err
	}

	// Check for required fields if specified
	if required, ok := schema["required"].([]string); ok {
		for _, field := range required {
			if _, exists := data[field]; !exists {
				return json.Unmarshal([]byte(input), &data)
			}
		}
	}

	return nil
}
