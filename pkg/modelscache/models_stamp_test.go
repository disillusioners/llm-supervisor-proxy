package modelscache

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// remove-external-upstream P1 task 7 (R1 — release-blocking): the
// LAYERED Internal=true write-stamp. These tests pin the DECORATOR
// layer's read-your-write contract: a write with Internal=false must be
// stamped over BEFORE delegation so that
//   (a) the WRAPPED manager reads back internal=true IMMEDIATELY (the
//       positive cache deep-copies the CALLER's struct — an inner-only
//       stamp would leave Internal=false cached for ≤60s and false-404
//       every fresh write through the boundary !Internal gate), and
//   (b) the inner store receives the stamped value through delegation.
// The JSON-backend and DB-manager layers are pinned in their own
// packages (pkg/models config_stamp_test.go / store-side write paths).

func TestCachedModelsConfig_WriteStamp_AddModel_ReadThroughWrapped(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	payload := models.ModelConfig{
		ID:            "m-stamped",
		Name:          "stamped",
		Enabled:       true,
		Internal:      false, // caller "omits the tick" — must be stamped over
		InternalModel: "upstream-x",
		Credentials:   models.TestRefs("cred-1"),
	}
	if err := c.AddModel(payload); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// (1) read-back through the WRAPPED manager is IMMEDIATE — no
	// reconcile wait; the positive cache must hold the STAMPED copy.
	got := c.GetModel("m-stamped")
	if got == nil {
		t.Fatal("wrapped GetModel(m-stamped) = nil")
	}
	if !got.Internal {
		t.Error("wrapped cache holds Internal=false — decorator missed the pre-delegation stamp (R1 read-your-write gap)")
	}

	// (2) the inner store received the stamped value via delegation.
	innerGot := inner.findModel("m-stamped")
	if innerGot == nil {
		t.Fatal("inner store never received the AddModel delegation")
	}
	if !innerGot.Internal {
		t.Error("inner store row is not internal=true — delegation delivered an unstamped copy")
	}
}

func TestCachedModelsConfig_WriteStamp_UpdateModel_ReadThroughWrapped(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	seed := models.ModelConfig{
		ID:            "m-stamped",
		Name:          "stamped",
		Enabled:       true,
		Internal:      false,
		InternalModel: "upstream-x",
		Credentials:   models.TestRefs("cred-1"),
	}
	if err := c.AddModel(seed); err != nil {
		t.Fatalf("AddModel seed: %v", err)
	}

	update := seed
	update.Name = "stamped-v2"
	update.Internal = false // PUT without the internal flag — must stamp over
	if err := c.UpdateModel("m-stamped", update); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}

	got := c.GetModel("m-stamped")
	if got == nil {
		t.Fatal("wrapped GetModel(m-stamped) = nil after update")
	}
	if !got.Internal {
		t.Error("wrapped cache holds Internal=false after UpdateModel — decorator missed the pre-delegation stamp")
	}
	if got.Name != "stamped-v2" {
		t.Errorf("cached Name = %q, want stamped-v2 (update must land)", got.Name)
	}
	innerGot := inner.findModel("m-stamped")
	if innerGot == nil || !innerGot.Internal {
		t.Error("inner store row is not internal=true after UpdateModel delegation")
	}
}
