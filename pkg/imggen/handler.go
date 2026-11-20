// Package imggen handler. Auth mirrors pkg/proxy/handler.go:265-356
// (Architect Amendment 16). Deviations from the chat handler:
//   - No fallback hops (image-gen models have FallbackChain
//     rejected at config-validate time per BE-D2).
//   - Credential bearer replaces the client token — the client's
//     Authorization / X-API-Key / x-api-key is STRIPPED before
//     upstream (BE-A2 — the proxy's sk- token must never reach
//     MiniMax).
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
	"github.com/disillusioners/llm-supervisor-proxy/pkg/authz"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// Constants
const (
	// defaultImageGenTimeout is the package default applied when
	// the config knob is zero. Mirrors config.Defaults.ImageGenTimeout.
	defaultImageGenTimeout = 120 * time.Second

	// defaultImageGenGroupWalkTimeout is the package default applied
	// when the config knob is zero. Mirrors
	// config.Defaults.ImageGenGroupWalkTimeout (= 2 × the per-member
	// default — imggen-groups commission / F-1 / D-17).
	defaultImageGenGroupWalkTimeout = 240 * time.Second

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

	// eventTypeImageGeneration is the events.Bus type for every
	// telemetry payload the handler publishes (BE-E1). Centralised
	// so the 15 publish sites stay in lockstep with the
	// /fe/api/events SSE forwarder contract.
	eventTypeImageGeneration = "image_generation"
)

// maxResponseBodyBytesForTest lets the over-cap unit test use a
// smaller threshold so the test doesn't have to allocate
// 64MB+1 bytes (the production constant is preserved above for
// the live path). 0 means "use the production constant". This
// is package-private; only the imggen test suite can override
// it.
var maxResponseBodyBytesForTest int64

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

	// modelsConfig is the OPTIONAL seam to the models manager. It
	// answers the group-membership question the scope resolution needs
	// (a token that grants a GROUP must reach that group's current
	// members).
	//
	// It mirrors the decision lane's seam (pkg/decision's
	// SetModelsConfig) and exists for the same reason: the membership
	// must be read from the IN-MEMORY models snapshot (the
	// modelscache decorator's non-copying accessor), NEVER from a live
	// DB read — the raw manager's GetModelNative is a `SELECT … FROM
	// models` issued UNDER m.mu.RLock, which stalls every reader
	// behind the writer-preferring RWMutex (Ruling 5).
	//
	// It is optional by design: a handler built without it keeps the
	// pre-group behaviour (no membership expansion — the model's own
	// axis decides).
	modelsConfig models.ModelsConfigInterface
}

// configGetter is the slice of config.ManagerInterface this
// handler actually uses. Defined locally so tests can substitute
// a stub without depending on the full config surface.
//
// imggen-groups commission / F-1 / D-17: the GROUP walk budget
// getter joins the per-member deadline getter here. BOTH are HOT
// (per-request reads — PUT /fe/api/config applies them without a
// restart); neither is boot-owned.
type configGetter interface {
	GetImageGenTimeout() time.Duration
	GetImageGenGroupWalkTimeout() time.Duration
}

// groupLaneSeam is the OPTIONAL lane-query seam (the imggen-groups
// commission). It lives OUTSIDE models.ModelsConfigInterface on
// purpose: the optional-seam shape keeps the structural test fakes
// and the JSON backend valid (the house pattern — the precedent:
// ClearGroupMemberCooldown, groupLaneReader).
type groupLaneSeam interface {
	GroupLane(groupID string) (models.GroupLane, bool)
}

// The compile-time proof that the PRODUCTION models binding satisfies
// the lane seam: the production conf.ModelsConfig is
// *modelscache.CachedModelsConfig (CACHE_LAYER_ENABLED defaults ON),
// so a REMOVED decorator forward must break the BUILD, not silently
// no-op in production while the raw-manager tests stay green (the
// modelscache-decorator-forward-trap, exact shape).
var _ groupLaneSeam = (*modelscache.CachedModelsConfig)(nil)

// NewHandler constructs the /v1/image_generation handler. The
// usage counter may be nil (metering silently skipped) and the
// event bus may be nil (telemetry silently skipped) — the
// handler degrades gracefully so test / legacy setups work.
func NewHandler(configMgr configGetter, bus *events.Bus, tokenStore auth.TokenStoreInterface, usageCounter *usage.Counter) *Handler {
	return &Handler{
		configMgr:  configMgr,
		bus:        bus,
		tokenStore: tokenStore,
		usage:      usageCounter,
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

// imageGenGroupWalkTimeout returns the TOTAL walk budget of an
// image-gen GROUP request (imggen-groups commission / F-1 / D-17),
// falling back to the package default when the config knob is zero
// (e.g., a hand-edited config or a test stub).
//
// HOT: the read goes through the config manager's per-request getter,
// so PUT /fe/api/config applies a new budget without a restart.
func (h *Handler) imageGenGroupWalkTimeout() time.Duration {
	if h.configMgr == nil {
		return defaultImageGenGroupWalkTimeout
	}
	d := h.configMgr.GetImageGenGroupWalkTimeout()
	if d <= 0 {
		return defaultImageGenGroupWalkTimeout
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
//     6-9. THE ATTEMPT (the shared attempt mechanics —
//     attemptImageGenMember; imggen-groups commission / T1.6 — the
//     standalone path AND the group walk call the SAME function):
//     upstream URL + body via UpstreamProvider.BuildUpstream (build
//     failure ⇒ 400); a single upstream POST under the caller's
//     deadline (ImageGenTimeout — default 120s, clamp [30s, 4min] per
//     BE-T1; exactly ONE upstream call, no retry, no fallback —
//     Amendment 14 / T1.4.5); the response read via
//     io.LimitReader(64 MB + 1) (over-cap ⇒ 502, abort; single read —
//     classification + metering + event reuse the same []byte,
//     Amendment 12); InspectResponse for EVERY upstream response
//     (single-classifier seam, Amendment 3). PassAsIs ⇒ relay
//     verbatim; PromoteToError ⇒ write RelayStatus + upstream body.
//  10. Meter: IncrementTokenImages + IncrementModelImages with
//     request_count=1, image_count=BillableImages(body) (0 on
//     error-in-200). Documented divergence from chat's success-
//     only convention (Amendment 14).
//  11. Telemetry: events.Bus.Publish(image_generation, plain
//     map) — nil-safe (no-op on nil bus).
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
			h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
				"outcome":         "request_too_large",
				"max_bytes":       maxRequestBodyBytes,
				"duration_ms":     time.Since(startTime).Milliseconds(),
				"upstream_status": 0,
			})
			h.openAIError(w, http.StatusRequestEntityTooLarge,
				"request body exceeds 16 MB cap (BE-G1)")
			return
		}
		h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
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

	// 1) Auth. Mirrors pkg/proxy/handler.go:265-308 — tokenStore
	// nil ⇒ auth disabled (BE-A1 / Leader Ruling L2).
	authToken, ok := h.authenticate(r)
	if !ok {
		h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
			"outcome":         "auth_failed",
			"duration_ms":     time.Since(startTime).Milliseconds(),
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
		h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
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
		h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
			"outcome":         "missing_model",
			"duration_ms":     time.Since(startTime).Milliseconds(),
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
	//
	// The RESOLVED row is captured (not discarded): the scope
	// predicate authorizes the RESOLVED row (PD-9). On this lane the
	// resolved id IS the client string — claims are group-only, so a
	// non-group row always satisfies row.ID == modelID.
	row, ok := h.resolveModel(modelID)
	if !ok {
		// 404 — unknown model (BE-A1 / Amendment 17). Distinct
		// from 403 (model known but not in token's allowed_models).
		h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
			"outcome":         "model_not_found",
			"model":           modelID,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusNotFound,
			fmt.Sprintf("unknown model %q (image-gen models are served by /v1/image_generation only)", modelID))
		return
	}

	// 4) THE KIND / LANE GATE (imggen-groups commission / T1.5).
	//
	// PRECEDENCE (the decision lane's law, pkg/decision/handler.go:
	// "kind/lane BEFORE scope"): the gate runs BEFORE the scope
	// predicate below, so a misroute answer is TOKEN-INDEPENDENT — it
	// can never leak the lane configuration through a permission
	// answer.
	//
	// Three arms:
	//   - a GROUP whose lane is image-gen ⇒ the group serving branch
	//     (the walk);
	//   - a GROUP whose lane is NOT image-gen (chat / decision /
	//     unknown) ⇒ 400 image_misroute;
	//   - a NON-group row that is not image-gen ⇒ 400 image_misroute.
	//     This closes a REAL pre-existing gap: ANY model id used to
	//     resolve here, pass the scope check, resolve a credential and
	//     die at the provider lookup as a 502 unsupported_provider —
	//     and a chat model whose provider happened to be minimax would
	//     even be SERVED on this lane.
	//   - a plain image-gen row ⇒ today's path, unchanged.
	if row.IsGroup() {
		if !h.groupIsImageGenLane(row.ID) {
			h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
				"outcome":         imggenMisrouteCode,
				"model":           modelID,
				"duration_ms":     time.Since(startTime).Milliseconds(),
				"upstream_status": 0,
			})
			h.openAIErrorWithCode(w, http.StatusBadRequest,
				fmt.Sprintf("image-generation models are served by /v1/image_generation only (group %q is not an image-gen group)", row.ID),
				imggenMisrouteCode)
			return
		}
	} else if !row.IsImageGen() {
		h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
			"outcome":         imggenMisrouteCode,
			"model":           modelID,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIErrorWithCode(w, http.StatusBadRequest,
			fmt.Sprintf("image-generation models are served by /v1/image_generation only (model %q is kind %q)", row.ID, row.Kind),
			imggenMisrouteCode)
		return
	}

	// 5) THE SCOPE PREDICATE (the shared entry gate).
	//
	// ADR D8: the four entry sites collapse into ONE predicate — the
	// scope resolved by pkg/authz (the ONE resolver). A lane may not
	// re-implement the wildcard rule or read the raw grant slices.
	//
	// PD-9: the gate authorizes the RESOLVED row (row.ID), never the
	// client-requested string. On this lane the two are identical
	// (claims are group-only), so the check is behaviourally identical
	// to today's allow-list check.
	//
	// PRECEDENCE (T2.13): 401 (auth) -> 400 missing_model ->
	// 404 (unknown model) -> 400 image_misroute (kind/lane) ->
	// 403 (scope) -> 502. The scope check runs AFTER the 404 and
	// AFTER the kind/lane gate, so an unknown id is never answered
	// with a permission error and a misroute is never answered with
	// a permission error.
	//
	// NOTE: for a PLAIN image-generation model this lane has NO
	// candidate list — an image-generation model has no FallbackChain
	// (pkg/imggen/handler.go's header) — so the entry check is the
	// WHOLE scope obligation on that path. For a GROUP the scope
	// surface is bigger (the group's members are the candidates), and
	// the group arm owns it (the marked SCOPE seam in group.go — the
	// authz composition lands in Phase 2).
	scope := authz.Resolve(authToken, h.membersOf())
	if scope != nil && !scope.Allows(row.ID) {
		h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
			"outcome":         "model_not_allowed",
			"model":           modelID,
			"token":           authToken.ID,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.sendModelNotAllowedError(w, modelID)
		return
	}

	// 6) THE GROUP BRANCH (an image-gen-lane group). The lane gate
	// was already resolved above (it runs BEFORE the scope predicate,
	// so the misroute answer is token-independent); the group arm
	// re-checks it fail-closed and then runs the walk.
	if row.IsGroup() {
		h.serveImageGenGroup(w, r, row, authToken, bodyBytes, startTime)
		return
	}

	// 7) Credential resolution. We use the chat-side helper
	// through the ModelsConfigInterface so the existing
	// affinity engine (Phase 3 / pkg/credentiallb) participates
	// when wired. Stable key (modelID, modelID) per BE-A3 +
	// Amendment 18.
	cred, ok := h.resolveCredential(modelID)
	if !ok {
		h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
			"outcome":         "credential_unresolved",
			"model":           modelID,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIError(w, http.StatusBadGateway,
			fmt.Sprintf("no credentials configured for model %q", modelID))
		return
	}

	// 8-12) THE ATTEMPT (imggen-groups commission / T1.6): the
	// provider lookup, the upstream build, the single POST, the
	// bounded read and the classification live in ONE shared
	// function — the standalone path and the GROUP walk call the
	// SAME code, so the two can never drift.
	//
	// The deadline is the CALLER's: the standalone path passes the
	// per-request ImageGenTimeout; the group walk passes
	// min(remaining walk budget, ImageGenTimeout). The attempt
	// mechanics themselves are identical.
	ctx, cancel := context.WithTimeout(r.Context(), h.imageGenTimeout())
	defer cancel()

	attempt := h.attemptImageGenMember(ctx, cred, bodyBytes, r)
	if attempt.failure != nil {
		// A pre-relay failure: the attempt never produced a
		// classified response, so the envelope is the CALLER's.
		// The detail keys (provider / error / max_bytes) travel in
		// the failure so the telemetry payload keeps the exact key
		// set this path published before the extraction.
		f := attempt.failure
		event := map[string]interface{}{
			"outcome":         f.outcome,
			"model":           modelID,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": attempt.upstreamStatus,
		}
		for key, value := range f.detail {
			event[key] = value
		}
		h.publishEvent(eventTypeImageGeneration, event)
		h.openAIError(w, f.status, f.message)
		if f.meter {
			h.meterFailure(authToken, modelID, f.outcome)
		}
		return
	}

	provider := attempt.provider
	outcome := attempt.outcome
	respBody := attempt.body
	respStatus := attempt.upstreamStatus

	// 13) Relay (T1.4.6 / write-first observe-after). On
	// PassAsIs: copy headers, write upstream status, write body
	// verbatim. On PromoteToError: 502 (or whatever RelayStatus
	// is), preserve the upstream body verbatim (no envelope of
	// our own — error-in-200 is MiniMax's own error shape).
	copyResponseHeaders(w, attempt.headers)
	// Content-Length is already stripped by isHopByHopHeader in
	// copyResponseHeaders, so no stale upstream length can survive
	// when a hook rewrites or normalizes the body. The Del calls
	// below are defensive belt-and-suspenders guards ensuring Go's
	// HTTP server stack computes the exact length for the relayed body.
	if outcome.RelayStatus == 0 {
		if _, ok := provider.(ResponseRewriter); ok {
			w.Header().Del("Content-Length")
		}
	} else {
		if _, ok := provider.(ErrorBodyNormalizer); ok {
			w.Header().Del("Content-Length")
		}
	}
	if outcome.RelayStatus == 0 {
		// PassAsIs — covers upstream HTTP ≥400 passthrough AND
		// 200-success. The handler relays the upstream status
		// verbatim (Amendment 3; the seam owns the decision).
		w.WriteHeader(respStatus)
	} else {
		// PromoteToError. The upstream body is preserved verbatim
		// (no envelope of our own on error-in-200 — BE-T3
		// Amendment 5 family decision; the MiniMax-native
		// client reads base_resp from the preserved body).
		if outcome.RelayStatus == http.StatusOK {
			// FUTURE-PROOFING TRIPWIRE (reviewer-deferred item):
			// an adapter that returns RelayStatus==200 on a
			// non-success path would silently relabel the
			// upstream's failure as success. The Inspector
			// contract is "non-zero RelayStatus ⇒ fail"; if a
			// future adapter wants to bypass this clamp it
			// must signal success through Outcome.RelayStatus==0
			// (PassAsIs) instead. Log + clamp to 502.
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
			modelID, outcome.Reason, respStatus, modelID, cred.Provider)
	}
	if _, err := w.Write(respBody); err != nil {
		// The client disconnected; we still bill the request
		// (the upstream completed) but log for observability.
		log.Printf("[imggen] write to client failed: %v (model=%s, provider=%s)", err, modelID, cred.Provider)
	}

	// 14) Meter + event. Meter first (observe-after: even on
	// error-in-200, request_count+1, image_count=0 — Amendment
	// 14 documented divergence from chat's success-only). Then
	// publish the telemetry event with the final outcome.
	billable := 0
	if outcome.RelayStatus == 0 {
		n, _ := provider.BillableImages(respBody)
		billable = n
	}
	h.meter(authToken, modelID, billable)

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
	h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
		"model":           modelID,
		"provider":        cred.Provider,
		"success_count":   successCount,
		"failed_count":    failedCount,
		"upstream_status": respStatus,
		"duration_ms":     time.Since(startTime).Milliseconds(),
		"outcome":         eventOutcome,
	})
}

// imgGenAttempt is the result of ONE upstream attempt (imggen-groups
// commission / T1.6 — the shared attempt mechanics).
//
// The attempt performs NO client write: the caller relays (standalone
// path) or classifies (group walk) the result.
type imgGenAttempt struct {
	// provider is the resolved adapter (nil when the lookup failed).
	provider UpstreamProvider
	// cred is the credential the attempt ran with.
	cred models.ResolvedCredential
	// outcome is the classifier verdict — the SINGLE-classifier seam
	// (Architect Amendment 3: InspectResponse is called for EVERY
	// upstream response; the handler never branches on
	// resp.StatusCode directly).
	outcome Outcome
	// upstreamStatus is the RAW upstream HTTP status (0 when the
	// attempt never reached the upstream).
	upstreamStatus int
	// body is the upstream body — read ONCE (classification,
	// metering and the relay reuse the same []byte; Architect
	// Amendment 12 single-materialization).
	body []byte
	// headers are the upstream response headers (the relay copies the
	// non-hop-by-hop ones).
	headers http.Header
	// failure describes an attempt that produced NO classified
	// response (build failure, transport error, over-cap body, a
	// failed conversion). nil on a completed attempt.
	failure *attemptFailure
}

// attemptFailure is a PRE-RELAY failure of one attempt: the attempt
// never produced a classified response, so the caller owns the
// envelope, the telemetry payload and the billing decision.
type attemptFailure struct {
	// outcome is the telemetry outcome for the failed attempt.
	outcome string
	// status is the HTTP status the standalone path answers with.
	status int
	// message is the standalone envelope's message.
	message string
	// detail carries the failure-class-specific telemetry keys
	// (provider / error / max_bytes). The standalone path's event
	// payload keeps the exact key set it published before the
	// extraction.
	detail map[string]interface{}
	// meter reports whether the failure is billed (request_count=1,
	// image_count=0 — the Amendment 14 bill-on-error continuity).
	meter bool
}

// attemptImageGenMember performs ONE upstream attempt for one
// credential and classifies the outcome.
//
// It is the SHARED attempt mechanics (imggen-groups commission /
// T1.6): the standalone path and the group walk call the SAME
// function, so the two can never drift. It performs NO client write.
//
// The caller's ctx carries the deadline: the standalone path passes
// the per-request ImageGenTimeout; the group walk passes
// min(remaining walk budget, ImageGenTimeout).
func (h *Handler) attemptImageGenMember(ctx context.Context, cred models.ResolvedCredential, bodyBytes []byte, r *http.Request) imgGenAttempt {
	res := imgGenAttempt{cred: cred}

	// Look up the per-provider UpstreamProvider. Phase 1 ships
	// minimax only; unknown providers ⇒ 502 (the handler does not
	// silently fall back to a different provider).
	provider, ok := lookup(cred.Provider)
	if !ok {
		res.failure = &attemptFailure{
			outcome: "unsupported_provider",
			status:  http.StatusBadGateway,
			message: fmt.Sprintf("no image-generation adapter registered for provider %q", cred.Provider),
			detail:  map[string]interface{}{"provider": cred.Provider},
		}
		return res
	}
	res.provider = provider

	upstreamURL, upstreamBody, err := provider.BuildUpstream(cred, bodyBytes)
	if err != nil {
		res.failure = &attemptFailure{
			outcome: "build_failed",
			status:  http.StatusBadRequest,
			message: err.Error(),
			detail:  map[string]interface{}{"error": err.Error()},
		}
		return res
	}

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(upstreamBody))
	if err != nil {
		res.failure = &attemptFailure{
			outcome: "request_build_failed",
			status:  http.StatusInternalServerError,
			message: "failed to build upstream request",
			detail: map[string]interface{}{
				"provider": cred.Provider,
				"error":    err.Error(),
			},
		}
		return res
	}

	// Headers. Content-Type + credential bearer. Copy remaining
	// client headers EXCEPT hop-by-hop (Host, Content-Length,
	// Transfer-Encoding) AND EXCEPT client auth headers
	// (Authorization, X-API-Key, x-api-key) — the proxy's sk-
	// token must never reach MiniMax (BE-A2). Also EXCEPT the
	// client Content-Type (the app-owned application/json above
	// is authoritative — Add-ing the client's CT duplicates the
	// header upstream) AND the client Accept-Encoding: Go's
	// http.Transport only transparently decompresses a gzip
	// response when IT added Accept-Encoding itself; a copied
	// header (cloudflared injects "Accept-Encoding: gzip" toward
	// origin) disables auto-decompression and a gzipped body
	// reaches the JSON parser ⇒ 502. Excluding both lets the
	// transport own encoding negotiation.
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+cred.APIKey)
	for key, values := range r.Header {
		if isHopByHopHeader(key) || isClientAuthHeader(key) ||
			strings.EqualFold(key, "Content-Type") ||
			strings.EqualFold(key, "Accept-Encoding") {
			continue
		}
		for _, value := range values {
			upstreamReq.Header.Add(key, value)
		}
	}

	// Single upstream call. Exactly ONE per attempt (the standalone
	// path: exactly one per request — Amendment 14; the group walk:
	// one per member attempt).
	resp, err := sharedHTTPClient.Do(upstreamReq)
	if err != nil {
		// Deadline / context expiry surfaces here as well. 504 on
		// context.DeadlineExceeded; 502 on transport error.
		status := http.StatusBadGateway
		outcome := "upstream_transport_error"
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			status = http.StatusGatewayTimeout
			outcome = "deadline"
		}
		res.failure = &attemptFailure{
			outcome: outcome,
			status:  status,
			message: fmt.Sprintf("upstream request failed: %v", err),
			detail: map[string]interface{}{
				"provider": cred.Provider,
				"error":    err.Error(),
			},
			meter: true,
		}
		return res
	}
	defer resp.Body.Close()
	res.upstreamStatus = resp.StatusCode
	res.headers = resp.Header

	// Response read bounded (BE-G2). io.LimitReader 64 MB + 1 so the
	// handler can detect over-cap distinctly (the +1 byte triggers a
	// separate branch below; the over-cap body is never materialized
	// into a second []byte — single read, single classification,
	// single relay).
	cap := int64(maxResponseBodyBytes)
	if maxResponseBodyBytesForTest > 0 {
		cap = maxResponseBodyBytesForTest
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, cap+1))
	if err != nil {
		res.failure = &attemptFailure{
			outcome: "upstream_read_failed",
			status:  http.StatusBadGateway,
			message: "upstream read failed",
			detail: map[string]interface{}{
				"provider": cred.Provider,
				"error":    err.Error(),
			},
			meter: true,
		}
		return res
	}
	if len(respBody) > int(cap) {
		res.failure = &attemptFailure{
			outcome: "response_too_large",
			status:  http.StatusBadGateway,
			message: fmt.Sprintf("upstream response exceeds %d MB cap (BE-G2)", cap>>20),
			detail: map[string]interface{}{
				"provider":  cred.Provider,
				"max_bytes": cap,
			},
			meter: true,
		}
		return res
	}

	// Classification (Amendment 3 — single-classifier seam).
	// InspectResponse is called for EVERY upstream response; the
	// handler never branches on resp.StatusCode directly. The
	// Outcome carries the relay decision.
	outcome := provider.InspectResponse(resp.StatusCode, respBody)

	// Optional body-rewrite hook (the IO-conversion seam). Runs ONLY
	// after InspectResponse returns PassAsIs, and only for adapters
	// that implement ResponseRewriter. On a conversion failure the
	// handler surfaces a 502 with an OpenAI envelope and bills the
	// request as a failure (the upstream completed but its body could
	// not be shaped into the existing IO format).
	if outcome.RelayStatus == 0 {
		if rw, ok := provider.(ResponseRewriter); ok {
			converted, err := rw.RewriteResponse(respBody)
			if err != nil {
				res.failure = &attemptFailure{
					outcome: "response_conversion_failed",
					status:  http.StatusBadGateway,
					message: "failed to convert upstream response: " + err.Error(),
					detail: map[string]interface{}{
						"provider": cred.Provider,
						"error":    err.Error(),
					},
					meter: true,
				}
				return res
			}
			respBody = converted
		}
	}

	// Optional error-body normalizer (the promoted-error path only).
	// Runs ONLY when InspectResponse promoted the response to an
	// error, and only for adapters that implement
	// ErrorBodyNormalizer. The success path is untouched.
	if outcome.RelayStatus != 0 {
		if nb, ok := provider.(ErrorBodyNormalizer); ok {
			respBody = nb.NormalizeErrorBody(respBody)
		}
	}

	res.outcome = outcome
	res.body = respBody
	return res
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
// Metering runs on a DETACHED context (Background + 5s bound),
// never the request context: the upstream call has already
// completed by the time metering runs, so a client disconnect
// mid-relay must not cancel the billing (review F1; chat
// precedent — pkg/proxy/handler.go increments its usage counter
// on context.Background()).
//
// Nil-safe: when h.usage is nil, the call is a no-op so legacy /
// test setups work without a usage counter.
func (h *Handler) meter(authToken *auth.AuthToken, modelID string, imageCount int) {
	if h.usage == nil {
		return
	}
	// Deliberately detached from the request context (see doc
	// comment); 5s bounds the DB write.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
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
// request_count only on success. Metering is detached from the
// request context (see meter) — mapped-error paths include
// client-visible timeouts where the request context may already
// be dead.
func (h *Handler) meterFailure(authToken *auth.AuthToken, modelID, outcome string) {
	_ = outcome // reserved for future structured metering — currently a no-op
	h.meter(authToken, modelID, 0)
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

// openAIErrorWithCode threads the supplied errorCode into the
// envelope (image_misroute / imggen_all_members_failed / similar
// typed codes). Mirrors the decision lane's helper (pkg/decision/
// handler.go) — the packages do not import each other (the import
// guard forbids it), so the helper is spelled twice.
func (h *Handler) openAIErrorWithCode(w http.ResponseWriter, code int, message, errorCode string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeServerError, errorCode, message))
}

// sendAuthError writes the OpenAI-envelope 401. Maps 401 to
// "authentication_error" (matches pkg/proxy/handler.go:346-355 —
// the public-surface decision the chat handler adopted so FE
// and other clients parse 4xx envelopes identically across
// /v1/chat/completions and /v1/image_generation).
func (h *Handler) sendAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(models.NewOpenAIError(
		models.ErrorTypeAuthenticationError, "", "Invalid or expired API key"))
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

// resolveModel + resolveCredential are the modelConfig + credential
// seam. They are stored as struct fields on the Handler so tests
// can substitute a stub; in production cmd/main.go:238-251 wires
// them to the same modelsConfig + ResolveInternalConfigWithAffinity
// seam the chat path uses (single source of truth for model
// resolution; no separate store). The default stubs return
// ok=false so a unit test that forgets to install the shim
// surfaces as a clean 502 (not a panic). The package owns no
// ModelsConfigInterface import — the production wiring lives in
// cmd/main.go (the import guard at task 1.8.2 audits this).

// modelResolver returns the model config for a model ID.
type modelResolver func(modelID string) (*models.ModelConfig, bool)

// credentialResolver returns the resolved credential for a model ID.
type credentialResolver func(modelID string) (models.ResolvedCredential, bool)

// membersOf returns the CURRENT member ids of a group — the adapter
// pkg/authz.Resolve consumes to expand a granted group into its
// members.
//
// The accessor is the OPTIONAL models.GroupMembershipReader seam: the
// modelscache decorator implements it with a NON-COPYING read of its
// in-memory snapshot (one small allocation — the member-id slice), so
// the request path performs NO DB read (Ruling 5). Without the seam
// (a handler built without the models config) there is no membership
// expansion at all.
func (h *Handler) membersOf() func(string) []string {
	return func(groupID string) []string {
		if h == nil || h.modelsConfig == nil {
			return nil
		}
		if r, ok := h.modelsConfig.(models.GroupMembershipReader); ok {
			return r.GroupMembers(groupID)
		}
		for _, row := range h.modelsConfig.GetModels() {
			if row.ID != groupID || !row.IsGroup() {
				continue
			}
			out := make([]string, 0, len(row.Members))
			for _, m := range row.Members {
				out = append(out, m.ModelID)
			}
			return out
		}
		return nil
	}
}

// SetModelsConfig wires the models-manager seam (the group-membership
// reader the scope resolution needs). It mirrors the decision lane's
// seam; nil-safe (no-op on nil).
func (h *Handler) SetModelsConfig(cfg models.ModelsConfigInterface) {
	if h == nil {
		return
	}
	h.modelsConfig = cfg
}

// groupLane resolves the group's lane through the OPTIONAL seam.
// Nil-safe: a handler without the models config (or without the seam)
// answers "no lane" — the group branch then fails CLOSED.
func (h *Handler) groupLane(groupID string) (models.GroupLane, bool) {
	if h == nil || h.modelsConfig == nil {
		return "", false
	}
	gl, ok := h.modelsConfig.(groupLaneSeam)
	if !ok {
		return "", false
	}
	return gl.GroupLane(groupID)
}

// groupIsImageGenLane is the group branch's entry gate: the group
// must be an IMAGE-GEN-lane group.
//
// The seam is OPTIONAL: an absent seam (or an unresolvable lane — a
// mixed-kind group) fails CLOSED — the caller keeps the 400
// image_misroute path — and the refusal is logged LOUDLY, because a
// missing decorator forward would otherwise be a SILENT no-op in
// production (the modelscache-decorator-forward-trap).
func (h *Handler) groupIsImageGenLane(groupID string) bool {
	lane, ok := h.groupLane(groupID)
	if !ok {
		log.Printf("[imggen] group=%s: the group-lane seam is absent or the lane is unresolvable — failing closed (a group is never served without a resolved lane)", groupID)
		return false
	}
	return lane == models.GroupLaneImageGen
}

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
// at minimum).
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
// pattern used by the raw-passthrough handlers.
//
// It takes the header MAP (not the response) so the shared attempt
// (T1.6) can hand the headers over without carrying the whole
// response around.
func copyResponseHeaders(w http.ResponseWriter, header http.Header) {
	for key, values := range header {
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
