package proxy

import (
	"encoding/json"
	"net/http"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// ProtocolAdapter - Abstracts differences between API protocols (OpenAI, Anthropic)
// ─────────────────────────────────────────────────────────────────────────────

// ProtocolAdapter defines the interface for translating between client protocols
// and the internal unified format (which is OpenAI-compatible for upstream).
//
// The flow is:
//  1. Client sends request in their protocol (OpenAI or Anthropic)
//  2. ParseRequest() normalizes to internal format + extracts metadata
//  3. ToUpstreamRequest() converts to OpenAI format for upstream
//  4. Response is received from upstream (OpenAI format)
//  5. Track response content for storage (content, thinking, tool calls)
//  6. ToClientResponse() converts OpenAI response back to client protocol
//  7. ToClientError() converts errors to client protocol format
type ProtocolAdapter interface {
	// Protocol returns the protocol name (e.g., "openai", "anthropic")
	Protocol() string

	// ParseRequest parses the incoming HTTP request and returns:
	// - The parsed request body as a map (client's native format)
	// - Normalized metadata for storage/logging
	// - Error if parsing fails
	ParseRequest(r *http.Request) (body map[string]interface{}, meta *RequestMetadata, err error)

	// ToUpstreamRequest converts the client request to OpenAI format for upstream.
	// The returned []byte is the JSON body to send to upstream.
	ToUpstreamRequest(body map[string]interface{}, modelMapping models.ModelsConfigInterface) ([]byte, error)

	// ToStoreMessages converts request messages to store format for logging.
	ToStoreMessages(body map[string]interface{}) []store.Message

	// ExtractUpstreamModel extracts the model name that should be used for upstream.
	// This may differ from the client's model due to mapping.
	ExtractUpstreamModel(body map[string]interface{}, modelMapping models.ModelsConfigInterface) string

	// IsStream returns true if this is a streaming request.
	IsStream(body map[string]interface{}) bool

	// ResponseWriter handles writing the response back to the client.
	// This abstracts the difference between streaming and non-streaming,
	// as well as protocol-specific response formats.
	ResponseWriter

	// ErrorWriter handles writing errors back to the client in protocol format.
	ErrorWriter
}

// RequestMetadata contains normalized metadata extracted from a request.
type RequestMetadata struct {
	ClientModel   string // Model name as sent by client
	UpstreamModel string // Model name to use for upstream (after mapping)
	IsStream      bool
	Parameters    map[string]interface{} // Additional parameters for logging
}

// ResponseWriter handles writing responses back to the client.
// This interface is designed for buffer-then-flush streaming pattern.
type ResponseWriter interface {
	// WriteNonStreamResponse writes a non-streaming response.
	// openaiResponse is the raw OpenAI-format response from upstream.
	WriteNonStreamResponse(w http.ResponseWriter, openaiResponse []byte) error

	// WriteBufferedStream writes a buffered streaming response.
	// openaiBuffer contains ALL chunks from upstream (including "data: [DONE]\n\n").
	// The adapter translates the buffer if needed and writes to client.
	WriteBufferedStream(w http.ResponseWriter, openaiBuffer []byte) error

	// SetStreamHeaders sets the appropriate headers for streaming responses.
	SetStreamHeaders(w http.ResponseWriter)
}

// ErrorWriter handles writing errors back to the client.
type ErrorWriter interface {
	// WriteError writes an error response.
	WriteError(w http.ResponseWriter, errorType, message string, statusCode int)

	// WriteStreamError writes an error as a streaming event.
	WriteStreamError(w http.ResponseWriter, errorType, message string)
}

// ─────────────────────────────────────────────────────────────────────────────
// ResponseState - Tracks accumulated response content for storage
// ─────────────────────────────────────────────────────────────────────────────

// ResponseState tracks the accumulated response content during a request.
// This is used to build the assistant message that gets stored in the request log.
type ResponseState struct {
	Content   string
	Thinking  string
	ToolCalls []store.ToolCall
}

// Reset clears the response state.
func (rs *ResponseState) Reset() {
	rs.Content = ""
	rs.Thinking = ""
	rs.ToolCalls = nil
}

// AppendContent appends text content to the response.
func (rs *ResponseState) AppendContent(text string) {
	rs.Content += text
}

// AppendThinking appends thinking content to the response.
func (rs *ResponseState) AppendThinking(text string) {
	rs.Thinking += text
}

// AddToolCall adds a tool call to the response.
func (rs *ResponseState) AddToolCall(tc store.ToolCall) {
	rs.ToolCalls = append(rs.ToolCalls, tc)
}

// ToAssistantMessage converts the response state to a store.Message.
func (rs *ResponseState) ToAssistantMessage() store.Message {
	msg := store.Message{
		Role:     "assistant",
		Content:  rs.Content,
		Thinking: rs.Thinking,
	}
	if len(rs.ToolCalls) > 0 {
		msg.ToolCalls = rs.ToolCalls
	}
	return msg
}

// ─────────────────────────────────────────────────────────────────────────────
// ResponseExtractor - Extracts content from OpenAI responses for storage
// ──────────────────────────────────────────────────────────────────────-------

// ResponseExtractor extracts content from OpenAI-format responses.
// This is used by both adapters since upstream is always OpenAI format.
type ResponseExtractor struct{}

// ExtractFromNonStream extracts content, thinking, and tool calls from a non-streaming response.
func (e *ResponseExtractor) ExtractFromNonStream(openaiResponse []byte) (state ResponseState, err error) {
	var resp map[string]interface{}
	if err := json.Unmarshal(openaiResponse, &resp); err != nil {
		return state, err
	}

	choices, _ := resp["choices"].([]interface{})
	if len(choices) == 0 {
		return state, nil
	}

	choice, _ := choices[0].(map[string]interface{})
	message, _ := choice["message"].(map[string]interface{})
	if message == nil {
		return state, nil
	}

	// Extract content
	if content, ok := message["content"].(string); ok {
		state.Content = content
	}

	// Extract thinking (from reasoning_content if present)
	if reasoning, ok := message["reasoning_content"].(string); ok {
		state.Thinking = reasoning
	}

	// Extract tool calls
	if toolCalls, ok := message["tool_calls"].([]interface{}); ok {
		for _, tc := range toolCalls {
			if tcMap, ok := tc.(map[string]interface{}); ok {
				toolCall := store.ToolCall{
					ID:   getString(tcMap, "id"),
					Type: getString(tcMap, "type"),
				}
				if fn, ok := tcMap["function"].(map[string]interface{}); ok {
					toolCall.Function.Name = getString(fn, "name")
					toolCall.Function.Arguments = getString(fn, "arguments")
				}
				state.ToolCalls = append(state.ToolCalls, toolCall)
			}
		}
	}

	return state, nil
}

// ExtractFromStreamDelta extracts content from a streaming delta chunk.
func (e *ResponseExtractor) ExtractFromStreamDelta(delta map[string]interface{}) (content, thinking string) {
	// Extract content
	if c, ok := delta["content"].(string); ok {
		content = c
	}

	// Extract reasoning_content (thinking)
	if rc, ok := delta["reasoning_content"].(string); ok {
		thinking = rc
	}

	return content, thinking
}

// getString safely extracts a string from a map.
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
