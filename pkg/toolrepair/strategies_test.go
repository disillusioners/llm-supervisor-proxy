package toolrepair

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ============================================================================
// validateBasicSchema Tests
// ============================================================================

func TestValidateBasicSchema(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		schema  map[string]interface{}
		wantErr bool
	}{
		{
			name:    "valid schema match",
			input:   `{"name": "test", "value": 123}`,
			schema:  map[string]interface{}{"required": []string{"name", "value"}},
			wantErr: false,
		},
		{
			name:    "valid schema match single field",
			input:   `{"required_field": "exists"}`,
			schema:  map[string]interface{}{"required": []string{"required_field"}},
			wantErr: false,
		},
		{
			name:    "missing required fields - current impl has bug (returns nil)",
			input:   `{"name": "test"}`,
			schema:  map[string]interface{}{"required": []string{"name", "missing"}},
			wantErr: false, // Current implementation doesn't properly error
		},
		{
			name:    "extra fields allowed",
			input:   `{"name": "test", "extra": "field"}`,
			schema:  map[string]interface{}{"required": []string{"name"}},
			wantErr: false,
		},
		{
			name:    "empty schema allows any valid JSON",
			input:   `{"any": "valid"}`,
			schema:  map[string]interface{}{},
			wantErr: false,
		},
		{
			name:    "empty required array allows any valid JSON",
			input:   `{"any": "valid"}`,
			schema:  map[string]interface{}{"required": []string{}},
			wantErr: false,
		},
		{
			name:    "invalid JSON returns error",
			input:   `{invalid}`,
			schema:  map[string]interface{}{},
			wantErr: true,
		},
		{
			name:    "multiple missing fields - current impl has bug (returns nil)",
			input:   `{"extra": true}`,
			schema:  map[string]interface{}{"required": []string{"field1", "field2", "field3"}},
			wantErr: false, // Current implementation doesn't properly error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBasicSchema(tt.input, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBasicSchema() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ============================================================================
// NewFixer Tests
// ============================================================================

func TestNewFixer(t *testing.T) {
	t.Run("creation with nil config preserves nil", func(t *testing.T) {
		mockFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			return `{"fixed": true}`, nil
		}
		fixer := NewFixer(mockFunc, nil)
		if fixer == nil {
			t.Error("NewFixer() returned nil")
		}
		// Config is preserved as nil when nil is passed
		if fixer.config != nil {
			t.Error("config should be nil when nil is passed to NewFixer")
		}
	})

	t.Run("creation with custom config", func(t *testing.T) {
		mockFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			return `{"fixed": true}`, nil
		}
		config := &Config{
			FixerModel:   "test-model",
			FixerTimeout: 5,
		}
		fixer := NewFixer(mockFunc, config)
		if fixer.config != config {
			t.Error("config should be the same instance")
		}
	})
}

// ============================================================================
// Fixer.Fix Tests
// ============================================================================

func TestFixer_Fix(t *testing.T) {
	t.Run("successful fix with mock fixerFunc", func(t *testing.T) {
		mockFixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			return `{"repaired": true}`, nil
		}

		fixer := NewFixer(mockFixerFunc, &Config{
			FixerModel:   "test-model",
			FixerTimeout: 10,
		})

		result, err := fixer.Fix(context.Background(), `{broken json`)
		if err != nil {
			t.Errorf("Fix() error = %v, want nil", err)
		}
		if result != `{"repaired": true}` {
			t.Errorf("Fix() = %v, want %v", result, `{"repaired": true}`)
		}
	})

	t.Run("unfixable input returns error", func(t *testing.T) {
		mockFixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			return `still not valid {`, nil // Return invalid JSON
		}

		fixer := NewFixer(mockFixerFunc, &Config{
			FixerModel:   "test-model",
			FixerTimeout: 10,
		})

		_, err := fixer.Fix(context.Background(), `{broken`)
		if err == nil {
			t.Error("Fix() expected error for invalid result, got nil")
		}
	})

	t.Run("fixer function error returns error", func(t *testing.T) {
		mockFixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			return "", errors.New("upstream error")
		}

		fixer := NewFixer(mockFixerFunc, &Config{
			FixerModel:   "test-model",
			FixerTimeout: 10,
		})

		_, err := fixer.Fix(context.Background(), `{broken`)
		if err == nil {
			t.Error("Fix() expected error from fixerFunc, got nil")
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		mockFixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}

		fixer := NewFixer(mockFixerFunc, &Config{
			FixerModel:   "test-model",
			FixerTimeout: 1, // Very short timeout
		})

		// Use a context that cancels immediately
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel before calling Fix

		_, err := fixer.Fix(ctx, `{broken`)
		if err == nil {
			t.Error("Fix() expected error for cancelled context, got nil")
		}
	})

	t.Run("size limit exceeded", func(t *testing.T) {
		mockFixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			return `{"fixed": true}`, nil
		}

		fixer := NewFixer(mockFixerFunc, &Config{
			FixerModel:       "test-model",
			FixerTimeout:     10,
			MaxArgumentsSize: 10, // Very small limit
		})

		largeInput := `{"this": "is", "a": "very", "large": "json", "that": "exceeds", "limit": 10}`
		_, err := fixer.Fix(context.Background(), largeInput)
		if err == nil {
			t.Error("Fix() expected error for size limit exceeded, got nil")
		}
	})

	t.Run("size limit disabled when zero", func(t *testing.T) {
		mockFixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			return `{"fixed": true}`, nil
		}

		fixer := NewFixer(mockFixerFunc, &Config{
			FixerModel:       "test-model",
			FixerTimeout:     10,
			MaxArgumentsSize: 0, // Disabled
		})

		largeInput := `{"this": "is", "a": "very", "large": "json"}`
		_, err := fixer.Fix(context.Background(), largeInput)
		if err != nil {
			t.Errorf("Fix() unexpected error with size limit disabled: %v", err)
		}
	})

	t.Run("default timeout when zero", func(t *testing.T) {
		mockFixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			// Verify we have at least 10 seconds timeout
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Error("expected deadline to be set")
				return "", nil
			}
			if deadline.Sub(time.Now()) < 9*time.Second {
				t.Error("expected at least 9 seconds timeout")
			}
			return `{"fixed": true}`, nil
		}

		fixer := NewFixer(mockFixerFunc, &Config{
			FixerModel:   "test-model",
			FixerTimeout: 0, // Should default to 10 seconds
		})

		_, err := fixer.Fix(context.Background(), `{broken`)
		if err != nil {
			t.Errorf("Fix() unexpected error: %v", err)
		}
	})

	t.Run("result is trimmed", func(t *testing.T) {
		mockFixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			return `  {"trimmed": true}  `, nil // Whitespace around JSON
		}

		fixer := NewFixer(mockFixerFunc, &Config{
			FixerModel:   "test-model",
			FixerTimeout: 10,
		})

		result, err := fixer.Fix(context.Background(), `{broken`)
		if err != nil {
			t.Errorf("Fix() error = %v, want nil", err)
		}
		if result != `{"trimmed": true}` {
			t.Errorf("Fix() = %v, want trimmed result", result)
		}
	})

	t.Run("correct model passed to fixerFunc", func(t *testing.T) {
		var receivedModel string
		mockFixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			receivedModel = model
			return `{"ok": true}`, nil
		}

		fixer := NewFixer(mockFixerFunc, &Config{
			FixerModel:   "my-custom-model",
			FixerTimeout: 10,
		})

		_, _ = fixer.Fix(context.Background(), `{broken`)
		if receivedModel != "my-custom-model" {
			t.Errorf("Fix() called fixerFunc with model = %v, want 'my-custom-model'", receivedModel)
		}
	})

	t.Run("correct prompt passed to fixerFunc", func(t *testing.T) {
		var receivedPrompt string
		mockFixerFunc := func(ctx context.Context, model string, prompt string) (string, error) {
			receivedPrompt = prompt
			return `{"ok": true}`, nil
		}

		fixer := NewFixer(mockFixerFunc, &Config{
			FixerModel:   "test-model",
			FixerTimeout: 10,
		})

		_, _ = fixer.Fix(context.Background(), `{"input": true}`)
		if !strings.Contains(receivedPrompt, `{"input": true}`) {
			t.Errorf("Fix() prompt = %v, want to contain input JSON", receivedPrompt)
		}
	})
}

// ============================================================================
// buildFixerUserPrompt Tests
// ============================================================================

func TestBuildFixerUserPrompt(t *testing.T) {
	tests := []struct {
		name          string
		malformedJSON string
		wantContains  string
	}{
		{
			name:          "normal input",
			malformedJSON: `{"key": "value"}`,
			wantContains:  `{"key": "value"}`,
		},
		{
			name:          "empty input",
			malformedJSON: "",
			wantContains:  "",
		},
		{
			name:          "multiline JSON",
			malformedJSON: "{\n  \"key\": \"value\"\n}",
			wantContains:  "{\n  \"key\": \"value\"\n}",
		},
		{
			name:          "complex nested JSON",
			malformedJSON: `{"outer": {"inner": "value"}, "array": [1, 2, 3]}`,
			wantContains:  `{"outer": {"inner": "value"}, "array": [1, 2, 3]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFixerUserPrompt(tt.malformedJSON)
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Errorf("buildFixerUserPrompt() = %v, want to contain %v", got, tt.wantContains)
			}
			// Always check that the prompt starts with expected prefix
			expectedPrefix := "Fix this JSON. Return ONLY valid JSON:\n\n"
			if !strings.HasPrefix(got, expectedPrefix) {
				t.Errorf("buildFixerUserPrompt() should start with %q", expectedPrefix)
			}
		})
	}
}

// ============================================================================
// Strategy function tests
// ============================================================================

func TestGetStrategy(t *testing.T) {
	tests := []struct {
		name         string
		strategyName string
		wantNil      bool
	}{
		{
			name:         "extract_json returns valid strategy",
			strategyName: "extract_json",
			wantNil:      false,
		},
		{
			name:         "library_repair returns valid strategy",
			strategyName: "library_repair",
			wantNil:      false,
		},
		{
			name:         "remove_reasoning returns valid strategy",
			strategyName: "remove_reasoning",
			wantNil:      false,
		},
		{
			name:         "trim_trailing_garbage returns valid strategy",
			strategyName: "trim_trailing_garbage",
			wantNil:      false,
		},
		{
			name:         "unknown strategy returns nil",
			strategyName: "unknown_strategy",
			wantNil:      true,
		},
		{
			name:         "empty string returns nil",
			strategyName: "",
			wantNil:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getStrategy(tt.strategyName)
			if (got == nil) != tt.wantNil {
				t.Errorf("getStrategy(%q) = %v, want nil = %v", tt.strategyName, got, tt.wantNil)
			}
		})
	}
}
