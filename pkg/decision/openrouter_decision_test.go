// openrouter_decision_test.go — openrouter-decision-provider
// commission (2026-11-08). Anti-vacuous coverage for the new
// openrouter System One adapter: registry resolution, default-
// URL table, bearer-auth capture at the System One URL, and the
// preflight (safety_identifier) coverage. Mirrors the typesafe
// precedents in handler_test.go / typesafe_coverage_test.go.
package decision

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestDecisionRegistry_OpenRouter_Registered — lookup("openrouter")
// resolves to the shared System One adapter with the openrouter
// name + default base, while openai/typesafe still resolve and
// a chat-only provider (minimax) still 502s.
func TestDecisionRegistry_OpenRouter_Registered(t *testing.T) {
	p, ok := lookup("openrouter")
	if !ok || p == nil {
		t.Fatal("lookup(\"openrouter\") must resolve (registry widened)")
	}
	sp, ok := p.(*systemOneProvider)
	if !ok {
		t.Fatalf("openrouter adapter is %T, want *systemOneProvider", p)
	}
	if sp.name != OpenRouterDecisionProviderName {
		t.Errorf("openrouter adapter name = %q, want %q", sp.name, OpenRouterDecisionProviderName)
	}
	if sp.defaultBase != "https://openrouter.ai/api" {
		t.Errorf("openrouter adapter defaultBase = %q, want %q", sp.defaultBase, "https://openrouter.ai/api")
	}

	for _, name := range []string{"openai", "typesafe"} {
		if got, ok := lookup(name); !ok || got == nil {
			t.Errorf("lookup(%q) must still resolve after openrouter add", name)
		}
	}
	if got, ok := lookup("minimax"); ok || got != nil {
		t.Errorf("lookup(\"minimax\") must NOT resolve (chat-only provider) — got ok=%v", ok)
	}

	// Typesafe adapter identity unchanged (regression guard).
	tp, ok := lookup("typesafe")
	if !ok {
		t.Fatal("typesafe lookup failed")
	}
	tsp, ok := tp.(*systemOneProvider)
	if !ok {
		t.Fatalf("typesafe adapter is %T, want *systemOneProvider", tp)
	}
	if tsp.name != TypesafeProviderName {
		t.Errorf("typesafe adapter name = %q, want %q", tsp.name, TypesafeProviderName)
	}
	if tsp.defaultBase != "https://api.typesafe.ai" {
		t.Errorf("typesafe adapter defaultBase = %q, want %q", tsp.defaultBase, "https://api.typesafe.ai")
	}
}

// TestOpenRouterDecisionProvider_BuildUpstream_DefaultURL — mirror
// of TestOpenAIDecisionProvider_BuildUpstream_DefaultURL /
// TestTypesafeProvider_BuildUpstream URL table. Empty / trailing-
// slash cred.BaseURL fall back to the openrouter default; an
// override host is honored verbatim.
func TestOpenRouterDecisionProvider_BuildUpstream_DefaultURL(t *testing.T) {
	p := NewOpenRouterDecisionProvider()
	cases := []struct {
		name    string
		baseURL string
		wantURL string
	}{
		{name: "empty_default", baseURL: "", wantURL: "https://openrouter.ai/api/v1/systemone"},
		{name: "trailing_slash", baseURL: "https://openrouter.ai/api/", wantURL: "https://openrouter.ai/api/v1/systemone"},
		{name: "override_host", baseURL: "https://eu.openrouter.ai/api", wantURL: "https://eu.openrouter.ai/api/v1/systemone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &DecisionRequest{Model: "decision-router", Input: json.RawMessage(`"x"`),
				Questions: []Question{{Type: QuestionTypePredicate, Name: "foo", Instructions: "?"}}}
			cred := models.ResolvedCredential{
				Provider: "openrouter", InternalModel: "typesafe/jev-1.13",
				APIKey: "k", BaseURL: tc.baseURL,
			}
			raw := []byte(`{"model":"decision-router","input":"x","questions":[{"type":"predicate","name":"foo"}]}`)
			url, body, err := p.BuildUpstream(cred, req, raw)
			if err != nil {
				t.Fatalf("BuildUpstream: %v", err)
			}
			if url != tc.wantURL {
				t.Errorf("URL = %q, want %q", url, tc.wantURL)
			}
			if !strings.Contains(string(body), `"model":"typesafe/jev-1.13"`) {
				t.Errorf("body missing internal-model rewrite: %s", body)
			}
		})
	}
}

// TestHandler_OpenRouter_BearerAuth_AtSystemOneURL — auth capture:
// Authorization: Bearer <key> reaches a mock openrouter upstream
// at the System One URL (path /v1/systemone), with the System
// One translation applied (noul question type).
func TestHandler_OpenRouter_BearerAuth_AtSystemOneURL(t *testing.T) {
	body := systemOneSuccessBody(map[string]interface{}{
		"is_x": map[string]interface{}{"answer": true, "confidence": 0.9},
	}, 10, 0)
	env := newEnvForProvider(t, fakeScript{status: 200, body: body}, "openrouter", "typesafe/jev-1.13", "")
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	env.fake.mu.Lock()
	gotAuth := env.fake.lastAuthH
	gotPath := env.fake.lastPath
	upstreamBody := append([]byte(nil), env.fake.lastBody...)
	env.fake.mu.Unlock()

	if gotAuth != "Bearer sk-cred-secret" {
		t.Errorf("upstream Authorization = %q, want %q (only credential bearer)", gotAuth, "Bearer sk-cred-secret")
	}
	if gotPath != "/v1/systemone" {
		t.Errorf("upstream path = %q, want %q (System One URL)", gotPath, "/v1/systemone")
	}
	if !strings.Contains(string(upstreamBody), `"type":"noul"`) {
		t.Errorf("upstream body missing System One noul translation: %s", upstreamBody)
	}
	if env.fake.hitCount.Load() != 1 {
		t.Errorf("upstream hit count = %d, want 1", env.fake.hitCount.Load())
	}
}

// TestHandler_OpenRouter_SafetyIdentifier_400 — the preflight
// covers openrouter (System One transport): safety_identifier
// ⇒ 400 before any upstream call.
func TestHandler_OpenRouter_SafetyIdentifier_400(t *testing.T) {
	env := newEnvForProvider(t, fakeScript{status: 200, body: []byte(`{}`)}, "openrouter", "typesafe/jev-1.13", "")
	body := mustJSON(map[string]interface{}{
		"model":             env.modelID,
		"input":             "x",
		"questions":         []interface{}{map[string]interface{}{"type": "predicate", "name": "x", "instructions": "?"}},
		"safety_identifier": "user-1234",
	})
	rec := env.sendRequest(http.MethodPost, string(body), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("safety_identifier on openrouter: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if env.fake.hitCount.Load() != 0 {
		t.Errorf("upstream hit count = %d, want 0 (400 fires before upstream call)", env.fake.hitCount.Load())
	}
	if !strings.Contains(rec.Body.String(), "safety_identifier") {
		t.Errorf("response should mention safety_identifier: %s", rec.Body.String())
	}
}

// TestHandler_OpenRouter_UnregisteredChatProvider_502 — a chat-
// only provider (minimax) still 502s on the decision path.
func TestHandler_OpenRouter_UnregisteredChatProvider_502(t *testing.T) {
	env := newEnvForProvider(t, fakeScript{status: 200, body: []byte(`{}`)}, "minimax", "x", "")
	rec := env.sendRequest(http.MethodPost, standardRequestBody(env.modelID), nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("minimax on decision path: status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no decision adapter registered for provider") {
		t.Errorf("response should be the unregistered-provider 502: %s", rec.Body.String())
	}
}
