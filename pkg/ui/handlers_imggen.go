package ui

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/imggen"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// =============================================================================
// Image-generation TEST button — POST /fe/api/image_generation/test
//
// The chat "Test Connection" button (POST /fe/api/models/test →
// handleTestModel) CANNOT serve image-gen rows:
//
//   - providers.NewProvider("minimax") SUCCEEDS, but it posts a CHAT
//     body to /chat/completions and never touches /image_generation
//     (pkg/providers/openai.go) — the wrong-upstream failure is
//     SILENT (unlike the decision lane, where the "typesafe"
//     provider fails loudly), so the chat path must not be reused,
//   - the chat body {messages:[{role:"user",content:"hi"}]} is not a
//     legal image-generation body.
//
// So this is a NEW SIBLING route. The chat test path is untouched.
//
// The route mirrors the essentials of the /v1/image_generation
// handler (pkg/imggen/handler.go):
//
//   - model resolution by EXACT ROW ID (the FE sends the row id it
//     has — /v1/image_generation resolves by row id too),
//   - the model MUST be an image-gen model (IsImageGen gate — the
//     production /v1/image_generation path has NO kind gate, so
//     without this gate a chat-kind row would be silently forwarded
//     to the image-generation upstream),
//   - credential resolution from the model's FIRST credential via an
//     exact-ID credential lookup (test-button convention — see the
//     doc comment at the selection site),
//   - the registry adapter + ONE upstream call + InspectResponse (all
//     inside imggen.ExecuteTest — the upstream-call discipline is NOT
//     re-implemented here),
//   - the configured ImageGenTimeout (the same knob the image-gen
//     handler uses — NOT the decision lane's 30s: an image
//     generation legitimately takes 17-60s).
//
// The route NEVER dumps the raw upstream body to the FE: only the
// enumerated fields (the image URLs, the counts, the short upstream
// message, the adapter's reason) cross. A failure is classified
// (status + a code + a short message) so the FE can render "402 —
// insufficient funds" instead of a raw JSON blob.
// =============================================================================

// imgGenTestDefaultTimeout mirrors the imggen package's own default
// (pkg/imggen/handler.go defaultImageGenTimeout — 120s). The config
// layer clamps ImageGenTimeout to [30s, 4min]; this is the fallback
// for a zero-valued knob.
//
// It is DELIBERATELY NOT the decision lane's 30s: an image
// generation can legitimately be silent for 17-60s, and a 30s probe
// deadline would report false failures.
const imgGenTestDefaultTimeout = 120 * time.Second

// imgGenTestMaxMessageLen caps the upstream message the FE receives.
// The upstream message is a SHORT human-readable string ("invalid
// parameters"); the cap is a belt-and-suspenders guard against a
// pathological upstream body.
const imgGenTestMaxMessageLen = 500

// imgGenTestMaxBodyBytes caps the FE request body (house convention —
// the same 64KB cap as the decision / chat test paths). The FE sends
// only {"model_id": "..."}.
const imgGenTestMaxBodyBytes = 64 * 1024

// imgGenTestPrompt is the canned prompt of the probe. It is
// deliberately trivial (a single object on a plain background) so
// the generation is cheap and fast and the rendering is
// predictable. The BE owns the body — the FE sends only the row id.
const imgGenTestPrompt = "a small red apple on a plain white background"

// imgGenTestProbeGate is the process-wide single-flight guard for
// the image-generation test probe (one probe at a time).
type imgGenTestProbeGate struct {
	mu       sync.Mutex
	inFlight bool
}

// imgGenTestProbe is the process-wide SINGLE-FLIGHT guard for the
// image-generation test probe.
//
// Why it exists: a probe is a REAL, BILLABLE image generation. The
// FE disables the button of the row being probed (per-row busy
// flag), but a row storm (an operator clicking five rows) would
// fire five concurrent paid generations.
//
// The contract is deliberately the simplest one that cannot bill
// twice by accident: ONE probe at a time, process-wide. A second
// concurrent probe is NOT queued — it is rejected immediately with
// {"success": false, "code": "busy"} so the operator sees a clear
// "already running" state instead of a silent wait.
//
// This supersedes the architecture doc's per-row cap + cooldown
// design (simpler, and it needs no per-row state).
var imgGenTestProbe imgGenTestProbeGate

// acquire takes the probe slot. It returns false when another
// probe is already in flight (the caller answers "busy").
func (p *imgGenTestProbeGate) acquire() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inFlight {
		return false
	}
	p.inFlight = true
	return true
}

// release frees the probe slot.
func (p *imgGenTestProbeGate) release() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inFlight = false
}

// ImgGenTestRequest is the FE-facing body of the image-gen test
// button. Only the row id travels — the BE owns the canned body.
type ImgGenTestRequest struct {
	// ModelID is the image-gen model's ROW id (the FE passes the id
	// it renders in the row).
	ModelID string `json:"model_id"`
}

// ImgGenTestResponse is the FE-facing result of an image-gen test.
//
// The wire shape mirrors the decision test DTO (DecisionTestResponse)
// with image-specific fields:
//
//   - success + model + image_urls (+ the counts) on the happy path,
//   - status (+ relay_status / upstream_status) + code + error on
//     failure.
//
// `status` is the classification status the FE maps to a human
// message ("Insufficient funds", "Rate limited", ...). `code` is the
// STABLE machine-readable slug of the class (the FE prefers it over
// the status label when it knows it). `relay_status` preserves the
// adapter's Outcome.RelayStatus verbatim and `upstream_status` the
// raw upstream status.
//
// The image URLs are the upstream's own CDN locators, relayed
// VERBATIM so the FE can render <img src> directly — no second
// proxy hop, no base64, no server-side image fetch. Their lifetime
// is not recorded anywhere in the repo: the FE renders them
// immediately.
type ImgGenTestResponse struct {
	Success        bool     `json:"success"`
	Model          string   `json:"model,omitempty"`
	Provider       string   `json:"provider,omitempty"`
	CredentialID   string   `json:"credential_id,omitempty"`
	ImageURLs      []string `json:"image_urls,omitempty"`
	ImageCount     int      `json:"image_count,omitempty"`
	SuccessCount   int      `json:"success_count,omitempty"`
	FailedCount    int      `json:"failed_count,omitempty"`
	Status         int      `json:"status"`
	RelayStatus    int      `json:"relay_status,omitempty"`
	UpstreamStatus int      `json:"upstream_status,omitempty"`
	Code           string   `json:"code,omitempty"`
	Error          string   `json:"error,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	DurationMS     int64    `json:"duration_ms,omitempty"`
}

// imgGenTestBody is the canned minimal image-generation request.
//
// The `model` field carries the PROXY ROW ID (exactly like a real
// client posting to /v1/image_generation); the MiniMax adapter
// rewrites that token to the upstream alias (InternalModel) and
// appends "/image_generation" to the base URL.
//
// `response_format: "url"` is pinned: the upstream answers with
// CDN URLs (not base64 blobs), and the FE renders those URLs
// directly — the payload stays tiny and no image bytes travel
// through the proxy.
type imgGenTestBody struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	AspectRatio    string `json:"aspect_ratio"`
	N              int    `json:"n"`
	ResponseFormat string `json:"response_format"`
}

// handleImgGenTest handles POST /fe/api/image_generation/test.
//
// Response contract:
//
//   - malformed request (bad JSON / missing model_id) → 400 with the
//     house /fe/api bare {"error": "..."} envelope,
//   - unknown / non-image-gen model → 404 / 400 with the same bare
//     envelope (a client-side mistake, not an upstream class),
//   - a probe that is already in flight → 200 with
//     {"success": false, "code": "busy"} (the FE renders the class),
//   - everything else → 200 with an ImgGenTestResponse body
//     (success OR a classified failure).
//
// The classified-failure-in-a-200 shape is deliberate: the FE routes
// this call through apiFetch, which throws on a non-2xx status and
// would drop the structured result. A 200 keeps ONE response shape
// on the wire for every outcome the operator cares about.
func (s *Server) handleImgGenTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 64KB (house convention — same cap as the
	// chat / decision test paths).
	r.Body = http.MaxBytesReader(w, r.Body, imgGenTestMaxBodyBytes)

	var req ImgGenTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeFEError(w, http.StatusBadRequest, fmt.Sprintf("Invalid JSON: %v", err))
		return
	}

	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		writeFEError(w, http.StatusBadRequest, "model_id is required")
		return
	}

	// ---- Model resolution: EXACT ROW ID ----------------------------
	// /v1/image_generation resolves the model by row id (the client
	// posts the proxy's model id); the FE test button sends the row
	// id it renders.
	model := s.modelsConfig.GetModel(modelID)
	if model == nil {
		writeFEError(w, http.StatusNotFound, fmt.Sprintf("Model not found: %s", modelID))
		return
	}
	// ---- KIND GATE (MANDATORY) --------------------------------------
	// The production /v1/image_generation path has NO kind gate
	// (pkg/imggen/handler.go resolves the model and forwards it).
	// Without this gate a CHAT-kind row would be silently forwarded
	// to the image-generation upstream — a quiet failure in the
	// wrong place. The gate fires BEFORE any credential resolution
	// and before any upstream call.
	if !model.IsImageGen() {
		writeFEError(w, http.StatusBadRequest,
			fmt.Sprintf("Model %q is not an image-gen model (kind=%q)", modelID, model.Kind))
		return
	}

	// ---- Credential resolution: FIRST credential, exact-ID lookup --
	// TEST-BUTTON CONVENTION: the probe always uses the model's
	// FIRST credential (credentials[0]) resolved by exact credential
	// id — NOT the credential-LB engine's pick. This mirrors the
	// chat + decision test buttons' deliberate "primary credential
	// only" convention, so an operator clicking Test validates the
	// credential they see first instead of a rotating engine pick.
	// The production path (ResolveInternalConfigWithAffinity) is
	// untouched.
	if len(model.Credentials) == 0 {
		writeFEError(w, http.StatusBadRequest,
			fmt.Sprintf("Model %q has no credentials configured", modelID))
		return
	}
	credID := model.Credentials[0].CredentialID
	cred := s.modelsConfig.GetCredential(credID)
	if cred == nil {
		writeFEError(w, http.StatusNotFound,
			fmt.Sprintf("Credential not found: %s", credID))
		return
	}
	if cred.APIKey == "" {
		writeFEError(w, http.StatusBadRequest,
			"credential has no API key (decryption may have failed - try re-saving the credential)")
		return
	}

	// The upstream shape mirrors resolveWithCredential
	// (pkg/store/database/store.go): provider + api key come from
	// the credential, the base URL is the model override first and
	// the credential second, and the upstream model name is the
	// model's internal_model.
	baseURL := model.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}
	resolved := models.ResolvedCredential{
		Provider:      cred.Provider,
		APIKey:        cred.APIKey,
		BaseURL:       baseURL,
		InternalModel: model.InternalModel,
		CredentialID:  cred.ID,
	}

	// ---- Canned body ------------------------------------------------
	rawBody, err := buildImgGenTestBody(modelID)
	if err != nil {
		writeFEError(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to build the image-gen test request: %v", err))
		return
	}

	// ---- Single-flight guard -----------------------------------------
	// One probe at a time, process-wide. The loser is rejected
	// immediately (never queued) with a classified "busy" outcome.
	if !imgGenTestProbe.acquire() {
		resp := ImgGenTestResponse{
			Success:      false,
			Model:        modelID,
			Provider:     cred.Provider,
			CredentialID: cred.ID,
			Status:       http.StatusTooManyRequests,
			Code:         imggen.CodeBusy,
			Error:        "another image-generation test is already running — wait for it to finish",
		}
		log.Printf("[imggen-test] model=%s credential=%s provider=%s status=%d code=%s success=%t err=%q",
			modelID, cred.ID, cred.Provider, resp.Status, resp.Code, resp.Success, resp.Error)
		writeImgGenTestJSON(w, resp)
		return
	}
	defer imgGenTestProbe.release()

	// ---- Run the probe ----------------------------------------------
	start := time.Now()
	result := imggen.ExecuteTest(resolved, rawBody, s.imgGenTestTimeout())
	durationMS := time.Since(start).Milliseconds()

	resp := ImgGenTestResponse{
		Success:        result.Status == http.StatusOK && result.Code == imggen.CodeOK && result.ImageCount > 0,
		Model:          modelID,
		Provider:       cred.Provider,
		CredentialID:   cred.ID,
		ImageURLs:      result.ImageURLs,
		ImageCount:     result.ImageCount,
		SuccessCount:   result.SuccessCount,
		FailedCount:    result.FailedCount,
		Status:         result.Status,
		RelayStatus:    result.RelayStatus,
		UpstreamStatus: result.UpstreamStatus,
		Code:           result.Code,
		Error:          truncateImgGenTestMessage(result.Error),
		Reason:         truncateImgGenTestMessage(result.Reason),
		DurationMS:     durationMS,
	}

	log.Printf("[imggen-test] model=%s credential=%s provider=%s status=%d relay_status=%d upstream_status=%d code=%s images=%d duration_ms=%d success=%t err=%q",
		modelID, cred.ID, cred.Provider, resp.Status, resp.RelayStatus, resp.UpstreamStatus, resp.Code,
		resp.ImageCount, resp.DurationMS, resp.Success, resp.Error)

	writeImgGenTestJSON(w, resp)
}

// buildImgGenTestBody builds the canned minimal image-generation
// request for the given proxy row id.
func buildImgGenTestBody(modelID string) ([]byte, error) {
	body := imgGenTestBody{
		Model:          modelID,
		Prompt:         imgGenTestPrompt,
		AspectRatio:    "1:1",
		N:              1,
		ResponseFormat: "url",
	}
	return json.Marshal(body)
}

// truncateImgGenTestMessage caps the upstream message AND the
// adapter reason the FE receives. The upstream message is a SHORT
// human-readable string and the reason is normally a short slug;
// the cap is the belt-and-suspenders guard on the BE side (the FE
// caps again at 300). The reason is upstream-INFLUENCED (the
// adapter quotes an upstream-controlled token into it), so it MUST
// be capped at this boundary too.
func truncateImgGenTestMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) <= imgGenTestMaxMessageLen {
		return msg
	}
	return msg[:imgGenTestMaxMessageLen] + "…"
}

// imgGenTestTimeout returns the per-probe deadline — the SAME
// configured ImageGenTimeout the /v1/image_generation handler uses
// (the config layer clamps it to [30s, 4min]; the zero-value
// fallback mirrors pkg/imggen's own 120s default).
func (s *Server) imgGenTestTimeout() time.Duration {
	if s.configMgr == nil {
		return imgGenTestDefaultTimeout
	}
	d := s.configMgr.GetImageGenTimeout()
	if d <= 0 {
		return imgGenTestDefaultTimeout
	}
	return d
}

// writeImgGenTestJSON writes the classified result with HTTP 200 (the
// one-shape contract every upstream-classified outcome uses).
func writeImgGenTestJSON(w http.ResponseWriter, resp ImgGenTestResponse) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
