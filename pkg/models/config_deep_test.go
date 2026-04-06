package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// =============================================================================
// Duration type tests
// =============================================================================

func TestDuration_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		duration Duration
		want     string
	}{
		{"zero", Duration(0), `"0s"`},
		{"seconds", Duration(5 * time.Second), `"5s"`},
		{"minutes", Duration(2 * time.Minute), `"2m0s"`},
		{"complex", Duration(1*time.Minute + 50*time.Second), `"1m50s"`},
		{"hours", Duration(1 * time.Hour), `"1h0m0s"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.duration.MarshalJSON()
			if err != nil {
				t.Errorf("MarshalJSON() error = %v", err)
				return
			}
			if string(got) != tt.want {
				t.Errorf("MarshalJSON() = %v, want %v", string(got), tt.want)
			}
		})
	}
}

func TestDuration_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Duration
		wantErr bool
	}{
		// String formats
		{"zero string", `"0s"`, Duration(0), false},
		{"seconds", `"5s"`, Duration(5 * time.Second), false},
		{"minutes", `"2m"`, Duration(2 * time.Minute), false},
		{"complex", `"1m50s"`, Duration(1*time.Minute + 50*time.Second), false},
		{"hours", `"1h"`, Duration(1 * time.Hour), false},

		// Number formats (nanoseconds)
		{"zero number", `0`, Duration(0), false},
		{"seconds as number", `5000000000`, Duration(5 * time.Second), false},
		{"minutes as number", `120000000000`, Duration(2 * time.Minute), false},

		// Negative values
		{"negative string", `"-1s"`, Duration(0), true},
		{"negative number", `-1000`, Duration(0), true},

		// Invalid formats
		{"invalid string", `"invalid"`, Duration(0), true},
		{"invalid type", `true`, Duration(0), true},
		{"invalid object", `{}`, Duration(0), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := json.Unmarshal([]byte(tt.input), &d)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && d != tt.want {
				t.Errorf("UnmarshalJSON() = %v, want %v", d, tt.want)
			}
		})
	}
}

func TestDuration_String(t *testing.T) {
	tests := []struct {
		name     string
		duration Duration
		want     string
	}{
		{"zero", Duration(0), "0s"},
		{"seconds", Duration(5 * time.Second), "5s"},
		{"minutes", Duration(2 * time.Minute), "2m0s"},
		{"complex", Duration(1*time.Minute + 50*time.Second), "1m50s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.duration.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDuration_Duration(t *testing.T) {
	tests := []struct {
		name     string
		duration Duration
		want     time.Duration
	}{
		{"zero", Duration(0), 0},
		{"seconds", Duration(5 * time.Second), 5 * time.Second},
		{"minutes", Duration(2 * time.Minute), 2 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.duration.Duration(); got != tt.want {
				t.Errorf("Duration() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// GetConfigPath tests
// =============================================================================

func TestGetConfigPath(t *testing.T) {
	// Just verify it returns a non-empty path
	path := GetConfigPath()
	if path == "" {
		t.Error("GetConfigPath() returned empty string")
	}
	// Should end with models.json
	if filepath.Base(path) != "models.json" {
		t.Errorf("GetConfigPath() = %v, should end with models.json", path)
	}
}

// =============================================================================
// ModelConfig method tests
// =============================================================================

func TestModelConfig_GetReleaseStreamChunkDeadline(t *testing.T) {
	tests := []struct {
		name   string
		config ModelConfig
		want   time.Duration
	}{
		{"zero deadline", ModelConfig{ReleaseStreamChunkDeadline: 0}, 0},
		{"set deadline", ModelConfig{ReleaseStreamChunkDeadline: Duration(110 * time.Second)}, 110 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.GetReleaseStreamChunkDeadline(); got != tt.want {
				t.Errorf("GetReleaseStreamChunkDeadline() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestModelConfig_IsInternal(t *testing.T) {
	tests := []struct {
		name     string
		config   ModelConfig
		expected bool
	}{
		{"internal true", ModelConfig{Internal: true}, true},
		{"internal false", ModelConfig{Internal: false}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.IsInternal(); got != tt.expected {
				t.Errorf("IsInternal() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestModelConfig_GetInternalConfig(t *testing.T) {
	tests := []struct {
		name       string
		config     ModelConfig
		wantCredID string
		wantBase   string
		wantModel  string
		wantOK     bool
	}{
		{
			name:       "internal model",
			config:     ModelConfig{Internal: true, CredentialID: "cred1", InternalBaseURL: "https://api.example.com", InternalModel: "model-1"},
			wantCredID: "cred1",
			wantBase:   "https://api.example.com",
			wantModel:  "model-1",
			wantOK:     true,
		},
		{
			name:       "non-internal model",
			config:     ModelConfig{Internal: false},
			wantCredID: "",
			wantBase:   "",
			wantModel:  "",
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			credID, _, baseURL, model, ok := tt.config.GetInternalConfig()
			if ok != tt.wantOK {
				t.Errorf("GetInternalConfig() ok = %v, want %v", ok, tt.wantOK)
			}
			if credID != tt.wantCredID {
				t.Errorf("GetInternalConfig() credID = %v, want %v", credID, tt.wantCredID)
			}
			if baseURL != tt.wantBase {
				t.Errorf("GetInternalConfig() baseURL = %v, want %v", baseURL, tt.wantBase)
			}
			if model != tt.wantModel {
				t.Errorf("GetInternalConfig() model = %v, want %v", model, tt.wantModel)
			}
		})
	}
}

// =============================================================================
// ModelsConfig method tests
// =============================================================================

func TestNewModelsConfig(t *testing.T) {
	cfg := NewModelsConfig()
	if cfg == nil {
		t.Fatal("NewModelsConfig() returned nil")
	}
	if cfg.Models == nil {
		t.Error("NewModelsConfig().Models is nil")
	}
	if cfg.Credentials == nil {
		t.Error("NewModelsConfig().Credentials is nil")
	}
}

func TestModelsConfig_GetModel(t *testing.T) {
	cfg := &ModelsConfig{
		Models: []ModelConfig{
			{ID: "model-1", Name: "Model 1", Enabled: true},
			{ID: "model-2", Name: "Model 2", Enabled: false},
		},
	}

	tests := []struct {
		name    string
		modelID string
		wantNil bool
		wantID  string
	}{
		{"existing model", "model-1", false, "model-1"},
		{"second model", "model-2", false, "model-2"},
		{"non-existent", "model-3", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.GetModel(tt.modelID)
			if (got == nil) != tt.wantNil {
				t.Errorf("GetModel() = %v, want nil = %v", got, tt.wantNil)
			}
			if got != nil && got.ID != tt.wantID {
				t.Errorf("GetModel().ID = %v, want %v", got.ID, tt.wantID)
			}
		})
	}
}

func TestModelsConfig_GetFallbackChain(t *testing.T) {
	cfg := &ModelsConfig{
		Models: []ModelConfig{
			{ID: "model-1", Name: "Model 1", FallbackChain: []string{"fallback-1"}},
			{ID: "model-2", Name: "Model 2", FallbackChain: nil},
		},
	}

	tests := []struct {
		name      string
		modelID   string
		wantNil   bool
		wantChain []string
	}{
		{"with fallback", "model-1", false, []string{"model-1", "fallback-1"}},
		{"no fallback", "model-2", false, []string{"model-2"}},
		{"non-existent", "model-3", true, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.GetFallbackChain(tt.modelID)
			if (got == nil) != tt.wantNil {
				t.Errorf("GetFallbackChain() = %v, want nil = %v", got, tt.wantNil)
			}
			if !tt.wantNil {
				if len(got) != len(tt.wantChain) {
					t.Errorf("GetFallbackChain() len = %v, want %v", len(got), len(tt.wantChain))
				}
				for i, v := range tt.wantChain {
					if got[i] != v {
						t.Errorf("GetFallbackChain()[%d] = %v, want %v", i, got[i], v)
					}
				}
			}
		})
	}
}

func TestModelsConfig_GetTruncateParams(t *testing.T) {
	cfg := &ModelsConfig{
		Models: []ModelConfig{
			{ID: "model-1", Name: "Model 1", TruncateParams: []string{"max_tokens", "store"}},
			{ID: "model-2", Name: "Model 2", TruncateParams: nil},
		},
	}

	tests := []struct {
		name    string
		modelID string
		wantNil bool
		wantLen int
	}{
		{"with params", "model-1", false, 2},
		{"empty params", "model-2", true, 0},
		{"non-existent", "model-3", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.GetTruncateParams(tt.modelID)
			if (got == nil) != tt.wantNil {
				t.Errorf("GetTruncateParams() = %v, want nil = %v", got, tt.wantNil)
			}
			if got != nil && len(got) != tt.wantLen {
				t.Errorf("GetTruncateParams() len = %v, want %v", len(got), tt.wantLen)
			}
		})
	}
}

func TestModelsConfig_AddModel(t *testing.T) {
	tests := []struct {
		name    string
		model   ModelConfig
		wantErr bool
		errType error
	}{
		{
			name:    "valid model",
			model:   ModelConfig{ID: "new-model", Name: "New Model", Enabled: true},
			wantErr: false,
		},
		{
			name:    "empty ID",
			model:   ModelConfig{ID: "", Name: "Test", Enabled: true},
			wantErr: true,
			errType: ErrInvalidModelID,
		},
		{
			name:    "empty Name",
			model:   ModelConfig{ID: "test", Name: "", Enabled: true},
			wantErr: true,
			errType: ErrInvalidModelName,
		},
		{
			name:    "duplicate ID",
			model:   ModelConfig{ID: "existing", Name: "Existing Model", Enabled: true},
			wantErr: true,
			errType: ErrDuplicateModelID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ModelsConfig{
				Models: []ModelConfig{
					{ID: "existing", Name: "Existing", Enabled: true},
				},
				Credentials: NewCredentialsConfig(),
			}

			err := cfg.AddModel(tt.model)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddModel() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errType != nil && err != tt.errType {
				t.Errorf("AddModel() error type = %v, want %v", err, tt.errType)
			}
		})
	}
}

func TestModelsConfig_UpdateModel(t *testing.T) {
	tests := []struct {
		name       string
		modelID    string
		model      ModelConfig
		wantErr    bool
		errContain string
	}{
		{
			name:    "valid update",
			modelID: "model-1",
			model:   ModelConfig{ID: "model-1", Name: "Updated Model", Enabled: true},
			wantErr: false,
		},
		{
			name:       "empty ID",
			modelID:    "model-1",
			model:      ModelConfig{ID: "", Name: "Test"},
			wantErr:    true,
			errContain: "invalid model ID",
		},
		{
			name:       "empty Name",
			modelID:    "model-1",
			model:      ModelConfig{ID: "model-1", Name: ""},
			wantErr:    true,
			errContain: "invalid model name",
		},
		{
			name:       "change ID",
			modelID:    "model-1",
			model:      ModelConfig{ID: "new-id", Name: "Changed ID"},
			wantErr:    true,
			errContain: "cannot change model ID",
		},
		{
			name:       "non-existent",
			modelID:    "non-existent",
			model:      ModelConfig{ID: "non-existent", Name: "Not Found"},
			wantErr:    true,
			errContain: "model not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ModelsConfig{
				Models: []ModelConfig{
					{ID: "model-1", Name: "Model 1", Enabled: true},
				},
				Credentials: NewCredentialsConfig(),
			}

			err := cfg.UpdateModel(tt.modelID, tt.model)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateModel() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errContain != "" && (err == nil || !containsString(err.Error(), tt.errContain)) {
				t.Errorf("UpdateModel() error should contain %q, got %v", tt.errContain, err)
			}
		})
	}
}

func TestModelsConfig_RemoveModel(t *testing.T) {
	tests := []struct {
		name       string
		modelID    string
		wantErr    bool
		errContain string
	}{
		{"valid removal", "model-1", false, ""},
		{"non-existent", "non-existent", true, "model not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ModelsConfig{
				Models: []ModelConfig{
					{ID: "model-1", Name: "Model 1", Enabled: true},
				},
				Credentials: NewCredentialsConfig(),
			}

			err := cfg.RemoveModel(tt.modelID)
			if (err != nil) != tt.wantErr {
				t.Errorf("RemoveModel() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errContain != "" && (err == nil || !containsString(err.Error(), tt.errContain)) {
				t.Errorf("RemoveModel() error should contain %q, got %v", tt.errContain, err)
			}
		})
	}
}

func TestModelsConfig_GetEnabledModels(t *testing.T) {
	cfg := &ModelsConfig{
		Models: []ModelConfig{
			{ID: "model-1", Name: "Model 1", Enabled: true},
			{ID: "model-2", Name: "Model 2", Enabled: false},
			{ID: "model-3", Name: "Model 3", Enabled: true},
		},
	}

	got := cfg.GetEnabledModels()
	if len(got) != 2 {
		t.Errorf("GetEnabledModels() returned %d models, want 2", len(got))
	}
	for _, m := range got {
		if !m.Enabled {
			t.Errorf("GetEnabledModels() returned disabled model %s", m.ID)
		}
	}
}

func TestModelsConfig_GetModels(t *testing.T) {
	models := []ModelConfig{
		{ID: "model-1", Name: "Model 1"},
		{ID: "model-2", Name: "Model 2"},
	}
	cfg := &ModelsConfig{Models: models}

	got := cfg.GetModels()
	if len(got) != 2 {
		t.Errorf("GetModels() returned %d models, want 2", len(got))
	}

	// Verify it's a copy, not the original
	got[0].ID = "modified"
	if cfg.Models[0].ID != "model-1" {
		t.Error("GetModels() returned a reference, not a copy")
	}
}

// =============================================================================
// Credential management on ModelsConfig
// =============================================================================

func TestModelsConfig_GetCredential(t *testing.T) {
	cred := CredentialConfig{ID: "cred-1", Provider: "openai", APIKey: "test-key"}
	cfg := &ModelsConfig{
		Credentials: &CredentialsConfig{credentials: map[string]CredentialConfig{"cred-1": cred}},
	}

	tests := []struct {
		name    string
		id      string
		wantNil bool
	}{
		{"existing", "cred-1", false},
		{"non-existent", "cred-2", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.GetCredential(tt.id)
			if (got == nil) != tt.wantNil {
				t.Errorf("GetCredential() = %v, want nil = %v", got, tt.wantNil)
			}
		})
	}
}

func TestModelsConfig_GetCredentials(t *testing.T) {
	cfg := &ModelsConfig{
		Credentials: &CredentialsConfig{
			credentials: map[string]CredentialConfig{
				"cred-1": {ID: "cred-1", Provider: "openai", APIKey: "key1"},
				"cred-2": {ID: "cred-2", Provider: "anthropic", APIKey: "key2"},
			},
		},
	}

	got := cfg.GetCredentials()
	if len(got) != 2 {
		t.Errorf("GetCredentials() returned %d credentials, want 2", len(got))
	}
}

func TestModelsConfig_AddCredential(t *testing.T) {
	tests := []struct {
		name    string
		cred    CredentialConfig
		wantErr bool
	}{
		{
			name:    "valid credential",
			cred:    CredentialConfig{ID: "new-cred", Provider: "openai", APIKey: "test-key"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewModelsConfig()
			err := cfg.AddCredential(tt.cred)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddCredential() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestModelsConfig_UpdateCredential(t *testing.T) {
	initialCred := CredentialConfig{ID: "cred-1", Provider: "openai", APIKey: "original-key"}
	cfg := &ModelsConfig{
		Credentials: &CredentialsConfig{credentials: map[string]CredentialConfig{"cred-1": initialCred}},
	}

	tests := []struct {
		name    string
		id      string
		cred    CredentialConfig
		wantErr bool
	}{
		{
			name:    "valid update",
			id:      "cred-1",
			cred:    CredentialConfig{ID: "cred-1", Provider: "openai", APIKey: "updated-key"},
			wantErr: false,
		},
		{
			name:    "non-existent",
			id:      "non-existent",
			cred:    CredentialConfig{ID: "non-existent", Provider: "openai", APIKey: "key"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.UpdateCredential(tt.id, tt.cred)
			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateCredential() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestModelsConfig_RemoveCredential(t *testing.T) {
	initialCred := CredentialConfig{ID: "cred-1", Provider: "openai", APIKey: "key"}
	cfg := &ModelsConfig{
		Models: []ModelConfig{
			{ID: "model-1", Name: "Model 1", Internal: true, CredentialID: "cred-1", InternalModel: "model"},
		},
		Credentials: &CredentialsConfig{credentials: map[string]CredentialConfig{"cred-1": initialCred}},
	}

	tests := []struct {
		name       string
		id         string
		wantErr    bool
		errContain string
	}{
		{
			name:       "in use by model",
			id:         "cred-1",
			wantErr:    true,
			errContain: "credential is in use",
		},
		{
			name:       "non-existent",
			id:         "non-existent",
			wantErr:    true,
			errContain: "credential not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cfg.RemoveCredential(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("RemoveCredential() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errContain != "" && (err == nil || !containsString(err.Error(), tt.errContain)) {
				t.Errorf("RemoveCredential() error should contain %q, got %v", tt.errContain, err)
			}
		})
	}
}

// =============================================================================
// Load/Save tests
// =============================================================================

func TestModelsConfig_Load(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "models.json")

	// Create a test config file
	testConfig := struct {
		Models      []ModelConfig      `json:"models"`
		Credentials []CredentialConfig `json:"credentials"`
	}{
		Models: []ModelConfig{
			{ID: "model-1", Name: "Model 1", Enabled: true},
		},
		Credentials: []CredentialConfig{
			{ID: "cred-1", Provider: "openai", APIKey: "test-key"},
		},
	}

	data, _ := json.Marshal(testConfig)
	os.WriteFile(filePath, data, 0644)

	cfg := NewModelsConfig()
	err := cfg.Load(filePath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(cfg.Models) != 1 {
		t.Errorf("Load() Models len = %d, want 1", len(cfg.Models))
	}
	if cfg.Models[0].ID != "model-1" {
		t.Errorf("Load() Model.ID = %v, want model-1", cfg.Models[0].ID)
	}
}

func TestModelsConfig_Load_CreatesEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "nonexistent.json")

	cfg := NewModelsConfig()
	err := cfg.Load(filePath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// File should have been created
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("Load() should create the config file")
	}
}

func TestModelsConfig_Save(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "models.json")

	cfg := NewModelsConfig()
	// Load sets the internal filePath needed by Save
	if err := cfg.Load(filePath); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg.Models = append(cfg.Models, ModelConfig{ID: "model-1", Name: "Model 1", Enabled: true})

	// Add a credential to avoid validation issues
	cred := CredentialConfig{ID: "cred-1", Provider: "openai", APIKey: "test-key"}
	cfg.Credentials.AddCredential(cred)

	err := cfg.Save()
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Verify file exists and is valid JSON
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Save() did not create file: %v", err)
	}

	var loaded struct {
		Models []ModelConfig `json:"models"`
	}
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Saved file is not valid JSON: %v", err)
	}

	if len(loaded.Models) != 1 {
		t.Errorf("Saved Models len = %d, want 1", len(loaded.Models))
	}
}

func TestModelsConfig_Save_ValidationError(t *testing.T) {
	cfg := NewModelsConfig()
	cfg.Models = append(cfg.Models, ModelConfig{ID: "", Name: "Invalid"}) // Invalid: empty ID
	cfg.filePath = t.TempDir() + "/models.json"                           // Set a file path for Save()

	err := cfg.Save()
	if err == nil {
		t.Error("Save() should return error for invalid config")
	}
}

// =============================================================================
// ResolveInternalConfig tests
// =============================================================================

func TestModelsConfig_ResolveInternalConfig(t *testing.T) {
	cred := CredentialConfig{ID: "cred-1", Provider: "openai", APIKey: "test-key"}
	cfg := &ModelsConfig{
		Models: []ModelConfig{
			{ID: "model-1", Name: "Model 1", Internal: true, CredentialID: "cred-1", InternalModel: "gpt-4"},
		},
		Credentials: &CredentialsConfig{credentials: map[string]CredentialConfig{"cred-1": cred}},
	}

	tests := []struct {
		name      string
		modelID   string
		wantModel string
		wantOK    bool
	}{
		{"existing internal model", "model-1", "gpt-4", true},
		{"non-internal model", "model-2", "", false},
		{"non-existent model", "model-3", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, model, ok := cfg.ResolveInternalConfig(tt.modelID)
			if ok != tt.wantOK {
				t.Errorf("ResolveInternalConfig() ok = %v, want %v", ok, tt.wantOK)
			}
			if model != tt.wantModel {
				t.Errorf("ResolveInternalConfig() model = %v, want %v", model, tt.wantModel)
			}
		})
	}
}

// =============================================================================
// Helper functions
// =============================================================================

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
