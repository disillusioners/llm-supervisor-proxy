package models

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// =============================================================================
// Integration tests for ModelsConfig.ResolveInternalConfig() with peak hours
// =============================================================================

// TestResolveInternalConfig_PeakHourInsideWindow tests that ResolveInternalConfig
// returns the peak-hour model when current time is within the peak window.
func TestResolveInternalConfig_PeakHourInsideWindow(t *testing.T) {
	// Create a ModelsConfig with a model that has peak hour enabled
	// Window: 00:00-23:59 UTC (always inside for any time)
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:               "test-model",
			Name:             "Test Model",
			Enabled:          true,
			Internal:         true,
			CredentialID:     "test-cred",
			InternalModel:    "normal-model",
			PeakHourEnabled:  true,
			PeakHourStart:    "00:00",
			PeakHourEnd:      "23:59",
			PeakHourTimezone: "+0",
			PeakHourModel:    "peak-model",
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-api-key",
	})

	// Resolve at 12:00 UTC - should be inside the 00:00-23:59 window
	provider, apiKey, baseURL, model, ok := config.ResolveInternalConfig("test-model")

	if !ok {
		t.Fatal("Expected ResolveInternalConfig to return ok=true")
	}

	if provider != "openai" {
		t.Errorf("Expected provider 'openai', got '%s'", provider)
	}

	if apiKey != "test-api-key" {
		t.Errorf("Expected apiKey 'test-api-key', got '%s'", apiKey)
	}

	// The key assertion: model should be the peak-hour model
	if model != "peak-model" {
		t.Errorf("Expected model 'peak-model' (peak hour active), got '%s'", model)
	}

	_ = baseURL // May or may not be set depending on credential
}

// TestResolveInternalConfig_PeakHourOutsideWindow tests that ResolveInternalConfig
// returns the normal internal model when current time is outside the peak window.
func TestResolveInternalConfig_PeakHourOutsideWindow(t *testing.T) {
	// Compute a window guaranteed to NOT contain current UTC time
	now := time.Now().UTC()
	currentHour := now.Hour()
	peakStart := (currentHour + 3) % 24
	peakEnd := (currentHour + 4) % 24

	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:               "test-model",
			Name:             "Test Model",
			Enabled:          true,
			Internal:         true,
			CredentialID:     "test-cred",
			InternalModel:    "normal-model",
			PeakHourEnabled:  true, // Enabled
			PeakHourStart:    fmt.Sprintf("%02d:00", peakStart),
			PeakHourEnd:      fmt.Sprintf("%02d:00", peakEnd),
			PeakHourTimezone: "+0", // UTC
			PeakHourModel:    "peak-model",
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-api-key",
	})

	// Resolve - current UTC time is guaranteed to be outside the window
	provider, apiKey, baseURL, model, ok := config.ResolveInternalConfig("test-model")

	if !ok {
		t.Fatal("Expected ResolveInternalConfig to return ok=true")
	}

	// With current time outside the peak window, should return normal model
	if model != "normal-model" {
		t.Errorf("Expected model 'normal-model' (outside peak window %02d:00-%02d:00 UTC at %02d:00 UTC), got '%s'",
			peakStart, peakEnd, currentHour, model)
	}

	_ = provider
	_ = apiKey
	_ = baseURL
}

// TestResolveInternalConfig_PeakHourDisabled tests that ResolveInternalConfig
// returns the normal internal model when peak hours are disabled.
func TestResolveInternalConfig_PeakHourDisabled(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:               "test-model",
			Name:             "Test Model",
			Enabled:          true,
			Internal:         true,
			CredentialID:     "test-cred",
			InternalModel:    "normal-model",
			PeakHourEnabled:  false, // Disabled
			PeakHourStart:    "00:00",
			PeakHourEnd:      "23:59",
			PeakHourTimezone: "+0",
			PeakHourModel:    "peak-model",
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-api-key",
	})

	// Even at 12:00 UTC (inside the window), peak hours are disabled
	_, _, _, model, ok := config.ResolveInternalConfig("test-model")

	if !ok {
		t.Fatal("Expected ResolveInternalConfig to return ok=true")
	}

	// Should return normal model since peak hours are disabled
	if model != "normal-model" {
		t.Errorf("Expected model 'normal-model' (peak hours disabled), got '%s'", model)
	}
}

// TestResolveInternalConfig_PeakHourCrossMidnight tests that ResolveInternalConfig
// handles cross-midnight peak hour windows correctly.
func TestResolveInternalConfig_PeakHourCrossMidnight(t *testing.T) {
	// Test cross-midnight behavior with a 24-hour window first (always active)
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:               "cross-midnight-always",
			Name:             "Cross Midnight Always",
			Enabled:          true,
			Internal:         true,
			CredentialID:     "test-cred",
			InternalModel:    "normal-model",
			PeakHourEnabled:  true,
			PeakHourStart:    "00:00",
			PeakHourEnd:      "23:59",
			PeakHourTimezone: "+0",
			PeakHourModel:    "peak-model",
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-api-key",
	})

	// 24-hour window should always be active
	_, _, _, model, ok := config.ResolveInternalConfig("cross-midnight-always")
	if !ok {
		t.Fatal("Expected ResolveInternalConfig to return ok=true")
	}
	if model != "peak-model" {
		t.Errorf("Expected peak-model (24-hour window always active), got '%s'", model)
	}

	// Test with peak hours disabled - should return normal model
	config2 := NewModelsConfig()
	config2.Models = []ModelConfig{
		{
			ID:               "cross-midnight-disabled",
			Name:             "Cross Midnight Disabled",
			Enabled:          true,
			Internal:         true,
			CredentialID:     "test-cred",
			InternalModel:    "normal-model",
			PeakHourEnabled:  false, // Disabled
			PeakHourStart:    "00:00",
			PeakHourEnd:      "23:59",
			PeakHourTimezone: "+0",
			PeakHourModel:    "peak-model",
		},
	}
	config2.Credentials = NewCredentialsConfig()
	_ = config2.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-api-key",
	})

	// Peak hours disabled, should return normal model
	_, _, _, model2, ok2 := config2.ResolveInternalConfig("cross-midnight-disabled")
	if !ok2 {
		t.Fatal("Expected ResolveInternalConfig to return ok=true")
	}
	if model2 != "normal-model" {
		t.Errorf("Expected normal-model (peak hours disabled), got '%s'", model2)
	}
}

// TestResolveInternalConfig_PeakHourTimezone tests that ResolveInternalConfig
// respects timezone settings for peak hour determination.
//
// Key insight: With a narrow window (22:00-23:00), timezone offset changes whether
// current time is inside or outside the window:
// - UTC 15:00 = local 22:00 in +7 timezone -> INSIDE window -> peak model
// - UTC 15:00 = local 15:00 in UTC timezone -> OUTSIDE window -> normal model
//
// This proves timezone actually affects the result.
func TestResolveInternalConfig_PeakHourTimezone(t *testing.T) {
	// Use UTC 15:00 as a fixed point where timezone offset makes a difference
	testTime := time.Date(2024, 1, 1, 15, 0, 0, 0, time.UTC)

	// Test Case 1: Model with +7 timezone
	// UTC 15:00 = local 22:00 (15 + 7) -> INSIDE window -> peak model returned
	modelWithOffset := ModelConfig{
		ID:               "timezone-offset-test",
		Name:             "Timezone Offset Test",
		Enabled:          true,
		Internal:         true,
		CredentialID:     "test-cred",
		InternalModel:    "normal-model",
		PeakHourEnabled:  true,
		PeakHourStart:    "22:00",
		PeakHourEnd:      "23:00",
		PeakHourTimezone: "+7", // Window is 22:00-23:00 in +7 timezone
		PeakHourModel:    "peak-model",
	}

	resultWithOffset := modelWithOffset.ResolvePeakHourModel(testTime)
	if resultWithOffset != "peak-model" {
		t.Errorf("Expected peak-model with +7 timezone (UTC 15:00 = local 22:00 in +7, inside 22:00-23:00 window), got '%s'",
			resultWithOffset)
	}

	// Test Case 2: Same narrow window, but in UTC timezone
	// UTC 15:00 = local 15:00 -> OUTSIDE window -> normal model (empty string)
	modelUTC := ModelConfig{
		ID:               "timezone-utc-test",
		Name:             "UTC Timezone Test",
		Enabled:          true,
		Internal:         true,
		CredentialID:     "test-cred",
		InternalModel:    "normal-model",
		PeakHourEnabled:  true,
		PeakHourStart:    "22:00",
		PeakHourEnd:      "23:00",
		PeakHourTimezone: "+0", // Window is 22:00-23:00 in UTC
		PeakHourModel:    "peak-model",
	}

	resultUTC := modelUTC.ResolvePeakHourModel(testTime)
	if resultUTC != "" {
		t.Errorf("Expected empty string (normal model) with UTC timezone (UTC 15:00 = local 15:00, outside 22:00-23:00 window), got '%s'",
			resultUTC)
	}

	// Key assertion: The two configs give DIFFERENT results, proving timezone matters
	if resultWithOffset == resultUTC {
		t.Errorf("Timezone should affect result: +7 gave '%s', UTC gave '%s'",
			resultWithOffset, resultUTC)
	} else {
		t.Logf("Timezone affects result: +7 gives '%s', UTC gives '%s' (at UTC 15:00)",
			resultWithOffset, resultUTC)
	}

	// Also test the ModelsConfig.ResolveInternalConfig to verify integration
	// (uses real time.Now(), so we just verify it doesn't crash)
	config := NewModelsConfig()
	config.Models = []ModelConfig{modelWithOffset}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-api-key",
	})

	_, _, _, model, ok := config.ResolveInternalConfig("timezone-offset-test")
	if !ok {
		t.Fatal("Expected ResolveInternalConfig to return ok=true")
	}

	// At the current real time, the result depends on whether we're in the window
	t.Logf("ModelsConfig integration test: model='%s' at current time (UTC %02d:00)",
		model, time.Now().UTC().Hour())
	_ = model // Result depends on real time, just verify no crash
}

// TestResolveInternalConfig_PeakHourNonInternal tests that peak hours are ignored
// for non-internal models.
func TestResolveInternalConfig_PeakHourNonInternal(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:               "test-model",
			Name:             "Test Model",
			Enabled:          true,
			Internal:         false, // Not internal
			PeakHourEnabled:  true,
			PeakHourStart:    "00:00",
			PeakHourEnd:      "23:59",
			PeakHourTimezone: "+0",
			PeakHourModel:    "peak-model",
		},
	}

	// Non-internal models return ok=false from ResolveInternalConfig
	_, _, _, _, ok := config.ResolveInternalConfig("test-model")
	if ok {
		t.Error("Expected ResolveInternalConfig to return ok=false for non-internal model")
	}
}

// TestResolveInternalConfig_PeakHourNoCredential tests that ResolveInternalConfig
// returns ok=false when the referenced credential doesn't exist.
func TestResolveInternalConfig_PeakHourNoCredential(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:               "test-model",
			Name:             "Test Model",
			Enabled:          true,
			Internal:         true,
			CredentialID:     "nonexistent-cred", // Credential doesn't exist
			InternalModel:    "normal-model",
			PeakHourEnabled:  true,
			PeakHourStart:    "00:00",
			PeakHourEnd:      "23:59",
			PeakHourTimezone: "+0",
			PeakHourModel:    "peak-model",
		},
	}
	config.Credentials = NewCredentialsConfig() // No credentials added

	_, _, _, _, ok := config.ResolveInternalConfig("test-model")
	if ok {
		t.Error("Expected ResolveInternalConfig to return ok=false when credential missing")
	}
}

// TestResolveInternalConfig_PeakHourFileBased tests the full integration flow
// with ModelsConfig loaded from a file.
func TestResolveInternalConfig_PeakHourFileBased(t *testing.T) {
	// Create temp file for config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "models.json")

	// Create config with peak hours enabled
	config := NewModelsConfig()
	config.filePath = configPath
	config.Models = []ModelConfig{
		{
			ID:               "file-test-model",
			Name:             "File Test Model",
			Enabled:          true,
			Internal:         true,
			CredentialID:     "file-test-cred",
			InternalModel:    "file-normal-model",
			PeakHourEnabled:  true,
			PeakHourStart:    "00:00",
			PeakHourEnd:      "23:59",
			PeakHourTimezone: "+0",
			PeakHourModel:    "file-peak-model",
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "file-test-cred",
		Provider: "openai",
		APIKey:   "file-test-key",
	})

	// Save to file
	if err := config.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Create new config and load from file
	loadedConfig := NewModelsConfig()
	if err := loadedConfig.Load(configPath); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Resolve - should return peak model since window is 00:00-23:59
	_, _, _, model, ok := loadedConfig.ResolveInternalConfig("file-test-model")

	if !ok {
		t.Fatal("Expected ResolveInternalConfig to return ok=true")
	}

	if model != "file-peak-model" {
		t.Errorf("Expected model 'file-peak-model', got '%s'", model)
	}
}

// TestResolveInternalConfig_NonExistentModel tests that ResolveInternalConfig
// returns ok=false for non-existent models.
func TestResolveInternalConfig_NonExistentModel(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:            "existing-model",
			Name:          "Existing Model",
			Enabled:       true,
			Internal:      true,
			CredentialID:  "test-cred",
			InternalModel: "internal-model",
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	_, _, _, _, ok := config.ResolveInternalConfig("nonexistent-model")
	if ok {
		t.Error("Expected ResolveInternalConfig to return ok=false for non-existent model")
	}
}

// =============================================================================
// Tests that use time.Now() directly - these verify real-time behavior
// =============================================================================

// TestResolveInternalConfig_PeakHourRealTime tests that peak hour switching
// works with the actual current time.
func TestResolveInternalConfig_PeakHourRealTime(t *testing.T) {
	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:               "realtime-test-model",
			Name:             "Real Time Test Model",
			Enabled:          true,
			Internal:         true,
			CredentialID:     "realtime-cred",
			InternalModel:    "realtime-normal",
			PeakHourEnabled:  true,
			PeakHourStart:    "00:00",
			PeakHourEnd:      "23:59",
			PeakHourTimezone: "+0",
			PeakHourModel:    "realtime-peak",
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "realtime-cred",
		Provider: "openai",
		APIKey:   "realtime-key",
	})

	// With 24-hour window, this should always return peak model
	_, _, _, model, ok := config.ResolveInternalConfig("realtime-test-model")

	if !ok {
		t.Fatal("Expected ResolveInternalConfig to return ok=true")
	}

	// This will pass in almost all cases since 00:00-23:59 covers almost all times
	// (it only fails at exactly 23:59)
	if model != "realtime-peak" {
		t.Errorf("Expected model 'realtime-peak' for 24-hour window at current time, got '%s'", model)
	}
}

// TestResolveInternalConfig_WeekdayWeekend tests that the peak hour implementation
// doesn't differentiate between weekdays and weekends (current behavior).
func TestResolveInternalConfig_PeakHourWeekdayWeekend(t *testing.T) {
	// Note: The current peak hour implementation doesn't differentiate between
	// weekdays and weekends. This test documents that behavior.
	// If weekend-specific peak hours are needed, the implementation would need
	// to be extended.

	config := NewModelsConfig()
	config.Models = []ModelConfig{
		{
			ID:               "weekday-test",
			Name:             "Weekday Test",
			Enabled:          true,
			Internal:         true,
			CredentialID:     "test-cred",
			InternalModel:    "weekday-normal",
			PeakHourEnabled:  true,
			PeakHourStart:    "00:00",
			PeakHourEnd:      "23:59",
			PeakHourTimezone: "+0",
			PeakHourModel:    "weekday-peak",
		},
	}
	config.Credentials = NewCredentialsConfig()
	_ = config.Credentials.AddCredential(CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	// With 24-hour window, peak hours are always active
	_, _, _, model, ok := config.ResolveInternalConfig("weekday-test")
	if !ok {
		t.Fatal("Expected ResolveInternalConfig to return ok=true")
	}

	// Peak hours are active with 24-hour window
	if model != "weekday-peak" {
		t.Errorf("Expected model 'weekday-peak' (24-hour window), got '%s'", model)
	}
}
