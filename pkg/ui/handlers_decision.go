package ui

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/decision"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// =============================================================================
// Decision-model TEST button — POST /fe/api/decisions/test
//
// The chat "Test Connection" button (POST /fe/api/models/test →
// handleTestModel) CANNOT serve decisions:
//
//   - providers.NewProvider("typesafe") errors (the decision
//     adapters live in pkg/decision's private registry, not in the
//     chat provider factory), and
//   - the chat body {messages:[{role:"user",content:"hi"}]} is not
//     a legal decision body.
//
// So this is a NEW SIBLING route. The chat test path is untouched.
//
// The route mirrors the essentials of the /v1/decisions handler
// (pkg/decision/handler.go):
//
//   - model resolution by EXACT ROW ID (the FE sends the row id it
//     has — /v1/decisions resolves by row id too),
//   - the model MUST be a decision-kind model (IsDecision gate),
//   - credential resolution from the model's FIRST credential via an
//     exact-ID credential lookup (test-button convention — see the
//     doc comment at the selection site),
//   - the registry adapter + the typesafe pre-flight check + ONE
//     upstream call + InspectResponse (all inside
//     decision.ExecuteTest — the upstream-call discipline is NOT
//     re-implemented here),
//   - the configured DecisionTimeout (the same knob the decision
//     handler uses — NOT the 5-minute chat client timeout).
//
// The route NEVER dumps the raw upstream body to the FE: a failure
// is classified (status + a short message extracted from the
// upstream envelope) so the FE can render "402 — insufficient
// funds" instead of a raw JSON blob.
// =============================================================================

// decisionTestDefaultTimeout mirrors the decision package's own
// default (pkg/decision/handler.go defaultDecisionTimeout — 30s).
// The config layer clamps DecisionTimeout to [10s, 60s]; this is
// the fallback for a zero-valued knob.
const decisionTestDefaultTimeout = 30 * time.Second

// decisionTestMaxMessageLen caps the upstream message the FE
// receives. The upstream message is a SHORT human-readable string
// ("You exceeded your current quota"); the cap is a belt-and-
// suspenders guard against a pathological upstream body.
const decisionTestMaxMessageLen = 500

// DecisionTestRequest is the FE-facing body of the decision test
// button. Only the row id travels — the BE owns the canned body.
type DecisionTestRequest struct {
	// ModelID is the decision model's ROW id (the FE passes the id
	// it renders in the row).
	ModelID string `json:"model_id"`
}

// DecisionTestResponse is the FE-facing result of a decision test.
//
// The wire shape mirrors the chat test DTO (TestModelResponse) with
// decision-specific fields:
//
//   - success + model + answers (+ usage) on the happy path,
//   - status (+ relay_status / upstream_status) + error on failure.
//
// `status` is the classification status the FE maps to a human
// message ("Insufficient funds", "Rate limited", ...). It is the
// UPSTREAM HTTP status whenever the upstream answered with an error
// status; `relay_status` preserves the decision adapter's
// Outcome.RelayStatus verbatim (both shipped adapters promote every
// error envelope to 502) and `upstream_status` preserves the raw
// upstream status.
type DecisionTestResponse struct {
	Success        bool              `json:"success"`
	Model          string            `json:"model,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	CredentialID   string            `json:"credential_id,omitempty"`
	Answers        []decision.Answer `json:"answers,omitempty"`
	Usage          *decision.Usage   `json:"usage,omitempty"`
	Status         int               `json:"status"`
	RelayStatus    int               `json:"relay_status,omitempty"`
	UpstreamStatus int               `json:"upstream_status,omitempty"`
	Error          string            `json:"error,omitempty"`
	Reason         string            `json:"reason,omitempty"`
}

// decisionTestQuestionBody is one question of the canned body.
type decisionTestQuestionBody struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
}

// decisionTestBody is the canned minimal decision request. The
// `model` field carries the PROXY ROW ID (exactly like a real
// client posting to /v1/decisions); the openai adapter rewrites
// that token to the upstream model name (byte-faithful pass-through,
// pkg/decision/openaidecision.go), and the typesafe adapter
// translates the whole body to System One.
//
// NOTE: safety_identifier is DELIBERATELY ABSENT. It is optional
// on the OpenAI wire, and the typesafe adapter REJECTS any request
// carrying it with a 400 (pkg/decision/validators.go:248) — a
// canned body with it would hard-fail the zen/typesafe arm.
type decisionTestBody struct {
	Model     string                    `json:"model"`
	Input     string                    `json:"input"`
	Questions []decisionTestQuestionBody `json:"questions"`
}

// handleDecisionTest handles POST /fe/api/decisions/test.
//
// Response contract:
//
//   - malformed request (bad JSON / missing model_id) → 400 with the
//     house /fe/api bare {"error": "..."} envelope,
//   - unknown / non-decision model → 404 / 400 with the same bare
//     envelope (a client-side mistake, not an upstream class),
//   - everything else → 200 with a DecisionTestResponse body
//     (success OR a classified failure).
//
// The classified-failure-in-a-200 shape is deliberate: the FE routes
// this call through apiFetch, which throws on a non-2xx status and
// would drop the structured result. A 200 keeps ONE response shape
// on the wire for every outcome the operator cares about.
func (s *Server) handleDecisionTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 64KB (house convention — same cap as
	// the chat test path).
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req DecisionTestRequest
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
	// /v1/decisions resolves the model by row id (the client posts
	// the proxy's model id); the FE test button sends the row id it
	// renders. Decision rows can never be shadow-claimed (a group's
	// shadow_model_id must reference a CHAT-kind model — see
	// pkg/models/group.go:193), so the lookup below is row-exact.
	model := s.modelsConfig.GetModel(modelID)
	if model == nil {
		writeFEError(w, http.StatusNotFound, fmt.Sprintf("Model not found: %s", modelID))
		return
	}
	if !model.IsDecision() {
		writeFEError(w, http.StatusBadRequest,
			fmt.Sprintf("Model %q is not a decision model (kind=%q)", modelID, model.Kind))
		return
	}

	// ---- Credential resolution: FIRST credential, exact-ID lookup --
	// TEST-BUTTON CONVENTION: the probe always uses the model's
	// FIRST credential (credentials[0]) resolved by exact credential
	// id — NOT the credential-LB engine's pick. This mirrors the
	// chat test button's deliberate "primary credential only"
	// convention (pkg/ui/frontend/src/components/config/ModelForm.tsx:391
	// — "Test Connection targets the primary credential only"), so an
	// operator clicking Test validates the credential they see first
	// instead of a rotating engine pick. The production path
	// (ResolveInternalConfigWithAffinity) is untouched.
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
	// model's internal_model (decision models reject peak-hour
	// switching at write time, so no peak-hour branch applies here).
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
	rawBody, typedReq, err := buildDecisionTestBody(modelID)
	if err != nil {
		writeFEError(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to build the decision test request: %v", err))
		return
	}

	// ---- Run the probe ----------------------------------------------
	result := decision.ExecuteTest(resolved, typedReq, rawBody, s.decisionTestTimeout())

	resp := DecisionTestResponse{
		Success:        result.Response != nil && result.Status == http.StatusOK && result.RelayStatus == 0,
		Model:          modelID,
		Provider:       cred.Provider,
		CredentialID:   cred.ID,
		Status:         result.Status,
		RelayStatus:    result.RelayStatus,
		UpstreamStatus: result.UpstreamStatus,
		Reason:         result.Reason,
		Error:          decisionTestUpstreamMessage(result.Body),
	}
	if result.Response != nil {
		// The upstream's own model echo (e.g. "gpt-6-luna") is more
		// informative than the proxy row id — fall back to the row id
		// when the upstream echoed nothing.
		if result.Response.Model != "" {
			resp.Model = result.Response.Model
		}
		resp.Answers = result.Response.Answers
		usage := result.Response.Usage
		resp.Usage = &usage
	}
	if resp.Error == "" && !resp.Success {
		// No upstream message (or no upstream body at all) — fall
		// back to the adapter's reason so the FE always has
		// something to show next to the classification.
		resp.Error = result.Reason
	}

	log.Printf("[decision-test] model=%s credential=%s provider=%s status=%d relay_status=%d upstream_status=%d success=%t",
		modelID, cred.ID, cred.Provider, resp.Status, resp.RelayStatus, resp.UpstreamStatus, resp.Success)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// buildDecisionTestBody builds the canned minimal decision request
// for the given proxy row id.
//
// The body is marshalled ONCE and then parsed into the typed
// decision.DecisionRequest — so the typed `model` field and the raw
// bytes always agree (the openai adapter's byte-faithful model
// rewrite depends on the raw body carrying the model token).
func buildDecisionTestBody(modelID string) ([]byte, *decision.DecisionRequest, error) {
	body := decisionTestBody{
		Model: modelID,
		Input: "Customer wrote: I was charged twice for order #4411 and want a refund.",
		Questions: []decisionTestQuestionBody{
			{
				Type:         "predicate",
				Name:         "is_refund_request",
				Instructions: "Is the customer asking for a refund?",
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, err
	}
	var typed decision.DecisionRequest
	if err := json.Unmarshal(raw, &typed); err != nil {
		return nil, nil, err
	}
	return raw, &typed, nil
}

// decisionTestUpstreamMessage extracts a SHORT human-readable
// message from an upstream error body.
//
// It deliberately NEVER returns the raw body: the FE must render a
// classified message, not a raw dump. Shapes probed (in order):
// the OpenAI envelope {"error":{"message":...}}, a bare
// {"error":"..."}, {"message":"..."} and {"detail":"..."}. An
// unrecognized body yields "" (the FE then falls back to the
// status class alone).
func decisionTestUpstreamMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	// The envelope shape and the plain-string shape need SEPARATE
	// decodes: {"error":{"message":...}} does not decode into a
	// *string field (the whole decode fails), so the plain decode
	// would never see it.
	var envelope struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil {
		if envelope.Error != nil && strings.TrimSpace(envelope.Error.Message) != "" {
			return truncateDecisionTestMessage(envelope.Error.Message)
		}
	}
	var plain struct {
		Error   *string `json:"error"`
		Message string  `json:"message"`
		Detail  string  `json:"detail"`
	}
	if err := json.Unmarshal(body, &plain); err == nil {
		if plain.Error != nil && strings.TrimSpace(*plain.Error) != "" {
			return truncateDecisionTestMessage(*plain.Error)
		}
		if strings.TrimSpace(plain.Message) != "" {
			return truncateDecisionTestMessage(plain.Message)
		}
		if strings.TrimSpace(plain.Detail) != "" {
			return truncateDecisionTestMessage(plain.Detail)
		}
	}
	return ""
}

// truncateDecisionTestMessage caps the extracted upstream message.
func truncateDecisionTestMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) <= decisionTestMaxMessageLen {
		return msg
	}
	return msg[:decisionTestMaxMessageLen] + "…"
}

// decisionTestTimeout returns the per-probe deadline — the SAME
// configured DecisionTimeout the /v1/decisions handler uses (the
// config layer clamps it to [10s, 60s]; the zero-value fallback
// mirrors pkg/decision's own 30s default).
func (s *Server) decisionTestTimeout() time.Duration {
	if s.configMgr == nil {
		return decisionTestDefaultTimeout
	}
	d := s.configMgr.GetDecisionTimeout()
	if d <= 0 {
		return decisionTestDefaultTimeout
	}
	return d
}

// writeFEError writes the house /fe/api bare error envelope
// ({"error": "<string>"}).
func writeFEError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
