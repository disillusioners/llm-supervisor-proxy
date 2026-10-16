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
		"id":                              "decision-1",
		"name":                            broken.Name,
		"enabled":                         broken.Enabled,
		"kind":                            broken.Kind,
		"internal":                        broken.Internal,
		"internal_model":                  broken.InternalModel,
		"fallback_chain":                  broken.FallbackChain,
		"secondary_upstream_model":        broken.SecondaryUpstreamModel,
		"peak_hour_enabled":               broken.PeakHourEnabled,
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
//	POST inline mirror                             ⇒  400 + errSubstring
//	PUT  inline mirror (model must exist)          ⇒  400 + errSubstring
//
// EACH mirror is asserted independently and unconditionally: both
// POST AND PUT must return 400 carrying the rule's phrase (P1
// review fix — the previous form only errored when BOTH mirrors
// accepted a violation, so a single drifting mirror passed
// silently). Any drift at any of the three enforcement sites
// fails loudly. (D3 exception: the phrase arrives via the
// pre-empting cross-cutting credential rule — see the D3 case
// comment for the evidence; both mirrors are still asserted at
// 400 with that phrase.)
func TestKindRules_Parity_AllThreeSitesInSync(t *testing.T) {
	type ruleCase struct {
		name   string
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
			// REDUNDANT with the cross-cutting chat rule
			// (validateCredentials rule (a): internal ⇒ credentials
			// non-empty; pkg/ui/server.go, POST block ~:860 + PUT
			// block ~:1183 — both run BEFORE the kind mirrors).
			//
			// Dual-enforcement overlap, decided from source evidence
			// (not assumption): every payload that reaches the
			// decision mirror's own D3 check has already passed D1
			// (internal=true, since D1 fires first in both mirrors
			// and in the helper) and has len(credentials)==0 — which
			// is exactly rule (a)'s trigger. The cross-cutting rule
			// therefore provably ALWAYS fires first at the wire for
			// every D3-violating payload, with the message
			// "credentials: at least one credential is required when
			// internal is true (…)", prefixed "invalid credentials: ".
			// The D3 mirror phrase `kind "decision" requires at
			// least one credential` is wire-unreachable in POST/PUT
			// (defense-in-depth: still live at the DB/helper layer
			// via AddModel → ValidateKindRules).
			//
			// Stance per the P1 review: keep the overlap substring
			// "at least one credential" (shared by BOTH phrasings),
			// and assert BOTH mirrors return 400 — the rule IS
			// enforced at the wire level, just by the pre-empting
			// cross-cutting phrasing.
			name:   "D3_decision_requires_credentials",
			substr: "at least one credential",
			mutate: func(m *models.ModelConfig) { m.Credentials = nil },
		},
		{
			// Phrase shared by helper ("model <id>: kind \"decision\"
			// requires the primary credential's provider to be …"),
			// POST mirror and PUT mirror — pinned to the exact
			// allowed-provider list so a provider-list drift
			// anywhere fails loudly.
			name:   "D4_decision_provider_gate_rejects_minimax",
			substr: `requires the primary credential's provider to be "openai" or "typesafe"`,
			mutate: func(m *models.ModelConfig) {
				m.Credentials = decisionRefs("minimax-cred")
			},
		},
		{
			name:   "D5_decision_rejects_fallback_chain",
			substr: "does not support fallback_chain",
			mutate: func(m *models.ModelConfig) { m.FallbackChain = []string{"other"} },
		},
		{
			name:   "D6_decision_rejects_secondary_upstream_model",
			substr: "does not support secondary_upstream_model",
			mutate: func(m *models.ModelConfig) { m.SecondaryUpstreamModel = "x" },
		},
		{
			name:   "D7_decision_rejects_peak_hour_enabled",
			substr: "does not support peak_hour_enabled",
			mutate: func(m *models.ModelConfig) { m.PeakHourEnabled = true },
		},
		{
			name:   "D8_decision_rejects_release_stream_chunk_deadline",
			substr: "does not support release_stream_chunk_deadline",
			mutate: func(m *models.ModelConfig) { m.ReleaseStreamChunkDeadline = models.Duration(60 * 1e9) },
		},
		{
			name:   "D9_decision_requires_exclude_from_ultimate_switching",
			substr: "requires exclude_from_ultimate_switching to be true",
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

			// --- POST inline mirror (P1.9 fix: hard 400 + phrase) ---
			postStatus, postErrSubstr := postDecisionModel(t, srv.URL, marshalBrokenModel(broken))
			if postStatus != http.StatusBadRequest {
				t.Errorf("POST inline mirror ACCEPTED the broken model (status=%d, err=%q) — server.go POST inline has drifted for %s; must reject with 400",
					postStatus, postErrSubstr, tc.name)
			} else if !strings.Contains(postErrSubstr, tc.substr) {
				t.Errorf("POST inline err %q does not contain expected rule phrase %q — server.go POST inline has drifted", postErrSubstr, tc.substr)
			}

			// --- PUT inline mirror (P1.9 fix: hard 400 + phrase) ---
			// Seed the valid base model first (PUT requires it
			// to exist).
			if err := ts.mockModels.AddModel(base); err != nil {
				t.Fatalf("seed base model: %v", err)
			}
			putStatus, putErrSubstr := putDecisionModel(t, srv.URL, "decision-1", marshalBrokenModel(broken))
			if putStatus != http.StatusBadRequest {
				t.Errorf("PUT inline mirror ACCEPTED the broken model (status=%d, err=%q) — server.go PUT inline has drifted for %s; must reject with 400",
					putStatus, putErrSubstr, tc.name)
			} else if !strings.Contains(putErrSubstr, tc.substr) {
				t.Errorf("PUT inline err %q does not contain expected rule phrase %q — server.go PUT inline has drifted", putErrSubstr, tc.substr)
			}

			// No OR-shortcut: each mirror above is asserted
			// independently (400 + phrase), so a single drifting
			// mirror fails this subtest on its own.
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

// ───────────────────────────────────────────────────────────────
// load-balance-groups / P1.7 — group-kind parity extension.
// Same three-site drift lock as the decision matrix above,
// extended to the G1-G8 rule set (plan §5.3): the helper
// reference (models.ValidateKindRules group arm +
// models.ValidateGroupMembers) ≡ POST inline mirror ≡ PUT inline
// mirror. TestKindRules_Parity_AllThreeSitesInSync now covers
// 4 kinds × 3 sites.
// ───────────────────────────────────────────────────────────────

// addGroupMembers seeds the member/shadow-target fixture rows the
// group cases resolve against (the addDecisionCreds analog for
// member refs): two chat-kind models, one image-gen model (kind
// rejection cases), and one enabled group claiming "member-a"
// (the G8 uniqueness scan needs a competing claimer).
func addGroupMembers(ts *modelTestServer) {
	ts.mockModels.AddModel(models.ModelConfig{
		ID: "member-a", Name: "Member A", Enabled: true, Kind: models.KindChat,
	})
	ts.mockModels.AddModel(models.ModelConfig{
		ID: "member-b", Name: "Member B", Enabled: true, Kind: "",
	})
	ts.mockModels.AddModel(models.ModelConfig{
		ID: "member-img", Name: "Member Img", Enabled: true, Kind: models.KindImageGen,
		Internal: true, InternalModel: "image-01",
		Credentials:                  []models.CredentialRef{{CredentialID: "test-cred", Weight: 1, Position: 0}},
		ExcludeFromUltimateSwitching: true,
	})
	ts.mockModels.AddModel(models.ModelConfig{
		ID: "other-group", Name: "Other Group", Enabled: true, Kind: models.KindGroup,
		Internal: true, ExcludeFromUltimateSwitching: true,
		Members:       []models.GroupMember{{ModelID: "member-b", Weight: 1}},
		ShadowModelID: "member-a",
	})
}

// groupBase returns a fully valid group row (2 model-ID members,
// no claim — the claim cases mutate it on).
func groupBase() models.ModelConfig {
	return models.ModelConfig{
		ID:                           "group-1",
		Name:                         "Group One",
		Enabled:                      true,
		Kind:                         models.KindGroup,
		Internal:                     true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []models.GroupMember{{ModelID: "member-a", Weight: 1}, {ModelID: "member-b", Weight: 2}},
	}
}

// validateGroupRulesCaller is the helper reference for the group
// matrix (the validateDecisionRulesCaller analog): the shared
// ValidateKindRules group arm (field rules) plus the exported
// collection-aware ValidateGroupMembers, validated against the
// live mock model set. Returns "" on success.
func validateGroupRulesCaller(m models.ModelConfig, collection []models.ModelConfig) string {
	if err := models.ValidateKindRules(&m, ""); err != nil {
		return err.Error()
	}
	if err := models.ValidateGroupMembers(&m, collection); err != nil {
		return err.Error()
	}
	return ""
}

// marshalGroupModel builds the JSON request body for a group
// payload (the marshalBrokenModel analog — members/shadow_model_id
// included; zero-value optional fields omitted).
func marshalGroupModel(m models.ModelConfig) string {
	payload := map[string]interface{}{
		"id":                              "group-1",
		"name":                            m.Name,
		"enabled":                         m.Enabled,
		"kind":                            m.Kind,
		"internal":                        m.Internal,
		"exclude_from_ultimate_switching": m.ExcludeFromUltimateSwitching,
	}
	if len(m.Members) > 0 {
		members := make([]map[string]interface{}, 0, len(m.Members))
		for _, mem := range m.Members {
			members = append(members, map[string]interface{}{"model_id": mem.ModelID, "weight": mem.Weight})
		}
		payload["members"] = members
	} else {
		payload["members"] = []map[string]interface{}{}
	}
	if m.ShadowModelID != "" {
		payload["shadow_model_id"] = m.ShadowModelID
	}
	if len(m.FallbackChain) > 0 {
		payload["fallback_chain"] = m.FallbackChain
	}
	if m.SecondaryUpstreamModel != "" {
		payload["secondary_upstream_model"] = m.SecondaryUpstreamModel
	}
	if m.PeakHourEnabled {
		payload["peak_hour_enabled"] = true
	}
	if m.ReleaseStreamChunkDeadline != 0 {
		payload["release_stream_chunk_deadline"] = time.Duration(m.ReleaseStreamChunkDeadline).String()
	}
	body, _ := json.Marshal(payload)
	return string(body)
}

// TestKindRules_Parity_Group_AllThreeSitesInSync — the G1-G8
// matrix across helper / POST / PUT (same per-mirror independent
// assertion discipline as the decision matrix).
func TestKindRules_Parity_Group_AllThreeSitesInSync(t *testing.T) {
	type ruleCase struct {
		name   string
		substr string
		mutate func(*models.ModelConfig)
	}

	cases := []ruleCase{
		{
			name:   "G1_group_requires_internal",
			substr: "requires internal to be true",
			mutate: func(m *models.ModelConfig) { m.Internal = false },
		},
		{
			name:   "G2_group_requires_at_least_one_member",
			substr: "requires at least one member",
			mutate: func(m *models.ModelConfig) { m.Members = nil },
		},
		{
			name:   "G2_group_member_cap_16",
			substr: "exceeds max of 16 entries",
			mutate: func(m *models.ModelConfig) {
				members := make([]models.GroupMember, 0, models.MaxGroupMembers+1)
				for i := 0; i <= models.MaxGroupMembers; i++ {
					members = append(members, models.GroupMember{ModelID: "member-a", Weight: 1})
				}
				m.Members = members
			},
		},
		{
			name:   "G3_member_must_exist",
			substr: "does not exist (members must reference existing chat-kind models)",
			mutate: func(m *models.ModelConfig) {
				m.Members = []models.GroupMember{{ModelID: "member-a", Weight: 1}, {ModelID: "ghost", Weight: 1}}
			},
		},
		{
			name:   "G3_member_weight_positive",
			substr: "weight must be > 0",
			mutate: func(m *models.ModelConfig) {
				m.Members = []models.GroupMember{{ModelID: "member-a", Weight: 0}}
			},
		},
		{
			name:   "G4_no_duplicate_members",
			substr: "duplicate member",
			mutate: func(m *models.ModelConfig) {
				m.Members = []models.GroupMember{{ModelID: "member-a", Weight: 1}, {ModelID: "member-a", Weight: 2}}
			},
		},
		{
			name:   "G5_group_as_member_forbidden",
			substr: "group-as-member is forbidden",
			mutate: func(m *models.ModelConfig) {
				m.Members = []models.GroupMember{{ModelID: "member-a", Weight: 1}, {ModelID: "other-group", Weight: 1}}
			},
		},
		{
			name:   "G3_member_must_be_chat_kind",
			substr: "must be a chat-kind model",
			mutate: func(m *models.ModelConfig) {
				m.Members = []models.GroupMember{{ModelID: "member-img", Weight: 1}}
			},
		},
		{
			name:   "G6_group_rejects_fallback_chain",
			substr: "does not support fallback_chain",
			mutate: func(m *models.ModelConfig) { m.FallbackChain = []string{"member-b"} },
		},
		{
			name:   "G6_group_rejects_secondary_upstream_model",
			substr: "does not support secondary_upstream_model",
			mutate: func(m *models.ModelConfig) { m.SecondaryUpstreamModel = "x" },
		},
		{
			name:   "G6_group_rejects_peak_hour_enabled",
			substr: "does not support peak_hour_enabled",
			mutate: func(m *models.ModelConfig) { m.PeakHourEnabled = true },
		},
		{
			name:   "G6_group_rejects_release_stream_chunk_deadline",
			substr: "does not support release_stream_chunk_deadline",
			mutate: func(m *models.ModelConfig) { m.ReleaseStreamChunkDeadline = models.Duration(60 * 1e9) },
		},
		{
			name:   "G7_group_requires_exclude_from_ultimate_switching",
			substr: "requires exclude_from_ultimate_switching to be true",
			mutate: func(m *models.ModelConfig) { m.ExcludeFromUltimateSwitching = false },
		},
		{
			name:   "G8_group_cannot_shadow_own_id",
			substr: "cannot be the group's own ID",
			mutate: func(m *models.ModelConfig) { m.ShadowModelID = "group-1" },
		},
		{
			name:   "G8_shadow_target_must_exist",
			substr: "does not exist (must reference an existing chat-kind model)",
			mutate: func(m *models.ModelConfig) { m.ShadowModelID = "ghost" },
		},
		{
			name:   "G8_shadow_target_must_be_chat_kind",
			substr: "must reference a chat-kind model (got kind",
			mutate: func(m *models.ModelConfig) { m.ShadowModelID = "member-img" },
		},
		{
			name:   "G8_shadow_target_cannot_be_a_group",
			substr: "must reference a chat-kind model, not a group",
			mutate: func(m *models.ModelConfig) { m.ShadowModelID = "other-group" },
		},
		{
			name:   "G8_uniqueness_no_other_enabled_group_claims_same_id",
			substr: "already claimed by enabled group",
			mutate: func(m *models.ModelConfig) { m.ShadowModelID = "member-a" },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newModelTestServer()
			addGroupMembers(ts)
			srv := ts.serve()
			defer srv.Close()

			base := groupBase()
			broken := base
			tc.mutate(&broken)

			// --- Helper reference (ValidateKindRules group arm +
			// ValidateGroupMembers against the live mock set) ---
			helperErr := validateGroupRulesCaller(broken, ts.mockModels.GetModels())
			if helperErr == "" {
				t.Fatalf("helper returned nil err for broken group — the rule under test did not fire")
			}
			if !strings.Contains(helperErr, tc.substr) {
				t.Fatalf("helper err %q does not contain expected rule phrase %q — models group validators may have drifted", helperErr, tc.substr)
			}

			// --- POST inline mirror ---
			postStatus, postErrSubstr := postDecisionModel(t, srv.URL, marshalGroupModel(broken))
			if postStatus != http.StatusBadRequest {
				t.Errorf("POST inline mirror ACCEPTED the broken group (status=%d, err=%q) — server.go POST group arm has drifted for %s; must reject with 400",
					postStatus, postErrSubstr, tc.name)
			} else if !strings.Contains(postErrSubstr, tc.substr) {
				t.Errorf("POST inline err %q does not contain expected rule phrase %q — server.go POST group arm has drifted", postErrSubstr, tc.substr)
			}

			// --- PUT inline mirror (seed the valid base first) ---
			if err := ts.mockModels.AddModel(base); err != nil {
				t.Fatalf("seed base group: %v", err)
			}
			putStatus, putErrSubstr := putDecisionModel(t, srv.URL, "group-1", marshalGroupModel(broken))
			if putStatus != http.StatusBadRequest {
				t.Errorf("PUT inline mirror ACCEPTED the broken group (status=%d, err=%q) — server.go PUT group arm has drifted for %s; must reject with 400",
					putStatus, putErrSubstr, tc.name)
			} else if !strings.Contains(putErrSubstr, tc.substr) {
				t.Errorf("PUT inline err %q does not contain expected rule phrase %q — server.go PUT group arm has drifted", putErrSubstr, tc.substr)
			}
		})
	}
}

// TestKindRules_Parity_Positive_ValidGroupModelAccepted — sanity
// check (mirrors the decision positive test): a valid group —
// including the SELF-SHADOW claim (shadow = its own member, the
// day-1 onboarding shape) — POSTs as 201. The member-a claim is
// free here because addGroupMembers is NOT called (no competing
// group).
func TestKindRules_Parity_Positive_ValidGroupModelAccepted(t *testing.T) {
	ts := newModelTestServer()
	// Seed ONLY the chat members (no competing group).
	ts.mockModels.AddModel(models.ModelConfig{ID: "member-a", Name: "Member A", Enabled: true, Kind: models.KindChat})
	ts.mockModels.AddModel(models.ModelConfig{ID: "member-b", Name: "Member B", Enabled: true, Kind: ""})
	srv := ts.serve()
	defer srv.Close()

	valid := groupBase()
	valid.ShadowModelID = "member-a" // self-shadow of own member — LEGAL
	if helperErr := validateGroupRulesCaller(valid, ts.mockModels.GetModels()); helperErr != "" {
		t.Fatalf("helper rejected the valid self-shadow group: %q", helperErr)
	}

	status, errSub := postDecisionModel(t, srv.URL, marshalGroupModel(valid))
	if status != http.StatusCreated {
		t.Fatalf("valid group (self-shadow) should be accepted on POST, got status=%d err=%q", status, errSub)
	}
}

// TestHandleModels_GET_KindArms_Group — P1.6 GET-arm evidence
// (§6.4 lock-step arm 1): ?kind=group returns ONLY groups; the
// chat arm and the default (unknown-kind) arm EXCLUDE groups.
func TestHandleModels_GET_KindArms_Group(t *testing.T) {
	ts := newModelTestServer()
	addGroupMembers(ts)
	ts.mockModels.AddModel(models.ModelConfig{ID: "plain-chat", Name: "Plain Chat", Enabled: true, Kind: models.KindChat})
	srv := ts.serve()
	defer srv.Close()

	get := func(t *testing.T, url string) map[string]Model {
		t.Helper()
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var out []Model
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode: %v (%s)", err, body)
		}
		byID := make(map[string]Model, len(out))
		for _, m := range out {
			byID[m.ID] = m
		}
		return byID
	}

	// ?kind=group → only group rows, members + claim surfaced.
	groups := get(t, srv.URL+"/fe/api/models?kind=group")
	if len(groups) != 1 {
		t.Fatalf("?kind=group should return exactly the one group row, got %d: %v", len(groups), groups)
	}
	g, ok := groups["other-group"]
	if !ok {
		t.Fatalf("?kind=group missing other-group: %v", groups)
	}
	if len(g.Members) != 1 || g.Members[0].ModelID != "member-b" || g.ShadowModelID != "member-a" {
		t.Errorf("group wire shape lost: Members=%v ShadowModelID=%q", g.Members, g.ShadowModelID)
	}

	// ?kind=chat → groups excluded (chat-arm-leak regression class).
	chat := get(t, srv.URL+"/fe/api/models?kind=chat")
	if _, leaked := chat["other-group"]; leaked {
		t.Error("?kind=chat leaked the group row (chat arm must exclude groups)")
	}
	if _, missing := chat["plain-chat"]; !missing {
		t.Error("?kind=chat lost a plain chat row")
	}

	// unknown kind → treated as chat (groups still excluded).
	def := get(t, srv.URL+"/fe/api/models?kind=banana")
	if _, leaked := def["other-group"]; leaked {
		t.Error("default arm leaked the group row (must exclude groups)")
	}

	// no filter → everything (today's behavior).
	all := get(t, srv.URL+"/fe/api/models")
	if _, missing := all["other-group"]; !missing {
		t.Error("no-filter GET lost the group row")
	}
	if _, missing := all["plain-chat"]; !missing {
		t.Error("no-filter GET lost the chat row")
	}
}
