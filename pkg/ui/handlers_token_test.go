package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
)

// mockTokenStore implements auth.TokenStoreInterface for testing
type mockTokenStore struct {
	tokens        map[string]*auth.AuthToken
	updateErr     error
	createTokenFn func(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) (string, *auth.AuthToken, error)
}

func newMockTokenStore() *mockTokenStore {
	return &mockTokenStore{
		tokens: make(map[string]*auth.AuthToken),
	}
}

func (m *mockTokenStore) AddToken(token *auth.AuthToken) {
	m.tokens[token.ID] = token
}

func (m *mockTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error) {
	return nil, nil
}

func (m *mockTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) (string, *auth.AuthToken, error) {
	if m.createTokenFn != nil {
		return m.createTokenFn(ctx, name, expiresAt, createdBy, ultimateModelEnabled, ultimateModelID, allowedModels)
	}
	return "", nil, nil
}

func (m *mockTokenStore) DeleteToken(ctx context.Context, id string) error {
	if _, ok := m.tokens[id]; !ok {
		return auth.ErrTokenNotFound
	}
	delete(m.tokens, id)
	return nil
}

func (m *mockTokenStore) ListTokens(ctx context.Context) ([]auth.AuthToken, error) {
	var result []auth.AuthToken
	for _, t := range m.tokens {
		result = append(result, *t)
	}
	return result, nil
}

func (m *mockTokenStore) GetTokenByID(ctx context.Context, id string) (*auth.AuthToken, error) {
	token, ok := m.tokens[id]
	if !ok {
		return nil, auth.ErrTokenNotFound
	}
	return token, nil
}

func (m *mockTokenStore) UpdateTokenPermission(ctx context.Context, id string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	token, ok := m.tokens[id]
	if !ok {
		return auth.ErrTokenNotFound
	}
	token.UltimateModelEnabled = ultimateModelEnabled
	token.UltimateModelID = ultimateModelID
	token.AllowedModels = allowedModels
	return nil
}

// tokenTestServer creates a Server with a mock token store for testing
type tokenTestServer struct {
	*Server
	mockStore *mockTokenStore
}

func newTokenTestServer() *tokenTestServer {
	mockStore := newMockTokenStore()

	s := &Server{
		tokenStore: mockStore,
	}

	return &tokenTestServer{
		Server:    s,
		mockStore: mockStore,
	}
}

// TestHandleTokenDetail_Patch_SuccessToggleOn tests successful PATCH to enable permission
func TestHandleTokenDetail_Patch_SuccessToggleOn(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:                   "test-token-id",
		Name:                 "Test Token",
		UltimateModelEnabled: false,
	}
	ts.mockStore.AddToken(token)

	// PATCH to enable ultimate model
	body := `{"ultimate_model_enabled": true}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify response
	var resp map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp["success"] {
		t.Error("expected success = true in response")
	}

	// Verify token was updated in store
	if !ts.mockStore.tokens["test-token-id"].UltimateModelEnabled {
		t.Error("expected token ultimate_model_enabled = true after PATCH")
	}
}

// TestHandleTokenDetail_Patch_SuccessToggleOff tests successful PATCH to disable permission
func TestHandleTokenDetail_Patch_SuccessToggleOff(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token with ultimate_model_enabled = true
	token := &auth.AuthToken{
		ID:                   "test-token-id",
		Name:                 "Test Token",
		UltimateModelEnabled: true,
	}
	ts.mockStore.AddToken(token)

	// PATCH to disable ultimate model
	body := `{"ultimate_model_enabled": false}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify response
	var resp map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp["success"] {
		t.Error("expected success = true in response")
	}

	// Verify token was updated in store
	if ts.mockStore.tokens["test-token-id"].UltimateModelEnabled {
		t.Error("expected token ultimate_model_enabled = false after PATCH")
	}
}

// TestHandleTokenDetail_Patch_SuccessToggleBothDirections tests toggling permission on then off
func TestHandleTokenDetail_Patch_SuccessToggleBothDirections(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token with ultimate_model_enabled = false
	token := &auth.AuthToken{
		ID:                   "test-token-id",
		Name:                 "Test Token",
		UltimateModelEnabled: false,
	}
	ts.mockStore.AddToken(token)

	// First, toggle ON
	body := `{"ultimate_model_enabled": true}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("first PATCH: expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	if !ts.mockStore.tokens["test-token-id"].UltimateModelEnabled {
		t.Error("after first PATCH: expected token ultimate_model_enabled = true")
	}

	// Second, toggle OFF
	body = `{"ultimate_model_enabled": false}`
	req = httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("second PATCH: expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	if ts.mockStore.tokens["test-token-id"].UltimateModelEnabled {
		t.Error("after second PATCH: expected token ultimate_model_enabled = false")
	}
}

// TestHandleTokenDetail_Patch_NotFound tests PATCH with non-existent token ID
func TestHandleTokenDetail_Patch_NotFound(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// PATCH non-existent token
	body := `{"ultimate_model_enabled": true}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/nonexistent-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleTokenDetail_Patch_InvalidJSON tests PATCH with malformed JSON body
func TestHandleTokenDetail_Patch_InvalidJSON(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:                   "test-token-id",
		Name:                 "Test Token",
		UltimateModelEnabled: false,
	}
	ts.mockStore.AddToken(token)

	// PATCH with invalid JSON
	body := `{invalid json}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleTokenDetail_Patch_EmptyBody tests PATCH with empty body
func TestHandleTokenDetail_Patch_EmptyBody(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:                   "test-token-id",
		Name:                 "Test Token",
		UltimateModelEnabled: false,
	}
	ts.mockStore.AddToken(token)

	// PATCH with empty body
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(""))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleTokenDetail_Patch_ContentType tests PATCH response has correct content type
func TestHandleTokenDetail_Patch_ContentType(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:                   "test-token-id",
		Name:                 "Test Token",
		UltimateModelEnabled: false,
	}
	ts.mockStore.AddToken(token)

	body := `{"ultimate_model_enabled": true}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("expected Content-Type = 'application/json', got %s", contentType)
	}
}

// TestHandleTokenDetail_Patch_MissingID tests PATCH with missing token ID
func TestHandleTokenDetail_Patch_MissingID(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// PATCH with empty ID path
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/", strings.NewReader(`{"ultimate_model_enabled": true}`))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleTokenDetail_Patch_TokenStoreNotConfigured tests PATCH when token store is nil
func TestHandleTokenDetail_Patch_TokenStoreNotConfigured(t *testing.T) {
	s := &Server{tokenStore: nil}
	ctx := context.Background()

	body := `{"ultimate_model_enabled": true}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/test-token-id", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleTokenDetail(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

// TestHandleTokenDetail_Patch_MethodNotAllowed tests that only PATCH and DELETE are allowed
func TestHandleTokenDetail_Patch_MethodNotAllowed(t *testing.T) {
	ts := newTokenTestServer()

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut}
	for _, method := range methods {
		req := httptest.NewRequest(method, "/fe/api/tokens/test-token-id", nil)
		w := httptest.NewRecorder()

		ts.handleTokenDetail(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("method %s: expected status 405, got %d", method, w.Code)
		}
	}
}

// TestHandleTokenDetail_Delete_Success tests successful DELETE
func TestHandleTokenDetail_Delete_Success(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:                   "test-token-id",
		Name:                 "Test Token",
		UltimateModelEnabled: false,
	}
	ts.mockStore.AddToken(token)

	req := httptest.NewRequest(http.MethodDelete, "/fe/api/tokens/test-token-id", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d: %s", w.Code, w.Body.String())
	}

	// Verify token was deleted
	if _, ok := ts.mockStore.tokens["test-token-id"]; ok {
		t.Error("expected token to be deleted")
	}
}

// TestHandleTokenDetail_Delete_NotFound tests DELETE with non-existent token ID
func TestHandleTokenDetail_Delete_NotFound(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	req := httptest.NewRequest(http.MethodDelete, "/fe/api/tokens/nonexistent-id", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Ultimate Model ID API Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestHandleTokenDetail_Patch_UltimateModel_SetValue tests PATCH to set ultimate_model
func TestHandleTokenDetail_Patch_UltimateModel_SetValue(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:                   "ultimate-model-token",
		Name:                 "Test Token",
		UltimateModelEnabled: true,
		UltimateModelID:      "", // Initially empty
	}
	ts.mockStore.AddToken(token)

	// PATCH to set ultimate_model
	body := `{"ultimate_model": "gpt-4o"}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/ultimate-model-token", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify token was updated in store
	if ts.mockStore.tokens["ultimate-model-token"].UltimateModelID != "gpt-4o" {
		t.Errorf("expected token ultimate_model = %q, got %q", "gpt-4o", ts.mockStore.tokens["ultimate-model-token"].UltimateModelID)
	}
}

// TestHandleTokenDetail_Patch_UltimateModel_Clear tests PATCH to clear ultimate_model
func TestHandleTokenDetail_Patch_UltimateModel_Clear(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token with ultimate_model already set
	token := &auth.AuthToken{
		ID:                   "clear-ultimate-token",
		Name:                 "Test Token",
		UltimateModelEnabled: true,
		UltimateModelID:      "gpt-4o", // Already set
	}
	ts.mockStore.AddToken(token)

	// PATCH with empty string to clear
	body := `{"ultimate_model": ""}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/clear-ultimate-token", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify token ultimate_model was cleared
	if ts.mockStore.tokens["clear-ultimate-token"].UltimateModelID != "" {
		t.Errorf("expected token ultimate_model = %q (cleared), got %q", "", ts.mockStore.tokens["clear-ultimate-token"].UltimateModelID)
	}
}

// TestHandleTokenDetail_Patch_UltimateModel_KeepExisting tests PATCH without ultimate_model keeps existing
func TestHandleTokenDetail_Patch_UltimateModel_KeepExisting(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token with ultimate_model already set
	token := &auth.AuthToken{
		ID:                   "keep-ultimate-token",
		Name:                 "Test Token",
		UltimateModelEnabled: true,
		UltimateModelID:      "gpt-4o", // Already set
	}
	ts.mockStore.AddToken(token)

	// PATCH with only ultimate_model_enabled (no ultimate_model field)
	body := `{"ultimate_model_enabled": true}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/keep-ultimate-token", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify token ultimate_model was preserved
	if ts.mockStore.tokens["keep-ultimate-token"].UltimateModelID != "gpt-4o" {
		t.Errorf("expected token ultimate_model = %q (preserved), got %q", "gpt-4o", ts.mockStore.tokens["keep-ultimate-token"].UltimateModelID)
	}
}

// TestHandleTokenDetail_Patch_UltimateModel_FullUpdate tests PATCH with all fields
func TestHandleTokenDetail_Patch_UltimateModel_FullUpdate(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Create a token in the mock store
	token := &auth.AuthToken{
		ID:                   "full-update-token",
		Name:                 "Test Token",
		UltimateModelEnabled: false,
		UltimateModelID:      "",
		AllowedModels:        nil,
	}
	ts.mockStore.AddToken(token)

	// PATCH with all fields
	body := `{"ultimate_model_enabled": true, "ultimate_model": "claude-3-opus", "allowed_models": ["gpt-4o", "gpt-4"]}`
	req := httptest.NewRequest(http.MethodPatch, "/fe/api/tokens/full-update-token", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.handleTokenDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify all fields were updated
	updated := ts.mockStore.tokens["full-update-token"]
	if !updated.UltimateModelEnabled {
		t.Error("expected ultimate_model_enabled = true")
	}
	if updated.UltimateModelID != "claude-3-opus" {
		t.Errorf("expected ultimate_model = %q, got %q", "claude-3-opus", updated.UltimateModelID)
	}
	if len(updated.AllowedModels) != 2 || updated.AllowedModels[0] != "gpt-4o" {
		t.Errorf("expected allowed_models = %v, got %v", []string{"gpt-4o", "gpt-4"}, updated.AllowedModels)
	}
}

// TestHandleTokens_CreateWithUltimateModel tests POST with ultimate_model
func TestHandleTokens_CreateWithUltimateModel(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Configure the mock to return proper values
	ts.mockStore.createTokenFn = func(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) (string, *auth.AuthToken, error) {
		plaintext := "sk-test-token-123456789012345678901234567890123456789012345678901234"
		token := &auth.AuthToken{
			ID:                   "new-token-id",
			Name:                 name,
			UltimateModelEnabled: ultimateModelEnabled,
			UltimateModelID:      ultimateModelID,
			AllowedModels:        allowedModels,
			CreatedBy:            createdBy,
			CreatedAt:            time.Now(),
		}
		return plaintext, token, nil
	}

	// POST with ultimate_model
	body := `{"name": "test-ultimate", "ultimate_model_enabled": true, "ultimate_model": "gpt-4o"}`
	req := httptest.NewRequest(http.MethodPost, "/fe/api/tokens", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.createToken(ctx, w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	// Parse response
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify ultimate_model in response
	if resp["ultimate_model"] != "gpt-4o" {
		t.Errorf("expected ultimate_model = %q, got %v", "gpt-4o", resp["ultimate_model"])
	}
}

// TestHandleTokens_CreateWithNilUltimateModel tests POST without ultimate_model field
func TestHandleTokens_CreateWithNilUltimateModel(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Configure the mock to return proper values
	ts.mockStore.createTokenFn = func(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) (string, *auth.AuthToken, error) {
		plaintext := "sk-test-token-123456789012345678901234567890123456789012345678901234"
		token := &auth.AuthToken{
			ID:                   "new-token-id",
			Name:                 name,
			UltimateModelEnabled: ultimateModelEnabled,
			UltimateModelID:      ultimateModelID,
			AllowedModels:        allowedModels,
			CreatedBy:            createdBy,
			CreatedAt:            time.Now(),
		}
		return plaintext, token, nil
	}

	// POST without ultimate_model field
	body := `{"name": "test-no-ultimate"}`
	req := httptest.NewRequest(http.MethodPost, "/fe/api/tokens", strings.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	ts.createToken(ctx, w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d: %s", w.Code, w.Body.String())
	}

	// Parse response
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Verify ultimate_model is empty string
	if resp["ultimate_model"] != "" {
		t.Errorf("expected ultimate_model = %q (empty), got %v", "", resp["ultimate_model"])
	}
}

// TestListTokens_ResponseIncludesUltimateModel tests GET list includes ultimate_model
func TestListTokens_ResponseIncludesUltimateModel(t *testing.T) {
	ts := newTokenTestServer()
	ctx := context.Background()

	// Add tokens with and without ultimate_model
	ts.mockStore.AddToken(&auth.AuthToken{
		ID:                   "token-with-ultimate",
		Name:                 "Token With Ultimate",
		UltimateModelEnabled: true,
		UltimateModelID:      "gpt-4o",
	})
	ts.mockStore.AddToken(&auth.AuthToken{
		ID:                   "token-without-ultimate",
		Name:                 "Token Without Ultimate",
		UltimateModelEnabled: false,
		UltimateModelID:      "",
	})

	req := httptest.NewRequest(http.MethodGet, "/fe/api/tokens", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	ts.listTokens(ctx, w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Parse response
	var tokens []map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&tokens); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}

	// Find each token and verify ultimate_model
	found := make(map[string]string)
	for _, token := range tokens {
		name := token["name"].(string)
		ultimateModel := token["ultimate_model"].(string)
		found[name] = ultimateModel
	}

	if found["Token With Ultimate"] != "gpt-4o" {
		t.Errorf("expected Token With Ultimate ultimate_model = %q, got %q", "gpt-4o", found["Token With Ultimate"])
	}

	if found["Token Without Ultimate"] != "" {
		t.Errorf("expected Token Without Ultimate ultimate_model = %q, got %q", "", found["Token Without Ultimate"])
	}
}
