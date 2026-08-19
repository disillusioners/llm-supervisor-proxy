package translator

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
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
// level formatDriftCounter (atomic) and its per-format dedup set (a
// sync.Map) are the only shared state.
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
// It counts DISTINCT unknown format values, not entry volume — repeated
// entries carrying the same unknown format value increment it only ONCE
// process-wide (P2-9: "doesn't double-increment for repeated identical
// drift"). The getter is exposed for the Phase-3 verification report.
var formatDriftCounter atomic.Uint64

// seenUnknownFormats is the process-wide dedup set backing the
// distinct-value semantics of formatDriftCounter. LoadOrStore returning
// loaded=false is the "first time this format value was observed" signal
// that authorizes exactly one counter increment.
var seenUnknownFormats sync.Map

// FormatDriftCount returns the current value of the format-drift counter.
// Exported for verification reporting (P3-6); not used by the proxy itself.
func FormatDriftCount() uint64 {
	return formatDriftCounter.Load()
}

// package-private counter incremented by recordDrift on every
// observation (not deduped). The deduped formatDriftCounter
// (above) counts DISTINCT values; this one counts TOTAL
// observations. The sampled WARN log is gated on this counter
// (1-in-N) so a hot stream of unknown-format chunks remains
// observable while the dedup counter stays a faithful "how many
// distinct formats drifted" metric.
var formatDriftObserved atomic.Uint64

// FormatDriftObservationCount returns the current value of the
// format-drift observation counter (one increment per recordDrift
// call, NOT deduped). Exported for tests (W7).
func FormatDriftObservationCount() uint64 {
	return formatDriftObserved.Load()
}

// recordDrift records one unknown-format observation. The deduped
// formatDriftCounter counts DISTINCT unknown format values
// (each distinct value increments it exactly ONCE process-wide);
// the formatDriftObserved counter increments on every call so
// the sampled WARN log is gated on a counter that reflects
// actual observation volume (W7 — without this, the sampled
// WARN could never fire when the dedup counter alone is used:
// dedup of repeated format values would suppress the log
// signal even on heavy drift).
func recordDrift(entry map[string]any) {
	formatDriftObserved.Add(1)
	if formatVal, ok := entry["format"].(string); ok {
		if _, dup := seenUnknownFormats.LoadOrStore(formatVal, struct{}{}); !dup {
			formatDriftCounter.Add(1)
		}
	}
	// Sample the WARN log on the observation counter (NOT the
	// dedup counter) so volume-of-observations drives the log
	// rate. 1-in-64 keeps line ratio tolerable for live
	// verification.
	if formatDriftObserved.Load()%formatDriftLogEvery != 0 {
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
	_, err := translateNonStreamResponseBody(body)
	return err
}

// translateNonStreamResponseBody is the package-private core of
// TranslateNonStreamResponseBody. It returns the same error semantics and,
// additionally, whether the body was MUTATED (changed=true) or left
// untouched (changed=false). "Untouched" means: no reasoning_details key
// was read-and-processed, no audio/audio_content/name key was deleted, and
// nothing else was written. TranslateNonStreamResponseBytes uses the flag
// to skip a pointless re-marshal when the translator is a semantic no-op
// on the input (N-1/Q3 — data-absent gate-ON responses keep their
// original byte formatting: key order, number formatting, whitespace).
//
// changed=true covers every path that touches the map: the audio/name
// strip counts as a modification ONLY when one of those keys was actually
// present (delete of an absent key is a no-op, verified via the
// two-value map lookup); reading + deleting reasoning_details counts;
// overwriting reasoning_content under the single-winner rule counts.
func translateNonStreamResponseBody(body map[string]any) (changed bool, err error) {
	if body == nil {
		return false, fmt.Errorf("TranslateNonStreamResponseBody: body is nil")
	}
	rawChoices, exists := body["choices"]
	if !exists {
		return false, nil // no choices → nothing to translate
	}
	choices, ok := rawChoices.([]any)
	if !ok {
		return false, fmt.Errorf("TranslateNonStreamResponseBody: choices is %T, expected []any", rawChoices)
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
		// Per-message changed flag (W8). The strip of MiniMax-only
		// sibling metadata (audio / audio_content / name) is
		// GATED on translation having actually fired for THIS
		// message — we do not strip on messages that have no
		// reasoning_details at all. This keeps gate-ON
		// responses that carry no reasoning_details byte-identical
		// to upstream (no strip = no re-marshal). Translation
		// "fired" here means: the message had a reasoning_details
		// key (any value — non-empty array, empty array, or
		// wrong type all count).
		msgChanged := false
		rawDetails, hasDetails := msg["reasoning_details"]
		if hasDetails {
			msgChanged = true
			// Walk reasoning_details and build the concatenated
			// text into ReasoningContent. Single-winner (W6/D2):
			// when details are present and non-empty, the details
			// array WINS and any pre-existing reasoning_content
			// is DISCARDED entirely (no dedup-to-nothing, no
			// O(n²) containment drops — just replace). When
			// details are absent, the existing reasoning_content
			// is preserved untouched.
			details, ok := rawDetails.([]any)
			if !ok || len(details) == 0 {
				// Wrong type or empty array: clear details
				// and leave reasoning_content alone.
				delete(msg, "reasoning_details")
			} else {
				// details present and non-empty → reasoning_details WINS.
				// Build a fresh accumulator from the entries'
				// text. Any pre-existing reasoning_content is
				// DISCARDED (W6 true single-winner — no dedup,
				// no concatenation). Per-entry processing:
				// H7 skip-empty, unknown-type skip,
				// intra-array dedup via containment.
				var b strings.Builder
				for _, rawEntry := range details {
					entry, ok := rawEntry.(map[string]any)
					if !ok {
						continue
					}
					// Unknown entry type → log-and-skip.
					t, _ := entry["type"].(string)
					if t != "" && t != reasoningTextType {
						if entryDebugSamplingAllowed() {
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
					// Intra-array dedup (containment): if we
					// already emitted this text in the same
					// array, skip. Protects against
					// cumulative-mode replay and duplicates
					// within a single details array.
					if b.Len() > 0 && strings.Index(b.String(), text) >= 0 {
						continue
					}
					b.WriteString(text)
				}
				msg["reasoning_content"] = b.String()
				delete(msg, "reasoning_details")
			}
		}
		if msgChanged {
			// Translation actually fired for this message —
			// strip the MiniMax-only sibling metadata fields so
			// they do not leak upstream semantics to OpenAI
			// clients. Done AFTER the reasoning pass to keep the
			// rest of the message intact. Each strip counts as
			// a modification only when the key was actually
			// present (delete of an absent key is a map no-op).
			if _, present := msg["audio"]; present {
				delete(msg, "audio")
			}
			if _, present := msg["audio_content"]; present {
				delete(msg, "audio_content")
			}
			if _, present := msg["name"]; present {
				delete(msg, "name")
			}
			changed = true
		}
	}
	return changed, nil
}

// TranslateNonStreamResponseBytes is the response-side bytes wrapper. It
// parses the body, runs the map-core translator, and re-marshals the
// result. Use this on verbatim-byte call sites that already hold response
// bytes and have no usable map representation (notably ultim-ext
// non-stream per §5.3 — the only verbatim path).
//
// Data-absent fast path (N-1/Q3): when the map-core translator reports
// that nothing was modified (no reasoning_details anywhere AND none of
// the audio/audio_content/name strip keys present), the ORIGINAL input
// bytes are returned untouched — no re-marshal, so key order, number
// formatting and whitespace survive exactly as upstream sent them. This
// keeps gate-ON responses that carry no reasoning_details (ordinary
// MiniMax responses using reasoning_content only, or neither field)
// byte-identical to what the upstream produced, instead of reformatted
// (alphabetized keys, float64 re-encoded) for zero semantic benefit.
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
	changed, err := translateNonStreamResponseBody(m)
	if err != nil {
		return nil, err
	}
	if !changed {
		// N-1/Q3: semantic no-op — pass the original bytes through
		// without re-marshaling so upstream byte formatting is
		// preserved on data-absent gate-ON responses.
		return body, nil
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

// ChunkBytes processes one SSE line. The input is an SSE line WITH or
// WITHOUT the `data: ` prefix and WITHOUT the trailing `\n\n` (W9 —
// mirrors the call-site pattern at translator/stream.go:42-46). If the
// prefix is present, it is stripped first.
//
// Returned values:
//
//   - stripped: the line as it should be written downstream.
//
//     (a) UNCHANGED path — when the translator made NO mutation for
//     this line (no choice carried reasoning_details): stripped is
//     the ORIGINAL input line bytes VERBATIM, including whatever
//     framing the caller supplied (the `data: ` prefix and any
//     trailing newline). C1 mirrors the non-stream fast-path
//     behavior from commit 3e7eba9: prefix, key order, number
//     formatting, whitespace all preserved exactly. In this case
//     emitted is nil.
//
//     (b) MUTATED path — when ANY choice's delta carried
//     reasoning_details present-and-non-empty (single-winner rule
//     at line 515): stripped is the re-marshaled line, FRAMED as a
//     complete SSE event (`data: ` + raw JSON + `\n\n`), with the
//     details stripped from the delta and delta.reasoning_content
//     deleted (W4 single-winner on the wire). emitted is non-nil
//     and contains the per-entry reasoning_content chunks, each
//     ALSO framed as `data: ` + raw JSON + `\n\n`.
//
//   - emitted: zero or more client chunks to write IN ORDER, each
//     ALREADY framed as `data: ` + raw JSON + `\n\n` on the mutated
//     path (per (b) above). On the unchanged path, emitted is nil.
//     The caller writes stripped followed by each emitted[i] in
//     order; because the translator pre-frames both, the caller
//     performs ZERO additional framing — the C1 uniform contract
//     holds at the call-site level: every returned line is a
//     fully-framed SSE event, mutated or unchanged.
//
//   - err: ALWAYS nil. Per §6.1, the stream translator is error-free
//     by construction; anomalies are logged as sampled WARN and the
//     line is passed through (or with reasoning_details stripped if
//     the line parsed). The error return is preserved on the API for
//     future-proofing the §6.1 contract.
//
// Call-site newline handling is the CALLER's concern, not the
// translator's. Two of the three call sites today have slightly
// different input shapes (documented here, NOT normalized — see C1
// framing nuance note below):
//
//   - ultimate-external (handler_external.go:300-309) reads via
//     `reader.ReadString('\n')`, so the input line INCLUDES its
//     trailing `\n`. The translator's `\n\n` terminator on the
//     mutated path adds a second `\n`, producing `…\n\n` (SSE-legal).
//     On the unchanged path the verbatim return carries the
//     caller's `\n` (SSE-legal terminator).
//   - race-external (race_executor.go:1294-1301) strips the trailing
//     `\n` BEFORE passing to the translator, then appends a `\n` via
//     streamBuffer.Add (stream_buffer.go:119) when the chunk is
//     queued. The translator's `\n\n` terminator on the mutated
//     path adds the missing framing, then the buffer adds its `\n`,
//     producing `…\n\n\n` (triple-newline, SSE-legal — multiple
//     consecutive `\n` are valid message terminators per SSE spec).
//     On the unchanged path the verbatim return is newline-stripped
//     by the caller, then the buffer adds its `\n`, producing
//     `…\n` (SSE-legal).
//
// The framing contract from the translator's perspective is
// uniform: every returned line is fully framed. The `\n\n\n`
// vs `\n` variance is a property of the caller's input + buffer
// combination, not a translator contract.
//
// Algorithm (C1, W3, W4, W6):
//
//  1. Parse-failure → (line, nil, nil) passthrough VERBATIM (C1
//     guard — never re-marshal a line we did not parse).
//  2. If no choices array → return the ORIGINAL line VERBATIM
//     (no mutation occurred; passthrough byte-identical to upstream).
//  3. Walk ALL choices (W3 — was choices[0]-only, leaked details on
//     choices[1+]). For each choice with delta.reasoning_details:
//     a. Delete reasoning_details and delta.reasoning_content (W4
//     single-winner on the wire — when details win, the
//     content string is discarded entirely).
//     b. For each entry: H7 skip-empty, unknown-type skip, format
//     drift counter; H2 dedup vs lastIssued[choiceIdx]
//     (containment); cumulative-suffix mode (text is strict
//     superset of lastIssued[choiceIdx] → emit suffix only).
//     c. Emit per-entry chunks (raw JSON, framed by frameSSELine
//     in step 5).
//  4. If no choice had reasoning_details, return the ORIGINAL line
//     VERBATIM (C1 unchanged-on-passthrough guarantee).
//  5. Otherwise re-marshal the chunk, frame it (data: + \n\n), and
//     frame each emitted chunk the same way (uniform contract).
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

	// Step 0: remember the original line for verbatim passthrough.
	// The C1 guarantee is that when the translator made NO mutation
	// for this line, the original bytes are returned — prefix,
	// key order, number formatting, whitespace all preserved.
	originalLine := line

	// Step 1: strip `data: ` prefix if present. W9 — the caller's
	// contract is to pass the line WITHOUT the prefix, but we strip
	// it if it's there to mirror translator/stream.go:42-46
	// precedent (chunks can be normalized with or without prefix
	// upstream).
	prefix := []byte("data: ")
	hadPrefix := bytesHasPrefix(line, prefix)
	if hadPrefix {
		line = line[len(prefix):]
	}

	// Step 2: parse the JSON payload. Parse-failure → passthrough
	// VERBATIM (C1 — never re-marshal a line we did not parse, so
	// upstream byte formatting survives exactly).
	var chunk map[string]any
	if jerr := json.Unmarshal(line, &chunk); jerr != nil {
		log.Printf("[WARN] minimax stream chunk parse failure: %v", jerr)
		return originalLine, nil, nil
	}

	// Step 3: locate choices[].delta.reasoning_details.
	// W3 — walk ALL choices, not just choices[0]. The per-choice
	// lastIssued state map is keyed by choice index, so this is a
	// natural extension.
	rawChoices, hasChoices := chunk["choices"].([]any)
	if !hasChoices || len(rawChoices) == 0 {
		// No choices array at all → C1 verbatim passthrough.
		return originalLine, nil, nil
	}

	// Walk all choices. If NO choice carries reasoning_details,
	// this is a no-op and we return the ORIGINAL line VERBATIM.
	mutated := false
	for _, rawChoice := range rawChoices {
		choice, ok := rawChoice.(map[string]any)
		if !ok {
			continue
		}
		delta, hasDelta := choice["delta"].(map[string]any)
		if !hasDelta {
			continue
		}
		rawDetails, hasDetails := delta["reasoning_details"]
		if !hasDetails {
			continue
		}
		mutated = true

		details, ok := rawDetails.([]any)
		if !ok || len(details) == 0 {
			// Wrong type or empty: strip the key (R11 — no leak) and
			// continue. Per symmetry with the non-stream path, leave
			// delta.reasoning_content untouched here — the single-winner
			// rule only fires when details are present AND non-empty.
			delete(delta, "reasoning_details")
			continue
		}

		// W4 — when reasoning_details is present and non-empty, delete
		// the delta's reasoning_content (true single-winner on the
		// wire: the details array wins, the string is discarded).
		// Mirrors the W6 single-winner rule on the non-stream path
		// and on the typed openai.go path.
		delete(delta, "reasoning_content")

		// Determine the choice index from the choice map.
		choiceIdx := 0
		if v, ok := choice["index"].(float64); ok {
			choiceIdx = int(v)
		}

		// Build emitted chunks in array order. We have to be
		// careful that emit-time and state-update are atomic
		// w.r.t. each entry so a future change can re-derive
		// state.lastIssued from emitted text if needed.
		for _, rawEntry := range details {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				continue
			}
			// Unknown entry type → log-and-skip.
			t2, _ := entry["type"].(string)
			if t2 != "" && t2 != reasoningTextType {
				if entryDebugSamplingAllowed() {
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

			// H2 dedup vs the last emitted text for this
			// choice. Containment (strings.Index) — not
			// equality — so cumulative upstream emission
			// (which contains the prior text) is detected
			// and emitted as a suffix only.
			prior := t.lastIssued[choiceIdx]
			if prior != "" && strings.Index(prior, text) >= 0 {
				// Already covered by prior emission →
				// skip.
				continue
			}

			var emittedText string
			if prior != "" && strings.HasPrefix(text, prior) {
				// Cumulative-suffix mode: text is a
				// strict superset of prior. Emit only
				// the suffix to avoid quadratic growth.
				emittedText = text[len(prior):]
			} else {
				emittedText = text
			}
			if emittedText == "" {
				// Cumulative mode + identical text →
				// no new emission. Update state to
				// the longer text and continue.
				t.lastIssued[choiceIdx] = text
				continue
			}

			// Build the emitted client chunk: same shape
			// as the original line, but with
			// reasoning_details removed and
			// delta.reasoning_content set to the suffix.
			emittedLine := buildReasoningContentChunk(chunk, choiceIdx, emittedText)
			emitted = append(emitted, emittedLine)
			t.lastIssued[choiceIdx] = text
		}

		// Strip reasoning_details from this choice's delta
		// (the corresponding emitted chunks already carry
		// the content via delta.reasoning_content).
		delete(delta, "reasoning_details")
	}

	if !mutated {
		// C1 — no choice had reasoning_details ⇒ no mutation
		// occurred. Return the ORIGINAL line VERBATIM. This
		// is the byte-identical fast path that mirrors the
		// non-stream fast path from commit 3e7eba9: gate-ON
		// passthrough lines become byte-identical to upstream
		// (today they are mangled by an unnecessary re-marshal).
		return originalLine, nil, nil
	}

	// Mutation occurred: re-marshal the chunk and return a
	// FRAMED SSE line as stripped (`data: ` + raw JSON +
	// `\n\n`). The C1 uniform-framing contract means callers
	// never have to distinguish mutated vs unchanged — every
	// returned line is a fully-framed SSE event.
	strippedRaw, mErr := json.Marshal(chunk)
	if mErr != nil {
		// Marshal failure is unexpected for a chunk we just
		// parsed; log and return the ORIGINAL line VERBATIM
		// (C1 — never write re-marshaled bytes we cannot
		// produce; fall back to the upstream line so the
		// client still sees valid SSE).
		log.Printf("[WARN] minimax stream chunk marshal failure: %v", mErr)
		return originalLine, nil, nil
	}
	stripped = frameSSELine(strippedRaw)
	// Frame each emitted chunk so the uniform-framing contract
	// holds on the mutated path.
	for i, e := range emitted {
		emitted[i] = frameSSELine(e)
	}
	return stripped, emitted, nil
}

// frameSSELine wraps a raw JSON payload as one fully-framed
// SSE event: `data: ` + payload + `\n\n`. Used by ChunkBytes
// on the mutated path so callers always receive framed
// lines (C1 uniform contract).
func frameSSELine(rawJSON []byte) []byte {
	out := make([]byte, 0, len(rawJSON)+8)
	out = append(out, []byte("data: ")...)
	out = append(out, rawJSON...)
	out = append(out, '\n', '\n')
	return out
}

// buildReasoningContentChunk builds one client chunk from a stripped
// source chunk by setting delta.reasoning_content and copying the rest
// of the envelope (id / object / created / model / usage if present).
// It does NOT include reasoning_details (the caller already deleted
// that key from the source chunk before calling this). It also does
// NOT carry finish_reason (the chunk is mid-stream reasoning; the
// finish_reason stays on the final chunk).
func buildReasoningContentChunk(source map[string]any, choiceIdx int, content string) []byte {
	chunk := make(map[string]any, len(source))
	for k, v := range source {
		if k == "choices" || k == "delta" {
			continue // we replace these below
		}
		chunk[k] = v
	}
	// Build the choices array with a single entry for our choiceIdx.
	// The wire format expects the choices array to align with the
	// chunk in question.
	srcChoices, _ := source["choices"].([]any)
	choice := make(map[string]any)
	if choiceIdx < len(srcChoices) {
		if c0, ok := srcChoices[choiceIdx].(map[string]any); ok {
			for k, v := range c0 {
				if k == "delta" {
					continue
				}
				// Per-entry emission must NOT carry a
				// finish_reason (the chunk is mid-stream
				// reasoning — finish_reason stays on
				// the final chunk).
				if k == "finish_reason" {
					continue
				}
				choice[k] = v
			}
		}
	}
	choice["index"] = float64(choiceIdx)
	// Build the per-entry reasoning delta. The emitted reasoning
	// chunk is reasoning-only — it does NOT carry other delta
	// siblings from the source choice (content / role / tool_calls
	// stay on the stripped chunk at the top of ChunkBytes' loop,
	// not on these per-entry chunks). An earlier draft of this
	// function tried to copy siblings from source["delta"], but
	// the lookup is at the wrong level (delta is nested at
	// source["choices"][choiceIdx]["delta"], not at the chunk
	// root), so the block was dead — it never executed and
	// emitted chunks have always been reasoning-only. Removed to
	// stop misleading future readers.
	choice["delta"] = map[string]any{"reasoning_content": content}
	chunk["choices"] = []any{choice}

	out, err := json.Marshal(chunk)
	if err != nil {
		// Defensive: should be impossible for a chunk we already
		// parsed.
		return nil
	}
	return out
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

// entryDebugSeen is the atomic counter backing the 1-in-N
// per-process sampling gate for unknown-entry-type debug logs.
// It mirrors the openai.go:639-647 pattern (atomic.Uint64 +
// Add(1) + modulo) so concurrent stream loops can call the
// Allow()-style helper race-free.
var entryDebugSeen atomic.Uint64

// entryDebugSamplingN is the inverse sampling rate (1 in N
// invocations are logged). The hot path is reasoning.text only;
// unknown types are rare so a 1-in-N rate is fine.
const entryDebugSamplingN = 64

// entryDebugSamplingAllowed returns true 1 in N invocations. The
// counter is atomic (entryDebugSeen) so concurrent stream loops
// are race-free.
func entryDebugSamplingAllowed() bool {
	return entryDebugSeen.Add(1)%entryDebugSamplingN == 0
}
