package proxy

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// mockModelsConfig implements models.ModelsConfigInterface for testing
type mockModelsConfig struct {
	models      []models.ModelConfig
	credentials []models.CredentialConfig
}

func (m *mockModelsConfig) GetModels() []models.ModelConfig {
	return m.models
}

func (m *mockModelsConfig) GetEnabledModels() []models.ModelConfig {
	var result []models.ModelConfig
	for _, model := range m.models {
		if model.Enabled {
			result = append(result, model)
		}
	}
	return result
}

func (m *mockModelsConfig) GetModel(modelID string) *models.ModelConfig {
	for _, model := range m.models {
		if model.ID == modelID {
			copy := model
			return &copy
		}
	}
	return nil
}

func (m *mockModelsConfig) GetTruncateParams(modelID string) []string {
	if model := m.GetModel(modelID); model != nil {
		return model.TruncateParams
	}
	return nil
}

func (m *mockModelsConfig) GetFallbackChain(modelID string) []string {
	if model := m.GetModel(modelID); model != nil {
		return model.FallbackChain
	}
	return nil
}

func (m *mockModelsConfig) AddModel(model models.ModelConfig) error {
	m.models = append(m.models, model)
	return nil
}

func (m *mockModelsConfig) UpdateModel(modelID string, model models.ModelConfig) error {
	for i, existing := range m.models {
		if existing.ID == modelID {
			m.models[i] = model
			return nil
		}
	}
	return models.ErrModelNotFound
}

func (m *mockModelsConfig) RemoveModel(modelID string) error {
	for i, model := range m.models {
		if model.ID == modelID {
			m.models = append(m.models[:i], m.models[i+1:]...)
			return nil
		}
	}
	return models.ErrModelNotFound
}

func (m *mockModelsConfig) Save() error {
	return nil
}

func (m *mockModelsConfig) Validate() error {
	return nil
}

func (m *mockModelsConfig) GetCredential(id string) *models.CredentialConfig {
	for _, cred := range m.credentials {
		if cred.ID == id {
			copy := cred
			return &copy
		}
	}
	return nil
}

func (m *mockModelsConfig) GetCredentials() []models.CredentialConfig {
	return m.credentials
}

func (m *mockModelsConfig) AddCredential(cred models.CredentialConfig) error {
	m.credentials = append(m.credentials, cred)
	return nil
}

func (m *mockModelsConfig) UpdateCredential(id string, cred models.CredentialConfig) error {
	for i, existing := range m.credentials {
		if existing.ID == id {
			m.credentials[i] = cred
			return nil
		}
	}
	return models.ErrCredentialNotFound
}

func (m *mockModelsConfig) RemoveCredential(id string) error {
	for i, cred := range m.credentials {
		if cred.ID == id {
			m.credentials = append(m.credentials[:i], m.credentials[i+1:]...)
			return nil
		}
	}
	return models.ErrCredentialNotFound
}

func (m *mockModelsConfig) ResolveInternalConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool) {
	modelConfig := m.GetModel(modelID)
	if modelConfig == nil || !modelConfig.Internal {
		return "", "", "", "", false
	}

	if modelConfig.CredentialID == "" {
		return "", "", "", "", false
	}

	cred := m.GetCredential(modelConfig.CredentialID)
	if cred == nil {
		return "", "", "", "", false
	}

	// Provider comes only from credential
	provider = cred.Provider

	baseURL = modelConfig.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}

	return provider, cred.APIKey, baseURL, modelConfig.InternalModel, true
}

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
			name:     "internal with credential",
			config:   &models.ModelConfig{Internal: true, CredentialID: "test-cred"},
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
	mockResolver := &mockModelsConfig{}
	handler := &InternalHandler{
		config:   &models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
		resolver: mockResolver,
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
	mockResolver := &mockModelsConfig{}
	config := &models.ModelConfig{
		ID:            "test-model",
		Name:          "Test Model",
		Internal:      true,
		CredentialID:  "test-cred",
		InternalModel: "gpt-4",
	}

	handler := NewInternalHandler(config, mockResolver)
	if handler == nil {
		t.Fatal("expected handler, got nil")
	}
	if handler.config != config {
		t.Error("handler config mismatch")
	}
	if handler.resolver != mockResolver {
		t.Error("handler resolver mismatch")
	}
}
