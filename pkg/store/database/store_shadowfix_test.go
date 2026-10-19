package database

// P0 shadowfix — "group member id shadowed by its own group" (the
// credential-LB engine refresh path).
//
// The engine invalidation drain (drainCredentialsChanged) refreshes
// the CREDENTIAL-LB state of the model named by a
// model.credentials.changed event. The event is published by the
// model write path (AddModel/UpdateModel) for the model that was
// written — and the drain re-reads that model to pick up its
// credential list.
//
// The re-read was CLAIMS-FIRST. For a member ID that its own group
// claims (the day-1 self-shadow shape coding-group[coding]) the
// claims-first read returned the GROUP row — whose credential list is
// EMPTY — so the refresh called OnModelChanged(id, nil), which
// DELETES the member's engine state (credentiallb/engine.go:366).
// The member's next multi-credential resolution then hit
// ErrNoCredentials and failed with "failed to resolve internal config
// for model coding" (502) — and since the drain always fed it the
// group row, the state was never restored: a PERMANENT failure for
// that member (until a reboot re-seeds the engine).
//
// The drain must re-read by ROW IDENTITY (GetModelNative).

import (
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestShadowFix_CredentialsChangedDrain_KeepsSelfShadowMemberEngineState
// — after a member write, the member's credential-LB engine state
// must survive the bus-driven refresh.
func TestShadowFix_CredentialsChangedDrain_KeepsSelfShadowMemberEngineState(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()
	// A REAL bus — the drain only runs when the manager has one.
	bus := events.NewBus()
	mgr, err := NewModelsManager(store, bus)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	defer mgr.Close()

	for _, c := range []struct{ id, provider string }{
		{"cred-openai", "openai"},
		{"cred-openai-2", "openai"},
	} {
		if err := mgr.AddCredential(models.CredentialConfig{ID: c.id, Provider: c.provider, APIKey: "k"}); err != nil {
			t.Fatalf("AddCredential %s: %v", c.id, err)
		}
	}
	// The member carries TWO credentials — the engine path (GetOrSelect)
	// is what a wiped engine state breaks (the single-credential E-3
	// fast path never consults the engine).
	member := models.ModelConfig{
		ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "glm-5",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-openai", Weight: 1, Position: 0},
			{CredentialID: "cred-openai-2", Weight: 1, Position: 1},
		},
	}
	if err := mgr.AddModel(member); err != nil {
		t.Fatalf("AddModel(coding): %v", err)
	}
	// The group claims "coding" through its own shadow.
	if err := mgr.AddModel(models.ModelConfig{
		ID: "coding-group", Name: "Coding Group", Enabled: true,
		Kind: models.KindGroup, Internal: true,
		ExcludeFromUltimateSwitching: true,
		Members:                      []models.GroupMember{{ModelID: "coding", Weight: 1}},
		ShadowModelID:                "coding",
	}); err != nil {
		t.Fatalf("AddModel(coding-group): %v", err)
	}

	// A member write — this publishes model.credentials.changed for
	// "coding" and the drain re-reads it.
	member.InternalModel = "glm-5b"
	if err := mgr.UpdateModel("coding", member); err != nil {
		t.Fatalf("UpdateModel(coding): %v", err)
	}

	// Wait for the ASYNCHRONOUS drain (drainCredentialsChanged) to
	// process the member's credentials-changed event — the assertion
	// below must observe the POST-drain state.
	//
	// Polling "does the member resolve?" is NOT a valid wait here.
	// The WRITE PATH already refreshed the engine synchronously
	// (UpdateModel -> engine.OnModelChanged), so that condition is
	// TRUE BEFORE the drain runs: a poll on it is satisfied in
	// microseconds and would no longer observe the drain's effect —
	// it would MASK the regression this test pins (measured against
	// the claims-first drain: a naive poll passes in ~0.3ms). The wait
	// below is a FIFO BARRIER instead:
	//
	//   * the drain consumes ONE channel in ONE goroutine, so events
	//     are handled in publish order;
	//   * the canary event is published AFTER the member write, so
	//     the drain reaches it only after the member's event;
	//   * the canary is a model that exists ONLY in the engine (it
	//     has no DB row), so the drain's re-read finds nothing and
	//     DROPS its engine state — a deterministic, observable
	//     marker of drain progress.
	//
	// Once the canary's engine state is gone, the member's event has
	// been processed and the member's engine state has settled.
	const canaryModel = "drain-canary"
	mgr.Engine().RebindFromStore(canaryModel, models.TestRefs("cred-openai"))
	if _, ok := mgr.Engine().Stats()[canaryModel]; !ok {
		t.Fatalf("canary prime failed: the engine has no state for %q", canaryModel)
	}
	bus.Publish(events.Event{
		Type:      credentiallb.EventCredentialsChanged,
		Timestamp: time.Now().Unix(),
		Data:      map[string]interface{}{"model_id": canaryModel},
	})

	const (
		drainDeadline     = 3 * time.Second
		drainPollInterval = 25 * time.Millisecond
	)
	deadline := time.Now().Add(drainDeadline)
	for {
		if _, ok := mgr.Engine().Stats()[canaryModel]; !ok {
			break // the drain passed the member's event — state settled
		}
		if time.Now().After(deadline) {
			t.Fatalf("the credentials-changed drain did not process the member's event within %s (the canary engine state is still present, so the drain never reached the canary event)", drainDeadline)
		}
		time.Sleep(drainPollInterval)
	}

	res, ok := mgr.ResolveInternalConfigWithAffinity("coding", "conv-1")
	if !ok {
		t.Fatalf("the self-shadow member did not resolve after its own write — the credentials-changed drain wiped its credential-LB engine state (the claims-first re-read returned the credential-less group row)")
	}
	if res.CredentialID == "" {
		t.Fatalf("resolved credential is empty: %+v", res)
	}
}
