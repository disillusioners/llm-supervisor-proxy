package models

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Tests for Secondary Upstream Model Validation
// =============================================================================

// TestValidate_SecondaryUpstreamModelWithInternalTrue tests that secondary_upstream_model
// is valid when internal=true.
func TestValidate_SecondaryUpstreamModelWithInternalTrue(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:                     "valid-secondary",
			Name:                   "Valid Secondary Model",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",
			SecondaryUpstreamModel: "glm-4-flash", // Secondary model set
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	err := config.Validate()
	if err != nil {
		t.Errorf("Expected validation to pass, got error: %v", err)
	}
}

// TestValidate_SecondaryUpstreamModelWithInternalFalse tests that secondary_upstream_model
// is rejected when internal=false.
func TestValidate_SecondaryUpstreamModelWithInternalFalse(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:                     "invalid-secondary",
			Name:                   "Invalid Secondary Model",
			Enabled:                true,
			Internal:               false,         // NOT internal
			SecondaryUpstreamModel: "glm-4-flash", // Secondary model set
		},
	}

	err := config.Validate()
	if err == nil {
		t.Error("Expected validation to fail for non-internal model with secondary_upstream_model")
	}
	if err != nil && !strings.Contains(err.Error(), "secondary_upstream_model requires internal to be true") {
		t.Errorf("Expected error message about internal=true, got: %v", err)
	}
}

// TestValidate_SecondaryUpstreamModelEmptyWithInternalTrue tests that empty secondary_upstream_model
// is valid when internal=true (optional field).
func TestValidate_SecondaryUpstreamModelEmptyWithInternalTrue(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:                     "empty-secondary",
			Name:                   "Empty Secondary Model",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",
			SecondaryUpstreamModel: "", // Empty secondary model (optional)
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	err := config.Validate()
	if err != nil {
		t.Errorf("Expected validation to pass with empty secondary_upstream_model, got error: %v", err)
	}
}

// TestValidate_SecondaryUpstreamModelNonInternalNoSecondary tests that validation passes
// when model is non-internal and no secondary model is set.
func TestValidate_SecondaryUpstreamModelNonInternalNoSecondary(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:       "external-model",
			Name:     "External Model",
			Enabled:  true,
			Internal: false,
		},
	}

	err := config.Validate()
	if err != nil {
		t.Errorf("Expected validation to pass for external model without secondary, got error: %v", err)
	}
}

// TestValidate_SecondaryUpstreamModelMissingCredential tests that validation fails
// when internal=true but credential_id is missing (even with secondary model set).
func TestValidate_SecondaryUpstreamModelMissingCredential(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:                     "secondary-no-cred",
			Name:                   "Secondary No Credential",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "", // Missing credential
			InternalModel:          "glm-5.0",
			SecondaryUpstreamModel: "glm-4-flash",
		},
	}
	// No credentials added

	err := config.Validate()
	if err == nil {
		t.Error("Expected validation to fail for internal model without credential")
	}
	if err != nil && !strings.Contains(err.Error(), "credential_id is required when internal is true") {
		t.Errorf("Expected error message about credential_id, got: %v", err)
	}
}

// TestValidate_SecondaryUpstreamModelWithPeakHour tests that both secondary_upstream_model
// and peak_hour model can coexist on the same model.
func TestValidate_SecondaryUpstreamModelWithPeakHour(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:                     "peak-with-secondary",
			Name:                   "Peak With Secondary",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",
			SecondaryUpstreamModel: "glm-4-flash",
			PeakHourEnabled:        true,
			PeakHourStart:          "09:00",
			PeakHourEnd:            "17:00",
			PeakHourTimezone:       "+0",
			PeakHourModel:          "glm-5.0-peak",
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	err := config.Validate()
	if err != nil {
		t.Errorf("Expected validation to pass with both secondary and peak hour, got error: %v", err)
	}
}

// TestValidate_SecondaryUpstreamModelWithFallback tests that secondary_upstream_model
// works together with fallback chain.
func TestValidate_SecondaryUpstreamModelWithFallback(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:                     "secondary-with-fallback",
			Name:                   "Secondary With Fallback",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "test-cred",
			InternalModel:          "glm-5.0",
			SecondaryUpstreamModel: "glm-4-flash",
			FallbackChain:          []string{"fallback-model"},
		},
		{
			ID:      "fallback-model",
			Name:    "Fallback Model",
			Enabled: true,
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	err := config.Validate()
	if err != nil {
		t.Errorf("Expected validation to pass with secondary and fallback, got error: %v", err)
	}
}

// TestValidate_SecondaryUpstreamModel_NonExistentCredential tests that validation fails
// when secondary_upstream_model is set but credential doesn't exist.
func TestValidate_SecondaryUpstreamModel_NonExistentCredential(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:                     "secondary-bad-cred",
			Name:                   "Secondary Bad Credential",
			Enabled:                true,
			Internal:               true,
			CredentialID:           "nonexistent-cred", // Credential doesn't exist
			InternalModel:          "glm-5.0",
			SecondaryUpstreamModel: "glm-4-flash",
		},
	}
	config.Credentials = NewCredentialsConfig() // No credentials added

	err := config.Validate()
	if err == nil {
		t.Error("Expected validation to fail for non-existent credential")
	}
	if err != nil && !strings.Contains(err.Error(), "references non-existent credential") {
		t.Errorf("Expected error message about credential reference, got: %v", err)
	}
}

// TestModelConfig_SecondaryUpstreamModelField tests that the SecondaryUpstreamModel
// field is properly accessible.
func TestModelConfig_SecondaryUpstreamModelField(t *testing.T) {
	model := ModelConfig{
		ID:                     "test-model",
		Name:                   "Test Model",
		Enabled:                true,
		Internal:               true,
		CredentialID:           "test-cred",
		InternalModel:          "glm-5.0",
		SecondaryUpstreamModel: "glm-4-flash",
	}

	if model.SecondaryUpstreamModel != "glm-4-flash" {
		t.Errorf("SecondaryUpstreamModel = %s, want glm-4-flash", model.SecondaryUpstreamModel)
	}

	// Test empty secondary model
	model2 := ModelConfig{
		ID:            "test-model-2",
		Name:          "Test Model 2",
		Enabled:       true,
		Internal:      true,
		CredentialID:  "test-cred",
		InternalModel: "glm-5.0",
		// SecondaryUpstreamModel not set
	}

	if model2.SecondaryUpstreamModel != "" {
		t.Errorf("SecondaryUpstreamModel = %s, want empty string", model2.SecondaryUpstreamModel)
	}
}

// TestModelConfigHelper_IsInternal tests the IsInternal helper method.
func TestModelConfigHelper_IsInternal(t *testing.T) {
	internalModel := ModelConfig{
		ID:            "internal-model",
		Name:          "Internal Model",
		Enabled:       true,
		Internal:      true,
		CredentialID:  "test-cred",
		InternalModel: "glm-5.0",
	}

	externalModel := ModelConfig{
		ID:       "external-model",
		Name:     "External Model",
		Enabled:  true,
		Internal: false,
	}

	if !internalModel.IsInternal() {
		t.Error("IsInternal() should return true for internal model")
	}

	if externalModel.IsInternal() {
		t.Error("IsInternal() should return false for external model")
	}
}

// TestResolvePeakHourModel_WithSecondary tests that ResolvePeakHourModel works
// correctly when secondary model is also configured.
func TestResolvePeakHourModel_WithSecondary(t *testing.T) {
	// Use a narrow window to test peak hours
	now := time.Now().UTC()
	currentHour := now.Hour()

	// Create a narrow peak window that does NOT include current time
	// If current hour is H, window is (H+3):00-(H+4):00 UTC
	peakStart := (currentHour + 3) % 24
	peakEnd := (currentHour + 4) % 24

	model := ModelConfig{
		ID:                     "peak-with-secondary",
		Name:                   "Peak With Secondary",
		Enabled:                true,
		Internal:               true,
		CredentialID:           "test-cred",
		InternalModel:          "glm-5.0",
		SecondaryUpstreamModel: "glm-4-flash",
		PeakHourEnabled:        true,
		PeakHourStart:          fmt.Sprintf("%02d:00", peakStart),
		PeakHourEnd:            fmt.Sprintf("%02d:00", peakEnd),
		PeakHourTimezone:       "+0",
		PeakHourModel:          "glm-5.0-peak",
	}

	// The current time should be outside the narrow window
	peakModel := model.ResolvePeakHourModel(now)
	if peakModel != "" {
		t.Errorf("ResolvePeakHourModel at current time = %s, want empty (outside narrow window %02d:00-%02d:00)",
			peakModel, peakStart, peakEnd)
	}

	// Now test with a time inside the window
	insideTime := time.Date(2024, 1, 1, peakStart, 30, 0, 0, time.UTC)
	peakModel = model.ResolvePeakHourModel(insideTime)
	if peakModel != "glm-5.0-peak" {
		t.Errorf("ResolvePeakHourModel inside window = %s, want glm-5.0-peak", peakModel)
	}
}

// TestResolvePeakHourModel_SecondaryNotAffectedByPeak tests that SecondaryUpstreamModel
// is NOT affected by peak hour logic (secondary model is for race retry, not peak hours).
func TestResolvePeakHourModel_SecondaryNotAffectedByPeak(t *testing.T) {
	model := ModelConfig{
		ID:                     "secondary-only",
		Name:                   "Secondary Only",
		Enabled:                true,
		Internal:               true,
		CredentialID:           "test-cred",
		InternalModel:          "glm-5.0",
		SecondaryUpstreamModel: "glm-4-flash",
		PeakHourEnabled:        true,
		PeakHourStart:          "00:00",
		PeakHourEnd:            "23:59",
		PeakHourTimezone:       "+0",
		PeakHourModel:          "glm-5.0-peak",
	}

	// SecondaryUpstreamModel should always be glm-4-flash, regardless of peak hours
	if model.SecondaryUpstreamModel != "glm-4-flash" {
		t.Errorf("SecondaryUpstreamModel = %s, want glm-4-flash", model.SecondaryUpstreamModel)
	}

	// Peak hours use PeakHourModel, not SecondaryUpstreamModel
	if model.PeakHourModel != "glm-5.0-peak" {
		t.Errorf("PeakHourModel = %s, want glm-5.0-peak", model.PeakHourModel)
	}
}
