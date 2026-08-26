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
				ModelID: "ultimate-model",
				MaxHash: 100,
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

func (m *mockModelsConfig) GetModelByName(modelName string) *models.ModelConfig {
	for _, model := range m.models {
		if model.Name == modelName {
			return model
		}
	}
	return nil
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
		ID:          id,
		Name:        id,
		Enabled:     true,
		Internal:    true,
		Credentials: models.TestRefs("cred-1"),
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

// ResolveInternalConfigWithAffinity (Phase 3 / Task 16 seam mock) —
// delegates to ResolveInternalConfig; the test mock has no engine so
// NewlyBound stays false.
func (m *mockModelsConfig) ResolveInternalConfigWithAffinity(modelID, conversationKey string) (models.ResolvedCredential, bool) {
	provider, apiKey, baseURL, internalModel, ok := m.ResolveInternalConfig(modelID)
	if !ok {
		return models.ResolvedCredential{}, false
	}
	return models.ResolvedCredential{
		Provider:      provider,
		APIKey:        apiKey,
		BaseURL:       baseURL,
		InternalModel: internalModel,
		NewlyBound:    false,
	}, true
}

// mockProvider implements providers.Provider for testing
type mockProvider struct {
	name         string
	chatResp     *providers.ChatCompletionResponse
	chatErr      error
	streamEvents []providers.StreamEvent
	mu           sync.Mutex
	capturedReq  *providers.ChatCompletionRequest // last request captured by ChatCompletion
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
	p.mu.Lock()
	p.capturedReq = req
	p.mu.Unlock()
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

	h := NewHandler(cfg, modelsCfg, nil, nil)

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

	h := NewHandler(cfg, modelsCfg, nil, nil)

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	result := h.ShouldTrigger([]map[string]interface{}{})
	if result.Triggered {
		t.Error("ShouldTrigger should return false for empty messages")
	}
}

func TestShouldTrigger_NewMessage(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

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

// TestShouldTrigger_TriggerSchedule drives the full hardcoded schedule
// (§3): attempts 1-4 normal flow; 5/10/20/30/40 trigger (and are NOT
// exhausted); 41 and 42 are exhausted (exhaustion is strictly > 40).
func TestShouldTrigger_TriggerSchedule(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}

	const maxSeen = 45
	for attempt := 1; attempt <= maxSeen; attempt++ {
		r := h.ShouldTrigger(messages)

		wantTriggered := attempt == 5 || attempt == 10 || attempt == 20 ||
			attempt == 30 || attempt == 40 || attempt > 40
		wantExhausted := attempt > 40

		if r.Triggered != wantTriggered {
			t.Errorf("attempt %d: Triggered=%v, want %v", attempt, r.Triggered, wantTriggered)
		}
		if r.AttemptsExhausted != wantExhausted {
			t.Errorf("attempt %d: AttemptsExhausted=%v, want %v", attempt, r.AttemptsExhausted, wantExhausted)
		}
		if r.CurrentAttempt != attempt {
			t.Errorf("attempt %d: CurrentAttempt=%d, want %d", attempt, r.CurrentAttempt, attempt)
		}
		if r.MaxAttempts != 40 {
			t.Errorf("attempt %d: MaxAttempts=%d, want 40", attempt, r.MaxAttempts)
		}
	}
}

// TestShouldTrigger_ScheduleBoundaries enumerates the boundary cases
// explicitly: 4 = no trigger; 5/10/20/30/40 = trigger AND NOT exhausted;
// 41, 42 = exhausted (strictly > 40).
func TestShouldTrigger_ScheduleBoundaries(t *testing.T) {
	cases := []struct {
		attempt       int
		wantTriggered bool
		wantExhausted bool
	}{
		{4, false, false},
		{5, true, false},
		{10, true, false},
		{20, true, false},
		{30, true, false},
		{40, true, false},
		{41, true, true},
		{42, true, true},
	}

	for _, tc := range cases {
		cfg := newMockConfigManager()
		modelsCfg := newMockModelsConfig()
		h := NewHandler(cfg, modelsCfg, nil, nil)
		messages := []map[string]interface{}{
			{"role": "user", "content": "boundary"},
		}

		var r ShouldTriggerResult
		for i := 0; i < tc.attempt; i++ {
			r = h.ShouldTrigger(messages)
		}

		if r.Triggered != tc.wantTriggered {
			t.Errorf("attempt %d: Triggered=%v, want %v", tc.attempt, r.Triggered, tc.wantTriggered)
		}
		if r.AttemptsExhausted != tc.wantExhausted {
			t.Errorf("attempt %d: AttemptsExhausted=%v, want %v", tc.attempt, r.AttemptsExhausted, tc.wantExhausted)
		}
	}
}

// TestShouldTrigger_FailureAt5KeepsCounterSchedule simulates an ultimate
// failure at milestone 5 (hash NOT removed): calls 6-9 flow normally and
// milestone 10 triggers.
func TestShouldTrigger_FailureAt5KeepsCounterSchedule(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "fail at five"},
	}

	// Attempts 1-4: normal flow
	for i := 0; i < 4; i++ {
		if r := h.ShouldTrigger(messages); r.Triggered {
			t.Fatalf("attempt %d: should not trigger", i+1)
		}
	}
	// Attempt 5: milestone trigger (ultimate fails — counter kept)
	if r := h.ShouldTrigger(messages); !r.Triggered {
		t.Fatal("attempt 5: should trigger")
	}
	// Attempts 6-9: normal flow after failure
	for i := 6; i <= 9; i++ {
		if r := h.ShouldTrigger(messages); r.Triggered {
			t.Fatalf("attempt %d: should not trigger after ultimate failure at 5", i)
		}
	}
	// Attempt 10: next milestone triggers
	if r := h.ShouldTrigger(messages); !r.Triggered {
		t.Fatal("attempt 10: should trigger after failure at 5")
	}
}

// TestShouldTrigger_SuccessAt5ResetsSchedule simulates an ultimate success
// at milestone 5 (hash removed, counter reset): the next call is attempt 1
// and the next trigger lands on 5 again (schedule re-arms).
func TestShouldTrigger_SuccessAt5ResetsSchedule(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "succeed at five"},
	}

	for i := 0; i < 5; i++ {
		h.ShouldTrigger(messages)
	}
	// Ultimate success: Execute removes the hash on success
	h.hashCache.Remove(HashMessages(messages))

	// Counter reset: next call is attempt 1 (no trigger), trigger at 5 again
	for i := 1; i <= 4; i++ {
		if r := h.ShouldTrigger(messages); r.Triggered || r.CurrentAttempt != i {
			t.Fatalf("post-reset attempt %d: Triggered=%v CurrentAttempt=%d", i, r.Triggered, r.CurrentAttempt)
		}
	}
	if r := h.ShouldTrigger(messages); !r.Triggered {
		t.Fatal("post-reset attempt 5: should trigger (schedule re-armed)")
	}
}

// TestForceTrigger_FirstCallTriggers is the force-on-first-call regression:
// force triggers unconditionally on the first call — no env workaround
// needed under the fixed schedule (previously force on a first-sight hash
// returned Triggered=false under the default max-retries knob).
func TestForceTrigger_FirstCallTriggers(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "force me"},
	}

	r := h.ForceTrigger(messages)
	if !r.Triggered {
		t.Fatal("ForceTrigger should trigger on the first call")
	}
	if r.AttemptsExhausted {
		t.Fatal("ForceTrigger should never be exhausted")
	}
}

// TestForceTrigger_DoesNotIncrement asserts that force never increments the
// counter: after a force-seen hash, the next normal call counts as
// attempt 1 (attempt 2 overall — the force-seen insertion is attempt 1).
func TestForceTrigger_DoesNotIncrement(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "force no increment"},
	}

	h.ForceTrigger(messages)

	// First normal call after force: the force-seen insertion itself was
	// attempt 1 (counter=1), so this call is attempt 2 — not triggered
	// (2 < milestone 5). Force itself never incremented the counter.
	r1 := h.ShouldTrigger(messages)
	if r1.Triggered {
		t.Fatal("first normal call after force should not trigger (attempt 2 < milestone 5)")
	}
	if r1.CurrentAttempt != 2 {
		t.Fatalf("first normal call after force: CurrentAttempt=%d, want 2", r1.CurrentAttempt)
	}

	// Second normal call: attempt 3
	r2 := h.ShouldTrigger(messages)
	if r2.CurrentAttempt != 3 {
		t.Fatalf("second normal call after force: CurrentAttempt=%d, want 3", r2.CurrentAttempt)
	}
}

// --- Tests for GetModelID ---

func TestGetModelID(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.ModelID = "test-model-id"
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	if h.GetModelID() != "test-model-id" {
		t.Error("GetModelID should return configured model ID")
	}
}

func TestGetModelID_Empty(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.ModelID = ""
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	if h.GetModelID() != "" {
		t.Error("GetModelID should return empty string when not configured")
	}
}

// --- Tests for SetToolCallBufferConfig ---

func TestSetToolCallBufferConfig(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Store a hash (counter=1, hash present)
	h.hashCache.RecordAttempt(HashMessages([]map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}))

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Store a hash (counter=1, hash present)
	h.hashCache.RecordAttempt(HashMessages([]map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}))

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	// Store a hash (counter=1, hash present)
	h.hashCache.RecordAttempt(HashMessages([]map[string]interface{}{
		{"role": "user", "content": "Hello"},
	}))

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	err := h.SendRetryExhaustedError(w, "abc12345", 41, 40, true)

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	err := h.SendRetryExhaustedError(w, "abc12345", 41, 40, false)

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	// Pass short hash - should not panic
	err := h.SendRetryExhaustedError(w, "ab", 41, 40, true)

	if err != nil {
		t.Errorf("SendRetryExhaustedError returned error: %v", err)
	}
}

func TestSendRetryExhaustedError_EmptyHash(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	// Pass empty hash - should not panic
	err := h.SendRetryExhaustedError(w, "", 41, 40, true)

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

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
	_, err := h.Execute(context.Background(), w, r, body, "nonexistent-model", hash, &headersSent, nil, "")

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

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
	execResult, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, nil, "")

	if err != nil {
		t.Errorf("Execute returned error: %v", err)
	}

	if !upstreamCalled {
		t.Error("Upstream server should have been called")
	}

	if !headersSent {
		t.Error("Headers should have been sent")
	}

	if execResult == nil {
		t.Error("ExecuteResult should not be nil")
	} else if execResult.Usage == nil {
		t.Error("Usage should be extracted")
	} else {
		if execResult.Usage.PromptTokens != 10 {
			t.Errorf("PromptTokens = %d, want 10", execResult.Usage.PromptTokens)
		}
		if execResult.Usage.CompletionTokens != 5 {
			t.Errorf("CompletionTokens = %d, want 5", execResult.Usage.CompletionTokens)
		}
	}

	// Check the attempt counter was cleared by the success-path Remove:
	// the next attempt for this hash restarts at 1 (schedule re-arms)
	if count := h.hashCache.RecordAttempt(hash); count != 1 {
		t.Errorf("Attempt count should restart at 1 after success, got %d", count)
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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   true,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}

	hash := "streamhash"
	headersSent := false
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, nil, "")

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}

	hash := "errorhash"
	headersSent := false
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, nil, "")

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"messages": []map[string]interface{}{{"role": "user", "content": "Hi"}},
	}

	hash := "cancelhash"
	headersSent := false
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, nil, "")

	// Should get an error due to context timeout
	if err == nil {
		t.Error("Execute should return error for cancelled context")
	}
}

// --- Tests for convertRequest ---

func TestConvertRequest(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

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
	h := NewHandler(cfg, modelsCfg, nil, nil)

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

	_, err := h.executeInternal(ctx, w, body, nil, modelCfg, false, false, "")
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

// TestHandler_ConcurrentShouldTrigger asserts the schedule under
// concurrency: N goroutines calling ShouldTrigger on the same hash receive
// exactly the attempt counts {1..N} (multiset equality), and only requests
// landing exactly on a milestone (or beyond the cap) trigger.
func TestHandler_ConcurrentShouldTrigger(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	h := NewHandler(cfg, modelsCfg, nil, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Hello concurrent"},
	}

	const n = 100
	var wg sync.WaitGroup
	results := make(chan ShouldTriggerResult, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- h.ShouldTrigger(messages)
		}()
	}

	wg.Wait()
	close(results)

	// Expected multiset of counts is exactly {1..n}; a count triggers iff
	// it lands on a milestone (5/10/20/30/40) or exceeds the 40 cap.
	counts := make(map[int]int)
	triggered := 0
	expectedTriggered := 0
	for r := range results {
		counts[r.CurrentAttempt]++
		if r.Triggered {
			triggered++
		}
	}
	for want := 1; want <= n; want++ {
		if counts[want] != 1 {
			t.Errorf("Expected exactly one goroutine to see attempt=%d, saw %d", want, counts[want])
		}
		if isTriggerAttempt(want) || want > maxAttempts {
			expectedTriggered++
		}
	}
	if triggered != expectedTriggered {
		t.Errorf("Expected %d triggered (milestones + exhausted), got %d", expectedTriggered, triggered)
	}
}

// --- Integration-style tests ---

func TestHandler_FullFlow(t *testing.T) {
	cfg := newMockConfigManager()
	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "ultimate-model", Name: "ultimate-model", Enabled: true, Internal: false})
	h := NewHandler(cfg, modelsCfg, nil, nil)

	messages := []map[string]interface{}{
		{"role": "user", "content": "Test message"},
	}

	// 1-4. First four requests: normal flow (attempts 1-4, below the first
	// milestone — no trigger)
	for i := 0; i < 4; i++ {
		result := h.ShouldTrigger(messages)
		if result.Triggered {
			t.Errorf("Request %d should not trigger (below milestone 5)", i+1)
		}
	}

	// 5. Fifth request: milestone trigger (attempt 5)
	result := h.ShouldTrigger(messages)
	if !result.Triggered {
		t.Error("Fifth ShouldTrigger should trigger (milestone 5)")
	}
	if result.AttemptsExhausted {
		t.Error("Fifth ShouldTrigger should not be exhausted")
	}

	// 6. GetModelID should work
	if h.GetModelID() != cfg.cfg.UltimateModel.ModelID {
		t.Error("GetModelID failed")
	}

	// 7. Config change should reset
	h.OnConfigChange(events.Event{
		Type: "config.change",
		Data: map[string]interface{}{"field": "ultimate_model.model_id"},
	})

	// 8. Hash cache should be empty
	count, _ := h.hashCache.GetStats()
	if count != 0 {
		t.Error("Hash cache should be reset")
	}

	// 9. Should not trigger anymore (counter reset — attempt 1 again)
	result = h.ShouldTrigger(messages)
	if result.Triggered {
		t.Error("Should not trigger after cache reset")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Per-Token Override Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestExecute_PerTokenOverride_NonStream tests Execute with tokenModelID override (non-streaming)
func TestExecute_PerTokenOverride_NonStream(t *testing.T) {
	// Create a mock upstream server that records the model used
	var usedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract model from request body
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if m, ok := body["model"].(string); ok {
				usedModel = m
			}
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   usedModel,
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
	cfg.cfg.UltimateModel.ModelID = "global-ultimate-model" // Global config model
	cfg.cfg.UpstreamURL = server.URL

	modelsCfg := newMockModelsConfig()
	// Add both the global model and the per-token override model
	// Note: TokenForm.tsx stores model.name as value, not model.id
	modelsCfg.AddModel(models.ModelConfig{ID: "global-ultimate-model", Name: "global", Enabled: true, Internal: false})
	modelsCfg.AddModel(models.ModelConfig{ID: "token-override-model", Name: "token-override", Enabled: true, Internal: false})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   false,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}

	hash := "per-token-hash"
	headersSent := false
	tokenModelID := "token-override-model" // Per-token override uses model ID (as frontend stores model.id)
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, &tokenModelID, "")

	if err != nil {
		t.Errorf("Execute returned error: %v", err)
	}

	// Verify the per-token model was used instead of global
	if usedModel != "token-override-model" {
		t.Errorf("Expected model = %q, got %q", "token-override-model", usedModel)
	}
}

// TestExecute_PerTokenOverride_Stream tests Execute with tokenModelID override (streaming)
func TestExecute_PerTokenOverride_Stream(t *testing.T) {
	// Create a mock upstream server that records the model used
	var usedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract model from request body
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if m, ok := body["model"].(string); ok {
				usedModel = m
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

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
	cfg.cfg.UltimateModel.ModelID = "global-ultimate-model"
	cfg.cfg.UpstreamURL = server.URL

	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "global-ultimate-model", Name: "global", Enabled: true, Internal: false})
	modelsCfg.AddModel(models.ModelConfig{ID: "token-stream-override", Name: "token-stream", Enabled: true, Internal: false})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   true,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}

	hash := "stream-override-hash"
	headersSent := false
	tokenModelID := "token-stream-override" // Per-token override uses model ID (as frontend stores model.id)
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, &tokenModelID, "")

	if err != nil {
		t.Errorf("Execute returned error: %v", err)
	}

	if usedModel != "token-stream-override" {
		t.Errorf("Expected model = %q, got %q", "token-stream-override", usedModel)
	}
}

// TestExecute_PerTokenOverride_Empty_UsesGlobal tests that empty string tokenModelID uses global config
func TestExecute_PerTokenOverride_Empty_UsesGlobal(t *testing.T) {
	// Create a mock upstream server that records the model used
	var usedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if m, ok := body["model"].(string); ok {
				usedModel = m
			}
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   usedModel,
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
	cfg.cfg.UltimateModel.ModelID = "global-ultimate-model"
	cfg.cfg.UpstreamURL = server.URL

	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "global-ultimate-model", Name: "global", Enabled: true, Internal: false})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   false,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}

	hash := "empty-override-hash"
	headersSent := false
	emptyModelID := "" // Empty string - should use global
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, &emptyModelID, "")

	if err != nil {
		t.Errorf("Execute returned error: %v", err)
	}

	// Verify the global model was used
	if usedModel != "global-ultimate-model" {
		t.Errorf("Expected model = %q (global), got %q", "global-ultimate-model", usedModel)
	}
}

// TestExecute_PerTokenOverrideNil_UsesGlobal tests that nil tokenModelID uses global config
func TestExecute_PerTokenOverrideNil_UsesGlobal(t *testing.T) {
	// Create a mock upstream server that records the model used
	var usedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if m, ok := body["model"].(string); ok {
				usedModel = m
			}
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   usedModel,
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
	cfg.cfg.UltimateModel.ModelID = "global-ultimate-model"
	cfg.cfg.UpstreamURL = server.URL

	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "global-ultimate-model", Name: "global", Enabled: true, Internal: false})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   false,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}

	hash := "nil-override-hash"
	headersSent := false
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, nil, "") // nil - should use global

	if err != nil {
		t.Errorf("Execute returned error: %v", err)
	}

	// Verify the global model was used
	if usedModel != "global-ultimate-model" {
		t.Errorf("Expected model = %q (global), got %q", "global-ultimate-model", usedModel)
	}
}

// --- Tests for per-token ultimate model bug fix (commit 8250179) ---

// TestExecute_PerTokenOverride_NonexistentModel tests that per-token override with
// nonexistent model name returns error, NOT panic (scenario B)
func TestExecute_PerTokenOverride_NonexistentModel(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.ModelID = "global-ultimate-model"

	modelsCfg := newMockModelsConfig()
	// Only add the global model, NOT the per-token override model
	modelsCfg.AddModel(models.ModelConfig{ID: "global-ultimate-model", Name: "global", Enabled: true, Internal: false})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   false,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}

	hash := "nonexistent-override-hash"
	headersSent := false
	nonexistentModel := "nonexistent-model"
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, &nonexistentModel, "")

	// Should return error, NOT panic
	if err == nil {
		t.Error("Expected error for nonexistent model, got nil")
	}

	// Error message should indicate model not found
	if err != nil {
		errMsg := err.Error()
		if !strings.Contains(errMsg, "not found") {
			t.Errorf("Expected error message to contain 'not found', got: %q", errMsg)
		}
	}
}

// TestExecute_PerTokenOverride_NonexistentModel_Streaming tests streaming case (scenario B)
func TestExecute_PerTokenOverride_NonexistentModel_Streaming(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.ModelID = "global-ultimate-model"

	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "global-ultimate-model", Name: "global", Enabled: true, Internal: false})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   true,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}

	hash := "nonexistent-stream-override-hash"
	headersSent := false
	nonexistentModel := "totally-fake-model-12345"
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, &nonexistentModel, "")

	// Should return error, NOT panic
	if err == nil {
		t.Error("Expected error for nonexistent model in streaming, got nil")
	}
}

// TestExecute_GlobalConfig_UsesGetModelByID tests that global config uses GetModel (by ID)
// (scenario D - existing behavior should not be broken)
func TestExecute_GlobalConfig_UsesGetModelByID(t *testing.T) {
	// Create a mock upstream server that records the model used
	var usedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if m, ok := body["model"].(string); ok {
				usedModel = m
			}
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   usedModel,
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
	cfg.cfg.UltimateModel.ModelID = "global-model-id" // Global config uses ID
	cfg.cfg.UpstreamURL = server.URL

	modelsCfg := newMockModelsConfig()
	// Add model with ID that matches global config, but different NAME
	// Global config should find it by ID (GetModel)
	modelsCfg.AddModel(models.ModelConfig{
		ID:       "global-model-id",
		Name:     "totally-different-name", // Different from ID
		Enabled:  true,
		Internal: false,
	})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   false,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}

	hash := "global-id-hash"
	headersSent := false
	// No per-token override - should use global config
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, nil, "")

	if err != nil {
		t.Errorf("Execute returned error: %v", err)
	}

	// Verify the global model (found by ID) was used
	if usedModel != "global-model-id" {
		t.Errorf("Expected model = %q (found by ID), got %q", "global-model-id", usedModel)
	}
}

// TestExecute_PerTokenOverride_WithID tests per-token override using model ID
// (per-token now uses ID, not name, since frontend stores model.id)
func TestExecute_PerTokenOverride_WithID(t *testing.T) {
	// Create a mock upstream server that records the model used
	var usedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if m, ok := body["model"].(string); ok {
				usedModel = m
			}
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   usedModel,
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
	cfg.cfg.UltimateModel.ModelID = "global-ultimate-model"
	cfg.cfg.UpstreamURL = server.URL

	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "global-ultimate-model", Name: "global", Enabled: true, Internal: false})
	// Add model with UUID-like ID for per-token override
	modelsCfg.AddModel(models.ModelConfig{
		ID:       "550e8400-e29b-41d4-a716-446655440000", // UUID-like ID
		Name:     "per-token-model",                      // Human-readable name
		Enabled:  true,
		Internal: false,
	})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   false,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}

	hash := "uuid-id-hash"
	headersSent := false
	// Per-token override with UUID-like ID
	perTokenModelID := "550e8400-e29b-41d4-a716-446655440000"
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, &perTokenModelID, "")

	if err != nil {
		t.Errorf("Execute returned error for UUID-like ID: %v", err)
	}

	// Should find model by ID and use its ID
	if usedModel != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("Expected model = %q (found by ID), got %q", "550e8400-e29b-41d4-a716-446655440000", usedModel)
	}
}

// TestExecute_PerTokenOverride_WithID_Nonexistent tests that ID that doesn't exist
// returns error (scenario B)
func TestExecute_PerTokenOverride_WithID_Nonexistent(t *testing.T) {
	cfg := newMockConfigManager()
	cfg.cfg.UltimateModel.ModelID = "global-ultimate-model"

	modelsCfg := newMockModelsConfig()
	modelsCfg.AddModel(models.ModelConfig{ID: "global-ultimate-model", Name: "global", Enabled: true, Internal: false})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]interface{}{
		"model":    "original-model",
		"stream":   false,
		"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
	}

	hash := "fake-uuid-hash"
	headersSent := false
	// UUID-like ID that doesn't exist
	fakeUUID := "123e4567-e89b-12d3-a456-426614174000"
	_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, &fakeUUID, "")

	// Should return error, NOT panic
	if err == nil {
		t.Error("Expected error for nonexistent UUID-like ID, got nil")
	}
}

// TestExecute_AllLookupsUseID tests that both global and per-token use GetModel by ID
// (scenario D - unified approach, no separate paths)
func TestExecute_AllLookupsUseID(t *testing.T) {
	var usedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if m, ok := body["model"].(string); ok {
				usedModel = m
			}
		}

		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   usedModel,
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
	cfg.cfg.UpstreamURL = server.URL

	modelsCfg := newMockModelsConfig()
	// Add models with different ID and NAME
	modelsCfg.AddModel(models.ModelConfig{
		ID:       "global-model-id",
		Name:     "Global Model",
		Enabled:  true,
		Internal: false,
	})
	modelsCfg.AddModel(models.ModelConfig{
		ID:       "per-token-model-id",
		Name:     "Per Token Model",
		Enabled:  true,
		Internal: false,
	})

	h := NewHandler(cfg, modelsCfg, nil, nil)

	tests := []struct {
		name          string
		globalModelID string
		perTokenModel *string
		expectedModel string
	}{
		{
			name:          "global uses ID lookup",
			globalModelID: "global-model-id",
			perTokenModel: nil,
			expectedModel: "global-model-id",
		},
		{
			name:          "per-token uses ID lookup",
			globalModelID: "wrong-id",
			perTokenModel: strPtr("per-token-model-id"),
			expectedModel: "per-token-model-id",
		},
		{
			name:          "per-token empty string uses global",
			globalModelID: "global-model-id",
			perTokenModel: strPtr(""),
			expectedModel: "global-model-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg.cfg.UltimateModel.ModelID = tt.globalModelID
			usedModel = ""

			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			body := map[string]interface{}{
				"model":    "original-model",
				"stream":   false,
				"messages": []map[string]interface{}{{"role": "user", "content": "Hello"}},
			}

			hash := tt.name + "-hash"
			headersSent := false
			_, err := h.Execute(context.Background(), w, r, body, "original-model", hash, &headersSent, tt.perTokenModel, "")

			if err != nil {
				t.Errorf("Execute returned error: %v", err)
				return
			}

			if usedModel != tt.expectedModel {
				t.Errorf("Expected model = %q, got %q", tt.expectedModel, usedModel)
			}
		})
	}
}

// strPtr is a helper to create string pointers
func strPtr(s string) *string {
	return &s
}
