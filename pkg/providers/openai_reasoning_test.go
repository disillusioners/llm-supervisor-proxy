package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// P2-1a: helper-level tests
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractReasoningDetailsFromRawData_NoDetails(t *testing.T) {
	data := []byte(`{"choices":[{"delta":{"content":"hi"}}]}`)
	emitted, hasDetails := extractReasoningDetailsFromRawData(data)
	if hasDetails {
		t.Errorf("hasDetails should be false, got true")
	}
	if len(emitted) != 0 {
		t.Errorf("emitted should be empty, got %v", emitted)
	}
}

func TestExtractReasoningDetailsFromRawData_OneEntry(t *testing.T) {
	data := []byte(`{"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.text","text":"a"}]}}]}`)
	emitted, hasDetails := extractReasoningDetailsFromRawData(data)
	if !hasDetails {
		t.Errorf("hasDetails should be true")
	}
	if len(emitted) != 1 || emitted[0] != "a" {
		t.Errorf("emitted = %v, want [a]", emitted)
	}
}

func TestExtractReasoningDetailsFromRawData_MultipleEntries(t *testing.T) {
	data := []byte(`{"choices":[{"delta":{"reasoning_details":[
		{"type":"reasoning.text","text":"a"},
		{"type":"reasoning.text","text":"b"}
	]}}]}`)
	emitted, _ := extractReasoningDetailsFromRawData(data)
	if len(emitted) != 2 || emitted[0] != "a" || emitted[1] != "b" {
		t.Errorf("emitted = %v, want [a b] in order", emitted)
	}
}

func TestExtractReasoningDetailsFromRawData_EmptyTextSkipped(t *testing.T) {
	data := []byte(`{"choices":[{"delta":{"reasoning_details":[
		{"type":"reasoning.text","text":""},
		{"type":"reasoning.text","text":"real"}
	]}}]}`)
	emitted, hasDetails := extractReasoningDetailsFromRawData(data)
	if !hasDetails {
		t.Errorf("hasDetails should be true even with skip-empty")
	}
	if len(emitted) != 1 || emitted[0] != "real" {
		t.Errorf("emitted = %v, want [real]", emitted)
	}
}

func TestExtractReasoningDetailsFromRawData_UnknownTypeSkipped(t *testing.T) {
	data := []byte(`{"choices":[{"delta":{"reasoning_details":[
		{"type":"reasoning.text","text":"keep"},
		{"type":"unknown.type","text":"drop"}
	]}}]}`)
	emitted, _ := extractReasoningDetailsFromRawData(data)
	if len(emitted) != 1 || emitted[0] != "keep" {
		t.Errorf("emitted = %v, want [keep]", emitted)
	}
}

func TestExtractReasoningDetailsFromRawData_ContentFallback(t *testing.T) {
	// Forward-compat: `content` key works (H3 / §6.2)
	data := []byte(`{"choices":[{"delta":{"reasoning_details":[
		{"type":"reasoning.text","content":"v2-style"}
	]}}]}`)
	emitted, _ := extractReasoningDetailsFromRawData(data)
	if len(emitted) != 1 || emitted[0] != "v2-style" {
		t.Errorf("emitted = %v, want [v2-style] (content fallback)", emitted)
	}
}

func TestExtractReasoningDetailsFromRawData_Dedup(t *testing.T) {
	// H2 dedup: cumulative-mode upstream replay.
	data := []byte(`{"choices":[{"delta":{"reasoning_details":[
		{"type":"reasoning.text","text":"abc"},
		{"type":"reasoning.text","text":"abc"}
	]}}]}`)
	emitted, _ := extractReasoningDetailsFromRawData(data)
	if len(emitted) != 1 {
		t.Errorf("intra-chunk dedup: expected 1 emit, got %d", len(emitted))
	}
}

func TestExtractReasoningDetailsFromRawData_MessageFallback(t *testing.T) {
	// Non-stream shape: message.reasoning_details.
	data := []byte(`{"choices":[{"message":{"reasoning_details":[
		{"type":"reasoning.text","text":"ns"}
	]}}]}`)
	emitted, hasDetails := extractReasoningDetailsFromRawData(data)
	if !hasDetails {
		t.Errorf("hasDetails should be true for message shape")
	}
	if len(emitted) != 1 || emitted[0] != "ns" {
		t.Errorf("emitted = %v, want [ns]", emitted)
	}
}

func TestExtractReasoningDetailsFromRawData_InvalidJSON(t *testing.T) {
	emitted, hasDetails := extractReasoningDetailsFromRawData([]byte("not json"))
	if hasDetails || len(emitted) != 0 {
		t.Errorf("invalid JSON should return zero values, got emitted=%v hasDetails=%v", emitted, hasDetails)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// populateReasoningFromDetails — typed path non-stream
// ─────────────────────────────────────────────────────────────────────────────

func TestPopulateReasoningFromDetails_BasicConcat(t *testing.T) {
	msg := &ChatMessage{
		ReasoningContent: "",
		ReasoningDetails: []ReasoningDetailEntry{
			{Type: "reasoning.text", Text: "a"},
			{Type: "reasoning.text", Text: "b"},
		},
	}
	populateReasoningFromDetails(msg)
	if msg.ReasoningContent != "ab" {
		t.Errorf("ReasoningContent = %q, want ab", msg.ReasoningContent)
	}
	if msg.ReasoningDetails != nil {
		t.Errorf("ReasoningDetails should be nil after populate, got %+v", msg.ReasoningDetails)
	}
}

func TestPopulateReasoningFromDetails_TrueSingleWinner(t *testing.T) {
	// W6 true single-winner: pre-existing reasoning_content is
	// DISCARDED entirely (no dedup-vs-prior, no concatenation,
	// no O(n²) containment). The result is the entries
	// concatenated, with intra-accumulator dedup applied.
	msg := &ChatMessage{
		ReasoningContent: "abc",
		ReasoningDetails: []ReasoningDetailEntry{
			{Type: "reasoning.text", Text: "abc"},
			{Type: "reasoning.text", Text: "new"},
		},
	}
	populateReasoningFromDetails(msg)
	// "abc" (pre-existing) is discarded; entries are concatenated
	// in order with intra-array dedup: "abc" and "new" are
	// distinct ⇒ both kept. Result: "abcnew".
	if msg.ReasoningContent != "abcnew" {
		t.Errorf("ReasoningContent = %q, want abcnew (W6 true single-winner)", msg.ReasoningContent)
	}
}

func TestPopulateReasoningFromDetails_EmptyTextSkipped(t *testing.T) {
	msg := &ChatMessage{
		ReasoningDetails: []ReasoningDetailEntry{
			{Type: "reasoning.text", Text: ""},
			{Type: "reasoning.text", Text: "  "},
			{Type: "reasoning.text", Text: "real"},
		},
	}
	populateReasoningFromDetails(msg)
	if msg.ReasoningContent != "real" {
		t.Errorf("ReasoningContent = %q, want real", msg.ReasoningContent)
	}
}

func TestPopulateReasoningFromDetails_BothFieldsSingleWinner(t *testing.T) {
	// Single-winner: when BOTH fields are present, reasoning_details
	// WINS and reasoning_content is ignored (not concatenated).
	msg := &ChatMessage{
		ReasoningContent: "should-be-ignored",
		ReasoningDetails: []ReasoningDetailEntry{
			{Type: "reasoning.text", Text: "details-text"},
		},
	}
	populateReasoningFromDetails(msg)
	if msg.ReasoningContent != "details-text" {
		t.Errorf("ReasoningContent = %q, want details-text (single-winner rule)", msg.ReasoningContent)
	}
}

func TestPopulateReasoningFromDetails_NoDetailsPreservesRC(t *testing.T) {
	// When details are absent, the existing reasoning_content is
	// preserved untouched.
	msg := &ChatMessage{ReasoningContent: "upstream-string"}
	populateReasoningFromDetails(msg)
	if msg.ReasoningContent != "upstream-string" {
		t.Errorf("ReasoningContent = %q, want upstream-string", msg.ReasoningContent)
	}
}

func TestPopulateReasoningFromDetails_NilMsgSafe(t *testing.T) {
	// Should not panic.
	populateReasoningFromDetails(nil)
}

func TestPopulateReasoningFromDetails_OmitemptyDropsKey(t *testing.T) {
	// R11 no-leak: after populate, the JSON marshal of the message
	// must not contain "reasoning_details" (omitempty drops the nil
	// slice).
	msg := &ChatMessage{
		ReasoningDetails: []ReasoningDetailEntry{
			{Type: "reasoning.text", Text: "hi"},
		},
	}
	populateReasoningFromDetails(msg)
	b, _ := json.Marshal(msg)
	if strings.Contains(string(b), "reasoning_details") {
		t.Errorf("marshaled message still contains reasoning_details: %s", b)
	}
	if !strings.Contains(string(b), `"reasoning_content":"hi"`) {
		t.Errorf("marshaled message missing reasoning_content: %s", b)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Integration: ChatCompletion (non-stream) end-to-end
// ─────────────────────────────────────────────────────────────────────────────

// TestOpenAIProvider_NonStream_ExtractsReasoningDetails — the typed
// path: when the upstream returns reasoning_details, the returned
// ChatCompletionResponse has ReasoningContent populated and
// ReasoningDetails cleared.
func TestOpenAIProvider_NonStream_ExtractsReasoningDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ChatCompletionResponse{
			ID:      "r",
			Object:  "chat.completion",
			Created: 1700000000,
			Model:   "m",
			Choices: []Choice{
				{
					Index: 0,
					Message: &ChatMessage{
						Role:    "assistant",
						Content: "final",
						ReasoningDetails: []ReasoningDetailEntry{
							{Type: "reasoning.text", Text: "think-1"},
							{Type: "reasoning.text", Text: "think-2"},
						},
					},
					FinishReason: "stop",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("k", server.URL)
	got, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Model:    "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if got.Choices[0].Message.ReasoningContent != "think-1think-2" {
		t.Errorf("ReasoningContent = %q, want think-1think-2", got.Choices[0].Message.ReasoningContent)
	}
	if got.Choices[0].Message.ReasoningDetails != nil {
		t.Errorf("ReasoningDetails should be nil after extraction, got %+v", got.Choices[0].Message.ReasoningDetails)
	}
}

func TestOpenAIProvider_NonStream_BothFieldsSingleWinner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ChatCompletionResponse{
			ID: "r", Object: "chat.completion", Created: 1, Model: "m",
			Choices: []Choice{
				{
					Index: 0,
					Message: &ChatMessage{
						Role:             "assistant",
						Content:          "final",
						ReasoningContent: "should-be-ignored",
						ReasoningDetails: []ReasoningDetailEntry{
							{Type: "reasoning.text", Text: "details-text"},
						},
					},
					FinishReason: "stop",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("k", server.URL)
	got, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Model:    "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if got.Choices[0].Message.ReasoningContent != "details-text" {
		t.Errorf("ReasoningContent = %q, want details-text (single-winner)", got.Choices[0].Message.ReasoningContent)
	}
}

func TestOpenAIProvider_NonStream_NoDetailsLeavesRCAlone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ChatCompletionResponse{
			ID: "r", Object: "chat.completion", Created: 1, Model: "m",
			Choices: []Choice{
				{
					Index: 0,
					Message: &ChatMessage{
						Role:             "assistant",
						Content:          "final",
						ReasoningContent: "upstream-only",
					},
					FinishReason: "stop",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("k", server.URL)
	got, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Model:    "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if got.Choices[0].Message.ReasoningContent != "upstream-only" {
		t.Errorf("ReasoningContent = %q, want upstream-only (no details ⇒ preserved)", got.Choices[0].Message.ReasoningContent)
	}
}

func TestOpenAIProvider_NonStream_EmptyTextSkipped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ChatCompletionResponse{
			ID: "r", Object: "chat.completion", Created: 1, Model: "m",
			Choices: []Choice{
				{
					Index: 0,
					Message: &ChatMessage{
						Role:    "assistant",
						Content: "final",
						ReasoningDetails: []ReasoningDetailEntry{
							{Type: "reasoning.text", Text: ""},
							{Type: "reasoning.text", Text: "real"},
						},
					},
					FinishReason: "stop",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("k", server.URL)
	got, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Model:    "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if got.Choices[0].Message.ReasoningContent != "real" {
		t.Errorf("ReasoningContent = %q, want real (H7 skip-empty)", got.Choices[0].Message.ReasoningContent)
	}
}

func TestOpenAIProvider_NonStream_NonMiniMaxShapeRoundTripsUnchanged(t *testing.T) {
	// A non-MiniMax shape (no reasoning_details, with reasoning_content)
	// must round-trip unchanged.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := ChatCompletionResponse{
			ID: "r", Object: "chat.completion", Created: 1, Model: "m",
			Choices: []Choice{
				{
					Index: 0,
					Message: &ChatMessage{
						Role:             "assistant",
						Content:          "hello",
						ReasoningContent: "deepseek-think",
					},
					FinishReason: "stop",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAIProvider("k", server.URL)
	got, err := p.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Model:    "m",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if got.Choices[0].Message.ReasoningContent != "deepseek-think" {
		t.Errorf("ReasoningContent = %q, want deepseek-think (untouched)", got.Choices[0].Message.ReasoningContent)
	}
	if got.Choices[0].Message.ReasoningDetails != nil {
		t.Errorf("ReasoningDetails should remain nil for non-MiniMax-shape input")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Integration: StreamChatCompletion end-to-end
// ─────────────────────────────────────────────────────────────────────────────

func TestOpenAIProvider_Stream_EmitsThinkingPerEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Two reasoning_details entries, then content, then finish.
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"a"},{"type":"reasoning.text","text":"b"}]}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{"content":"final"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := NewOpenAIProvider("k", server.URL)
	evCh, err := p.StreamChatCompletion(context.Background(), &ChatCompletionRequest{
		Model: "m", Stream: true,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}
	var think []string
	for ev := range evCh {
		if ev.Type == "thinking" {
			think = append(think, ev.ReasoningContent)
		}
	}
	if len(think) != 2 || think[0] != "a" || think[1] != "b" {
		t.Errorf("thinking events = %v, want [a b] in order", think)
	}
}

func TestOpenAIProvider_Stream_BothFieldsSingleWinner(t *testing.T) {
	// When the chunk carries BOTH delta.reasoning_details and
	// delta.reasoning_content, reasoning_details WINS and the
	// reasoning_content field is NOT extracted as a thinking event.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Both fields in the SAME chunk: details wins.
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"from-details"}],"reasoning_content":"should-be-ignored"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := NewOpenAIProvider("k", server.URL)
	evCh, err := p.StreamChatCompletion(context.Background(), &ChatCompletionRequest{
		Model: "m", Stream: true,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}
	var think []string
	for ev := range evCh {
		if ev.Type == "thinking" {
			think = append(think, ev.ReasoningContent)
		}
	}
	if len(think) != 1 || think[0] != "from-details" {
		t.Errorf("thinking events = %v, want [from-details] (single-winner rule)", think)
	}
}

func TestOpenAIProvider_Stream_NoDetailsFallsBackToRC(t *testing.T) {
	// When reasoning_details is absent, the existing
	// reasoning_content (DeepSeek-style) path is preserved.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_content":"deepseek-think"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := NewOpenAIProvider("k", server.URL)
	evCh, err := p.StreamChatCompletion(context.Background(), &ChatCompletionRequest{
		Model: "m", Stream: true,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}
	var think []string
	for ev := range evCh {
		if ev.Type == "thinking" {
			think = append(think, ev.ReasoningContent)
		}
	}
	if len(think) != 1 || think[0] != "deepseek-think" {
		t.Errorf("thinking events = %v, want [deepseek-think] (fallback to reasoning_content)", think)
	}
}

func TestOpenAIProvider_Stream_EmptyDetailsSkipped(t *testing.T) {
	// H7 skip-empty: empty entries are NOT emitted.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":""},{"type":"reasoning.text","text":"real"}]}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := NewOpenAIProvider("k", server.URL)
	evCh, err := p.StreamChatCompletion(context.Background(), &ChatCompletionRequest{
		Model: "m", Stream: true,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}
	var think []string
	for ev := range evCh {
		if ev.Type == "thinking" {
			think = append(think, ev.ReasoningContent)
		}
	}
	if len(think) != 1 || think[0] != "real" {
		t.Errorf("thinking events = %v, want [real] (H7 skip-empty)", think)
	}
}

func TestOpenAIProvider_Stream_NonMiniMaxShapePassthrough(t *testing.T) {
	// A non-MiniMax shape (no reasoning_details, no reasoning_content)
	// produces no thinking events.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	p := NewOpenAIProvider("k", server.URL)
	evCh, err := p.StreamChatCompletion(context.Background(), &ChatCompletionRequest{
		Model: "m", Stream: true,
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChatCompletion: %v", err)
	}
	var think []string
	for ev := range evCh {
		if ev.Type == "thinking" {
			think = append(think, ev.ReasoningContent)
		}
	}
	if len(think) != 0 {
		t.Errorf("non-MiniMax-shape: no thinking events, got %v", think)
	}
}
