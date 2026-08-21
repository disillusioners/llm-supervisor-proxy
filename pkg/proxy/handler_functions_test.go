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
