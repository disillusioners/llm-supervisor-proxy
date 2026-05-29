package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// =============================================================================
// Unit tests for ModelConfig.ExcludeFromUltimateSwitching JSON serialization
// =============================================================================

// TestModelConfig_ExcludeFromUltimateSwitching_JSONRoundTrip tests that the
// ExcludeFromUltimateSwitching field is correctly serialized and deserialized.
func TestModelConfig_ExcludeFromUltimateSwitching_JSONRoundTrip(t *testing.T) {
	// Create a ModelConfig with ExcludeFromUltimateSwitching = true
	original := ModelConfig{
		ID:                          "test-model",
		Name:                        "Test Model",
		Enabled:                     true,
		ExcludeFromUltimateSwitching: true,
	}

	// Marshal to JSON
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Verify the JSON contains the expected field
	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}

	// Check that exclude_from_ultimate_switching is present and true
	if val, ok := jsonMap["exclude_from_ultimate_switching"]; !ok {
		t.Error("JSON should contain 'exclude_from_ultimate_switching' field")
	} else if val != true {
		t.Errorf("exclude_from_ultimate_switching should be true, got %v", val)
	}

	// Unmarshal back
	var restored ModelConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify the field is preserved
	if !restored.ExcludeFromUltimateSwitching {
		t.Error("ExcludeFromUltimateSwitching should be true after round-trip")
	}
}

// TestModelConfig_ExcludeFromUltimateSwitching_DefaultFalse tests that when
// the field is not set, it defaults to false.
func TestModelConfig_ExcludeFromUltimateSwitching_DefaultFalse(t *testing.T) {
	// Create a ModelConfig without ExcludeFromUltimateSwitching set
	original := ModelConfig{
		ID:      "test-model",
		Name:    "Test Model",
		Enabled: true,
	}

	// Verify the default is false
	if original.ExcludeFromUltimateSwitching != false {
		t.Errorf("Default ExcludeFromUltimateSwitching should be false, got %v", original.ExcludeFromUltimateSwitching)
	}

	// Marshal to JSON
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Verify the JSON does NOT contain the field (omitempty)
	var jsonMap map[string]interface{}
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}

	// Check that exclude_from_ultimate_switching is NOT present (omitted)
	if val, ok := jsonMap["exclude_from_ultimate_switching"]; ok {
		t.Errorf("JSON should NOT contain 'exclude_from_ultimate_switching' field when false, got %v", val)
	}

	// Unmarshal from JSON that doesn't have the field
	jsonWithoutField := `{"id":"test-model","name":"Test Model","enabled":true}`
	var restored ModelConfig
	if err := json.Unmarshal([]byte(jsonWithoutField), &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify the default is false
	if restored.ExcludeFromUltimateSwitching != false {
		t.Error("ExcludeFromUltimateSwitching should be false when not present in JSON")
	}
}

// TestModelConfig_ExcludeFromUltimateSwitching_JSONFieldMatches tests that the JSON
// field name matches the expected snake_case format.
func TestModelConfig_ExcludeFromUltimateSwitching_JSONFieldMatches(t *testing.T) {
	original := ModelConfig{
		ID:                          "test-model",
		Name:                        "Test Model",
		Enabled:                     true,
		ExcludeFromUltimateSwitching: true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Check that the JSON field name uses snake_case (exclude_from_ultimate_switching)
	// not camelCase (excludeFromUltimateSwitching)
	jsonStr := string(data)
	expectedField := "exclude_from_ultimate_switching"
	if !strings.Contains(jsonStr, expectedField) {
		t.Errorf("JSON should contain snake_case field '%s', got: %s", expectedField, jsonStr)
	}

	// Ensure camelCase is NOT used
	unexpectedField := "excludeFromUltimateSwitching"
	if strings.Contains(jsonStr, unexpectedField) {
		t.Errorf("JSON should NOT contain camelCase field '%s', got: %s", unexpectedField, jsonStr)
	}
}

// TestModelConfig_ExcludeFromUltimateSwitching_WithOtherFields tests that the field
// serializes correctly when combined with other optional fields.
func TestModelConfig_ExcludeFromUltimateSwitching_WithOtherFields(t *testing.T) {
	original := ModelConfig{
		ID:                          "test-model",
		Name:                        "Test Model",
		Enabled:                     true,
		FallbackChain:               []string{"fallback-1"},
		ExcludeFromUltimateSwitching: true,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var restored ModelConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if !restored.ExcludeFromUltimateSwitching {
		t.Error("ExcludeFromUltimateSwitching should be preserved with other fields")
	}

	if len(restored.FallbackChain) != 1 || restored.FallbackChain[0] != "fallback-1" {
		t.Errorf("FallbackChain not preserved correctly, got: %v", restored.FallbackChain)
	}
}

// TestModelConfig_ExcludeFromUltimateSwitching_ExplicitFalse tests that explicitly
// setting the field to false results in omitempty behavior (field not in JSON).
func TestModelConfig_ExcludeFromUltimateSwitching_ExplicitFalse(t *testing.T) {
	original := ModelConfig{
		ID:                          "test-model",
		Name:                        "Test Model",
		Enabled:                     true,
		ExcludeFromUltimateSwitching: false, // Explicitly set to false
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// When false and omitempty, the field should be omitted from JSON
	jsonStr := string(data)
	if strings.Contains(jsonStr, "exclude_from_ultimate_switching") {
		t.Errorf("False value with omitempty should be omitted from JSON, got: %s", jsonStr)
	}

	// But it should still deserialize correctly when present in JSON
	jsonWithField := `{"id":"test","name":"Test","enabled":true,"exclude_from_ultimate_switching":false}`
	var restored ModelConfig
	if err := json.Unmarshal([]byte(jsonWithField), &restored); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if restored.ExcludeFromUltimateSwitching != false {
		t.Error("Explicit false value should deserialize correctly")
	}
}
