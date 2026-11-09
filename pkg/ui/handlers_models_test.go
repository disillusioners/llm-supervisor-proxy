package ui

import (
	"encoding/json"
	"fmt"
	"io"
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

func (m *mockModelsConfig) GetModelByName(modelName string) *models.ModelConfig {
	for _, model := range m.models {
		if model.Name == modelName {
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
	cred := m.GetCredential(model.PrimaryCredentialID())
	if cred == nil {
		return "", "", "", "", false
	}
	return cred.Provider, cred.APIKey, cred.BaseURL, model.InternalModel, true
}

// ResolveInternalConfigWithAffinity (Phase 3 / Task 16 seam mock).
func (m *mockModelsConfig) ResolveInternalConfigWithAffinity(modelID, conversationKey string) (models.ResolvedCredential, bool) {
	provider, apiKey, baseURL, internalModel, ok := m.ResolveInternalConfig(modelID)
	if !ok {
		return models.ResolvedCredential{}, false
	}
	mc := m.GetModel(modelID)
	primaryID := ""
	if mc != nil {
		primaryID = mc.PrimaryCredentialID()
	}
	return models.ResolvedCredential{
		Provider:      provider,
		APIKey:        apiKey,
		BaseURL:       baseURL,
		InternalModel: internalModel,
		CredentialID:  primaryID,
		NewlyBound:    false,
	}, true
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
	// Seed a few credentials for the Phase-4 multi-credential matrix:
	//   - test-cred          provider=openai      (existing single-cred path)
	//   - test-cred-2        provider=openai      (multi-credential, same provider)
	//   - test-cred-anthropic provider=anthropic  (provider-mismatch tests)
	mockModels.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
	})
	mockModels.AddCredential(models.CredentialConfig{
		ID:       "test-cred-2",
		Provider: "openai",
		APIKey:   "test-key-2",
	})
	mockModels.AddCredential(models.CredentialConfig{
		ID:       "test-cred-anthropic",
		Provider: "anthropic",
		APIKey:   "test-key-anthropic",
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
	mux.HandleFunc("/fe/api/models/validate", ts.handleValidateModel)
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
		Credentials:            models.TestRefs("test-cred"),
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
		"credentials": [{"credential_id": "test-cred", "weight": 1, "position": 0}],
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
		"credentials": [{"credential_id": "test-cred", "weight": 1, "position": 0}],
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
		Credentials:   models.TestRefs("test-cred"),
		InternalModel: "glm-5.0",
	})

	// Update with secondary upstream model
	body := `{
		"name": "Updated Model",
		"enabled": true,
		"internal": true,
		"credentials": [{"credential_id": "test-cred", "weight": 1, "position": 0}],
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
		Credentials:   models.TestRefs("test-cred"),
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

// =============================================================================
// Phase 4 — Multi-Credential Wire-Shape Tests
//
// The Model DTO carries `credentials[]` instead of `credential_id`. These
// tests cover the validation matrix (POST/PUT/validate), the deprecation
// window (legacy `credential_id` is silently dropped when `credentials[]`
// is present), the position-stamping invariant, and the W6 collapse-close
// regression: PUT replacing a 1-ref model with 2 refs must persist both.
// =============================================================================

// postModel is a small helper that POSTs `body` to /fe/api/models on the
// test server and returns the response. Keeps the new tests focused on
// assertions instead of boilerplate.
func postModel(t *testing.T, server *httptest.Server, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(server.URL+"/fe/api/models", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	return resp
}

// putModel is the PUT counterpart.
func putModel(t *testing.T, server *httptest.Server, id, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, server.URL+"/fe/api/models/"+id, strings.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to build PUT request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT failed: %v", err)
	}
	return resp
}

// readBodyError drains the body and returns the parsed {error: "..."} map.
func readBodyError(t *testing.T, resp *http.Response) map[string]string {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("Failed to decode error body: %v", err)
	}
	return m
}

// TestHandleModels_GET_ReturnsCredentialsArray verifies the GET response
// hydrates the credentials[] slice (preserving order) and never emits a
// top-level credential_id field.
func TestHandleModels_GET_ReturnsCredentialsArray(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{
		ID:            "multi-cred-model",
		Name:          "Multi Cred Model",
		Enabled:       true,
		Internal:      true,
		Credentials:   models.TestRefsWeighted(models.CredentialRef{CredentialID: "test-cred", Weight: 2}, models.CredentialRef{CredentialID: "test-cred-2", Weight: 1}),
		InternalModel: "glm-5.0",
	})

	resp, err := http.Get(server.URL + "/fe/api/models")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	// Decode into a Model first to confirm typed read works.
	var got []Model
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("Failed to decode typed response: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Expected 1 model, got %d", len(got))
	}
	if len(got[0].Credentials) != 2 {
		t.Fatalf("Expected 2 credential refs, got %d", len(got[0].Credentials))
	}
	if got[0].Credentials[0].CredentialID != "test-cred" || got[0].Credentials[0].Weight != 2 || got[0].Credentials[0].Position != 0 {
		t.Errorf("credentials[0] = %+v, want {test-cred, 2, 0}", got[0].Credentials[0])
	}
	if got[0].Credentials[1].CredentialID != "test-cred-2" || got[0].Credentials[1].Weight != 1 || got[0].Credentials[1].Position != 1 {
		t.Errorf("credentials[1] = %+v, want {test-cred-2, 1, 1}", got[0].Credentials[1])
	}

	// Decode into a raw map to confirm no top-level credential_id field
	// (also exercises the merge-gate assertion via JSON surface).
	// Re-fetch because the typed decode consumed the body.
	resp2, err := http.Get(server.URL + "/fe/api/models")
	if err != nil {
		t.Fatalf("GET (2nd) failed: %v", err)
	}
	defer resp2.Body.Close()
	var raw []map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&raw); err != nil {
		t.Fatalf("Failed to decode raw response: %v", err)
	}
	if _, ok := raw[0]["credential_id"]; ok {
		t.Errorf("GET response must NOT contain top-level credential_id key, got: %v", raw[0])
	}
	if _, ok := raw[0]["credentials"]; !ok {
		t.Errorf("GET response must contain credentials[] key, got: %v", raw[0])
	}
}

// (a) POST internal=true + credentials:[] → 400
func TestHandleModels_POST_RejectsEmptyCredentialsWhenInternal(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"id": "empty-creds-model",
		"name": "Empty Creds",
		"enabled": true,
		"internal": true,
		"credentials": [],
		"internal_model": "glm-5.0"
	}`

	resp := postModel(t, server, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}
	errResp := readBodyError(t, resp)
	if !strings.Contains(errResp["error"], "credentials") || !strings.Contains(errResp["error"], "at least one credential is required") {
		t.Errorf("Error should mention credentials + at least one credential, got: %s", errResp["error"])
	}
}

// (b) POST mixed-provider entries → 400 (openai + anthropic mixed)
func TestHandleModels_POST_RejectsMixedProviderCredentials(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"id": "mixed-provider-model",
		"name": "Mixed Provider",
		"enabled": true,
		"internal": true,
		"credentials": [
			{"credential_id": "test-cred", "weight": 1, "position": 0},
			{"credential_id": "test-cred-anthropic", "weight": 1, "position": 1}
		],
		"internal_model": "glm-5.0"
	}`

	resp := postModel(t, server, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}
	errResp := readBodyError(t, resp)
	if !strings.Contains(errResp["error"], "provider") || !strings.Contains(errResp["error"], "does not match primary provider") {
		t.Errorf("Error should mention provider mismatch, got: %s", errResp["error"])
	}
}

// (c) POST weight=0 entry → 400
func TestHandleModels_POST_RejectsZeroWeight(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"id": "zero-weight-model",
		"name": "Zero Weight",
		"enabled": true,
		"internal": true,
		"credentials": [{"credential_id": "test-cred", "weight": 0, "position": 0}],
		"internal_model": "glm-5.0"
	}`

	resp := postModel(t, server, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}
	errResp := readBodyError(t, resp)
	if !strings.Contains(errResp["error"], "weight") || !strings.Contains(errResp["error"], "must be > 0") {
		t.Errorf("Error should mention weight must be > 0, got: %s", errResp["error"])
	}
}

// (d) REGRESSION (Approver-mandated): POST sends BOTH legacy
// top-level credential_id AND credentials[] — legacy is IGNORED,
// credentials[] wins, stored refs == credentials[] payload, response
// JSON has NO top-level credential_id key.
func TestHandleModels_POST_IgnoresLegacyCredentialIDWhenCredentialsPresent(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"id": "legacy-plus-new-model",
		"name": "Legacy Plus New",
		"enabled": true,
		"internal": true,
		"credential_id": "test-cred-2",
		"credentials": [{"credential_id": "test-cred", "weight": 1, "position": 0}],
		"internal_model": "glm-5.0"
	}`

	resp := postModel(t, server, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}

	// Response: decode into raw map to assert NO top-level
	// credential_id key.
	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("Failed to decode raw response: %v", err)
	}
	if _, ok := raw["credential_id"]; ok {
		t.Errorf("response must NOT contain top-level credential_id, got: %v", raw)
	}
	credsAny, ok := raw["credentials"]
	if !ok {
		t.Fatalf("response must contain credentials[] key, got: %v", raw)
	}
	creds, ok := credsAny.([]interface{})
	if !ok || len(creds) != 1 {
		t.Fatalf("credentials[] must be a 1-element array, got: %v", credsAny)
	}
	first, ok := creds[0].(map[string]interface{})
	if !ok || first["credential_id"] != "test-cred" {
		t.Errorf("credentials[0].credential_id = %v, want test-cred", first["credential_id"])
	}

	// Stored: the model holds the credentials[] payload (test-cred),
	// not the legacy field (test-cred-2).
	stored := ts.mockModels.GetModel("legacy-plus-new-model")
	if stored == nil {
		t.Fatal("Model was not stored")
	}
	if len(stored.Credentials) != 1 {
		t.Fatalf("Stored credentials count = %d, want 1", len(stored.Credentials))
	}
	if stored.Credentials[0].CredentialID != "test-cred" {
		t.Errorf("Stored credentials[0] = %s, want test-cred (legacy test-cred-2 must be dropped)", stored.Credentials[0].CredentialID)
	}
	if stored.PrimaryCredentialID() != "test-cred" {
		t.Errorf("PrimaryCredentialID() = %s, want test-cred", stored.PrimaryCredentialID())
	}
}

// TestHandleModelDetail_PUT_IgnoresLegacyCredentialIDWhenCredentialsPresent
// is the PUT counterpart of the (d) regression — same guarantee, on
// the PUT path that previously collapsed multi-cred models to 1 ref
// (the Phase-1 W6 hazard).
func TestHandleModelDetail_PUT_IgnoresLegacyCredentialIDWhenCredentialsPresent(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{
		ID:            "model-put-legacy",
		Name:          "Model PUT Legacy",
		Enabled:       true,
		Internal:      true,
		Credentials:   models.TestRefs("test-cred-2"),
		InternalModel: "glm-5.0",
	})

	body := `{
		"name": "Model PUT Legacy",
		"enabled": true,
		"internal": true,
		"credential_id": "test-cred",
		"credentials": [{"credential_id": "test-cred-2", "weight": 1, "position": 0}],
		"internal_model": "glm-5.0"
	}`

	resp := putModel(t, server, "model-put-legacy", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	var raw map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("Failed to decode raw response: %v", err)
	}
	if _, ok := raw["credential_id"]; ok {
		t.Errorf("response must NOT contain top-level credential_id, got: %v", raw)
	}

	stored := ts.mockModels.GetModel("model-put-legacy")
	if stored == nil {
		t.Fatal("Model not stored")
	}
	if stored.PrimaryCredentialID() != "test-cred-2" {
		t.Errorf("PrimaryCredentialID() = %s, want test-cred-2 (legacy test-cred must be dropped)", stored.PrimaryCredentialID())
	}
}

// (e) POST valid 2-entry credentials [A(w2), B(w1)] → 201; response
// echoes array with positions stamped 0,1; mock store holds both.
func TestHandleModels_POST_AcceptsMultiCredentialAndStampsPositions(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"id": "two-cred-model",
		"name": "Two Cred Model",
		"enabled": true,
		"internal": true,
		"credentials": [
			{"credential_id": "test-cred", "weight": 2, "position": 99},
			{"credential_id": "test-cred-2", "weight": 1, "position": 42}
		],
		"internal_model": "glm-5.0"
	}`

	resp := postModel(t, server, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}

	var model Model
	if err := json.NewDecoder(resp.Body).Decode(&model); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(model.Credentials) != 2 {
		t.Fatalf("Expected 2 credentials in response, got %d", len(model.Credentials))
	}
	if model.Credentials[0].CredentialID != "test-cred" || model.Credentials[0].Weight != 2 || model.Credentials[0].Position != 0 {
		t.Errorf("credentials[0] = %+v, want {test-cred, 2, 0 (server-stamped)}", model.Credentials[0])
	}
	if model.Credentials[1].CredentialID != "test-cred-2" || model.Credentials[1].Weight != 1 || model.Credentials[1].Position != 1 {
		t.Errorf("credentials[1] = %+v, want {test-cred-2, 1, 1 (server-stamped)}", model.Credentials[1])
	}

	// Store holds both with stamped positions.
	stored := ts.mockModels.GetModel("two-cred-model")
	if stored == nil {
		t.Fatal("Model was not added to store")
	}
	if len(stored.Credentials) != 2 {
		t.Fatalf("Stored credentials count = %d, want 2", len(stored.Credentials))
	}
	if stored.Credentials[0].Position != 0 || stored.Credentials[1].Position != 1 {
		t.Errorf("Stored positions = %d,%d, want 0,1", stored.Credentials[0].Position, stored.Credentials[1].Position)
	}
}

// (f) POST duplicate credential_id entries → 400
func TestHandleModels_POST_RejectsDuplicateCredentials(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"id": "dup-cred-model",
		"name": "Dup Cred Model",
		"enabled": true,
		"internal": true,
		"credentials": [
			{"credential_id": "test-cred", "weight": 1, "position": 0},
			{"credential_id": "test-cred", "weight": 1, "position": 1}
		],
		"internal_model": "glm-5.0"
	}`

	resp := postModel(t, server, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}
	errResp := readBodyError(t, resp)
	if !strings.Contains(errResp["error"], "duplicate") {
		t.Errorf("Error should mention duplicate, got: %s", errResp["error"])
	}
}

// (g) POST 17 entries → 400
func TestHandleModels_POST_RejectsExceedingMaxCredentials(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	// Build 17 credential entries (only 2 are unique, the rest are
	// duplicates which would also fail — but the size cap must fire
	// first per validateCredentials ordering). Use a single
	// credential id padded with non-existent ids so the size cap
	// surfaces before the unknown-id errors.
	var b strings.Builder
	b.WriteString(`{"id":"too-many","name":"Too Many","enabled":true,"internal":true,"credentials":[`)
	for i := 0; i < 17; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"credential_id":"fake-%d","weight":1,"position":%d}`, i, i)
	}
	b.WriteString(`],"internal_model":"x"}`)

	resp := postModel(t, server, b.String())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}
	errResp := readBodyError(t, resp)
	if !strings.Contains(errResp["error"], "exceeds max") {
		t.Errorf("Error should mention exceeds max, got: %s", errResp["error"])
	}
}

// (h) POST unknown credential_id → 400
func TestHandleModels_POST_RejectsUnknownCredentialID(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"id": "unknown-cred-model",
		"name": "Unknown Cred Model",
		"enabled": true,
		"internal": true,
		"credentials": [{"credential_id": "does-not-exist", "weight": 1, "position": 0}],
		"internal_model": "glm-5.0"
	}`

	resp := postModel(t, server, body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}
	errResp := readBodyError(t, resp)
	if !strings.Contains(errResp["error"], "unknown credential") {
		t.Errorf("Error should mention unknown credential, got: %s", errResp["error"])
	}
}

// (i) PUT: update model from 1 ref to 2 refs → 200, store updated
// (proves W6 collapse closed).
func TestHandleModelDetail_PUT_ReplacesOneRefWithTwoRefs(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{
		ID:            "w6-collapse-target",
		Name:          "W6 Target",
		Enabled:       true,
		Internal:      true,
		Credentials:   models.TestRefs("test-cred"),
		InternalModel: "glm-5.0",
	})

	body := `{
		"name": "W6 Target",
		"enabled": true,
		"internal": true,
		"credentials": [
			{"credential_id": "test-cred", "weight": 1, "position": 0},
			{"credential_id": "test-cred-2", "weight": 1, "position": 1}
		],
		"internal_model": "glm-5.0"
	}`

	resp := putModel(t, server, "w6-collapse-target", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	stored := ts.mockModels.GetModel("w6-collapse-target")
	if stored == nil {
		t.Fatal("Model not found")
	}
	if len(stored.Credentials) != 2 {
		t.Fatalf("Stored credentials count = %d, want 2 (W6 collapse regression)", len(stored.Credentials))
	}
	if stored.Credentials[0].CredentialID != "test-cred" || stored.Credentials[1].CredentialID != "test-cred-2" {
		t.Errorf("Stored credentials = %+v, want [test-cred, test-cred-2]", stored.Credentials)
	}
}

// (j.a) handleValidateModel — mixed-provider → {valid:false, errors contains provider message}
func TestHandleValidateModel_RejectsMixedProvider(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"name": "Validate Mixed",
		"enabled": true,
		"internal": true,
		"credentials": [
			{"credential_id": "test-cred", "weight": 1, "position": 0},
			{"credential_id": "test-cred-anthropic", "weight": 1, "position": 1}
		],
		"internal_model": "glm-5.0"
	}`

	req, err := http.NewRequest(http.MethodPost, server.URL+"/fe/api/models/validate", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to build validate request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("validate POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}

	var out struct {
		Valid  bool     `json:"valid"`
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Failed to decode validate response: %v", err)
	}
	if out.Valid {
		t.Fatal("expected valid=false for mixed-provider payload")
	}
	found := false
	for _, e := range out.Errors {
		if strings.Contains(e, "provider") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an error mentioning provider, got: %v", out.Errors)
	}
}

// (j.b) handleValidateModel — valid multi-ref → {valid:true}
func TestHandleValidateModel_AcceptsValidMultiCredential(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"name": "Validate Valid Multi",
		"enabled": true,
		"internal": true,
		"credentials": [
			{"credential_id": "test-cred", "weight": 2, "position": 0},
			{"credential_id": "test-cred-2", "weight": 1, "position": 1}
		],
		"internal_model": "glm-5.0"
	}`

	req, err := http.NewRequest(http.MethodPost, server.URL+"/fe/api/models/validate", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to build validate request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("validate POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	var out struct {
		Valid  bool     `json:"valid"`
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Failed to decode validate response: %v", err)
	}
	if !out.Valid {
		t.Errorf("expected valid=true for valid multi-credential payload, got errors: %v", out.Errors)
	}
}

// TestHandleValidateModel_RejectsEmptyCredentialsWhenInternal covers
// validate-endpoint path of the (a) test — distinct code path from
// the POST/PUT 400 because the response shape is {valid:false, errors:[...]}.
func TestHandleValidateModel_RejectsEmptyCredentialsWhenInternal(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	body := `{
		"name": "Validate Empty",
		"enabled": true,
		"internal": true,
		"credentials": [],
		"internal_model": "glm-5.0"
	}`

	req, err := http.NewRequest(http.MethodPost, server.URL+"/fe/api/models/validate", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to build validate request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("validate POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}

	var out struct {
		Valid  bool     `json:"valid"`
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Failed to decode validate response: %v", err)
	}
	if out.Valid {
		t.Fatal("expected valid=false for empty credentials on internal model")
	}
	if len(out.Errors) == 0 {
		t.Fatal("expected non-empty errors array")
	}
	found := false
	for _, e := range out.Errors {
		if strings.Contains(e, "at least one credential") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an error mentioning 'at least one credential', got: %v", out.Errors)
	}
}

// =============================================================================
// Copy-model route tests (POST /fe/api/models/{id}/copy)
// =============================================================================

// copySourceModel seeds a fully-populated source row for the copy
// tests (every behaviour-defining field carries a non-zero value).
func copySourceModel(ts *modelTestServer) {
	ts.mockModels.AddModel(models.ModelConfig{
		ID:                           "copy-src",
		Name:                         "Copy Source",
		Enabled:                      true,
		FallbackChain:                []string{"other-model"},
		TruncateParams:               []string{"max_completion_tokens"},
		Kind:                         models.KindChat,
		Internal:                     true,
		InternalBaseURL:              "https://upstream.example/v1",
		InternalModel:                "glm-5.0",
		Credentials:                  models.TestRefs("test-cred"),
		ReleaseStreamChunkDeadline:   models.Duration(90_000_000_000),
		PeakHourEnabled:              true,
		PeakHourStart:                "09:00",
		PeakHourEnd:                  "18:00",
		PeakHourTimezone:             "+7",
		PeakHourModel:                "glm-4-flash",
		SecondaryUpstreamModel:       "glm-4-flash",
		ExcludeFromUltimateSwitching: true,
	})
}

// postCopy issues POST /fe/api/models/{id}/copy with the given body.
func postCopy(t *testing.T, server *httptest.Server, sourceID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, server.URL+"/fe/api/models/"+sourceID+"/copy", strings.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to build copy request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("copy POST failed: %v", err)
	}
	return resp
}

// TestHandleModelCopy_CopiesModelFaithfully asserts the copy is
// created (201) with every behaviour field carried over, the identity
// fields regenerated, enabled=false (dormant clone) and the credential
// REFS shared (no secret duplication, no credential row written).
func TestHandleModelCopy_CopiesModelFaithfully(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	copySourceModel(ts)

	resp := postCopy(t, server, "copy-src", `{"new_id":"copy-dst"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}

	var out Model
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if out.ID != "copy-dst" {
		t.Errorf("ID = %q, want %q", out.ID, "copy-dst")
	}
	if out.Name != "copy-dst" {
		t.Errorf("Name = %q, want %q (the name is the new id)", out.Name, "copy-dst")
	}
	if out.Enabled {
		t.Error("Enabled = true, want false (the copy must be a dormant clone)")
	}
	if !out.Internal || out.InternalBaseURL != "https://upstream.example/v1" || out.InternalModel != "glm-5.0" {
		t.Errorf("internal upstream fields not copied: %+v", out)
	}
	if len(out.TruncateParams) != 1 || out.TruncateParams[0] != "max_completion_tokens" {
		t.Errorf("TruncateParams = %v, want [max_completion_tokens]", out.TruncateParams)
	}
	if out.ReleaseStreamChunkDeadline != models.Duration(90_000_000_000) {
		t.Errorf("ReleaseStreamChunkDeadline = %v, want 90s", out.ReleaseStreamChunkDeadline)
	}
	if !out.PeakHourEnabled || out.PeakHourStart != "09:00" || out.PeakHourEnd != "18:00" ||
		out.PeakHourTimezone != "+7" || out.PeakHourModel != "glm-4-flash" {
		t.Errorf("peak-hour fields not copied: %+v", out)
	}
	if out.SecondaryUpstreamModel != "glm-4-flash" {
		t.Errorf("SecondaryUpstreamModel = %q, want glm-4-flash", out.SecondaryUpstreamModel)
	}
	if !out.ExcludeFromUltimateSwitching {
		t.Error("ExcludeFromUltimateSwitching = false, want true")
	}
	if out.Kind != models.KindChat {
		t.Errorf("Kind = %q, want %q", out.Kind, models.KindChat)
	}
	if len(out.Credentials) != 1 || out.Credentials[0].CredentialID != "test-cred" {
		t.Errorf("Credentials = %v, want one ref to test-cred", out.Credentials)
	}
	if len(out.Members) != 0 {
		t.Errorf("Members = %v, want empty (the copy is standalone)", out.Members)
	}
	if out.ShadowModelID != "" {
		t.Errorf("ShadowModelID = %q, want empty", out.ShadowModelID)
	}

	// The stored row must match the response.
	stored := ts.mockModels.GetModel("copy-dst")
	if stored == nil {
		t.Fatal("Copy was not added to the store")
	}
	if stored.Enabled {
		t.Error("Stored copy Enabled = true, want false")
	}
	if stored.Name != "copy-dst" {
		t.Errorf("Stored copy Name = %q, want %q", stored.Name, "copy-dst")
	}
	if len(stored.Credentials) != 1 || stored.Credentials[0].CredentialID != "test-cred" {
		t.Errorf("Stored copy Credentials = %v, want one ref to test-cred", stored.Credentials)
	}

	// The source row must be untouched.
	source := ts.mockModels.GetModel("copy-src")
	if source == nil {
		t.Fatal("Source model disappeared")
	}
	if !source.Enabled || source.Name != "Copy Source" {
		t.Errorf("Source row was mutated: %+v", source)
	}
	if len(source.Credentials) != 1 || source.Credentials[0].CredentialID != "test-cred" {
		t.Errorf("Source credentials were mutated: %v", source.Credentials)
	}
}

// TestHandleModelCopy_CredentialTableUntouched asserts the copy
// SHARES the credential rows: the credential set is unchanged (no
// secret duplication) and the copy references the same credential id.
func TestHandleModelCopy_CredentialTableUntouched(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	copySourceModel(ts)

	before := ts.mockModels.GetCredentials()
	if len(before) != 3 {
		t.Fatalf("Expected 3 seeded credentials, got %d", len(before))
	}

	resp := postCopy(t, server, "copy-src", `{"new_id":"copy-dst"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}

	after := ts.mockModels.GetCredentials()
	if len(after) != len(before) {
		t.Fatalf("Credential count changed: %d -> %d (the copy must not duplicate credentials)", len(before), len(after))
	}
	// Compare BY ID — the mock's credential map has no stable order.
	beforeByID := make(map[string]models.CredentialConfig, len(before))
	for _, c := range before {
		beforeByID[c.ID] = c
	}
	for _, c := range after {
		orig, ok := beforeByID[c.ID]
		if !ok {
			t.Errorf("Credential %q appeared after the copy (the credentials table must not be written)", c.ID)
			continue
		}
		if c.APIKey != orig.APIKey {
			t.Errorf("Credential %q changed: api_key %q -> %q", c.ID, orig.APIKey, c.APIKey)
		}
	}
}

// TestHandleModelCopy_FallbackChainDropsSourceEntry asserts the
// documented deviation: a fallback entry equal to the SOURCE id is
// dropped from the copy.
func TestHandleModelCopy_FallbackChainDropsSourceEntry(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	copySourceModel(ts)
	// Give the source a fallback chain that points at itself.
	ts.mockModels.UpdateModel("copy-src", models.ModelConfig{
		ID: "copy-src", Name: "Copy Source", Enabled: true, Internal: true,
		InternalModel:                "glm-5.0",
		Credentials:                  models.TestRefs("test-cred"),
		FallbackChain:                []string{"copy-src", "other-model"},
		TruncateParams:               []string{"max_completion_tokens"},
		Kind:                         models.KindChat,
		ShadowModelID:                "shadowed-model",
		ExcludeFromUltimateSwitching: true,
	})

	resp := postCopy(t, server, "copy-src", `{"new_id":"copy-dst"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}

	stored := ts.mockModels.GetModel("copy-dst")
	if stored == nil {
		t.Fatal("Copy was not added to the store")
	}
	if len(stored.FallbackChain) != 1 || stored.FallbackChain[0] != "other-model" {
		t.Errorf("FallbackChain = %v, want [other-model] (the source entry must be dropped)", stored.FallbackChain)
	}
	if stored.ShadowModelID != "" {
		t.Errorf("ShadowModelID = %q, want empty (the copy must not claim the shadow id)", stored.ShadowModelID)
	}
}

// TestHandleModelCopy_GroupSourceRefused asserts a group row cannot
// be copied (400) and that nothing is written.
func TestHandleModelCopy_GroupSourceRefused(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	ts.mockModels.AddModel(models.ModelConfig{
		ID:                           "group-1",
		Name:                         "Group One",
		Enabled:                      true,
		Kind:                         models.KindGroup,
		Internal:                     true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []models.GroupMember{{ModelID: "copy-src", Weight: 1}},
	})

	resp := postCopy(t, server, "group-1", `{"new_id":"group-copy"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}
	var errOut map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errOut); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}
	if errOut["error"] != "group models cannot be copied" {
		t.Errorf("error = %q, want %q", errOut["error"], "group models cannot be copied")
	}
	if ts.mockModels.GetModel("group-copy") != nil {
		t.Error("A refused group copy must not create a row")
	}
}

// TestHandleModelCopy_DuplicateNewIDRejected asserts a duplicate new
// id is rejected (400) AND that the existing row is NOT overwritten
// (the store's INSERT is an UPSERT — the route's pre-check is what
// prevents the silent clobber).
func TestHandleModelCopy_DuplicateNewIDRejected(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	copySourceModel(ts)
	ts.mockModels.AddModel(models.ModelConfig{
		ID:            "copy-dst",
		Name:          "Existing Model",
		Enabled:       true,
		Internal:      true,
		InternalModel: "glm-5.0",
		Credentials:   models.TestRefs("test-cred"),
	})

	resp := postCopy(t, server, "copy-src", `{"new_id":"copy-dst"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400, got %d", resp.StatusCode)
	}
	var errOut map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errOut); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}
	if errOut["error"] != "duplicate model ID" {
		t.Errorf("error = %q, want %q", errOut["error"], "duplicate model ID")
	}

	// The existing row must be intact (no upsert overwrite).
	existing := ts.mockModels.GetModel("copy-dst")
	if existing == nil {
		t.Fatal("Existing model was deleted by the refused copy")
	}
	if existing.Name != "Existing Model" || !existing.Enabled {
		t.Errorf("Existing model was modified by the refused copy: %+v", existing)
	}

	// Exactly one row with that id.
	count := 0
	for _, m := range ts.mockModels.GetModels() {
		if m.ID == "copy-dst" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Expected exactly 1 row with id copy-dst, got %d", count)
	}
}

// TestHandleModelCopy_MissingSource asserts a missing source is a 404
// with the DELETE-style "model not found" envelope.
func TestHandleModelCopy_MissingSource(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	resp := postCopy(t, server, "no-such-model", `{"new_id":"copy-dst"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("Expected status 404, got %d", resp.StatusCode)
	}
	var errOut map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errOut); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}
	if errOut["error"] != "model not found" {
		t.Errorf("error = %q, want %q", errOut["error"], "model not found")
	}
	if ts.mockModels.GetModel("copy-dst") != nil {
		t.Error("A refused copy must not create a row")
	}
}

// TestHandleModelCopy_InvalidNewID asserts the new-id validation
// rules and that a rejected copy leaves nothing behind.
func TestHandleModelCopy_InvalidNewID(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty", body: `{"new_id":""}`},
		{name: "missing", body: `{}`},
		{name: "slash", body: `{"new_id":"a/b"}`},
		{name: "whitespace", body: `{"new_id":"a b"}`},
		{name: "same as source", body: `{"new_id":"copy-src"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newModelTestServer()
			server := ts.serve()
			defer server.Close()

			copySourceModel(ts)

			resp := postCopy(t, server, "copy-src", tt.body)
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("Expected status 400, got %d", resp.StatusCode)
			}
			var errOut map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&errOut); err != nil {
				t.Fatalf("Failed to decode error response: %v", err)
			}
			if errOut["error"] == "" {
				t.Error("Expected a non-empty error message")
			}
			if len(ts.mockModels.GetModels()) != 1 {
				t.Errorf("A rejected copy must leave nothing behind, got %d rows", len(ts.mockModels.GetModels()))
			}
		})
	}
}

// TestHandleModelCopy_WrongMethod asserts the POST-only gate returns
// the house plain-text 405.
func TestHandleModelCopy_WrongMethod(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			ts := newModelTestServer()
			server := ts.serve()
			defer server.Close()

			copySourceModel(ts)

			req, err := http.NewRequest(method, server.URL+"/fe/api/models/copy-src/copy", nil)
			if err != nil {
				t.Fatalf("Failed to build request: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s failed: %v", method, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("Expected status 405, got %d", resp.StatusCode)
			}
			if len(ts.mockModels.GetModels()) != 1 {
				t.Errorf("A rejected copy must leave nothing behind, got %d rows", len(ts.mockModels.GetModels()))
			}
		})
	}
}

// TestHandleModelCopy_NotAddedToAnyGroup asserts the copy lands in NO
// group: group membership lives on the group row, so the copy route
// must not touch any group row.
func TestHandleModelCopy_NotAddedToAnyGroup(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	copySourceModel(ts)
	ts.mockModels.AddModel(models.ModelConfig{
		ID:                           "group-1",
		Name:                         "Group One",
		Enabled:                      true,
		Kind:                         models.KindGroup,
		Internal:                     true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []models.GroupMember{{ModelID: "copy-src", Weight: 1}},
	})

	resp := postCopy(t, server, "copy-src", `{"new_id":"copy-dst"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.StatusCode)
	}

	// The group row must be unchanged (same members, same weights).
	group := ts.mockModels.GetModel("group-1")
	if group == nil {
		t.Fatal("Group row disappeared")
	}
	if len(group.Members) != 1 || group.Members[0].ModelID != "copy-src" {
		t.Errorf("Group membership was modified by the copy: %+v", group.Members)
	}

	// No group may claim the copy.
	for _, m := range ts.mockModels.GetModels() {
		if !m.IsGroup() {
			continue
		}
		for _, member := range m.Members {
			if member.ModelID == "copy-dst" {
				t.Errorf("Copy was added to group %q", m.ID)
			}
		}
	}
}

// TestHandleModelDetail_PUTAndDelete_StillWork is the regression
// guard for the handleModelDetail restructure: the nested copy path
// must not disturb the existing PUT / DELETE behaviour.
func TestHandleModelDetail_PUTAndDelete_StillWork(t *testing.T) {
	ts := newModelTestServer()
	server := ts.serve()
	defer server.Close()

	copySourceModel(ts)

	// PUT
	putBody := `{"name":"Renamed","enabled":false,"internal":true,"internal_model":"glm-5.0","credentials":[{"credential_id":"test-cred","weight":1,"position":0}]}`
	req, err := http.NewRequest(http.MethodPut, server.URL+"/fe/api/models/copy-src", strings.NewReader(putBody))
	if err != nil {
		t.Fatalf("Failed to build PUT request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200 on PUT, got %d", resp.StatusCode)
	}
	if got := ts.mockModels.GetModel("copy-src"); got == nil || got.Name != "Renamed" {
		t.Errorf("PUT did not update the model: %+v", got)
	}

	// DELETE
	delReq, err := http.NewRequest(http.MethodDelete, server.URL+"/fe/api/models/copy-src", nil)
	if err != nil {
		t.Fatalf("Failed to build DELETE request: %v", err)
	}
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE failed: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("Expected status 204 on DELETE, got %d", delResp.StatusCode)
	}
	if ts.mockModels.GetModel("copy-src") != nil {
		t.Error("DELETE did not remove the model")
	}
}

// TestHandleModelCopy_DBBackend_PersistsRowAndRefusesClobber exercises
// the copy route against the REAL DB-backed ModelsManager (SQLite) —
// the production write path.
//
// It pins three things the mock cannot:
//
//  1. the copy is really PERSISTED (a row with the new id exists, with
//     enabled=0 and name = the new id);
//  2. the credentials table is NOT written (the copy shares the
//     source's credential rows — no secret is duplicated);
//  3. a duplicate new id is refused AND the existing row is NOT
//     overwritten — the store's INSERT is an UPSERT (PostgreSQL
//     "ON CONFLICT (id) DO UPDATE", SQLite "INSERT OR REPLACE"), so
//     the route's pre-check is the ONLY thing standing between a
//     mis-typed copy and a silent clobber of a live model row.
func TestHandleModelCopy_DBBackend_PersistsRowAndRefusesClobber(t *testing.T) {
	ts := newRealPUTTestServer(t)
	defer ts.cleanup()

	// Seed the source row (a chat model with one credential ref).
	if err := ts.mgr.AddModel(models.ModelConfig{
		ID:             "copy-src",
		Name:           "Copy Source",
		Enabled:        true,
		Internal:       true,
		InternalModel:  "glm-5.0",
		Credentials:    models.TestRefs("openai-cred"),
		TruncateParams: []string{"max_completion_tokens"},
	}); err != nil {
		t.Fatalf("seed source model: %v", err)
	}

	credsBefore := ts.mgr.GetCredentials()

	resp := postCopy(t, ts.server, "copy-src", `{"new_id":"copy-dst"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 201, got %d (body: %s)", resp.StatusCode, string(body))
	}

	// The row must be persisted, dormant, and named after the new id.
	row := ts.mgr.GetModelNative("copy-dst")
	if row == nil {
		t.Fatal("Copy row was not persisted")
	}
	if row.Enabled {
		t.Error("Persisted copy Enabled = true, want false (dormant clone)")
	}
	if row.Name != "copy-dst" {
		t.Errorf("Persisted copy Name = %q, want %q", row.Name, "copy-dst")
	}
	if row.InternalModel != "glm-5.0" {
		t.Errorf("Persisted copy InternalModel = %q, want glm-5.0", row.InternalModel)
	}
	if len(row.Credentials) != 1 || row.Credentials[0].CredentialID != "openai-cred" {
		t.Errorf("Persisted copy Credentials = %v, want one ref to openai-cred", row.Credentials)
	}

	// The credentials table must be untouched (refs are shared).
	credsAfter := ts.mgr.GetCredentials()
	if len(credsAfter) != len(credsBefore) {
		t.Errorf("Credential count changed: %d -> %d (the copy must not write the credentials table)", len(credsBefore), len(credsAfter))
	}

	// A duplicate new id must be refused AND must not clobber the row.
	dupResp := postCopy(t, ts.server, "copy-src", `{"new_id":"copy-dst"}`)
	defer dupResp.Body.Close()
	if dupResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status 400 on a duplicate new id, got %d", dupResp.StatusCode)
	}
	var errOut map[string]string
	if err := json.NewDecoder(dupResp.Body).Decode(&errOut); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}
	if errOut["error"] != "duplicate model ID" {
		t.Errorf("error = %q, want %q", errOut["error"], "duplicate model ID")
	}

	row = ts.mgr.GetModelNative("copy-dst")
	if row == nil {
		t.Fatal("The existing row was destroyed by the refused copy")
	}
	if row.Enabled || row.Name != "copy-dst" {
		t.Errorf("The existing row was overwritten by the refused copy: %+v", row)
	}
}

// TestHandleModelCopy_ReachableThroughRegisteredMux proves the copy
// route is reachable through the REAL ServeMux registration surface:
// NO new mux pattern was added — the existing "/fe/api/models/"
// subtree (registered in RegisterHandlers) catches "{id}/copy", the
// same way it catches "{id}".
func TestHandleModelCopy_ReachableThroughRegisteredMux(t *testing.T) {
	ts := newModelTestServer()

	mux := http.NewServeMux()
	ts.RegisterHandlers(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	copySourceModel(ts)

	resp := postCopy(t, server, "copy-src", `{"new_id":"copy-dst"}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected status 201, got %d (body: %s)", resp.StatusCode, string(body))
	}
	if ts.mockModels.GetModel("copy-dst") == nil {
		t.Fatal("Copy was not added to the store")
	}
}
