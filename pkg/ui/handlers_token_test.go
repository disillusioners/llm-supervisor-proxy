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
	tokens    map[string]*auth.AuthToken
	updateErr error
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

func (m *mockTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool) (string, *auth.AuthToken, error) {
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

func (m *mockTokenStore) UpdateTokenPermission(ctx context.Context, id string, ultimateModelEnabled bool) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	token, ok := m.tokens[id]
	if !ok {
		return auth.ErrTokenNotFound
	}
	token.UltimateModelEnabled = ultimateModelEnabled
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
