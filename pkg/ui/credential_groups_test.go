package ui

// credential-groups / T-27, T-28, T-28b, T-28c — the /fe/api
// credential-group surface, the UI-side validation mirror and the
// model-test expansion.
//
// The suite runs against the REAL DB-backed ModelsManager (SQLite, temp
// file) — the authoritative flattening seam and the authoritative group
// CRUD — so the tests exercise the same code the production binary
// binds (the store, not a mock).
//
// What is pinned here:
//
//   - the CRUD round-trip (POST 201 / GET / PUT 200 / DELETE 204)
//   - the status mapping (400 / 404 / 409 / 405 / 500)
//   - the bare-JSON {"error": ...} envelope at EVERY error site
//   - used_by on BOTH the list and the detail, in ONE batched pass
//   - the fixed lowercase wire keys (id/name/provider/members/used_by)
//   - the UI-side validation mirror accepting a GROUP ref on all four
//     model write/validate paths (create / edit / copy / validate)
//   - handleTestModel expanding a GROUP id to its first member
//   - the 9router-primary guard firing for a group-primary model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelscache"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// groupTestEnv is the UI test harness: a real ModelsManager over a
// temp SQLite database, wired into a Server whose mux carries the
// credential-group routes, the model routes and the model-test route.
type groupTestEnv struct {
	server *httptest.Server
	mgr    *database.ModelsManager
	store  *database.Store
	// modelsConfig is the config bound into the Server — the tests
	// swap it for the failing-save variant where needed.
	modelsConfig models.ModelsConfigInterface
}

func newGroupTestEnv(t *testing.T) *groupTestEnv {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv("DATABASE_URL", "sqlite:"+filepath.Join(tmpDir, "test.db"))
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

	s := &Server{modelsConfig: mgr}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/credential-groups", s.handleCredentialGroups)
	mux.HandleFunc("/fe/api/credential-groups/", s.handleCredentialGroupDetail)
	mux.HandleFunc("/fe/api/models", s.handleModels)
	mux.HandleFunc("/fe/api/models/", s.handleModelDetail)
	mux.HandleFunc("/fe/api/models/validate", s.handleValidateModel)
	mux.HandleFunc("/fe/api/models/test", s.handleTestModel)
	srv := httptest.NewServer(mux)

	t.Cleanup(func() {
		srv.Close()
		store.Close()
	})
	return &groupTestEnv{server: srv, mgr: mgr, store: store, modelsConfig: mgr}
}

// doJSON issues a JSON request against the harness and returns the
// status, the Content-Type and the raw body.
func (e *groupTestEnv) doJSON(t *testing.T, method, path string, body interface{}) (int, string, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), raw
}

// seedCredential adds a credential to the manager.
func (e *groupTestEnv) seedCredential(t *testing.T, id, provider, baseURL string) {
	t.Helper()
	if err := e.mgr.AddCredential(models.CredentialConfig{
		ID:       id,
		Provider: provider,
		APIKey:   "key-" + id,
		BaseURL:  baseURL,
	}); err != nil {
		t.Fatalf("AddCredential(%s): %v", id, err)
	}
}

// assertBareJSONError asserts the A-9 envelope: application/json +
// a body that parses as a JSON object carrying a non-empty "error".
func assertBareJSONError(t *testing.T, status int, contentType string, body []byte, wantStatus int) {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("status = %d, want %d (body=%s)", status, wantStatus, string(body))
	}
	if !strings.Contains(strings.ToLower(contentType), "application/json") {
		t.Fatalf("Content-Type = %q, want application/json (A-9: the frontend calls res.json())", contentType)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("error body is not JSON (%v) — the frontend would throw a SyntaxError: %s", err, string(body))
	}
	msg, ok := envelope["error"].(string)
	if !ok || msg == "" {
		t.Fatalf("error envelope has no \"error\" string: %s", string(body))
	}
}

// TestCredentialGroups_RoundTrip (T-28): POST 201 → GET list → GET
// detail → PUT 200 → DELETE 204. The wire keys are asserted from the
// RAW body (a map decode) so a missing/renamed json tag fails the
// test — the frontend reads these exact lowercase keys.
func TestCredentialGroups_RoundTrip(t *testing.T) {
	env := newGroupTestEnv(t)
	env.seedCredential(t, "cred-A", "openai", "http://a")
	env.seedCredential(t, "cred-B", "openai", "http://b")

	// POST → 201
	status, ct, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
		"id":       "g1",
		"name":     "OpenAI pool",
		"provider": "openai",
		"members":  []string{"cred-A", "cred-B"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201 (body=%s)", status, string(body))
	}
	if !strings.Contains(strings.ToLower(ct), "application/json") {
		t.Fatalf("POST Content-Type = %q, want application/json", ct)
	}

	// The wire keys are FIXED (lowercase snake_case) — decode into a
	// map and assert the exact key set.
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode POST body: %v (body=%s)", err, string(body))
	}
	for _, key := range []string{"id", "name", "provider", "members", "used_by"} {
		if _, ok := created[key]; !ok {
			t.Errorf("POST response is missing the %q key — the frontend reads the lowercase snake_case shape (body=%s)", key, string(body))
		}
	}
	for _, key := range []string{"ID", "Name", "Provider", "Members", "UsedBy"} {
		if _, ok := created[key]; ok {
			t.Errorf("POST response carries the Go field name %q — the json tags are mandatory (body=%s)", key, string(body))
		}
	}
	if created["id"] != "g1" {
		t.Errorf("POST id = %v, want g1", created["id"])
	}
	members, ok := created["members"].([]any)
	if !ok || len(members) != 2 || members[0] != "cred-A" || members[1] != "cred-B" {
		t.Errorf("POST members = %v, want [cred-A cred-B]", created["members"])
	}
	if usedBy, ok := created["used_by"].([]any); !ok || len(usedBy) != 0 {
		t.Errorf("POST used_by = %v, want an empty array (no model references the group yet)", created["used_by"])
	}

	// GET list → 200, a bare JSON ARRAY
	status, _, body = env.doJSON(t, http.MethodGet, "/fe/api/credential-groups", nil)
	if status != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200 (body=%s)", status, string(body))
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("GET list body is not a JSON ARRAY: %v (body=%s)", err, string(body))
	}
	if len(list) != 1 {
		t.Fatalf("GET list returned %d groups, want 1 (body=%s)", len(list), string(body))
	}
	if list[0]["id"] != "g1" {
		t.Errorf("GET list group id = %v, want g1", list[0]["id"])
	}
	if _, ok := list[0]["used_by"]; !ok {
		t.Errorf("GET list element carries no used_by key (A-10): %s", string(body))
	}

	// GET detail → 200
	status, _, body = env.doJSON(t, http.MethodGet, "/fe/api/credential-groups/g1", nil)
	if status != http.StatusOK {
		t.Fatalf("GET detail status = %d, want 200 (body=%s)", status, string(body))
	}
	var detail map[string]any
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode GET detail: %v (body=%s)", err, string(body))
	}
	if detail["name"] != "OpenAI pool" || detail["provider"] != "openai" {
		t.Errorf("GET detail = %v, want name/provider preserved", detail)
	}

	// PUT → 200 (the id is immutable; the path id wins)
	status, _, body = env.doJSON(t, http.MethodPut, "/fe/api/credential-groups/g1", map[string]any{
		"name":     "OpenAI pool v2",
		"provider": "openai",
		"members":  []string{"cred-A"},
	})
	if status != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 (body=%s)", status, string(body))
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode PUT body: %v (body=%s)", err, string(body))
	}
	if detail["name"] != "OpenAI pool v2" {
		t.Errorf("PUT name = %v, want \"OpenAI pool v2\"", detail["name"])
	}

	// DELETE → 204
	status, _, body = env.doJSON(t, http.MethodDelete, "/fe/api/credential-groups/g1", nil)
	if status != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204 (body=%s)", status, string(body))
	}

	// The group is gone.
	status, _, body = env.doJSON(t, http.MethodGet, "/fe/api/credential-groups/g1", nil)
	if status != http.StatusNotFound {
		t.Fatalf("GET after DELETE status = %d, want 404 (body=%s)", status, string(body))
	}
}

// TestCredentialGroups_StatusMapping (T-28): every documented status
// is produced, and EVERY error site answers the bare-JSON envelope.
func TestCredentialGroups_StatusMapping(t *testing.T) {
	env := newGroupTestEnv(t)
	env.seedCredential(t, "cred-A", "openai", "http://a")
	env.seedCredential(t, "cred-B", "openai", "http://b")
	env.seedCredential(t, "cred-x", "anthropic", "http://x")

	// Seed a good group.
	if status, _, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
		"id": "g1", "name": "pool", "provider": "openai", "members": []string{"cred-A", "cred-B"},
	}); status != http.StatusCreated {
		t.Fatalf("seed group status = %d, want 201 (body=%s)", status, string(body))
	}

	t.Run("400 malformed JSON", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, env.server.URL+"/fe/api/credential-groups", strings.NewReader("{not json"))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		assertBareJSONError(t, resp.StatusCode, resp.Header.Get("Content-Type"), raw, http.StatusBadRequest)
	})

	t.Run("400 missing id", func(t *testing.T) {
		status, ct, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
			"name": "no id", "provider": "openai",
		})
		assertBareJSONError(t, status, ct, body, http.StatusBadRequest)
	})

	t.Run("400 missing name", func(t *testing.T) {
		status, ct, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
			"id": "g-nameless", "provider": "openai",
		})
		assertBareJSONError(t, status, ct, body, http.StatusBadRequest)
	})

	t.Run("400 invalid provider", func(t *testing.T) {
		status, ct, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
			"id": "g-badprov", "name": "bad", "provider": "not-a-provider",
		})
		assertBareJSONError(t, status, ct, body, http.StatusBadRequest)
	})

	t.Run("400 cross-provider member", func(t *testing.T) {
		status, ct, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
			"id": "g-mixed", "name": "mixed", "provider": "openai", "members": []string{"cred-A", "cred-x"},
		})
		assertBareJSONError(t, status, ct, body, http.StatusBadRequest)
	})

	t.Run("400 missing member", func(t *testing.T) {
		status, ct, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
			"id": "g-missing-member", "name": "missing", "provider": "openai", "members": []string{"cred-nope"},
		})
		assertBareJSONError(t, status, ct, body, http.StatusBadRequest)
	})

	t.Run("400 duplicate group id", func(t *testing.T) {
		status, ct, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
			"id": "g1", "name": "dup", "provider": "openai", "members": []string{"cred-A"},
		})
		assertBareJSONError(t, status, ct, body, http.StatusBadRequest)
	})

	t.Run("400 missing id in path", func(t *testing.T) {
		// The {id} route is registered with the trailing slash, so the
		// bare subtree path carries an EMPTY id.
		status, ct, body := env.doJSON(t, http.MethodGet, "/fe/api/credential-groups/", nil)
		assertBareJSONError(t, status, ct, body, http.StatusBadRequest)
	})

	t.Run("404 unknown id", func(t *testing.T) {
		status, ct, body := env.doJSON(t, http.MethodGet, "/fe/api/credential-groups/nope", nil)
		assertBareJSONError(t, status, ct, body, http.StatusNotFound)
	})

	t.Run("405 wrong method", func(t *testing.T) {
		status, ct, body := env.doJSON(t, http.MethodDelete, "/fe/api/credential-groups", nil)
		assertBareJSONError(t, status, ct, body, http.StatusMethodNotAllowed)

		status, ct, body = env.doJSON(t, http.MethodPost, "/fe/api/credential-groups/g1", map[string]any{})
		assertBareJSONError(t, status, ct, body, http.StatusMethodNotAllowed)
	})

	t.Run("409 in use", func(t *testing.T) {
		// A model references the group ⇒ the delete is a conflict.
		status, _, body := env.doJSON(t, http.MethodPost, "/fe/api/models", map[string]any{
			"id": "m-uses-g1", "name": "m-uses-g1", "enabled": true,
			"internal": true, "internal_model": "gpt-4o",
			"credentials": []map[string]any{{"group_id": "g1", "weight": 1}},
		})
		if status != http.StatusCreated {
			t.Fatalf("create referencing model status = %d, want 201 (body=%s)", status, string(body))
		}
		status, ct, body := env.doJSON(t, http.MethodDelete, "/fe/api/credential-groups/g1", nil)
		assertBareJSONError(t, status, ct, body, http.StatusConflict)
	})
}

// failingSaveModelsConfig is the manager with a failing Save() — the
// 500 branch of the group endpoints.
type failingSaveModelsConfig struct {
	*database.ModelsManager
}

func (f *failingSaveModelsConfig) Save() error {
	return errors.New("disk is on fire (test)")
}

// TestCredentialGroups_SaveFailureIs500 (T-28): a Save() failure
// answers 500 (never 400) — and still with the bare-JSON envelope.
func TestCredentialGroups_SaveFailureIs500(t *testing.T) {
	env := newGroupTestEnv(t)
	env.seedCredential(t, "cred-A", "openai", "http://a")

	s := &Server{modelsConfig: &failingSaveModelsConfig{ModelsManager: env.mgr}}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/credential-groups", s.handleCredentialGroups)
	mux.HandleFunc("/fe/api/credential-groups/", s.handleCredentialGroupDetail)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	buf, _ := json.Marshal(map[string]any{"id": "g1", "name": "pool", "provider": "openai"})
	resp, err := http.Post(srv.URL+"/fe/api/credential-groups", "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	assertBareJSONError(t, resp.StatusCode, resp.Header.Get("Content-Type"), raw, http.StatusInternalServerError)
}

// countingModelsConfig counts the ONE batched used_by pass.
type countingModelsConfig struct {
	*database.ModelsManager
	batchedCalls int
}

func (c *countingModelsConfig) ModelsUsingCredentialGroupsAll() map[string][]string {
	c.batchedCalls++
	return c.ModelsManager.ModelsUsingCredentialGroupsAll()
}

// TestCredentialGroups_UsedByOnListAndDetail (A-10): used_by is
// carried by BOTH the list and the detail, and the list computes it in
// ONE batched pass (never one scan per group).
func TestCredentialGroups_UsedByOnListAndDetail(t *testing.T) {
	env := newGroupTestEnv(t)
	env.seedCredential(t, "cred-A", "openai", "http://a")
	env.seedCredential(t, "cred-B", "openai", "http://b")

	counter := &countingModelsConfig{ModelsManager: env.mgr}
	s := &Server{modelsConfig: counter}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/credential-groups", s.handleCredentialGroups)
	mux.HandleFunc("/fe/api/credential-groups/", s.handleCredentialGroupDetail)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Three groups, one of them referenced by a model.
	for _, g := range []struct {
		id      string
		members []string
	}{
		{"g1", []string{"cred-A"}},
		{"g2", []string{"cred-A"}},
		{"g3", []string{"cred-B"}},
	} {
		if err := env.mgr.AddCredentialGroup(models.CredentialGroupConfig{
			ID: g.id, Name: g.id, Provider: "openai", Members: g.members,
		}); err != nil {
			t.Fatalf("AddCredentialGroup(%s): %v", g.id, err)
		}
	}
	if err := env.mgr.AddModel(models.ModelConfig{
		ID: "m-uses-g2", Name: "m-uses-g2", Enabled: true, Internal: true, InternalModel: "gpt-4o",
		Credentials: []models.CredentialRef{{GroupID: "g2", Weight: 1}},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// LIST → used_by on every element, in ONE batched pass.
	counter.batchedCalls = 0
	resp, err := http.Get(srv.URL + "/fe/api/credential-groups")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200 (body=%s)", resp.StatusCode, string(raw))
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode list: %v (body=%s)", err, string(raw))
	}
	if len(list) != 3 {
		t.Fatalf("list returned %d groups, want 3 (body=%s)", len(list), string(raw))
	}
	usage := map[string][]any{}
	for _, g := range list {
		ub, ok := g["used_by"].([]any)
		if !ok {
			t.Fatalf("group %v carries no used_by array: %s", g["id"], string(raw))
		}
		usage[g["id"].(string)] = ub
	}
	if len(usage["g2"]) != 1 || usage["g2"][0] != "m-uses-g2" {
		t.Errorf("g2 used_by = %v, want [m-uses-g2]", usage["g2"])
	}
	if len(usage["g1"]) != 0 || len(usage["g3"]) != 0 {
		t.Errorf("unreferenced groups must carry an empty used_by: g1=%v g3=%v", usage["g1"], usage["g3"])
	}
	if counter.batchedCalls != 1 {
		t.Errorf("batched used_by passes = %d, want exactly 1 (never one scan per group)", counter.batchedCalls)
	}

	// DETAIL → used_by too.
	counter.batchedCalls = 0
	resp, err = http.Get(srv.URL + "/fe/api/credential-groups/g2")
	if err != nil {
		t.Fatalf("GET detail: %v", err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET detail status = %d, want 200 (body=%s)", resp.StatusCode, string(raw))
	}
	var detail map[string]any
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode detail: %v (body=%s)", err, string(raw))
	}
	ub, ok := detail["used_by"].([]any)
	if !ok || len(ub) != 1 || ub[0] != "m-uses-g2" {
		t.Errorf("detail used_by = %v, want [m-uses-g2]", detail["used_by"])
	}
}

// TestCredentialGroups_ValidationMirrorAcceptsGroupRef (T-28c — the
// LOAD-BEARING fix): the UI-side validation mirror accepts a group
// reference on ALL FOUR model write/validate paths (create / edit /
// copy / validate). Before the fix every group ref was rejected with
// 400 "credentials[0]: identifier is empty" — the feature was
// unreachable through the API.
func TestCredentialGroups_ValidationMirrorAcceptsGroupRef(t *testing.T) {
	env := newGroupTestEnv(t)
	env.seedCredential(t, "cred-A", "openai", "http://a")
	env.seedCredential(t, "cred-B", "openai", "http://b")

	if status, _, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
		"id": "g1", "name": "pool", "provider": "openai", "members": []string{"cred-A", "cred-B"},
	}); status != http.StatusCreated {
		t.Fatalf("seed group status = %d, want 201 (body=%s)", status, string(body))
	}

	groupRef := []map[string]any{{"group_id": "g1", "weight": 1}}

	// (1) CREATE
	status, _, body := env.doJSON(t, http.MethodPost, "/fe/api/models", map[string]any{
		"id": "m-group", "name": "m-group", "enabled": true,
		"internal": true, "internal_model": "gpt-4o",
		"credentials": groupRef,
	})
	if status != http.StatusCreated {
		t.Fatalf("create model with a group ref: status = %d, want 201 (body=%s)", status, string(body))
	}
	if strings.Contains(string(body), "identifier is empty") {
		t.Fatalf("the UI mirror still rejects a group ref: %s", string(body))
	}

	// (2) EDIT (PUT)
	status, _, body = env.doJSON(t, http.MethodPut, "/fe/api/models/m-group", map[string]any{
		"id": "m-group", "name": "m-group renamed", "enabled": true,
		"internal": true, "internal_model": "gpt-4o",
		"credentials": groupRef,
	})
	if status != http.StatusOK {
		t.Fatalf("edit model with a group ref: status = %d, want 200 (body=%s)", status, string(body))
	}

	// (3) COPY
	status, _, body = env.doJSON(t, http.MethodPost, "/fe/api/models/m-group/copy", map[string]any{
		"new_id": "m-group-copy",
	})
	if status != http.StatusCreated {
		t.Fatalf("copy a model with a group ref: status = %d, want 201 (body=%s)", status, string(body))
	}
	if strings.Contains(string(body), "identifier is empty") {
		t.Fatalf("the UI mirror still rejects a group ref on the copy path: %s", string(body))
	}

	// (4) VALIDATE
	status, _, body = env.doJSON(t, http.MethodPost, "/fe/api/models/validate", map[string]any{
		"id": "m-validate", "name": "m-validate", "enabled": true,
		"internal": true, "internal_model": "gpt-4o",
		"credentials": groupRef,
	})
	if status != http.StatusOK {
		t.Fatalf("validate a model with a group ref: status = %d, want 200 (body=%s)", status, string(body))
	}
	var validateResp map[string]any
	if err := json.Unmarshal(body, &validateResp); err != nil {
		t.Fatalf("decode validate response: %v (body=%s)", err, string(body))
	}
	if validateResp["valid"] != true {
		t.Fatalf("validate a model with a group ref: valid = %v, want true (body=%s)", validateResp["valid"], string(body))
	}

	// A MISSING group is still a 400 (and the mirror text is the
	// UI-mirror one — never the store-side "credential_id is empty").
	status, _, body = env.doJSON(t, http.MethodPost, "/fe/api/models", map[string]any{
		"id": "m-missing-group", "name": "m-missing-group", "enabled": true,
		"internal": true, "internal_model": "gpt-4o",
		"credentials": []map[string]any{{"group_id": "g-nope", "weight": 1}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("a missing group ref: status = %d, want 400 (body=%s)", status, string(body))
	}
	if strings.Contains(string(body), "model m-missing-group: credentials[0].credential_id is empty") {
		t.Fatalf("the mirror emitted the STORE-side error text: %s", string(body))
	}
	if !strings.Contains(string(body), "unknown credential group") {
		t.Errorf("the missing-group rejection must name the unknown group: %s", string(body))
	}

	// A NON-CHAT kind forbids group refs (R-5).
	status, _, body = env.doJSON(t, http.MethodPost, "/fe/api/models", map[string]any{
		"id": "m-decision-group", "name": "m-decision-group", "enabled": true,
		"internal": true, "internal_model": "gpt-4o", "kind": models.KindDecision,
		"credentials": groupRef,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("a group ref on a decision model: status = %d, want 400 (body=%s)", status, string(body))
	}
}

// TestUIGuard_9routerPrimaryFiresForGroupModel (A-2 row 8): the
// six 9router-primary guards read the FLATTENED primary credential.
// A group-primary model whose group's provider is 9router must be
// rejected for a chat model — before the fix the guard was bypassed
// (Credentials[0].CredentialID was EMPTY for a group ref).
func TestUIGuard_9routerPrimaryFiresForGroupModel(t *testing.T) {
	env := newGroupTestEnv(t)
	env.seedCredential(t, "cred-9r", "9router", "http://9r")

	if status, _, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
		"id": "g-9r", "name": "9router pool", "provider": "9router", "members": []string{"cred-9r"},
	}); status != http.StatusCreated {
		t.Fatalf("seed group status = %d, want 201 (body=%s)", status, string(body))
	}

	// A chat model on a 9router group ⇒ 400.
	status, _, body := env.doJSON(t, http.MethodPost, "/fe/api/models", map[string]any{
		"id": "m-9r-group", "name": "m-9r-group", "enabled": true,
		"internal": true, "internal_model": "gpt-4o",
		"credentials": []map[string]any{{"group_id": "g-9r", "weight": 1}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("chat model on a 9router group: status = %d, want 400 (body=%s)", status, string(body))
	}
	if !strings.Contains(string(body), "9router") {
		t.Fatalf("the rejection must name the 9router provider: %s", string(body))
	}

	// The PUT twin must fire too.
	status, _, body = env.doJSON(t, http.MethodPut, "/fe/api/models/m-9r-group", map[string]any{
		"id": "m-9r-group", "name": "m-9r-group", "enabled": true,
		"internal": true, "internal_model": "gpt-4o",
		"credentials": []map[string]any{{"group_id": "g-9r", "weight": 1}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("PUT chat model on a 9router group: status = %d, want 400 (body=%s)", status, string(body))
	}
}

// TestHandleTestModel_GroupIDTestsFirstMember (T-28b / A-11): the
// model-test flow accepts a GROUP id and tests the FIRST flattened
// member. Before the fix it dead-ended with 400 "Credential not
// found: <group id>".
func TestHandleTestModel_GroupIDTestsFirstMember(t *testing.T) {
	var hits []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer upstream.Close()

	env := newGroupTestEnv(t)
	env.seedCredential(t, "cred-A", "openai", upstream.URL)
	env.seedCredential(t, "cred-B", "openai", upstream.URL)

	if status, _, body := env.doJSON(t, http.MethodPost, "/fe/api/credential-groups", map[string]any{
		"id": "g1", "name": "pool", "provider": "openai", "members": []string{"cred-A", "cred-B"},
	}); status != http.StatusCreated {
		t.Fatalf("seed group status = %d, want 201 (body=%s)", status, string(body))
	}

	// The TEST button posts the GROUP id.
	status, ct, body := env.doJSON(t, http.MethodPost, "/fe/api/models/test", map[string]any{
		"credential_id":  "g1",
		"internal_model": "gpt-4o",
	})
	if status != http.StatusOK {
		t.Fatalf("test model with a group id: status = %d, want 200 (body=%s)", status, string(body))
	}
	if !strings.Contains(strings.ToLower(ct), "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var resp TestModelResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode test response: %v (body=%s)", err, string(body))
	}
	if !resp.Success {
		t.Fatalf("test model with a group id failed: %+v", resp)
	}
	if len(hits) != 1 {
		t.Fatalf("upstream calls = %d, want 1 (the group's first member is tested)", len(hits))
	}
	// The FIRST MEMBER was used (key-cred-A), not the group id.
	if !strings.Contains(hits[0], "key-cred-A") {
		t.Fatalf("upstream Authorization = %q, want the group's FIRST member (key-cred-A)", hits[0])
	}

	// An unknown id still answers 400 "Credential not found" (the
	// legacy behaviour is preserved for non-group ids).
	status, _, body = env.doJSON(t, http.MethodPost, "/fe/api/models/test", map[string]any{
		"credential_id":  "no-such-credential",
		"internal_model": "gpt-4o",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("test model with an unknown id: status = %d, want 400 (body=%s)", status, string(body))
	}
	if !strings.Contains(string(body), "Credential not found") {
		t.Fatalf("unknown id must report \"Credential not found\": %s", string(body))
	}
}

// TestValidateCredentials_DirectRulesUnchanged (T-28c): the
// group-aware mirror keeps the DIRECT-ref rules byte-identical.
func TestValidateCredentials_DirectRulesUnchanged(t *testing.T) {
	creds := []models.CredentialConfig{
		{ID: "cred-A", Provider: "openai"},
		{ID: "cred-B", Provider: "anthropic"},
	}

	// (a) internal + empty → the legacy message.
	errs := validateCredentials(nil, true, creds, nil, models.KindChat)
	if len(errs) != 1 || !strings.Contains(errs[0], "at least one credential is required") {
		t.Fatalf("rule (a) errors = %v, want the legacy \"at least one credential is required\"", errs)
	}

	// (b) empty identifier → the legacy mirror text.
	errs = validateCredentials([]models.CredentialRef{{Weight: 1}}, false, creds, nil, models.KindChat)
	if len(errs) != 1 || errs[0] != "credentials[0]: identifier is empty" {
		t.Fatalf("rule (b) errors = %v, want [credentials[0]: identifier is empty]", errs)
	}

	// (b) unknown credential → the legacy mirror text.
	errs = validateCredentials([]models.CredentialRef{{CredentialID: "nope", Weight: 1}}, false, creds, nil, models.KindChat)
	if len(errs) != 1 || !strings.Contains(errs[0], "references an unknown credential") {
		t.Fatalf("unknown-credential errors = %v, want the legacy unknown-credential message", errs)
	}

	// (c) weight → the legacy mirror text.
	errs = validateCredentials([]models.CredentialRef{{CredentialID: "cred-A"}}, false, creds, nil, models.KindChat)
	if len(errs) != 1 || !strings.Contains(errs[0], "weight must be > 0") {
		t.Fatalf("weight errors = %v, want the legacy weight message", errs)
	}

	// (e) duplicate → the legacy mirror text.
	errs = validateCredentials([]models.CredentialRef{
		{CredentialID: "cred-A", Weight: 1},
		{CredentialID: "cred-A", Weight: 1},
	}, false, creds, nil, models.KindChat)
	if len(errs) != 1 || !strings.Contains(errs[0], "duplicate identifier") {
		t.Fatalf("duplicate errors = %v, want the legacy duplicate message", errs)
	}

	// (d) provider mismatch → the legacy mirror text.
	errs = validateCredentials([]models.CredentialRef{
		{CredentialID: "cred-A", Weight: 1},
		{CredentialID: "cred-B", Weight: 1},
	}, false, creds, nil, models.KindChat)
	if len(errs) != 1 || !strings.Contains(errs[0], "does not match primary provider") {
		t.Fatalf("provider errors = %v, want the legacy provider-mismatch message", errs)
	}

	// (f) size cap → the legacy mirror text.
	many := make([]models.CredentialRef, models.MaxCredentialRefs+1)
	for i := range many {
		many[i] = models.CredentialRef{CredentialID: fmt.Sprintf("c-%d", i), Weight: 1}
	}
	errs = validateCredentials(many, false, creds, nil, models.KindChat)
	if len(errs) == 0 || !strings.Contains(errs[0], "list exceeds max of") {
		t.Fatalf("size-cap errors = %v, want the legacy size-cap message", errs)
	}

	// A group ref on a chat model with an existing group → accepted.
	groups := []models.CredentialGroupConfig{{ID: "g1", Name: "pool", Provider: "openai", Members: []string{"cred-A"}}}
	errs = validateCredentials([]models.CredentialRef{{GroupID: "g1", Weight: 1}}, false, creds, groups, models.KindChat)
	if len(errs) != 0 {
		t.Fatalf("a valid group ref was rejected: %v", errs)
	}

	// A group ref on a NON-chat kind → rejected (R-5).
	errs = validateCredentials([]models.CredentialRef{{GroupID: "g1", Weight: 1}}, false, creds, groups, models.KindDecision)
	if len(errs) != 1 || !strings.Contains(errs[0], "not allowed for kind") {
		t.Fatalf("non-chat group ref errors = %v, want the kind rejection", errs)
	}

	// BOTH-set → rejected (R-1).
	errs = validateCredentials([]models.CredentialRef{{CredentialID: "cred-A", GroupID: "g1", Weight: 1}}, false, creds, groups, models.KindChat)
	if len(errs) != 1 || !strings.Contains(errs[0], "both credential_id and group_id set") {
		t.Fatalf("both-set errors = %v, want the both-set rejection", errs)
	}
}

// interfacePathModelsConfig is a models.ModelsConfigInterface whose
// ModelsUsingCredentialGroupsAll returns a CONFIGURED map and whose
// GetModels returns a CONFIGURED (possibly stale) model list.
//
// It is the PRODUCTION-SHAPE proof: the used_by surface must come from
// the interface (the authoritative DB pass), never from the model list
// (the decorator's 15s-TTL cache) when the interface supplies a map.
type interfacePathModelsConfig struct {
	*database.ModelsManager
	batched      map[string][]string
	batchedCalls int
	list         []models.ModelConfig
	listCalls    int
}

func (c *interfacePathModelsConfig) ModelsUsingCredentialGroupsAll() map[string][]string {
	c.batchedCalls++
	return c.batched
}

func (c *interfacePathModelsConfig) GetModels() []models.ModelConfig {
	c.listCalls++
	return c.list
}

// TestCredentialGroups_UsedByTakesInterfacePathNotStaleModelList proves
// the UI reads used_by from the INTERFACE (the authoritative one-pass DB
// scan) and NOT from the model list.
//
// The mock makes the two disagree: the interface says g2 is used by
// "authoritative-model"; the stale list says g1 is used by
// "stale-model". The response must follow the interface, and the
// list-derived fallback must NOT run at all.
func TestCredentialGroups_UsedByTakesInterfacePathNotStaleModelList(t *testing.T) {
	env := newGroupTestEnv(t)
	env.seedCredential(t, "cred-A", "openai", "http://a")

	for _, id := range []string{"g1", "g2"} {
		if err := env.mgr.AddCredentialGroup(models.CredentialGroupConfig{
			ID: id, Name: id, Provider: "openai", Members: []string{"cred-A"},
		}); err != nil {
			t.Fatalf("AddCredentialGroup(%s): %v", id, err)
		}
	}

	cfg := &interfacePathModelsConfig{
		ModelsManager: env.mgr,
		batched:       map[string][]string{"g2": {"authoritative-model"}},
		list: []models.ModelConfig{{
			ID: "stale-model", Name: "stale-model",
			Credentials: []models.CredentialRef{{GroupID: "g1", Weight: 1}},
		}},
	}
	s := &Server{modelsConfig: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/credential-groups", s.handleCredentialGroups)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/fe/api/credential-groups")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200 (body=%s)", resp.StatusCode, string(raw))
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode list: %v (body=%s)", err, string(raw))
	}
	if len(list) != 2 {
		t.Fatalf("list returned %d groups, want 2 (body=%s)", len(list), string(raw))
	}
	usage := map[string][]any{}
	for _, g := range list {
		ub, ok := g["used_by"].([]any)
		if !ok {
			t.Fatalf("group %v carries no used_by array: %s", g["id"], string(raw))
		}
		usage[g["id"].(string)] = ub
	}
	if len(usage["g2"]) != 1 || usage["g2"][0] != "authoritative-model" {
		t.Errorf("g2 used_by = %v, want [authoritative-model] (the interface path must win)", usage["g2"])
	}
	if len(usage["g1"]) != 0 {
		t.Errorf("g1 used_by = %v, want [] (the stale model list must not leak into used_by)", usage["g1"])
	}
	if cfg.batchedCalls != 1 {
		t.Errorf("batched used_by passes = %d, want exactly 1", cfg.batchedCalls)
	}
	if cfg.listCalls != 0 {
		t.Errorf("GetModels() calls = %d, want 0 (the list fallback must NOT run when the interface supplies a map)", cfg.listCalls)
	}
}

// TestCredentialGroups_UsedByFallsBackToModelListWhenInterfaceNil proves
// the nil/empty guard still works: when the interface returns nil (the
// JSON twin and the test mocks carry no credential groups), the UI
// derives the same map in ONE pass over the model list — never one scan
// per group.
func TestCredentialGroups_UsedByFallsBackToModelListWhenInterfaceNil(t *testing.T) {
	env := newGroupTestEnv(t)
	env.seedCredential(t, "cred-A", "openai", "http://a")

	if err := env.mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "g1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	cfg := &interfacePathModelsConfig{
		ModelsManager: env.mgr,
		batched:       nil,
		list: []models.ModelConfig{{
			ID: "list-model", Name: "list-model",
			Credentials: []models.CredentialRef{{GroupID: "g1", Weight: 1}},
		}},
	}
	s := &Server{modelsConfig: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/credential-groups", s.handleCredentialGroups)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/fe/api/credential-groups")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200 (body=%s)", resp.StatusCode, string(raw))
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode list: %v (body=%s)", err, string(raw))
	}
	if len(list) != 1 {
		t.Fatalf("list returned %d groups, want 1 (body=%s)", len(list), string(raw))
	}
	ub, ok := list[0]["used_by"].([]any)
	if !ok {
		t.Fatalf("group carries no used_by array: %s", string(raw))
	}
	if len(ub) != 1 || ub[0] != "list-model" {
		t.Errorf("used_by = %v, want [list-model] (the nil-guard fallback must derive the map from the model list)", ub)
	}
	if cfg.batchedCalls != 1 {
		t.Errorf("batched used_by passes = %d, want exactly 1 (the interface is tried first)", cfg.batchedCalls)
	}
	if cfg.listCalls != 1 {
		t.Errorf("GetModels() calls = %d, want exactly 1 (the fallback must make ONE pass over the model list)", cfg.listCalls)
	}
}

// TestCredentialGroups_UsedByReflectsDirectDBWriteImmediately is the
// PRODUCTION-SHAPE proof: it binds the REAL modelscache decorator over
// the REAL manager — exactly what cmd/main.go binds when
// CACHE_LAYER_ENABLED is on (the default) — and writes a model
// DIRECTLY to the DB.
//
// The decorator's model-list snapshot does NOT see that write (no
// reconcile tick), so a used_by derived from the decorator's model list
// would MISS it and answer "not used". The authoritative interface path
// reads the DB, so the response must carry the model IMMEDIATELY.
func TestCredentialGroups_UsedByReflectsDirectDBWriteImmediately(t *testing.T) {
	env := newGroupTestEnv(t)
	env.seedCredential(t, "cred-A", "openai", "http://a")

	// The group is created BEFORE the decorator boots, so the
	// decorator's group mirror holds it (the list endpoint enumerates
	// groups from the models config).
	if err := env.mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "g1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	// PRODUCTION BINDING: the decorator over the real manager.
	dec, err := modelscache.NewCachedModelsConfig(env.mgr, modelscache.Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	defer dec.Stop()

	// The model is written DIRECTLY to the DB — the decorator's
	// snapshot never sees it (the reconciler does not tick within the
	// test).
	if err := env.mgr.AddModel(models.ModelConfig{
		ID: "m-direct", Name: "m-direct", Enabled: true, Internal: true, InternalModel: "gpt-4o",
		Credentials: []models.CredentialRef{{GroupID: "g1", Weight: 1}},
	}); err != nil {
		t.Fatalf("AddModel(direct): %v", err)
	}

	// PREMISE: the decorator's model-list snapshot is stale — it must
	// NOT contain the directly-written model. If it did, the test would
	// prove nothing.
	if snap := dec.GetModels(); len(snap) != 0 {
		t.Fatalf("premise broken: the decorator's snapshot must be stale, got %d models", len(snap))
	}

	s := &Server{modelsConfig: dec}
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/credential-groups", s.handleCredentialGroups)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/fe/api/credential-groups")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200 (body=%s)", resp.StatusCode, string(raw))
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode list: %v (body=%s)", err, string(raw))
	}
	if len(list) != 1 {
		t.Fatalf("list returned %d groups, want 1 (body=%s)", len(list), string(raw))
	}
	ub, ok := list[0]["used_by"].([]any)
	if !ok {
		t.Fatalf("group carries no used_by array: %s", string(raw))
	}
	if len(ub) != 1 || ub[0] != "m-direct" {
		t.Fatalf("used_by = %v, want [m-direct] (the authoritative DB pass must see the direct write immediately)", ub)
	}
}
