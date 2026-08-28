package modelscache_test

// proxy_integration_test.go — db-cache-layer 1D end-to-end: the REAL
// CachedModelsConfig (over a real SQLite-backed ModelsManager) wired
// into the REAL proxy handler. Pre-warm the cache, cut the DB, drive
// HandleChatCompletions through httptest, assert a 503
// config_store_unavailable (never a silent external passthrough).
//
// External test package (modelscache_test): it imports pkg/proxy,
// which imports pkg/modelscache — legal only from outside the package,
// and it keeps the modelscache source edge one-way.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// downModelsSource is a minimal outage wrapper over the real manager:
// while down, every strict read answers connection-refused (infra
// class) — exactly what the cache sees during a DB outage. Mutator
// calls still reach the physical DB (as out-of-band/direct SQL would).
type downModelsSource struct {
	*database.ModelsManager
	down bool
}

type connRefusedError struct{}

func (connRefusedError) Error() string { return "dial tcp 127.0.0.1:5432: connect: connection refused" }

func (d *downModelsSource) failIfDown() error {
	if d.down {
		return connRefusedError{}
	}
	return nil
}

func (d *downModelsSource) GetModelsStrict(ctx context.Context) ([]models.ModelConfig, error) {
	if err := d.failIfDown(); err != nil {
		return nil, err
	}
	return d.ModelsManager.GetModelsStrict(ctx)
}

func (d *downModelsSource) GetEnabledModelsStrict(ctx context.Context) ([]models.ModelConfig, error) {
	if err := d.failIfDown(); err != nil {
		return nil, err
	}
	return d.ModelsManager.GetEnabledModelsStrict(ctx)
}

func (d *downModelsSource) GetCredentialsStrict(ctx context.Context) ([]models.CredentialConfig, error) {
	if err := d.failIfDown(); err != nil {
		return nil, err
	}
	return d.ModelsManager.GetCredentialsStrict(ctx)
}

func (d *downModelsSource) GetModelStrict(ctx context.Context, modelID string) (*models.ModelConfig, error) {
	if err := d.failIfDown(); err != nil {
		return nil, err
	}
	return d.ModelsManager.GetModelStrict(ctx, modelID)
}

func (d *downModelsSource) GetModelByNameStrict(ctx context.Context, modelName string) (*models.ModelConfig, error) {
	if err := d.failIfDown(); err != nil {
		return nil, err
	}
	return d.ModelsManager.GetModelByNameStrict(ctx, modelName)
}

func (d *downModelsSource) GetCredentialStrict(ctx context.Context, id string) (*models.CredentialConfig, error) {
	if err := d.failIfDown(); err != nil {
		return nil, err
	}
	return d.ModelsManager.GetCredentialStrict(ctx, id)
}

func TestProxyIntegration_UnhealthyDecoratorReturns503EndToEnd(t *testing.T) {
	// Real SQLite store + real manager.
	dbPath := filepath.Join(t.TempDir(), "integration.db")
	t.Setenv("DATABASE_URL", "sqlite:"+dbPath)
	dbStore, err := database.NewConnection(context.Background())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer dbStore.Close()
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	mgr, err := database.NewModelsManager(dbStore, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	defer mgr.Close()

	// The external upstream records hits — a hit during the outage
	// scenario for an UNKNOWN model would be the misroute class.
	var upstreamHits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("UPSTREAM_URL", upstream.URL)
	t.Setenv("MAX_GENERATION_TIME", "10s")
	configMgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}

	// Warm-cache boot (DB up), then cut it.
	src := &downModelsSource{ModelsManager: mgr}
	cached, err := modelscache.WrapModels(src, modelscache.Options{})
	if err != nil {
		t.Fatalf("WrapModels: %v", err)
	}
	defer cached.Stop()
	src.down = true

	// Flip the decorator unhealthy through a real failed read (the
	// same path the boundary itself takes), then assert the signal.
	if m := cached.GetModel("never-seen-model"); m != nil {
		t.Fatalf("never-seen model must not resolve with the DB down")
	}
	if cached.Healthy() {
		t.Fatal("decorator must be unhealthy after a failed read during the outage")
	}

	// Wire the DECORATOR (not the raw manager) into the proxy.
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)
	h := proxy.NewHandler(&proxy.Config{
		ConfigMgr:    configMgr,
		ModelsConfig: cached,
		EventBus:     bus,
	}, bus, reqStore, nil, nil, nil, nil)

	// Unknown model + DB down → 503, never an upstream hit.
	postJSON := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.HandleChatCompletions(w, req)
		return w
	}

	w := postJSON(`{"model":"never-seen-model","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 config_store_unavailable, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "config_store_unavailable") {
		t.Errorf("body must carry config_store_unavailable: %s", w.Body.String())
	}
	if upstreamHits.Load() != 0 {
		t.Fatalf("MISROUTE: unknown-model request reached the external upstream during the outage")
	}

	// A KNOWN (cached) configured-external model still serves
	// end-to-end while the DB is down — zero failures for known
	// entities (criterion A). Seed it directly on the manager
	// (out-of-band write), let the cache learn it while "up", then
	// re-enter the outage.
	if err := mgr.AddModel(models.ModelConfig{ID: "ext-configured", Name: "ext-configured", Enabled: true}); err != nil {
		t.Fatalf("seed external model: %v", err)
	}
	src.down = false
	if m := cached.GetModel("ext-configured"); m == nil {
		t.Fatalf("cache must learn the configured external model while up")
	}
	src.down = true

	w2 := postJSON(`{"model":"ext-configured","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if w2.Code != http.StatusOK {
		t.Fatalf("known configured model must serve during the outage, got %d: %s", w2.Code, w2.Body.String())
	}
	if upstreamHits.Load() != 1 {
		t.Errorf("known external model should hit the upstream exactly once, got %d", upstreamHits.Load())
	}
}
