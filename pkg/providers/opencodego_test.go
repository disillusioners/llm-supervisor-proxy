package providers

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// =============================================================================
// opencode-go (OpenCode Go) provider commission — behavioral tests.
//
// opencode-go is a DISTINCT provider from "opencode" (Zen): subscription
// realm, provider-native attribution on upstream requests:
//
//   - x-opencode-session: conversation-stable session id, derived
//     deterministically from the proxy conversation key (ses_ + 12 hex
//     + 14 base62; same shape the vendor client mints, research report
//     §c; live-verified shape ses_01a1492acf28MpenyGEstH02J1 in the R4
//     E2E).
//   - User-Agent: official-client format default
//     (opencode/stable/2.0.32/cli) with OPENCODE_GO_USER_AGENT override.
//
// F11 (R4 scope addition): responses on this surface carry
// reasoning_content as a FLAT STRING — both stream deltas and the
// non-stream message — never reasoning_details; the path must accept
// BOTH shapes, and the inactive field is the literal "" (never
// null/absent) which must not false-trigger content/thinking emission.
//
// F12 (R4 scope addition): the Go surface emits usage TWICE per stream
// (combined finish chunk + verbatim trailing usage-only chunk,
// choices:[]); metering must count ONCE.
//
// Verbatim wire shapes below are quoted from the R4 capture
// (.agents/tester/RESULTS/2026-10-17-opencode-e2e-r4.md). No live
// network calls.
// =============================================================================

// openCodeGoSessionShape pins the wire format: "ses_" + exactly 12 hex
// + exactly 14 base62 = 30 chars total. (The research report's "Total
// length 29" line is an arithmetic slip — 4 + 26 = 30; the 12+14 slot
// split is the load-bearing contract.)
var openCodeGoSessionShape = regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`)

const openCodeGoSessionWireLen = 30

// r4NonStreamBody is the verbatim R4 call-#1 body (deepseek-v4.1-flash,
// reasoning_content flat string on message).
const r4NonStreamBody = `{"id":"021792228916816e0fa5d59a7b50e9f748f37815b909e28fc8f09","object":"chat.completion","created":1792228917,"model":"deepseek-v4.1-flash","choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":"R4-OK","reasoning_content":"The user wants me to reply with exactly \"R4-OK\". Simple."}}],"usage":{"prompt_tokens":38,"completion_tokens":21,"total_tokens":59,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":16}}}`

// r4SSEChunks is the verbatim R4 call-#2 stream: role header → 7
// reasoning deltas → 7 content deltas (last one riding finish_reason
// + full usage) → trailing usage-only duplicate → [DONE].
var r4SSEChunks = []string{
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":""}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":"The"}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":" user wants exactly"}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":" \"R4-OK"}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":"\". I should reply"}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":" with just"}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":" that"}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":"."}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"R","reasoning_content":""}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"4","reasoning_content":""}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"-","reasoning_content":""}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"<","reasoning_content":""}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"system","reasoning_content":""}}],"usage":null}`,
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":">OK","reasoning_content":""}}],"usage":null}`,
	// Chunk 15 (combined finish chunk: finish_reason + last content
	// delta + FULL usage — the most-compact form on this surface).
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"finish_reason":"stop","delta":{"role":"assistant","content":"</system>","reasoning_content":""}}],"usage":{"prompt_tokens":38,"completion_tokens":28,"total_tokens":66,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":17}}}`,
	// Chunk 16 (trailing usage-only DUPLICATE — choices:[]).
	`{"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[],"usage":{"prompt_tokens":38,"completion_tokens":28,"total_tokens":66,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":17}}}`,
}

// r4StreamUsage is the verbatim chunk-15/16 usage triple.
var r4StreamUsage = Usage{PromptTokens: 38, CompletionTokens: 28, TotalTokens: 66}

// sseServer spins an httptest upstream emitting the given raw SSE data
// lines verbatim and capturing the inbound request headers per call.
func sseServer(t *testing.T, dataLines []string, captures *[]map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captures != nil {
			*captures = append(*captures, map[string]string{
				"session":           r.Header.Get("x-opencode-session"),
				"user_agent":        r.Header.Get("User-Agent"),
				"authorization":     r.Header.Get("Authorization"),
				"content_type":      r.Header.Get("Content-Type"),
				"x_opencode_client": r.Header.Get("x-opencode-client"),
			})
		}
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, line := range dataLines {
			fmt.Fprintf(w, "data: %s\n", line)
		}
		fmt.Fprint(w, "data: [DONE]\n")
		f.Flush()
	}))
}

// jsonCompletionServer spins an httptest upstream answering NON-STREAM
// chat completions with a minimal valid body, capturing inbound request
// headers per call (the session/UA gates exercise the non-stream path).
func jsonCompletionServer(t *testing.T, captures *[]map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captures != nil {
			*captures = append(*captures, map[string]string{
				"session":       r.Header.Get("x-opencode-session"),
				"user_agent":    r.Header.Get("User-Agent"),
				"authorization": r.Header.Get("Authorization"),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"oc-go-test","object":"chat.completion","created":1792228917,"model":"deepseek-v4.1-flash","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
}

// TestOpencodeGo_Factory_DistinctIdentity verifies the factory wires
// opencode-go as its own identity: default base URL is the Go realm,
// the transport is the shared OpenAIProvider, and the flavour flag is
// set only for opencode-go.
func TestOpencodeGo_Factory_DistinctIdentity(t *testing.T) {
	p, err := NewProvider("opencode-go", "test-key", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected shared OpenAIProvider impl (Name 'openai'), got %q", p.Name())
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider, got %T", p)
	}
	if !op.openCodeGo {
		t.Error("expected openCodeGo flavour flag set for opencode-go provider")
	}
	if op.baseURL != OpenCodeGoDefaultBaseURL {
		t.Errorf("default baseURL: got %q, want %q", op.baseURL, OpenCodeGoDefaultBaseURL)
	}

	// Override URL honored.
	op2, err := NewProvider("opencode-go", "k", "https://example.invalid/go/v1")
	if err != nil {
		t.Fatalf("override: unexpected error: %v", err)
	}
	if op2.(*OpenAIProvider).baseURL != "https://example.invalid/go/v1" {
		t.Errorf("override baseURL not honored: %q", op2.(*OpenAIProvider).baseURL)
	}

	// Zen stays flag-free (distinct identities, structurally).
	zen, err := NewProvider("opencode", "test-key", "")
	if err != nil {
		t.Fatalf("zen: unexpected error: %v", err)
	}
	if zen.(*OpenAIProvider).openCodeGo {
		t.Error("opencode (Zen) provider must NOT carry the opencode-go flavour flag")
	}
}

// TestDeriveOpenCodeGoSessionID_ShapeAndDeterminism pins the derivation
// contract: ses_ shape (12 hex + 14 base62), determinism per seed, and
// distinctness across seeds.
func TestDeriveOpenCodeGoSessionID_ShapeAndDeterminism(t *testing.T) {
	seed := "64-hex-conversation-key-from-credentiallb-0123456789abcdef"
	id := DeriveOpenCodeGoSessionID(seed)

	if !openCodeGoSessionShape.MatchString(id) {
		t.Fatalf("session id %q does not match ses_ shape %v", id, openCodeGoSessionShape)
	}
	if len(id) != openCodeGoSessionWireLen {
		t.Fatalf("session id length: got %d, want %d", len(id), openCodeGoSessionWireLen)
	}

	// Determinism: same conversation ⇒ identical id.
	for i := 0; i < 100; i++ {
		if got := DeriveOpenCodeGoSessionID(seed); got != id {
			t.Fatalf("derivation not deterministic: %q vs %q", got, id)
		}
	}

	// Distinctness: different conversations ⇒ different ids.
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		other := DeriveOpenCodeGoSessionID(fmt.Sprintf("conversation-key-%d", i))
		if !openCodeGoSessionShape.MatchString(other) {
			t.Fatalf("id %q fails shape", other)
		}
		if seen[other] {
			t.Fatalf("collision at %d: %q", i, other)
		}
		seen[other] = true
	}

	// Domain separation: the same seed under the session domain must
	// not equal any raw use of the seed (the conversation key itself
	// is a 64-hex credential-affinity binding; the session id is not
	// correlatable with it).
	if strings.Contains(id, seed) {
		t.Error("session id must not embed the raw seed")
	}
}

// TestOpenCodeGoSessionSeed_DegenerateFallback pins the W-2 fallback:
// empty conversationKey ⇒ deterministic (model, credential)-keyed
// seed, stable across calls, non-empty.
func TestOpenCodeGoSessionSeed_DegenerateFallback(t *testing.T) {
	if got := OpenCodeGoSessionSeed("conv-key", "model-a", "cred-1"); got != "conv-key" {
		t.Errorf("non-empty conversationKey must pass through verbatim, got %q", got)
	}
	a := OpenCodeGoSessionSeed("", "model-a", "cred-1")
	b := OpenCodeGoSessionSeed("", "model-a", "cred-1")
	if a == "" || a != b {
		t.Errorf("degenerate seed must be non-empty and deterministic: %q vs %q", a, b)
	}
	if a == OpenCodeGoSessionSeed("", "model-b", "cred-1") {
		t.Error("degenerate seed must differ across model ids")
	}
	if a == OpenCodeGoSessionSeed("", "model-a", "cred-2") {
		t.Error("degenerate seed must differ across credential ids")
	}
}

// TestOpencodeGo_SessionHeader_ShapeAndStability (gates a + b) — the
// opencode-go path stamps x-opencode-session with the correct ses_
// shape, and the SAME conversation across two independent provider
// constructions (the retry shape: race attempts / failover re-drives
// construct fresh providers) re-derives the SAME session id.
func TestOpencodeGo_SessionHeader_ShapeAndStability(t *testing.T) {
	var captures []map[string]string
	server := jsonCompletionServer(t, &captures)
	defer server.Close()

	conversationKey := "b7e23ec29af22b0b4e41da31e868d57226121c84e8a2e2a40b1d29b4e8d1f2c3"
	convID, credID := "client-facing-model", "cred-42"

	// "Attempt 1".
	p1, err := NewProvider("opencode-go", "test-key", server.URL)
	if err != nil {
		t.Fatalf("p1: %v", err)
	}
	AttachOpenCodeGoAttribution(p1, conversationKey, convID, credID)
	req := &ChatCompletionRequest{Model: "deepseek-v4.1-flash", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}
	if _, err := p1.ChatCompletion(context.Background(), req); err != nil {
		t.Fatalf("attempt1: %v", err)
	}

	// "Retry" — fresh provider construction (exactly what a race
	// attempt or credential failover does), same conversation inputs.
	p2, err := NewProvider("opencode-go", "test-key", server.URL)
	if err != nil {
		t.Fatalf("p2: %v", err)
	}
	AttachOpenCodeGoAttribution(p2, conversationKey, convID, credID)
	if _, err := p2.ChatCompletion(context.Background(), req); err != nil {
		t.Fatalf("attempt2: %v", err)
	}

	if len(captures) != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", len(captures))
	}
	sid1, sid2 := captures[0]["session"], captures[1]["session"]
	if !openCodeGoSessionShape.MatchString(sid1) {
		t.Fatalf("gate (a): x-opencode-session %q fails ses_ shape", sid1)
	}
	if want := DeriveOpenCodeGoSessionID(OpenCodeGoSessionSeed(conversationKey, convID, credID)); sid1 != want {
		t.Fatalf("gate (a): wire session %q does not equal the pure derivation %q", sid1, want)
	}

	// Gate (b): identical across retries.
	if sid1 != sid2 {
		t.Fatalf("gate (b): session id changed across retries: %q vs %q", sid1, sid2)
	}

	// Sanity: auth untouched.
	for i, cap := range captures {
		if cap["authorization"] != "Bearer test-key" {
			t.Errorf("call %d Authorization: got %q", i, cap["authorization"])
		}
	}
}

// TestOpencodeGo_DifferentConversations_DifferentSessions (gate c).
func TestOpencodeGo_DifferentConversations_DifferentSessions(t *testing.T) {
	var captures []map[string]string
	server := jsonCompletionServer(t, &captures)
	defer server.Close()

	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		p, err := NewProvider("opencode-go", "test-key", server.URL)
		if err != nil {
			t.Fatalf("provider %d: %v", i, err)
		}
		AttachOpenCodeGoAttribution(p, fmt.Sprintf("conv-key-%d", i), "model-a", "cred-1")
		if _, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		sid := captures[i]["session"]
		if seen[sid] {
			t.Fatalf("gate (c): duplicate session id %q across distinct conversations", sid)
		}
		seen[sid] = true
	}
}

// TestOpencodeGo_UserAgent_DefaultAndOverride (gate d).
func TestOpencodeGo_UserAgent_DefaultAndOverride(t *testing.T) {
	var captures []map[string]string
	server := jsonCompletionServer(t, &captures)
	defer server.Close()

	// Default: official-client format.
	p, _ := NewProvider("opencode-go", "test-key", server.URL)
	AttachOpenCodeGoAttribution(p, "conv", "model", "cred")
	if _, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("default UA call: %v", err)
	}
	if got := captures[0]["user_agent"]; got != DefaultOpenCodeGoUserAgent() {
		t.Errorf("gate (d) default UA: got %q, want %q", got, DefaultOpenCodeGoUserAgent())
	}
	if !strings.HasPrefix(captures[0]["user_agent"], "opencode/stable/") {
		t.Errorf("default UA must match opencode/{channel}/{version}/{name}, got %q", captures[0]["user_agent"])
	}

	// Override wins.
	t.Setenv(EnvOpenCodeGoUserAgent, "llm-supervisor-proxy/1.0")
	if got := ResolveOpenCodeGoUserAgent(); got != "llm-supervisor-proxy/1.0" {
		t.Fatalf("env override not resolved: %q", got)
	}
	p2, _ := NewProvider("opencode-go", "test-key", server.URL)
	AttachOpenCodeGoAttribution(p2, "conv", "model", "cred")
	if _, err := p2.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("override UA call: %v", err)
	}
	if got := captures[1]["user_agent"]; got != "llm-supervisor-proxy/1.0" {
		t.Errorf("gate (d) override UA: got %q, want llm-supervisor-proxy/1.0", got)
	}
}

// TestOpencode_NonRegression_NoSessionNoUA (gate e) — the existing
// "opencode" (Zen) provider carries NO x-opencode-session and NO
// custom User-Agent, and even a blind attach call is a no-op.
func TestOpencode_NonRegression_NoSessionNoUA(t *testing.T) {
	var captures []map[string]string
	server := jsonCompletionServer(t, &captures)
	defer server.Close()

	zen, _ := NewProvider("opencode", "test-key", server.URL)
	// Blind attach — must be a kind-checked no-op on Zen.
	AttachOpenCodeGoAttribution(zen, "conv-key", "model", "cred")
	if _, err := zen.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "kimi-k3", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("zen call: %v", err)
	}
	if got := captures[0]["session"]; got != "" {
		t.Errorf("gate (e): opencode (Zen) must NOT carry x-opencode-session, got %q", got)
	}
	if strings.HasPrefix(captures[0]["user_agent"], "opencode/") {
		t.Errorf("gate (e): opencode (Zen) UA must be untouched, got %q", captures[0]["user_agent"])
	}

	// Plain OpenAI provider: same no-op guarantee.
	plain := NewOpenAIProvider("test-key", server.URL)
	AttachOpenCodeGoAttribution(plain, "conv-key", "model", "cred")
	if _, err := plain.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "gpt-4o", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("plain call: %v", err)
	}
	if got := captures[1]["session"]; got != "" {
		t.Errorf("plain OpenAI provider must NOT carry x-opencode-session, got %q", got)
	}
}

// TestOpencodeGo_F11_NonStream_ReasoningContentString — verbatim R4
// non-stream body: reasoning_content flat string on message must be
// absorbed (both shapes accepted; this surface's shape is the string).
func TestOpencodeGo_F11_NonStream_ReasoningContentString(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(r4NonStreamBody))
	}))
	defer server.Close()

	p, _ := NewProvider("opencode-go", "test-key", server.URL)
	resp, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "deepseek-v4.1-flash", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("non-stream: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message == nil {
		t.Fatalf("unexpected choices: %+v", resp.Choices)
	}
	msg := resp.Choices[0].Message
	if got, want := msg.Content, "R4-OK"; got != want {
		t.Errorf("content: got %q, want %q", got, want)
	}
	want := `The user wants me to reply with exactly "R4-OK". Simple.`
	if msg.ReasoningContent != want {
		t.Errorf("F11: message.reasoning_content string not absorbed: got %q, want %q", msg.ReasoningContent, want)
	}
	if len(msg.ReasoningDetails) != 0 {
		t.Errorf("F11: no reasoning_details on this surface, got %+v", msg.ReasoningDetails)
	}
}

// TestOpencodeGo_F11_NonStream_ReasoningDetailsArray — the OpenAI
// reasoning_details array shape is ALSO accepted (single-winner rule:
// details win, flat string ignored when both present).
func TestOpencodeGo_F11_NonStream_ReasoningDetailsArray(t *testing.T) {
	body := `{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"answer","reasoning_content":"legacy-string","reasoning_details":[{"type":"reasoning.text","text":"structured thought"}]}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	p, _ := NewProvider("opencode-go", "test-key", server.URL)
	resp, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("non-stream details: %v", err)
	}
	msg := resp.Choices[0].Message
	// Single-winner (D2) absorption: details WIN — their text is
	// absorbed into ReasoningContent and the typed ReasoningDetails
	// field is nil'd (R11, omitempty drops the key on re-marshal).
	if len(msg.ReasoningDetails) != 0 {
		t.Fatalf("F11: typed ReasoningDetails should be nil'd after absorption, got %+v", msg.ReasoningDetails)
	}
	if msg.ReasoningContent != "structured thought" {
		t.Errorf("F11: reasoning_details array not absorbed into reasoning_content: got %q, want %q", msg.ReasoningContent, "structured thought")
	}
}

// TestOpencodeGo_F11_F12_Stream_VerbatimSignature — the headline R4
// test: the verbatim 16-chunk Go-surface stream through the typed
// parser must yield
//   - NO spurious events from the literal "" inactive field (F11),
//   - 7 thinking + 7 content deltas with the exact R4 texts (F11),
//   - exactly ONE done event whose usage is the finish chunk's values
//     ONCE (66 total — not 132; F12: the trailing usage-only duplicate
//     chunk is absorbed by the parser's choices-guard).
func TestOpencodeGo_F11_F12_Stream_VerbatimSignature(t *testing.T) {
	server := sseServer(t, r4SSEChunks, nil)
	defer server.Close()

	p, _ := NewProvider("opencode-go", "test-key", server.URL)
	events, err := p.StreamChatCompletion(context.Background(), &ChatCompletionRequest{Model: "deepseek-v4.1-flash", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var thinking, content []string
	var doneCount, usageCount int
	var doneUsage Usage
	var finishReason string
	for ev := range events {
		switch ev.Type {
		case "thinking":
			thinking = append(thinking, ev.ReasoningContent)
		case "content":
			content = append(content, ev.Content)
		case "done":
			doneCount++
			if ev.Response != nil && (ev.Response.Usage.PromptTokens > 0 || ev.Response.Usage.CompletionTokens > 0 || ev.Response.Usage.TotalTokens > 0) {
				usageCount++
				doneUsage = ev.Response.Usage
			}
			finishReason = ev.FinishReason
		case "error":
			t.Fatalf("parser error event: %v", ev.Error)
		}
	}

	// F11 — exact delta sequences from the R4 capture.
	wantThinking := []string{"The", " user wants exactly", " \"R4-OK", "\". I should reply", " with just", " that", "."}
	if len(thinking) != len(wantThinking) {
		t.Fatalf("F11 thinking deltas: got %d (%q), want %d", len(thinking), thinking, len(wantThinking))
	}
	for i := range wantThinking {
		if thinking[i] != wantThinking[i] {
			t.Errorf("F11 thinking[%d]: got %q, want %q", i, thinking[i], wantThinking[i])
		}
	}
	wantContent := []string{"R", "4", "-", "<", "system", ">OK", "</system>"}
	if len(content) != len(wantContent) {
		t.Fatalf("F11 content deltas: got %d (%q), want %d", len(content), content, len(wantContent))
	}
	for i := range wantContent {
		if content[i] != wantContent[i] {
			t.Errorf("F11 content[%d]: got %q, want %q", i, content[i], wantContent[i])
		}
	}

	// F12 — ONE done event, usage counted ONCE (66, not 132).
	// Raw-SSE seam twin: pkg/proxy/opencodego_stream_metering_test.go
	// (the extractUsageFromSSEChunk guard suite).
	if doneCount != 1 {
		t.Fatalf("F12: expected exactly one done event, got %d", doneCount)
	}
	if usageCount != 1 {
		t.Fatalf("F12: expected usage observed exactly once on the done event, got %d", usageCount)
	}
	if doneUsage != r4StreamUsage {
		t.Errorf("F12: usage: got %+v, want %+v (identical duplicate must not accumulate)", doneUsage, r4StreamUsage)
	}
	if doneUsage.TotalTokens == 2*r4StreamUsage.TotalTokens {
		t.Error("F12: DOUBLE-COUNT detected — trailing usage-only chunk accumulated")
	}
	if finishReason != "stop" {
		t.Errorf("finish_reason: got %q, want stop", finishReason)
	}
}

// TestOpencodeGo_F12_Stream_IncludeUsageTrailingOnlyShape — the
// non-regression property: OpenAI's stream_options.include_usage
// convention delivers usage ONLY in a trailing usage-only chunk. That
// chunk is the SOLE usage carrier and must still meter (the F12 guard
// absorbs DUPLICATES, it does not ignore usage-only chunks).
func TestOpencodeGo_F12_Stream_IncludeUsageTrailingOnlyShape(t *testing.T) {
	chunks := []string{
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}],"usage":null}`,
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`,
		// Sole usage carrier: trailing usage-only chunk (choices:[]).
		`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
	}
	server := sseServer(t, chunks, nil)
	defer server.Close()

	p, _ := NewProvider("opencode-go", "test-key", server.URL)
	events, err := p.StreamChatCompletion(context.Background(), &ChatCompletionRequest{Model: "gpt-4o", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	sawUsageOnce := 0
	for ev := range events {
		if ev.Type == "done" {
			if ev.Response != nil && ev.Response.Usage.TotalTokens == 15 {
				sawUsageOnce++
			}
		}
	}
	if sawUsageOnce != 1 {
		t.Errorf("F12 include_usage shape: usage-only trailing chunk must be the metered carrier exactly once, got %d", sawUsageOnce)
	}
}

// TestOpencodeGo_Stream_AttributionOnBothPaths — attribution headers
// are stamped on the STREAMING path too (setHeaders is shared, but the
// commission asks for evidence).
func TestOpencodeGo_Stream_AttributionOnBothPaths(t *testing.T) {
	var captures []map[string]string
	server := sseServer(t, r4SSEChunks[0:2], &captures)
	defer server.Close()

	p, _ := NewProvider("opencode-go", "test-key", server.URL)
	AttachOpenCodeGoAttribution(p, "conv-key-stream", "model", "cred")
	events, err := p.StreamChatCompletion(context.Background(), &ChatCompletionRequest{Model: "m", Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	for range events { // drain
	}
	if !openCodeGoSessionShape.MatchString(captures[0]["session"]) {
		t.Errorf("streaming path: session header %q fails shape", captures[0]["session"])
	}
	if captures[0]["user_agent"] != DefaultOpenCodeGoUserAgent() {
		t.Errorf("streaming path: UA %q", captures[0]["user_agent"])
	}
}

// TestOpencodeGo_MetadataVisibility — the FE dropdown surfaces the new
// provider with the distinct display name and Go-realm base URL.
func TestOpencodeGo_MetadataVisibility(t *testing.T) {
	info := providerMetadataMap[ProviderOpencodeGo]
	if info.name != "OpenCode Go" {
		t.Errorf("display name: got %q, want OpenCode Go", info.name)
	}
	if info.baseURL != OpenCodeGoDefaultBaseURL {
		t.Errorf("metadata baseURL: got %q, want %q", info.baseURL, OpenCodeGoDefaultBaseURL)
	}
	if !IsProviderSupported("opencode-go") {
		t.Error("opencode-go must be a supported provider")
	}
	// Zen's metadata untouched (non-regression).
	zen := providerMetadataMap[ProviderOpencode]
	if zen.name != "OpenCode Zen" || zen.baseURL != "https://opencode.ai/inference/openai/v1" {
		t.Errorf("opencode (Zen) metadata drifted: %+v", zen)
	}
}

// TestEncodeBase62Padded — the base62 slot encoding: charset, padding,
// and the 80-bit width bound (62^14 ≈ 1.24e25 > 2^80 ≈ 1.21e24 ⇒ any
// 10-byte value encodes into ≤ 14 chars, so left-padding to 14 is lossless).
func TestEncodeBase62Padded(t *testing.T) {
	// Shape: fixed width, charset membership, zero-pad semantics.
	// Round-trip values stay within int64 (the decoder's domain); the
	// full 80-bit bound is checked separately below via big.Int.
	for _, n := range []int64{0, 1, 61, 62, 3843, 3844, 1 << 40, 1 << 62, (1 << 62) - 1 + 12345} {
		enc := encodeBase62Padded(big.NewInt(n), 14)
		if len(enc) != 14 {
			t.Errorf("width: %d → len %d (%q), want 14", n, len(enc), enc)
		}
		for _, ch := range enc {
			if !strings.ContainsRune(base62Charset, ch) {
				t.Errorf("charset violation: %q contains %q", enc, string(ch))
			}
		}
		// Round-trip: decode(encode(n)) == n.
		if dec := decodeBase62(t, enc); dec != n {
			t.Errorf("round-trip %d: got %d via %q", n, dec, enc)
		}
	}
	// Zero encodes as all-zero padding (deterministic).
	if got := encodeBase62Padded(big.NewInt(0), 14); got != "00000000000000" {
		t.Errorf("zero encoding: got %q", got)
	}
	// 80-bit bound: the largest 10-byte value (2^80 - 1) must encode
	// into exactly 14 chars (lossless left-pad, no overflow char).
	max80 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 80), big.NewInt(1))
	enc := encodeBase62Padded(max80, 14)
	if len(enc) != 14 {
		t.Errorf("2^80-1 encodes to len %d (%q), want exactly 14", len(enc), enc)
	}
	// 62^13 ≈ 2^77.36 ⇒ (2^80-1)/62^13 ≈ 6.26 ⇒ leading digit '6'.
	if enc[0] != '6' {
		t.Errorf("2^80-1 leading digit: got %q (%q), want '6'", string(enc[0]), enc)
	}
}

// decodeBase62 inverts encodeBase62Padded for the round-trip check.
func decodeBase62(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	for _, ch := range s {
		idx := strings.IndexRune(base62Charset, ch)
		if idx < 0 {
			t.Fatalf("decodeBase62: invalid char %q in %q", string(ch), s)
		}
		n = n*int64(len(base62Charset)) + int64(idx)
	}
	return n
}
