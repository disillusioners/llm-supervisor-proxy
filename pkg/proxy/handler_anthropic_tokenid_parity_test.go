package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// recordingModelsConfig
// ─────────────────────────────────────────────────────────────────────────────

// recordingModelsConfig wraps a real models.ModelsConfigInterface and
// captures every conversationKey passed to
// ResolveInternalConfigWithAffinity so a test can assert the per-token
// salting wiring on the Anthropic path. All other methods are promoted
// from the embedded interface (GetModel, GetCredential, etc.).
//
// This wrapper is the ONLY observable side-channel for arc.conversationKey
// after HandleAnthropicMessages returns (arc itself is local to the
// handler; the conversationKey is consumed downstream by
// attemptAnthropicModel → arc.conf.ModelsConfig.ResolveInternalConfigWithAffinity).
type recordingModelsConfig struct {
	models.ModelsConfigInterface
	keys []string
}

// ResolveInternalConfigWithAffinity delegates to the wrapped config and
// records the conversationKey so the test can read it.
func (r *recordingModelsConfig) ResolveInternalConfigWithAffinity(modelID, conversationKey string) (models.ResolvedCredential, bool) {
	r.keys = append(r.keys, conversationKey)
	return r.ModelsConfigInterface.ResolveInternalConfigWithAffinity(modelID, conversationKey)
}

// ─────────────────────────────────────────────────────────────────────────────
// Test Anthropic tokenID parity (Phase 3 residuals — Item 1)
//
// Verifies that HandleAnthropicMessages salts the conversationKey with
// the authenticated token's ID, bringing the Anthropic path to parity
// with the OpenAI path's W3 fix at handler.go:431-474.
//
// The test MUST discriminate against the previous hard-coded tokenID=""
// wiring (which made every authenticated client share one affinity
// bucket). Pre-fix, keys[0] == keys[1] (both unsalted); post-fix, the
// keys differ because the tokenID salt is different.
// ─────────────────────────────────────────────────────────────────────────────

func TestAnthropic_ConversationKeyPerTokenSalting(t *testing.T) {
	// Upstream is irrelevant: the ModelsConfig recording wrapper
	// captures arc.conversationKey BEFORE the HTTP request, so a
	// minimal 200 responder is enough to let the handler run to
	// completion and not panic on a missing credential. The
	// credential's BaseURL still must be reachable so the handler
	// doesn't fail before reaching the recording call; an
	// httptest.NewServer gives us a localhost target.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Non-streaming OpenAI-shape completion that translates cleanly
		// back to Anthropic.
		w.Write([]byte(`{"id":"test","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(upstream.Close)

	// Two distinct tokens, DIFFERENT IDs — these are the principals
	// whose conversation keys must NOT collide.
	tokens := map[string]*auth.AuthToken{
		"sk-tok-A-payload-padded-to-sixtyfour-hex-chars-aaaaaaaaaaaaaaaa": {
			ID:                   "token-A-uuid",
			Name:                 "Token A",
			UltimateModelEnabled: false,
		},
		"sk-tok-B-payload-padded-to-sixtyfour-hex-chars-bbbbbbbbbbbbbbbb": {
			ID:                   "token-B-uuid",
			Name:                 "Token B",
			UltimateModelEnabled: false,
		},
	}
	tokenStore := &recordingTokenStore{tokens: tokens}

	// Real ModelsConfig with one Internal model + one credential so
	// the resolve path inside attemptAnthropicModel is exercised and
	// the recording wrapper captures the conversationKey.
	modelsCfg := models.NewModelsConfig()
	if err := modelsCfg.AddCredential(models.CredentialConfig{
		ID:       "test-cred",
		Provider: "openai",
		APIKey:   "sk-test",
		BaseURL:  upstream.URL,
	}); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	if err := modelsCfg.AddModel(models.ModelConfig{
		ID:            "anthropic-test-model",
		Name:          "anthropic-test-model",
		Enabled:       true,
		Internal:      true,
		Credentials:   models.TestRefs("test-cred"),
		InternalModel: "gpt-4o-internal",
	}); err != nil {
		t.Fatalf("add model: %v", err)
	}

	rec := &recordingModelsConfig{ModelsConfigInterface: modelsCfg}

	// Build the Handler directly (mirrors newAnthropicTestHandler but
	// with our custom tokenStore + recording ModelsConfig).
	t.Setenv("APPLY_ENV_OVERRIDES", "true")
	t.Setenv("UPSTREAM_URL", upstream.URL)

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: rec,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	h := NewHandler(cfg, bus, reqStore, nil, tokenStore, nil, nil)

	const firstUserMsg = "Hello, world"
	body := anthropicBody("anthropic-test-model", false, []map[string]interface{}{
		{"role": "user", "content": firstUserMsg},
	})

	// ── Case A: token A ────────────────────────────────────────────
	reqA := makeAnthropicRequestWithAuth(t, body, "sk-tok-A-payload-padded-to-sixtyfour-hex-chars-aaaaaaaaaaaaaaaa")
	rec.keys = nil // reset to isolate just this call
	h.HandleAnthropicMessages(httptest.NewRecorder(), reqA)
	if len(rec.keys) == 0 {
		t.Fatalf("expected ResolveInternalConfigWithAffinity to be called for token A; got no recorded keys")
	}
	keyA := rec.keys[len(rec.keys)-1]

	// ── Case B: token B ────────────────────────────────────────────
	reqB := makeAnthropicRequestWithAuth(t, body, "sk-tok-B-payload-padded-to-sixtyfour-hex-chars-bbbbbbbbbbbbbbbb")
	rec.keys = nil
	h.HandleAnthropicMessages(httptest.NewRecorder(), reqB)
	if len(rec.keys) == 0 {
		t.Fatalf("expected ResolveInternalConfigWithAffinity to be called for token B; got no recorded keys")
	}
	keyB := rec.keys[len(rec.keys)-1]

	// ── Case C: anonymous (no auth header) ─────────────────────────
	reqAnon := makeAnthropicRequestWithAuth(t, body, "")
	rec.keys = nil
	h.HandleAnthropicMessages(httptest.NewRecorder(), reqAnon)
	if len(rec.keys) == 0 {
		t.Fatalf("expected ResolveInternalConfigWithAffinity to be called for anonymous request; got no recorded keys")
	}
	keyAnon := rec.keys[len(rec.keys)-1]

	// ── Assertions ─────────────────────────────────────────────────

	// (1) Salted: token A and token B MUST produce DIFFERENT keys for
	// the SAME first-user-message — the whole point of the W3 parity
	// fix. Pre-fix this assertion fails because both paths used "".
	if keyA == keyB {
		t.Fatalf("token-A and token-B produced the SAME conversationKey %q — tokenID is not being salted on the Anthropic path", keyA)
	}
	// (Sanity: keys must look like 64-char hex SHA-256.)
	if len(keyA) != 64 || len(keyB) != 64 || len(keyAnon) != 64 {
		t.Fatalf("expected 64-char hex keys; got A=%d B=%d anon=%d chars", len(keyA), len(keyB), len(keyAnon))
	}

	// (2) Anonymous request MUST produce the UNSALTED baseline key
	// (A-1 graceful degradation), equal to
	// ComputeConversationKey(modelID, "", firstUserMessage).
	wantAnon := credentiallb.ComputeConversationKey("anthropic-test-model", "", firstUserMsg)
	if keyAnon != wantAnon {
		t.Fatalf("anonymous conversationKey = %q, want unsalted baseline %q", keyAnon, wantAnon)
	}

	// (3) Token A and token B salted keys MUST equal
	// ComputeConversationKey(modelID, tokenID, firstUserMessage) with
	// their respective IDs — closes the loop on the wiring.
	wantA := credentiallb.ComputeConversationKey("anthropic-test-model", "token-A-uuid", firstUserMsg)
	wantB := credentiallb.ComputeConversationKey("anthropic-test-model", "token-B-uuid", firstUserMsg)
	if keyA != wantA {
		t.Fatalf("token-A conversationKey = %q, want %q", keyA, wantA)
	}
	if keyB != wantB {
		t.Fatalf("token-B conversationKey = %q, want %q", keyB, wantB)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// recordingTokenStore is a minimal auth.TokenStoreInterface for the
// parity test. It only honors the two tokens registered in the test
// and rejects everything else (returning nil, false from ValidateToken
// so authenticate falls through to nil-tokenID).
type recordingTokenStore struct {
	tokens map[string]*auth.AuthToken
}

func (r *recordingTokenStore) ValidateToken(_ context.Context, plaintext string) (*auth.AuthToken, error) {
	if t, ok := r.tokens[plaintext]; ok {
		return t, nil
	}
	return nil, auth.ErrTokenNotFound
}

// Unused methods — panic if hit, mirroring mockTokenStore pattern.
func (r *recordingTokenStore) CreateToken(context.Context, string, *time.Time, string, bool, string, []string) (string, *auth.AuthToken, error) {
	panic("not implemented")
}
func (r *recordingTokenStore) DeleteToken(context.Context, string) error { panic("not implemented") }
func (r *recordingTokenStore) ListTokens(context.Context) ([]auth.AuthToken, error) {
	panic("not implemented")
}
func (r *recordingTokenStore) GetTokenByID(context.Context, string) (*auth.AuthToken, error) {
	panic("not implemented")
}
func (r *recordingTokenStore) UpdateTokenPermission(context.Context, string, bool, string, []string) error {
	panic("not implemented")
}

// makeAnthropicRequestWithAuth is makeAnthropicRequest with an explicit
// Bearer token (empty string → no Authorization header at all).
func makeAnthropicRequestWithAuth(t *testing.T, body map[string]interface{}, bearer string) *http.Request {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	// Drop x-api-key header that makeAnthropicRequest would set —
	// extractAPIKey checks Bearer first, so this isn't strictly
	// required, but keep the contract tight: the parity test sends
	// ONLY a Bearer header.
	req.Header.Del("x-api-key")
	return req
}