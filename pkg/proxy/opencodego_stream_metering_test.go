package proxy

import (
	"testing"
)

// =============================================================================
// F12 (opencode-go commission scope addition) — stream-side usage metering
// dedupe guard, verified against the VERBATIM R4 wire signature.
//
// The OpenCode Go surface emits usage TWICE per stream: on the combined
// finish chunk AND verbatim in a trailing usage-only chunk (choices:[]).
// OpenAI's stream_options.include_usage convention, by contrast, delivers
// usage ONLY in a trailing usage-only chunk. The guard in
// extractUsageFromSSEChunk is count-once-idempotent with LAST-WINS
// semantics: identical repeats are absorbed (no state change), genuinely
// new triples update the slot; usage-only chunks are never blanket-ignored.
// The meter reads exactly ONE final value per stream ⇒ ONE increment.
//
// Cross-ref: the typed-seam twin of this suite is the end-to-end
// verbatim-signature F12 test in pkg/providers/opencodego_test.go
// (TestOpencodeGo_F11_F12_Stream_VerbatimSignature — TotalTokens==66,
// not 132).
//
// Wire shapes below are quoted from .agents/tester/RESULTS/
// 2026-10-17-opencode-e2e-r4.md (chunks 15/16 verbatim). No live calls.
// =============================================================================

// r4MeterChunk15 is the verbatim combined finish chunk (finish_reason +
// last content delta + FULL usage).
const r4MeterChunk15 = `data: {"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[{"index":0,"finish_reason":"stop","delta":{"role":"assistant","content":"</system>","reasoning_content":""}}],"usage":{"prompt_tokens":38,"completion_tokens":28,"total_tokens":66,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":17}}}`

// r4MeterChunk16 is the verbatim trailing usage-only DUPLICATE
// (choices:[], usage identical to chunk 15).
const r4MeterChunk16 = `data: {"id":"021792228926870f0a583d75381f7c0a662383f3247915e52a469","object":"chat.completion.chunk","created":1792228927,"model":"deepseek-v4.1-flash","choices":[],"usage":{"prompt_tokens":38,"completion_tokens":28,"total_tokens":66,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":17}}}`

// TestExtractUsageFromSSEChunk_GoDuplicateCountedOnce — the verbatim Go
// duplicate pair through the metering extractor: the slot holds the
// single final triple (66 total), NOT an accumulation (132 would be the
// double-count signature).
func TestExtractUsageFromSSEChunk_GoDuplicateCountedOnce(t *testing.T) {
	req := &upstreamRequest{}
	extractUsageFromSSEChunk(req, []byte(r4MeterChunk15))
	extractUsageFromSSEChunk(req, []byte(r4MeterChunk16)) // verbatim duplicate

	got := req.GetUsage()
	if got == nil {
		t.Fatal("usage slot empty — the finish chunk's usage was not captured")
	}
	if got.PromptTokens != 38 || got.CompletionTokens != 28 || got.TotalTokens != 66 {
		t.Fatalf("ONE meter row per stream violated: got %+v, want {38 28 66}", got)
	}
	if got.TotalTokens == 132 {
		t.Fatal("DOUBLE-COUNT detected: duplicate usage chunk accumulated")
	}
}

// TestExtractUsageFromSSEChunk_LastWinsOnNewValues — last-wins semantics:
// a genuinely NEW triple (running estimate → final totals) replaces the
// earlier one; only exact repeats are absorbed.
func TestExtractUsageFromSSEChunk_LastWinsOnNewValues(t *testing.T) {
	req := &upstreamRequest{}
	extractUsageFromSSEChunk(req, []byte(`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	extractUsageFromSSEChunk(req, []byte(`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)) // absorbed
	extractUsageFromSSEChunk(req, []byte(`data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":8,"total_tokens":20}}`)) // new → wins

	got := req.GetUsage()
	if got == nil || got.TotalTokens != 20 {
		t.Fatalf("last-wins: got %+v, want total 20", got)
	}
}

// TestExtractUsageFromSSEChunk_IncludeUsageTrailingOnly — the
// non-regression property: OpenAI's include_usage shape delivers usage
// ONLY in the trailing usage-only chunk; it is the sole carrier and must
// meter exactly once (the guard absorbs DUPLICATES, it does not ignore
// usage-only chunks).
func TestExtractUsageFromSSEChunk_IncludeUsageTrailingOnly(t *testing.T) {
	req := &upstreamRequest{}
	// No finish-chunk usage at all; the trailing chunk is the only carrier.
	extractUsageFromSSEChunk(req, []byte(`data: {"choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":"stop"}],"usage":null}`))
	extractUsageFromSSEChunk(req, []byte(`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))

	got := req.GetUsage()
	if got == nil || got.PromptTokens != 10 || got.CompletionTokens != 5 || got.TotalTokens != 15 {
		t.Fatalf("include_usage shape must meter from the trailing usage-only chunk: got %+v", got)
	}
}

// TestExtractUsageFromSSEChunk_ZeroUsageIgnored — zero/absent usage must
// not pollute the slot (pre-existing behavior, pinned for the guard).
func TestExtractUsageFromSSEChunk_ZeroUsageIgnored(t *testing.T) {
	req := &upstreamRequest{}
	extractUsageFromSSEChunk(req, []byte(`data: {"choices":[{"index":0,"delta":{"role":"assistant"}}],"usage":null}`))
	if req.GetUsage() != nil {
		t.Fatalf("zero usage must not stamp the slot: got %+v", req.GetUsage())
	}
}

// TestExtractUsageFromChunk_TrailingUsageOnlyChunk — the client-facing
// completion extractor (handler.go reverse-scan → meter) must parse the
// verbatim usage-only chunk shape (choices:[]) — that is the chunk the
// reverse scan lands on for Go-surface streams, and it carries the same
// triple as the finish chunk (idempotent either way).
func TestExtractUsageFromChunk_TrailingUsageOnlyChunk(t *testing.T) {
	usage := extractUsageFromChunk([]byte(r4MeterChunk16[len("data: "):]))
	if usage == nil {
		t.Fatal("usage-only chunk (choices:[]) not parsed by the meter extractor")
	}
	if usage.PromptTokens != 38 || usage.CompletionTokens != 28 || usage.TotalTokens != 66 {
		t.Fatalf("verbatim R4 chunk-16 usage: got %+v, want {38 28 66}", usage)
	}
}
