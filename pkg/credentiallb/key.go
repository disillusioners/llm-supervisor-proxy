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
// (modelID + "|" + tokenID + "|" + firstUserMessage).
//
// A-1: the tokenID salt is mandatory in production wiring (post-auth
// source: rc.tokenID); it prevents templated-agent-fleet skew where
// many agents under DIFFERENT principals share a byte-identical first
// message. tokenID == "" (anonymous requests) degrades to the unsalted
// form — accepted residual risk (within-principal templated skew is
// documented for operators).
//
// A-2: when the first user message's content is multimodal
// ([]interface{}), ExtractFirstUserMessage returns the canonical JSON
// of the content (sorted keys, no whitespace), so the key is computed
// over that canonical form here.
//
// W-2: if firstUserMessage == "", the caller MUST skip the engine
// entirely and use weighted-random-without-affinity for that request
// (the engine itself also enforces this: GetOrSelect(..., "") returns
// a fresh pick with no binding stored).
//
// Full 64 hex chars, no truncation (birthday-paradox bound; same
// policy as ultimatemodel.HashMessages).
func ComputeConversationKey(modelID, tokenID, firstUserMessage string) string {
	sum := sha256.Sum256([]byte(modelID + "|" + tokenID + "|" + firstUserMessage))
	return hex.EncodeToString(sum[:])
}

// ExtractFirstUserMessage returns the canonical content of the first
// message with role=="user":
//   - string content → returned as-is
//   - []interface{} content (multimodal) → canonical JSON of the array
//     (sorted keys, no whitespace; per A-2 — the multimodal-as-""
//     fallback is REMOVED, all requests get affinity)
//   - user messages with missing/empty/null content are skipped in
//     favor of the NEXT user message; if no user message carries
//     usable content, or no user-role message exists → ""
//
// messages is the snapshot from rc.originalMessages
// ([]interface{} of map[string]interface{}).
func ExtractFirstUserMessage(messages []interface{}) string {
	for _, m := range messages {
		msg, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "user" {
			continue
		}
		switch content := msg["content"].(type) {
		case string:
			if content != "" {
				return content
			}
			// Empty string content: degenerate entry — skip in favor
			// of the next user message (plan Task 1 test).
		case []interface{}:
			if len(content) > 0 {
				return canonicalJSON(content)
			}
			// Empty array: same degenerate skip.
		}
		// Missing / null content: skip to the next user message.
	}
	return ""
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
