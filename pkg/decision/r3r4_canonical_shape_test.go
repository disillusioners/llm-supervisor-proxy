// r3r4_canonical_shape_test.go — R3/R4 CLOSURE (2026-11-08).
//
// Reproduces and locks the canonical System One wire shape on the
// REQUEST side: choice criteria = top-level string map (label →
// description string); score criteria = top-level ordered string
// array; noul/predicate = no criteria. A shape-asserting mock
// upstream rejects the previous nested-under-"choice"/"score"
// object-valued shape (422) and accepts only the canonical shape
// (200). Driven through the proxy decision path for BOTH
// providers (typesafe + openrouter share the adapter).
package decision

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// validateSystemOneShape rejects any request body whose questions do
// not use the canonical top-level criteria shapes. Returns nil only
// when EVERY question conforms.
func validateSystemOneShape(body []byte) error {
	var req struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("unparseable request: %w", err)
	}
	for name, raw := range req.Questions {
		var q map[string]json.RawMessage
		if err := json.Unmarshal(raw, &q); err != nil {
			return fmt.Errorf("question %q unparseable: %w", name, err)
		}
		typ := rawString(q["type"])
		switch typ {
		case "noul", "predicate":
			if _, ok := q["criteria"]; ok {
				return fmt.Errorf("question %q (%s): must not carry criteria", name, typ)
			}
		case "choice":
			if _, ok := q["choice"]; ok {
				return fmt.Errorf("question %q (choice): criteria nested under 'choice' (rejected shape)", name)
			}
			critRaw, ok := q["criteria"]
			if !ok {
				return fmt.Errorf("question %q (choice): missing top-level criteria", name)
			}
			var crit map[string]string
			if err := json.Unmarshal(critRaw, &crit); err != nil {
				return fmt.Errorf("question %q (choice): criteria must be map[string]string, got %v", name, err)
			}
		case "score":
			if _, ok := q["score"]; ok {
				return fmt.Errorf("question %q (score): criteria nested under 'score' (rejected shape)", name)
			}
			critRaw, ok := q["criteria"]
			if !ok {
				return fmt.Errorf("question %q (score): missing top-level criteria", name)
			}
			var crit []string
			if err := json.Unmarshal(critRaw, &crit); err != nil {
				return fmt.Errorf("question %q (score): criteria must be []string, got %v", name, err)
			}
		default:
			return fmt.Errorf("question %q: unknown type %q", name, typ)
		}
	}
	return nil
}

func rawString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// newShapeMockUpstream returns an httptest server that accepts only
// the canonical System One wire shape (200 + success body) and
// rejects everything else with 422. The mock is the LOAD-BEARING
// assertion: it validates shape rather than blindly returning 200.
func newShapeMockUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := validateSystemOneShape(body); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		answers := map[string]interface{}{
			"is_x":        map[string]interface{}{"answer": true, "confidence": 0.9},
			"dept":        map[string]interface{}{"answer": "billing", "confidence": 0.8},
			"frustration": map[string]interface{}{"answer": "calm", "confidence": 0.7},
		}
		w.Write(systemOneSuccessBody(answers, 10, 0))
	}))
}

// newEnvForMockUpstream mirrors newEnvForProvider but points the
// credential at an externally supplied upstream URL (the shape mock).
func newEnvForMockUpstream(t *testing.T, provider, internalModel, upstreamURL string) *handlerTestEnv {
	t.Helper()
	db, dbCleanup := newDecisionUsageTestDB(t)
	uc := usage.NewCounter(db, database.SQLite)
	bus := events.NewBus()
	ts := newMemTokenStore()
	h := NewHandler(stubConfig{timeout: 2 * time.Second}, bus, ts, uc)

	modelID := "decision-" + provider
	h.SetModelResolver(func(id string) (*models.ModelConfig, bool) {
		if id != modelID {
			return nil, false
		}
		return &models.ModelConfig{
			ID:            modelID,
			Name:          "Decision " + provider,
			Kind:          models.KindDecision,
			Enabled:       true,
			Internal:      true,
			InternalModel: internalModel,
			Credentials: []models.CredentialRef{
				{CredentialID: "cred-1", Weight: 1, Position: 0},
			},
		}, true
	})
	h.SetCredentialResolver(func(id string) (models.ResolvedCredential, bool) {
		if id != modelID {
			return models.ResolvedCredential{}, false
		}
		return models.ResolvedCredential{
			Provider:      provider,
			APIKey:        "sk-cred-secret",
			BaseURL:       upstreamURL,
			InternalModel: internalModel,
			CredentialID:  "cred-1",
		}, true
	})

	tok := &auth.AuthToken{
		ID:        "tok-test",
		Name:      "Test Token",
		TokenHash: auth.HashToken("sk-test-token"),
	}
	ts.add(tok)

	return &handlerTestEnv{
		t:          t,
		handler:    h,
		bus:        bus,
		usage:      uc,
		tokenStore: ts,
		modelID:    modelID,
		provider:   provider,
		dbCleanup:  dbCleanup,
	}
}

// TestR3R4_CanonicalShape_PredicateAccepted — noul path (already
// working live) must stay green: no criteria emitted.
func TestR3R4_CanonicalShape_PredicateAccepted(t *testing.T) {
	mock := newShapeMockUpstream(t)
	defer mock.Close()
	env := newEnvForMockUpstream(t, "typesafe", "jev-1.13.0", mock.URL)
	defer env.dbCleanup()

	body := mustJSON(map[string]interface{}{
		"model": env.modelID,
		"input": "x",
		"questions": []interface{}{
			map[string]interface{}{"type": "predicate", "name": "is_x", "instructions": "is it true?"},
		},
	})
	rec := env.sendRequest(http.MethodPost, string(body), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("predicate (noul) should be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestR3R4_CanonicalShape_ChoiceObjectAccepted — object-valued
// internal representation canonicalizes to top-level string map.
func TestR3R4_CanonicalShape_ChoiceObjectAccepted(t *testing.T) {
	mock := newShapeMockUpstream(t)
	defer mock.Close()
	env := newEnvForMockUpstream(t, "typesafe", "jev-1.13.0", mock.URL)
	defer env.dbCleanup()

	body := mustJSON(map[string]interface{}{
		"model": env.modelID,
		"input": "x",
		"questions": []interface{}{
			map[string]interface{}{
				"type":         "choice",
				"name":         "dept",
				"instructions": "where?",
				"choices": []interface{}{
					map[string]interface{}{"value": "billing", "description": "Billing/refunds"},
					map[string]interface{}{"value": "technical", "description": "Tech support"},
				},
			},
		},
	})
	rec := env.sendRequest(http.MethodPost, string(body), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("choice (object form) should be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestR3R4_CanonicalShape_ChoiceFlatAccepted — flat label-only
// internal representation canonicalizes to top-level string map
// with empty description values.
func TestR3R4_CanonicalShape_ChoiceFlatAccepted(t *testing.T) {
	mock := newShapeMockUpstream(t)
	defer mock.Close()
	env := newEnvForMockUpstream(t, "typesafe", "jev-1.13.0", mock.URL)
	defer env.dbCleanup()

	body := mustJSON(map[string]interface{}{
		"model": env.modelID,
		"input": "x",
		"questions": []interface{}{
			map[string]interface{}{
				"type":         "choice",
				"name":         "dept",
				"instructions": "where?",
				"choices": []interface{}{
					map[string]interface{}{"value": "billing"},
					map[string]interface{}{"value": "technical"},
				},
			},
		},
	})
	rec := env.sendRequest(http.MethodPost, string(body), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("choice (flat form) should be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestR3R4_CanonicalShape_ScoreObjectAccepted — object-valued
// internal representation canonicalizes to top-level string array.
func TestR3R4_CanonicalShape_ScoreObjectAccepted(t *testing.T) {
	mock := newShapeMockUpstream(t)
	defer mock.Close()
	env := newEnvForMockUpstream(t, "typesafe", "jev-1.13.0", mock.URL)
	defer env.dbCleanup()

	body := mustJSON(map[string]interface{}{
		"model": env.modelID,
		"input": "x",
		"questions": []interface{}{
			map[string]interface{}{
				"type":         "score",
				"name":         "frustration",
				"instructions": "rate anger",
				"levels": []interface{}{
					map[string]interface{}{"label": "calm", "description": "Calm"},
					map[string]interface{}{"label": "angry", "description": "Angry"},
				},
			},
		},
	})
	rec := env.sendRequest(http.MethodPost, string(body), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("score (object form) should be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestR3R4_CanonicalShape_ScoreFlatAccepted — flat label-only
// internal representation canonicalizes to top-level string array.
func TestR3R4_CanonicalShape_ScoreFlatAccepted(t *testing.T) {
	mock := newShapeMockUpstream(t)
	defer mock.Close()
	env := newEnvForMockUpstream(t, "typesafe", "jev-1.13.0", mock.URL)
	defer env.dbCleanup()

	body := mustJSON(map[string]interface{}{
		"model": env.modelID,
		"input": "x",
		"questions": []interface{}{
			map[string]interface{}{
				"type":         "score",
				"name":         "frustration",
				"instructions": "rate anger",
				"levels": []interface{}{
					map[string]interface{}{"label": "calm"},
					map[string]interface{}{"label": "angry"},
				},
			},
		},
	})
	rec := env.sendRequest(http.MethodPost, string(body), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("score (flat form) should be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestR3R4_CanonicalShape_BothProviders — shared adapter serves
// typesafe + openrouter identically.
func TestR3R4_CanonicalShape_BothProviders(t *testing.T) {
	for _, prov := range []string{"typesafe", "openrouter"} {
		prov := prov
		t.Run(prov, func(t *testing.T) {
			mock := newShapeMockUpstream(t)
			defer mock.Close()
			env := newEnvForMockUpstream(t, prov, "typesafe/jev-1.13", mock.URL)
			defer env.dbCleanup()

			body := mustJSON(map[string]interface{}{
				"model": env.modelID,
				"input": "x",
				"questions": []interface{}{
					map[string]interface{}{"type": "predicate", "name": "is_x", "instructions": "is it true?"},
					map[string]interface{}{
						"type": "choice", "name": "dept", "instructions": "where?",
						"choices": []interface{}{
							map[string]interface{}{"value": "billing", "description": "Billing/refunds"},
							map[string]interface{}{"value": "technical", "description": "Tech support"},
						},
					},
					map[string]interface{}{
						"type": "score", "name": "frustration", "instructions": "rate",
						"levels": []interface{}{
							map[string]interface{}{"label": "calm"},
							map[string]interface{}{"label": "angry"},
						},
					},
				},
			})
			rec := env.sendRequest(http.MethodPost, string(body), nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("[%s] combined request should be accepted, got %d: %s", prov, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestR3R4_CanonicalShape_RejectsNestedShape — a direct
// unit-level guard: if translateQuestion ever regresses to the
// nested-under-choice/score shape, this fails. Drives
// BuildUpstream and inspects the emitted bytes.
func TestR3R4_CanonicalShape_RejectsNestedShape(t *testing.T) {
	p := NewTypesafeProvider()
	req := &DecisionRequest{
		Model: "x", Input: json.RawMessage(`"x"`),
		Questions: []Question{
			{Type: QuestionTypeChoice, Name: "dept", Instructions: "?",
				Choices: []ChoiceOption{{Value: "billing", Description: "Billing/refunds"}, {Value: "technical", Description: "Tech support"}}},
			{Type: QuestionTypeScore, Name: "frustration", Instructions: "rate",
				Levels: []ScoreLevel{{Label: "calm"}, {Label: "angry"}}},
		},
	}
	cred := models.ResolvedCredential{Provider: "typesafe", InternalModel: "jev"}
	_, body, err := p.BuildUpstream(cred, req, []byte(`{}`))
	if err != nil {
		t.Fatalf("BuildUpstream: %v", err)
	}
	s := string(body)
	if strings.Contains(s, `"choice":`) || strings.Contains(s, `"score":`) {
		t.Fatalf("request body still contains nested choice/score wrapper: %s", s)
	}
	if strings.Contains(s, `"value":`) {
		t.Fatalf("request body still contains object-valued criteria (value field): %s", s)
	}
	if strings.Contains(s, `"index":`) {
		t.Fatalf("request body still contains score index field: %s", s)
	}
	if !strings.Contains(s, `"criteria":{"billing":"Billing/refunds"}`) && !strings.Contains(s, `"criteria": {"billing":"Billing/refunds"}`) {
		// tolerate spacing
		if !strings.Contains(s, `"billing":"Billing/refunds"`) {
			t.Fatalf("request body missing canonical choice criteria map: %s", s)
		}
	}
	if !strings.Contains(s, `"criteria":["calm","angry"]`) && !strings.Contains(s, `"criteria": ["calm", "angry"]`) {
		t.Fatalf("request body missing canonical score criteria array: %s", s)
	}
}

// TestValidateSystemOneShape_RejectsOldNestedShape — review council
// Suggestion #2 (2026-11-08): negative control exercising the
// mock's 422 rejection branch directly with a hand-written
// old-shape body (criteria nested under "choice"/"score" with
// object values — the byte-level R3/R4 settlement spec). Complements
// TestR3R4_CanonicalShape_RejectsNestedShape, which only checks
// emitted bytes via BuildUpstream.
func TestValidateSystemOneShape_RejectsOldNestedShape(t *testing.T) {
	oldShape := []byte(`{"model":"jev","state":"x","questions":{"dept":{"type":"choice","instructions":"where?","choice":{"criteria":{"billing":{"value":"billing","description":"Billing/refunds"}}}},"frustration":{"type":"score","instructions":"rate","score":{"criteria":[{"index":0,"label":"Calm"}]}}}}`)
	if err := validateSystemOneShape(oldShape); err == nil {
		t.Fatal("validateSystemOneShape accepted the old nested shape; expected rejection")
	}
	// canonical shape must still pass
	canonical := []byte(`{"model":"jev","state":"x","questions":{"dept":{"type":"choice","instructions":"where?","criteria":{"billing":"Billing/refunds"}},"frustration":{"type":"score","instructions":"rate","criteria":["Calm"]}}}`)
	if err := validateSystemOneShape(canonical); err != nil {
		t.Fatalf("validateSystemOneShape rejected canonical shape: %v", err)
	}
}
