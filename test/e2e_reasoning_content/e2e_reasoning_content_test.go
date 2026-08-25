package e2e_reasoning_content

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
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

// mockUpstream captures incoming requests and returns configurable responses
type mockUpstream struct {
	t                *testing.T
	handler          http.HandlerFunc
	mu               sync.Mutex
	capturedRequests []capturedRequest
}

type capturedRequest struct {
	Body       []byte
	BodyParsed map[string]interface{}
	Headers    http.Header
}

func newMockUpstream(t *testing.T, handler http.HandlerFunc) *mockUpstream {
	return &mockUpstream{t: t, handler: handler}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test Infrastructure
// ─────────────────────────────────────────────────────────────────────────────

// testEnv holds all test dependencies
type testEnv struct {
	db         *sql.DB
	dbStore    *database.Store
	tokenStore *auth.TokenStore
	upstream   *mockUpstream
	handler    *proxy.Handler
}

// setupTestEnv creates a complete test environment with mock upstream
func setupTestEnv(t *testing.T, upstreamHandler http.HandlerFunc) *testEnv {
	t.Helper()

	// Create mock upstream
	mockUp := newMockUpstream(t, upstreamHandler)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture the request
		body, _ := io.ReadAll(r.Body)
		var bodyParsed map[string]interface{}
		json.Unmarshal(body, &bodyParsed)

		mockUp.mu.Lock()
		mockUp.capturedRequests = append(mockUp.capturedRequests, capturedRequest{
			Body:       body,
			BodyParsed: bodyParsed,
			Headers:    r.Header.Clone(),
		})
		mockUp.mu.Unlock()

		// Restore the body so the actual handler can read it
		r.Body = io.NopCloser(bytes.NewReader(body))

		// Call the actual handler
		mockUp.handler(w, r)
	}))
	t.Cleanup(upstream.Close)

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

	// Create models config
	modelsConfig := models.NewModelsConfig()

	// Add credential and internal model
	// Use the full URL from the httptest server
	if err := modelsConfig.AddCredential(models.CredentialConfig{
		ID:       "deepseek-cred",
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("Failed to add credential: %v", err)
	}

	// Add internal model (DeepSeek-style)
	if err := modelsConfig.AddModel(models.ModelConfig{
		ID:            "deepseek-r1",
		Name:          "DeepSeek R1",
		Enabled:       true,
		Internal:      true,
		Credentials: models.TestRefs("deepseek-cred"),
		InternalModel: "deepseek-reasoner",
	}); err != nil {
		t.Fatalf("Failed to add model: %v", err)
	}

	// Create event bus
	bus := events.NewBus()

	// Create request store
	reqStore := store.NewRequestStore(100)

	// Create proxy config manager
	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("MAX_GENERATION_TIME", "10s")
	cfgMgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("Failed to create config manager: %v", err)
	}

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
		upstream:   mockUp,
		handler:    handler,
	}
}

// makeHandlerRequest creates a request for the proxy handler
func makeHandlerRequest(model, plaintextToken string, messages []map[string]interface{}, stream bool) *http.Request {
	body := map[string]interface{}{
		"model":    model,
		"stream":   stream,
		"messages": messages,
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
// Test: Non-Streaming Multi-Turn with Reasoning Content
// ─────────────────────────────────────────────────────────────────────────────

func TestE2EReasoningContent_NonStreaming_MultiTurn(t *testing.T) {
	// Create mock upstream that returns responses with reasoning_content
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse request body
		body, _ := io.ReadAll(r.Body)
		var reqBody map[string]interface{}
		json.Unmarshal(body, &reqBody)

		// Check if this is turn 1 (first request) or turn 2 (follow-up)
		messages, _ := reqBody["messages"].([]interface{})
		isTurn1 := len(messages) == 1

		w.Header().Set("Content-Type", "application/json")

		if isTurn1 {
			// Turn 1: Return response with reasoning_content
			resp := map[string]interface{}{
				"id":      "chatcmpl-turn1",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   "deepseek-reasoner",
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"message": map[string]interface{}{
							"role":              "assistant",
							"content":           "The answer is 42.",
							"reasoning_content": "Let me think step by step about this...",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]int{
					"prompt_tokens":     10,
					"completion_tokens": 20,
					"total_tokens":      30,
				},
			}
			json.NewEncoder(w).Encode(resp)
		} else {
			// Turn 2: Verify reasoning_content was forwarded, return normal response
			// Look for reasoning_content in messages
			hasReasoning := false
			for _, msg := range messages {
				if m, ok := msg.(map[string]interface{}); ok {
					if rc, ok := m["reasoning_content"].(string); ok && rc != "" {
						hasReasoning = true
						break
					}
				}
			}

			if !hasReasoning {
				// This is the failure case - reasoning_content was not forwarded
				t.Errorf("Turn 2: reasoning_content was NOT forwarded to upstream")
			} else {
				t.Logf("Turn 2: reasoning_content was correctly forwarded to upstream")
			}

			resp := map[string]interface{}{
				"id":      "chatcmpl-turn2",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   "deepseek-reasoner",
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "Follow-up response.",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]int{
					"prompt_tokens":     20,
					"completion_tokens": 10,
					"total_tokens":      30,
				},
			}
			json.NewEncoder(w).Encode(resp)
		}
	})

	env := setupTestEnv(t, upstreamHandler)
	ctx := context.Background()

	// Create token
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "test-token", nil, "test", false, "", nil)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// === TURN 1 ===
	t.Log("=== Turn 1: Client sends user message, upstream returns reasoning_content ===")
	req1 := makeHandlerRequest("deepseek-r1", plaintext, []map[string]interface{}{
		{"role": "user", "content": "What is 2+2?"},
	}, false)

	rr1 := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("Turn 1: Expected 200 OK, got %d: %s", rr1.Code, rr1.Body.String())
	}

	// Parse response and verify reasoning_content is present
	var resp1 map[string]interface{}
	if err := json.Unmarshal(rr1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("Turn 1: Failed to parse response: %v", err)
	}

	choices1, _ := resp1["choices"].([]interface{})
	if len(choices1) == 0 {
		t.Fatal("Turn 1: No choices in response")
	}

	msg1 := choices1[0].(map[string]interface{})["message"].(map[string]interface{})
	reasoningContent1, hasRC1 := msg1["reasoning_content"].(string)

	if !hasRC1 || reasoningContent1 == "" {
		t.Errorf("Turn 1: Expected reasoning_content in response, got: %v", msg1["reasoning_content"])
	} else {
		t.Logf("Turn 1: Received reasoning_content: %q", reasoningContent1)
	}

	// === TURN 2 ===
	t.Log("=== Turn 2: Client sends conversation with reasoning_content from Turn 1 ===")
	req2 := makeHandlerRequest("deepseek-r1", plaintext, []map[string]interface{}{
		{"role": "user", "content": "What is 2+2?"},
		{"role": "assistant", "content": "The answer is 42.", "reasoning_content": reasoningContent1},
		{"role": "user", "content": "Thanks! Can you explain how you got that?"},
	}, false)

	rr2 := httptest.NewRecorder()
	env.handler.HandleChatCompletions(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("Turn 2: Expected 200 OK, got %d: %s", rr2.Code, rr2.Body.String())
	}

	// Verify reasoning_content was forwarded in the request
	env.upstream.mu.Lock()
	capturedTurn2 := env.upstream.capturedRequests[len(env.upstream.capturedRequests)-1]
	env.upstream.mu.Unlock()

	messages2, _ := capturedTurn2.BodyParsed["messages"].([]interface{})
	foundReasoning := false
	for i, msg := range messages2 {
		if m, ok := msg.(map[string]interface{}); ok {
			if rc, ok := m["reasoning_content"].(string); ok && rc != "" {
				foundReasoning = true
				t.Logf("Turn 2: Message %d has reasoning_content: %q", i, rc)
			}
		}
	}

	if !foundReasoning {
		t.Error("Turn 2: reasoning_content was NOT forwarded in the request body to upstream")
	}

	// Verify response is successful
	var resp2 map[string]interface{}
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("Turn 2: Failed to parse response: %v", err)
	}

	choices2, _ := resp2["choices"].([]interface{})
	if len(choices2) == 0 {
		t.Fatal("Turn 2: No choices in response")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: Streaming Multi-Turn with Reasoning Content
// ─────────────────────────────────────────────────────────────────────────────

func TestE2EReasoningContent_Streaming_MultiTurn(t *testing.T) {
	// Track if reasoning_content was found in turn 2
	var turn2HasReasoning bool
	var turn2Mu sync.Mutex

	// Create mock upstream that returns streaming responses with reasoning_content
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var reqBody map[string]interface{}
		json.Unmarshal(body, &reqBody)

		messages, _ := reqBody["messages"].([]interface{})
		isTurn1 := len(messages) == 1

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		if isTurn1 {
			// Turn 1: Send reasoning_content chunk, then content chunk
			reasoningChunk := map[string]interface{}{
				"id":      "chatcmpl-stream1",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   "deepseek-reasoner",
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"delta": map[string]interface{}{
							"reasoning_content": "Let me think about this...",
						},
					},
				},
			}
			fmt.Fprintf(w, "data: %s\n\n", mustMarshal(reasoningChunk))

			contentChunk := map[string]interface{}{
				"id":      "chatcmpl-stream1",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   "deepseek-reasoner",
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"delta": map[string]interface{}{
							"role":    "assistant",
							"content": "The answer is 42.",
						},
					},
				},
			}
			fmt.Fprintf(w, "data: %s\n\n", mustMarshal(contentChunk))

			// Final chunk with usage
			finalChunk := map[string]interface{}{
				"id":      "chatcmpl-stream1",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   "deepseek-reasoner",
				"choices": []map[string]interface{}{
					{
						"index":         0,
						"delta":         map[string]interface{}{},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]int{
					"prompt_tokens":     10,
					"completion_tokens": 20,
					"total_tokens":      30,
				},
			}
			fmt.Fprintf(w, "data: %s\n\n", mustMarshal(finalChunk))
		} else {
			// Turn 2: Verify reasoning_content was forwarded
			hasReasoning := false
			for _, msg := range messages {
				if m, ok := msg.(map[string]interface{}); ok {
					if rc, ok := m["reasoning_content"].(string); ok && rc != "" {
						hasReasoning = true
						break
					}
				}
			}

			turn2Mu.Lock()
			turn2HasReasoning = hasReasoning
			turn2Mu.Unlock()

			// Send normal streaming response
			chunk := map[string]interface{}{
				"id":      "chatcmpl-stream2",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   "deepseek-reasoner",
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"delta": map[string]interface{}{
							"role":    "assistant",
							"content": "Follow-up response.",
						},
					},
				},
			}
			fmt.Fprintf(w, "data: %s\n\n", mustMarshal(chunk))

			finalChunk := map[string]interface{}{
				"id":      "chatcmpl-stream2",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   "deepseek-reasoner",
				"choices": []map[string]interface{}{
					{
						"index":         0,
						"delta":         map[string]interface{}{},
						"finish_reason": "stop",
					},
				},
			}
			fmt.Fprintf(w, "data: %s\n\n", mustMarshal(finalChunk))
		}

		fmt.Fprintln(w, "data: [DONE]")
	})

	env := setupTestEnv(t, upstreamHandler)
	ctx := context.Background()

	// Create token
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "test-token-stream", nil, "test", false, "", nil)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	// === TURN 1 ===
	t.Log("=== Turn 1: Streaming request, upstream returns reasoning_content chunks ===")
	req1 := makeHandlerRequest("deepseek-r1", plaintext, []map[string]interface{}{
		{"role": "user", "content": "What is 2+2?"},
	}, true)

	rr1 := httptest.NewRecorder()

	// Run handler for streaming
	env.handler.HandleChatCompletions(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("Turn 1: Expected 200 OK, got %d", rr1.Code)
	}

	// Parse streaming response and verify reasoning_content is present
	streamingStr := rr1.Body.String()
	if !strings.Contains(streamingStr, "reasoning_content") {
		t.Errorf("Turn 1: Expected reasoning_content in streaming response, got: %s", streamingStr)
	} else {
		t.Logf("Turn 1: Found reasoning_content in streaming response")
	}

	// Extract reasoning_content from stream
	var reasoningContent1 string
	for _, line := range strings.Split(streamingStr, "\n") {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				continue
			}
			var chunk map[string]interface{}
			if json.Unmarshal([]byte(data), &chunk) == nil {
				choices, _ := chunk["choices"].([]interface{})
				if len(choices) > 0 {
					delta := choices[0].(map[string]interface{})["delta"].(map[string]interface{})
					if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
						reasoningContent1 = rc
						break
					}
				}
			}
		}
	}

	if reasoningContent1 == "" {
		t.Error("Turn 1: Could not extract reasoning_content from streaming response")
	} else {
		t.Logf("Turn 1: Extracted reasoning_content: %q", reasoningContent1)
	}

	// === TURN 2 ===
	t.Log("=== Turn 2: Streaming request with reasoning_content from Turn 1 ===")
	req2 := makeHandlerRequest("deepseek-r1", plaintext, []map[string]interface{}{
		{"role": "user", "content": "What is 2+2?"},
		{"role": "assistant", "content": "The answer is 42.", "reasoning_content": reasoningContent1},
		{"role": "user", "content": "Thanks!"},
	}, true)

	rr2 := httptest.NewRecorder()

	// Reset tracking
	turn2Mu.Lock()
	turn2HasReasoning = false
	turn2Mu.Unlock()

	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		env.handler.HandleChatCompletions(rr2, req2)
	}()

	<-done2

	// Verify reasoning_content was forwarded
	turn2Mu.Lock()
	hasReasoning := turn2HasReasoning
	turn2Mu.Unlock()

	if !hasReasoning {
		t.Error("Turn 2: reasoning_content was NOT forwarded to upstream")
	} else {
		t.Logf("Turn 2: reasoning_content was correctly forwarded to upstream")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: Verify Reasoning Content in Request Forwarding
// ─────────────────────────────────────────────────────────────────────────────

func TestE2EReasoningContent_RequestForwarding(t *testing.T) {
	var capturedBodies []map[string]interface{}
	var mu sync.Mutex

	// Create mock upstream that captures request bodies
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var bodyParsed map[string]interface{}
		json.Unmarshal(body, &bodyParsed)

		mu.Lock()
		capturedBodies = append(capturedBodies, bodyParsed)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   "deepseek-reasoner",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"message": map[string]interface{}{
						"role":    "assistant",
						"content": "Response.",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]int{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		json.NewEncoder(w).Encode(resp)
	})

	env := setupTestEnv(t, upstreamHandler)
	ctx := context.Background()

	// Create token
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "test-token-fwd", nil, "test", false, "", nil)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	tests := []struct {
		name          string
		messages      []map[string]interface{}
		wantReasoning bool
		wantValue     string
	}{
		{
			name: "single message with reasoning_content",
			messages: []map[string]interface{}{
				{"role": "assistant", "content": "Answer", "reasoning_content": "Thinking..."},
			},
			wantReasoning: true,
			wantValue:     "Thinking...",
		},
		{
			name: "multiple messages with reasoning_content in one",
			messages: []map[string]interface{}{
				{"role": "user", "content": "Hello"},
				{"role": "assistant", "content": "Hi!", "reasoning_content": "Greeting..."},
				{"role": "user", "content": "How are you?"},
			},
			wantReasoning: true,
			wantValue:     "Greeting...",
		},
		{
			name: "no reasoning_content",
			messages: []map[string]interface{}{
				{"role": "user", "content": "Hello"},
				{"role": "assistant", "content": "Hi!"},
			},
			wantReasoning: false,
		},
		{
			name: "empty reasoning_content",
			messages: []map[string]interface{}{
				{"role": "assistant", "content": "Answer", "reasoning_content": ""},
			},
			wantReasoning: false, // Empty string with omitempty is not forwarded
		},
		{
			name: "reasoning_content with name field",
			messages: []map[string]interface{}{
				{"role": "assistant", "content": "Answer", "reasoning_content": "Thinking...", "name": "assistant_1"},
			},
			wantReasoning: true,
			wantValue:     "Thinking...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset captured bodies
			mu.Lock()
			capturedBodies = nil
			mu.Unlock()

			req := makeHandlerRequest("deepseek-r1", plaintext, tt.messages, false)
			rr := httptest.NewRecorder()
			env.handler.HandleChatCompletions(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
			}

			// Check captured request
			mu.Lock()
			captured := capturedBodies[len(capturedBodies)-1]
			mu.Unlock()

			messages, _ := captured["messages"].([]interface{})
			var foundReasoning string
			for _, msg := range messages {
				if m, ok := msg.(map[string]interface{}); ok {
					if rc, ok := m["reasoning_content"].(string); ok && rc != "" {
						foundReasoning = rc
						break
					}
				}
			}

			if tt.wantReasoning {
				if foundReasoning == "" {
					t.Errorf("Expected reasoning_content to be forwarded, but it was not")
				} else if tt.wantValue != "" && foundReasoning != tt.wantValue {
					t.Errorf("reasoning_content = %q, want %q", foundReasoning, tt.wantValue)
				} else {
					t.Logf("Correctly forwarded reasoning_content: %q", foundReasoning)
				}
			} else {
				if foundReasoning != "" {
					t.Errorf("Expected no reasoning_content, but got %q", foundReasoning)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: Verify Reasoning Content in Response Forwarding
// ─────────────────────────────────────────────────────────────────────────────

func TestE2EReasoningContent_ResponseForwarding(t *testing.T) {
	tests := []struct {
		name             string
		upstreamResponse map[string]interface{}
		wantReasoning    bool
		wantValue        string
	}{
		{
			name: "response with reasoning_content",
			upstreamResponse: map[string]interface{}{
				"id":      "chatcmpl-rc",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   "deepseek-reasoner",
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"message": map[string]interface{}{
							"role":              "assistant",
							"content":           "The answer is 42.",
							"reasoning_content": "Let me calculate...",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]int{
					"prompt_tokens":     10,
					"completion_tokens": 20,
					"total_tokens":      30,
				},
			},
			wantReasoning: true,
			wantValue:     "Let me calculate...",
		},
		{
			name: "response without reasoning_content",
			upstreamResponse: map[string]interface{}{
				"id":      "chatcmpl-no-rc",
				"object":  "chat.completion",
				"created": time.Now().Unix(),
				"model":   "deepseek-reasoner",
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "Hello!",
						},
						"finish_reason": "stop",
					},
				},
				"usage": map[string]int{
					"prompt_tokens":     10,
					"completion_tokens": 5,
					"total_tokens":      15,
				},
			},
			wantReasoning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tt.upstreamResponse)
			})

			env := setupTestEnv(t, upstreamHandler)
			ctx := context.Background()

			// Create token
			plaintext, _, err := env.tokenStore.CreateToken(ctx, "test-token-resp-"+tt.name, nil, "test", false, "", nil)
			if err != nil {
				t.Fatalf("CreateToken failed: %v", err)
			}

			req := makeHandlerRequest("deepseek-r1", plaintext, []map[string]interface{}{
				{"role": "user", "content": "Hello"},
			}, false)

			rr := httptest.NewRecorder()
			env.handler.HandleChatCompletions(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
			}

			// Parse response
			var resp map[string]interface{}
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("Failed to parse response: %v", err)
			}

			choices, _ := resp["choices"].([]interface{})
			if len(choices) == 0 {
				t.Fatal("No choices in response")
			}

			msg := choices[0].(map[string]interface{})["message"].(map[string]interface{})
			reasoningContent, hasRC := msg["reasoning_content"].(string)

			if tt.wantReasoning {
				if !hasRC || reasoningContent == "" {
					t.Errorf("Expected reasoning_content in response, got: %v", msg["reasoning_content"])
				} else if tt.wantValue != "" && reasoningContent != tt.wantValue {
					t.Errorf("reasoning_content = %q, want %q", reasoningContent, tt.wantValue)
				} else {
					t.Logf("Correctly received reasoning_content: %q", reasoningContent)
				}
			} else {
				if hasRC && reasoningContent != "" {
					t.Errorf("Expected no reasoning_content, but got %q", reasoningContent)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test: Streaming Response with Reasoning Content Chunks
// ─────────────────────────────────────────────────────────────────────────────

func TestE2EReasoningContent_StreamingResponseChunks(t *testing.T) {
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send reasoning_content chunk first
		reasoningChunk := map[string]interface{}{
			"id":      "chatcmpl-stream-rc",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   "deepseek-reasoner",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"delta": map[string]interface{}{
						"reasoning_content": "Thinking step by step...",
					},
				},
			},
		}
		fmt.Fprintf(w, "data: %s\n\n", mustMarshal(reasoningChunk))

		// Send content chunk
		contentChunk := map[string]interface{}{
			"id":      "chatcmpl-stream-rc",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   "deepseek-reasoner",
			"choices": []map[string]interface{}{
				{
					"index": 0,
					"delta": map[string]interface{}{
						"role":    "assistant",
						"content": "The answer is 42.",
					},
				},
			},
		}
		fmt.Fprintf(w, "data: %s\n\n", mustMarshal(contentChunk))

		// Send final chunk
		finalChunk := map[string]interface{}{
			"id":      "chatcmpl-stream-rc",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   "deepseek-reasoner",
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"delta":         map[string]interface{}{},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]int{
				"prompt_tokens":     10,
				"completion_tokens": 20,
				"total_tokens":      30,
			},
		}
		fmt.Fprintf(w, "data: %s\n\n", mustMarshal(finalChunk))

		fmt.Fprintln(w, "data: [DONE]")
	})

	env := setupTestEnv(t, upstreamHandler)
	ctx := context.Background()

	// Create token
	plaintext, _, err := env.tokenStore.CreateToken(ctx, "test-token-stream-rc", nil, "test", false, "", nil)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	req := makeHandlerRequest("deepseek-r1", plaintext, []map[string]interface{}{
		{"role": "user", "content": "What is 2+2?"},
	}, true)

	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		env.handler.HandleChatCompletions(rr, req)
	}()

	<-done

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}

	// Parse streaming response and verify reasoning_content chunks
	streamingStr := rr.Body.String()

	// Check that reasoning_content appears in the stream
	if !strings.Contains(streamingStr, "reasoning_content") {
		t.Errorf("Expected reasoning_content in streaming response chunks")
	}

	// Extract reasoning_content from chunks
	var foundReasoning string
	for _, line := range strings.Split(streamingStr, "\n") {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" || data == "" {
				continue
			}
			var chunk map[string]interface{}
			if json.Unmarshal([]byte(data), &chunk) == nil {
				choices, _ := chunk["choices"].([]interface{})
				if len(choices) > 0 {
					delta := choices[0].(map[string]interface{})["delta"].(map[string]interface{})
					if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
						foundReasoning = rc
						break
					}
				}
			}
		}
	}

	if foundReasoning == "" {
		t.Error("Could not extract reasoning_content from streaming response")
	} else if foundReasoning != "Thinking step by step..." {
		t.Errorf("reasoning_content = %q, want %q", foundReasoning, "Thinking step by step...")
	} else {
		t.Logf("Correctly extracted reasoning_content from streaming response: %q", foundReasoning)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper Functions
// ─────────────────────────────────────────────────────────────────────────────

// mustMarshal panics on JSON marshal error
func mustMarshal(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
