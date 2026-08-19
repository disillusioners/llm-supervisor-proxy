package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
	// translator is imported HELPER-ONLY for ExtractEntryText (R3
	// layering decision). The provider → translator direction is an
	// inversion of the usual translator → providers type flow. This
	// import must NOT pull any provider types back into translator
	// (the translator package does not import pkg/providers; the
	// reasoning shape lives in the local ReasoningDetailEntry type
	// defined in interface.go). pkg/providers MUST NOT export its
	// own ReasoningDetail type — the boundary code in openai.go
	// is responsible for translating between this local type and
	// the translator's helper-only function. See R3 layering
	// decision in architecture-recommendation.md §11.
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
)

// OpenAIProvider implements Provider for OpenAI-compatible APIs
type OpenAIProvider struct {
	apiKey        string
	baseURL       string
	client        *http.Client
	bufferStore   *bufferstore.BufferStore       // Optional: for saving debug info
	requestID     string                         // Optional: request ID for buffer naming
	repairer      *toolrepair.Repairer           // Optional: for repairing tool call JSON
	eventCallback toolrepair.RepairEventCallback // Optional: callback for repair events
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(apiKey, baseURL string) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAIProvider{
		apiKey:  apiKey,
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// SetDebugContext sets the buffer store and request ID for debug file saving
func (p *OpenAIProvider) SetDebugContext(bufferStore *bufferstore.BufferStore, requestID string) {
	p.bufferStore = bufferStore
	p.requestID = requestID
}

// SetRepairer sets the tool call repairer and optional event callback
func (p *OpenAIProvider) SetRepairer(repairer *toolrepair.Repairer, callback toolrepair.RepairEventCallback) {
	p.repairer = repairer
	p.eventCallback = callback
}

// Name returns the provider name
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// ChatCompletion sends a non-streaming chat completion request
func (p *OpenAIProvider) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	req.Stream = false

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		// Per Go's http.Client.Do docs: "If the returned error is non-nil, the
		// Response.Body is non-nil and must be closed" in some error cases (e.g., redirect errors)
		if resp != nil {
			resp.Body.Close()
		}
		return nil, &ProviderError{
			Provider:  p.Name(),
			Message:   err.Error(),
			Retryable: isNetworkError(err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		// Write request to file (message, toolcall) and provide a link in frontend for debugging
		return nil, p.handleError(resp, req)
	}

	var result ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Repair tool calls if repairer is configured
	if p.repairer != nil {
		for i := range result.Choices {
			if result.Choices[i].Message == nil {
				continue
			}

			// Convert tool calls to repair data
			toolCallsData := make([]toolrepair.ToolCallData, len(result.Choices[i].Message.ToolCalls))
			for j, tc := range result.Choices[i].Message.ToolCalls {
				toolCallsData[j] = toolrepair.ToolCallData{
					ID:        tc.ID,
					Type:      tc.Type,
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				}
			}

			// Repair tool calls
			repairedCalls, stats := p.repairer.RepairToolCallsData(toolCallsData, p.eventCallback)
			if stats.Repaired > 0 || stats.Failed > 0 {
				log.Printf("[TOOL-REPAIR] total=%d repaired=%d failed=%d duration=%v",
					stats.TotalToolCalls, stats.Repaired, stats.Failed, stats.Duration)
			}

			// Update with repaired data
			for j, rc := range repairedCalls {
				result.Choices[i].Message.ToolCalls[j].Function.Arguments = rc.Arguments
			}
		}
	}

	// D2 + P2-1a: extract reasoning_details on the typed/non-stream
	// path. Single-winner rule: when Message.ReasoningDetails is
	// non-empty, it WINS and any pre-existing ReasoningContent is
	// ignored. When details are absent, ReasoningContent is preserved
	// untouched (the upstream may have set it as a string already).
	// ReasoningDetails is set to nil so omitempty drops the key on
	// the typed Encode (R11 — no leak).
	//
	// Provider-agnostic and data-driven (R2-residual D2 wrong-layer
	// guard): the openai.go parser does not branch on cred.Provider.
	// Non-MiniMax upstreams do not carry reasoning_details so the
	// helper is naturally inert.
	for i := range result.Choices {
		if result.Choices[i].Message == nil {
			continue
		}
		populateReasoningFromDetails(result.Choices[i].Message)
	}

	return &result, nil
}

// StreamChatCompletion sends a streaming chat completion request
func (p *OpenAIProvider) StreamChatCompletion(ctx context.Context, req *ChatCompletionRequest) (<-chan StreamEvent, error) {
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	p.setHeaders(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		// Per Go's http.Client.Do docs: even on error, resp.Body may be non-nil and must be closed
		if resp != nil {
			resp.Body.Close()
		}
		return nil, &ProviderError{
			Provider:  p.Name(),
			Message:   err.Error(),
			Retryable: isNetworkError(err),
		}
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, p.handleError(resp, req)
	}

	eventCh := make(chan StreamEvent, 100)

	go func() {
		defer close(eventCh)
		defer resp.Body.Close()

		p.processStream(resp.Body, eventCh)
	}()

	return eventCh, nil
}

// IsRetryable checks if an error should trigger a retry
func (p *OpenAIProvider) IsRetryable(err error) bool {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Retryable
	}
	return false
}

// setHeaders sets common headers for OpenAI requests
func (p *OpenAIProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
}

// handleError converts HTTP error response to ProviderError
// If bufferStore is configured, saves the request to a file for debugging
func (p *OpenAIProvider) handleError(resp *http.Response, req *ChatCompletionRequest) *ProviderError {
	// Limit body size to 10MB to prevent unbounded memory consumption
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))

	var apiErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	json.Unmarshal(body, &apiErr)

	msg := apiErr.Error.Message
	if msg == "" {
		msg = string(body)
	}

	// Determine if retryable based on status code
	retryable := false
	switch resp.StatusCode {
	case 429: // Rate limit
		retryable = true
	case 500, 502, 503, 504: // Server errors
		retryable = true
	}

	providerErr := &ProviderError{
		Provider:   p.Name(),
		StatusCode: resp.StatusCode,
		Message:    msg,
		Retryable:  retryable,
	}

	// Save request to file for debugging
	if p.bufferStore != nil && p.requestID != "" && req != nil {
		if requestJSON, err := json.MarshalIndent(req, "", "  "); err == nil {
			bufferID := fmt.Sprintf("%s_provider_request", p.requestID)
			if saveErr := p.bufferStore.Save(bufferID, requestJSON); saveErr == nil {
				providerErr.BufferID = bufferID
			}
		}
	}

	return providerErr
}

// getToolCallIDs extracts tool call IDs for logging
func getToolCallIDs(toolCalls []ToolCall) []string {
	ids := make([]string, len(toolCalls))
	for i, tc := range toolCalls {
		ids[i] = tc.ID
	}
	return ids
}

// processStream processes SSE stream and sends normalized events
func (p *OpenAIProvider) processStream(reader io.Reader, eventCh chan<- StreamEvent) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024) // 10MB max

	var lastResponse *ChatCompletionResponse

	// Accumulate tool calls during streaming (index -> accumulated data)
	accumulatedToolCalls := make(map[int]*ToolCall)
	argumentsBuilders := make(map[int]*strings.Builder)

	// W4 — per-stream emitted-text map for reasoning_details
	// cross-chunk dedup. Mirrors the external StreamTranslator's
	// lastIssued field (minimax_stream.go): cumulative-mode
	// upstreams double-emit the same text inside a growing
	// reasoning_details array; without per-stream state the
	// typed path would emit the same text twice (once per
	// chunk). The map is keyed by choice index and stores the
	// most recently emitted text per choice; per-entry emission
	// uses containment (strict-superset → suffix-only) so the
	// dedup is conservative.
	streamReasoningLastIssued := make(map[int]string)

	// sendDoneEvent sends the done event with the final response
	// This is called when we see [DONE] or when stream ends with a finish_reason
	sendDoneEvent := func() {
		if lastResponse != nil {
			// Convert accumulated tool calls to Message.ToolCalls for the final response
			if len(accumulatedToolCalls) > 0 {
				// Ensure Message exists
				if lastResponse.Choices[0].Message == nil {
					lastResponse.Choices[0].Message = &ChatMessage{
						Role: "assistant",
					}
				}

				// Extract built argument strings before copying
				for idx, sb := range argumentsBuilders {
					if tc, ok := accumulatedToolCalls[idx]; ok {
						tc.Function.Arguments = sb.String()
					}
				}

				// Convert accumulated tool calls to sorted slice
				maxIndex := 0
				for idx := range accumulatedToolCalls {
					if idx > maxIndex {
						maxIndex = idx
					}
				}

				toolCalls := make([]ToolCall, maxIndex+1)
				for idx, tc := range accumulatedToolCalls {
					toolCalls[idx] = *tc
				}
				lastResponse.Choices[0].Message.ToolCalls = toolCalls
			}

			// Repair tool calls in the final response
			if p.repairer != nil && lastResponse.Choices[0].Message != nil {
				toolCalls := lastResponse.Choices[0].Message.ToolCalls
				if len(toolCalls) > 0 {
					// Convert tool calls to repair data
					toolCallsData := make([]toolrepair.ToolCallData, len(toolCalls))
					for j, tc := range toolCalls {
						toolCallsData[j] = toolrepair.ToolCallData{
							ID:        tc.ID,
							Type:      tc.Type,
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						}
					}

					// Repair tool calls
					repairedCalls, stats := p.repairer.RepairToolCallsData(toolCallsData, p.eventCallback)
					if stats.Repaired > 0 || stats.Failed > 0 {
						log.Printf("[TOOL-REPAIR] total=%d repaired=%d failed=%d duration=%v",
							stats.TotalToolCalls, stats.Repaired, stats.Failed, stats.Duration)
					}

					// Update with repaired data
					for j, rc := range repairedCalls {
						lastResponse.Choices[0].Message.ToolCalls[j].Function.Arguments = rc.Arguments
					}
				}
			}

			// Extract finish reason from the response
			finishReason := ""
			if len(lastResponse.Choices) > 0 {
				finishReason = lastResponse.Choices[0].FinishReason
			}

			eventCh <- StreamEvent{
				Type:         "done",
				Response:     lastResponse,
				FinishReason: finishReason,
			}
		} else {
			eventCh <- StreamEvent{
				Type: "done",
			}
		}
	}

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if line == "" {
			continue
		}

		// Only process data lines
		// Use "data:" prefix (without space) to handle variations like "data: [DONE]", "data:[DONE]", "data:  [DONE]"
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		// Extract data after "data:" prefix, trimming any whitespace
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		// Check for stream end marker
		if strings.HasPrefix(data, "[DONE]") {
			sendDoneEvent()
			return
		}

		var chunk ChatCompletionResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			eventCh <- StreamEvent{
				Type:  "error",
				Error: fmt.Errorf("failed to parse chunk: %w", err),
			}
			continue
		}

		// Extract content delta
		if len(chunk.Choices) > 0 {
			choice := chunk.Choices[0]
			if choice.Delta != nil {
				// Content can be string or nil during streaming
				if contentStr, ok := choice.Delta.Content.(string); ok && contentStr != "" {
					eventCh <- StreamEvent{
						Type:    "content",
						Content: contentStr,
					}
				}
				// Handle reasoning_details (MiniMax-style
				// typed thinking). Emit ONE thinking StreamEvent
				// per entry, in array order. Single-winner rule
				// (W4 + D2 + Round-2): when reasoning_details
				// is present and non-empty (hasDetails == true),
				// it WINS and reasoning_content is NOT extracted
				// (the existing extractReasoningContent call is
				// skipped). The flag is presence-based — set when
				// ANY choice carried a non-empty array, regardless
				// of whether any entries survived filtering — so
				// the internal typed path matches the external
				// translator at minimax_stream.go:529-554
				// (single-winner fires on presence, not on entry
				// survival). When reasoning_details is absent,
				// fall back to reasoning_content (DeepSeek-style).
				emittedPerChoice, hasDetails := extractReasoningDetailsByChoiceForStream([]byte(data))
				if hasDetails {
					// W4 — per-choice cross-chunk dedup
					// (mirrors the external path's
					// StreamTranslator.lastIssued):
					// strict-superset → suffix-only,
					// containment → skip. The
					// per-entry intra-chunk dedup
					// inside
					// extractReasoningDetailsByChoice
					// covers duplicates within a
					// single array; this loop adds
					// the cross-chunk suffix-mode
					// for cumulative-mode upstreams.
					for _, item := range emittedPerChoice {
						prior := streamReasoningLastIssued[item.choiceIdx]
						if prior != "" && strings.Contains(prior, item.text) {
							// Already covered
							// by prior
							// emission →
							// skip.
							continue
						}
						var outText string
						if prior != "" && strings.HasPrefix(item.text, prior) {
							// Cumulative-suffix
							// mode →
							// suffix-only.
							outText = item.text[len(prior):]
						} else {
							outText = item.text
						}
						if outText == "" {
							streamReasoningLastIssued[item.choiceIdx] = item.text
							continue
						}
						streamReasoningLastIssued[item.choiceIdx] = item.text
						eventCh <- StreamEvent{
							Type:             "thinking",
							ReasoningContent: outText,
						}
					}
				} else if reasoningContent := extractReasoningContent([]byte(data)); reasoningContent != "" {
					eventCh <- StreamEvent{
						Type:             "thinking",
						ReasoningContent: reasoningContent,
					}
				}
				// Handle tool_calls in streaming - accumulate them
				if len(choice.Delta.ToolCalls) > 0 {
					for _, tc := range choice.Delta.ToolCalls {
						// Get or create accumulated tool call using the index
						index := tc.Index

						if accumulatedToolCalls[index] == nil {
							accumulatedToolCalls[index] = &ToolCall{
								Type: tc.Type,
							}
						}

						// Accumulate ID (only set once)
						if tc.ID != "" {
							accumulatedToolCalls[index].ID = tc.ID
						}

						// Accumulate function data
						if tc.Function.Name != "" {
							accumulatedToolCalls[index].Function.Name = tc.Function.Name
						}
						if tc.Function.Arguments != "" {
							if argumentsBuilders[index] == nil {
								argumentsBuilders[index] = &strings.Builder{}
							}
							argumentsBuilders[index].WriteString(tc.Function.Arguments)
						}
					}

					eventCh <- StreamEvent{
						Type:      "tool_call",
						ToolCalls: choice.Delta.ToolCalls,
					}
				}
			}
			if choice.FinishReason != "" {
				lastResponse = &chunk
				// D2 + P2-1a: when the final chunk carries a
				// Message (some non-OpenAI providers do this),
				// apply the single-winner rule at extraction
				// time on the typed path. Reasoning_details wins
				// over any pre-existing reasoning_content;
				// ReasoningDetails is set to nil so omitempty
				// drops the key on the typed done event (R11 —
				// no leak). For OpenAI the final chunk's Message
				// is typically nil and this is a no-op; the
				// per-entry thinking events already carried the
				// text downstream.
				if lastResponse.Choices[0].Message != nil {
					populateReasoningFromDetails(lastResponse.Choices[0].Message)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		eventCh <- StreamEvent{
			Type:  "error",
			Error: err,
		}
		return
	}

	// If we reach here without seeing [DONE], check if we received a finish_reason.
	// Some providers (like MiniMax) don't send [DONE] marker but close the connection
	// after sending finish_reason. In this case, treat it as a successful completion.
	if lastResponse != nil && len(lastResponse.Choices) > 0 && lastResponse.Choices[0].FinishReason != "" {
		log.Printf("[PROVIDER] Stream ended without [DONE] marker but has finish_reason=%s, treating as complete",
			lastResponse.Choices[0].FinishReason)
		sendDoneEvent()
		return
	}

	// If we reach here without [DONE] and without finish_reason, the stream ended prematurely.
	// This can happen if the upstream closes the connection unexpectedly.
	// Send an error event to signal the stream was incomplete.
	eventCh <- StreamEvent{
		Type:  "error",
		Error: fmt.Errorf("stream ended without [DONE] marker"),
	}
}

// isNetworkError checks if the error is a network-level error
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	// Network errors are generally retryable
	return true
}

// parseRetryAfter parses the Retry-After header
func parseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}

	// Try parsing as seconds
	if secs, err := strconv.Atoi(header); err == nil {
		return time.Duration(secs) * time.Second
	}

	// Try parsing as date
	if t, err := time.Parse(time.RFC1123, header); err == nil {
		return time.Until(t)
	}

	return 0
}

// extractReasoningContent extracts reasoning_content field from raw JSON chunk
// This is used for DeepSeek-style thinking models that include reasoning_content in deltas
func extractReasoningContent(data []byte) string {
	var rawChunk struct {
		Choices []struct {
			Delta struct {
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &rawChunk); err != nil {
		return ""
	}
	if len(rawChunk.Choices) > 0 {
		return rawChunk.Choices[0].Delta.ReasoningContent
	}
	return ""
}

// unknownTypeDebugN is the inverse sampling rate for the unknown-entry-type
// debug log below (1 in N invocations are logged). Matches the translator's
// entryDebugSeen counter (minimax_stream.go, 1 in 64) so both skip sites
// behave symmetrically per plan §6.1.
const unknownTypeDebugN = 64

// unknownTypeDebugSeen counts invocations of unknownTypeDebugSamplingAllowed;
// every Nth invocation is allowed. Atomic so the stream parser's use from
// concurrent stream loops is race-free.
var unknownTypeDebugSeen atomic.Uint64

// unknownTypeDebugSamplingAllowed is the local 1-in-N sampling gate for the
// unknown-entry-type debug log at the openai.go skip site (M-1). Deliberately
// a minimal local duplicate of the translator's package-private sampler:
// exporting a sampler helper from translator would add API surface that R3
// (helper-only provider → translator import) does not require.
func unknownTypeDebugSamplingAllowed() bool {
	return unknownTypeDebugSeen.Add(1)%unknownTypeDebugN == 0
}

// extractReasoningDetailsFromRawData walks an upstream SSE chunk's
// `choices[].delta.reasoning_details` array (or
// `choices[].message.reasoning_details` for non-stream responses) and
// returns one emitted-text string per non-empty entry, in array order.
//
// The function is DATA-DRIVEN and PROVIDER-AGNOSTIC — it does not
// branch on cred.Provider (R2-residual D2 wrong-layer guard: provider
// gating belongs to the caller/flag layer at pkg/proxy/handler.go
// entry, never the parser). Non-MiniMax upstreams do not carry
// reasoning_details so this is naturally inert on those providers.
//
// Per-entry processing (one emitted-text string per entry):
//   - type != "reasoning.text" (when type is present) → skip
//     (forward-compat: MiniMax may add typed entries that map to
//     different fields; we only emit the plain reasoning text
//     entries on the OpenAI reasoning_content channel).
//   - text = translator.ExtractEntryText(entry) (text key wins,
//     content key is forward-compat fallback per H3 / §6.2).
//   - empty after strings.TrimSpace → skip (H7).
//   - contained in alreadyEmitted (strings.Index >= 0) → skip
//     (H2 dedup; protects against cumulative-mode upstream replay
//     and the mock harness's "both fields" case at the array level).
//
// Returns nil when the input does not carry reasoning_details or when
// all entries are filtered out — callers distinguish "no details" from
// "details but no emit" by checking the boolean return.
func extractReasoningDetailsFromRawData(data []byte) (emittedTexts []string, hasDetails bool) {
	var rawChunk struct {
		Choices []struct {
			Delta struct {
				ReasoningDetails []map[string]any `json:"reasoning_details"`
			} `json:"delta"`
			Message struct {
				ReasoningDetails []map[string]any `json:"reasoning_details"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &rawChunk); err != nil {
		return nil, false
	}
	if len(rawChunk.Choices) == 0 {
		return nil, false
	}
	// W3 — walk ALL choices, not just choices[0]. Previously
	// choices[1+] was silently dropped on the typed path; now we
	// accumulate entries from every choice in array order so a
	// multi-choice chunk's reasoning_details all surface. The
	// per-choice emitted list is flattened in the order of
	// choices[].
	var acc strings.Builder
	for _, c := range rawChunk.Choices {
		// Prefer delta.reasoning_details (stream path); fall
		// back to message.reasoning_details (non-stream — though
		// the non-stream typed path uses Message.ReasoningDetails
		// directly and does not call this helper; this fallback
		// exists for symmetry and to keep the helper usable from
		// both call sites).
		entries := c.Delta.ReasoningDetails
		if len(entries) == 0 {
			entries = c.Message.ReasoningDetails
		}
		if len(entries) == 0 {
			continue
		}
		hasDetails = true
		for _, entry := range entries {
			// ExtractEntryText is a helper-only import from
			// pkg/proxy/translator; it must NOT pull provider
			// types back — see R3 layering decision.
			text := translator.ExtractEntryText(entry)
			if strings.TrimSpace(text) == "" {
				continue // H7 skip-empty
			}
			// Type filter: when the entry carries a `type`
			// field that is non-empty and not
			// "reasoning.text", skip. This is forward-compat
			// for any future typed entries MiniMax may add
			// (e.g. reasoning.summary, reasoning.encrypted).
			// M-1: per plan §6.1 the skip is NOT silent —
			// emit a sampled debug log, symmetric with the
			// translator's unknown-type handling in
			// minimax_stream.go. The sampler is a local
			// 1-in-N gate (duplicate of the translator's
			// package-private sampler) rather than an
			// exported helper: duplicating ~5 lines costs
			// less surface than exporting a new translator
			// API (R3 keeps the provider → translator import
			// helper-only; ExtractEntryText already stretches
			// that boundary).
			if t, ok := entry["type"].(string); ok && t != "" && t != translator.ReasoningTextType {
				if unknownTypeDebugSamplingAllowed() {
					log.Printf("[DEBUG] openai.go reasoning_details unknown entry type=%q (skipped)", t)
				}
				continue
			}
			// H2 dedup: skip if text is already contained in
			// already-emitted text. Containment
			// (strings.Index >= 0) rather than equality, so
			// cumulative upstream emission is also detected.
			if acc.Len() > 0 && strings.Index(acc.String(), text) >= 0 {
				continue
			}
			emittedTexts = append(emittedTexts, text)
			acc.WriteString(text)
		}
	}
	return emittedTexts, hasDetails
}

// populateReasoningFromDetails applies the single-winner rule (D2) at
// extraction time on the typed path. When the upstream message carries
// `reasoning_details` (non-empty), reasoning_details WINS and any
// pre-existing `reasoning_content` is IGNORED (not concatenated, not
// duplicated). When reasoning_details is absent or empty, the existing
// reasoning_content is preserved untouched.
//
// For each entry in details: H2 dedup (containment vs the running
// accumulator only — never vs any pre-existing reasoning_content, which
// is discarded entirely under W6), H7 skip-empty, unknown-type skip.
// The concatenated text is written to msg.ReasoningContent and
// msg.ReasoningDetails is set to nil so omitempty drops the key on
// typed encode (R11 — no leak).
//
// The function is DATA-DRIVEN and PROVIDER-AGNOSTIC — it does not
// branch on cred.Provider (R2-residual D2 wrong-layer guard).
func populateReasoningFromDetails(msg *ChatMessage) {
	if msg == nil {
		return
	}
	if len(msg.ReasoningDetails) == 0 {
		// No details ⇒ keep existing ReasoningContent. The
		// single-winner rule only fires when details are present.
		return
	}
	// Details present ⇒ reasoning_details WINS. W6 true
	// single-winner: any pre-existing reasoning_content is
	// DISCARDED entirely (no dedup-to-nothing, no O(n²)
	// containment drops — just replace). Per-entry processing:
	// H7 skip-empty, unknown-type skip, intra-array dedup via
	// containment.
	var acc strings.Builder
	for _, entry := range msg.ReasoningDetails {
		// Pass the typed Text through the translator helper as a
		// map[string]any{"text": ...} so the helper is the
		// single source of truth for the text-extraction
		// policy (including the forward-compat `content`
		// fallback per H3 / §6.2). ExtractEntryText is a
		// helper-only import from pkg/proxy/translator; it
		// must NOT pull provider types back — see R3 layering
		// decision.
		text := translator.ExtractEntryText(map[string]any{
			"text": entry.Text,
		})
		if strings.TrimSpace(text) == "" {
			continue // H7 skip-empty
		}
		// Intra-array dedup (containment): if we already
		// emitted this text in the same array, skip. Protects
		// against cumulative-mode replay and duplicates within
		// a single details array.
		if acc.Len() > 0 && strings.Index(acc.String(), text) >= 0 {
			continue
		}
		acc.WriteString(text)
	}
	msg.ReasoningContent = acc.String()
	msg.ReasoningDetails = nil // omitempty drops the key on encode (R11)
}

// HydrateReasoningDetails is the shared request-side map→struct hydration
// for `msg["reasoning_details"]` (D1 carry). It returns a fresh
// []ReasoningDetailEntry slice populated from a JSON-decoded
// `[]interface{}` of `map[string]interface{}` entries, copying each
// present field by reflection-free type assertion. Returns nil if the
// key is absent or the value is not `[]interface{}`.
//
// Inputs are accepted in two equivalent shapes:
//   - `map[string]interface{}` entries (the normal JSON-decoded case;
//     both race-external and pre-translated request bodies land here).
//   - `translator.ReasoningDetail` struct entries (race-internal path:
//     pkg/proxy/race_executor.go:843 calls this helper AFTER
//     translator.TranslateRequestBody mutates bodyMap in place at
//     race_executor.go:174 — the translator writes
//     `[]any{translator.ReasoningDetail{…}}` so this helper must accept
//     struct values to avoid a silent empty-slice return and a downstream
//     omitempty drop on the final marshal — see T3b findings
//     2026-08-19). Struct entries are converted to a per-element map
//     here so the field extraction below stays single-path.
//
// Twin-divergence analysis: the two near-duplicate hydration blocks in
// pkg/proxy/race_executor.go (~836-864) and
// pkg/ultimatemodel/handler_internal.go (~525-560) were byte-identical
// at extraction time; this helper collapses them to a single source of
// truth. Kills the recurrence vector for the reasoning_content
// silent-drop bug class (origin: commit 83814b0 fixed a twin-divergence
// in convertRequest hydration) — both sites now share the same code
// path so the next divide-and-conquer fork produces one bug fix, not
// two.
//
// Used by the type-stripped reasoning_details hydration on the
// race-internal (pkg/proxy/race_executor.go) and ultimate-internal
// (pkg/ultimatemodel/handler_internal.go) request paths.
func HydrateReasoningDetails(msg map[string]any) []ReasoningDetailEntry {
	raw, ok := msg["reasoning_details"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]ReasoningDetailEntry, 0, len(raw))
	for _, rd := range raw {
		var rdMap map[string]interface{}
		switch typed := rd.(type) {
		case map[string]interface{}:
			rdMap = typed
		case translator.ReasoningDetail:
			rdMap = map[string]interface{}{
				"type":   typed.Type,
				"id":     typed.ID,
				"format": typed.Format,
				"index":  typed.Index,
				"text":   typed.Text,
			}
		default:
			continue
		}
		entry := ReasoningDetailEntry{}
		if t, ok := rdMap["type"].(string); ok {
			entry.Type = t
		}
		if id, ok := rdMap["id"].(string); ok {
			entry.ID = id
		}
		if format, ok := rdMap["format"].(string); ok {
			entry.Format = format
		}
		if index, ok := rdMap["index"].(float64); ok {
			entry.Index = int(index)
		}
		if text, ok := rdMap["text"].(string); ok {
			entry.Text = text
		}
		out = append(out, entry)
	}
	return out
}

// reasoningDetailEmit is one emitted-text string for the
// cross-chunk dedup pass, tagged with the choice index it came
// from so the caller can apply per-choice lastIssued state.
type reasoningDetailEmit struct {
	choiceIdx int
	text      string
}

// extractReasoningDetailsByChoiceForStream parses an upstream SSE
// chunk and returns one emit per non-empty reasoning_details
// entry across ALL choices, in array order, plus a hasDetails
// flag indicating whether ANY choice carried a present-and-non-empty
// reasoning_details array. W3 — walks every choice (the previous
// implementation read choices[0] only, silently dropping
// reasoning_details on choices[1+]). W4 — the returned slice is
// per-choice-indexed so the caller can apply the cross-chunk
// lastIssued map.
//
// Per-entry processing mirrors the external StreamTranslator's
// behavior: H7 skip-empty, unknown-type skip, intra-array dedup
// via containment. Cross-chunk dedup (suffix mode) is the
// caller's job — see processStream's use of
// streamReasoningLastIssued.
//
// hasDetails is the W4/Round-2 single-winner signal: it is set
// when ANY choice had a non-empty reasoning_details array
// regardless of whether any entries survived filtering (H7
// skip-empty, unknown-type skip). This mirrors the external
// translator's presence-based logic at minimax_stream.go:529-554
// (single-winner rule fires when details are present AND non-empty
// on the wire). The caller uses hasDetails — NOT len(emits)>0 —
// to gate the reasoning_content fallback so the internal typed
// path matches the external path on the all-skipped-entries edge.
func extractReasoningDetailsByChoiceForStream(data []byte) (emits []reasoningDetailEmit, hasDetails bool) {
	var rawChunk struct {
		Choices []struct {
			Index float64 `json:"index"`
			Delta struct {
				ReasoningDetails []map[string]any `json:"reasoning_details"`
			} `json:"delta"`
			Message struct {
				ReasoningDetails []map[string]any `json:"reasoning_details"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &rawChunk); err != nil {
		return nil, false
	}
	if len(rawChunk.Choices) == 0 {
		return nil, false
	}
	var out []reasoningDetailEmit
	for _, c := range rawChunk.Choices {
		// Prefer delta.reasoning_details (stream path); fall back
		// to message.reasoning_details (kept for symmetry with
		// extractReasoningDetailsFromRawData).
		entries := c.Delta.ReasoningDetails
		if len(entries) == 0 {
			entries = c.Message.ReasoningDetails
		}
		if len(entries) == 0 {
			continue
		}
		// Mark presence-and-non-empty for this choice. The single-winner
		// rule fires on presence alone — see external path
		// (minimax_stream.go:529-554) and the W4 contract. Set the
		// flag here so all-skipped-entries (H7 skip-empty, unknown-type
		// skip) still gate off the reasoning_content fallback at the
		// call site.
		hasDetails = true
		choiceIdx := int(c.Index)
		// Per-choice intra-array dedup via containment.
		var seen strings.Builder
		for _, entry := range entries {
			text := translator.ExtractEntryText(entry)
			if strings.TrimSpace(text) == "" {
				continue // H7 skip-empty
			}
			// Unknown-type skip (forward-compat for
			// non-"reasoning.text" typed entries).
			if t, ok := entry["type"].(string); ok && t != "" && t != translator.ReasoningTextType {
				if unknownTypeDebugSamplingAllowed() {
					log.Printf("[DEBUG] openai.go reasoning_details unknown entry type=%q (skipped)", t)
				}
				continue
			}
			if seen.Len() > 0 && strings.Index(seen.String(), text) >= 0 {
				continue
			}
			seen.WriteString(text)
			out = append(out, reasoningDetailEmit{choiceIdx: choiceIdx, text: text})
		}
	}
	return out, hasDetails
}
