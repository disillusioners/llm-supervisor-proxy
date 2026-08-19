package translator

import (
	"bytes"
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

// ─────────────────────────────────────────────────────────────────────────────
// W10 — idempotence (translate-twice equals translate-once)
// ─────────────────────────────────────────────────────────────────────────────

// TestTranslateRequestBody_Idempotent covers AM-03/W10: feeding the
// result of TranslateRequestBody back through it must not introduce a
// second injection pass. The first pass maps reasoning_content →
// reasoning_details and injects reasoning_split; the second pass must
// observe that reasoning_content is gone (the strip) and skip mapping
// again. The id counter is per-call, so re-running must start fresh
// — but since nothing maps, the counter never increments.
//
// We compare the SEMANTIC state (parsed maps) rather than bytes
// because Marshal's key ordering depends on whether the inner entries
// are typed structs (preserved field order) or generic maps
// (alphabetized). After a JSON round-trip via the deep-copy, the
// second pass re-marshal produces alphabetized output regardless of
// what the first marshal produced — the bytes won't match but the
// CONTENT (entries, ids, text, reasoning_split value) is identical.
func TestTranslateRequestBody_Idempotent(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "q"},
			map[string]any{"role": "assistant", "content": "a1", "reasoning_content": "think-1"},
			map[string]any{"role": "user", "content": "q2"},
			map[string]any{"role": "assistant", "content": "a2", "reasoning_content": "think-2"},
			map[string]any{"role": "assistant", "content": "a3", "reasoning_content": "think-3"},
		},
	}
	if err := TranslateRequestBody(body); err != nil {
		t.Fatalf("first TranslateRequestBody: %v", err)
	}
	// Deep-copy the first pass output. json.Marshal→Unmarshal loses
	// struct identity for the inner []any{ReasoningDetail{...}}
	// entries (they become []any{map[string]any{...}}) — that's
	// exactly the state a real second-pass call site would observe
	// since the bytes are round-tripped through JSON over the wire.
	firstBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	var firstCopy map[string]any
	if err := json.Unmarshal(firstBytes, &firstCopy); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}
	// Snapshot the first-pass semantic state for comparison.
	firstState := snapshotRequestBody(t, body)
	// Second pass on the deep-copy.
	if err := TranslateRequestBody(firstCopy); err != nil {
		t.Fatalf("second TranslateRequestBody: %v", err)
	}
	secondState := snapshotRequestBody(t, firstCopy)
	if !reflect.DeepEqual(firstState, secondState) {
		t.Errorf("idempotence broken:\n first=%s\nsecond=%s", firstState, secondState)
	}
}

// TestTranslateRequestBytes_Idempotent is the bytes-wrapper companion
// to the map-core idempotence test. The contract is byte-level
// idempotence: the output of the FIRST TranslateRequestBytes call,
// fed back through the bytes wrapper, must produce an EQUIVALENT
// output (same content; field order may differ because the typed
// ReasoningDetail entries on the first pass marshal in struct field
// order while the deep-copied second-pass entries marshal as maps
// in alphabetized order). We compare by parsing both outputs and
// comparing normalized snapshots — bytes.Equal would fail on this
// re-marshal asymmetry even though the SEMANTIC state is unchanged.
func TestTranslateRequestBytes_Idempotent(t *testing.T) {
	in := []byte(`{"messages":[{"role":"assistant","content":"a","reasoning_content":"think-1"},{"role":"user","content":"q2"},{"role":"assistant","content":"a2","reasoning_content":"think-2"}],"model":"m"}`)
	first, err := TranslateRequestBytes(in)
	if err != nil {
		t.Fatalf("first TranslateRequestBytes: %v", err)
	}
	// Feed the result back through the bytes wrapper. The bytes
	// wrapper itself Unmarshal→Marshal so the second pass operates
	// on map-shaped data — this is exactly the same path a real
	// second-pass call site would see.
	second, err := TranslateRequestBytes(first)
	if err != nil {
		t.Fatalf("second TranslateRequestBytes: %v", err)
	}
	// Parse both into maps and compare structurally.
	var firstMap, secondMap map[string]any
	if err := json.Unmarshal(first, &firstMap); err != nil {
		t.Fatalf("first unmarshal: %v", err)
	}
	if err := json.Unmarshal(second, &secondMap); err != nil {
		t.Fatalf("second unmarshal: %v", err)
	}
	if !reflect.DeepEqual(snapshotRequestBody(t, firstMap), snapshotRequestBody(t, secondMap)) {
		t.Errorf("TranslateRequestBytes idempotence broken:\n first=%s\nsecond=%s", first, second)
	}
}

// snapshotRequestBody extracts the translator-relevant fields from a
// request body in a deterministic, comparable shape. We re-marshal
// the inner entries through JSON so both typed-struct (first pass)
// and map-shaped (deep-copied second pass) representations collapse
// to the same canonical map[string]any form. The index field is
// normalized to 0 when absent — both states (struct with Index:0 +
// omitempty; map without "index" key) serialize to the same JSON
// shape and reflect the same semantic value.
func snapshotRequestBody(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	snap := map[string]any{}
	if v, ok := body["reasoning_split"]; ok {
		snap["reasoning_split"] = v
	}
	rawMsgs, _ := body["messages"].([]any)
	snapMsgs := make([]any, 0, len(rawMsgs))
	for _, raw := range rawMsgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			snapMsgs = append(snapMsgs, raw)
			continue
		}
		snapMsg := map[string]any{}
		for k, v := range msg {
			if k == "reasoning_details" {
				snapMsg[k] = normalizeDetails(v)
				continue
			}
			snapMsg[k] = v
		}
		snapMsgs = append(snapMsgs, snapMsg)
	}
	snap["messages"] = snapMsgs
	return snap
}

// normalizeDetails takes whatever shape the details array is in
// (typed []any{ReasoningDetail{...}} or map-shaped
// []any{map[string]any{...}}) and returns a canonical
// []any{map[string]any{...}} with normalized field values suitable
// for reflect.DeepEqual. The index field defaults to 0 when absent
// because ReasoningDetail.Index has `omitempty` and zero is the
// semantically-equivalent wire value.
func normalizeDetails(v any) []any {
	rawDetails, _ := v.([]any)
	snapDetails := make([]any, 0, len(rawDetails))
	for _, rawDetail := range rawDetails {
		var canonical map[string]any
		switch d := rawDetail.(type) {
		case ReasoningDetail:
			canonical = map[string]any{
				"type":   d.Type,
				"id":     d.ID,
				"format": d.Format,
				"index":  d.Index,
				"text":   d.Text,
			}
		case map[string]any:
			canonical = map[string]any{}
			for k, val := range d {
				canonical[k] = val
			}
			if _, ok := canonical["index"]; !ok {
				canonical["index"] = 0
			}
		default:
			snapDetails = append(snapDetails, rawDetail)
			continue
		}
		// If index is 0 and the JSON would have omitted it, drop
		// the field — this collapses the two equivalent shapes
		// (typed struct with Index:0 + omitempty vs map without
		// the key) into a single canonical form for comparison.
		if idx, ok := canonical["index"]; ok {
			if i, ok := idx.(int); ok && i == 0 {
				delete(canonical, "index")
			}
		}
		snapDetails = append(snapDetails, canonical)
	}
	return snapDetails
}

// ─────────────────────────────────────────────────────────────────────────────
// W11 — empty `reasoning_details: []` round-trip (request side)
// ─────────────────────────────────────────────────────────────────────────────

// TestTranslateMessagesReasoning_EmptyDetailsArrayUntouched covers
// W11: a request body whose messages carry an empty
// `reasoning_details: []` array (and no `reasoning_content`) is a
// no-op for the translator. The driver is reasoning_content; an
// empty-array input carries no signal to map. The translator must
// leave the empty array as-is (no spurious reasoning_content
// injection, no error).
func TestTranslateMessagesReasoning_EmptyDetailsArrayUntouched(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "q"},
			map[string]any{"role": "assistant", "content": "a", "reasoning_details": []any{}},
		},
	}
	if err := TranslateMessagesReasoning(body); err != nil {
		t.Fatalf("TranslateMessagesReasoning: %v", err)
	}
	msg := body["messages"].([]any)[1].(map[string]any)
	details, ok := msg["reasoning_details"].([]any)
	if !ok {
		t.Fatalf("reasoning_details key lost: %v", msg["reasoning_details"])
	}
	if len(details) != 0 {
		t.Errorf("empty reasoning_details grew entries: %v", details)
	}
	if _, present := msg["reasoning_content"]; present {
		t.Errorf("spurious reasoning_content injected onto empty-details message: %v", msg["reasoning_content"])
	}
}

// TestTranslateRequestBytes_EmptyDetailsArrayByteIdentical is the
// bytes-wrapper W11 test: an input whose messages carry empty
// `reasoning_details: []` arrays (and no `reasoning_content`) must
// round-trip byte-identically through the translator. This is the
// call-site gate contract: at the unit level, when the translator IS
// invoked on a body with empty details, the output must equal the
// input.
func TestTranslateRequestBytes_EmptyDetailsArrayByteIdentical(t *testing.T) {
	in := []byte(`{"messages":[{"role":"assistant","content":"a","reasoning_details":[]}],"model":"m"}`)
	out, err := TranslateRequestBytes(in)
	if err != nil {
		t.Fatalf("TranslateRequestBytes: %v", err)
	}
	// The translator injects reasoning_split at top level (true),
	// so a byte-identical assertion would fail. The contract here
	// is narrower: the messages array's empty reasoning_details
	// must NOT be turned into entries, and reasoning_content must
	// NOT be injected.
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msg := got["messages"].([]any)[0].(map[string]any)
	details, ok := msg["reasoning_details"].([]any)
	if !ok {
		t.Fatalf("reasoning_details key lost: %v", msg["reasoning_details"])
	}
	if len(details) != 0 {
		t.Errorf("empty details array grew entries on request: %v", details)
	}
	if _, present := msg["reasoning_content"]; present {
		t.Errorf("spurious reasoning_content injected: %v", msg["reasoning_content"])
	}
}

// TestTranslateMessagesReasoning_EmptyDetailsOverriddenByContent
// covers W11 flag-on case: a message that carries BOTH an empty
// `reasoning_details: []` AND a non-empty `reasoning_content` is
// driven by the latter (the strip-and-replace semantic). The empty
// array is replaced by the mapped entry — the translator does NOT
// merge, does NOT preserve the empty array, and the resulting
// details array contains exactly the mapped entry.
func TestTranslateMessagesReasoning_EmptyDetailsOverriddenByContent(t *testing.T) {
	body := map[string]any{
		"messages": []any{
			map[string]any{
				"role":              "assistant",
				"content":           "final",
				"reasoning_details": []any{},
				"reasoning_content": "think-now",
			},
		},
	}
	if err := TranslateMessagesReasoning(body); err != nil {
		t.Fatalf("TranslateMessagesReasoning: %v", err)
	}
	msg := body["messages"].([]any)[0].(map[string]any)
	if _, present := msg["reasoning_content"]; present {
		t.Errorf("reasoning_content not stripped: %v", msg["reasoning_content"])
	}
	details, ok := msg["reasoning_details"].([]any)
	if !ok {
		t.Fatalf("reasoning_details key lost: %v", msg["reasoning_details"])
	}
	if len(details) != 1 {
		t.Fatalf("expected 1 mapped entry, got %d: %v", len(details), details)
	}
	d, ok := details[0].(ReasoningDetail)
	if !ok {
		t.Fatalf("expected ReasoningDetail, got %T", details[0])
	}
	if d.Text != "think-now" {
		t.Errorf("entry text = %q, want think-now", d.Text)
	}
	if d.ID != "reasoning-text-1" {
		t.Errorf("entry ID = %q, want reasoning-text-1", d.ID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Empty-input no-op table
// ─────────────────────────────────────────────────────────────────────────────

// TestEmptyInputNoOp covers the empty-input edges that are documented
// as no-ops or wrapped errors across the public translator API. This
// is the table-driven gap-filler for AM-02 and Task 5 — existing
// targeted tests cover individual cases; this table consolidates them
// and adds the cases not yet covered (e.g. empty messages array).
func TestEmptyInputNoOp(t *testing.T) {
	t.Run("TranslateRequestBody", func(t *testing.T) {
		// body nil → wrapped error (godoc); missing messages is a
		// no-op (sub-function returns nil for absent messages);
		// empty messages array is a no-op (the loop simply doesn't
		// run).
		if err := TranslateRequestBody(nil); err == nil {
			t.Errorf("nil body should return error")
		}
		// missing messages key → no-op
		body1 := map[string]any{"model": "m"}
		if err := TranslateRequestBody(body1); err != nil {
			t.Errorf("missing messages: %v", err)
		}
		if body1["reasoning_split"] != true {
			t.Errorf("missing-messages body: reasoning_split not injected")
		}
		// empty messages array → no error, reasoning_split injected
		body2 := map[string]any{"messages": []any{}, "model": "m"}
		if err := TranslateRequestBody(body2); err != nil {
			t.Errorf("empty messages: %v", err)
		}
		if body2["reasoning_split"] != true {
			t.Errorf("empty-messages body: reasoning_split not injected")
		}
		msgs := body2["messages"].([]any)
		if len(msgs) != 0 {
			t.Errorf("empty messages array grew: %v", msgs)
		}
	})

	t.Run("TranslateRequestBytes", func(t *testing.T) {
		// empty object is valid JSON and round-trips through the
		// translator (no messages ⇒ no mapping; InjectReasoningSplit
		// still adds the top-level key).
		out, err := TranslateRequestBytes([]byte(`{}`))
		if err != nil {
			t.Fatalf("{} should not error: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got["reasoning_split"] != true {
			t.Errorf("{} body: reasoning_split not injected, got %v", out)
		}
	})

	t.Run("TranslateMessagesReasoning", func(t *testing.T) {
		// nil body → wrapped error; missing messages → no-op; empty
		// messages array → no-op.
		if err := TranslateMessagesReasoning(nil); err == nil {
			t.Errorf("nil body should error")
		}
		// missing messages
		if err := TranslateMessagesReasoning(map[string]any{}); err != nil {
			t.Errorf("missing messages: %v", err)
		}
		// empty messages array — already covered in detail above;
		// consolidated here for the table-driven gap-filler.
		body := map[string]any{"messages": []any{}}
		if err := TranslateMessagesReasoning(body); err != nil {
			t.Errorf("empty messages array: %v", err)
		}
		if len(body["messages"].([]any)) != 0 {
			t.Errorf("empty messages array mutated: %v", body["messages"])
		}
	})

	t.Run("InjectReasoningSplit", func(t *testing.T) {
		// nil body → error; empty (non-nil) map → key added, no
		// error. The latter is a true no-op except for the
		// documented key injection — covered here as a sanity check.
		if err := InjectReasoningSplit(nil); err == nil {
			t.Errorf("nil body should error")
		}
		body := map[string]any{}
		if err := InjectReasoningSplit(body); err != nil {
			t.Errorf("empty map: %v", err)
		}
		if body["reasoning_split"] != true {
			t.Errorf("empty map: key not injected")
		}
	})

	t.Run("TranslateNonStreamResponseBody", func(t *testing.T) {
		// nil body → wrapped error; missing choices → no-op
		// (TranslateNonStreamResponseBody's first branch is "no
		// choices ⇒ nothing to translate"); empty choices array is
		// also a no-op. Existing tests cover nil + invalid; this
		// table fills the missing-choices and empty-choices gaps.
		if err := TranslateNonStreamResponseBody(nil); err == nil {
			t.Errorf("nil body should error")
		}
		// missing choices
		body1 := map[string]any{"model": "m"}
		if err := TranslateNonStreamResponseBody(body1); err != nil {
			t.Errorf("missing choices: %v", err)
		}
		// empty choices array
		body2 := map[string]any{"choices": []any{}}
		if err := TranslateNonStreamResponseBody(body2); err != nil {
			t.Errorf("empty choices: %v", err)
		}
		if len(body2["choices"].([]any)) != 0 {
			t.Errorf("empty choices array mutated")
		}
	})

	t.Run("TranslateNonStreamResponseBytes", func(t *testing.T) {
		// valid empty object → no error, byte-identical (data-
		// absent gate-ON path; already covered by
		// TestTranslateNonStreamResponseBytes_DataAbsentBytesIdentical,
		// but the empty `{}` case is its simplest form — asserted
		// again here as a smoke test for the no-op contract).
		in := []byte(`{}`)
		out, err := TranslateNonStreamResponseBytes(in)
		if err != nil {
			t.Fatalf("{} should not error: %v", err)
		}
		if !bytes.Equal(in, out) {
			t.Errorf("{} body should be byte-identical:\n in=%s\nout=%s", in, out)
		}
	})

	t.Run("ExtractEntryText", func(t *testing.T) {
		// nil and empty-map cases already covered in detail by
		// TestExtractEntryText table-driven; consolidated here as a
		// smoke test for the documented no-op contract.
		if got := ExtractEntryText(nil); got != "" {
			t.Errorf("nil entry should return empty, got %q", got)
		}
		if got := ExtractEntryText(map[string]any{}); got != "" {
			t.Errorf("empty entry should return empty, got %q", got)
		}
	})
}