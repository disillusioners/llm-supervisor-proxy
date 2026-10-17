package credentiallb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// ErrNoCredentials is returned by GetOrSelect when the model has no
// credentials configured. Exact sentinel per the contract (design
// note #5).
var ErrNoCredentials = errors.New("credentiallb: model has no credentials")

// ComputeConversationKey returns a 64-char hex SHA256 of
// (modelID + "|" + tokenID + "|" + leadingUserRun) where
// leadingUserRun is the canonical JSON ARRAY of the leading contiguous
// run of user-message contents (ExtractLeadingUserRun).
//
// A-1: the tokenID salt is mandatory in production wiring (post-auth
// source: rc.tokenID); it prevents templated-agent-fleet skew where
// many agents under DIFFERENT principals share a byte-identical
// leading run. tokenID == "" (anonymous requests) degrades to the
// unsalted form — accepted residual risk (within-principal templated
// skew is documented for operators).
//
// A-2: when a user message's content is multimodal ([]interface{}),
// ExtractLeadingUserRun collects it and canonicalJSON renders the
// whole run canonically (sorted keys, sorted array elements, no
// whitespace), so the key is computed over that canonical form here.
//
// RUN-JOIN COLLISION SAFETY: the run is hashed as one canonical JSON
// array, not a "|" concatenation — each element is JSON-escaped inside
// the brackets, so a raw "," (or "|") inside an element cannot split
// or alias an element boundary (["a|b"] and ["a","b"] render
// differently, and ["a,b"] cannot split into ["a","b"]).
//
// W-2: if leadingUserRun == "", the caller MUST skip the engine
// entirely and use weighted-random-without-affinity for that request
// (the engine itself also enforces this: GetOrSelect(..., "") returns
// a fresh pick with no binding stored).
//
// Full 64 hex chars, no truncation (birthday-paradox bound; same
// policy as ultimatemodel.HashMessages).
func ComputeConversationKey(modelID, tokenID, leadingUserRun string) string {
	sum := sha256.Sum256([]byte(modelID + "|" + tokenID + "|" + leadingUserRun))
	return hex.EncodeToString(sum[:])
}

// ExtractLeadingUserRun returns the canonical JSON ARRAY of the
// contents of the LEADING CONTIGUOUS RUN of user messages — the
// conversation-affinity input that superseded the retired
// first-user-message-only extraction (2026-10-17: the ensemble
// upstream inserts task context as MULTIPLE CONSECUTIVE user
// messages, so hashing only the first one lost most of the signal).
//
// Collection walk over the message list, from index 0 (each entry
// dispatches exactly once on its role):
//   - role == "system"  → SKIP (not collected; walk continues)
//   - role == "user"    → usable content COLLECTED (predicate below);
//     unusable content skipped — the walk continues either way
//   - role == "assistant" → STOP (run ends)
//   - any OTHER role (tool / function / developer / a non-map entry
//     with no role) → STOP (run ends)
//   - messages exhausted → STOP
//
// Per-message usable-content predicate (identical to the retired
// ExtractFirstUserMessage):
//   - string content, non-empty → collected as-is
//   - []interface{} (multimodal), non-empty → collected; canonicalized
//     by the outer canonicalJSON below (per A-2)
//   - missing / null / empty content → skipped, NOT terminating
//
// Returns:
//   - non-empty run → canonical JSON array text of the collected
//     contents: "[" + e0 + "," + e1 + ... + "]" — sorted object keys
//     AND sorted array-element order (the A-2 canonicalization, so a
//     reordered run hashes equal; pinned in key_test.go)
//   - empty run (no messages, system-only, assistant-first, tool
//     entry mid-run, ...) → "" → the caller's W-2 gate: fresh
//     weighted pick, no binding stored. NOTE this is a deliberate
//     semantic change vs the retired extractor, which searched for
//     the first user message ANYWHERE (past assistant/tool entries);
//     the leading run stops at the first non-system/non-user entry.
//
// messages is the snapshot from rc.originalMessages
// ([]interface{} of map[string]interface{}).
func ExtractLeadingUserRun(messages []interface{}) string {
	collected := make([]interface{}, 0, 8)
walk:
	for _, m := range messages {
		msg, ok := m.(map[string]interface{})
		if !ok {
			// Non-map entry: no role to dispatch on → the "any OTHER
			// role" rule terminates the run (malformed wire entry).
			break walk
		}
		switch role, _ := msg["role"].(string); role {
		case "system":
			// Context preamble may be interleaved with the opening
			// user messages; it neither joins the run nor ends it.
		case "user":
			switch content := msg["content"].(type) {
			case string:
				if content != "" {
					collected = append(collected, content)
				}
				// Empty string: degenerate entry — skipped, walk
				// continues (retired-extractor predicate, unchanged).
			case []interface{}:
				if len(content) > 0 {
					collected = append(collected, content)
				}
				// Empty array: same degenerate skip.
			}
			// Missing / null content: skipped, walk continues.
		default:
			// assistant / tool / function / developer / empty role:
			// the leading run ends here.
			break walk
		}
	}
	if len(collected) == 0 {
		return ""
	}
	// Hash the run as ONE canonical JSON array (collision-safe join —
	// elements are individually JSON-escaped so the "," inside an
	// element cannot split an element boundary; see
	// ComputeConversationKey). canonicalJSON renders object keys
	// and array elements sorted, so the run is content-order-invariant.
	return canonicalJSON(collected)
}

// canonicalJSON renders v as deterministic JSON text: object keys
// sorted lexicographically, no whitespace, and — for the A-2
// content-order-invariance requirement — ARRAY ELEMENTS are rendered
// first and then sorted by their canonical serialization, so the same
// multimodal parts in different order produce the same text (plan
// Task 1 / Files row #7 acceptance: "same content in different order
// produces the same hash"). Sorted keys alone cannot deliver array
// order-invariance; sorting rendered elements is what satisfies the
// pinned acceptance test.
func canonicalJSON(v interface{}) string {
	var b strings.Builder
	writeCanonicalJSON(&b, v)
	return b.String()
}

func writeCanonicalJSON(b *strings.Builder, v interface{}) {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, k)
			b.WriteByte(':')
			writeCanonicalJSON(b, val[k])
		}
		b.WriteByte('}')
	case []interface{}:
		// Render each element canonically, then sort the rendered
		// forms (content-order invariance per A-2).
		parts := make([]string, len(val))
		for i, item := range val {
			var eb strings.Builder
			writeCanonicalJSON(&eb, item)
			parts[i] = eb.String()
		}
		sort.Strings(parts)
		b.WriteByte('[')
		for i, p := range parts {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(p)
		}
		b.WriteByte(']')
	case string:
		writeJSONString(b, val)
	case bool:
		if val {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		b.WriteString(strconv.FormatFloat(val, 'g', -1, 64))
	case nil:
		b.WriteString("null")
	default:
		// Fallback for json.Number and any exotic type: marshal via
		// encoding/json (deterministic for scalars).
		raw, err := json.Marshal(val)
		if err != nil {
			b.WriteString("null")
			return
		}
		b.Write(raw)
	}
}

// writeJSONString appends a JSON-escaped string literal (delegates to
// encoding/json for the escaping rules — deterministic output).
func writeJSONString(b *strings.Builder, s string) {
	raw, err := json.Marshal(s)
	if err != nil {
		b.WriteString(`""`)
		return
	}
	b.Write(raw)
}
