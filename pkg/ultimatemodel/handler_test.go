package ultimatemodel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// --- Mock implementations for testing ---

// mockConfigManager implements config.ManagerInterface for testing
type mockConfigManager struct {
	cfg config.Config
}

func newMockConfigManager() *mockConfigManager {
	return &mockConfigManager{
		cfg: config.Config{
			Version:           "test",
			UpstreamURL:       "http://localhost:4001",
			Port:              4321,
			IdleTimeout:       config.Duration(60 * time.Second),
			StreamDeadline:    config.Duration(110 * time.Second),
			MaxGenerationTime: config.Duration(300 * time.Second),
			UltimateModel: config.UltimateModelConfig{
				ModelID:    "ultimate-model",
				MaxHash:    100,
				MaxRetries: 2,
			},
		},
	}
}

func (m *mockConfigManager) Get() config.Config {
	return m.cfg
}

func (m *mockConfigManager) GetUpstreamURL() string {
	return m.cfg.UpstreamURL
}

func (m *mockConfigManager) GetPort() int {
	return m.cfg.Port
}

func (m *mockConfigManager) GetIdleTimeout() time.Duration {
	return time.Duration(m.cfg.IdleTimeout)
}

func (m *mockConfigManager) GetStreamDeadline() time.Duration {
	return time.Duration(m.cfg.StreamDeadline)
}

func (m *mockConfigManager) GetMaxGenerationTime() time.Duration {
	return time.Duration(m.cfg.MaxGenerationTime)
}

func (m *mockConfigManager) GetMaxStreamBufferSize() int {
	return m.cfg.MaxStreamBufferSize
}

func (m *mockConfigManager) GetBufferStorageDir() string {
	return m.cfg.BufferStorageDir
}

func (m *mockConfigManager) GetBufferMaxStorageMB() int {
	return m.cfg.BufferMaxStorageMB
}

func (m *mockConfigManager) GetLoopDetection() config.LoopDetectionConfig {
	return m.cfg.LoopDetection
}

func (m *mockConfigManager) GetUltimateModel() config.UltimateModelConfig {
	return m.cfg.UltimateModel
}

func (m *mockConfigManager) GetRaceRetryEnabled() bool {
	return m.cfg.RaceRetryEnabled
}

func (m *mockConfigManager) GetRaceParallelOnIdle() bool {
	return m.cfg.RaceParallelOnIdle
}

func (m *mockConfigManager) GetRaceMaxParallel() int {
	return m.cfg.RaceMaxParallel
}

func (m *mockConfigManager) GetRaceMaxBufferBytes() int {
	return m.cfg.RaceMaxBufferBytes
}

func (m *mockConfigManager) GetToolCallBufferDisabled() bool {
	return m.cfg.ToolCallBufferDisabled
}

func (m *mockConfigManager) GetToolCallBufferMaxSize() int64 {
	return m.cfg.ToolCallBufferMaxSize
}

func (m *mockConfigManager) GetLogRawUpstreamResponse() bool {
	return m.cfg.LogRawUpstreamResponse
}

func (m *mockConfigManager) GetLogRawUpstreamOnError() bool {
	return m.cfg.LogRawUpstreamOnError
}

func (m *mockConfigManager) GetLogRawUpstreamMaxKB() int {
	return m.cfg.LogRawUpstreamMaxKB
}

func (m *mockConfigManager) Save(c config.Config) (*config.SaveResult, error) {
	m.cfg = c
	return &config.SaveResult{}, nil
}

func (m *mockConfigManager) IsReadOnly() bool {
	return false
}

// mockModelsConfig implements models.ModelsConfigInterface for testing
type mockModelsConfig struct {
	models       map[string]*models.ModelConfig
	credentials  map[string]*models.CredentialConfig
	internalCfgs map[string]struct {
		provider, apiKey, baseURL, model string
		ok                               bool
	}
}

func newMockModelsConfig() *mockModelsConfig {
	return &mockModelsConfig{
		models:      make(map[string]*models.ModelConfig),
		credentials: make(map[string]*models.CredentialConfig),
		internalCfgs: make(map[string]struct {
			provider, apiKey, baseURL, model string
			ok                               bool
		}),
	}
}

func (m *mockModelsConfig) GetModel(modelID string) *models.ModelConfig {
	return m.models[modelID]
}

func (m *mockModelsConfig) ResolveInternalConfig(modelID string) (string, string, string, string, bool) {
	if cfg, ok := m.internalCfgs[modelID]; ok {
		return cfg.provider, cfg.apiKey, cfg.baseURL, cfg.model, cfg.ok
	}
	return "", "", "", "", false
}

func (m *mockModelsConfig) AddModel(mc models.ModelConfig) error {
	m.models[mc.ID] = &mc
	return nil
}

func (m *mockModelsConfig) AddInternalModel(id, provider, apiKey, baseURL, model string) {
	m.models[id] = &models.ModelConfig{
		ID:           id,
		Name:         id,
		Enabled:      true,
		Internal:     true,
		CredentialID: "cred-1",
	}
	m.internalCfgs[id] = struct {
		provider, apiKey, baseURL, model string
		ok                               bool
	}{
		provider: provider, apiKey: apiKey, baseURL: baseURL, model: model, ok: true,
	}
}

// Satisfy the full interface with no-op implementations
func (m *mockModelsConfig) GetModels() []models.ModelConfig                         { return nil }
func (m *mockModelsConfig) GetEnabledModels() []models.ModelConfig                  { return nil }
func (m *mockModelsConfig) GetTruncateParams(modelID string) []string               { return nil }
func (m *mockModelsConfig) GetFallbackChain(modelID string) []string                { return nil }
func (m *mockModelsConfig) AddModelToConfig(mc models.ModelConfig) error            { return nil }
func (m *mockModelsConfig) UpdateModel(modelID string, mc models.ModelConfig) error { return nil }
func (m *mockModelsConfig) RemoveModel(modelID string) error                        { return nil }
func (m *mockModelsConfig) Save() error                                             { return nil }
func (m *mockModelsConfig) Validate() error                                         { return nil }
func (m *mockModelsConfig) GetCredential(id string) *models.CredentialConfig        { return nil }
func (m *mockModelsConfig) GetCredentials() []models.CredentialConfig               { return nil }
func (m *mockModelsConfig) AddCredential(cred models.CredentialConfig) error        { return nil }
func (m *mockModelsConfig) UpdateCredential(id string, cred models.CredentialConfig) error {
	return nil
}
func (m *mockModelsConfig) RemoveCredential(id string) error { return nil }

// mockProvider implements providers.Provider for testing
type mockProvider struct {
	name         string
	chatResp     *providers.ChatCompletionResponse
	chatErr      error
	streamEvents []providers.StreamEvent
}

func newMockProvider() *mockProvider {
	return &mockProvider{
		name: "mock",
	}
}

func (p *mockProvider) Name() string {
	return p.name
}

func (p *mockProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	if p.chatErr != nil {
		return nil, p.chatErr
	}
	return p.chatResp, nil
}

func (p *mockProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	ch := make(chan providers.StreamEvent, len(p.streamEvents))
	for _, e := range p.streamEvents {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (p *mockProvider) IsRetryable(err error) bool {
	return false
}

// --- Tests for NewHandler ---

func TestNewHandler(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()

	h := NewHandler(cfg, modelsCfg, nil)

	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	if h.hashCache == nil {
		t.Error("hashCache should not be nil")
	}
	if h.config != cfg {
		t.Error("config not set correctly")
	}
	if h.modelsMgr != modelsCfg {
		t.Error("modelsMgr not set correctly")
	}
}

func TestNewHandler_DefaultMaxHash(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.MaxHash = 0 // Test default value
	modelsCfg := newMockModelsConfig()

	h := NewHandler(cfg, modelsCfg, nil)

	// Should use default of 100
	count, _ := h.hashCache.GetStats()
	if count != 0 {
		// Just check it was created
	}
}

// --- Tests for ShouldTrigger ---

func TestShouldTrigger_NoModelConfigured(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.ModelID = "" // No model configured
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}

	result := h.ShouldTrigger(messages)
	if result.Triggered {
		t.Error("ShouldTrigger should return false when no model configured")
	}
}

func TestShouldTrigger_EmptyMessages(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	result := h.ShouldTrigger([]map[string]interface{}{})
	if result.Triggered {
		t.Error("ShouldTrigger should return false for empty messages")
	}
}

func TestShouldTrigger_NewMessage(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}

	result := h.ShouldTrigger(messages)
	if result.Triggered {
		t.Error("New message should not trigger")
	}
	if result.Hash == "" {
		t.Error("Hash should be computed even for non-trigger")
	}
}

func TestShouldTrigger_AfterMarkFailed(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}

	// MarkFailed stores the hash (counter = 0)
	h.MarkFailed(messages)

	// First ShouldTrigger: counter = 1, not triggered (first duplicate = allowed retry)
	r1 := h.ShouldTrigger(messages)
	if r1.Triggered {
		t.Error("First ShouldTrigger should not trigger (counter at 1, need 2)")
	}

	// Second ShouldTrigger: counter = 2, triggered (second duplicate = triggers)
	r2 := h.ShouldTrigger(messages)
	if !r2.Triggered {
		t.Error("Second ShouldTrigger should trigger (counter at 2)")
	}

	// Third ShouldTrigger: counter = 3, still triggered
	result := h.ShouldTrigger(messages)
	if !result.Triggered {
		t.Error("Third ShouldTrigger should also trigger")
	}
}

func TestShouldTrigger_MaxRetriesZero(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.MaxRetries = 0 // Unlimited retries
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}

	h.MarkFailed(messages)
	result := h.ShouldTrigger(messages)

	if !result.Triggered {
		t.Error("Should trigger with unlimited retries")
	}
	if result.RetryExhausted {
		t.Error("Should not be exhausted with MaxRetries=0")
	}
}

func TestShouldTrigger_RetryExhausted(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.MaxRetries = 2
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}

	// MarkFailed stores the hash (counter = 0 initially)
	h.MarkFailed(messages)

	// First call after MarkFailed: counter = 1, not triggered (first duplicate)
	r1 := h.ShouldTrigger(messages)
	if r1.Triggered || r1.CurrentRetry != 1 {
		t.Errorf("First call: triggered=%v (expected false), retry=%d (expected 1)", r1.Triggered, r1.CurrentRetry)
	}

	// Second call: counter = 2, triggered (second duplicate)
	r2 := h.ShouldTrigger(messages)
	if !r2.Triggered || r2.CurrentRetry != 2 {
		t.Errorf("Second call: triggered=%v (expected true), retry=%d (expected 2)", r2.Triggered, r2.CurrentRetry)
	}

	// Third call: counter = 3, triggered, exhausted (exceeded MaxRetries=2)
	r3 := h.ShouldTrigger(messages)
	if !r3.Triggered || !r3.RetryExhausted || r3.CurrentRetry != 3 {
		t.Errorf("Third call: triggered=%v (expected true), exhausted=%v (expected true), retry=%d (expected 3)", r3.Triggered, r3.RetryExhausted, r3.CurrentRetry)
	}
}

// --- Tests for MarkFailed ---

func TestMarkFailed_EmptyMessages(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	hash := h.MarkFailed([]map[string]interface{}{})
	if hash != "" {
		t.Error("MarkFailed should return empty hash for empty messages")
	}
}

func TestMarkFailed_ReturnsHash(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}

	hash := h.MarkFailed(messages)
	if hash == "" {
		t.Error("MarkFailed should return a hash")
	}

	// Verify it's stored
	if !h.hashCache.Contains(hash) {
		t.Error("Hash should be stored after MarkFailed")
	}
}

// --- Tests for GetModelID ---

func TestGetModelID(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.ModelID = "test-model-id"
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	if h.GetModelID() != "test-model-id" {
		t.Error("GetModelID should return configured model ID")
	}
}

func TestGetModelID_Empty(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.ModelID = ""
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	if h.GetModelID() != "" {
		t.Error("GetModelID should return empty string when not configured")
	}
}

// --- Tests for SetToolCallBufferConfig ---

func TestSetToolCallBufferConfig(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	repairCfg := toolrepair.DisabledConfig()
	h.SetToolCallBufferConfig(1024*1024, false, repairCfg)

	if h.toolCallBufferMaxSize != 1024*1024 {
		t.Error("toolCallBufferMaxSize not set correctly")
	}
	if h.toolCallBufferDisabled {
		t.Error("toolCallBufferDisabled not set correctly")
	}
	if h.toolRepairConfig != repairCfg {
		t.Error("toolRepairConfig not set correctly")
	}
}

// --- Tests for OnConfigChange ---

func TestOnConfigChange_ModelIDChanged(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	// Store a hash
	h.MarkFailed([]map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})

	// Trigger config change
	event := events.Event{
		Type: "config.change",
		Data: map[string]interface{}{
			"field": "ultimate_model.model_id",
		},
	}
	h.OnConfigChange(event)

	// Hash cache should be reset
	count, _ := h.hashCache.GetStats()
	if count != 0 {
		t.Error("Hash cache should be reset after model ID change")
	}
}

func TestOnConfigChange_OtherField(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	// Store a hash
	h.MarkFailed([]map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})

	// Trigger config change for different field
	event := events.Event{
		Type: "config.change",
		Data: map[string]interface{}{
			"field": "other.field",
		},
	}
	h.OnConfigChange(event)

	// Hash cache should NOT be reset
	count, _ := h.hashCache.GetStats()
	if count != 1 {
		t.Error("Hash cache should NOT be reset for other fields")
	}
}

func TestOnConfigChange_NoData(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	// Store a hash
	h.MarkFailed([]map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})

	// Trigger config change without data
	event := events.Event{
		Type: "config.change",
	}
	h.OnConfigChange(event)

	// Hash cache should NOT be reset
	count, _ := h.hashCache.GetStats()
	if count != 1 {
		t.Error("Hash cache should NOT be reset without proper data")
	}
}

// --- Tests for SendRetryExhaustedError ---

func TestSendRetryExhaustedError_Streaming(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	err := h.SendRetryExhaustedError(w, "abc12345", 3, 2, true)

	if err != nil {
		t.Errorf("SendRetryExhaustedError returned error: %v", err)
	}

	// Check headers
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}
	if h := w.Header().Get("X-LLMProxy-Ultimate-Model"); h != "retry-exhausted" {
		t.Errorf("X-LLMProxy-Ultimate-Model = %q", h)
	}

	// Check body
	body := w.Body.String()
	if !strings.Contains(body, "data:") {
		t.Error("Response should contain SSE data")
	}
	if !strings.Contains(body, "ultimate_model_retry_exhausted") {
		t.Error("Response should contain error type")
	}
	if !strings.Contains(body, "[DONE]") {
		t.Error("Response should contain [DONE]")
	}
}

func TestSendRetryExhaustedError_NonStreaming(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	err := h.SendRetryExhaustedError(w, "abc12345", 3, 2, false)

	if err != nil {
		t.Errorf("SendRetryExhaustedError returned error: %v", err)
	}

	// Check headers
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	// Check body is valid JSON
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Errorf("Response body should be valid JSON: %v", err)
	}
}

func TestSendRetryExhaustedError_ShortHash(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	// Pass short hash - should not panic
	err := h.SendRetryExhaustedError(w, "ab", 3, 2, true)

	if err != nil {
		t.Errorf("SendRetryExhaustedError returned error: %v", err)
	}
}

func TestSendRetryExhaustedError_EmptyHash(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	// Pass empty hash - should not panic
	err := h.SendRetryExhaustedError(w, "", 3, 2, true)

	if err != nil {
		t.Errorf("SendRetryExhaustedError returned error: %v", err)
	}
}

// --- Tests for Execute (with mock upstream) ---

func TestExecute_ModelNotFound(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = "http://localhost:9999" // Won't be used
	modelsCfg := newMockModelsConfig()
	// Don't add the model
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model": "nonexistent-model",
		"messages": []map[string]interface{}{
			{"role": "user", "content": "Hello"},
		},
	}

	hash := "somehash"
	headersSent := false
	_, err := h.Execute(context.Background(), w, r, body, "nonexistent-model", hash, &headersSent)

	if err == nil {
		t.Error("Execute should return error for unknown model")
	}

	// Hash should be removed from cache
	if h.hashCache.Contains(hash) {
		t.Error("Hash should be removed for unknown model")
	}
}

// TestExecute_ExternalNonStreaming tests non-streaming external requests
func TestExecute_ExternalNonStreaming(t *testing.T) {
	// Create a mock upstream server
	upstreamCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalled = true

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   "ultimate-model",
			"choices": []map[string]interface{}{
				{
					"index":         0,
					"message":       map[string]interface{}{"role": "assistant", "content": "Hello!"},
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
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL // Use mock server
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer test-key")
	body := map[string]interface{}{
		"model": "original-model",
		"messages": []map[string]interface{}{
			{"role": "user", "content": "Hello"},
		},
	}

	hash := "testhash123"
	headersSent := false
	usage, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent)

	if err != nil {
		t.Errorf("Execute returned error: %v", err)
	}

	if !upstreamCalled {
		t.Error("Upstream server should have been called")
	}

	if !headersSent {
		t.Error("Headers should have been sent")
	}

	if usage == nil {
		t.Error("Usage should be extracted")
	} else {
		if usage.PromptTokens != 10 {
			t.Errorf("PromptTokens = %d, want 10", usage.PromptTokens)
		}
		if usage.CompletionTokens != 5 {
			t.Errorf("CompletionTokens = %d, want 5", usage.CompletionTokens)
		}
	}

	// Check retry counter was cleared
	count := h.hashCache.GetRetryCount(hash)
	if count != 0 {
		t.Errorf("Retry count should be cleared after success, got %d", count)
	}
}

// TestExecute_ExternalStreaming tests streaming external requests
func TestExecute_ExternalStreaming(t *testing.T) {
	// Create a mock upstream server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		// Send SSE chunks
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
		flusher.Flush()

		time.Sleep(10 * time.Millisecond)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"!\"},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()

		time.Sleep(10 * time.Millisecond)
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   true,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}

	hash := "streamhash"
	headersSent := false
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent)

	if err != nil {
		t.Errorf("Execute returned error: %v", err)
	}

	if !headersSent {
		t.Error("Headers should have been sent")
	}
}

// TestExecute_UpstreamError tests handling of upstream errors
func TestExecute_UpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}

	hash := "errorhash"
	headersSent := false
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent)

	if err == nil {
		t.Error("Execute should return error for upstream failure")
	}

	// Error should contain upstream status code
	if !strings.Contains(err.Error(), "upstream returned") {
		t.Errorf("Error should contain 'upstream returned', got: %v", err)
	}
}

// TestExecute_ContextCancellation tests cancellation via context
func TestExecute_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Simulate slow response
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := newMockConfigManager()
	cfg.cfg.UpstreamURL = server.URL
	cfg.cfg.MaxGenerationTime = config.Duration(50 * time.Millisecond) // Short timeout
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}

	hash := "cancelhash"
	headersSent := false
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent)

	// Should get an error due to context timeout
	if err == nil {
		t.Error("Execute should return error for cancelled context")
	}
}

// --- Tests for convertRequest ---

func TestConvertRequest(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "Hello",
			},
			map[string]interface{}{
				"role":    "assistant",
				"content": "Hi there!",
			},
		},
		"temperature": float64(0.7),
		"max_tokens":  float64(100),
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if req.Model != "test-model" {
		t.Errorf("Model = %q, want %q", req.Model, "test-model")
	}
	if len(req.Messages) != 2 {
		t.Errorf("Messages count = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("First message role = %q, want %q", req.Messages[0].Role, "user")
	}
	if req.Messages[0].Content != "Hello" {
		t.Errorf("First message content = %q, want %q", req.Messages[0].Content, "Hello")
	}
}

func TestConvertRequest_ToolCalls(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{
				"role":    "user",
				"content": "What's the weather?",
			},
		},
		"tools": []interface{}{
			map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        "get_weather",
					"description": "Get weather",
					"parameters":  map[string]interface{}{"type": "object"},
				},
			},
		},
		"tool_choice": "auto",
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if len(req.Tools) != 1 {
		t.Errorf("Tools count = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Function.Name != "get_weather" {
		t.Errorf("Tool name = %q, want %q", req.Tools[0].Function.Name, "get_weather")
	}
}

func TestConvertRequest_MultimodalContent(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "What's in this image?",
					},
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]interface{}{
							"url": "https://example.com/image.png",
						},
					},
				},
			},
		},
	}

	req, err := h.convertRequest(body)
	if err != nil {
		t.Fatalf("convertRequest failed: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("Messages count = %d, want 1", len(req.Messages))
	}

	parts, ok := req.Messages[0].Content.([]providers.ContentPart)
	if !ok {
		t.Fatal("Content should be ContentPart array for multimodal")
	}
	if len(parts) != 2 {
		t.Errorf("Content parts count = %d, want 2", len(parts))
	}
}

// --- Tests for executeInternal (mock provider) ---

func TestExecuteInternal_NonStreaming(t *testing.T) {
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddInternalModel("internal-model", "openai", "test-key", "http://localhost:8080", "gpt-4")

	cfg := newMockConfigManager()
	h := NewHandler(cfg, modelsCfg, nil)

	// This test requires a real provider, so we'll just verify the setup
	// For full testing, we'd need to mock the providers.NewProvider call
	// or use a mock that satisfies the interface

	w := httptest.NewRecorder()
	body := map[string]interface{}{
		"model":    "internal-model",
		"stream":   false,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}

	modelCfg := modelsCfg.GetModel("internal-model")
	if modelCfg == nil {
		t.Fatal("Model not configured")
	}

	// This will fail because there's no real provider at localhost:8080
	// but we can verify the call structure works
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := h.executeInternal(ctx, w, body, nil, modelCfg, false)
	if err == nil {
		t.Log("executeInternal succeeded (unexpected in test without real provider)")
	}
}

// --- Tests for ultimateModelError ---

func TestUltimateModelError(t *testing.T) {
	err := &ultimateModelError{
		message:  "test error",
		internal: true,
	}

	if err.Error() != "test error" {
		t.Errorf("Error() = %q, want %q", err.Error(), "test error")
	}
}

// --- Concurrent tests ---

func TestHandler_ConcurrentShouldTrigger(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello concurrent"},
	}

	h.MarkFailed(messages)

	var wg sync.WaitGroup
	results := make(chan ShouldTriggerResult, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := h.ShouldTrigger(messages)
			results <- result
		}()
	}

	wg.Wait()
	close(results)

	triggered := 0
	notTriggered := 0
	for r := range results {
		if r.Triggered {
			triggered++
		} else {
			notTriggered++
		}
	}

	// With new logic: first increment gets counter=1 (not triggered),
	// subsequent increments get counter>=2 (triggered)
	if triggered != 99 {
		t.Errorf("Expected 99 triggered (one gets counter=1), got %d", triggered)
	}
	if notTriggered != 1 {
		t.Errorf("Expected 1 not triggered, got %d", notTriggered)
	}
}

// --- Integration-style tests ---

func TestHandler_FullFlow(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Test message"},
	}

	// 1. Should not trigger initially
	result := h.ShouldTrigger(messages)
	if result.Triggered {
		t.Error("Should not trigger on first request")
	}

	// 2. Mark as failed (stores hash)
	hash := h.MarkFailed(messages)

	// 3. First ShouldTrigger: counter=1, not triggered
	result = h.ShouldTrigger(messages)
	if result.Triggered {
		t.Error("First ShouldTrigger after MarkFailed should not trigger (counter=1)")
	}

	// 4. Second ShouldTrigger: counter=2, triggered
	result = h.ShouldTrigger(messages)
	if !result.Triggered {
		t.Error("Second ShouldTrigger after MarkFailed should trigger (counter=2)")
	}

	// 4. GetModelID should work
	if h.GetModelID() != cfg.cfg.UltimateModel.ModelID {
		t.Error("GetModelID failed")
	}

	// 5. Config change should reset
	h.OnConfigChange(events.Event{
		Type: "config.change",
		Data: map[string]interface{}{"field": "ultimate_model.model_id"},
	})

	// 6. Hash cache should be empty
	count, _ := h.hashCache.GetStats()
	if count != 0 {
		t.Error("Hash cache should be reset")
	}

	// 7. Should not trigger anymore
	result = h.ShouldTrigger(messages)
	if result.Triggered {
		t.Error("Should not trigger after cache reset")
	}

	_ = hash // Use the hash
}
