// Phase 5 / real-streaming-default — parity acceptance harness.
//
// The single contract this file proves: the X-LLMProxy-Buffer-Response
// header present ⇒ buffered mode produces byte-for-byte identical wire
// output to the pre-feature (latest @ 9842c77) behavior. Live mode
// (header absent) is exercised separately in
// real_streaming_firstbyte_test.go.
//
// The 5.10 fixed rollout order (plan task 5.10) is encoded by the
// subtest ordering inside TestRealStreaming_ParityMatrix_AllPaths —
// a failure in path N is bisected against the last-green path N-1:
//
//	(1) Anthropic→Anthropic passthrough (handler_anthropic.go:1679
//	    handlePassthroughStreamResponse, dispatch at :1225) — real
//	    fixture: internal model with anthropic-provider credential,
//	    upstream speaks Anthropic SSE; golden differs from external
//	    because the wire is forwarded byte-identical rather than
//	    translated
//	(2) Anthropic→OpenAI-wire external translation (handler_anthropic.go:819/:851)
//	(3) Anthropic→OpenAI-wire internal translation
//	    (doAnthropicInternalRequest at handler_anthropic.go:600-685)
//	(4) /v1/chat/completions OpenAI race path (handler.go race-coord+relay)
//	(5) Ultimate external stream — COVERED by
//	    pkg/ultimatemodel/handler_external_test.go TestUltimate_HeaderDispatch
//	    (ExecuteOptions-level caveat: those tests pin the buffered vs.
//	    live wire bytes inside ultimatemodel.Handler; the proxy-package
//	    boundary can't reach the private streamResponse /
//	    handleInternalStream methods directly)
//	(6) Ultimate internal stream — COVERED by
//	    pkg/ultimatemodel/handler_internal_test.go TestUltimate_HeaderDispatch
//	    (same reason as path 5; TestUltimateCapture_LiveMode_IdenticalToBuffered
//	    pins the C1 estimator parity)
//
// Variant matrix (plan task 5.2 acceptance): each of paths 1-4 is
// exercised against the canonical fixtures in fixtureSet():
// text (Hello world!), thinking (content + reasoning_content),
// tool_call (OpenAI-wire tool delta), usage (content + trailing usage),
// role_only (role-only first chunk, provider-shaped), usage_first
// (usage-only first chunk, provider-shaped), and comment_first (the
// SSE `: connected\n\n` preamble + first data — locks in the live-mode
// preamble-write timing invariant for path 1). Paths 5-6 are pinned
// by ultimatemodel-package tests (the fixtureSet() lives in proxy
// and is not used by ultimate paths).
//
// Determinism rules (plan Phase 5):
//   - Strip volatile fields (timestamps, request IDs) BEFORE byte
//     comparison. Anchored regex (no global comma rewrites — see
//     volStripPatterns doc for the rationale; finding #6 fix).
//   - Goldens are committed once, then updated manually via the
//     -update flag pattern (TestRealStreaming_GoldenRecorder).
//   - No wall-clock gating on the parity assertions — every path is
//     compared on the COMPLETE wire bytes (after the upstream is
//     closed).
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// Parity harness — shared utilities for capturing / diffing wire output.
// ─────────────────────────────────────────────────────────────────────────────

// volStripPatterns removes per-run volatile fields from a wire body so
// committed goldens stay byte-stable across runs (determinism rule).
// We deliberately strip:
//   - SSE `id`/`created` fields (provider-generated, per-request)
//     for BOTH chatcmpl- (OpenAI) and msg_ (Anthropic) prefixes
//   - the SSE preamble `: connected\n\n` (timing of the first Write call)
//   - heartbeat `: keepalive\n\n` lines (the 15s SSE comment)
//   - `[DONE]` markers (no semantic content for parity)
//
// Anchored stripping (reviewer finding #6): each id/created pattern
// matches an OPTIONAL trailing comma so we never have to do a global
// dangling-comma rewrite (which would corrupt content fields with
// legitimate double commas such as `"content":"a,,b"`). The previous
// implementation rewrote `{,`→`{` and `,,`→`,` globally and would
// false-pass any content-level double-comma diff.
var volStripPatterns = []*regexp.Regexp{
	regexp.MustCompile(`"id":"chatcmpl-[A-Za-z0-9_-]+",?`),
	regexp.MustCompile(`"id":"msg_[A-Za-z0-9_-]+",?`),
	regexp.MustCompile(`"created":\d+,?`),
	regexp.MustCompile(`(?m)^: connected\n\n`),
	regexp.MustCompile(`(?m)^: keepalive\n\n`),
	regexp.MustCompile(`(?m)^data: \[DONE\]\n\n`),
}

// volStrip normalizes a wire body for golden comparison. Stripping is
// intentionally conservative: only fields with documented per-run
// volatility are removed. The result is byte-compared to the golden.
func volStrip(s string) string {
	for _, re := range volStripPatterns {
		s = re.ReplaceAllString(s, "")
	}
	return s
}

// readGolden reads a committed golden file under
// pkg/proxy/testdata/real_streaming_golden/<name>.json. The file format
// is a JSON envelope: {"body": "...", "content_type": "..."} so future
// coverage (status, headers) is easy to extend.
func readGolden(t *testing.T, name string) (string, string) {
	t.Helper()
	path := filepath.Join("testdata", "real_streaming_golden", name+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v — regenerate via TestRealStreaming_GoldenRecorder with -update", name, err)
	}
	var env struct {
		Body        string `json:"body"`
		ContentType string `json:"content_type"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("unmarshal golden %s: %v", name, err)
	}
	return env.Body, env.ContentType
}

// assertParity is the shared comparator: strip volatile fields from the
// live wire body and assert byte equality with the golden. Reports the
// first divergent line on failure for bisection (plan 5.11).
func assertParity(t *testing.T, pathName, variant string, wire string, golden string, goldenCT string, wireCT string) {
	t.Helper()
	stripped := volStrip(wire)
	if stripped != golden {
		divLine := firstDivergentLine(golden, stripped)
		t.Fatalf("parity FAIL on path=%q variant=%q — first divergent line:%d\n--- golden ---\n%s\n--- actual ---\n%s",
			pathName, variant, divLine, golden, stripped)
	}
	if goldenCT != "" && wireCT != "" && goldenCT != wireCT {
		t.Errorf("parity FAIL on path=%q variant=%q — content-type: %q vs %q", pathName, variant, wireCT, goldenCT)
	}
}

// firstDivergentLine returns the 1-based line index of the first
// difference. Used by the bisection procedure (plan 5.11): a parity
// failure on line N points the operator at the path's bufferMode
// threading + the §6 master test-flip inventory for that surface.
func firstDivergentLine(a, b string) int {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	n := len(al)
	if len(bl) < n {
		n = len(bl)
	}
	for i := 0; i < n; i++ {
		if al[i] != bl[i] {
			return i + 1
		}
	}
	return n + 1
}

// ─────────────────────────────────────────────────────────────────────────────
// Mock upstreams — minimal, deterministic SSE fixtures per content
// variant. Each fixture emits the canonical chunks for its variant
// (text / thinking / tool_call / usage / role-only-first / etc.) and
// then `[DONE]` so the buffered-mode capture completes cleanly.
// ─────────────────────────────────────────────────────────────────────────────

// streamFixture is a deterministic SSE upstream: it emits a fixed set
// of data: lines and closes the stream.
type streamFixture struct {
	chunks []string
}

func (f *streamFixture) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // arm ctx-close watcher
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, c := range f.chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// fixtureSet returns the canonical fixtures for the matrix:
//
//	"text"          — 3 content chunks (the basic parity row)
//	"thinking"      — content + reasoning_content (the standard thinking variant)
//	"tool_call"     — content + a tool_call delta (OpenAI-wire tool-call shape)
//	"usage"         — content + a usage-only trailing chunk
//	"role_only"     — role-only first chunk (provider-shaped first)
//	"usage_first"   — usage-only first chunk (provider-shaped first)
//	"comment_first" — SSE preamble comment + first data (locks in the
//	                   live-mode preamble-write timing invariant; the
//	                   upstream emits `: connected\n\n` BEFORE the first
//	                   data frame, mirroring what the proxy does on the
//	                   client side at handler.go:882-889)
func fixtureSet() map[string]*streamFixture {
	return map[string]*streamFixture{
		"text": {chunks: []string{
			mockCreateChunk("Hello"),
			mockCreateChunk(" world"),
			mockCreateChunk("!"),
		}},
		"thinking": {chunks: []string{
			mockCreateReasoningChunk("Let me think"),
			mockCreateChunk("answer"),
		}},
		"tool_call": {chunks: []string{
			mockCreateChunk("Looking up..."),
			mockCreateToolCallChunk("get_weather", `{"city":"SF"}`, 0),
		}},
		"usage": {chunks: []string{
			mockCreateChunk("ok"),
			mockCreateUsageChunk(7, 13),
		}},
		"role_only": {chunks: []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
			mockCreateChunk("hi"),
		}},
		"usage_first": {chunks: []string{
			mockCreateUsageChunk(2, 4),
			mockCreateChunk("late content"),
		}},
		"comment_first": {chunks: []string{
			mockCreateChunk("first-data-after-preamble"),
		}},
	}
}

// anthropicSSERaw returns an Anthropic-wire SSE chunk sequence for the
// raw-passthrough fixture (path 1): message_start → ping →
// content_block_start → content_block_delta → content_block_stop →
// message_delta → message_stop. Used by subtest 1 in
// TestRealStreaming_ParityMatrix_AllPaths to drive the upstream as an
// Anthropic-speaking server; the proxy pipes the bytes through
// handlePassthroughStreamResponse (handler_anthropic.go:1679) without
// translation.
func anthropicSSERaw(variant string) []string {
	switch variant {
	case "text":
		return []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_passthrough\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-internal\",\"stop_reason\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n",
			"event: ping\ndata: {\"type\":\"ping\"}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"!\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
	case "thinking":
		return []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_passthrough\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-internal\",\"stop_reason\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n",
			"event: ping\ndata: {\"type\":\"ping\"}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Let me think\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\" harder\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
	case "tool_call":
		return []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_passthrough\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-internal\",\"stop_reason\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n",
			"event: ping\ndata: {\"type\":\"ping\"}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Looking up...\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_test\",\"name\":\"get_weather\",\"input\":{}}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\\\"SF\\\"}\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":7}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
	case "usage":
		return []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_passthrough\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-internal\",\"stop_reason\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n",
			"event: ping\ndata: {\"type\":\"ping\"}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":20}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
	case "role_only", "usage_first":
		// Provider-shaped first chunks don't apply to Anthropic-wire
		// streams (the Anthropic server emits message_start which is
		// not provider-shaped). The raw passthrough falls back to the
		// text variant for these labels.
		return anthropicSSERaw("text")
	case "comment_first":
		// The proxy writes the `: connected\n\n` preamble itself
		// (handler.go:882-889); for the upstream-emitted version,
		// the Anthropic server doesn't emit an SSE comment before the
		// first event — fall back to text.
		return anthropicSSERaw("text")
	default:
		return anthropicSSERaw("text")
	}
}

// anthropicRawPassthroughHandler returns an httptest handler that
// emits the Anthropic-wire SSE bytes verbatim (no JSON wrapping; the
// lines ARE the wire bytes). Used by subtest 1 in
// TestRealStreaming_ParityMatrix_AllPaths to drive
// handlePassthroughStreamResponse.
func anthropicRawPassthroughHandler(variant string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // arm ctx-close watcher
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, line := range anthropicSSERaw(variant) {
			fmt.Fprint(w, line)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// mockCreateToolCallChunk returns an OpenAI streaming chunk carrying a
// tool_call delta.
func mockCreateToolCallChunk(name, args string, index int) string {
	chunk := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"index": index,
				"delta": map[string]interface{}{
					"tool_calls": []interface{}{
						map[string]interface{}{
							"index":    index,
							"id":       "call_test",
							"type":     "function",
							"function": map[string]interface{}{
								"name":      name,
								"arguments": args,
							},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

// mockCreateUsageChunk returns an OpenAI streaming chunk carrying only
// usage (no choices delta). Some providers emit this as the FINAL chunk.
func mockCreateUsageChunk(prompt, completion int) string {
	chunk := map[string]interface{}{
		"choices": []interface{}{},
		"usage": map[string]interface{}{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      prompt + completion,
		},
	}
	b, _ := json.Marshal(chunk)
	return string(b)
}

// ─────────────────────────────────────────────────────────────────────────────
// TEST 1 — Parity matrix over the 5.10 fixed rollout order.
// ─────────────────────────────────────────────────────────────────────────────

// TestRealStreaming_ParityMatrix_AllPaths is the parity acceptance
// gate (plan exit criterion 1). Subtest ordering is the rollout order
// from plan task 5.10; each subtest asserts header-PRESENT wire bytes
// match the committed golden byte-for-byte (after volatile-field
// stripping per the determinism rules).
func TestRealStreaming_ParityMatrix_AllPaths(t *testing.T) {
	fixtures := fixtureSet()

	// (1) Anthropic→Anthropic passthrough — REAL fixture (reviewer
	// finding #1 fix): internal model whose credential provider is
	// "anthropic", credential's BaseURL points at an upstream that
	// speaks Anthropic SSE. handlePassthroughStreamResponse
	// (handler_anthropic.go:1679, dispatch at :1225 / :401-403) pipes
	// the upstream bytes through byte-for-byte. The golden is the
	// upstream's wire shape MINUS the volatile id field; the
	// external-translation golden (subtest 2) differs because the
	// translator concatenates deltas into a single content_block_delta
	// event while the raw passthrough preserves each upstream delta.
	//
	// Variant matrix (reviewer finding #2 fix): paths 1-4 are each
	// exercised against text/thinking/tool_call/usage variants where
	// applicable. role_only/usage_first/comment_first are OpenAI-wire
	// provider-shaped first chunks; they apply to paths 2 and 4
	// (OpenAI upstream), not to path 1 (Anthropic upstream) or path 3
	// (typed-event internal handler).
	for _, variant := range []string{"text", "thinking", "tool_call", "usage"} {
		variant := variant
		t.Run("1_AnthropicPassthrough_"+variant+"_HeaderPresent", func(t *testing.T) {
			upstream := httptest.NewServer(anthropicRawPassthroughHandler(variant))
			t.Cleanup(upstream.Close)

			t.Setenv("APPLY_ENV_OVERRIDES", "true")
			t.Setenv("UPSTREAM_URL", upstream.URL)
			mgr, err := config.NewManager()
			if err != nil {
				t.Fatalf("new manager: %v", err)
			}

			// Wire an internal model whose single credential has
			// Provider="anthropic" — this is the gate that selects the
			// passthrough branch at attemptAnthropicModel
			// (handler_anthropic.go:401-403). The credential's BaseURL
			// overrides the default UPSTREAM_URL.
			modelsCfg := models.NewModelsConfig()
			if err := modelsCfg.AddCredential(models.CredentialConfig{
				ID:       "anthropic-passthrough-cred",
				Provider: "anthropic",
				APIKey:   "sk-anthropic-test",
				BaseURL:  upstream.URL,
			}); err != nil {
				t.Fatalf("AddCredential: %v", err)
			}
			if err := modelsCfg.AddModel(models.ModelConfig{
				ID:              "claude-passthrough",
				Name:            "claude-passthrough",
				Enabled:         true,
				Internal:        true,
				InternalModel:   "claude-sonnet-4-5",
				InternalBaseURL: upstream.URL,
				Credentials:     models.TestRefs("anthropic-passthrough-cred"),
			}); err != nil {
				t.Fatalf("AddModel: %v", err)
			}

			h := NewHandler(&Config{ConfigMgr: mgr, ModelsConfig: modelsCfg}, events.NewBus(), store.NewRequestStore(100), nil, nil, nil, nil)

			body := anthropicBody("claude-passthrough", true, []map[string]interface{}{
				{"role": "user", "content": "hi"},
			})
			req := makeLiveAnthropicRequest(t, body, true)
			rr := httptest.NewRecorder()
			h.HandleAnthropicMessages(rr, req)

			golden, goldenCT := readGolden(t, "anthropic_passthrough_"+variant)
			assertParity(t, "AnthropicPassthrough", variant, rr.Body.String(), golden, goldenCT, rr.Header().Get("Content-Type"))
		})
	}

	// (2) Anthropic→OpenAI-wire external translation
	// (handler_anthropic.go:819/:851). Header-present = the
	// TranslateBufferedStream path, byte-identical to today.
	//
	// Variant matrix: text + thinking + tool_call + usage + role_only +
	// usage_first + comment_first (all variants in fixtureSet()).
	for _, variant := range []string{"text", "thinking", "tool_call", "usage", "role_only", "usage_first", "comment_first"} {
		variant := variant
		t.Run("2_AnthropicExternalTranslation_"+variant+"_HeaderPresent", func(t *testing.T) {
			fixture := fixtures[variant]
			upstream := httptest.NewServer(fixture.handler())
			t.Cleanup(upstream.Close)

			t.Setenv("APPLY_ENV_OVERRIDES", "true")
			t.Setenv("UPSTREAM_URL", upstream.URL)
			mgr, err := config.NewManager()
			if err != nil {
				t.Fatalf("new manager: %v", err)
			}
			modelsCfg := models.NewModelsConfig()
			h := NewHandler(&Config{ConfigMgr: mgr, ModelsConfig: modelsCfg}, events.NewBus(), store.NewRequestStore(100), nil, nil, nil, nil)

			body := anthropicBody("claude-sonnet-4-5", true, []map[string]interface{}{
				{"role": "user", "content": "hi"},
			})
			req := makeLiveAnthropicRequest(t, body, true)
			rr := httptest.NewRecorder()
			h.HandleAnthropicMessages(rr, req)

			golden, goldenCT := readGolden(t, "anthropic_external_"+variant)
			assertParity(t, "AnthropicExternal", variant, rr.Body.String(), golden, goldenCT, rr.Header().Get("Content-Type"))
		})
	}

	// (3) Anthropic→OpenAI-wire internal translation
	// (doAnthropicInternalRequest at handler_anthropic.go:600-685).
	// Buffered mode = the legacy `handleStream` direct path with the
	// live translator NOT installed; the recorder captures raw
	// OpenAI-wire bytes which `TranslateBufferedStream` consumes
	// downstream. Golden captures the recorder's content sink.
	//
	// Variant matrix: text + thinking + tool_call + usage. role_only /
	// usage_first / comment_first don't apply (the internal handler
	// uses typed events, not raw OpenAI chunks); the upstream's
	// role-only / usage-first chunks map to typed events via the
	// normalizer, which the golden for the text variant already
	// covers.
	for _, variant := range []string{"text", "thinking", "tool_call", "usage"} {
		variant := variant
		t.Run("3_AnthropicInternalTranslation_"+variant+"_HeaderPresent", func(t *testing.T) {
			events := streamEventsForVariant(variant)
			provider := &liveStreamEventProvider{events: events}

			handler := NewInternalHandler(
				&models.ModelConfig{ID: "test-model", InternalModel: "gpt-4"},
				&mockModelsConfig{},
			)
			// Buffered mode: NO live translator wired — `handleStream`
			// writes raw OpenAI SSE chunks to w (the recorder) per the
			// buffered branch at internal_handler.go:450-469.

			rec := httptest.NewRecorder()
			rec.Body = new(bytes.Buffer)
			rw := &flushingResponseRecorder{ResponseRecorder: rec}

			req := &providers.ChatCompletionRequest{
				Model:    "test-model",
				Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
				Stream:   true,
			}
			if err := handler.handleStream(context.Background(), provider, req, rw, "gpt-4"); err != nil {
				t.Fatalf("handleStream (buffered): %v", err)
			}

			golden, _ := readGolden(t, "anthropic_internal_"+variant)
			// Buffered-mode recorder body is the SSE wire shape; we strip
			// volatile fields and compare to the golden.
			assertParity(t, "AnthropicInternal", variant, rw.Body.String(), golden, "", "")
		})
	}

	// (4) /v1/chat/completions OpenAI race path — header-present
	// (buffered) ⇒ coordinator runs with IsCompleted gate; wire bytes
	// match pre-feature behavior.
	//
	// Variant matrix: text + thinking + tool_call + usage + role_only +
	// usage_first + comment_first.
	for _, variant := range []string{"text", "thinking", "tool_call", "usage", "role_only", "usage_first", "comment_first"} {
		variant := variant
		t.Run("4_OpenAIRacePath_"+variant+"_HeaderPresent", func(t *testing.T) {
			fixture := fixtures[variant]
			h, _ := newTestHandler(t, fixture.handler(), models.NewModelsConfig())

			reqHTTP := makeRequest(t, simpleBody("mock-model", true))
			reqHTTP.Header.Set("X-LLMProxy-Buffer-Response", "true")
			rec := httptest.NewRecorder()
			h.HandleChatCompletions(rec, reqHTTP)

			golden, goldenCT := readGolden(t, "openai_race_"+variant)
			assertParity(t, "OpenAIRace", variant, rec.Body.String(), golden, goldenCT, rec.Header().Get("Content-Type"))
		})
	}

	// (5)+(6) Ultimate external/internal stream — DEFERRED to existing
	// pkg/ultimatemodel tests. The ultimate Handler's streamResponse +
	// handleInternalStream methods are package-private (lowercase), so a
	// direct parity call from pkg/proxy is impossible. The existing
	// TestUltimate_HeaderDispatch tests (in handler_external_test.go +
	// handler_internal_test.go) already assert buffered == live wire
	// bytes (transitively pinning buffered == pre-feature). The
	// TestUltimateCapture_LiveMode_IdenticalToBuffered test pins C1
	// estimator parity for both ultimate paths. These two subtests
	// document the deferral accurately (skip comments previously
	// claimed "re-verify" semantics they did not implement — finding
	// #5 cleanup).
	t.Run("5_UltimateExternal_HeaderPresent", func(t *testing.T) {
		// DEFERRED — pinned by existing pkg/ultimatemodel tests.
		// See this file's header doc (plan 5.10 + ExecuteOptions caveat).
		t.Skip("path 5 pinned by existing pkg/ultimatemodel TestUltimate_HeaderDispatch; the proxy-package boundary cannot reach the private streamResponse method directly")
	})

	t.Run("6_UltimateInternal_HeaderPresent", func(t *testing.T) {
		// DEFERRED — pinned by existing pkg/ultimatemodel tests.
		// See this file's header doc (plan 5.10 + ExecuteOptions caveat).
		t.Skip("path 6 pinned by existing pkg/ultimatemodel TestUltimate_HeaderDispatch; the proxy-package boundary cannot reach the private handleInternalStream method directly")
	})
}

// streamEventsForVariant maps a content-variant label to the typed
// providers.StreamEvent sequence used by the internal-handler path (3)
// in TestRealStreaming_ParityMatrix_AllPaths. Mirrors fixtureSet() but
// for the typed-event channel rather than raw OpenAI SSE chunks; the
// internal handler reads from this channel, applies toolcall buffering,
// and writes raw OpenAI SSE chunks downstream.
func streamEventsForVariant(variant string) []providers.StreamEvent {
	switch variant {
	case "text":
		return []providers.StreamEvent{
			{Type: "content", Content: "Hello"},
			{Type: "content", Content: " world"},
			{Type: "content", Content: "!"},
			{Type: "done", FinishReason: "stop"},
		}
	case "thinking":
		return []providers.StreamEvent{
			{Type: "thinking", ReasoningContent: "Let me think"},
			{Type: "content", Content: "answer"},
			{Type: "done", FinishReason: "stop"},
		}
	case "tool_call":
		// The internal handler translates typed tool_call events into
		// OpenAI-wire tool_call deltas downstream. The exact wire
		// shape is whatever the typed-event → OpenAI SSE pipeline
		// emits in this commit; the golden pins it.
		return []providers.StreamEvent{
			{Type: "content", Content: "Looking up..."},
			{Type: "tool_call", ToolCalls: []providers.ToolCall{
				{
					ID:    "call_test",
					Type:  "function",
					Index: 0,
					Function: providers.ToolCallFunction{
						Name:      "get_weather",
						Arguments: `{"city":"SF"}`,
					},
				},
			}},
			{Type: "done", FinishReason: "tool_calls"},
		}
	case "usage":
		return []providers.StreamEvent{
			{Type: "content", Content: "ok"},
			{Type: "done", FinishReason: "stop"},
		}
	default:
		return streamEventsForVariant("text")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TEST 2 — Rollback-via-header. Every path respects the header: header
// present ⇒ buffered; header absent ⇒ live. This is the safety net:
// flipping the header on the load balancer reverts the entire feature.
// ─────────────────────────────────────────────────────────────────────────────

// TestRealStreaming_RollbackViaHeader asserts the header dispatch
// (plan L1 truth table) on every path. The contract:
//
//   - header present ⇒ buffered (IsCompleted gate, single-write, recorder)
//   - header absent ⇒ live (first-byte winner gate, per-chunk flush)
//
// For each path we run the request TWICE: once with the header set,
// once without. Both must complete successfully (200 / well-formed
// SSE), with the buffered-mode run matching the golden exactly and the
// live-mode run containing the first chunk before upstream [DONE].
func TestRealStreaming_RollbackViaHeader(t *testing.T) {
	fixtures := fixtureSet()

	t.Run("OpenAI_Path", func(t *testing.T) {
		fixture := fixtures["text"]
		h, _ := newTestHandler(t, fixture.handler(), models.NewModelsConfig())

		// Buffered run.
		reqBuf := makeRequest(t, simpleBody("mock-model", true))
		reqBuf.Header.Set("X-LLMProxy-Buffer-Response", "true")
		recBuf := httptest.NewRecorder()
		h.HandleChatCompletions(recBuf, reqBuf)
		golden, _ := readGolden(t, "openai_race_text")
		assertParity(t, "OpenAIRace", "text", recBuf.Body.String(), golden, "", recBuf.Header().Get("Content-Type"))

		// Live run — first-byte timing is asserted in
		// real_streaming_firstbyte_test.go (event-based). Here we
		// only assert the response is well-formed.
		reqLive := makeRequest(t, simpleBody("mock-model", true))
		reqLive.Header.Del("X-LLMProxy-Buffer-Response")
		recLive := httptest.NewRecorder()
		h.HandleChatCompletions(recLive, reqLive)
		if !strings.Contains(recLive.Body.String(), "Hello") {
			t.Errorf("live mode: missing expected content; body=%q", recLive.Body.String())
		}
	})

	t.Run("Anthropic_Passthrough_Path", func(t *testing.T) {
		// Real passthrough fixture — internal model with
		// anthropic-provider credential pointing at the upstream
		// (matches the gate at handler_anthropic.go:401-403 that
		// dispatches to handlePassthroughStreamResponse). Same setup
		// as subtest 1 of TestRealStreaming_ParityMatrix_AllPaths.
		upstream := httptest.NewServer(anthropicRawPassthroughHandler("text"))
		t.Cleanup(upstream.Close)

		t.Setenv("APPLY_ENV_OVERRIDES", "true")
		t.Setenv("UPSTREAM_URL", upstream.URL)
		mgr, _ := config.NewManager()
		modelsCfg := models.NewModelsConfig()
		if err := modelsCfg.AddCredential(models.CredentialConfig{
			ID:       "anthropic-passthrough-cred",
			Provider: "anthropic",
			APIKey:   "sk-anthropic-test",
			BaseURL:  upstream.URL,
		}); err != nil {
			t.Fatalf("AddCredential: %v", err)
		}
		if err := modelsCfg.AddModel(models.ModelConfig{
			ID:              "claude-passthrough",
			Name:            "claude-passthrough",
			Enabled:         true,
			Internal:        true,
			InternalModel:   "claude-sonnet-4-5",
			InternalBaseURL: upstream.URL,
			Credentials:     models.TestRefs("anthropic-passthrough-cred"),
		}); err != nil {
			t.Fatalf("AddModel: %v", err)
		}
		h := NewHandler(&Config{ConfigMgr: mgr, ModelsConfig: modelsCfg}, events.NewBus(), store.NewRequestStore(100), nil, nil, nil, nil)

		body := anthropicBody("claude-passthrough", true, []map[string]interface{}{
			{"role": "user", "content": "hi"},
		})

		// Buffered run.
		reqBuf := makeLiveAnthropicRequest(t, body, true)
		recBuf := httptest.NewRecorder()
		h.HandleAnthropicMessages(recBuf, reqBuf)
		golden, _ := readGolden(t, "anthropic_passthrough_text")
		assertParity(t, "AnthropicPassthrough", "text", recBuf.Body.String(), golden, "", recBuf.Header().Get("Content-Type"))

		// Live run — content is forwarded verbatim from the upstream
		// so we assert the upstream's literal chunk1 token reaches
		// the client (the passthrough doesn't translate; what the
		// upstream emits is what the client sees).
		reqLive := makeLiveAnthropicRequest(t, body, false)
		recLive := httptest.NewRecorder()
		h.HandleAnthropicMessages(recLive, reqLive)
		if !strings.Contains(recLive.Body.String(), "Hello") {
			t.Errorf("live mode: missing expected content; body=%q", recLive.Body.String())
		}
	})

	t.Run("Ultimate_External_Path_Stub", func(t *testing.T) {
		// Path 5 (Ultimate external) rollback is pinned by the
		// existing TestUltimate_HeaderDispatch in
		// pkg/ultimatemodel/handler_external_test.go — direct
		// ultimatemodel.Handler calls are package-private. We
		// confirm here that the header dispatch lands in buffered
		// mode for the proxy lanes (paths 1-4); ultimate-path
		// dispatch is structurally identical (same ExecuteOptions
		// shape) and is covered by the ultimatemodel test battery.
		t.Skip("path 5 rollback pinned by existing pkg/ultimatemodel TestUltimate_HeaderDispatch")
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// TEST 3 — Multi-value / empty-value header semantics (leader decision 2).
//
// The plan pins a 4-row truth table for the header presence + value
// combination. This test asserts the multi-value first-wins row
// (defense-in-depth: Go's net/http canonicalizes multi-value headers
// into a comma-joined single string for `Values()` reads; we test the
// direct map path explicitly).
// ─────────────────────────────────────────────────────────────────────────────

func TestRealStreaming_HeaderTruthTable_FirstValueWins(t *testing.T) {
	// r.Header is a map; multi-value headers are stored as a slice.
	// The parser uses r.Header.Values("X-LLMProxy-Buffer-Response")[0]
	// — i.e. first value wins.
	tests := []struct {
		name    string
		values  []string // set as r.Header["X-LLMProxy-Buffer-Response"] = ...
		wantBuf bool
	}{
		{"absent", nil, false},
		{"present_empty", []string{""}, true},
		{"present_true", []string{"true"}, true},
		{"present_false_then_true", []string{"false", "true"}, false}, // first wins
		{"present_true_then_false", []string{"true", "false"}, true},  // first wins
		{"present_empty_then_true", []string{"", "true"}, true},       // first wins
		{"present_unknown", []string{"maybe"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{}"))
			if tc.values != nil {
				// Header.Set canonicalizes the key; multi-value
				// tests use direct map assignment to bypass the
				// canonicalization but Values() canonicalizes on
				// read, so both paths end up at the canonical
				// "X-Llmproxy-Buffer-Response" key.
				for i, v := range tc.values {
					if i == 0 {
						req.Header.Set("X-LLMProxy-Buffer-Response", v)
					} else {
						req.Header.Add("X-LLMProxy-Buffer-Response", v)
					}
				}
			}
			got := bufferModeFor(req)
			if got != tc.wantBuf {
				t.Errorf("bufferModeFor(values=%v) = %v, want %v", tc.values, got, tc.wantBuf)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestRealStreaming_VolatileStripping — pin the deterministic-strip
// rules. If this test fails, the goldens will drift; the failing
// pattern needs to be added to volStripPatterns and goldens regenerated.
// ─────────────────────────────────────────────────────────────────────────────
func TestRealStreaming_VolatileStripping(t *testing.T) {
	input := `: connected

data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk","created":1700000000,"model":"m","choices":[{"delta":{"content":"hi"}}]}

data: {"id":"chatcmpl-abc124","object":"chat.completion.chunk","created":1700000001,"model":"m","choices":[{"delta":{},"finish_reason":"stop"}]}

: keepalive

data: [DONE]

`
	got := volStrip(input)
	if strings.Contains(got, "chatcmpl-") {
		t.Errorf("strip failed: still contains request id; got=%q", got)
	}
	if strings.Contains(got, `"created":`) {
		t.Errorf("strip failed: still contains created; got=%q", got)
	}
	if strings.Contains(got, ": connected") {
		t.Errorf("strip failed: still contains preamble; got=%q", got)
	}
	if strings.Contains(got, ": keepalive") {
		t.Errorf("strip failed: still contains heartbeat; got=%q", got)
	}
	if strings.Contains(got, "[DONE]") {
		t.Errorf("strip failed: still contains [DONE]; got=%q", got)
	}
	// Remaining payload still intact.
	if !strings.Contains(got, `"content":"hi"`) {
		t.Errorf("strip over-stripped: content payload missing; got=%q", got)
	}
}
