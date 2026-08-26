package ultimatemodel

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// ─────────────────────────────────────────────────────────────────────────────
// Phase 3 / Task 21 — ultimate-internal rate-limit failover hook tests.
// Mock-based (no live DB): an engine-backed resolver + a scripted
// provider that 429s its first N calls (deterministic regardless of
// which credential the weighted pick lands on first).
// ─────────────────────────────────────────────────────────────────────────────

// failoverModelsConfig overrides the package mock's GetCredential (nil
// in the base mock) and the affinity seam (engine-backed, mirroring
// ModelsManager.ResolveInternalConfigWithAffinity).
type failoverModelsConfig struct {
	*mockModelsConfig
	engine    *credentiallb.Engine
	model     *models.ModelConfig
	creds     map[string]*models.CredentialConfig
	resolves  int
	resolveMu sync.Mutex
}

func (m *failoverModelsConfig) GetModel(modelID string) *models.ModelConfig {
	if m.model != nil && m.model.ID == modelID {
		copy := *m.model
		return &copy
	}
	return nil
}

func (m *failoverModelsConfig) GetCredential(id string) *models.CredentialConfig {
	if c, ok := m.creds[id]; ok {
		copy := *c
		return &copy
	}
	return nil
}

func (m *failoverModelsConfig) ResolveInternalConfigWithAffinity(modelID, conversationKey string) (models.ResolvedCredential, bool) {
	m.resolveMu.Lock()
	m.resolves++
	m.resolveMu.Unlock()
	mc := m.GetModel(modelID)
	if mc == nil || !mc.Internal {
		return models.ResolvedCredential{}, false
	}
	if len(mc.Credentials) <= 1 {
		// E-3 fast path (no engine).
		cred := m.GetCredential(mc.PrimaryCredentialID())
		if cred == nil {
			return models.ResolvedCredential{}, false
		}
		return models.ResolvedCredential{
			Provider: cred.Provider, APIKey: cred.APIKey, BaseURL: cred.BaseURL,
			InternalModel: mc.InternalModel, CredentialID: cred.ID, NewlyBound: false,
		}, true
	}
	credID, newlyBound, err := m.engine.GetOrSelect(modelID, conversationKey)
	if err != nil || credID == "" {
		return models.ResolvedCredential{}, false
	}
	cred := m.GetCredential(credID)
	if cred == nil {
		return models.ResolvedCredential{}, false
	}
	return models.ResolvedCredential{
		Provider: cred.Provider, APIKey: cred.APIKey, BaseURL: cred.BaseURL,
		InternalModel: mc.InternalModel, CredentialID: credID, NewlyBound: newlyBound,
	}, true
}

// scriptedFailoverProvider 429s its first failN calls then succeeds and
// records the API keys served.
type scriptedFailoverProvider struct {
	mu      sync.Mutex
	failN   int
	calls   int
	apiKeys []string
}

func (p *scriptedFailoverProvider) Name() string { return "scripted-ult" }

func (p *scriptedFailoverProvider) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	key := req.Model // carrying key via Model is awkward; use recorded factory key below
	p.mu.Unlock()
	_ = key
	if n <= p.failN {
		return nil, &providers.ProviderError{
			Provider: "scripted-ult", StatusCode: 429,
			Message: "rate limited (scripted)", ErrorType: "rate_limit", ErrorCode: "rate_limit",
			Retryable: true, RetryAfter: 2 * time.Second,
		}
	}
	return &providers.ChatCompletionResponse{
		ID: "chatcmpl-ult", Object: "chat.completion", Created: time.Now().Unix(), Model: req.Model,
		Choices: []providers.Choice{{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "recovered"}, FinishReason: "stop"}},
		Usage:   providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	}, nil
}

func (p *scriptedFailoverProvider) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	return nil, &providers.ProviderError{StatusCode: 429, ErrorType: "rate_limit"}
}

func (p *scriptedFailoverProvider) IsRetryable(err error) bool { return true }

func (p *scriptedFailoverProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newUltFailoverEnv(t *testing.T, credCount, failN int) (*Handler, *failoverModelsConfig, *scriptedFailoverProvider, *events.Bus, *credentiallb.Engine) {
	t.Helper()

	creds := map[string]*models.CredentialConfig{}
	ids := make([]string, credCount)
	for i := 0; i < credCount; i++ {
		id := "cred-" + string(rune('A'+i))
		ids[i] = id
		creds[id] = &models.CredentialConfig{ID: id, Provider: "openai", APIKey: "key-" + id, BaseURL: "http://ult-upstream-" + id}
	}
	mc := &models.ModelConfig{
		ID: "um1", Name: "um1", Enabled: true, Internal: true,
		InternalModel: "up-um1", Credentials: models.TestRefs(ids...),
	}

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 13, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("um1", models.TestRefs(ids...))

	fmc := &failoverModelsConfig{
		mockModelsConfig: newMockModelsConfig(),
		engine:           engine,
		model:            mc,
		creds:            creds,
	}

	scripted := &scriptedFailoverProvider{failN: failN}
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		scripted.mu.Lock()
		scripted.apiKeys = append(scripted.apiKeys, apiKey)
		scripted.mu.Unlock()
		return scripted, nil
	}
	t.Cleanup(func() { newProviderClient = origNew })

	bus := events.NewBus()
	h := NewHandler(newMockConfigManager(), fmc, bus, engine)
	return h, fmc, scripted, bus, engine
}

// TestUltimateInternal_RateLimitFailover_SucceedsOnSecondCredential
// (Task 21 happy path): 2-credential model, first call 429s, retry with
// the reselected credential succeeds. Asserts: exactly one retry (2
// provider calls), a distinct second credential, and the
// model_credential_failover event with the canonical payload.
func TestUltimateInternal_RateLimitFailover_SucceedsOnSecondCredential(t *testing.T) {
	h, _, scripted, bus, _ := newUltFailoverEnv(t, 2, 1)

	body := map[string]interface{}{
		"model":    "um1",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		"stream":   false,
	}
	w := httptest.NewRecorder()

	result, err := h.executeInternalWithRateLimitFailover(
		context.Background(), w, body, nil,
		h.modelsMgr.GetModel("um1"), false, false, "ult-conv-key-1",
	)

	if err != nil {
		t.Fatalf("expected failover to recover, got error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ExecuteResult after failover")
	}
	if got := scripted.callCount(); got != 2 {
		t.Errorf("provider calls = %d, want 2 (initial 429 + single retry)", got)
	}
	// Distinct credentials across the two attempts.
	scripted.mu.Lock()
	keys := append([]string(nil), scripted.apiKeys...)
	scripted.mu.Unlock()
	if len(keys) == 2 && keys[0] == keys[1] {
		t.Errorf("retry reused the same credential: %v", keys)
	}

	// Task 20 event.
	ch, _ := bus.Subscribe()
	defer bus.Unsubscribe(ch)
	var sawFailover bool
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			if evt.Type == credentiallb.EventCredentialFailover {
				sawFailover = true
				data, _ := evt.Data.(map[string]interface{})
				for _, key := range []string{"model_id", "from_credential_id", "to_credential_id", "reason", "retry_after_ms", "cooldown_ms", "attempt_index"} {
					if _, ok := data[key]; !ok {
						t.Errorf("failover payload missing %q: %v", key, data)
					}
				}
			}
			if evt.Type == "model_credential_selected" {
				t.Errorf("ultimate path must NOT publish model_credential_selected (single source of truth in race_executor)")
			}
		default:
			if !sawFailover {
				t.Errorf("no model_credential_failover event observed")
			}
			return
		}
	}
}

// TestUltimateInternal_SingleCredential_Propagates (Task 21): a
// single-credential model must NOT retry — the error propagates in the
// existing failure shape.
func TestUltimateInternal_SingleCredential_Propagates(t *testing.T) {
	// Build a 1-credential env manually (newUltFailoverEnv is multi-cred).
	creds := map[string]*models.CredentialConfig{
		"cred-only": {ID: "cred-only", Provider: "openai", APIKey: "key-only", BaseURL: "http://ult-upstream-only"},
	}
	mc := &models.ModelConfig{
		ID: "um1", Name: "um1", Enabled: true, Internal: true,
		InternalModel: "up-um1", Credentials: models.TestRefs("cred-only"),
	}
	engine := credentiallb.NewEngine(time.Hour, time.Hour, 13, 60*time.Second)
	t.Cleanup(engine.Stop)
	fmc := &failoverModelsConfig{mockModelsConfig: newMockModelsConfig(), engine: engine, model: mc, creds: creds}

	scripted := &scriptedFailoverProvider{failN: 99} // always 429
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) { return scripted, nil }
	t.Cleanup(func() { newProviderClient = origNew })

	h := NewHandler(newMockConfigManager(), fmc, events.NewBus(), engine)

	body := map[string]interface{}{
		"model":    "um1",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}
	w := httptest.NewRecorder()

	_, err := h.executeInternalWithRateLimitFailover(
		context.Background(), w, body, nil, h.modelsMgr.GetModel("um1"), false, false, "ult-conv-key-2",
	)
	if err == nil {
		t.Fatal("expected the 429 to propagate for a single-credential model")
	}
	if got := scripted.callCount(); got != 1 {
		t.Errorf("provider calls = %d, want 1 (no retry on single-cred model)", got)
	}
}

// TestUltimateInternal_NonRateLimit_Propagates (Task 21 control): a
// 500-class provider error must NOT trigger credential failover.
func TestUltimateInternal_NonRateLimit_Propagates(t *testing.T) {
	h, fmc, _, _, _ := newUltFailoverEnv(t, 2, 0)

	// Replace the provider with an always-500 one.
	boom := &providerErrorOnly{err: &providers.ProviderError{StatusCode: 500, Message: "boom"}}
	origNew := newProviderClient
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) { return boom, nil }
	t.Cleanup(func() { newProviderClient = origNew })

	body := map[string]interface{}{
		"model":    "um1",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}
	w := httptest.NewRecorder()

	_, err := h.executeInternalWithRateLimitFailover(
		context.Background(), w, body, nil, h.modelsMgr.GetModel("um1"), false, false, "ult-conv-key-3",
	)
	if err == nil {
		t.Fatal("expected the 500 to propagate (no failover for non-rate-limit)")
	}
	if got := boom.calls; got != 1 {
		t.Errorf("provider calls = %d, want 1 (no retry)", got)
	}
	_ = fmc
}

type providerErrorOnly struct {
	err   error
	calls int
}

func (p *providerErrorOnly) Name() string { return "err-only" }
func (p *providerErrorOnly) ChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (*providers.ChatCompletionResponse, error) {
	p.calls++
	return nil, p.err
}
func (p *providerErrorOnly) StreamChatCompletion(ctx context.Context, req *providers.ChatCompletionRequest) (<-chan providers.StreamEvent, error) {
	p.calls++
	return nil, p.err
}
func (p *providerErrorOnly) IsRetryable(err error) bool { return false }
