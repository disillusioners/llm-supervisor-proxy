package modelscache

// outage_test.go — the ≥1h simulated-outage acceptance test (success
// criteria A–D, H). Uses the REAL *database.ModelsManager over a real
// SQLite store (so the strict reads, the engine path, and the
// resolver variant are production code) behind a small outage
// wrapper that injects connection-refused while "down" and delegates
// while "up" — a deterministic stand-in for stopping the DB.

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// Compile-time proof that the production manager satisfies the
// decorator's strictSource contract (the decorator's consumer side).
var _ strictSource = (*database.ModelsManager)(nil)

// ─── outage wrappers ─────────────────────────────────────────────────────────

// outageModelsSource delegates to the real manager; while down, every
// strict read answers with connection-refused (infra class). The
// resolver variant always delegates (it performs no I/O — the cache
// feeds it cached data).
type outageModelsSource struct {
	*database.ModelsManager
	down atomic.Bool
}

func (o *outageModelsSource) errIfDown() error {
	if o.down.Load() {
		return connRefused("connection refused")
	}
	return nil
}

func (o *outageModelsSource) GetModelsStrict(ctx context.Context) ([]models.ModelConfig, error) {
	if err := o.errIfDown(); err != nil {
		return nil, err
	}
	return o.ModelsManager.GetModelsStrict(ctx)
}

func (o *outageModelsSource) GetEnabledModelsStrict(ctx context.Context) ([]models.ModelConfig, error) {
	if err := o.errIfDown(); err != nil {
		return nil, err
	}
	return o.ModelsManager.GetEnabledModelsStrict(ctx)
}

func (o *outageModelsSource) GetCredentialsStrict(ctx context.Context) ([]models.CredentialConfig, error) {
	if err := o.errIfDown(); err != nil {
		return nil, err
	}
	return o.ModelsManager.GetCredentialsStrict(ctx)
}

func (o *outageModelsSource) GetModelStrict(ctx context.Context, modelID string) (*models.ModelConfig, error) {
	if err := o.errIfDown(); err != nil {
		return nil, err
	}
	return o.ModelsManager.GetModelStrict(ctx, modelID)
}

func (o *outageModelsSource) GetModelByNameStrict(ctx context.Context, modelName string) (*models.ModelConfig, error) {
	if err := o.errIfDown(); err != nil {
		return nil, err
	}
	return o.ModelsManager.GetModelByNameStrict(ctx, modelName)
}

func (o *outageModelsSource) GetCredentialStrict(ctx context.Context, id string) (*models.CredentialConfig, error) {
	if err := o.errIfDown(); err != nil {
		return nil, err
	}
	return o.ModelsManager.GetCredentialStrict(ctx, id)
}

// ─── mutator overrides (additive, edge-case coverage) ─────────────────────
//
// The original outageModelsSource only wrapped the strict reads so
// out-of-band writes could reach the physical DB during an outage
// simulation (the existing recovery test calls st.mgr.AddModel
// directly). Edge-case coverage for the cache's write-through path
// during outage (e.g. db-cache-layer edge-case test (d)) needs the
// mutators to honor the down flag too. Existing tests never call
// these overrides with down=true (mutators always go through
// st.mgr.X), so the additions are behavior-preserving for the
// existing test corpus.

// outageTokenStore delegates to the real token store while up.
type outageTokenStore struct {
	auth.TokenStoreInterface
	down atomic.Bool
}

func (o *outageTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error) {
	if o.down.Load() {
		return nil, connRefused("connection refused")
	}
	return o.TokenStoreInterface.ValidateToken(ctx, plaintext)
}

// outageMutatorError is returned by the outageModelsSource mutator
// overrides when down=true. The synthetic *net.OpError shape is
// reserved for the contract-matrix outage wrappers that feed
// isInfraError; the cache's write-through path here is exercised
// with a plain error so the mutator-failure behavior matches what
// a real SQLite I/O error would look like in production.
var outageMutatorError = errors.New("outage: connection refused")

func (o *outageModelsSource) AddModel(model models.ModelConfig) error {
	if o.down.Load() {
		return outageMutatorError
	}
	return o.ModelsManager.AddModel(model)
}

func (o *outageModelsSource) UpdateModel(modelID string, model models.ModelConfig) error {
	if o.down.Load() {
		return outageMutatorError
	}
	return o.ModelsManager.UpdateModel(modelID, model)
}

func (o *outageModelsSource) RemoveModel(modelID string) error {
	if o.down.Load() {
		return outageMutatorError
	}
	return o.ModelsManager.RemoveModel(modelID)
}

func (o *outageModelsSource) AddCredential(cred models.CredentialConfig) error {
	if o.down.Load() {
		return outageMutatorError
	}
	return o.ModelsManager.AddCredential(cred)
}

func (o *outageModelsSource) UpdateCredential(id string, cred models.CredentialConfig) error {
	if o.down.Load() {
		return outageMutatorError
	}
	return o.ModelsManager.UpdateCredential(id, cred)
}

func (o *outageModelsSource) RemoveCredential(id string) error {
	if o.down.Load() {
		return outageMutatorError
	}
	return o.ModelsManager.RemoveCredential(id)
}

// ─── fixtures over the real stack ────────────────────────────────────────────

type outageStack struct {
	t       *testing.T
	dbStore *database.Store
	mgr     *database.ModelsManager
	src     *outageModelsSource
	tokSrc  *outageTokenStore
	models  *CachedModelsConfig
	tokens  *CachedTokenStore
	clk     *fakeClock
}

func newOutageStack(t *testing.T) *outageStack {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "outage.db")
	t.Setenv("DATABASE_URL", "sqlite:"+dbPath)
	dbStore, err := database.NewConnection(context.Background())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { dbStore.Close() })
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	mgr, err := database.NewModelsManager(dbStore, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	t.Cleanup(mgr.Close)

	// Seed credentials (encryption disabled in tests → plaintext
	// pass-through, which is exactly what the cache stores).
	for _, c := range []models.CredentialConfig{
		{ID: "cred-a", Provider: "openai", APIKey: "sk-outage-a", BaseURL: "https://a.example.com"},
		{ID: "cred-b", Provider: "openai", APIKey: "sk-outage-b", BaseURL: "https://b.example.com"},
	} {
		if err := mgr.AddCredential(c); err != nil {
			t.Fatalf("AddCredential: %v", err)
		}
	}
	seed := []models.ModelConfig{
		// (a) configured EXTERNAL model — passthrough class.
		{ID: "ext-configured", Name: "ext-configured", Enabled: true},
		// (b) internal single-credential model.
		{ID: "int-single", Name: "int-single", Enabled: true, Internal: true, InternalModel: "single-x",
			Credentials: []models.CredentialRef{{CredentialID: "cred-a", Weight: 1, Position: 0}}},
		// (c) internal multi-credential (affinity/engine) model.
		{ID: "int-multi", Name: "int-multi", Enabled: true, Internal: true, InternalModel: "multi-x",
			Credentials: []models.CredentialRef{
				{CredentialID: "cred-a", Weight: 1, Position: 0},
				{CredentialID: "cred-b", Weight: 1, Position: 1},
			}},
	}
	for _, m := range seed {
		if err := mgr.AddModel(m); err != nil {
			t.Fatalf("AddModel(%s): %v", m.ID, err)
		}
	}

	src := &outageModelsSource{ModelsManager: mgr}
	tokReal := auth.NewTokenStore(dbStore.DB, dbStore.Dialect)
	tokSrc := &outageTokenStore{TokenStoreInterface: tokReal}

	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	modelsCache, err := NewCachedModelsConfig(src, Options{Clock: clk.Now, ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	t.Cleanup(modelsCache.Stop)

	return &outageStack{
		t: t, dbStore: dbStore, mgr: mgr, src: src, tokSrc: tokSrc,
		models: modelsCache, tokens: NewCachedTokenStore(tokSrc, Options{Clock: clk.Now}),
		clk: clk,
	}
}

// ─── the headline test ───────────────────────────────────────────────────────

// TestOutageSimulation_NoMisroutes_OneHourSimulated drives 100
// requests across the four W7 classes through a simulated 1h DB
// outage (+10 min of token-TTL margin asserted explicitly) and asserts
// the plan's single-sentence completion test: zero misroutes to
// localhost:4001, zero silent 401s for valid tokens, zero silent-empty
// model lists; 503-signal only for the never-seen model; recovery
// converges ≤120s (W8).
func TestOutageSimulation_NoMisroutes_OneHourSimulated(t *testing.T) {
	st := newOutageStack(t)

	// Create tokens through the cache (write-through) and warm the
	// resolutions + external verdicts BEFORE the outage.
	tokPlain, tok, err := st.tokens.CreateToken(context.Background(), "svc", nil, "test", false, "", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	warm := func() {
		if _, ok := st.models.ResolveInternalConfigWithAffinity("int-single", "warmup"); !ok {
			t.Fatal("warmup single resolution failed")
		}
		if _, ok := st.models.ResolveInternalConfigWithAffinity("int-multi", "conv-mix"); !ok {
			t.Fatal("warmup multi resolution failed")
		}
		if _, _, _, _, ok := st.models.ResolveInternalConfig("int-single"); !ok {
			t.Fatal("warmup legacy resolution failed")
		}
		if m := st.models.GetModel("ext-configured"); m == nil {
			t.Fatal("warmup external model lookup failed")
		}
		if _, err := st.tokens.ValidateToken(context.Background(), tokPlain); err != nil {
			t.Fatalf("warmup token: %v", err)
		}
	}
	warm()

	// ── OUTAGE BEGINS ──────────────────────────────────────────────────────
	st.src.down.Store(true)
	st.tokSrc.down.Store(true)

	// The first failed reconciler tick flips healthy → the fail-fast
	// signal is live for unknown models.
	st.models.reconcileOnce()
	if st.models.Healthy() {
		t.Fatal("outage must flip healthy=false after a failed tick")
	}

	// +10 minutes of SIMULATED wall-clock — well past the 60s positive
	// token TTL and the 60s model negative TTL (acceptance of C2/W6:
	// auth at t>60s into the outage).
	st.clk.Advance(10 * time.Minute)

	zeroMisroutes, zeroSilent401, zeroEmptyLists := true, true, true
	var neverSeenRejected int

	// 100 requests across the four classes (a)–(d) per W7.
	for i := 0; i < 25; i++ {
		// (a) configured external model — passthrough class: the cached
		// entry keeps the boundary on the configured path; nothing
		// silently falls back to the raw-default upstream.
		if m := st.models.GetModel("ext-configured"); m == nil {
			zeroMisroutes = false
			t.Errorf("iter %d: configured external model vanished during outage", i)
		}

		// (b) internal single-credential: both resolver surfaces stay
		// internal (never "external" — the misroute invariant) and keep
		// returning the cached credential.
		res, ok := st.models.ResolveInternalConfigWithAffinity("int-single", "conv-single")
		if !ok || res.Provider != "openai" || res.APIKey != "sk-outage-a" {
			zeroMisroutes = false
			t.Errorf("iter %d: single-cred resolution degraded: %+v ok=%v", i, res, ok)
		}
		if p := normalizers.DetectProvider(st.models, "int-single"); p != "openai" {
			zeroMisroutes = false
			t.Errorf("iter %d: DetectProvider under outage = %q (misroute class)", i, p)
		}
		if _, _, _, m, ok := st.models.ResolveInternalConfig("int-single"); !ok || m != "single-x" {
			zeroMisroutes = false
			t.Errorf("iter %d: legacy resolver degraded (ok=%v model=%q)", i, ok, m)
		}

		// (c) internal multi-credential affinity: conversation sticks
		// to its credential from cache.
		resA, okA := st.models.ResolveInternalConfigWithAffinity("int-multi", "conv-affinity")
		resB, okB := st.models.ResolveInternalConfigWithAffinity("int-multi", "conv-affinity")
		if !okA || !okB || resA.CredentialID != resB.CredentialID {
			zeroMisroutes = false
			t.Errorf("iter %d: affinity broke: %+v/%+v", i, resA, resB)
		}

		// (d) credFailover class: the 429'd credential is excluded at
		// the engine seam and the SAME conversation re-resolves onto
		// the surviving credential — all cache-served.
		excluded := resA.CredentialID
		st.mgr.Engine().ExcludeAndReselect("int-multi", "conv-affinity", excluded, 0)
		resF, okF := st.models.ResolveInternalConfigWithAffinity("int-multi", "conv-affinity")
		if !okF || resF.CredentialID == excluded {
			zeroMisroutes = false
			t.Errorf("iter %d: credFailover resolution failed to switch (excluded=%s got=%+v)", i, excluded, resF)
		}

		// Auth at t>60s into the outage: known tokens ride the stale
		// tier (degraded-allow) — zero silent 401s.
		vt, verr := st.tokens.ValidateToken(context.Background(), tokPlain)
		if verr != nil || vt == nil || vt.ID != tok.ID {
			zeroSilent401 = false
			t.Errorf("iter %d: known token rejected during outage: %v", i, verr)
		}

		// Model lists never silently go empty.
		if len(st.models.GetModels()) == 0 || len(st.models.GetEnabledModels()) == 0 {
			zeroEmptyLists = false
			t.Errorf("iter %d: silent-empty model list during outage", i)
		}

		// The never-seen model is the ONLY failure class: nil + not
		// healthy → the boundary 503s (asserted end-to-end in the 1D
		// failsafe tests; here we pin the signal).
		if m := st.models.GetModel("never-seen-model"); m != nil {
			t.Errorf("iter %d: never-seen model resolved during outage", i)
		} else {
			neverSeenRejected++
		}

		// Advance simulated time: after 25 iterations of ~2.5 min each
		// the outage has run ≥1h of simulated wall-clock.
		st.clk.Advance(150 * time.Second)
	}

	if !zeroMisroutes {
		t.Error("FAIL: misroute-class degradation observed during the outage")
	}
	if !zeroSilent401 {
		t.Error("FAIL: silent 401 observed for a valid token during the outage")
	}
	if !zeroEmptyLists {
		t.Error("FAIL: silent-empty model list observed during the outage")
	}
	if neverSeenRejected != 25 {
		t.Errorf("never-seen model must be consistently rejected, got %d/25", neverSeenRejected)
	}

	// ── OUTAGE ENDS — recovery convergence ≤120s (W8) ─────────────────────
	// An out-of-band write happens while the cache is still cut off
	// (direct SQL / direct manager call — invisible to the decorator).
	if err := st.mgr.AddModel(models.ModelConfig{ID: "int-added", Name: "int-added", Enabled: true,
		Internal: true, InternalModel: "added-x",
		Credentials: []models.CredentialRef{{CredentialID: "cred-a", Weight: 1, Position: 0}}}); err != nil {
		t.Fatalf("out-of-band AddModel: %v", err)
	}

	st.src.down.Store(false)
	st.tokSrc.down.Store(false)

	// One tick past the first 60s window — the reconciler swaps the
	// post-recovery state in (≤120s worst case per W8).
	st.clk.Advance(70 * time.Second)
	st.models.reconcileOnce()
	if !st.models.Healthy() {
		t.Fatal("recovery tick must restore healthy=true")
	}
	if m := st.models.GetModel("int-added"); m == nil {
		t.Fatal("post-recovery tick must converge to the new DB state")
	}
	if res, ok := st.models.ResolveInternalConfigWithAffinity("int-added", "post-recovery"); !ok || res.APIKey != "sk-outage-a" {
		t.Fatalf("post-recovery resolution: %+v ok=%v", res, ok)
	}
	// Tokens revalidate normally once the store answers.
	if vt, err := st.tokens.ValidateToken(context.Background(), tokPlain); err != nil || vt.ID != tok.ID {
		t.Fatalf("post-recovery token validation: %v %v", vt, err)
	}
}

// TestOutageSimulation_DecryptFailedCredentialIsNeverServed pins
// criterion N end-to-end over the real store: a credential whose
// stored ciphertext cannot be decrypted never becomes a usable APIKey.
func TestOutageSimulation_DecryptFailedCredentialIsNeverServed(t *testing.T) {
	// Enable REAL encryption for this test so a garbage ciphertext
	// actually fails decryption (with no key configured Decrypt is a
	// pass-through and nothing can fail).
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	crypto.ResetEncryptionState()
	t.Setenv(crypto.EnvEncryptionKey, key)
	if err := crypto.InitEncryption(); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	t.Cleanup(func() { crypto.ResetEncryptionState() })

	st := newOutageStack(t)

	// Raw-insert an undecryptable credential row (bypasses
	// encrypt-on-write), then reference it from a new model.
	if _, err := st.dbStore.DB.Exec(
		`INSERT INTO credentials (id, provider, api_key, base_url, created_at, updated_at) VALUES (?, ?, ?, '', datetime('now'), datetime('now'))`,
		"cred-undecryptable", "openai", "not-a-ciphertext-ENOENT-style"); err != nil {
		t.Fatalf("raw insert: %v", err)
	}
	if err := st.mgr.AddModel(models.ModelConfig{ID: "int-bad-cred", Name: "int-bad-cred", Enabled: true,
		Internal: true, InternalModel: "bad-x",
		Credentials: []models.CredentialRef{{CredentialID: "cred-undecryptable", Weight: 1, Position: 0}}}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// The cache's view: strict single-row read returns
	// ErrDecryptionFailed → decorator negative-caches, serves nil.
	if cred := st.models.GetCredential("cred-undecryptable"); cred != nil {
		t.Fatalf("undecryptable credential must never be served, got %+v", cred)
	}
	// Resolution for a model referencing it fails closed — never 200
	// with ciphertext-as-key, never a silent misroute.
	if res, ok := st.models.ResolveInternalConfigWithAffinity("int-bad-cred", "conv"); ok {
		t.Fatalf("resolution over undecryptable credential must fail, got %+v", res)
	}
}
