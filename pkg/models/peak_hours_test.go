package models

import (
	"strings"
	"testing"
	"time"
)

// =============================================================================
// parseUTCOffset tests
// =============================================================================

func TestParseUTCOffset(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    float64
		wantErr bool
	}{
		// Valid offsets
		{"positive integer", "+7", 7.0, false},
		{"negative integer", "-5", -5.0, false},
		{"positive decimal", "+5.5", 5.5, false},
		{"negative decimal", "-3.75", -3.75, false},
		{"positive no sign", "7", 0, true}, // sign required
		{"zero with sign", "+0", 0.0, false},
		{"empty string", "", 0.0, false},
		{"positive decimal single digit", "+1.5", 1.5, false},

		// Invalid formats
		{"letters", "abc", 0, true},
		{"missing sign", "5", 0, true}, // sign required
		{"colon instead of dot", "+5:30", 0, true},
		{"text after number", "7abc", 0, true},

		// Out of range
		{"too negative", "-13", 0, true},
		{"too positive", "+15", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUTCOffset(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseUTCOffset(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseUTCOffset(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// =============================================================================
// parseTime tests
// =============================================================================

func TestParseTime(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantH   int
		wantM   int
		wantErr bool
	}{
		// Valid times
		{"midnight", "00:00", 0, 0, false},
		{"noon", "12:00", 12, 0, false},
		{"end of day", "23:59", 23, 59, false},
		{"some time", "09:30", 9, 30, false},
		{"evening", "21:45", 21, 45, false},

		// Invalid formats
		{"empty", "", 0, 0, true}, // parseTime returns error for empty
		{"single digit hour", "9:30", 0, 0, true},
		{"no colon", "0930", 0, 0, true},
		{"too many digits", "100:00", 0, 0, true},
		{"missing minute", "09:", 0, 0, true},
		{"invalid hour", "25:00", 0, 0, true},
		{"invalid minute", "12:60", 0, 0, true},
		{"negative hour", "-01:00", 0, 0, true},
		{"with spaces", " 09:00 ", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotH, gotM, err := parseTime(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTime(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && (gotH != tt.wantH || gotM != tt.wantM) {
				t.Errorf("parseTime(%q) = (%d, %d), want (%d, %d)", tt.input, gotH, gotM, tt.wantH, tt.wantM)
			}
		})
	}
}

// =============================================================================
// isWithinWindow tests
// =============================================================================

func TestIsWithinWindow(t *testing.T) {
	tests := []struct {
		name    string
		current int
		start   int
		end     int
		want    bool
	}{
		// Normal window (start < end): 09:00 to 17:00 (540 to 1020 minutes)
		{"normal inside", 720, 540, 1020, true},   // 12:00
		{"normal at start", 540, 540, 1020, true}, // 09:00 (included)
		{"normal at end", 1020, 540, 1020, false}, // 17:00 (excluded)
		{"normal before", 539, 540, 1020, false},  // 08:59
		{"normal after", 1021, 540, 1020, false},  // 17:01

		// Cross-midnight window (start > end): 22:00 to 06:00 (1320 to 360 minutes)
		{"cross-night inside before midnight", 1380, 1320, 360, true}, // 23:00
		{"cross-night inside after midnight", 60, 1320, 360, true},    // 01:00
		{"cross-night at start", 1320, 1320, 360, true},               // 22:00 (included)
		{"cross-night at end", 360, 1320, 360, false},                 // 06:00 (excluded)
		{"cross-night before start", 1319, 1320, 360, false},          // 21:59
		{"cross-night after end", 361, 1320, 360, false},              // 06:01

		// Edge cases
		{"single minute window", 100, 100, 101, true},  // [100, 101)
		{"single minute at end", 101, 100, 101, false}, // 101 not included
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isWithinWindow(tt.current, tt.start, tt.end)
			if got != tt.want {
				t.Errorf("isWithinWindow(%d, %d, %d) = %v, want %v", tt.current, tt.start, tt.end, got, tt.want)
			}
		})
	}
}

// =============================================================================
// validateTimeFormat tests
// =============================================================================

func TestValidateTimeFormat(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid formats
		{"valid midnight", "00:00", false},
		{"valid noon", "12:00", false},
		{"valid end", "23:59", false},
		{"valid various", "09:30", false},
		{"empty (optional)", "", false},

		// Invalid formats
		{"24:00", "24:00", true},
		{"25:00", "25:00", true},
		{"12:60", "12:60", true},
		{"12:99", "12:99", true},
		{"single digit hour", "9:00", true},
		{"missing colon", "0900", true},
		{"extra colon", "09:00:00", true},
		{"no minutes", "09:", true},
		{"negative hour", "-01:00", true},
		{"letters", "ab:cd", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTimeFormat(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTimeFormat(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

// =============================================================================
// validateUTCOffset tests
// =============================================================================

func TestValidateUTCOffset(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid offsets
		{"positive integer", "+7", false},
		{"negative integer", "-5", false},
		{"positive decimal", "+5.5", false},
		{"negative decimal", "-3.75", false},
		{"zero without sign", "0", true}, // sign required
		{"empty", "", false},
		{"positive without sign", "7", true}, // sign required
		{"boundary -12", "-12", false},
		{"boundary +14", "+14", false},
		{"decimal 0.5", "+0.5", false},

		// Invalid formats
		{"out of range -13", "-13", true},
		{"out of range +15", "+15", true},
		{"letters", "abc", true},
		{"colon format", "+5:30", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUTCOffset(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUTCOffset(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

// =============================================================================
// ResolvePeakHourModel tests
// =============================================================================

func TestResolvePeakHourModel(t *testing.T) {
	testTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		m    ModelConfig
		want string
	}{
		// Disabled cases
		{
			name: "disabled peak hours",
			m: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  false,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "peak-model",
			},
			want: "",
		},
		{
			name: "non-internal model",
			m: ModelConfig{
				ID:               "test",
				Internal:         false,
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "peak-model",
			},
			want: "",
		},
		{
			name: "empty peak model",
			m: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "",
			},
			want: "",
		},

		// These test cases with valid configs depend on current time
		// We test that the function returns non-empty when configured correctly
		// and we're within the window (actual window testing is done separately)
		{
			name: "valid config returns peak model when in window",
			m: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "00:00",
				PeakHourEnd:      "23:59",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak-model-24h",
			},
			want: "peak-model-24h", // 24-hour window should always be active
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.m.ResolvePeakHourModel(testTime)
			if got != tt.want {
				t.Errorf("ResolvePeakHourModel(%v) = %v, want %v", testTime, got, tt.want)
			}
		})
	}
}

// TestResolvePeakHourModelWindow tests the window logic with specific test times
func TestResolvePeakHourModelWindow(t *testing.T) {
	tests := []struct {
		name     string
		model    ModelConfig
		testTime time.Time
		want     string
	}{
		// Normal window: 13:00-18:00 UTC (+0)
		{
			name: "normal window inside",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "13:00",
				PeakHourEnd:      "18:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC),
			want:     "peak_model",
		},
		{
			name: "normal window start boundary",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "13:00",
				PeakHourEnd:      "18:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 13, 0, 0, 0, time.UTC),
			want:     "peak_model",
		},
		{
			name: "normal window end boundary",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "13:00",
				PeakHourEnd:      "18:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC),
			want:     "",
		},
		{
			name: "normal window outside",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "13:00",
				PeakHourEnd:      "18:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
			want:     "",
		},

		// Cross-midnight window: 22:00-06:00 UTC (+0)
		{
			name: "cross-midnight inside before midnight",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "22:00",
				PeakHourEnd:      "06:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 23, 0, 0, 0, time.UTC),
			want:     "peak_model",
		},
		{
			name: "cross-midnight inside after midnight",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "22:00",
				PeakHourEnd:      "06:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 3, 0, 0, 0, time.UTC),
			want:     "peak_model",
		},
		{
			name: "cross-midnight start boundary",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "22:00",
				PeakHourEnd:      "06:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 22, 0, 0, 0, time.UTC),
			want:     "peak_model",
		},
		{
			name: "cross-midnight end boundary",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "22:00",
				PeakHourEnd:      "06:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 6, 0, 0, 0, time.UTC),
			want:     "",
		},
		{
			name: "cross-midnight outside",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "22:00",
				PeakHourEnd:      "06:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			want:     "",
		},

		// Timezone tests
		{
			name: "positive timezone +7: 13:00-18:00 local, test at 08:00 UTC",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "13:00",
				PeakHourEnd:      "18:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 8, 0, 0, 0, time.UTC), // 15:00 local
			want:     "peak_model",
		},
		{
			name: "negative timezone -5: 13:00-18:00 local, test at 18:00 UTC",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "13:00",
				PeakHourEnd:      "18:00",
				PeakHourTimezone: "-5",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC), // 13:00 local
			want:     "peak_model",
		},
		{
			name: "fractional timezone +5.5: 13:00-18:00 local, test at 08:30 UTC",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "13:00",
				PeakHourEnd:      "18:00",
				PeakHourTimezone: "+5.5",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 8, 30, 0, 0, time.UTC), // 14:00 local
			want:     "peak_model",
		},

		// Disabled/Invalid cases
		{
			name: "disabled",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  false,
				PeakHourStart:    "13:00",
				PeakHourEnd:      "18:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC),
			want:     "",
		},
		{
			name: "non-internal",
			model: ModelConfig{
				ID:               "test",
				Internal:         false,
				PeakHourEnabled:  true,
				PeakHourStart:    "13:00",
				PeakHourEnd:      "18:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "peak_model",
			},
			testTime: time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC),
			want:     "",
		},
		{
			name: "empty peak model",
			model: ModelConfig{
				ID:               "test",
				Internal:         true,
				PeakHourEnabled:  true,
				PeakHourStart:    "13:00",
				PeakHourEnd:      "18:00",
				PeakHourTimezone: "+0",
				PeakHourModel:    "",
			},
			testTime: time.Date(2024, 1, 15, 15, 0, 0, 0, time.UTC),
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.model.ResolvePeakHourModel(tt.testTime)
			if got != tt.want {
				t.Errorf("ResolvePeakHourModel(%v) = %v, want %v", tt.testTime, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Validate peak hours tests
// =============================================================================

func TestValidatePeakHours(t *testing.T) {
	// Create a valid credential for testing
	validCredID := "valid-cred-id"
	validConfig := &ModelsConfig{
		Models: []ModelConfig{
			{
				ID:       "other-model",
				Name:     "Other Model",
				Enabled:  true,
				Internal: false,
			},
		},
		Credentials: &CredentialsConfig{
			credentials: map[string]CredentialConfig{
				validCredID: {
					ID:       validCredID,
					Provider: "openai",
					APIKey:   "test-key",
				},
			},
		},
	}

	tests := []struct {
		name       string
		model      ModelConfig
		wantErr    bool
		errContain string
	}{
		// Peak hours disabled - no validation required
		{
			name: "disabled - no validation",
			model: ModelConfig{
				ID:              "test",
				Name:            "Test",
				Enabled:         true,
				PeakHourEnabled: false,
			},
			wantErr:    false,
			errContain: "",
		},

		// Missing internal
		{
			name: "missing internal upstream",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         false,
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "peak-model",
			},
			wantErr:    true,
			errContain: "peak_hour_enabled requires internal to be true",
		},

		// Missing required fields
		{
			name: "missing peak_hour_start",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "peak-model",
			},
			wantErr:    true,
			errContain: "peak_hour_start is required",
		},
		{
			name: "missing peak_hour_end",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "",
				PeakHourTimezone: "+7",
				PeakHourModel:    "peak-model",
			},
			wantErr:    true,
			errContain: "peak_hour_end is required",
		},
		{
			name: "missing peak_hour_timezone",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "",
				PeakHourModel:    "peak-model",
			},
			wantErr:    true,
			errContain: "peak_hour_timezone is required",
		},
		{
			name: "missing peak_hour_model",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "",
			},
			wantErr:    true,
			errContain: "peak_hour_model is required",
		},

		// Invalid time formats
		{
			name: "invalid peak_hour_start - 25:00",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "25:00",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "peak-model",
			},
			wantErr:    true,
			errContain: "invalid peak_hour_start",
		},
		{
			name: "invalid peak_hour_end - 24:00",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "24:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "peak-model",
			},
			wantErr:    true,
			errContain: "invalid peak_hour_end",
		},

		// Invalid UTC offset
		{
			name: "invalid UTC offset - out of range",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "+15",
				PeakHourModel:    "peak-model",
			},
			wantErr:    true,
			errContain: "invalid peak_hour_timezone",
		},

		// Same start and end
		{
			name: "same start and end",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "09:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "peak-model",
			},
			wantErr:    true,
			errContain: "peak_hour_start and peak_hour_end cannot be the same",
		},

		// Valid configuration
		{
			name: "valid peak hours config",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "+7",
				PeakHourModel:    "peak-model",
			},
			wantErr:    false,
			errContain: "",
		},
		{
			name: "valid cross-midnight config",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "22:00",
				PeakHourEnd:      "06:00",
				PeakHourTimezone: "-5",
				PeakHourModel:    "overnight-model",
			},
			wantErr:    false,
			errContain: "",
		},
		{
			name: "valid decimal timezone",
			model: ModelConfig{
				ID:               "test",
				Name:             "Test",
				Enabled:          true,
				Internal:         true,
				CredentialID:     validCredID,
				InternalModel:    "test-model",
				PeakHourEnabled:  true,
				PeakHourStart:    "09:00",
				PeakHourEnd:      "17:00",
				PeakHourTimezone: "+5.5",
				PeakHourModel:    "india-model",
			},
			wantErr:    false,
			errContain: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build config with the test model
			config := &ModelsConfig{
				Models:      []ModelConfig{tt.model},
				Credentials: validConfig.Credentials,
			}

			err := config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errContain != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContain) {
					t.Errorf("Validate() error = %v, should contain %q", err, tt.errContain)
				}
			}
		})
	}
}

// =============================================================================
// Integration test with actual time
// =============================================================================

func TestResolvePeakHourModelIntegration(t *testing.T) {
	// This test verifies the integration with actual time.Now()
	// It creates a window that should always be active (00:00-23:59)

	model := ModelConfig{
		ID:               "integration-test",
		Internal:         true,
		PeakHourEnabled:  true,
		PeakHourStart:    "00:00",
		PeakHourEnd:      "23:59",
		PeakHourTimezone: "+0",
		PeakHourModel:    "always-peak",
	}

	got := model.ResolvePeakHourModel(time.Now().UTC())
	if got != "always-peak" {
		t.Errorf("ResolvePeakHourModel() = %v, want always-peak (24-hour window should always be active)", got)
	}
}

// =============================================================================
// Benchmark tests
// =============================================================================

func BenchmarkResolvePeakHourModel(b *testing.B) {
	model := ModelConfig{
		ID:               "benchmark-test",
		Internal:         true,
		PeakHourEnabled:  true,
		PeakHourStart:    "09:00",
		PeakHourEnd:      "17:00",
		PeakHourTimezone: "+7",
		PeakHourModel:    "peak-model",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = model.ResolvePeakHourModel(time.Now())
	}
}

func BenchmarkIsWithinWindow(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isWithinWindow(720, 540, 1020) // 12:00 in 09:00-17:00 window
	}
}
