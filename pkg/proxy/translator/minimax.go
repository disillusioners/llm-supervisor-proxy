package translator

import (
	"encoding/json"
	"fmt"
)

// ─────────────────────────────────────────────────────────────────────────────
// MiniMax reasoning_details ↔ reasoning_content request-side translation
// ─────────────────────────────────────────────────────────────────────────────
//
// Scope: EXTERNAL request paths only (race-external + ultimate-external per D2).
// Internal request paths (race-internal + ultimate-internal) carry the
// reasoning details via typed fields on providers.ChatMessage (D1) and the
// typed ReasoningSplit *bool on providers.ChatCompletionRequest.
//
// Thread safety: NOT goroutine-safe. Each translator function mutates the
// caller-supplied map in place; the bytes wrapper allocates a fresh map.
// Gate (MiniMax provider + flag) is the caller's job — translator functions
// run unconditionally on the input body.
// ─────────────────────────────────────────────────────────────────────────────

// ReasoningDetail mirrors a single MiniMax wire-format reasoning_details
// entry. JSON tags match the MiniMax wire shape; omitempty on all fields
// keeps unknown future sub-fields forward-compatible on round-trip.
type ReasoningDetail struct {
	Type   string `json:"type,omitempty"`
	ID     string `json:"id,omitempty"`
	Format string `json:"format,omitempty"`
	Index  int    `json:"index,omitempty"`
	Text   string `json:"text,omitempty"`
}

// reasoningTextType is the MiniMax wire type for plain reasoning text.
const reasoningTextType = "reasoning.text"

// reasoningTextFormat is the MiniMax wire format identifier for v1.
const reasoningTextFormat = "MiniMax-response-v1"

// reasoningTextIDPrefix is prepended to the per-message counter to build a
// stable identifier (e.g. "reasoning-text-1").
const reasoningTextIDPrefix = "reasoning-text-"

// TranslateRequestBody is the request-side map-core translator. It mutates
// the supplied body in place to convert client-supplied
// `messages[i].reasoning_content` strings into MiniMax
// `messages[i].reasoning_details` arrays and to inject the top-level
// `reasoning_split: true` flag.
//
// Not goroutine-safe; the gate (MiniMax credential + flag header) is the
// caller's responsibility. For typed-struct request sites (internal paths),
// use the bytes wrapper or call the sub-functions directly.
//
// Returns a wrapped error only if the body is nil or the messages array is
// malformed (not []any). On error, the map is left unmutated.
func TranslateRequestBody(body map[string]any) error {
	if err := TranslateMessagesReasoning(body); err != nil {
		return err
	}
	return InjectReasoningSplit(body)
}

// TranslateRequestBytes is the request-side bytes wrapper. It parses the
// body, runs the map-core translator, and re-marshals the result. Use this
// on verbatim-byte call sites that already hold request bytes and have no
// usable map representation.
//
// Not goroutine-safe; the gate is the caller's responsibility.
//
// Returns a wrapped error if the input is not valid JSON, the map shape is
// invalid, or re-marshal fails. On parse error, no bytes are returned.
func TranslateRequestBytes(body []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("TranslateRequestBytes: parse body: %w", err)
	}
	if err := TranslateRequestBody(m); err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// TranslateMessagesReasoning walks body["messages"] and, for each message
// carrying a non-empty reasoning_content string, replaces it with a
// reasoning_details array of one entry (type "reasoning.text",
// format "MiniMax-response-v1", index 0, id "reasoning-text-<n>"). The
// per-message counter <n> starts at 1 and is monotonic across the slice.
// The original reasoning_content field is deleted (strip-and-replace).
// Messages with empty or absent reasoning_content are left untouched.
//
// Not goroutine-safe; the gate is the caller's responsibility.
//
// Returns a wrapped error if body is nil or messages is present but not a
// []any. On error, the map is left unmutated.
func TranslateMessagesReasoning(body map[string]any) error {
	if body == nil {
		return fmt.Errorf("TranslateMessagesReasoning: body is nil")
	}
	rawMessages, exists := body["messages"]
	if !exists {
		return nil // absent messages ⇒ nothing to do
	}
	messages, ok := rawMessages.([]any)
	if !ok {
		return fmt.Errorf("TranslateMessagesReasoning: messages is %T, expected []any", rawMessages)
	}

	counter := 0
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue // non-map entries are passed through untouched
		}
		rc, present := msg["reasoning_content"]
		if !present {
			continue
		}
		text, ok := rc.(string)
		if !ok || text == "" {
			// empty / non-string reasoning_content ⇒ leave untouched
			continue
		}
		counter++
		msg["reasoning_details"] = []any{
			ReasoningDetail{
				Type:   reasoningTextType,
				ID:     fmt.Sprintf("%s%d", reasoningTextIDPrefix, counter),
				Format: reasoningTextFormat,
				Index:  0,
				Text:   text,
			},
		}
		// strip-and-replace: drop the original reasoning_content field
		delete(msg, "reasoning_content")
	}
	return nil
}

// InjectReasoningSplit sets body["reasoning_split"] = true at the top
// level. Top-level placement is the documented choice (Q8, R7): the MiniMax
// SDK reads reasoning_split from the body root, not from inside any nested
// object, and a top-level key survives the bodyCopy shallow-copy semantics
// used by the ultimate-external path.
//
// Not goroutine-safe; the gate is the caller's responsibility.
//
// Returns a wrapped error if body is nil. The map is left unmutated on
// error.
func InjectReasoningSplit(body map[string]any) error {
	if body == nil {
		return fmt.Errorf("InjectReasoningSplit: body is nil")
	}
	body["reasoning_split"] = true
	return nil
}
