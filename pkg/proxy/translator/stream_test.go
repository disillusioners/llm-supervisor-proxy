package translator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type SSEEvent struct {
	EventType string
	Data      map[string]interface{}
}

func parseSSEEvents(output []byte) []SSEEvent {
	var events []SSEEvent
	scanner := bufio.NewScanner(bytes.NewReader(output))
	var currentEvent string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			var data map[string]interface{}
			json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data)
			events = append(events, SSEEvent{currentEvent, data})
			currentEvent = ""
		}
	}
	return events
}

func findEventsByType(events []SSEEvent, eventType string) []SSEEvent {
	var result []SSEEvent
	for _, e := range events {
		if e.EventType == eventType {
			result = append(result, e)
		}
	}
	return result
}

func TestGenerateAnthropicEvents_ThinkingOnly(t *testing.T) {
	openaiBuffer := `data: {"choices":[{"delta":{"reasoning_content":"Let me think..."}}]}
data: {"choices":[{"delta":{"reasoning_content":" More thoughts."}}]}
data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}
data: [DONE]
`
	result, err := TranslateBufferedStream([]byte(openaiBuffer), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	events := parseSSEEvents(result)

	contentBlockStarts := findEventsByType(events, string(EventContentBlockStart))
	if len(contentBlockStarts) != 1 {
		t.Errorf("expected 1 content_block_start, got %d", len(contentBlockStarts))
	}

	block0 := contentBlockStarts[0]
	if block0.Data["index"].(float64) != 0 {
		t.Errorf("expected block index 0, got %v", block0.Data["index"])
	}

	cb, ok := block0.Data["content_block"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected content_block in data")
	}
	if cb["type"] != "thinking" {
		t.Errorf("expected block type 'thinking', got %v", cb["type"])
	}

	contentBlockDeltas := findEventsByType(events, string(EventContentBlockDelta))
	if len(contentBlockDeltas) != 1 {
		t.Errorf("expected 1 content_block_delta, got %d", len(contentBlockDeltas))
	}

	delta0 := contentBlockDeltas[0]
	delta, ok := delta0.Data["delta"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected delta in data")
	}
	if delta["type"] != "thinking_delta" {
		t.Errorf("expected delta type 'thinking_delta', got %v", delta["type"])
	}
	if delta["thinking"] != "Let me think... More thoughts." {
		t.Errorf("expected thinking content, got %v", delta["thinking"])
	}

	contentBlockStops := findEventsByType(events, string(EventContentBlockStop))
	if len(contentBlockStops) != 1 {
		t.Errorf("expected 1 content_block_stop, got %d", len(contentBlockStops))
	}

	textEvents := findEventsByType(events, string(EventContentBlockStart))
	for _, e := range textEvents {
		cb, _ := e.Data["content_block"].(map[string]interface{})
		if cb["type"] == "text" {
			t.Error("should NOT have text block when only thinking content is present")
		}
	}
}

func TestGenerateAnthropicEvents_TextOnly(t *testing.T) {
	openaiBuffer := `data: {"choices":[{"delta":{"content":"Hello"}}]}
data: {"choices":[{"delta":{"content":" world!"}}]}
data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}
data: [DONE]
`
	result, err := TranslateBufferedStream([]byte(openaiBuffer), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	events := parseSSEEvents(result)

	contentBlockStarts := findEventsByType(events, string(EventContentBlockStart))
	if len(contentBlockStarts) != 1 {
		t.Errorf("expected 1 content_block_start, got %d", len(contentBlockStarts))
	}

	block0 := contentBlockStarts[0]
	if block0.Data["index"].(float64) != 0 {
		t.Errorf("expected block index 0, got %v", block0.Data["index"])
	}

	cb, ok := block0.Data["content_block"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected content_block in data")
	}
	if cb["type"] != "text" {
		t.Errorf("expected block type 'text', got %v", cb["type"])
	}

	contentBlockDeltas := findEventsByType(events, string(EventContentBlockDelta))
	if len(contentBlockDeltas) != 1 {
		t.Errorf("expected 1 content_block_delta, got %d", len(contentBlockDeltas))
	}

	delta0 := contentBlockDeltas[0]
	delta, ok := delta0.Data["delta"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected delta in data")
	}
	if delta["type"] != "text_delta" {
		t.Errorf("expected delta type 'text_delta', got %v", delta["type"])
	}
	if delta["text"] != "Hello world!" {
		t.Errorf("expected text content 'Hello world!', got %v", delta["text"])
	}

	contentBlockStops := findEventsByType(events, string(EventContentBlockStop))
	if len(contentBlockStops) != 1 {
		t.Errorf("expected 1 content_block_stop, got %d", len(contentBlockStops))
	}

	thinkingEvents := findEventsByType(events, string(EventContentBlockStart))
	for _, e := range thinkingEvents {
		cb, _ := e.Data["content_block"].(map[string]interface{})
		if cb["type"] == "thinking" {
			t.Error("should NOT have thinking block when only text content is present")
		}
	}
}

func TestGenerateAnthropicEvents_ThinkingAndText(t *testing.T) {
	openaiBuffer := `data: {"choices":[{"delta":{"reasoning_content":"Let me think..."}}]}
data: {"choices":[{"delta":{"reasoning_content":" More thoughts."}}]}
data: {"choices":[{"delta":{"content":"Here is the answer."}}]}
data: {"choices":[{"delta":{"content":" And more."}}]}
data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}
data: [DONE]
`
	result, err := TranslateBufferedStream([]byte(openaiBuffer), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	events := parseSSEEvents(result)

	contentBlockStarts := findEventsByType(events, string(EventContentBlockStart))
	if len(contentBlockStarts) != 2 {
		t.Errorf("expected 2 content_block_start (thinking + text), got %d", len(contentBlockStarts))
	}

	block0 := contentBlockStarts[0]
	if block0.Data["index"].(float64) != 0 {
		t.Errorf("expected first block index 0, got %v", block0.Data["index"])
	}
	cb0, _ := block0.Data["content_block"].(map[string]interface{})
	if cb0["type"] != "thinking" {
		t.Errorf("expected first block type 'thinking', got %v", cb0["type"])
	}

	block1 := contentBlockStarts[1]
	if block1.Data["index"].(float64) != 1 {
		t.Errorf("expected second block index 1, got %v", block1.Data["index"])
	}
	cb1, _ := block1.Data["content_block"].(map[string]interface{})
	if cb1["type"] != "text" {
		t.Errorf("expected second block type 'text', got %v", cb1["type"])
	}

	contentBlockDeltas := findEventsByType(events, string(EventContentBlockDelta))
	if len(contentBlockDeltas) != 2 {
		t.Errorf("expected 2 content_block_delta, got %d", len(contentBlockDeltas))
	}

	delta0 := contentBlockDeltas[0]
	deltaData0, _ := delta0.Data["delta"].(map[string]interface{})
	if deltaData0["type"] != "thinking_delta" {
		t.Errorf("expected first delta type 'thinking_delta', got %v", deltaData0["type"])
	}
	if contentBlockDeltas[0].Data["index"].(float64) != 0 {
		t.Errorf("expected first delta index 0, got %v", contentBlockDeltas[0].Data["index"])
	}

	delta1 := contentBlockDeltas[1]
	deltaData1, _ := delta1.Data["delta"].(map[string]interface{})
	if deltaData1["type"] != "text_delta" {
		t.Errorf("expected second delta type 'text_delta', got %v", deltaData1["type"])
	}
	if contentBlockDeltas[1].Data["index"].(float64) != 1 {
		t.Errorf("expected second delta index 1, got %v", contentBlockDeltas[1].Data["index"])
	}

	contentBlockStops := findEventsByType(events, string(EventContentBlockStop))
	if len(contentBlockStops) != 2 {
		t.Errorf("expected 2 content_block_stop, got %d", len(contentBlockStops))
	}

	stop0 := contentBlockStops[0]
	if stop0.Data["index"].(float64) != 0 {
		t.Errorf("expected first stop index 0, got %v", stop0.Data["index"])
	}

	stop1 := contentBlockStops[1]
	if stop1.Data["index"].(float64) != 1 {
		t.Errorf("expected second stop index 1, got %v", stop1.Data["index"])
	}
}

func TestGenerateAnthropicEvents_ThinkingTextAndToolCalls(t *testing.T) {
	openaiBuffer := `data: {"choices":[{"delta":{"reasoning_content":"Let me think..."}}]}
data: {"choices":[{"delta":{"content":"Here is the answer."}}]}
data: {"choices":[{"delta":{"tool_calls":[{"id":"call_abc123","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"SF\"}"}}]}}]}
data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}
data: [DONE]
`
	result, err := TranslateBufferedStream([]byte(openaiBuffer), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	events := parseSSEEvents(result)

	contentBlockStarts := findEventsByType(events, string(EventContentBlockStart))
	if len(contentBlockStarts) != 3 {
		t.Errorf("expected 3 content_block_start (thinking + text + tool_use), got %d", len(contentBlockStarts))
	}

	block0 := contentBlockStarts[0]
	cb0, _ := block0.Data["content_block"].(map[string]interface{})
	if cb0["type"] != "thinking" {
		t.Errorf("expected first block type 'thinking', got %v", cb0["type"])
	}

	block1 := contentBlockStarts[1]
	cb1, _ := block1.Data["content_block"].(map[string]interface{})
	if cb1["type"] != "text" {
		t.Errorf("expected second block type 'text', got %v", cb1["type"])
	}

	block2 := contentBlockStarts[2]
	if block2.Data["index"].(float64) != 2 {
		t.Errorf("expected third block index 2, got %v", block2.Data["index"])
	}
	cb2, _ := block2.Data["content_block"].(map[string]interface{})
	if cb2["type"] != "tool_use" {
		t.Errorf("expected third block type 'tool_use', got %v", cb2["type"])
	}
	if cb2["id"] != "call_abc123" {
		t.Errorf("expected tool id 'call_abc123', got %v", cb2["id"])
	}
	if cb2["name"] != "get_weather" {
		t.Errorf("expected tool name 'get_weather', got %v", cb2["name"])
	}

	contentBlockDeltas := findEventsByType(events, string(EventContentBlockDelta))
	if len(contentBlockDeltas) != 3 {
		t.Errorf("expected 3 content_block_delta, got %d", len(contentBlockDeltas))
	}

	toolDelta := contentBlockDeltas[2]
	toolDeltaData, _ := toolDelta.Data["delta"].(map[string]interface{})
	if toolDeltaData["type"] != "input_json_delta" {
		t.Errorf("expected tool delta type 'input_json_delta', got %v", toolDeltaData["type"])
	}

	contentBlockStops := findEventsByType(events, string(EventContentBlockStop))
	if len(contentBlockStops) != 3 {
		t.Errorf("expected 3 content_block_stop, got %d", len(contentBlockStops))
	}
}

func TestGenerateAnthropicEvents_ToolCallsOnly(t *testing.T) {
	openaiBuffer := `data: {"choices":[{"delta":{"tool_calls":[{"id":"call_abc123","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"SF\"}"}}]}}]}
data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}
data: [DONE]
`
	result, err := TranslateBufferedStream([]byte(openaiBuffer), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	events := parseSSEEvents(result)

	contentBlockStarts := findEventsByType(events, string(EventContentBlockStart))
	if len(contentBlockStarts) != 1 {
		t.Errorf("expected 1 content_block_start (tool_use only), got %d", len(contentBlockStarts))
	}

	block0 := contentBlockStarts[0]
	if block0.Data["index"].(float64) != 0 {
		t.Errorf("expected block index 0, got %v", block0.Data["index"])
	}
	cb, _ := block0.Data["content_block"].(map[string]interface{})
	if cb["type"] != "tool_use" {
		t.Errorf("expected block type 'tool_use', got %v", cb["type"])
	}

	thinkingEvents := findEventsByType(events, string(EventContentBlockStart))
	for _, e := range thinkingEvents {
		contentBlock, _ := e.Data["content_block"].(map[string]interface{})
		if contentBlock["type"] == "thinking" {
			t.Error("should NOT have thinking block when only tool_calls content is present")
		}
	}

	textEvents := findEventsByType(events, string(EventContentBlockStart))
	for _, e := range textEvents {
		contentBlock, _ := e.Data["content_block"].(map[string]interface{})
		if contentBlock["type"] == "text" {
			t.Error("should NOT have text block when only tool_calls content is present")
		}
	}
}

func TestGenerateAnthropicEvents_MultipleToolCalls(t *testing.T) {
	openaiBuffer := `data: {"choices":[{"delta":{"tool_calls":[{"id":"call_abc123","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"SF\"}"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_def456","type":"function","function":{"name":"get_time","arguments":"{}"}}]}}]}
data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}
data: [DONE]
`
	result, err := TranslateBufferedStream([]byte(openaiBuffer), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	events := parseSSEEvents(result)

	contentBlockStarts := findEventsByType(events, string(EventContentBlockStart))
	if len(contentBlockStarts) != 2 {
		t.Errorf("expected 2 content_block_start, got %d", len(contentBlockStarts))
	}

	block0 := contentBlockStarts[0]
	if block0.Data["index"].(float64) != 0 {
		t.Errorf("expected first block index 0, got %v", block0.Data["index"])
	}
	cb0, _ := block0.Data["content_block"].(map[string]interface{})
	if cb0["type"] != "tool_use" {
		t.Errorf("expected first block type 'tool_use', got %v", cb0["type"])
	}
	if cb0["id"] != "call_abc123" {
		t.Errorf("expected first tool id 'call_abc123', got %v", cb0["id"])
	}
	if cb0["name"] != "get_weather" {
		t.Errorf("expected first tool name 'get_weather', got %v", cb0["name"])
	}

	block1 := contentBlockStarts[1]
	if block1.Data["index"].(float64) != 1 {
		t.Errorf("expected second block index 1, got %v", block1.Data["index"])
	}
	cb1, _ := block1.Data["content_block"].(map[string]interface{})
	if cb1["type"] != "tool_use" {
		t.Errorf("expected second block type 'tool_use', got %v", cb1["type"])
	}
	if cb1["id"] != "call_def456" {
		t.Errorf("expected second tool id 'call_def456', got %v", cb1["id"])
	}
	if cb1["name"] != "get_time" {
		t.Errorf("expected second tool name 'get_time', got %v", cb1["name"])
	}

	contentBlockStops := findEventsByType(events, string(EventContentBlockStop))
	if len(contentBlockStops) != 2 {
		t.Errorf("expected 2 content_block_stop, got %d", len(contentBlockStops))
	}

	if contentBlockStops[0].Data["index"].(float64) != 0 {
		t.Errorf("expected first stop index 0, got %v", contentBlockStops[0].Data["index"])
	}
	if contentBlockStops[1].Data["index"].(float64) != 1 {
		t.Errorf("expected second stop index 1, got %v", contentBlockStops[1].Data["index"])
	}
}

func TestGenerateAnthropicEvents_EventOrder(t *testing.T) {
	openaiBuffer := `data: {"choices":[{"delta":{"reasoning_content":"Thinking..."}}]}
data: {"choices":[{"delta":{"content":"Answer."}}]}
data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}
data: [DONE]
`
	result, err := TranslateBufferedStream([]byte(openaiBuffer), "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("translation failed: %v", err)
	}

	events := parseSSEEvents(result)

	expectedOrder := []string{
		string(EventMessageStart),
		string(EventPing),
		string(EventContentBlockStart),
		string(EventContentBlockDelta),
		string(EventContentBlockStop),
		string(EventContentBlockStart),
		string(EventContentBlockDelta),
		string(EventContentBlockStop),
		string(EventMessageDelta),
		string(EventMessageStop),
	}

	if len(events) != len(expectedOrder) {
		t.Fatalf("expected %d events, got %d", len(expectedOrder), len(events))
	}

	for i, expected := range expectedOrder {
		if events[i].EventType != expected {
			t.Errorf("event %d: expected '%s', got '%s'", i, expected, events[i].EventType)
		}
	}
}
