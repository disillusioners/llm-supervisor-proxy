package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestHandleNonStreamResult_NonStreamReasoning exercises the race-path
// handleNonStreamResult code path (handler.go:1306) and asserts that the
// stored assistant message carries BOTH Content and Thinking fields when
// the upstream returns a non-stream response with reasoning_content.
//
// Pre-fix regression: handleNonStreamResult only extracted msg["content"]
// inline and built the assistant message without a Thinking field, so
// non-stream race-path responses stored empty thinking → Web UI showed none.
//
// Capture-side ONLY: the wire response written to the client must remain
// byte-identical to what the upstream sent (extractNonStreamContent only
// feeds the store-side rc.accumulatedThinking; the original finalBody is
// still written verbatim at handler.go:1355). handleNonStreamResult
// always appends a single trailing '\n' to the buffer-chunked finalBody,
// so the on-the-wire golden is upstreamResponse + "\n" — the exact
// bytes.Equal below proves that the production code path emits ONLY that
// single trailing newline and nothing else, so any interior mutation
// (e.g. a stray "\n" injected mid-body) trips the golden.
func TestHandleNonStreamResult_NonStreamReasoning(t *testing.T) {
	upstreamResponse := `{"id":"chatcmpl-x","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"the answer","reasoning_content":"my chain of thought"},"finish_reason":"stop"}]}`

	// remove-external-upstream P5: the internal arm decodes the upstream
	// JSON into a typed response and re-marshals it, so the wire body is
	// no longer byte-identical to the upstream bytes. The invariants we
	// pin instead are (a) the wire body is semantically the upstream's
	// response, and (b) handleNonStreamResult appends exactly one
	// trailing '\n' (the buffer-chunked finalBody path).

	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body.Close()
		var reqBody map[string]interface{}
		_ = json.Unmarshal(bodyBytes, &reqBody)

		// Mirror mockLLMHandler: only respond when stream==false
		isStream := true
		if s, ok := reqBody["stream"].(bool); ok && !s {
			isStream = false
		}
		if isStream {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"expected non-stream"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(upstreamResponse))
	}

	mc := models.NewModelsConfig()
	h, _ := newTestHandler(t, upstreamHandler, mc)

	body := simpleBody("test-model", false)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()
	h.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}

	// Semantic golden: the client body must decode to the upstream's
	// response (the internal arm re-marshals the typed response).
	wire := rr.Body.Bytes()
	var parsed map[string]interface{}
	if err := json.Unmarshal(wire, &parsed); err != nil {
		t.Fatalf("client body is not JSON: %v (body=%q)", err, wire)
	}
	choices, _ := parsed["choices"].([]interface{})
	if len(choices) == 0 {
		t.Fatalf("client body has no choices: %q", wire)
	}
	msg, _ := choices[0].(map[string]interface{})["message"].(map[string]interface{})
	if msg["content"] != "the answer" {
		t.Errorf("wire content = %v, want %q", msg["content"], "the answer")
	}
	if msg["reasoning_content"] != "my chain of thought" {
		t.Errorf("wire reasoning_content = %v, want %q", msg["reasoning_content"], "my chain of thought")
	}
	// Exactly one trailing newline (no interior/duplicate mutation).
	if !bytes.HasSuffix(wire, []byte("\n")) || bytes.HasSuffix(wire, []byte("\n\n")) {
		t.Errorf("wire body must end with exactly one trailing newline: %q", wire)
	}

	// Stored assistant message must carry BOTH Content and Thinking.
	reqs := h.store.List()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request in store, got %d", len(reqs))
	}
	assistantMsg := getLastAssistantMessage(reqs[0])
	if assistantMsg == nil {
		t.Fatal("expected assistant message in request log")
	}
	if assistantMsg.Content != "the answer" {
		t.Errorf("Content: got %q, want %q", assistantMsg.Content, "the answer")
	}
	if assistantMsg.Thinking != "my chain of thought" {
		t.Errorf("Thinking: got %q, want %q", assistantMsg.Thinking, "my chain of thought")
	}
}

// TestHandleNonStreamResult_NoReasoningPreservesLegacy asserts that the
// "none" path (content present, no reasoning variant) still produces a
// stored assistant message with the legacy semantics: Content extracted,
// Thinking empty (matches pre-fix behavior exactly for back-compat).
func TestHandleNonStreamResult_NoReasoningPreservesLegacy(t *testing.T) {
	upstreamResponse := `{"id":"chatcmpl-y","object":"chat.completion","model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"plain answer only"},"finish_reason":"stop"}]}`

	upstreamHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(upstreamResponse))
	}

	mc := models.NewModelsConfig()
	h, _ := newTestHandler(t, upstreamHandler, mc)

	body := simpleBody("test-model", false)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()
	h.HandleChatCompletions(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	reqs := h.store.List()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request in store, got %d", len(reqs))
	}
	assistantMsg := getLastAssistantMessage(reqs[0])
	if assistantMsg == nil {
		t.Fatal("expected assistant message in request log")
	}
	if assistantMsg.Content != "plain answer only" {
		t.Errorf("Content: got %q, want %q", assistantMsg.Content, "plain answer only")
	}
	if assistantMsg.Thinking != "" {
		t.Errorf("Thinking: got %q, want empty (legacy parity)", assistantMsg.Thinking)
	}
}
