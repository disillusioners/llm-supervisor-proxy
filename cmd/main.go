package main

import (
	"context"
	"encoding/json"
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

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL != "" {
		log.Printf("Using PostgreSQL database (DATABASE_URL is set)")
	} else {
		log.Printf("Using SQLite database for local development")
	}

	cfg := configMgr.Get()

	// Initialize Proxy Config
	proxyConfig := &proxy.Config{
		ConfigMgr:    configMgr,
		ModelsConfig: modelsConfig,
	}

	// Initialize UI Server
	uiServer := ui.NewServer(bus, configMgr, proxyConfig, modelsConfig, reqStore)

	// Initialize Proxy Handler
	proxyHandler := proxy.NewHandler(proxyConfig, bus, reqStore)

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
		configJSON, _ := json.MarshalIndent(cfg, "", "  ")
		log.Printf("Current Configuration:\n%s", string(configJSON))

		allModels := modelsConfig.GetModels()
		modelsJSON, _ := json.MarshalIndent(allModels, "", "  ")
		log.Printf("Loaded Models:\n%s", string(modelsJSON))

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
