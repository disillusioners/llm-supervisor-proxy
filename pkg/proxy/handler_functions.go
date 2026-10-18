package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Initialization
// ─────────────────────────────────────────────────────────────────────────────

// configUnavailableError is the sentinel returned by initRequestContext
// when the boundary fail-fast gate trips. It wraps
// modelscache.ErrConfigUnavailable (so errors.Is keeps matching at the
// caller) and carries the request-scoped context (reqID, model) the
// caller needs to publish the config_store_unavailable event — the
// requestContext is nil on this path, so the event fields must ride on
// the error (review 2026-08-28: the former `rc != nil` guard in
// HandleChatCompletions was dead code and the OpenAI-boundary event
// never published, unlike the Anthropic mirror).
type configUnavailableError struct {
	reqID string
	model string
}

func (e *configUnavailableError) Error() string {
	return fmt.Sprintf("%s: cannot resolve model %q", modelscache.ErrConfigUnavailable, e.model)
}

func (e *configUnavailableError) Unwrap() error { return modelscache.ErrConfigUnavailable }

// ErrModelNotFound — the unknown/non-internal model sentinel returned by
// initRequestContext (remove-external-upstream P1). Every model must
// resolve through internal credentials; an unknown OR legacy
// non-internal model 404s at the boundary with ZERO upstream requests.
var ErrModelNotFound = errors.New("model_not_found")

// modelNotFoundError is the sentinel returned by initRequestContext
// when the unknown/non-internal 404 gate trips. It wraps
// ErrModelNotFound (so errors.Is keeps matching at the caller) and
// carries the request-scoped context (reqID, model) the caller needs
// to publish the model_not_found event — the requestContext is nil on
// this path, so the event fields ride on the error (exact
// configUnavailableError pattern above).
type modelNotFoundError struct {
	reqID string
	model string
}

func (e *modelNotFoundError) Error() string {
	return fmt.Sprintf("%s: unknown or non-internal model %q", ErrModelNotFound, e.model)
}

func (e *modelNotFoundError) Unwrap() error { return ErrModelNotFound }

// Request-parse sentinels returned by initRequestContext and matched
// with errors.Is at the HandleChatCompletions dispatch (tidy finding
// 6). The message strings are the historical wire contract — they
// must stay byte-identical (they were string-compared before).
var (
	// ErrInvalidUpstreamURL — the configured upstream URL could not
	// be joined into a request target URL.
	ErrInvalidUpstreamURL = errors.New("invalid_upstream_url")

	// ErrReadBodyFailed — the request body could not be read.
	ErrReadBodyFailed = errors.New("read_body_failed")

	// ErrInvalidJSON — the request body is not valid JSON.
	ErrInvalidJSON = errors.New("invalid_json")
)

// initRequestContext parses the incoming request, creates the request log,
// resolves the fallback chain, and returns a fully populated requestContext.
func (h *Handler) initRequestContext(r *http.Request) (*requestContext, error) {
	conf := h.config.Clone()
	targetURL, err := url.JoinPath(conf.UpstreamURL, "/v1/chat/completions")
	if err != nil {
		return nil, ErrInvalidUpstreamURL
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		return nil, ErrReadBodyFailed
	}
	r.Body.Close()

	var requestBody map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestBody); err != nil {
		return nil, ErrInvalidJSON
	}

	reqID := uuid.New().String()
	startTime := time.Now()

	// Capture request messages via the OpenAI adapter so that reasoning_content
	// (request-side thinking) is preserved on store.Message. Thinking. The legacy
	// parseMessages helper dropped reasoning_content, so the Web UI never showed
	// request-side thinking. This is capture-side only; the request body forwarded
	// upstream is unchanged.
	storeMessages := NewOpenAIAdapter().ToStoreMessages(requestBody)
	model, _ := requestBody["model"].(string)
	originalModel := model

	// Deep-copy original messages for retry reconstruction
	var originalMessages []interface{}
	if msgs, ok := requestBody["messages"].([]interface{}); ok {
		originalMessages = make([]interface{}, len(msgs))
		copy(originalMessages, msgs)
	}

	isStream := false
	if s, ok := requestBody["stream"].(bool); ok && s {
		isStream = true
	}

	// Extract parameters (exclude standard fields that are shown separately)
	parameters := extractParameters(requestBody)

	// Extract app tag from x-proxy-app header for request grouping
	appTag := r.Header.Get("x-proxy-app")

	reqLog := &store.RequestLog{
		ID:            reqID,
		Status:        "running",
		Model:         model,
		OriginalModel: originalModel,
		StartTime:     startTime,
		Messages:      storeMessages,
		Retries:       0,
		FallbackUsed:  []string{},
		IsStream:      isStream,
		Parameters:    parameters,
		AppTag:        appTag,
	}
	h.store.Add(reqLog)

	// Resolve model ID to config at the boundary
	var resolvedModel *models.ModelConfig
	if conf.ModelsConfig != nil {
		resolvedModel = conf.ModelsConfig.GetModel(originalModel)
	}

	// db-cache-layer 1D — boundary fail-fast gate (one of the three
	// pkg/proxy source sites this feature touched: this gate in
	// handler_functions.go, the sentinel→503/event conversion in
	// handler.go, and the Anthropic mirror in handler_anthropic.go).
	// A nil resolution is
	// LEGITIMATE (an external/unknown model → passthrough, a real
	// feature) ONLY when the config store can actually answer. When
	// the store is unhealthy (DB outage, cache exhausted) nil means
	// "cannot know" — silently passing such a request through to the
	// external upstream is exactly the 2026-08-27 misroute class. The
	// caller (HandleChatCompletions) converts the sentinel into a 503
	// config_store_unavailable.
	if resolvedModel == nil && conf.ModelsConfig != nil {
		if health, ok := conf.ModelsConfig.(modelscache.ConfigStoreHealth); ok && !health.Healthy() {
			return nil, &configUnavailableError{reqID: reqID, model: originalModel}
		}
	}

	// remove-external-upstream P1 — unknown/non-internal 404 gate.
	// Runs AFTER the resolve and AFTER the cache-unhealthy 503 check
	// above: an unhealthy store still wins with config_store_unavailable
	// ("cannot know"), but a HEALTHY store that answers nil — or answers
	// with a legacy `internal = 0` row — now 404s instead of bypass-
	// forwarding to the retired global upstream.
	//
	// The `!Internal` conjunct is LOAD-BEARING, not belt-and-braces:
	// GetModel never filters `enabled` for non-group rows, and migration
	// 032's disable arm leaves `internal = 0` on legacy rows PERMANENTLY
	// (it never flips internal) — so for the permanent legacy rows the
	// conjunct is the SOLE 404 mechanism. Do not simplify it away.
	//
	// reqLog finalization (review I8): the log row was added with
	// Status "running" above; the pre-existing cache-503 path leaves
	// that row stuck ("running" ghost). This gate finalizes it —
	// Status "failed" + Error/EndTime/Duration, re-Add (overwrite-by-ID).
	// The requestContext is nil on this path, so reqID/model ride on the
	// sentinel (modelNotFoundError) for the caller's event publication.
	if resolvedModel == nil || !resolvedModel.Internal {
		notFoundMsg := fmt.Sprintf("model '%s' not found", originalModel)
		reqLog.Status = "failed"
		reqLog.Error = notFoundMsg
		reqLog.EndTime = time.Now()
		reqLog.Duration = reqLog.EndTime.Sub(startTime).String()
		h.store.Add(reqLog)
		return nil, &modelNotFoundError{reqID: reqID, model: originalModel}
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

	// Phase 3 / Task 2 (superseded 2026-10-17: leading-user-run) —
	// cache the leading-user-run extraction here so the post-auth
	// wiring site at handler.go can hash it with tokenID. The
	// token-salted conversationKey is NOT computed here — tokenID is
	// unset at this line; see A-1 + A-1-wiring in the plan. The run is
	// the canonical JSON array of the LEADING CONTIGUOUS user-message
	// contents (ExtractLeadingUserRun); multimodal content
	// ([]interface{}) is canonical-JSON hashed per A-2.
	var leadingUserRun string
	if resolvedModel != nil && resolvedModel.Internal {
		leadingUserRun = credentiallb.ExtractLeadingUserRun(originalMessages)
	}

	// Per-request delivery mode from the X-LLMProxy-Buffer-Response opt-in
	// header (real-streaming-default plan, Phase 1). Locked L1 semantics are
	// presence-aware — see bufferModeFor below. Precedence (Q3, as amended):
	// header PRESENT ⇒ buffered mode ⇒ today's StreamDeadline machinery runs
	// unchanged; header ABSENT ⇒ live mode ⇒ (Phase 2+) the coordinator's
	// first-byte winner gate applies and StreamDeadline becomes a
	// no-forwardable-byte timeout.
	bufferMode := bufferModeFor(r)

	return &requestContext{
		conf:             conf,
		targetURL:        targetURL,
		reqID:            reqID,
		startTime:        startTime,
		reqLog:           reqLog,
		modelList:        modelList,
		resolvedModel:    resolvedModel,
		requestBody:      requestBody,
		rawBody:          bodyBytes, // Save original body bytes
		isStream:         isStream,
		originalHeaders:  r.Header,
		method:           r.Method,
		baseCtx:          r.Context(),
		originalMessages: originalMessages,
		bufferMode:       bufferMode,
		leadingUserRun:   leadingUserRun,
	}, nil
}

// bufferModeFor reports whether the client opted into buffered (legacy)
// streaming delivery via the X-LLMProxy-Buffer-Response header.
//
// Semantics (LOCKED, plan decision L1 — presence-aware):
//
//	ABSENT header             ⇒ live streaming (false — the new default)
//	PRESENT + true/1/yes/on   ⇒ buffered (true), case-insensitive value
//	PRESENT + empty value     ⇒ buffered (true — bare-header opt-in)
//	PRESENT + anything else   ⇒ live streaming (false): the falsy
//	                            spellings (false/0/no/off) AND any unknown
//	                            non-empty value — opt-in must be explicit
//	                            and correctly spelled
//
// The presence check MUST use r.Header.Values, never r.Header.Get: Get
// conflates an absent header with a PRESENT empty value and would invert
// the default. Values canonicalizes the header key, matching the net/http
// server-side parser which stores every incoming wire key in canonical
// form regardless of client casing.
func bufferModeFor(r *http.Request) bool {
	vals := r.Header.Values("X-LLMProxy-Buffer-Response")
	if len(vals) == 0 {
		return false
	}
	return parseBufferResponseValue(vals[0])
}

// parseBufferResponseValue interprets a PRESENT X-LLMProxy-Buffer-Response
// value. Truthy spellings (true/1/yes/on, case-insensitive) and a genuinely
// empty value opt into buffered delivery; every other value — the falsy
// spellings (false/0/no/off) and any unknown non-empty string — falls to
// live streaming.
//
// Empty-vs-whitespace distinction (locked fixture table, plan Test
// Strategy): only a raw-empty value is the bare-header opt-in. A
// whitespace-only value trims to "" but counts as UNKNOWN — it must NOT
// be conflated with the deliberate empty opt-in, so the emptiness check
// happens BEFORE trimming. (A plain trim-then-switch with "" in the
// truthy case list would wrongly buffer whitespace-only values.)
// Surrounding whitespace around a real token is still trimmed
// ("  true  " ⇒ buffered).
func parseBufferResponseValue(v string) bool {
	if v == "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}
