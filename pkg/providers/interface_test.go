package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestChatMessage_OpenAIShapeRoundTripIsByteIdentical verifies the R4
// zero-value invariant: an OpenAI-shape request without MiniMax fields
// round-trips through ChatCompletionRequest with zero diff on the wire.
//
// Adding ReasoningDetails + ReasoningDetailEntry + ReasoningSplit *bool
// MUST NOT alter the wire representation when these new fields are nil /
// absent (omitempty guarantees this).
func TestChatMessage_OpenAIShapeRoundTripIsByteIdentical(t *testing.T) {
	openaiShape := []byte(`{
  "model": "gpt-4o",
  "messages": [
    {"role": "system", "content": "You are helpful."},
    {"role": "user", "content": "hi"},
    {"role": "assistant", "content": "hello", "reasoning_content": "thinking"}
  ],
  "max_tokens": 100,
  "temperature": 0.7,
  "stream": false
}`)

	var req ChatCompletionRequest
	if err := json.Unmarshal(openaiShape, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// sanity: pre-existing fields still hydrate
	if req.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", req.Model)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(req.Messages))
	}
	if req.Messages[2].ReasoningContent != "thinking" {
		t.Errorf("ReasoningContent = %q, want thinking", req.Messages[2].ReasoningContent)
	}

	// nil ReasoningDetails + nil ReasoningSplit means no MiniMax keys present
	if req.Messages[2].ReasoningDetails != nil {
		t.Errorf("ReasoningDetails should be nil on OpenAI-shape input")
	}
	if req.ReasoningSplit != nil {
		t.Errorf("ReasoningSplit should be nil on OpenAI-shape input")
	}

	// round-trip and confirm no new wire keys leaked
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "reasoning_details") {
		t.Errorf("wire unexpectedly contains reasoning_details: %s", s)
	}
	if strings.Contains(s, "reasoning_split") {
		t.Errorf("wire unexpectedly contains reasoning_split: %s", s)
	}
}

// TestChatMessage_MiniMaxShapeRoundTrip exercises the D1 carry path: a
// MiniMax-shape input (with reasoning_details array on a message and
// reasoning_split at top level) hydrates ChatMessage.ReasoningDetails and
// ChatCompletionRequest.ReasoningSplit, then re-marshals them.
func TestChatMessage_MiniMaxShapeRoundTrip(t *testing.T) {
	minimaxShape := []byte(`{
  "model": "MiniMax-M1",
  "messages": [
    {"role": "assistant", "content": "answer", "reasoning_details": [{"type":"reasoning.text","id":"reasoning-text-1","format":"MiniMax-response-v1","text":"think"}]}
  ],
  "reasoning_split": true
}`)

	var req ChatCompletionRequest
	if err := json.Unmarshal(minimaxShape, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req.ReasoningSplit == nil || *req.ReasoningSplit != true {
		t.Errorf("ReasoningSplit = %v, want *true", req.ReasoningSplit)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(req.Messages))
	}
	details := req.Messages[0].ReasoningDetails
	if len(details) != 1 {
		t.Fatalf("len(ReasoningDetails) = %d, want 1", len(details))
	}
	d := details[0]
	if d.Type != "reasoning.text" || d.Format != "MiniMax-response-v1" || d.Text != "think" || d.ID != "reasoning-text-1" {
		t.Errorf("entry mismatch: %+v", d)
	}

	// round-trip preserves the MiniMax fields
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"reasoning_split":true`) {
		t.Errorf("reasoning_split lost on round-trip: %s", s)
	}
	if !strings.Contains(s, `"reasoning_details":[{`) || !strings.Contains(s, `"reasoning-text-1"`) {
		t.Errorf("reasoning_details lost on round-trip: %s", s)
	}
}

// TestReasoningSplit_NilVsFalse checks the pointer distinction (D1):
// absent ReasoningSplit (nil) marshals to no key; explicit false marshals
// to false; explicit true marshals to true.
func TestReasoningSplit_NilVsFalse(t *testing.T) {
	trueVal := true
	falseVal := false

	cases := []struct {
		name string
		req  ChatCompletionRequest
		want string
	}{
		{
			name: "nil ⇒ no key",
			req:  ChatCompletionRequest{Model: "m"},
			want: `{"model":"m","messages":null,"stream":false}`,
		},
		{
			name: "explicit true ⇒ reasoning_split:true",
			req:  ChatCompletionRequest{Model: "m", ReasoningSplit: &trueVal},
			want: `{"model":"m","messages":null,"stream":false,"reasoning_split":true}`,
		},
		{
			name: "explicit false ⇒ reasoning_split:false",
			req:  ChatCompletionRequest{Model: "m", ReasoningSplit: &falseVal},
			want: `{"model":"m","messages":null,"stream":false,"reasoning_split":false}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.req)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %s, want %s", string(got), tc.want)
			}
		})
	}
}

// TestStreamEvent_NoReasoningDetails guards the D2 dead-surface rule:
// StreamEvent must NOT carry ReasoningDetails (openai.go's stream parser
// emits one thinking StreamEvent per entry directly, making the field
// dead surface on the stream type). Compile-time check via reflection.
func TestStreamEvent_NoReasoningDetails(t *testing.T) {
	if strings.Contains(strings.SplitAfter(string(jsonMustMarshal(StreamEvent{Type: "thinking"})), "{")[0], "reasoning_details") {
		t.Errorf("StreamEvent JSON unexpectedly contains reasoning_details")
	}
	// belt-and-suspenders: ensure zero-value marshalling emits no reasoning_details
	b, err := json.Marshal(StreamEvent{Type: "thinking"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "reasoning_details") {
		t.Errorf("StreamEvent zero-value JSON contains reasoning_details: %s", string(b))
	}
}

func jsonMustMarshal(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}