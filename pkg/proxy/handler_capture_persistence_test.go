package proxy

// Fix 3 end-to-end proxy-level test for the reasoning-observability effort.
//
// Verifies the persistence step at pkg/proxy/handler.go:660-680:
// when ultimate model triggers and Execute returns an ExecuteResult
// with non-empty Content + Thinking, the outer handler persists
// store.Message{Role: "assistant", Content, Thinking} alongside
// the existing usage store.
//
// This test does NOT replace the byte-identity proofs in
// pkg/ultimatemodel/handler_capture_test.go — those tests run
// inside the ultimatemodel package and assert ExecuteResult fields
// directly. This test verifies the OUTER persistence contract.

import (
	"bytes"
	"context"
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

// mockUltimateExternalResponse builds a non-stream upstream body
// that mirrors the assistant content + reasoning_content shape the
// capture-side parser is meant to extract.
func mockUltimateExternalResponse(content, thinking string, prompt, completion, total int) string {
	resp := map[string]interface{}{
		"id":      "chatcmpl-capture-test",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "ultimate-model",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":              "assistant",
					"content":           content,
					"reasoning_content": thinking,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      total,
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// TestUltimateModel_PersistsAssistantContentAndThinking_External
// is the Fix 3 persistence test for the external ultimate path.
//
// Sets up an external ultimate model that points at a mock upstream
// returning content + reasoning_content, triggers the ultimate via
// X-Force-Ultimate-Model, and verifies the persisted RequestLog has
// a store.Message{Role: "assistant", Content, Thinking}.
func TestUltimateModel_PersistsAssistantContentAndThinking_External(t *testing.T) {
	const wantContent = "captured assistant reply"
	const wantThinking = "captured reasoning trail"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, mockUltimateExternalResponse(wantContent, wantThinking, 7, 3, 10))
	}))
	defer upstream.Close()

	// Bootstrap in-memory DB + token + model + handler exactly like
	// TestUltimateModel_ExcludedModel_ForceTriggerBypassesExclusion
	// does. The ultimate model is External so executeExternal runs
	// and the upstream is our httptest.NewServer above.
	db := setupIntegrationDB(t)
	counter := usage.NewCounter(db, database.SQLite)
	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, _, err := tokenStore.CreateToken(
		context.Background(),
		"token-with-ultimate",
		nil,
		"test-user",
		true, // ultimateModelEnabled
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	modelsConfig := models.NewModelsConfig()
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
		ID:           "ultimate-model",
		Name:         "Ultimate Model",
		Enabled:      true,
		Internal:     false, // EXTERNAL — executes through executeExternal
		Credentials: models.TestRefs("test-credential"),
	})
	if err != nil {
		t.Fatalf("AddModel (ultimate): %v", err)
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
	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter, nil)

	// Trigger ultimate via the force header so we don't depend on
	// the duplicate-request path or hash-cache timing.
	body := map[string]interface{}{
		"model":  "any-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	bodyBytes, _ := json.Marshal(body)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)
	httpReq.Header.Set("X-Force-Ultimate-Model", "true")

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("request failed: %d - %s", rec.Code, rec.Body.String())
	}

	// Find the most recent completed request log — the ultimate
	// path is the only place that writes Content+Thinking in this
	// flow.
	all := reqStore.List()
	if len(all) == 0 {
		t.Fatal("no request logs persisted")
	}
	var log *store.RequestLog
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].UltimateModelUsed {
			log = all[i]
			break
		}
	}
	if log == nil {
		t.Fatal("no ultimate request log found")
	}

	// Find the persisted assistant message.
	var assistantMsg *store.Message
	for i := len(log.Messages) - 1; i >= 0; i-- {
		if log.Messages[i].Role == "assistant" {
			assistantMsg = &log.Messages[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatalf("no assistant message persisted; log messages: %+v", log.Messages)
	}

	if assistantMsg.Content != wantContent {
		t.Errorf("persisted Content = %q, want %q", assistantMsg.Content, wantContent)
	}
	if assistantMsg.Thinking != wantThinking {
		t.Errorf("persisted Thinking = %q, want %q", assistantMsg.Thinking, wantThinking)
	}

	// Sanity: usage must still be set (Fix 3 must not break the
	// existing usage store path).
	if log.Usage == nil {
		t.Error("Usage was not persisted")
	} else if log.Usage.TotalTokens != 10 {
		t.Errorf("Usage.TotalTokens = %d, want 10", log.Usage.TotalTokens)
	}
}

// TestUltimateModel_PersistsAssistantContentAndThinking_ExternalStream
// is the streaming counterpart of the non-stream persistence test.
// Same setup, but the upstream emits SSE chunks with content +
// reasoning_content deltas, the request is stream=true, and we
// verify the persisted assistant message accumulates across chunks.
func TestUltimateModel_PersistsAssistantContentAndThinking_ExternalStream(t *testing.T) {
	const wantContent = "streamed answer"
	const wantThinking = "streamed reasoning"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"reasoning_content\":\"%s\"}}]}\n\n", wantThinking)
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", wantContent)
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	db := setupIntegrationDB(t)
	counter := usage.NewCounter(db, database.SQLite)
	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, _, err := tokenStore.CreateToken(
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

	modelsConfig := models.NewModelsConfig()
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
		ID:           "ultimate-model",
		Name:         "Ultimate Model",
		Enabled:      true,
		Internal:     false,
		Credentials: models.TestRefs("test-credential"),
	})
	if err != nil {
		t.Fatalf("AddModel (ultimate): %v", err)
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
	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter, nil)

	body := map[string]interface{}{
		"model":  "any-model",
		"stream": true,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	bodyBytes, _ := json.Marshal(body)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)
	httpReq.Header.Set("X-Force-Ultimate-Model", "true")

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("request failed: %d - %s", rec.Code, rec.Body.String())
	}

	// Client-received bytes must be EXACTLY the upstream SSE stream
	// (capture-vs-wire proof at the proxy level). Previously this
	// was a bytes.Contains check on wantContent + wantThinking which
	// would silently pass if the proxy injected interior '\n' bytes,
	// duplicated chunks, or stripped any framing lines. The W3
	// hardening is a full bytes.Equal against the expected SSE
	// body — the upstream writes 4 data lines (reasoning, content,
	// final-chunk-with-usage, [DONE]) separated by SSE-style "\n\n"
	// delimiters, and the proxy must write them verbatim.
	wantBody := []byte(
		"data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"reasoning_content\":\"" + wantThinking + "\"}}]}\n\n" +
			"data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"" + wantContent + "\"}}]}\n\n" +
			"data: {\"id\":\"1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n" +
			"data: [DONE]\n\n",
	)
	if got := rec.Body.Bytes(); !bytes.Equal(got, wantBody) {
		t.Errorf("client bytes drifted from golden\n got: %q (%d bytes)\nwant: %q (%d bytes)", got, len(got), wantBody, len(wantBody))
	}

	// Find the ultimate request log and verify the persisted
	// assistant message.
	all := reqStore.List()
	var log *store.RequestLog
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].UltimateModelUsed {
			log = all[i]
			break
		}
	}
	if log == nil {
		t.Fatal("no ultimate request log found")
	}
	var assistantMsg *store.Message
	for i := len(log.Messages) - 1; i >= 0; i-- {
		if log.Messages[i].Role == "assistant" {
			assistantMsg = &log.Messages[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatalf("no assistant message persisted; log messages: %+v", log.Messages)
	}
	if assistantMsg.Content != wantContent {
		t.Errorf("persisted Content = %q, want %q", assistantMsg.Content, wantContent)
	}
	if assistantMsg.Thinking != wantThinking {
		t.Errorf("persisted Thinking = %q, want %q", assistantMsg.Thinking, wantThinking)
	}
}

// mockUltimateInternalResponse builds a non-stream upstream body in
// OpenAI chat-completions shape with content + reasoning_content. It is
// the internal-path counterpart of mockUltimateExternalResponse: the
// internal path is reached when modelCfg.Internal=true and the proxy
// routes through ultimatemodel.executeInternal, which constructs an
// OpenAIProvider that calls <credential.BaseURL>/chat/completions. The
// provider's typed JSON decode reads `reasoning_content` directly into
// ChatMessage.ReasoningContent, which executeInternal then copies
// verbatim into ExecuteResult.Thinking (the outer handler persists it
// as store.Message.Thinking).
func mockUltimateInternalResponse(content, thinking string, prompt, completion, total int) string {
	resp := map[string]interface{}{
		"id":      "chatcmpl-internal-test",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   "internal-model",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]interface{}{
					"role":              "assistant",
					"content":           content,
					"reasoning_content": thinking,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      total,
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// TestUltimateModel_PersistsAssistantContentAndThinking_Internal is the
// internal-path counterpart of the External non-stream persistence test.
//
// Sets up an INTERNAL ultimate model (Internal=true, provider=openai)
// whose credential BaseURL points at a mock upstream. The OpenAI
// provider constructed by ultimatemodel.executeInternal calls the
// upstream, parses the response into a typed ChatCompletionResponse
// (with ReasoningContent populated), and executeInternal copies the
// fields verbatim into ExecuteResult. The outer handler then persists
// store.Message{Role: "assistant", Content, Thinking} (handler.go:660-705).
//
// Closes the W2 gap: prior to this test, the internal path had only
// structural coverage in handler_capture_persistence_test.go and the
// executeInternal capture was exercised only at the ultimatemodel
// package level. This test verifies the OUTER persistence contract.
func TestUltimateModel_PersistsAssistantContentAndThinking_Internal(t *testing.T) {
	const wantContent = "internal assistant reply"
	const wantThinking = "internal reasoning trail"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, mockUltimateInternalResponse(wantContent, wantThinking, 7, 3, 10))
	}))
	defer upstream.Close()

	// Same bootstrap as TestUltimateModel_PersistsAssistantContentAndThinking_External,
	// but the ultimate model is Internal=true so executeInternal runs
	// (it constructs an OpenAIProvider against the credential BaseURL
	// which is our httptest upstream).
	db := setupIntegrationDB(t)
	counter := usage.NewCounter(db, database.SQLite)
	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, _, err := tokenStore.CreateToken(
		context.Background(),
		"token-with-ultimate",
		nil,
		"test-user",
		true, // ultimateModelEnabled
		"",
		nil,
	)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	modelsConfig := models.NewModelsConfig()
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
		ID:              "ultimate-model",
		Name:            "Ultimate Model",
		Enabled:         true,
		Internal:        true, // INTERNAL — routes through executeInternal
		Credentials: models.TestRefs("test-credential"),
		InternalModel:   "internal-model",
		InternalBaseURL: upstream.URL,
	})
	if err != nil {
		t.Fatalf("AddModel (ultimate): %v", err)
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
	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter, nil)

	body := map[string]interface{}{
		"model":  "any-model",
		"stream": false,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	bodyBytes, _ := json.Marshal(body)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)
	httpReq.Header.Set("X-Force-Ultimate-Model", "true")

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("request failed: %d - %s", rec.Code, rec.Body.String())
	}

	all := reqStore.List()
	if len(all) == 0 {
		t.Fatal("no request logs persisted")
	}
	var log *store.RequestLog
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].UltimateModelUsed {
			log = all[i]
			break
		}
	}
	if log == nil {
		t.Fatal("no ultimate request log found")
	}

	var assistantMsg *store.Message
	for i := len(log.Messages) - 1; i >= 0; i-- {
		if log.Messages[i].Role == "assistant" {
			assistantMsg = &log.Messages[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatalf("no assistant message persisted; log messages: %+v", log.Messages)
	}

	if assistantMsg.Content != wantContent {
		t.Errorf("persisted Content = %q, want %q", assistantMsg.Content, wantContent)
	}
	if assistantMsg.Thinking != wantThinking {
		t.Errorf("persisted Thinking = %q, want %q", assistantMsg.Thinking, wantThinking)
	}

	if log.Usage == nil {
		t.Error("Usage was not persisted")
	} else if log.Usage.TotalTokens != 10 {
		t.Errorf("Usage.TotalTokens = %d, want 10", log.Usage.TotalTokens)
	}
}

// TestUltimateModel_PersistsAssistantContentAndThinking_InternalStream is
// the streaming counterpart of TestUltimateModel_PersistsAssistantContentAndThinking_Internal.
//
// The upstream emits SSE chunks with content + reasoning_content deltas.
// The OpenAI provider's processStream path turns each `data:` line into
// a typed StreamEvent; ultimatemodel.executeInternalStream copies
// content + reasoning_content deltas into ExecuteResult.Content and
// ExecuteResult.Thinking, and the outer handler persists the assembled
// store.Message.
func TestUltimateModel_PersistsAssistantContentAndThinking_InternalStream(t *testing.T) {
	const wantContent = "internal streamed answer"
	const wantThinking = "internal streamed reasoning"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"reasoning_content\":\"%s\"}}]}\n\n", wantThinking)
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"%s\"}}]}\n\n", wantContent)
		flusher.Flush()
		fmt.Fprintf(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	db := setupIntegrationDB(t)
	counter := usage.NewCounter(db, database.SQLite)
	tokenStore := auth.NewTokenStore(db, database.SQLite)
	plaintextToken, _, err := tokenStore.CreateToken(
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

	modelsConfig := models.NewModelsConfig()
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
		ID:              "ultimate-model",
		Name:            "Ultimate Model",
		Enabled:         true,
		Internal:        true,
		Credentials: models.TestRefs("test-credential"),
		InternalModel:   "internal-model",
		InternalBaseURL: upstream.URL,
	})
	if err != nil {
		t.Fatalf("AddModel (ultimate): %v", err)
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
	h := NewHandler(cfg, bus, reqStore, bufStore, tokenStore, counter, nil)

	body := map[string]interface{}{
		"model":  "any-model",
		"stream": true,
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	bodyBytes, _ := json.Marshal(body)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+plaintextToken)
	httpReq.Header.Set("X-Force-Ultimate-Model", "true")

	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Fatalf("request failed: %d - %s", rec.Code, rec.Body.String())
	}

	all := reqStore.List()
	var log *store.RequestLog
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].UltimateModelUsed {
			log = all[i]
			break
		}
	}
	if log == nil {
		t.Fatal("no ultimate request log found")
	}
	var assistantMsg *store.Message
	for i := len(log.Messages) - 1; i >= 0; i-- {
		if log.Messages[i].Role == "assistant" {
			assistantMsg = &log.Messages[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatalf("no assistant message persisted; log messages: %+v", log.Messages)
	}
	if assistantMsg.Content != wantContent {
		t.Errorf("persisted Content = %q, want %q", assistantMsg.Content, wantContent)
	}
	if assistantMsg.Thinking != wantThinking {
		t.Errorf("persisted Thinking = %q, want %q", assistantMsg.Thinking, wantThinking)
	}
}
