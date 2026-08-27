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
	"net/url"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/token"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/translator"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolcall"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/toolrepair"
)

// newProviderClient is a variable that can be overridden in tests to inject mock providers
var newProviderClient = providers.NewProvider

// raceExtProviderIsMiniMax returns true iff cfg.UpstreamCredentialID is set
// and resolves to a credential whose Provider matches providers.ProviderMiniMax
// (case-insensitive). Empty UpstreamCredentialID or missing credential ⇒
// false (D3). Used by the race-external 5th-site gate (P1-8 b).
func raceExtProviderIsMiniMax(cfg *ConfigSnapshot) bool {
	if cfg == nil || cfg.ModelsConfig == nil || cfg.UpstreamCredentialID == "" {
		return false
	}
	cred := cfg.ModelsConfig.GetCredential(cfg.UpstreamCredentialID)
	if cred == nil {
		return false
	}
	return strings.ToLower(cred.Provider) == strings.ToLower(string(providers.ProviderMiniMax))
}

// raceIntProviderIsMiniMax returns true iff the resolved internal model
// has a primary credential (Credentials[0]) that resolves to a MiniMax
// credential. modelID is the upstream model id from the race
// coordinator. modelCfg may be nil for the call before
// resolveInternalConfig runs; we re-resolve by ID to keep the helper
// stateless. Used by the race-internal twin A gate (P1-8 a).
func raceIntProviderIsMiniMax(cfg *ConfigSnapshot, modelID string) bool {
	if cfg == nil || cfg.ModelsConfig == nil || modelID == "" {
		return false
	}
	mc := cfg.ModelsConfig.GetModel(modelID)
	if mc == nil {
		return false
	}
	primaryCredentialID := mc.PrimaryCredentialID()
	if primaryCredentialID == "" {
		return false
	}
	cred := cfg.ModelsConfig.GetCredential(primaryCredentialID)
	if cred == nil {
		return false
	}
	return strings.ToLower(cred.Provider) == strings.ToLower(string(providers.ProviderMiniMax))
}

// executeRequest performs the actual HTTP call to upstream
// and streams the response into the request's buffer.
// It checks if the model is internal and routes accordingly.
//
// interleaved is the X-Proxy-Interleaved-Thinking flag carried from the
// race coordinator. Drives the MiniMax reasoning_details split-mode gate
// inside the executed sub-paths; flag-absent calls short-circuit the gate
// (H5) so non-MiniMax / non-flagged requests stay byte-identical.
//
// conversationKey (Phase 3 / Task 4) is the per-request affinity key
// from the post-auth wiring site (handler.go:401+). Threaded through to
// the resolver seam (ResolveInternalConfigWithAffinity).
//
// engine (Phase 3 / Task 6) is the optional Load-Balancing engine
// (nil-safe per W-3 / E-3). Used by the post-resolution publish site
// (W-1) to choose between "publish model_credential_selected" (the
// engine stored a binding) and "do nothing" (the engine returned a
// pinned-existing binding, or no engine at all).
//
// onCredentialSelected (Phase 3 / Task 6) is the callback that emits
// model_credential_selected with the result struct. Calling it here
// keeps the publish at "single source of truth" — only this path
// publishes, never the ultimate-internal or /v1/messages paths.
func executeRequest(
	ctx context.Context,
	cfg *ConfigSnapshot,
	originalReq *http.Request,
	rawBody []byte,
	req *upstreamRequest,
	interleaved bool,
	conversationKey string,
	engine *credentiallb.Engine,
	onCredentialSelected func(modelID string, cred models.ResolvedCredential),
) error {
	req.MarkStarted()

	// Stash the inputs the resolvers and the publish site need on the
	// per-attempt struct (upstreamRequest has no extra fields; threading
	// through the function signature is the simplest). The publish site
	// consults req.resolved.NewlyBound after the internal execution
	// returns; the conversationKey + engine are reused by both resolution
	// sites (secondary + primary fallback).
	if req != nil {
		req.conversationKey = conversationKey
		req.engine = engine
		req.onCredentialSelected = onCredentialSelected
	}

	// Check if this model uses internal upstream
	// Note: ModelsConfig may be nil in tests, so check first
	if cfg.ModelsConfig != nil {
		modelConfig := cfg.ModelsConfig.GetModel(req.modelID)

		if modelConfig != nil && modelConfig.Internal {
			err := executeInternalRequest(ctx, cfg, rawBody, req, interleaved)

			// Phase 3 / Task 6 — single-source-of-truth publish site for
			// model_credential_selected. Fires ONLY when:
			//   1. the resolver returned ok (req.resolvedOK), AND
			//   2. the engine returned newlyBound=true (W-1: a binding
			//      was stored on this call).
			// Per C2: empty-key fresh picks return NewlyBound=false ⇒
			// no event (no binding stored). Per W-1: pinned-existing
			// reuses return NewlyBound=false ⇒ no event.
			publishCredentialSelected(req)
			return err
		}
	}

	// External upstream: use the configured upstream URL
	return executeExternalRequest(ctx, cfg, originalReq, rawBody, req, interleaved)
}

// publishCredentialSelected (Phase 3 / Task 6) fires the
// model_credential_selected event when the engine's NewlyBound flag is
// true for an internal execution. The single-source-of-truth here
// means the race-internal path owns event publication; the ultimate
// paths do NOT publish — and an empty conversationKey (W-2 + C2) is
// naturally absorbed because empty-key picks return NewlyBound=false.
func publishCredentialSelected(req *upstreamRequest) {
	if req == nil || !req.resolvedOK {
		return
	}
	if !req.resolved.NewlyBound {
		return
	}
	if req.onCredentialSelected == nil {
		return
	}
	req.onCredentialSelected(req.modelID, req.resolved)
}

// resolveSpecificInternalCredential (Phase 3 / Task 18 — Round 3b B3)
// resolves the EXACT credentialID handed back by
// engine.ExcludeAndReselect for a modelTypeCredFailover row. It skips
// the affinity seam entirely (a fresh GetOrSelect could re-roll the
// pick — especially on empty-key requests where every engine call
// picks fresh) and mirrors the field derivation of
// store.resolveWithCredential: provider from the credential, baseURL =
// model override > credential default, internal model with peak-hour
// substitution. NewlyBound is false: the rebind/fresh-pick side effect
// already happened inside ExcludeAndReselect; a reselection is not a
// first binding (W-1), so no model_credential_selected fires for it.
func resolveSpecificInternalCredential(cfg *ConfigSnapshot, modelID, credentialID string) (models.ResolvedCredential, bool) {
	if cfg == nil || cfg.ModelsConfig == nil {
		return models.ResolvedCredential{}, false
	}
	modelConfig := cfg.ModelsConfig.GetModel(modelID)
	if modelConfig == nil || !modelConfig.Internal {
		return models.ResolvedCredential{}, false
	}
	// Phase 3 / Round 3j C1 belt-and-braces — the reselected credential
	// MUST belong to this model's Credentials list. If it doesn't, the
	// engine handed back a credential for a DIFFERENT model — refusing
	// it here prevents wrong-ACCOUNT billing (running models[1]'s
	// upstream call on models[0]'s account).
	//
	// This refusal is ONE of two layers that together prevent wrong-
	// model serving on credFailover:
	//   - Spawn layer (spawn()'s modelTypeCredFailover case reads
	//     triggerInfo.modelID — Round 3j C1 critical fix) — this is
	//     what makes wrong-model serving IMPOSSIBLE for the rate-limit
	//     trigger path.
	//   - Executor refusal (this block) — defense-in-depth: even if a
	//     wrong-modelID reaches us through a future regression, the
	//     wrong ACCOUNT is still blocked here. Note: the refusal does
	//     NOT itself make wrong-model serving impossible — the
	//     credential-id-mismatch log only proves the spawn fix was
	//     bypassed; the fall-through re-resolve inside executeInternal
	//     could in principle still serve the wrong model on the
	//     correct account if the spawn fix regressed. The spawn fix
	//     is the load-bearing layer.
	//
	// Fall-through on refusal: return (zero, false) and let the
	// existing failure handling in executeInternalRequest surface it.
	credBelongs := false
	for _, ref := range modelConfig.Credentials {
		if ref.CredentialID == credentialID {
			credBelongs = true
			break
		}
	}
	if !credBelongs {
		log.Printf("[LB-FAILOVER] refusing credential %q — not in model %s's Credentials list (possible wrong-model spawn)",
			credentialID, modelID)
		return models.ResolvedCredential{}, false
	}
	cred := cfg.ModelsConfig.GetCredential(credentialID)
	if cred == nil {
		return models.ResolvedCredential{}, false
	}
	baseURL := modelConfig.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}
	internalModel := modelConfig.InternalModel
	if peakModel := modelConfig.ResolvePeakHourModel(time.Now()); peakModel != "" {
		log.Printf("[PEAK-HOUR] peak hour active for model %s: using %s instead of %s",
			modelConfig.ID, peakModel, modelConfig.InternalModel)
		internalModel = peakModel
	}
	return models.ResolvedCredential{
		Provider:      cred.Provider,
		APIKey:        cred.APIKey,
		BaseURL:       baseURL,
		InternalModel: internalModel,
		CredentialID:  credentialID,
		NewlyBound:    false, // reselection ≠ first binding (W-1)
	}, true
}

// executeInternalRequest handles requests to internal providers (bypassing external upstream).
//
// interleaved is the X-Proxy-Interleaved-Thinking flag (parsed at handler
// entry, threaded via executeRequest). Drives the MiniMax
// reasoning_details split-mode gate for race-internal; passed down to
// convertToProviderRequest's caller (W6) which decides whether to call
// the translator.
func executeInternalRequest(ctx context.Context, cfg *ConfigSnapshot, rawBody []byte, req *upstreamRequest, interleaved bool) error {
	// Check if we should use secondary upstream model (for modelTypeSecond)
	useSecondary := req.UseSecondaryUpstream()

	var internalModel string
	var resolved models.ResolvedCredential
	var resolvedOK bool

	// Phase 3 / Task 18 (Round 3b B3 + Round 3c C3(1)) — a
	// modelTypeCredFailover row resolves the SPECIFIC reselected
	// credential surfaced by engine.ExcludeAndReselect (pre-resolved at
	// classification time in the coordinator; rides
	// spawnTriggerInfo.credentialID). NOT a fresh GetOrSelect: for
	// empty-key requests a fresh engine call would re-roll the weighted
	// pick and could re-select the just-excluded (cooling) credential.
	if req.reselectedCredentialID != "" && cfg.ModelsConfig != nil {
		resolved, resolvedOK = resolveSpecificInternalCredential(cfg, req.modelID, req.reselectedCredentialID)
		if resolvedOK {
			log.Printf("[LB-FAILOVER] attempt %d using reselected credential %s for model %s",
				req.id, req.reselectedCredentialID, req.modelID)
		}
	}

	// Phase 3 / Task 4 — primary + secondary paths both go through the
	// affinity seam. The secondary path inherits the primary credential
	// (do NOT call the engine a second time per the contract).
	if !resolvedOK && useSecondary && cfg.ModelsConfig != nil {
		modelConfig := cfg.ModelsConfig.GetModel(req.modelID)
		if modelConfig != nil && modelConfig.SecondaryUpstreamModel != "" && modelConfig.InternalModel != "" {
			// Resolve credential/provider from primary config, but use secondary model name
			resolved, resolvedOK = cfg.ModelsConfig.ResolveInternalConfigWithAffinity(req.modelID, req.conversationKey)
			if resolvedOK {
				internalModel = modelConfig.SecondaryUpstreamModel
				log.Printf("[SECONDARY] Using secondary upstream model %s instead of %s for model %s",
					internalModel, modelConfig.InternalModel, req.modelID)

				// Publish event for frontend tracking
				if cfg.EventBus != nil {
					cfg.EventBus.Publish(events.Event{
						Type:      "race_secondary_model_used",
						Timestamp: time.Now().Unix(),
						Data: map[string]interface{}{
							"id":              fmt.Sprintf("%d", req.id),
							"model_id":        req.modelID,
							"primary_model":   modelConfig.InternalModel,
							"secondary_model": modelConfig.SecondaryUpstreamModel,
						},
					})
				}
			}
		}
	}

	// Fallback to normal resolution if secondary not used or not configured
	if !resolvedOK {
		resolved, resolvedOK = cfg.ModelsConfig.ResolveInternalConfigWithAffinity(req.modelID, req.conversationKey)
	}

	// Capture the resolution result for the post-completion publish site
	// (Phase 3 / Task 6). The executeRequest caller reads these fields
	// after this function returns and fires the model_credential_selected
	// event when newlyBound=true (the engine's W-1 signal).
	req.resolved = resolved
	req.resolvedOK = resolvedOK

	if !resolvedOK {
		return fmt.Errorf("failed to resolve internal config for model %s", req.modelID)
	}

	// Phase 3 / Task 18 (Round 3b B3) — a modelTypeCredFailover row
	// resolves the SPECIFIC reselected credential surfaced by
	// engine.ExcludeAndReselect at the coordinator layer (above). The
	// `req.reselectedCredentialID` field is consumed by the executor's
	// primary resolution path (race_executor.go just above) via
	// resolveSpecificInternalCredential — there is NO secondary
	// override to apply here. The downstream provider call below reads
	// `resolved.CredentialID` / `resolved.APIKey` directly, so the
	// reselect already propagates end-to-end. (The original Round-3
	// `if` block was a no-op placeholder from the case-1 prototype
	// and is removed — Round 3j W2.)

	provider := resolved.Provider
	apiKey := resolved.APIKey
	baseURL := resolved.BaseURL
	if internalModel == "" {
		internalModel = resolved.InternalModel
	}

	// Create provider client
	providerClient, err := newProviderClient(provider, apiKey, baseURL)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	log.Printf("[DEBUG] Race attempt %d calling internal provider: %s (model=%s, baseURL=%s, credential_id=%s)", req.id, provider, internalModel, baseURL, resolved.CredentialID)

	// Parse request body
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(rawBody, &bodyMap); err != nil {
		return fmt.Errorf("failed to parse request body: %w", err)
	}

	// Check if streaming
	isStream := false
	if stream, ok := bodyMap["stream"].(bool); ok {
		isStream = stream
	}

	// P1-8(a): twin A — gate runs in this CALLER of convertToProviderRequest
	// per W6 (converter stays pure). When the gate fires, the translator
	// mutates bodyMap in place (top-level reasoning_split + per-message
	// reasoning_details) BEFORE the converter hydrates the typed
	// *ChatCompletionRequest; the converter then carries the mutations
	// through map→struct hydration (D1 reasoning_details on ChatMessage +
	// top-level reasoning_split is set by the caller after convertRequest
	// returns — see the typed-field setter below).
	if interleaved && raceIntProviderIsMiniMax(cfg, req.modelID) {
		if err := translator.TranslateRequestBody(bodyMap); err != nil {
			return fmt.Errorf("race-internal translator: %w", err)
		}
	}

	// Convert to provider request
	providerReq, err := convertToProviderRequest(bodyMap, internalModel)
	if err != nil {
		return fmt.Errorf("failed to convert request: %w", err)
	}

	// P1-8(d) on race-internal (typed-field setter — symmetric with twin B
	// on ultimate-internal). When the gate fired above, bodyMap now
	// carries reasoning_split at the top level.
	//
	// This typed setter is REQUIRED, not belt-and-suspenders:
	// convertToProviderRequest does not propagate top-level reasoning_split
	// from bodyMap into the typed struct, so without this explicit
	// assignment providerReq.ReasoningSplit would stay nil and
	// json.Marshal(req) would drop the field via omitempty whenever the
	// struct is re-marshalled (logging, future typed sites, etc.).
	if interleaved && raceIntProviderIsMiniMax(cfg, req.modelID) && providerReq.ReasoningSplit == nil {
		t := true
		providerReq.ReasoningSplit = &t
	}

	if isStream {
		// Detect provider for normalization context
		provider := normalizers.DetectProvider(cfg.ModelsConfig, req.modelID)
		normCtx := normalizers.NewContext(provider, fmt.Sprintf("%d", req.id))
		normalizers.GetRegistry().ResetAll(normCtx)
		return handleInternalStream(ctx, providerClient, providerReq, req, internalModel, normCtx, cfg.ToolRepair, cfg.StreamDeadline, rawBody)
	}
	return handleInternalNonStream(ctx, providerClient, providerReq, req, internalModel, rawBody)
}

// executeExternalRequest handles requests to external upstream (LiteLLM, etc.).
//
// Phase 3 / Task 24 (Round 3 — R3-7 scope guard): this env-only path is
// UNCHANGED by credential load-balancing and rate-limit credential
// failover. The cfg.UpstreamCredentialID resolution below uses the
// deployment's single env credential — it is NOT model-owned, so a 429
// here follows the existing model-racing behavior (no
// ExcludeAndReselect, no cooldown, no modelTypeCredFailover spawn).
// grep-guard: zero hits of ExcludeAndReselect / IsRateLimitError /
// modelTypeCredFailover within this function.
//
// interleaved is the X-Proxy-Interleaved-Thinking flag (parsed at handler
// entry, threaded via executeRequest). Drives the MiniMax
// reasoning_details split-mode gate for race-external; when gated on, the
// translator mutates bodyMap before re-marshalling.
func executeExternalRequest(ctx context.Context, cfg *ConfigSnapshot, originalReq *http.Request, rawBody []byte, req *upstreamRequest, interleaved bool) error {
	// 1. Prepare upstream request
	// Check for test upstream header (for testing with mock servers)
	upstreamURL := cfg.UpstreamURL

	// Set the target URL to upstream
	u, err := url.Parse(upstreamURL)
	if err != nil {
		return fmt.Errorf("invalid upstream URL: %w", err)
	}
	u.Path, _ = url.JoinPath(u.Path, "/v1/chat/completions")

	// 1.5 Modify body to use current model ID
	var bodyMap map[string]interface{}
	finalBody := rawBody
	if err := json.Unmarshal(rawBody, &bodyMap); err == nil {
		bodyMap["model"] = req.modelID

		// P1-8(b): 5th wiring site — race-external body-map mutation.
		// Gate = interleaved && upstream credential is MiniMax. Empty
		// UpstreamCredentialID ⇒ providerIsMiniMax=false (D3).
		//
		// The pre-existing model-override unmarshal (above) and marshal
		// (below) run unconditionally regardless of the gate — the model
		// field is always overwritten. The NEW translator call is the
		// only mutation gated on interleaved && raceExtProviderIsMiniMax(cfg);
		// gated-off means the translator is not invoked, so no new body
		// change occurs beyond the pre-existing model-override behavior.
		if interleaved && raceExtProviderIsMiniMax(cfg) {
			if err := translator.TranslateRequestBody(bodyMap); err != nil {
				return fmt.Errorf("race-external translator: %w", err)
			}
		}

		if b, err := json.Marshal(bodyMap); err == nil {
			finalBody = b
		}
	}
	// Create fresh request with context and body
	upstreamReq, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(finalBody))
	if err != nil {
		return fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Copy headers from original request
	for k, v := range originalReq.Header {
		// Skip standard proxy-unsafe headers
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Host") || strings.HasPrefix(strings.ToLower(k), "x-llmproxy-") {
			continue
		}
		// D4: NARROW strip of exactly x-proxy-interleaved-thinking on the
		// race-external path. Case-insensitive via strings.EqualFold. The
		// general x-proxy-* strip is REJECTED (would change flag-absent
		// forwarding behavior — old clients never send this header, so
		// the narrow strip preserves the flag-absent invariant). Mirrors
		// the same strip in pkg/ultimatemodel/handler_external.go:79-87.
		if strings.EqualFold(k, "x-proxy-interleaved-thinking") {
			continue
		}
		upstreamReq.Header[k] = v
	}
	upstreamReq.Host = u.Host

	// If UpstreamCredentialID is configured, resolve the credential and set auth header
	// This allows the proxy to authenticate with external upstream providers
	// using a different token than what the client provided
	if cfg.UpstreamCredentialID != "" && cfg.ModelsConfig != nil {
		// Remove all auth headers first to avoid conflicts
		upstreamReq.Header.Del("Authorization")
		upstreamReq.Header.Del("X-API-Key")
		upstreamReq.Header.Del("x-api-key")
		upstreamReq.Header.Del("api-key")

		// Resolve credential
		cred := cfg.ModelsConfig.GetCredential(cfg.UpstreamCredentialID)
		if cred != nil {
			apiKey := cred.ResolveAPIKey()
			if apiKey != "" {
				upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)
				log.Printf("[DEBUG] Race attempt %d: using upstream credential %s for authentication", req.id, cfg.UpstreamCredentialID)
			}
		} else {
			log.Printf("[WARN] Race attempt %d: upstream credential %s not found", req.id, cfg.UpstreamCredentialID)
		}
	}

	log.Printf("[DEBUG] Race attempt %d calling: %s (Host: %s)", req.id, upstreamReq.URL.String(), upstreamReq.Host)

	client := &http.Client{
		Timeout: 0, // Timeout is handled by context
	}

	// 2. Perform request
	resp, err := client.Do(upstreamReq)
	if err != nil {
		return fmt.Errorf("upstream request failed: %w", err)
	}

	// Deferred function to close response body only if req.resp hasn't been
	// cleared by cleanup() (which happens when Cancel() is called).
	// This prevents double-close when both Cancel() and this defer execute.
	defer func() {
		req.mu.Lock()
		if req.resp != nil && req.resp.Body != nil {
			req.resp.Body.Close()
			req.resp = nil
		}
		req.mu.Unlock()
	}()

	req.SetResp(resp)

	// Track HTTP status code for error type detection
	req.SetHTTPStatus(resp.StatusCode)

	// 3. Check for immediate error
	if resp.StatusCode >= 400 {
		// Read response body for error details
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		bodyStr := string(bodyBytes)

		// Check if the response body contains context overflow patterns
		if models.IsContextOverflowError(fmt.Errorf("%s", bodyStr)) {
			return fmt.Errorf("upstream returned error: %s - %s", resp.Status, bodyStr)
		}

		return fmt.Errorf("upstream returned error: %s", resp.Status)
	}

	// 4. Check if this is a streaming or non-streaming response
	contentType := resp.Header.Get("Content-Type")
	isStreaming := strings.Contains(contentType, "text/event-stream")

	if !isStreaming {
		// Non-streaming response: read entire body as single chunk
		// (interleaved is threaded through P2-4 so the response
		// translator's MiniMax gate is visible at the call site).
		return handleNonStreamingResponse(ctx, cfg, resp, req, finalBody, interleaved)
	}

	// Streaming response
	req.MarkStreaming()
	// Detect provider for normalization
	provider := normalizers.DetectProvider(cfg.ModelsConfig, req.modelID)
	return handleStreamingResponse(ctx, cfg, resp, req, provider, finalBody, interleaved)
}

// handleInternalNonStream handles non-streaming requests for internal providers
func handleInternalNonStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, upstreamReq *upstreamRequest, internalModel string, rawBody []byte) error {
	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		// Extract HTTP status from ProviderError if available
		if providerErr, ok := err.(*providers.ProviderError); ok && providerErr.StatusCode > 0 {
			upstreamReq.SetHTTPStatus(providerErr.StatusCode)
		}
		return err
	}

	// Extract usage from response and store it
	if resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 || resp.Usage.TotalTokens > 0 {
		upstreamReq.SetUsage(&TokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		})
	}

	// Marshal response to JSON
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	// If provider didn't return usage, use fallback token counting
	if usage := upstreamReq.GetUsage(); usage == nil || (usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0) {
		if token.FallbackEnabled() {
			tokenizer := token.GetTokenizer()
			// Convert bodyMap back to rawBody for fallback counting
			reqBody, _ := json.Marshal(req)
			promptTokens, err := tokenizer.CountPromptTokens(reqBody, internalModel)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, internalModel)
			}
			// Extract completion text from the response we already have
			var respMap map[string]interface{}
			json.Unmarshal(data, &respMap)
			var completionText string
			if choices, ok := respMap["choices"].([]interface{}); ok && len(choices) > 0 {
				if choice, ok := choices[0].(map[string]interface{}); ok {
					if msg, ok := choice["message"].(map[string]interface{}); ok {
						if content, ok := msg["content"].(string); ok {
							completionText = content
						}
					}
				}
			}
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, internalModel)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, internalModel)
			}
			upstreamReq.SetUsage(&TokenUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			})
			log.Printf("[DEBUG][fallback-token-count] internal non-streaming: model=%s prompt=%d completion=%d total=%d",
				internalModel, promptTokens, completionTokens, promptTokens+completionTokens)
		}
	}

	// Add as single chunk
	if buf := upstreamReq.GetBuffer(); !buf.Add(data) {
		return fmt.Errorf("buffer limit exceeded: non-streaming response for model %s", internalModel)
	}

	return nil
}

// handleInternalStream handles streaming requests for internal providers
func handleInternalStream(ctx context.Context, provider providers.Provider, req *providers.ChatCompletionRequest, upstreamReq *upstreamRequest, internalModel string, normCtx *normalizers.NormalizeContext, toolRepairConfig toolrepair.Config, streamDeadline time.Duration, rawBody []byte) error {
	// Snapshot the buffer once: Cancel() (client disconnect) releases the
	// buffer field concurrently (cleanup sets it to nil), so this handler
	// must never re-read upstreamReq.buffer after this point. The snapshot
	// stays valid after Close() — Add returns false, GetAllRawBytesOnce
	// still returns the buffered chunks (regression: nil-deref SIGSEGV in
	// the fallback token-count path).
	buf := upstreamReq.GetBuffer()

	eventCh, err := provider.StreamChatCompletion(ctx, req)
	if err != nil {
		// Extract HTTP status from ProviderError if available
		if providerErr, ok := err.(*providers.ProviderError); ok && providerErr.StatusCode > 0 {
			upstreamReq.SetHTTPStatus(providerErr.StatusCode)
		}
		return err
	}

	upstreamReq.MarkStreaming()

	// Track state for proper streaming format
	firstChunk := true
	nextToolCallIndex := 0
	seenToolCallIDs := make(map[string]int)

	// Create tool call buffer with integrated repair
	// This replaces the separate accumulator + post-stream repair pattern
	// Repair happens during streaming when tool calls are emitted
	var toolCallBuffer *toolcall.ToolCallBuffer
	if toolRepairConfig.Enabled {
		toolCallBuffer = toolcall.NewToolCallBufferWithRepair(
			5*1024*1024, // 5MB default
			internalModel,
			fmt.Sprintf("%d", upstreamReq.id),
			&toolRepairConfig,
		)
	} else {
		// Buffer without repair (repair disabled)
		// This is still needed to accumulate chunked tool call arguments
		toolCallBuffer = toolcall.NewToolCallBuffer(
			5*1024*1024, // 5MB default
			internalModel,
			fmt.Sprintf("%d", upstreamReq.id),
		)
	}

	for event := range eventCh {
		// Check for context cancellation or explicit cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if the request was cancelled to exit promptly
		// This ensures the goroutine exits immediately when Cancel() is called,
		// even if context cancellation hasn't propagated yet
		if upstreamReq.IsCancelled() {
			return context.Canceled
		}

		switch event.Type {
		case "content":
			// Write SSE data event
			// OpenAI streaming format: role is only present in FIRST chunk
			// Use map to control exactly what gets serialized (avoid zero-value string issue)
			var data []byte
			if firstChunk {
				// First chunk includes role
				chunk := map[string]interface{}{
					"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   internalModel,
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"role":    "assistant",
								"content": event.Content,
							},
						},
					},
				}
				data, _ = json.Marshal(chunk)
			} else {
				// Subsequent chunks: NO role field at all (not even empty string)
				chunk := map[string]interface{}{
					"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   internalModel,
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"content": event.Content,
							},
						},
					},
				}
				data, _ = json.Marshal(chunk)
			}
			line := fmt.Sprintf("data: %s\n", data)
			// Apply normalization to ensure consistent format
			normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName([]byte(line), normCtx)
			if modified {
				log.Printf("[DEBUG] Race attempt %d (internal): normalized chunk by %s", upstreamReq.id, normalizerName)
			}
			if !buf.Add(normalizedLine) {
				return fmt.Errorf("buffer limit exceeded: content chunk for model %s", internalModel)
			}
			firstChunk = false

		case "tool_call":
			// Write tool_call delta
			// Must include index field for each tool call (required for streaming)
			// Use map to control exactly what gets serialized
			if len(event.ToolCalls) > 0 {
				toolCalls := make([]map[string]interface{}, len(event.ToolCalls))
				for i, tc := range event.ToolCalls {
					// Use the index from the tool call delta directly.
					// The provider (OpenAI) already assigns correct indices based on the upstream response.
					// Reassigning indices here causes mismatches when arguments chunks don't have IDs.
					index := tc.Index
					if index == 0 && tc.ID == "" && tc.Function.Name == "" {
						// Fallback: if index is 0 but this is actually position-based (no ID, no name),
						// use position-based index as a last resort
						index = i
					}

					// Track seen IDs for debugging/logging purposes only
					if tc.ID != "" {
						if _, seen := seenToolCallIDs[tc.ID]; !seen {
							seenToolCallIDs[tc.ID] = index
							if index >= nextToolCallIndex {
								nextToolCallIndex = index + 1
							}
						}
					}

					toolCalls[i] = map[string]interface{}{
						"index": index,
						"id":    tc.ID,
						"type":  tc.Type,
						"function": map[string]interface{}{
							"name":      tc.Function.Name,
							"arguments": tc.Function.Arguments,
						},
					}
				}
				chunk := map[string]interface{}{
					"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   internalModel,
					"choices": []map[string]interface{}{
						{
							"index": 0,
							"delta": map[string]interface{}{
								"tool_calls": toolCalls,
							},
						},
					},
				}
				data, _ := json.Marshal(chunk)
				line := fmt.Sprintf("data: %s\n", data)

				// Apply normalization to ensure consistent format
				normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName([]byte(line), normCtx)
				if modified {
					log.Printf("[DEBUG] Race attempt %d (internal): normalized chunk by %s", upstreamReq.id, normalizerName)
				}

				// Process through tool call buffer with integrated repair
				// The buffer accumulates fragments and repairs when complete
				var chunksToEmit [][]byte
				if toolCallBuffer != nil {
					chunksToEmit = toolCallBuffer.ProcessChunk(normalizedLine)
				} else {
					chunksToEmit = [][]byte{normalizedLine}
				}

				// Add all chunks to buffer
				for _, chunk := range chunksToEmit {
					if !buf.Add(chunk) {
						return fmt.Errorf("buffer limit exceeded: tool_call chunk for model %s", internalModel)
					}
				}
			}

		case "thinking":
			// Write thinking/reasoning content (DeepSeek-style reasoning_content field)
			// Use map to control exactly what gets serialized
			chunk := map[string]interface{}{
				"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   internalModel,
				"choices": []map[string]interface{}{
					{
						"index": 0,
						"delta": map[string]interface{}{
							"reasoning_content": event.ReasoningContent,
						},
					},
				},
			}
			data, _ := json.Marshal(chunk)
			line := fmt.Sprintf("data: %s\n", data)
			// Apply normalization to ensure consistent format
			normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName([]byte(line), normCtx)
			if modified {
				log.Printf("[DEBUG] Race attempt %d (internal): normalized chunk by %s", upstreamReq.id, normalizerName)
			}
			if !buf.Add(normalizedLine) {
				return fmt.Errorf("buffer limit exceeded: thinking chunk for model %s", internalModel)
			}

		case "done":
			// Flush any remaining buffered tool calls with repair
			if toolCallBuffer != nil {
				flushChunks := toolCallBuffer.Flush()
				for _, chunk := range flushChunks {
					if !buf.Add(chunk) {
						log.Printf("[WARN] Race attempt %d (internal): failed to flush tool call chunk", upstreamReq.id)
					}
				}

				// Log repair stats if any repairs occurred
				stats := toolCallBuffer.GetRepairStats()
				if stats.Attempted > 0 {
					log.Printf("[TOOL-BUFFER] Race attempt %d (internal): Repair stats: attempted=%d, success=%d, failed=%d",
						upstreamReq.id, stats.Attempted, stats.Successful, stats.Failed)
				}
			}

			// Write final chunk with finish_reason before [DONE]
			// This is required by OpenAI streaming format - clients expect finish_reason in the last chunk
			// Use the finish_reason from the event (e.g., "tool_calls" for tool calls, "stop" for normal completion)
			finishReason := event.FinishReason
			if finishReason == "" {
				finishReason = "stop"
			}

			// Validate finish_reason
			validReasons := map[string]bool{"stop": true, "tool_calls": true, "length": true, "content_filter": true}
			if !validReasons[finishReason] {
				log.Printf("[WARN] Invalid finish_reason: %s, defaulting to 'stop'", finishReason)
				finishReason = "stop"
			}

			// Build final chunk - include usage if available from the done event
			finalChunk := map[string]interface{}{
				"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   internalModel,
				"choices": []map[string]interface{}{
					{
						"index":         0,
						"delta":         map[string]interface{}{},
						"finish_reason": finishReason,
					},
				},
			}

			// Inject usage from the full response if available
			// This is critical for clients to track token usage for streaming responses
			if event.Response != nil && (event.Response.Usage.PromptTokens > 0 || event.Response.Usage.CompletionTokens > 0 || event.Response.Usage.TotalTokens > 0) {
				finalChunk["usage"] = map[string]int{
					"prompt_tokens":     event.Response.Usage.PromptTokens,
					"completion_tokens": event.Response.Usage.CompletionTokens,
					"total_tokens":      event.Response.Usage.TotalTokens,
				}

				// Also store usage for retrieval after race completes
				upstreamReq.SetUsage(&TokenUsage{
					PromptTokens:     event.Response.Usage.PromptTokens,
					CompletionTokens: event.Response.Usage.CompletionTokens,
					TotalTokens:      event.Response.Usage.TotalTokens,
				})
			}

			finalData, _ := json.Marshal(finalChunk)
			finalLine := fmt.Sprintf("data: %s\n", finalData)
			if !buf.Add([]byte(finalLine)) {
				return fmt.Errorf("buffer limit exceeded: final chunk for model %s", internalModel)
			}

			// Write [DONE] marker
			if !buf.Add([]byte("data: [DONE]\n")) {
				return fmt.Errorf("buffer limit exceeded: done marker for model %s", internalModel)
			}

			// If provider didn't return usage in the done event, use fallback
			if usage := upstreamReq.GetUsage(); usage == nil || (usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0) {
				if token.FallbackEnabled() {
					tokenizer := token.GetTokenizer()
					promptTokens, err := tokenizer.CountPromptTokens(rawBody, internalModel)
					if err != nil {
						log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, internalModel)
					}
					rawBytes := buf.GetAllRawBytesOnce()
					completionText := token.ExtractCompletionTextFromChunks(rawBytes)
					completionTokens, err := tokenizer.CountCompletionTokens(completionText, internalModel)
					if err != nil {
						log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, internalModel)
					}
					upstreamReq.SetUsage(&TokenUsage{
						PromptTokens:     promptTokens,
						CompletionTokens: completionTokens,
						TotalTokens:      promptTokens + completionTokens,
					})
					log.Printf("[DEBUG][fallback-token-count] internal streaming: model=%s prompt=%d completion=%d total=%d",
						internalModel, promptTokens, completionTokens, promptTokens+completionTokens)
				}
			}

			return nil

		case "error":
			errMsg := "unknown error"
			if event.Error != nil {
				errMsg = event.Error.Error()
			}
			log.Printf("[RACE] Internal provider stream error: %s", errMsg)
			return fmt.Errorf("provider stream error: %s", errMsg)
		}
	}

	// If we get here without "done", the stream ended unexpectedly
	return fmt.Errorf("stream ended without done signal")
}

// convertToProviderRequest converts map[string]interface{} to providers.ChatCompletionRequest
func convertToProviderRequest(body map[string]interface{}, model string) (*providers.ChatCompletionRequest, error) {
	req := &providers.ChatCompletionRequest{}
	req.Model = model

	if messages, ok := body["messages"].([]interface{}); ok {
		for msgIdx, m := range messages {
			if msg, ok := m.(map[string]interface{}); ok {
				chatMsg := providers.ChatMessage{}
				if role, ok := msg["role"].(string); ok {
					chatMsg.Role = role
				}
				if content, ok := msg["content"]; ok {
					switch c := content.(type) {
					case string:
						chatMsg.Content = content
					case []interface{}:
						// Multimodal content - handle each part
						contentParts := make([]providers.ContentPart, len(c))
						for i, part := range c {
							if partMap, ok := part.(map[string]interface{}); ok {
								cp := providers.ContentPart{}
								if partType, ok := partMap["type"].(string); ok {
									cp.Type = partType
								}
								if text, ok := partMap["text"].(string); ok {
									cp.Text = text
								}
								if imageURL, ok := partMap["image_url"].(map[string]interface{}); ok {
									if url, ok := imageURL["url"].(string); ok {
										cp.ImageURL = &providers.ImageURL{
											URL: url,
										}
									}
								}
								contentParts[i] = cp
							}
						}
						chatMsg.Content = contentParts
					}
				}
				if toolCalls, ok := msg["tool_calls"].([]interface{}); ok {
					chatMsg.ToolCalls = make([]providers.ToolCall, len(toolCalls))
					for i, tc := range toolCalls {
						if tcMap, ok := tc.(map[string]interface{}); ok {
							toolCall := providers.ToolCall{}
							if id, ok := tcMap["id"].(string); ok {
								toolCall.ID = id
							}
							if tcType, ok := tcMap["type"].(string); ok {
								toolCall.Type = tcType
							}
							if fn, ok := tcMap["function"].(map[string]interface{}); ok {
								toolCall.Function = providers.ToolCallFunction{}
								if name, ok := fn["name"].(string); ok {
									toolCall.Function.Name = name
								}
								if args, ok := fn["arguments"].(string); ok {
									toolCall.Function.Arguments = args
								}
							}
							chatMsg.ToolCalls[i] = toolCall
						}
					}
				}
				// Handle tool_call_id for tool role messages (required by MiniMax and other providers)
				if toolCallID, ok := msg["tool_call_id"].(string); ok {
					chatMsg.ToolCallID = toolCallID
				}
				// Handle reasoning_content for DeepSeek R1-style thinking models
				if reasoningContent, ok := msg["reasoning_content"].(string); ok {
					chatMsg.ReasoningContent = reasoningContent
				}
				// D1 carry: hydrate MiniMax per-message reasoning_details
				// into ChatMessage.ReasoningDetails (local ReasoningDetailEntry
				// per R3 — pkg/providers does not import pkg/proxy/translator).
				// The field exists for request-side map→struct hydration so
				// the typed openai.go marshaler emits reasoning_details on
				// the wire (P1-8 d). Delegated to providers.HydrateReasoningDetails
				// to eliminate the twin-divergence duplication with the
				// ultimate-internal convertRequest path (M4 dedup).
				chatMsg.ReasoningDetails = providers.HydrateReasoningDetails(msg)
				// Handle name field for preserving message sender identity
				if name, ok := msg["name"].(string); ok {
					chatMsg.Name = name
				}
				// Debug log for tool role messages to diagnose MiniMax compatibility issues
				if chatMsg.Role == "tool" {
					if chatMsg.ToolCallID == "" {
						log.Printf("[WARN] Message[%d] has role='tool' but missing tool_call_id - this may cause MiniMax API error", msgIdx)
					}
				}
				req.Messages = append(req.Messages, chatMsg)
			}
		}
	}

	if temperature, ok := body["temperature"].(float64); ok {
		req.Temperature = &temperature
	}

	if maxTokens, ok := body["max_tokens"].(float64); ok {
		maxTokensInt := int(maxTokens)
		req.MaxTokens = &maxTokensInt
	}

	if stream, ok := body["stream"].(bool); ok {
		req.Stream = stream
	}

	if tools, ok := body["tools"].([]interface{}); ok {
		req.Tools = make([]providers.Tool, len(tools))
		for i, t := range tools {
			if tMap, ok := t.(map[string]interface{}); ok {
				tool := providers.Tool{}
				if toolType, ok := tMap["type"].(string); ok {
					tool.Type = toolType
				}
				if fn, ok := tMap["function"].(map[string]interface{}); ok {
					tool.Function = providers.ToolFunction{}
					if name, ok := fn["name"].(string); ok {
						tool.Function.Name = name
					}
					if desc, ok := fn["description"].(string); ok {
						tool.Function.Description = desc
					}
					if params, ok := fn["parameters"].(map[string]interface{}); ok {
						tool.Function.Parameters = params
					}
				}
				req.Tools[i] = tool
			}
		}
	}

	if toolChoice, exists := body["tool_choice"]; exists {
		req.ToolChoice = translator.TranslateOpenAIToolChoiceToAnthropic(toolChoice)
	}

	if extra, ok := body["extra"].(map[string]interface{}); ok {
		req.Extra = extra
	}

	return req, nil
}

// handleNonStreamingResponse reads a non-streaming JSON response
//
// interleaved is the X-Proxy-Interleaved-Thinking flag carried from
// the race coordinator (via executeRequest → executeExternalRequest).
// P2-4 (a): drives the response-side MiniMax reasoning_details
// translator on the race-external non-stream path. The gate is
// (interleaved && raceExtProviderIsMiniMax(cfg)) — short-circuits
// BEFORE any parse (H5). Gated-off is a pure no-op (preserves
// byte-identical negative-case invariant).
func handleNonStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest, rawBody []byte, interleaved bool) error {
	// Limit body size to 10MB to prevent unbounded memory consumption
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return fmt.Errorf("read error: %w", err)
	}

	// Apply tool repair to non-streaming JSON response if enabled
	if cfg.ToolRepair.Enabled {
		repairedBody, repaired := repairToolCallArgumentsInNonStreamingResponse(body, cfg.ToolRepair)
		if repaired {
			body = repairedBody
			log.Printf("[TOOL-REPAIR] Race attempt %d: repaired malformed tool_call arguments in non-streaming response", req.id)
		}
	}

	// P2-4 (a): race-external non-stream — invoke the response
	// translator on the upstream body BEFORE the final buffer add.
	// Gate is (interleaved && raceExtProviderIsMiniMax(cfg)) — the
	// provider check uses the same helper as the request-side gate
	// (D3). Gate short-circuits BEFORE any parse, so gated-off
	// preserves byte-identical behavior on this verbatim path.
	if interleaved && raceExtProviderIsMiniMax(cfg) {
		translated, terr := translator.TranslateNonStreamResponseBytes(body)
		if terr != nil {
			// §6.1 — non-stream KEEPS error return; caller
			// (race_executor's coordinator) routes to
			// WriteError 502 api_error. We return the wrapped
			// error to surface the parse/shape failure to the
			// client as an upstream error.
			return fmt.Errorf("race-external response translator: %w", terr)
		}
		body = translated
	}

	// Extract usage from response and store it
	var respMap map[string]interface{}
	if err := json.Unmarshal(body, &respMap); err == nil {
		if usageMap, ok := respMap["usage"].(map[string]interface{}); ok {
			promptTokens := intValue(usageMap["prompt_tokens"])
			completionTokens := intValue(usageMap["completion_tokens"])
			totalTokens := intValue(usageMap["total_tokens"])
			if promptTokens > 0 || completionTokens > 0 || totalTokens > 0 {
				req.SetUsage(&TokenUsage{
					PromptTokens:     promptTokens,
					CompletionTokens: completionTokens,
					TotalTokens:      totalTokens,
				})
			}
		}
	}

	// If provider didn't return usage, use fallback token counting
	if usage := req.GetUsage(); usage == nil || (usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0) {
		if token.FallbackEnabled() {
			tokenizer := token.GetTokenizer()
			promptTokens, err := tokenizer.CountPromptTokens(rawBody, req.modelID)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, req.modelID)
			}
			completionText := token.ExtractCompletionTextFromJSON(body)
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, req.modelID)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, req.modelID)
			}
			req.SetUsage(&TokenUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			})
			log.Printf("[DEBUG][fallback-token-count] non-streaming: model=%s prompt=%d completion=%d total=%d",
				req.modelID, promptTokens, completionTokens, promptTokens+completionTokens)
		}
	}

	// Add as single chunk (the non-streaming JSON response)
	if buf := req.GetBuffer(); !buf.Add(body) {
		return fmt.Errorf("buffer limit exceeded: non-streaming response for model %s", req.modelID)
	}

	return nil
}

// getNormalizerDescription returns a human-readable description of what a normalizer fixes
func getNormalizerDescription(normalizerName string) string {
	switch normalizerName {
	case "fix_empty_role":
		return "Fixed empty role field in delta (changed to 'assistant')"
	case "fix_tool_call_index":
		return "Added missing index field to tool_calls"
	case "tool_call_arguments_repair":
		return "Repaired malformed JSON in tool_call arguments"
	default:
		return "Normalized stream chunk"
	}
}

// intValue safely converts an interface{} to int
func intValue(v interface{}) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	default:
		return 0
	}
}

// extractUsageFromSSEChunk extracts usage data from an SSE chunk if present
// The chunk is expected to be in the format: "data: {...json...}\n"
func extractUsageFromSSEChunk(req *upstreamRequest, line []byte) {
	// Check if this is a data line
	const dataPrefix = "data: "
	if len(line) <= len(dataPrefix) {
		return
	}
	if !bytes.HasPrefix(line, []byte(dataPrefix)) {
		return
	}

	// Extract JSON part (skip "data: " prefix)
	jsonPart := line[len(dataPrefix):]

	// Quick filter: skip unmarshaling if chunk likely has no usage data
	// Most chunks don't contain usage/choices fields, so avoid expensive JSON parse
	if !bytes.Contains(jsonPart, []byte(`"usage"`)) && !bytes.Contains(jsonPart, []byte(`"choices"`)) {
		return
	}

	// Try to parse as JSON
	var chunk map[string]interface{}
	if err := json.Unmarshal(jsonPart, &chunk); err != nil {
		return
	}

	// Look for usage field
	usageMap, ok := chunk["usage"].(map[string]interface{})
	if !ok {
		return
	}

	promptTokens := intValue(usageMap["prompt_tokens"])
	completionTokens := intValue(usageMap["completion_tokens"])
	totalTokens := intValue(usageMap["total_tokens"])

	// Only set if we have meaningful usage data
	if promptTokens > 0 || completionTokens > 0 || totalTokens > 0 {
		req.SetUsage(&TokenUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
		})
	}
}

// getKeys returns the keys of a map as a slice
func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// repairToolCallArgumentsInNonStreamingResponse repairs malformed JSON in tool_call arguments
// within a non-streaming JSON response. Returns the (potentially modified) body and whether it was modified.
func repairToolCallArgumentsInNonStreamingResponse(body []byte, config toolrepair.Config) ([]byte, bool) {
	if !config.Enabled {
		return body, false
	}

	repairer := toolrepair.NewRepairer(&config)

	// Try to parse as JSON
	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return body, false
	}

	// Navigate to choices[].message.tool_calls
	choices, ok := resp["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return body, false
	}

	modified := false

	for _, choice := range choices {
		choiceMap, ok := choice.(map[string]interface{})
		if !ok {
			continue
		}

		message, ok := choiceMap["message"].(map[string]interface{})
		if !ok {
			continue
		}

		toolCalls, ok := message["tool_calls"].([]interface{})
		if !ok || len(toolCalls) == 0 {
			continue
		}

		// Process each tool call
		for _, tc := range toolCalls {
			tcMap, ok := tc.(map[string]interface{})
			if !ok {
				continue
			}

			// Get function object
			fn, ok := tcMap["function"].(map[string]interface{})
			if !ok {
				continue
			}

			// Get arguments string
			args, ok := fn["arguments"].(string)
			if !ok || args == "" {
				continue
			}

			// Check if arguments are already valid JSON
			var js interface{}
			if json.Unmarshal([]byte(args), &js) == nil {
				continue
			}

			// Attempt repair
			result := repairer.RepairArguments(args, "")
			if result.Success && result.Repaired != args {
				fn["arguments"] = result.Repaired
				modified = true
			}
		}
	}

	if !modified {
		return body, false
	}

	// Marshal back to JSON
	repaired, err := json.Marshal(resp)
	if err != nil {
		return body, false
	}

	return repaired, true
}

// handleStreamingResponse handles SSE streaming responses
// IMPORTANT: This function does NOT return error on idle timeout.
// Per the unified race retry design, the main request should continue streaming
// even after idle timeout - the coordinator will spawn parallel requests.
// Idle timeout detection is handled by the coordinator via TrackActivity().
//
// interleaved is the X-Proxy-Interleaved-Thinking flag carried from
// the race coordinator (via executeRequest → executeExternalRequest).
// P2-5 (a): drives the response-side StreamTranslator on the
// race-external stream path. The gate is (interleaved &&
// raceExtProviderIsMiniMax(cfg)) — short-circuits BEFORE any parse
// (H5). Gated-off is a pure no-op: the StreamTranslator instance
// is only constructed when the gate fires.
func handleStreamingResponse(ctx context.Context, cfg *ConfigSnapshot, resp *http.Response, req *upstreamRequest, provider string, rawBody []byte, interleaved bool) error {
	// Snapshot the buffer once: Cancel() (client disconnect) releases the
	// buffer field concurrently (cleanup sets it to nil), so this handler
	// must never re-read req.buffer after this point. The snapshot stays
	// valid after Close() — Add returns false, GetAllRawBytesOnce still
	// returns the buffered chunks.
	buf := req.GetBuffer()

	// MEMORY TRAP FIX: Use bufio.Reader with increased buffer instead of bufio.Scanner
	// to avoid issues with long SSE lines and memory retention.
	reader := bufio.NewReaderSize(resp.Body, 64*1024) // 64KB buffer

	sawDone := false

	// Create normalization context for this stream
	normCtx := normalizers.NewContext(provider, fmt.Sprintf("%d", req.id))

	// Reset normalizer state for this new stream to avoid state leakage
	normalizers.GetRegistry().ResetAll(normCtx)

	// Create tool call buffer with integrated repair
	// This replaces the separate accumulator + post-stream repair pattern
	// Repair happens during streaming when tool calls are emitted
	var toolCallBuffer *toolcall.ToolCallBuffer
	if !cfg.ToolCallBufferDisabled && cfg.ToolRepair.Enabled {
		toolCallBuffer = toolcall.NewToolCallBufferWithRepair(
			cfg.ToolCallBufferMaxSize,
			req.modelID,
			fmt.Sprintf("%d", req.id),
			&cfg.ToolRepair,
		)
	} else if !cfg.ToolCallBufferDisabled {
		// Buffer without repair (repair disabled)
		toolCallBuffer = toolcall.NewToolCallBuffer(
			cfg.ToolCallBufferMaxSize,
			req.modelID,
			fmt.Sprintf("%d", req.id),
		)
	}

	// P2-5 (a): race-external stream — construct the StreamTranslator
	// per-stream instance ONLY when the gate fires. Gated-off ⇒ no
	// instance ⇒ no per-chunk work ⇒ byte-identical passthrough.
	// The instance's lifetime is the loop scope (P2-3 / §3.3).
	var streamTranslator *translator.StreamTranslator
	if interleaved && raceExtProviderIsMiniMax(cfg) {
		streamTranslator = translator.NewStreamTranslator()
	}

	for {
		// Set idle timeout for reading
		var line []byte
		var readErr error

		// Setup idle timeout wrapper with configurable timeout
		// Use a longer read timeout to allow the coordinator to detect idle
		readTimeout := time.Duration(cfg.IdleTimeout) * 2 // Double the idle timeout for read
		if readTimeout < 30*time.Second {
			readTimeout = 30 * time.Second // Minimum 30s
		}

		readDone := make(chan struct{})
		go func() {
			line, readErr = reader.ReadBytes('\n')
			close(readDone)
		}()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-readDone:
			// Track activity for coordinator's idle detection
			req.TrackActivity()
			// Check if the request was cancelled to exit promptly
			// This ensures the goroutine exits immediately when Cancel() is called,
			// even if context cancellation hasn't propagated yet
			if req.IsCancelled() {
				return context.Canceled
			}
			// Continuous processing
		case <-time.After(readTimeout):
			// Read timeout - but DON'T return error!
			// The coordinator will detect idle and spawn parallel requests.
			// We continue waiting for the read to complete.
			// This prevents cancelling the main request prematurely.
			log.Printf("[RACE] Request %d: read timeout after %v, continuing to wait...", req.id, readTimeout)
			// Wait for the read to eventually complete or context cancellation
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-readDone:
				req.TrackActivity()
				// Continue processing
			}
		}

		if len(line) > 0 {
			// Remove trailing newline for consistency with scanner
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			// and \r if present
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}

			// IMPORTANT: Apply normalization FIRST
			// This fixes issues like concatenated JSON chunks from providers like MiniMax
			// Example malformed input:  data: {...} {...}
			// Fixed output:             data: {...}\ndata: {...}
			normalizedLine, modified, normalizerName := normalizers.NormalizeWithContextAndName(line, normCtx)
			if modified {
				log.Printf("[DEBUG] Race attempt %d: normalized malformed stream chunk by %s", req.id, normalizerName)

				// Publish event to frontend if event bus is available
				if cfg.EventBus != nil {
					description := getNormalizerDescription(normalizerName)
					cfg.EventBus.Publish(events.Event{
						Type:      "stream_normalize",
						Timestamp: time.Now().Unix(),
						Data: map[string]interface{}{
							"id":          fmt.Sprintf("%d", req.id),
							"normalizer":  normalizerName,
							"provider":    provider,
							"description": description,
						},
					})
				}
			}

			// P2-5 (a) / P2-7: apply the response-side stream
			// translator BEFORE the tool call buffer (W3 ordering
			// — the tool-call buffer sees a chunk already stripped
			// of reasoning_details so it never has to special-case
			// the field). The translator returns (stripped,
			// emitted) — the caller writes stripped first, then
			// each emitted[i] in order, before the next flush
			// boundary. The translator is error-free by
			// construction (§6.1); err is always nil.
			translatorLine := normalizedLine
			var translatorEmitted [][]byte
			if streamTranslator != nil {
				var terr error
				translatorLine, translatorEmitted, terr = streamTranslator.ChunkBytes(normalizedLine)
				if terr != nil {
					// §6.1: should be impossible. Log + continue
					// with the original line.
					log.Printf("[WARN] race-external stream translator: %v", terr)
					translatorLine = normalizedLine
					translatorEmitted = nil
				}
			}

			// Process through tool call buffer (if enabled)
			// The buffer accumulates tool call fragments, repairs when complete, and emits
			// Non-tool-call chunks pass through immediately
			var chunksToEmit [][]byte
			if toolCallBuffer != nil {
				chunksToEmit = toolCallBuffer.ProcessChunk(translatorLine)
			} else {
				chunksToEmit = [][]byte{translatorLine}
			}

			// Prepend the translator's emitted chunks (if any) so
			// the client sees reasoning_content deltas before the
			// stripped upstream chunk on each loop iteration. C1
			// uniform-framing contract: ChunkBytes frames both
			// `stripped` and each `emitted[i]` (data: + \n\n) on
			// the mutated path, and returns the ORIGINAL line
			// VERBATIM (already framed) on the unchanged path.
			// The call site is therefore responsible for ZERO
			// framing here — just concatenate in order. The
			// stripping pattern matches the SSE shape used by
			// the race-external path's downstream consumer.
			if len(translatorEmitted) > 0 {
				full := make([][]byte, 0, len(translatorEmitted)+len(chunksToEmit))
				full = append(full, translatorEmitted...)
				full = append(full, chunksToEmit...)
				chunksToEmit = full
			}

			// Add all chunks to buffer
			// Phase 2 / real-streaming-default — PREDICATE HAZARD FIX
			// (task 2.3): the isStreamErrorChunk check must run BEFORE
			// the Add loop. A stream whose ONLY chunk is an error chunk
			// must not transiently win the first-byte winner gate
			// (`req.GetError()==nil && req.GetBuffer().TotalLen()>0`) —
			// if the error chunk is Added before the error is returned,
			// the predicate fires on it and preempts fallback. Behavior-
			// neutral in buffered mode (failed buffers are never read
			// there); only the on-error raw-dump content at
			// handler.go:1159-1164 changes.
			if isStreamErrorChunk(line) != "" {
				return fmt.Errorf("upstream streamed error chunk: %s", isStreamErrorChunk(line))
			}

			for _, chunk := range chunksToEmit {
				if !buf.Add(chunk) {
					return fmt.Errorf("buffer limit exceeded: streaming tool_call chunk for model %s", req.modelID)
				}
			}

			// Extract usage from SSE chunk if present
			extractUsageFromSSEChunk(req, normalizedLine)

			// Check for [DONE]
			if string(line) == "data: [DONE]" {
				sawDone = true
				break
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return fmt.Errorf("read error: %w", readErr)
		}
	}

	if !sawDone {
		return fmt.Errorf("upstream closed connection prematurely")
	}

	// Flush any remaining buffered tool calls
	// This ensures even incomplete tool calls are emitted at stream end
	if toolCallBuffer != nil {
		flushChunks := toolCallBuffer.Flush()
		for _, chunk := range flushChunks {
			if !buf.Add(chunk) {
				log.Printf("[WARN] Race attempt %d: failed to add flushed tool call chunk (buffer limit exceeded)", req.id)
				break
			}
		}
		if len(flushChunks) > 0 {
			log.Printf("[DEBUG] Race attempt %d: flushed %d buffered tool call chunks at stream end", req.id, len(flushChunks))
		}

		// Log repair stats if any repairs occurred
		stats := toolCallBuffer.GetRepairStats()
		if stats.Attempted > 0 {
			log.Printf("[TOOL-BUFFER] Race attempt %d: Repair stats: attempted=%d, success=%d, failed=%d",
				req.id, stats.Attempted, stats.Successful, stats.Failed)

			// Publish event to frontend if event bus is available
			if cfg.EventBus != nil {
				cfg.EventBus.Publish(events.Event{
					Type:      "tool_repair",
					Timestamp: time.Now().Unix(),
					Data: map[string]interface{}{
						"id":          fmt.Sprintf("%d", req.id),
						"provider":    provider,
						"description": fmt.Sprintf("Repaired %d malformed JSON in tool_call arguments (during streaming)", stats.Successful),
					},
				})
			}
		}
	}

	// If no usage was found during streaming, use fallback
	if usage := req.GetUsage(); usage == nil || (usage.PromptTokens == 0 && usage.CompletionTokens == 0 && usage.TotalTokens == 0) {
		if token.FallbackEnabled() {
			tokenizer := token.GetTokenizer()
			promptTokens, err := tokenizer.CountPromptTokens(rawBody, req.modelID)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting prompt tokens: %v, model=%s", err, req.modelID)
			}
			rawBytes := buf.GetAllRawBytesOnce()
			completionText := token.ExtractCompletionTextFromChunks(rawBytes)
			completionTokens, err := tokenizer.CountCompletionTokens(completionText, req.modelID)
			if err != nil {
				log.Printf("[DEBUG][fallback-token-count] error counting completion tokens: %v, model=%s", err, req.modelID)
			}
			req.SetUsage(&TokenUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			})
			log.Printf("[DEBUG][fallback-token-count] streaming: model=%s prompt=%d completion=%d total=%d",
				req.modelID, promptTokens, completionTokens, promptTokens+completionTokens)
		}
	}

	return nil
}
