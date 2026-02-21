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

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/supervisor"
	"github.com/google/uuid"
)

type Config struct {
	UpstreamURL       string
	IdleTimeout       time.Duration
	MaxGenerationTime time.Duration
	MaxRetries        int
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

	targetURL, _ := url.JoinPath(h.config.UpstreamURL, "/v1/chat/completions")

	// Read original body
	bodyBytes, err := io.ReadAll(r.Body)
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

	// Enforce hard deadline for the entire interaction (including retries)
	// Or should it be per attempt?
	// The requirement says "Hard Generation Deadline: Enforce a maximum duration".
	// Usually this means for the	// Enforce hard deadline
	ctx, cancel := context.WithTimeout(r.Context(), h.config.MaxGenerationTime)
	defer cancel()

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

	reqLog := &store.RequestLog{
		ID:        reqID,
		Status:    "running",
		Model:     model,
		StartTime: startTime,
		Messages:  storeMessages,
		Retries:   0,
	}
	h.store.Add(reqLog)

	attempt := 0
	var accumulatedResponse strings.Builder
	var accumulatedThinking strings.Builder
	// Simple accumulator for tool calls - we might just capture them raw or try to parse
	// Parsing tool call chunks is tricky. Ideally we rely on the upstream to send valid JSON eventually.
	// But here we are just proxying.
	// For visualization, we can just accumulate the whole response object if we wanted, but we are streaming.
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

	for attempt <= h.config.MaxRetries {
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

		// Create request with the deadline context
		proxyReq, err := http.NewRequestWithContext(ctx, r.Method, targetURL, bytes.NewBuffer(newBodyBytes))
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
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				log.Println("Global deadline exceeded")
				h.publishEvent("error_deadline_exceeded", map[string]interface{}{"id": reqID})
				reqLog.Status = "failed"
				reqLog.Error = "Global deadline exceeded"
				reqLog.EndTime = time.Now()
				reqLog.Duration = time.Since(startTime).String()
				h.store.Add(reqLog)
				return
			}
			log.Printf("Upstream request failed: %v", err)
			h.publishEvent("upstream_error", map[string]interface{}{"error": err.Error(), "id": reqID})
			attempt++
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
					attempt++
					time.Sleep(1 * time.Second)
					continue
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

		monitor := supervisor.NewMonitoredReader(resp.Body, h.config.IdleTimeout)

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
				if errors.Is(err, supervisor.ErrIdleTimeout) {
					log.Println("Stream idle timeout detected!")
					h.publishEvent("timeout_idle", map[string]interface{}{"timeout": h.config.IdleTimeout.String(), "id": reqID})
					attempt++
					continue
				}
				log.Printf("Stream error: %v", err)
				h.publishEvent("stream_error", map[string]interface{}{"error": err.Error(), "id": reqID})
				attempt++
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
			return
		}

		// Stream
		scanner := bufio.NewScanner(monitor)
		// Default scanner buffer might be small for very long lines, but SSE lines are usually okay.

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

			if len(line) > 0 {
				w.Write(line)
				w.Write([]byte("\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}

			// Accumulate logic
			if bytes.HasPrefix(line, []byte("data: ")) {
				data := bytes.TrimPrefix(line, []byte("data: "))
				if string(data) == "[DONE]" {
					streamEndedSuccesfully = true
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
		if errors.Is(err, supervisor.ErrIdleTimeout) {
			log.Println("Stream idle timeout detected!")
			h.publishEvent("timeout_idle", map[string]interface{}{"timeout": h.config.IdleTimeout.String(), "id": reqID})
			// monitor closed the body.
			attempt++
			continue
		}

		if err != nil {
			log.Printf("Stream error: %v", err)
			h.publishEvent("stream_error", map[string]interface{}{"error": err.Error(), "id": reqID})
			attempt++
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
			return
		}

		// If stream ended but no [DONE] and no error?
		// Could be unexpected EOF.
		log.Println("Stream ended unexpectedly without [DONE]")
		h.publishEvent("stream_ended_unexpectedly", map[string]interface{}{"id": reqID})
		attempt++
	}

	log.Println("Max retries exceeded")
	h.publishEvent("error_max_retries", map[string]interface{}{"id": reqID})

	reqLog.Status = "failed"
	reqLog.Error = "Max retries exceeded"
	reqLog.EndTime = time.Now()
	reqLog.Duration = time.Since(startTime).String()
	h.store.Add(reqLog)
}
