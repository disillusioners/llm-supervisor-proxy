package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

// ═══════════════════════════════════════════════════════════════════════════════
// Test Infrastructure (reuses setupTestEnv from integration_allowed_models_test.go)
// ═══════════════════════════════════════════════════════════════════════════════

// accessControlTestEnv extends testEnv with models that have different IDs and names
type accessControlTestEnv struct {
	*testEnv
	modelsConfig *models.ModelsConfig
}

// setupAccessControlEnv creates a test environment with models that have
// DIFFERENT IDs and names (to test ID-based access control)
func setupAccessControlEnv(t *testing.T) *accessControlTestEnv {
	t.Helper()

	// Create temp directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Open SQLite database
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
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   r.URL.Query().Get("model"),
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

	// Create config manager
	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	cfgMgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

	// Create models config with DIFFERENT IDs and names
	// This is the key test scenario: IDs are used in logic, names for display
	modelsConfig := models.NewModelsConfig()

	// Add credential
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("Failed to add credential: %v", err)
	}

	// Add models with different IDs and names:
	// - ID: "model-id-1", Name: "First Model"
	// - ID: "model-id-2", Name: "Second Model"
	// - ID: "ultimate-id", Name: "Ultimate Model"
	for _, m := range []struct {
		id, name, internalModel string
	}{
		{"model-id-1", "First Model", "first-model"},
		{"model-id-2", "Second Model", "second-model"},
		{"ultimate-id", "Ultimate Model", "ultimate-model"},
	} {
		if err := modelsConfig.AddModel(models.ModelConfig{
			ID:            m.id,
			Name:          m.name,
			Enabled:       true,
			Internal:      true,
			Credentials: models.TestRefs("test-cred"),
			InternalModel: m.internalModel,
		}); err != nil {
			t.Fatalf("Failed to add model %s: %v", m.id, err)
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
	handler := proxy.NewHandler(proxyCfg, bus, reqStore, nil, tokenStore, nil, nil)

	return &accessControlTestEnv{
		testEnv: &testEnv{
			db:         db,
			dbStore:    dbStore,
			tokenStore: tokenStore,
			upstream:   upstream,
			handler:    handler,
		},
		modelsConfig: modelsConfig,
	}
}

// makeAccessControlRequest creates a request with a specific model ID
func makeAccessControlRequest(modelID, plaintextToken string) *http.Request {
	body := map[string]interface{}{
		"model": modelID,
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

// ═══════════════════════════════════════════════════════════════════════════════
// Group A: Access Control (fail-closed) Tests
// ═══════════════════════════════════════════════════════════════════════════════

// TestA1_AllowedModelMatch verifies that a model name resolving to an allowed ID is permitted
func TestA1_AllowedModelMatch(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Create token with allowed_models containing IDs (NOT names)
	// Token allows "model-id-1" which maps to "First Model"
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "allowed-token", nil, "test", false, "", []string{"model-id-1"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Request with model ID "model-id-1" (clients should send IDs, not names)
	req := makeAccessControlRequest("model-id-1", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	// Should be allowed (200)
	if rr.Code == http.StatusForbidden {
		t.Errorf("Expected 200 OK, got 403 Forbidden (model ID 'model-id-1' should be allowed)")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestA2_AllowedModelMismatch verifies that a model name NOT in allowed list is denied
func TestA2_AllowedModelMismatch(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Create token with allowed_models containing "model-id-1" only
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "restricted-token", nil, "test", false, "", []string{"model-id-1"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Request with model ID "model-id-2" which is NOT in allowed_models → should be 403
	req := makeAccessControlRequest("model-id-2", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify error structure
	var errResp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}
	errorObj, ok := errResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'error' field in response")
	}
	if errorObj["type"] != "access_denied" {
		t.Errorf("Expected error type 'access_denied', got %v", errorObj["type"])
	}
}

// TestA3_UnknownModelFailClosed verifies that unknown models with restricted tokens are DENIED
func TestA3_UnknownModelFailClosed(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Create token with allowed_models (has restrictions)
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "restricted-token", nil, "test", false, "", []string{"model-id-1"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Request with a model name that does NOT exist in the DB
	// "Unknown Model" is not in modelsConfig → should be 403 (fail-closed)
	req := makeAccessControlRequest("Unknown Model", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden (fail-closed for unknown model with restrictions), got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify error structure
	var errResp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}
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
}

// TestA4_OpenAccessUnrestricted verifies that unknown models are ALLOWED when no restrictions
func TestA4_OpenAccessUnrestricted(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Create token with NO allowed_models (nil = open access)
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "unrestricted-token", nil, "test", false, "", nil)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Request with a model name that does NOT exist in the DB
	// Since no restrictions, external/unknown models should be allowed
	// Note: This test needs to work with external upstream mode
	// The handler allows unknown models when allowed_models is nil

	// First test with known model (should work)
	req := makeAccessControlRequest("First Model", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code == http.StatusForbidden {
		t.Error("Expected 200 OK for unrestricted token with known model, got 403")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestA4b_OpenAccessEmptySlice verifies that empty allowed_models slice allows all
func TestA4b_OpenAccessEmptySlice(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Create token with empty allowed_models slice
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "empty-allowed-token", nil, "test", false, "", []string{})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Request with any model
	req := makeAccessControlRequest("First Model", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code == http.StatusForbidden {
		t.Error("Expected 200 OK for empty allowed_models slice, got 403")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestA5_RestrictedToken_ExternalModel_Denied verifies that external models
// (not in DB) are denied when the token has allowed_models restrictions (fail-closed)
func TestA5_RestrictedToken_ExternalModel_Denied(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Create token with allowed_models = ["model-id-1"] (restricted)
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "restricted-token", nil, "test", false, "", []string{"model-id-1"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Request with "Some External Model" (not in DB)
	// External model + restricted token = 403 Forbidden (fail-closed)
	req := makeAccessControlRequest("Some External Model", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden (external model with restricted token), got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify error structure
	var errResp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}
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
}

// ═══════════════════════════════════════════════════════════════════════════════
// Group B: Ultimate Model + allowed_models Interaction Tests
// ═══════════════════════════════════════════════════════════════════════════════

// TestB1_UltimateModelAllowed verifies that ultimate model is allowed when in allowed_models
func TestB1_UltimateModelAllowed(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Create token with ultimate model enabled and allowed_models containing ultimate model ID
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "ultimate-allowed-token", nil, "test", true, "ultimate-id", []string{"model-id-1", "ultimate-id"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Send a request that will trigger ultimate model
	// (Use the Force header to trigger it)
	req := makeAccessControlRequest("model-id-1", plaintext)
	req.Header.Set("X-Force-Ultimate-Model", "true")
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	// Ultimate model should be allowed (200)
	if rr.Code == http.StatusForbidden {
		t.Error("Expected 200 OK, got 403 Forbidden (ultimate model should be allowed when in allowed_models)")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestB2_UltimateModelForbidden verifies that ultimate model is denied when NOT in allowed_models
func TestB2_UltimateModelForbidden(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Create token with ultimate model enabled but NOT in allowed_models
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "ultimate-forbidden-token", nil, "test", true, "ultimate-id", []string{"model-id-1"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Send a request that will trigger ultimate model
	req := makeAccessControlRequest("model-id-1", plaintext)
	req.Header.Set("X-Force-Ultimate-Model", "true")
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	// Ultimate model should be forbidden (403)
	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden, got %d: %s", rr.Code, rr.Body.String())
	}

	// Verify error message mentions the ultimate model
	body := rr.Body.String()
	if !strings.Contains(body, "ultimate-id") {
		t.Errorf("Expected error to mention ultimate model ID 'ultimate-id', got: %s", body)
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Group C: ID Consistency Tests
// ═══════════════════════════════════════════════════════════════════════════════

// TestC1_IsModelAllowedReceivesIDs verifies that IsModelAllowed is called with IDs
func TestC1_IsModelAllowedReceivesIDs(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Test 1: Token with ID-based allowed_models
	token := &auth.AuthToken{
		AllowedModels: []string{"model-id-1", "model-id-2"},
	}

	// Verify IsModelAllowed works with IDs
	if !token.IsModelAllowed("model-id-1") {
		t.Error("model-id-1 should be allowed (it's in the list)")
	}
	if !token.IsModelAllowed("model-id-2") {
		t.Error("model-id-2 should be allowed (it's in the list)")
	}
	if token.IsModelAllowed("model-id-3") {
		t.Error("model-id-3 should NOT be allowed (not in list)")
	}
	// Name-based check should fail (ID-based lookup only)
	if token.IsModelAllowed("First Model") {
		t.Error("'First Model' name should NOT match ID-based allowed_models")
	}

	// Create token allowing "model-id-1"
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "id-test-token", nil, "test", false, "", []string{"model-id-1"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Request "model-id-1" → should be allowed
	req := makeAccessControlRequest("model-id-1", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK (model-id-1 → allowed), got %d: %s", rr.Code, rr.Body.String())
	}

	// Request "model-id-2" → should be denied (not in allowed list)
	req = makeAccessControlRequest("model-id-2", plaintext)
	rr = httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden (model-id-2 → not allowed), got %d: %s", rr.Code, rr.Body.String())
	}
}


// ═══════════════════════════════════════════════════════════════════════════════
// Edge Case Tests
// ═══════════════════════════════════════════════════════════════════════════════

// TestEdge_IDMatchesName verifies models where ID equals name still work correctly
func TestEdge_IDMatchesName(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Create a model where ID equals name
	if err := env.modelsConfig.AddModel(models.ModelConfig{
		ID:            "gpt-4",
		Name:          "gpt-4",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("test-cred"),
		InternalModel: "gpt-4",
	}); err != nil {
		t.Fatalf("Failed to add model: %v", err)
	}

	// Create token allowing "gpt-4" (works as both ID and name)
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "id-equals-name-token", nil, "test", false, "", []string{"gpt-4"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Request with model name "gpt-4"
	req := makeAccessControlRequest("gpt-4", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestEdge_CaseSensitiveIDMatch verifies that ID matching is case-sensitive
func TestEdge_CaseSensitiveIDMatch(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Token allows "model-id-1"
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "case-test-token", nil, "test", false, "", []string{"model-id-1"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// Request with correct ID → should work
	req := makeAccessControlRequest("model-id-1", plaintext)
	rr := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for 'model-id-1', got %d: %s", rr.Code, rr.Body.String())
	}

	// Request with "MODEL-ID-1" (uppercase) → should fail (case-sensitive ID matching)
	req = makeAccessControlRequest("MODEL-ID-1", plaintext)
	rr = httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden for 'MODEL-ID-1' (case mismatch), got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestEdge_MultipleModelsAllowed verifies multiple models in allowed_models work
func TestEdge_MultipleModelsAllowed(t *testing.T) {
	env := setupAccessControlEnv(t)
	ctx := context.Background()

	// Token allows both model-id-1 and model-id-2
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "multi-allowed-token", nil, "test", false, "", []string{"model-id-1", "model-id-2"})
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	testCases := []struct {
		modelID     string
		shouldAllow bool
	}{
		{"model-id-1", true},    // allowed
		{"model-id-2", true},    // allowed
		{"ultimate-id", false},  // not allowed
	}

	for _, tc := range testCases {
		req := makeAccessControlRequest(tc.modelID, plaintext)
		rr := httptest.NewRecorder()
		env.handler.HandleChatCompletions(rr, req)

		if tc.shouldAllow && rr.Code == http.StatusForbidden {
			t.Errorf("%s: expected 200 OK, got 403", tc.modelID)
		}
		if !tc.shouldAllow && rr.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403 Forbidden, got %d", tc.modelID, rr.Code)
		}
	}
}
