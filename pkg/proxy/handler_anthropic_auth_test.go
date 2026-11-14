package proxy

// ─────────────────────────────────────────────────────────────────────────────
// Security hotfix (defect v191 / anomaly 4a231ed0) — Anthropic-lane auth
// parity pins.
//
// The /v1/messages route called authenticate() and DISCARDED the result (its
// only consumer was the per-principal conversation-key salt), so ANY caller —
// including fully anonymous ones — reached every catalog model. These tests
// pin the post-fix contract, mirroring the chat lane's Model Access Control
// tests in handler_test.go (TestHandler_ModelNotAllowed_Returns403 /
// TestHandler_ModelAllowed_PassesThrough / TestHandler_AllModelsAllowed_
// PassesThrough):
//
//   (a) no token                       ⇒ 401 sendAuthError
//   (a') invalid/expired token         ⇒ 401 sendAuthError
//   (b) token, out-of-scope model      ⇒ 403 sendModelNotAllowedError
//   (c) token, allowed model           ⇒ 200 (chat semantics preserved)
//   (c') token, no allowed_models list ⇒ 200 (fail-open parity)
//
// The anonymous-401 regression is additionally pinned inside
// TestAnthropic_ConversationKeyPerTokenSalting Case C (key derivation must
// NOT run for a rejected request).
// ─────────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// newAnthropicAuthTestHandler builds a Handler wired with the given token
// store, one internal model ("claude-sonnet-4-5") whose credential points at
// a counting mock upstream, and the canonical anthropic request/response
// builders. Mirrors the chat-lane auth-test fixture (newTestManagerWithConfig
// + modelAccessMockTokenStore); the internal model row is seeded via
// seedInternalFixtureNamed. The two lanes' contracts are pinned against
// identical wiring.
func newAnthropicAuthTestHandler(t *testing.T, hits *atomic.Int64, tokenStore auth.TokenStoreInterface) *Handler {
	t.Helper()

	upstream := httptest.NewServer(upstreamCountingHandler(hits))

	mgr := newTestManagerWithConfig(t, upstream.URL)

	mc := models.NewModelsConfig()
	seedInternalFixtureNamed(t, mc, upstream.URL, "claude-sonnet-4-5")

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: mc,
	}
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	h := NewHandler(cfg, bus, reqStore, nil, tokenStore, nil, nil, nil)

	t.Cleanup(func() { upstream.Close() })
	return h
}

// anthropicAuthFixtureToken returns a mock token store holding one token
// under plaintext with the given allowed-models list.
func anthropicAuthFixtureToken(plaintext string, allowedModels []string) *modelAccessMockTokenStore {
	tokenStore := newModelAccessMockTokenStore()
	tokenStore.addToken(plaintext, &auth.AuthToken{
		ID:            "auth-hotfix-token-id",
		Name:          "Auth Hotfix Token",
		AllowedModels: allowedModels,
	})
	return tokenStore
}

// TestAnthropic_NoToken_Returns401 pins regression (a): the previously
// anonymous call (defect v191 — runtime-proven 200 at v191 with tokenID
// empty) must now be rejected with 401 BEFORE any upstream traffic.
func TestAnthropic_NoToken_Returns401(t *testing.T) {
	var hits atomic.Int64
	plaintext := "sk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	h := newAnthropicAuthTestHandler(t, &hits, anthropicAuthFixtureToken(plaintext, nil))

	body := anthropicBody("claude-sonnet-4-5", false, []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})
	req := makeAnthropicRequestWithAuth(t, body, "") // no Bearer, no x-api-key
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no-token /v1/messages: status = %d, want 401 (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Invalid or expired API key") {
		t.Errorf("401 body must carry the chat-lane auth error message, got: %s", rr.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("unauthenticated request must make ZERO upstream requests; got %d", hits.Load())
	}

	reqs := h.store.List()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request in store, got %d", len(reqs))
	}
	if reqs[0].Status != "failed" {
		t.Errorf("expected status 'failed', got '%s'", reqs[0].Status)
	}
	if !strings.Contains(reqs[0].Error, "Authentication failed") {
		t.Errorf("expected error to contain 'Authentication failed', got: %s", reqs[0].Error)
	}
}

// TestAnthropic_InvalidToken_Returns401 pins (a'): a well-formed but unknown
// key is the same contract as no key — 401, zero upstream traffic.
func TestAnthropic_InvalidToken_Returns401(t *testing.T) {
	var hits atomic.Int64
	plaintext := "sk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	h := newAnthropicAuthTestHandler(t, &hits, anthropicAuthFixtureToken(plaintext, nil))

	body := anthropicBody("claude-sonnet-4-5", false, []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})
	req := makeAnthropicRequestWithAuth(t, body, "sk-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("invalid-token /v1/messages: status = %d, want 401 (body=%s)", rr.Code, rr.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("invalid-token request must make ZERO upstream requests; got %d", hits.Load())
	}
	reqs := h.store.List()
	if len(reqs) != 1 || reqs[0].Status != "failed" {
		t.Fatalf("expected 1 failed request row, got %d rows", len(reqs))
	}
}

// TestAnthropic_ModelNotAllowed_Returns403 pins (b): a token whose
// allowed_models list excludes the requested model gets the chat lane's 403
// envelope, again with zero upstream traffic.
func TestAnthropic_ModelNotAllowed_Returns403(t *testing.T) {
	var hits atomic.Int64
	plaintext := "sk-cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	h := newAnthropicAuthTestHandler(t, &hits, anthropicAuthFixtureToken(plaintext, []string{"some-other-model"}))

	body := anthropicBody("claude-sonnet-4-5", false, []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})
	req := makeAnthropicRequestWithAuth(t, body, plaintext)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope model: status = %d, want 403 (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "model not allowed") {
		t.Errorf("expected response body to contain 'model not allowed', got: %s", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "claude-sonnet-4-5") {
		t.Errorf("expected response body to contain denied model name 'claude-sonnet-4-5', got: %s", rr.Body.String())
	}
	if hits.Load() != 0 {
		t.Errorf("denied request must make ZERO upstream requests; got %d", hits.Load())
	}

	reqs := h.store.List()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request in store, got %d", len(reqs))
	}
	if reqs[0].Status != "failed" {
		t.Errorf("expected status 'failed', got '%s'", reqs[0].Status)
	}
	if !strings.Contains(reqs[0].Error, "not allowed") {
		t.Errorf("expected error to contain 'not allowed', got: %s", reqs[0].Error)
	}
}

// TestAnthropic_ModelAllowed_PassesThrough pins (c): chat semantics are
// preserved — an authenticated token whose allowed_models list contains the
// requested model is served normally (200, completed row).
func TestAnthropic_ModelAllowed_PassesThrough(t *testing.T) {
	var hits atomic.Int64
	plaintext := "sk-dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	h := newAnthropicAuthTestHandler(t, &hits, anthropicAuthFixtureToken(plaintext, []string{"claude-sonnet-4-5"}))

	body := anthropicBody("claude-sonnet-4-5", false, []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})
	req := makeAnthropicRequestWithAuth(t, body, plaintext)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
		t.Fatalf("allowed token/model must not be rejected: status = %d (body=%s)", rr.Code, rr.Body.String())
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("allowed request must reach the upstream exactly once; got %d", hits.Load())
	}

	reqs := h.store.List()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request in store, got %d", len(reqs))
	}
	if reqs[0].Status != "completed" {
		t.Errorf("expected status 'completed', got '%s'", reqs[0].Status)
	}
}

// TestAnthropic_UnrestrictedToken_PassesThrough pins (c'): chat-lane parity
// with TestHandler_AllModelsAllowed_PassesThrough — a token with a nil/empty
// allowed_models list may access any model (fail-open, pkg/auth/token.go).
func TestAnthropic_UnrestrictedToken_PassesThrough(t *testing.T) {
	var hits atomic.Int64
	plaintext := "sk-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	h := newAnthropicAuthTestHandler(t, &hits, anthropicAuthFixtureToken(plaintext, nil))

	body := anthropicBody("claude-sonnet-4-5", false, []map[string]interface{}{
		{"role": "user", "content": "Hello"},
	})
	req := makeAnthropicRequestWithAuth(t, body, plaintext)
	rr := httptest.NewRecorder()

	h.HandleAnthropicMessages(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("unrestricted token must be served: status = %d (body=%s)", rr.Code, rr.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("unrestricted request must reach the upstream exactly once; got %d", hits.Load())
	}
}
