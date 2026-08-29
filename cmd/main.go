package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/mcp"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/middleware/gzipmw"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/ui"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"
)

// Version is set at build time via -ldflags
var Version = "dev"

func main() {
	ctx := context.Background()

	// Initialize Shared Components
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100) // Keep last 100 requests

	var configMgr config.ManagerInterface
	// Phase 3 — main.go now binds modelsMgr at the concrete
	// *database.ModelsManager type so credLB := modelsMgr.Engine() can
	// run (Dispatcher Ruling #2: ModelsManager owns the engine). The
	// proxyConfig still consumes the interface (proxy.Config
	// ModelsConfig field is unchanged).
	var modelsMgr *database.ModelsManager
	var modelsConfig models.ModelsConfigInterface
	var dbStore *database.Store

	// Always use database storage
	// - If DATABASE_URL is set with postgres://, uses PostgreSQL
	// - Otherwise uses SQLite at ~/.config/llm-supervisor-proxy/config.db
	var err error
	dbStore, configMgr, modelsMgr, err = database.InitializeAll(ctx, bus)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	// Phase 3 / Round 3g — graceful shutdown teardown ordering:
	//   srv.Shutdown (drains HTTP)
	//   → credLB.Stop (engine janitor stop)
	//   → modelsMgr.Close (event-bus unsubscribe + per-instance cleanup)
	//   → dbStore.Close (closes DB)
	// Implementation: register defers in this order so LIFO fires the
	// reverse. Existing `defer dbStore.Close()` at the original site stays;
	// we ADD modelsMgr.Close() RIGHT BELOW it (so it runs before dbStore.Close
	// at LIFO) and credLB.Stop() RIGHT BELOW that.
	defer dbStore.Close()
	if modelsMgr != nil {
		defer modelsMgr.Close()
	}

	// Phase 3 / Task 10 + 12 — obtain the engine (Dispatcher Ruling #2
	// — ModelsManager owns the engine; main.go only OBTAINS it). The
	// local variable name `credLB` per the plan cross-reference.
	//
	// Phase 3 / Task 11 — EventCredentialsChanged subscription: the
	// plan's original text placed the bus subscription here, but the
	// Phase-2 ModelsManager ALREADY owns it (store.go wires a bus
	// subscription that forwards EventCredentialsChanged →
	// Engine.OnModelChanged with E-2 filter-survivors semantics, and
	// ModelsManager.Close() unsubscribes it). A second subscription in
	// main.go would double-deliver every event — the single
	// ModelsManager loop IS the production-path handler the task's
	// acceptance describes. main.go deliberately does NOT subscribe.
	credLB := modelsMgr.Engine()

	// Phase 3 — the interface-typed modelsConfig used by proxyConfig +
	// uiServer points at the same concrete *ModelsManager.
	//
	// db-cache-layer 1C — CACHE_LAYER_ENABLED env flag (leader
	// decision 5: default ON; explicit "false" disables — the instant
	// rollback lever, no rebuild needed). With the cache enabled the
	// decorator wraps the manager at this seam: boot priming is
	// synchronous (DB-down at boot keeps today's log.Fatalf fail-fast
	// posture, leader decision 4); a 60s reconciler keeps the cache
	// fresh; the boundary sites 503 config_store_unavailable when the
	// store is unhealthy (1D).
	cacheLayerEnabled := os.Getenv("CACHE_LAYER_ENABLED") != "false"
	log.Printf("[cache] CACHE_LAYER_ENABLED=%v", cacheLayerEnabled)

	var wrappedModels *modelscache.CachedModelsConfig
	var wrappedTokens *modelscache.CachedTokenStore

	if cacheLayerEnabled {
		// Options: only genuinely runtime-specific values — every
		// default (TTLs, caps, fill timeout) is owned by
		// Options.withDefaults (tidy finding 7), never duplicated at
		// the call site. Options.UpstreamURL feeds only the boot-time
		// dead-default (localhost:4001) WARN tripwire; read the config
		// snapshot locally (cfg is materialized further below).
		wrappedModels, err = modelscache.NewCachedModelsConfig(modelsMgr, modelscache.Options{
			UpstreamURL: configMgr.Get().UpstreamURL,
		})
		if err != nil {
			log.Fatalf("Failed to prime models cache: %v", err)
		}
		modelsConfig = wrappedModels
	} else {
		modelsConfig = modelsMgr
	}

	// Phase 3 / Round 3g — engine stop is the LAST defer registered
	// before srv.Shutdown, so LIFO fires it first after srv.Shutdown
	// returns (and the srv.Shutdown itself happens implicitly when
	// the signal handler triggers — see below).
	if credLB != nil {
		defer credLB.Stop()
	}

	// Initialize encryption (required for internal upstream API key storage)
	if err := crypto.InitEncryption(); err != nil {
		log.Fatalf("Encryption initialization failed: %v", err)
	}
	log.Printf("Encryption initialized")

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL != "" {
		log.Printf("Using PostgreSQL database (DATABASE_URL is set)")
	} else {
		log.Printf("Using SQLite database for local development")
	}

	cfg := configMgr.Get()

	// Initialize Buffer Store for persisting stream error buffers
	bufferStorageDir := cfg.BufferStorageDir
	if bufferStorageDir == "" {
		// Use default data directory
		userConfigDir, err := os.UserConfigDir()
		if err != nil {
			log.Fatalf("Failed to get user config directory: %v", err)
		}
		bufferStorageDir = filepath.Join(userConfigDir, "llm-supervisor-proxy", "buffers")
	}
	bufferStore, err := bufferstore.New(bufferStorageDir, int64(cfg.BufferMaxStorageMB)*1024*1024)
	if err != nil {
		log.Fatalf("Failed to initialize buffer store: %v", err)
	}
	log.Printf("Buffer storage initialized at: %s (max %d MB)", bufferStorageDir, cfg.BufferMaxStorageMB)

	// Initialize Proxy Config (interface consumers see modelsConfig).
	proxyConfig := &proxy.Config{
		ConfigMgr:    configMgr,
		ModelsConfig: modelsConfig,
		EventBus:     bus,
	}

	// Initialize Token Store
	// db-cache-layer 1C — the same CACHE_LAYER_ENABLED seam wraps the
	// token store: three-tier cache (positive 60s / stale-positive
	// served only on infra-class errors / negative 60s, LRU 10k) so
	// known tokens stay authorized through a DB outage (criterion C).
	// The same tokenStore variable flows to ui.NewServer, proxy.
	// NewHandler, and mcp.NewServer unchanged.
	var tokenStore auth.TokenStoreInterface
	if cacheLayerEnabled {
		// Bare Options: withDefaults owns every default (tidy finding
		// 7) — no duplicated literal TTLs/caps at the call site.
		wrappedTokens = modelscache.NewCachedTokenStore(auth.NewTokenStore(dbStore.DB, dbStore.Dialect), modelscache.Options{})
		tokenStore = wrappedTokens
	} else {
		tokenStore = auth.NewTokenStore(dbStore.DB, dbStore.Dialect)
	}

	// Initialize Usage Counter
	var usageCounter *usage.Counter
	if dbStore != nil {
		usageCounter = usage.NewCounter(dbStore.DB, dbStore.Dialect)
		log.Printf("Usage counter initialized")
	}

	// Initialize UI Server
	uiServer := ui.NewServer(bus, configMgr, proxyConfig, modelsConfig, reqStore, bufferStore, tokenStore, dbStore)
	ui.SetVersion(Version)

	// Initialize Proxy Handler (Phase 3 / Task 12) — the engine is the 7th
	// positional arg. Pass nil for legacy / test paths; the seam is
	// nil-safe and degrades to single-credential resolution via
	// ResolveInternalConfigWithAffinity's nil-engine branch.
	proxyHandler := proxy.NewHandler(proxyConfig, bus, reqStore, bufferStore, tokenStore, usageCounter, credLB)

	// Setup Server
	mux := http.NewServeMux()

	// Check for deprecated MCP_PROXY_PORT env var
	if os.Getenv("MCP_PROXY_PORT") != "" {
		log.Printf("[MCP] WARNING: MCP_PROXY_PORT is deprecated, use MCP_ENABLED=true instead. MCP now runs on the main port.")
	}

	// Initialize MCP Proxy Server (optional — only if MCP_ENABLED is set)
	var mcpServer *mcp.Server
	if os.Getenv("MCP_ENABLED") == "true" {
		mcpStore := mcp.NewMCPStore(dbStore.DB, dbStore.Dialect)
		mcpServer = mcp.NewServer(mcpStore, bus, tokenStore)

		// Register MCP management API routes on the main mux (for frontend access)
		mcpServer.RegisterAPIHandlers(mux)

		// Register MCP proxy routes on the main mux (for client access)
		mcpServer.RegisterProxyHandlers(mux)

		log.Printf("[MCP] MCP proxy enabled")
	} else {
		log.Printf("[MCP] MCP proxy disabled (set MCP_ENABLED=true to enable)")
	}

	// Register UI handlers (root /, /api/...)
	uiServer.RegisterHandlers(mux)

	// Register Proxy handlers
	mux.HandleFunc("/v1/chat/completions", proxyHandler.HandleChatCompletions)
	mux.HandleFunc("/v1/messages", proxyHandler.HandleAnthropicMessages) // Anthropic Messages API endpoint
	mux.HandleFunc("/v1/models", proxyHandler.HandleModels)              // OpenAI-compatible models list

	// Health check endpoint
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Calculate server timeouts
	// ReadTimeout: Time limit for reading entire request (headers + body)
	// This prevents slowloris attacks where client sends headers byte-by-byte
	readTimeout := 30 * time.Second

	// WriteTimeout: Time limit for writing response
	// Set to MaxGenerationTime + buffer for retries + processing overhead
	// This ensures connections can't hang indefinitely
	writeTimeout := cfg.MaxGenerationTime.Duration() * 3 // 3x for retries + overhead
	if writeTimeout < 5*time.Minute {
		writeTimeout = 5 * time.Minute // Minimum 5 minutes
	}
	if writeTimeout > 30*time.Minute {
		writeTimeout = 30 * time.Minute // Maximum 30 minutes
	}

	// IdleTimeout: Time to keep keep-alive connections alive between requests
	// This prevents connection pool exhaustion
	idleTimeout := 300 * time.Second

	// Transparent request-body gzip decompression is wired as the
	// INNER of two middlewares wrapping the mux. Order matters: gzipmw
	// runs FIRST so every handler — proxy, ultimatemodel (which
	// ultimately calls Execute → executeExternal with body bytes
	// produced from r.Body via initRequestContext's parse), UI, MCP
	// — sees a decompressed body. recoveryMiddleware is the OUTER
	// wrapper so a panic inside gzipmw (e.g. on a pathological
	// gzip header) still gets a 500 instead of crashing the process.
	// Gzip errors are handled explicitly inside gzipmw and emit
	// OpenAI-shape 4xx envelopes; recoveryMiddleware only catches
	// the unexpected.
	//
	// Compression scope: gzipmw is conditional on Content-Encoding:
	// gzip and ONLY gzip; requests without that header (or with
	// "identity") pass through untouched so uncompressed clients
	// experience zero behavior change. See pkg/middleware/gzipmw for
	// the contract and cap story.
	// db-cache-layer 1C (W2) — cache teardown ordering. The final
	// shutdown sequence on signal must be:
	//   srv.Shutdown (drains HTTP; called explicitly below)
	//   → wrappedModels.Stop (cancels the reconciler + in-flight scan)
	//   → wrappedTokens.Stop (no-op by contract, kept for symmetry)
	//   → credLB.Stop (engine janitor)
	//   → modelsMgr.Close (event-bus unsubscribe)
	//   → dbStore.Close (closes the DB)
	// Defers register in source order and fire LIFO, so these two are
	// registered LAST (right before the listen block, after the
	// credLB/modelsMgr/dbStore defers above) with nil guards for the
	// CACHE_LAYER_ENABLED=false path. Registration order here is
	// tokens-then-models so LIFO fires models-then-tokens.
	if wrappedTokens != nil {
		defer wrappedTokens.Stop()
	}
	if wrappedModels != nil {
		defer wrappedModels.Stop()
	}

	srv := &http.Server{
		Addr:           ":" + strconv.Itoa(cfg.Port),
		Handler:        recoveryMiddleware(gzipmw.DecompressRequest(mux)),
		ReadTimeout:    readTimeout,
		WriteTimeout:   writeTimeout,
		IdleTimeout:    idleTimeout,
		MaxHeaderBytes: 1 << 20, // 1MB max header size (prevents memory exhaustion)
	}

	// Graceful Shutdown
	go func() {
		log.Printf("LLM Supervisor Proxy (build %s) starting on port %d", Version, cfg.Port)
		configJSON, _ := json.MarshalIndent(cfg, "", "  ")
		log.Printf("Current Configuration:\n%s", string(configJSON))

		allModels := modelsConfig.GetModels()
		modelsJSON, _ := json.MarshalIndent(allModels, "", "  ")
		log.Printf("Loaded Models:\n%s", string(modelsJSON))

		log.Printf("Dashboard available at http://localhost:%d", cfg.Port)
		log.Printf("OpenAI endpoint: http://localhost:%d/v1/chat/completions", cfg.Port)
		log.Printf("Anthropic endpoint: http://localhost:%d/v1/messages", cfg.Port)
		log.Printf("Models endpoint: http://localhost:%d/v1/models", cfg.Port)

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	// Shutdown MCP server (connection cleanup only)
	if mcpServer != nil {
		if err := mcpServer.Shutdown(context.Background()); err != nil {
			log.Printf("[MCP] MCP shutdown error: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		// Round 3j S3 — was `log.Fatal`, which SKIPPED the deferred
		// teardown (engine stop → modelsMgr close → dbStore close) and
		// terminated the process. We log and fall through; the LIFO
		// defers below still fire so the engine janitor stops cleanly
		// and the DB connection is released. A force-shutdown is a
		// warning, not a reason to leak goroutines or DB handles.
		log.Printf("Server forced to shutdown (continuing with deferred teardown): %v", err)
	}

	log.Println("Server exiting")
}

// recoveryMiddleware catches panics in HTTP handlers, logs the stack trace,
// returns HTTP 500 to the client, and prevents the process from crashing.
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic recovered: %v\n%s", err, debug.Stack())
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
