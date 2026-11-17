package database

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestCredentialGroup_PersistedShape_PlainModel asserts the EXACT persisted
// shape for a model whose credentials are plain (direct) credential refs:
// credentials_json carries credential_id/weight/position and NO group_id key.
func TestCredentialGroup_PersistedShape_PlainModel(t *testing.T) {
	mgr, store, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddModel(models.ModelConfig{
		ID: "m-plain", Name: "M plain", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{{CredentialID: "cred-A", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	var raw string
	if err := store.DB.QueryRow(`SELECT credentials_json FROM models WHERE id = ?`, "m-plain").Scan(&raw); err != nil {
		t.Fatalf("read row: %v", err)
	}

	expectedBytes, err := json.Marshal([]models.CredentialRef{{CredentialID: "cred-A", Weight: 1, Position: 0}})
	if err != nil {
		t.Fatalf("marshal expected: %v", err)
	}
	if raw != string(expectedBytes) {
		t.Fatalf("plain model credentials_json = %s, want %s", raw, string(expectedBytes))
	}
	if strings.Contains(raw, "group_id") {
		t.Fatalf("plain model persisted a group_id key: %s", raw)
	}
}

// TestCredentialGroup_PersistedShape_GroupRef asserts the counterpart: a model
// whose refs include a group ref persists a group_id key.
func TestCredentialGroup_PersistedShape_GroupRef(t *testing.T) {
	mgr, store, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g-one", Name: "G one", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m-group", Name: "M group", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{{GroupID: "g-one", Weight: 2, Position: 1}},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	var raw string
	if err := store.DB.QueryRow(`SELECT credentials_json FROM models WHERE id = ?`, "m-group").Scan(&raw); err != nil {
		t.Fatalf("read row: %v", err)
	}

	expectedBytes, err := json.Marshal([]models.CredentialRef{{GroupID: "g-one", Weight: 2, Position: 1}})
	if err != nil {
		t.Fatalf("marshal expected: %v", err)
	}
	if raw != string(expectedBytes) {
		t.Fatalf("group model credentials_json = %s, want %s", raw, string(expectedBytes))
	}
	if !strings.Contains(raw, "group_id") {
		t.Fatalf("group model did not persist a group_id key: %s", raw)
	}
}
