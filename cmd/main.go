package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/crypto"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/ui"
)

// Version is set at build time via -ldflags
var Version = "dev"

func main() {
	ctx := context.Background()

	// Initialize Shared Components
	bus := events.NewBus()
	reqStore := store.NewRequestStore(100) // Keep last 100 requests

	var configMgr config.ManagerInterface
	var modelsConfig models.ModelsConfigInterface
	var dbStore *database.Store

	// Always use database storage
	// - If DATABASE_URL is set with postgres://, uses PostgreSQL
	// - Otherwise uses SQLite at ~/.config/llm-supervisor-proxy/config.db
	var err error
	dbStore, configMgr, modelsConfig, err = database.InitializeAll(ctx, bus)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer dbStore.Close()

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

	// Initialize Proxy Config
	proxyConfig := &proxy.Config{
		ConfigMgr:    configMgr,
		ModelsConfig: modelsConfig,
	}

	// Initialize Token Store
	tokenStore := auth.NewTokenStore(dbStore.DB, dbStore.Dialect)

	// Initialize UI Server
	uiServer := ui.NewServer(bus, configMgr, proxyConfig, modelsConfig, reqStore, bufferStore, tokenStore)
	ui.SetVersion(Version)

	// Initialize Proxy Handler
	proxyHandler := proxy.NewHandler(proxyConfig, bus, reqStore, bufferStore, tokenStore)

	// Setup Server
	mux := http.NewServeMux()

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
		Handler:        mux,
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("Server forced to shutdown:", err)
	}

	log.Println("Server exiting")
}
