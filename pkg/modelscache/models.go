package modelscache

import (
	"context"
	"errors"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// Defaults (hardcoded first — env knobs are Phase 2 per leader
// decision 2 / open question Q2).
const (
	defaultPositiveTTL       = 60 * time.Second // tokens: positive verdict TTL
	defaultNegativeTTL       = 60 * time.Second // models negative + tokens negative TTL
	defaultStaleCap          = 24 * time.Hour   // both decorators: stale/last-known-good hard cap
	defaultStrictFillTimeout = 5 * time.Second  // planner ruling K / W9
	defaultReconcileInterval = 60 * time.Second
	defaultTokenCacheCap     = 10000 // planner ruling B
	defaultIDIndexCap        = 50000 // W4
)

// stopWaitSlack is the slack added to StrictFillTimeout for the
// bounded reconciler-wait in Stop: the scan budget itself plus one
// second, so a scan started the instant before Stop still fits
// inside the wait (6s at the default 5s fill timeout).
const stopWaitSlack = time.Second

// Options configures both decorators. Zero-value fields fall back to
// the hardcoded defaults above. Clock is the injection point for the
// outage-simulation tests (correction N6); nil means time.Now.
//
// Field names are decorator-neutral (tidy finding 15): each TTL names
// the tier it drives, and a single StaleCap serves both decorators.
type Options struct {
	ModelsNegTTL      time.Duration // models: not-found negative-cache TTL
	TokensPositiveTTL time.Duration // tokens: positive verdict TTL
	NegativeTTL       time.Duration // tokens: negative verdict TTL
	StaleCap          time.Duration // models + tokens: stale/last-known-good hard cap
	StrictFillTimeout time.Duration
	LRUCap            int
	ReconcileInterval time.Duration
	// UpstreamURL is the global proxy upstream used ONLY by the
	// boot-time dead-default WARN tripwire (warn.go). Optional.
	UpstreamURL string
	// Clock overrides time.Now for tests. Optional.
	Clock func() time.Time
}

func (o Options) withDefaults() Options {
	if o.ModelsNegTTL <= 0 {
		o.ModelsNegTTL = defaultNegativeTTL
	}
	if o.TokensPositiveTTL <= 0 {
		o.TokensPositiveTTL = defaultPositiveTTL
	}
	if o.NegativeTTL <= 0 {
		o.NegativeTTL = defaultNegativeTTL
	}
	if o.StaleCap <= 0 {
		o.StaleCap = defaultStaleCap
	}
	if o.StrictFillTimeout <= 0 {
		o.StrictFillTimeout = defaultStrictFillTimeout
	}
	if o.LRUCap <= 0 {
		o.LRUCap = defaultTokenCacheCap
	}
	if o.ReconcileInterval <= 0 {
		o.ReconcileInterval = defaultReconcileInterval
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	return o
}

// credEntry is a cached credential. decryptOK=false negative-caches a
// decrypt failure (arch §5: never serve ciphertext) — the entry holds
// no key material in that case.
type credEntry struct {
	cred        *models.CredentialConfig // decrypted plaintext; nil when !decryptOK
	decryptOK   bool
	refreshedAt time.Time
}

// negEntry is a negative-cache record for a not-found model ID/name.
type negEntry struct {
	checkedAt time.Time
}

// CachedModelsConfig decorates models.ModelsConfigInterface with an
// in-process cache of models + decrypted credentials. Implements the
// full 18-method interface plus ConfigStoreHealth (boundary fail-fast
// signal) and Stop (deferred teardown).
//
// Failure semantics (architecture §2 matrix):
//   - HIT            → serve cached copy, zero DB (row 1)
//   - miss + DB up   → strict-fill, serve; not-found → 60s negative (row 2/3)
//   - miss + DB down → nil + Healthy()==false → boundary 503 (row 4)
//   - credential missing/undecryptable → never ciphertext (row 5)
//   - stale + DB down → serve last-known-good, Healthy()==false (row 6)
type CachedModelsConfig struct {
	inner strictSource
	opts  Options

	mu            sync.RWMutex
	modelsByID    map[string]*models.ModelConfig
	modelsByName  map[string]string // Name → ID
	credsByID     map[string]credEntry
	negByID       map[string]negEntry
	negByName     map[string]negEntry
	modelsSnap    []models.ModelConfig
	enabledSnap   []models.ModelConfig
	lastRefresh   time.Time
	healthy       bool
	staleWarnOnce bool

	stopCh   chan struct{}
	stopOnce sync.Once
	// scanCancel cancels the in-flight reconciler scan so Stop() can
	// abort outstanding work (planner ruling Stop-cancel / W3).
	scanMu      sync.Mutex
	scanCancel  context.CancelFunc
	reconcileWG sync.WaitGroup
}

// NewCachedModelsConfig builds the decorator and synchronously primes
// it via the strict list reads. Boot with the DB down returns an
// error — the caller (cmd/main.go) keeps today's log.Fatalf fail-fast
// posture (leader decision 4). No background goroutine is started on
// failure.
func NewCachedModelsConfig(inner strictSource, opts Options) (*CachedModelsConfig, error) {
	if inner == nil {
		return nil, errors.New("modelscache: NewCachedModelsConfig requires a non-nil inner source")
	}
	o := opts.withDefaults()
	c := &CachedModelsConfig{
		inner:  inner,
		opts:   o,
		stopCh: make(chan struct{}),
	}

	// Boot priming (task 1.B.6): non-cancellable parent, 5s budget —
	// boot should fail fast rather than hang. Any error aborts priming
	// entirely (no partial state; decrypt-failure-in-scan included per
	// planner ruling J).
	ctx, cancel := context.WithTimeout(context.Background(), o.StrictFillTimeout)
	modelsList, enabledList, creds, err := c.strictSnapshot(ctx)
	cancel()
	if err != nil {
		return nil, err
	}

	c.applySnapshot(modelsList, enabledList, creds)

	// Boot-only observability: dead-default tripwire (1.B.10 / K) and
	// the one-time decorator-meta line (1.E.2).
	warnDeadDefaultUpstream(o.UpstreamURL, c.enabledSnap)
	log.Printf("[cache] models decorator enabled (modelsNeg=%s staleCap=%s, %d models, %d credentials)",
		o.ModelsNegTTL, o.StaleCap, len(c.modelsSnap), len(c.credsByID))

	c.reconcileWG.Add(1)
	go c.reconcileLoop()
	return c, nil
}

// strictSnapshot runs the three strict list reads under one context.
// The caller owns the context (boot: 5s timeout; reconciler:
// cancellable).
func (c *CachedModelsConfig) strictSnapshot(ctx context.Context) ([]models.ModelConfig, []models.ModelConfig, []models.CredentialConfig, error) {
	modelsList, err := c.inner.GetModelsStrict(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	enabledList, err := c.inner.GetEnabledModelsStrict(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	creds, err := c.inner.GetCredentialsStrict(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	return modelsList, enabledList, creds, nil
}

// applySnapshot installs a fully-successful snapshot under the write
// lock: rebuilds every map, clears negatives (the full scan just
// re-answered every not-found definitively), swaps the ordered
// snapshots, marks healthy.
func (c *CachedModelsConfig) applySnapshot(modelsList, enabledList []models.ModelConfig, creds []models.CredentialConfig) {
	byID := make(map[string]*models.ModelConfig, len(modelsList))
	byName := make(map[string]string, len(modelsList))
	for i := range modelsList {
		m := deepCopyModelConfig(&modelsList[i])
		byID[m.ID] = m
		byName[m.Name] = m.ID
	}
	credMap := make(map[string]credEntry, len(creds))
	for i := range creds {
		credMap[creds[i].ID] = credEntry{
			cred:        deepCopyCredentialConfig(&creds[i]),
			decryptOK:   true,
			refreshedAt: c.now(),
		}
	}

	c.mu.Lock()
	c.modelsByID = byID
	c.modelsByName = byName
	c.credsByID = credMap
	c.negByID = map[string]negEntry{}
	c.negByName = map[string]negEntry{}
	c.modelsSnap = deepCopyModelConfigs(modelsList)
	c.enabledSnap = deepCopyModelConfigs(enabledList)
	c.lastRefresh = c.now()
	c.healthy = true
	c.staleWarnOnce = false
	c.mu.Unlock()
}

// now is the single clock read point (correction N6).
func (c *CachedModelsConfig) now() time.Time {
	return c.opts.Clock()
}

// Healthy implements ConfigStoreHealth. Safe under concurrent
// reconciler writes (RLock-protected snapshot read).
func (c *CachedModelsConfig) Healthy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.healthy
}

// Stop terminates the reconciler goroutine. Per the Stop-cancel
// contract (planner ruling, W3): signal stopCh AND cancel the
// in-flight scan context first; an un-cancellable scan may run to
// completion but its result is discarded (the swap re-checks stopCh).
// Idempotent.
//
// The reconciler-wait is bounded by StrictFillTimeout + stopWaitSlack
// (6s at the defaults; review remediation 2026-08-28): a stuck driver
// must not hang teardown. On timeout a WARN is logged and Stop
// returns — the reconciler goroutine is abandoned mid-scan;
// sync.Once keeps idempotent Stop safe across multiple callers.
func (c *CachedModelsConfig) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
		c.scanMu.Lock()
		if c.scanCancel != nil {
			c.scanCancel()
		}
		c.scanMu.Unlock()
	})
	done := make(chan struct{})
	go func() {
		c.reconcileWG.Wait()
		close(done)
	}()
	wait := c.opts.StrictFillTimeout + stopWaitSlack
	select {
	case <-done:
	case <-time.After(wait):
		log.Printf("[WARN] [cache] reconciler did not stop within %s — abandoned mid-scan; teardown proceeding", wait)
	}
}

// reconcileLoop drives the 60s reconciler (task 1.B.7).
func (c *CachedModelsConfig) reconcileLoop() {
	defer c.reconcileWG.Done()
	ticker := time.NewTicker(c.opts.ReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.reconcileOnce()
		}
	}
}

// reconcileOnce performs one reconciler tick: run the three strict
// reads OUTSIDE the decorator lock (a 5s-budgeted DB call must never
// block readers), then swap only on full success. Abort rules
// (planner rulings J + C3 / risk 8a):
//   - ANY strict read error            → no swap, healthy=false, WARN
//   - successful empty scan while the
//     cached snapshot is non-empty     → no swap, healthy=false, WARN
//   - otherwise                        → atomic swap, healthy=true
//
// Negative entries do not survive a successful swap: the full scan
// has just re-answered every not-found question definitively (the
// 60s TTL bounds the in-between window).
//
// The whole body is wrapped in defer/recover + WARN (mirroring
// pkg/credentiallb sweepOnce): a panic no longer silently stops the
// reconciler — the next tick runs another sweep.
func (c *CachedModelsConfig) reconcileOnce() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[WARN] [cache] reconciler sweep recovered from panic: %v", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), c.opts.StrictFillTimeout)
	c.scanMu.Lock()
	c.scanCancel = cancel
	c.scanMu.Unlock()
	defer func() {
		cancel()
		c.scanMu.Lock()
		c.scanCancel = nil
		c.scanMu.Unlock()
	}()

	modelsList, enabledList, creds, err := c.strictSnapshot(ctx)
	failed := err != nil
	if !failed {
		// Suspicious-empty guard (C3): a successful-but-empty list
		// while the cache holds a non-empty snapshot aborts the swap —
		// it would destroy last-known-good config and re-arm the very
		// bug class this layer exists to fix. The detailed WARN below
		// is the ONE log for this condition (tidy finding 5): the
		// shared no-swap posture follows without a second
		// "strict read failed" WARN.
		c.mu.RLock()
		prevModels, prevCreds := len(c.modelsSnap), len(c.credsByID)
		c.mu.RUnlock()
		if (len(modelsList) == 0 && prevModels > 0) || (len(creds) == 0 && prevCreds > 0) {
			log.Printf("[WARN] [cache] reconciler: suspicious empty scan (models %d→%d, creds %d→%d) — aborting swap, preserving last-known-good",
				prevModels, len(modelsList), prevCreds, len(creds))
			failed = true
		}
	}
	if failed {
		if err != nil {
			// Credential IDs are deliberately omitted from the WARN
			// (1.B.6); the error text itself carries no key material.
			log.Printf("[WARN] [cache] reconciler: strict read failed (%v) — no swap, serving last-known-good", err)
		}
		c.mu.Lock()
		c.healthy = false
		if !c.staleWarnOnce && c.now().Sub(c.lastRefresh) > c.opts.StaleCap {
			log.Printf("[WARN] [cache] models snapshot older than staleness cap %s — continuing to serve last-known-good", c.opts.StaleCap)
			c.staleWarnOnce = true
		}
		c.mu.Unlock()
		return
	}

	// Swap only if not stopped meanwhile; discard the scan result
	// after Stop (ruling Stop-cancel #3).
	select {
	case <-c.stopCh:
		return
	default:
	}
	c.applySnapshot(modelsList, enabledList, creds)
}

// ─── Read paths ──────────────────────────────────────────────────────────────

// lookupCachedModel is the shared read prologue of the ID-keyed read
// paths: it returns the cached entry for modelID (nil on miss;
// entries are immutable once installed — swaps replace map entries,
// never mutate them) and whether a fresh negative-cache verdict
// exists. Callers must not hold c.mu.
func (c *CachedModelsConfig) lookupCachedModel(modelID string) (m *models.ModelConfig, negFresh bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if n, ok := c.negByID[modelID]; ok && c.now().Sub(n.checkedAt) <= c.opts.ModelsNegTTL {
		return nil, true
	}
	return c.modelsByID[modelID], false
}

// lookupCachedModelByName is the name-keyed twin used by
// GetModelByName: fresh negByName verdict → (nil, true); otherwise
// the name→ID→model indirection.
func (c *CachedModelsConfig) lookupCachedModelByName(modelName string) (m *models.ModelConfig, negFresh bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if n, ok := c.negByName[modelName]; ok && c.now().Sub(n.checkedAt) <= c.opts.ModelsNegTTL {
		return nil, true
	}
	if id, ok := c.modelsByName[modelName]; ok {
		return c.modelsByID[id], false
	}
	return nil, false
}

// GetModel serves the cached model; on miss it strict-fills. nil is
// returned for a definitive not-found (negative-cached 60s) AND for
// an infra failure (Healthy()==false distinguishes the two — the
// boundary sites consume exactly that signal).
func (c *CachedModelsConfig) GetModel(modelID string) *models.ModelConfig {
	// Fast path: hit under RLock, deep-copy out.
	m, negFresh := c.lookupCachedModel(modelID)
	if negFresh {
		return nil
	}
	if m != nil {
		return deepCopyModelConfig(m)
	}

	// Miss: strict-fill under a 5s budget (planner ruling K).
	ctx, cancel := context.WithTimeout(context.Background(), c.opts.StrictFillTimeout)
	m, err := c.inner.GetModelStrict(ctx, modelID)
	cancel()
	if err != nil {
		if errors.Is(err, database.ErrModelNotFound) {
			c.storeNegativeByID(modelID)
			return nil
		}
		c.markUnhealthy()
		return nil
	}
	c.cacheModel(m)
	return deepCopyModelConfig(m)
}

// GetModelByName is the name-keyed twin of GetModel.
func (c *CachedModelsConfig) GetModelByName(modelName string) *models.ModelConfig {
	m, negFresh := c.lookupCachedModelByName(modelName)
	if negFresh {
		return nil
	}
	if m != nil {
		return deepCopyModelConfig(m)
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.opts.StrictFillTimeout)
	m, err := c.inner.GetModelByNameStrict(ctx, modelName)
	cancel()
	if err != nil {
		if errors.Is(err, database.ErrModelNotFound) {
			c.storeNegativeByName(modelName)
			return nil
		}
		c.markUnhealthy()
		return nil
	}
	c.cacheModel(m)
	return deepCopyModelConfig(m)
}

// GetModels returns the cached full snapshot unconditionally (the
// legacy silent-[] path is unreachable through the decorator — plan
// overview C1 note). Deep copy-on-read.
func (c *CachedModelsConfig) GetModels() []models.ModelConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return deepCopyModelConfigs(c.modelsSnap)
}

// GetEnabledModels returns the cached enabled snapshot
// unconditionally. During a DB outage this is the last-known-good
// list (failure-mode row 6) — never a silent empty [].
func (c *CachedModelsConfig) GetEnabledModels() []models.ModelConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return deepCopyModelConfigs(c.enabledSnap)
}

// GetTruncateParams serves from the cached model map.
func (c *CachedModelsConfig) GetTruncateParams(modelID string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.modelsByID[modelID]
	if !ok {
		return nil
	}
	if len(m.TruncateParams) == 0 {
		return nil
	}
	return copyStrings(m.TruncateParams)
}

// GetFallbackChain serves from the cached model map ([ID] + chain).
func (c *CachedModelsConfig) GetFallbackChain(modelID string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m, ok := c.modelsByID[modelID]
	if !ok {
		return nil
	}
	result := make([]string, 0, len(m.FallbackChain)+1)
	result = append(result, m.ID)
	result = append(result, m.FallbackChain...)
	return result
}

// GetCredential serves the cached decrypted credential. Decrypt
// failures are negative-cached (entry with decryptOK=false → nil
// forever until the reconciler heals) — ciphertext is NEVER served
// (arch §5 / matrix row 5).
func (c *CachedModelsConfig) GetCredential(id string) *models.CredentialConfig {
	c.mu.RLock()
	if e, ok := c.credsByID[id]; ok {
		if !e.decryptOK || e.cred == nil {
			c.mu.RUnlock()
			return nil
		}
		cp := deepCopyCredentialConfig(e.cred)
		c.mu.RUnlock()
		return cp
	}
	c.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), c.opts.StrictFillTimeout)
	cred, err := c.inner.GetCredentialStrict(ctx, id)
	cancel()
	if err != nil {
		if errors.Is(err, database.ErrDecryptionFailed) {
			// Negative-cache the decrypt failure: no key material is
			// stored for the entry.
			c.mu.Lock()
			c.credsByID[id] = credEntry{decryptOK: false, refreshedAt: c.now()}
			c.mu.Unlock()
			return nil
		}
		if errors.Is(err, database.ErrCredentialNotFound) {
			return nil
		}
		c.markUnhealthy()
		return nil
	}
	c.mu.Lock()
	c.credsByID[id] = credEntry{cred: deepCopyCredentialConfig(cred), decryptOK: true, refreshedAt: c.now()}
	c.mu.Unlock()
	return deepCopyCredentialConfig(cred)
}

// GetCredentials returns the cached decrypted credentials (skipping
// decrypt-failed entries). Unconditional cached data — never a silent
// empty [] on DB error.
func (c *CachedModelsConfig) GetCredentials() []models.CredentialConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]models.CredentialConfig, 0, len(c.credsByID))
	for _, e := range c.credsByID {
		if !e.decryptOK || e.cred == nil {
			continue
		}
		result = append(result, *deepCopyCredentialConfig(e.cred))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// ─── Resolution paths (the hot seam) ─────────────────────────────────────────

// ResolveInternalConfigWithAffinity is the ZERO-DB hot path: read the
// cached model, back a credential-lookup closure with the cached
// credential map, and delegate to the strict resolver variant
// (task 1.A.4). The 2+-credential engine path (GetOrSelect, affinity,
// ExcludeAndReselect heal) is preserved inside the variant.
func (c *CachedModelsConfig) ResolveInternalConfigWithAffinity(modelID, conversationKey string) (models.ResolvedCredential, bool) {
	// Fetch the cached model pointer via the shared prologue (entries
	// are immutable once installed — swaps replace map entries, never
	// mutate them), releasing the lock BEFORE calling into the
	// resolver so the closure can take its own RLocks (RLock recursion
	// under a queued writer would deadlock).
	cached, negFresh := c.lookupCachedModel(modelID)
	if negFresh || cached == nil {
		// Unknown model: strict-fill once so a post-boot DB add becomes
		// visible; if the DB is down this degrades to ok=false (the
		// caller-side failure path), never a misroute.
		c.GetModel(modelID)
		c.mu.RLock()
		cached = c.modelsByID[modelID]
		c.mu.RUnlock()
		if cached == nil {
			return models.ResolvedCredential{}, false
		}
	}

	credLookup := func(credentialID string) (*models.CredentialConfig, bool) {
		c.mu.RLock()
		defer c.mu.RUnlock()
		e, ok := c.credsByID[credentialID]
		if !ok || !e.decryptOK || e.cred == nil {
			return nil, false
		}
		// Immutable-once-installed entries: hand the pointer out for
		// read-only resolution; public getters deep-copy.
		return e.cred, true
	}
	return c.inner.ResolveInternalConfigWithAffinityCached(cached, conversationKey, credLookup)
}

// ResolveInternalConfig is the LEGACY 5-tuple resolver served from the
// cache (task 1.B.5, mandatory override (b) / W1): without it,
// normalizers.DetectProvider (called on every request at
// race_executor.go:393/:559) would hit the DB-bound legacy path and
// return "external" during an outage for known internal models —
// partially re-arming the misroute bug class.
//
// Semantics mirror the store's single-credential fall-through:
// PrimaryCredentialID → cached credential → (provider, key, baseURL,
// peak-hour-resolved model).
func (c *CachedModelsConfig) ResolveInternalConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool) {
	m, neg := c.lookupCachedModel(modelID)
	if neg || m == nil {
		return "", "", "", "", false
	}
	if !m.Internal {
		return "", "", "", "", false
	}
	primaryID := m.PrimaryCredentialID()
	if primaryID == "" {
		return "", "", "", "", false
	}
	c.mu.RLock()
	e, found := c.credsByID[primaryID]
	c.mu.RUnlock()
	if !found || !e.decryptOK || e.cred == nil {
		return "", "", "", "", false
	}

	provider = e.cred.Provider
	baseURL = m.InternalBaseURL
	if baseURL == "" {
		baseURL = e.cred.BaseURL
	}
	actualModel := m.InternalModel
	if peakModel := m.ResolvePeakHourModel(c.now()); peakModel != "" {
		log.Printf("[PEAK-HOUR] peak hour active for model %s: using %s instead of %s", m.ID, peakModel, m.InternalModel)
		actualModel = peakModel
	}
	return provider, e.cred.APIKey, baseURL, actualModel, true
}

// ─── Write-through mutators (synchronous, arch ruling 4) ─────────────────────

// AddModel delegates to the inner store, then writes the
// authoritative payload through and clears the negative entries for
// the model's ID and name.
func (c *CachedModelsConfig) AddModel(model models.ModelConfig) error {
	if err := c.inner.AddModel(model); err != nil {
		return err
	}
	cp := deepCopyModelConfig(&model)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modelsByID[cp.ID] = cp
	c.modelsByName[cp.Name] = cp.ID
	delete(c.negByID, cp.ID)
	delete(c.negByName, cp.Name)
	c.modelsSnap, c.enabledSnap = rebuildSnapshotsLocked(c.modelsByID)
	c.healthy = true
	return nil
}

// UpdateModel delegates to the inner store, then replaces the cached
// entry (handling renames by rebuilding the name index) and clears the
// negative entries for the model's (possibly new) ID and name —
// parity with AddModel; without this, a model renamed onto a name
// that was negative-cached in the prior TTL window would shadow the
// live entry in GetModelByName.
func (c *CachedModelsConfig) UpdateModel(modelID string, model models.ModelConfig) error {
	if err := c.inner.UpdateModel(modelID, model); err != nil {
		return err
	}
	cp := deepCopyModelConfig(&model)
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.modelsByID[modelID]; ok && old.Name != cp.Name {
		delete(c.modelsByName, old.Name)
	}
	delete(c.modelsByID, modelID)
	c.modelsByID[cp.ID] = cp
	c.modelsByName[cp.Name] = cp.ID
	delete(c.negByID, cp.ID)
	delete(c.negByName, cp.Name)
	c.modelsSnap, c.enabledSnap = rebuildSnapshotsLocked(c.modelsByID)
	c.healthy = true
	return nil
}

// RemoveModel delegates to the inner store, then evicts the model
// from both the ID and the name maps.
func (c *CachedModelsConfig) RemoveModel(modelID string) error {
	if err := c.inner.RemoveModel(modelID); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.modelsByID[modelID]; ok {
		delete(c.modelsByName, old.Name)
		delete(c.negByName, old.Name)
	}
	delete(c.modelsByID, modelID)
	delete(c.negByID, modelID)
	c.modelsSnap, c.enabledSnap = rebuildSnapshotsLocked(c.modelsByID)
	return nil
}

// AddCredential delegates to the inner store and invalidates only
// (lazy refill on next read) — arch §3: avoids empty-key /
// keep-existing-payload ambiguity in update requests.
func (c *CachedModelsConfig) AddCredential(cred models.CredentialConfig) error {
	if err := c.inner.AddCredential(cred); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.credsByID, cred.ID)
	c.mu.Unlock()
	return nil
}

// UpdateCredential delegates to the inner store and invalidates only.
func (c *CachedModelsConfig) UpdateCredential(id string, cred models.CredentialConfig) error {
	if err := c.inner.UpdateCredential(id, cred); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.credsByID, id)
	c.mu.Unlock()
	return nil
}

// RemoveCredential delegates to the inner store and drops the cached
// credential by ID.
func (c *CachedModelsConfig) RemoveCredential(id string) error {
	if err := c.inner.RemoveCredential(id); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.credsByID, id)
	c.mu.Unlock()
	return nil
}

// Save delegates to the inner store (no-op for the DB backend).
func (c *CachedModelsConfig) Save() error { return c.inner.Save() }

// Validate delegates to the inner store (validates against DB truth).
func (c *CachedModelsConfig) Validate() error { return c.inner.Validate() }

// ─── Internal helpers ────────────────────────────────────────────────────────

func (c *CachedModelsConfig) storeNegativeByID(id string) {
	c.mu.Lock()
	c.negByID[id] = negEntry{checkedAt: c.now()}
	c.mu.Unlock()
}

func (c *CachedModelsConfig) storeNegativeByName(name string) {
	c.mu.Lock()
	c.negByName[name] = negEntry{checkedAt: c.now()}
	c.mu.Unlock()
}

func (c *CachedModelsConfig) markUnhealthy() {
	c.mu.Lock()
	c.healthy = false
	c.mu.Unlock()
}

// cacheModel installs a strict-filled model into the maps (copy under
// lock) and clears any negative entry for it.
func (c *CachedModelsConfig) cacheModel(m *models.ModelConfig) {
	if m == nil {
		return
	}
	cp := deepCopyModelConfig(m)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modelsByID[cp.ID] = cp
	c.modelsByName[cp.Name] = cp.ID
	delete(c.negByID, cp.ID)
	delete(c.negByName, cp.Name)
	c.modelsSnap, c.enabledSnap = rebuildSnapshotsLocked(c.modelsByID)
}
