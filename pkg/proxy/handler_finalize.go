// ─────────────────────────────────────────────────────────────────────────────
// Finalization
// ─────────────────────────────────────────────────────────────────────────────

package proxy

import (
	"log"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// finalizeSuccess updates the request log with the completed status.
func (h *Handler) finalizeSuccess(rc *requestContext) {
	rc.reqLog.Status = "completed"

	// Append assistant message to Messages array
	// This is the source of response content - no separate Response/Thinking fields needed
	assistantMsg := store.Message{
		Role:     "assistant",
		Content:  rc.accumulatedResponse.String(),
		Thinking: rc.accumulatedThinking.String(),
	}

	// Include tool calls if any were accumulated
	if len(rc.accumulatedToolCalls) > 0 {
		// Convert argument builders to strings
		assistantMsg.ToolCalls = make([]store.ToolCall, len(rc.accumulatedToolCalls))
		for i, tc := range rc.accumulatedToolCalls {
			assistantMsg.ToolCalls[i] = tc
			// Copy arguments from builder (if available)
			if i < len(rc.toolCallArgBuilders) {
				assistantMsg.ToolCalls[i].Function.Arguments = rc.toolCallArgBuilders[i].String()
			}
		}
	}

	rc.reqLog.Messages = append(rc.reqLog.Messages, assistantMsg)

	rc.reqLog.EndTime = time.Now()
	rc.reqLog.Duration = time.Since(rc.startTime).String()
	h.store.Add(rc.reqLog)
}

// handleModelFailure updates the request log and publishes events when a model
// has exhausted all retries.
func (h *Handler) handleModelFailure(rc *requestContext, modelIndex int, currentModel string) {
	log.Printf("Model %s failed (retries exhausted or unrecoverable error)", currentModel)
	h.publishEvent("error_max_upstream_error_retries", map[string]interface{}{"id": rc.reqID})

	rc.reqLog.Status = "failed"
	rc.reqLog.Error = "Model failed"
	rc.reqLog.EndTime = time.Now()
	rc.reqLog.Duration = time.Since(rc.startTime).String()
	h.store.Add(rc.reqLog)

	// Notify about fallback transition
	if modelIndex+1 < len(rc.modelList) {
		nextModel := rc.modelList[modelIndex+1]
		rc.reqLog.CurrentFallback = nextModel

		h.publishEvent("fallback_triggered", map[string]interface{}{
			"id":         rc.reqID,
			"from_model": currentModel,
			"to_model":   nextModel,
			"reason":     "upstream_error",
		})
	}

	// Only track in "FallbackUsed" if the model that *just failed* was actually a fallback
	if modelIndex > 0 {
		rc.reqLog.FallbackUsed = append(rc.reqLog.FallbackUsed, currentModel)
	}
}
