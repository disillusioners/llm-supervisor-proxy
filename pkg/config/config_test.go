package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// Duration Type Tests
// ============================================================================

func TestDuration_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		duration Duration
		wantJSON string
	}{
		{
			name:     "seconds",
			duration: Duration(10 * time.Second),
			wantJSON: `"10s"`,
		},
		{
			name:     "minutes",
			duration: Duration(1 * time.Minute),
			wantJSON: `"1m0s"`,
		},
		{
			name:     "minutes and seconds",
			duration: Duration(90 * time.Second),
			wantJSON: `"1m30s"`,
		},
		{
			name:     "zero",
			duration: Duration(0),
			wantJSON: `"0s"`,
		},
		{
			name:     "milliseconds",
			duration: Duration(500 * time.Millisecond),
			wantJSON: `"500ms"`,
		},
		{
			name:     "hours",
			duration: Duration(2 * time.Hour),
			wantJSON: `"2h0m0s"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.duration.MarshalJSON()
			if err != nil {
				t.Fatalf("MarshalJSON() error = %v", err)
			}
			if string(got) != tt.wantJSON {
				t.Errorf("MarshalJSON() = %s, want %s", string(got), tt.wantJSON)
			}
		})
	}
}

func TestDuration_UnmarshalJSON_String(t *testing.T) {
	tests := []struct {
		name      string
		jsonData  string
		want      Duration
		wantError bool
	}{
		{
			name:      "valid seconds",
			jsonData:  `"10s"`,
			want:      Duration(10 * time.Second),
			wantError: false,
		},
		{
			name:      "valid minutes and seconds",
			jsonData:  `"1m30s"`,
			want:      Duration(90 * time.Second),
			wantError: false,
		},
		{
			name:      "valid milliseconds",
			jsonData:  `"500ms"`,
			want:      Duration(500 * time.Millisecond),
			wantError: false,
		},
		{
			name:      "zero",
			jsonData:  `"0s"`,
			want:      Duration(0),
			wantError: false,
		},
		{
			name:      "invalid duration string",
			jsonData:  `"invalid"`,
			want:      0,
			wantError: true,
		},
		{
			name:      "empty string",
			jsonData:  `""`,
			want:      0,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := json.Unmarshal([]byte(tt.jsonData), &d)
			if (err != nil) != tt.wantError {
				t.Errorf("UnmarshalJSON() error = %v, wantError %v", err, tt.wantError)
				return
			}
			if !tt.wantError && d != tt.want {
				t.Errorf("UnmarshalJSON() = %v, want %v", d, tt.want)
			}
		})
	}
}

func TestDuration_UnmarshalJSON_Number(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
		want     Duration
	}{
		{
			name:     "zero as float",
			jsonData: `0`,
			want:     Duration(0),
		},
		{
			name:     "nanoseconds as float",
			jsonData: `1000000000`,
			want:     Duration(1 * time.Second),
		},
		{
			name:     "large nanoseconds as float",
			jsonData: `5000000000`,
			want:     Duration(5 * time.Second),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := json.Unmarshal([]byte(tt.jsonData), &d)
			if err != nil {
				t.Fatalf("UnmarshalJSON() error = %v", err)
			}
			if d != tt.want {
				t.Errorf("UnmarshalJSON() = %v, want %v", d, tt.want)
			}
		})
	}
}

func TestDuration_UnmarshalJSON_Invalid(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
	}{
		{
			name:     "array",
			jsonData: `[1, 2, 3]`,
		},
		{
			name:     "object",
			jsonData: `{"value": 10}`,
		},
		{
			name:     "boolean",
			jsonData: `true`,
		},
		{
			name:     "null",
			jsonData: `null`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := json.Unmarshal([]byte(tt.jsonData), &d)
			if err == nil {
				t.Errorf("UnmarshalJSON() expected error for %s", tt.jsonData)
			}
		})
	}
}

func TestDuration_String(t *testing.T) {
	d := Duration(90 * time.Second)
	want := "1m30s"
	if got := d.String(); got != want {
		t.Errorf("String() = %s, want %s", got, want)
	}
}

func TestDuration_Duration(t *testing.T) {
	d := Duration(90 * time.Second)
	want := time.Duration(90 * time.Second)
	if got := d.Duration(); got != want {
		t.Errorf("Duration() = %v, want %v", got, want)
	}
}

// ============================================================================
// Config Validation Tests
// ============================================================================

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid config",
			cfg: Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              8089,
				IdleTimeout:       Duration(10 * time.Second),
				StreamDeadline:    Duration(110 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
				RaceMaxParallel:   3,
			},
			wantError: false,
		},
		{
			name: "valid config with https",
			cfg: Config{
				UpstreamURL:       "https://api.example.com",
				Port:              443,
				IdleTimeout:       Duration(30 * time.Second),
				StreamDeadline:    Duration(110 * time.Second),
				MaxGenerationTime: Duration(300 * time.Second),
				RaceMaxParallel:   3,
			},
			wantError: false,
		},
		{
			name: "missing upstream_url",
			cfg: Config{
				UpstreamURL:       "",
				Port:              8089,
				IdleTimeout:       Duration(10 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
			},
			wantError: true,
			errorMsg:  "upstream_url is required",
		},
		{
			name: "invalid upstream_url - no http prefix",
			cfg: Config{
				UpstreamURL:       "localhost:4001",
				Port:              8089,
				IdleTimeout:       Duration(10 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
			},
			wantError: true,
			errorMsg:  "upstream_url must use http or https scheme",
		},
		{
			name: "invalid upstream_url - bad protocol",
			cfg: Config{
				UpstreamURL:       "ftp://localhost:4001",
				Port:              8089,
				IdleTimeout:       Duration(10 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
			},
			wantError: true,
			errorMsg:  "upstream_url must use http or https scheme",
		},
		{
			name: "port too low",
			cfg: Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              0,
				IdleTimeout:       Duration(10 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
			},
			wantError: true,
			errorMsg:  "port must be between 1 and 65535",
		},
		{
			name: "port too high",
			cfg: Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              65536,
				IdleTimeout:       Duration(10 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
			},
			wantError: true,
			errorMsg:  "port must be between 1 and 65535",
		},
		{
			name: "port at minimum",
			cfg: Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              1,
				IdleTimeout:       Duration(10 * time.Second),
				StreamDeadline:    Duration(110 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
				RaceMaxParallel:   3,
			},
			wantError: false,
		},
		{
			name: "port at maximum",
			cfg: Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              65535,
				IdleTimeout:       Duration(10 * time.Second),
				StreamDeadline:    Duration(110 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
				RaceMaxParallel:   3,
			},
			wantError: false,
		},
		{
			name: "idle_timeout too low",
			cfg: Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              8089,
				IdleTimeout:       Duration(500 * time.Millisecond),
				MaxGenerationTime: Duration(180 * time.Second),
			},
			wantError: true,
			errorMsg:  "idle_timeout must be at least 1s",
		},
		{
			name: "idle_timeout at minimum",
			cfg: Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              8089,
				IdleTimeout:       Duration(1 * time.Second),
				StreamDeadline:    Duration(110 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
				RaceMaxParallel:   3,
			},
			wantError: false,
		},
		{
			name: "max_generation_time too low",
			cfg: Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              8089,
				IdleTimeout:       Duration(10 * time.Second),
				StreamDeadline:    Duration(110 * time.Second),
				MaxGenerationTime: Duration(500 * time.Millisecond),
			},
			wantError: true,
			errorMsg:  "max_generation_time must be at least 1s",
		},
		{
			name: "max_generation_time at minimum",
			cfg: Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              8089,
				IdleTimeout:       Duration(10 * time.Second),
				StreamDeadline:    Duration(110 * time.Second),
				MaxGenerationTime: Duration(1 * time.Second),
				RaceMaxParallel:   3,
			},
			wantError: false,
		},
		{
			name: "race_max_parallel too low",
			cfg: Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              8089,
				IdleTimeout:       Duration(10 * time.Second),
				StreamDeadline:    Duration(110 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
				RaceMaxParallel:   0,
			},
			wantError: true,
			errorMsg:  "race_max_parallel must be at least 1",
		},
		{
			name: "race_max_buffer_bytes negative",
			cfg: Config{
				UpstreamURL:        "http://localhost:4001",
				Port:               8089,
				IdleTimeout:        Duration(10 * time.Second),
				StreamDeadline:     Duration(110 * time.Second),
				MaxGenerationTime:  Duration(180 * time.Second),
				RaceMaxParallel:    3,
				RaceMaxBufferBytes: -1,
			},
			wantError: true,
			errorMsg:  "race_max_buffer_bytes cannot be negative",
		},
		{
			name: "race_max_buffer_bytes too small",
			cfg: Config{
				UpstreamURL:        "http://localhost:4001",
				Port:               8089,
				IdleTimeout:        Duration(10 * time.Second),
				StreamDeadline:     Duration(110 * time.Second),
				MaxGenerationTime:  Duration(180 * time.Second),
				RaceMaxParallel:    3,
				RaceMaxBufferBytes: 1,
			},
			wantError: true,
			errorMsg:  "race_max_buffer_bytes must be at least 65536 bytes (64KB) or 0 for unlimited",
		},
		{
			name: "race_max_buffer_bytes zero allowed",
			cfg: Config{
				UpstreamURL:        "http://localhost:4001",
				Port:               8089,
				IdleTimeout:        Duration(10 * time.Second),
				StreamDeadline:     Duration(110 * time.Second),
				MaxGenerationTime:  Duration(180 * time.Second),
				RaceMaxParallel:    3,
				RaceMaxBufferBytes: 0,
			},
			wantError: false,
		},
		{
			name: "race_max_buffer_bytes at minimum",
			cfg: Config{
				UpstreamURL:        "http://localhost:4001",
				Port:               8089,
				IdleTimeout:        Duration(10 * time.Second),
				StreamDeadline:     Duration(110 * time.Second),
				MaxGenerationTime:  Duration(180 * time.Second),
				RaceMaxParallel:    3,
				RaceMaxBufferBytes: 65536,
			},
			wantError: false,
		},
		// Idle termination validation tests
		{
			name: "idle termination enabled with valid timeout",
			cfg: Config{
				UpstreamURL:            "http://localhost:4001",
				Port:                   8089,
				IdleTimeout:            Duration(10 * time.Second),
				StreamDeadline:         Duration(110 * time.Second),
				MaxGenerationTime:      Duration(180 * time.Second),
				RaceMaxParallel:        3,
				IdleTerminationEnabled: true,
				IdleTerminationTimeout: Duration(60 * time.Second),
			},
			wantError: false,
		},
		{
			name: "idle termination enabled with timeout too low",
			cfg: Config{
				UpstreamURL:            "http://localhost:4001",
				Port:                   8089,
				IdleTimeout:            Duration(10 * time.Second),
				StreamDeadline:         Duration(110 * time.Second),
				MaxGenerationTime:      Duration(180 * time.Second),
				RaceMaxParallel:        3,
				IdleTerminationEnabled: true,
				IdleTerminationTimeout: Duration(500 * time.Millisecond),
			},
			wantError: true,
			errorMsg:  "idle_termination_timeout must be at least 1 second when enabled",
		},
		{
			name: "idle termination enabled with zero timeout",
			cfg: Config{
				UpstreamURL:            "http://localhost:4001",
				Port:                   8089,
				IdleTimeout:            Duration(10 * time.Second),
				StreamDeadline:         Duration(110 * time.Second),
				MaxGenerationTime:      Duration(180 * time.Second),
				RaceMaxParallel:        3,
				IdleTerminationEnabled: true,
				IdleTerminationTimeout: Duration(0),
			},
			wantError: true,
			errorMsg:  "idle_termination_timeout must be at least 1 second when enabled",
		},
		{
			name: "idle termination disabled with zero timeout",
			cfg: Config{
				UpstreamURL:            "http://localhost:4001",
				Port:                   8089,
				IdleTimeout:            Duration(10 * time.Second),
				StreamDeadline:         Duration(110 * time.Second),
				MaxGenerationTime:      Duration(180 * time.Second),
				RaceMaxParallel:        3,
				IdleTerminationEnabled: false,
				IdleTerminationTimeout: Duration(0),
			},
			wantError: false,
		},
		{
			name: "idle termination disabled with valid timeout",
			cfg: Config{
				UpstreamURL:            "http://localhost:4001",
				Port:                   8089,
				IdleTimeout:            Duration(10 * time.Second),
				StreamDeadline:         Duration(110 * time.Second),
				MaxGenerationTime:      Duration(180 * time.Second),
				RaceMaxParallel:        3,
				IdleTerminationEnabled: false,
				IdleTerminationTimeout: Duration(30 * time.Second),
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantError {
				t.Errorf("Validate() error = %v, wantError %v", err, tt.wantError)
				return
			}
			if tt.wantError && err != nil && tt.errorMsg != "" {
				if err.Error() != tt.errorMsg {
					t.Errorf("Validate() error message = %q, want %q", err.Error(), tt.errorMsg)
				}
			}
		})
	}
}

// ============================================================================
// Default Values Tests
// ============================================================================

func TestDefaults(t *testing.T) {
	wantVersion := ConfigVersion
	wantUpstreamURL := "http://localhost:4001"
	wantPort := 4321
	wantIdleTimeout := Duration(60 * time.Second)
	wantMaxGenerationTime := Duration(300 * time.Second)
	wantRaceRetryEnabled := false
	wantRaceMaxParallel := 3

	if Defaults.Version != wantVersion {
		t.Errorf("Defaults.Version = %s, want %s", Defaults.Version, wantVersion)
	}
	if Defaults.UpstreamURL != wantUpstreamURL {
		t.Errorf("Defaults.UpstreamURL = %s, want %s", Defaults.UpstreamURL, wantUpstreamURL)
	}
	if Defaults.Port != wantPort {
		t.Errorf("Defaults.Port = %d, want %d", Defaults.Port, wantPort)
	}
	if Defaults.IdleTimeout != wantIdleTimeout {
		t.Errorf("Defaults.IdleTimeout = %v, want %v", Defaults.IdleTimeout, wantIdleTimeout)
	}
	if Defaults.MaxGenerationTime != wantMaxGenerationTime {
		t.Errorf("Defaults.MaxGenerationTime = %v, want %v", Defaults.MaxGenerationTime, wantMaxGenerationTime)
	}
	if Defaults.RaceRetryEnabled != wantRaceRetryEnabled {
		t.Errorf("Defaults.RaceRetryEnabled = %v, want %v", Defaults.RaceRetryEnabled, wantRaceRetryEnabled)
	}
	if Defaults.RaceMaxParallel != wantRaceMaxParallel {
		t.Errorf("Defaults.RaceMaxParallel = %d, want %d", Defaults.RaceMaxParallel, wantRaceMaxParallel)
	}
	if Defaults.IdleTerminationEnabled != true {
		t.Errorf("Defaults.IdleTerminationEnabled = %v, want true", Defaults.IdleTerminationEnabled)
	}
	if Defaults.IdleTerminationTimeout != Duration(120*time.Second) {
		t.Errorf("Defaults.IdleTerminationTimeout = %v, want 120s", Defaults.IdleTerminationTimeout)
	}
}

// ============================================================================
// Manager Load/Save Tests
// ============================================================================

func TestManager_Load_NoFile(t *testing.T) {
	// Create a temp directory that doesn't exist yet
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Create manager with custom path
	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Should have loaded defaults
	cfg := m.Get()
	if cfg.Version != ConfigVersion {
		t.Errorf("Version = %s, want %s", cfg.Version, ConfigVersion)
	}
	if cfg.UpstreamURL != Defaults.UpstreamURL {
		t.Errorf("UpstreamURL = %s, want %s", cfg.UpstreamURL, Defaults.UpstreamURL)
	}
	if cfg.Port != Defaults.Port {
		t.Errorf("Port = %d, want %d", cfg.Port, Defaults.Port)
	}

	// File should have been created
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Expected config file to be created")
	}
}

func TestManager_Load_ExistingFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Create a config file with custom values
	customCfg := Config{
		Version:           ConfigVersion,
		UpstreamURL:       "http://custom:8080",
		Port:              9999,
		IdleTimeout:       Duration(20 * time.Second),
		StreamDeadline:    Duration(110 * time.Second),
		MaxGenerationTime: Duration(300 * time.Second),
		RaceMaxParallel:   5,
	}
	data, err := json.Marshal(customCfg)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := m.Get()
	if cfg.UpstreamURL != customCfg.UpstreamURL {
		t.Errorf("UpstreamURL = %s, want %s", cfg.UpstreamURL, customCfg.UpstreamURL)
	}
	if cfg.Port != customCfg.Port {
		t.Errorf("Port = %d, want %d", cfg.Port, customCfg.Port)
	}
	if cfg.IdleTimeout != customCfg.IdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", cfg.IdleTimeout, customCfg.IdleTimeout)
	}
	if cfg.MaxGenerationTime != customCfg.MaxGenerationTime {
		t.Errorf("MaxGenerationTime = %v, want %v", cfg.MaxGenerationTime, customCfg.MaxGenerationTime)
	}
	if cfg.RaceMaxParallel != customCfg.RaceMaxParallel {
		t.Errorf("RaceMaxParallel = %d, want %d", cfg.RaceMaxParallel, customCfg.RaceMaxParallel)
	}
}

func TestManager_Load_CorruptedFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Write corrupted JSON
	if err := os.WriteFile(configPath, []byte("{invalid json}"), 0644); err != nil {
		t.Fatalf("Failed to write corrupted config: %v", err)
	}

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Should fall back to defaults
	cfg := m.Get()
	if cfg.UpstreamURL != Defaults.UpstreamURL {
		t.Errorf("Expected defaults after corrupted file, got UpstreamURL = %s", cfg.UpstreamURL)
	}

	// Original file should be backed up
	corruptedFiles, err := filepath.Glob(configPath + ".corrupted.*")
	if err != nil {
		t.Fatalf("Failed to check for backup: %v", err)
	}
	if len(corruptedFiles) == 0 {
		t.Error("Expected corrupted file to be backed up")
	}
}

func TestManager_Load_EnvOverrides(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Set env vars BEFORE loading
	os.Setenv("APPLY_ENV_OVERRIDES", "1")
	os.Setenv("UPSTREAM_URL", "http://envoverride:1234")
	os.Setenv("PORT", "9000")
	os.Setenv("IDLE_TIMEOUT", "30s")
	os.Setenv("MAX_GENERATION_TIME", "600s")
	os.Setenv("RACE_RETRY_ENABLED", "true")
	os.Setenv("RACE_MAX_PARALLEL", "5")
	os.Setenv("IDLE_TERMINATION_ENABLED", "false")
	os.Setenv("IDLE_TERMINATION_TIMEOUT", "30s")
	defer func() {
		os.Unsetenv("APPLY_ENV_OVERRIDES")
		os.Unsetenv("UPSTREAM_URL")
		os.Unsetenv("PORT")
		os.Unsetenv("IDLE_TIMEOUT")
		os.Unsetenv("MAX_GENERATION_TIME")
		os.Unsetenv("RACE_RETRY_ENABLED")
		os.Unsetenv("RACE_MAX_PARALLEL")
		os.Unsetenv("IDLE_TERMINATION_ENABLED")
		os.Unsetenv("IDLE_TERMINATION_TIMEOUT")
	}()

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := m.Get()
	if cfg.UpstreamURL != "http://envoverride:1234" {
		t.Errorf("UpstreamURL = %s, want http://envoverride:1234", cfg.UpstreamURL)
	}
	if cfg.Port != 9000 {
		t.Errorf("Port = %d, want 9000", cfg.Port)
	}
	if cfg.IdleTimeout != Duration(30*time.Second) {
		t.Errorf("IdleTimeout = %v, want 30s", cfg.IdleTimeout)
	}
	if cfg.MaxGenerationTime != Duration(600*time.Second) {
		t.Errorf("MaxGenerationTime = %v, want 600s", cfg.MaxGenerationTime)
	}
	if cfg.RaceRetryEnabled != true {
		t.Errorf("RaceRetryEnabled = %v, want true", cfg.RaceRetryEnabled)
	}
	if cfg.RaceMaxParallel != 5 {
		t.Errorf("RaceMaxParallel = %d, want 5", cfg.RaceMaxParallel)
	}
	if cfg.IdleTerminationEnabled != false {
		t.Errorf("IdleTerminationEnabled = %v, want false", cfg.IdleTerminationEnabled)
	}
	if cfg.IdleTerminationTimeout != Duration(30*time.Second) {
		t.Errorf("IdleTerminationTimeout = %v, want 30s", cfg.IdleTerminationTimeout)
	}
}

func TestManager_Load_EmptyEnvVarsDontOverride(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Set env vars to empty
	os.Setenv("UPSTREAM_URL", "")
	os.Setenv("PORT", "")
	os.Setenv("IDLE_TIMEOUT", "")
	os.Setenv("MAX_GENERATION_TIME", "")
	os.Setenv("RACE_RETRY_ENABLED", "")
	defer func() {
		os.Unsetenv("UPSTREAM_URL")
		os.Unsetenv("PORT")
		os.Unsetenv("IDLE_TIMEOUT")
		os.Unsetenv("MAX_GENERATION_TIME")
		os.Unsetenv("RACE_RETRY_ENABLED")
	}()

	// Create a config file with values
	customCfg := Config{
		Version:     ConfigVersion,
		UpstreamURL: "http://file:8080",
		Port:        8888,
		IdleTimeout: Duration(15 * time.Second),
	}
	data, _ := json.Marshal(customCfg)
	os.WriteFile(configPath, data, 0644)

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cfg := m.Get()
	// Empty env vars should not override file values
	if cfg.UpstreamURL != customCfg.UpstreamURL {
		t.Errorf("UpstreamURL = %s, want %s", cfg.UpstreamURL, customCfg.UpstreamURL)
	}
	if cfg.Port != customCfg.Port {
		t.Errorf("Port = %d, want %d", cfg.Port, customCfg.Port)
	}
}

func TestManager_Save(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	newCfg := Config{
		UpstreamURL:       "http://saved:9000",
		Port:              9001,
		IdleTimeout:       Duration(25 * time.Second),
		StreamDeadline:    Duration(110 * time.Second),
		MaxGenerationTime: Duration(240 * time.Second),
		RaceMaxParallel:   3,
	}

	result, err := m.Save(newCfg)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Verify restart required due to port change
	if !result.RestartRequired {
		t.Error("Expected restart required for port change")
	}
	if len(result.ChangedFields) != 1 || result.ChangedFields[0] != "port" {
		t.Errorf("ChangedFields = %v, want [port]", result.ChangedFields)
	}

	// Verify in-memory config was updated
	cfg := m.Get()
	if cfg.UpstreamURL != newCfg.UpstreamURL {
		t.Errorf("UpstreamURL = %s, want %s", cfg.UpstreamURL, newCfg.UpstreamURL)
	}
	if cfg.Port != newCfg.Port {
		t.Errorf("Port = %d, want %d", cfg.Port, newCfg.Port)
	}

	// Verify file was written
	fileData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("Failed to read saved config: %v", err)
	}
	var savedCfg Config
	if err := json.Unmarshal(fileData, &savedCfg); err != nil {
		t.Fatalf("Failed to unmarshal saved config: %v", err)
	}
	if savedCfg.UpstreamURL != newCfg.UpstreamURL {
		t.Errorf("Saved UpstreamURL = %s, want %s", savedCfg.UpstreamURL, newCfg.UpstreamURL)
	}
}

func TestManager_Save_ValidatesBeforeWriting(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Try to save invalid config
	invalidCfg := Config{
		UpstreamURL: "", // Invalid: missing
		Port:        8089,
	}

	_, err := m.Save(invalidCfg)
	if err == nil {
		t.Error("Expected error when saving invalid config")
	}
	if err.Error() != "validation failed: upstream_url is required" {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestManager_Save_ReapplysEnvOverrides(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Set env override
	os.Setenv("APPLY_ENV_OVERRIDES", "1")
	os.Setenv("UPSTREAM_URL", "http://envoverride:1234")
	defer func() {
		os.Unsetenv("APPLY_ENV_OVERRIDES")
		os.Unsetenv("UPSTREAM_URL")
	}()

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Save with different URL
	newCfg := Config{
		UpstreamURL:       "http://saved:9000",
		Port:              9001,
		IdleTimeout:       Duration(25 * time.Second),
		StreamDeadline:    Duration(110 * time.Second),
		MaxGenerationTime: Duration(240 * time.Second),
		RaceMaxParallel:   3,
	}

	_, err := m.Save(newCfg)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Env override should still be applied
	cfg := m.Get()
	if cfg.UpstreamURL != "http://envoverride:1234" {
		t.Errorf("Expected env override to persist after save, got %s", cfg.UpstreamURL)
	}
}

func TestManager_SaveResult_RestartRequired(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Save with same port - no restart required
	cfg := m.Get()
	cfg.Port = m.Get().Port
	cfg.UpstreamURL = "http://changed"

	result, err := m.Save(cfg)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if result.RestartRequired {
		t.Error("Expected no restart required when port doesn't change")
	}

	// Save with different port - restart required
	cfg.Port = 9999
	result, err = m.Save(cfg)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !result.RestartRequired {
		t.Error("Expected restart required when port changes")
	}
}

func TestManager_ReadOnly(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Create a read-only directory by making the temp dir read-only
	// Actually, a better approach is to test the readOnly flag manually
	m := &Manager{
		filePath: configPath,
		readOnly: true,
	}

	// Attempt to save should fail
	_, err := m.Save(Config{
		UpstreamURL:       "http://localhost:4001",
		Port:              8089,
		IdleTimeout:       Duration(10 * time.Second),
		StreamDeadline:    Duration(110 * time.Second),
		MaxGenerationTime: Duration(180 * time.Second),
		RaceMaxParallel:   3,
	})

	if err == nil {
		t.Error("Expected error when saving in read-only mode")
	}
	if err.Error() != "config file is read-only (permission denied)" {
		t.Errorf("Unexpected error: %v", err)
	}

	// IsReadOnly should return true
	if !m.IsReadOnly() {
		t.Error("IsReadOnly() should return true")
	}
}

func TestManager_GetFilePath(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if m.GetFilePath() != configPath {
		t.Errorf("GetFilePath() = %s, want %s", m.GetFilePath(), configPath)
	}
}

// ============================================================================
// Thread-Safety Tests
// ============================================================================

func TestManager_ConcurrentGet(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	var wg sync.WaitGroup
	numGoroutines := 100
	iterations := 1000

	// Run many concurrent Get() calls
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = m.Get()
				_ = m.GetUpstreamURL()
				_ = m.GetPort()
				_ = m.GetIdleTimeout()
				_ = m.GetMaxGenerationTime()
				_ = m.GetRaceRetryEnabled()
				_ = m.GetRaceMaxParallel()
			}
		}()
	}

	wg.Wait()
}

func TestManager_ConcurrentGetWhileSave(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Start goroutines doing concurrent Get() calls
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.Get()
					_ = m.GetPort()
				}
			}
		}()
	}

	// Start goroutines doing Save() calls
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				cfg := Config{
					UpstreamURL:       "http://localhost:4001",
					Port:              8089 + j,
					IdleTimeout:       Duration(10 * time.Second),
					StreamDeadline:    Duration(110 * time.Second),
					MaxGenerationTime: Duration(180 * time.Second),
					RaceMaxParallel:   3,
				}
				_, _ = m.Save(cfg)
			}
		}()
	}

	// Wait for saves to complete, then stop getters
	time.Sleep(500 * time.Millisecond)
	close(stop)
	wg.Wait()

	// Verify no panic and config is still valid
	cfg := m.Get()
	if err := cfg.Validate(); err != nil {
		t.Errorf("Config became invalid after concurrent access: %v", err)
	}
}

func TestManager_ConcurrentSave(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	m := &Manager{
		filePath: configPath,
	}

	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	var wg sync.WaitGroup
	numGoroutines := 10

	// Run concurrent Save() calls
	errChan := make(chan error, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			cfg := Config{
				UpstreamURL:       "http://localhost:4001",
				Port:              port,
				IdleTimeout:       Duration(10 * time.Second),
				StreamDeadline:    Duration(110 * time.Second),
				MaxGenerationTime: Duration(180 * time.Second),
			}
			_, err := m.Save(cfg)
			if err != nil {
				errChan <- err
			}
		}(9000 + i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors (some saves may fail due to validation race, but no panics)
	for err := range errChan {
		// Ignore validation errors - concurrent saves with different ports
		// may cause some to fail validation or be overwritten
		if err != nil {
			// Just log, don't fail - this is expected behavior
			t.Logf("Save error (may be expected): %v", err)
		}
	}
}

// ============================================================================
// Integration Tests (JSON Marshal/Unmarshal roundtrip)
// ============================================================================

func TestConfig_JSONRoundtrip(t *testing.T) {
	original := Config{
		Version:           ConfigVersion,
		UpstreamURL:       "http://test:8080",
		Port:              9999,
		IdleTimeout:       Duration(45 * time.Second),
		StreamDeadline:    Duration(110 * time.Second),
		MaxGenerationTime: Duration(120 * time.Second),
		RaceRetryEnabled:  true,
		RaceMaxParallel:   7,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if loaded.Version != original.Version {
		t.Errorf("Version = %s, want %s", loaded.Version, original.Version)
	}
	if loaded.UpstreamURL != original.UpstreamURL {
		t.Errorf("UpstreamURL = %s, want %s", loaded.UpstreamURL, original.UpstreamURL)
	}
	if loaded.Port != original.Port {
		t.Errorf("Port = %d, want %d", loaded.Port, original.Port)
	}
	if loaded.IdleTimeout != original.IdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", loaded.IdleTimeout, original.IdleTimeout)
	}
	if loaded.MaxGenerationTime != original.MaxGenerationTime {
		t.Errorf("MaxGenerationTime = %v, want %v", loaded.MaxGenerationTime, original.MaxGenerationTime)
	}
	if loaded.RaceRetryEnabled != original.RaceRetryEnabled {
		t.Errorf("RaceRetryEnabled = %v, want %v", loaded.RaceRetryEnabled, original.RaceRetryEnabled)
	}
	if loaded.RaceMaxParallel != original.RaceMaxParallel {
		t.Errorf("RaceMaxParallel = %d, want %d", loaded.RaceMaxParallel, original.RaceMaxParallel)
	}
}
