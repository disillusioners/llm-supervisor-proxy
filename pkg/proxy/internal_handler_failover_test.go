package proxy

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

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// Phase 3 / Task 22 — /v1/messages internal hook (InternalHandler seam)
// + anthropic-passthrough internal branch. HTTP-level: a real
// OpenAIProvider (resp. the passthrough client) drives an httptest
// upstream that 429s the FIRST API key it serves and succeeds for every
// other key — deterministic regardless of which credential the
// weighted pick lands on first.
// ─────────────────────────────────────────────────────────────────────────────

// rateLimitOnceUpstream 429s (Retry-After + rate_limit body) the first
// distinct API key it serves; any other key gets a valid 200 response.
type rateLimitOnceUpstream struct {
	mu             sync.Mutex
	failedKey      string
	calls          int
	keys           []string
	speakAnthropic bool
}

func (u *rateLimitOnceUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.calls++
	key := r.Header.Get("Authorization")
	if key == "" {
		key = r.Header.Get("x-api-key")
	}
	u.keys = append(u.keys, key)
	first := u.failedKey == ""
	if first {
		u.failedKey = key
	}
	u.mu.Unlock()

	if first {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limited","type":"rate_limit","code":"rate_limit"}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if u.speakAnthropic {
		// Minimal Anthropic /v1/messages non-stream response.
		fmt.Fprint(w, `{"id":"msg_test","type":"message","role":"assistant","model":"claude-x","content":[{"type":"text","text":"recovered"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`)
		return
	}
	fmt.Fprint(w, `{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"up-m1","choices":[{"index":0,"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
}

func (u *rateLimitOnceUpstream) callCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

func newInternalFailoverModel(credIDs []string, baseURL string) (*mockModelsConfig, *models.ModelConfig) {
	mock := &mockModelsConfig{}
	for _, id := range credIDs {
		mock.AddCredential(models.CredentialConfig{ID: id, Provider: "openai", APIKey: "key-" + id, BaseURL: baseURL})
	}
	mc := &models.ModelConfig{
		ID: "im1", Name: "im1", Enabled: true, Internal: true,
		InternalModel: "up-im1", Credentials: models.TestRefs(credIDs...),
	}
	mock.AddModel(*mc)
	return mock, mc
}

// TestInternalHandler_RateLimitFailover (Task 22 happy path at the
// InternalHandler seam): first credential 429s → ExcludeAndReselect →
// single retry with the reselected credential succeeds. Asserts the
// model_credential_failover event and that model_credential_selected
// is never published on this path.
func TestInternalHandler_RateLimitFailover(t *testing.T) {
	upstream := &rateLimitOnceUpstream{}
	srv := httptest.NewServer(upstream)
	defer srv.Close()

	mock, mc := newInternalFailoverModel([]string{"cred-A", "cred-B"}, srv.URL)

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 21, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("im1", models.TestRefs("cred-A", "cred-B"))
	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	bus := events.NewBus()

	ih := NewInternalHandler(mc, resolver)
	ih.SetCredentialFailover(engine, bus)

	w := httptest.NewRecorder()
	err := ih.HandleRequest(context.Background(), map[string]interface{}{
		"model":    "im1",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}, w, false, "v1m-conv-key-1")

	if err != nil {
		t.Fatalf("expected failover retry to succeed, got: %v", err)
	}
	if got := upstream.callCount(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (429 + single retry)", got)
	}
	// Two DISTINCT keys served.
	upstream.mu.Lock()
	keys := append([]string(nil), upstream.keys...)
	upstream.mu.Unlock()
	if len(keys) == 2 && keys[0] == keys[1] {
		t.Errorf("retry reused the same credential: %v", keys)
	}
	// Recorder holds ONLY the successful body (pre-first-byte guard: the
	// failed initial call wrote nothing).
	if !strings.Contains(w.Body.String(), "recovered") {
		t.Errorf("recorder body missing success payload: %q", w.Body.String())
	}

	// Events: exactly one model_credential_failover; zero selected.
	if got := len(drainEvents(bus, credentiallb.EventCredentialFailover)); got != 1 {
		t.Errorf("failover events = %d, want 1", got)
	}
	if got := len(drainEvents(bus, "model_credential_selected")); got != 0 {
		t.Errorf("model_credential_selected events = %d, want 0 on the /v1/messages path", got)
	}
}

// TestInternalHandler_NoEngine_Propagates (Task 22 nil-safety): without
// SetCredentialFailover the hook is inert — the 429 propagates and
// exactly one upstream call is made.
func TestInternalHandler_NoEngine_Propagates(t *testing.T) {
	upstream := &rateLimitOnceUpstream{}
	srv := httptest.NewServer(upstream)
	defer srv.Close()

	mock, mc := newInternalFailoverModel([]string{"cred-A", "cred-B"}, srv.URL)

	ih := NewInternalHandler(mc, mock) // no SetCredentialFailover

	w := httptest.NewRecorder()
	err := ih.HandleRequest(context.Background(), map[string]interface{}{
		"model":    "im1",
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}, w, false, "v1m-conv-key-2")

	if err == nil {
		t.Fatal("expected the 429 to propagate when no engine is installed")
	}
	if got := upstream.callCount(); got != 1 {
		t.Errorf("upstream calls = %d, want 1", got)
	}
	if !strings.Contains(err.Error(), "429") && !strings.Contains(err.Error(), "rate") {
		t.Logf("propagated error: %v", err)
	}
}

// TestAnthropicPassthrough_RateLimitFailover (Task 22 — Round 3b
// leader ruling (i) branch): internal model + anthropic-provider
// credentials drive doAnthropicRequest passthrough; the first
// credential 429s → hook reselects → the retry succeeds. Pre-write
// guard: the client ResponseWriter receives ONLY the success body.
func TestAnthropicPassthrough_RateLimitFailover(t *testing.T) {
	upstream := &rateLimitOnceUpstream{speakAnthropic: true}
	srv := httptest.NewServer(upstream)
	defer srv.Close()

	mock := &mockModelsConfig{}
	for _, id := range []string{"cred-A", "cred-B"} {
		mock.AddCredential(models.CredentialConfig{ID: id, Provider: "anthropic", APIKey: "key-" + id, BaseURL: srv.URL})
	}
	mc := &models.ModelConfig{
		ID: "am1", Name: "am1", Enabled: true, Internal: true,
		InternalModel: "claude-x", Credentials: models.TestRefs("cred-A", "cred-B"),
	}
	mock.AddModel(*mc)

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 23, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("am1", models.TestRefs("cred-A", "cred-B"))
	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	bus := events.NewBus()
	h := &Handler{credEngine: engine, bus: bus, client: &http.Client{}, store: store.NewRequestStore(100)}

	conf := newTestConfigSnapshot("am1")
	conf.ModelsConfig = resolver
	conf.UpstreamURL = srv.URL

	arc := &anthropicRequestContext{
		conf:                *conf,
		reqID:               "test-arc",
		startTime:           time.Now(),
		reqLog:              &store.RequestLog{ID: "test-arc", Status: "running"},
		baseCtx:             context.Background(),
		method:              http.MethodPost,
		originalHeaders:     http.Header{},
		requestBody:         []byte(`{"model":"am1","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`),
		originalBody:        []byte(`{"model":"am1","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`),
		isAnthropicUpstream: true,
		conversationKey:     "pass-conv-key-1",
	}

	w := httptest.NewRecorder()
	success := h.attemptAnthropicModel(w, arc, 0, "am1")

	if !success {
		t.Fatalf("expected passthrough failover to succeed (recovered body)")
	}
	if got := upstream.callCount(); got != 2 {
		t.Errorf("upstream calls = %d, want 2 (429 + single retry)", got)
	}
	upstream.mu.Lock()
	keys := append([]string(nil), upstream.keys...)
	upstream.mu.Unlock()
	if len(keys) == 2 && keys[0] == keys[1] {
		t.Errorf("retry reused the same credential: %v", keys)
	}
	if !strings.Contains(w.Body.String(), "recovered") {
		t.Errorf("client body missing success payload: %q", w.Body.String())
	}
	if got := len(drainEvents(bus, credentiallb.EventCredentialFailover)); got != 1 {
		t.Errorf("failover events = %d, want 1", got)
	}
	// JSON sanity of the served body.
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Errorf("client body not valid JSON: %v", err)
	}
}

// TestAnthropicPassthrough_HeadersSent_Guard (Task 23): when Anthropic
// bytes were already written (arc.headersSent), the hook must NOT
// retry — the failure propagates as-is.
func TestAnthropicPassthrough_HeadersSent_Guard(t *testing.T) {
	upstream := &rateLimitOnceUpstream{speakAnthropic: true}
	srv := httptest.NewServer(upstream)
	defer srv.Close()

	mock := &mockModelsConfig{}
	for _, id := range []string{"cred-A", "cred-B"} {
		mock.AddCredential(models.CredentialConfig{ID: id, Provider: "anthropic", APIKey: "key-" + id, BaseURL: srv.URL})
	}
	mc := &models.ModelConfig{
		ID: "am1", Name: "am1", Enabled: true, Internal: true,
		InternalModel: "claude-x", Credentials: models.TestRefs("cred-A", "cred-B"),
	}
	mock.AddModel(*mc)

	engine := credentiallb.NewEngine(time.Hour, time.Hour, 29, 60*time.Second)
	t.Cleanup(engine.Stop)
	engine.RebindFromStore("am1", models.TestRefs("cred-A", "cred-B"))
	resolver := &engineBackedResolver{mockModelsConfig: mock, engine: engine}

	h := &Handler{credEngine: engine, bus: events.NewBus(), client: &http.Client{}, store: store.NewRequestStore(100)}

	conf := newTestConfigSnapshot("am1")
	conf.ModelsConfig = resolver
	conf.UpstreamURL = srv.URL

	arc := &anthropicRequestContext{
		conf:                *conf,
		reqID:               "test-arc-guard",
		startTime:           time.Now(),
		reqLog:              &store.RequestLog{ID: "test-arc-guard", Status: "running"},
		baseCtx:             context.Background(),
		method:              http.MethodPost,
		originalHeaders:     http.Header{},
		requestBody:         []byte(`{"model":"am1","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`),
		originalBody:        []byte(`{"model":"am1","messages":[{"role":"user","content":"hi"}],"max_tokens":16}`),
		isAnthropicUpstream: true,
		conversationKey:     "pass-conv-key-2",
		headersSent:         true, // ALREADY writing to the client
	}

	w := httptest.NewRecorder()
	success := h.attemptAnthropicModel(w, arc, 0, "am1")

	if success {
		t.Fatal("expected failure to propagate when headers already sent (pre-first-byte guard)")
	}
	if got := upstream.callCount(); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (no retry post-first-byte)", got)
	}
}
