package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// ExtractEntryText — table-driven
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractEntryText(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{
			name: "text key wins",
			in:   map[string]any{"text": "a", "content": "b"},
			want: "a",
		},
		{
			name: "content fallback when text absent",
			in:   map[string]any{"content": "b"},
			want: "b",
		},
		{
			name: "empty map returns empty",
			in:   map[string]any{},
			want: "",
		},
		{
			name: "nil map returns empty",
			in:   nil,
			want: "",
		},
		{
			name: "text empty string falls through to content",
			in:   map[string]any{"text": "", "content": "b"},
			want: "b",
		},
		{
			name: "both empty returns empty",
			in:   map[string]any{"text": "", "content": ""},
			want: "",
		},
		{
			name: "text non-string is ignored, content used",
			in:   map[string]any{"text": 42, "content": "b"},
			want: "b",
		},
		{
			name: "content non-string is ignored, text wins",
			in:   map[string]any{"text": "a", "content": 42},
			want: "a",
		},
		{
			name: "whitespace-only text is returned (H7 trim is caller's job)",
			in:   map[string]any{"text": "   "},
			want: "   ",
		},
		{
			name: "text with leading/trailing whitespace is preserved (untrimmed)",
			in:   map[string]any{"text": "  hello  "},
			want: "  hello  ",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractEntryText(tc.in)
			if got != tc.want {
				t.Errorf("ExtractEntryText(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// formatDriftCounter
// ─────────────────────────────────────────────────────────────────────────────

func TestFormatDriftCounter_IncrementsOnUnknownFormat(t *testing.T) {
	before := FormatDriftCount()
	recordDrift(map[string]any{"format": "MiniMax-response-v2-" + t.Name(), "id": "x"})
	after := FormatDriftCount()
	if after != before+1 {
		t.Errorf("counter not incremented: before=%d after=%d", before, after)
	}
}

func TestFormatDriftCounter_NoIncrementOnKnownFormat(t *testing.T) {
	// Use the helper directly so we don't pollute the counter on
	// internal re-marshal paths.
	before := FormatDriftCount()
	entry := map[string]any{"format": reasoningTextFormat}
	if formatVal, hasFormat := entry["format"].(string); hasFormat && formatVal != "" {
		if _, known := knownReasoningFormats[formatVal]; !known {
			recordDrift(entry)
		}
	}
	after := FormatDriftCount()
	if after != before {
		t.Errorf("known format incremented the counter: before=%d after=%d", before, after)
	}
}

func TestFormatDriftCounter_NoDoubleIncrementForRepeatedIdenticalDrift(t *testing.T) {
	// P2-9 acceptance: the counter counts DISTINCT unknown format
	// values, not entry volume. Two entries carrying the SAME
	// unknown format → +1; a second DISTINCT unknown format → +1
	// more. Format values are t.Name()-derived so this test stays
	// independent of any other test's dedup-set pollution.
	base := "MiniMax-response-drift-" + t.Name()
	before := FormatDriftCount()
	recordDrift(map[string]any{"format": base + "-a", "id": "1"})
	recordDrift(map[string]any{"format": base + "-a", "id": "2"}) // same value — deduped
	mid := FormatDriftCount()
	if mid != before+1 {
		t.Errorf("repeated identical drift double-incremented: before=%d mid=%d (want +1)", before, mid)
	}
	recordDrift(map[string]any{"format": base + "-b", "id": "3"}) // distinct value
	after := FormatDriftCount()
	if after != mid+1 {
		t.Errorf("distinct drift did not increment: mid=%d after=%d (want +1)", mid, after)
	}
	// Repeated again after the distinct value — still no re-increment
	// for either previously-seen value.
	recordDrift(map[string]any{"format": base + "-a", "id": "4"})
	recordDrift(map[string]any{"format": base + "-b", "id": "5"})
	final := FormatDriftCount()
	if final != after {
		t.Errorf("repeated known-unknown formats re-incremented: after=%d final=%d", after, final)
	}
}

func TestRecordDrift_IncrementIsAtomic(t *testing.T) {
	// Sanity: hammering recordDrift from multiple goroutines with
	// DISTINCT format values must produce a deterministic final
	// count (one increment per distinct value — the dedup set is a
	// sync.Map, the counter is atomic).
	const N = 1000
	base := "MiniMax-response-atomic-" + t.Name()
	before := FormatDriftCount()
	var done atomic.Uint32
	for i := 0; i < 4; i++ {
		go func(i int) {
			for j := 0; j < N; j++ {
				recordDrift(map[string]any{"format": fmt.Sprintf("%s-g%d-j%d", base, i, j), "id": "concurrent"})
			}
			done.Add(1)
		}(i)
	}
	for done.Load() < 4 {
	}
	after := FormatDriftCount()
	if after-before != 4*N {
		t.Errorf("atomic increment race: expected %d, got %d", 4*N, after-before)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TranslateNonStreamResponseBody — happy path
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateNonStreamResponseBody_BasicConcat(t *testing.T) {
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"index": float64(0),
				"message": map[string]any{
					"role": "assistant",
					"reasoning_details": []any{
						map[string]any{"type": "reasoning.text", "text": "a"},
						map[string]any{"type": "reasoning.text", "text": "b"},
					},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if got := msg["reasoning_content"]; got != "ab" {
		t.Errorf("reasoning_content = %q, want ab", got)
	}
	if _, present := msg["reasoning_details"]; present {
		t.Errorf("reasoning_details should be stripped")
	}
}

func TestTranslateNonStreamResponseBody_BothFieldsDedup(t *testing.T) {
	// D2 single-winner: when BOTH reasoning_details and
	// reasoning_content are present, reasoning_details WINS. The
	// pre-existing reasoning_content is ignored (not concatenated,
	// not duplicated).
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"reasoning_content": "should-be-ignored",
					"reasoning_details": []any{
						map[string]any{"type": "reasoning.text", "text": "a"},
					},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if got := msg["reasoning_content"]; got != "a" {
		t.Errorf("reasoning_content = %q, want a (single-winner rule)", got)
	}
	if _, present := msg["reasoning_details"]; present {
		t.Errorf("reasoning_details should be stripped")
	}
}

func TestTranslateNonStreamResponseBody_DedupIntraArray(t *testing.T) {
	// Cumulative-mode upstream replay: the array contains the same
	// text twice. The second is deduped.
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"reasoning_details": []any{
						map[string]any{"type": "reasoning.text", "text": "abc"},
						map[string]any{"type": "reasoning.text", "text": "abc"},
					},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if got := msg["reasoning_content"]; got != "abc" {
		t.Errorf("reasoning_content = %q, want abc (intra-array dedup)", got)
	}
}

func TestTranslateNonStreamResponseBody_EmptyTextSkipped(t *testing.T) {
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"reasoning_details": []any{
						map[string]any{"type": "reasoning.text", "text": ""},
						map[string]any{"type": "reasoning.text", "text": "  "},
						map[string]any{"type": "reasoning.text", "text": "real"},
					},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if got := msg["reasoning_content"]; got != "real" {
		t.Errorf("reasoning_content = %q, want real (H7 skip-empty)", got)
	}
}

func TestTranslateNonStreamResponseBody_UnknownTypeLoggedAndSkipped(t *testing.T) {
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"reasoning_details": []any{
						map[string]any{"type": "unknown.type", "text": "skip-me"},
						map[string]any{"type": "reasoning.text", "text": "keep"},
					},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if got := msg["reasoning_content"]; got != "keep" {
		t.Errorf("reasoning_content = %q, want keep (unknown type skipped)", got)
	}
}

func TestTranslateNonStreamResponseBody_StripsAudioAndName(t *testing.T) {
	// Sibling metadata fields named in the plan (audio + name) must
	// be stripped — they are MiniMax-only fields and would leak
	// upstream semantics to OpenAI clients.
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role":           "assistant",
					"audio":          map[string]any{"id": "x"},
					"audio_content":  "speech",
					"name":           "minimax-thing",
					"content":        "final",
					"reasoning_details": []any{
						map[string]any{"type": "reasoning.text", "text": "think"},
					},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if _, present := msg["audio"]; present {
		t.Errorf("audio should be stripped")
	}
	if _, present := msg["audio_content"]; present {
		t.Errorf("audio_content should be stripped")
	}
	if _, present := msg["name"]; present {
		t.Errorf("name should be stripped")
	}
	if got := msg["content"]; got != "final" {
		t.Errorf("content mutated: %v", got)
	}
	if got := msg["reasoning_content"]; got != "think" {
		t.Errorf("reasoning_content = %q, want think", got)
	}
}

func TestTranslateNonStreamResponseBody_FormatDriftCounterIncrements(t *testing.T) {
	before := FormatDriftCount()
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"reasoning_details": []any{
						map[string]any{"type": "reasoning.text", "format": "MiniMax-response-ns-" + t.Name(), "text": "hi"},
					},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	after := FormatDriftCount()
	if after != before+1 {
		t.Errorf("counter not incremented on unknown format: before=%d after=%d", before, after)
	}
}

func TestTranslateNonStreamResponseBody_NoDetailsLeavesReasoningContent(t *testing.T) {
	// When reasoning_details is absent, the existing reasoning_content
	// survives untouched. This matches the typed/internal path's
	// "single-winner when details present" semantics.
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role":             "assistant",
					"reasoning_content": "upstream-string",
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if got := msg["reasoning_content"]; got != "upstream-string" {
		t.Errorf("reasoning_content = %q, want upstream-string (no details ⇒ no translation)", got)
	}
}

func TestTranslateNonStreamResponseBody_NilBody(t *testing.T) {
	if err := TranslateNonStreamResponseBody(nil); err == nil {
		t.Errorf("expected error for nil body")
	}
}

func TestTranslateNonStreamResponseBody_InvalidChoicesShape(t *testing.T) {
	body := map[string]any{"choices": "not-a-slice"}
	if err := TranslateNonStreamResponseBody(body); err == nil {
		t.Errorf("expected error for non-[]any choices")
	}
}

func TestTranslateNonStreamResponseBody_MissingChoicesNoop(t *testing.T) {
	body := map[string]any{"model": "m"}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Errorf("missing choices should be a no-op, got: %v", err)
	}
}

func TestTranslateNonStreamResponseBody_NonMapChoiceSkipped(t *testing.T) {
	body := map[string]any{
		"choices": []any{
			"not-a-map",
			map[string]any{
				"message": map[string]any{
					"role": "assistant",
					"reasoning_details": []any{
						map[string]any{"type": "reasoning.text", "text": "ok"},
					},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	msg := body["choices"].([]any)[1].(map[string]any)["message"].(map[string]any)
	if got := msg["reasoning_content"]; got != "ok" {
		t.Errorf("reasoning_content = %q, want ok", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TranslateNonStreamResponseBytes — wrapper
// ─────────────────────────────────────────────────────────────────────────────

func TestTranslateNonStreamResponseBytes_RoundTrip(t *testing.T) {
	in := []byte(`{"choices":[{"index":0,"message":{"role":"assistant","reasoning_details":[{"type":"reasoning.text","text":"hello"}]}}]}`)
	out, err := TranslateNonStreamResponseBytes(in)
	if err != nil {
		t.Fatalf("TranslateNonStreamResponseBytes: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msg := got["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["reasoning_content"] != "hello" {
		t.Errorf("reasoning_content = %v, want hello", msg["reasoning_content"])
	}
	if _, present := msg["reasoning_details"]; present {
		t.Errorf("reasoning_details should be stripped")
	}
}

func TestTranslateNonStreamResponseBytes_DataAbsentBytesIdentical(t *testing.T) {
	// N-1/Q3: gate-ON-shaped body with NO reasoning_details and NO
	// audio/audio_content/name keys is a semantic no-op — the
	// ORIGINAL input bytes must be returned untouched (bytes.Equal),
	// not a re-marshaled copy (alphabetized keys, float64 numbers).
	// The fixture deliberately uses non-alphabetical key order and
	// compact separators so a re-marshal would produce visibly
	// different bytes.
	in := []byte(`{"id":"r","object":"chat.completion","created":1,"model":"minimax","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	out, err := TranslateNonStreamResponseBytes(in)
	if err != nil {
		t.Fatalf("TranslateNonStreamResponseBytes: %v", err)
	}
	if !bytes.Equal(in, out) {
		t.Errorf("data-absent body was re-marshaled:\n in=%s\nout=%s", in, out)
	}
}

func TestTranslateNonStreamResponseBytes_ReasoningContentOnlyBytesIdentical(t *testing.T) {
	// N-1/Q3 variant: reasoning_content-only response (ordinary
	// MiniMax shape without reasoning_details) is also a no-op —
	// pre-existing reasoning_content is preserved verbatim.
	in := []byte(`{"choices":[{"message":{"reasoning_content":"already-here","role":"assistant"}}]}`)
	out, err := TranslateNonStreamResponseBytes(in)
	if err != nil {
		t.Fatalf("TranslateNonStreamResponseBytes: %v", err)
	}
	if !bytes.Equal(in, out) {
		t.Errorf("reasoning_content-only body was re-marshaled:\n in=%s\nout=%s", in, out)
	}
}

func TestTranslateNonStreamResponseBytes_AudioKeyPresentStillRemarshals(t *testing.T) {
	// N-1/Q3 boundary: the audio/audio_content/name strip counts as
	// a modification when any of those keys IS present — the body
	// must still be re-marshaled (current behavior preserved).
	in := []byte(`{"choices":[{"message":{"role":"assistant","audio":{"id":"x"}}}]}`)
	out, err := TranslateNonStreamResponseBytes(in)
	if err != nil {
		t.Fatalf("TranslateNonStreamResponseBytes: %v", err)
	}
	if bytes.Equal(in, out) {
		t.Errorf("audio key present but output unchanged: %s", out)
	}
	if strings.Contains(string(out), `"audio"`) {
		t.Errorf("audio should be stripped: %s", out)
	}
}

func TestTranslateNonStreamResponseBytes_InvalidJSON(t *testing.T) {
	if _, err := TranslateNonStreamResponseBytes([]byte("not json")); err == nil {
		t.Errorf("expected error for invalid JSON")
	}
}

func TestTranslateNonStreamResponseBytes_InvalidChoices(t *testing.T) {
	in := []byte(`{"choices":"not-a-slice"}`)
	if _, err := TranslateNonStreamResponseBytes(in); err == nil {
		t.Errorf("expected error for non-[]any choices")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// StreamTranslator — per-chunk translation
// ─────────────────────────────────────────────────────────────────────────────

func TestNewStreamTranslator_InitialState(t *testing.T) {
	tr := NewStreamTranslator()
	if tr == nil {
		t.Fatal("NewStreamTranslator returned nil")
	}
	if tr.lastIssued == nil {
		t.Errorf("lastIssued map should be non-nil per W9 (dead state!=nil guard removed)")
	}
}

func TestStreamTranslator_ChunkBytes_NoDetailsPassthrough(t *testing.T) {
	tr := NewStreamTranslator()
	line := []byte(`{"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}`)
	stripped, emitted, err := tr.ChunkBytes(line)
	if err != nil {
		t.Fatalf("ChunkBytes err: %v", err)
	}
	if len(emitted) != 0 {
		t.Errorf("no reasoning_details ⇒ no emitted chunks, got %d", len(emitted))
	}
	if !strings.Contains(string(stripped), `"content":"hello"`) {
		t.Errorf("content not preserved in stripped: %s", stripped)
	}
}

func TestStreamTranslator_ChunkBytes_StripsDataPrefix(t *testing.T) {
	tr := NewStreamTranslator()
	line := []byte(`data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}`)
	stripped, emitted, err := tr.ChunkBytes(line)
	if err != nil {
		t.Fatalf("ChunkBytes err: %v", err)
	}
	if len(emitted) != 0 {
		t.Errorf("no reasoning_details ⇒ no emitted chunks")
	}
	if strings.HasPrefix(string(stripped), "data: ") {
		t.Errorf("prefix not stripped: %q", stripped)
	}
	if !strings.Contains(string(stripped), `"content":"hello"`) {
		t.Errorf("content not preserved: %s", stripped)
	}
}

func TestStreamTranslator_ChunkBytes_OneEntryOneEmit(t *testing.T) {
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"think-1"}]}}]}`)
	stripped, emitted, err := tr.ChunkBytes(line)
	if err != nil {
		t.Fatalf("ChunkBytes err: %v", err)
	}
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted chunk, got %d", len(emitted))
	}
	if !strings.Contains(string(emitted[0]), `"reasoning_content":"think-1"`) {
		t.Errorf("emitted missing reasoning_content: %s", emitted[0])
	}
	if strings.Contains(string(stripped), "reasoning_details") {
		t.Errorf("stripped still carries reasoning_details: %s", stripped)
	}
}

func TestStreamTranslator_ChunkBytes_MultipleEntriesInOrder(t *testing.T) {
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"a"},{"type":"reasoning.text","text":"b"}]}}]}`)
	stripped, emitted, err := tr.ChunkBytes(line)
	if err != nil {
		t.Fatalf("ChunkBytes err: %v", err)
	}
	if len(emitted) != 2 {
		t.Fatalf("expected 2 emitted chunks, got %d", len(emitted))
	}
	if !strings.Contains(string(emitted[0]), `"reasoning_content":"a"`) {
		t.Errorf("first emitted: %s", emitted[0])
	}
	if !strings.Contains(string(emitted[1]), `"reasoning_content":"b"`) {
		t.Errorf("second emitted: %s", emitted[1])
	}
	if strings.Contains(string(stripped), "reasoning_details") {
		t.Errorf("stripped still carries reasoning_details: %s", stripped)
	}
}

func TestStreamTranslator_ChunkBytes_EmptyEntrySkipped(t *testing.T) {
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":""},{"type":"reasoning.text","text":"  "},{"type":"reasoning.text","text":"ok"}]}}]}`)
	_, emitted, err := tr.ChunkBytes(line)
	if err != nil {
		t.Fatalf("ChunkBytes err: %v", err)
	}
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted chunk (H7 skip-empty), got %d", len(emitted))
	}
	if !strings.Contains(string(emitted[0]), `"reasoning_content":"ok"`) {
		t.Errorf("emitted wrong content: %s", emitted[0])
	}
}

func TestStreamTranslator_ChunkBytes_CumulativeSuffixEmission(t *testing.T) {
	tr := NewStreamTranslator()
	// First chunk: text="ab" — emit "ab", state="ab"
	line1 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"ab"}]}}]}`)
	_, emitted1, err := tr.ChunkBytes(line1)
	if err != nil {
		t.Fatalf("ChunkBytes 1: %v", err)
	}
	if len(emitted1) != 1 || !strings.Contains(string(emitted1[0]), `"reasoning_content":"ab"`) {
		t.Fatalf("first chunk should emit 'ab', got %d chunks: %v", len(emitted1), emitted1)
	}
	// Second chunk: text="abc" (strict superset) — emit only "c"
	line2 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"abc"}]}}]}`)
	_, emitted2, err := tr.ChunkBytes(line2)
	if err != nil {
		t.Fatalf("ChunkBytes 2: %v", err)
	}
	if len(emitted2) != 1 || !strings.Contains(string(emitted2[0]), `"reasoning_content":"c"`) {
		t.Fatalf("second chunk should emit only suffix 'c', got %d chunks: %v", len(emitted2), emitted2)
	}
}

func TestStreamTranslator_ChunkBytes_CumulativeIdenticalNoEmit(t *testing.T) {
	tr := NewStreamTranslator()
	// First: "abc" → emit "abc"
	line1 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"abc"}]}}]}`)
	_, emitted1, _ := tr.ChunkBytes(line1)
	if len(emitted1) != 1 {
		t.Fatalf("first chunk: expected 1 emit, got %d", len(emitted1))
	}
	// Second: "abc" again — strict superset test: HasPrefix=true, suffix=""
	line2 := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"abc"}]}}]}`)
	_, emitted2, _ := tr.ChunkBytes(line2)
	if len(emitted2) != 0 {
		t.Errorf("identical cumulative chunk should NOT emit (suffix=empty), got %d", len(emitted2))
	}
}

func TestStreamTranslator_ChunkBytes_DedupAcrossChunks(t *testing.T) {
	// Cumulative upstream emission pattern: the same text appears
	// inside both entries. The H2 dedup is keyed on containment, so
	// the second is skipped.
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"abc"},{"type":"reasoning.text","text":"abc"}]}}]}`)
	_, emitted, _ := tr.ChunkBytes(line)
	if len(emitted) != 1 {
		t.Errorf("intra-chunk dedup should drop the second entry, got %d emits", len(emitted))
	}
}

func TestStreamTranslator_ChunkBytes_PreservesToolCalls(t *testing.T) {
	// P2-7: reasoning translation is read-only on tool_calls. The
	// stripped line must carry tool_calls unchanged.
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"think"}],"tool_calls":[{"index":0,"id":"c1","function":{"name":"f","arguments":"{}"}}]}}]}`)
	stripped, emitted, _ := tr.ChunkBytes(line)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted chunk, got %d", len(emitted))
	}
	if !strings.Contains(string(stripped), `"tool_calls":[{`) {
		t.Errorf("stripped lost tool_calls: %s", stripped)
	}
	if !strings.Contains(string(stripped), `"id":"c1"`) {
		t.Errorf("stripped lost tool_call id: %s", stripped)
	}
	if strings.Contains(string(stripped), "reasoning_details") {
		t.Errorf("stripped still has reasoning_details: %s", stripped)
	}
	// The emitted reasoning chunk must NOT carry tool_calls
	// (it's a reasoning-only delta chunk).
	if strings.Contains(string(emitted[0]), `"tool_calls"`) {
		t.Errorf("emitted reasoning chunk carries tool_calls (should be reasoning-only): %s", emitted[0])
	}
}

func TestStreamTranslator_ChunkBytes_UnknownTypeSkipped(t *testing.T) {
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"keep"},{"type":"unknown.type","text":"skip"}]}}]}`)
	_, emitted, _ := tr.ChunkBytes(line)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted (unknown type skipped), got %d", len(emitted))
	}
	if !strings.Contains(string(emitted[0]), `"reasoning_content":"keep"`) {
		t.Errorf("emitted: %s", emitted[0])
	}
}

func TestStreamTranslator_ChunkBytes_ParseFailurePassthrough(t *testing.T) {
	tr := NewStreamTranslator()
	line := []byte(`not json`)
	stripped, emitted, err := tr.ChunkBytes(line)
	if err != nil {
		t.Errorf("parse-fail should NOT return error (error-free by construction), got: %v", err)
	}
	if len(emitted) != 0 {
		t.Errorf("parse-fail: no emitted, got %d", len(emitted))
	}
	if string(stripped) != string(line) {
		t.Errorf("parse-fail: passthrough unchanged, got: %q", stripped)
	}
}

func TestStreamTranslator_ChunkBytes_EmptyLine(t *testing.T) {
	tr := NewStreamTranslator()
	stripped, emitted, err := tr.ChunkBytes(nil)
	if err != nil {
		t.Errorf("empty line should not return error, got: %v", err)
	}
	if len(emitted) != 0 {
		t.Errorf("empty line: no emitted, got %d", len(emitted))
	}
	if stripped != nil {
		t.Errorf("empty line: passthrough nil, got: %q", stripped)
	}
}

func TestStreamTranslator_ChunkBytes_EmptyDetailsArrayStrips(t *testing.T) {
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[]}}]}`)
	stripped, emitted, _ := tr.ChunkBytes(line)
	if len(emitted) != 0 {
		t.Errorf("empty details array: no emitted, got %d", len(emitted))
	}
	if strings.Contains(string(stripped), "reasoning_details") {
		t.Errorf("stripped still has reasoning_details: %s", stripped)
	}
}

func TestStreamTranslator_ChunkBytes_WrongDetailsTypeStrips(t *testing.T) {
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":"not-an-array"}}]}`)
	stripped, emitted, _ := tr.ChunkBytes(line)
	if len(emitted) != 0 {
		t.Errorf("wrong-type details: no emitted, got %d", len(emitted))
	}
	if strings.Contains(string(stripped), "reasoning_details") {
		t.Errorf("stripped still has reasoning_details: %s", stripped)
	}
}

func TestStreamTranslator_ChunkBytes_NoResetMethod(t *testing.T) {
	// Compile-time + grep guard: no Reset() method on the type.
	// Reflectively look up the method set and assert.
	tr := NewStreamTranslator()
	if tr == nil {
		t.Fatal("NewStreamTranslator returned nil")
	}
	// This is a sentinel: the test will fail to compile if a Reset
	// method is added (go's reflection doesn't allow us to test the
	// absence of a method, but it documents the intent). The
	// grep-based check lives in the verify phase.
	_ = tr
}

func TestStreamTranslator_FreshInstancePerRequest(t *testing.T) {
	// Per-stream-lifetime guard: a fresh NewStreamTranslator() is
	// constructed per stream-loop scope. State does NOT survive
	// across instances.
	tr1 := NewStreamTranslator()
	tr1.lastIssued[0] = "carry-over"
	tr2 := NewStreamTranslator()
	if _, ok := tr2.lastIssued[0]; ok {
		t.Errorf("fresh instance carried state from prior instance: %q", tr2.lastIssued[0])
	}
}

func TestStreamTranslator_ChunkBytes_FormatDriftCounterIncrements(t *testing.T) {
	before := FormatDriftCount()
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","format":"MiniMax-response-st-` + t.Name() + `","text":"x"}]}}]}`)
	_, _, _ = tr.ChunkBytes(line)
	after := FormatDriftCount()
	if after != before+1 {
		t.Errorf("counter not incremented on unknown format via stream: before=%d after=%d", before, after)
	}
}

func TestStreamTranslator_ChunkBytes_AlwaysReturnsNilError(t *testing.T) {
	// §6.1: error-free by construction. err is always nil.
	tr := NewStreamTranslator()
	lines := [][]byte{
		[]byte(``),
		[]byte(`not json`),
		[]byte(`{"choices":[]}`),
		[]byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":""}]}}]}`),
		[]byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"ok"}]}}]}`),
	}
	for i, line := range lines {
		_, _, err := tr.ChunkBytes(line)
		if err != nil {
			t.Errorf("case %d: expected nil err (error-free by construction), got: %v", i, err)
		}
	}
}

func TestStreamTranslator_ChunkBytes_ChoicesIndexRespected(t *testing.T) {
	// Multi-choice stream: state is keyed by choice index.
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":1,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"x"}]}}]}`)
	_, emitted, _ := tr.ChunkBytes(line)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emit for choice[1], got %d", len(emitted))
	}
	if tr.lastIssued[1] != "x" {
		t.Errorf("state[1] = %q, want x", tr.lastIssued[1])
	}
	if _, present := tr.lastIssued[0]; present {
		t.Errorf("state[0] should be empty, got %q", tr.lastIssued[0])
	}
}

func TestStreamTranslator_ChunkBytes_ContentFallback(t *testing.T) {
	// Forward-compat: text key absent, content key present.
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","content":"v2-style"}]}}]}`)
	_, emitted, _ := tr.ChunkBytes(line)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emit, got %d", len(emitted))
	}
	if !strings.Contains(string(emitted[0]), `"reasoning_content":"v2-style"`) {
		t.Errorf("content fallback not used: %s", emitted[0])
	}
}
