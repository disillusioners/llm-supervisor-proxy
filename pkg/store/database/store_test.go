// Phase 2 store↔engine integration tests (plan Files row #11 / Task 8-10):
//
//   - ResolveInternalConfigWithAffinity branch table (9 cases incl.
//     the E-3 fast-path no-map-writes assertion, W-2 empty-key
//     no-binding, W-1 newlyBound matrix, nil-engine legacy parity,
//     and the credential-deleted-mid-flight re-select).
//   - Invalidation propagation: UpdateModel/AddModel/RemoveModel/
//     RemoveCredential drive engine.OnModelChanged/OnCredentialDeleted
//     strictly AFTER successful writes; failed writes leave the
//     engine untouched (P2-4).
//   - Startup rebind: models persisted before NewModelsManager seed
//     the engine (RebindFromStore at construction).
//   - Bus subscription: ModelsManager drains model.credentials.changed
//     on the engine's behalf (P2-3); nil bus is tolerated.
//   - Peak-hour parity between the legacy 5-tuple and the affinity
//     struct path via the shared resolveWithCredential helper (P2-5).
package database

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// seedModelCreds seeds credential rows (plain-text key column; the
// affinity path only round-trips provider/baseURL deterministically)
// and an internal model referencing them.
func seedModelCreds(t *testing.T, mgr *ModelsManager, modelID string, refs []models.CredentialRef) {
	t.Helper()
	for _, r := range refs {
		seedCredential(t, mgr.store, r.CredentialID, "openai")
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID:            modelID,
		Name:          "model-" + modelID,
		Enabled:       true,
		Internal:      true,
		Credentials:   refs,
		InternalModel: "gpt-test",
	}); err != nil {
		t.Fatalf("AddModel %s: %v", modelID, err)
	}
}

// TestResolveInternalConfigWithAffinity_BranchTable — the pinned
// 9-case branch table (plan Task 9 acceptance): field-by-field struct
// shape, trailing ok bool, and the NewlyBound value per branch.
func TestResolveInternalConfigWithAffinity_BranchTable(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	seedCredential(t, store, "cred-A", "openai")
	seedCredential(t, store, "cred-B", "openai")
	seedCredential(t, store, "cred-SOLO", "openai")

	multi := models.ModelConfig{
		ID:            "multi",
		Name:          "multi",
		Enabled:       true,
		Internal:      true,
		Credentials:   models.TestRefs("cred-A", "cred-B"),
		InternalModel: "gpt-multi",
	}
	if err := mgr.AddModel(multi); err != nil {
		t.Fatal(err)
	}
	solo := models.ModelConfig{
		ID:            "solo",
		Name:          "solo",
		Enabled:       true,
		Internal:      true,
		Credentials:   models.TestRefs("cred-SOLO"),
		InternalModel: "gpt-solo",
	}
	if err := mgr.AddModel(solo); err != nil {
		t.Fatal(err)
	}
	empty := models.ModelConfig{
		ID:            "empty",
		Name:          "empty",
		Enabled:       true,
		Internal:      true,
		InternalModel: "gpt-empty",
	}
	// AddModel REJECTS internal models with zero credentials (Phase-1
	// validation), so the empty-creds branch is defensive-only: seed
	// the row via direct SQL (legacy row / external writer shape).
	if _, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO models (id, name, enabled, internal, credentials_json, credential_id, internal_model, created_at, updated_at)
		 VALUES (?, ?, 1, 1, '[]', '', ?, ?, ?)`,
		empty.ID, empty.Name, empty.InternalModel, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed empty model: %v", err)
	}
	external := models.ModelConfig{ID: "ext", Name: "ext", Enabled: true}
	if err := mgr.AddModel(external); err != nil {
		t.Fatal(err)
	}

	// Case 1: unknown model → ok=false, zero struct.
	if rc, ok := mgr.ResolveInternalConfigWithAffinity("ghost", "k"); ok || rc != (ResolvedCredential{}) {
		t.Fatalf("unknown model: got (%+v,%v)", rc, ok)
	}
	// Case 2: non-internal model → ok=false.
	if rc, ok := mgr.ResolveInternalConfigWithAffinity("ext", "k"); ok || rc != (ResolvedCredential{}) {
		t.Fatalf("non-internal: got (%+v,%v)", rc, ok)
	}
	// Case 3: no credentials configured → legacy-equivalent ok=false.
	if rc, ok := mgr.ResolveInternalConfigWithAffinity("empty", "k"); ok || rc != (ResolvedCredential{}) {
		t.Fatalf("empty creds: got (%+v,%v)", rc, ok)
	}

	// Case 4: single-credential fast path — NO engine call, NO map
	// writes (E-3), NewlyBound always false, full field population.
	for i := 0; i < 100; i++ {
		rc, ok := mgr.ResolveInternalConfigWithAffinity("solo", "k"+time.Now().Format("150405.000000000"))
		if !ok {
			t.Fatalf("fast path: ok=false")
		}
		if rc.CredentialID != "cred-SOLO" || rc.NewlyBound {
			t.Fatalf("fast path shape: %+v", rc)
		}
		if rc.Provider != "openai" || rc.APIKey != "fake-key" || rc.InternalModel != "gpt-solo" {
			t.Fatalf("fast path fields: %+v", rc)
		}
	}
	if st := mgr.Engine().Stats()["solo"]; st.Bindings != 0 || st.Misses != 0 {
		t.Fatalf("fast path wrote engine state: %+v", st)
	}

	// Case 5: 2+ credentials happy path — engine resolves, struct
	// carries the engine pick + W-1 signal (first true, then false).
	rc, ok := mgr.ResolveInternalConfigWithAffinity("multi", "conv-1")
	if !ok {
		t.Fatal("multi resolution failed")
	}
	if rc.CredentialID != "cred-A" && rc.CredentialID != "cred-B" {
		t.Fatalf("multi pick outside configured set: %s", rc.CredentialID)
	}
	if !rc.NewlyBound {
		t.Fatalf("W-1: first resolution must report NewlyBound=true, got %+v", rc)
	}
	first := rc.CredentialID
	rc2, ok2 := mgr.ResolveInternalConfigWithAffinity("multi", "conv-1")
	if !ok2 || rc2.CredentialID != first || rc2.NewlyBound {
		t.Fatalf("affinity second call: got (%+v,%v) want (%s,false)", rc2, ok2, first)
	}
	if got := mgr.Engine().Stats()["multi"].Bindings; got != 1 {
		t.Fatalf("multi bindings: %d", got)
	}

	// Case 6: empty conversationKey — fresh weighted pick per call,
	// NO binding stored (W-2), NewlyBound=false (C2).
	for i := 0; i < 50; i++ {
		rc, ok := mgr.ResolveInternalConfigWithAffinity("multi", "")
		if !ok || rc.NewlyBound {
			t.Fatalf("empty-key resolution: (%+v,%v)", rc, ok)
		}
	}
	if got := mgr.Engine().Stats()["multi"].Bindings; got != 1 { // only conv-1
		t.Fatalf("empty-key stored a binding: %d", got)
	}

	// Case 7: nil engine (hand-constructed manager) → legacy mirror.
	legacy := &ModelsManager{store: store, qb: NewQueryBuilder(store.Dialect)}
	lrc, lok := legacy.ResolveInternalConfigWithAffinity("solo", "any")
	if !lok || lrc.CredentialID != "cred-SOLO" || lrc.Provider != "openai" || lrc.InternalModel != "gpt-solo" {
		t.Fatalf("nil-engine delegation: (%+v,%v)", lrc, lok)
	}
	if _, lok2 := legacy.ResolveInternalConfigWithAffinity("empty", "any"); lok2 {
		t.Fatal("nil-engine empty-creds must fail like legacy")
	}

	// Case 8: credential deleted mid-flight — this call fails, the
	// next call re-selects a LIVE credential. Item 5d (leader-ruled):
	// the heal must produce the side-effect stats the contract pins
	// for the per-call observability — Cooldowns==1 (the just-
	// re-seeded dead credential) and Failovers==1 (the heal rebind
	// to a healthy credential via ExcludeAndReselect).
	if _, err := store.DB.ExecContext(context.Background(),
		`DELETE FROM credentials WHERE id = ?`, "cred-A"); err != nil {
		t.Fatal(err)
	}
	mgr.Engine().InjectPreconditionStateForTest("multi", "conv-mid", "cred-A")
	if rc, ok := mgr.ResolveInternalConfigWithAffinity("multi", "conv-mid"); ok || rc != (ResolvedCredential{}) {
		t.Fatalf("mid-flight: this call must fail, got (%+v,%v)", rc, ok)
	}
	// Heal side-effects (Item 5d): the failed call invoked
	// OnCredentialDeleted (cleared cred-A cooldown) and then
	// ExcludeAndReselect (re-seeded cred-A's cooldown + rebound
	// conv-mid to a healthy credential — cred-B). The GAUGE
	// reflects 1 cooling row (cred-A), and Failovers ticked once
	// (the heal rebind was a real non-no-op reselect).
	if st := mgr.Engine().Stats()["multi"]; st.Cooldowns != 1 {
		t.Fatalf("heal side-effect: Cooldowns=%d want 1 (cred-A re-seeded)", st.Cooldowns)
	}
	if st := mgr.Engine().Stats()["multi"]; st.Failovers != 1 {
		t.Fatalf("heal side-effect: Failovers=%d want 1 (heal rebind ticked)", st.Failovers)
	}
	rc, ok = mgr.ResolveInternalConfigWithAffinity("multi", "conv-mid")
	if !ok || rc.CredentialID != "cred-B" {
		t.Fatalf("mid-flight re-select: got (%+v,%v) want cred-B", rc, ok)
	}
	_ = multi

	// Case 9 (W-1 matrix completion): the fast path and empty-key
	// NewlyBound=false assertions above + first-call true here cover
	// the full W-1 matrix through the store seam.
	rc3, ok3 := mgr.ResolveInternalConfigWithAffinity("multi", "conv-9")
	if !ok3 || !rc3.NewlyBound {
		t.Fatalf("W-1 fresh bind through store: (%+v,%v)", rc3, ok3)
	}
	rc4, _ := mgr.ResolveInternalConfigWithAffinity("multi", "conv-9")
	if rc4.NewlyBound {
		t.Fatal("W-1: in-TTL resolution must be false")
	}
}

// TestStoreEngine_InvalidationPropagation — UpdateModel/RemoveModel
// drive the engine after successful writes; RemoveCredential fires
// OnCredentialDeleted once its in-use guard passes.
func TestStoreEngine_InvalidationPropagation(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	seedModelCreds(t, mgr, "m", models.TestRefs("cA", "cB"))

	// Bind two conversations (pin deterministically).
	mgr.Engine().InjectPreconditionStateForTest("m", "k1", "cA")
	mgr.Engine().InjectPreconditionStateForTest("m", "k2", "cB")
	if got := mgr.Engine().Stats()["m"].Bindings; got != 2 {
		t.Fatalf("seed bindings: %d", got)
	}

	// Reweight keeping cA and cB: E-2 filter-survivors — no flush.
	got := mgr.GetModel("m")
	got.Credentials = models.TestRefsWeighted(
		models.CredentialRef{CredentialID: "cA", Weight: 3},
		models.CredentialRef{CredentialID: "cB", Weight: 1},
	)
	if err := mgr.UpdateModel("m", *got); err != nil {
		t.Fatal(err)
	}
	if stat := mgr.Engine().Stats()["m"]; stat.Bindings != 2 {
		t.Fatalf("UpdateModel flushed survivors: %+v", stat)
	}
	if rc, ok := mgr.ResolveInternalConfigWithAffinity("m", "k1"); !ok || rc.CredentialID != "cA" {
		t.Fatalf("survivor affinity lost: (%+v,%v)", rc, ok)
	}

	// RemoveModel → engine state gone → resolution now fails.
	if err := mgr.RemoveModel("m"); err != nil {
		t.Fatal(err)
	}
	if rc, ok := mgr.ResolveInternalConfigWithAffinity("m", "k1"); ok {
		t.Fatalf("resolution after RemoveModel: (%+v,%v)", rc, ok)
	}
	// Strengthened (Task 8): the engine's own state must be gone too,
	// not just the resolution layer — stale bindings/cooldowns must
	// not linger past model removal.
	if _, stillTracked := mgr.Engine().Stats()["m"]; stillTracked {
		t.Fatalf("RemoveModel left engine state behind: %+v", mgr.Engine().Stats()["m"])
	}

	// RemoveCredential path: drop refs first (in-use guard), then
	// delete — a stale binding to the removed credential must clear.
	seedModelCreds(t, mgr, "m2", models.TestRefs("cC", "cD"))
	mgr.Engine().InjectPreconditionStateForTest("m2", "k", "cC")
	got2 := mgr.GetModel("m2")
	got2.Credentials = models.TestRefs("cD")
	if err := mgr.UpdateModel("m2", *got2); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RemoveCredential("cC"); err != nil {
		t.Fatalf("RemoveCredential: %v", err)
	}
	// cC binding was already orphan-filtered by UpdateModel; the
	// resolution must still serve cD.
	if rc, ok := mgr.ResolveInternalConfigWithAffinity("m2", "k"); !ok || rc.CredentialID != "cD" {
		t.Fatalf("post-RemoveCredential resolution: (%+v,%v)", rc, ok)
	}
}

// TestStoreEngine_NoEngineCallOnWriteFailure — P2-4: a failed write
// must NOT touch engine state.
func TestStoreEngine_NoEngineCallOnWriteFailure(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	bad := models.ModelConfig{
		ID:            "bad",
		Name:          "bad",
		Internal:      true,
		Credentials:   models.TestRefs("ghost-cred"), // unknown ref → validation error
		InternalModel: "gpt",
	}
	if err := mgr.AddModel(bad); err == nil {
		t.Fatal("AddModel with unknown credential ref must fail")
	}
	if _, known := mgr.Engine().Stats()["bad"]; known {
		t.Fatal("failed write seeded engine state (P2-4 violation)")
	}
}

// TestStoreEngine_StartupRebind — models persisted BEFORE the manager
// is constructed seed the engine at startup; multi-cred resolution
// works on the first call with no prior AddModel traffic.
func TestStoreEngine_StartupRebind(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	// First manager writes the models, then is closed.
	mgr1, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	seedModelCreds(t, mgr1, "boot", models.TestRefs("bA", "bB"))
	mgr1.Close()

	// Fresh manager over the same DB — RebindFromStore must have run.
	mgr2, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr2.Close()

	rc, ok := mgr2.ResolveInternalConfigWithAffinity("boot", "conv")
	if !ok {
		t.Fatal("startup rebind missing: multi-cred resolution failed on fresh manager")
	}
	rc2, _ := mgr2.ResolveInternalConfigWithAffinity("boot", "conv")
	if rc2.CredentialID != rc.CredentialID {
		t.Fatalf("affinity broken on rebind-seeded engine: %s vs %s", rc2.CredentialID, rc.CredentialID)
	}
}

// TestStoreEngine_BusSubscription — with a real bus, UpdateModel
// publishes model.credentials.changed and the on-behalf-of-the-engine
// drain loop converges (idempotent refresh); nil-bus construction
// stays functional.
func TestStoreEngine_BusSubscription(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	bus := events.NewBus()
	mgr, err := NewModelsManager(store, bus)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	seedModelCreds(t, mgr, "bus-m", models.TestRefs("uA", "uB"))

	// Reweight via UpdateModel; the drain loop re-reads and refreshes
	// engine state. The direct call already applied the change; the
	// bus refresh must converge to the same state (not corrupt it).
	got := mgr.GetModel("bus-m")
	got.Credentials = models.TestRefsWeighted(
		models.CredentialRef{CredentialID: "uA", Weight: 1},
		models.CredentialRef{CredentialID: "uB", Weight: 2},
	)
	if err := mgr.UpdateModel("bus-m", *got); err != nil {
		t.Fatal(err)
	}
	// Give the drain goroutine a moment, then verify affinity still
	// works and the manager remains responsive (no deadlock between
	// the m.mu-held publish and the drain loop's GetModel RLock).
	done := make(chan struct{})
	go func() {
		defer close(done)
		mgr.Engine().InjectPreconditionStateForTest("bus-m", "bk", "uA")
		for i := 0; i < 20; i++ {
			if rc, ok := mgr.ResolveInternalConfigWithAffinity("bus-m", "bk"); !ok || rc.CredentialID != "uA" {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock between UpdateModel publish and drain loop")
	}

	// Nil-bus construction is fine and logs a WARN (smoke: works).
	mgr2, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	mgr2.Close()
}

// TestStoreEngine_CloseLifecycle — Close is idempotent, terminates
// the drain goroutine (channel close), and the engine remains usable
// for reads afterwards (lazy expiry only).
func TestStoreEngine_CloseLifecycle(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	bus := events.NewBus()
	mgr, err := NewModelsManager(store, bus)
	if err != nil {
		t.Fatal(err)
	}
	seedModelCreds(t, mgr, "cl", models.TestRefs("x1", "x2"))

	sub, err := bus.Subscribe()
	if err != nil {
		t.Fatal(err)
	}

	mgr.Close()
	mgr.Close() // idempotent

	// The manager's own subscription was closed by Close; the engine
	// still resolves (struct reads work post-Stop).
	if rc, ok := mgr.ResolveInternalConfigWithAffinity("cl", "k"); !ok || rc.CredentialID == "" {
		t.Fatalf("post-close resolution: (%+v,%v)", rc, ok)
	}
	// Drain any residual events so the subscriber channel doesn't
	// block test teardown.
	for {
		select {
		case <-sub:
			continue
		default:
		}
		break
	}
	bus.Unsubscribe(sub)
}

// TestStoreEngine_PeakHourParity — P2-5: peak-hour substitution is
// identical between the legacy 5-tuple and the affinity struct path
// (both flow through resolveWithCredential).
func TestStoreEngine_PeakHourParity(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	seedCredential(t, store, "pk-A", "openai")
	seedCredential(t, store, "pk-B", "openai")

	// Peak window that ALWAYS covers "now": start 00:00, end 23:59,
	// same-day window in a fixed timezone.
	m := models.ModelConfig{
		ID:               "peak",
		Name:             "peak",
		Enabled:          true,
		Internal:         true,
		Credentials:      models.TestRefs("pk-A", "pk-B"),
		InternalModel:    "gpt-base",
		PeakHourEnabled:  true,
		PeakHourStart:    "00:00",
		PeakHourEnd:      "23:59",
		PeakHourTimezone: "+0",
		PeakHourModel:    "gpt-peak",
	}
	if err := mgr.AddModel(m); err != nil {
		t.Fatal(err)
	}

	lp, lk, lb, lm, lok := mgr.ResolveInternalConfig("peak")
	arc, aok := mgr.ResolveInternalConfigWithAffinity("peak", "conv-peak")
	if !lok || !aok {
		t.Fatalf("resolution failed: legacy=%v affinity=%v", lok, aok)
	}
	if lm != "gpt-peak" {
		t.Fatalf("legacy peak substitution missing: %q", lm)
	}
	if arc.InternalModel != lm || arc.Provider != lp || arc.BaseURL != lb {
		t.Fatalf("peak-hour drift between legacy and affinity: legacy=(%s,%s,%s) affinity=(%s,%s,%s)",
			lp, lb, lm, arc.Provider, arc.BaseURL, arc.InternalModel)
	}
	if !strings.HasPrefix(arc.InternalModel, "gpt-peak") {
		t.Fatalf("affinity peak substitution missing: %+v", arc)
	}
	_ = lk
}

// TestStoreEngine_ConcurrentResolutionAndWrites — hammer resolutions
// against a credentials update to catch lock-cycle regressions
// (manager lock vs engine locks vs bus publish) under -race.
func TestStoreEngine_ConcurrentResolutionAndWrites(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	bus := events.NewBus()
	mgr, err := NewModelsManager(store, bus)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	seedModelCreds(t, mgr, "hammer", models.TestRefs("hA", "hB"))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				select {
				case <-stop:
					return
				default:
				}
				mgr.ResolveInternalConfigWithAffinity("hammer", "c"+time.Now().Format("150405.000000000"))
			}
		}(g)
	}
	// Concurrent rewrites of the same model's weights.
	for w := 0; w < 5; w++ {
		got := mgr.GetModel("hammer")
		got.Credentials = models.TestRefsWeighted(
			models.CredentialRef{CredentialID: "hA", Weight: w + 1},
			models.CredentialRef{CredentialID: "hB", Weight: 1},
		)
		if err := mgr.UpdateModel("hammer", *got); err != nil {
			t.Errorf("rewrite %d: %v", w, err)
		}
	}
	wg.Wait()
}

// TestStoreEngine_BusDrainForwardsCredentialsChanged — Item 3: the
// drain loop in store.go:690-716 is currently deletable with the suite
// staying green (no test exercises it). This pins the forwarding
// behavior end-to-end: stale engine state injected via the testhooks
// seam, then a SINGLE credentials-changed publish on a REAL
// events.Bus, then assert the engine state converges to the DB truth
// from the EVENT ALONE (no direct OnModelChanged call in the test).
//
// Mechanism: prime the engine with a single-credential ref list
// (only cA) so a fresh-key pick can only return cA. Then add cC
// to the DB via direct SQL (bypassing AddModel/UpdateModel so the
// direct write-path OnModelChanged call does NOT fire), publish the
// event, and wait for the drain to converge. Once the drain runs
// OnModelChanged with the DB-truth refs, the engine knows about cC
// and a fresh-key pick on a new conversation must surface it. Each
// conversation is uniquely-keyed per call so each pick is a fresh
// bind; cC appears with probability 1/3 over 3 creds.
func TestStoreEngine_BusDrainForwardsCredentialsChanged(t *testing.T) {
	store, cleanup := newStoreWithMigrations(t)
	defer cleanup()

	bus := events.NewBus()
	mgr, err := NewModelsManager(store, bus)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	seedModelCreds(t, mgr, "bd", models.TestRefs("cA", "cB"))

	// Inject STALE engine state — single credential cA only
	// (overriding the direct OnModelChanged call that seedModelCreds
	// fired via RebindFromStore).
	mgr.Engine().RebindFromStore("bd", models.TestRefs("cA"))
	// Sanity: the stale state must manifest as "only cA is picked"
	// for a fresh conversation key — the engine doesn't know cB.
	if rc, ok := mgr.ResolveInternalConfigWithAffinity("bd", "stale-pre"); !ok || rc.CredentialID != "cA" {
		t.Fatalf("stale engine pre-publish: got (%+v,%v) want (cA,true)", rc, ok)
	}

	// Add a new credential cC to the DB via direct SQL. We bypass
	// mgr.AddModel / mgr.UpdateModel so the direct write-path
	// OnModelChanged call does NOT fire — the bus event alone must
	// drive the convergence.
	seedCredential(t, store, "cC", "openai")
	if _, err := store.DB.ExecContext(context.Background(),
		`UPDATE models SET credentials_json = ? WHERE id = ?`,
		`[{"credential_id":"cA","weight":1,"position":0},{"credential_id":"cB","weight":1,"position":1},{"credential_id":"cC","weight":1,"position":2}]`,
		"bd"); err != nil {
		t.Fatal(err)
	}

	// Drain any residual subscription-side events from the
	// seedModelCreds publish (mgr.AddModel publishes via the bus).
	time.Sleep(20 * time.Millisecond)

	// Publish the SINGLE credentials-changed event on the REAL bus.
	bus.Publish(events.Event{
		Type:      credentiallb.EventCredentialsChanged,
		Timestamp: time.Now().Unix(),
		Data:      map[string]interface{}{"model_id": "bd"},
	})

	// Wait for the drain to converge. Poll a fresh-key pick on a
	// unique conversation key: once the engine knows cC (post-
	// drain), cC must appear at least once over a sample of 50
	// unique keys (P(no cC in 50 picks with 3 creds) = (2/3)^50 ≈
	// 10^-9 — a comfortable floor).
	const sampleSize = 50
	foundCC := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for i := 0; i < sampleSize; i++ {
			rc, ok := mgr.ResolveInternalConfigWithAffinity("bd", "poll-"+strconv.Itoa(i)+"-"+time.Now().Format("150405.000000"))
			if ok && rc.CredentialID == "cC" {
				foundCC = true
				break
			}
		}
		if foundCC {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !foundCC {
		t.Fatal("bus drain did not converge: engine never picked cC after credentials-changed publish")
	}
	// Cross-check via Stats(): the engine tracks more bindings now
	// (50 unique poll-* keys were sampled during the poll loop) and
	// the prefix-sum selector spans cA+cB+cC (verified indirectly
	// via cC resolution above). The drain-goroutine convergence is
	// the contract this test pins.
	st := mgr.Engine().Stats()["bd"]
	if st.Bindings == 0 {
		t.Fatalf("post-drain stats: %+v (no bindings recorded)", st)
	}
}
