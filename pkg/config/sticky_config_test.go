package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStickyLastSuccess_DefaultOff pins the feature-gate default: OFF.
func TestStickyLastSuccess_DefaultOff(t *testing.T) {
	if Defaults.StickyLastSuccessEnabled {
		t.Fatal("StickyLastSuccessEnabled must default to false")
	}
	if Defaults.StickyLastSuccessEnabled != false {
		t.Fatal("Defaults.StickyLastSuccessEnabled must be false")
	}
}

// TestStickyLastSuccess_EnvOverride pins STICKY_LAST_SUCCESS_ENABLED.
func TestStickyLastSuccess_EnvOverride(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"1", true},
		{"true", true},
		{"false", false},
		{"0", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, "config.json")

			t.Setenv("APPLY_ENV_OVERRIDES", "1")
			t.Setenv("STICKY_LAST_SUCCESS_ENABLED", tc.value)

			m := &Manager{filePath: configPath}
			if err := m.Load(); err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := m.Get().StickyLastSuccessEnabled; got != tc.want {
				t.Fatalf("StickyLastSuccessEnabled = %v, want %v", got, tc.want)
			}
			if got := m.GetStickyLastSuccessEnabled(); got != tc.want {
				t.Fatalf("GetStickyLastSuccessEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestStickyLastSuccess_JSONRoundTrip pins the json key + persistence
// (the JSON backend saves the whole struct — zero merge logic there).
func TestStickyLastSuccess_JSONRoundTrip(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	t.Setenv("STICKY_LAST_SUCCESS_ENABLED", "")

	m := &Manager{filePath: configPath}
	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	cfg := m.Get()
	cfg.StickyLastSuccessEnabled = true
	if _, err := m.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	m2 := &Manager{filePath: configPath}
	if err := m2.Load(); err != nil {
		t.Fatalf("reload error = %v", err)
	}
	if !m2.Get().StickyLastSuccessEnabled {
		t.Fatal("StickyLastSuccessEnabled did not round-trip through config.json")
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	if !strings.Contains(string(raw), `"sticky_last_success_enabled"`) {
		t.Fatalf("json key sticky_last_success_enabled missing: %s", raw)
	}
}
