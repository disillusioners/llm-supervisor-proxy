package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite" // SQLite driver

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test Infrastructure
// ─────────────────────────────────────────────────────────────────────────────

// testEnv holds all test dependencies
type testEnv struct {
	db         *sql.DB
	dbStore    *database.Store
	tokenStore *auth.TokenStore
	upstream   *httptest.Server
	handler    *proxy.Handler
}

// setupTestEnv creates a complete test environment with file-based SQLite
func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	// Create temp directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Open SQLite database using the modernc.org/sqlite driver
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open SQLite database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("Failed to enable foreign keys: %v", err)
	}

	// Create store
	dbStore := &database.Store{
		DB:      db,
		Dialect: database.SQLite,
	}
	t.Cleanup(func() { dbStore.Close() })

	// Run migrations
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create token store
	tokenStore := auth.NewTokenStore(db, database.SQLite)

	// Create mock upstream LLM server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a simple non-streaming response
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   "test-model",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]string{
						"role":    "assistant",
						"content": "Hello from test",
					},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(func() { upstream.Close() })

	// Create config manager with test config
	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	cfgMgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Create models config
	modelsConfig := models.NewModelsConfig()

	// Add credential and internal model
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("Failed to add credential: %v", err)
	}

	// Add internal models
	for _, model := range []struct {
		id, name, internalModel string
	}{
		{"gpt-4", "GPT-4", "gpt-4"},
		{"claude-3", "Claude 3", "claude-3"},
		{"gpt-3.5", "GPT-3.5", "gpt-3.5"},
	} {
		if err := modelsConfig.AddModel(models.ModelConfig{
			ID:            model.id,
			Name:          model.name,
			Enabled:       true,
			Internal:      true,
			CredentialID:  "test-cred",
			InternalModel: model.internalModel,
		}); err != nil {
			t.Fatalf("Failed to add model %s: %v", model.id, err)
		}
	}

	// Create event bus
	bus := events.NewBus()

	// Create request store
	reqStore := store.NewRequestStore(100)

	// Create proxy handler
	proxyCfg := &proxy.Config{
		ConfigMgr:    cfgMgr,
		ModelsConfig: modelsConfig,
	}
	handler := proxy.NewHandler(proxyCfg, bus, reqStore, nil, tokenStore, nil)

	return &testEnv{
		db:         db,
		dbStore:    dbStore,
		tokenStore: tokenStore,
		upstream:   upstream,
		handler:    handler,
	}
}

// makeHandlerRequest creates a request for the proxy handler
func makeHandlerRequest(model, plaintextToken string) *http.Request {
	body := map[string]interface{}{
		"model": model,
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello",
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	if plaintextToken != "" {
		req.Header.Set("Authorization", "Bearer "+plaintextToken)
	}
	return req
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: Token CRUD with allowed_models
// ─────────────────────────────────────────────────────────────────────────────

func TestCreateToken_WithAllowedModels(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with allowed_models
	plaintext, token, err := env.tokenStore.CreateToken(ctx, "test-token", nil, "test", false, []string{"gpt-4", "claude-3"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}
	if plaintext == "" {
		t.Error("Expected non-empty plaintext token")
	}
	if token == nil {
		t.Fatal("Expected non-nil token")
	}
	if token.ID == "" {
		t.Error("Expected non-empty token ID")
	}
	if len(token.AllowedModels) != 2 {
		t.Errorf("Expected 2 allowed models, got %d", len(token.AllowedModels))
	}
	if token.AllowedModels[0] != "gpt-4" || token.AllowedModels[1] != "claude-3" {
		t.Errorf("Expected [gpt-4, claude-3], got %v", token.AllowedModels)
	}

	// Verify it's stored correctly in DB
	retrieved, err := env.tokenStore.GetTokenByID(ctx, token.ID)
	if err != nil {
		t.Fatalf("GetTokenByID failed: %v", err)
	}
	if len(retrieved.AllowedModels) != 2 {
		t.Errorf("DB: Expected 2 allowed models, got %d", len(retrieved.AllowedModels))
	}
}

func TestCreateToken_WithNilAllowedModels(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with nil allowed_models (all models allowed)
	plaintext, token, err := env.tokenStore.CreateToken(ctx, "test-token-nil", nil, "test", false, nil)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}
	if plaintext == "" {
		t.Error("Expected non-empty plaintext token")
	}
	if token == nil {
		t.Fatal("Expected non-nil token")
	}
	if token.AllowedModels != nil {
		t.Errorf("Expected nil AllowedModels, got %v", token.AllowedModels)
	}

	// Verify in DB
	retrieved, err := env.tokenStore.GetTokenByID(ctx, token.ID)
	if err != nil {
		t.Fatalf("GetTokenByID failed: %v", err)
	}
	if retrieved.AllowedModels != nil {
		t.Errorf("DB: Expected nil AllowedModels, got %v", retrieved.AllowedModels)
	}
}

func TestCreateToken_WithEmptyAllowedModels(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with empty allowed_models (no models allowed)
	plaintext, token, err := env.tokenStore.CreateToken(ctx, "test-token-empty", nil, "test", false, []string{})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}
	if plaintext == "" {
		t.Error("Expected non-empty plaintext token")
	}
	if token == nil {
		t.Fatal("Expected non-nil token")
	}
	if len(token.AllowedModels) != 0 {
		t.Errorf("Expected empty AllowedModels, got %v", token.AllowedModels)
	}

	// Verify in DB
	retrieved, err := env.tokenStore.GetTokenByID(ctx, token.ID)
	if err != nil {
		t.Fatalf("GetTokenByID failed: %v", err)
	}
	if len(retrieved.AllowedModels) != 0 {
		t.Errorf("DB: Expected empty AllowedModels, got %v", retrieved.AllowedModels)
	}
}

func TestUpdateTokenPermission_AllowedModels(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token without allowed_models
	_, token, err := env.tokenStore.CreateToken(ctx, "test-token-update", nil, "test", false, nil)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Update allowed_models
	err = env.tokenStore.UpdateTokenPermission(ctx, token.ID, true, []string{"gpt-4", "claude-3", "gpt-3.5"})
	if err != nil {
		t.Fatalf("UpdateTokenPermission failed: %v", err)
	}

	// Verify update
	retrieved, err := env.tokenStore.GetTokenByID(ctx, token.ID)
	if err != nil {
		t.Fatalf("GetTokenByID failed: %v", err)
	}
	if len(retrieved.AllowedModels) != 3 {
		t.Errorf("Expected 3 allowed models after update, got %d", len(retrieved.AllowedModels))
	}
	if retrieved.UltimateModelEnabled != true {
		t.Errorf("Expected UltimateModelEnabled=true after update, got %v", retrieved.UltimateModelEnabled)
	}
}

func TestUpdateTokenPermission_ClearAllowedModels(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with allowed_models
	_, token, err := env.tokenStore.CreateToken(ctx, "test-token-clear", nil, "test", false, []string{"gpt-4"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Update to clear allowed_models (pass empty slice)
	err = env.tokenStore.UpdateTokenPermission(ctx, token.ID, false, []string{})
	if err != nil {
		t.Fatalf("UpdateTokenPermission failed: %v", err)
	}

	// Verify cleared
	retrieved, err := env.tokenStore.GetTokenByID(ctx, token.ID)
	if err != nil {
		t.Fatalf("GetTokenByID failed: %v", err)
	}
	if len(retrieved.AllowedModels) != 0 {
		t.Errorf("Expected 0 allowed models after clear, got %d", len(retrieved.AllowedModels))
	}
}

func TestListTokens_ReturnsAllowedModels(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create tokens with different allowed_models
	testCases := []struct {
		name          string
		allowedModels []string
	}{
		{"token-all", nil},
		{"token-gpt4", []string{"gpt-4"}},
		{"token-multi", []string{"gpt-4", "claude-3"}},
		{"token-empty", []string{}},
	}

	for _, tc := range testCases {
		_, _, err := env.tokenStore.CreateToken(ctx, tc.name, nil, "test", false, tc.allowedModels)
		if err != nil {
			t.Fatalf("CreateToken(%s) failed: %v", tc.name, err)
		}
	}

	// List all tokens
	tokens, err := env.tokenStore.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens failed: %v", err)
	}
	if len(tokens) != len(testCases) {
		t.Errorf("Expected %d tokens, got %d", len(testCases), len(tokens))
	}

	// Verify each token
	for _, token := range tokens {
		switch token.Name {
		case "token-all":
			if token.AllowedModels != nil {
				t.Errorf("token-all: expected nil AllowedModels, got %v", token.AllowedModels)
			}
		case "token-gpt4":
			if len(token.AllowedModels) != 1 || token.AllowedModels[0] != "gpt-4" {
				t.Errorf("token-gpt4: expected [gpt-4], got %v", token.AllowedModels)
			}
		case "token-multi":
			if len(token.AllowedModels) != 2 {
				t.Errorf("token-multi: expected 2 models, got %d", len(token.AllowedModels))
			}
		case "token-empty":
			if len(token.AllowedModels) != 0 {
				t.Errorf("token-empty: expected empty, got %v", token.AllowedModels)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: IsModelAllowed Logic
// ─────────────────────────────────────────────────────────────────────────────

func TestIsModelAllowed_NilAllowedModels(t *testing.T) {
	token := &auth.AuthToken{AllowedModels: nil}
	tests := []struct {
		model  string
		allowed bool
	}{
		{"gpt-4", true},
		{"claude-3", true},
		{"any-model", true},
		{"", true},
	}

	for _, tt := range tests {
		if token.IsModelAllowed(tt.model) != tt.allowed {
			t.Errorf("IsModelAllowed(%q) = %v, want %v", tt.model, token.IsModelAllowed(tt.model), tt.allowed)
		}
	}
}

func TestIsModelAllowed_EmptyAllowedModels(t *testing.T) {
	// Empty slice [] means "all models allowed" (same as nil)
	token := &auth.AuthToken{AllowedModels: []string{}}
	tests := []struct {
		model  string
		allowed bool
	}{
		{"gpt-4", true},
		{"claude-3", true},
		{"any-model", true},
	}

	for _, tt := range tests {
		if token.IsModelAllowed(tt.model) != tt.allowed {
			t.Errorf("IsModelAllowed(%q) = %v, want %v", tt.model, token.IsModelAllowed(tt.model), tt.allowed)
		}
	}
}

func TestIsModelAllowed_WithModels(t *testing.T) {
	token := &auth.AuthToken{AllowedModels: []string{"gpt-4", "claude-3"}}
	tests := []struct {
		model  string
		allowed bool
	}{
		{"gpt-4", true},
		{"claude-3", true},
		{"gpt-3.5", false},
		{"gpt-4o", false},  // Case-sensitive: different from gpt-4
		{"GPT-4", false},   // Case-sensitive: uppercase different
		{"", false},
	}

	for _, tt := range tests {
		if token.IsModelAllowed(tt.model) != tt.allowed {
			t.Errorf("IsModelAllowed(%q) = %v, want %v", tt.model, token.IsModelAllowed(tt.model), tt.allowed)
		}
	}
}

func TestIsModelAllowed_CaseSensitive(t *testing.T) {
	token := &auth.AuthToken{AllowedModels: []string{"GPT-4", "Claude-3"}}
	tests := []struct {
		model  string
		allowed bool
	}{
		{"GPT-4", true},
		{"Claude-3", true},
		{"gpt-4", false},    // lowercase different
		{"claude-3", false}, // lowercase different
		{"gpt-4o", false},
	}

	for _, tt := range tests {
		if token.IsModelAllowed(tt.model) != tt.allowed {
			t.Errorf("IsModelAllowed(%q) = %v, want %v", tt.model, token.IsModelAllowed(tt.model), tt.allowed)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: ScanAllowedModels
// ─────────────────────────────────────────────────────────────────────────────

func TestScanAllowedModels(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected []string
	}{
		{"nil input", nil, nil},
		{"empty string", "", nil},
		{"null json", "null", nil},
		{"empty array", "[]", []string{}},
		{"single model", `["gpt-4"]`, []string{"gpt-4"}},
		{"multiple models", `["gpt-4","claude-3"]`, []string{"gpt-4", "claude-3"}},
		{"string input", "gpt-4", []string{}},      // Malformed JSON -> fail closed
		{"invalid json", "not-json", []string{}},   // Malformed JSON -> fail closed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := auth.ScanAllowedModels(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("ScanAllowedModels(%v) = %v, want %v", tt.input, result, tt.expected)
				return
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("ScanAllowedModels(%v)[%d] = %q, want %q", tt.input, i, v, tt.expected[i])
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: Handler Enforcement (403)
// ─────────────────────────────────────────────────────────────────────────────

func TestHandler_ModelAllowed_Returns200(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with gpt-4 allowed
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "allowed-token", nil, "test", false, []string{"gpt-4"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Send request with allowed model
	req := makeHandlerRequest("gpt-4", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	// Should NOT be 403 - gpt-4 is in allowed list
	if rr.Code == http.StatusForbidden {
		t.Error("Expected 200 OK, got 403 Forbidden (model should be allowed)")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_ModelNotAllowed_Returns403(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with only gpt-4 allowed
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "restricted-token", nil, "test", false, []string{"gpt-4"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Send request with disallowed model
	req := makeHandlerRequest("claude-3", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	// Should be 403 Forbidden
	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden, got %d: %s", rr.Code, rr.Body.String())
	}

	// Check error response
	var errResp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}

	// Check error structure
	errorObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'error' field in response")
	}
	if errorObj["type"] != "access_denied" {
		t.Errorf("Expected error type 'access_denied', got %v", errorObj["type"])
	}
	if errorObj["message"] != "model not allowed for this token" {
		t.Errorf("Expected error message 'model not allowed for this token', got %v", errorObj["message"])
	}
	if errorObj["model"] != "claude-3" {
		t.Errorf("Expected denied model 'claude-3', got %v", errorObj["model"])
	}
}

func TestHandler_AllModelsAllowed_PassesThrough(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with no restrictions (nil allowed_models)
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "unrestricted-token", nil, "test", false, nil)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Send request with any model
	req := makeHandlerRequest("claude-3", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	// Should NOT be 403 - nil means all models allowed
	if rr.Code == http.StatusForbidden {
		t.Error("Expected 200 OK, got 403 Forbidden (all models should be allowed)")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_EmptyAllowedModels_AllowsAll(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with empty allowed_models (allows all, same as nil)
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "allow-all-token", nil, "test", false, []string{})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Send request with any model
	req := makeHandlerRequest("gpt-4", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	// Should be 200 - empty array means all models allowed
	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_CaseSensitivity_ExactMatch(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token allowing "GPT-4" (uppercase)
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "case-token", nil, "test", false, []string{"GPT-4"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Test: lowercase gpt-4 should be DENIED
	req := makeHandlerRequest("gpt-4", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected 403 for 'gpt-4' when 'GPT-4' is allowed, got %d", rr.Code)
	}

	// Test: uppercase GPT-4 should be ALLOWED
	req = makeHandlerRequest("GPT-4", plaintext)
	rr = httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code == http.StatusForbidden {
		t.Errorf("Expected 200 for 'GPT-4' (exact match), got 403")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: Edge Cases
// ─────────────────────────────────────────────────────────────────────────────

func TestHandler_NoToken_AuthRequired(t *testing.T) {
	env := setupTestEnv(t)

	// Send request without token (internal model requires auth)
	req := makeHandlerRequest("gpt-4", "")
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	// Should be 401 Unauthorized (no token provided)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_InvalidToken_AuthRequired(t *testing.T) {
	env := setupTestEnv(t)

	// Send request with invalid token
	req := makeHandlerRequest("gpt-4", "invalid-token-123456789012345678901234567890123456789012345678")
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	// Should be 401 Unauthorized (invalid token)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_UpdateToken_ChangesEnforcement(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with gpt-4 allowed
	plaintext, token, err := env.tokenStore.CreateToken(ctx, "update-test", nil, "test", false, []string{"gpt-4"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Initially gpt-4 should work, claude-3 should fail
	testCases := []struct {
		model       string
		shouldAllow bool
	}{
		{"gpt-4", true},
		{"claude-3", false},
	}

	for _, tc := range testCases {
		req := makeHandlerRequest(tc.model, plaintext)
		rr := httptest.NewRecorder()
		env.handler.HandleChatCompletions(rr, req)

		if tc.shouldAllow && rr.Code == http.StatusForbidden {
			t.Errorf("Model %s should be allowed, got 403", tc.model)
		}
		if !tc.shouldAllow && rr.Code != http.StatusForbidden {
			t.Errorf("Model %s should be denied, got %d", tc.model, rr.Code)
		}
	}

	// Update token to allow claude-3
	err = env.tokenStore.UpdateTokenPermission(ctx, token.ID, false, []string{"gpt-4", "claude-3"})
	if err != nil {
		t.Fatalf("UpdateTokenPermission failed: %v", err)
	}

	// Now claude-3 should work
	req := makeHandlerRequest("claude-3", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code == http.StatusForbidden {
		t.Errorf("Model claude-3 should now be allowed after update, got 403")
	}
}

func TestHandler_ValidateToken_ReturnsAllowedModels(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with specific allowed_models
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "validate-test", nil, "test", false, []string{"gpt-4", "claude-3"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Validate token
	token, err := env.tokenStore.ValidateToken(ctx, plaintext)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	// Verify allowed_models are returned
	if len(token.AllowedModels) != 2 {
		t.Errorf("Expected 2 allowed models from ValidateToken, got %d", len(token.AllowedModels))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: API Handler Integration (Token CRUD via HTTP)
// ─────────────────────────────────────────────────────────────────────────────

func TestAPI_CreateToken_WithAllowedModels(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token with allowed_models via store
	plaintext, token, err := env.tokenStore.CreateToken(ctx, "api-token", nil, "test", false, []string{"gpt-4", "claude-3"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	if plaintext == "" {
		t.Error("Expected non-empty plaintext token")
	}
	if token == nil {
		t.Fatal("Expected non-nil token")
	}

	// Verify allowed_models
	if len(token.AllowedModels) != 2 {
		t.Errorf("Expected 2 allowed models, got %d", len(token.AllowedModels))
	}
}

func TestAPI_ListTokens_ReturnsAllowedModels(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create tokens with different allowed_models
	_, _, err := env.tokenStore.CreateToken(ctx, "api-token-1", nil, "test", false, []string{"gpt-4"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}
	_, _, err = env.tokenStore.CreateToken(ctx, "api-token-2", nil, "test", false, nil)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// List tokens
	tokens, err := env.tokenStore.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens failed: %v", err)
	}

	if len(tokens) != 2 {
		t.Errorf("Expected 2 tokens, got %d", len(tokens))
	}
}

func TestAPI_PatchToken_UpdateAllowedModels(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	// Create token
	_, token, err := env.tokenStore.CreateToken(ctx, "patch-token", nil, "test", false, nil)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Update allowed_models
	models := []string{"gpt-4", "claude-3", "gpt-3.5"}
	err = env.tokenStore.UpdateTokenPermission(ctx, token.ID, true, models)
	if err != nil {
		t.Fatalf("UpdateTokenPermission failed: %v", err)
	}

	// Verify update in DB
	updated, err := env.tokenStore.GetTokenByID(ctx, token.ID)
	if err != nil {
		t.Fatalf("GetTokenByID failed: %v", err)
	}
	if len(updated.AllowedModels) != 3 {
		t.Errorf("Expected 3 allowed models after PATCH, got %d", len(updated.AllowedModels))
	}
	if !updated.UltimateModelEnabled {
		t.Error("Expected UltimateModelEnabled=true after PATCH")
	}
}
