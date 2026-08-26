package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/google/uuid"
)

// Debug mode for Anthropic endpoint
var debugAnthropic = os.Getenv("DEBUG_ANTHROPIC") == "1"

func debugLog(format string, args ...interface{}) {
	if debugAnthropic {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Anthropic Messages API Handler
// ─────────────────────────────────────────────────────────────────────────────

// HandleAnthropicMessages handles requests to the /v1/messages endpoint.
// It translates Anthropic Messages API requests to OpenAI Chat Completions format,
// proxies to upstream, and translates responses back to Anthropic format.
func (h *Handler) HandleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Close body when done (including error paths)
	defer r.Body.Close()

	// Parse Anthropic request
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		h.sendAnthropicError(w, "invalid_request_error", "Failed to read request body", http.StatusBadRequest)
		return
	}

	debugLog("=== INCOMING ANTHROPIC REQUEST ===")
	debugLog("Request Body: %s", string(bodyBytes))
	debugLog("Headers: %v", r.Header)

	var anthropicReq translator.AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &anthropicReq); err != nil {
		h.sendAnthropicError(w, "invalid_request_error", "Invalid JSON body", http.StatusBadRequest)
		return
	}

	debugLog("Model: %s", anthropicReq.Model)
	debugLog("Stream: %v", anthropicReq.Stream)
	debugLog("Messages Count: %d", len(anthropicReq.Messages))
	debugLog("MaxTokens: %d", anthropicReq.MaxTokens)
	debugLog("System: %v", anthropicReq.System)

	// Validate request
	if err := validateAnthropicRequest(&anthropicReq); err != nil {
		h.sendAnthropicError(w, "invalid_request_error", err.Error(), http.StatusBadRequest)
		return
	}

	// Get config snapshot
	conf := h.config.Clone()

	// For external upstream (default): always OpenAI protocol (LiteLLM)
	// For internal upstream: detection happens inside attemptAnthropicModel via credential.Provider
	isAnthropicUpstream := false

	// Build target URL — strip trailing /v1 from upstream to avoid double path
	cleanURL := strings.TrimSuffix(conf.UpstreamURL, "/v1")
	cleanURL = strings.TrimSuffix(cleanURL, "/")

	var targetURL string
	if isAnthropicUpstream {
		targetURL = cleanURL + "/v1/messages"
	} else {
		targetURL = cleanURL + "/v1/chat/completions"
	}

	// Build request body
	var requestBody []byte
	if isAnthropicUpstream {
		// Passthrough: use original Anthropic body as-is
		requestBody = bodyBytes
	} else {
		// Translate to OpenAI format
		openaiReq := translator.TranslateRequest(&anthropicReq, nil)
		var err error
		requestBody, err = json.Marshal(openaiReq)
		if err != nil {
			h.sendAnthropicError(w, "api_error", "Failed to translate request", http.StatusInternalServerError)
			return
		}
		debugLog("=== TRANSLATED OPENAI REQUEST ===")
		debugLog("OpenAI Body: %s", string(requestBody))
	}

	// Create request log
	reqID := uuid.New().String()
	startTime := time.Now()
	storeMessages := convertAnthropicMessagesToStore(anthropicReq.Messages)

	// Add system message if present (Anthropic has System as separate field)
	if anthropicReq.System != nil {
		systemContent := translator.TranslateSystem(anthropicReq.System)
		if systemContent != "" {
			storeMessages = append([]store.Message{{Role: "system", Content: systemContent}}, storeMessages...)
		}
	}

	isStream := anthropicReq.Stream

	// Extract app tag from x-proxy-app header for request grouping
	appTag := r.Header.Get("x-proxy-app")

	// Safely extract original model name
	originalModel := anthropicReq.Model

	reqLog := &store.RequestLog{
		ID:            reqID,
		Status:        "running",
		Model:         anthropicReq.Model,
		OriginalModel: originalModel,
		StartTime:     startTime,
		Messages:      storeMessages,
		IsStream:      isStream,
		Parameters: map[string]interface{}{
			"max_tokens": anthropicReq.MaxTokens,
			"endpoint":   "anthropic",
		},
		AppTag: appTag,
	}
	h.store.Add(reqLog)

	h.publishEvent("request_started", map[string]interface{}{"id": reqID})

	// Resolve model ID to config at the boundary
	var resolvedModel *models.ModelConfig
	if conf.ModelsConfig != nil {
		resolvedModel = conf.ModelsConfig.GetModel(originalModel)
	}

	// Build model list using resolved config
	var modelList []string
	if resolvedModel != nil {
		// Known model — use ID and fallback chain from config
		modelList = make([]string, 0, 1+len(resolvedModel.FallbackChain))
		modelList = append(modelList, resolvedModel.ID)
		modelList = append(modelList, resolvedModel.FallbackChain...)
	} else {
		// External/unknown model — no fallback chain, just use raw name
		modelList = []string{originalModel}
	}

	// Set ModelID to the resolved ID (or raw name for external models)
	if resolvedModel != nil {
		conf.ModelID = resolvedModel.ID
	} else {
		conf.ModelID = originalModel
	}

	// Create anthropic request context
	arc := &anthropicRequestContext{
		conf:                conf,
		targetURL:           targetURL,
		reqID:               reqID,
		startTime:           startTime,
		reqLog:              reqLog,
		modelList:           modelList,
		anthropicReq:        &anthropicReq,
		requestBody:         requestBody,
		originalBody:        bodyBytes,
		isStream:            isStream,
		originalModel:       anthropicReq.Model,
		baseCtx:             r.Context(),
		method:              r.Method,
		originalHeaders:     r.Header,
		isAnthropicUpstream: isAnthropicUpstream,
	}

	// Phase 3 / Task 16 — Anthropic-path affinity plumbing.
	//
	// Cache firstUserMessage here (cheap canonical walk over the
	// Anthropic-shape messages: role=="user" → first content-bearing
	// message). The Anthropic wire-shape content is string OR
	// []ContentPart (multimodal); ExtractFirstUserMessage expects the
	// OpenAI-shape messages (role/user/content map) — for Anthropic, the
	// helper still works because the role + content fields line up after
	// our pre-processing. For multimodals we canonicalize the JSON
	// like the OpenAI path does.
	//
	// Resolve auth (mirrors handler.go:411-437 — the OpenAI path's
	// POST-AUTH wiring site). h.authenticate returns (nil, true) when
	// tokenStore is nil (auth disabled), so the call is a no-op when
	// auth is off; when the client sends no/invalid Bearer we also get
	// nil and degrade to the unsalted key (A-1 graceful degradation).
	// Only when a valid sk- token is presented does arc.tokenID take
	// the token's ID, salting the conversationKey per-principal.
	var arcTokenID string
	if h.tokenStore != nil {
		if authToken, _ := h.authenticate(r); authToken != nil {
			arcTokenID = authToken.ID
		}
	}
	if resolvedModel != nil && resolvedModel.Internal {
		arc.firstUserMessage = credentiallb.ExtractFirstUserMessage(arcMessagesAsInterfaces(anthropicReq.Messages))
		// POST-AUTH wiring (Task 16 / A-1): arcTokenID carries the
		// authenticated token's ID for salted conversation keys (the
		// OPENAI-path parity fix landed in W3 at handler.go:431-474).
		// Anonymous requests (no token / invalid token / auth disabled)
		// keep arcTokenID == "" → unsalted key — accepted residual
		// risk per A-1.
		//
		// W-2 + C2 (mirrors the chat-completions post-auth site): when no
		// first user message was extracted the key stays "" — the engine
		// picks FRESH per request and stores NO binding; the
		// ""-as-own-bucket reading is REMOVED per W-2. The caller owns
		// this gate (ComputeConversationKey never returns "" by itself).
		if arc.firstUserMessage == "" {
			arc.conversationKey = ""
			log.Printf("[LB] (anthropic) empty conversationKey for modelID=%s; engine will pick fresh per request (no binding stored)",
				resolvedModel.ID)
		} else {
			arc.conversationKey = credentiallb.ComputeConversationKey(resolvedModel.ID, arcTokenID, arc.firstUserMessage)
			tokDisp := arcTokenID
			if tokDisp == "" {
				tokDisp = "<empty>"
			}
			log.Printf("[LB] (anthropic) computed conversationKey hash=%s modelID=%s tokenID=%s len(firstUserMessage)=%d",
				arc.conversationKey[:8],
				resolvedModel.ID,
				tokDisp,
				len(arc.firstUserMessage),
			)
		}
	}

	// Outer loop: iterate through models (original + fallbacks)
	for modelIndex, currentModel := range arc.modelList {
		if modelIndex > 0 {
			log.Printf("Attempting fallback model: %s (index %d)", currentModel, modelIndex)
		}

		if arc.baseCtx.Err() != nil {
			log.Printf("Client disconnected, failing request")
			break
		}

		// Save mutable arc state for restoration on failure
		savedIsAnthropicUpstream := arc.isAnthropicUpstream
		savedTargetURL := arc.targetURL
		savedCredentialAPIKey := arc.credentialAPIKey
		savedRequestBody := make([]byte, len(arc.requestBody))
		copy(savedRequestBody, arc.requestBody)
		savedAnthropicReqModel := arc.anthropicReq.Model

		// Update model in request body
		if arc.isAnthropicUpstream {
			// For passthrough, unmarshal, update model, re-marshal
			var reqBody map[string]interface{}
			if err := json.Unmarshal(arc.requestBody, &reqBody); err != nil {
				log.Printf("Failed to unmarshal request body for model update: %v", err)
				continue
			}
			reqBody["model"] = currentModel
			newBody, err := json.Marshal(reqBody)
			if err != nil {
				log.Printf("Failed to marshal request body for model update: %v", err)
				continue
			}
			arc.requestBody = newBody
		} else {
			// For OpenAI translation path, re-translate with new model
			arc.anthropicReq.Model = currentModel
			openaiReq := translator.TranslateRequest(arc.anthropicReq, nil)
			newBody, err := json.Marshal(openaiReq)
			if err != nil {
				log.Printf("Failed to marshal translated request: %v", err)
				continue
			}
			arc.requestBody = newBody
		}

		success := h.attemptAnthropicModel(w, arc, modelIndex, currentModel)
		if success {
			return
		}

		// Restore arc state for next fallback iteration
		arc.isAnthropicUpstream = savedIsAnthropicUpstream
		arc.targetURL = savedTargetURL
		arc.credentialAPIKey = savedCredentialAPIKey
		arc.requestBody = savedRequestBody
		arc.anthropicReq.Model = savedAnthropicReqModel
		// W4: a failed attempt (internal or otherwise) may have left partial
		// accumulation in arc.accumulatedThinking — e.g. a failed internal
		// attempt captures thinking into its sink mid-stream before the
		// failure. Persisting that would double-accumulate or pollute the
		// fallback's persisted thinking, so drop it before the next
		// attempt: the fallback's persisted thinking must carry ONLY the
		// fallback's own thinking.
		arc.accumulatedThinking.Reset()

		arc.reqLog.Status = "failed"
		arc.reqLog.Error = "Model failed"
	}

	// All models failed
	log.Printf("All models failed for Anthropic request")
	h.publishEvent("all_models_failed", map[string]interface{}{"id": arc.reqID})

	if !arc.headersSent {
		// Use stored error if available, otherwise send generic error
		if arc.lastError != nil && arc.lastStatusCode > 0 {
			translatedError, _ := translator.TranslateError(arc.lastError, arc.lastStatusCode)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(arc.lastStatusCode)
			w.Write(translatedError)
		} else {
			h.sendAnthropicError(w, "api_error", "All models failed after retries", http.StatusBadGateway)
		}
	} else {
		// Headers already sent (streaming) - send SSE error event
		h.sendAnthropicSSEError(w, "api_error", "All models failed after retries")
	}
}

// attemptAnthropicModel attempts a single model request
func (h *Handler) attemptAnthropicModel(w http.ResponseWriter, arc *anthropicRequestContext, modelIndex int, currentModel string) bool {
	// Check if this model uses internal upstream
	modelConfig := arc.conf.ModelsConfig.GetModel(currentModel)
	isInternal := modelConfig != nil && modelConfig.Internal

	if arc.baseCtx.Err() != nil {
		return true // Client disconnected
	}

	var success bool
	if isInternal {
		// Phase 3 / Task 22 (Round 3b leader ruling (i)) — the
		// anthropic-passthrough internal branch now selects its
		// credential through the AFFINITY SEAM (the pre-Phase-3 read
		// was the single-credential PrimaryCredentialID()). This is the
		// second ResolveInternalConfigWithAffinity call site in this
		// file (the first is the InternalHandler construction inside
		// doAnthropicInternalRequest) — the branch participates in
		// conversation-sticky LB like the other three LB'd paths.
		//
		// The resolved credential's PROVIDER decides passthrough vs the
		// internal OpenAI handler (anthropic-provider credential ⇒ raw
		// passthrough via doAnthropicRequest; transport UNCHANGED per
		// the dispatch ruling — only the credential read + the
		// rate-limit hook are new).
		resolved, resolvedOK := arc.conf.ModelsConfig.ResolveInternalConfigWithAffinity(modelConfig.ID, arc.conversationKey)
		var cred *models.CredentialConfig
		if resolvedOK {
			cred = arc.conf.ModelsConfig.GetCredential(resolved.CredentialID)
		}
		credProvider := ""
		if cred != nil {
			credProvider = strings.ToLower(cred.Provider)
		}
		if credProvider == "anthropic" {
			// Anthropic provider — use passthrough mode
			arc.isAnthropicUpstream = true
			// Build passthrough target URL from credential's base_url or model config
			passthroughURL := modelConfig.InternalBaseURL
			credAPIKey := ""
			if cred != nil {
				if passthroughURL == "" {
					passthroughURL = cred.BaseURL
				}
				credAPIKey = cred.ResolveAPIKey()
			}
			if passthroughURL == "" {
				passthroughURL = arc.conf.UpstreamURL
			}
			cleanURL := strings.TrimSuffix(passthroughURL, "/v1")
			cleanURL = strings.TrimSuffix(cleanURL, "/")
			arc.targetURL = cleanURL + "/v1/messages"
			// Use original Anthropic body (not translated)
			arc.requestBody = make([]byte, len(arc.originalBody))
			copy(arc.requestBody, arc.originalBody)
			// Set the actual upstream model name
			if modelConfig.InternalModel != "" {
				var reqBody map[string]interface{}
				if json.Unmarshal(arc.requestBody, &reqBody) == nil {
					reqBody["model"] = modelConfig.InternalModel
					var err error
					arc.requestBody, err = json.Marshal(reqBody)
					if err != nil {
						log.Printf("Failed to marshal request body for model update: %v", err)
						return false // Return false instead of proceeding with stale body
					}
				}
			}
			// Set credential API key on arc for doAnthropicRequest to use
			if credAPIKey != "" {
				arc.credentialAPIKey = credAPIKey
			}
			success = h.doAnthropicRequest(w, arc)

			// Phase 3 / Task 22 (Round 3b review #4 + Round 3c — W1):
			// rate-limit failover hook on the anthropic-passthrough branch.
			// The branch produces NO *ProviderError; classify via
			// arc.lastStatusCode == 429 OR body `type` substring
			// `rate_limit` (isAnthropicRateLimit). Pre-write guard:
			// !arc.headersSent (no Anthropic bytes written — the
			// first-byte marker is handler_anthropic.go:648-654, NOT
			// adapter_anthropic.go SetStreamHeaders). Multi-cred +
			// engine present; single retry per mode (R3-5 budget is
			// structurally bounded — one retry ≤ len(credentials)−1).
			if !success &&
				!arc.headersSent &&
				isAnthropicRateLimit(arc) &&
				h.credEngine != nil &&
				resolvedOK &&
				len(modelConfig.Credentials) > 1 {
				reselected, mode := h.credEngine.ExcludeAndReselect(
					modelConfig.ID,
					arc.conversationKey,
					resolved.CredentialID,
					arc.retryAfter,
				)
				switch mode {
				case credentiallb.ReselectNone:
					// C2 narrowing: no credential available — fall
					// through to the model loop's failure handling.
					log.Printf("[LB-FAILOVER] anthropic-passthrough: model=%s no reselectable credential (mode=%v); falling through", modelConfig.ID, mode)
				default:
					// ReselectHealthy (incl. B2 no-op + empty-key fresh
					// pick per C2) OR ReselectSoonestExpiry (single
					// attempt — this hook fires once per attempt, so
					// the single-shot property is structural).
					if mode == credentiallb.ReselectSoonestExpiry {
						log.Printf("[WARN] [LB-FAILOVER] anthropic-passthrough: model=%s all credentials cooling; single attempt with soonest-expiring cred=%s", modelConfig.ID, reselected)
					}
					if reselected == "" || reselected == resolved.CredentialID {
						break // nothing new to try — propagate
					}
					newCred := arc.conf.ModelsConfig.GetCredential(reselected)
					if newCred == nil {
						break // deleted mid-flight — propagate
					}
					newKey := newCred.ResolveAPIKey()
					if newKey == "" {
						break
					}
					log.Printf("[LB-FAILOVER] anthropic-passthrough: model=%s rate-limited on cred=%s; failing over to cred=%s", modelConfig.ID, resolved.CredentialID, reselected)
					// Task 20 — model_credential_failover event.
					h.publishAnthropicCredentialFailover(modelConfig.ID, resolved.CredentialID, reselected, arc.retryAfter)
					// Re-apply with the reselected credential: API key
					// AND the new credential's base URL (a failover
					// credential may live on a different deployment).
					retryURL := modelConfig.InternalBaseURL
					if retryURL == "" {
						retryURL = newCred.BaseURL
					}
					if retryURL != "" {
						rc := strings.TrimSuffix(retryURL, "/v1")
						rc = strings.TrimSuffix(rc, "/")
						arc.targetURL = rc + "/v1/messages"
					}
					arc.credentialAPIKey = newKey
					// Reset pre-write state for the retry.
					arc.lastError = nil
					arc.lastStatusCode = 0
					arc.retryAfter = 0
					success = h.doAnthropicRequest(w, arc)
				}
			}
		} else {
			success = h.doAnthropicInternalRequest(w, arc, modelConfig)
		}
	} else {
		success = h.doAnthropicRequest(w, arc)
	}

	return success
}

// doAnthropicRequest performs a single upstream request
func (h *Handler) doAnthropicRequest(w http.ResponseWriter, arc *anthropicRequestContext) bool {
	// Create HTTP request
	req, err := http.NewRequestWithContext(arc.baseCtx, arc.method, arc.targetURL, bytes.NewReader(arc.requestBody))
	if err != nil {
		log.Printf("Failed to create Anthropic upstream request: %v", err)
		return false
	}

	// Copy headers based on upstream protocol
	if arc.isAnthropicUpstream {
		copyAnthropicHeadersPassthrough(req, arc.originalHeaders)
	} else {
		copyAnthropicHeaders(req, arc.originalHeaders)
	}

	// Resolve credential and set auth header
	if arc.conf.UpstreamCredentialID != "" {
		req.Header.Del("Authorization")
		req.Header.Del("X-API-Key")
		req.Header.Del("x-api-key")
		req.Header.Del("api-key")

		cred := arc.conf.ModelsConfig.GetCredential(arc.conf.UpstreamCredentialID)
		if cred != nil {
			apiKey := cred.ResolveAPIKey()
			if apiKey != "" {
				if arc.isAnthropicUpstream {
					req.Header.Set("x-api-key", apiKey)
				} else {
					req.Header.Set("Authorization", "Bearer "+apiKey)
				}
			}
		}
	} else if arc.credentialAPIKey != "" {
		// Internal model passthrough: use credential's API key
		req.Header.Del("Authorization")
		req.Header.Del("X-API-Key")
		req.Header.Del("x-api-key")
		req.Header.Del("api-key")
		if arc.isAnthropicUpstream {
			req.Header.Set("x-api-key", arc.credentialAPIKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+arc.credentialAPIKey)
		}
	}

	// Send request
	resp, err := h.client.Do(req)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		log.Printf("Anthropic upstream request failed: %v", err)
		return false
	}
	defer resp.Body.Close()

	// Handle non-OK status
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("Anthropic upstream returned %d: %s", resp.StatusCode, string(bodyBytes))
		debugLog("=== UPSTREAM ERROR RESPONSE ===")
		debugLog("Status: %d", resp.StatusCode)
		debugLog("Body: %s", string(bodyBytes))
		arc.lastError = bodyBytes
		arc.lastStatusCode = resp.StatusCode
		// Phase 3 / Task 22 (Round 3c — W1): the anthropic-passthrough
		// branch produces no *ProviderError, so Retry-After cannot be
		// recovered from the provider-error struct. Capture it here at
		// the existing lastStatusCode set-site so the rate-limit hook
		// can use it as the cooldown seed without re-parsing the body.
		captureRetryAfterHeader(arc, resp)
		return false
	}

	// Handle response based on upstream protocol
	if arc.isAnthropicUpstream {
		// Passthrough: forward upstream response as-is (already in Anthropic format)
		if arc.isStream {
			return h.handlePassthroughStreamResponse(w, resp, arc)
		}
		return h.handlePassthroughNonStreamResponse(w, resp, arc)
	}

	// Translation mode: translate OpenAI response to Anthropic format
	if arc.isStream {
		return h.handleAnthropicStreamResponse(w, resp, arc)
	}
	return h.handleAnthropicNonStreamResponse(w, resp, arc)
}

// flushingResponseRecorder wraps httptest.ResponseRecorder to implement http.Flusher
type flushingResponseRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushingResponseRecorder) Flush() {
	// No-op: the ResponseRecorder already captures all written data
}

// doAnthropicInternalRequest handles requests for internal models (direct provider calls)
// It uses the InternalHandler to call the provider directly, then translates the response
// from OpenAI format to Anthropic format.
func (h *Handler) doAnthropicInternalRequest(w http.ResponseWriter, arc *anthropicRequestContext, modelConfig *models.ModelConfig) bool {
	// Parse the OpenAI request body
	var openaiReq map[string]interface{}
	if err := json.Unmarshal(arc.requestBody, &openaiReq); err != nil {
		log.Printf("Failed to parse OpenAI request body: %v", err)
		return false
	}

	// Create a response recorder that supports flushing (for streaming)
	recorder := &flushingResponseRecorder{httptest.NewRecorder()}

	log.Printf("[DEBUG ANTHROPIC] Creating InternalHandler for model: %s", modelConfig.ID)

	// Side-channel sink for thinking/reasoning text. The InternalHandler's
	// case "thinking" arm deliberately does NOT write reasoning_content SSE
	// chunks to `w`, because the recorder body is fed to
	// translator.TranslateBufferedStream which would emit Anthropic thinking
	// blocks to the client (violating byte-identity with the pre-fix
	// behaviour). Instead we accumulate the thinking text in this builder and
	// copy it into arc.accumulatedThinking below for persistence.
	var capturedThinking strings.Builder

	// Use InternalHandler to make the request
	internalHandler := NewInternalHandler(modelConfig, arc.conf.ModelsConfig)
	// Phase 3 / Task 22 — install the engine + bus so the
	// InternalHandler seam owns the /v1/messages rate-limit failover
	// hook (ExcludeAndReselect + single retry) for this request. The
	// handler is constructed fresh above, so per-request state (sink,
	// failover hooks) cannot leak across requests.
	internalHandler.SetCredentialFailover(h.credEngine, h.bus)
	// The handler is created fresh above, so it has no prior sink and this
	// call can never double-set; treat an error as a programming bug and
	// abort the internal attempt rather than persisting missing thinking.
	if err := internalHandler.SetThinkingSink(&capturedThinking); err != nil {
		log.Printf("[DEBUG ANTHROPIC] Failed to install thinking sink (programming error): %v", err)
		arc.lastError = []byte(err.Error())
		arc.lastStatusCode = http.StatusBadGateway
		return false
	}
	err := internalHandler.HandleRequest(arc.baseCtx, openaiReq, recorder, arc.isStream, arc.conversationKey)
	if err != nil {
		log.Printf("[DEBUG ANTHROPIC] Internal request failed: %v", err)
		arc.lastError = []byte(err.Error())
		arc.lastStatusCode = http.StatusBadGateway
		return false
	}

	// Check response status
	if recorder.Code != http.StatusOK {
		arc.lastError = recorder.Body.Bytes()
		arc.lastStatusCode = recorder.Code
		log.Printf("[DEBUG ANTHROPIC] Internal request returned %d: %s", recorder.Code, string(arc.lastError))
		return false
	}

	// Persist any captured thinking text (side channel) so the final
	// store.Message.Thinking field still gets populated. This runs BEFORE
	// the response handler because handleAnthropicInternalStreamResponse
	// calls extractOpenAIResponseContentFromSSE, which will now return an
	// empty thinking string (the recorder no longer carries thinking SSE).
	//
	// W4: this copy is deliberately gated under the SUCCESS path
	// (HandleRequest returned nil AND recorder is 200). A failed internal
	// attempt may have partially captured thinking before the failure;
	// copying it here would leave the partial text in
	// arc.accumulatedThinking and pollute the fallback's persisted
	// thinking. Partial captures from failed attempts are discarded along
	// with the local builder.
	if capturedThinking.Len() > 0 {
		arc.accumulatedThinking.WriteString(capturedThinking.String())
	}

	log.Printf("[DEBUG ANTHROPIC] Recorder body length: %d bytes", recorder.Body.Len())
	log.Printf("[DEBUG ANTHROPIC] Recorder body preview: %.200s", recorder.Body.String()[:200])

	// Translate response from OpenAI to Anthropic format
	if arc.isStream {
		log.Printf("[DEBUG ANTHROPIC] Calling handleAnthropicInternalStreamResponse")
		return h.handleAnthropicInternalStreamResponse(w, recorder.Body.Bytes(), arc)
	}
	log.Printf("[DEBUG ANTHROPIC] Calling handleAnthropicInternalNonStreamResponse")
	return h.handleAnthropicInternalNonStreamResponse(w, recorder.Body.Bytes(), arc)
}

// handleAnthropicInternalNonStreamResponse handles non-streaming internal responses
func (h *Handler) handleAnthropicInternalNonStreamResponse(w http.ResponseWriter, openaiBody []byte, arc *anthropicRequestContext) bool {
	debugLog("=== INTERNAL OPENAI RESPONSE ===")
	debugLog("OpenAI Body: %s", string(openaiBody))

	// Extract content for storage before translation
	content, thinking, toolCalls := extractOpenAIResponseContentFromJSON(openaiBody)
	arc.accumulatedResponse.WriteString(content)
	arc.accumulatedThinking.WriteString(thinking)
	arc.accumulatedToolCalls = append(arc.accumulatedToolCalls, toolCalls...)

	// Translate to Anthropic format
	anthropicResp, err := translator.TranslateNonStreamResponse(openaiBody, arc.originalModel)
	if err != nil {
		log.Printf("Failed to translate Anthropic internal response: %v", err)
		return false
	}

	debugLog("=== ANTHROPIC RESPONSE ===")
	debugLog("Anthropic Body: %s", string(anthropicResp))

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(anthropicResp)

	// Finalize with assistant message
	h.finalizeAnthropicSuccess(arc)

	return true
}

// handleAnthropicInternalStreamResponse handles streaming internal responses
func (h *Handler) handleAnthropicInternalStreamResponse(w http.ResponseWriter, openaiBody []byte, arc *anthropicRequestContext) bool {
	// Extract content for storage before translation
	content, thinking, toolCalls := extractOpenAIResponseContentFromSSE(openaiBody)
	arc.accumulatedResponse.WriteString(content)
	arc.accumulatedThinking.WriteString(thinking)
	arc.accumulatedToolCalls = append(arc.accumulatedToolCalls, toolCalls...)

	// Translate buffered OpenAI stream to Anthropic format
	anthropicEvents, err := translator.TranslateBufferedStream(openaiBody, arc.originalModel)
	if err != nil {
		log.Printf("Failed to translate Anthropic internal stream: %v", err)
		h.sendAnthropicSSEError(w, "api_error", "Stream translation failed")
		return true // Don't retry after headers sent
	}

	// Send headers if not already sent
	if !arc.headersSent {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		arc.headersSent = true

		// Send initial comment to establish byte stream
		w.Write([]byte(": connected\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	// Write translated events
	w.Write(anthropicEvents)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Finalize with assistant message
	h.finalizeAnthropicSuccess(arc)

	return true
}

// handleAnthropicNonStreamResponse handles a non-streaming response
func (h *Handler) handleAnthropicNonStreamResponse(w http.ResponseWriter, resp *http.Response, arc *anthropicRequestContext) bool {
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		log.Printf("Failed to read Anthropic upstream response: %v", err)
		return false
	}

	// Extract content for storage before translation
	content, thinking, toolCalls := extractOpenAIResponseContentFromJSON(bodyBytes)
	arc.accumulatedResponse.WriteString(content)
	arc.accumulatedThinking.WriteString(thinking)
	arc.accumulatedToolCalls = append(arc.accumulatedToolCalls, toolCalls...)

	// Translate to Anthropic format
	anthropicResp, err := translator.TranslateNonStreamResponse(bodyBytes, arc.originalModel)
	if err != nil {
		log.Printf("Failed to translate Anthropic response: %v", err)
		return false
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(anthropicResp)

	// Finalize with assistant message
	h.finalizeAnthropicSuccess(arc)

	return true
}

// handleAnthropicStreamResponse handles a streaming response
func (h *Handler) handleAnthropicStreamResponse(w http.ResponseWriter, resp *http.Response, arc *anthropicRequestContext) bool {
	debugLog("=== STREAM RESPONSE START ===")
	debugLog("Request ID: %s", arc.reqID)
	debugLog("Model: %s", arc.originalModel)

	// Send headers immediately for TTFB
	if !arc.headersSent {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		arc.headersSent = true

		// Send initial comment to establish byte stream
		w.Write([]byte(": connected\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		debugLog("Headers sent, connection established")
	}

	// Buffer all OpenAI chunks
	var buffer bytes.Buffer
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // Increase max token size from default 64KB to 1MB
	chunkCount := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		chunkCount++

		// Buffer the line
		buffer.Write(line)
		buffer.WriteByte('\n')

		// Log chunk (truncated for large content)
		lineStr := string(line)
		if len(lineStr) > 200 {
			debugLog("Chunk #%d: %s...", chunkCount, lineStr[:200])
		} else {
			debugLog("Chunk #%d: %s", chunkCount, lineStr)
		}

		// Check for [DONE]
		if bytes.HasPrefix(line, []byte("data: [DONE]")) {
			debugLog("Stream complete, received %d chunks, translating...", chunkCount)

			// Extract content for storage before translation
			content, thinking, toolCalls := extractOpenAIResponseContentFromSSE(buffer.Bytes())
			arc.accumulatedResponse.WriteString(content)
			arc.accumulatedThinking.WriteString(thinking)
			arc.accumulatedToolCalls = append(arc.accumulatedToolCalls, toolCalls...)

			// Translate buffered stream and flush
			anthropicEvents, err := translator.TranslateBufferedStream(buffer.Bytes(), arc.originalModel)
			if err != nil {
				log.Printf("Failed to translate Anthropic stream: %v", err)
				h.sendAnthropicSSEError(w, "api_error", "Stream translation failed")
				return true // Don't retry after headers sent
			}

			// Log translated events
			eventLines := strings.Split(string(anthropicEvents), "\n")
			debugLog("=== TRANSLATED EVENTS (%d lines) ===", len(eventLines))
			for i, eventLine := range eventLines {
				if strings.TrimSpace(eventLine) != "" {
					debugLog("Event line %d: %s", i+1, eventLine)
				}
			}

			w.Write(anthropicEvents)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

			// Finalize with assistant message
			h.finalizeAnthropicSuccess(arc)

			debugLog("=== STREAM COMPLETE ===")
			return true
		}
	}

	// Stream ended without [DONE]
	if err := scanner.Err(); err != nil {
		log.Printf("Anthropic stream error: %v", err)
	}

	h.sendAnthropicSSEError(w, "api_error", "Stream ended unexpectedly")
	return true // Don't retry after headers sent
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper Functions
// ─────────────────────────────────────────────────────────────────────────────

// anthropicRequestContext holds state for an Anthropic request
type anthropicRequestContext struct {
	conf                ConfigSnapshot
	targetURL           string
	reqID               string
	startTime           time.Time
	reqLog              *store.RequestLog
	modelList           []string
	anthropicReq        *translator.AnthropicRequest
	requestBody         []byte // request body to send (translated or passthrough)
	originalBody        []byte // original Anthropic body (always preserved)
	isStream            bool
	originalModel       string
	baseCtx             context.Context
	method              string
	originalHeaders     http.Header
	headersSent         bool
	lastError           []byte
	lastStatusCode      int
	isAnthropicUpstream bool   // true when upstream speaks Anthropic protocol
	credentialAPIKey    string // resolved API key from model's credential (for internal passthrough)

	// Phase 3 / Task 16 — Anthropic-path affinity plumbing.
	//
	// firstUserMessage is cached at attempt-anthropic-model time (cheap
	// walk over Anthropic-shape messages). conversationKey is computed
	// POST-AUTH on the Anthropic path equivalent of handler.go:401+
	// (HandleAnthropicMessages after auth populates authToken / tokenID),
	// then threaded through NewInternalHandler.HandleRequest so
	// /v1/messages internal resolution uses ResolveInternalConfigWithAffinity.
	//
	// Retry-After (Round 3c — W1): the anthropic-passthrough branch
	// produces NO *ProviderError; doAnthropicRequest discards
	// resp.Header. captured at the existing arc.lastStatusCode set-site
	// (handler_anthropic.go:423-424) so the Task 22 W1 classifier +
	// ExcludeAndReselect retry can read it.
	firstUserMessage string
	conversationKey  string
	retryAfter       time.Duration

	// Response tracking (for storing assistant message)
	accumulatedResponse  strings.Builder
	accumulatedThinking  strings.Builder
	accumulatedToolCalls []store.ToolCall
}

// sendAnthropicError sends an error response in Anthropic format
func (h *Handler) sendAnthropicError(w http.ResponseWriter, errorType, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	errorResp := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errorType,
			"message": message,
		},
	}
	errBytes, err := json.Marshal(errorResp)
	if err != nil {
		log.Printf("failed to marshal error response: %v", err)
		return
	}
	w.Write(errBytes)
}

// finalizeAnthropicSuccess updates the request log and appends the assistant message.
// This is the equivalent of finalizeSuccess in handler_functions.go.
func (h *Handler) finalizeAnthropicSuccess(arc *anthropicRequestContext) {
	// Build assistant message from accumulated response
	assistantMsg := store.Message{
		Role:     "assistant",
		Content:  arc.accumulatedResponse.String(),
		Thinking: arc.accumulatedThinking.String(),
	}

	// Include tool calls if any were accumulated
	if len(arc.accumulatedToolCalls) > 0 {
		assistantMsg.ToolCalls = arc.accumulatedToolCalls
	}

	// Append to messages array
	arc.reqLog.Messages = append(arc.reqLog.Messages, assistantMsg)

	// Update status and timing
	arc.reqLog.Status = "completed"
	arc.reqLog.EndTime = time.Now()
	arc.reqLog.Duration = time.Since(arc.startTime).String()
	h.store.Add(arc.reqLog)
	h.publishEvent("request_completed", map[string]interface{}{"id": arc.reqID})
}

// extractOpenAIResponseContent extracts content, thinking, and tool calls from OpenAI response.
func extractOpenAIResponseContentFromJSON(openaiBody []byte) (content, thinking string, toolCalls []store.ToolCall) {
	var resp map[string]interface{}
	if err := json.Unmarshal(openaiBody, &resp); err != nil {
		return "", "", nil
	}

	choices, _ := resp["choices"].([]interface{})
	if len(choices) == 0 {
		return "", "", nil
	}

	choice, _ := choices[0].(map[string]interface{})
	message, _ := choice["message"].(map[string]interface{})
	if message == nil {
		return "", "", nil
	}

	// Extract content
	content, _ = message["content"].(string)

	// Extract thinking (from reasoning_content if present)
	thinking, _ = message["reasoning_content"].(string)

	// Extract tool calls
	if tcs, ok := message["tool_calls"].([]interface{}); ok {
		for _, tc := range tcs {
			if tcMap, ok := tc.(map[string]interface{}); ok {
				toolCall := store.ToolCall{
					ID:   getStringVal(tcMap, "id"),
					Type: getStringVal(tcMap, "type"),
				}
				if fn, ok := tcMap["function"].(map[string]interface{}); ok {
					toolCall.Function.Name = getStringVal(fn, "name")
					toolCall.Function.Arguments = getStringVal(fn, "arguments")
				}
				toolCalls = append(toolCalls, toolCall)
			}
		}
	}
	return content, thinking, toolCalls
}

// extractOpenAIResponseContentFromSSE extracts content, thinking, and tool calls from buffered OpenAI SSE lines.
// Unlike FromJSON, this parses each "data: {...}" line and accumulates content from streaming deltas.
func extractOpenAIResponseContentFromSSE(sseBuffer []byte) (content, thinking string, toolCalls []store.ToolCall) {
	scanner := bufio.NewScanner(bytes.NewReader(sseBuffer))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()

		// Only process data lines
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}

		data := bytes.TrimPrefix(line, []byte("data: "))

		// Skip [DONE] marker
		if bytes.Equal(data, []byte("[DONE]")) {
			continue
		}

		// Parse the chunk JSON
		var chunk map[string]interface{}
		if err := json.Unmarshal(data, &chunk); err != nil {
			log.Printf("extractOpenAIResponseContentFromSSE: skipping malformed chunk: %v", err)
			continue
		}

		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}

		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}

		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}

		// Accumulate text content
		if c, ok := delta["content"].(string); ok {
			content += c
		}

		// Accumulate thinking (reasoning_content or thinking field)
		if r, ok := delta["reasoning_content"].(string); ok {
			thinking += r
		}
		if t, ok := delta["thinking"].(string); ok {
			thinking += t
		}

		// Accumulate tool calls by index
		if tcs, ok := delta["tool_calls"].([]interface{}); ok {
			for _, tc := range tcs {
				tcMap, ok := tc.(map[string]interface{})
				if !ok {
					continue
				}

				index := 0
				if idx, ok := tcMap["index"].(float64); ok {
					index = int(idx)
				}

				// Ensure we have enough slots
				for len(toolCalls) <= index {
					toolCalls = append(toolCalls, store.ToolCall{})
				}

				if id, ok := tcMap["id"].(string); ok && id != "" {
					toolCalls[index].ID = id
				}

				if fn, ok := tcMap["function"].(map[string]interface{}); ok {
					if name, ok := fn["name"].(string); ok {
						toolCalls[index].Function.Name = name
					}
					if args, ok := fn["arguments"].(string); ok {
						toolCalls[index].Function.Arguments += args
					}
				}
			}
		}
	}

	return content, thinking, toolCalls
}

// getStringVal safely extracts a string from a map
func getStringVal(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// sendAnthropicSSEError sends an error as an SSE event in Anthropic format
func (h *Handler) sendAnthropicSSEError(w http.ResponseWriter, errorType, message string) {
	errorEvent := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errorType,
			"message": message,
		},
	}
	eventBytes, err := json.Marshal(errorEvent)
	if err != nil {
		log.Printf("failed to marshal error response: %v", err)
		return
	}
	fmt.Fprintf(w, "event: error\ndata: %s\n\n", string(eventBytes))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// validateAnthropicRequest validates an Anthropic request
func validateAnthropicRequest(req *translator.AnthropicRequest) error {
	if req.Model == "" {
		return fmt.Errorf("model is required")
	}
	if req.MaxTokens == 0 {
		return fmt.Errorf("max_tokens is required")
	}
	if len(req.Messages) == 0 {
		return fmt.Errorf("messages is required")
	}
	for _, msg := range req.Messages {
		if msg.Role != "user" && msg.Role != "assistant" {
			return fmt.Errorf("invalid role: %s (must be 'user' or 'assistant')", msg.Role)
		}
	}
	return nil
}

// copyAnthropicHeadersPassthrough forwards all original Anthropic headers as-is.
// Unlike copyAnthropicHeaders, this does NOT convert x-api-key to Authorization Bearer.
func copyAnthropicHeadersPassthrough(dst *http.Request, src http.Header) {
	for name, values := range src {
		switch strings.ToLower(name) {
		case "content-length", "host":
			continue
		}
		for _, value := range values {
			dst.Header.Add(name, value)
		}
	}
	dst.Header.Set("Content-Type", "application/json")
}

// handlePassthroughNonStreamResponse forwards a non-streaming Anthropic response as-is.
func (h *Handler) handlePassthroughNonStreamResponse(w http.ResponseWriter, resp *http.Response, arc *anthropicRequestContext) bool {
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		log.Printf("Failed to read passthrough response: %v", err)
		return false
	}

	// Extract content for storage
	var anthropicResp translator.AnthropicResponse
	if err := json.Unmarshal(bodyBytes, &anthropicResp); err == nil {
		for _, block := range anthropicResp.Content {
			switch block.Type {
			case "text":
				arc.accumulatedResponse.WriteString(block.Text)
			case "thinking":
				arc.accumulatedThinking.WriteString(block.Thinking)
			case "tool_use":
				inputStr := string(block.Input)
				arc.accumulatedToolCalls = append(arc.accumulatedToolCalls, store.ToolCall{
					ID:   block.ID,
					Type: block.Type,
					Function: store.Function{
						Name:      block.Name,
						Arguments: inputStr,
					},
				})
			}
		}
	}

	// Forward response as-is
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(bodyBytes)

	h.finalizeAnthropicSuccess(arc)
	return true
}

// handlePassthroughStreamResponse pipes Anthropic SSE events directly to the client.
// No translation needed — just forward each line as-is for real-time streaming.
// Also extracts content from SSE events for request logging.
func (h *Handler) handlePassthroughStreamResponse(w http.ResponseWriter, resp *http.Response, arc *anthropicRequestContext) bool {
	debugLog("=== PASSTHROUGH STREAM START ===")
	debugLog("Request ID: %s", arc.reqID)
	debugLog("Model: %s", arc.originalModel)

	// Send headers immediately for TTFB
	if !arc.headersSent {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		arc.headersSent = true

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		debugLog("Headers sent, connection established")
	}

	// Pipe upstream SSE to client, parsing for content extraction
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		// Check if client disconnected
		if arc.baseCtx.Err() != nil {
			log.Printf("Client disconnected during passthrough stream")
			break
		}

		line := scanner.Bytes()

		// Extract text from content_block_delta events for logging
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			var event map[string]interface{}
			if json.Unmarshal(data, &event) == nil {
				switch eventType := event["type"]; eventType {
				case "content_block_delta":
					if delta, ok := event["delta"].(map[string]interface{}); ok {
						if delta["type"] == "text_delta" {
							if text, ok := delta["text"].(string); ok {
								arc.accumulatedResponse.WriteString(text)
							}
						} else if delta["type"] == "thinking_delta" {
							if thinking, ok := delta["thinking"].(string); ok {
								arc.accumulatedThinking.WriteString(thinking)
							}
						}
					}
				case "message_delta":
					// stop_reason available here if needed
				}
			}
		}

		// Forward the line as-is
		w.Write(line)
		w.Write([]byte("\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Passthrough stream scanner error: %v", err)
	}

	h.finalizeAnthropicSuccess(arc)
	return true
}

// copyAnthropicHeaders copies headers for upstream request
func copyAnthropicHeaders(dst *http.Request, src http.Header) {
	for name, values := range src {
		// Skip certain headers
		switch strings.ToLower(name) {
		case "content-length", "host":
			continue
		case "x-api-key":
			// Translate x-api-key to Authorization Bearer for OpenAI upstream
			if len(values) > 0 {
				dst.Header.Set("Authorization", "Bearer "+values[0])
			}
			continue
		}
		for _, value := range values {
			dst.Header.Add(name, value)
		}
	}
	dst.Header.Set("Content-Type", "application/json")
}

// convertAnthropicMessagesToStore converts Anthropic messages to store format
func convertAnthropicMessagesToStore(messages []translator.AnthropicMessage) []store.Message {
	var result []store.Message
	for _, msg := range messages {
		content := ""
		switch c := msg.Content.(type) {
		case string:
			content = c
		case []interface{}:
			// Extract text from content blocks
			var sb strings.Builder
			for _, block := range c {
				if bm, ok := block.(map[string]interface{}); ok {
					if t, ok := bm["text"].(string); ok {
						sb.WriteString(t)
					}
				}
			}
			content = sb.String()
		}
		result = append(result, store.Message{
			Role:    msg.Role,
			Content: content,
		})
	}
	return result
}

// publishAnthropicCredentialFailover (Phase 3 / Task 20 / R3-8) emits
// the model_credential_failover event from the anthropic-passthrough
// branch with the canonical payload shape. cooldown_ms mirrors the
// engine's effective seed (retryAfter when present, else the 60s
// default); attempt_index is 1 (single retry per hook fire).
func (h *Handler) publishAnthropicCredentialFailover(modelID, fromID, toID string, retryAfter time.Duration) {
	cooldown := credentiallb.DefaultCooldown
	if retryAfter > 0 {
		cooldown = retryAfter
	}
	h.publishEvent(credentiallb.EventCredentialFailover, map[string]interface{}{
		"model_id":           modelID,
		"from_credential_id": fromID,
		"to_credential_id":   toID,
		"reason":             "rate_limit",
		"retry_after_ms":     retryAfter.Milliseconds(),
		"cooldown_ms":        cooldown.Milliseconds(),
		"attempt_index":      1,
	})
}

// arcMessagesAsInterfaces (Phase 3 / Task 16) converts Anthropic-shape
// messages to the OpenAI-shape []interface{} that
// credentiallb.ExtractFirstUserMessage walks. The conversion is
// minimal: each Anthropic message becomes a {"role": "...", "content": ...}
// map. String content is preserved; []ContentBlock / multimodal
// content is wrapped as []interface{} for the canonical-JSON path.
func arcMessagesAsInterfaces(messages []translator.AnthropicMessage) []interface{} {
	out := make([]interface{}, 0, len(messages))
	for _, msg := range messages {
		m := map[string]interface{}{
			"role": msg.Role,
		}
		switch c := msg.Content.(type) {
		case string:
			m["content"] = c
		case []interface{}:
			// Already []interface{} — pass through.
			m["content"] = c
		case []translator.ContentBlock:
			blocks := make([]interface{}, 0, len(c))
			for _, b := range c {
				blocks = append(blocks, map[string]interface{}{
					"type": b.Type,
					"text": b.Text,
				})
			}
			m["content"] = blocks
		default:
			m["content"] = ""
		}
		out = append(out, m)
	}
	return out
}

// captureRetryAfterHeader (Phase 3 / Task 22 — Round 3c — W1): the
// anthropic-passthrough branch produces no *ProviderError; doAnthropicRequest
// discards resp.Header. captureRetryAfterHeader is called at the
// existing arc.lastStatusCode set-site (handler_anthropic.go:423-424)
// to recover the Retry-After duration into arc.retryAfter so the
// downstream rate-limit classifier (lastStatusCode==429) can pair
// the retry budget + cooldown seeding without re-parsing the response.
func captureRetryAfterHeader(arc *anthropicRequestContext, resp *http.Response) {
	h := resp.Header.Get("Retry-After")
	if h == "" {
		arc.retryAfter = 0
		return
	}
	if secs, err := strconv.Atoi(h); err == nil {
		arc.retryAfter = time.Duration(secs) * time.Second
		return
	}
	if t, err := time.Parse(time.RFC1123, h); err == nil {
		arc.retryAfter = time.Until(t)
		if arc.retryAfter < 0 {
			arc.retryAfter = 0
		}
		return
	}
	arc.retryAfter = 0
}

// isAnthropicRateLimit (Phase 3 / Task 22 — Round 3c — W1) classifies
// the anthropic-passthrough branch's stored error as a rate-limit
// condition: arc.lastStatusCode == 429 OR the body `type` field
// (extracted from arc.lastError when set) contains the substring
// "rate_limit". HTTP-200-with-embedded-rate-limit is OUT OF SCOPE
// per R3-1 vocabulary pinning.
func isAnthropicRateLimit(arc *anthropicRequestContext) bool {
	if arc == nil {
		return false
	}
	if arc.lastStatusCode == 429 {
		return true
	}
	if len(arc.lastError) == 0 {
		return false
	}
	var body struct {
		Type string `json:"type"`
		Code string `json:"code"`
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(arc.lastError, &body); err != nil {
		return false
	}
	if strings.Contains(strings.ToLower(body.Type), "rate_limit") {
		return true
	}
	if strings.Contains(strings.ToLower(body.Error.Type), "rate_limit") {
		return true
	}
	switch body.Code {
	case "rate_limit", "rate_limit_error", "rate_limit_exceeded":
		return true
	}
	switch body.Error.Code {
	case "rate_limit", "rate_limit_error", "rate_limit_exceeded":
		return true
	}
	return false
}


