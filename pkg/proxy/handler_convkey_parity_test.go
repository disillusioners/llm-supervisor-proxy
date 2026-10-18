package proxy

import (
	"encoding/json"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
)

// TestConversationKey_OpenAIAnthropicExtractionParity — spec item (f):
// the OpenAI (/v1/chat/completions) and Anthropic (/v1/messages)
// handler paths must produce the SAME conversation key for the same
// LOGICAL message list. The OpenAI path feeds rc.originalMessages
// ([]interface{} of role/content maps) straight into
// credentiallb.ExtractLeadingUserRun; the Anthropic path converts its
// typed []translator.AnthropicMessage via arcMessagesAsInterfaces
// first. Both must land on the same leading user run — and therefore
// the same token-salted key — so a conversation keeps its sticky
// binding across both wire protocols.
func TestConversationKey_OpenAIAnthropicExtractionParity(t *testing.T) {
	cases := []struct {
		name      string
		openAI    []interface{}
		anthropic []translator.AnthropicMessage
		wantRun   string // "" ⇒ both paths must yield the empty run
	}{
		{
			name: "single user message",
			openAI: []interface{}{
				map[string]interface{}{"role": "user", "content": "Hello, world"},
			},
			anthropic: []translator.AnthropicMessage{
				{Role: "user", Content: "Hello, world"},
			},
			wantRun: `["Hello, world"]`,
		},
		{
			name: "system preamble then consecutive user run",
			openAI: []interface{}{
				map[string]interface{}{"role": "system", "content": "you are terse"},
				map[string]interface{}{"role": "user", "content": "task one"},
				map[string]interface{}{"role": "system", "content": "extra context"},
				map[string]interface{}{"role": "user", "content": "task two"},
			},
			anthropic: []translator.AnthropicMessage{
				{Role: "user", Content: "task one"},
				{Role: "user", Content: "task two"},
			},
			wantRun: `["task one","task two"]`,
		},
		{
			name: "assistant terminates the leading run",
			openAI: []interface{}{
				map[string]interface{}{"role": "user", "content": "q"},
				map[string]interface{}{"role": "assistant", "content": "a"},
				map[string]interface{}{"role": "user", "content": "follow-up"},
			},
			anthropic: []translator.AnthropicMessage{
				{Role: "user", Content: "q"},
				{Role: "assistant", Content: "a"},
				{Role: "user", Content: "follow-up"},
			},
			wantRun: `["q"]`,
		},
		{
			name: "assistant-first degenerate conversation",
			openAI: []interface{}{
				map[string]interface{}{"role": "assistant", "content": "prefilled"},
				map[string]interface{}{"role": "user", "content": "hello"},
			},
			anthropic: []translator.AnthropicMessage{
				{Role: "assistant", Content: "prefilled"},
				{Role: "user", Content: "hello"},
			},
			wantRun: "",
		},
		{
			name: "multimodal user message",
			openAI: []interface{}{
				map[string]interface{}{"role": "user", "content": []interface{}{
					map[string]interface{}{"type": "text", "text": "look"},
					map[string]interface{}{"type": "image", "url": "x"},
				}},
			},
			anthropic: []translator.AnthropicMessage{
				// Content as []interface{} exercises arcMessagesAsInterfaces'
				// pass-through branch — the wire-equivalent of the OpenAI
				// shape, so the canonical texts line up byte-for-byte.
				{Role: "user", Content: []interface{}{
					map[string]interface{}{"type": "text", "text": "look"},
					map[string]interface{}{"type": "image", "url": "x"},
				}},
			},
			wantRun: `[[{"text":"look","type":"text"},{"type":"image","url":"x"}]]`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotOpenAI := credentiallb.ExtractLeadingUserRun(tc.openAI)
			gotAnthropic := credentiallb.ExtractLeadingUserRun(arcMessagesAsInterfaces(tc.anthropic))

			if gotOpenAI != tc.wantRun {
				t.Fatalf("openai run: got %q want %q", gotOpenAI, tc.wantRun)
			}
			if gotOpenAI != gotAnthropic {
				t.Fatalf("parity violated: openai=%q anthropic=%q", gotOpenAI, gotAnthropic)
			}

			ka := credentiallb.ComputeConversationKey("m1", "tok1", gotOpenAI)
			kb := credentiallb.ComputeConversationKey("m1", "tok1", gotAnthropic)
			if ka != kb {
				t.Fatalf("key parity violated: openai=%s anthropic=%s", ka, kb)
			}
		})
	}
}

// TestConversationKey_AnthropicTopLevelSystemExcluded — parity-gap
// case: the Anthropic wire carries the system prompt in a TOP-LEVEL
// `system` field, not as a message. Round-trip an
// AnthropicRequest{System: …, Messages: …} through JSON (the shape
// HandleMessages actually parses) and pin that the top-level system
// NEVER enters the leading user run or the key — symmetric in effect
// with the OpenAI system-message SKIP exercised above.
func TestConversationKey_AnthropicTopLevelSystemExcluded(t *testing.T) {
	req := translator.AnthropicRequest{
		System: "you are terse",
		Messages: []translator.AnthropicMessage{
			{Role: "user", Content: "task one"},
			{Role: "user", Content: "task two"},
		},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal anthropic request: %v", err)
	}
	var parsed translator.AnthropicRequest
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal anthropic request: %v", err)
	}
	if parsed.System == nil {
		t.Fatalf("round-trip lost the top-level system field; case would pass vacuously")
	}

	// OpenAI wire-equivalent: the same logical conversation with the
	// system prompt as inline system messages (SKIPped by the walk).
	openAI := []interface{}{
		map[string]interface{}{"role": "system", "content": "you are terse"},
		map[string]interface{}{"role": "user", "content": "task one"},
		map[string]interface{}{"role": "user", "content": "task two"},
	}
	wantRun := `["task one","task two"]`

	gotOpenAI := credentiallb.ExtractLeadingUserRun(openAI)
	gotAnthropic := credentiallb.ExtractLeadingUserRun(arcMessagesAsInterfaces(parsed.Messages))

	if gotOpenAI != wantRun || gotAnthropic != wantRun {
		t.Fatalf("run mismatch: openai=%q anthropic=%q want %q", gotOpenAI, gotAnthropic, wantRun)
	}
	if gotOpenAI != gotAnthropic {
		t.Fatalf("parity violated: openai=%q anthropic=%q", gotOpenAI, gotAnthropic)
	}

	ka := credentiallb.ComputeConversationKey("m1", "tok1", gotOpenAI)
	kb := credentiallb.ComputeConversationKey("m1", "tok1", gotAnthropic)
	if ka != kb {
		t.Fatalf("key parity violated: openai=%s anthropic=%s", ka, kb)
	}
}
