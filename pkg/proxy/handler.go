package proxy

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
	"net/url"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/supervisor"
	"github.com/google/uuid"
)

// Config holds runtime configuration for the proxy handler
type Config struct {
	ConfigMgr    *config.Manager      // Config manager for dynamic updates
	ModelsConfig *models.ModelsConfig `json:"-"`
}

// Clone returns a snapshot of the current config values
func (c *Config) Clone() ConfigSnapshot {
	cfg := c.ConfigMgr.Get()
	return ConfigSnapshot{
		UpstreamURL:             cfg.UpstreamURL,
		IdleTimeout:             cfg.IdleTimeout.Duration(),
		MaxGenerationTime:       cfg.MaxGenerationTime.Duration(),
		MaxUpstreamErrorRetries: cfg.MaxUpstreamErrorRetries,
		MaxIdleRetries:          cfg.MaxIdleRetries,
		MaxGenerationRetries:    cfg.MaxGenerationRetries,
		ModelsConfig:            c.ModelsConfig,
	}
}

// ConfigSnapshot is an immutable snapshot of config values for a single request
type ConfigSnapshot struct {
	UpstreamURL             string
	IdleTimeout             time.Duration
	MaxGenerationTime       time.Duration
	MaxUpstreamErrorRetries int
	MaxIdleRetries          int
	MaxGenerationRetries    int
	ModelsConfig            *models.ModelsConfig
}

type Handler struct {
	config *Config
	bus    *events.Bus
	store  *store.RequestStore
	client *http.Client
}

func NewHandler(config *Config, bus *events.Bus, store *store.RequestStore) *Handler {
	return &Handler{
		config: config,
		bus:    bus,
		store:  store,
		client: &http.Client{},
	}
}

func (h *Handler) publishEvent(eventType string, data interface{}) {
	if h.bus != nil {
		h.bus.Publish(events.Event{
			Type:      eventType,
			Timestamp: time.Now().Unix(),
			Data:      data,
		})
	}
}

func (h *Handler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	conf := h.config.Clone()
	targetURL, err := url.JoinPath(conf.UpstreamURL, "/v1/chat/completions")
	if err != nil {
		http.Error(w, "Invalid Upstream URL configuration", http.StatusInternalServerError)
		return
	}

	// Read original body with 10MB limit
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}
	r.Body.Close()

	// Parse body as JSON map
	var requestBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestBody); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	// Use base context, handle generation deadlines per-attempt natively inside
	baseCtx := r.Context()

	// Create Request Log
	reqID := uuid.New().String()
	startTime := time.Now()

	// Convert messages to store format
	var storeMessages []store.Message
	if msgs, ok := requestBody["messages"].([]interface{}); ok {
		for _, m := range msgs {
			if msgMap, ok := m.(map[string]interface{}); ok {
				role, _ := msgMap["role"].(string)
				content, _ := msgMap["content"].(string)
				storeMessages = append(storeMessages, store.Message{Role: role, Content: content})
			}
		}
	}

	model, _ := requestBody["model"].(string)
	originalModel := model

	reqLog := &store.RequestLog{
		ID:            reqID,
		Status:        "running",
		Model:         model,
		OriginalModel: originalModel,
		StartTime:     startTime,
		Messages:      storeMessages,
		Retries:       0,
		FallbackUsed:  []string{},
	}
	h.store.Add(reqLog)

	// Set up fallback models
	var allModels []string
	if conf.ModelsConfig != nil {
		fallbackChain := conf.ModelsConfig.GetFallbackChain(originalModel)
		// GetFallbackChain returns [original, fallback1, fallback2, ...]
		// We only want the fallbacks, not the original
		if len(fallbackChain) > 0 {
			allModels = fallbackChain[1:] // Skip the first one (original)
		}
	}

	// Build the complete model list: original + fallbacks
	modelList := []string{originalModel}
	modelList = append(modelList, allModels...)

	var accumulatedResponse strings.Builder
	var accumulatedThinking strings.Builder

	// Let's keep it simple: We just accumulate the text for thinking.
	// For tool calls, we'll try to reconstruct them if possible, or just store the raw JSON of the last chunk if it contains the full tool call?
	// No, tool calls are streamed.
	// Let's skip complex tool call parsing for this iteration and focus on Content + Thinking.
	// Actually, the requirement says "tool call should show".
	// We'll need a struct to hold temporary tool state.

	headersSent := false

	h.publishEvent("request_started", map[string]interface{}{"id": reqID})

	isStream := false
	if s, ok := requestBody["stream"].(bool); ok && s {
		isStream = true
	}

	// Outer loop: iterate through models (original + fallbacks)
	for modelIndex, currentModel := range modelList {
		// Log if this is a fallback attempt
		if modelIndex > 0 {
			log.Printf("Attempting fallback model: %s (index %d)", currentModel, modelIndex)
		}

		// Check if client disconnected before attempting this model
		if baseCtx.Err() != nil {
			log.Printf("Client disconnected, failing request")
			break
		}

		// Update model in request body
		requestBody["model"] = currentModel

		// Reset attempt counters for each new model
		errorRetries := 0
		idleRetries := 0
		genRetries := 0
		var lastErr error

		// Inner loop: retry logic for current model
		for {
			attempt := errorRetries + idleRetries + genRetries
			if errorRetries > conf.MaxUpstreamErrorRetries || idleRetries > conf.MaxIdleRetries || genRetries > conf.MaxGenerationRetries {
				break
			}
			if attempt > 0 {
				log.Printf("Retrying request (attempt %d)...", attempt)
				reqLog.Retries = attempt
				reqLog.Status = "retrying"
				h.store.Add(reqLog) // Update store

				h.publishEvent("retry_attempt", map[string]interface{}{"attempt": attempt, "id": reqID})

				if isStream {
					// Modify request body for retry
					messages, ok := requestBody["messages"].([]interface{})
					if !ok {
						log.Println("Could not find messages, aborting retry")
						http.Error(w, "Invalid request: messages not found", http.StatusBadRequest)
						return
					}

					// If we have content, append it
					if accumulatedResponse.Len() > 0 {
						messages = append(messages, map[string]string{
							"role":    "assistant",
							"content": accumulatedResponse.String(),
						})
					}

					messages = append(messages, map[string]string{
						"role":    "user",
						"content": "The previous response was interrupted. Continue exactly where you stopped.",
					})

					requestBody["messages"] = messages
				}
			}

			newBodyBytes, _ := json.Marshal(requestBody)

			// Create request with the per-attempt deadline context
			attemptCtx, attemptCancel := context.WithTimeout(baseCtx, conf.MaxGenerationTime)
			defer attemptCancel()

			proxyReq, err := http.NewRequestWithContext(attemptCtx, r.Method, targetURL, bytes.NewBuffer(newBodyBytes))
			if err != nil {
				log.Printf("Failed to create request: %v", err)
				return
			}

			// Copy headers
			for name, values := range r.Header {
				if name == "Content-Length" {
					continue
				}
				for _, value := range values {
					proxyReq.Header.Add(name, value)
				}
			}

			resp, err := h.client.Do(proxyReq)
			if err != nil {
				lastErr = err
				if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
					log.Printf("Attempt %d generation deadline exceeded", attempt)
					h.publishEvent("error_deadline_exceeded", map[string]interface{}{"id": reqID})
					genRetries++
					continue
				}
				if errors.Is(baseCtx.Err(), context.Canceled) {
					log.Println("Client disconnected")
					reqLog.Status = "failed"
					reqLog.Error = "Client disconnected"
					reqLog.EndTime = time.Now()
					reqLog.Duration = time.Since(startTime).String()
					h.store.Add(reqLog)
					return
				}
				log.Printf("Upstream request failed: %v", err)
				h.publishEvent("upstream_error", map[string]interface{}{"error": err.Error(), "id": reqID})
				errorRetries++
				time.Sleep(500 * time.Millisecond) // Slight backoff
				continue
			}

			// Cleanup response body if we don't finish loop
			// We defer close inside the loop but we need to be careful not to close it too early if we wrap it.
			// The MonitoredReader takes ownership.

			if resp.StatusCode != http.StatusOK {
				// If not 200, assume error and maybe retry?
				// If 5xx or 429, retry.
				// But first, if we haven't sent headers, we can pass through error.
				if !headersSent {
					// Only retry on 5xx or 429
					if resp.StatusCode >= 500 || resp.StatusCode == 429 {
						resp.Body.Close()
						log.Printf("Upstream returned %d", resp.StatusCode)
						h.publishEvent("upstream_error_status", map[string]interface{}{"status": resp.StatusCode, "id": reqID})
						errorRetries++
						time.Sleep(1 * time.Second)
						continue
					}

					// If there is a fallback model available, don't pass the error straight to the client yet.
					if modelIndex+1 < len(modelList) {
						resp.Body.Close()
						log.Printf("Upstream returned %d for model %s. Triggering fallback.", resp.StatusCode, currentModel)
						break
					}

					// Otherwise pass through
					for k, v := range resp.Header {
						w.Header()[k] = v
					}
					w.WriteHeader(resp.StatusCode)
					io.Copy(w, resp.Body)
					resp.Body.Close()

					reqLog.Status = "failed"
					reqLog.Error = fmt.Sprintf("Upstream returned %d", resp.StatusCode)
					reqLog.EndTime = time.Now()
					reqLog.Duration = time.Since(startTime).String()
					h.store.Add(reqLog)
					return
				}
				resp.Body.Close()
				return
			}

			monitor := supervisor.NewMonitoredReader(resp.Body, conf.IdleTimeout)

			if !headersSent {
				for k, v := range resp.Header {
					w.Header()[k] = v
				}
				w.WriteHeader(http.StatusOK)
				// Force flush headers?
				// if f, ok := w.(http.Flusher); ok { f.Flush() }
				headersSent = true
			}

			if !isStream {
				bodyBytes, err := io.ReadAll(monitor)
				if err != nil {
					lastErr = err
					if errors.Is(err, supervisor.ErrIdleTimeout) {
						log.Println("Stream idle timeout detected!")
						h.publishEvent("timeout_idle", map[string]interface{}{"timeout": conf.IdleTimeout.String(), "id": reqID})
						idleRetries++
						continue
					}
					if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
						log.Printf("Attempt %d generation deadline exceeded", attempt)
						h.publishEvent("error_deadline_exceeded", map[string]interface{}{"id": reqID})
						monitor.Close()
						genRetries++
						continue
					}
					log.Printf("Stream error: %v", err)
					h.publishEvent("stream_error", map[string]interface{}{"error": err.Error(), "id": reqID})
					monitor.Close()
					errorRetries++
					continue
				}

				w.Write(bodyBytes)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}

				var respMap map[string]interface{}
				if err := json.Unmarshal(bodyBytes, &respMap); err == nil {
					if choices, ok := respMap["choices"].([]interface{}); ok && len(choices) > 0 {
						if choice, ok := choices[0].(map[string]interface{}); ok {
							if msg, ok := choice["message"].(map[string]interface{}); ok {
								if content, ok := msg["content"].(string); ok {
									accumulatedResponse.WriteString(content)
								}
								if rc, ok := msg["reasoning_content"].(string); ok {
									accumulatedThinking.WriteString(rc)
								}
								if psf, ok := msg["provider_specific_fields"].(map[string]interface{}); ok {
									if rc, ok := psf["reasoning_content"].(string); ok {
										accumulatedThinking.WriteString(rc)
									}
								}
							}
						}
					}
				}

				h.publishEvent("request_completed", map[string]interface{}{"id": reqID})
				reqLog.Status = "completed"
				reqLog.Response = accumulatedResponse.String()
				reqLog.Thinking = accumulatedThinking.String()
				reqLog.EndTime = time.Now()
				reqLog.Duration = time.Since(startTime).String()
				h.store.Add(reqLog)
				monitor.Close()
				return
			}

			// Stream
			scanner := bufio.NewScanner(monitor)
			// Default scanner buffer might be small for very long lines, but SSE lines are usually okay.
			buffer := make([]byte, 0, 1024*1024)
			scanner.Buffer(buffer, 1024*1024) // 1MB limit

			streamEndedSuccesfully := false

			for scanner.Scan() {
				line := scanner.Bytes()

				// Passthrough
				// Note: We might be sending duplicate "role": "assistant" chunks if we retry?
				// The client handles SSE events. Repeated events usually just append.
				// If we retry, we are starting a NEW stream.
				// Ideally, we shouldn't send previous content again if we use "Continue".
				// But "Continue" makes the LLM allow generating the *rest*.
				// So the client receives: [Part 1] [Connection Break] [Part 2].
				// This works perfectly for concatenating text.

				w.Write(line)
				w.Write([]byte("\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}

				// Accumulate logic
				if bytes.HasPrefix(line, []byte("data: ")) {
					data := bytes.TrimPrefix(line, []byte("data: "))
					if string(data) == "[DONE]" {
						streamEndedSuccesfully = true
						// Make sure to write the final empty line that ends the `data: [DONE]` event
						w.Write([]byte("\n"))
						if f, ok := w.(http.Flusher); ok {
							f.Flush()
						}
						break
					}

					var chunk map[string]interface{}
					// Use a quick unmarshal to avoid overhead? No, safety first.
					if err := json.Unmarshal(data, &chunk); err == nil {
						if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
							if choice, ok := choices[0].(map[string]interface{}); ok {
								if delta, ok := choice["delta"].(map[string]interface{}); ok {
									// Content
									if content, ok := delta["content"].(string); ok {
										accumulatedResponse.WriteString(content)
										// Live update response in store? Maybe too frequent.
										// Update at end is safer for perf.
									}
									// Thinking (DeepSeek style or reasoning_content)
									// Check for different keys
									if thinking, ok := delta["reasoning_content"].(string); ok {
										accumulatedThinking.WriteString(thinking)
									} else if thinking, ok := delta["thinking"].(string); ok { // Some models use 'thinking'
										accumulatedThinking.WriteString(thinking)
									}

									// Tool Calls
									// Tool calls come in chunks too.
									// We need to accumulate them.
									// This is getting complex for a simple proxy.
									// For the MVP visualization, we might just want to store the FINAL accumulated result?
									// But we are streaming.
									// Actually, the Store updates happen at the END (or on retry).
									// So we just need to accumulate everything.
								}
							}
						}
					}
				}
			}

			// If we got here, scanner stopped.
			err = scanner.Err()
			if err != nil {
				lastErr = err
				if errors.Is(err, supervisor.ErrIdleTimeout) {
					log.Println("Stream idle timeout detected!")
					h.publishEvent("timeout_idle", map[string]interface{}{"timeout": conf.IdleTimeout.String(), "id": reqID})
					idleRetries++
					continue
				}
				if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
					log.Printf("Attempt %d generation deadline exceeded", attempt)
					h.publishEvent("error_deadline_exceeded", map[string]interface{}{"id": reqID})
					monitor.Close()
					genRetries++
					continue
				}
				log.Printf("Stream error: %v", err)
				h.publishEvent("stream_error", map[string]interface{}{"error": err.Error(), "id": reqID})
				monitor.Close()
				errorRetries++
				continue
			}

			// If scanner finished without error, we are likely done.
			if streamEndedSuccesfully {
				h.publishEvent("request_completed", map[string]interface{}{"id": reqID})

				reqLog.Status = "completed"
				reqLog.Response = accumulatedResponse.String()
				reqLog.Thinking = accumulatedThinking.String() // Save thinking
				reqLog.EndTime = time.Now()
				reqLog.Duration = time.Since(startTime).String() // Approximate
				h.store.Add(reqLog)
				monitor.Close()
				return
			}

			// If stream ended but no [DONE] and no error?
			// Could be unexpected EOF.
			log.Println("Stream ended unexpectedly without [DONE]")
			h.publishEvent("stream_ended_unexpectedly", map[string]interface{}{"id": reqID})
			monitor.Close()
			errorRetries++
		}

		log.Printf("Model %s failed (retries exhausted or unrecoverable error)", currentModel)
		h.publishEvent("error_max_upstream_error_retries", map[string]interface{}{"id": reqID})

		reqLog.Status = "failed"
		reqLog.Error = "Model failed"
		reqLog.EndTime = time.Now()
		reqLog.Duration = time.Since(startTime).String()
		h.store.Add(reqLog)

		// Check if there's a next model to fall back to
		if modelIndex+1 < len(modelList) {
			nextModel := modelList[modelIndex+1]
			reqLog.CurrentFallback = nextModel

			// Publish fallback triggered event for ALL transitions (including primary -> first fallback)
			h.publishEvent("fallback_triggered", events.FallbackEvent{
				FromModel: currentModel,
				ToModel:   nextModel,
				Reason:    determineFailureReason(lastErr, errorRetries, conf.MaxUpstreamErrorRetries, idleRetries, conf.MaxIdleRetries, genRetries, conf.MaxGenerationRetries),
			})
		}

		// Only track in "FallbackUsed" if the model that *just failed* was actually a fallback (not the primary)
		if modelIndex > 0 {
			reqLog.FallbackUsed = append(reqLog.FallbackUsed, currentModel)
		}
	} // End of outer model loop

	// If we reach here, all models have failed and we haven't sent any response to the client
	// Send an error response if headers haven't been sent yet
	if !headersSent {
		log.Printf("All models failed, sending error response to client")
		h.publishEvent("all_models_failed", map[string]interface{}{"id": reqID})
		http.Error(w, "All models failed after retries", http.StatusBadGateway)
	}
}

// determineFailureReason determines the reason for failure based on the last error and attempt count
func determineFailureReason(err error, errorRetries, maxUpstreamErrorRetries, idleRetries, maxIdleRetries, genRetries, maxGenRetries int) string {
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if err != nil && errors.Is(err, supervisor.ErrIdleTimeout) {
		return "idle_timeout"
	}
	if idleRetries > maxIdleRetries {
		return "max_idle_retries"
	}
	if genRetries > maxGenRetries {
		return "max_generation_retries"
	}
	if errorRetries > maxUpstreamErrorRetries {
		return "max_upstream_error_retries"
	}
	return "upstream_error"
}
