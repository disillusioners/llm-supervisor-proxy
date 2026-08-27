package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// buildRequestBodyForInitContext marshals body into an *http.Request suitable
// for Handler.initRequestContext. Content-Type is set so any future body
// validation stays consistent.
func buildRequestBodyForInitContext(t *testing.T, body map[string]interface{}) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestInitRequestContext_CapturesRequestReasoningContent verifies the fix for
// the request-side reasoning capture bug: messages containing reasoning_content
// in the request body MUST be stored with Thinking populated, so the Web UI
// can render request-side thinking.
//
// Regression target: pkg/proxy/handler_functions.go initRequestContext previously
// called parseMessages, which dropped reasoning_content on request messages.
func TestInitRequestContext_CapturesRequestReasoningContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	h := newTestHandlerWithURL(t, upstream.URL)

	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "You are helpful."},
			map[string]interface{}{"role": "user", "content": "What is 2+2?"},
			map[string]interface{}{
				"role":              "assistant",
				"content":           "The answer is 4.",
				"reasoning_content": "Let me compute: 2 + 2 = 4.",
			},
			map[string]interface{}{
				"role":              "user",
				"content":           "And 3+3?",
				"reasoning_content": "User provided their own scratch work.",
			},
		},
	}

	req := buildRequestBodyForInitContext(t, body)

	rc, err := h.initRequestContext(req)
	if err != nil {
		t.Fatalf("initRequestContext() error = %v", err)
	}
	if rc.reqLog == nil {
		t.Fatal("initRequestContext() returned nil reqLog")
	}

	got := rc.reqLog.Messages
	if len(got) != 4 {
		t.Fatalf("expected 4 store messages, got %d (%+v)", len(got), got)
	}

	// Assistant message with reasoning_content — primary bug fix.
	if got[2].Role != "assistant" {
		t.Errorf("got[2].Role = %q, want %q", got[2].Role, "assistant")
	}
	if got[2].Content != "The answer is 4." {
		t.Errorf("got[2].Content = %q, want %q", got[2].Content, "The answer is 4.")
	}
	if got[2].Thinking != "Let me compute: 2 + 2 = 4." {
		t.Errorf("got[2].Thinking = %q, want %q (request-side reasoning_content must be captured)", got[2].Thinking, "Let me compute: 2 + 2 = 4.")
	}

	// User message with reasoning_content — also covered by the fix.
	if got[3].Role != "user" {
		t.Errorf("got[3].Role = %q, want %q", got[3].Role, "user")
	}
	if got[3].Thinking != "User provided their own scratch work." {
		t.Errorf("got[3].Thinking = %q, want %q (request-side reasoning_content on user messages must also be captured)", got[3].Thinking, "User provided their own scratch work.")
	}

	// Messages without reasoning_content must keep Thinking empty.
	if got[0].Thinking != "" {
		t.Errorf("got[0].Thinking = %q, want empty (system message had no reasoning_content)", got[0].Thinking)
	}
	if got[1].Thinking != "" {
		t.Errorf("got[1].Thinking = %q, want empty (user message had no reasoning_content)", got[1].Thinking)
	}
}

// TestInitRequestContext_ToStoreMessagesDropsEmptyRole is the S3 pin
// test: it documents the INTENTIONAL divergence between the legacy
// parseMessages (kept as a test oracle) and the new production
// adapter path (NewOpenAIAdapter().ToStoreMessages →
// parseOpenAIMessages) on a request body that contains a message
// with role == "".
//
// Legacy parseMessages KEPT such a message in the returned slice
// verbatim (Role: ""), while the new adapter path SKIPS it (see
// adapter_helpers.go:60-63 — `if role == "" { continue }`). The
// new behavior is the desired one: messages with no role carry no
// information the store or Web UI can render, and the previous
// behavior of preserving a Role: "" entry was a side effect of the
// original parser's permissive loop, not a designed contract.
//
// This test pins that divergence so any future change to either
// path is forced to make the divergence decision explicitly (instead
// of accidentally re-aligning them via a refactor).
func TestInitRequestContext_ToStoreMessagesDropsEmptyRole(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	h := newTestHandlerWithURL(t, upstream.URL)

	// Mix of empty-role and well-formed messages; the empty-role
	// entry has non-empty content + tool_calls (so the divergence
	// is about role, not about content presence).
	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "You are helpful."},
			map[string]interface{}{"role": "", "content": "orphan with empty role"}, // dropped by new path
			map[string]interface{}{"role": "user", "content": "Hello"},
			map[string]interface{}{"role": "", "content": "second orphan"}, // dropped by new path
		},
	}

	// 1. Reference: legacy parseMessages KEEPS the empty-role
	//    entries (Role: "" in the output). This is the pre-8185c76
	//    behavior the test oracle preserves.
	legacy := parseMessages(body)
	if len(legacy) != 4 {
		t.Fatalf("legacy parseMessages returned %d messages, want 4 (it must keep empty-role entries)", len(legacy))
	}
	emptyRoleCount := 0
	for _, m := range legacy {
		if m.Role == "" {
			emptyRoleCount++
		}
	}
	if emptyRoleCount != 2 {
		t.Fatalf("legacy parseMessages kept %d empty-role messages, want 2 (sanity check on oracle behavior)", emptyRoleCount)
	}

	// 2. Production: h.initRequestContext calls
	//    NewOpenAIAdapter().ToStoreMessages which routes through
	//    parseOpenAIMessages. The empty-role entries MUST be
	//    dropped — only the 2 well-formed messages survive.
	req := buildRequestBodyForInitContext(t, body)
	rc, err := h.initRequestContext(req)
	if err != nil {
		t.Fatalf("initRequestContext() error = %v", err)
	}
	got := rc.reqLog.Messages
	if len(got) != 2 {
		t.Fatalf("got %d store messages, want 2 (the new adapter path drops empty-role entries)\ngot: %+v", len(got), got)
	}

	// 3. Explicit empty-role assertion: the new path must NOT
	//    contain a Role: "" entry under any index.
	for i, m := range got {
		if m.Role == "" {
			t.Errorf("got[%d].Role = %q, want non-empty (new adapter path must drop empty-role messages)", i, m.Role)
		}
	}

	// 4. Content sanity: the surviving messages are the
	//    system + user entries, in order.
	if got[0].Role != "system" || got[0].Content != "You are helpful." {
		t.Errorf("got[0] = %+v, want {Role: system, Content: You are helpful.}", got[0])
	}
	if got[1].Role != "user" || got[1].Content != "Hello" {
		t.Errorf("got[1] = %+v, want {Role: user, Content: Hello}", got[1])
	}

	// 5. Adapter-direct comparison: calling the production
	//    adapter without going through initRequestContext must
	//    produce the same drop. This guards against an
	//    initRequestContext refactor that silently re-aligns the
	//    behavior.
	direct := NewOpenAIAdapter().ToStoreMessages(body)
	if len(direct) != 2 {
		t.Errorf("OpenAIAdapter().ToStoreMessages returned %d messages, want 2 (empty-role entries must be dropped)", len(direct))
	}
}

// TestInitRequestContext_NoRegressionOnMissingReasoningContent is the
// regression guard: for request bodies that do NOT contain reasoning_content,
// the store messages produced by initRequestContext must be IDENTICAL to what
// the legacy parseMessages function produced, except that the new path also
// captures Thinking when it IS present (verified by the other test).
//
// "Identical" here means: same Role, same Content for string-content cases,
// same ToolCalls. The new path uses the OpenAI adapter's extractContentAsString
// which joins multimodal (array) text parts with "\n", whereas legacy
// parseMessages concatenated them with no separator. The "\n" join is the
// adapter-correct behavior and only affects multimodal array content; it is
// documented as an intentional, capture-side-only change (no wire impact).
func TestInitRequestContext_NoRegressionOnMissingReasoningContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	h := newTestHandlerWithURL(t, upstream.URL)

	body := map[string]interface{}{
		"model": "test-model",
		"messages": []interface{}{
			map[string]interface{}{"role": "system", "content": "You are helpful."},
			map[string]interface{}{"role": "user", "content": "Hello"},
			map[string]interface{}{"role": "assistant", "content": "Hi there!"},
			map[string]interface{}{
				"role":    "assistant",
				"content": "Calling a tool.",
				"tool_calls": []interface{}{
					map[string]interface{}{
						"id":   "call_abc",
						"type": "function",
						"function": map[string]interface{}{
							"name":      "lookup",
							"arguments": `{"q":"x"}`,
						},
					},
				},
			},
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "part1"},
					map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": "http://x"}},
					map[string]interface{}{"type": "text", "text": "part2"},
				},
			},
		},
	}

	// Sanity: legacy parseMessages is still in the codebase, so we can compute
	// the expected output directly and compare against the new path.
	want := parseMessages(body)
	if len(want) != 5 {
		t.Fatalf("legacy parseMessages returned %d messages, want 5", len(want))
	}

	req := buildRequestBodyForInitContext(t, body)

	rc, err := h.initRequestContext(req)
	if err != nil {
		t.Fatalf("initRequestContext() error = %v", err)
	}
	got := rc.reqLog.Messages
	if len(got) != len(want) {
		t.Fatalf("got %d store messages, want %d", len(got), len(want))
	}

	for i := range got {
		if got[i].Role != want[i].Role {
			t.Errorf("[%d].Role = %q, want %q", i, got[i].Role, want[i].Role)
		}
		if got[i].Thinking != "" {
			t.Errorf("[%d].Thinking = %q, want empty (no reasoning_content in request)", i, got[i].Thinking)
		}

		// Compare ToolCalls by value. Both nil/empty and populated must match.
		if len(got[i].ToolCalls) != len(want[i].ToolCalls) {
			t.Errorf("[%d].ToolCalls length = %d, want %d", i, len(got[i].ToolCalls), len(want[i].ToolCalls))
			continue
		}
		for j := range want[i].ToolCalls {
			if !reflect.DeepEqual(got[i].ToolCalls[j], want[i].ToolCalls[j]) {
				t.Errorf("[%d].ToolCalls[%d] = %+v, want %+v", i, j, got[i].ToolCalls[j], want[i].ToolCalls[j])
			}
		}

		// Content comparison is split by message kind:
		//   - String content (indexes 0..3): must be IDENTICAL to legacy.
		//   - Multimodal array content (index 4): legacy concatenates with no
		//     separator ("part1part2"); new path joins with "\n" ("part1\npart2").
		//     The new behavior is adapter-correct and capture-side-only; assert it.
		if i == 4 {
			if got[i].Content != "part1\npart2" {
				t.Errorf("[%d].Content = %q, want %q (multimodal text parts joined with newline)", i, got[i].Content, "part1\npart2")
			}
			if want[i].Content != "part1part2" {
				t.Errorf("[%d] legacy Content = %q, want %q (sanity check on legacy behavior)", i, want[i].Content, "part1part2")
			}
			continue
		}
		if got[i].Content != want[i].Content {
			t.Errorf("[%d].Content = %q, want %q", i, got[i].Content, want[i].Content)
		}
	}

	// Explicit identity check on the assistant-with-tools message to make any
	// drift obvious in the test output.
	idx := 3
	if got[idx].ToolCalls[0].ID != "call_abc" {
		t.Errorf("assistant tool call ID = %q, want %q", got[idx].ToolCalls[0].ID, "call_abc")
	}
	if got[idx].ToolCalls[0].Function.Name != "lookup" {
		t.Errorf("assistant tool call name = %q, want %q", got[idx].ToolCalls[0].Function.Name, "lookup")
	}

	// And confirm store.Messages is a non-nil, populated slice (no accidental nil result).
	if rc.reqLog.Messages == nil {
		t.Error("reqLog.Messages is nil; expected populated slice")
	}
}

// TestBufferModeHeaderParsing pins the LOCKED L1 truth table for the
// X-LLMProxy-Buffer-Response opt-in header (real-streaming-default plan,
// Phase 1; approver iteration 001):
//
//	ABSENT header             ⇒ live streaming (false — the new default)
//	PRESENT + true/1/yes/on   ⇒ buffered (true), case-insensitive value
//	PRESENT + empty value     ⇒ buffered (true — bare-header opt-in)
//	PRESENT + false/0/no/off  ⇒ live streaming (false), case-insensitive
//	PRESENT + unknown value   ⇒ live streaming (false — opt-in must be
//	                             explicit and correctly spelled; unknown
//	                             values must NOT fall back to buffered)
//
// Fixture mechanics: PRESENT rows use DIRECT map access so PRESENT+empty
// ([]string{""}) stays distinct from ABSENT (no entry at all) —
// http.Header.Set cannot express that distinction reliably. The map key
// is canonicalized with http.CanonicalHeaderKey, mirroring the net/http
// server-side header parser (which canonicalizes every incoming wire key,
// whatever casing the client used): r.Header.Values canonicalizes only
// its LOOKUP key, so a raw non-canonical map entry would be invisible to
// it — such an entry cannot occur for a server-parsed request. The
// mixed-case-key rows prove any client spelling of the header name
// resolves to the same canonical entry.
func TestBufferModeHeaderParsing(t *testing.T) {
	const headerName = "X-LLMProxy-Buffer-Response"

	tests := []struct {
		name string
		// setHeader false ⇒ ABSENT (no header entry at all).
		setHeader bool
		// headerKey is the (possibly mixed-case) header name to register;
		// only meaningful when setHeader is true.
		headerKey string
		value     string
		want      bool
	}{
		// PRESENT + truthy ⇒ buffered.
		{name: "present true", setHeader: true, headerKey: headerName, value: "true", want: true},
		{name: "present 1", setHeader: true, headerKey: headerName, value: "1", want: true},
		{name: "present yes", setHeader: true, headerKey: headerName, value: "yes", want: true},
		{name: "present on", setHeader: true, headerKey: headerName, value: "on", want: true},
		{name: "present True (case variant)", setHeader: true, headerKey: headerName, value: "True", want: true},
		{name: "present TRUE (case variant)", setHeader: true, headerKey: headerName, value: "TRUE", want: true},
		{name: "present YES (case variant)", setHeader: true, headerKey: headerName, value: "YES", want: true},

		// PRESENT + empty ⇒ buffered (the deliberate bare-header opt-in).
		{name: "present empty value", setHeader: true, headerKey: headerName, value: "", want: true},

		// PRESENT + falsy ⇒ live streaming.
		{name: "present false", setHeader: true, headerKey: headerName, value: "false", want: false},
		{name: "present 0", setHeader: true, headerKey: headerName, value: "0", want: false},
		{name: "present no", setHeader: true, headerKey: headerName, value: "no", want: false},
		{name: "present off", setHeader: true, headerKey: headerName, value: "off", want: false},
		{name: "present FALSE (case variant)", setHeader: true, headerKey: headerName, value: "FALSE", want: false},
		{name: "present No (case variant)", setHeader: true, headerKey: headerName, value: "No", want: false},

		// PRESENT + unknown ⇒ live streaming (misspelled/garbage must NOT
		// fall back to legacy buffered; whitespace-only is unknown after
		// TrimSpace, not empty).
		{name: "present unknown maybe", setHeader: true, headerKey: headerName, value: "maybe", want: false},
		{name: "present unknown trueish", setHeader: true, headerKey: headerName, value: "trueish", want: false},
		{name: "present whitespace-only", setHeader: true, headerKey: headerName, value: " ", want: false},

		// ABSENT ⇒ live streaming (the new default).
		{name: "absent header", setHeader: false, want: false},

		// Mixed-case header KEYS: each spelling with a truthy value must
		// yield buffered (canonicalization handled by r.Header.Values).
		{name: "mixed-case key X-LLMProxy-Buffer-Response", setHeader: true, headerKey: "X-LLMProxy-Buffer-Response", value: "true", want: true},
		{name: "mixed-case key x-llmproxy-buffer-response", setHeader: true, headerKey: "x-llmproxy-buffer-response", value: "1", want: true},
		{name: "mixed-case key X-LLMPROXY-BUFFER-RESPONSE", setHeader: true, headerKey: "X-LLMPROXY-BUFFER-RESPONSE", value: "on", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			if tt.setHeader {
				req.Header[http.CanonicalHeaderKey(tt.headerKey)] = []string{tt.value}
			}
			got := bufferModeFor(req)
			if got != tt.want {
				t.Errorf("bufferModeFor() = %v, want %v (key=%q value=%q)", got, tt.want, tt.headerKey, tt.value)
			}
		})
	}
}
