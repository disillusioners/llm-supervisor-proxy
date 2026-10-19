package ui

// Root-cause probe for the pre-existing UpdateModel PUT bug — the
// mission said "the UpdateModel PUT handler has a pre-existing bug
// that breaks FE model EDITING (PUT updates) for ALL model kinds —
// chat, image-gen, AND decision — equally. It is a baseline bug
// (NOT introduced by decision-model-type)."
//
// This test exercises the REAL handler chain (handleModelDetail)
// against BOTH the JSON-file and DB-backed implementations of the
// models.ModelsConfigInterface. The mission did not know which
// implementation runs in production (the dispatcher pre-check
// only listed pkg/ui/server.go, pkg/store/database/store.go, and
// pkg/store/database/querybuilder.go — pkg/models/config.go is
// ALSO a ModelsConfigInterface implementation, and a real PUT
// flows through it whenever the JSON-file backend is wired in).
//
// The table iterates chat / image-gen / decision over BOTH
// backends. For each row: seed a valid model → PUT a
// meaningfully-different version → GET and verify the edit landed
// (every meaningful field, especially the kind).
//
// If the PUT path is broken, every row fails on the same symptom
// (the field round-trip OR the kind persistence). That is the
// "RIGHT reason" the mission asked for — not an incidental error.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// realPUTTestServer wires the real handleModelDetail against a real
// DB-backed ModelsManager (no mock, no cache). Seed credentials so
// the PUT path's kind-rules + multi-credential validation accept the
// payload.
type realPUTTestServer struct {
	server  *httptest.Server
	mgr     *database.ModelsManager
	cleanup func()
}

func newRealPUTTestServer(t *testing.T) *realPUTTestServer {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	t.Setenv("DATABASE_URL", "sqlite:"+dbPath)
	store, err := database.NewConnection(context.Background())
	if err != nil {
		t.Fatalf("create SQLite: %v", err)
	}
	if err := store.RunMigrations(context.Background()); err != nil {
		store.Close()
		t.Fatalf("migrations: %v", err)
	}
	mgr, err := database.NewModelsManager(store, nil)
	if err != nil {
		store.Close()
		t.Fatalf("NewModelsManager: %v", err)
	}

	// Seed credentials used by the three kinds.
	for _, c := range []models.CredentialConfig{
		{ID: "openai-cred", Provider: "openai", APIKey: "k"},
		{ID: "minimax-cred", Provider: "minimax", APIKey: "k"},
		{ID: "typesafe-cred", Provider: "typesafe", APIKey: "k"},
	} {
		if err := mgr.AddCredential(c); err != nil {
			store.Close()
			t.Fatalf("AddCredential(%s): %v", c.ID, err)
		}
	}

	s := &Server{modelsConfig: mgr}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/models", s.handleModels)
	mux.HandleFunc("/fe/api/models/", s.handleModelDetail)
	mux.HandleFunc("/fe/api/models/validate", s.handleValidateModel)
	srv := httptest.NewServer(mux)

	cleanup := func() {
		srv.Close()
		store.Close()
	}
	return &realPUTTestServer{server: srv, mgr: mgr, cleanup: cleanup}
}

// doPUT POSTs/PUTs a JSON body to the server.
func doPUT(t *testing.T, srv *httptest.Server, id string, body map[string]interface{}) (int, []byte) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/fe/api/models/"+id, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.Bytes()
}

// doGet returns the model from the LIST endpoint (no single-row GET
// exists in handleModelDetail — it only handles PUT/DELETE). Returns
// the entry for `id`, or nil if absent.
func doGet(t *testing.T, srv *httptest.Server, id string) (int, map[string]interface{}) {
	t.Helper()
	resp, err := http.Get(srv.URL + "/fe/api/models")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var list []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, m := range list {
		if m["id"] == id {
			return resp.StatusCode, m
		}
	}
	return resp.StatusCode, nil
}

// TestHandleModelDetail_PUT_AllKinds_EditsPersist is the
// root-cause probe. It PUTs a meaningful edit for each of the three
// kinds and asserts the GET round-trip reflects the edit. The
// mission is explicit: if the bug is real, this test fails on the
// unfixed code for the right reason (the field round-trip is
// broken), not incidentally.
func TestHandleModelDetail_PUT_AllKinds_EditsPersist(t *testing.T) {
	ts := newRealPUTTestServer(t)
	defer ts.cleanup()

	tests := []struct {
		name string
		// seed returns the model to seed via direct mgr.AddModel.
		seed func() models.ModelConfig
		// id is the URL id (same as seed().ID).
		id string
		// kind is what the seed's Kind should be — drives the PUT body's
		// kind field (the FE includes kind on image-gen / decision).
		kind string
		// edit returns the PUT body. Must differ in at least one
		// meaningful field from the seed.
		edit func() map[string]interface{}
		// assertions receives the GET response and fails on drift.
		assertions func(t *testing.T, got map[string]interface{})
	}{
		{
			name: "chat",
			kind: models.KindChat,
			seed: func() models.ModelConfig {
				return models.ModelConfig{
					ID:            "chat-edit",
					Name:          "Chat Original",
					Enabled:       true,
					Internal:      true,
					InternalModel: "gpt-4o",
					Credentials:   models.TestRefs("openai-cred"),
				}
			},
			id: "chat-edit",
			edit: func() map[string]interface{} {
				return map[string]interface{}{
					"id":                           "chat-edit",
					"name":                         "Chat Edited",
					"enabled":                      true,
					"kind":                         models.KindChat,
					"internal":                     true,
					"internal_model":               "gpt-4o-mini",
					"secondary_upstream_model":     "gpt-4o-fallback",
					"exclude_from_ultimate_switching": false,
					"credentials": []map[string]interface{}{
						{"credential_id": "openai-cred", "weight": 1, "position": 0},
					},
				}
			},
			assertions: func(t *testing.T, got map[string]interface{}) {
				if got["name"] != "Chat Edited" {
					t.Errorf("name = %v, want %q", got["name"], "Chat Edited")
				}
				if got["internal_model"] != "gpt-4o-mini" {
					t.Errorf("internal_model = %v, want %q", got["internal_model"], "gpt-4o-mini")
				}
				if got["secondary_upstream_model"] != "gpt-4o-fallback" {
					t.Errorf("secondary_upstream_model = %v, want %q", got["secondary_upstream_model"], "gpt-4o-fallback")
				}
			},
		},
		{
			name: "image-gen",
			kind: models.KindImageGen,
			seed: func() models.ModelConfig {
				return models.ModelConfig{
					ID:            "imggen-edit",
					Name:          "ImgGen Original",
					Enabled:       true,
					Kind:          models.KindImageGen,
					Internal:      true,
					InternalModel: "image-01",
					Credentials:   models.TestRefs("minimax-cred"),
				}
			},
			id: "imggen-edit",
			edit: func() map[string]interface{} {
				return map[string]interface{}{
					"id":             "imggen-edit",
					"name":           "ImgGen Edited",
					"enabled":        true,
					"kind":           models.KindImageGen,
					"internal":       true,
					"internal_model": "image-02",
					"credentials": []map[string]interface{}{
						{"credential_id": "minimax-cred", "weight": 1, "position": 0},
					},
					"exclude_from_ultimate_switching": true,
				}
			},
			assertions: func(t *testing.T, got map[string]interface{}) {
				if got["name"] != "ImgGen Edited" {
					t.Errorf("name = %v, want %q", got["name"], "ImgGen Edited")
				}
				if got["internal_model"] != "image-02" {
					t.Errorf("internal_model = %v, want %q", got["internal_model"], "image-02")
				}
				if got["kind"] != models.KindImageGen {
					t.Errorf("kind = %v, want %q (PUT must not demote image-gen to chat)", got["kind"], models.KindImageGen)
				}
			},
		},
		{
			name: "decision",
			kind: models.KindDecision,
			seed: func() models.ModelConfig {
				return models.ModelConfig{
					ID:                           "decision-edit",
					Name:                         "Decision Original",
					Enabled:                      true,
					Kind:                         models.KindDecision,
					Internal:                     true,
					InternalModel:                "gpt-6-luna",
					Credentials:                  models.TestRefs("openai-cred"),
					ExcludeFromUltimateSwitching: true,
				}
			},
			id: "decision-edit",
			edit: func() map[string]interface{} {
				return map[string]interface{}{
					"id":             "decision-edit",
					"name":           "Decision Edited",
					"enabled":        true,
					"kind":           models.KindDecision,
					"internal":       true,
					"internal_model": "gpt-6-vega",
					"credentials": []map[string]interface{}{
						{"credential_id": "openai-cred", "weight": 1, "position": 0},
					},
					"exclude_from_ultimate_switching": true,
				}
			},
			assertions: func(t *testing.T, got map[string]interface{}) {
				if got["name"] != "Decision Edited" {
					t.Errorf("name = %v, want %q", got["name"], "Decision Edited")
				}
				if got["internal_model"] != "gpt-6-vega" {
					t.Errorf("internal_model = %v, want %q", got["internal_model"], "gpt-6-vega")
				}
				if got["kind"] != models.KindDecision {
					t.Errorf("kind = %v, want %q (PUT must not demote decision to chat)", got["kind"], models.KindDecision)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Seed via direct DB write (bypasses the PUT path).
			seeded := tc.seed()
			if err := ts.mgr.AddModel(seeded); err != nil {
				t.Fatalf("seed AddModel: %v", err)
			}

			// PUT the edit.
			status, body := doPUT(t, ts.server, tc.id, tc.edit())
			if status != http.StatusOK {
				t.Fatalf("PUT status = %d (want 200), body=%s", status, string(body))
			}

			// GET and verify the edit landed.
			getStatus, got := doGet(t, ts.server, tc.id)
			if getStatus != http.StatusOK {
				t.Fatalf("GET status = %d (want 200)", getStatus)
			}
			if got == nil {
				t.Fatalf("GET body empty")
			}
			tc.assertions(t, got)
		})
	}
}

// TestHandleModelDetail_PUT_KindFieldMirrorsPOST guards that
// the PUT path accepts (and round-trips) the same kind values the
// POST path accepts. The mission said the bug is "for ALL model
// kinds — chat, image-gen, AND decision — equally", so a positive
// sanity check that the value lands proves the kind is not being
// silently dropped on the round-trip. If the path is broken, the
// GET returns Kind == "" (chat default) for every kind.
func TestHandleModelDetail_PUT_KindFieldMirrorsPOST(t *testing.T) {
	ts := newRealPUTTestServer(t)
	defer ts.cleanup()

	for _, kind := range []string{models.KindChat, models.KindImageGen, models.KindDecision} {
		t.Run(kind, func(t *testing.T) {
			id := "kind-" + kind
			creds := "openai-cred"
			if kind == models.KindImageGen {
				creds = "minimax-cred"
			}
			excludeFromUltimate := true
			if kind == models.KindChat {
				excludeFromUltimate = false
			}
			// Seed via direct DB write so the PUT path has an
			// existing row to update.
			if err := ts.mgr.AddModel(models.ModelConfig{
				ID:                           id,
				Name:                         "Kind Mirror " + kind,
				Enabled:                      true,
				Kind:                         kind,
				Internal:                     true,
				InternalModel:                "any",
				Credentials:                  models.TestRefs(creds),
				ExcludeFromUltimateSwitching: excludeFromUltimate,
			}); err != nil {
				t.Fatalf("seed AddModel: %v", err)
			}
			body := map[string]interface{}{
				"id":             id,
				"name":           "Kind Mirror " + kind,
				"enabled":        true,
				"kind":           kind,
				"internal":       true,
				"internal_model": "any",
				"credentials": []map[string]interface{}{
					{"credential_id": creds, "weight": 1, "position": 0},
				},
				"exclude_from_ultimate_switching": excludeFromUltimate,
			}
			status, respBody := doPUT(t, ts.server, id, body)
			if status != http.StatusOK {
				t.Fatalf("PUT status = %d (want 200), body=%s", status, string(respBody))
			}
			getStatus, got := doGet(t, ts.server, id)
			if getStatus != http.StatusOK {
				t.Fatalf("GET status = %d (want 200)", getStatus)
			}
			if got == nil {
				t.Fatalf("GET body did not contain id=%s (PUT deleted the row?)", id)
			}
			if got["kind"] != kind {
				t.Fatalf("kind round-trip = %v, want %q (PUT path dropped the kind)", got["kind"], kind)
			}
		})
	}
}

// jsonFileTestServer wires the REAL handleModelDetail against the
// JSON-file-backed *models.ModelsConfig (NOT the DB path). The
// mission's dispatcher pre-check only listed pkg/ui/server.go,
// pkg/store/database/store.go, and pkg/store/database/querybuilder.go
// as UpdateModel sites — but pkg/models/config.go is ALSO a
// ModelsConfigInterface implementation, and the production PUT
// handler routes through it whenever the JSON-file backend is
// wired (some operator setups). The two backends MUST stay
// behavior-equivalent (the interface contract demands it).
type jsonFileTestServer struct {
	server  *httptest.Server
	mc      *models.ModelsConfig
	cleanup func()
}

func newJSONFilePUTTestServer(t *testing.T) *jsonFileTestServer {
	t.Helper()

	// Save needs a real file path; use t.TempDir so cleanup is
	// automatic. ModelsConfig's filePath is unexported, but
	// Load(filePath) sets it AND populates Models + Credentials
	// from the JSON file (no-op on a fresh file).
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "models.json")
	mc := models.NewModelsConfig()
	if err := mc.Load(cfgPath); err != nil {
		t.Fatalf("Load(%s): %v", cfgPath, err)
	}
	if err := mc.AddCredential(models.CredentialConfig{ID: "openai-cred", Provider: "openai", APIKey: "k"}); err != nil {
		t.Fatalf("AddCredential(openai): %v", err)
	}
	if err := mc.AddCredential(models.CredentialConfig{ID: "minimax-cred", Provider: "minimax", APIKey: "k"}); err != nil {
		t.Fatalf("AddCredential(minimax): %v", err)
	}
	if err := mc.AddCredential(models.CredentialConfig{ID: "typesafe-cred", Provider: "typesafe", APIKey: "k"}); err != nil {
		t.Fatalf("AddCredential(typesafe): %v", err)
	}

	s := &Server{modelsConfig: mc}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/models", s.handleModels)
	mux.HandleFunc("/fe/api/models/", s.handleModelDetail)
	mux.HandleFunc("/fe/api/models/validate", s.handleValidateModel)
	srv := httptest.NewServer(mux)

	cleanup := func() { srv.Close() }
	return &jsonFileTestServer{server: srv, mc: mc, cleanup: cleanup}
}

// TestHandleModelDetail_PUT_JSONFileBackend_AllKinds_EditsPersist
// is the root-cause probe at the API layer. It wires the
// handleModelDetail to the JSON-file ModelsConfig (the OTHER
// implementation of ModelsConfigInterface) and exercises the same
// table — chat / image-gen / decision. Every row is a real PUT
// through the real handler chain; the JSON-file UpdateModel is
// the path under test.
//
// The pre-fix behavior: every internal model with credentials
// returns 400 "credential_id 'cred-X' references non-existent
// credential" because the post-update testConfig omits
// mc.Credentials — the bug the user reported, isolated to a
// concrete reproducer.
func TestHandleModelDetail_PUT_JSONFileBackend_AllKinds_EditsPersist(t *testing.T) {
	ts := newJSONFilePUTTestServer(t)
	defer ts.cleanup()

	tests := []struct {
		name string
		// seed returns the model to seed via the JSON-file path.
		seed func() models.ModelConfig
		id   string
		// edit returns the PUT body. Must include all the fields a
		// production form sends, including the kind discriminator
		// (image-gen / decision always send kind; chat forms omit
		// it but the JSON-file path treats "" as chat default).
		edit func() map[string]interface{}
		// assertions receives the GET response and fails on drift.
		assertions func(t *testing.T, got map[string]interface{})
	}{
		{
			name: "chat",
			seed: func() models.ModelConfig {
				return models.ModelConfig{
					ID:            "chat-edit",
					Name:          "Chat Original",
					Enabled:       true,
					Internal:      true,
					InternalModel: "gpt-4o",
					Credentials:   models.TestRefs("openai-cred"),
				}
			},
			id: "chat-edit",
			edit: func() map[string]interface{} {
				return map[string]interface{}{
					"id":                           "chat-edit",
					"name":                         "Chat Edited",
					"enabled":                      true,
					"internal":                     true,
					"internal_model":               "gpt-4o-mini",
					"secondary_upstream_model":     "gpt-4o-fallback",
					"exclude_from_ultimate_switching": false,
					"credentials": []map[string]interface{}{
						{"credential_id": "openai-cred", "weight": 1, "position": 0},
					},
				}
			},
			assertions: func(t *testing.T, got map[string]interface{}) {
				if got["name"] != "Chat Edited" {
					t.Errorf("name = %v, want %q", got["name"], "Chat Edited")
				}
				if got["internal_model"] != "gpt-4o-mini" {
					t.Errorf("internal_model = %v, want %q", got["internal_model"], "gpt-4o-mini")
				}
				if got["secondary_upstream_model"] != "gpt-4o-fallback" {
					t.Errorf("secondary_upstream_model = %v, want %q", got["secondary_upstream_model"], "gpt-4o-fallback")
				}
			},
		},
		{
			name: "image-gen",
			seed: func() models.ModelConfig {
				return models.ModelConfig{
					ID:            "imggen-edit",
					Name:          "ImgGen Original",
					Enabled:       true,
					Kind:          models.KindImageGen,
					Internal:      true,
					InternalModel: "image-01",
					Credentials:   models.TestRefs("minimax-cred"),
				}
			},
			id: "imggen-edit",
			edit: func() map[string]interface{} {
				return map[string]interface{}{
					"id":             "imggen-edit",
					"name":           "ImgGen Edited",
					"enabled":        true,
					"kind":           models.KindImageGen,
					"internal":       true,
					"internal_model": "image-02",
					"credentials": []map[string]interface{}{
						{"credential_id": "minimax-cred", "weight": 1, "position": 0},
					},
					"exclude_from_ultimate_switching": true,
				}
			},
			assertions: func(t *testing.T, got map[string]interface{}) {
				if got["name"] != "ImgGen Edited" {
					t.Errorf("name = %v, want %q", got["name"], "ImgGen Edited")
				}
				if got["internal_model"] != "image-02" {
					t.Errorf("internal_model = %v, want %q", got["internal_model"], "image-02")
				}
				if got["kind"] != models.KindImageGen {
					t.Errorf("kind = %v, want %q (PUT must not demote image-gen to chat)", got["kind"], models.KindImageGen)
				}
			},
		},
		{
			name: "decision",
			seed: func() models.ModelConfig {
				return models.ModelConfig{
					ID:                           "decision-edit",
					Name:                         "Decision Original",
					Enabled:                      true,
					Kind:                         models.KindDecision,
					Internal:                     true,
					InternalModel:                "gpt-6-luna",
					Credentials:                  models.TestRefs("openai-cred"),
					ExcludeFromUltimateSwitching: true,
				}
			},
			id: "decision-edit",
			edit: func() map[string]interface{} {
				return map[string]interface{}{
					"id":             "decision-edit",
					"name":           "Decision Edited",
					"enabled":        true,
					"kind":           models.KindDecision,
					"internal":       true,
					"internal_model": "gpt-6-vega",
					"credentials": []map[string]interface{}{
						{"credential_id": "openai-cred", "weight": 1, "position": 0},
					},
					"exclude_from_ultimate_switching": true,
				}
			},
			assertions: func(t *testing.T, got map[string]interface{}) {
				if got["name"] != "Decision Edited" {
					t.Errorf("name = %v, want %q", got["name"], "Decision Edited")
				}
				if got["internal_model"] != "gpt-6-vega" {
					t.Errorf("internal_model = %v, want %q", got["internal_model"], "gpt-6-vega")
				}
				if got["kind"] != models.KindDecision {
					t.Errorf("kind = %v, want %q (PUT must not demote decision to chat)", got["kind"], models.KindDecision)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Seed via the JSON-file path so the seeded model is
			// visible to the same UpdateModel under test.
			if err := ts.mc.AddModel(tc.seed()); err != nil {
				t.Fatalf("seed AddModel: %v", err)
			}

			status, body := doPUT(t, ts.server, tc.id, tc.edit())
			if status != http.StatusOK {
				t.Fatalf("PUT status = %d (want 200), body=%s", status, string(body))
			}

			getStatus, got := doGet(t, ts.server, tc.id)
			if getStatus != http.StatusOK {
				t.Fatalf("GET status = %d (want 200)", getStatus)
			}
			if got == nil {
				t.Fatalf("GET body did not contain id=%s (PUT deleted the row?)", tc.id)
			}
			tc.assertions(t, got)
		})
	}
}

// doDELETE sends DELETE /fe/api/models/{id} and returns the
// status + body. handleModelDetail's DELETE branch returns
// 204 NoContent on success and 404 on "model not found" /
// RemoveModel error. The body is empty on success.
func doDELETE(t *testing.T, srv *httptest.Server, id string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/fe/api/models/"+id, nil)
	if err != nil {
		t.Fatalf("build DELETE: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.Bytes()
}

// TestHandleModelDetail_DELETE_JSONFileBackend_RemainingModel
// mirrors the UpdateModel probe at the API layer. It seeds TWO
// models via the JSON-file ModelsConfig — one internal with a
// credential ref (the row that should REMAIN after the DELETE),
// and one trivial model (the row to be DELETED). The handler
// DELETE /fe/api/models/{id} calls RemoveModel, which builds a
// post-removal testConfig WITHOUT copying mc.Credentials
// (the same missing-credentials pattern as the UpdateModel fix
// at pkg/models/config.go:724-728). The spurious Validate guard
// then fires on the REMAINING model's credential ref.
//
// Pre-fix behavior: the handler returns 404 with the exact
// `credential_id 'X' references non-existent credential`
// message, even though the model EXISTS and the deletion is
// semantically valid. After the mirror fix, the DELETE returns
// 204 NoContent and the remaining model survives intact.
func TestHandleModelDetail_DELETE_JSONFileBackend_RemainingModel(t *testing.T) {
	ts := newJSONFilePUTTestServer(t)
	defer ts.cleanup()

	// Seed the model that should REMAIN after the DELETE — it
	// carries the credential ref that the post-removal testConfig
	// Validate() scans.
	if err := ts.mc.AddModel(models.ModelConfig{
		ID:            "survivor",
		Name:          "Survivor",
		Enabled:       true,
		Internal:      true,
		InternalModel: "gpt-4o",
		Credentials:   models.TestRefs("openai-cred"),
	}); err != nil {
		t.Fatalf("seed survivor: %v", err)
	}
	// Seed the model to be DELETED. remove-external-upstream P1:
	// the store-level internal=true stamp applies to every seeded row,
	// so this row now carries the same credential ref as the survivor
	// (a credential-less seed fails the internal-model credential
	// validation before the DELETE ever runs). The row's purpose is
	// unchanged: exist long enough to be DELETEd.
	if err := ts.mc.AddModel(models.ModelConfig{
		ID:            "to-delete",
		Name:          "To Delete",
		Enabled:       true,
		Internal:      true,
		InternalModel: "gpt-4o",
		Credentials:   models.TestRefs("openai-cred"),
	}); err != nil {
		t.Fatalf("seed to-delete: %v", err)
	}

	// Sanity: both rows are present pre-DELETE.
	if ts.mc.GetModel("survivor") == nil {
		t.Fatal("seed precondition broken: survivor missing before DELETE")
	}
	if ts.mc.GetModel("to-delete") == nil {
		t.Fatal("seed precondition broken: to-delete missing before DELETE")
	}

	status, body := doDELETE(t, ts.server, "to-delete")
	if status != http.StatusNoContent {
		t.Fatalf("DELETE status = %d (want 204), body=%s", status, string(body))
	}

	// The survivor must STILL be present — the DELETE only
	// targets the to-delete id.
	if got := ts.mc.GetModel("survivor"); got == nil {
		t.Fatal("survivor was incorrectly removed by DELETE of a different id")
	}
	if got := ts.mc.GetModel("to-delete"); got != nil {
		t.Errorf("to-delete still present after DELETE: %+v", got)
	}
}

// TestHandleModelDetail_DELETE_DBBackend_ImmuneProbe is the
// companion probe: the DB-backed ModelsManager is wired
// through the real handler chain and the same DELETE scenario
// runs. The DB path validates against m.GetCredentials() at
// call time, not against a copied snapshot — so it is IMMUNE
// to the missing-Credentials pattern that breaks the JSON-file
// backend. This test must pass at RED state too (i.e. BEFORE
// the JSON-file fix lands); that is the contract proof that the
// DB path never needed the same fix.
func TestHandleModelDetail_DELETE_DBBackend_ImmuneProbe(t *testing.T) {
	ts := newRealPUTTestServer(t)
	defer ts.cleanup()

	// Seed the survivor (carries the credential ref) and the
	// to-delete row — which post-P1 also carries a credential ref
	// (the store-level internal=true stamp makes credential-less
	// seeds fail validation; see the JSON-file twin above).
	if err := ts.mgr.AddModel(models.ModelConfig{
		ID:            "survivor-db",
		Name:          "Survivor",
		Enabled:       true,
		Internal:      true,
		InternalModel: "gpt-4o",
		Credentials:   models.TestRefs("openai-cred"),
	}); err != nil {
		t.Fatalf("seed survivor-db: %v", err)
	}
	if err := ts.mgr.AddModel(models.ModelConfig{
		ID:            "to-delete-db",
		Name:          "To Delete",
		Enabled:       true,
		Internal:      true,
		InternalModel: "gpt-4o",
		Credentials:   models.TestRefs("openai-cred"),
	}); err != nil {
		t.Fatalf("seed to-delete-db: %v", err)
	}

	status, body := doDELETE(t, ts.server, "to-delete-db")
	if status != http.StatusNoContent {
		t.Fatalf("DELETE status = %d (want 204), body=%s", status, string(body))
	}
	if ts.mgr.GetModel("survivor-db") == nil {
		t.Fatal("survivor-db was incorrectly removed by DELETE of a different id")
	}
	if ts.mgr.GetModel("to-delete-db") != nil {
		t.Error("to-delete-db still present after DELETE")
	}
}

// TestHandleModelDetail_PUT_JSONFileBackend_FailedValidationLeavesStateIntact
// is the Item-2 RED probe. The pre-fix code mutates mc.Models[i]
// BEFORE calling Validate, so a failed-validation PUT leaves
// the target's in-memory state drifted to the new (PUT) values
// — even though the handler returns 400 to the FE. The fix
// reorders: validate against a snapshot of the proposed state
// FIRST, mutate only on success.
//
// Setup seeds two models: a valid TARGET (the PUT target) and a
// SIBLING with a pre-existing Validate() violation (a credential
// ref that doesn't exist in the credentials map). The PUT body
// itself is clean — the handler's per-PUT inline validation
// accepts it. The failure lands in UpdateModel's post-mutation
// testConfig.Validate() scan over the whole Models slice, where
// the sibling's bad ref is detected.
//
// RED proof: after the failed PUT, the target's in-memory state
// must still hold the ORIGINAL values (not the PUT values).
// Pre-fix: the in-memory state is drifted to the PUT values
// (the bug). Post-fix: the in-memory state is the original
// (the validate-before-mutate contract).
func TestHandleModelDetail_PUT_JSONFileBackend_FailedValidationLeavesStateIntact(t *testing.T) {
	ts := newJSONFilePUTTestServer(t)
	defer ts.cleanup()

	// Seed the TARGET model via AddModel so the seed passes
	// Validate (a clean chat internal model).
	if err := ts.mc.AddModel(models.ModelConfig{
		ID:            "target",
		Name:          "Target Original",
		Enabled:       true,
		Internal:      true,
		InternalModel: "gpt-4o",
		Credentials:   models.TestRefs("openai-cred"),
	}); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	// Seed a SIBLING model with a pre-existing Validate()
	// violation: an internal model whose primary credential's
	// ref does not exist in the credentials map. The
	// handleModelDetail inline validation only inspects the
	// PUT body, not the sibling — so the handler accepts a
	// clean PUT payload targeting `target`. The post-mutation
	// testConfig.Validate() scan over the full Models slice
	// finds the sibling's bad ref and rejects.
	//
	// Bypass AddModel (which would Validate) and inject the
	// sibling directly into the exported Models slice. The
	// test is single-threaded (httptest PUTs block until the
	// handler returns; the read-back is a sequential
	// GetModel call), so no lock is needed.
	ts.mc.Models = append(ts.mc.Models, models.ModelConfig{
		ID:            "broken-sibling",
		Name:          "Broken Sibling",
		Enabled:       true,
		Internal:      true,
		InternalModel: "gpt-4o",
		// Refers to a credential that does not exist in the
		// map — validateModelAgainstCredentials' per-ref
		// existence check fires on this row in testConfig.
		// Validate().
		Credentials: models.TestRefs("does-not-exist"),
	})

	// Pre-PUT sanity: target is in its known-valid state.
	pre := ts.mc.GetModel("target")
	if pre == nil {
		t.Fatal("seed precondition broken: target missing before PUT")
	}
	if pre.Name != "Target Original" {
		t.Fatalf("seed precondition: Name = %q, want %q", pre.Name, "Target Original")
	}
	if pre.InternalModel != "gpt-4o" {
		t.Fatalf("seed precondition: InternalModel = %q, want %q", pre.InternalModel, "gpt-4o")
	}

	// PUT a CLEAN payload targeting `target`. The handler's
	// inline validation accepts it (the PUT body's own
	// credentials are valid, the kind is chat, the ID matches
	// the URL). UpdateModel is then called, which (pre-fix)
	// mutates mc.Models[i] BEFORE Validate. The Validate scan
	// finds the broken-sibling row and rejects — but the
	// mutation has already happened.
	putBody := map[string]interface{}{
		"id":             "target",
		"name":           "Target After PUT",
		"enabled":        true,
		"internal":       true,
		"internal_model": "gpt-4o-mini",
		"credentials": []map[string]interface{}{
			{"credential_id": "openai-cred", "weight": 1, "position": 0},
		},
		"exclude_from_ultimate_switching": false,
	}
	status, body := doPUT(t, ts.server, "target", putBody)
	// Both pre-fix and post-fix return 400 here: the sibling
	// is genuinely invalid and the testConfig scan must
	// reject any PUT while the broken-sibling is in the
	// config. The difference is in the in-memory state, NOT
	// the HTTP status.
	if status != http.StatusBadRequest {
		t.Fatalf("PUT status = %d (want 400 — the broken-sibling must cause the testConfig.Validate() scan to reject), body=%s", status, string(body))
	}
	// Sanity: the rejection reason names the sibling, not
	// the target (the PUT body itself is clean).
	if !strings.Contains(string(body), "broken-sibling") {
		t.Fatalf("PUT body did not name the broken-sibling as the rejection cause (the inline handler validation may have rejected the body before UpdateModel was reached), body=%s", string(body))
	}

	// In-memory state of the target after the failed PUT:
	// must be UNCHANGED from the pre-PUT snapshot. This is
	// the Item-2 contract: failed validation = no mutation.
	// Pre-fix: the mutation runs before Validate, so the
	// in-memory state is drifted to the PUT values (the bug).
	// Post-fix: Validate runs before the mutation, so the
	// in-memory state holds the original values (the fix).
	post := ts.mc.GetModel("target")
	if post == nil {
		t.Fatal("target disappeared after failed PUT")
	}
	if post.Name != "Target Original" {
		t.Errorf("in-memory drift on failed-validation PUT: Name = %q, want %q (UpdateModel must not mutate mc.Models[i] before Validate passes)", post.Name, "Target Original")
	}
	if post.InternalModel != "gpt-4o" {
		t.Errorf("in-memory drift on failed-validation PUT: InternalModel = %q, want %q (UpdateModel must not mutate mc.Models[i] before Validate passes)", post.InternalModel, "gpt-4o")
	}
}
