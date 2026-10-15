// Package decision handler. Skeleton modeled on pkg/imggen/handler.go
// (~80% copy-and-rename of the handler shell; Decision Models
// commission / §3.2 / §3.2.a). At P2 the handler owns the
// complete 17-step lifecycle:
//
//  1. POST-only method guard (house style; 405 via http.Error).
//  2. MaxBytesReader at 16 MB → 413 (house OpenAI envelope on
//     *http.MaxBytesError).
//  3. Auth (extractAPIKey + ValidateToken with 5s ctx). tokenStore
//     nil ⇒ auth disabled. Invalid token ⇒ 401 (house envelope).
//  4. parseBody: typed parse into DecisionRequest ONCE (the
//     original raw bytes are buffered for the openai-decision
//     byte-faithful pass-through at step 10).
//  5. Typed gotcha validators (validators.go) — 400 + house
//     OpenAI envelope on the first failure (typed validation).
//  6. resolveModel + EXPLICIT IsDecision() check — chat / image-gen
//     models posted here return 400 + code "decision_misroute"
//     (closes imggen reverse-direction gap).
//  7. Token allow-list (403 only when AllowedModels is non-empty
//     AND the model is not in the list).
//  8. Credential resolution (stable affinity key (modelID, modelID)).
//  9. Provider lookup on the local registry. Unknown provider ⇒ 502
//     + "no decision adapter registered for provider" (finding 6
//     trimmed message).
// 10. provider.BuildUpstream(cred, req, rawBody). On err ⇒ 502 +
//     house envelope code "decision_upstream_build_failed" (Q15).
// 11. Timeout ctx (DecisionTimeout, default 30s, clamp [10s,60s]).
// 12. sharedHTTPClient.Do with the timeout ctx threaded via
//     http.NewRequestWithContext (finding 4; NEVER r.Context() —
//     would leak parent request lifecycle and defeat the
//     per-request clamp).
// 13. Response cap io.LimitReader(cap+1) at 64MB (mirror
//     pkg/imggen/handler.go:404); over-cap ⇒ 502.
// 14. provider.InspectResponse(respBytes, originalReq) — unified
//     2-arg typed signature (finding 5). On success returns typed
//     *DecisionResponse for relay; on PromoteToError returns
//     Outcome{RelayStatus, Reason} with body preserved verbatim.
// 15. Relay write: Outcome.RelayStatus == 0 ⇒ body verbatim;
//     != 0 ⇒ set that status, body preserved verbatim.
// 16. Meter on success only — Increment + IncrementModelUsage with
//     input_tokens + output_tokens (chat semantics; chat at
//     pkg/proxy/handler.go:899 / :915); detached 5s ctx (mirror
//     pkg/imggen/handler.go:581). Failures write NO usage row.
// 17. DecisionEvent via events.Bus on every completed decision
//     (sibling of imggen eventTypeImageGeneration).
package decision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// Constants

const (
	// defaultDecisionTimeout is the package default applied when
	// the config knob is zero. Mirrors config.Defaults.DecisionTimeout.
	// Tight default per Q14 / architect amendment 8: vendor
	// "10× faster than Responses API" claim is unverified
	// marketing.
	defaultDecisionTimeout = 30 * time.Second

	// maxRequestBodyBytes is the request body cap. 16 MB
	// accommodates decisions carrying up to ~128 base64 image
	// parts at ~128KB average per the plan's R7 arithmetic.
	// Over-cap → 413 via errors.As(*http.MaxBytesError).
	maxRequestBodyBytes = 16 << 20

	// maxResponseBodyBytes is the response body cap (BE-G2
	// analog). Decisions are short (~451 B openai-decision JSON,
	// System One a bit larger with answers[] — no base64
	// payloads). The +1 lets the handler detect over-cap
	// distinctly.
	maxResponseBodyBytes = 64 << 20

	// meterTimeout bounds the detached DB write (mirror imggen).
	meterTimeout = 5 * time.Second

	// eventTypeDecision is the events.Bus type for every
	// telemetry payload the handler publishes. Mirrors
	// eventTypeImageGeneration at pkg/imggen/handler.go:62
	// (sibling string const). Map payload; nil-safe.
	eventTypeDecision = "decision"
)

// Handler is the /v1/decisions HTTP handler. Construction is
// the single composition root in main.go: pass the config
// manager (for DecisionTimeout), the event bus (for telemetry),
// the token store (for auth), and the usage counter (for
// metering). Mirrors imggen.NewHandler verbatim.
type Handler struct {
	configMgr         configGetter
	bus               *events.Bus
	tokenStore        auth.TokenStoreInterface
	usage             *usage.Counter
	resolveModel      modelResolver
	resolveCredential credentialResolver
}

// configGetter is the slice of config.ManagerInterface this
// handler actually uses. Defined locally so tests can substitute
// a stub without depending on the full config surface.
type configGetter interface {
	GetDecisionTimeout() time.Duration
}

// NewHandler constructs the /v1/decisions handler. The usage
// counter may be nil (metering silently skipped) and the event
// bus may be nil (telemetry silently skipped) — the handler
// degrades gracefully so test / legacy setups work.
func NewHandler(configMgr configGetter, bus *events.Bus, tokenStore auth.TokenStoreInterface, usageCounter *usage.Counter) *Handler {
	return &Handler{
		configMgr:  configMgr,
		bus:        bus,
		tokenStore: tokenStore,
		usage:      usageCounter,
	}
}

// decisionTimeout returns the configured per-request deadline,
// falling back to the package default when the config knob is
// zero (e.g., a hand-edited config or a test stub).
func (h *Handler) decisionTimeout() time.Duration {
	if h.configMgr == nil {
		return defaultDecisionTimeout
	}
	d := h.configMgr.GetDecisionTimeout()
	if d <= 0 {
		return defaultDecisionTimeout
	}
	return d
}

// HandleDecision is the POST /v1/decisions entry point
// (Decision Models commission / §3.2 / §10 P1+P2). It is
// registered in cmd/main.go as a sibling of the chat routes.
// POST-only is enforced in-handler (house style; no Go 1.22 mux
// method patterns anywhere else in the codebase per
// cmd/main.go:286-290).
func (h *Handler) HandleDecision(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Step 1 — POST-only method guard.
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Step 2 — MaxBytesReader at 16 MB → 413 on *http.MaxBytesError.
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	// rawBody is the exact request bytes — buffered ONCE before
	// parse for the openai-decision byte-faithful pass-through
	// (Q18 / A26). The typed parse below consumes the typed
	// shape but does NOT touch the raw bytes.
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			h.publishEvent(eventTypeDecision, map[string]interface{}{
				"outcome":         "request_too_large",
				"max_bytes":       maxRequestBodyBytes,
				"duration_ms":     time.Since(startTime).Milliseconds(),
				"upstream_status": 0,
			})
			h.openAIError(w, http.StatusRequestEntityTooLarge,
				"request body exceeds 16 MB cap")
			return
		}
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "request_read_failed",
			"error":           err.Error(),
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusInternalServerError,
			"failed to read request body")
		return
	}
	_ = r.Body.Close()

	// Step 3 — Auth.
	authToken, ok := h.authenticate(r)
	if !ok {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "auth_failed",
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.sendAuthError(w)
		return
	}

	// Step 4 — parseBody: typed parse into the typed
	// DecisionRequest. The raw bytes are kept for the
	// openai-decision adapter (Q18 byte-faithful pass-through).
	var req DecisionRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "invalid_json",
			"error":           err.Error(),
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Model == "" {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "missing_model",
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadRequest, "model field is required")
		return
	}
	modelID := req.Model

	// Step 5 — Typed gotcha validators (typed predicates in
	// validators.go). 400 + house OpenAI envelope on the first
	// failure (Q4 — typed validation lives at the edge; typesafe
	// re-asserts a subset at BuildUpstream as defense-in-depth).
	if msg, failed := validateFirstFailed(&req); failed {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "validation_failed",
			"model":           modelID,
			"error":           msg,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadRequest, msg)
		return
	}

	// Step 6 — resolveModel + EXPLICIT IsDecision() check
	// (closes the imggen reverse-direction gap; Decision Models /
	// §3.4 row 3). Chat and image-gen models posted here are
	// rejected with 400 + code "decision_misroute" (house
	// envelope). Unknown models return 404 first.
	model, ok := h.safeResolveModel(modelID)
	if !ok {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "model_not_found",
			"model":           modelID,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusNotFound,
			fmt.Sprintf("unknown model %q", modelID))
		return
	}
	if !model.IsDecision() {
		log.Printf("[decision] rejecting decision-route post to non-decision kind model=%s (kind=%s)", model.ID, model.Kind)
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "decision_misroute",
			"model":           modelID,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIErrorWithCode(w, http.StatusBadRequest,
			fmt.Sprintf("decision models are served by /v1/decisions only (got kind %q)", model.Kind),
			"decision_misroute")
		return
	}

	// Step 7 — Token allow-list (mirrors imggen). Empty
	// AllowedModels = all models allowed.
	if authToken != nil && len(authToken.AllowedModels) > 0 {
		if !authToken.IsModelAllowed(modelID) {
			h.publishEvent(eventTypeDecision, map[string]interface{}{
				"outcome":         "model_not_allowed",
				"model":           modelID,
				"token":           authToken.ID,
				"duration_ms":     time.Since(startTime).Milliseconds(),
				"upstream_status": 0,
			})
			h.sendModelNotAllowedError(w, modelID)
			return
		}
	}

	// Step 8 — Credential resolution. main.go wires the resolver
	// to modelsConfig.ResolveInternalConfigWithAffinity(modelID,
	// modelID) (stable key per BE-A3 + amendment 18 — same pattern
	// the chat and imggen paths use).
	cred, ok := h.safeResolveCredential(modelID)
	if !ok {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "credential_unresolved",
			"model":           modelID,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadGateway,
			fmt.Sprintf("no credentials configured for model %q", modelID))
		return
	}

	// Step 9 — Provider lookup. Empty registry ⇒ 502 (clean
	// operational fact post-P2; finding 6 trimmed the P1
	// wording to drop the stale "(P1 ships with an empty
	// registry by design; P2 registers ...)" clause).
	provider, ok := lookup(cred.Provider)
	if !ok {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "unsupported_provider",
			"model":           modelID,
			"provider":        cred.Provider,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadGateway,
			fmt.Sprintf("no decision adapter registered for provider %q", cred.Provider))
		return
	}

	// Step 9.5 — Provider-specific pre-flight edge checks.
	// These are edge cases the typesafe adapter's
	// BuildUpstream re-validates as defense-in-depth
	// (amendment 4), but the proxy's house posture is to
	// surface them as 400 BEFORE any upstream call so the
	// client gets a deterministic status. The openai path
	// forwards data URLs as-is (Q3 / R2 posture) so no
	// pre-flight check is needed there.
	if cred.Provider == TypesafeProviderName {
		if msg, failed := typesafePreflightValidate(&req); failed {
			h.publishEvent(eventTypeDecision, map[string]interface{}{
				"outcome":         "typesafe_preflight_failed",
				"model":           modelID,
				"provider":        cred.Provider,
				"error":           msg,
				"duration_ms":     time.Since(startTime).Milliseconds(),
				"upstream_status": 0,
			})
			h.openAIError(w, http.StatusBadRequest, msg)
			return
		}
	}

	// Step 10 — BuildUpstream. err ⇒ 502 + house envelope code
	// "decision_upstream_build_failed" (Q15; BuildUpstream-
	// level vs validation-level distinction).
	upstreamURL, upstreamBody, err := provider.BuildUpstream(cred, &req, rawBody)
	if err != nil {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "build_failed",
			"model":           modelID,
			"provider":        cred.Provider,
			"error":           err.Error(),
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIErrorWithCode(w, http.StatusBadGateway,
			"failed to construct upstream request: "+err.Error(),
			"decision_upstream_build_failed")
		return
	}

	// Step 11 — timeout ctx. The ctx is owned here (not
	// r.Context()) so the per-request clamp binds only this
	// step (finding 4). Threaded through the upstream call via
	// http.NewRequestWithContext at step 12 (NEVER r.Context() —
	// would leak the parent request lifecycle and defeat the
	// per-request DecisionTimeout clamp).
	ctx, cancel := context.WithTimeout(context.Background(), h.decisionTimeout())
	defer cancel()

	// Step 12 — sharedHTTPClient.Do with the timeout ctx
	// threaded via http.NewRequestWithContext (finding 4).
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(upstreamBody))
	if err != nil {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "request_build_failed",
			"model":           modelID,
			"provider":        cred.Provider,
			"error":           err.Error(),
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusInternalServerError,
			"failed to build upstream HTTP request")
		return
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+cred.APIKey)
	// Strip client auth headers + hop-by-hop headers (proxy's
	// sk- token must never reach the decision upstream —
	// parallel of imggen BE-A2).
	for key, values := range r.Header {
		if isHopByHopHeader(key) || isClientAuthHeader(key) {
			continue
		}
		for _, value := range values {
			upstreamReq.Header.Add(key, value)
		}
	}

	// Single upstream call (no retry / fallback / race — the
	// decision shape is sync + token-billed, decisions are
	// cheap+fast).
	resp, err := sharedHTTPClient.Do(upstreamReq)
	if err != nil {
		// Deadline / context expiry surfaces here as well —
		// 504 on context.DeadlineExceeded; 502 on transport
		// error.
		status := http.StatusBadGateway
		outcome := "upstream_transport_error"
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			status = http.StatusGatewayTimeout
			outcome = "deadline"
		}
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         outcome,
			"model":           modelID,
			"provider":        cred.Provider,
			"error":           err.Error(),
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, status, fmt.Sprintf("upstream request failed: %v", err))
		h.publishFinalFailureEvent(modelID, cred.Provider, startTime, 0, "transport_failure", nil)
		return
	}
	defer resp.Body.Close()

	// Step 13 — Response read bounded (BE-G2 mirror). The +1
	// byte triggers a separate over-cap branch.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "upstream_read_failed",
			"model":           modelID,
			"provider":        cred.Provider,
			"error":           err.Error(),
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": resp.StatusCode,
		})
		h.openAIError(w, http.StatusBadGateway, "upstream read failed")
		h.publishFinalFailureEvent(modelID, cred.Provider, startTime, resp.StatusCode, "upstream_read_failed", nil)
		return
	}
	if len(respBody) > maxResponseBodyBytes {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "response_too_large",
			"model":           modelID,
			"provider":        cred.Provider,
			"max_bytes":       maxResponseBodyBytes,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": resp.StatusCode,
		})
		h.openAIError(w, http.StatusBadGateway,
			fmt.Sprintf("upstream response exceeds %d MB cap", maxResponseBodyBytes>>20))
		h.publishFinalFailureEvent(modelID, cred.Provider, startTime, resp.StatusCode, "response_too_large", nil)
		return
	}

	// Step 14 — InspectResponse (unified 2-arg signature;
	// finding 5). The typed response is the relay payload on
	// success; the Outcome carries the relay decision.
	typedResp, outcome, err := provider.InspectResponse(respBody, &req)
	if err != nil {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "inspect_error",
			"model":           modelID,
			"provider":        cred.Provider,
			"error":           err.Error(),
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": resp.StatusCode,
		})
		h.openAIError(w, http.StatusBadGateway, "inspect error: "+err.Error())
		h.publishFinalFailureEvent(modelID, cred.Provider, startTime, resp.StatusCode, "inspect_error", respBody)
		return
	}

	// Step 15 — Relay write. Outcome.RelayStatus == 0 ⇒
	// PassAsIs; != 0 ⇒ PromoteToError (imggen-style; status +
	// body preserved verbatim — no envelope of our own).
	if outcome.RelayStatus == 0 {
		// PassAsIs — the typed response IS the relay. Encode
		// and write; the upstream HTTP status is the relay
		// status (typically 200, but PassAsIs covers upstream
		// HTTP ≥400 passthrough too — e.g. an upstream that
		// returns 200 with a typed DecisionResponse shape).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		if typedResp != nil {
			if err := json.NewEncoder(w).Encode(typedResp); err != nil {
				log.Printf("[decision] write to client failed: %v (model=%s, provider=%s)", err, modelID, cred.Provider)
			}
		} else {
			// 200 with no typed response — relay the raw
			// bytes verbatim (PassAsIs).
			if _, err := w.Write(respBody); err != nil {
				log.Printf("[decision] write to client failed: %v (model=%s, provider=%s)", err, modelID, cred.Provider)
			}
		}
	} else {
		// PromoteToError. The upstream body is preserved
		// verbatim — no envelope of our own on error-in-200
		// (imggen-style; the upstream's own error shape is
		// what clients pattern-match).
		if outcome.RelayStatus == http.StatusOK {
			// FUTURE-PROOFING TRIPWIRE (imggen
			// reviewer-deferred item): an adapter that
			// returns RelayStatus==200 on a non-success
			// path would silently relabel the upstream's
			// failure as success. Clamp to 502.
			log.Printf("[decision] adapter returned RelayStatus=200 on a non-success outcome (%s) — clamping to 502", outcome.Reason)
			w.WriteHeader(http.StatusBadGateway)
		} else {
			w.WriteHeader(outcome.RelayStatus)
		}
		if _, err := w.Write(respBody); err != nil {
			log.Printf("[decision] write to client failed: %v (model=%s, provider=%s)", err, modelID, cred.Provider)
		}
	}

	// Step 16 — Meter on success only (chat semantics, NOT
	// imggen's meterFailure). Detached 5s ctx (mirror imggen)
	// so client disconnect cannot cancel billing. Reads
	// input_tokens + output_tokens from the typed Usage.
	if outcome.RelayStatus == 0 {
		h.meterOnSuccess(authToken, modelID, provider, typedResp)
	}

	// Step 17 — Final decision event for telemetry. The event
	// is published AFTER the relay write so the duration covers
	// the entire round-trip including the client write.
	h.publishFinalOutcomeEvent(modelID, cred.Provider, startTime, resp.StatusCode, outcome, typedResp)
}

// ---------- Auth + low-level helpers ----------

func (h *Handler) authenticate(r *http.Request) (*auth.AuthToken, bool) {
	if h.tokenStore == nil {
		return nil, true
	}
	apiKey := extractAPIKey(r)
	if apiKey == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	token, err := h.tokenStore.ValidateToken(ctx, apiKey)
	if err != nil {
		return nil, false
	}
	return token, true
}

func extractAPIKey(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}
	if v := r.Header.Get("X-API-Key"); v != "" {
		return v
	}
	if v := r.Header.Get("x-api-key"); v != "" {
		return v
	}
	return ""
}

// ---------- Meter (success-only, detached) ----------

// meterOnSuccess writes a single usage row only when the
// upstream response is a successful relay. Mirrors the chat
// writer set (pkg/proxy/handler.go:899/915): Increment +
// IncrementModelUsage with input_tokens + output_tokens. The
// 5s ctx is detached (background) so client disconnect cannot
// cancel billing (review F1; precedent: pkg/imggen/handler.go:581).
//
// On failure paths (outcome.RelayStatus != 0) NOTHING is
// written — success-only semantics per amendment 5 / Q5.
// Failure telemetry flows via publishFinalFailureEvent.
func (h *Handler) meterOnSuccess(authToken *auth.AuthToken, modelID string, provider UpstreamProvider, resp *DecisionResponse) {
	if h.usage == nil {
		return
	}
	if resp == nil {
		return
	}
	usage := provider.ExtractUsage(resp)
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		// Defensive — zero-token usage is not the policy
		// (success-only meters success). Log + skip.
		log.Printf("[decision] skipping meter for zero-token usage (model=%s)", modelID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), meterTimeout)
	defer cancel()
	hourBucket := time.Now().UTC().Format("2006-01-02T15")
	var tokenID string
	if authToken != nil {
		tokenID = authToken.ID
	}
	promptTokens := int(usage.InputTokens)
	completionTokens := int(usage.OutputTokens)
	totalTokens := promptTokens + completionTokens

	if err := h.usage.Increment(ctx, tokenID, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
		log.Printf("[decision] Increment failed: %v (model=%s, token=%s)", err, modelID, tokenID)
	}
	if err := h.usage.IncrementModelUsage(ctx, modelID, hourBucket, 1, promptTokens, completionTokens, totalTokens); err != nil {
		log.Printf("[decision] IncrementModelUsage failed: %v (model=%s)", err, modelID)
	}
}

// ---------- Telemetry ----------

// publishEvent is a nil-safe wrapper around bus.Publish.
// Sibling of pkg/imggen/handler.go:614 — accepts a free-form
// map; the Event payload shape is the same.
func (h *Handler) publishEvent(eventType string, data map[string]interface{}) {
	if h.bus == nil {
		return
	}
	h.bus.Publish(events.Event{
		Type:      eventType,
		Timestamp: time.Now().Unix(),
		Data:      data,
	})
}

// publishFinalOutcomeEvent is the success-path telemetry —
// fires AFTER the relay write so the duration covers the
// full round-trip + client write.
func (h *Handler) publishFinalOutcomeEvent(modelID, providerName string, startTime time.Time, upstreamStatus int, outcome *Outcome, typedResp *DecisionResponse) {
	eventOutcome := "success"
	if outcome == nil || outcome.RelayStatus != 0 {
		eventOutcome = "error"
	}
	var usage Usage
	if typedResp != nil {
		usage = typedResp.Usage
	}
	h.publishEvent(eventTypeDecision, map[string]interface{}{
		"model":           modelID,
		"provider":        providerName,
		"input_tokens":    usage.InputTokens,
		"output_tokens":   usage.OutputTokens,
		"upstream_status": upstreamStatus,
		"duration_ms":     time.Since(startTime).Milliseconds(),
		"outcome":         eventOutcome,
	})
}

// publishFinalFailureEvent is the failure-path telemetry. The
// typed response is nil; we emit only the outcome / status
// fields.
func (h *Handler) publishFinalFailureEvent(modelID, providerName string, startTime time.Time, upstreamStatus int, outcome string, _ []byte) {
	h.publishEvent(eventTypeDecision, map[string]interface{}{
		"model":           modelID,
		"provider":        providerName,
		"upstream_status": upstreamStatus,
		"duration_ms":     time.Since(startTime).Milliseconds(),
		"outcome":         outcome,
	})
}

// ---------- OpenAI-envelope error helpers ----------

// openAIError writes a house OpenAI-envelope 4xx/5xx via
// models.NewOpenAIError (mirrors pkg/imggen/handler.go:629).
func (h *Handler) openAIError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeServerError, "", message))
}

// openAIErrorWithCode threads the supplied errorCode into the
// envelope (decision_misroute / decision_upstream_build_failed
// / similar typed codes).
func (h *Handler) openAIErrorWithCode(w http.ResponseWriter, code int, message, errorCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeServerError, errorCode, message))
}

func (h *Handler) sendAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeAuthenticationError, "", "Invalid or expired API key"))
}

func (h *Handler) sendModelNotAllowedError(w http.ResponseWriter, modelName string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"type":    "access_denied",
			"message": "model not allowed for this token",
			"model":   modelName,
		},
	})
}

// ---------- Resolvers + composition ----------

type modelResolver func(modelID string) (*models.ModelConfig, bool)
type credentialResolver func(modelID string) (models.ResolvedCredential, bool)

func (h *Handler) SetModelResolver(fn modelResolver) {
	if h == nil {
		return
	}
	h.resolveModel = fn
}

func (h *Handler) SetCredentialResolver(fn credentialResolver) {
	if h == nil {
		return
	}
	h.resolveCredential = fn
}

func (h *Handler) safeResolveModel(modelID string) (*models.ModelConfig, bool) {
	if h.resolveModel == nil {
		return nil, false
	}
	return h.resolveModel(modelID)
}

func (h *Handler) safeResolveCredential(modelID string) (models.ResolvedCredential, bool) {
	if h.resolveCredential == nil {
		return models.ResolvedCredential{}, false
	}
	return h.resolveCredential(modelID)
}

func NewModelResolverFromConfig(getModel func(string) *models.ModelConfig) modelResolver {
	return func(modelID string) (*models.ModelConfig, bool) {
		if getModel == nil {
			return nil, false
		}
		m := getModel(modelID)
		if m == nil {
			return nil, false
		}
		return m, true
	}
}

func NewCredentialResolverFromConfig(resolve func(string) (models.ResolvedCredential, bool)) credentialResolver {
	return func(modelID string) (models.ResolvedCredential, bool) {
		if resolve == nil {
			return models.ResolvedCredential{}, false
		}
		return resolve(modelID)
	}
}

// ---------- Header helpers (mirror imggen) ----------

func isHopByHopHeader(h string) bool {
	switch strings.ToLower(h) {
	case "host", "content-length", "transfer-encoding":
		return true
	}
	return false
}

func isClientAuthHeader(h string) bool {
	switch strings.ToLower(h) {
	case "authorization", "x-api-key":
		return true
	}
	return false
}
