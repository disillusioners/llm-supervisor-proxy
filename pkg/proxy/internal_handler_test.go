package proxy

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

func TestCanHandleInternal(t *testing.T) {
	tests := []struct {
		name     string
		config   *models.ModelConfig
		expected bool
	}{
		{
			name:     "nil config",
			config:   nil,
			expected: false,
		},
		{
			name:     "not internal",
			config:   &models.ModelConfig{Internal: false},
			expected: false,
		},
		{
			name:     "internal true",
			config:   &models.ModelConfig{Internal: true},
			expected: true,
		},
		{
			name:     "internal with provider",
			config:   &models.ModelConfig{Internal: true, InternalProvider: "openai"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CanHandleInternal(tt.config)
			if result != tt.expected {
				t.Errorf("CanHandleInternal() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestInternalHandler_convertRequest(t *testing.T) {
	handler := &InternalHandler{
		config: &models.ModelConfig{
			ID:            "test-model",
			InternalModel: "gpt-4",
		},
	}

	tests := []struct {
		name    string
		body    map[string]interface{}
		checkFn func(*testing.T, *providers.ChatCompletionRequest)
	}{
		{
			name: "basic request",
			body: map[string]interface{}{
				"model": "test-model",
				"messages": []interface{}{
					map[string]interface{}{"role": "user", "content": "Hello"},
				},
			},
			checkFn: func(t *testing.T, req *providers.ChatCompletionRequest) {
				if len(req.Messages) != 1 {
					t.Errorf("expected 1 message, got %d", len(req.Messages))
				}
				if req.Messages[0].Role != "user" {
					t.Errorf("expected role 'user', got %q", req.Messages[0].Role)
				}
			},
		},
		{
			name: "with temperature",
			body: map[string]interface{}{
				"model":       "test-model",
				"messages":    []interface{}{},
				"temperature": 0.7,
			},
			checkFn: func(t *testing.T, req *providers.ChatCompletionRequest) {
				if req.Temperature == nil || *req.Temperature != 0.7 {
					t.Error("expected temperature 0.7")
				}
			},
		},
		{
			name: "with max_tokens",
			body: map[string]interface{}{
				"model":      "test-model",
				"messages":   []interface{}{},
				"max_tokens": float64(100),
			},
			checkFn: func(t *testing.T, req *providers.ChatCompletionRequest) {
				if req.MaxTokens == nil || *req.MaxTokens != 100 {
					t.Error("expected max_tokens 100")
				}
			},
		},
		{
			name: "with stream",
			body: map[string]interface{}{
				"model":    "test-model",
				"messages": []interface{}{},
				"stream":   true,
			},
			checkFn: func(t *testing.T, req *providers.ChatCompletionRequest) {
				if !req.Stream {
					t.Error("expected stream true")
				}
			},
		},
		{
			name: "extra fields go to Extra",
			body: map[string]interface{}{
				"model":        "test-model",
				"messages":     []interface{}{},
				"custom_field": "custom_value",
			},
			checkFn: func(t *testing.T, req *providers.ChatCompletionRequest) {
				if req.Extra["custom_field"] != "custom_value" {
					t.Error("expected custom_field in Extra")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := handler.convertRequest(tt.body)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.checkFn(t, req)
		})
	}
}

func TestNewInternalHandler(t *testing.T) {
	config := &models.ModelConfig{
		ID:               "test-model",
		Name:             "Test Model",
		Internal:         true,
		InternalProvider: "openai",
		InternalModel:    "gpt-4",
	}

	handler := NewInternalHandler(config)
	if handler == nil {
		t.Fatal("expected handler, got nil")
	}
	if handler.config != config {
		t.Error("handler config mismatch")
	}
}
