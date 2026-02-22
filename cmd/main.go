package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/ui"
)

// Version is set at build time via -ldflags
var Version = "dev"

func main() {
	// Initialize Shared Components
	bus := events.NewBus()
	store := store.NewRequestStore(100) // Keep last 100 requests
	modelsConfig := models.NewModelsConfig()

	// Load models from user config directory: ~/.config/llm-supervisor-proxy/models.json
	modelsConfigPath := models.GetConfigPath()
	_ = modelsConfig.Load(modelsConfigPath) // Ignore error if file doesn't exist

	// Initialize Config Manager
	configMgr, err := config.NewManagerWithEventBus(bus)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	cfg := configMgr.Get()

	// Initialize Proxy Config
	proxyConfig := &proxy.Config{
		ConfigMgr:    configMgr,
		ModelsConfig: modelsConfig,
	}

	// Initialize UI Server
	uiServer := ui.NewServer(bus, configMgr, proxyConfig, modelsConfig, store)

	// Initialize Proxy Handler
	proxyHandler := proxy.NewHandler(proxyConfig, bus, store)

	// Setup Server
	mux := http.NewServeMux()

	// Register UI handlers (root /, /api/...)
	uiServer.RegisterHandlers(mux)

	// Register Proxy handler
	mux.HandleFunc("/v1/chat/completions", proxyHandler.HandleChatCompletions)

	srv := &http.Server{
		Addr:    ":" + strconv.Itoa(cfg.Port),
		Handler: mux,
	}

	// Graceful Shutdown
	go func() {
		log.Printf("LLM Supervisor Proxy (build %s) starting on port %d", Version, cfg.Port)
		log.Printf("Config: Upstream=%s, IdleTimeout=%s, MaxGenTime=%s, MaxUpstreamErrorRetries=%d",
			cfg.UpstreamURL, cfg.IdleTimeout, cfg.MaxGenerationTime, cfg.MaxUpstreamErrorRetries)
		log.Printf("Dashboard available at http://localhost:%d", cfg.Port)

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
