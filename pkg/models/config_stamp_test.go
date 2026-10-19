package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// remove-external-upstream P1 task 7 (layer iii/iii): the JSON backend
// stamps Internal=true on AddModel/UpdateModel BEFORE validation and as
// a LOOP over mc.Models in Save. These tests pin the stamp + the
// persistence round-trip: a write with Internal=false persists and
// reads back internal=true (no way left to persist internal=false).

func TestModelsConfig_JSONBackend_WriteStamp_AddAndUpdate(t *testing.T) {
	mc := NewModelsConfig()
	if err := mc.AddCredential(CredentialConfig{ID: "cred-1", Provider: "openai", APIKey: "k", BaseURL: "http://x"}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}

	payload := ModelConfig{
		ID:            "m1",
		Name:          "m1",
		Enabled:       true,
		Internal:      false, // caller "omits the tick" — must be stamped over
		InternalModel: "upstream-x",
		Credentials:   TestRefs("cred-1"),
	}
	if err := mc.AddModel(payload); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	if got := mc.GetModel("m1"); got == nil || !got.Internal {
		t.Errorf("AddModel: GetModel(m1).Internal = %+v, want true (stamp must precede validation+store)", got)
	}

	update := payload
	update.Name = "m1-v2"
	update.Internal = false // PUT without the internal flag — must stamp over
	if err := mc.UpdateModel("m1", update); err != nil {
		t.Fatalf("UpdateModel: %v", err)
	}
	if got := mc.GetModel("m1"); got == nil || !got.Internal {
		t.Errorf("UpdateModel: GetModel(m1).Internal = %+v, want true", got)
	}
	if got := mc.GetModel("m1"); got != nil && got.Name != "m1-v2" {
		t.Errorf("UpdateModel: Name = %q, want m1-v2", got.Name)
	}
}

func TestModelsConfig_JSONBackend_WriteStamp_SaveLoop(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "models.json")

	mc := NewModelsConfig()
	if err := mc.Load(filePath); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := mc.AddCredential(CredentialConfig{ID: "cred-1", Provider: "openai", APIKey: "k", BaseURL: "http://x"}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	// Seed a row with Internal explicitly false through the RAW slice
	// (bypassing AddModel's stamp) to prove the Save loop converges it.
	mc.Models = append(mc.Models, ModelConfig{
		ID:            "legacy-row",
		Name:          "legacy-row",
		Enabled:       true,
		Internal:      false,
		InternalModel: "upstream-x",
		Credentials:   TestRefs("cred-1"),
	})

	if err := mc.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The in-memory row must be stamped by the Save loop.
	if got := mc.GetModel("legacy-row"); got == nil || !got.Internal {
		t.Errorf("in-memory row after Save: Internal = %+v, want true (Save loop stamp)", got)
	}

	// And the persisted file must carry internal=true (round-trip).
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	var disk struct {
		Models []ModelConfig `json:"models"`
	}
	if err := json.Unmarshal(data, &disk); err != nil {
		t.Fatalf("saved file not valid JSON: %v", err)
	}
	found := false
	for _, m := range disk.Models {
		if m.ID == "legacy-row" {
			found = true
			if !m.Internal {
				t.Error("persisted legacy-row carries internal=false — Save loop did not stamp the file payload")
			}
		}
	}
	if !found {
		t.Fatal("legacy-row missing from saved file")
	}
}
