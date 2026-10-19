package modelscache

// P0 shadowfix — "group member id shadowed by its own group" (cache
// layer).
//
// The decorator's claim-eviction hook (§3.4) DELETES the claimed
// shadow ID's cache entry when a group claims it:
//
//	evictClaimEntriesLocked(shadowID) → delete(c.modelsByID, shadowID)
//
// So the moment a group claims "coding" (its own member's ID), the
// MEMBER row is no longer cached — while the CLAIM is live. The
// execution-resolution seam then takes the miss path, and the
// pre-fix fill called c.GetModel(modelID) — which is CLAIMS-FIRST:
// the claims redirect returned the GROUP row and the MEMBER row was
// never fetched. The resolution then failed with
// "failed to resolve internal config for model coding" (502) even
// though the member row carries the credentials.
//
// The fill must be CLAIM-BLIND (Am22 L1: "member refs resolve by
// direct ID; claims apply to client-facing resolution only").

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestCache_SelfShadowMemberResolvesAfterClaimEviction — the
// self-shadow member MUST resolve through the decorator's hot
// resolution seam after its ID has been claimed (and evicted) by the
// group.
func TestCache_SelfShadowMemberResolvesAfterClaimEviction(t *testing.T) {
	_, cache, cleanup := newCacheManager(t)
	defer cleanup()

	// The member row (its ID is the group's shadow id).
	if err := cache.AddModel(models.ModelConfig{
		ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "glm-5", Credentials: models.TestRefs("cred-openai"),
	}); err != nil {
		t.Fatalf("seed coding: %v", err)
	}
	// The group's second member (the group fixture references it).
	if err := cache.AddModel(models.ModelConfig{
		ID: "coding-alt", Name: "Coding Alt", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "kimi", Credentials: models.TestRefs("cred-zhipu"),
	}); err != nil {
		t.Fatalf("seed coding-alt: %v", err)
	}
	// The group claims "coding" — this EVICTS the member's cache
	// entry (the claim-eviction hook) and activates the claim.
	if err := cache.AddModel(validGroupRow()); err != nil {
		t.Fatalf("AddModel group: %v", err)
	}

	// The claim is live: the CLIENT-FACING read still resolves to the
	// group row (claims-first — unchanged by the shadowfix).
	if got := cache.GetModel("coding"); got == nil || got.ID != "coding-group" {
		t.Fatalf("claim-aware GetModel(coding) = %+v, want the group row", got)
	}

	// The EXECUTION-resolution seam must resolve the MEMBER row.
	res, ok := cache.ResolveInternalConfigWithAffinity("coding", "conv-1")
	if !ok {
		t.Fatalf("ResolveInternalConfigWithAffinity(coding) failed — the self-shadow member did not resolve (the claims-first fill returned the group row instead of the member row)")
	}
	if res.InternalModel != "glm-5" {
		t.Errorf("resolved internal model = %q, want %q (the MEMBER row's internal model)", res.InternalModel, "glm-5")
	}
	if res.CredentialID != "cred-openai" {
		t.Errorf("resolved credential = %q, want %q", res.CredentialID, "cred-openai")
	}
}
