package providers

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// =============================================================================
// "opencode" (OpenCode Zen) provider-native UA commission — behavioral tests.
//
// Zen is a DISTINCT provider from opencode-go, sharing only the wire. Its
// provider-native attribution is UA-ONLY: the official-client User-Agent
// default (opencode/{channel}/{version}/{name} → opencode/stable/2.0.32/cli)
// with OPENCODE_USER_AGENT override, and NO x-opencode-session (Zen has no
// known session-header gate; the free-tier "client-lock" is UA-shaped —
// 2026-10-17 Zen research note + research report §c/§e).
//
// The UA is stamped inside the SHARED attribution seam
// (AttachOpenCodeGoAttribution → OpenAIProvider.setHeaders), so the real
// internal path (factory + attach, exactly what race_executor.go:367-379
// performs), the ultimate-model internal path, and the UI test-connection
// path all inherit it with no per-path wiring. The pkg/ui suite
// (handlers_models_test_connection_test.go) pins the test-path twin against the
// SAME exported default, establishing real-path/test-path parity.
//
// No live network calls: the upstream is an httptest server capturing
// inbound headers (jsonCompletionServer, opencodego_test.go).
// =============================================================================

// TestOpencodeZen_UA_DefaultShape pins the Zen UA default: the
// official-client format, and byte-equal to the Go default (the vendor's
// App.useragent is provider-independent).
func TestOpencodeZen_UA_DefaultShape(t *testing.T) {
	got := DefaultOpenCodeUserAgent()
	if got != "opencode/stable/2.0.32/cli" {
		t.Errorf("default UA: got %q, want opencode/stable/2.0.32/cli", got)
	}
	goUA := DefaultOpenCodeGoUserAgent() // evaluated once — one literal pin + one parity assert, no doubled expression
	if got != goUA {
		t.Errorf("Zen default UA %q must equal the Go default %q (provider-independent client UA)", got, goUA)
	}
}

// TestOpencodeZen_UA_OverrideAndWhitespace pins the
// OPENCODE_USER_AGENT resolution contract: trimmed non-empty wins,
// whitespace-only falls back to the default.
func TestOpencodeZen_UA_OverrideAndWhitespace(t *testing.T) {
	// Unset ⇒ default.
	prev, had := os.LookupEnv(EnvOpenCodeUserAgent)
	if err := os.Unsetenv(EnvOpenCodeUserAgent); err != nil {
		t.Fatalf("unset: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(EnvOpenCodeUserAgent, prev)
		} else {
			_ = os.Unsetenv(EnvOpenCodeUserAgent)
		}
	})

	if got := ResolveOpenCodeUserAgent(); got != DefaultOpenCodeUserAgent() {
		t.Errorf("unset: got %q, want default %q", got, DefaultOpenCodeUserAgent())
	}

	// Non-empty override wins (trimmed).
	t.Setenv(EnvOpenCodeUserAgent, "  llm-supervisor-proxy/9.9  ")
	if got := ResolveOpenCodeUserAgent(); got != "llm-supervisor-proxy/9.9" {
		t.Errorf("override: got %q, want llm-supervisor-proxy/9.9 (trimmed)", got)
	}

	// Whitespace-only falls back to the default.
	t.Setenv(EnvOpenCodeUserAgent, "   \t ")
	if got := ResolveOpenCodeUserAgent(); got != DefaultOpenCodeUserAgent() {
		t.Errorf("whitespace: got %q, want default %q", got, DefaultOpenCodeUserAgent())
	}
}

// TestOpencodeGo_UA_WhitespaceFallback completes gate (d) for
// the Go knob: whitespace-only must fall back to the default.
func TestOpencodeGo_UA_WhitespaceFallback(t *testing.T) {
	t.Setenv(EnvOpenCodeGoUserAgent, "   ")
	if got := ResolveOpenCodeGoUserAgent(); got != DefaultOpenCodeGoUserAgent() {
		t.Errorf("whitespace: got %q, want default %q", got, DefaultOpenCodeGoUserAgent())
	}
}

// TestOpencodeZen_RealPath_UAOnly exercises the real-path seam
// (factory ProviderOpencode → AttachOpenCodeGoAttribution → ChatCompletion,
// exactly race_executor.go:367-379's sequence): Zen carries the
// provider-native UA and NO session header.
func TestOpencodeZen_RealPath_UAOnly(t *testing.T) {
	var captures []map[string]string
	server := jsonCompletionServer(t, &captures)
	defer server.Close()

	p, err := NewProvider("opencode", "test-key", server.URL)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}
	// Real path attaches with the thread's conversation key; the Zen UA is
	// conversation-independent so the key value is irrelevant to the UA.
	AttachOpenCodeGoAttribution(p, "conv-key-real-path", "kimi-k3", "cred-1")
	if _, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "kimi-k3", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("call: %v", err)
	}

	if len(captures) != 1 {
		t.Fatalf("expected 1 upstream call, got %d", len(captures))
	}
	if got, want := captures[0]["user_agent"], DefaultOpenCodeUserAgent(); got != want {
		t.Errorf("Zen real-path UA: got %q, want %q", got, want)
	}
	if got := captures[0]["session"]; got != "" {
		t.Errorf("Zen must NOT carry x-opencode-session, got %q", got)
	}
	if got := captures[0]["authorization"]; got != "Bearer test-key" {
		t.Errorf("auth untouched: got %q", got)
	}
}

// TestOpencodeZen_UA_ConversationIndependent — the UA is identical whether
// the request carries a real conversation key (real path) or the degenerate
// empty key (UI test-connection path). This IS the real-path/test-path UA
// parity anchor for Zen; the pkg/ui suite asserts the same exported default
// on the test path.
func TestOpencodeZen_UA_ConversationIndependent(t *testing.T) {
	var captures []map[string]string
	server := jsonCompletionServer(t, &captures)
	defer server.Close()

	calls := []string{"conv-key-a", "", "conv-key-b"}
	for _, ck := range calls {
		p, err := NewProvider("opencode", "test-key", server.URL)
		if err != nil {
			t.Fatalf("provider: %v", err)
		}
		AttachOpenCodeGoAttribution(p, ck, "kimi-k3", "cred-1")
		if _, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "kimi-k3", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
			t.Fatalf("call: %v", err)
		}
	}
	if len(captures) != len(calls) {
		t.Fatalf("expected %d calls, got %d", len(calls), len(captures))
	}
	want := DefaultOpenCodeUserAgent()
	for i, cap := range captures {
		if cap["user_agent"] != want {
			t.Errorf("call %d UA: got %q, want %q (UA must not depend on the conversation key)", i, cap["user_agent"], want)
		}
		if cap["session"] != "" {
			t.Errorf("call %d must NOT carry a session header, got %q", i, cap["session"])
		}
	}
}

// TestOpencodeZen_EnvOverride_AppliedToWire — the OPENCODE_USER_AGENT
// override reaches the wire for Zen.
func TestOpencodeZen_EnvOverride_AppliedToWire(t *testing.T) {
	var captures []map[string]string
	server := jsonCompletionServer(t, &captures)
	defer server.Close()

	t.Setenv(EnvOpenCodeUserAgent, "my-coding-agent/1.0")
	p, _ := NewProvider("opencode", "test-key", server.URL)
	AttachOpenCodeGoAttribution(p, "conv", "kimi-k3", "cred")
	if _, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "kimi-k3", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if got := captures[0]["user_agent"]; got != "my-coding-agent/1.0" {
		t.Errorf("Zen override not applied: got %q, want my-coding-agent/1.0", got)
	}
}

// TestOpencode_EnvOverrides_ScopedToTheirProvider — the two override knobs
// are fully independent: OPENCODE_USER_AGENT must NOT leak to opencode-go,
// and OPENCODE_GO_USER_AGENT must NOT leak to Zen.
func TestOpencode_EnvOverrides_ScopedToTheirProvider(t *testing.T) {
	var captures []map[string]string
	server := jsonCompletionServer(t, &captures)
	defer server.Close()

	t.Setenv(EnvOpenCodeUserAgent, "zen-only/1.0")
	t.Setenv(EnvOpenCodeGoUserAgent, "go-only/1.0")

	// Zen: gets its own override.
	zen, _ := NewProvider("opencode", "test-key", server.URL)
	AttachOpenCodeGoAttribution(zen, "conv", "kimi-k3", "cred")
	if _, err := zen.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "kimi-k3", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("zen: %v", err)
	}
	if got := captures[0]["user_agent"]; got != "zen-only/1.0" {
		t.Errorf("Zen must use OPENCODE_USER_AGENT: got %q, want zen-only/1.0", got)
	}

	// opencode-go: gets its own override, NOT Zen's.
	ocg, _ := NewProvider("opencode-go", "test-key", server.URL)
	AttachOpenCodeGoAttribution(ocg, "conv", "deepseek-v4.1-flash", "cred")
	if _, err := ocg.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "deepseek-v4.1-flash", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("go: %v", err)
	}
	if got := captures[1]["user_agent"]; got != "go-only/1.0" {
		t.Errorf("opencode-go must use OPENCODE_GO_USER_AGENT, not the Zen knob: got %q, want go-only/1.0", got)
	}
	if !strings.HasPrefix(captures[1]["session"], "ses_") {
		t.Errorf("opencode-go must still carry a ses_ session: got %q", captures[1]["session"])
	}
}

// TestOpencodeGo_ByteIdentical — gate: opencode-go behavior is unchanged
// (UA default + ses_ session), regardless of the Zen knob.
func TestOpencodeGo_ByteIdentical(t *testing.T) {
	var captures []map[string]string
	server := jsonCompletionServer(t, &captures)
	defer server.Close()

	// The Zen knob set must not perturb opencode-go.
	t.Setenv(EnvOpenCodeUserAgent, "zen-only/1.0")

	p, _ := NewProvider("opencode-go", "test-key", server.URL)
	AttachOpenCodeGoAttribution(p, "conv-key", "deepseek-v4.1-flash", "cred-42")
	if _, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "deepseek-v4.1-flash", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("go call: %v", err)
	}
	if got, want := captures[0]["user_agent"], DefaultOpenCodeGoUserAgent(); got != want {
		t.Errorf("opencode-go UA: got %q, want %q", got, want)
	}
	wantSID := DeriveOpenCodeGoSessionID(OpenCodeGoSessionSeed("conv-key", "deepseek-v4.1-flash", "cred-42"))
	if got := captures[0]["session"]; got != wantSID {
		t.Errorf("opencode-go session: got %q, want %q", got, wantSID)
	}
}

// TestOtherProviders_NoAttributionUA_Unchanged — the control across the
// rest of the factory: no provider other than the two OpenCode flavours
// carries an attribution UA or a session header, even with both override
// env vars set and a blind attach call.
//
// The control list is DERIVED, not hardcoded: pkg/models.ValidProviderNames()
// minus the two OpenCode entries. Identities the factory cannot construct
// yet (anthropic/gemini/typesafe — NewProvider errors) are skipped, so a
// newly added provider that IS constructible is automatically driven by
// this guard and cannot silently escape it.
func TestOtherProviders_NoAttributionUA_Unchanged(t *testing.T) {
	var captures []map[string]string
	server := jsonCompletionServer(t, &captures)
	defer server.Close()

	t.Setenv(EnvOpenCodeUserAgent, "should-not-leak/1.0")
	t.Setenv(EnvOpenCodeGoUserAgent, "should-not-leak-either/1.0")

	excluded := map[string]bool{
		string(ProviderOpencode):   true,
		string(ProviderOpencodeGo): true,
	}
	var others []string
	for _, name := range models.ValidProviderNames() {
		if !excluded[name] {
			others = append(others, name)
		}
	}
	for _, name := range others {
		if excluded[name] {
			t.Fatalf("control list must exclude the OpenCode flavours, got %q", name)
		}
	}

	var driven []string
	for _, pt := range others {
		p, err := NewProvider(pt, "test-key", server.URL)
		if err != nil {
			// Not constructible yet (e.g. anthropic/gemini/typesafe):
			// no wire surface to guard until the factory implements it.
			t.Logf("%s: not constructible by the factory, skipped (%v)", pt, err)
			continue
		}
		AttachOpenCodeGoAttribution(p, "conv", "m", "cred") // blind attach — kind-checked no-op
		if _, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
			t.Fatalf("%s call: %v", pt, err)
		}
		driven = append(driven, pt)
	}

	if len(captures) != len(driven) {
		t.Fatalf("expected %d calls, got %d", len(driven), len(captures))
	}
	for i, cap := range captures {
		pt := driven[i]
		if cap["session"] != "" {
			t.Errorf("%s must NOT carry x-opencode-session, got %q", pt, cap["session"])
		}
		if strings.HasPrefix(cap["user_agent"], "opencode/") {
			t.Errorf("%s must NOT carry an attribution UA, got %q", pt, cap["user_agent"])
		}
		if strings.Contains(cap["user_agent"], "should-not-leak") {
			t.Errorf("%s must NOT receive an OpenCode UA override, got %q", pt, cap["user_agent"])
		}
	}
}

// TestOpencodeZen_NoSessionHeader_AtSeamLevel — a defensive pin at the
// setHeaders seam: Zen attribution carries a UA but an empty SessionID, so
// each header is gated independently and NO session header is emitted.
func TestOpencodeZen_NoSessionHeader_AtSeamLevel(t *testing.T) {
	p, _ := NewProvider("opencode", "test-key", "https://example.invalid/v1")
	op := p.(*OpenAIProvider)
	if !op.openCodeZen {
		t.Fatal("expected openCodeZen flavour flag set by the factory")
	}
	AttachOpenCodeGoAttribution(p, "", "m", "c")
	if op.attribution == nil {
		t.Fatal("expected attribution attached for Zen")
	}
	if op.attribution.SessionID != "" {
		t.Errorf("Zen attribution must carry no session id, got %q", op.attribution.SessionID)
	}
	if op.attribution.UserAgent == "" {
		t.Error("Zen attribution must carry a UA")
	}

	req, _ := http.NewRequest("POST", "https://example.invalid/v1/chat/completions", nil)
	op.setHeaders(req)
	if got := req.Header.Get("x-opencode-session"); got != "" {
		t.Errorf("Zen setHeaders emitted a session header: %q", got)
	}
	if got, want := req.Header.Get("User-Agent"), DefaultOpenCodeUserAgent(); got != want {
		t.Errorf("Zen setHeaders UA: got %q, want %q", got, want)
	}
}

// TestOpencodeZen_SessionHeader_FlavorGuard — negative pin for the
// setHeaders flavour gate (reviewer info #1): a Zen-flagged instance
// handed a NON-EMPTY SessionID directly (bypassing
// AttachOpenCodeGoAttribution via the exported SetSessionAttribution)
// must STILL never emit x-opencode-session. Session emission is a
// Go-realm contract only; the Zen design note forbids that surface, so
// the gate is p.openCodeGo && SessionID != "" — defence-in-depth at the
// single stamping site, not just an empty-field convention.
func TestOpencodeZen_SessionHeader_FlavorGuard(t *testing.T) {
	p, _ := NewProvider("opencode", "test-key", "https://example.invalid/v1")
	op := p.(*OpenAIProvider)
	if op.openCodeGo || !op.openCodeZen {
		t.Fatal("expected the factory's Zen-only flavour flags (openCodeZen=true, openCodeGo=false)")
	}
	op.SetSessionAttribution(SessionAttribution{
		SessionID: DeriveOpenCodeGoSessionID("flavor-guard-seed"), // handed a session id ON PURPOSE
		UserAgent: DefaultOpenCodeUserAgent(),
	})
	if op.attribution == nil || op.attribution.SessionID == "" {
		t.Fatal("precondition: Zen provider must accept attribution carrying a non-empty SessionID (the gate lives in setHeaders)")
	}

	req, _ := http.NewRequest("POST", "https://example.invalid/v1/chat/completions", nil)
	op.setHeaders(req)
	if got := req.Header.Get("x-opencode-session"); got != "" {
		t.Errorf("Zen-flagged provider emitted x-opencode-session despite the flavour gate: %q", got)
	}
	if got, want := req.Header.Get("User-Agent"), DefaultOpenCodeUserAgent(); got != want {
		t.Errorf("Zen UA must still be stamped with the session gate in place: got %q, want %q", got, want)
	}
}
