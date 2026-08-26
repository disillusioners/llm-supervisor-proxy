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
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
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
	modelsConfig = modelsMgr

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
	tokenStore := auth.NewTokenStore(dbStore.DB, dbStore.Dialect)

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

	srv := &http.Server{
		Addr:           ":" + strconv.Itoa(cfg.Port),
		Handler:        recoveryMiddleware(mux),
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
		log.Fatal("Server forced to shutdown:", err)
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
