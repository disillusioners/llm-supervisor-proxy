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

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/ui"
)

func main() {
	// Configuration
	upstreamURL := os.Getenv("UPSTREAM_URL")
	if upstreamURL == "" {
		upstreamURL = "http://localhost:4001"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8089"
	}

	idleTimeoutStr := os.Getenv("IDLE_TIMEOUT")
	idleTimeout := 10 * time.Second
	if val, err := time.ParseDuration(idleTimeoutStr); err == nil {
		idleTimeout = val
	}

	maxGenTimeStr := os.Getenv("MAX_GENERATION_TIME")
	maxGenTime := 180 * time.Second
	if val, err := time.ParseDuration(maxGenTimeStr); err == nil {
		maxGenTime = val
	}

	maxRetriesStr := os.Getenv("MAX_RETRIES")
	maxRetries := 1
	if val, err := strconv.Atoi(maxRetriesStr); err == nil {
		maxRetries = val
	}

	// Initialize Shared Components
	bus := events.NewBus()
	store := store.NewRequestStore(100) // Keep last 100 requests
	config := &proxy.Config{
		UpstreamURL:       upstreamURL,
		IdleTimeout:       idleTimeout,
		MaxGenerationTime: maxGenTime,
		MaxRetries:        maxRetries,
	}

	// Initialize UI Server
	uiServer := ui.NewServer(bus, config, store)

	// Initialize Proxy Handler
	proxyHandler := proxy.NewHandler(config, bus, store)

	// Setup Server
	mux := http.NewServeMux()

	// Register UI handlers (root /, /api/...)
	uiServer.RegisterHandlers(mux)

	// Register Proxy handler
	mux.HandleFunc("/v1/chat/completions", proxyHandler.HandleChatCompletions)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	// Graceful Shutdown
	go func() {
		log.Printf("Starting LLM Supervisor Proxy on port %s", port)
		log.Printf("Config: Upstream=%s, IdleTimeout=%s, MaxGenTime=%s, MaxRetries=%d",
			upstreamURL, idleTimeout, maxGenTime, maxRetries)
		log.Printf("Dashboard available at http://localhost:%s", port)

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
