package ui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/bufferstore"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/proxy"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// Model is the wire DTO exposed by the model CRUD handlers
// (`handleModels`, `handleModelDetail`, `handleValidateModel`).
//
// Phase 4 wire shape: the multi-credential `credentials[]` slice is
// the SINGLE application source of truth on the API surface. The DB
// column `models.credential_id` is retained only as a derived shadow
// (= `credentials[0].credential_id`) for legacy/external readers
// until migration 029 lands; the API contract never re-exposes it as
// a top-level field. A legacy top-level `credential_id` key in a
// POST/PUT payload is silently DROPPED by the decoder as an unknown
// field during the deprecation window — when `credentials[]` is
// present, the array wins; when `credentials[]` is absent on an
// internal model, the server returns 400 telling the operator to
// reload the Web UI and re-save. The single-credential test endpoint
// (`TestModelRequest`) is the only legitimate carrier of a top-level
// credential identifier; it remains exempt from this gate by design.
type Model struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	FallbackChain  []string `json:"fallback_chain"`
	TruncateParams []string `json:"truncate_params,omitempty"`
	// Internal upstream fields
	Internal        bool                   `json:"internal"`
	Credentials     []models.CredentialRef `json:"credentials,omitempty"`       // Ordered, weighted credential refs (Phase 4 wire shape)
	InternalBaseURL string                 `json:"internal_base_url,omitempty"` // Base URL override (optional)
	InternalModel   string                 `json:"internal_model,omitempty"`
	// Stream buffering deadline
	ReleaseStreamChunkDeadline models.Duration `json:"release_stream_chunk_deadline,omitempty"`
	// Peak hour auto-switch fields
	PeakHourEnabled  bool   `json:"peak_hour_enabled"`
	PeakHourStart    string `json:"peak_hour_start"`
	PeakHourEnd      string `json:"peak_hour_end"`
	PeakHourTimezone string `json:"peak_hour_timezone"`
	PeakHourModel    string `json:"peak_hour_model"`
	// Secondary upstream model for retry logic
	SecondaryUpstreamModel string `json:"secondary_upstream_model,omitempty"`
	// Ultimate model exclusion
	ExcludeFromUltimateSwitching bool `json:"exclude_from_ultimate_switching,omitempty"`
}

// Credential represents a credential for API authentication (with masked API key)
type Credential struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"` // Masked API key (e.g., "sk-abcde***")
	BaseURL  string `json:"base_url,omitempty"`
}

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	bus          *events.Bus
	configMgr    config.ManagerInterface      // Config manager
	proxyConfig  *proxy.Config                // Keep for models config access
	modelsConfig models.ModelsConfigInterface // Models config
	store        *store.RequestStore
	bufferStore  *bufferstore.BufferStore
	tokenStore   auth.TokenStoreInterface
	dbStore      *database.Store
	mu           sync.Mutex
}

func NewServer(bus *events.Bus, configMgr config.ManagerInterface, proxyConfig *proxy.Config, modelsConfig models.ModelsConfigInterface, store *store.RequestStore, bufferStore *bufferstore.BufferStore, tokenStore auth.TokenStoreInterface, dbStore *database.Store) *Server {
	return &Server{
		bus:          bus,
		configMgr:    configMgr,
		proxyConfig:  proxyConfig,
		modelsConfig: modelsConfig,
		store:        store,
		bufferStore:  bufferStore,
		tokenStore:   tokenStore,
		dbStore:      dbStore,
	}
}

// version is set at build time via -ldflags (passed from main package)
var version = "dev"

// SetVersion allows the main package to set the version at startup
func SetVersion(v string) {
	version = v
}

func (s *Server) RegisterHandlers(mux *http.ServeMux) {
	// Static files
	staticFS, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(staticFS))

	// UI at /ui
	mux.HandleFunc("/ui/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/ui/")
		if path == "" || path == "index.html" {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		// Try static file first
		f, err := staticFS.Open(path)
		if err == nil {
			f.Close()
			http.StripPrefix("/ui/", fileServer).ServeHTTP(w, r)
			return
		}
		// Fallback to index.html for SPA client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	// Redirect root to UI
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/ui/", http.StatusTemporaryRedirect)
			return
		}
		http.NotFound(w, r)
	})

	// API at /fe/api
	mux.HandleFunc("/fe/api/version", s.handleVersion)
	mux.HandleFunc("/fe/api/ram", s.handleRam)
	mux.HandleFunc("/fe/api/config", s.handleConfig)
	mux.HandleFunc("/fe/api/models", s.handleModels)
	mux.HandleFunc("/fe/api/models/", s.handleModelDetail)
	mux.HandleFunc("/fe/api/models/validate", s.handleValidateModel)
	mux.HandleFunc("/fe/api/models/test", s.handleTestModel)
	mux.HandleFunc("/fe/api/events", s.handleEvents)
	mux.HandleFunc("/fe/api/requests", s.handleRequests)
	// /fe/api/requests/{id} and /fe/api/requests/{id}/summary share a
	// single mux entry because stdlib ServeMux matches the registered
	// PREFIX /fe/api/requests/ as a subtree: both /fe/api/requests/x
	// and /fe/api/requests/x/summary land here. handleRequestDetail
	// and handleRequestSummary are dispatched from inside this single
	// handler based on the URL suffix, not by ServeMux longest-prefix
	// resolution (only ONE pattern ending in "/" can match any
	// subtree, so we can't register them separately and still keep
	// the summary route working).
	mux.HandleFunc("/fe/api/requests/", s.handleRequestDetailOrSummary)
	mux.HandleFunc("/fe/api/app-tags", s.handleAppTags)
	mux.HandleFunc("/fe/api/buffers/", s.handleBufferContent)
	// Token management
	mux.HandleFunc("/fe/api/tokens", s.handleTokens)
	mux.HandleFunc("/fe/api/tokens/", s.handleTokenDetail)
	// Credential management
	mux.HandleFunc("/fe/api/credentials", s.handleCredentials)
	mux.HandleFunc("/fe/api/credentials/", s.handleCredentialDetail)
	// Provider list
	mux.HandleFunc("/fe/api/providers", s.handleProviders)
	// Usage API
	mux.HandleFunc("/fe/api/usage", s.handleUsage)
	mux.HandleFunc("/fe/api/usage/tokens", s.handleUsageTokens)
	mux.HandleFunc("/fe/api/usage/summary", s.handleUsageSummary)
	mux.HandleFunc("/fe/api/usage/models", s.handleUsageModels)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"version": version})
}

func (s *Server) handleRam(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Alloc is bytes of allocated heap objects (current usage)
	allocBytes := m.Alloc
	allocMB := float64(allocBytes) / 1024 / 1024

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"alloc_bytes": allocBytes,
		"alloc_mb":    allocMB,
	})
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	providersList := providers.GetProviders()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(providersList)
}

// RequestSummary is the metadata-only projection returned by
// handleRequests() and handleRequestSummary() (P0-1).
//
// Heavy fields (`messages`, `parameters`) are deliberately omitted so
// that a default /fe/api/requests fetch returns a tiny payload (~50 KB
// instead of ~96 MB for a 100-entry store). FE clients that need the
// full body can either:
//
//   - request the detail endpoint: GET /fe/api/requests/{id} (full
//     shape including messages), OR
//   - opt-in via /fe/api/requests?include=messages (legacy parity).
//
// Adding fields here is intentionally a no-default-omission change —
// every json tag is plain (no `omitempty` on required projection fields)
// so the wire shape is stable. Fields that are zero in the source are
// emitted as zero values so the FE schema is predictable.
//
// New fields added by this struct:
//   - MessageCount: int — number of stored messages on this request.
//   - TotalSizeBytes: int — approximate in-memory byte cost (see
//     store.RequestLog.Size for the heuristic). Surfaced so the UI can
//     show payload weights and the operator can spot outlier requests
//     before opening them.
type RequestSummary struct {
	ID                string                 `json:"id"`
	Status            string                 `json:"status"`
	Model             string                 `json:"model"`
	StartTime         time.Time              `json:"startTime"`
	EndTime           time.Time              `json:"endTime"`
	Duration          string                 `json:"duration"`
	Retries           int                    `json:"retries"`
	Error             string                 `json:"error,omitempty"`
	Usage             *store.Usage           `json:"usage,omitempty"`
	TokenID           string                 `json:"token_id,omitempty"`
	TokenName         string                 `json:"token_name,omitempty"`
	OriginalModel     string                 `json:"original_model,omitempty"`
	FallbackUsed      []string               `json:"fallback_used,omitempty"`
	CurrentFallback   string                 `json:"current_fallback,omitempty"`
	UltimateModelUsed bool                   `json:"ultimate_model_used"`
	UltimateModelID   string                 `json:"ultimate_model_id,omitempty"`
	IsStream          bool                   `json:"is_stream"`
	AppTag            string                 `json:"app_tag,omitempty"`
	UpstreamRequests  store.UpstreamRequestStatus `json:"upstream_requests,omitempty"`
	// P0-1 summary projection additions:
	MessageCount    int   `json:"message_count"`
	TotalSizeBytes  int64 `json:"total_size_bytes"`
}

// summaryFromRequest builds a RequestSummary from a stored *store.RequestLog.
// Cheap: only the per-string-field copies are touched. The TotalSizeBytes
// value uses the O(1) sizeByID lookup via store.SizeOf(id) — NOT r.Size()
// (which is O(n) over messages and would dominate the list endpoint's
// cost on the FE API payload fix). The list path calls this once per
// entry so the per-entry constant matters.
func summaryFromRequest(s *Server, r *store.RequestLog) RequestSummary {
	return RequestSummary{
		ID:                r.ID,
		Status:            r.Status,
		Model:             r.Model,
		StartTime:         r.StartTime,
		EndTime:           r.EndTime,
		Duration:          r.Duration,
		Retries:           r.Retries,
		Error:             r.Error,
		Usage:             r.Usage,
		TokenID:           r.TokenID,
		TokenName:         r.TokenName,
		OriginalModel:     r.OriginalModel,
		FallbackUsed:      r.FallbackUsed,
		CurrentFallback:   r.CurrentFallback,
		UltimateModelUsed: r.UltimateModelUsed,
		UltimateModelID:   r.UltimateModelID,
		IsStream:          r.IsStream,
		AppTag:            r.AppTag,
		UpstreamRequests:  r.UpstreamRequests,
		MessageCount:      len(r.Messages),
		TotalSizeBytes:    s.store.SizeOf(r.ID),
	}
}

// Pagination defaults & caps for handleRequests (P0-1).
const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// parsePagination extracts limit/offset query parameters with the
// following rules:
//
//   - limit: defaults to defaultListLimit (50) when absent, blank,
//     non-numeric, negative, or zero. Values above maxListLimit (200)
//     are clamped DOWN to maxListLimit. Non-numeric strings silently
//     fall back to the default (NOT a 400 — bad input is treated as
//     "no input" so an old URL with stale query junk still works).
//   - offset: defaults to 0; negative values are clamped UP to 0.
//
// Returns limit, offset, and a bool indicating whether the caller
// passed ANY pagination parameter (even an invalid one). The handler
// uses that bool to decide whether to apply pagination OR (when
// include=messages AND no pagination params) return everything.
func parsePagination(q url.Values) (limit, offset int, hasParams bool) {
	limit = defaultListLimit
	offset = 0

	if v := q.Get("limit"); v != "" {
		hasParams = true
		if n, err := strconv.Atoi(v); err == nil {
			if n > 0 {
				limit = n
			} else {
				// n <= 0 (including negative): fall back to default.
				limit = defaultListLimit
			}
		}
		// err != nil: leave limit at default; bad input is non-fatal.
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	if v := q.Get("offset"); v != "" {
		hasParams = true
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
		// err != nil OR n <= 0: leave offset at 0 (negative clamps up).
	}

	return limit, offset, hasParams
}

// applyPagination returns a sub-slice of items bounded by offset and
// limit. The returned slice MAY alias items' backing array — callers
// must not mutate the elements (which are pointers; mutating *RequestLog
// fields through the sub-slice would mutate the originals). The slice
// itself is a fresh slice header with its own len/cap pointing into
// the source's storage; mutating the header (e.g. items[0] = nil) is
// safe but mutating *items[i] is NOT.
func applyPagination(items []*store.RequestLog, limit, offset int) []*store.RequestLog {
	if offset >= len(items) {
		return []*store.RequestLog{}
	}
	items = items[offset:]
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	appFilter := q.Get("app")
	includeMessages := q.Get("include") == "messages"
	limit, offset, hasPaginationParams := parsePagination(q)

	var requests []*store.RequestLog
	switch appFilter {
	case "", "all":
		requests = s.store.List()
	case "default":
		requests = s.store.ListFiltered("")
	default:
		requests = s.store.ListFiltered(appFilter)
	}

	// Legacy parity: include=messages WITHOUT pagination params returns
	// the entire filtered list (external scripts may depend on this).
	// Any pagination param (limit or offset, even invalid) switches
	// us into the paginated/contracted path. include=messages WITH
	// pagination params is honored: messages stay in payload, but the
	// page is bounded by limit/offset.
	if includeMessages && !hasPaginationParams {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(requests)
		return
	}

	requests = applyPagination(requests, limit, offset)

	w.Header().Set("Content-Type", "application/json")
	if includeMessages {
		// include=messages WITH pagination → full per-item shape,
		// but paged.
		json.NewEncoder(w).Encode(requests)
		return
	}

	summaries := make([]RequestSummary, len(requests))
	for i, req := range requests {
		summaries[i] = summaryFromRequest(s, req)
	}
	json.NewEncoder(w).Encode(summaries)
}

func (s *Server) handleAppTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tags := s.store.GetUniqueAppTags()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tags)
}

func (s *Server) handleRequestDetail(w http.ResponseWriter, r *http.Request) {
	// /fe/api/requests/{id} — full payload incl. messages
	id := r.URL.Path[len("/fe/api/requests/"):]
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

// handleRequestDetailOrSummary dispatches /fe/api/requests/{id} and
// /fe/api/requests/{id}/summary to the right handler. Both share the
// same prefix (which is why we route them through one mux entry: the
// stdlib ServeMux's subtree match for /fe/api/requests/ catches both).
//
// The suffix /summary goes to handleRequestSummary; anything else
// (including the bare /fe/api/requests/ prefix with no ID) goes to
// handleRequestDetail. We intentionally do NOT split into two mux
// entries — stdlib ServeMux would not allow both /fe/api/requests/
// (catch-all) and /fe/api/requests/{anything} (exact match) to coexist
// with deterministic ordering, so the dispatcher keeps the routing
// surface explicit and testable.
func (s *Server) handleRequestDetailOrSummary(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/summary") {
		s.handleRequestSummary(w, r)
		return
	}
	s.handleRequestDetail(w, r)
}

// handleRequestSummary serves GET /fe/api/requests/{id}/summary — the
// metadata-only projection of a single request, no `messages`.
//
// Used by the FE to insert brand-new rows arriving via SSE
// `request_started` events WITHOUT triggering a full list refetch. The
// shape is exactly one element of the default handleRequests() output,
// so the FE can append the JSON directly into its list state.
func (s *Server) handleRequestSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Path is /fe/api/requests/{id}/summary
	const prefix = "/fe/api/requests/"
	const suffix = "/summary"
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
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
	json.NewEncoder(w).Encode(summaryFromRequest(s, req))
}

func (s *Server) handleBufferContent(w http.ResponseWriter, r *http.Request) {
	// /fe/api/buffers/{id}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Path[len("/fe/api/buffers/"):]
	if id == "" {
		http.Error(w, "Missing buffer ID", http.StatusBadRequest)
		return
	}

	if s.bufferStore == nil {
		http.Error(w, "Buffer storage not configured", http.StatusServiceUnavailable)
		return
	}

	content, err := s.bufferStore.Get(id)
	if err != nil {
		http.Error(w, "Buffer not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(content)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.configMgr.Get())
		return

	case http.MethodPut: // Changed from POST to RESTful convention
		// Limit request body to 64KB to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var cfg config.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
			return
		}

		// Clear read-only fields (prevent client manipulation)
		cfg.Version = ""   // Will be set by Save()
		cfg.UpdatedAt = "" // Will be set by Save()

		result, err := s.configMgr.Save(cfg)
		if err != nil {
			if strings.Contains(err.Error(), "validation failed") {
				http.Error(w, err.Error(), http.StatusBadRequest)
			} else if strings.Contains(err.Error(), "read-only") {
				http.Error(w, "Config file is read-only", http.StatusForbidden)
			} else {
				http.Error(w, fmt.Sprintf("Failed to save: %v", err), http.StatusInternalServerError)
			}
			return
		}

		// Include restart hint in response
		response := struct {
			config.Config
			RestartRequired bool     `json:"restart_required"`
			ChangedFields   []string `json:"changed_fields,omitempty"`
		}{
			Config:          s.configMgr.Get(),
			RestartRequired: result.RestartRequired,
			ChangedFields:   result.ChangedFields,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
		return

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	sub, err := s.bus.Subscribe()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer s.bus.Unsubscribe(sub)

	// We need to know when client disconnects
	ctx := r.Context()

	// Add heartbeat ticker to keep connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case evt := <-sub:
			data, _ := json.Marshal(evt)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				// Client disconnected or write error
				return
			}
			flusher.Flush()
		case <-ticker.C:
			// Send comment heartbeat
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				// Client disconnected
				return
			}
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

// validateCredentials enforces the Phase-1 multi-credential validation
// matrix on a wire-shape []models.CredentialRef slice before it is
// persisted as a `models.ModelConfig.Credentials` field. It mirrors
// the rules in `pkg/models/config.go` ModelsConfig.Validate
// (lines ~584-636) so that UI and persisted-DB validation agree on
// every error path.
//
// Rules enforced (in order):
//
//   (a) internal=true && len(creds)==0 → at least one credential
//
//	is required.
//
//   (b) each entry's credential identifier is non-empty AND exists in
//
//	`existingCreds` (looked up by id).
//
//   (c) each entry's weight is strictly positive (> 0).
//   (d) all entries' providers match the first entry's provider
//
//	(case-insensitive, same as pkg/models).
//
//   (e) no duplicate credential identifiers within the slice.
//   (f) len(creds) <= models.MaxCredentialRefs (16).
//
// On acceptance, the function walks the slice and STAMPS each
// entry's Position to the slice index (0-based) so callers persist
// deterministic ordering regardless of the operator's payload order
// (the frontend sends positions only as a server-managed hint —
// see Phase 1 plan-overview).
//
// Returns a slice of human-readable error messages (empty on success).
// The caller decides the HTTP response shape (POST/PUT return
// 400 {error}; the validate endpoint returns {valid:false,
// errors:[...]}).
func validateCredentials(creds []models.CredentialRef, internal bool, existingCreds []models.CredentialConfig) []string {
	var errs []string

	// Build a lookup index by id for rule (b) and rule (d).
	byID := make(map[string]models.CredentialConfig, len(existingCreds))
	for _, c := range existingCreds {
		byID[c.ID] = c
	}

	// Rule (a) — internal models must carry at least one credential.
	if internal && len(creds) == 0 {
		errs = append(errs, "credentials: at least one credential is required when internal is true (the legacy single-credential field was removed — reload the Web UI and re-save)")
	}

	// Rule (f) — size cap. Checked before per-entry loops so a 17-entry
	// payload reports the cap, not 17 weight errors.
	if len(creds) > models.MaxCredentialRefs {
		errs = append(errs, fmt.Sprintf("credentials: list exceeds max of %d refs (got %d)", models.MaxCredentialRefs, len(creds)))
	}

	// Pre-resolve primary provider (rule (d) anchor) for the
	// case-insensitive same-provider comparison.
	var primaryProvider string
	if len(creds) > 0 {
		if primary, ok := byID[creds[0].CredentialID]; ok {
			primaryProvider = strings.ToLower(primary.Provider)
		}
	}

	seen := make(map[string]bool, len(creds))
	for idx, ref := range creds {
		// Rule (b) — non-empty id that exists.
		if ref.CredentialID == "" {
			errs = append(errs, fmt.Sprintf("credentials[%d]: identifier is empty", idx))
		} else if existing, ok := byID[ref.CredentialID]; !ok {
			errs = append(errs, fmt.Sprintf("credentials[%d]: identifier %q references an unknown credential", idx, ref.CredentialID))
		} else if idx == 0 {
			// First entry exists — fine, just used below for the
			// provider-match comparison.
			_ = existing
		}

		// Rule (c) — strictly positive weight.
		if ref.Weight <= 0 {
			errs = append(errs, fmt.Sprintf("credentials[%d] (identifier=%q): weight must be > 0, got %d", idx, ref.CredentialID, ref.Weight))
		}

		// Rule (e) — no duplicates within the slice.
		if ref.CredentialID != "" {
			if seen[ref.CredentialID] {
				errs = append(errs, fmt.Sprintf("credentials[%d]: duplicate identifier %q in credentials list", idx, ref.CredentialID))
			}
			seen[ref.CredentialID] = true
		}

		// Rule (d) — all entries share the primary provider
		// (case-insensitive). Only enforced once both the primary
		// and the current entry resolve to existing credentials;
		// otherwise the (b) error above already covers it.
		if idx > 0 && primaryProvider != "" {
			if cur, ok := byID[ref.CredentialID]; ok {
				if strings.ToLower(cur.Provider) != primaryProvider {
					errs = append(errs, fmt.Sprintf("credentials[%d] (identifier=%q): provider %q does not match primary provider %q", idx, ref.CredentialID, cur.Provider, creds[0].CredentialID))
				}
			}
		}
	}

	if len(errs) == 0 {
		// Rule (g) — stamp positions to the slice index on acceptance.
		for i := range creds {
			creds[i].Position = i
		}
	}

	return errs
}

// handleModels handles GET and POST /fe/api/models
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		modelConfigs := s.modelsConfig.GetModels()
		models := make([]Model, len(modelConfigs))
		for i, mc := range modelConfigs {
			models[i] = Model{
				ID:                           mc.ID,
				Name:                         mc.Name,
				Enabled:                      mc.Enabled,
				FallbackChain:                mc.FallbackChain,
				TruncateParams:               mc.TruncateParams,
				Internal:                     mc.Internal,
				Credentials:                  mc.Credentials,
				InternalBaseURL:              mc.InternalBaseURL,
				InternalModel:                mc.InternalModel,
				ReleaseStreamChunkDeadline:   mc.ReleaseStreamChunkDeadline,
				PeakHourEnabled:              mc.PeakHourEnabled,
				PeakHourStart:                mc.PeakHourStart,
				PeakHourEnd:                  mc.PeakHourEnd,
				PeakHourTimezone:             mc.PeakHourTimezone,
				PeakHourModel:                mc.PeakHourModel,
				SecondaryUpstreamModel:       mc.SecondaryUpstreamModel,
				ExcludeFromUltimateSwitching: mc.ExcludeFromUltimateSwitching,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(models)

	case http.MethodPost:
		// Limit request body to 64KB to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var newModel Model
		if err := json.NewDecoder(r.Body).Decode(&newModel); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Validate model
		if newModel.Name == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "model name is required"})
			return
		}

		// Validate peak hour requires internal upstream
		if newModel.PeakHourEnabled && !newModel.Internal {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "peak_hour_enabled requires internal upstream to be enabled",
			})
			return
		}

		// Validate secondary_upstream_model requires internal upstream
		if newModel.SecondaryUpstreamModel != "" && !newModel.Internal {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "secondary_upstream_model requires internal to be true",
			})
			return
		}

		// Phase 4 multi-credential validation. validateCredentials
		// stamps positions into the slice on acceptance — so the
		// persisted ModelConfig carries deterministic ordering even
		// when the operator's payload didn't include (or had
		// inconsistent) position values. The validator emits ALL
		// applicable errors in one pass (not just the first); we
		// collapse them to a single 400 by joining with "; " so the
		// API contract keeps the {error: "..."} shape (no array
		// support in POST/PUT response per house style).
		if credErrs := validateCredentials(newModel.Credentials, newModel.Internal, s.modelsConfig.GetCredentials()); len(credErrs) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid credentials: " + strings.Join(credErrs, "; "),
			})
			return
		}

		// Generate ID if not provided
		if newModel.ID == "" {
			newModel.ID = fmt.Sprintf("model-%d", time.Now().UnixNano())
		}

		// Convert to models.ModelConfig. Positions have already been
		// stamped by validateCredentials (or the slice is empty for
		// an external model — both are legal).
		modelConfig := models.ModelConfig{
			ID:                           newModel.ID,
			Name:                         newModel.Name,
			Enabled:                      newModel.Enabled,
			FallbackChain:                newModel.FallbackChain,
			TruncateParams:               newModel.TruncateParams,
			Internal:                     newModel.Internal,
			Credentials:                  newModel.Credentials,
			InternalBaseURL:              newModel.InternalBaseURL,
			InternalModel:                newModel.InternalModel,
			ReleaseStreamChunkDeadline:   newModel.ReleaseStreamChunkDeadline,
			PeakHourEnabled:              newModel.PeakHourEnabled,
			PeakHourStart:                newModel.PeakHourStart,
			PeakHourEnd:                  newModel.PeakHourEnd,
			PeakHourTimezone:             newModel.PeakHourTimezone,
			PeakHourModel:                newModel.PeakHourModel,
			SecondaryUpstreamModel:       newModel.SecondaryUpstreamModel,
			ExcludeFromUltimateSwitching: newModel.ExcludeFromUltimateSwitching,
		}

		if err := s.modelsConfig.AddModel(modelConfig); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Persist changes to disk
		if err := s.modelsConfig.Save(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(newModel)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleModelDetail handles PUT and DELETE /fe/api/models/{id}
//
// Phase 4 replaced the single-ref DTO; the legacy top-level
// `credential_id` field is dropped as an unknown JSON field during the
// deprecation window. TestModelRequest (the operator-driven test
// endpoint) remains the only legitimate carrier of a top-level
// credential identifier — exempt from this gate by design.
func (s *Server) handleModelDetail(w http.ResponseWriter, r *http.Request) {
	// /fe/api/models/{id}
	id := r.URL.Path[len("/fe/api/models/"):]
	if id == "" {
		http.Error(w, "Missing ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodPut:
		// Limit request body to 64KB to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var updatedModel Model
		if err := json.NewDecoder(r.Body).Decode(&updatedModel); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Validate model
		if updatedModel.Name == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "model name is required"})
			return
		}

		// Validate peak hour requires internal upstream
		if updatedModel.PeakHourEnabled && !updatedModel.Internal {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "peak_hour_enabled requires internal upstream to be enabled",
			})
			return
		}

		// Validate secondary_upstream_model requires internal upstream
		if updatedModel.SecondaryUpstreamModel != "" && !updatedModel.Internal {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "secondary_upstream_model requires internal to be true",
			})
			return
		}

		// Phase 4 multi-credential validation. Same rules as POST
		// (see validateCredentials above). Positions are stamped
		// into the slice on acceptance so persistence is
		// deterministic.
		if credErrs := validateCredentials(updatedModel.Credentials, updatedModel.Internal, s.modelsConfig.GetCredentials()); len(credErrs) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "invalid credentials: " + strings.Join(credErrs, "; "),
			})
			return
		}

		// Keep the same ID
		updatedModel.ID = id

		// Convert to models.ModelConfig and update. Positions are
		// already stamped by validateCredentials above.
		modelConfig := models.ModelConfig{
			ID:                           updatedModel.ID,
			Name:                         updatedModel.Name,
			Enabled:                      updatedModel.Enabled,
			FallbackChain:                updatedModel.FallbackChain,
			TruncateParams:               updatedModel.TruncateParams,
			Internal:                     updatedModel.Internal,
			Credentials:                  updatedModel.Credentials,
			InternalBaseURL:              updatedModel.InternalBaseURL,
			InternalModel:                updatedModel.InternalModel,
			ReleaseStreamChunkDeadline:   updatedModel.ReleaseStreamChunkDeadline,
			PeakHourEnabled:              updatedModel.PeakHourEnabled,
			PeakHourStart:                updatedModel.PeakHourStart,
			PeakHourEnd:                  updatedModel.PeakHourEnd,
			PeakHourTimezone:             updatedModel.PeakHourTimezone,
			PeakHourModel:                updatedModel.PeakHourModel,
			SecondaryUpstreamModel:       updatedModel.SecondaryUpstreamModel,
			ExcludeFromUltimateSwitching: updatedModel.ExcludeFromUltimateSwitching,
		}

		if err := s.modelsConfig.UpdateModel(id, modelConfig); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Persist changes to disk
		if err := s.modelsConfig.Save(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updatedModel)

	case http.MethodDelete:
		if err := s.modelsConfig.RemoveModel(id); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Persist changes to disk
		if err := s.modelsConfig.Save(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleValidateModel handles POST /fe/api/models/validate
func (s *Server) handleValidateModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 64KB to prevent memory exhaustion attacks
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var model Model
	if err := json.NewDecoder(r.Body).Decode(&model); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Validate model name
	if model.Name == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "model name is required"})
		return
	}

	// Validate fallback chain - check that all models in the chain exist
	modelConfigs := s.modelsConfig.GetModels()

	existingModels := make(map[string]bool)
	for _, m := range modelConfigs {
		existingModels[m.Name] = true
	}

	// Add the current model being validated to the check
	existingModels[model.Name] = true

	var validationErrors []string
	for _, fallback := range model.FallbackChain {
		if !existingModels[fallback] {
			validationErrors = append(validationErrors, fmt.Sprintf("fallback model '%s' not found", fallback))
		}
	}

	// Phase 4 multi-credential validation. Same rules as POST/PUT
	// (see validateCredentials above). The validator does NOT stamp
	// positions on this path — the validate endpoint is dry-run and
	// the caller's payload is read-only.
	if credErrs := validateCredentials(model.Credentials, model.Internal, s.modelsConfig.GetCredentials()); len(credErrs) > 0 {
		validationErrors = append(validationErrors, credErrs...)
	}

	w.Header().Set("Content-Type", "application/json")
	if len(validationErrors) > 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"valid":  false,
			"errors": validationErrors,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid": true,
	})
}

// TestModelRequest represents the request body for testing a model
type TestModelRequest struct {
	CredentialID    string `json:"credential_id,omitempty"` // Credential to use
	APIKey          string `json:"api_key,omitempty"`       // API key override (optional)
	InternalBaseURL string `json:"internal_base_url"`       // Base URL override (optional)
	InternalModel   string `json:"internal_model"`          // Provider's model name to test
}

// TestModelResponse represents the response for testing a model
type TestModelResponse struct {
	Success  bool   `json:"success"`
	Response string `json:"response,omitempty"`
	Model    string `json:"model,omitempty"`
	Error    string `json:"error,omitempty"`
}

// handleTestModel handles POST /fe/api/models/test
func (s *Server) handleTestModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body to 64KB to prevent memory exhaustion attacks
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req TestModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   fmt.Sprintf("Invalid JSON: %v", err),
		})
		return
	}

	var apiKey, provider, baseURL string

	// If credential_id is provided, look up the credential
	if req.CredentialID != "" {
		cred := s.modelsConfig.GetCredential(req.CredentialID)
		if cred == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(TestModelResponse{
				Success: false,
				Error:   fmt.Sprintf("Credential not found: %s", req.CredentialID),
			})
			return
		}
		apiKey = cred.APIKey
		provider = cred.Provider
		baseURL = cred.BaseURL
	}

	// Use request values as overrides
	if req.InternalBaseURL != "" {
		baseURL = req.InternalBaseURL
	}
	if req.APIKey != "" {
		apiKey = req.APIKey
	}

	// Validate required fields
	if provider == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   "credential_id is required (provider is determined from credential)",
		})
		return
	}

	if apiKey == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   "credential has no API key (decryption may have failed - try re-saving the credential)",
		})
		return
	}

	if req.InternalModel == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   "internal_model is required",
		})
		return
	}

	// Create provider using the factory
	providerClient, err := providers.NewProvider(provider, apiKey, baseURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to create provider: %v", err),
		})
		return
	}

	// Create a minimal chat completion request with "hi" message
	model := req.InternalModel
	if model == "" {
		model = "gpt-4o" // default model
	}

	chatReq := &providers.ChatCompletionRequest{
		Model: model,
		Messages: []providers.ChatMessage{
			{
				Role:    "user",
				Content: "hi",
			},
		},
	}

	// Send the request
	resp, err := providerClient.ChatCompletion(r.Context(), chatReq)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(TestModelResponse{
			Success: false,
			Error:   fmt.Sprintf("Request failed: %v", err),
		})
		return
	}

	// Extract response content
	var responseText string
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		// Content can be string or array
		switch c := resp.Choices[0].Message.Content.(type) {
		case string:
			responseText = c
		case []interface{}:
			// Flatten array content to string
			var sb strings.Builder
			for _, part := range c {
				if partMap, ok := part.(map[string]interface{}); ok {
					if text, ok := partMap["text"].(string); ok {
						sb.WriteString(text)
					}
				}
			}
			responseText = sb.String()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TestModelResponse{
		Success:  true,
		Response: responseText,
		Model:    resp.Model,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Token Management Handlers
// ─────────────────────────────────────────────────────────────────────────────

// TokenResponse represents a token in API responses (without sensitive data)
type TokenResponse struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	ExpiresAt            *string   `json:"expires_at,omitempty"`
	CreatedAt            string    `json:"created_at"`
	CreatedBy            string    `json:"created_by"`
	UltimateModelEnabled bool      `json:"ultimate_model_enabled"`
	UltimateModel        string    `json:"ultimate_model"`           // Empty = use global config
	AllowedModels        *[]string `json:"allowed_models,omitempty"` // nil/empty = all models allowed
}

// CreateTokenRequest represents the request body for creating a token
type CreateTokenRequest struct {
	Name                 string    `json:"name"`
	ExpiresAt            *string   `json:"expires_at,omitempty"` // ISO 8601 format, optional
	UltimateModelEnabled bool      `json:"ultimate_model_enabled"`
	UltimateModel        *string   `json:"ultimate_model,omitempty"` // nil/empty = use global config
	AllowedModels        *[]string `json:"allowed_models,omitempty"` // nil/empty = all models allowed
}

// CreateTokenResponse includes the plaintext token (shown only once)
type CreateTokenResponse struct {
	TokenResponse
	Token string `json:"token"` // Plaintext token - show only once!
}

// handleTokens handles GET (list) and POST (create) for tokens
func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		s.listTokens(ctx, w, r)
	case http.MethodPost:
		s.createToken(ctx, w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleTokenDetail handles DELETE and PATCH for a specific token
func (s *Server) handleTokenDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := strings.TrimPrefix(r.URL.Path, "/fe/api/tokens/")
	if id == "" {
		http.Error(w, "Token ID required", http.StatusBadRequest)
		return
	}

	if s.tokenStore == nil {
		http.Error(w, "Token store not configured", http.StatusInternalServerError)
		return
	}

	switch r.Method {
	case http.MethodDelete:
		err := s.tokenStore.DeleteToken(ctx, id)
		if err != nil {
			if err == auth.ErrTokenNotFound {
				http.Error(w, "Token not found", http.StatusNotFound)
				return
			}
			http.Error(w, fmt.Sprintf("Failed to delete token: %v", err), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case http.MethodPatch:
		s.updateTokenPermission(ctx, w, r, id)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// UpdateTokenPermissionRequest represents the request body for updating token permission
type UpdateTokenPermissionRequest struct {
	UltimateModelEnabled *bool     `json:"ultimate_model_enabled"`   // pointer for distinguishing unset vs false
	UltimateModel        *string   `json:"ultimate_model,omitempty"` // nil = keep existing; "" = use global; "value" = override
	AllowedModels        *[]string `json:"allowed_models,omitempty"` // nil = keep existing; empty array = all models allowed
}

// updateTokenPermission handles PATCH /fe/api/tokens/{id}
func (s *Server) updateTokenPermission(ctx context.Context, w http.ResponseWriter, r *http.Request, id string) {
	// Limit request body to 64KB to prevent memory exhaustion attacks
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

	var req UpdateTokenPermissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	// Get current token first to handle partial updates
	currentToken, err := s.tokenStore.GetTokenByID(ctx, id)
	if err != nil {
		if err == auth.ErrTokenNotFound {
			http.Error(w, "Token not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to get token: %v", err), http.StatusInternalServerError)
		return
	}

	// Determine ultimateModelEnabled to set:
	// - If UltimateModelEnabled is nil in request, keep existing value
	// - If UltimateModelEnabled is non-nil, use the provided value
	var ultimateModelEnabledToSet bool
	if req.UltimateModelEnabled != nil {
		ultimateModelEnabledToSet = *req.UltimateModelEnabled
	} else {
		ultimateModelEnabledToSet = currentToken.UltimateModelEnabled
	}

	// Determine ultimateModelID to set (three-state):
	// - If UltimateModel is nil in request, keep existing value
	// - If UltimateModel is non-nil (including empty string), use the provided value
	var ultimateModelIDToSet string
	if req.UltimateModel != nil {
		ultimateModelIDToSet = *req.UltimateModel
	} else {
		ultimateModelIDToSet = currentToken.UltimateModelID
	}

	// Determine allowed_models to set:
	// - If AllowedModels is nil in request, keep existing value
	// - If AllowedModels is non-nil (including empty array), use the provided value
	var allowedModelsToSet []string
	if req.AllowedModels != nil {
		allowedModelsToSet = *req.AllowedModels
	} else {
		allowedModelsToSet = currentToken.AllowedModels
	}

	err = s.tokenStore.UpdateTokenPermission(ctx, id, ultimateModelEnabledToSet, ultimateModelIDToSet, allowedModelsToSet)
	if err != nil {
		if err == auth.ErrTokenNotFound {
			http.Error(w, "Token not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("Failed to update token permission: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (s *Server) listTokens(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if s.tokenStore == nil {
		// Return empty list if token store not configured
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]TokenResponse{})
		return
	}

	tokens, err := s.tokenStore.ListTokens(ctx)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to list tokens: %v", err), http.StatusInternalServerError)
		return
	}

	response := make([]TokenResponse, len(tokens))
	for i, t := range tokens {
		response[i] = TokenResponse{
			ID:                   t.ID,
			Name:                 t.Name,
			CreatedAt:            t.CreatedAt.Format(time.RFC3339),
			CreatedBy:            t.CreatedBy,
			UltimateModelEnabled: t.UltimateModelEnabled,
			UltimateModel:        t.UltimateModelID, // Empty = use global config
			AllowedModels:        &t.AllowedModels,
		}
		if t.ExpiresAt != nil {
			iso := t.ExpiresAt.Format(time.RFC3339)
			response[i].ExpiresAt = &iso
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) createToken(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if s.tokenStore == nil {
		http.Error(w, "Token store not configured", http.StatusInternalServerError)
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	// Parse optional expiry
	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			http.Error(w, "Invalid expires_at format (use ISO 8601)", http.StatusBadRequest)
			return
		}
		expiresAt = &t
	}

	// Get creator from request (could be from OAuth header in production)
	createdBy := "api"
	if user := r.Header.Get("X-User"); user != "" {
		createdBy = user
	}

	// Get allowed models (nil/empty = all models allowed)
	var allowedModels []string
	if req.AllowedModels != nil {
		allowedModels = *req.AllowedModels
	}

	// Get ultimate model ID (nil/empty = use global config)
	var ultimateModelID string
	if req.UltimateModel != nil {
		ultimateModelID = *req.UltimateModel
	}

	plaintext, token, err := s.tokenStore.CreateToken(ctx, req.Name, expiresAt, createdBy, req.UltimateModelEnabled, ultimateModelID, allowedModels)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create token: %v", err), http.StatusInternalServerError)
		return
	}

	response := CreateTokenResponse{
		TokenResponse: TokenResponse{
			ID:                   token.ID,
			Name:                 token.Name,
			CreatedAt:            token.CreatedAt.Format(time.RFC3339),
			CreatedBy:            token.CreatedBy,
			UltimateModelEnabled: token.UltimateModelEnabled,
			UltimateModel:        token.UltimateModelID, // Empty = use global config
			AllowedModels:        &token.AllowedModels,
		},
		Token: plaintext, // Show once!
	}
	if token.ExpiresAt != nil {
		iso := token.ExpiresAt.Format(time.RFC3339)
		response.TokenResponse.ExpiresAt = &iso
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)
}

// maskAPIKey masks the API key for display, showing first 8 chars + "***"
// if the key is longer than 8 characters, otherwise just "***"
func maskAPIKey(apiKey string) string {
	if len(apiKey) > 8 {
		return apiKey[:8] + "***"
	}
	return "***"
}

// handleCredentials handles GET and POST /fe/api/credentials
func (s *Server) handleCredentials(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		credConfigs := s.modelsConfig.GetCredentials()
		credentials := make([]Credential, len(credConfigs))
		for i, cc := range credConfigs {
			credentials[i] = Credential{
				ID:       cc.ID,
				Provider: cc.Provider,
				APIKey:   maskAPIKey(cc.APIKey),
				BaseURL:  cc.BaseURL,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(credentials)

	case http.MethodPost:
		// Limit request body to 64KB to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var newCred Credential
		if err := json.NewDecoder(r.Body).Decode(&newCred); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Validate credential
		if newCred.ID == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "credential id is required"})
			return
		}
		if newCred.Provider == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "provider is required"})
			return
		}
		if newCred.APIKey == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "api_key is required"})
			return
		}

		// Convert to models.CredentialConfig
		credConfig := models.CredentialConfig{
			ID:       newCred.ID,
			Provider: newCred.Provider,
			APIKey:   newCred.APIKey,
			BaseURL:  newCred.BaseURL,
		}

		if err := s.modelsConfig.AddCredential(credConfig); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Persist changes to disk
		if err := s.modelsConfig.Save(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Return with masked API key
		response := Credential{
			ID:       newCred.ID,
			Provider: newCred.Provider,
			APIKey:   maskAPIKey(newCred.APIKey),
			BaseURL:  newCred.BaseURL,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(response)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleCredentialDetail handles GET, PUT, and DELETE /fe/api/credentials/{id}
func (s *Server) handleCredentialDetail(w http.ResponseWriter, r *http.Request) {
	// /fe/api/credentials/{id}
	id := r.URL.Path[len("/fe/api/credentials/"):]
	if id == "" {
		http.Error(w, "Missing ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		cred := s.modelsConfig.GetCredential(id)
		if cred == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "credential not found"})
			return
		}

		credential := Credential{
			ID:       cred.ID,
			Provider: cred.Provider,
			APIKey:   maskAPIKey(cred.APIKey),
			BaseURL:  cred.BaseURL,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(credential)

	case http.MethodPut:
		// Limit request body to 64KB to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)

		var updatedCred Credential
		if err := json.NewDecoder(r.Body).Decode(&updatedCred); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Validate credential
		if updatedCred.Provider == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "provider is required"})
			return
		}

		// Keep the same ID
		updatedCred.ID = id

		// If api_key is empty, preserve the existing one
		if updatedCred.APIKey == "" {
			existingCred := s.modelsConfig.GetCredential(id)
			if existingCred == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "credential not found"})
				return
			}
			updatedCred.APIKey = existingCred.APIKey
		}

		// Convert to models.CredentialConfig
		credConfig := models.CredentialConfig{
			ID:       updatedCred.ID,
			Provider: updatedCred.Provider,
			APIKey:   updatedCred.APIKey,
			BaseURL:  updatedCred.BaseURL,
		}

		if err := s.modelsConfig.UpdateCredential(id, credConfig); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Persist changes to disk
		if err := s.modelsConfig.Save(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Return with masked API key
		response := Credential{
			ID:       updatedCred.ID,
			Provider: updatedCred.Provider,
			APIKey:   maskAPIKey(updatedCred.APIKey),
			BaseURL:  updatedCred.BaseURL,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)

	case http.MethodDelete:
		if err := s.modelsConfig.RemoveCredential(id); err != nil {
			w.Header().Set("Content-Type", "application/json")
			// Check if error is due to credential being in use
			if strings.Contains(err.Error(), "in use") {
				w.WriteHeader(http.StatusConflict)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Persist changes to disk
		if err := s.modelsConfig.Save(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
