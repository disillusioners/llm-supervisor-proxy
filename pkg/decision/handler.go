// Package decision handler. Skeleton modeled on pkg/imggen/handler.go
// (~80% copy-and-rename of the handler shell; Decision Models
// commission / §3.2 / §3.2.a: P1 owns ONLY the handler shell —
// the typed wire parser (Step 4 parseBody), provider adapters
// (Steps 10 BuildUpstream + 14 InspectResponse), gotcha
// validators (Step 5), metering (Step 16), and event publish
// (Step 17) all land in P2).
//
// Deviations from the imggen template:
//   - Misroute guard is the REVERSE direction (imggen is the
//     "kind tagged at write" guard on /v1/image_generation — here
//     we're inside /v1/decisions and reject chat/image-gen
//     models in resolveModel with 400 + code "decision_misroute",
//     a house OpenAI envelope). Closes the imggen reverse-
//     direction gap that imggen itself never closed (§3.4 row 3).
//   - The shared registry is EMPTY at P1 — provider lookup
//     returns ok=false by design; the route is live but the
//     handler returns 502 until P2 registers openai/typesafe.
//     This is the documented P1 exit criterion (§10).
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
// (Decision Models commission / §3.2 / §10 P1). It is registered
// in cmd/main.go as a sibling of the chat routes. POST-only is
// enforced in-handler (house style; no Go 1.22 mux method patterns
// anywhere else in the codebase per cmd/main.go:286-290).
//
// P1 flow (steps 1-3, 6-9, 11-12 of §3.2 lifecycle — steps 4
// parseBody, 5 validators, 10 BuildUpstream, 13-14 inspect, 15
// relay, 16 meter, 17 event are P2 landers):
//
//  1. POST-only method guard (house style; 405 via http.Error).
//  2. MaxBytesReader at 16 MB → 413 (house OpenAI envelope on
//     *http.MaxBytesError).
//  3. Auth (extractAPIKey + ValidateToken with 5s ctx). tokenStore
//     nil ⇒ auth disabled (mirrors chat + imggen). Invalid token
//     ⇒ 401 (house OpenAI envelope).
//  6. Resolve model via the resolver fn; unknown ⇒ 404 (house
//     envelope). EXPLICIT IsDecision() check — chat / image-gen
//     models posted here return 400 + code "decision_misroute"
//     (closes imggen reverse-direction gap per §3.4 row 3).
//  7. Token allow-list (403 only when AllowedModels is non-empty
//     AND the model is not in the list; mirrors imggen).
//  8. Credential resolution via the resolver fn (stable
//     affinity key (modelID, modelID) — main.go wires this to
//     modelsConfig.ResolveInternalConfigWithAffinity). nil ⇒ 502.
//  9. Provider lookup on the local registry. P1: registry is
//     empty by design ⇒ 502 (documented P1 exit criterion). P2
//     registers openai + typesafe.
//  11. Timeout ctx (DecisionTimeout / default 30s / clamp
//      [10s, 60s]).
//  12. (At P1 the body is NOT relayed to an upstream — the
//      registry lookup already returned 502 at step 9.)
//
// The P1 body parse is minimal — we use a single-pass
// io.ReadAll on the request body and decode-once into a generic
// map to read the `model` field for resolution. The full typed
// parse into DecisionRequest (with body reuse for relay) is P2.
func (h *Handler) HandleDecision(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Step 1 — POST-only method guard.
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Step 2 — MaxBytesReader at 16 MB → 413 on *http.MaxBytesError.
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	bodyBytes, err := io.ReadAll(r.Body)
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

	// P1 body parse is minimal: read the `model` field for the
	// resolver lookup. P2 lands the typed parse into
	// DecisionRequest and reuses the same body bytes for relay.
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &bodyMap); err != nil {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "invalid_json",
			"error":           err.Error(),
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	modelID, _ := bodyMap["model"].(string)
	if modelID == "" {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "missing_model",
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadRequest, "model field is required")
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

	// Step 9 — Provider lookup. P1: local registry is empty by
	// design — the handler returns 502 here at the documented
	// P1 exit criterion. P2 registers openai + typesafe
	// adapters (and any future adapter slots into the registry
	// without touching the handler).
	_, ok = lookup(cred.Provider)
	if !ok {
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "unsupported_provider",
			"model":           modelID,
			"provider":        cred.Provider,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadGateway,
			fmt.Sprintf("no decision adapter registered for provider %q (P1 ships with an empty registry by design; P2 registers openai + typesafe)", cred.Provider))
		return
	}

	// Step 11 — timeout ctx (plumbing only at P1; the upstream
	// call lands in P2 alongside the BuildUpstream + InspectResponse
	// adapters).
	_, cancel := context.WithTimeout(r.Context(), h.decisionTimeout())
	defer cancel()

	// P1 exits here. Steps 12-17 (sharedHTTPClient.Do, response
	// cap, InspectResponse, relay, success-only meter, event
	// publish) all land in P2 once the openai-decision +
	// typesafe adapters are registered at init() time.

	// Defensive: keep a bodyBytes reference so go vet / deadcode
	// does not strip the Step 2 read; P2 reuses these bytes for
	// the openai-decision byte-faithful pass-through (Q18).
	_ = bodyBytes
}

// authenticate mirrors pkg/proxy/handler.go and pkg/imggen/handler.go.
// tokenStore nil ⇒ auth disabled. Returns (token, true) on success /
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

// extractAPIKey mirrors pkg/proxy/handler.go:265-285 — Authorization
// Bearer, X-API-Key, x-api-key.
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

// publishEvent is a nil-safe wrapper around bus.Publish. Mirrors
// pkg/imggen/handler.go:614.
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

// openAIError writes a house OpenAI-envelope 4xx/5xx via
// models.NewOpenAIError. Mirrors imggen/handler.go:629 verbatim
// when no errorCode is needed — the envelope carries
// ErrorTypeServerError + empty code.
//
// For decision_misroute-style cases where the error code MUST
// be in the envelope (e.g., the chat-side guard at site 5 +
// resolver kind-misroute at /v1/decisions), use
// openAIErrorWithCode which threads the code through the
// envelope exactly.
func (h *Handler) openAIError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeServerError, "", message))
}

// openAIErrorWithCode is the decision-aware error variant.
// Writes OpenAI envelope with ErrorTypeServerError as the
// type and the supplied errorCode as the code field. Used by
// the chat-side guard (site 5) and the /v1/decisions
// resolver IsDecision() guard (site 7 + the
// reverse-direction closure) so consumers can pattern-match
// the misroute class.
func (h *Handler) openAIErrorWithCode(w http.ResponseWriter, code int, message, errorCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeServerError, errorCode, message))
}

// sendAuthError writes the house OpenAI-envelope 401. Matches
// pkg/imggen/handler.go:642 (authentication_error envelope
// family — FE + other clients parse 4xx envelopes identically
// across /v1/chat/completions, /v1/image_generation, and
// /v1/decisions).
func (h *Handler) sendAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeAuthenticationError, "", "Invalid or expired API key"))
}

// sendModelNotAllowedError writes the nested 403 envelope. Mirrors
// pkg/imggen/handler.go:652 — same shape as the chat handler.
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
// credential seam. Stored as struct fields on the Handler so
// tests can substitute a stub; in production cmd/main.go wires
// them to the same modelsConfig + ResolveInternalConfigWithAffinity
// seam the chat path uses (single source of truth). The package
// owns no ModelsConfigInterface import — the production wiring
// lives in cmd/main.go, and the import guard audits this.
//
// Default stubs return ok=false so a unit test that forgets to
// install the shim surfaces as a clean 502 / 404 (not a panic).
// The empty stubs match imggen's posture (handler.go:664-697).

// modelResolver returns the model config for a model ID.
type modelResolver func(modelID string) (*models.ModelConfig, bool)

// credentialResolver returns the resolved credential for a model ID.
type credentialResolver func(modelID string) (models.ResolvedCredential, bool)

// SetModelResolver installs the model-lookup function. nil-safe.
func (h *Handler) SetModelResolver(fn modelResolver) {
	if h == nil {
		return
	}
	h.resolveModel = fn
}

// SetCredentialResolver installs the credential-lookup function.
// nil-safe.
func (h *Handler) SetCredentialResolver(fn credentialResolver) {
	if h == nil {
		return
	}
	h.resolveCredential = fn
}

// safeResolveModel is a nil-safe wrapper around h.resolveModel:
// if no resolver is installed, returns (nil, false). Defends
// against nil-pointer panics in unit tests that forget to wire
// the resolver seam — surfaces as 404 (mirrors imggen posture at
// handler.go:664-697: "default stubs return ok=false so a unit
// test that forgets to install the shim surfaces as a clean 502
// (not a panic)").
func (h *Handler) safeResolveModel(modelID string) (*models.ModelConfig, bool) {
	if h.resolveModel == nil {
		return nil, false
	}
	return h.resolveModel(modelID)
}

// safeResolveCredential mirrors safeResolveModel for the
// credential lookup.
func (h *Handler) safeResolveCredential(modelID string) (models.ResolvedCredential, bool) {
	if h.resolveCredential == nil {
		return models.ResolvedCredential{}, false
	}
	return h.resolveCredential(modelID)
}

// NewModelResolverFromConfig is the small adapter cmd/main.go
// uses to install the model-lookup function from a
// models.ModelsConfigInterface without pkg/decision importing
// the interface.
//
// Mirrors imggen/handler.go:715 verbatim. The signature is
// narrow: model ID in, (*models.ModelConfig, bool) out.
// Production wiring stays inside cmd/main.go's composition
// root.
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
// credential lookup. Mirrors imggen/handler.go:735.
//
// Stable key (modelID, modelID) per BE-A3 + amendment 18.
func NewCredentialResolverFromConfig(resolve func(string) (models.ResolvedCredential, bool)) credentialResolver {
	return func(modelID string) (models.ResolvedCredential, bool) {
		if resolve == nil {
			return models.ResolvedCredential{}, false
		}
		return resolve(modelID)
	}
}

// Reserved for P2 — the byte-faithful openai-decision pass-through
// reuses the captured request bytes after BuildUpstream. The
// import block currently pulls in `bytes` for the future
// `bytes.NewReader(upstreamBody)` site; go vet / doc lint catches
// the unused-import noise once P2 lands — we keep it now so the
// package shape matches the imggen template exactly (less
// churn in P2).
var _ = bytes.NewReader
