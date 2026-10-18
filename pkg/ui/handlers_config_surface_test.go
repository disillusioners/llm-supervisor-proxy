package ui

// remove-external-upstream P5 / D4 — config-surface pin.
//
// P1 removed the external-upstream surface from config.Config
// (UpstreamURL / UpstreamCredentialID fields and their env branches
// are gone). THIS test pins the retirement at the FE-API boundary:
// GET /fe/api/config must expose NO upstream_url and NO
// upstream_credential_id key — neither as a JSON key nor as a raw
// substring anywhere in the response body. The FE UI side (ProxySettings
// renders no Upstream URL input) is already covered by P3's vitest
// suite (phase3-frontend.md), so the API + UI halves together close
// behavioral test #5 from phase5-tests.md.
//
// Wiring uses the production config backend: a DB-backed
// database.ConfigManager on a temp SQLite store (cmd/main.go binds
// ConfigManager exclusively when DATABASE_URL is set), driven through
// the real handleConfig handler.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

func TestHandleConfig_GET_NoUpstreamSurface(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("DATABASE_URL", "sqlite:"+filepath.Join(tmpDir, "test.db"))
	dbStore, err := database.NewConnection(context.Background())
	if err != nil {
		t.Fatalf("create SQLite: %v", err)
	}
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		dbStore.Close()
		t.Fatalf("migrations: %v", err)
	}
	cm, err := database.NewConfigManager(dbStore, nil)
	if err != nil {
		dbStore.Close()
		t.Fatalf("NewConfigManager: %v", err)
	}

	s := &Server{configMgr: cm}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/config", s.handleConfig)
	srv := httptest.NewServer(mux)
	defer func() {
		srv.Close()
		dbStore.Close()
	}()

	resp, err := http.Get(srv.URL + "/fe/api/config")
	if err != nil {
		t.Fatalf("GET /fe/api/config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var cfg map[string]interface{}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("decode config JSON: %v (body: %s)", err, string(body))
	}

	// Sanity: the response is a real config object, not an empty map —
	// otherwise the absence assertions below would pass vacuously.
	if _, ok := cfg["port"]; !ok {
		t.Fatalf("config response carries no \"port\" key — unexpected shape (body: %s)", string(body))
	}

	for _, key := range []string{"upstream_url", "upstream_credential_id"} {
		if _, present := cfg[key]; present {
			t.Errorf("GET /fe/api/config exposes retired key %q (remove-external-upstream regression)", key)
		}
		if strings.Contains(string(body), key) {
			t.Errorf("GET /fe/api/config raw body contains retired substring %q (remove-external-upstream regression)", key)
		}
	}
}
