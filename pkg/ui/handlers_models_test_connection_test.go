package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// =============================================================================
// Test-connection path ("Test Model" button) — provider-native UA parity.
//
// handleTestModel constructs the provider through the SAME factory as the
// real internal path and calls the SAME attribution seam
// (providers.AttachOpenCodeGoAttribution, pkg/ui/server.go:1909) before the
// request goes out. This suite pins that the test path therefore carries the
// identical provider-native headers as the real path:
//
//   - opencode (Zen): the provider-native UA (value == the same exported
//     providers.DefaultOpenCodeUserAgent() the pkg/providers real-path suite
//     asserts), and NO x-opencode-session.
//   - opencode-go: UA + a ses_-prefixed session derived from the degenerate
//     seed — deterministic per (model, credential), stable across clicks.
//   - openai (control): no attribution UA, no session header.
//
// No live network calls: the credential's base_url points at an httptest
// upstream that captures inbound headers.
// =============================================================================

// testModelUpstream spins an httptest upstream answering the non-stream
// chat-completions call and capturing inbound headers per call.
func testModelUpstream(t *testing.T, captures *[]map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captures = append(*captures, map[string]string{
			"session":    r.Header.Get("x-opencode-session"),
			"user_agent": r.Header.Get("User-Agent"),
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
}

// postTestModel drives handleTestModel with the given credential/model.
func postTestModel(t *testing.T, s *Server, credentialID, internalModel string) TestModelResponse {
	t.Helper()
	body, _ := json.Marshal(TestModelRequest{CredentialID: credentialID, InternalModel: internalModel})
	req := httptest.NewRequest(http.MethodPost, "/fe/api/models/test", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleTestModel(w, req)

	var resp TestModelResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
	}
	return resp
}

func newTestModelServer(t *testing.T, upstreamURL string) *Server {
	t.Helper()
	mock := newMockModelsConfigForHandlers()
	for _, c := range []struct{ id, provider string }{
		{"cred-zen", "opencode"},
		{"cred-go", "opencode-go"},
		{"cred-openai", "openai"},
	} {
		mock.AddCredential(models.CredentialConfig{
			ID:       c.id,
			Provider: c.provider,
			APIKey:   "test-key",
			BaseURL:  upstreamURL,
		})
	}
	return &Server{modelsConfig: mock}
}

// TestHandleTestModel_Zen_CarriesProviderNativeUA — the test-path twin of
// the pkg/providers real-path test: Zen gets the provider-native UA and no
// session header; the UA equals the SAME exported default (parity).
func TestHandleTestModel_Zen_CarriesProviderNativeUA(t *testing.T) {
	var captures []map[string]string
	upstream := testModelUpstream(t, &captures)
	defer upstream.Close()

	s := newTestModelServer(t, upstream.URL)
	resp := postTestModel(t, s, "cred-zen", "kimi-k3")
	if !resp.Success {
		t.Fatalf("test model failed: %+v", resp)
	}

	if len(captures) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(captures))
	}
	if got, want := captures[0]["user_agent"], providers.DefaultOpenCodeUserAgent(); got != want {
		t.Errorf("Zen test-path UA: got %q, want %q (must equal the real-path default)", got, want)
	}
	if got := captures[0]["session"]; got != "" {
		t.Errorf("Zen test path must NOT carry x-opencode-session, got %q", got)
	}
}

// TestHandleTestModel_OpencodeGo_CarriesSessionAndUA — the Go test-path twin:
// UA + a deterministic degenerate-seed session (pins the deterministic-seed
// behavior on the test path).
func TestHandleTestModel_OpencodeGo_CarriesSessionAndUA(t *testing.T) {
	var captures []map[string]string
	upstream := testModelUpstream(t, &captures)
	defer upstream.Close()

	s := newTestModelServer(t, upstream.URL)
	resp := postTestModel(t, s, "cred-go", "deepseek-v4.1-flash")
	if !resp.Success {
		t.Fatalf("test model failed: %+v", resp)
	}

	if len(captures) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(captures))
	}
	if got, want := captures[0]["user_agent"], providers.DefaultOpenCodeGoUserAgent(); got != want {
		t.Errorf("opencode-go test-path UA: got %q, want %q", got, want)
	}
	// Deterministic degenerate seed: the UI test request has no conversation
	// anchor, so the session is a pure function of (model, credentialID).
	wantSID := providers.DeriveOpenCodeGoSessionID(providers.OpenCodeGoSessionSeed("", "deepseek-v4.1-flash", "cred-go"))
	if got := captures[0]["session"]; got != wantSID {
		t.Errorf("opencode-go test-path session: got %q, want %q (deterministic degenerate seed)", got, wantSID)
	}
	if !strings.HasPrefix(captures[0]["session"], "ses_") {
		t.Errorf("opencode-go test-path session must be ses_-prefixed, got %q", captures[0]["session"])
	}
}

// TestHandleTestModel_Control_OpenAI_NoAttributionUA — vanilla openai on the
// test path is unchanged: no attribution UA, no session header.
func TestHandleTestModel_Control_OpenAI_NoAttributionUA(t *testing.T) {
	var captures []map[string]string
	upstream := testModelUpstream(t, &captures)
	defer upstream.Close()

	// Even with both override env vars set, a non-OpenCode provider must not
	// receive any attribution header.
	t.Setenv(providers.EnvOpenCodeUserAgent, "should-not-leak/1.0")
	t.Setenv(providers.EnvOpenCodeGoUserAgent, "should-not-leak-either/1.0")

	s := newTestModelServer(t, upstream.URL)
	resp := postTestModel(t, s, "cred-openai", "gpt-4o")
	if !resp.Success {
		t.Fatalf("test model failed: %+v", resp)
	}

	if len(captures) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(captures))
	}
	if got := captures[0]["session"]; got != "" {
		t.Errorf("openai test path must NOT carry x-opencode-session, got %q", got)
	}
	if strings.HasPrefix(captures[0]["user_agent"], "opencode/") {
		t.Errorf("openai test path must NOT carry an attribution UA, got %q", captures[0]["user_agent"])
	}
	if strings.Contains(captures[0]["user_agent"], "should-not-leak") {
		t.Errorf("openai test path must NOT receive an OpenCode UA, got %q", captures[0]["user_agent"])
	}
}

// TestHandleTestModel_Zen_EnvOverride — the OPENCODE_USER_AGENT override
// flows through the test button too.
func TestHandleTestModel_Zen_EnvOverride(t *testing.T) {
	var captures []map[string]string
	upstream := testModelUpstream(t, &captures)
	defer upstream.Close()

	t.Setenv(providers.EnvOpenCodeUserAgent, "my-coding-agent/1.0")
	s := newTestModelServer(t, upstream.URL)
	resp := postTestModel(t, s, "cred-zen", "kimi-k3")
	if !resp.Success {
		t.Fatalf("test model failed: %+v", resp)
	}
	if got := captures[0]["user_agent"]; got != "my-coding-agent/1.0" {
		t.Errorf("Zen test-path override not applied: got %q, want my-coding-agent/1.0", got)
	}
}

// TestHandleTestModel_CrossPathParity_RealVsTestButton — cross-path parity
// (reviewer warning #1): drive BOTH provider paths against the SAME mock
// upstream and assert the captured headers are EQUAL, not merely
// default-shaped:
//
//   - real internal path: factory NewProvider + AttachOpenCodeGoAttribution
//     + ChatCompletion — exactly the sequence the real internal path
//     performs (race_executor.go:367-379; the degenerate empty
//     conversation key matches handleTestModel's synthetic request).
//   - test path: handleTestModel (the UI Test-Model button seam,
//     pkg/ui/server.go:1903-1909).
//
// A future divergence in the test path (a skipped attach, a different UA
// resolution) would previously keep BOTH existing suites green — each only
// compared against the exported default. This test compares the two paths
// AGAINST EACH OTHER: UA parity for both OpenCode flavours, session parity
// for opencode-go (same degenerate seed ⇒ same ses_ id), no session on
// either path for Zen.
func TestHandleTestModel_CrossPathParity_RealVsTestButton(t *testing.T) {
	cases := []struct {
		name         string
		provider     string
		model        string
		credentialID string
	}{
		{"zen", "opencode", "kimi-k3", "cred-zen"},
		{"opencode-go", "opencode-go", "deepseek-v4.1-flash", "cred-go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captures []map[string]string
			upstream := testModelUpstream(t, &captures)
			defer upstream.Close()

			// Real internal path (capture 0): empty conversation key —
			// the same degenerate seed the test button's synthetic
			// request produces, so opencode-go session ids are
			// comparable.
			p, err := providers.NewProvider(tc.provider, "test-key", upstream.URL)
			if err != nil {
				t.Fatalf("provider: %v", err)
			}
			providers.AttachOpenCodeGoAttribution(p, "", tc.model, tc.credentialID)
			if _, err := p.ChatCompletion(context.Background(), &providers.ChatCompletionRequest{
				Model:    tc.model,
				Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
			}); err != nil {
				t.Fatalf("real path call: %v", err)
			}

			// Test path (capture 1): the button, against the same upstream
			// (the credential's base_url is the mock).
			s := newTestModelServer(t, upstream.URL)
			resp := postTestModel(t, s, tc.credentialID, tc.model)
			if !resp.Success {
				t.Fatalf("test path failed: %+v", resp)
			}

			if len(captures) != 2 {
				t.Fatalf("expected 2 upstream calls (real + test), got %d", len(captures))
			}
			real, test := captures[0], captures[1]
			if real["user_agent"] != test["user_agent"] {
				t.Errorf("UA parity: real path %q != test path %q", real["user_agent"], test["user_agent"])
			}
			if real["session"] != test["session"] {
				t.Errorf("session parity: real path %q != test path %q", real["session"], test["session"])
			}
			switch tc.name {
			case "opencode-go":
				if !strings.HasPrefix(test["session"], "ses_") {
					t.Errorf("opencode-go parity session must be ses_-prefixed, got %q", test["session"])
				}
			default: // zen
				if test["session"] != "" {
					t.Errorf("Zen must carry no session header on EITHER path, test path got %q", test["session"])
				}
			}
		})
	}
}
