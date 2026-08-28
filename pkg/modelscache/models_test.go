package modelscache

// models_test.go — unit tests for CachedModelsConfig (boot priming,
// write-through, negative cache, reconciler, deep copy, hot-path
// resolvers, dead-default WARN, LRU).

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"

	// Test-only import: DetectProvider is the W1 consumer whose
	// DB-down behavior the decorator must fix. normalizers does not
	// import modelscache, so the package's one-way source edge holds.
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy/normalizers"
)

// fakeClock is the N6 injection point.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }
func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}
func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// fakeStrictSource is an in-memory strictSource with error injection
// and call counting.
type fakeStrictSource struct {
	mu       sync.Mutex
	models   []models.ModelConfig
	creds    []models.CredentialConfig
	listErr  error // injected into all three list reads
	modelErr error // injected into single-row model reads
	credErr  error // injected into single-row credential reads
	stall    chan struct{}

	counts struct {
		listModels, listEnabled, listCreds int
		getModel, getModelByName, getCred  int
		legacyGetModel, legacyGetCred      int
		legacyResolve, resolveAffinity     int
		resolveCached                      int
		addModel, updateModel, removeModel int
		addCred, updateCred, removeCred    int
	}
}

func (f *fakeStrictSource) snapshotCounts() (m, mc, gc int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts.getModel, f.counts.getModelByName, f.counts.getCred
}

func (f *fakeStrictSource) listCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts.listModels + f.counts.listEnabled + f.counts.listCreds
}

func (f *fakeStrictSource) legacyCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts.legacyGetModel + f.counts.legacyGetCred + f.counts.legacyResolve + f.counts.resolveAffinity
}

// maybeStall blocks until the stall channel closes or ctx is done
// (used by the Stop-cancels-inflight-scan test).
func (f *fakeStrictSource) maybeStall(ctx context.Context) error {
	if f.stall == nil {
		return nil
	}
	select {
	case <-f.stall:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeStrictSource) findModel(id string) *models.ModelConfig {
	for i := range f.models {
		if f.models[i].ID == id {
			cp := f.models[i]
			return &cp
		}
	}
	return nil
}

// ─── strict reads ────────────────────────────────────────────────────────────

func (f *fakeStrictSource) GetModelsStrict(ctx context.Context) ([]models.ModelConfig, error) {
	f.mu.Lock()
	f.counts.listModels++
	f.mu.Unlock()
	if err := f.maybeStall(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]models.ModelConfig, len(f.models))
	copy(out, f.models)
	return out, nil
}

func (f *fakeStrictSource) GetEnabledModelsStrict(ctx context.Context) ([]models.ModelConfig, error) {
	f.mu.Lock()
	f.counts.listEnabled++
	f.mu.Unlock()
	if err := f.maybeStall(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []models.ModelConfig
	for i := range f.models {
		if f.models[i].Enabled {
			out = append(out, f.models[i])
		}
	}
	return out, nil
}

func (f *fakeStrictSource) GetCredentialsStrict(ctx context.Context) ([]models.CredentialConfig, error) {
	f.mu.Lock()
	f.counts.listCreds++
	f.mu.Unlock()
	if err := f.maybeStall(ctx); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]models.CredentialConfig, len(f.creds))
	copy(out, f.creds)
	return out, nil
}

func (f *fakeStrictSource) GetModelStrict(ctx context.Context, modelID string) (*models.ModelConfig, error) {
	f.mu.Lock()
	f.counts.getModel++
	m := f.findModel(modelID)
	var locked models.ModelConfig
	if m != nil {
		locked = *m
	}
	err := f.modelErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, database.ErrModelNotFound
	}
	return &locked, nil
}

func (f *fakeStrictSource) GetModelByNameStrict(ctx context.Context, modelName string) (*models.ModelConfig, error) {
	f.mu.Lock()
	f.counts.getModelByName++
	var found *models.ModelConfig
	for i := range f.models {
		if f.models[i].Name == modelName {
			cp := f.models[i]
			found = &cp
		}
	}
	err := f.modelErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, database.ErrModelNotFound
	}
	return found, nil
}

func (f *fakeStrictSource) GetCredentialStrict(ctx context.Context, id string) (*models.CredentialConfig, error) {
	f.mu.Lock()
	f.counts.getCred++
	var found *models.CredentialConfig
	for i := range f.creds {
		if f.creds[i].ID == id {
			cp := f.creds[i]
			found = &cp
		}
	}
	err := f.credErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, database.ErrCredentialNotFound
	}
	return found, nil
}

// ResolveInternalConfigWithAffinityCached — faithful single/primary
// semantics via the supplied closure (the engine path is covered by
// outage_test with the real *database.ModelsManager).
func (f *fakeStrictSource) ResolveInternalConfigWithAffinityCached(cached *models.ModelConfig, conversationKey string, credLookup func(string) (*models.CredentialConfig, bool)) (models.ResolvedCredential, bool) {
	f.mu.Lock()
	f.counts.resolveCached++
	f.mu.Unlock()
	if cached == nil || !cached.Internal {
		return models.ResolvedCredential{}, false
	}
	primary := cached.PrimaryCredentialID()
	if primary == "" {
		return models.ResolvedCredential{}, false
	}
	cred, ok := credLookup(primary)
	if !ok || cred == nil {
		return models.ResolvedCredential{}, false
	}
	return models.ResolvedCredential{
		Provider:      cred.Provider,
		APIKey:        cred.APIKey,
		BaseURL:       cred.BaseURL,
		InternalModel: cached.InternalModel,
		CredentialID:  primary,
	}, true
}

// ─── legacy interface surface (must stay UNTOUCHED by the decorator's
// hot paths — the counters prove it) ─────────────────────────────────────────

func (f *fakeStrictSource) GetModel(modelID string) *models.ModelConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.legacyGetModel++
	return f.findModel(modelID)
}

func (f *fakeStrictSource) GetModelByName(modelName string) *models.ModelConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.legacyGetModel++
	for i := range f.models {
		if f.models[i].Name == modelName {
			cp := f.models[i]
			return &cp
		}
	}
	return nil
}

func (f *fakeStrictSource) GetModels() []models.ModelConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.legacyGetModel++
	out := make([]models.ModelConfig, len(f.models))
	copy(out, f.models)
	return out
}

func (f *fakeStrictSource) GetEnabledModels() []models.ModelConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.legacyGetModel++
	var out []models.ModelConfig
	for i := range f.models {
		if f.models[i].Enabled {
			out = append(out, f.models[i])
		}
	}
	return out
}

func (f *fakeStrictSource) GetCredential(id string) *models.CredentialConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.legacyGetCred++
	for i := range f.creds {
		if f.creds[i].ID == id {
			cp := f.creds[i]
			return &cp
		}
	}
	return nil
}

func (f *fakeStrictSource) GetCredentials() []models.CredentialConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.legacyGetCred++
	out := make([]models.CredentialConfig, len(f.creds))
	copy(out, f.creds)
	return out
}

func (f *fakeStrictSource) ResolveInternalConfig(modelID string) (string, string, string, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.legacyResolve++
	return "", "", "", "", false
}

func (f *fakeStrictSource) ResolveInternalConfigWithAffinity(modelID, conversationKey string) (models.ResolvedCredential, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.resolveAffinity++
	return models.ResolvedCredential{}, false
}

// ─── mutators (record calls, mutate state) ──────────────────────────────────

func (f *fakeStrictSource) AddModel(model models.ModelConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.addModel++
	for i := range f.models {
		if f.models[i].ID == model.ID {
			f.models[i] = model
			return nil
		}
	}
	f.models = append(f.models, model)
	return nil
}

func (f *fakeStrictSource) UpdateModel(modelID string, model models.ModelConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.updateModel++
	for i := range f.models {
		if f.models[i].ID == modelID {
			f.models[i] = model
			return nil
		}
	}
	return models.ErrModelNotFound
}

func (f *fakeStrictSource) RemoveModel(modelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.removeModel++
	for i := range f.models {
		if f.models[i].ID == modelID {
			f.models = append(f.models[:i], f.models[i+1:]...)
			return nil
		}
	}
	return models.ErrModelNotFound
}

func (f *fakeStrictSource) AddCredential(cred models.CredentialConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.addCred++
	for i := range f.creds {
		if f.creds[i].ID == cred.ID {
			f.creds[i] = cred
			return nil
		}
	}
	f.creds = append(f.creds, cred)
	return nil
}

func (f *fakeStrictSource) UpdateCredential(id string, cred models.CredentialConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.updateCred++
	for i := range f.creds {
		if f.creds[i].ID == id {
			f.creds[i] = cred
			return nil
		}
	}
	return errors.New("credential not found")
}

func (f *fakeStrictSource) RemoveCredential(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.removeCred++
	for i := range f.creds {
		if f.creds[i].ID == id {
			f.creds = append(f.creds[:i], f.creds[i+1:]...)
			return nil
		}
	}
	return errors.New("credential not found")
}

func (f *fakeStrictSource) Save() error     { return nil }
func (f *fakeStrictSource) Validate() error { return nil }

func (f *fakeStrictSource) GetTruncateParams(modelID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.legacyGetModel++
	m := f.findModel(modelID)
	if m == nil || len(m.TruncateParams) == 0 {
		return nil
	}
	out := make([]string, len(m.TruncateParams))
	copy(out, m.TruncateParams)
	return out
}

func (f *fakeStrictSource) GetFallbackChain(modelID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts.legacyGetModel++
	m := f.findModel(modelID)
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.FallbackChain)+1)
	out = append(out, m.ID)
	out = append(out, m.FallbackChain...)
	return out
}

// ─── fixtures ────────────────────────────────────────────────────────────────

func fixtureModels() []models.ModelConfig {
	return []models.ModelConfig{
		{ID: "m-alpha", Name: "alpha", Enabled: true, Internal: true, InternalModel: "alpha-x",
			Credentials: []models.CredentialRef{{CredentialID: "cred-1", Weight: 1, Position: 0}}},
		{ID: "m-beta", Name: "beta", Enabled: true, FallbackChain: []string{"m-alpha"}, TruncateParams: []string{"store"}},
		{ID: "m-gamma", Name: "gamma", Enabled: false},
	}
}

func fixtureCreds() []models.CredentialConfig {
	return []models.CredentialConfig{
		{ID: "cred-1", Provider: "openai", APIKey: "sk-plain-1", BaseURL: "https://one.example.com"},
		{ID: "cred-2", Provider: "zhipu", APIKey: "sk-plain-2", BaseURL: "https://two.example.com"},
	}
}

func connRefused(msg string) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(msg)}
}

func mustWrap(t *testing.T, inner *fakeStrictSource, opts Options) *CachedModelsConfig {
	t.Helper()
	c, err := WrapModels(inner, opts)
	if err != nil {
		t.Fatalf("WrapModels: %v", err)
	}
	t.Cleanup(c.Stop)
	return c
}

// ─── boot priming ────────────────────────────────────────────────────────────

func TestCachedModelsConfig_BootPriming_Happy(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	if !c.Healthy() {
		t.Fatal("expected healthy=true after successful priming")
	}
	if got := c.GetModels(); len(got) != 3 {
		t.Errorf("expected 3 cached models, got %d", len(got))
	}
	if got := c.GetEnabledModels(); len(got) != 2 {
		t.Errorf("expected 2 enabled models, got %d", len(got))
	}
	if got := c.GetCredentials(); len(got) != 2 {
		t.Errorf("expected 2 cached credentials, got %d", len(got))
	}
	if m := c.GetModel("m-alpha"); m == nil || m.InternalModel != "alpha-x" {
		t.Errorf("GetModel(m-alpha): %+v", m)
	}
	if m := c.GetModelByName("beta"); m == nil || m.ID != "m-beta" {
		t.Errorf("GetModelByName(beta): %+v", m)
	}
}

func TestCachedModelsConfig_BootPriming_Cold(t *testing.T) {
	inner := &fakeStrictSource{listErr: errors.New("DB down")}
	c, err := WrapModels(inner, Options{})
	if err == nil {
		c.Stop()
		t.Fatal("expected boot priming error, got nil")
	}
	if c != nil {
		t.Errorf("expected nil decorator on cold boot, got %+v", c)
	}
}

// ─── write-through ───────────────────────────────────────────────────────────

func TestCachedModelsConfig_WriteThrough_ModelMutators(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{Clock: clk.Now})

	// AddModel clears the negative entry for the ID AND the name.
	if c.GetModel("m-new") != nil {
		t.Fatal("m-new should not exist yet")
	}
	c.mu.RLock()
	if _, ok := c.negByID["m-new"]; !ok {
		t.Fatal("expected negative entry for m-new after not-found read")
	}
	c.mu.RUnlock()

	if err := c.AddModel(models.ModelConfig{ID: "m-new", Name: "new", Enabled: true, InternalModel: "new-x"}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	c.mu.RLock()
	_, negID := c.negByID["m-new"]
	_, negName := c.negByName["new"]
	c.mu.RUnlock()
	if negID || negName {
		t.Error("AddModel must clear negative entries for ID and name")
	}
	if m := c.GetModel("m-new"); m == nil {
		t.Fatal("AddModel write-through must make the model immediately visible")
	}

	// UpdateModel replaces the authoritative payload.
	if err := c.UpdateModel("m-new", models.ModelConfig{ID: "m-new", Name: "new2", Enabled: true, InternalModel: "replaced"}); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	if m := c.GetModel("m-new"); m == nil || m.InternalModel != "replaced" {
		t.Errorf("UpdateModel write-through: %+v", m)
	}
	if m := c.GetModelByName("new2"); m == nil {
		t.Error("rename must update the name index (new2)")
	}
	if m := c.GetModelByName("new"); m != nil {
		t.Error("rename must drop the old name index entry")
	}

	// RemoveModel evicts both ID and name maps.
	if err := c.RemoveModel("m-new"); err != nil {
		t.Fatalf("RemoveModel: %v", err)
	}
	if c.GetModel("m-new") != nil || c.GetModelByName("new2") != nil {
		t.Error("RemoveModel must evict ID and name maps")
	}
	if got := c.GetModels(); len(got) != 3 {
		t.Errorf("snapshot must reflect removal, got %d models", len(got))
	}
}

func TestCachedModelsConfig_WriteThrough_CredentialMutatorsInvalidateOnly(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	if err := c.UpdateCredential("cred-1", models.CredentialConfig{ID: "cred-1", Provider: "openai", APIKey: "sk-rotated"}); err != nil {
		t.Fatalf("UpdateCredential: %v", err)
	}
	c.mu.RLock()
	_, cached := c.credsByID["cred-1"]
	c.mu.RUnlock()
	if cached {
		t.Error("credential mutators must INVALIDATE (lazy refill), not write-through")
	}
	// Lazy refill: next read picks up the rotated key from the inner store.
	if cred := c.GetCredential("cred-1"); cred == nil || cred.APIKey != "sk-rotated" {
		t.Errorf("lazy refill after invalidation: %+v", cred)
	}

	if err := c.AddCredential(models.CredentialConfig{ID: "cred-3", Provider: "grok", APIKey: "sk-3"}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	if cred := c.GetCredential("cred-3"); cred == nil {
		t.Error("lazy refill must serve the added credential")
	}

	if err := c.RemoveCredential("cred-3"); err != nil {
		t.Fatalf("RemoveCredential: %v", err)
	}
	if cred := c.GetCredential("cred-3"); cred != nil {
		t.Errorf("removed credential must not resolve: %+v", cred)
	}
}

// ─── negative cache ──────────────────────────────────────────────────────────

func TestCachedModelsConfig_NegativeCache_TTLAndRevalidation(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{Clock: clk.Now})

	// Not-found → negative cached; repeated reads stay off the DB.
	if c.GetModel("nope") != nil {
		t.Fatal("nope must not resolve")
	}
	byID, _, _ := inner.snapshotCounts()
	first := byID
	if c.GetModel("nope") != nil {
		t.Fatal("nope must still not resolve")
	}
	byID, _, _ = inner.snapshotCounts()
	if byID != first {
		t.Error("negative cache must suppress repeat DB reads within TTL")
	}

	// Past the TTL the entry revalidates (DB read happens again).
	clk.Advance(61 * time.Second)
	if c.GetModel("nope") != nil {
		t.Fatal("nope must not resolve after TTL")
	}
	byID, _, _ = inner.snapshotCounts()
	if byID != first+1 {
		t.Errorf("expected revalidation read after TTL, calls %d → %d", first, byID)
	}
}

// TestCachedModelsConfig_UpdateModelClearsNegativeCache — review fix
// 2026-08-28 (finding 1): a model updated/renamed onto an ID or name
// that was negative-cached within the TTL window must resolve
// immediately — parity with AddModel. Previously UpdateModel left the
// negative entries in place, and GetModelByName checks negByName
// BEFORE modelsByName, so a renamed-on model shadowed its own live
// entry for up to the TTL.
func TestCachedModelsConfig_UpdateModelClearsNegativeCache(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{Clock: clk.Now})

	// Prime fresh negative entries for an absent ID and absent name.
	if c.GetModel("m-renamed") != nil {
		t.Fatal("absent ID must not resolve")
	}
	if c.GetModelByName("renamed") != nil {
		t.Fatal("absent name must not resolve")
	}

	// Update m-gamma onto BOTH the negative-cached ID and name —
	// within the TTL window (clock not advanced).
	renamed := models.ModelConfig{ID: "m-renamed", Name: "renamed", Enabled: true}
	if err := c.UpdateModel("m-gamma", renamed); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}

	if m := c.GetModel("m-renamed"); m == nil {
		t.Error("GetModel must clear the negative ID entry on update (parity with AddModel)")
	}
	if m := c.GetModelByName("renamed"); m == nil {
		t.Error("GetModelByName must clear the negative name entry on update (parity with AddModel)")
	}
}

// ─── reconciler ──────────────────────────────────────────────────────────────

func TestCachedModelsConfig_Reconciler_HappyDownRecover(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{Clock: clk.Now, ReconcileInterval: time.Hour})

	// Down tick: infra error → no swap, last-known-good preserved, unhealthy.
	inner.mu.Lock()
	inner.listErr = connRefused("connection refused")
	inner.models = nil // simulate the legacy silent-[] hazard source
	inner.creds = nil
	inner.mu.Unlock()

	c.reconcileOnce()
	if c.Healthy() {
		t.Error("reconciler tick on DB-down must set healthy=false")
	}
	if got := c.GetModels(); len(got) != 3 {
		t.Errorf("last-known-good models must be preserved, got %d", len(got))
	}
	if got := c.GetCredentials(); len(got) != 2 {
		t.Errorf("last-known-good credentials must be preserved, got %d", len(got))
	}
	if m := c.GetModel("m-alpha"); m == nil {
		t.Error("known model must still resolve from cache during outage")
	}
	if res, ok := c.ResolveInternalConfigWithAffinity("m-alpha", "conv-1"); !ok || res.Provider != "openai" {
		t.Errorf("resolution during outage: %+v ok=%v", res, ok)
	}

	// Unknown model + DB down → nil + unhealthy (boundary 503 signal).
	if m := c.GetModel("never-seen"); m != nil {
		t.Error("never-seen model during outage must return nil")
	}

	// Recovery tick: DB back with NEW state → swap happens.
	inner.mu.Lock()
	inner.listErr = nil
	inner.models = []models.ModelConfig{{ID: "m-omega", Name: "omega", Enabled: true}}
	inner.creds = fixtureCreds()
	inner.mu.Unlock()
	c.reconcileOnce()
	if !c.Healthy() {
		t.Fatal("successful tick must restore healthy=true")
	}
	if got := c.GetModels(); len(got) != 1 || got[0].ID != "m-omega" {
		t.Errorf("post-recovery swap must reflect new state: %+v", got)
	}
}

func TestCachedModelsConfig_Reconciler_AbortOnErrorPerC3(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{ReconcileInterval: time.Hour})

	// Pre-populated with 2 credentials; a transient infra-error scan
	// must NOT swap an empty credsByID.
	inner.mu.Lock()
	inner.listErr = connRefused("connection refused")
	inner.mu.Unlock()
	c.reconcileOnce()
	c.mu.RLock()
	credCount := len(c.credsByID)
	c.mu.RUnlock()
	if credCount != 2 {
		t.Fatalf("abort-on-error must preserve 2 credentials, got %d", credCount)
	}
	if c.Healthy() {
		t.Error("failed tick must set healthy=false")
	}

	// Legitimate empty credentials scan (all credentials genuinely
	// deleted) with previous NON-empty snapshot → suspicious-empty
	// guard also aborts (C3 conservative rule).
	inner.mu.Lock()
	inner.listErr = nil
	inner.creds = nil
	inner.mu.Unlock()
	c.reconcileOnce()
	c.mu.RLock()
	credCount = len(c.credsByID)
	c.mu.RUnlock()
	if credCount != 2 {
		t.Errorf("suspicious-empty guard must preserve credentials, got %d", credCount)
	}

	// Legitimate empty from a previously-empty snapshot → swap allowed.
	inner2 := &fakeStrictSource{}
	c2, err := WrapModels(inner2, Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("WrapModels empty boot: %v", err)
	}
	c2.reconcileOnce()
	if !c2.Healthy() {
		t.Error("empty-but-successful tick with empty prior snapshot must stay healthy")
	}
	c2.Stop()
}

func TestCachedModelsConfig_Reconciler_StopCancelsInflightScan(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}

	// Boot WITHOUT the stall (priming must succeed), then arm the stall
	// so the next reconciler tick blocks mid-scan.
	c, err := WrapModels(inner, Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("WrapModels: %v", err)
	}
	inner.mu.Lock()
	inner.stall = make(chan struct{})
	inner.mu.Unlock()

	scanDone := make(chan struct{})
	go func() {
		c.reconcileOnce()
		close(scanDone)
	}()

	// Give the scan a moment to enter the stall, then Stop() — the
	// in-flight context must be cancelled and the scan must return
	// promptly (it honors ctx.Done), without us releasing the stall.
	time.Sleep(50 * time.Millisecond)
	stopDone := make(chan struct{})
	go func() {
		c.Stop()
		close(stopDone)
	}()
	select {
	case <-stopDone:
		// Stop returned: either the scan honored cancellation or was
		// abandoned per the contract; both pass.
	case <-time.After(6 * time.Second):
		t.Fatal("Stop() must cancel the in-flight scan within ~5s")
	}
	select {
	case <-scanDone:
	case <-time.After(2 * time.Second):
		t.Fatal("scan goroutine must observe cancellation")
	}
	close(inner.stall)
}

// ─── deep copy ───────────────────────────────────────────────────────────────

func TestCachedModelsConfig_DeepCopy_FallbackChain(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	m := c.GetModel("m-beta")
	m.FallbackChain = append(m.FallbackChain, "injected")
	m.TruncateParams[0] = "mutated"

	m2 := c.GetModel("m-beta")
	if len(m2.FallbackChain) != 1 || m2.FallbackChain[0] != "m-alpha" {
		t.Errorf("FallbackChain mutation leaked into cache: %v", m2.FallbackChain)
	}
	if m2.TruncateParams[0] != "store" {
		t.Errorf("TruncateParams mutation leaked into cache: %v", m2.TruncateParams)
	}

	creds := c.GetCredentials()
	creds[0].APIKey = "leaked"
	if c.GetCredentials()[0].APIKey != "sk-plain-1" {
		t.Error("credential mutation leaked into cache")
	}

	// Write-through must also deep-copy INBOUND payloads: mutating the
	// caller's struct after AddModel must not affect the cache.
	in := models.ModelConfig{ID: "m-alias", Name: "alias", Enabled: true, FallbackChain: []string{"x"}}
	if err := c.AddModel(in); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	in.FallbackChain[0] = "mutated"
	if got := c.GetModel("m-alias"); got.FallbackChain[0] != "x" {
		t.Errorf("inbound aliasing leaked into cache: %v", got.FallbackChain)
	}
}

// ─── hot-path resolvers ──────────────────────────────────────────────────────

func TestCachedModelsConfig_ResolveInternalConfigWithAffinity_HotPathZeroDB(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	// Warm the model + credentials (boot priming already loaded them).
	res, ok := c.ResolveInternalConfigWithAffinity("m-alpha", "conv-1")
	if !ok {
		t.Fatal("expected ok=true on warm cache")
	}
	if res.Provider != "openai" || res.APIKey != "sk-plain-1" || res.InternalModel != "alpha-x" {
		t.Errorf("resolution mismatch: %+v", res)
	}

	// ZERO single-row reads, ZERO legacy reads — only the *Cached
	// variant may have been consulted.
	m, mb, gc := inner.snapshotCounts()
	if m != 0 || mb != 0 || gc != 0 {
		t.Errorf("hot path must make zero single-row reads, got model=%d byName=%d cred=%d", m, mb, gc)
	}
	if inner.legacyCalls() != 0 {
		t.Errorf("hot path must not touch legacy reads (got %d)", inner.legacyCalls())
	}
	inner.mu.Lock()
	cached := inner.counts.resolveCached
	inner.mu.Unlock()
	if cached != 1 {
		t.Errorf("expected exactly one ResolveInternalConfigWithAffinityCached call, got %d", cached)
	}
}

func TestCachedModelsConfig_ResolveInternalConfig_LegacyHotPathZeroDB(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	provider, apiKey, baseURL, model, ok := c.ResolveInternalConfig("m-alpha")
	if !ok || provider != "openai" || apiKey != "sk-plain-1" || model != "alpha-x" || baseURL != "https://one.example.com" {
		t.Fatalf("legacy resolver from cache: (%q,%q,%q,%q,%v)", provider, apiKey, baseURL, model, ok)
	}
	if m, mb, gc := inner.snapshotCounts(); m != 0 || mb != 0 || gc != 0 {
		t.Errorf("legacy hot path must make zero single-row reads: %d/%d/%d", m, mb, gc)
	}
	if inner.legacyCalls() != 0 {
		t.Errorf("legacy hot path must not touch inner legacy reads (got %d)", inner.legacyCalls())
	}

	// Unknown / external models keep the legacy contract.
	if _, _, _, _, ok := c.ResolveInternalConfig("never"); ok {
		t.Error("unknown model must not resolve")
	}
	if _, _, _, _, ok := c.ResolveInternalConfig("m-beta"); ok {
		t.Error("non-internal model must not resolve")
	}
}

// TestCachedModelsConfig_DetectProvider_UnderDBDown (W1): the
// decorator's cache-served legacy ResolveInternalConfig keeps
// normalizers.DetectProvider returning the real provider during a DB
// outage instead of the "external" fallback.
func TestCachedModelsConfig_DetectProvider_UnderDBDown(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	// Freeze the DB: every strict read now fails.
	inner.mu.Lock()
	inner.listErr = connRefused("connection refused")
	inner.modelErr = connRefused("connection refused")
	inner.credErr = connRefused("connection refused")
	inner.mu.Unlock()
	c.markUnhealthyForTest()

	if got := normalizers.DetectProvider(c, "m-alpha"); got != "openai" {
		t.Errorf("DetectProvider under DB-down must return cached provider, got %q", got)
	}
	if got := normalizers.DetectProvider(c, "unknown-model"); got != "external" {
		t.Errorf("unknown model stays external, got %q", got)
	}
}

// markUnhealthyForTest forces the unhealthy flag (test-only helper;
// production flips it via the reconciler).
func (c *CachedModelsConfig) markUnhealthyForTest() { c.markUnhealthy() }

// ─── dead-default WARN ───────────────────────────────────────────────────────

func TestCachedModelsConfig_Warn_DeadDefault(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(log.LstdFlags)
	}()

	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c, err := WrapModels(inner, Options{UpstreamURL: "http://localhost:4001"})
	if err != nil {
		t.Fatalf("WrapModels: %v", err)
	}
	defer c.Stop()

	out := buf.String()
	if !strings.Contains(out, "development default http://localhost:4001") {
		t.Errorf("expected dead-default WARN at boot, log: %q", out)
	}
	if !strings.Contains(out, "for model m-alpha") {
		t.Errorf("WARN must name the model, log: %q", out)
	}

	// Non-default upstream → no WARN.
	buf.Reset()
	c2, err := WrapModels(inner, Options{UpstreamURL: "https://litellm.example.com"})
	if err != nil {
		t.Fatalf("WrapModels: %v", err)
	}
	defer c2.Stop()
	if strings.Contains(buf.String(), "localhost:4001") {
		t.Errorf("no WARN expected for configured upstream, log: %q", buf.String())
	}
}

// ─── LRU (1.B.4 acceptance) ──────────────────────────────────────────────────

func TestLRU_EvictsOldestBeyondCap(t *testing.T) {
	l := newLRU(3)
	for i := 0; i < 8; i++ {
		l.Put(string(rune('a'+i)), i)
	}
	if l.Len() != 3 {
		t.Fatalf("cap 3 must hold 3, got %d", l.Len())
	}
	// Oldest three evicted; newest three present.
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		if _, ok := l.Peek(k); ok {
			t.Errorf("key %q should have been evicted", k)
		}
	}
	for _, k := range []string{"f", "g", "h"} {
		if _, ok := l.Peek(k); !ok {
			t.Errorf("key %q should be live", k)
		}
	}
	// Get refreshes recency: 'f' becomes newest, 'g' is now LRU.
	l.Get("f")
	l.Put("z", 99)
	if _, ok := l.Peek("g"); ok {
		t.Error("LRU order after Get must evict 'g' first")
	}
	if _, ok := l.Peek("f"); !ok {
		t.Error("'f' was refreshed and must survive")
	}
}

func TestLRU_DeleteRemovesMapAndList(t *testing.T) {
	l := newLRU(4)
	l.Put("k1", 1)
	l.Put("k2", 2)
	l.Delete("k1")
	if _, ok := l.Peek("k1"); ok {
		t.Error("Delete must remove the map entry")
	}
	if l.Len() != 1 {
		t.Errorf("Delete must remove the list node, len=%d", l.Len())
	}
	l.Put("k1", 10) // reinsert after delete works
	if v, ok := l.Get("k1"); !ok || v.(int) != 10 {
		t.Errorf("reinsert after delete failed: %v %v", v, ok)
	}
}
