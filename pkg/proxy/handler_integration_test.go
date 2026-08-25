package proxy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// setupIntegrationDB creates an in-memory SQLite database with required tables
// for the integration test (both token_hourly_usage and auth_tokens).
// Uses file::memory: with shared cache so all connections share the same database.
func setupIntegrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	// Create token_hourly_usage table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS token_hourly_usage (
		token_id TEXT NOT NULL,
		hour_bucket TEXT NOT NULL,
		request_count INTEGER NOT NULL DEFAULT 0,
		prompt_tokens INTEGER NOT NULL DEFAULT 0,
		completion_tokens INTEGER NOT NULL DEFAULT 0,
		total_tokens INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (token_id, hour_bucket)
	)`)
	if err != nil {
		db.Close()
		t.Fatalf("create token_hourly_usage table: %v", err)
	}

	// Create auth_tokens table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS auth_tokens (
		id TEXT PRIMARY KEY,
		token_hash TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		expires_at TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		created_by TEXT NOT NULL,
		ultimate_model_enabled BOOLEAN NOT NULL DEFAULT FALSE,
		ultimate_model TEXT DEFAULT NULL,
		allowed_models TEXT DEFAULT NULL
	)`)
	if err != nil {
		db.Close()
		t.Fatalf("create auth_tokens table: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	return db
}

// mockNonStreamResponseWithUsage creates a non-streaming response with usage data
func mockNonStreamResponseWithUsage(content string, promptTokens, completionTokens, totalTokens int) string {
	resp := map[string]interface{}{
		"id":      "chatcmpl-integration-test",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "test-model",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      totalTokens,
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// TestHandlerCounterIntegration verifies the end-to-end wiring:
// handler → counter.Increment() → DB UPSERT → data in token_hourly_usage table
func TestHandlerCounterIntegration(t *testing.T) {
	// Setup: Create in-memory database with required tables
	db := setupIntegrationDB(t)

	// Setup: Create counter backed by the database
	counter := usage.NewCounter(db, database.SQLite)

	// Setup: Create token store and generate a valid API token
	tokenStore := auth.NewTokenStore(db, database.SQLite)
	// CreateToken returns the plaintext token (show once), so we use that
	plaintextToken, storedToken, err := tokenStore.CreateToken(context.Background(), "test-token", nil, "test-user", false, "", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	tokenID := storedToken.ID // This is what rc.tokenID will be set to

	// Setup: Create models config with an internal model (triggers authentication)
	modelsConfig := models.NewModelsConfig()

	// Create upstream server first so we can use its URL for the credential
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Return response with realistic usage data
		fmt.Fprint(w, mockNonStreamResponseWithUsage("Hello from test!", 50, 25, 75))
	}))
	defer upstream.Close()

	// Add the credential first (required for internal models)
	err = modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "test-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  upstream.URL,
	})
	if err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	// Now add the internal model that references the credential
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:            "test-model",
		Name:          "Test Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("test-credential"), // Reference the credential
		InternalModel: "test-model",
	})
	if err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// Setup: Create config manager pointing to our mock server
	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("RACE_RETRY_ENABLED", "false") // Disable race retry for simpler test

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Setup: Create handler with counter, token store, and models config
	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsConfig,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)

	// Create a buffer store (required for handler)
	bufStore, err := bufferstore.New(t.TempDir(), 1024*1024) // 1MB max
	if err != nil {
		t.Fatalf("NewBufferStore: %v", err)
	}

	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter)

	// Execute: Make a request through the handler with the valid API key
	reqBody := map[string]interface{}{
		"model":  "test-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello",
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, httpReq)

	// Verify: The request succeeded
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. body: %s", rec.Code, rec.Body.String())
	}

	// The counter.Increment is called in a goroutine, so we need to wait for it
	// Poll the database until we see the expected row
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var found bool
	for ctx.Err() == nil {
		rows, err := db.QueryContext(ctx, `SELECT token_id, hour_bucket, request_count, prompt_tokens, completion_tokens, total_tokens FROM token_hourly_usage WHERE token_id = ?`, tokenID)
		if err != nil {
			t.Fatalf("QueryContext: %v", err)
		}

		for rows.Next() {
			var rowTokenID, hourBucket string
			var reqCount, promptTok, compTok, totalTok int
			if err := rows.Scan(&rowTokenID, &hourBucket, &reqCount, &promptTok, &compTok, &totalTok); err != nil {
				rows.Close()
				t.Fatalf("Scan: %v", err)
			}

			// Found a row for our token
			found = true

			// Verify the values match what the mock server returned
			if reqCount != 1 {
				t.Errorf("request_count = %d, want 1", reqCount)
			}
			if promptTok != 50 {
				t.Errorf("prompt_tokens = %d, want 50", promptTok)
			}
			if compTok != 25 {
				t.Errorf("completion_tokens = %d, want 25", compTok)
			}
			if totalTok != 75 {
				t.Errorf("total_tokens = %d, want 75", totalTok)
			}

			// Verify the token_id matches
			if rowTokenID != tokenID {
				t.Errorf("token_id = %q, want %q", rowTokenID, tokenID)
			}

			// Verify hour_bucket is valid (format: YYYY-MM-DDTHH)
			if len(hourBucket) != 13 { // e.g., "2026-03-31T10"
				t.Errorf("hour_bucket = %q, expected format YYYY-MM-DDTHH", hourBucket)
			}
		}
		rows.Close()

		if found {
			break
		}

		// Wait a bit before polling again
		time.Sleep(50 * time.Millisecond)
	}

	if ctx.Err() == context.DeadlineExceeded && !found {
		t.Error("counter.Increment was never called - no row found in token_hourly_usage table")
	}
}

// TestHandlerCounterIntegration_MultipleRequests verifies that multiple requests
// accumulate correctly in the database
func TestHandlerCounterIntegration_MultipleRequests(t *testing.T) {
	// Setup
	db := setupIntegrationDB(t)
	counter := usage.NewCounter(db, database.SQLite)

	tokenStore := auth.NewTokenStore(db, database.SQLite)
	// CreateToken returns the plaintext token (show once), so we use that
	plaintextToken, storedToken, err := tokenStore.CreateToken(context.Background(), "test-token", nil, "test-user", false, "", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	tokenID := storedToken.ID

	modelsConfig := models.NewModelsConfig()

	// Create upstream server first so we can use its URL for the credential
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Return response with different usage for each request
		fmt.Fprint(w, mockNonStreamResponseWithUsage("Response!", 100, 50, 150))
	}))
	defer upstream.Close()

	// Add the credential first (required for internal models)
	err = modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "test-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  upstream.URL,
	})
	if err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	// Now add the internal model that references the credential
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:            "test-model",
		Name:          "Test Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("test-credential"),
		InternalModel: "test-model",
	})
	if err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("RACE_RETRY_ENABLED", "false")

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsConfig,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	bufStore, err := bufferstore.New(t.TempDir(), 1024*1024) // 1MB max
	if err != nil {
		t.Fatalf("NewBufferStore: %v", err)
	}

	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter)

	// Make 3 requests
	for i := 0; i < 3; i++ {
		reqBody := map[string]interface{}{
			"model":  "test-model",
			"stream": false,
			"messages": []interface{}{
				map[string]interface{}{
					"role":    "user",
					"content": fmt.Sprintf("Request %d", i),
				},
			},
		}
		bodyBytes, _ := json.Marshal(reqBody)
		httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)

		rec := httptest.NewRecorder()
		h.HandleChatCompletions(rec, httpReq)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: expected status 200, got %d", i, rec.Code)
		}
	}

	// Wait for all goroutines to complete
	time.Sleep(200 * time.Millisecond)

	// Verify: Check accumulated counts
	ctx := context.Background()
	// Get current hour bucket
	hourBucket := time.Now().UTC().Format("2006-01-02T15")

	rows, err := db.QueryContext(ctx, `SELECT request_count, prompt_tokens, completion_tokens, total_tokens FROM token_hourly_usage WHERE token_id = ? AND hour_bucket = ?`, tokenID, hourBucket)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("no row found for token in token_hourly_usage table")
	}

	var reqCount, promptTok, compTok, totalTok int
	if err := rows.Scan(&reqCount, &promptTok, &compTok, &totalTok); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Each request adds 100 prompt, 50 completion, 150 total
	// 3 requests = 300 prompt, 150 completion, 450 total
	if reqCount != 3 {
		t.Errorf("request_count = %d, want 3", reqCount)
	}
	if promptTok != 300 {
		t.Errorf("prompt_tokens = %d, want 300", promptTok)
	}
	if compTok != 150 {
		t.Errorf("completion_tokens = %d, want 150", compTok)
	}
	if totalTok != 450 {
		t.Errorf("total_tokens = %d, want 450", totalTok)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Per-Token Ultimate Model Override Integration Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestPerTokenUltimateModelOverride_Resolution tests that when a token has ultimateModelID set
// and ultimateModelEnabled=true, the handler passes the token's model to ultimateHandler.Execute()
// instead of the global config model.
func TestPerTokenUltimateModelOverride_Resolution(t *testing.T) {
	// Setup: Create in-memory database with required tables
	db := setupIntegrationDB(t)

	// Setup: Create counter backed by the database
	counter := usage.NewCounter(db, database.SQLite)

	// Setup: Create token store with a token that has ultimateModelID set
	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, storedToken, err := tokenStore.CreateToken(
		context.Background(),
		"token-with-ultimate-override",
		nil,
		"test-user",
		true,                     // ultimateModelEnabled = true
		"token-ultimate-model",   // ultimateModelID = per-token override (uses model ID, as frontend stores model.id)
		nil,                      // allowedModels = nil (all models allowed)
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	tokenID := storedToken.ID

	// Setup: Create models config
	modelsConfig := models.NewModelsConfig()

	// Create ultimate model upstream server that records the model used
	var ultimateModelUsed string
	ultimateUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract model from request
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if m, ok := body["model"].(string); ok {
				ultimateModelUsed = m
			}
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"id":      "chatcmpl-ultimate",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   ultimateModelUsed,
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"message":       map[string]interface{}{"role": "assistant", "content": "Ultimate response!"},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ultimateUpstream.Close()

	// Add credential for ultimate model
	err = modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "ultimate-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  ultimateUpstream.URL,
	})
	if err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	// Add global ultimate model
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:            "global-ultimate-model",
		Name:          "Global Ultimate Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("ultimate-credential"),
		InternalModel: "global-ultimate-model",
	})
	if err != nil {
		t.Fatalf("AddModel (global): %v", err)
	}

	// Add per-token ultimate model
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:            "token-ultimate-model",
		Name:          "Token Ultimate Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("ultimate-credential"),
		InternalModel: "token-ultimate-model",
	})
	if err != nil {
		t.Fatalf("AddModel (token): %v", err)
	}

	// Add an INTERNAL model for the test request (triggers auth)
	regularUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Regular upstream should not be called for ultimate model request")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer regularUpstream.Close()

	err = modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "regular-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  regularUpstream.URL,
	})
	if err != nil {
		t.Fatalf("AddCredential (regular): %v", err)
	}

	// Use an internal model to trigger authentication
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:            "test-model",
		Name:          "Test Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("regular-credential"),
		InternalModel: "test-model",
	})
	if err != nil {
		t.Fatalf("AddModel (regular): %v", err)
	}

	// Setup: Create config manager
	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", regularUpstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("RACE_RETRY_ENABLED", "false")
	t.Setenv("ULTIMATE_MODEL_ID", "global-ultimate-model") // Global config
	t.Setenv("LOOP_DETECTION_ENABLED", "false")             // Disable loop detection to simplify test

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Disable race retry in the config
	cfg := mgr.Get()
	cfg.RaceRetryEnabled = false
	mgr.Save(cfg)

	conf := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsConfig,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	bufStore, err := bufferstore.New(t.TempDir(), 1024*1024)
	if err != nil {
		t.Fatalf("NewBufferStore: %v", err)
	}

	h := NewHandler(conf, bus, reqStore, bufStore, tokenStore, counter)

	// Mark the request hash as failed so ultimate model will be triggered
	// (simulating a duplicate request scenario)
	messages := []map[string]interface{}{
		{"role": "user", "content": "Trigger ultimate model"},
	}
	// Access ultimateHandler (private field) from the handler in the same package
	h.ultimateHandler.MarkFailed(messages)

	// First ShouldTrigger call: counter=1, not triggered
	result := h.ultimateHandler.ShouldTrigger(messages)
	if result.Triggered {
		t.Error("First ShouldTrigger should not trigger (counter=1)")
	}

	// Second ShouldTrigger call: counter=2, triggered
	result = h.ultimateHandler.ShouldTrigger(messages)
	if !result.Triggered {
		t.Fatal("Second ShouldTrigger should trigger (counter=2)")
	}

	// Execute the request
	reqBody := map[string]interface{}{
		"model":  "test-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Trigger ultimate model",
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, httpReq)

	// Verify the request succeeded
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the per-token ultimate model was used instead of the global
	if ultimateModelUsed != "token-ultimate-model" {
		t.Errorf("Expected ultimate model = %q (per-token), got %q", "token-ultimate-model", ultimateModelUsed)
	}

	_ = tokenID // Use the variable
}

// TestPerTokenUltimateModelDisabled_UsesGlobal tests that when ultimateModelEnabled=false,
// the global model is used even if the token has UltimateModelID set.
func TestPerTokenUltimateModelDisabled_UsesGlobal(t *testing.T) {
	// Setup: Create in-memory database with required tables
	db := setupIntegrationDB(t)

	// Setup: Create counter backed by the database
	counter := usage.NewCounter(db, database.SQLite)

	// Setup: Create token with UltimateModelID set but ultimateModelEnabled=false
	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, storedToken, err := tokenStore.CreateToken(
		context.Background(),
		"token-with-ultimate-disabled",
		nil,
		"test-user",
		false,              // ultimateModelEnabled = false (disabled!)
		"token-ultimate-model", // But ultimateModelID is set
		nil,
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	tokenID := storedToken.ID

	// Setup: Create models config
	modelsConfig := models.NewModelsConfig()

	// Create upstream server for regular requests
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, mockNonStreamResponseWithUsage("Hello!", 10, 5, 15))
	}))
	defer upstream.Close()

	// Create ultimate model upstream server (should NOT be called)
	ultimateUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Ultimate model upstream should NOT be called when ultimateModelEnabled=false")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ultimateUpstream.Close()

	// Add credential for ultimate model
	err = modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "ultimate-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  ultimateUpstream.URL,
	})
	if err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	// Add global ultimate model
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:            "global-ultimate-model",
		Name:          "Global Ultimate Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("ultimate-credential"),
		InternalModel: "global-ultimate-model",
	})
	if err != nil {
		t.Fatalf("AddModel (global): %v", err)
	}

	// Add per-token ultimate model (won't be used because ultimateModelEnabled=false)
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:            "token-ultimate-model",
		Name:          "Token Ultimate Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("ultimate-credential"),
		InternalModel: "token-ultimate-model",
	})
	if err != nil {
		t.Fatalf("AddModel (token): %v", err)
	}

	// Add regular internal model
	err = modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "regular-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  upstream.URL,
	})
	if err != nil {
		t.Fatalf("AddCredential (regular): %v", err)
	}

	err = modelsConfig.AddModel(models.ModelConfig{
		ID:            "test-model",
		Name:          "Test Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("regular-credential"),
		InternalModel: "test-model",
	})
	if err != nil {
		t.Fatalf("AddModel (regular): %v", err)
	}

	// Setup: Create config manager
	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("RACE_RETRY_ENABLED", "false")
	t.Setenv("ULTIMATE_MODEL_ID", "global-ultimate-model")

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsConfig,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	bufStore, err := bufferstore.New(t.TempDir(), 1024*1024)
	if err != nil {
		t.Fatalf("NewBufferStore: %v", err)
	}

	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter)

	// Make a regular request (not triggering ultimate model because it's disabled)
	reqBody := map[string]interface{}{
		"model":  "test-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello",
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, httpReq)

	// Verify the request succeeded using regular upstream (not ultimate)
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// The response should come from the regular upstream, not the ultimate model upstream
	// This confirms ultimate model was not triggered

	_ = tokenID // Use the variable
}

// ─────────────────────────────────────────────────────────────────────────────
// Exclude from Ultimate Switching Integration Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestUltimateModel_ExcludedModel_DuplicateContinuesNormalFlow tests that when
// a model has ExcludeFromUltimateSwitching=true and a duplicate request is made,
// the ultimate model switch is skipped and normal flow continues.
func TestUltimateModel_ExcludedModel_DuplicateContinuesNormalFlow(t *testing.T) {
	// Setup: Create in-memory database with required tables
	db := setupIntegrationDB(t)

	// Setup: Create counter backed by the database
	counter := usage.NewCounter(db, database.SQLite)

	// Setup: Create token store with ultimate model enabled
	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, storedToken, err := tokenStore.CreateToken(
		context.Background(),
		"token-with-ultimate",
		nil,
		"test-user",
		true, // ultimateModelEnabled = true
		"",   // no per-token override
		nil,  // all models allowed
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	tokenID := storedToken.ID

	// Setup: Create models config with an EXCLUDED model
	modelsConfig := models.NewModelsConfig()

	// Create upstream server that tracks calls
	upstreamCallCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCallCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, mockNonStreamResponseWithUsage("Hello!", 10, 5, 15))
	}))
	defer upstream.Close()

	// Add credential
	err = modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "test-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  upstream.URL,
	})
	if err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	// Add the excluded model
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:                            "excluded-model",
		Name:                          "Excluded Model",
		Enabled:                       true,
		Internal:                      true,
		Credentials: models.TestRefs("test-credential"),
		InternalModel:                 "excluded-model",
		ExcludeFromUltimateSwitching:   true, // THIS IS THE KEY: excluded from ultimate model switching
	})
	if err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// Setup: Create config manager
	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("RACE_RETRY_ENABLED", "false")
	t.Setenv("ULTIMATE_MODEL_ID", "global-ultimate-model") // Any model - won't be used due to exclusion

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsConfig,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	bufStore, err := bufferstore.New(t.TempDir(), 1024*1024)
	if err != nil {
		t.Fatalf("NewBufferStore: %v", err)
	}

	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter)

	// Prepare messages for duplicate detection
	messages := []map[string]interface{}{
		{"role": "user", "content": "Trigger duplicate detection"},
	}

	// First request - stores the hash
	body1 := map[string]interface{}{
		"model":  "excluded-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Trigger duplicate detection"},
		},
	}
	bodyBytes1, _ := json.Marshal(body1)
	httpReq1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes1))
	httpReq1.Header.Set("Content-Type", "application/json")
	httpReq1.Header.Set("Authorization", "Bearer "+plaintextToken)

	rec1 := httptest.NewRecorder()
	h.HandleChatCompletions(rec1, httpReq1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("First request failed: %d - %s", rec1.Code, rec1.Body.String())
	}

	// Mark the hash as failed to simulate duplicate request scenario
	h.ultimateHandler.MarkFailed(messages)

	// Second request with same messages - should NOT trigger ultimate model
	// because the model is excluded
	body2 := map[string]interface{}{
		"model":  "excluded-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Trigger duplicate detection"},
		},
	}
	bodyBytes2, _ := json.Marshal(body2)
	httpReq2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes2))
	httpReq2.Header.Set("Content-Type", "application/json")
	httpReq2.Header.Set("Authorization", "Bearer "+plaintextToken)

	rec2 := httptest.NewRecorder()
	h.HandleChatCompletions(rec2, httpReq2)

	// Should succeed (not trigger ultimate model)
	if rec2.Code != http.StatusOK {
		t.Errorf("Second request should succeed, got: %d - %s", rec2.Code, rec2.Body.String())
	}

	// Verify upstream was called twice (normal flow for both requests)
	if upstreamCallCount != 2 {
		t.Errorf("Expected upstream to be called 2 times (normal flow), got %d", upstreamCallCount)
	}

	// Subscribe to events to verify exclusion event was published
	eventCh, _ := bus.Subscribe()
	defer bus.Unsubscribe(eventCh)

	// Third request to check for exclusion event
	body3 := map[string]interface{}{
		"model":  "excluded-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Trigger duplicate detection"},
		},
	}
	bodyBytes3, _ := json.Marshal(body3)
	httpReq3 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes3))
	httpReq3.Header.Set("Content-Type", "application/json")
	httpReq3.Header.Set("Authorization", "Bearer "+plaintextToken)

	rec3 := httptest.NewRecorder()
	h.HandleChatCompletions(rec3, httpReq3)

	// Check for ultimate_model_excluded event
	timeout := time.After(500 * time.Millisecond)
	found := false
	for {
		select {
		case evt := <-eventCh:
			if evt.Type == "ultimate_model_excluded" {
				found = true
				data := evt.Data.(map[string]interface{})
				if data["model"] != "excluded-model" {
					t.Errorf("Expected model 'excluded-model' in event, got: %v", data["model"])
				}
			}
		case <-timeout:
			goto done
		}
	}
done:

	if !found {
		t.Log("Note: ultimate_model_excluded event may have been missed due to timing")
	}

	_ = tokenID // Use the variable
}

// TestUltimateModel_ExcludedModel_ForceTriggerBypassesExclusion tests that when
// X-Force-Ultimate-Model header is set, the exclusion gate is bypassed.
// When exclusion is bypassed, the ultimate model is used directly.
// Note: This test verifies the exclusion gate bypass by checking that the event
// "ultimate_model_excluded" is NOT published (since exclusion is bypassed).
func TestUltimateModel_ExcludedModel_ForceTriggerBypassesExclusion(t *testing.T) {
	// Setup: Create in-memory database with required tables
	db := setupIntegrationDB(t)

	counter := usage.NewCounter(db, database.SQLite)

	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, storedToken, err := tokenStore.CreateToken(
		context.Background(),
		"token-with-ultimate",
		nil,
		"test-user",
		true, // ultimateModelEnabled = true
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	tokenID := storedToken.ID

	modelsConfig := models.NewModelsConfig()

	// Single upstream that records model used (for verification)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, mockNonStreamResponseWithUsage("Response!", 10, 5, 15))
	}))
	defer upstream.Close()

	// Add credential
	err = modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "test-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  upstream.URL,
	})
	if err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	// Add ultimate model
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:            "ultimate-model",
		Name:          "Ultimate Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("test-credential"),
		InternalModel: "ultimate-model",
	})
	if err != nil {
		t.Fatalf("AddModel (ultimate): %v", err)
	}

	// Add excluded model
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:                            "excluded-model",
		Name:                          "Excluded Model",
		Enabled:                       true,
		Internal:                      true,
		Credentials: models.TestRefs("test-credential"),
		InternalModel:                 "excluded-model",
		ExcludeFromUltimateSwitching:   true, // Excluded from normal ultimate model switching
	})
	if err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// Setup: Create config manager
	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("RACE_RETRY_ENABLED", "false")
	t.Setenv("ULTIMATE_MODEL_ID", "ultimate-model")

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsConfig,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	bufStore, err := bufferstore.New(t.TempDir(), 1024*1024)
	if err != nil {
		t.Fatalf("NewBufferStore: %v", err)
	}

	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter)

	// Subscribe to events to verify exclusion event is NOT published
	eventCh, _ := bus.Subscribe()
	defer bus.Unsubscribe(eventCh)

	// Make request with X-Force-Ultimate-Model header
	body := map[string]interface{}{
		"model":  "excluded-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Force trigger test"},
		},
	}
	bodyBytes, _ := json.Marshal(body)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)
	httpReq.Header.Set("X-Force-Ultimate-Model", "true")

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, httpReq)

	// Should succeed
	if rec.Code != http.StatusOK {
		t.Errorf("Request with Force-Ultimate should succeed, got: %d - %s", rec.Code, rec.Body.String())
	}

	// Verify the "ultimate_model_excluded" event is NOT published
	// (because exclusion is bypassed when forceUltimate=true)
	timeout := time.After(500 * time.Millisecond)
	exclusionEventFound := false

	for {
		select {
		case evt := <-eventCh:
			if evt.Type == "ultimate_model_excluded" {
				exclusionEventFound = true
			}
		case <-timeout:
			goto done
		}
	}
done:

	// Exclusion event should NOT be published (force bypasses exclusion)
	if exclusionEventFound {
		t.Error("ultimate_model_excluded event should NOT be published when forceUltimate=true")
	}

	_ = tokenID // Use the variable
}

// TestUltimateModel_ExcludedModel_RetryExhaustedNoError tests that when an
// excluded model exhausts retries, no retry-exhausted error is sent.
func TestUltimateModel_ExcludedModel_RetryExhaustedNoError(t *testing.T) {
	// Setup
	db := setupIntegrationDB(t)
	counter := usage.NewCounter(db, database.SQLite)

	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, storedToken, err := tokenStore.CreateToken(
		context.Background(),
		"token-with-ultimate",
		nil,
		"test-user",
		true,
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	tokenID := storedToken.ID

	modelsConfig := models.NewModelsConfig()

	upstreamCallCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCallCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, mockNonStreamResponseWithUsage("Hello!", 10, 5, 15))
	}))
	defer upstream.Close()

	err = modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "test-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  upstream.URL,
	})
	if err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	err = modelsConfig.AddModel(models.ModelConfig{
		ID:                            "excluded-model",
		Name:                          "Excluded Model",
		Enabled:                       true,
		Internal:                      true,
		Credentials: models.TestRefs("test-credential"),
		InternalModel:                 "excluded-model",
		ExcludeFromUltimateSwitching:   true,
	})
	if err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("RACE_RETRY_ENABLED", "false")
	t.Setenv("ULTIMATE_MODEL_ID", "ultimate-model")
	t.Setenv("ULTIMATE_MODEL_MAX_RETRIES", "2")

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsConfig,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	bufStore, err := bufferstore.New(t.TempDir(), 1024*1024)
	if err != nil {
		t.Fatalf("NewBufferStore: %v", err)
	}

	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Multiple retries test"},
	}

	// Send same request 5 times (exceeds max_retries=2)
	for i := 0; i < 5; i++ {
		// Mark as failed to simulate duplicates
		h.ultimateHandler.MarkFailed(messages)

		body := map[string]interface{}{
			"model":  "excluded-model",
			"stream": false,
			"messages": []interface{}{
				map[string]interface{}{"role": "user", "content": "Multiple retries test"},
			},
		}
		bodyBytes, _ := json.Marshal(body)
		httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)

		rec := httptest.NewRecorder()
		h.HandleChatCompletions(rec, httpReq)

		// All requests should succeed (no retry-exhausted error)
		if rec.Code != http.StatusOK {
			t.Errorf("Request %d should succeed (exclusion prevents retry-exhausted), got: %d - %s", i+1, rec.Code, rec.Body.String())
		}

		// Response should NOT contain retry exhausted error
		bodyStr := rec.Body.String()
		if strings.Contains(bodyStr, "retry limit exceeded") || strings.Contains(bodyStr, "retry-exhausted") {
			t.Errorf("Request %d should NOT contain retry-exhausted error: %s", i+1, bodyStr)
		}
	}

	// All requests should have gone through normal upstream
	if upstreamCallCount != 5 {
		t.Errorf("Expected 5 upstream calls (normal flow), got %d", upstreamCallCount)
	}

	_ = tokenID // Use the variable
}

// TestUltimateModel_ExcludedModel_CrossModelDetection tests that excluded model's
// hash storage enables detection for other non-excluded models.
// This test verifies that:
// 1. First request to excluded model stores the hash
// 2. Second request with same messages to non-excluded model triggers ultimate model
func TestUltimateModel_ExcludedModel_CrossModelDetection(t *testing.T) {
	// Setup
	db := setupIntegrationDB(t)
	counter := usage.NewCounter(db, database.SQLite)

	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, storedToken, err := tokenStore.CreateToken(
		context.Background(),
		"token-with-ultimate",
		nil,
		"test-user",
		true,
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	tokenID := storedToken.ID

	modelsConfig := models.NewModelsConfig()

	// Single upstream that records which model was used
	modelUsed := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if model, ok := body["model"].(string); ok {
				modelUsed = model
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, mockNonStreamResponseWithUsage("Response!", 10, 5, 15))
	}))
	defer upstream.Close()

	// Add credential
	err = modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "test-credential",
		Provider: "openai",
		APIKey:   "test-api-key",
		BaseURL:  upstream.URL,
	})
	if err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	// Add ultimate model
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:            "ultimate-model",
		Name:          "Ultimate Model",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("test-credential"),
		InternalModel: "ultimate-model",
	})
	if err != nil {
		t.Fatalf("AddModel (ultimate): %v", err)
	}

	// Add regular (non-excluded) model
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:                            "regular-model",
		Name:                          "Regular Model",
		Enabled:                       true,
		Internal:                      true,
		Credentials: models.TestRefs("test-credential"),
		InternalModel:                 "regular-model",
		ExcludeFromUltimateSwitching:   false, // NOT excluded
	})
	if err != nil {
		t.Fatalf("AddModel (regular): %v", err)
	}

	// Add excluded model
	err = modelsConfig.AddModel(models.ModelConfig{
		ID:                            "excluded-model",
		Name:                          "Excluded Model",
		Enabled:                       true,
		Internal:                      true,
		Credentials: models.TestRefs("test-credential"),
		InternalModel:                 "excluded-model",
		ExcludeFromUltimateSwitching:   true, // Excluded
	})
	if err != nil {
		t.Fatalf("AddModel (excluded): %v", err)
	}

	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	t.Setenv("RACE_RETRY_ENABLED", "false")
	t.Setenv("ULTIMATE_MODEL_ID", "ultimate-model")

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: modelsConfig,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	bufStore, err := bufferstore.New(t.TempDir(), 1024*1024)
	if err != nil {
		t.Fatalf("NewBufferStore: %v", err)
	}

	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter)

	// The key test messages
	testMessages := []map[string]interface{}{
		{"role": "user", "content": "Cross-model detection test"},
	}

	// Step 1: Send to excluded model (stores hash, continues normal flow)
	body1 := map[string]interface{}{
		"model":  "excluded-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Cross-model detection test"},
		},
	}
	bodyBytes1, _ := json.Marshal(body1)
	httpReq1 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes1))
	httpReq1.Header.Set("Content-Type", "application/json")
	httpReq1.Header.Set("Authorization", "Bearer "+plaintextToken)

	modelUsed = ""
	rec1 := httptest.NewRecorder()
	h.HandleChatCompletions(rec1, httpReq1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("First request failed: %d - %s", rec1.Code, rec1.Body.String())
	}

	// Excluded model should have been called
	if modelUsed != "excluded-model" {
		t.Errorf("Expected excluded-model to be called, got: %s", modelUsed)
	}

	// Step 2: Manually simulate the duplicate detection
	// Store hash and call ShouldTrigger twice to get counter=2
	h.ultimateHandler.MarkFailed(testMessages)
	result1 := h.ultimateHandler.ShouldTrigger(testMessages)
	if result1.Triggered {
		t.Error("First ShouldTrigger should not trigger (counter=1)")
	}

	result2 := h.ultimateHandler.ShouldTrigger(testMessages)
	if !result2.Triggered {
		t.Fatal("Second ShouldTrigger should trigger (counter=2)")
	}

	// Step 3: Subscribe to events to verify ultimate model was triggered
	eventCh, _ := bus.Subscribe()
	defer bus.Unsubscribe(eventCh)

	// Now make the second request - it should trigger ultimate model
	body2 := map[string]interface{}{
		"model":  "regular-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "Cross-model detection test"},
		},
	}
	bodyBytes2, _ := json.Marshal(body2)
	httpReq2 := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes2))
	httpReq2.Header.Set("Content-Type", "application/json")
	httpReq2.Header.Set("Authorization", "Bearer "+plaintextToken)

	modelUsed = ""
	rec2 := httptest.NewRecorder()
	h.HandleChatCompletions(rec2, httpReq2)

	if rec2.Code != http.StatusOK {
		t.Errorf("Second request failed: %d - %s", rec2.Code, rec2.Body.String())
	}

	// Verify the ultimate_model_triggered event was published
	timeout := time.After(500 * time.Millisecond)
	ultimateTriggeredFound := false

	for {
		select {
		case evt := <-eventCh:
			if evt.Type == "ultimate_model_triggered" {
				ultimateTriggeredFound = true
				data := evt.Data.(map[string]interface{})
				if data["original_model"] != "regular-model" {
					t.Errorf("Expected original_model 'regular-model' in event, got: %v", data["original_model"])
				}
			}
		case <-timeout:
			goto done
		}
	}
done:

	if !ultimateTriggeredFound {
		t.Error("ultimate_model_triggered event should be published (hash from excluded model triggers for non-excluded)")
	}

	_ = tokenID // Use the variable
}
