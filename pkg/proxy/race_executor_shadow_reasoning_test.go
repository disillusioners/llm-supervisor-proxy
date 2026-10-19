package proxy

// P0 shadowfix — pinning test for pkg/proxy/race_executor.go:48.
//
// The P0 fix (8a7d071, branch fix/group-shadow-member-resolution)
// changed the MiniMax interleaved-thinking gate helper
// raceIntProviderIsMiniMax (race_executor.go:34-65) from the
// CLAIMS-FIRST read to the CLAIM-BLIND read:
//
//	-mc := cfg.ModelsConfig.GetModel(modelID)   // claims-first → GROUP row
//	+mc := directGetModel(cfg, modelID)          // claim-blind  → MEMBER row
//
// WHY the difference matters: in the DAY-1 SELF-SHADOW shape (a group
// that claims its OWN member ID — "coding-group[coding]") the
// claims-first read of the member ID returns the GROUP row, which
// carries NO credentials. The gate therefore answered "not MiniMax"
// for that member only, and the reasoning split was silently skipped
// for it (the interleaved-thinking fix was then skipped for that
// member only).
//
// This file pins the CONCRETE OBSERVABLE of that fix: for a self-shadow
// member whose PRIMARY credential is a MiniMax credential, the typed
// ReasoningSplit flag MUST be applied to the provider request
// (race_executor.go:488-490). Reverting ONLY race_executor.go:48 makes
// this test FAIL (mutation-verified).
//
// The fixture uses the REAL JSON-backend *models.ModelsConfig (not a
// hand-rolled mock) so the claim index is built by production code
// (AddModel → rebuildClaimsLocked, pkg/models/config.go:705) and the
// claims-first / claim-blind divergence exercised here is the
// production one.

import (
	"context"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// Compile-time proof that the fixture backend satisfies BOTH seams the
// pinned site depends on: the models.ModelsConfigInterface contract
// (ConfigSnapshot.ModelsConfig) and the unexported directModelGetter
// contract that directGetModel asserts to take the CLAIM-BLIND path.
// Without the latter, directGetModel silently degrades to the
// claims-aware fallback and this test could not observe the :48 site
// at all.
var (
	_ models.ModelsConfigInterface = (*models.ModelsConfig)(nil)
	_ directModelGetter            = (*models.ModelsConfig)(nil)
)

// shadowReasoningModelsConfig builds the DAY-1 SELF-SHADOW shape
// ("coding-group[coding]") on the real *models.ModelsConfig:
//
//	cred-minimax (provider=minimax)
//	coding       — chat member, internal, internal_model=MiniMax-M1
//	coding-group — kind=group, enabled, members=[coding],
//	               shadow_model_id=coding (the group claims its OWN
//	               member — the canonical day-1 self-shadow)
//
// The group's claim makes the CLAIMS-FIRST read of "coding" return the
// GROUP row (no credentials) while the CLAIM-BLIND read returns the
// MEMBER row (the MiniMax credential). That divergence IS the P0
// bug's precondition.
func shadowReasoningModelsConfig(t *testing.T) *models.ModelsConfig {
	t.Helper()

	mc := models.NewModelsConfig()
	if err := mc.AddCredential(models.CredentialConfig{
		ID:       "cred-minimax",
		Provider: "minimax",
		APIKey:   "test-key",
	}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	member := models.ModelConfig{
		ID:            "coding",
		Name:          "Coding",
		Enabled:       true,
		Internal:      true,
		InternalModel: "MiniMax-M1",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-minimax", Weight: 1, Position: 0},
		},
	}
	if err := mc.AddModel(member); err != nil {
		t.Fatalf("AddModel(member): %v", err)
	}

	group := models.ModelConfig{
		ID:                           "coding-group",
		Name:                         "Coding Group",
		Enabled:                      true,
		Kind:                         models.KindGroup,
		Internal:                     true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []models.GroupMember{{ModelID: "coding", Weight: 1}},
		ShadowModelID:                "coding", // self-shadow: claims its OWN member
	}
	if err := mc.AddModel(group); err != nil {
		t.Fatalf("AddModel(group): %v", err)
	}

	return mc
}

// TestRaceInternal_SelfShadowMember_MiniMax_ReasoningSplitApplied pins
// the race_executor.go:48 site through the observable it produces.
//
// The self-shadow member "coding" is served by a MiniMax credential.
// Because its ID is claimed by the group that owns it, the claims-first
// read returns the GROUP row (credential-less) — the pre-fix behavior.
// The claim-blind read (directGetModel, the P0 fix) returns the MEMBER
// row, so the MiniMax gate fires and the typed ReasoningSplit flag is
// applied to the provider request (race_executor.go:488-490).
//
// OBSERVABLE: the typed `ReasoningSplit *bool` field of the captured
// providers.ChatCompletionRequest. That IS the assignment the :48 site
// guards — the gate helper is consulted at two sites, and the typed
// setter at :488-490 is the one that produces the flag on the wire
// (convertToProviderRequest does not propagate the top-level
// reasoning_split into the typed struct, hence the explicit setter).
func TestRaceInternal_SelfShadowMember_MiniMax_ReasoningSplitApplied(t *testing.T) {
	mc := shadowReasoningModelsConfig(t)

	// ── PRECONDITION (guards against a vacuous test) ────────────────
	// The claims-first read MUST return the GROUP row and the
	// claim-blind read MUST return the MEMBER row. Without this
	// divergence the :48 site would be indistinguishable from the
	// pre-fix code and the test would pass for the wrong reason.
	if g := mc.GetModel("coding"); g == nil || !g.IsGroup() {
		t.Fatalf("precondition: claims-first GetModel(\"coding\") = %+v, want the GROUP row (self-shadow claim live)", g)
	}
	if m := mc.GetModelNative("coding"); m == nil || m.IsGroup() {
		t.Fatalf("precondition: claim-blind GetModelNative(\"coding\") = %+v, want the MEMBER row", m)
	}
	if m := mc.GetModelNative("coding"); m.PrimaryCredentialID() != "cred-minimax" {
		t.Fatalf("precondition: member primary credential = %q, want \"cred-minimax\"", m.PrimaryCredentialID())
	}

	// ── provider mock (captures the typed request) ─────────────────
	mock := newTestProvider()
	mock.SetChatCompletionResponse(&providers.ChatCompletionResponse{
		ID: "r", Object: "chat.completion", Created: 1, Model: "MiniMax-M1",
		Choices: []providers.Choice{
			{Index: 0, Message: &providers.ChatMessage{Role: "assistant", Content: "ok"}, FinishReason: "stop"},
		},
		Usage: providers.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	})
	origNewProvider := newProviderClient
	t.Cleanup(func() { newProviderClient = origNewProvider })
	newProviderClient = func(providerType, apiKey, baseURL string) (providers.Provider, error) {
		return mock, nil
	}

	cfg := newTestConfigSnapshot("coding")
	cfg.ModelsConfig = mc

	body := []byte(`{
  "model": "coding",
  "messages": [
    {"role": "user", "content": "hi"},
    {"role": "assistant", "content": "answer", "reasoning_content": "think-1"}
  ]
}`)
	upstreamReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "coding", 1024*1024)

	// interleaved=true — the X-Proxy-Interleaved-Thinking flag the
	// P0 comment names ("the interleaved-thinking split was then
	// skipped for that member only").
	if err := executeInternalRequest(context.Background(), cfg, body, upstreamReq, true); err != nil {
		t.Fatalf("executeInternalRequest: %v", err)
	}

	captured := mock.GetCapturedRequest()
	if captured == nil {
		t.Fatal("provider did not capture the request")
	}

	// ── THE ASSERTION (the :48 site) ───────────────────────────────
	// The typed ReasoningSplit flag is the assignment the :48 site
	// guards: it is only reached when raceIntProviderIsMiniMax
	// answers TRUE for the self-shadow member — which is only
	// possible via the CLAIM-BLIND read (the P0 fix). With the
	// claims-first read (pre-fix) the GROUP row carries no
	// credential, the gate answers false, and the flag stays nil.
	if captured.ReasoningSplit == nil {
		t.Fatalf("ReasoningSplit = nil, want ptr(true): the self-shadow member must get the MiniMax reasoning split (race_executor.go:48 → :488-490)")
	}
	if *captured.ReasoningSplit != true {
		t.Errorf("ReasoningSplit = %v, want ptr(true) for the self-shadow MiniMax member", *captured.ReasoningSplit)
	}

	// Corroboration (same gate, translation site at race_executor.go:464):
	// the reasoning_content must have been translated into the typed
	// reasoning_details array on the assistant message.
	if len(captured.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(captured.Messages))
	}
	if len(captured.Messages[1].ReasoningDetails) != 1 {
		t.Fatalf("len(ReasoningDetails) = %d, want 1 (the self-shadow member must be translated too)", len(captured.Messages[1].ReasoningDetails))
	}
	rd := captured.Messages[1].ReasoningDetails[0]
	if rd.Type != "reasoning.text" || rd.Format != "MiniMax-response-v1" || rd.Index != 0 || rd.Text != "think-1" {
		t.Errorf("ReasoningDetails[0] = %+v, want {Type:reasoning.text, Format:MiniMax-response-v1, Index:0, Text:think-1}", rd)
	}
}
