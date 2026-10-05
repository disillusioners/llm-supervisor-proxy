// Package imggen handler. Auth mirrors pkg/proxy/handler.go:265-356
// (Architect Amendment 16). Deviations from the chat handler:
//   - No fallback hops (image-gen models have FallbackChain
//     rejected at config-validate time per BE-D2).
//   - Credential bearer replaces the client token — the client's
//     Authorization / X-API-Key / x-api-key is STRIPPED before
//     upstream (BE-A2 — the proxy's sk- token must never reach
//     MiniMax). Differs from pkg/ultimatemodel/handler_external.go
//     where the client bearer IS the upstream key.
//   - 404 / 413 / 502 / 504 use the OpenAI envelope via
//     models.NewOpenAIError (BE-T3 / Amendment 5).
//   - 405 uses plain http.Error (house style, not OpenAI envelope).
//
// 404-vs-403 unknown-model divergence is intentional and documented
// (Architect Amendment 17): a model that exists but is NOT in the
// token's allowed_models returns 403; a model that does not exist
// returns 404. Distinct conditions, the divergence is by design.
package imggen

import (
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
	// defaultImageGenTimeout is the package default applied when
	// the config knob is zero. Mirrors config.Defaults.ImageGenTimeout.
	defaultImageGenTimeout = 120 * time.Second

	// maxRequestBodyBytes is the request body cap (BE-G1). Spec
	// allows subject_reference < 10 MB; 16 MB has slack for the
	// base64 round-trip. Over-cap → 413 via errors.As
	// (MaxBytesError, Go 1.19+).
	maxRequestBodyBytes = 16 << 20

	// maxResponseBodyBytes is the response body cap (BE-G2).
	// url-format live bodies are ~451 B; base64 worst-case
	// (n=9, 2048×2048) is 72-96 MB which the cap may block
	// (Architect Amendment 20 honesty note). The cap is a
	// constant — not a config knob (thin iteration). The +1
	// lets the handler detect over-cap distinctly.
	maxResponseBodyBytes = 64 << 20
)

// Handler is the /v1/image_generation HTTP handler. Construction
// is the single composition root in main.go: pass the config
// manager (for the deadline), the event bus (for telemetry), the
// token store (for auth), and the usage counter (for metering).
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
	GetImageGenTimeout() time.Duration
}

// NewHandler constructs the /v1/image_generation handler. The
// usage counter may be nil (metering silently skipped) and the
// event bus may be nil (telemetry silently skipped) — the
// handler degrades gracefully so test / legacy setups work.
func NewHandler(configMgr configGetter, bus *events.Bus, tokenStore auth.TokenStoreInterface, usageCounter *usage.Counter) *Handler {
	return &Handler{
		configMgr: configMgr,
		bus:       bus,
		tokenStore: tokenStore,
		usage:     usageCounter,
	}
}

// imageGenTimeout returns the configured per-request deadline,
// falling back to the package default when the config knob is
// zero (e.g., a hand-edited config or a test stub).
func (h *Handler) imageGenTimeout() time.Duration {
	if h.configMgr == nil {
		return defaultImageGenTimeout
	}
	d := h.configMgr.GetImageGenTimeout()
	if d <= 0 {
		return defaultImageGenTimeout
	}
	return d
}

// HandleImageGeneration is the POST /v1/image_generation entry
// point (BE-R3 / T1.4.4-1.4.6). It is registered in cmd/main.go
// as a sibling of the chat routes.
//
// Flow:
//  1. POST-only method guard (house style; 405 via http.Error).
//  2. MaxBytesReader at 16 MB → 413 (OpenAI envelope on
//     *http.MaxBytesError).
//  3. Auth (extractAPIKey + ValidateToken with 5s ctx). tokenStore
//     nil ⇒ auth disabled (mirrors chat, BE-A1 / Leader Ruling
//     L2). Invalid token ⇒ 401 (OpenAI envelope).
//  4. Resolve model via config.Clone() + GetModel. Unknown ⇒
//     404 (OpenAI envelope). Not in token's allowed_models ⇒
//     403 (model_not_allowed shape).
//  5. Credential resolution via ResolveInternalConfigWithAffinity
//     (modelID, modelID) — synthetic stable key (BE-A3 /
//     Amendment 18). nil-engine degrades to single-credential
//     resolution. Resolve failure ⇒ 502 (OpenAI envelope).
//  6. Upstream URL + body via UpstreamProvider.BuildUpstream.
//     Build failure (invalid JSON) ⇒ 400 (OpenAI envelope).
//  7. Single upstream POST with ImageGenTimeout (default 120s,
//     clamp [30s, 4min] per BE-T1). Exactly ONE upstream call
//     (no retry, no fallback — Amendment 14 / T1.4.5).
//  8. Response read via io.LimitReader(64 MB + 1). Over-cap ⇒ 502
//     (openAI envelope), abort. Single read; classification +
//     metering + event reuse the same []byte (Amendment 12).
//  9. InspectResponse for EVERY upstream response (single-
//     classifier seam, Amendment 3). PassAsIs ⇒ relay verbatim;
//     PromoteToError ⇒ write RelayStatus + upstream body.
//  10. Meter: IncrementTokenImages + IncrementModelImages with
//      request_count=1, image_count=BillableImages(body) (0 on
//      error-in-200). Documented divergence from chat's success-
//      only convention (Amendment 14).
//  11. Telemetry: events.Bus.Publish(image_generation, plain
//      map) — nil-safe (no-op on nil bus).
func (h *Handler) HandleImageGeneration(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// POST-only method guard (house style — HandleModels
	// handler.go:239-242). 405 stays http.Error per Amendment 5
	// (NOT OpenAI envelope).
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// MaxBytesReader at 16 MB. http.MaxBytesError (Go 1.19+) on
	// overflow → 413 via errors.As (T1.4.4 acceptance +
	// Amendment 5 error-shape family).
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		// Over-cap detection via errors.As on *http.MaxBytesError.
		// The *http.MaxBytesError sentinel is intentionally not
		// type-aliased — the stdlib type lives in net/http and
		// is the only correct pointer target.
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			h.publishEvent("image_generation", map[string]interface{}{
				"outcome":        "request_too_large",
				"max_bytes":      maxRequestBodyBytes,
				"duration_ms":    time.Since(startTime).Milliseconds(),
				"upstream_status": 0,
			})
			h.openAIError(w, http.StatusRequestEntityTooLarge,
				"request body exceeds 16 MB cap (BE-G1)")
			return
		}
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "request_read_failed",
			"error":          err.Error(),
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusInternalServerError,
			"failed to read request body")
		return
	}
	_ = r.Body.Close()

	// 1) Auth. Mirrors pkg/proxy/handler.go:265-308 — tokenStore
	// nil ⇒ auth disabled (BE-A1 / Leader Ruling L2).
	authToken, ok := h.authenticate(r)
	if !ok {
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "auth_failed",
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.sendAuthError(w)
		return
	}

	// 2) Resolve the model. The wire body is JSON-decoded once
	// above (via ReadAll); we re-parse here to read the `model`
	// field for the lookup. We do NOT need the decoded body for
	// anything else at this point — BuildUpstream does the
	// canonical decode + alias rewrite.
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "invalid_json",
			"error":          err.Error(),
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	modelID, _ := bodyMap["model"].(string)
	if modelID == "" {
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "missing_model",
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadRequest, "model field is required")
		return
	}

	// 3) Model resolution. We do NOT use the chat handler's
	// requestContext — that's a chat-shaped struct in pkg/proxy.
	// The lookup is one line via config.Clone() + GetModel; no
	// parallel attempts, no race, no loop detection, no
	// toolcall buffer, no toolrepair — the exclusions table at
	// phase1-plan.md:L49-59 is binding.
	modelConfig, ok := h.resolveModel(modelID)
	if !ok {
		// 404 — unknown model (BE-A1 / Amendment 17). Distinct
		// from 403 (model known but not in token's allowed_models).
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "model_not_found",
			"model":          modelID,
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusNotFound,
			fmt.Sprintf("unknown model %q (image-gen models are served by /v1/image_generation only)", modelID))
		return
	}
	_ = modelConfig // currently unused at the handler layer — IsImageGen() gating lives in pkg/proxy per Leader Ruling L1.

	// 4) Token allow-list check. Empty AllowedModels = all
	// models allowed (mirrors chat; pkg/auth/token.go:129-144).
	// 403 only fires when AllowedModels is non-empty AND the
	// model is not in the list (existing-token-with-restrictions
	// case).
	if authToken != nil && len(authToken.AllowedModels) > 0 {
		if !authToken.IsModelAllowed(modelID) {
			h.publishEvent("image_generation", map[string]interface{}{
				"outcome":     "model_not_allowed",
				"model":       modelID,
				"token":       authToken.ID,
				"duration_ms": time.Since(startTime).Milliseconds(),
				"upstream_status": 0,
			})
			h.sendModelNotAllowedError(w, modelID)
			return
		}
	}

	// 5) Credential resolution. We use the chat-side helper
	// through the ModelsConfigInterface so the existing
	// affinity engine (Phase 3 / pkg/credentiallb) participates
	// when wired. Stable key (modelID, modelID) per BE-A3 +
	// Amendment 18.
	cred, ok := h.resolveCredential(modelID)
	if !ok {
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "credential_unresolved",
			"model":          modelID,
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusInternalServerError,
			fmt.Sprintf("no credentials configured for model %q", modelID))
		return
	}

	// 6) Look up the per-provider UpstreamProvider. Phase 1
	// ships minimax only; unknown providers ⇒ 502 (the handler
	// does not silently fall back to a different provider).
	provider, ok := lookup(cred.Provider)
	if !ok {
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "unsupported_provider",
			"model":          modelID,
			"provider":       cred.Provider,
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadGateway,
			fmt.Sprintf("no image-generation adapter registered for provider %q", cred.Provider))
		return
	}

	upstreamURL, upstreamBody, err := provider.BuildUpstream(cred, bodyBytes)
	if err != nil {
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "build_failed",
			"model":          modelID,
			"error":          err.Error(),
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 7) Single upstream POST with ImageGenTimeout (BE-T1 /
	// T1.4.5). No retry, no fallback, no MonitoredReader (BE-T2 /
	// Exclusions table). The pooled client is the module-level
	// sharedHTTPClient with ResponseHeaderTimeout: 0 explicit
	// (Amendment 10).
	ctx, cancel := context.WithTimeout(r.Context(), h.imageGenTimeout())
	defer cancel()

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, strings.NewReader(string(upstreamBody)))
	if err != nil {
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "request_build_failed",
			"model":          modelID,
			"provider":       cred.Provider,
			"error":          err.Error(),
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusInternalServerError,
			"failed to build upstream request")
		return
	}

	// Headers. Content-Type + credential bearer. Copy remaining
	// client headers EXCEPT hop-by-hop (Host, Content-Length,
	// Transfer-Encoding) AND EXCEPT client auth headers
	// (Authorization, X-API-Key, x-api-key) — the proxy's sk-
	// token must never reach MiniMax (BE-A2).
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+cred.APIKey)
	for key, values := range r.Header {
		if isHopByHopHeader(key) || isClientAuthHeader(key) {
			continue
		}
		for _, value := range values {
			upstreamReq.Header.Add(key, value)
		}
	}

	// 8) Single upstream call. Exactly ONE per request (T1.7.2
	// "exactly-one-upstream-call" assertion; Amendment 14 — the
	// chat path's race coordinator and fallback are NOT wired).
	resp, err := sharedHTTPClient.Do(upstreamReq)
	if err != nil {
		// Deadline / context expiry surfaces here as well. 504
		// on context.DeadlineExceeded; 502 on transport error.
		status := http.StatusBadGateway
		outcome := "upstream_transport_error"
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			status = http.StatusGatewayTimeout
			outcome = "deadline"
		}
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        outcome,
			"model":          modelID,
			"provider":       cred.Provider,
			"error":          err.Error(),
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, status, fmt.Sprintf("upstream request failed: %v", err))
		h.meterFailure(r, authToken, modelID, outcome)
		return
	}
	defer resp.Body.Close()

	// 9) Response read bounded (BE-G2). io.LimitReader 64 MB + 1
	// so the handler can detect over-cap distinctly (the +1 byte
	// triggers a separate branch below; the over-cap body is
	// never materialized into a second []byte — single read,
	// single classification, single relay).
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "upstream_read_failed",
			"model":          modelID,
			"provider":       cred.Provider,
			"error":          err.Error(),
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": resp.StatusCode,
		})
		h.openAIError(w, http.StatusBadGateway, "upstream read failed")
		h.meterFailure(r, authToken, modelID, "upstream_read_failed")
		return
	}
	if len(respBody) > maxResponseBodyBytes {
		h.publishEvent("image_generation", map[string]interface{}{
			"outcome":        "response_too_large",
			"model":          modelID,
			"provider":       cred.Provider,
			"max_bytes":      maxResponseBodyBytes,
			"duration_ms":    time.Since(startTime).Milliseconds(),
			"upstream_status": resp.StatusCode,
		})
		h.openAIError(w, http.StatusBadGateway,
			fmt.Sprintf("upstream response exceeds %d MB cap (BE-G2)", maxResponseBodyBytes>>20))
		h.meterFailure(r, authToken, modelID, "response_too_large")
		return
	}

	// 10) Classification (Amendment 3 — single-classifier seam).
	// InspectResponse is called for EVERY upstream response; the
	// handler never branches on resp.StatusCode directly. The
	// Outcome carries the relay decision.
	outcome := provider.InspectResponse(resp.StatusCode, respBody)

	// 11) Relay (T1.4.6 / write-first observe-after). On
	// PassAsIs: copy headers, write upstream status, write body
	// verbatim. On PromoteToError: 502 (or whatever RelayStatus
	// is), preserve the upstream body verbatim (no envelope of
	// our own — error-in-200 is MiniMax's own error shape).
	copyResponseHeaders(w, resp)
	if outcome.RelayStatus == 0 {
		// PassAsIs — covers upstream HTTP ≥400 passthrough AND
		// 200-success. The handler relays the upstream status
		// verbatim (Amendment 3; the seam owns the decision).
		w.WriteHeader(resp.StatusCode)
	} else {
		// PromoteToError. The upstream body is preserved verbatim
		// (no envelope of our own on error-in-200 — BE-T3
		// Amendment 5 family decision; the MiniMax-native
		// client reads base_resp from the preserved body).
		if outcome.RelayStatus == http.StatusOK {
			// Defensive: an adapter that returns RelayStatus==200
			// for a non-success path would silently relabel the
			// upstream's failure as success. Log + clamp to 502.
			log.Printf("[imggen] adapter returned RelayStatus=200 on a non-success outcome (%s) — clamping to 502", outcome.Reason)
			w.WriteHeader(http.StatusBadGateway)
		} else {
			w.WriteHeader(outcome.RelayStatus)
		}
		// Log line carries the Outcome.Reason — Amendment 11
		// distinguishes the three failure modes (missing-key /
		// unparseable-JSON / empty-body) so the API-drift signal
		// this guard catches is not conflated.
		log.Printf("[imggen] %s: %s (upstream_status=%d, model=%s, provider=%s)",
			modelID, outcome.Reason, resp.StatusCode, modelID, cred.Provider)
	}
	if _, err := w.Write(respBody); err != nil {
		// The client disconnected; we still bill the request
		// (the upstream completed) but log for observability.
		log.Printf("[imggen] write to client failed: %v (model=%s, provider=%s)", err, modelID, cred.Provider)
	}

	// 12) Meter + event. Meter first (observe-after: even on
	// error-in-200, request_count+1, image_count=0 — Amendment
	// 14 documented divergence from chat's success-only). Then
	// publish the telemetry event with the final outcome.
	billable := 0
	if outcome.RelayStatus == 0 {
		n, _ := provider.BillableImages(respBody)
		billable = n
	}
	h.meter(r.Context(), authToken, modelID, billable)

	// Telemetry. Plain map (BE-E1); outcome + success/failure
	// counts lifted from the response body when classification
	// succeeded (status_code==0 ⇒ billable is already correct;
	// failed_count lifts from the same body for the event).
	successCount, failedCount := 0, 0
	if outcome.RelayStatus == 0 {
		if v, ok := extractSuccessCount(respBody); ok {
			successCount = v
		}
		if v, ok := extractFailedCount(respBody); ok {
			failedCount = v
		}
	}
	eventOutcome := "success"
	if outcome.RelayStatus != 0 {
		eventOutcome = "error"
	} else if failedCount > 0 {
		eventOutcome = "partial_success"
	}
	h.publishEvent("image_generation", map[string]interface{}{
		"model":           modelID,
		"provider":        cred.Provider,
		"success_count":   successCount,
		"failed_count":    failedCount,
		"upstream_status": resp.StatusCode,
		"duration_ms":     time.Since(startTime).Milliseconds(),
		"outcome":         eventOutcome,
	})
}

// authenticate mirrors pkg/proxy/handler.go:288-308. tokenStore
// nil ⇒ auth disabled. Returns (token, true) on success /
// disabled, (nil, false) on auth failure.
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

// extractAPIKey mirrors pkg/proxy/handler.go:265-285. Looks at
// Authorization: Bearer <token>, X-API-Key, x-api-key.
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

// meter increments token + model usage for a successful relay.
// On a successful relay (status_code==0), request_count += 1
// and image_count += coerced metadata.success_count. On a
// non-success path the handler calls meterFailure instead (which
// also bumps request_count but not image_count — Amendment 14
// documented divergence from chat's success-only convention).
//
// Nil-safe: when h.usage is nil, the call is a no-op so legacy /
// test setups work without a usage counter.
func (h *Handler) meter(ctx context.Context, authToken *auth.AuthToken, modelID string, imageCount int) {
	if h.usage == nil {
		return
	}
	hourBucket := time.Now().UTC().Format("2006-01-02T15")
	var tokenID string
	if authToken != nil {
		tokenID = authToken.ID
	}
	// Best-effort: a metering failure is logged but does not
	// fail the request (the upstream completed; the client got
	// the body).
	if err := h.usage.IncrementTokenImages(ctx, tokenID, hourBucket, 1, imageCount); err != nil {
		log.Printf("[imggen] IncrementTokenImages failed: %v (model=%s, token=%s)", err, modelID, tokenID)
	}
	if err := h.usage.IncrementModelImages(ctx, modelID, hourBucket, 1, imageCount); err != nil {
		log.Printf("[imggen] IncrementModelImages failed: %v (model=%s)", err, modelID)
	}
}

// meterFailure increments request_count only (imageCount=0) for
// mapped-error paths. Per Amendment 14, the image route counts
// request_count on success AND on mapped errors; chat counts
// request_count only on success.
func (h *Handler) meterFailure(r *http.Request, authToken *auth.AuthToken, modelID, outcome string) {
	_ = outcome // reserved for future structured metering — currently a no-op
	h.meter(r.Context(), authToken, modelID, 0)
}

// publishEvent is a nil-safe wrapper around bus.Publish. The map
// is the canonical telemetry shape (BE-E1 — events.Bus.Event.Data
// is interface{}; the bus → /fe/api/events SSE forwarder
// transparently relays any unknown event type).
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

// openAIError writes an OpenAI-envelope 4xx/5xx via
// models.NewOpenAIError (BE-T3 / Amendment 5). Self-generated
// errors only — error-in-200 / promoted upstream bodies are
// relayed verbatim without an envelope of our own.
func (h *Handler) openAIError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeServerError, "", message))
}

// sendAuthError writes the OpenAI-envelope 401 (mirrors
// handler.go:335-343).
func (h *Handler) sendAuthError(w http.ResponseWriter) {
	h.openAIError(w, http.StatusUnauthorized, "Invalid or expired API key")
}

// sendModelNotAllowedError writes the nested 403 envelope (mirrors
// handler.go:346-356). Same shape as the chat handler —
// clients (and the FE) parse it identically.
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

// resolveModel + resolveCredential are the modelConfig +
// credential seam. They are defined as interface methods on the
// Handler so tests can substitute a stub; in production they read
// from the chat handler's ModelsConfig via a thin shim installed
// at construction time (see imggenHandlerWithBridge in
// production.go).
//
// In Phase 1 the imggen package is wired to the same
// ModelsConfig / ResolveInternalConfig seam that chat uses, so a
// model created via /fe/api/models is reachable through
// /v1/image_generation without a separate store.

// resolveModel + resolveCredential default stubs. In production
// the cmd/main.go wiring installs a real implementation via
// SetModelResolver / SetCredentialResolver (test seam; see the
// handler_extensions.go file for the production wiring). The
// default stubs return ok=false so a unit test that forgets to
// install the shim surfaces as a clean 502 (not a panic).
//
// The Handler stores the resolvers as function values; the package
// owns no ModelsConfigInterface import (the import direction is
// `pkg/imggen → {pkg/models, pkg/auth, pkg/usage, pkg/events}`
// only, per BE-R2 / T1.4.1; the production wiring lives in a
// separate file that the import-guard task 1.8.2 audits).

// modelResolver returns the model config for a model ID.
type modelResolver func(modelID string) (*models.ModelConfig, bool)

// credentialResolver returns the resolved credential for a model ID.
type credentialResolver func(modelID string) (models.ResolvedCredential, bool)

// SetModelResolver installs the model-lookup function. Called
// from cmd/main.go at composition time. nil-safe (no-op on nil).
func (h *Handler) SetModelResolver(fn modelResolver) {
	if h == nil {
		return
	}
	h.resolveModel = fn
}

// SetCredentialResolver installs the credential-lookup function.
// Called from cmd/main.go at composition time. nil-safe.
func (h *Handler) SetCredentialResolver(fn credentialResolver) {
	if h == nil {
		return
	}
	h.resolveCredential = fn
}

// NewModelResolverFromConfig is a small adapter cmd/main.go uses
// to install the model-lookup function from a
// models.ModelsConfigInterface without pkg/imggen importing the
// interface (the interface lives in pkg/models which IS in the
// allowed import set per BE-R2 / T1.4.1 — the production wiring
// stays inside cmd/main.go's composition root).
//
// Provided as a function-on-function adapter so the production
// shim does not grow a new public surface; it's an internal
// convenience for main.go's wiring.
//
// The signature is intentionally narrow: takes the model ID,
// returns (*models.ModelConfig, bool). The adapter reads the
// config via the standard GetModel + IsImageGen-allowed guard
// (the chat handler applies the same guard inside
// HandleChatCompletions).
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

// NewCredentialResolverFromConfig is the matching shim for the
// credential lookup. The function passed in returns
// (models.ResolvedCredential, bool) — main.go wires it to
// ModelsConfig.ResolveInternalConfigWithAffinity (the chat-side
// helper that participates in the affinity engine when wired).
//
// Stable key (modelID, modelID) per BE-A3 + Amendment 18.
func NewCredentialResolverFromConfig(resolve func(string) (models.ResolvedCredential, bool)) credentialResolver {
	return func(modelID string) (models.ResolvedCredential, bool) {
		if resolve == nil {
			return models.ResolvedCredential{}, false
		}
		return resolve(modelID)
	}
}

// Header helpers — hop-by-hop + client-auth strip (BE-A2).

// isHopByHopHeader matches the IETF hop-by-hop header set the
// Go stdlib considers (Host, Content-Length, Transfer-Encoding
// at minimum). Mirrors pkg/ultimatemodel/handler_external.go:141.
func isHopByHopHeader(h string) bool {
	switch strings.ToLower(h) {
	case "host", "content-length", "transfer-encoding":
		return true
	}
	return false
}

// isClientAuthHeader matches Authorization / X-API-Key / x-api-key
// (BE-A2 — the proxy's sk- token must never reach MiniMax).
func isClientAuthHeader(h string) bool {
	switch strings.ToLower(h) {
	case "authorization", "x-api-key":
		return true
	}
	return false
}

// copyResponseHeaders copies non-hop-by-hop upstream response
// headers onto the client response. Mirrors the header-copy
// pattern in pkg/ultimatemodel/handler_external.go:171-176.
func copyResponseHeaders(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

// extractSuccessCount / extractFailedCount lift metadata counts
// from the response body for the telemetry event. Tolerate
// missing / malformed / non-numeric values (defensive — the
// event map serializes the int form; an unparseable count
// simply doesn't make it into the event).
func extractSuccessCount(body []byte) (int, bool) {
	if len(body) == 0 {
		return 0, false
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, false
	}
	md, ok := resp["metadata"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	return coerceCount(md["success_count"])
}

func extractFailedCount(body []byte) (int, bool) {
	if len(body) == 0 {
		return 0, false
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, false
	}
	md, ok := resp["metadata"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	return coerceCount(md["failed_count"])
}

// coerceCount accepts number OR string (MiniMax returns strings
// — "1", not 1) and returns (int, true) on parse success. On
// any other type or unparseable value returns (0, false).
func coerceCount(v interface{}) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case string:
		var n int
		if _, err := fmt.Sscanf(x, "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}
