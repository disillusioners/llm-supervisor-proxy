package ui

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	bus    *events.Bus
	config *proxy.Config // Pointer to live config
	store  *store.RequestStore
	mu     sync.Mutex
}

func NewServer(bus *events.Bus, config *proxy.Config, store *store.RequestStore) *Server {
	return &Server{
		bus:    bus,
		config: config,
		store:  store,
	}
}

func (s *Server) RegisterHandlers(mux *http.ServeMux) {
	// Static files
	staticFS, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(staticFS))
	mux.Handle("/", fileServer)

	// API
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/requests", s.handleRequests)
	mux.HandleFunc("/api/requests/", s.handleRequestDetail)
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	requests := s.store.List()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(requests)
}

func (s *Server) handleRequestDetail(w http.ResponseWriter, r *http.Request) {
	// /api/requests/{id}
	id := r.URL.Path[len("/api/requests/"):]
	if id == "" {
		http.Error(w, "Missing ID", http.StatusBadRequest)
		return
	}

	req := s.store.Get(id)
	if req == nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(req)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.config)
		return
	}

	if r.Method == http.MethodPost {
		// Parse update
		var newConfig proxy.Config
		if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Update live config (simplified)
		// Note: concurrency safety for Config access in Handler needs to be ensured.
		// For now, we assume simple atomic-like struct copy or acceptable race for demo.
		// In prod, use atomic.Value or mutex.
		*s.config = newConfig

		s.bus.Publish(events.Event{
			Type:      "config_updated",
			Timestamp: time.Now().Unix(),
			Data:      newConfig,
		})

		w.WriteHeader(http.StatusOK)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	sub := s.bus.Subscribe()

	// Clean up on exit
	// We need to know when client disconnects
	ctx := r.Context()

	for {
		select {
		case evt := <-sub:
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-ctx.Done():
			s.bus.Unsubscribe(sub) // We need to expose Unsubscribe or handle it
			return
		}
	}
}
