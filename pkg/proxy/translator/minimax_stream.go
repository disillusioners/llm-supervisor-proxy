package translator

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
)

// ─────────────────────────────────────────────────────────────────────────────
// MiniMax reasoning_details ↔ reasoning_content response-side translation
// ─────────────────────────────────────────────────────────────────────────────
//
// Scope: EXTERNAL response paths only (D2 — race-external + ultimate-external).
// Internal response paths (race-internal + ultimate-internal) extract
// reasoning_details directly inside pkg/providers/openai.go's stream parser
// and response-assembly, so this translator is not invoked on those paths.
//
// Thread safety: NOT goroutine-safe. Each StreamTranslator instance is owned
// by a single stream loop; multiple instances are independent. The package-
// level formatDriftCounter is the only shared state and is atomic.
//
// The MiniMax provider + flag gate is the caller's responsibility; these
// functions run unconditionally on the input body / line and trust the
// caller to short-circuit on non-MiniMax / non-flagged requests (H5 — this
// is what preserves byte-identical behavior on gated-off paths).
// ─────────────────────────────────────────────────────────────────────────────

// knownReasoningFormats is the registered MiniMax response-side format
// version set. New versions are added here as upstream rolls them out. The
// proxy does NOT reject unknown formats — it logs a sampled WARN and
// continues; the strip (R11) is total regardless of format. This is a
// forward-compat hedge, not a contract.
var knownReasoningFormats = map[string]struct{}{
	reasoningTextFormat: {}, // "MiniMax-response-v1"
}

// formatDriftCounter is a process-wide counter incremented once per unique
// observed unknown format value (best-effort, see ChunkBytes / recordDrift).
// The getter is exposed for the Phase-3 verification report.
var formatDriftCounter atomic.Uint64

// FormatDriftCount returns the current value of the format-drift counter.
// Exported for verification reporting (P3-6); not used by the proxy itself.
func FormatDriftCount() uint64 {
	return formatDriftCounter.Load()
}

// recordDrift increments the format-drift counter and emits a sampled
// WARN log. Sampling is best-effort (1 in formatDriftLogEvery invocations)
// to bound log volume on a hot stream of unknown-format chunks. The
// counter is incremented unconditionally so verification can see the
// true drift rate even when WARNs are suppressed.
func recordDrift(entry map[string]any) {
	formatDriftCounter.Add(1)
	if formatDriftCounter.Load()%formatDriftLogEvery != 0 {
		return
	}
	log.Printf("[WARN] minimax reasoning_details unknown format=%v id=%v",
		entry["format"], entry["id"])
}

// formatDriftLogEvery is the inverse sampling rate for format-drift WARN
// logs. 1 in N warnings is emitted; the counter is always incremented.
// Modest value to keep the line ratio tolerable for live verification.
const formatDriftLogEvery = 64

// ExtractEntryText pulls the reasoning text out of a single MiniMax
// reasoning_details entry. It checks `text` first and falls back to
// `content` (forward-compat for a possible v2 rename per H3 / §6.2). The
// returned string is returned untrimmed; callers should apply TrimSpace
// before deciding whether to emit (H7 skip-empty).
//
// Exported (AM-13) so pkg/providers/openai.go can call it as a helper-
// only import (R3 layering — providers → translator is the inversion;
// see the mandated comment in pkg/providers/openai.go).
//
// Returns "" if neither key is present or if both are present and the
// resolved text is empty.
func ExtractEntryText(entry map[string]any) string {
	if entry == nil {
		return ""
	}
	if v, ok := entry["text"]; ok {
		if s, ok := v.(string); ok {
			if s != "" {
				return s
			}
		}
	}
	if v, ok := entry["content"]; ok {
		if s, ok := v.(string); ok {
			return s // may be "" — caller decides via TrimSpace
		}
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// Non-stream response translator (P2-1b / P2-6 / D2)
// ─────────────────────────────────────────────────────────────────────────────

// TranslateNonStreamResponseBody is the response-side map-core translator.
// It mutates the supplied body in place to convert
// `choices[i].message.reasoning_details` (array of typed entries) into
// `choices[i].message.reasoning_content` (concatenated string) and to
// strip the `reasoning_details` array + sibling metadata fields (audio /
// name) that the MiniMax response-side wire format can carry.
//
// Operational order of the single-winner rule (D2): when both
// `reasoning_details` and `reasoning_content` are present, the
// `reasoning_details` array WINS and `reasoning_content` is ignored
// (not concatenated, not duplicated). This matches the typed/internal
// path's extraction-time single-winner.
//
// H2 dedup: per-entry `strings.Index` containment check vs the
// (possibly-existing) `reasoning_content` — entries whose text is
// already contained in `reasoning_content` are dropped. H7 skip-empty:
// entries whose text is empty after TrimSpace are dropped. Unknown entry
// types (not "reasoning.text") are sampled-debug-logged and dropped.
//
// Not goroutine-safe; the gate is the caller's responsibility. Returns
// a wrapped error if the body is nil or `choices` is present but not a
// `[]any`. On error, the map is left unmutated.
func TranslateNonStreamResponseBody(body map[string]any) error {
	if body == nil {
		return fmt.Errorf("TranslateNonStreamResponseBody: body is nil")
	}
	rawChoices, exists := body["choices"]
	if !exists {
		return nil // no choices → nothing to translate
	}
	choices, ok := rawChoices.([]any)
	if !ok {
		return fmt.Errorf("TranslateNonStreamResponseBody: choices is %T, expected []any", rawChoices)
	}

	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]any)
		if !ok {
			continue
		}
		rawMsg, present := choice["message"]
		if !present {
			continue
		}
		msg, ok := rawMsg.(map[string]any)
		if !ok {
			continue
		}
		// Always strip the sibling metadata fields the MiniMax
		// response-side wire format can carry (the plan names audio
		// and name as the targets). Stripping is unconditional
		// because these are MiniMax-only fields and would leak
		// upstream semantics to OpenAI clients. Done BEFORE the
		// reasoning pass to keep the rest of the message intact.
		delete(msg, "audio")
		delete(msg, "audio_content")
		delete(msg, "name")

		// Walk reasoning_details and build the concatenated text
		// into ReasoningContent. Single-winner: if details are
		// present (non-empty), they win; reasoning_content (if any)
		// is ignored.
		rawDetails, hasDetails := msg["reasoning_details"]
		if !hasDetails {
			// No details ⇒ leave reasoning_content untouched
			// (preserves pre-existing upstream reasoning_content
			// when the upstream chose to send it without details).
			continue
		}
		details, ok := rawDetails.([]any)
		if !ok || len(details) == 0 {
			// Wrong type or empty array: clear details and
			// leave reasoning_content alone.
			delete(msg, "reasoning_details")
			continue
		}

		// details present and non-empty → reasoning_details WINS.
		// Start from an empty reasoning_content regardless of any
		// pre-existing value (single-winner rule, D2).
		existing := ""
		if v, ok := msg["reasoning_content"].(string); ok {
			existing = v
		}

		var b strings.Builder
		for _, rawEntry := range details {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				continue
			}
			// Unknown entry type → log-and-skip.
			t, _ := entry["type"].(string)
			if t != "" && t != reasoningTextType {
				if entryDebugSampling.Allow() {
					log.Printf("[DEBUG] minimax reasoning_details unknown entry type=%q", t)
				}
				continue
			}
			// Format-drift counter (P2-9).
			if formatVal, hasFormat := entry["format"].(string); hasFormat && formatVal != "" {
				if _, known := knownReasoningFormats[formatVal]; !known {
					recordDrift(entry)
				}
			}
			text := ExtractEntryText(entry)
			if strings.TrimSpace(text) == "" {
				// H7 skip-empty.
				continue
			}
			// H2 dedup: skip if text is contained in the
			// pre-existing reasoning_content (which we're
			// discarding, but it's the right comparison target
			// — the pre-existing value reflects what an earlier
			// phase of the conversation sent up). Note: when
			// reasoning_details wins, we ignore reasoning_content
			// entirely, so this dedup only protects against
			// intra-array duplicates from upstream cumulative
			// emission.
			if existing != "" && strings.Index(existing, text) >= 0 {
				continue
			}
			// Also dedup within our own accumulation: if we
			// already emitted this text, skip (covers both
			// cumulative-mode intra-array replay and the mock
			// harness's "both fields" case at the array level).
			if b.Len() > 0 && strings.Index(b.String(), text) >= 0 {
				continue
			}
			b.WriteString(text)
		}

		msg["reasoning_content"] = b.String()
		delete(msg, "reasoning_details")
	}
	return nil
}

// TranslateNonStreamResponseBytes is the response-side bytes wrapper. It
// parses the body, runs the map-core translator, and re-marshals the
// result. Use this on verbatim-byte call sites that already hold response
// bytes and have no usable map representation (notably ultim-ext
// non-stream per §5.3 — the only verbatim path).
//
// Not goroutine-safe; the gate is the caller's responsibility.
//
// Returns a wrapped error if the input is not valid JSON, the map shape
// is invalid, or re-marshal fails. On parse error, no bytes are returned.
// On translate error, the bytes returned reflect the unmutated map
// (the translator leaves the body unmutated on error per its godoc).
func TranslateNonStreamResponseBytes(body []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("TranslateNonStreamResponseBytes: parse body: %w", err)
	}
	if err := TranslateNonStreamResponseBody(m); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// ─────────────────────────────────────────────────────────────────────────────
// Stream translator (P2-2 / P2-3 / P2-8 / D2)
// ─────────────────────────────────────────────────────────────────────────────

// StreamTranslator is a per-stream translator for MiniMax-format
// `delta.reasoning_details` → `delta.reasoning_content` emission on
// EXTERNAL response paths. State lifetime is the stream-loop scope at
// the call site; the instance dies when the loop exits. There is no
// Reset() (race-retry footgun, §3.3) — the caller is expected to
// construct a fresh instance per stream.
type StreamTranslator struct {
	// lastIssued is the per-(streamID, choice_index) cumulative text
	// of the most recently emitted reasoning chunk. The map is
	// allocated by NewStreamTranslator and is never nil for instances
	// produced by the constructor.
	lastIssued map[int]string
}

// NewStreamTranslator returns a fresh instance with a non-nil
// lastIssued map. The returned instance is not goroutine-safe; it is
// intended for use by a single stream loop goroutine.
func NewStreamTranslator() *StreamTranslator {
	return &StreamTranslator{
		lastIssued: make(map[int]string),
	}
}

// ChunkBytes processes one SSE line. The input is an SSE line WITHOUT
// the `data: ` prefix and WITHOUT the trailing `\n\n` (W9 — mirrors
// the call-site pattern at translator/stream.go:42-46). If the prefix
// is present, it is stripped first.
//
// Returned values:
//
//   - stripped: the line as it should be written downstream — same
//     upstream line with `reasoning_details` removed. tool_calls /
//     content / finish_reason survive unchanged. If the line had no
//     `reasoning_details`, stripped is the original line and emitted
//     is nil (passthrough).
//   - emitted: zero or more client chunks to write IN ORDER, with the
//     caller responsible for adding the `data: ` prefix + trailing
//     `\n\n` before the next flush boundary (P2-2 godoc, H8 single-
//     flush on ultim-ext).
//   - err: ALWAYS nil. Per §6.1, the stream translator is error-free
//     by construction; anomalies are logged as sampled WARN and the
//     line is passed through (or with reasoning_details stripped if
//     the line parsed). The error return is preserved on the API for
//     future-proofing the §6.1 contract.
//
// Algorithm:
//
//  1. Strip the `data: ` prefix if present.
//  2. Parse the remainder as `map[string]any`. Parse-failure →
//     (line, nil, nil) passthrough. (Does not strip; caller may want
//     the raw line downstream.)
//  3. Locate `delta.reasoning_details` (or `message.reasoning_details`
//     for non-stream — but on the stream path it's always delta).
//     Absent → (line, nil, nil) passthrough.
//  4. For each entry in array order:
//     - extractEntryText → "" after TrimSpace → skip (H7)
//     - recordDrift if format is non-empty and unknown
//     - H2 dedup vs state.lastIssued[choiceIdx] (containment check)
//     - Cumulative-suffix mode: if text is strict superset of
//       lastIssued[choiceIdx], emit only the suffix and update state
//     - Else: emit the text and update state
//  5. Strip reasoning_details from a copy of the line and return as
//     stripped (tool_calls + content survive unchanged).
//
// Boundary documentation (P2-7): reasoning translation is read-only on
// tool_calls and only touches `reasoning_details`; per-event ordering
// is preserved by chunk-level emission in array order.
//
// Not goroutine-safe; see NewStreamTranslator godoc. The translator
// does not call into `toolrepair` and does not mutate tool_call state
// on ultim-ext (W3 — the per-event buffer is constructed later in the
// call site after ChunkBytes returns the stripped line).
func (t *StreamTranslator) ChunkBytes(line []byte) (stripped []byte, emitted [][]byte, err error) {
	if t == nil {
		// Defensive: the constructor never returns nil, but callers
		// that hand-craft an instance must still get safe behavior.
		return line, nil, nil
	}
	if len(line) == 0 {
		return line, nil, nil
	}

	// Step 1: strip `data: ` prefix if present. W9 — the caller's
	// contract is to pass the line WITHOUT the prefix, but we strip
	// it if it's there to mirror translator/stream.go:42-46 precedent
	// (chunks can be normalized with or without prefix upstream).
	prefix := []byte("data: ")
	if bytesHasPrefix(line, prefix) {
		line = line[len(prefix):]
	}

	// Step 2: parse the JSON payload.
	var chunk map[string]any
	if jerr := json.Unmarshal(line, &chunk); jerr != nil {
		// Parse-fail → passthrough unmodified. No stripped/emitted.
		log.Printf("[WARN] minimax stream chunk parse failure: %v", jerr)
		return line, nil, nil
	}

	// Step 3: locate choices[].delta.reasoning_details.
	// The MiniMax wire shape (and OpenAI's) is
	//   { "choices": [ { "index": N, "delta": { "reasoning_details": [...] } } ] }
	// so we walk choices to find the delta map. We process at most
	// one delta per call (the wire format's typical shape); for
	// multi-choice streams the per-entry state is keyed by choice
	// index so callers should make one call per chunk.
	rawChoices, hasChoices := chunk["choices"].([]any)
	if !hasChoices || len(rawChoices) == 0 {
		// No choices array at all → passthrough.
		return marshalChunkForPassthrough(chunk, line)
	}
	c0, _ := rawChoices[0].(map[string]any)
	delta, hasDelta := c0["delta"].(map[string]any)
	if !hasDelta {
		// No delta → nothing to do. Return the original line
		// (with prefix stripped if it was there) and no
		// emitted chunks.
		return marshalChunkForPassthrough(chunk, line)
	}

	rawDetails, hasDetails := delta["reasoning_details"]
	if !hasDetails {
		// No reasoning_details → passthrough.
		return marshalChunkForPassthrough(chunk, line)
	}
	details, ok := rawDetails.([]any)
	if !ok || len(details) == 0 {
		// Wrong type or empty: strip the key (R11 — no leak) and
		// return the line as stripped.
		delete(delta, "reasoning_details")
		return marshalChunkForPassthrough(chunk, line)
	}

	// Step 4: per-entry emission.
	// Determine the choice index from choices[0].index.
	choiceIdx := 0
	if v, ok := c0["index"].(float64); ok {
		choiceIdx = int(v)
	}

	// Build emitted chunks in array order. We have to be careful
	// that emit-time and state-update are atomic w.r.t. each entry
	// so a future change can re-derive state.lastIssued from
	// emitted text if needed.
	for _, rawEntry := range details {
		entry, ok := rawEntry.(map[string]any)
		if !ok {
			continue
		}
		// Unknown entry type → log-and-skip.
		t2, _ := entry["type"].(string)
		if t2 != "" && t2 != reasoningTextType {
			if entryDebugSampling.Allow() {
				log.Printf("[DEBUG] minimax stream reasoning_details unknown entry type=%q", t2)
			}
			continue
		}
		// Format-drift counter.
		if formatVal, hasFormat := entry["format"].(string); hasFormat && formatVal != "" {
			if _, known := knownReasoningFormats[formatVal]; !known {
				recordDrift(entry)
			}
		}
		text := ExtractEntryText(entry)
		if strings.TrimSpace(text) == "" {
			continue // H7 skip-empty
		}

		// H2 dedup vs the last emitted text for this choice.
		// Containment (strings.Index) — not equality — so cumulative
		// upstream emission (which contains the prior text) is
		// detected and emitted as a suffix only.
		prior := t.lastIssued[choiceIdx]
		if prior != "" && strings.Index(prior, text) >= 0 {
			// Already covered by prior emission → skip.
			continue
		}

		var emittedText string
		if prior != "" && strings.HasPrefix(text, prior) {
			// Cumulative-suffix mode: text is a strict superset
			// of prior. Emit only the suffix to avoid
			// quadratic growth.
			emittedText = text[len(prior):]
		} else {
			emittedText = text
		}
		if emittedText == "" {
			// Cumulative mode + identical text → no new
			// emission. Update state to the longer text and
			// continue.
			t.lastIssued[choiceIdx] = text
			continue
		}

		// Build the emitted client chunk: same shape as the
		// original line, but with reasoning_details removed and
		// delta.reasoning_content set to the suffix.
		emittedLine := buildReasoningContentChunk(chunk, choiceIdx, emittedText)
		emitted = append(emitted, emittedLine)
		t.lastIssued[choiceIdx] = text
	}

	// Step 5: strip reasoning_details from the original chunk and
	// return as stripped. The caller writes stripped first, then
	// each emitted[i] in order.
	delete(delta, "reasoning_details")
	stripped, mErr := json.Marshal(chunk)
	if mErr != nil {
		// Marshal failure is unexpected for a chunk we just
		// parsed; log and passthrough.
		log.Printf("[WARN] minimax stream chunk marshal failure: %v", mErr)
		return line, nil, nil
	}
	return stripped, emitted, nil
}

// buildReasoningContentChunk builds one client chunk from a stripped
// source chunk by setting delta.reasoning_content and copying the rest
// of the envelope (id / object / created / model / usage if present).
// It does NOT include reasoning_details (the caller already deleted
// that key from the source chunk before calling this).
func buildReasoningContentChunk(source map[string]any, choiceIdx int, content string) []byte {
	chunk := make(map[string]any, len(source))
	for k, v := range source {
		if k == "choices" || k == "delta" {
			continue // we replace these below
		}
		chunk[k] = v
	}
	// Build the choices array with a single entry for our choiceIdx.
	// For choice index 0 (the overwhelming majority of cases) we copy
	// the rest of choice[0] verbatim. For non-zero indices, we still
	// build a single-entry array; the wire format expects the
	// choices array to align with the chunk in question.
	srcChoices, _ := source["choices"].([]any)
	choice := make(map[string]any)
	finishReason := ""
	if choiceIdx < len(srcChoices) {
		if c0, ok := srcChoices[choiceIdx].(map[string]any); ok {
			for k, v := range c0 {
				if k == "delta" {
					continue
				}
				choice[k] = v
			}
			if v, ok := c0["finish_reason"].(string); ok {
				finishReason = v
			}
		}
	}
	choice["index"] = float64(choiceIdx)
	// Per-entry emission must NOT carry a finish_reason (the chunk
	// is mid-stream reasoning — finish_reason stays on the final
	// chunk). If the source happened to have finish_reason set we
	// preserve it only when no reasoning is emitted; here we always
	// strip it from the emitted reasoning chunk to keep the
	// semantics clean.
	if finishReason != "" {
		// omit — chunk is for a reasoning delta only
	}
	// delta: copy siblings if present (content, role, tool_calls)
	// and overlay reasoning_content.
	delta := make(map[string]any)
	if c0Delta, ok := source["delta"].(map[string]any); ok {
		for k, v := range c0Delta {
			if k == "reasoning_details" || k == "reasoning_content" {
				continue
			}
			delta[k] = v
		}
	}
	delta["reasoning_content"] = content
	choice["delta"] = delta
	chunk["choices"] = []any{choice}

	out, err := json.Marshal(chunk)
	if err != nil {
		// Defensive: should be impossible for a chunk we already
		// parsed.
		return nil
	}
	return out
}

// marshalChunkForPassthrough returns the original line bytes if it
// round-trips through the parsed map cleanly, or re-marshals the map
// if the keys are in canonical form. The intent is to avoid a
// re-marshal when possible (byte-identical on the hot path), and fall
// back to re-marshal when needed. For simplicity here we always
// re-marshal the parsed chunk — the marshal preserves the source's
// numeric float64 representations and the result is byte-equal to
// the original for OpenAI-shaped JSON (which is what we receive).
func marshalChunkForPassthrough(chunk map[string]any, original []byte) ([]byte, [][]byte, error) {
	out, err := json.Marshal(chunk)
	if err != nil {
		return original, nil, nil
	}
	return out, nil, nil
}

// bytesHasPrefix is the bytes.HasPrefix import-free equivalent. Tiny
// helper to keep the import list focused on the standard library
// packages we use.
func bytesHasPrefix(s, prefix []byte) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

// entryDebugSampling is a tiny per-process sampling guard for
// unknown-entry-type debug logs. The hot path is reasoning.text only;
// unknown types are rare so a 1-in-N rate is fine.
var entryDebugSampling = newSampler(64)

type sampler struct {
	n  uint64
	of uint64
}

func newSampler(of uint64) sampler { return sampler{of: of} }
func (s *sampler) Allow() bool     { s.n++; return s.n%s.of == 0 }
