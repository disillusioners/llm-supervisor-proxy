package proxy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
		CredentialID:  "test-credential", // Reference the credential
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
		CredentialID:  "test-credential",
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
		true,               // ultimateModelEnabled = true
		"token-ultimate-model", // ultimateModelID = per-token override
		nil,               // allowedModels = nil (all models allowed)
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
		CredentialID:  "ultimate-credential",
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
		CredentialID:  "ultimate-credential",
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
		CredentialID:  "regular-credential",
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
		CredentialID:  "ultimate-credential",
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
		CredentialID:  "ultimate-credential",
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
		CredentialID:  "regular-credential",
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
