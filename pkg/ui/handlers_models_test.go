package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// =============================================================================
// Mock ModelsConfig for handler tests
// =============================================================================

// mockModelsConfig implements models.ModelsConfigInterface for testing
type mockModelsConfig struct {
	models []models.ModelConfig
	cred   map[string]models.CredentialConfig
}

func newMockModelsConfigForHandlers() *mockModelsConfig {
	return &mockModelsConfig{
		models: make([]models.ModelConfig, 0),
		cred:   make(map[string]models.CredentialConfig),
	}
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
			return &model
		}
	}
	return nil
}

func (m *mockModelsConfig) GetTruncateParams(modelID string) []string {
	model := m.GetModel(modelID)
	if model == nil {
		return nil
	}
	return model.TruncateParams
}

func (m *mockModelsConfig) GetFallbackChain(modelID string) []string {
	model := m.GetModel(modelID)
	if model == nil {
		return nil
	}
	result := []string{model.ID}
	result = append(result, model.FallbackChain...)
	return result
}

func (m *mockModelsConfig) AddModel(model models.ModelConfig) error {
	for _, existing := range m.models {
		if existing.ID == model.ID {
			return models.ErrDuplicateModelID
		}
	}
	if model.ID == "" {
		return models.ErrInvalidModelID
	}
	if model.Name == "" {
		return models.ErrInvalidModelName
	}
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
	cred, ok := m.cred[id]
	if !ok {
		return nil
	}
	return &cred
}

func (m *mockModelsConfig) GetCredentials() []models.CredentialConfig {
	var result []models.CredentialConfig
	for _, cred := range m.cred {
		result = append(result, cred)
	}
	return result
}

func (m *mockModelsConfig) AddCredential(cred models.CredentialConfig) error {
	m.cred[cred.ID] = cred
	return nil
}

func (m *mockModelsConfig) UpdateCredential(id string, cred models.CredentialConfig) error {
	if _, ok := m.cred[id]; !ok {
		return models.ErrCredentialNotFound
	}
	m.cred[id] = cred
	return nil
}

func (m *mockModelsConfig) RemoveCredential(id string) error {
	delete(m.cred, id)
	return nil
}

func (m *mockModelsConfig) ResolveInternalConfig(modelID string) (string, string, string, string, bool) {
	model := m.GetModel(modelID)
	if model == nil || !model.Internal {
		return "", "", "", "", false
	}
	cred := m.GetCredential(model.CredentialID)
	if cred == nil {
		return "", "", "", "", false
	}
	return cred.Provider, cred.APIKey, cred.BaseURL, model.InternalModel, true
}

// =============================================================================
// Test server for model handler tests
// =============================================================================

type modelTestServer struct {
	*Server
	mockModels *mockModelsConfig
}

func newModelTestServer() *modelTestServer {
	mockModels := newMockModelsConfigForHandlers()
	// Add a credential for internal model testing
	mockModels.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})

	s := &Server{
		modelsConfig: mockModels,
	}

	return &modelTestServer{
		Server:     s,
		mockModels: mockModels,
	}
}

func (ts *modelTestServer) serve() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/models", ts.handleModels)
	mux.HandleFunc("/fe/api/models/", ts.handleModelDetail)
	return httptest.NewServer(mux)
}

// =============================================================================
// Handler Tests for Secondary Upstream Model
// =============================================================================

// TestHandleModels_GET_ReturnsSecondaryUpstreamModel tests that GET /fe/api/models
// returns the secondary_upstream_model field.
func TestHandleModels_GET_ReturnsSecondaryUpstreamModel(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	// Add a model with secondary upstream model
	ts.mockModels.AddModel(models.ModelConfig{
		ID:                     "test-model-with-secondary",
		Name:                   "Test Model With Secondary",
		Enabled:                true,
		Internal:               true,
		CredentialID:           "test-cred",
		InternalModel:          "glm-5.0",
		SecondaryUpstreamModel: "glm-4-flash",
	})

	// GET models
	resp, err := http.Get(server.URL + "/fe/api/models")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var models []Model
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(models) != 1 {
		t.Fatalf("Expected 1 model, got %d", len(models))
	}

	if models[0].SecondaryUpstreamModel != "glm-4-flash" {
		t.Errorf("SecondaryUpstreamModel = %s, want glm-4-flash", models[0].SecondaryUpstreamModel)
	}
}

// TestHandleModels_POST_CreatesModelWithSecondaryUpstreamModel tests that POST
// creates a model with secondary_upstream_model.
func TestHandleModels_POST_CreatesModelWithSecondaryUpstreamModel(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	// Create model with secondary upstream model
	body := `{
		"id": "new-model-with-secondary",
		"name": "New Model With Secondary",
		"enabled": true,
		"internal": true,
		"credential_id": "test-cred",
		"internal_model": "glm-5.0",
		"secondary_upstream_model": "glm-4-flash"
	}`

	resp, err := http.Post(server.URL+"/fe/api/models", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", resp.StatusCode)
	}

	var model Model
	if err := json.NewDecoder(resp.Body).Decode(&model); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if model.SecondaryUpstreamModel != "glm-4-flash" {
		t.Errorf("SecondaryUpstreamModel = %s, want glm-4-flash", model.SecondaryUpstreamModel)
	}

	// Verify it was actually added
	addedModel := ts.mockModels.GetModel("new-model-with-secondary")
	if addedModel == nil {
		t.Fatal("Model was not added to store")
	}
	if addedModel.SecondaryUpstreamModel != "glm-4-flash" {
		t.Errorf("Stored SecondaryUpstreamModel = %s, want glm-4-flash", addedModel.SecondaryUpstreamModel)
	}
}

// TestHandleModels_POST_RejectsSecondaryWithNonInternal tests that POST
// rejects secondary_upstream_model when internal=false.
func TestHandleModels_POST_RejectsSecondaryWithNonInternal(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	// Create model with secondary upstream model but internal=false
	body := `{
		"id": "invalid-model",
		"name": "Invalid Model",
		"enabled": true,
		"internal": false,
		"secondary_upstream_model": "glm-4-flash"
	}`

	resp, err := http.Post(server.URL+"/fe/api/models", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}

	var errResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}

	if !strings.Contains(errResp["error"], "secondary_upstream_model requires internal to be true") {
		t.Errorf("Error message should mention secondary_upstream_model, got: %s", errResp["error"])
	}
}

// TestHandleModels_POST_AcceptsEmptySecondaryWithInternal tests that POST
// accepts empty secondary_upstream_model when internal=true.
func TestHandleModels_POST_AcceptsEmptySecondaryWithInternal(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	// Create model with empty secondary upstream model
	body := `{
		"id": "model-with-empty-secondary",
		"name": "Model With Empty Secondary",
		"enabled": true,
		"internal": true,
		"credential_id": "test-cred",
		"internal_model": "glm-5.0",
		"secondary_upstream_model": ""
	}`

	resp, err := http.Post(server.URL+"/fe/api/models", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("Expected status 201, got %d", resp.StatusCode)
	}

	var model Model
	if err := json.NewDecoder(resp.Body).Decode(&model); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if model.SecondaryUpstreamModel != "" {
		t.Errorf("SecondaryUpstreamModel = %s, want empty", model.SecondaryUpstreamModel)
	}
}

// TestHandleModelDetail_PUT_UpdatesSecondaryUpstreamModel tests that PUT
// updates the secondary_upstream_model field.
func TestHandleModelDetail_PUT_UpdatesSecondaryUpstreamModel(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	// First add a model without secondary
	ts.mockModels.AddModel(models.ModelConfig{
		ID:            "model-to-update",
		Name:          "Model To Update",
		Enabled:       true,
		Internal:      true,
		CredentialID:  "test-cred",
		InternalModel: "glm-5.0",
	})

	// Update with secondary upstream model
	body := `{
		"name": "Updated Model",
		"enabled": true,
		"internal": true,
		"credential_id": "test-cred",
		"internal_model": "glm-5.0",
		"secondary_upstream_model": "glm-4-flash"
	}`

	req, err := http.NewRequest(http.MethodPut, server.URL+"/fe/api/models/model-to-update", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to create PUT request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var model Model
	if err := json.NewDecoder(resp.Body).Decode(&model); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if model.SecondaryUpstreamModel != "glm-4-flash" {
		t.Errorf("SecondaryUpstreamModel = %s, want glm-4-flash", model.SecondaryUpstreamModel)
	}

	// Verify it was actually updated
	updatedModel := ts.mockModels.GetModel("model-to-update")
	if updatedModel == nil {
		t.Fatal("Model was not updated in store")
	}
	if updatedModel.SecondaryUpstreamModel != "glm-4-flash" {
		t.Errorf("Stored SecondaryUpstreamModel = %s, want glm-4-flash", updatedModel.SecondaryUpstreamModel)
	}
}

// TestHandleModelDetail_PUT_RejectsSecondaryWithNonInternal tests that PUT
// rejects secondary_upstream_model when internal=false.
func TestHandleModelDetail_PUT_RejectsSecondaryWithNonInternal(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	// Update a model with secondary upstream model but internal=false
	body := `{
		"name": "Invalid Update",
		"enabled": true,
		"internal": false,
		"secondary_upstream_model": "glm-4-flash"
	}`

	req, err := http.NewRequest(http.MethodPut, server.URL+"/fe/api/models/some-model", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to create PUT request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}

	var errResp map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}

	if !strings.Contains(errResp["error"], "secondary_upstream_model requires internal to be true") {
		t.Errorf("Error message should mention secondary_upstream_model, got: %s", errResp["error"])
	}
}

// TestHandleModels_GET_ReturnsEmptySecondaryUpstreamModel tests that GET returns
// empty string for models without secondary_upstream_model set.
func TestHandleModels_GET_ReturnsEmptySecondaryUpstreamModel(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	// Add a model without secondary upstream model
	ts.mockModels.AddModel(models.ModelConfig{
		ID:            "model-without-secondary",
		Name:          "Model Without Secondary",
		Enabled:       true,
		Internal:      true,
		CredentialID:  "test-cred",
		InternalModel: "glm-5.0",
		// SecondaryUpstreamModel not set (empty)
	})

	// GET models
	resp, err := http.Get(server.URL + "/fe/api/models")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var models []Model
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(models) != 1 {
		t.Fatalf("Expected 1 model, got %d", len(models))
	}

	if models[0].SecondaryUpstreamModel != "" {
		t.Errorf("SecondaryUpstreamModel = %s, want empty string", models[0].SecondaryUpstreamModel)
	}
}

// TestHandleModels_GET_ReturnsNonInternalModelWithSecondary tests that GET correctly
// serializes models with internal=false (even without secondary set).
func TestHandleModels_GET_ReturnsNonInternalModelWithSecondary(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	// Add a non-internal model
	ts.mockModels.AddModel(models.ModelConfig{
		ID:       "non-internal-model",
		Name:     "Non Internal Model",
		Enabled:  true,
		Internal: false,
	})

	// GET models
	resp, err := http.Get(server.URL + "/fe/api/models")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var models []Model
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(models) != 1 {
		t.Fatalf("Expected 1 model, got %d", len(models))
	}

	if models[0].ID != "non-internal-model" {
		t.Errorf("Model ID = %s, want non-internal-model", models[0].ID)
	}

	if models[0].Internal {
		t.Error("Model should not be internal")
	}

	// Secondary should be empty for non-internal
	if models[0].SecondaryUpstreamModel != "" {
		t.Errorf("SecondaryUpstreamModel = %s, want empty for non-internal model", models[0].SecondaryUpstreamModel)
	}
}
