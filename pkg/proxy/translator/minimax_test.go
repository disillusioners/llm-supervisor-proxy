package translator

import (
	"encoding/json"
	"reflect"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// ReasoningDetail — JSON shape
// ─────────────────────────────────────────────────────────────────────────────

func TestReasoningDetail_JSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   ReasoningDetail
		want string
	}{
		{
			name: "all fields",
			// Index: 0 has omitempty → dropped on the wire (zero-value).
			in:   ReasoningDetail{Type: "reasoning.text", ID: "reasoning-text-1", Format: "MiniMax-response-v1", Index: 0, Text: "hello"},
			want: `{"type":"reasoning.text","id":"reasoning-text-1","format":"MiniMax-response-v1","text":"hello"}`,
		},
		{
			name: "index non-zero is preserved",
			in:   ReasoningDetail{Type: "reasoning.text", ID: "reasoning-text-1", Format: "MiniMax-response-v1", Index: 2, Text: "hello"},
			want: `{"type":"reasoning.text","id":"reasoning-text-1","format":"MiniMax-response-v1","index":2,"text":"hello"}`,
		},
		{
			name: "zero-value omits all keys",
			in:   ReasoningDetail{},
			want: `{}`,
		},
		{
			name: "text only",
			in:   ReasoningDetail{Text: "hello"},
			want: `{"text":"hello"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Errorf("got %s, want %s", string(b), tc.want)
			}
			var got ReasoningDetail
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(got, tc.in) {
				t.Errorf("round-trip mismatch: got %+v, want %+v", got, tc.in)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// InjectReasoningSplit
// ─────────────────────────────────────────────────────────────────────────────

func TestInjectReasoningSplit_TopLevelTrue(t *testing.T) {
	body := map[string]any{
		"model":    "minimax-m1",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	if err := InjectReasoningSplit(body); err != nil {
		t.Fatalf("InjectReasoningSplit: %v", err)
	}
	v, ok := body["reasoning_split"]
	if !ok {
		t.Fatalf("reasoning_split key missing")
	}
	if v != true {
		t.Errorf("reasoning_split = %v, want true", v)
	}
}

func TestInjectReasoningSplit_PreservesAllOtherKeys(t *testing.T) {
	body := map[string]any{
		"model":    "m",
		"messages": []any{1, 2, 3},
		"stream":   true,
		"extra":    map[string]any{"nested": "value"},
	}
	if err := InjectReasoningSplit(body); err != nil {
		t.Fatalf("InjectReasoningSplit: %v", err)
	}
	if body["model"] != "m" {
		t.Errorf("model mutated: %v", body["model"])
	}
	if body["stream"] != true {
		t.Errorf("stream mutated: %v", body["stream"])
	}
	extra, ok := body["extra"].(map[string]any)
	if !ok || extra["nested"] != "value" {
		t.Errorf("extra mutated: %v", body["extra"])
	}
}

func TestInjectReasoningSplit_NilBodyReturnsError(t *testing.T) {
	if err := InjectReasoningSplit(nil); err == nil {
		t.Errorf("expected error for nil body")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TranslateMessagesReasoning
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateMessagesReasoning_BasicStripAndReplace(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{
				"role":             "assistant",
				"content":          "final answer",
				"reasoning_content": "let me think about this",
			},
		},
	}
	if err := TranslateMessagesReasoning(body); err != nil {
		t.Fatalf("TranslateMessagesReasoning: %v", err)
	}
	msg := body["messages"].([]any)[0].(map[string]any)
	if _, present := msg["reasoning_content"]; present {
		t.Errorf("reasoning_content should be deleted, still present: %v", msg["reasoning_content"])
	}
	details, ok := msg["reasoning_details"].([]any)
	if !ok || len(details) != 1 {
		t.Fatalf("expected one reasoning_details entry, got %v", msg["reasoning_details"])
	}
	d, ok := details[0].(ReasoningDetail)
	if !ok {
		t.Fatalf("expected ReasoningDetail, got %T", details[0])
	}
	if d.Type != "reasoning.text" || d.Format != "MiniMax-response-v1" || d.Index != 0 || d.Text != "let me think about this" {
		t.Errorf("entry mismatch: %+v", d)
	}
	if d.ID != "reasoning-text-1" {
		t.Errorf("ID = %q, want reasoning-text-1", d.ID)
	}
}

func TestTranslateMessagesReasoning_MonotonicCounter(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "q1"},
			map[string]any{"role": "assistant", "content": "a1", "reasoning_content": "think-1"},
			map[string]any{"role": "user", "content": "q2"},
			map[string]any{"role": "assistant", "content": "a2", "reasoning_content": "think-2"},
			map[string]any{"role": "assistant", "content": "a3", "reasoning_content": "think-3"},
		},
	}
	if err := TranslateMessagesReasoning(body); err != nil {
		t.Fatalf("TranslateMessagesReasoning: %v", err)
	}
	msgs := body["messages"].([]any)
	wantIDs := []string{"", "reasoning-text-1", "", "reasoning-text-2", "reasoning-text-3"}
	for i, m := range msgs {
		msg := m.(map[string]any)
		details, hasDetails := msg["reasoning_details"].([]any)
		if wantIDs[i] == "" {
			if hasDetails {
				t.Errorf("message %d: unexpected reasoning_details", i)
			}
			continue
		}
		if !hasDetails || len(details) != 1 {
			t.Fatalf("message %d: expected one entry, got %v", i, msg["reasoning_details"])
		}
		d := details[0].(ReasoningDetail)
		if d.ID != wantIDs[i] {
			t.Errorf("message %d: ID = %q, want %q", i, d.ID, wantIDs[i])
		}
	}
}

func TestTranslateMessagesReasoning_EmptyAndAbsentUnchanged(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "q"},
			map[string]any{"role": "assistant", "content": "a", "reasoning_content": ""},
			map[string]any{"role": "user", "content": "q2"},
		},
	}
	if err := TranslateMessagesReasoning(body); err != nil {
		t.Fatalf("TranslateMessagesReasoning: %v", err)
	}
	msgs := body["messages"].([]any)
	for i, m := range msgs {
		msg := m.(map[string]any)
		if _, present := msg["reasoning_details"]; present {
			t.Errorf("message %d: reasoning_details unexpectedly injected", i)
		}
	}
}

func TestTranslateMessagesReasoning_InvalidMessagesShape(t *testing.T) {
	body := map[string]any{"messages": "not-a-slice"}
	err := TranslateMessagesReasoning(body)
	if err == nil {
		t.Fatalf("expected error for non-[]any messages")
	}
	// map unmutated on error
	if _, present := body["reasoning_details"]; present {
		t.Errorf("map mutated on error")
	}
}

func TestTranslateMessagesReasoning_NilBody(t *testing.T) {
	if err := TranslateMessagesReasoning(nil); err == nil {
		t.Errorf("expected error for nil body")
	}
}

func TestTranslateMessagesReasoning_NonStringReasoningContent(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "assistant", "content": "a", "reasoning_content": 42},
			map[string]any{"role": "assistant", "content": "a", "reasoning_content": []any{"x"}},
		},
	}
	if err := TranslateMessagesReasoning(body); err != nil {
		t.Fatalf("TranslateMessagesReasoning: %v", err)
	}
	msgs := body["messages"].([]any)
	for i, m := range msgs {
		msg := m.(map[string]any)
		if _, present := msg["reasoning_details"]; present {
			t.Errorf("message %d: reasoning_details should not be injected for non-string reasoning_content", i)
		}
	}
}

func TestTranslateMessagesReasoning_NonMapEntriesUntouched(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			"not-a-map",
			map[string]any{"role": "assistant", "content": "a", "reasoning_content": "think"},
		},
	}
	if err := TranslateMessagesReasoning(body); err != nil {
		t.Fatalf("TranslateMessagesReasoning: %v", err)
	}
	msgs := body["messages"].([]any)
	if _, ok := msgs[0].(string); !ok {
		t.Errorf("non-map entry was clobbered: %v", msgs[0])
	}
	d := msgs[1].(map[string]any)["reasoning_details"].([]any)[0].(ReasoningDetail)
	if d.ID != "reasoning-text-1" {
		t.Errorf("counter must skip non-map entries: got %q", d.ID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TranslateRequestBody (map-core)
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateRequestBody_OrderMessagesFirstThenSplit(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "assistant", "content": "a", "reasoning_content": "think"},
		},
	}
	if err := TranslateRequestBody(body); err != nil {
		t.Fatalf("TranslateRequestBody: %v", err)
	}
	// messages were walked (strip-and-replace applied) AND reasoning_split is set
	msg := body["messages"].([]any)[0].(map[string]any)
	if _, present := msg["reasoning_content"]; present {
		t.Errorf("reasoning_content not stripped")
	}
	if body["reasoning_split"] != true {
		t.Errorf("reasoning_split not set: %v", body["reasoning_split"])
	}
}

func TestTranslateRequestBody_NilBody(t *testing.T) {
	if err := TranslateRequestBody(nil); err == nil {
		t.Errorf("expected error for nil body")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TranslateRequestBytes (bytes wrapper)
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateRequestBytes_RoundTrip(t *testing.T) {
	in := []byte(`{"messages":[{"role":"assistant","content":"a","reasoning_content":"think"}],"model":"m"}`)
	out, err := TranslateRequestBytes(in)
	if err != nil {
		t.Fatalf("TranslateRequestBytes: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	if got["reasoning_split"] != true {
		t.Errorf("reasoning_split not set in output")
	}
	msg := got["messages"].([]any)[0].(map[string]any)
	if _, present := msg["reasoning_content"]; present {
		t.Errorf("reasoning_content not stripped in output")
	}
}

func TestTranslateRequestBytes_InvalidJSON(t *testing.T) {
	if _, err := TranslateRequestBytes([]byte("not json")); err == nil {
		t.Errorf("expected error for invalid JSON")
	}
}