package decision

// Decision Models commission / §9.2 (finding 12): NEW test for
// /v1/decisions misroute guard. The route has pkg/decision's own
// explicit `IsDecision()` reject at HandleDecision (closes the
// imggen reverse-direction gap). Confirms:
//   - decision resolver returns 400 + OpenAI envelope code
//     "decision_misroute" when a chat/image-gen model is posted
//     to /v1/decisions
//   - unknown model posted → 404 (not 400)
//   - chat model posted to /v1/decisions: 400 (decisions only)
//   - image-gen model posted to /v1/decisions: 400 (decisions only)
//   - decision model posted to /v1/decisions: passes guard,
//     hits empty provider registry (P1) → 502 (documented P1
//     exit criterion)

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// stubConfigMgr satisfies the Handler.configGetter seam with a
// deterministic 30s DecisionTimeout. The handler only reads
// GetDecisionTimeout(); time.Duration per the handler seam.
type stubConfigMgr struct{}

func (s *stubConfigMgr) GetDecisionTimeout() time.Duration { return 30 * time.Second }

// makeDecisionHandler builds a decision.Handler wired with a
// model resolver covering the supplied model and a credential
// resolver returning a stub OpenAI credential. Auth is disabled
// (tokenStore nil); the provider registry is EMPTY by design
// (P1 exit criterion).
//
// `kind` is the ModelConfig.Kind value (one of "" / models.KindChat
// / models.KindImageGen / models.KindDecision) as a raw string —
// the constants are untyped here so we cast at call sites below.
func makeDecisionHandler(t *testing.T, modelID, kind string) *Handler {
	t.Helper()

	getModel := func(id string) *models.ModelConfig {
		if id != modelID {
			return nil
		}
		mc := &models.ModelConfig{
			ID:      modelID,
			Name:    modelID,
			Enabled: true,
			Kind:    kind,
		}
		if kind != "" {
			mc.Internal = true
			mc.Credentials = []models.CredentialRef{{CredentialID: "openai-cred", Weight: 1, Position: 0}}
		}
		if kind == models.KindDecision {
			mc.InternalModel = "gpt-6-luna"
			mc.ExcludeFromUltimateSwitching = true
		}
		if kind == models.KindImageGen {
			mc.InternalModel = "image-01"
		}
		if kind == models.KindChat {
			mc.InternalModel = "gpt-4"
		}
		return mc
	}

	resolveCred := func(id string) (models.ResolvedCredential, bool) {
		if id != modelID {
			return models.ResolvedCredential{}, false
		}
		return models.ResolvedCredential{
			Provider:      "openai",
			APIKey:        "k",
			InternalModel: "gpt-6-luna",
		}, true
	}

	h := NewHandler(&stubConfigMgr{}, nil, nil, nil)
	h.SetModelResolver(NewModelResolverFromConfig(getModel))
	h.SetCredentialResolver(NewCredentialResolverFromConfig(resolveCred))
	return h
}

// decisionRequestBody builds a wire-shape POST body for
// /v1/decisions. The body is parsed via json.Unmarshal into a
// map; the `model` field is the only field the handler reads
// at P1 (typed parse is P2).
func decisionRequestBody(model string) string {
	return fmt.Sprintf(`{"model": %q, "input": "test"}`, model)
}

// TestDecision_Resolver_DecisionKind_404Unknown — unknown
// model posted to /v1/decisions returns 404 (not 400). The
// `IsDecision()` guard fires only AFTER model resolve; an
// unknown model must surface as a real 404 not a 400 masking
// the misconfig.
func TestDecision_Resolver_DecisionKind_404Unknown(t *testing.T) {
	h := makeDecisionHandler(t, "some-model", models.KindDecision)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(decisionRequestBody("not-configured-model")))
	req.Header.Set("Content-Type", "application/json")
	h.HandleDecision(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown model: status = %d, want 404 (the IsDecision() guard must not fire pre-resolve); body=%s", rec.Code, rec.Body.String())
	}
}

// TestDecision_Resolver_ChatModelMisroute_400_DecisionMisroute —
// chat model posted to /v1/decisions ⇒ 400 + OpenAI envelope
// code "decision_misroute". The `IsDecision()` reverse-
// direction guard fires AFTER resolve (we have a configured
// model) and BEFORE auth/upstream. Closes the imggen
// reverse-direction gap that imggen itself never closed.
func TestDecision_Resolver_ChatModelMisroute_400_DecisionMisroute(t *testing.T) {
	h := makeDecisionHandler(t, "chat-1", models.KindChat)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(decisionRequestBody("chat-1")))
	req.Header.Set("Content-Type", "application/json")
	h.HandleDecision(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("chat model on /v1/decisions: status = %d, want 400 (IsDecision() guard); body=%s", rec.Code, rec.Body.String())
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v; body=%s", err, rec.Body.String())
	}
	errObj, ok := parsed["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no 'error' object: %s", rec.Body.String())
	}
	code, _ := errObj["code"].(string)
	if code != "decision_misroute" {
		t.Errorf("error.code = %q, want %q", code, "decision_misroute")
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "/v1/decisions") {
		t.Errorf("error.message = %q, want it to mention /v1/decisions", msg)
	}
}

// TestDecision_Resolver_ImageGenModelMisroute_400_DecisionMisroute —
// image-gen model posted to /v1/decisions → 400 (NOT 502).
// Imggen models are served by /v1/image_generation, not here.
func TestDecision_Resolver_ImageGenModelMisroute_400_DecisionMisroute(t *testing.T) {
	h := makeDecisionHandler(t, "img-1", models.KindImageGen)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(decisionRequestBody("img-1")))
	req.Header.Set("Content-Type", "application/json")
	h.HandleDecision(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("image-gen model on /v1/decisions: status = %d, want 400 (IsDecision() guard catches BOTH chat + image-gen kinds); body=%s", rec.Code, rec.Body.String())
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v; body=%s", err, rec.Body.String())
	}
	errObj, ok := parsed["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no 'error' object: %s", rec.Body.String())
	}
	code, _ := errObj["code"].(string)
	if code != "decision_misroute" {
		t.Errorf("error.code = %q, want %q", code, "decision_misroute")
	}
}

// TestDecision_Resolver_DecisionModel_PassesGuard_502OnRegistry —
// decision model posted to /v1/decisions PASSES the IsDecision()
// guard AND the model-already-exists check, then hits the empty
// provider registry (P1 design) → 502 "no decision adapter
// registered for provider". This is the documented P1 exit
// criterion — the route is live but returns 502 until P2
// registers openai + typesafe.
func TestDecision_Resolver_DecisionModel_PassesGuard_502OnRegistry(t *testing.T) {
	h := makeDecisionHandler(t, "dec-1", models.KindDecision)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(decisionRequestBody("dec-1")))
	req.Header.Set("Content-Type", "application/json")
	h.HandleDecision(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("decision model on /v1/decisions (P1): status = %d, want 502 (empty provider registry); body=%s", rec.Code, rec.Body.String())
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body not JSON: %v; body=%s", err, rec.Body.String())
	}
	errObj, ok := parsed["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("body has no 'error' object: %s", rec.Body.String())
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "openai") {
		t.Errorf("error.message = %q, want it to mention the provider (openai) that has no registered adapter", msg)
	}
}

// TestDecision_MethodGuard_405 — non-POST returns 405 via
// http.Error (no envelope, house style). Plain text body.
func TestDecision_MethodGuard_405(t *testing.T) {
	h := NewHandler(&stubConfigMgr{}, nil, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/decisions", nil)
	h.HandleDecision(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET on /v1/decisions: status = %d, want 405", rec.Code)
	}
}

// TestDecision_NoResolversInstalled_404 — sanity: constructing
// a Handler with no resolvers installed (defaults left nil)
// does not panic on a POST. The model lookup yields nil →
// 404 (OpenAI envelope).
func TestDecision_NoResolversInstalled_404(t *testing.T) {
	h := NewHandler(&stubConfigMgr{}, nil, nil, nil)
	// do NOT install resolvers; default stubs return ok=false.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader(decisionRequestBody("any-model")))
	req.Header.Set("Content-Type", "application/json")
	h.HandleDecision(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("nil resolver: status = %d, want 404 (model lookup yields nil); body=%s", rec.Code, rec.Body.String())
	}
}
