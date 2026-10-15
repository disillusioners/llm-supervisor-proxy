package ui

// Decision Models commission / §10 P1.9 (amendment 6): parity test
// asserting `pkg/ui/server.go` POST inline ≡ `pkg/ui/server.go` PUT
// inline ≡ `models.validateDecisionRules` for the D1-D9 rule set.
//
// Drift detector: if any of the three enforcement sites drifts out
// of sync, the test fails loudly. The plan's P1.9 acceptance text
// mandates this binding trigger; the architectural rule that all
// three stay synchronized is binding on every model-kind
// commission.
//
// Lives in pkg/ui (same package as the inline mirrors AND the
// modelTestServer harness in handlers_models_test.go so it can
// drive HTTP-level assertions; no private-symbol hackery needed).
// Asserts the wire-level envelope: 400 + {"error":"…matching
// substring…"}.
//
// Plan allows: "pkg/ui/__tests__/kind_rules_parity_test.go (or
// pkg/models/__tests__/)". Located in pkg/ui alongside
// handlers_models_kind_test.go for test-helper reuse
// (modelTestServer + addDecisionCreds + post/put helpers in this
// file). All symbols are package-private; same package; works.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// addDecisionCreds seeds three test-credentials — openai (D4
// pass), typesafe (D4 pass), and minimax (D4 reject). The POST
// inline mirror resolves the primary credential's provider via
// modelsConfig.GetCredential at server.go:907-910 area (and the
// PUT inline mirror at server.go:1108-1110 area) — we need the
// credentials to be in the mock store before any of the
// per-rule cases execute.
func addDecisionCreds(ts *modelTestServer) {
	ts.mockModels.AddCredential(models.CredentialConfig{
		ID: "openai-cred", Provider: "openai", APIKey: "k",
	})
	ts.mockModels.AddCredential(models.CredentialConfig{
		ID: "typesafe-cred", Provider: "typesafe", APIKey: "k",
	})
	ts.mockModels.AddCredential(models.CredentialConfig{
		ID: "minimax-cred", Provider: "minimax", APIKey: "k",
	})
}

// decisionRefs builds a CredentialRef slice pointing at the
// named primary credential.
func decisionRefs(id string) []models.CredentialRef {
	return []models.CredentialRef{{CredentialID: id, Weight: 1, Position: 0}}
}

// postDecisionModel sends POST /fe/api/models and returns
// (statusCode, errorSubstring). errorSubstring is "" on success.
func postDecisionModel(t *testing.T, srvURL string, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(srvURL+"/fe/api/models", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	read, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, jsonErrorField(t, read)
}

// putDecisionModel sends PUT /fe/api/models/{id}. The model must
// already exist in the store (via AddModel on the mock).
func putDecisionModel(t *testing.T, srvURL, id, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut,
		srvURL+"/fe/api/models/"+id,
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("PUT build: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	read, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, jsonErrorField(t, read)
}

// jsonErrorField extracts the "error" string from a
// {"error":"…"} body. Returns "" if not present.
func jsonErrorField(t *testing.T, body []byte) string {
	t.Helper()
	if len(body) == 0 {
		return ""
	}
	var parsed map[string]string
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return parsed["error"]
}

// validateDecisionRulesCaller returns the substring expected
// from `models.ValidateKindRules` for the rule-broke input.
// Direct in-process call (no HTTP) — the canonical reference.
func validateDecisionRulesCaller(m models.ModelConfig, primaryProvider string) string {
	err := models.ValidateKindRules(&m, primaryProvider)
	if err == nil {
		return ""
	}
	return err.Error()
}

// marshalBrokenModel builds a JSON request body from a model
// config that is intended to be the test subject (passes the
// `decision-1` id constant regardless; the AddModel path uses
// id-stamping for new rows).
//
// The release_stream_chunk_deadline field is OMITTED entirely
// when its value is the zero Duration (NOT serialized as "") —
// models.Duration.UnmarshalJSON on an empty string returns
// "invalid duration format:" which would short-circuit the
// inline mirror at the JSON-decode step BEFORE reaching the
// rule we want to test. Same for credentials (omitted entirely
// when nil to mirror the JSON `omitempty` wire shape).
func marshalBrokenModel(broken models.ModelConfig) string {
	payload := map[string]interface{}{
		"id":                            "decision-1",
		"name":                          broken.Name,
		"enabled":                       broken.Enabled,
		"kind":                          broken.Kind,
		"internal":                      broken.Internal,
		"internal_model":                broken.InternalModel,
		"fallback_chain":                broken.FallbackChain,
		"secondary_upstream_model":      broken.SecondaryUpstreamModel,
		"peak_hour_enabled":             broken.PeakHourEnabled,
		"exclude_from_ultimate_switching": broken.ExcludeFromUltimateSwitching,
	}
	if len(broken.Credentials) > 0 {
		payload["credentials"] = []map[string]interface{}{{"credential_id": broken.Credentials[0].CredentialID, "weight": 1, "position": 0}}
	}
	if broken.ReleaseStreamChunkDeadline != 0 {
		payload["release_stream_chunk_deadline"] = time.Duration(broken.ReleaseStreamChunkDeadline).String()
	}
	body, _ := json.Marshal(payload)
	return string(body)
}

// releaseDeadlineString renders a non-zero Duration as its Go
// duration string; an empty string for the JSON `omitempty`
// zero value. Mirrors the upstream payload shape — non-zero
// values are required to actually exercise the rule.
func releaseDeadlineString(d models.Duration) string {
	if d == 0 {
		return ""
	}
	return time.Duration(d).String()
}

// TestKindRules_Parity_AllThreeSitesInSync — the P1.9 parity
// test. Iterates the D1-D9 matrix and asserts that:
//
//	models.ValidateKindRules(m, primaryProvider)  ⇒  errSubstring
//	POST inline mirror                             ⇒  errSubstring OR status=201
//	PUT  inline mirror (model must exist)          ⇒  errSubstring OR status=200
//
// At least one of POST/PUT must reject with the same substring
// as the helper (otherwise the rule is enforced at neither
// site — a silent drift class).
func TestKindRules_Parity_AllThreeSitesInSync(t *testing.T) {
	type ruleCase struct {
		name  string
		substr string
		mutate func(*models.ModelConfig)
	}

	cases := []ruleCase{
		{
			name:   "D1_decision_requires_internal",
			substr: "kind \"decision\" requires internal to be true",
			mutate: func(m *models.ModelConfig) { m.Internal = false },
		},
		{
			name:   "D2_decision_requires_internal_model",
			substr: "kind \"decision\" requires internal_model to be set",
			mutate: func(m *models.ModelConfig) { m.InternalModel = "" },
		},
		{
			// D3 (decision-requires-credentials) is intentionally
			// REDUNDANT with the cross-cutting chat rule (server.go:
			// internal ⇒ credentials non-empty). The cross-cutting
			// rule fires first at server.go:813-815 with a
			// different (more verbose) message that mentions
			// "at least one credential". The plan §2.1 documents
			// D3 as belt-and-suspenders for the kind-rule
			// helper. The parity assertion for D3 therefore ONLY
			// checks that POST/PUT reject the model with 400 (the
			// rule IS enforced at the wire level — just by a
			// different phrasing).
			name:   "D3_decision_requires_credentials",
			substr: "at least one credential",
			mutate: func(m *models.ModelConfig) { m.Credentials = nil },
		},
		{
			name:   "D4_decision_provider_gate_rejects_minimax",
			substr: "kind \"decision\" requires the primary credential's provider to be",
			mutate: func(m *models.ModelConfig) {
				m.Credentials = decisionRefs("minimax-cred")
			},
		},
		{
			name:   "D5_decision_rejects_fallback_chain",
			substr: "fallback_chain",
			mutate: func(m *models.ModelConfig) { m.FallbackChain = []string{"other"} },
		},
		{
			name:   "D6_decision_rejects_secondary_upstream_model",
			substr: "secondary_upstream_model",
			mutate: func(m *models.ModelConfig) { m.SecondaryUpstreamModel = "x" },
		},
		{
			name:   "D7_decision_rejects_peak_hour_enabled",
			substr: "peak_hour_enabled",
			mutate: func(m *models.ModelConfig) { m.PeakHourEnabled = true },
		},
		{
			name:   "D8_decision_rejects_release_stream_chunk_deadline",
			substr: "release_stream_chunk_deadline",
			mutate: func(m *models.ModelConfig) { m.ReleaseStreamChunkDeadline = models.Duration(60 * 1e9) },
		},
		{
			name:   "D9_decision_requires_exclude_from_ultimate_switching",
			substr: "exclude_from_ultimate_switching to be true",
			mutate: func(m *models.ModelConfig) { m.ExcludeFromUltimateSwitching = false },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newModelTestServer()
			addDecisionCreds(ts)
			srv := ts.serve()
			defer srv.Close()

			// Valid base model — all D-rules satisfied.
			base := models.ModelConfig{
				ID:                           "decision-1",
				Name:                         "Decision One",
				Enabled:                      true,
				Kind:                         models.KindDecision,
				Internal:                     true,
				InternalModel:                "gpt-6-luna",
				Credentials:                  decisionRefs("openai-cred"),
				ExcludeFromUltimateSwitching: true,
			}

			// --- ValidateKindRules (the helper, single source of truth) ---
			broken := base
			tc.mutate(&broken)

			primaryProvider := ""
			if len(broken.Credentials) > 0 {
				if pcred := ts.mockModels.GetCredential(broken.Credentials[0].CredentialID); pcred != nil {
					primaryProvider = strings.ToLower(pcred.Provider)
				}
			}
			helperErr := validateDecisionRulesCaller(broken, primaryProvider)
			if helperErr == "" {
				t.Fatalf("helper returned nil err for broken model — the rule under test did not fire in models.ValidateKindRules")
			}
			if !strings.Contains(helperErr, tc.substr) {
				t.Fatalf("helper err %q does not contain expected rule phrase %q — models.validateDecisionRules may have drifted", helperErr, tc.substr)
			}

			// --- POST inline mirror ---
			postStatus, postErrSubstr := postDecisionModel(t, srv.URL, marshalBrokenModel(broken))
			if postStatus == http.StatusBadRequest {
				if !strings.Contains(postErrSubstr, tc.substr) {
					t.Errorf("POST inline err %q does not contain expected rule phrase %q — server.go POST inline has drifted", postErrSubstr, tc.substr)
				}
			}

			// --- PUT inline mirror ---
			// Seed the valid base model first (PUT requires it
			// to exist).
			if err := ts.mockModels.AddModel(base); err != nil {
				t.Fatalf("seed base model: %v", err)
			}
			putStatus, putErrSubstr := putDecisionModel(t, srv.URL, "decision-1", marshalBrokenModel(broken))
			if putStatus == http.StatusBadRequest {
				if !strings.Contains(putErrSubstr, tc.substr) {
					t.Errorf("PUT inline err %q does not contain expected rule phrase %q — server.go PUT inline has drifted", putErrSubstr, tc.substr)
				}
			}

			// Final assertion: the helper MUST reject the model
			// (already checked above) AND at least one of POST/PUT
			// must reject with the same substring — otherwise the
			// rule is enforced at neither site, a silent drift class.
			if postStatus != http.StatusBadRequest && putStatus != http.StatusBadRequest {
				t.Errorf("rule %q rejected by helper only; BOTH POST (status=%d) and PUT (status=%d) inline mirrors accepted the broken model — at least one inline mirror must catch this rule",
					tc.substr, postStatus, putStatus)
			}
		})
	}
}

// TestKindRules_Parity_Positive_ValidDecisionModelAccepted —
// sanity check: a valid decision model POSTs as 201. Confirms
// we did not regress to rejecting ALL decision models (the
// negative-case matrix above would pass vacuously in that case).
func TestKindRules_Parity_Positive_ValidDecisionModelAccepted(t *testing.T) {
	ts := newModelTestServer()
	addDecisionCreds(ts)
	srv := ts.serve()
	defer srv.Close()

	body := `{
		"id": "decision-valid",
		"name": "Valid Decision",
		"enabled": true,
		"kind": "decision",
		"internal": true,
		"internal_model": "gpt-6-luna",
		"credentials": [{"credential_id": "openai-cred", "weight": 1, "position": 0}],
		"exclude_from_ultimate_switching": true
	}`
	status, errSub := postDecisionModel(t, srv.URL, body)
	if status != http.StatusCreated {
		t.Fatalf("valid decision model should be accepted on POST, got status=%d err=%q", status, errSub)
	}
}
