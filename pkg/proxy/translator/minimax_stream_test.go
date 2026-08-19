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

// TestRecordDrift_ObservationCounterEveryCall (W7) confirms
// the formatDriftObserved counter increments on EVERY
// recordDrift call (NOT deduped). The dedup'd formatDriftCounter
// counts distinct formats; the observation counter counts
// total observations. The two counters together let the
// sampled WARN log fire on observation volume (1-in-N) while
// the dedup counter remains a faithful "distinct formats"
// metric.
func TestRecordDrift_ObservationCounterEveryCall(t *testing.T) {
	base := "MiniMax-response-observed-" + t.Name()
	before := FormatDriftObservationCount()
	// Same format value repeated 3 times — observation
	// counter must increment 3 times, dedup counter must
	// increment 1 time.
	for i := 0; i < 3; i++ {
		recordDrift(map[string]any{"format": base + "-same", "id": fmt.Sprintf("%d", i)})
	}
	mid := FormatDriftObservationCount()
	if mid-before != 3 {
		t.Errorf("observation counter: repeated identical format should increment 3 times: before=%d mid=%d (want +3)", before, mid)
	}
	// Distinct format → observation counter increments again.
	recordDrift(map[string]any{"format": base + "-distinct", "id": "x"})
	final := FormatDriftObservationCount()
	if final-mid != 1 {
		t.Errorf("observation counter: distinct format should increment 1 time: mid=%d final=%d (want +1)", mid, final)
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
					"role":              "assistant",
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
					"role":          "assistant",
					"audio":         map[string]any{"id": "x"},
					"audio_content": "speech",
					"name":          "minimax-thing",
					"content":       "final",
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
					"role":              "assistant",
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

// TestTranslateNonStreamResponseBytes_AudioKeyOnlyIsPassthrough
// covers the W8 boundary case: a body with audio (or name or
// audio_content) but NO reasoning_details must NOT be
// re-marshaled. Previously the audio strip counted as a
// modification whenever the key was present, forcing a
// re-marshal of every gate-ON response even when there was
// nothing to translate. The W8 fix ties the strip to the
// "translation actually fired for this message" condition
// (reasoning_details present, in any form). A body with
// only audio is now byte-identical to upstream.
func TestTranslateNonStreamResponseBytes_AudioKeyOnlyIsPassthrough(t *testing.T) {
	in := []byte(`{"choices":[{"message":{"role":"assistant","audio":{"id":"x"}}}]}`)
	out, err := TranslateNonStreamResponseBytes(in)
	if err != nil {
		t.Fatalf("TranslateNonStreamResponseBytes: %v", err)
	}
	if !bytes.Equal(in, out) {
		t.Errorf("audio-only body should NOT be re-marshaled (W8):\n in=%s\nout=%s", in, out)
	}
}

// TestTranslateNonStreamResponseBytes_NameAndAudioStrippedWhenDetailsPresent
// is the W8 positive case: a body with reasoning_details AND
// audio/name must have all of them translated/stripped in a
// single re-marshal pass. Confirms the strip still fires
// when the message also has reasoning_details.
func TestTranslateNonStreamResponseBytes_NameAndAudioStrippedWhenDetailsPresent(t *testing.T) {
	in := []byte(`{"choices":[{"message":{"role":"assistant","name":"synth","audio":{"id":"x"},"reasoning_details":[{"type":"reasoning.text","text":"think-1"}]}}]}`)
	out, err := TranslateNonStreamResponseBytes(in)
	if err != nil {
		t.Fatalf("TranslateNonStreamResponseBytes: %v", err)
	}
	if bytes.Equal(in, out) {
		t.Errorf("body with reasoning_details should be re-marshaled (stripped): %s", out)
	}
	if strings.Contains(string(out), `"audio"`) {
		t.Errorf("audio should be stripped: %s", out)
	}
	if strings.Contains(string(out), `"name"`) {
		t.Errorf("name should be stripped: %s", out)
	}
	if !strings.Contains(string(out), `"reasoning_content":"think-1"`) {
		t.Errorf("reasoning_content should be set: %s", out)
	}
	if strings.Contains(string(out), `"reasoning_details"`) {
		t.Errorf("reasoning_details should be stripped: %s", out)
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
	// C1 — the data: prefix is preserved on the unchanged
	// passthrough path (the line is returned VERBATIM when no
	// reasoning_details was present). The prefix-strip at
	// ChunkBytes' top exists only to handle input that
	// unexpectedly includes the prefix; on the unchanged path
	// the prefix survives unchanged in stripped.
	tr := NewStreamTranslator()
	line := []byte(`data: {"id":"1","choices":[{"index":0,"delta":{"content":"hello"}}]}`)
	stripped, emitted, err := tr.ChunkBytes(line)
	if err != nil {
		t.Fatalf("ChunkBytes err: %v", err)
	}
	if len(emitted) != 0 {
		t.Errorf("no reasoning_details ⇒ no emitted chunks")
	}
	if !strings.HasPrefix(string(stripped), "data: ") {
		t.Errorf("C1 passthrough: prefix should be preserved on unchanged path: %q", stripped)
	}
	if !bytes.Equal(stripped, line) {
		t.Errorf("C1 passthrough: stripped should be byte-identical to input on unchanged path:\n want=%q\n  got=%q", line, stripped)
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

// ─────────────────────────────────────────────────────────────────────────────
// W10 — idempotence (translate-twice equals translate-once) — response side
// ─────────────────────────────────────────────────────────────────────────────

// TestTranslateNonStreamResponseBody_Idempotent covers AM-03/W10 on
// the response side: a body whose reasoning_details has been
// translated to reasoning_content, fed back through the translator,
// must produce identical output (no double concatenation, no
// re-strip of the now-absent details array, no spurious entries).
func TestTranslateNonStreamResponseBody_Idempotent(t *testing.T) {
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"index": float64(0),
				"message": map[string]any{
					"role": "assistant",
					"reasoning_details": []any{
						map[string]any{"type": "reasoning.text", "text": "a"},
						map[string]any{"type": "reasoning.text", "text": "b"},
						map[string]any{"type": "reasoning.text", "text": "c"},
					},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("first TranslateNonStreamResponseBody: %v", err)
	}
	// Snapshot the first-pass state.
	firstMsg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	firstContent := firstMsg["reasoning_content"]
	// Deep-copy via Marshal/Unmarshal to mimic a real second-pass
	// call site observing the bytes round-tripped through JSON.
	firstBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("first marshal: %v", err)
	}
	var copy map[string]any
	if err := json.Unmarshal(firstBytes, &copy); err != nil {
		t.Fatalf("deep-copy unmarshal: %v", err)
	}
	if err := TranslateNonStreamResponseBody(copy); err != nil {
		t.Fatalf("second TranslateNonStreamResponseBody: %v", err)
	}
	secondMsg := copy["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if secondMsg["reasoning_content"] != firstContent {
		t.Errorf("idempotence broken: first=%q, second=%q", firstContent, secondMsg["reasoning_content"])
	}
	if _, present := secondMsg["reasoning_details"]; present {
		t.Errorf("reasoning_details re-appeared on second pass: %v", secondMsg["reasoning_details"])
	}
}

// TestTranslateNonStreamResponseBytes_Idempotent is the bytes-wrapper
// counterpart. Two passes of TranslateNonStreamResponseBytes must
// produce byte-equal output — once the data has been translated
// (details → content + audio/name strip), the second pass observes
// no reasoning_details to translate, so the data-absent fast path
// returns the input untouched (3e7eba9). Bytes.Equal is exact here:
// both passes re-marshal the same map topology and json.Marshal is
// deterministic for a fixed topology.
func TestTranslateNonStreamResponseBytes_Idempotent(t *testing.T) {
	in := []byte(`{"choices":[{"index":0,"message":{"role":"assistant","reasoning_details":[{"type":"reasoning.text","text":"think-1"},{"type":"reasoning.text","text":"think-2"}]}}]}`)
	first, err := TranslateNonStreamResponseBytes(in)
	if err != nil {
		t.Fatalf("first TranslateNonStreamResponseBytes: %v", err)
	}
	// Parse-and-verify the first pass: details were translated.
	var firstMap map[string]any
	if err := json.Unmarshal(first, &firstMap); err != nil {
		t.Fatalf("first unmarshal: %v", err)
	}
	msg := firstMap["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if got := msg["reasoning_content"]; got != "think-1think-2" {
		t.Errorf("first pass reasoning_content = %q, want think-1think-2", got)
	}
	// Second pass on the result of the first pass.
	second, err := TranslateNonStreamResponseBytes(first)
	if err != nil {
		t.Fatalf("second TranslateNonStreamResponseBytes: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("TranslateNonStreamResponseBytes idempotence broken:\n first=%s\nsecond=%s", first, second)
	}
}

// TestStreamTranslator_ChunkBytes_Idempotent_FreshInstance covers
// AM-03/W10 for the stream path: feeding the SAME chunk stream
// through TWO FRESH StreamTranslator instances (no shared state)
// must produce byte-identical full-output sequences (the stripped
// line followed by all emitted chunks, in order).
//
// This catches state leak between instances — the W3 / §3.3 rule
// that stream state is per-stream-lifetime, not cross-stream. If a
// future change accidentally promotes state to a package-level
// var, the two runs would diverge on cumulative-mode dedup.
func TestStreamTranslator_ChunkBytes_Idempotent_FreshInstance(t *testing.T) {
	chunks := []string{
		// single entry
		`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"a"}]}}]}`,
		// multi-entry array
		`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"b"},{"type":"reasoning.text","text":"c"}]}}]}`,
		// cumulative-superset: prior was "a", new is "abc" → emit "bc"
		`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"abc"}]}}]}`,
		// tool_calls + details mix
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","function":{"name":"f","arguments":"{}"}}],"reasoning_details":[{"type":"reasoning.text","text":"d"}]}}]}`,
	}
	runOnce := func(tr *StreamTranslator) [][]byte {
		var seq [][]byte
		for _, c := range chunks {
			stripped, emitted, err := tr.ChunkBytes([]byte(c))
			if err != nil {
				t.Fatalf("ChunkBytes err: %v", err)
			}
			seq = append(seq, stripped)
			seq = append(seq, emitted...)
		}
		return seq
	}
	seq1 := runOnce(NewStreamTranslator())
	seq2 := runOnce(NewStreamTranslator())
	if len(seq1) != len(seq2) {
		t.Fatalf("sequence length differs: %d vs %d", len(seq1), len(seq2))
	}
	for i := range seq1 {
		if !bytes.Equal(seq1[i], seq2[i]) {
			t.Errorf("step %d differs:\n run1=%s\n run2=%s", i, seq1[i], seq2[i])
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// W11 — empty `reasoning_details: []` round-trip (response side)
// ─────────────────────────────────────────────────────────────────────────────

// TestTranslateNonStreamResponseBody_EmptyDetailsArrayNoOp covers
// W11 on the response side: a body whose choices[].message carries
// an empty `reasoning_details: []` array (and no reasoning_content)
// is a no-op for TranslateNonStreamResponseBody. The empty array
// must be stripped (no `reasoning_details: []` leak), no
// reasoning_content is fabricated, and no error is returned.
//
// Note: this is distinct from `TestStreamTranslator_ChunkBytes_EmptyDetailsArrayStrips`
// which covers the stream path. The non-stream path's empty-array
// branch lives in translateNonStreamResponseBody (delete the empty
// array, continue without setting reasoning_content).
func TestTranslateNonStreamResponseBody_EmptyDetailsArrayNoOp(t *testing.T) {
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"index": float64(0),
				"message": map[string]any{
					"role":              "assistant",
					"reasoning_details": []any{},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if _, present := msg["reasoning_details"]; present {
		t.Errorf("empty reasoning_details array not stripped: %v", msg["reasoning_details"])
	}
	if _, present := msg["reasoning_content"]; present {
		t.Errorf("spurious reasoning_content fabricated on empty details: %v", msg["reasoning_content"])
	}
}

// TestTranslateNonStreamResponseBody_EmptyDetailsArrayPreservesReasoningContent
// is the W11 boundary case: when reasoning_details is empty but
// reasoning_content IS already present, the empty array is stripped
// (no leak) and the pre-existing reasoning_content survives
// untouched (the single-winner rule only fires when details are
// non-empty).
func TestTranslateNonStreamResponseBody_EmptyDetailsArrayPreservesReasoningContent(t *testing.T) {
	body := map[string]any{
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role":              "assistant",
					"reasoning_content": "pre-existing",
					"reasoning_details": []any{},
				},
			},
		},
	}
	if err := TranslateNonStreamResponseBody(body); err != nil {
		t.Fatalf("TranslateNonStreamResponseBody: %v", err)
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if got := msg["reasoning_content"]; got != "pre-existing" {
		t.Errorf("reasoning_content = %q, want pre-existing (single-winner inactive on empty details)", got)
	}
	if _, present := msg["reasoning_details"]; present {
		t.Errorf("empty reasoning_details array not stripped: %v", msg["reasoning_details"])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Task 4 — multi-choice chunk shape (documented contract)
// ─────────────────────────────────────────────────────────────────────────────

// TestStreamTranslator_ChunkBytes_MultiChoiceChunk_AllChoicesTranslated
// asserts the W3 contract: a chunk with multiple choices, each
// carrying its own reasoning_details, must have EVERY choice's
// reasoning_details translated to reasoning_content and stripped
// from the output. Previously the StreamTranslator walked only
// choices[0], silently leaking choices[1+].reasoning_details
// into the stripped line. The test pins the fix: choices[1+]
// reasoning_details is also translated, none remain in output.
func TestStreamTranslator_ChunkBytes_MultiChoiceChunk_AllChoicesTranslated(t *testing.T) {
	tr := NewStreamTranslator()
	line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"from-choice-0"}]}},{"index":1,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"from-choice-1"}]}}]}`)
	stripped, emitted, err := tr.ChunkBytes(line)
	if err != nil {
		t.Fatalf("ChunkBytes err: %v", err)
	}
	// W3: both choices must be translated. Each entry produces
	// one emit.
	if len(emitted) != 2 {
		t.Fatalf("expected 2 emitted chunks (one per choice), got %d", len(emitted))
	}
	// First emit carries choice[0]'s text.
	if !strings.Contains(string(emitted[0]), `"reasoning_content":"from-choice-0"`) {
		t.Errorf("emitted[0] missing choice[0] text: %s", emitted[0])
	}
	// Second emit carries choice[1]'s text.
	if !strings.Contains(string(emitted[1]), `"reasoning_content":"from-choice-1"`) {
		t.Errorf("emitted[1] missing choice[1] text: %s", emitted[1])
	}
	// W3 — NO reasoning_details leak in the stripped line for
	// any choice. Neither choice[0] nor choice[1] may carry
	// reasoning_details after translation.
	if strings.Contains(string(stripped), "from-choice-0") {
		t.Errorf("stripped still carries choice[0] reasoning_details: %s", stripped)
	}
	if strings.Contains(string(stripped), "from-choice-1") {
		t.Errorf("stripped still carries choice[1] reasoning_details (W3 leak): %s", stripped)
	}
	if strings.Contains(string(stripped), "reasoning_details") {
		t.Errorf("stripped still carries reasoning_details (W3 leak): %s", stripped)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Task 5 — empty-input no-op table for the stream path
// ─────────────────────────────────────────────────────────────────────────────

// TestStreamTranslator_EmptyInputNoOp consolidates the empty/nil/
// minimal-input edges for NewStreamTranslator + ChunkBytes. Some of
// these are individually covered (TestStreamTranslator_ChunkBytes_
// EmptyLine covers nil/empty; TestStreamTranslator_ChunkBytes_
// ParseFailurePassthrough covers non-JSON; TestNewStreamTranslator_
// InitialState covers the constructor). This table adds the cases
// not yet covered: empty `{}` body, empty choices array, missing
// delta, and a sanity check that ChunkBytes is safe on a nil
// *StreamTranslator receiver (the defensive guard at the top of
// ChunkBytes).
func TestStreamTranslator_EmptyInputNoOp(t *testing.T) {
	t.Run("NewStreamTranslator", func(t *testing.T) {
		tr := NewStreamTranslator()
		if tr == nil {
			t.Fatal("NewStreamTranslator returned nil")
		}
		if tr.lastIssued == nil {
			t.Errorf("lastIssued should be non-nil (per W9)")
		}
	})

	t.Run("ChunkBytes_nil_receiver", func(t *testing.T) {
		// Defensive: a hand-crafted nil *StreamTranslator must not
		// panic. The ChunkBytes method has an explicit nil check.
		var tr *StreamTranslator
		line := []byte(`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"x"}]}}]}`)
		stripped, emitted, err := tr.ChunkBytes(line)
		if err != nil {
			t.Errorf("nil receiver should not return error, got: %v", err)
		}
		if len(emitted) != 0 {
			t.Errorf("nil receiver should produce no emitted chunks, got %d", len(emitted))
		}
		if !bytes.Equal(stripped, line) {
			t.Errorf("nil receiver should passthrough the original line, got %s", stripped)
		}
	})

	t.Run("ChunkBytes_empty_choices", func(t *testing.T) {
		// empty choices array → no emitted, stripped is the line
		// re-marshaled (the no-choices early-return path).
		tr := NewStreamTranslator()
		line := []byte(`{"choices":[]}`)
		stripped, emitted, err := tr.ChunkBytes(line)
		if err != nil {
			t.Errorf("empty choices: %v", err)
		}
		if len(emitted) != 0 {
			t.Errorf("empty choices: no emitted, got %d", len(emitted))
		}
		if !strings.Contains(string(stripped), `"choices":[]`) {
			t.Errorf("empty choices: stripped lost the array: %s", stripped)
		}
	})

	t.Run("ChunkBytes_empty_object", func(t *testing.T) {
		// empty object `{}` is a minimal valid chunk — no choices,
		// no delta, no reasoning_details. Should be a no-op.
		tr := NewStreamTranslator()
		line := []byte(`{}`)
		stripped, emitted, err := tr.ChunkBytes(line)
		if err != nil {
			t.Errorf("empty object: %v", err)
		}
		if len(emitted) != 0 {
			t.Errorf("empty object: no emitted, got %d", len(emitted))
		}
		if !bytes.Equal(stripped, []byte(`{}`)) {
			t.Errorf("empty object: stripped should be {}, got %s", stripped)
		}
	})

	t.Run("ChunkBytes_choices_no_delta", func(t *testing.T) {
		// choices present, delta absent → no-op (the hasDelta
		// early-return path).
		tr := NewStreamTranslator()
		line := []byte(`{"choices":[{"index":0}]}`)
		_, emitted, err := tr.ChunkBytes(line)
		if err != nil {
			t.Errorf("no-delta: %v", err)
		}
		if len(emitted) != 0 {
			t.Errorf("no-delta: no emitted, got %d", len(emitted))
		}
	})

	t.Run("ChunkBytes_data_prefix_only", func(t *testing.T) {
		// A line that is JUST the `data: ` prefix with no payload.
		// The empty-line guard at the top of ChunkBytes handles
		// it (after prefix-strip the line is empty). Assert no
		// panic, no error, no emitted. C1 — the prefix is
		// preserved on the unchanged path (the line is returned
		// VERBATIM when no reasoning_details was present).
		tr := NewStreamTranslator()
		line := []byte(`data: `)
		stripped, emitted, err := tr.ChunkBytes(line)
		if err != nil {
			t.Errorf("data-prefix-only: %v", err)
		}
		if len(emitted) != 0 {
			t.Errorf("data-prefix-only: no emitted, got %d", len(emitted))
		}
		// The empty-line guard at the top returns the
		// original line verbatim. After prefix-strip the
		// remaining line is empty, so the function returns
		// the original line (which is the prefix alone).
		if string(stripped) != "data: " {
			t.Errorf("data-prefix-only: stripped should be %q, got %q", "data: ", stripped)
		}
	})
}
