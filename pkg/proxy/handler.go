package proxy

// ─────────────────────────────────────────────────────────────────────────────
// handler.go — the OpenAI-wire request lifecycle (owner: /v1/chat/completions)
// ─────────────────────────────────────────────────────────────────────────────
//
// What this file owns:
//
//   - Request lifecycle ................ initRequestContext (config snapshot,
//                                         model resolution, conversation key,
//                                         group rewrite, sticky order) and the
//                                         HandleChatCompletions orchestration
//                                         that runs the race handoff and the
//                                         finalize paths.
//   - Race handoff .................... the bounded re-entry loop that builds
//                                         a coordinator per attempt set, waits
//                                         for a winner, and streams / renders
//                                         the result.
//   - Result finalization ............. streamResult + handleNonStreamResult
//                                         (relay, usage extraction, metering,
//                                         request_completed, sticky record).
//
// Companion files:
//
//   - handler_anthropic.go: the /v1/messages lane (sequential fallback walk,
//     no race coordinator). Anthropic-only behavior belongs THERE, not here.
//   - handler_functions.go: request-context construction + shared resolve
//     helpers. handler_groups.go: protocol-neutral group pipeline + the
//     sticky orchestrator. handler_helpers.go: the per-request context type.
//   - race_coordinator.go / race_executor.go: the RACE and the per-ATTEMPT
//     upstream call. If you are adding per-upstream-call behavior, you are in
//     the wrong file.
//
// Size rationale: this file sits in the 2000-line band because one request's
// lifecycle — the race handoff, the streaming relay
// with its heartbeat/idle/deadline arms, and the non-stream renderer — is ONE
// interleaved flow: the same requestContext fields and the same writeMu
// discipline thread through every arm. Splitting by concern would scatter
// those state-machine edges across files and make the winner-lock and
// write-ordering reasoning much weaker than the line count saves.
// TRIPWIRE: if this file passes ~2600 lines, split streamResult (relay arms)
// out FIRST — it is the largest self-contained block.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/authz"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/grouplb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/token"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxyheader"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/skipcache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/stickyorder"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
	usage "github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// decisionMisrouteCode is the machine-readable code for a decision-lane
// misroute. The pkg/decision package carries its own copy of the
// same value (packages do not import each other — the import guard
// forbids it); the two must stay in sync by hand.
const decisionMisrouteCode = "decision_misroute"

// imageGenMisrouteCode is the machine-readable code for a CHAT-family
// misroute to an image-gen model or an image-gen-lane GROUP (the
// imggen-groups commission / D-15).
//
// It is a DIFFERENT string from the image-gen lane's own
// "image_misroute" (pkg/imggen — the packages do not import each
// other, the import guard forbids it): the CHAT family says
// image_gen_misroute, the image-gen lane says image_misroute. Two
// distinct machine codes, never one shared string.
//
// The pkg/imggen package carries its own copy of the lane's code (the
// same by-hand sync as decisionMisrouteCode above).
const imageGenMisrouteCode = "image_gen_misroute"

// The compile-time proof that the PRODUCTION models binding satisfies
// the group-lane seam (the decorator-forward trap): a removed forward
// must break the BUILD, not silently no-op in production.
var _ groupLaneReader = (*modelscache.CachedModelsConfig)(nil)

// Config holds runtime configuration for the proxy handler
type Config struct {
	ConfigMgr    config.ManagerInterface      // Config manager for dynamic updates
	ModelsConfig models.ModelsConfigInterface // Models config for fallback chains
	EventBus     *events.Bus                  // Event bus for publishing events

	// UpstreamSkipCache (wi-7769a812 part 2 / USER RULING D3) is the
	// global upstream skip registry — the (credential|provider, model)
	// limit-failure marks selection consults. MAY BE NIL: unwired
	// callers (legacy tests) degrade to pre-feature behavior
	// byte-identically (every skipcache method is nil-receiver-safe).
	// Carried on Config (NOT a constructor arg) per the EventBus
	// precedent: a long-lived shared registry, same instance across
	// requests, no per-request capture semantics (unlike credEngine's
	// constructor-only law — the cache holds no per-request state).
	UpstreamSkipCache *skipcache.Cache

	// ModelSkipCache (wi-4d2c8e17 / ruling 2) is the MODEL-layer
	// registry — "is this MODEL limited/unavailable?" — written by
	// model-attributable walls and read by attempt suppression only.
	// MAY BE NIL (legacy/test embeddings degrade byte-identically).
	// Global layer separation: it is never consulted by the group
	// pick (which does not import modelcache).
	ModelSkipCache *modelcache.Cache
}

// Clone returns a snapshot of the current config values
func (c *Config) Clone() ConfigSnapshot {
	cfg := c.ConfigMgr.Get()
	// wi-7769a812 part 2 / D3: normalize the skip-cache TTL at the
	// snapshot seam — readers (coordinator marking, member advance,
	// re-entry exclusion) never see 0/negative. Unset/invalid ⇒ the
	// documented 60s default; the config Validate clamp [5s,1h]
	// bounds hand-set values before they get here.
	ttl := cfg.UpstreamSkipCacheTTL.Duration()
	if ttl <= 0 {
		ttl = skipcache.DefaultTTL
	}
	// wi-4d2c8e17 / ruling 2: normalize the model-layer fixed TTL at
	// the same seam (readers never see 0/negative).
	modelTTL := cfg.ModelSkipCacheTTL.Duration()
	if modelTTL <= 0 {
		modelTTL = modelcache.DefaultTTL
	}
	return ConfigSnapshot{
		IdleTimeout:              cfg.IdleTimeout.Duration(),
		StreamDeadline:           cfg.StreamDeadline.Duration(),
		MaxGenerationTime:        cfg.MaxGenerationTime.Duration(),
		MaxStreamBufferSize:      cfg.MaxStreamBufferSize,
		ModelsConfig:             c.ModelsConfig,
		LoopDetection:            cfg.LoopDetection,
		ToolRepair:               cfg.ToolRepair,
		RaceRetryEnabled:         cfg.RaceRetryEnabled,
		StickyLastSuccessEnabled: cfg.StickyLastSuccessEnabled,
		RaceParallelOnIdle:       cfg.RaceParallelOnIdle,
		RaceMaxParallel:          cfg.RaceMaxParallel,
		RaceMaxBufferBytes:       cfg.RaceMaxBufferBytes,
		LogRawUpstreamResponse:   cfg.LogRawUpstreamResponse,
		LogRawUpstreamOnError:    cfg.LogRawUpstreamOnError,
		LogRawUpstreamMaxKB:      cfg.LogRawUpstreamMaxKB,
		IdleTerminationEnabled:   cfg.IdleTerminationEnabled,
		IdleTerminationTimeout:   cfg.IdleTerminationTimeout.Duration(),
		UpstreamSkipCacheTTL:     ttl,
		UpstreamSkipCache:        c.UpstreamSkipCache,
		ModelSkipCacheTTL:        modelTTL,
		ModelSkipCache:           c.ModelSkipCache,
		EventBus:                 c.EventBus,
	}
}

type ConfigSnapshot struct {
	IdleTimeout         time.Duration
	StreamDeadline      time.Duration // Time limit before picking best buffer and continuing streaming
	MaxGenerationTime   time.Duration // Absolute hard timeout for entire request lifecycle
	MaxStreamBufferSize int
	ModelsConfig        models.ModelsConfigInterface
	LoopDetection       config.LoopDetectionConfig
	ToolRepair          toolrepair.Config

	// Race Retry
	RaceRetryEnabled   bool
	RaceParallelOnIdle bool
	RaceMaxParallel    int
	RaceMaxBufferBytes int
	ModelID            string // Primary model for this request

	// Sticky last-success race ORDER (snapshot of
	// config.StickyLastSuccessEnabled, per request). When true the
	// race chain / Anthropic walk promotes this conversation's last
	// delivered-success model to index 0 before construction. Off =
	// byte-identical request path.
	StickyLastSuccessEnabled bool

	// Raw Upstream Response Logging
	LogRawUpstreamResponse bool
	LogRawUpstreamOnError  bool
	LogRawUpstreamMaxKB    int

	// Idle Termination
	IdleTerminationEnabled bool
	IdleTerminationTimeout time.Duration

	// wi-7769a812 part 2 / USER RULING D3 — the global upstream skip
	// cache (limit-class failure marks) + its CONFIG-OWNED TTL,
	// hot-normalized at Clone (readers never see <=0). Nil cache =
	// feature off (nil-receiver-safe degradation everywhere).
	UpstreamSkipCacheTTL time.Duration
	UpstreamSkipCache    *skipcache.Cache

	// wi-4d2c8e17 / ruling 2 — the MODEL-layer cache ("is this MODEL
	// limited/unavailable?") + its CONFIG-OWNED fixed TTL
	// (hot-normalized at Clone). Read by attempt suppression only;
	// never by the group pick. Nil = feature off.
	ModelSkipCacheTTL time.Duration
	ModelSkipCache    *modelcache.Cache

	// Event Bus for publishing events during request handling
	EventBus *events.Bus
}

type Handler struct {
	config      *Config
	bus         *events.Bus
	store       *store.RequestStore
	client      *http.Client
	bufferStore *bufferstore.BufferStore
	tokenStore  auth.TokenStoreInterface
	counter     *usage.Counter

	// credEngine is the load-balancing engine injected at construction
	// (Phase 3 / Tasks 3 + W-3). The Config struct has NO CredEngine
	// field — constructor-only injection is the law (single source of
	// injection; no second Config.CredEngine path).
	//
	// MAY BE NIL: legacy single-credential paths and unit tests that
	// don't wire the engine pass nil. The race coordinator's
	// ResolveInternalConfigWithAffinity seam handles nil-engine by
	// returning the legacy single-credential resolution (Phase 2).
	credEngine *credentiallb.Engine

	// stickyEngine is the sticky last-success order registry, injected
	// at construction exactly like credEngine (constructor-only
	// injection; no Config field). MAY BE NIL: legacy/test paths pass
	// nil and every engine method is a nil-receiver no-op, so the
	// request path degrades to pre-feature behavior exactly.
	stickyEngine *stickyorder.Engine
}

// NewHandler (Phase 3 / Task 3 + W-3): 7th constructor parameter is the
// load-balancing engine. NO Config.CredEngine field — the 7th positional
// arg is the SOLE injection path.
//
// If credEngine is nil, the race coordinator's nil-engine branch serves
// the legacy single-credential fast path (Phase 2 / E-3); behavior is
// byte-identical to pre-Phase-3.
//
// The 8th constructor parameter (sticky last-success race ordering) is
// the sticky-engine injection — same constructor-only law. It may be
// nil: every stickyorder.Engine method is nil-receiver-safe, so a nil
// engine degrades the request path to pre-feature behavior exactly
// (tests and legacy embeddings pass nil; cmd/main.go wires the
// production engine).
//
//	credEngine may be nil.
//	stickyEngine may be nil.
func NewHandler(
	config *Config,
	bus *events.Bus,
	store *store.RequestStore,
	bufferStore *bufferstore.BufferStore,
	tokenStore auth.TokenStoreInterface,
	counter *usage.Counter,
	credEngine *credentiallb.Engine,
	stickyEngine *stickyorder.Engine,
) *Handler {
	h := &Handler{
		config:       config,
		bus:          bus,
		store:        store,
		credEngine:   credEngine,
		stickyEngine: stickyEngine,
		client: &http.Client{
			// IMPORTANT: Timeout is set to 0 for streaming support.
			// We use context deadlines (attemptCtx) instead of http.Client.Timeout because:
			// 1. http.Client.Timeout applies to entire request including response body reading
			// 2. For streaming, we need to allow reading the response body indefinitely
			// 3. Context deadline in doSingleAttempt handles cancellation properly
			Transport: &http.Transport{
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   100,
				IdleConnTimeout:       300 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second, // Timeout waiting for response headers (prevents stuck requests)
			},
		},
		bufferStore: bufferStore,
		tokenStore:  tokenStore,
		counter:     counter,
	}

	return h
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

// publishDecisionMisrouteEvent emits the telemetry event for a
// decision-lane misroute refusal (the chat lanes). The event type is
// lane-scoped: the text lane's own event vocabulary is untouched.
func (h *Handler) publishDecisionMisrouteEvent(modelID string) {
	h.publishEvent(decisionMisrouteCode, map[string]interface{}{
		"outcome": decisionMisrouteCode,
		"model":   modelID,
	})
}

// publishGroupHop emits a GROUP-layer member-hop observability event
// (wi-4d2c8e17 — LABELS). Payload keeps the group vocabulary
// (group_id / member_id / from_member / to_member / reason); to_member
// is ABSENT on an exhaustion event. Paired with the coordinator's
// model_* chain events so chain targets are never mislabeled as
// members.
func (h *Handler) publishGroupHop(eventType, groupID, fromMember, toMember, reason string) {
	data := map[string]interface{}{
		"group_id":    groupID,
		"member_id":   fromMember,
		"from_member": fromMember,
		"reason":      reason,
	}
	if toMember != "" {
		data["to_member"] = toMember
	}
	h.publishEvent(eventType, data)
}

// saveRawResponse saves the raw upstream response to disk and emits an event.
// This is a best-effort operation - errors are logged but don't fail the request.
// Should be called in a goroutine to avoid blocking the response.
func (h *Handler) saveRawResponse(requestID string, rawBytes []byte, rawRequestBody []byte, maxKB int) {
	if h.bufferStore == nil {
		return
	}
	if len(rawBytes) == 0 {
		return
	}

	maxBytes := int64(maxKB) * 1024

	// Skip if too large
	if int64(len(rawBytes)) > maxBytes {
		log.Printf("[RAW-LOG] Response too large: %d > %d limit (request=%s)",
			len(rawBytes), maxBytes, requestID)
		return
	}

	// Save response
	bufferID := fmt.Sprintf("%s-response", requestID)
	if err := h.bufferStore.Save(bufferID, rawBytes); err != nil {
		log.Printf("[RAW-LOG] Failed to save response: %v (request=%s)", err, requestID)
		return
	}

	// Save request body (for correlation) - optional but useful for debugging
	var requestBodyID string
	if len(rawRequestBody) > 0 && int64(len(rawRequestBody)) <= maxBytes {
		requestBodyID = fmt.Sprintf("%s-request", requestID)
		if err := h.bufferStore.Save(requestBodyID, rawRequestBody); err != nil {
			log.Printf("[RAW-LOG] Failed to save request body: %v (request=%s)", err, requestID)
			requestBodyID = "" // Clear on error
		}
	}

	// Emit event
	h.publishEvent("response_logged", map[string]interface{}{
		"id":              requestID,
		"buffer_id":       bufferID,
		"request_body_id": requestBodyID,
		"size_bytes":      len(rawBytes),
	})
}

// HandleModels returns the list of available models in OpenAI-compatible format.
// GET /v1/models
//
// ImgGen Models commission / BE-D6 / T1.3.1: image-gen models are
// excluded from the OpenAI-compatible list. The check uses
// ModelConfig.IsImageGen() (the same helper the /fe/api/models
// ?kind= filter and the T1.3.3 chat-misroute early-reject use);
// the list is built from GetEnabledModels() so a disabled image-gen
// model is already excluded regardless of kind.
func (h *Handler) HandleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := h.config.Clone()
	enabledModels := cfg.ModelsConfig.GetEnabledModels()

	// load-balance-groups / §3.4 (P2.5): second-pass shadow-hide.
	// Build the set of IDs claimed by ENABLED groups from the same
	// enabled listing (an interface-constrained scan — no per-row
	// model-kind branching in the listing loop below, no manager
	// type assertion). Groups themselves STAY listed (they are
	// chat-servable — clients must discover them); their claimed
	// models are hidden while the claim is active. A disabled group
	// is absent from GetEnabledModels ⇒ contributes no claim ⇒ the
	// claimed model reappears automatically.
	claimedIDs := make(map[string]bool, len(enabledModels))
	for _, m := range enabledModels {
		if m.IsGroup() && m.Enabled && m.ShadowModelID != "" {
			claimedIDs[m.ShadowModelID] = true
		}
	}

	// Build OpenAI-compatible response. Exclude image-gen and
	// decision models: they are served by /v1/image_generation
	// and /v1/decisions respectively and must never appear in the
	// OpenAI-compatible chat pickers (Decision Models / §3.1 site 6).
	//
	// Decision-groups commission: a group whose inferred lane is
	// DECISION is excluded too (it is served by /v1/decisions).
	// imggen-groups commission: the exclusion widens to EVERY
	// NON-CHAT lane — an image-gen-lane group is served by
	// /v1/image_generation and must not appear in the chat pickers
	// either. The lane derivation folds over ALL models (not the
	// enabled-only list): a disabled member still counts toward the
	// group's unanimity, so a group with a disabled decision or
	// image-gen member is still a non-chat group.
	allModels := cfg.ModelsConfig.GetModels()
	nonChatGroups := map[string]bool{}
	for _, g := range allModels {
		if !g.IsGroup() {
			continue
		}
		if lane, ok := groupLaneOf(cfg.ModelsConfig, g.ID); ok && lane != models.GroupLaneChat {
			nonChatGroups[g.ID] = true
		}
	}
	models := make([]map[string]interface{}, 0, len(enabledModels))
	for _, m := range enabledModels {
		if m.IsImageGen() || m.IsDecision() {
			continue
		}
		if nonChatGroups[m.ID] {
			continue // a non-chat-lane group — served by its own lane
		}
		if claimedIDs[m.ID] {
			continue // shadowed by an enabled group — hidden
		}
		models = append(models, map[string]interface{}{
			"id":       m.ID,
			"object":   "model",
			"created":  1700000000, // Static timestamp
			"owned_by": "llm-supervisor-proxy",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}

// extractAPIKey extracts the API key from Authorization Bearer or X-API-Key header
func (h *Handler) extractAPIKey(r *http.Request) string {
	// Check Authorization: Bearer <token> header
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}

	// Check X-API-Key header (capitalized)
	apiKey := r.Header.Get("X-API-Key")
	if apiKey != "" {
		return apiKey
	}

	// Check x-api-key header (lowercase, used by Anthropic SDK)
	apiKey = r.Header.Get("x-api-key")
	if apiKey != "" {
		return apiKey
	}

	return ""
}

// authenticate validates the API key and returns the token + true if valid
func (h *Handler) authenticate(r *http.Request) (*auth.AuthToken, bool) {
	// If tokenStore is nil, skip validation (auth disabled)
	if h.tokenStore == nil {
		return nil, true
	}

	apiKey := h.extractAPIKey(r)
	if apiKey == "" {
		return nil, false
	}

	// Create a timeout context for database query
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	token, err := h.tokenStore.ValidateToken(ctx, apiKey)
	if err != nil {
		return nil, false
	}
	return token, true
}

// requiresInternalAuth checks whether the request requires client API key
// validation. remove-external-upstream P1 (A10): every resolvable model is
// internal by construction (the boundary 404 gate rejects unknown and
// non-internal models before this point), so the old modelList walk for
// internal-ness is provably always-true and collapses to the tokenStore
// check. Auth semantics preserved: when a tokenStore is present every
// request authenticates exactly as known-internal models did before.
func (h *Handler) requiresInternalAuth(rc *requestContext) bool {
	// If tokenStore is nil, auth is disabled
	return h.tokenStore != nil
}

// sendAuthError sends a 401 Unauthorized JSON error response
func (h *Handler) sendAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeAuthenticationError,
		"",
		"Invalid or expired API key",
	))
}

// sendModelNotAllowedError sends a 403 Forbidden JSON error response for model access violations
func (h *Handler) sendModelNotAllowedError(w http.ResponseWriter, modelName string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"type":    "access_denied",
			"message": "model not allowed for this token",
			"model":   modelName,
		},
	})
}

// denyModelNotAllowed is THE 403 writer of the chat lane: it records
// the failed request, publishes the "model_not_allowed" event and
// writes the access_denied envelope.
//
// The envelope quotes the CLIENT-REQUESTED string (rc.reqLog.Model) —
// NEVER the canonical id of the resolved row (PD-9): for a claimed
// (shadow) id the canonical group id must not leak, or the response
// would disclose the claim topology.
//
// The event vocabulary is unchanged ("model_not_allowed", the same
// payload keys) — no new outcome string is introduced.
func (h *Handler) denyModelNotAllowed(w http.ResponseWriter, rc *requestContext) {
	resolvedID := ""
	if rc.resolvedModel != nil {
		resolvedID = rc.resolvedModel.ID
	}
	log.Printf("[AUTH] Model '%s' (resolved: %s) not allowed for token %s (%s)", rc.reqLog.Model, resolvedID, rc.tokenID, rc.tokenName)

	rc.reqLog.Status = "failed"
	rc.reqLog.Error = fmt.Sprintf("model '%s' not allowed for this token", rc.reqLog.Model)
	rc.reqLog.EndTime = time.Now()
	rc.reqLog.Duration = time.Since(rc.startTime).String()
	h.store.Add(rc.reqLog)

	h.publishEvent("model_not_allowed", map[string]interface{}{
		"id":    rc.reqID,
		"model": rc.reqLog.Model,
		"token": rc.tokenID,
	})

	h.sendModelNotAllowedError(w, rc.reqLog.Model)
}

// sendError sends a JSON error response in OpenAI-compatible format
func (h *Handler) sendError(w http.ResponseWriter, code int, message, errType, errorCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(models.NewOpenAIError(errType, errorCode, message))
}

// sendSSEError sends an error as an SSE event to the client.
// This is used when a streaming error occurs after headers have been sent,
// so we can't send a regular HTTP error response.
// OpenAI format: data: {"error":{"type":"...","message":"..."}}
func (h *Handler) sendSSEError(w http.ResponseWriter, errType, message string) {
	errResp := models.NewOpenAIError(errType, "", message)
	data, _ := json.Marshal(errResp)
	// OpenAI streaming error format: just data, no custom event type
	fmt.Fprintf(w, "data: %s\n\n", string(data))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// HandleChatCompletions is the main entry point for proxying chat completions.
func (h *Handler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	rc, err := h.initRequestContext(r)
	if err != nil {
		if errors.Is(err, modelscache.ErrConfigUnavailable) {
			// db-cache-layer 1D — fail-fast 503: the model could not be
			// resolved AND the config store is unhealthy. Never a silent
			// external passthrough on a DB error (2026-08-27 incident
			// class). The existing sendError helper already supports any
			// code/message (1.D.3).
			h.sendError(w, http.StatusServiceUnavailable, err.Error(), "config_store_unavailable", "")
			// rc is nil on this path, so the event fields ride on the
			// sentinel (configUnavailableError) — parity with the
			// Anthropic boundary site (handler_anthropic.go), which
			// publishes unconditionally.
			evtID, evtModel := "", ""
			var cue *configUnavailableError
			if errors.As(err, &cue) {
				evtID, evtModel = cue.reqID, cue.model
			}
			h.publishEvent("config_store_unavailable", map[string]interface{}{
				"id":    evtID,
				"model": evtModel,
			})
			return
		}
		if errors.Is(err, ErrModelNotFound) {
			// remove-external-upstream P1 — unknown/non-internal model.
			// Real-API 404 semantics (R7): invalid_request_error + code
			// model_not_found — NOT the decision/imggen server-error
			// shape. rc is nil on this path, so the event fields ride on
			// the sentinel (modelNotFoundError) — parity with the
			// config_store_unavailable site above. The request log was
			// already finalized at the gate (initRequestContext).
			evtID, evtModel := "", ""
			var mnfe *modelNotFoundError
			if errors.As(err, &mnfe) {
				evtID, evtModel = mnfe.reqID, mnfe.model
			}
			h.publishEvent("model_not_found", map[string]interface{}{
				"id":              evtID,
				"model":           evtModel,
				"outcome":         "model_not_found",
				"upstream_status": 0,
			})
			h.sendError(w, http.StatusNotFound, fmt.Sprintf("model '%s' not found", evtModel), "invalid_request_error", "model_not_found")
			return
		}
		if errors.Is(err, ErrReadBodyFailed) {
			http.Error(w, "Failed to read body", http.StatusInternalServerError)
		} else if errors.Is(err, ErrInvalidJSON) {
			http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		}
		return
	}

	// Reset accumulated buffers after request completes to prevent memory leaks
	// from strings.Builder internal buffers retaining capacity
	defer func() {
		if rc != nil {
			rc.reset()
		}
	}()

	// ImgGen Models commission / BE-D6 + Leader Ruling L1 + Architect
	// Amendment 4 — T1.3.3 IsImageGen() early-reject (CRITICAL —
	// unskippable). A chat request naming an image-gen model is
	// rejected here, AFTER model resolve succeeded (so we know it's
	// a real, configured model — not a typo) and BEFORE any
	// upstream / auth-race / metering work. Without this guard, the
	// chat path would resolve, pass auth, run the race coordinator,
	// and make a REAL upstream chat call to MiniMax with
	// model=image-01 that fails upstream — burning real money AND
	// polluting model_hourly_usage.request_count on the image-gen
	// model. The reject returns an OpenAI-envelope 400 with the
	// standard models.NewOpenAIError shape (matches the
	// self-generated error posture for /v1/image_generation at
	// pkg/imggen, BE-T3 Amendment 5). The guard is intentionally
	// the ONLY pkg/proxy change from this commission.
	if rc.resolvedModel != nil && rc.resolvedModel.IsImageGen() {
		log.Printf("[imggen] rejecting chat misroute to image-gen model=%s (model is served by /v1/image_generation only)", rc.resolvedModel.ID)
		h.sendError(w, http.StatusBadRequest,
			"image-gen models are served by /v1/image_generation only",
			models.ErrorTypeServerError, imageGenMisrouteCode)
		return
	}

	// Decision Models commission / §3.1 site 5 — sibling guard
	// for decision-kind models. Intentionally a SECOND `if`
	// branch (not a boolean `||` in the arm above) so the
	// `decision_misroute` error code stays distinct from
	// `image_gen_misroute` and the resolved-model kind is clear
	// in the log line. Code string follows the cross-route
	// canonical naming ("decision_misroute" identifies the
	// misroute class everywhere; the Anthropic route emits the
	// same code string through its own envelope family — see
	// handler_anthropic.go post-resolve guard).
	if rc.resolvedModel != nil && rc.resolvedModel.IsDecision() {
		log.Printf("[decision] rejecting chat misroute to decision model=%s (model is served by /v1/decisions only)", rc.resolvedModel.ID)
		h.sendError(w, http.StatusBadRequest,
			"decision models are served by /v1/decisions only",
			models.ErrorTypeServerError, decisionMisrouteCode)
		return
	}

	// Decision-groups commission — the GROUP arm of the misroute
	// guard: a GROUP whose inferred lane is decision is served by
	// /v1/decisions only. The guard is ADDITIVE and lane-gated: a
	// chat group is unaffected (its lane is chat), and the guard runs
	// BEFORE the group rewrite, so a decision group can never reach
	// the chat executor (the misroute is structurally impossible).
	//
	// The runtime defence-in-depth net: a group inferred as CHAT that
	// carries a decision member must NEVER silently serve — a loud
	// 400 + a telemetry event.
	if rc.resolvedModel != nil && rc.resolvedModel.IsGroup() {
		groupID := rc.resolvedModel.ID
		lane, laneOK := groupLaneOf(rc.conf.ModelsConfig, groupID)
		switch {
		case laneOK && lane == models.GroupLaneDecision:
			log.Printf("[decision] rejecting chat misroute to decision group=%s (the group is served by /v1/decisions only)", groupID)
			h.sendError(w, http.StatusBadRequest,
				"decision models are served by /v1/decisions only",
				models.ErrorTypeServerError, decisionMisrouteCode)
			return
		case laneOK && lane == models.GroupLaneImageGen:
			// imggen-groups commission / D-15: an image-gen-lane
			// GROUP is served by /v1/image_generation only. The arm
			// is ADDITIVE and lane-gated (a chat group is
			// unaffected) and it runs BEFORE the group rewrite, so
			// the misroute is structurally impossible.
			log.Printf("[imggen] rejecting chat misroute to image-gen group=%s (the group is served by /v1/image_generation only)", groupID)
			h.sendError(w, http.StatusBadRequest,
				"image-gen models are served by /v1/image_generation only",
				models.ErrorTypeServerError, imageGenMisrouteCode)
			return
		case laneOK && lane == models.GroupLaneChat && groupHasDecisionMember(rc.conf.ModelsConfig.GetModels(), groupID):
			log.Printf("[decision] refusing chat group=%s — the group carries a decision-kind member (the mixed shape must never be served on the chat lane)", groupID)
			h.publishDecisionMisrouteEvent(groupID)
			h.sendError(w, http.StatusBadRequest,
				"decision models are served by /v1/decisions only",
				models.ErrorTypeServerError, decisionMisrouteCode)
			return
		}
	}

	// Check if this request requires internal authentication
	// (model is internal and needs client API key validation)
	requiresAuth := h.requiresInternalAuth(rc)

	// Try to authenticate the request
	// Authentication is required for internal models OR when a token is provided
	var authToken *auth.AuthToken
	if requiresAuth || h.tokenStore != nil {
		authToken, _ = h.authenticate(r)
		if authToken == nil && requiresAuth {
			// Authentication required but failed
			rc.reqLog.Status = "failed"
			rc.reqLog.Error = "Authentication failed: invalid or expired API key"
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			h.store.Add(rc.reqLog)

			h.publishEvent("auth_failed", map[string]interface{}{
				"id":    rc.reqID,
				"error": "invalid_api_key",
			})

			h.sendAuthError(w)
			return
		}
	}

	// Populate token info if authenticated
	if authToken != nil {
		rc.tokenID = authToken.ID
		rc.tokenName = authToken.Name
	}

	// THE AUTHORIZATION SCOPE (ADR D2/D7) — resolved ONCE per request,
	// AFTER auth and BEFORE any candidate construction (D13).
	//
	//	nil     => UNRESTRICTED (anonymous, or a wildcard token)
	//	denyAll => DENY-ALL (an unreadable grant row)
	//	else    => restricted: Allows(id) is a set lookup
	//
	// The membersOf adapter reads the IN-MEMORY models snapshot
	// (models.GroupMembershipReader — the modelscache decorator's
	// non-copying accessor), so this adds NO DB read on the request
	// path (Ruling 5: a live SELECT under m.mu.RLock stalls every
	// reader behind the writer-preferring RWMutex).
	rc.scope = authz.Resolve(authToken, h.membersOf(&rc.conf))

	// Phase 3 / Task 2a — POST-AUTH wiring site (A-1, A-1-wiring).
	// HOISTED OUT of the `if authToken != nil` branch (Round 3j W3):
	// anonymous / unauthenticated requests must STILL get a conversation
	// key — they receive the unsalted key because rc.tokenID is the
	// zero value "" (the requestContext defaults it to empty). The
	// key is NEVER computed in initRequestContext (tokenID is unset
	// there at handler.go:352).
	//
	// remove-external-upstream P1 (A11): the `resolvedModel.Internal`
	// conjunct is GONE — every model that reaches this point is
	// internal by construction (the boundary 404 gate rejects
	// unknown/non-internal models). The nil check stays (the gate
	// guarantees non-nil, but the compiler cannot see it).
	//
	// W-2 + C2: when no leading user run was extracted (assistant-first
	// conversation, system-only, tool entry before any user message,
	// …), the key stays "" — the engine then picks FRESH per request
	// and stores NO binding (the ""-as-own-bucket reading is REMOVED
	// per W-2; ComputeConversationKey never returns "" by itself, so
	// the caller owns this gate). The DEBUG log below is the canonical
	// C2 observability mechanism.
	if rc.resolvedModel != nil {
		if rc.leadingUserRun == "" {
			rc.conversationKey = ""
			log.Printf("[LB] empty conversationKey for modelID=%s; engine will pick fresh per request (no binding stored)",
				rc.resolvedModel.ID)
		} else {
			rc.conversationKey = credentiallb.ComputeConversationKey(
				rc.resolvedModel.ID,
				rc.tokenID, // "" for anonymous — unsalted key
				rc.leadingUserRun,
			)
			log.Printf("[LB] computed conversationKey hash=%s modelID=%s tokenID=%s len(leadingUserRun)=%d",
				rc.conversationKey[:8],
				rc.resolvedModel.ID,
				rc.tokenIDDisplay(),
				len(rc.leadingUserRun),
			)
		}
	}

	// === MODEL ACCESS CHECK (THE SCOPE PREDICATE) ===
	//
	// D13 (THE SCOPE DECISION PRECEDES CANDIDATE CONSTRUCTION): this
	// gate runs BEFORE the group rewrite below, so an out-of-scope
	// request creates NO group binding, NO sticky write, NO member
	// expansion and NO upstream attempt — it is denied at the entry.
	// (It used to run AFTER the rewrite: the status was 403 either way,
	// but the side effects were not.)
	//
	// PD-9 (T2.16): the gate authorizes the RESOLVED row, never the
	// client-requested string. A claim is an ID-space alias (only
	// groups claim — modelscache/models.go:283-284), so a request naming
	// the claimed model id resolves to the claiming GROUP row, and that
	// group is what would be contacted. The unit of authorization is
	// the model that will actually be contacted (ADR D1).
	//
	// AUDIT BRIDGE: for every NON-claimed request resolvedModel.ID ==
	// reqLog.Model (the raw client string — handler_functions.go), so
	// this strict check is behaviourally IDENTICAL to today's single
	// check. Only the claimed path differs — and there the difference
	// IS the ruling.
	//
	// The 403 quotes the CLIENT-REQUESTED string (rc.reqLog.Model) —
	// never the canonical group id: a body carrying the canonical id
	// would disclose the claim topology.
	if rc.scope != nil {
		if rc.resolvedModel != nil {
			if !rc.scope.Allows(rc.resolvedModel.ID) {
				h.denyModelNotAllowed(w, rc)
				return
			}
		} else if !rc.scope.IsUnrestricted() {
			// Unknown model + restricted token: fail closed. Doc-only
			// arm today (the 404 gate at handler_functions.go fires
			// earlier), kept expressed through the PREDICATE (never
			// through len(AllowedModels)) so the conjunctive rule
			// cannot regress here.
			h.denyModelNotAllowed(w, rc)
			return
		}
	}
	// === END MODEL ACCESS CHECK ===

	// load-balance-groups §3.6.1 (Am21) — THE modelList rewrite,
	// OpenAI site. Runs AFTER the conversation-key derivation (the
	// sticky pick keys on the group-salted conversation key, which
	// needs the post-auth tokenID — the plan's §3.6.1 :149-159 build
	// site prepares the list; the substitution happens here, before
	// every modelList consumer: misroute guards above ran on the
	// GROUP row, and the race handoff below receives the rewritten
	// list; per-attempt re-fetches then run member-native). The
	// Anthropic mirror calls the SAME shared helper (parity by
	// construction; the parity test pins both sites).
	//
	// The rewrite is SCOPED (ADR D6): the member pick consults the
	// token's reach, so an out-of-reach member is never picked and
	// never cooled.
	if rc.resolvedModel != nil {
		outcome := h.applyGroupRewrite(&rc.conf, rc.modelList, rc.conversationKey, rc.scope)
		if outcome.failGroupID != "" {
			if outcome.scopeExcluded {
				// PD-14: a group that is empty BECAUSE OF SCOPE is a
				// PERMISSION failure — 403 access_denied, NEVER the 502
				// group_unservable envelope (the two failures have
				// different owners and different operator actions).
				//
				// ZERO upstream attempts occurred here (the rewrite is
				// the FIRST candidate construction) — so the
				// attempt-count-keyed rule agrees: a permission
				// failure, not an availability failure.
				h.denyModelNotAllowed(w, rc)
				return
			}
			// Am28 / §3.8: empty-at-runtime group ⇒ the normal
			// resolve-failure envelope — never a panic, never a
			// silent passthrough.
			log.Printf("[grouplb] request %s: %s", rc.reqID, groupFailMessage(outcome.failGroupID))
			rc.reqLog.Status = "failed"
			rc.reqLog.Error = groupFailMessage(outcome.failGroupID)
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			h.store.Add(rc.reqLog)
			h.publishEvent("request_failed", map[string]interface{}{
				"id":    rc.reqID,
				"error": rc.reqLog.Error,
			})
			h.sendError(w, http.StatusBadGateway, rc.reqLog.Error, models.ErrorTypeServerError, "group_unservable")
			return
		} else if outcome.pick != nil {
			pick := outcome.pick
			rc.modelListOriginal = rc.modelList
			rc.modelList = outcome.list
			rc.groupPick = pick
			// wi-7769a812 part 2 — exhaustive member iteration: the
			// sticky pick is this request's FIRST tried member.
			rc.groupTriedMembers = map[string]bool{pick.MemberID: true}
			// Am25 metering stamp: record the served member when it
			// differs from the client-requested string (day-1
			// self-shadow keeps RouteModel empty — exactly one usage
			// row, unchanged).
			if pick.MemberID != rc.reqLog.Model {
				rc.reqLog.RouteModel = pick.MemberID
			}
			log.Printf("[grouplb] request %s: group=%s → sticky member=%s (newlyBound=%v)",
				rc.reqID, pick.GroupID, pick.MemberID, pick.NewlyBound)
		} else if outcome.list != nil {
			// The rewrite ran but produced NO group pick (a dropped
			// fallback-position group — PD-12/arch PD-7). The list is
			// still swapped: the dropped entry must not survive into
			// the execution list. No pick ⇒ no group context ⇒ the
			// group re-entry path stays closed (it is gated on
			// rc.groupPick != nil).
			rc.modelListOriginal = rc.modelList
			rc.modelList = outcome.list
		}
	}

	// Header-driven flag for MiniMax reasoning_details split-mode translation.
	// Value-list semantics (R8/Q3): accepted values are case-sensitive
	// lowercase "true" or "1" only. Parsed via
	// proxyheader.ParseInterleavedThinkingHeader. Stored on rc for the
	// race paths.
	rc.interleavedThinking = proxyheader.ParseInterleavedThinkingHeader(r)

	h.publishEvent("request_started", map[string]interface{}{"id": rc.reqID})

	// For streaming requests, send headers early and start heartbeat
	// This ensures client receives heartbeats during race retry wait time
	var heartbeatCancel context.CancelFunc
	if rc.isStream {
		// SSE headers — Cache-Control: no-cache alone is insufficient
		// for Cloudflare (and similar CDNs). `no-transform` is required
		// to disable CF's response buffering and asset optimization, otherwise
		// even frequent SSE heartbeats sit in CF's buffer until CF's
		// 100/180s read timeout triggers (524 — proxy disconnects the
		// client even though the upstream response is still streaming).
		// See: CF docs §"no-transform" + §"Streaming responses".
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		rc.headersSent = true

		// Send initial connected message (no mutex needed for initial setup)
		fmt.Fprint(w, ": connected\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Create channel for heartbeat to signal client disconnection
		rc.clientGoneCh = make(chan struct{})

		// Use sync.Once to ensure clientGoneCh is only closed once
		// Even if heartbeat tries to send multiple times after disconnect
		var closeClientGoneCh sync.Once
		closeOnce := func() { closeClientGoneCh.Do(func() { close(rc.clientGoneCh) }) }

		// Start heartbeat - runs through race retry and streaming until request ends
		// Heartbeat checks connection every 5 seconds; on failure, closes clientGoneCh
		// Pass writeMu to synchronize heartbeat writes with streamResult writes
		heartbeatCancel = h.startSSEHeartbeat(w, &rc.writeMu, closeOnce)
	}

	// Ensure heartbeat is cancelled when this function returns
	defer func() {
		if heartbeatCancel != nil {
			heartbeatCancel()
		}
	}()

	// Unified Race Retry Design (Parallel Race)
	log.Printf("[RACE] Parallel race retry started for request %s", rc.reqID)
	// rc.interleavedThinking parsed at handler entry (handler.go:465+) is
	// threaded here so race-internal sites can read it via executeRequest
	// without taking a *requestContext dependency (B3 — executeInternalRequest
	// has neither rc nor *http.Request).
	//
	// Phase 3 / Round 3c C3 — the engine + conversationKey reach the
	// coordinator through this construction site:
	//   cmd/main.go → NewHandler(…, credLB) → Handler.credEngine → here
	// Constructor-only injection per W-3 holds end-to-end (no
	// Config.CredEngine second injection path).
	//
	// Phase 2 / real-streaming-default — compute liveFirstByteGate
	// HERE (NOT inside the constructor): isStream is a *requestContext
	// field, not a *ConfigSnapshot, and the constructor signature is
	// ConfigSnapshot-based. liveFirstByteGate is true only in real-
	// streaming mode (header absent AND isStream=true); the gate
	// redefines the winner-eligibility predicate from `IsCompleted()`
	// to "first forwardable data chunk". Buffered mode (header present)
	// and isStream=false requests keep the original IsCompleted gate
	// verbatim (M3 amendment, regression-pinned by
	// TestLiveRelay_NonStreamUnaffected).
	liveFirstByteGate := !rc.bufferMode && rc.isStream

	// load-balance-groups / Am24: the race-handoff block runs in a
	// bounded loop so a group request whose member pipeline fails
	// TERMINALLY (winner==nil, all attempts failed — NOT a context
	// cancellation) can re-enter EXACTLY ONCE with a reselected
	// member. Pre-first-byte is inherent on this path: the relay
	// writes data bytes only after a winner exists (the SSE
	// ": connected" comment is not a model byte), and once the
	// winner streams its first byte the flow never returns here
	// (N9 winner lock). Non-group requests take the loop exactly
	// once (groupPick == nil) — byte-identical behavior.
	stickyPromotionLogged := false // log-once per request across re-entry replays
	for {
		// Sticky last-success race ORDER (design site (a)): re-apply on
		// EVERY loop iteration, at the loop top — BEFORE the coordinator
		// aliases the list. The group re-entry rebuild (tryGroupReentry)
		// mutates rc.modelList and returns into this same loop, so the
		// rebuilt list is re-promoted by construction (idempotent: an
		// already-front winner no-ops). Off / empty key / nil engine /
		// no record ⇒ the list passes through untouched.
		//
		// LOG-ONCE: a group re-entry replay passes an empty reqID ONLY when
		// THIS request already logged a promotion, so the rebuilt list
		// cannot produce a second "promoted" line for the same request.
		// When iteration 1 did not move anything (the common re-entry
		// shape), the rebuilt list's promotion IS the request's first and
		// only line. The order stays observable in race_started either way.
		reorderLogID := rc.reqID
		if rc.groupAdvances > 0 && stickyPromotionLogged {
			reorderLogID = ""
		}
		// C1 — THE REACH FILTER (ADR D6/D7): every candidate outside the
		// token's reach is removed from the execution list BEFORE the
		// sticky ordering runs — sticky promotion must never promote an
		// entry the filter would remove.
		//
		// The filter runs on EVERY loop iteration (the group re-entry
		// rebuilds the list), so the durable invariant is C1 + C4.
		rc.modelList = h.applyReachFilter(rc.scope, rc.modelList)
		beforeStickyOrder := rc.modelList
		rc.modelList = h.applyStickyOrder(&rc.conf, rc.modelList, rc.conversationKey, reorderLogID)
		if reorderLogID != "" && !slices.Equal(beforeStickyOrder, rc.modelList) {
			stickyPromotionLogged = true
		}

		// PD-10 (T2.12): an empty execution list is a CONTRACT VIOLATION
		// at the coordinator — the caller must decide 403 (scope) or
		// 502 (unservability) BEFORE construction. The constructor
		// refuses an empty list (it used to silently re-expand it to
		// {cfg.ModelID}, re-introducing an out-of-scope model).
		if len(rc.modelList) == 0 {
			// Defensive conjunct (Phase-2 sec review W2): a 403 is only
			// ever correct when NO upstream attempt ran and the response
			// headers are not committed. Unreachable today (the empty
			// list ⇒ the coordinator was never constructed ⇒ attempts ==
			// 0 and no byte was written), but the terminal branches carry
			// the same conjunct; with >= 1 attempt this falls through to
			// the 502 all_models_failed path below.
			if rc.groupPick != nil && rc.upstreamAttempts == 0 && !rc.headersSent {
				// The group was in reach but NO member was → a scope
				// exclusion. (The pick/list consistency rule makes
				// rc.groupPick != nil imply a real GroupID, so this
				// test is sound — the two rules are load-bearing for
				// each other.)
				//
				// ZERO upstream attempts ran (the list is empty ⇒ the
				// coordinator was never constructed) ⇒ the
				// attempt-count-keyed rule agrees: a permission
				// failure, never an availability failure.
				h.denyModelNotAllowed(w, rc)
				return
			}
			// Defensive: an internally-empty build with no group pick —
			// the entry gate already caught the reachable case.
			log.Printf("[authz] request %s: EMPTY execution list with no group pick — refusing to construct the coordinator", rc.reqID)
			rc.reqLog.Status = "failed"
			rc.reqLog.Error = "no executable models for this request"
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			h.store.Add(rc.reqLog)
			h.publishEvent("request_failed", map[string]interface{}{
				"id":    rc.reqID,
				"error": rc.reqLog.Error,
			})
			h.sendError(w, http.StatusBadGateway, rc.reqLog.Error, models.ErrorTypeServerError, "all_models_failed")
			return
		}

		coordinator, coordErr := newRaceCoordinatorWithEvents(rc.baseCtx, &rc.conf, r, rc.rawBody, rc.modelList, h.bus, rc.reqID, rc.interleavedThinking, h.credEngine, rc.conversationKey, liveFirstByteGate, h.stickyEngine)
		if coordErr != nil {
			// The constructor REFUSES an empty list (PD-10) — the
			// caller guard above must have caught it first. Fail clean:
			// never a silent re-expansion, never an upstream attempt.
			log.Printf("[authz] request %s: race coordinator refused: %v", rc.reqID, coordErr)
			rc.reqLog.Status = "failed"
			rc.reqLog.Error = "no executable models for this request"
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			h.store.Add(rc.reqLog)
			h.publishEvent("request_failed", map[string]interface{}{
				"id":    rc.reqID,
				"error": rc.reqLog.Error,
			})
			h.sendError(w, http.StatusBadGateway, rc.reqLog.Error, models.ErrorTypeServerError, "all_models_failed")
			return
		}
		// Q15 (plan §3.7): the coordinator carries the group pick so
		// the W-1 publish site (race_executor) can fire
		// model_member_selected newlyBound-only.
		coordinator.groupPick = rc.groupPick
		// F6 (wi-7769a812 part 3): the credential-layer tried-set is
		// REQUEST-scoped — seed this fresh coordinator with everything
		// the previous re-entry iterations already tried, so a
		// credential that 402'd under an earlier coordinator cannot be
		// re-spawned here.
		coordinator.SeedRaceState(rc.credTriedAcrossRace, rc.soonestExpiryAcrossRace)
		coordinator.Start()

		winner := coordinator.WaitForWinner()

		// Capture upstream request statuses for UI display
		statuses := coordinator.GetRequestStatuses()
		rc.reqLog.UpstreamRequests = store.UpstreamRequestStatus{
			Main:     statuses["main"],
			Second:   statuses["second"],
			Fallback: statuses["fallback"],
		}

		if winner != nil {
			// WIN-CLEAR (wi-7769a812 part 3): the winner's identity is
			// proven healthy — clear its own live skip marks
			// ((credential, wire-model) + (provider, wire-model)) and
			// ITS OWN grouplb member-cooldown row — keyed by the
			// WINNER's modelList identity (winner.modelID), never
			// groupPick.MemberID: on a chain-entry win the picked head
			// FAILED this request and its cooldown must survive
			// (CRITICAL-FIX part 3 review, item 4). The incident's
			// companion defect: agentic-go WON while its marks stayed
			// live, so every later request still benched it.
			winnerWireModel := winner.resolved.InternalModel
			if winnerWireModel == "" {
				winnerWireModel = winner.modelID
			}
			h.clearWinnerLayerState(&rc.conf, rc.groupPick, winner.resolved.CredentialID, winner.resolved.Provider, winnerWireModel, winner.modelID)
			defer func() {
				if winner.cancel != nil {
					winner.cancel()
				}
			}()

			if rc.isStream {
				// Stream the final result from the winner's buffer
				if err := h.streamResult(w, rc, winner); err != nil {
					// Client write failed (e.g., Cloudflare/proxy dropped connection).
					// Not a race failure: the attempt counter for the hash was
					// already recorded at the early-exit gate, so a client retry
					// still progresses through the trigger schedule.
					log.Printf("[STREAM] Client write failed for request %s: %v", rc.reqID, err)

					// Cancel all other parallel requests to stop wasted upstream work.
					// The winner's cancel is handled by the defer below.
					coordinator.cancelAllExcept(winner)

					// Don't send error response - connection is already broken
					return
				}
			} else {
				// Send a single JSON response from the winner's buffer
				h.handleNonStreamResult(w, rc, winner)
			}
			return
		}

		// If winner is nil, it means either context cancelled or all models failed
		// F6 (wi-7769a812 part 3): harvest this coordinator's
		// credential-layer tried-set + W3 flags into the request-scoped
		// maps (seeded into the next iteration's coordinator), and union
		// the attempted model ids into the group tried-set — a member
		// that ran as a CHAIN entry of another member must not be
		// re-offered by the re-entry (F3).
		triedCreds, soonestSpent := coordinator.HarvestRaceState()
		if len(triedCreds) > 0 {
			if rc.credTriedAcrossRace == nil {
				rc.credTriedAcrossRace = make(map[string]bool)
			}
			for id := range triedCreds {
				rc.credTriedAcrossRace[id] = true
			}
		}
		if len(soonestSpent) > 0 {
			if rc.soonestExpiryAcrossRace == nil {
				rc.soonestExpiryAcrossRace = make(map[string]bool)
			}
			for m := range soonestSpent {
				rc.soonestExpiryAcrossRace[m] = true
			}
		}
		// THE ATTEMPT SEAM: the coordinator's spawn rows ARE this
		// request's upstream-attempt record (the same accounting the
		// all-failed verdict uses — modelAttemptCountLocked counts the
		// model-attempt rows). The counter lives on the REQUEST context
		// because the group re-entry builds a FRESH coordinator.
		attemptedModels := coordinator.AttemptedModelIDs()
		if len(attemptedModels) > 0 {
			rc.upstreamAttempts += len(attemptedModels)
		}
		if rc.groupPick != nil {
			if rc.groupTriedMembers == nil {
				rc.groupTriedMembers = make(map[string]bool)
			}
			for _, m := range attemptedModels {
				rc.groupTriedMembers[m] = true
			}
		}
		select {
		case <-rc.baseCtx.Done():
			// Context cancelled (timeout, client disconnect, etc.) - treat as failure
			log.Printf("Request %s context cancelled (timeout or client disconnect)", rc.reqID)
			h.handleRaceFailure(w, rc, coordinator, heartbeatCancel, "context_cancelled")
			return
		default:
			// All attempts failed with errors. Group-level Am24
			// re-entry — wi-7769a812 part 2 made it EXHAUSTIVE:
			// terminal member-pipeline failure ⇒ ExcludeAndReselect +
			// rebuild + resume, up to N attempts for an N-member group
			// (the one-re-entry latch is retired). The gate is
			// PRE-COMMIT only:
			//
			//   !rc.streamingNonRetryable.Load() — the commit flag
			//   (first NON-HEARTBEAT upstream payload frame forwarded).
			//   Headers / ": connected" / heartbeats are non-committing
			//   framing (headersSent is set BEFORE ": connected" — it is
			//   PROVEN to close the window on framing-only progress and
			//   is NEVER the gate).
			//
			//   LEADER DECISION 3 OVERTURN (2026-11-13, wi-7769a812
			//   part 2): the original ruling (real-streaming-default
			//   plan §Phase 5 / Deferred #2, comment preserved at the
			//   streamingNonRetryable field in handler_helpers.go)
			//   held the flag write-only because the winner-nil check
			//   covered enforcement. The winner-nil check cannot
			//   express the member-advance question: it lives inside
			//   the coordinator, and the advance decision is the
			//   HANDLER's, after the coordinator resolved with no
			//   winner. Pre-commit member advance is safe — framing
			//   carries no model content, so switching members behind
			//   headers/heartbeats cannot corrupt the stream. The old
			//   decision assumed post-commit/one-shot semantics for
			//   any future reader; the exhaustive pre-commit window is
			//   exactly the case it did not consider. Full overturn
			//   rationale: commit body of wi-7769a812 part 2 +
			//   handler_helpers.go streamingNonRetryable comment.
			//
			//   P2 fail-fast: the coordinator's final classified
			//   failure (FinalFailureReason) client-attributable ⇒
			//   stop honestly — no more members, no more models.
			if rc.groupPick != nil &&
				!rc.streamingNonRetryable.Load() &&
				rc.groupAdvances < models.MaxGroupMembers {
				// GROUP-layer observability (wi-4d2c8e17 — LABELS): the
				// GENUINE member hop is a GROUP event. Chain hops inside
				// the member pipeline emit model_* from the coordinator;
				// HERE we emit group_* only when the member pipeline is
				// exhausted and the handler actually walks to another
				// member (or finds none). Capture the failed member
				// BEFORE tryGroupReentry mutates rc.groupPick.
				failedGroup := rc.groupPick.GroupID
				failedMember := rc.groupPick.MemberID
				fr := coordinator.FinalFailureReason()
				if !memberAdvanceBlocked(fr) && h.tryGroupReentry(rc) {
					h.publishGroupHop("group_member_failed", failedGroup, failedMember, "", fr.String())
					h.publishGroupHop("group_fallback_attempted", failedGroup, failedMember, rc.groupPick.MemberID, fr.String())
					h.publishGroupHop("group_fallback_advanced", failedGroup, failedMember, rc.groupPick.MemberID, fr.String())
					continue
				}
				h.publishGroupHop("group_member_failed", failedGroup, failedMember, "", fr.String())
				h.publishGroupHop("group_fallback_exhausted", failedGroup, failedMember, "", fr.String())
			}
			log.Printf("All models failed for request %s (Race Retry)", rc.reqID)
			// ATTEMPT-COUNT-KEYED TERMINAL VERDICT (leader ruling): a
			// re-entry that exhausted ON SCOPE is a PERMISSION failure
			// ONLY when no upstream attempt ran at all. With >= 1
			// attempt the terminal availability envelope (502
			// all_models_failed) is UNCHANGED — we tried, the upstream
			// failed, and the failure is an availability failure.
			//
			// The !headersSent conjunct mirrors the Anthropic lane's
			// twin: once the response headers are on the wire a 403
			// body would corrupt the committed stream, so the verdict
			// falls through to the EXISTING terminal path below
			// (handleRaceFailure), which owns the headers-sent case
			// (it emits an SSE error instead of a JSON envelope).
			//
			// Behaviour-neutral today: scopeExhausted is set only by the
			// group re-entry, which runs only AFTER a failed coordinator
			// (so upstreamAttempts >= 1 and this branch is unreachable);
			// the conjunct makes the branch safe if a future path sets
			// scopeExhausted pre-attempt.
			if rc.scopeExhausted && rc.upstreamAttempts == 0 && !rc.headersSent {
				h.denyModelNotAllowed(w, rc)
				return
			}
			h.handleRaceFailure(w, rc, coordinator, heartbeatCancel, "all_models_failed")
			return
		}
	}
}

// tryGroupReentry performs the Am24 group-level reselect + rebuild.
// Member-internal 429s never reach here (absorbed by the member's own
// credential LB inside the race); this fires ONLY on the terminal
// all-attempts-failed exit. Returns true when the request should
// resume execution with the rebuilt list. wi-7769a812 part 2: the
// caller's gate bounds this to N attempts for an N-member group
// (groupAdvances counter + the pool's own exhaustion); the
// replacement member is refused when THIS request already tried it
// (groupTriedMembers) — pool-ordered iteration, each member at most
// once per request.
//
// group-backup-members / D5: when the recovery is sourced from the
// BACKUP pool (all defaults had been excluded/failed), the log
// line carries an explicit pool=backup token; the
// model_member_selected event's pool field is the same. The
// retry-after log line preserves existing behavior for the
// default-pool re-entry.
//
// wi-7769a812 part 3 — ORDERING-NOT-FILTERING (the governing
// semantics of this whole function, superseding the part-2 D3
// mode-switch cascade): the skip cache is ORDERING, never a gate —
// marks demote candidates to the tail (soonest-expiring first) and
// never remove them; the rewrite (pickServableGroupMember) is the
// single ordering authority for BOTH the offer and the rebuild, so
// they can no longer diverge. Within-request: a member that already
// failed THIS request stays excluded (groupTriedMembers — a TRUE
// exclusion); cross-request marks only demote. The member exclusion
// window handed to the engine on a genuine failure remains the
// CONFIG-OWNED skip-cache TTL.
func (h *Handler) tryGroupReentry(rc *requestContext) bool {
	if _, ok := rc.conf.ModelsConfig.(models.GroupRewriter); !ok {
		return false
	}
	if rc.groupTriedMembers == nil {
		rc.groupTriedMembers = make(map[string]bool)
	}
	// F3 — true within-request exclusion with a bounded offer walk:
	// each ExcludeGroupMember call excludes one member (genuine
	// failure bookkeeping — every refused offer already failed THIS
	// request) and the ordering sweep's head strictly advances, so
	// the walk terminates in ≤ member-count iterations. Bounded by
	// MaxGroupMembers for safety.
	//
	// The offer is SCOPED (ADR D6): the walk never offers an
	// out-of-reach member, and an out-of-reach member is never cooled
	// (a scope exclusion is not a health fact — it must not create
	// cross-request state).
	replacement, pool, avail := "", "", false
	excludeTarget := rc.groupPick.MemberID
	for iter := 0; iter <= models.MaxGroupMembers; iter++ {
		outcome := h.excludeGroupMemberScoped(&rc.conf, rc.scope, rc.groupPick.GroupID, rc.conversationKey, excludeTarget, rc.conf.UpstreamSkipCacheTTL)
		if !outcome.OK || outcome.Replacement == "" {
			if outcome.ScopeOnly {
				// EVERY remaining candidate was out of reach: a SCOPE
				// exhaustion, not an availability failure. The verdict
				// is attempt-count-keyed at the terminal site (the
				// re-entry itself only runs AFTER an attempt).
				rc.scopeExhausted = true
				log.Printf("[authz] request %s: group %s exhausted ON SCOPE (every remaining member is outside this token's reach)", rc.reqID, rc.groupPick.GroupID)
			}
			return false // single-member group / all members exhausted — real failure
		}
		replacement, pool, avail = outcome.Replacement, outcome.Pool, true
		if !rc.groupTriedMembers[replacement] {
			break
		}
		log.Printf("[grouplb] request %s: pool re-offered already-tried member %s — excluding and advancing the ordering scan", rc.reqID, replacement)
		excludeTarget = replacement
	}
	if !avail || replacement == "" || rc.groupTriedMembers[replacement] {
		log.Printf("[grouplb] request %s: offer walk exhausted without a fresh member — group fallback exhausted", rc.reqID)
		return false
	}

	// Rebuild the modelList via the SAME rewrite helper (the offer and
	// the rebuild are one ordering authority — store_group_rewrite.go
	// ExcludeGroupMember delegates to pickServableGroupMember).
	outcome := h.applyGroupRewrite(&rc.conf, rc.modelListOriginal, rc.conversationKey, rc.scope)
	if outcome.failGroupID != "" || outcome.pick == nil {
		if outcome.scopeExcluded {
			// SCOPE exhaustion at the rebuild. The verdict is
			// attempt-count-keyed (leader ruling): ZERO upstream
			// attempts ⇒ 403 access_denied; >= 1 attempt ⇒ the
			// terminal availability path (502) is unchanged.
			rc.scopeExhausted = true
			log.Printf("[authz] request %s: group %s rebuild exhausted ON SCOPE — no member of this group is inside the token's reach", rc.reqID, rc.groupPick.GroupID)
			return false
		}
		log.Printf("[grouplb] request %s: re-entry rebuild failed for group %s — treating as real failure", rc.reqID, rc.groupPick.GroupID)
		return false
	}
	pick := outcome.pick
	// F3 post-rebuild guard: the rebuild is the SAME ordering
	// authority as the offer walk, so it normally re-serves the
	// accepted replacement — the tried-mark happens HERE (after the
	// guard) so agreement passes and only genuine divergence onto an
	// already-attempted member is refused. Never race a member THIS
	// request already attempted.
	if rc.groupTriedMembers[pick.MemberID] {
		log.Printf("[grouplb] request %s: rebuild re-served already-tried member %s — group fallback exhausted", rc.reqID, pick.MemberID)
		return false
	}
	rc.groupTriedMembers[pick.MemberID] = true
	rc.groupAdvances++
	rc.modelList = outcome.list
	rc.groupPick = pick
	// C4 — THE REACH FILTER on the rebuilt list (the rebuild
	// re-materialises the member list). C1 re-filters at the loop top
	// of the next iteration — this makes the obligation explicit at
	// the swap site too (both, by design).
	rc.modelList = h.applyReachFilter(rc.scope, rc.modelList)
	if pick.MemberID != rc.reqLog.Model {
		rc.reqLog.RouteModel = pick.MemberID
	} else {
		rc.reqLog.RouteModel = ""
	}
	if pool == models.PoolBackup {
		log.Printf("[grouplb] request %s: terminal member failure — group %s pool=backup reselect member=%s → advancing to next member in pool order (exhaustive fallback)",
			rc.reqID, pick.GroupID, pick.MemberID)
	} else {
		log.Printf("[grouplb] request %s: terminal member failure — group %s reselect member=%s → advancing to next member in pool order (exhaustive fallback)",
			rc.reqID, pick.GroupID, pick.MemberID)
	}
	if h.bus != nil {
		payload := map[string]interface{}{
			"id":          rc.reqID,
			"group_id":    pick.GroupID,
			"member_id":   pick.MemberID,
			"replacement": replacement,
		}
		// group-backup-members / D5: the additive `pool` field
		// marks a BACKUP-pool serve ("backup"). Per the M3 contract
		// it is ABSENT for a default-pool serve — a "default" value
		// is never emitted.
		//
		// The model_member_selected event's own `pool` field cannot
		// cover this path: the re-entry's binding was created by
		// ExcludeGroupMember, so the rebuild returns
		// MemberNewlyBound=false and the newlyBound-gated
		// model_member_selected emission never fires. The failover
		// event is the only event the re-entry emits, so the pool
		// rides here. Bounded + redacted by construction (ids only).
		if pool == models.PoolBackup {
			payload["pool"] = models.PoolBackup
		}
		h.publishEvent(grouplb.EventMemberFailover, payload)
	}
	return true
}

// handleRaceFailure handles the common failure case for race retry.
// It finalizes the failed request (log, event, error response).
// This is called when either all models failed with errors OR the context was cancelled.
func (h *Handler) handleRaceFailure(w http.ResponseWriter, rc *requestContext, coordinator *raceCoordinator, heartbeatCancel context.CancelFunc, reason string) {
	// Cancel heartbeat first to prevent concurrent writes with sendSSEError
	// This is safe to call even if nil (no-op)
	if heartbeatCancel != nil {
		heartbeatCancel()
	}
	// Note: no hash marking is needed here — the attempt counter is
	// recorded at request entry (early-exit gate), so a client retry
	// still progresses through the trigger schedule.

	// Get final error info from coordinator (OpenCode-compatible format)
	var errInfo FinalErrorInfo
	switch reason {
	case "context_cancelled":
		// Create error info for context cancellation
		errInfo = FinalErrorInfo{
			HTTPStatus: http.StatusGatewayTimeout,
			Message:    "Request timeout or client disconnected",
			ErrorType:  "timeout",
			ErrorCode:  "request_timeout",
		}
	case "all_models_failed":
		// First check if stream deadline fired with no content (specific timeout message)
		if deadlineErr := coordinator.GetStreamDeadlineError(); deadlineErr != nil {
			errInfo = *deadlineErr
		} else {
			errInfo = coordinator.GetFinalErrorInfo()
		}
	}

	// Log failure
	rc.reqLog.Status = "failed"
	rc.reqLog.Error = errInfo.Message
	rc.reqLog.EndTime = time.Now()
	rc.reqLog.Duration = time.Since(rc.startTime).String()
	h.store.Add(rc.reqLog)

	h.publishEvent("request_failed", map[string]interface{}{
		"id":    rc.reqID,
		"error": rc.reqLog.Error,
	})

	// If headers already sent (streaming request), send SSE error instead of HTTP error
	if rc.headersSent {
		h.sendSSEError(w, models.ErrorTypeServerError, errInfo.Message)
	} else {
		h.sendError(w, errInfo.HTTPStatus, errInfo.Message, errInfo.ErrorType, errInfo.ErrorCode)
	}
}

// streamResult flushes the winner's buffer to the client.
// Note: Headers and heartbeat are already set up in HandleChatCompletions.
// Returns an error if client write failed (e.g., connection dropped by Cloudflare/proxy).
func (h *Handler) streamResult(w http.ResponseWriter, rc *requestContext, winner *upstreamRequest) error {
	buffer := winner.GetBuffer()
	if buffer == nil {
		// Buffer released by Cancel() before relay started — the request
		// was cancelled (client disconnect / coordinator shutdown) and no
		// data can ever arrive. Streaming an empty 200 would hang the
		// relay loop on a permanently-closed notify channel.
		return fmt.Errorf("winner buffer released before streaming (request cancelled)")
	}
	readIndex := 0

	// Track if we've already sent an error (to avoid duplicates)
	idleTerminated := false

	// Capture raw bytes BEFORE any pruning happens - this is the complete response
	//
	// Phase 2 / real-streaming-default (task 2.4 / R2 fix) — gate the
	// entry raw-capture to BUFFERED mode. In live mode the relay runs
	// concurrently with Adds, so the entry capture would save a partial
	// prefix and double-save with the Done capture (:1194-1200 below).
	// The Done capture is authoritative in live mode (full stream,
	// single-save); entry capture is byte-identical to today's buffered
	// double-save.
	if rc.bufferMode && rc.conf.LogRawUpstreamResponse {
		if buf := winner.GetBuffer(); buf != nil {
			capturedBytes := buf.GetAllRawBytesOnce()
			go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
		}
	}

	flusher, _ := w.(http.Flusher)

	// load-balance-groups / Am26 (Q5): group responses mask the
	// top-level "model" field of each complete SSE event to the
	// CLIENT-REQUESTED string. Buffered PER COMPLETE EVENT (Am6) —
	// never per raw chunk: a JSON object can straddle chunk
	// boundaries and a per-chunk patch would fail open on straddled
	// events (intermittent upstream-name leak). Non-group requests
	// get a nil masker — process() returns the chunk unchanged
	// (zero copies, zero risk for existing traffic). Content
	// extraction below keeps consuming the ORIGINAL chunk bytes.
	sseMasker := newSSEModelMasker(groupMaskTarget(rc))

	// Stream existing chunks first (with mutex to sync with heartbeat writes)
	chunks, _ := buffer.GetChunksFrom(readIndex)
	rc.writeMu.Lock()
	for _, chunk := range chunks {
		if _, err := w.Write(sseMasker.process(chunk)); err != nil {
			rc.writeMu.Unlock()
			return fmt.Errorf("client write failed: %w", err)
		}
		// Extract content for logging
		if bytes.HasPrefix(chunk, []byte("data: ")) {
			data := bytes.TrimPrefix(chunk, []byte("data: "))
			extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
		}
		readIndex++
		// Phase 2 / real-streaming-default (task 2.5) — set
		// `streamingNonRetryable` on the FIRST successful client
		// write, inside the existing rc.writeMu critical section.
		// The atomic Store(true) is inherently idempotent — no
		// Load-before-Store guard exists — and the writeMu critical
		// section provides the ordering/visibility boundary between
		// the relay write and any concurrent reader (reset() clears
		// it per request lifecycle — see handler_helpers.go:156).
		// After this point: no racing / fallback / mid-stream
		// credential failover — the stream is live.
		rc.streamingNonRetryable.Store(true)
	}
	if flusher != nil {
		flusher.Flush()
	}
	rc.writeMu.Unlock()
	// Phase 2 / real-streaming-default (task 2.4 / R2 fix) — gate
	// Prune to BUFFERED mode. In live mode the relay runs concurrently
	// with Adds, so Prune would non-deterministically truncate the
	// raw bytes consumed by the usage estimator (line ~1244) and
	// the bufferstore Done capture (~:1194-1200). Buffered mode
	// keeps today's behavior byte-identical (Prune never fires
	// there today anyway — ShouldPrune is false when readIndex==len).
	if rc.bufferMode && buffer.ShouldPrune(readIndex) {
		buffer.Prune(readIndex)
	}

	// Continue streaming until complete
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-rc.baseCtx.Done():
			// Context cancelled - could be client disconnect OR timeout
			// Go's HTTP server cancels r.Context() on client disconnect
			if rc.baseCtx.Err() == context.Canceled {
				return fmt.Errorf("client disconnected (context cancelled by HTTP server)")
			}
			return nil // Deadline exceeded - handled by coordinator
		case <-rc.clientGoneCh:
			// Heartbeat detected client disconnection - signal as error so handler cancels all requests
			return fmt.Errorf("client disconnected (heartbeat detected)")
		case <-buffer.NotifyCh():
			// New data available
			chunks, _ = buffer.GetChunksFrom(readIndex)
			rc.writeMu.Lock()
			for _, chunk := range chunks {
				if _, err := w.Write(sseMasker.process(chunk)); err != nil {
					rc.writeMu.Unlock()
					return fmt.Errorf("client write failed: %w", err)
				}
				// Extract content for logging
				if bytes.HasPrefix(chunk, []byte("data: ")) {
					data := bytes.TrimPrefix(chunk, []byte("data: "))
					extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
				}
				readIndex++
				// Phase 2 / task 2.5 — set streamingNonRetryable on
				// the first successful write (idempotent — atomic
				// Store is a no-op if already true; reset() in
				// handler_helpers.go clears it per request).
				rc.streamingNonRetryable.Store(true)
			}
			if flusher != nil {
				flusher.Flush()
			}
			rc.writeMu.Unlock()
			// Phase 2 / R2 fix — Prune gated to buffered mode (see
			// the entry-site comment at ~:1103 for rationale).
			if rc.bufferMode && buffer.ShouldPrune(readIndex) {
				buffer.Prune(readIndex)
			}
		case <-buffer.Done():
			// Stream complete - drain remaining data
			chunks, _ = buffer.GetChunksFrom(readIndex)
			rc.writeMu.Lock()
			for _, chunk := range chunks {
				if _, err := w.Write(sseMasker.process(chunk)); err != nil {
					rc.writeMu.Unlock()
					return fmt.Errorf("client write failed: %w", err)
				}
				if flusher != nil {
					flusher.Flush()
				}
				// Extract content for logging
				if bytes.HasPrefix(chunk, []byte("data: ")) {
					data := bytes.TrimPrefix(chunk, []byte("data: "))
					extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
				}
				readIndex++
				// Phase 2 / task 2.5 — see entry-site comment.
				rc.streamingNonRetryable.Store(true)
			}
			if flusher != nil {
				flusher.Flush()
			}
			rc.writeMu.Unlock()

			// Am26: flush any masked-stream tail bytes (an
			// incomplete final event) verbatim — fail-open, never
			// dropped.
			if tail := sseMasker.flush(); len(tail) > 0 {
				rc.writeMu.Lock()
				if _, err := w.Write(tail); err != nil {
					rc.writeMu.Unlock()
					return fmt.Errorf("client write failed: %w", err)
				}
				if flusher != nil {
					flusher.Flush()
				}
				rc.writeMu.Unlock()
			}

			// If stream failed, send error event to client
			if err := buffer.Err(); err != nil {
				// If idle termination already handled this, skip duplicate error
				if idleTerminated {
					log.Printf("[STREAM] Buffer closed after idle termination, skipping duplicate error")
					return nil
				}

				log.Printf("[ERROR] Stream buffer closed with error: %v", err)

				// Log raw response on error
				if rc.conf.LogRawUpstreamOnError {
					if buf := winner.GetBuffer(); buf != nil {
						capturedBytes := buf.GetAllRawBytesOnce()
						go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
					}
				}

				// Now safe to prune (after capturing raw response)
				// Phase 2 / R2 fix — Prune gated to buffered mode (see
				// the entry-site comment at ~:1103 for rationale). In
				// live mode the buffer is captured at ~:1206-1213 below
				// (Done case) and at the bufferstore Done site; this
				// error-path Prune is no longer needed in live mode.
				if rc.bufferMode {
					buffer.Prune(readIndex)
				}

				// Send OpenAI-compatible error response
				errResp := models.NewOpenAIError(models.ErrorTypeServerError, "", fmt.Sprintf("Streaming error: %v", err))
				data, _ := json.Marshal(errResp)
				rc.writeMu.Lock()
				fmt.Fprintf(w, "data: %s\n\n", string(data))
				if flusher != nil {
					flusher.Flush()
				}
				rc.writeMu.Unlock()
				return nil // Buffer error already handled with error response to client
			}

			// Log raw response on success if enabled - capture bytes BEFORE final pruning
			if rc.conf.LogRawUpstreamResponse {
				if buf := winner.GetBuffer(); buf != nil {
					capturedBytes := buf.GetAllRawBytesOnce()
					go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
				}
			}

			// Extract usage from the last SSE chunk (it contains the "usage" field)
			// IMPORTANT: Must do this BEFORE Prune, as Prune sets chunks to nil
			chunks, _ = buffer.GetChunksFrom(0)
			for i := len(chunks) - 1; i >= 0; i-- {
				chunk := chunks[i]
				if bytes.HasPrefix(chunk, []byte("data: ")) {
					data := bytes.TrimPrefix(chunk, []byte("data: "))
					if string(data) == "[DONE]" || string(data) == "" {
						continue
					}
					if usage := extractUsageFromChunk(data); usage != nil {
						rc.reqLog.Usage = usage
						break
					}
				}
			}

			// Fallback token counting: if provider didn't return usage, estimate from buffer
			if token.FallbackEnabled() {
				if rc.reqLog.Usage == nil || (rc.reqLog.Usage.PromptTokens == 0 && rc.reqLog.Usage.CompletionTokens == 0 && rc.reqLog.Usage.TotalTokens == 0) {
					tokenizer := token.GetTokenizer()
					model := winner.GetModelID()
					if model == "" {
						model, _ = rc.requestBody["model"].(string)
					}
					requestBytes, _ := json.Marshal(rc.requestBody)
					promptTokens, err := tokenizer.CountPromptTokens(requestBytes, model)
					if err != nil {
						log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, model)
					}

					rawBytes := winner.GetBuffer().GetAllRawBytesOnce()
					completionText := token.ExtractCompletionTextFromChunks(rawBytes)
					completionTokens, err := tokenizer.CountCompletionTokens(completionText, model)
					if err != nil {
						log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, model)
					}

					rc.reqLog.Usage = &store.Usage{
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						TotalTokens:      promptTokens + completionTokens,
					}
					log.Printf("[DEBUG][fallback-token-count] model=%s prompt=%d completion=%d total=%d (streaming)",
						model, promptTokens, completionTokens, promptTokens+completionTokens)
				}
			}

			// Now safe to prune (after extracting usage)
			// Phase 2 / R2 fix — Prune gated to buffered mode (see the
			// entry-site comment at ~:1103 for rationale). In live
			// mode, GetAllRawBytesOnce at line ~1252 was called BEFORE
			// this site, so the estimator has already seen the full
			// stream; further Prune would be a no-op for live mode but
			// keeps a single consistent shape across both modes.
			if rc.bufferMode {
				buffer.Prune(readIndex)
			}

			// Finalize tool call arguments from builders
			for i := range rc.accumulatedToolCalls {
				if i < len(rc.toolCallArgBuilders) {
					args := rc.toolCallArgBuilders[i].String()
					rc.accumulatedToolCalls[i].Function.Arguments = args

					// Validate JSON arguments
					if args != "" {
						var js interface{}
						if err := json.Unmarshal([]byte(args), &js); err != nil {
							log.Printf("[WARN] Tool call[%d] has invalid JSON arguments: %v (args length: %d)",
								i, err, len(args))
						}
					}
				}
			}

			// Check for duplicate tool call IDs
			seenIDs := make(map[string]int)
			for i, tc := range rc.accumulatedToolCalls {
				if tc.ID != "" {
					if firstIdx, exists := seenIDs[tc.ID]; exists {
						log.Printf("[WARN] Duplicate tool call ID '%s' at indices %d and %d", tc.ID, firstIdx, i)
					} else {
						seenIDs[tc.ID] = i
					}
				}
			}

			// Validate function names are present
			for i, tc := range rc.accumulatedToolCalls {
				if tc.Function.Name == "" {
					log.Printf("[WARN] Tool call[%d] has empty function name", i)
				}
			}

			// Store assistant message
			assistantMsg := store.Message{
				Role:    "assistant",
				Content: rc.accumulatedResponse.String(),
			}
			if rc.accumulatedThinking.Len() > 0 {
				assistantMsg.Thinking = rc.accumulatedThinking.String()
			}
			if len(rc.accumulatedToolCalls) > 0 {
				assistantMsg.ToolCalls = rc.accumulatedToolCalls
			}

			// Log success
			rc.reqLog.Status = "completed"
			rc.reqLog.EndTime = time.Now()
			rc.reqLog.Duration = time.Since(rc.startTime).String()
			rc.reqLog.Messages = append(rc.reqLog.Messages, assistantMsg)
			h.store.Add(rc.reqLog)

			// Count this request for hourly usage tracking
			if rc.tokenID != "" && h.counter != nil {
				var promptTokens, completionTokens, totalTokens int
				if rc.reqLog.Usage != nil {
					promptTokens = rc.reqLog.Usage.PromptTokens
					completionTokens = rc.reqLog.Usage.CompletionTokens
					totalTokens = rc.reqLog.Usage.TotalTokens
				}
				hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
				go func() {
					if err := h.counter.Increment(context.Background(), rc.tokenID, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
						log.Printf("failed to increment usage counter: %v", err)
					}
				}()
			}

			// Count this request for model hourly usage tracking
			if rc.reqLog.Model != "" && h.counter != nil {
				var promptTokens, completionTokens, totalTokens int
				if rc.reqLog.Usage != nil {
					promptTokens = rc.reqLog.Usage.PromptTokens
					completionTokens = rc.reqLog.Usage.CompletionTokens
					totalTokens = rc.reqLog.Usage.TotalTokens
				}
				hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
				go func() {
					// load-balance-groups / Q9: 5s detached-context
					// timeout (decision pattern).
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := h.counter.IncrementModelUsage(ctx, rc.reqLog.Model, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
						log.Printf("failed to increment model usage counter: %v", err)
					}
				}()

				// load-balance-groups / Am25 (Q9): the member usage
				// row — emitted ONLY when a group actually rewrote
				// the request (rc.groupPick != nil — set by
				// applyGroupRewrite on a real pick) AND the winner's
				// model differs from the client-requested string.
				// The group predicate is what keeps the
				// zero-groups==today AC: an ordinary non-group
				// fallback (B serves after A fails) also leaves
				// winnerModel != reqLog.Model, and must still
				// produce exactly ONE row (keyed reqLog.Model).
				// Direct and day-1 self-shadow requests likewise
				// produce exactly ONE row, unchanged.
				if winnerModel := winner.GetModelID(); winnerModel != "" && winnerModel != rc.reqLog.Model && rc.groupPick != nil && h.counter != nil {
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						if err := h.counter.IncrementModelUsage(ctx, winnerModel, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
							log.Printf("failed to increment member usage counter: %v", err)
						}
					}()
				}
			}

			h.publishEvent("request_completed", map[string]interface{}{
				"id":       rc.reqID,
				"model":    winner.GetModelID(),
				"duration": rc.reqLog.Duration,
				"race":     true,
			})

			// Sticky last-success: record ONLY a DELIVERED success —
			// this publish site is reached after the winner's bytes
			// were streamed to the client successfully (a client-write
			// failure returns at the [STREAM] Client write failed arm
			// above and never reaches here). The winner's model ID is a
			// concrete raced member id, matching the choke-point list
			// keyspace. Never recorded on race_winner_selected
			// (first-byte) or race_deadline_pick (best-partial;
			// winner.deadlinePick is written in
			// (*raceCoordinator).handleStreamingDeadline — NOT in
			// race_executor.go).
			if rc.conf.stickyActive(rc.conversationKey) && !winner.deadlinePick {
				h.stickyEngine.RecordSuccess(rc.conversationKey, winner.GetModelID())
			}

			// Log raw response on success if enabled - we capture at beginning of streamResult
			return nil
		case <-ticker.C:
			// Safety backup if notification missed
			chunks, _ = buffer.GetChunksFrom(readIndex)
			if len(chunks) > 0 {
				rc.writeMu.Lock()
				for _, chunk := range chunks {
					if _, err := w.Write(sseMasker.process(chunk)); err != nil {
						rc.writeMu.Unlock()
						return fmt.Errorf("client write failed: %w", err)
					}
					if bytes.HasPrefix(chunk, []byte("data: ")) {
						data := bytes.TrimPrefix(chunk, []byte("data: "))
						extractStreamChunkContent(data, &rc.accumulatedResponse, &rc.accumulatedThinking, &rc.accumulatedToolCalls, &rc.toolCallArgBuilders)
					}
					readIndex++
					// Phase 2 / task 2.5 — see entry-site comment.
					rc.streamingNonRetryable.Store(true)
				}
				if flusher != nil {
					flusher.Flush()
				}
				rc.writeMu.Unlock()
				// Phase 2 / R2 fix — Prune gated to buffered mode (see
				// the entry-site comment at ~:1103 for rationale).
				if rc.bufferMode && buffer.ShouldPrune(readIndex) {
					buffer.Prune(readIndex)
				}
			}

			// Idle termination: check if upstream has stopped sending data
			if rc.conf.IdleTerminationEnabled && rc.conf.IdleTerminationTimeout > 0 && !buffer.IsComplete() {
				if winner.IsIdle(rc.conf.IdleTerminationTimeout) {
					log.Printf("[STREAM] Idle termination: upstream idle for %v, terminating stream",
						time.Since(winner.GetLastActivity()))

					// Mark as terminated to avoid duplicate error in buffer.Done()
					idleTerminated = true

					// Cancel the winner to close the buffer and stop upstream goroutine.
					// Don't send SSE error - connection is already broken (Cloudflare dropped).
					// The buffer.Done() case will handle cleanup with idleTerminated guard.
					winner.Cancel()

					return nil
				}
			}
		}
	}
}

// handleNonStreamResult sends a single JSON response from the winner's buffer
func (h *Handler) handleNonStreamResult(w http.ResponseWriter, rc *requestContext, winner *upstreamRequest) {
	buffer := winner.GetBuffer()

	// Wait for buffer to be complete if not already
	select {
	case <-buffer.Done():
	case <-rc.baseCtx.Done():
		return
	}

	// Check for buffer error
	if err := buffer.Err(); err != nil {
		// Log raw response on error if enabled - capture bytes before returning
		if rc.conf.LogRawUpstreamOnError {
			if buf := winner.GetBuffer(); buf != nil {
				capturedBytes := buf.GetAllRawBytesOnce()
				go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		errResp := models.NewOpenAIError(models.ErrorTypeUpstreamError, "", fmt.Sprintf("Upstream error: %v", err))
		data, _ := json.Marshal(errResp)
		w.Write(data)
		return
	}

	chunks, _ := buffer.GetChunksFrom(0)
	var finalBody []byte

	// Concatenate chunks, stripping SSE prefixes if present
	for _, chunk := range chunks {
		line := string(chunk)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			data = strings.TrimSpace(data)
			if data == "[DONE]" || data == "" {
				continue
			}
			finalBody = append(finalBody, []byte(data)...)
		} else {
			finalBody = append(finalBody, chunk...)
		}
	}

	// Set headers
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// load-balance-groups / Am26 (Q5): group responses mask the
	// top-level "model" field to the CLIENT-REQUESTED string
	// (byte-preserving patch; fail-open to verbatim on any parse
	// surprise — the response is never dropped). Non-group requests:
	// byte-identical to upstream. Error envelopes never reach here
	// (they carry no top-level model and return above).
	if maskTarget := groupMaskTarget(rc); maskTarget != "" {
		patched := patchTopLevelModelField(finalBody, maskTarget)
		if len(patched) != len(finalBody) || !bytes.Equal(patched, finalBody) {
			finalBody = patched
		}
	}
	// Capture the client-write outcome: a failed (or SHORT) body write
	// means the response was NOT fully delivered, so the sticky
	// last-success record below must be skipped — the stream lane
	// already returns before its record site on a client write failure;
	// this makes the two lanes symmetric. STRICTLY SCOPED: the outcome
	// gates ONLY the sticky record. Status code, envelopes and logging
	// are unchanged.
	n, writeErr := w.Write(finalBody)

	// Extract content AND thinking for logging. The helper handles
	// reasoning_content / reasoning / thinking / provider_specific_fields.reasoning_content
	// variants in addition to plain string content, so non-stream race-path
	// responses now persist thinking alongside content (S3 cross-path parity).
	extractNonStreamContent(finalBody, &rc.accumulatedResponse, &rc.accumulatedThinking)

	// Extract usage from response
	var resp map[string]interface{}
	if err := json.Unmarshal(finalBody, &resp); err == nil {
		if usageData, ok := resp["usage"].(map[string]interface{}); ok {
			usage := &store.Usage{}
			if v, ok := usageData["prompt_tokens"].(float64); ok {
				usage.PromptTokens = int(v)
			}
			if v, ok := usageData["completion_tokens"].(float64); ok {
				usage.CompletionTokens = int(v)
			}
			if v, ok := usageData["total_tokens"].(float64); ok {
				usage.TotalTokens = int(v)
			}
			rc.reqLog.Usage = usage
		}

		// Fallback token counting: if provider didn't return usage, estimate from response
		if token.FallbackEnabled() {
			if rc.reqLog.Usage == nil || (rc.reqLog.Usage.PromptTokens == 0 && rc.reqLog.Usage.CompletionTokens == 0 && rc.reqLog.Usage.TotalTokens == 0) {
				tokenizer := token.GetTokenizer()
				model := winner.GetModelID()
				if model == "" {
					model, _ = rc.requestBody["model"].(string)
				}
				requestBytes, _ := json.Marshal(rc.requestBody)
				promptTokens, err := tokenizer.CountPromptTokens(requestBytes, model)
				if err != nil {
					log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, model)
				}

				completionText := token.ExtractCompletionTextFromJSON(finalBody)
				completionTokens, err := tokenizer.CountCompletionTokens(completionText, model)
				if err != nil {
					log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, model)
				}

				rc.reqLog.Usage = &store.Usage{
					PromptTokens:     promptTokens,
					CompletionTokens: completionTokens,
					TotalTokens:      promptTokens + completionTokens,
				}
				log.Printf("[DEBUG][fallback-token-count] model=%s prompt=%d completion=%d total=%d (non-streaming)",
					model, promptTokens, completionTokens, promptTokens+completionTokens)
			}
		}
	}

	// Log success
	rc.reqLog.Status = "completed"
	rc.reqLog.EndTime = time.Now()
	rc.reqLog.Duration = time.Since(rc.startTime).String()

	assistantMsg := store.Message{
		Role:     "assistant",
		Content:  rc.accumulatedResponse.String(),
		Thinking: rc.accumulatedThinking.String(),
	}
	rc.reqLog.Messages = append(rc.reqLog.Messages, assistantMsg)
	h.store.Add(rc.reqLog)

	// Count this request for hourly usage tracking
	if rc.tokenID != "" && h.counter != nil {
		var promptTokens, completionTokens, totalTokens int
		if rc.reqLog.Usage != nil {
			promptTokens = rc.reqLog.Usage.PromptTokens
			completionTokens = rc.reqLog.Usage.CompletionTokens
			totalTokens = rc.reqLog.Usage.TotalTokens
		}
		hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
		go func() {
			if err := h.counter.Increment(context.Background(), rc.tokenID, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
				log.Printf("failed to increment usage counter: %v", err)
			}
		}()
	}

	// Count this request for model hourly usage tracking
	if rc.reqLog.Model != "" && h.counter != nil {
		var promptTokens, completionTokens, totalTokens int
		if rc.reqLog.Usage != nil {
			promptTokens = rc.reqLog.Usage.PromptTokens
			completionTokens = rc.reqLog.Usage.CompletionTokens
			totalTokens = rc.reqLog.Usage.TotalTokens
		}
		hourBucket := rc.reqLog.StartTime.UTC().Format("2006-01-02T15")
		go func() {
			// load-balance-groups / Q9: 5s detached-context timeout
			// (decision pattern).
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := h.counter.IncrementModelUsage(ctx, rc.reqLog.Model, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
				log.Printf("failed to increment model usage counter: %v", err)
			}
		}()

		// load-balance-groups / Am25 (Q9): the member usage row —
		// emitted ONLY when a group actually rewrote the request
		// (rc.groupPick != nil) AND the winner's model differs from
		// the client-requested string — same gate as the stream
		// site (rewritten ⇒ 2 rows, direct ⇒ 1, day-1 ⇒ exactly 1,
		// non-group fallback ⇒ exactly 1).
		if winnerModel := winner.GetModelID(); winnerModel != "" && winnerModel != rc.reqLog.Model && rc.groupPick != nil && h.counter != nil {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := h.counter.IncrementModelUsage(ctx, winnerModel, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
					log.Printf("failed to increment member usage counter: %v", err)
				}
			}()
		}
	}

	h.publishEvent("request_completed", map[string]interface{}{
		"id":       rc.reqID,
		"model":    winner.GetModelID(),
		"duration": rc.reqLog.Duration,
		"race":     true,
	})

	// Sticky last-success: record ONLY a DELIVERED success — this is
	// the non-stream completion site, reached after the winner's
	// response body was written. The writeErr / short-write gate is the
	// non-stream mirror of the stream lane's client-write-fail early
	// return: a dropped or truncated body is not a delivered success.
	if rc.conf.stickyActive(rc.conversationKey) && !winner.deadlinePick && writeErr == nil && n == len(finalBody) {
		h.stickyEngine.RecordSuccess(rc.conversationKey, winner.GetModelID())
	}

	// Log raw response on success if enabled - capture bytes before returning
	if rc.conf.LogRawUpstreamResponse {
		if buf := winner.GetBuffer(); buf != nil {
			capturedBytes := buf.GetAllRawBytesOnce()
			go h.saveRawResponse(rc.reqID, capturedBytes, rc.rawBody, rc.conf.LogRawUpstreamMaxKB)
		}
	}
}
