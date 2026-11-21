package config

// imggen-groups commission / F-1 / D-17 — the image-gen GROUP WALK
// BUDGET knob (`image_gen_group_walk_timeout`).
//
// The knob is the TOTAL walk budget of an image-gen group request —
// the OUTER bound over the member walk. The per-member
// `image_gen_timeout` stays the INNER bound (a member attempt's ctx =
// min(remaining walk budget, ImageGenTimeout)).
//
// The three properties pinned here:
//   - the DEFAULT is 240s (= 2 × the per-member default),
//   - the CLAMP is [60s, 10min] (0 = "unset" → the handler seam
//     applies the default),
//   - the read is HOT: the getter reads the LIVE config, so a
//     PUT /fe/api/config (a Save) applies a new budget WITHOUT a
//     restart.
//
// No migration: the knob rides the existing config blob (the
// config_json column / config.json file) — the DB-backed manager
// marshals the whole Config struct.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The compile-time proof that the ManagerInterface carries the walk
// budget getter — the interface the PRODUCTION composition root binds
// (cmd/main.go: `var configMgr config.ManagerInterface`).
var _ ManagerInterface = (*Manager)(nil)

// TestDefaults_ImageGenGroupWalkTimeoutIs240s — the default walk
// budget is 240s (2 × the 120s per-member default).
func TestDefaults_ImageGenGroupWalkTimeoutIs240s(t *testing.T) {
	if Defaults.ImageGenGroupWalkTimeout != Duration(240*time.Second) {
		t.Errorf("Defaults.ImageGenGroupWalkTimeout = %v, want 240s", Defaults.ImageGenGroupWalkTimeout)
	}
}

// TestValidate_ImageGenGroupWalkTimeoutBounds — the clamp is
// [60s, 10min]; 0 is accepted (the handler seam degrades to the
// default).
func TestValidate_ImageGenGroupWalkTimeoutBounds(t *testing.T) {
	cases := []struct {
		name    string
		value   Duration
		wantErr bool
	}{
		{"unset falls back to the default", 0, false},
		{"below the lower bound", Duration(59 * time.Second), true},
		{"at the lower bound", Duration(60 * time.Second), false},
		{"mid range", Duration(240 * time.Second), false},
		{"at the upper bound", Duration(10 * time.Minute), false},
		{"above the upper bound", Duration(10*time.Minute + time.Second), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Defaults
			cfg.ImageGenGroupWalkTimeout = tc.value
			err := cfg.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Validate() with image_gen_group_walk_timeout=%v: expected an error, got nil", tc.value)
				}
				if !strings.Contains(err.Error(), "image_gen_group_walk_timeout") {
					t.Errorf("Validate() error = %q, want it to name image_gen_group_walk_timeout", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() with image_gen_group_walk_timeout=%v: unexpected error: %v", tc.value, err)
			}
		})
	}
}

// TestManager_GetImageGenGroupWalkTimeout_HotRead — the getter reads
// the LIVE config: a Save (what PUT /fe/api/config does) applies a
// new budget WITHOUT a restart, and the getter sees it immediately.
//
// The Manager is constructed with a temp file path (the package's own
// pattern) so the test never touches the real user config.
func TestManager_GetImageGenGroupWalkTimeout_HotRead(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	m := &Manager{filePath: configPath}
	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// The loaded default is the default walk budget.
	if got := m.GetImageGenGroupWalkTimeout(); got != 240*time.Second {
		t.Fatalf("GetImageGenGroupWalkTimeout() after Load = %v, want 240s", got)
	}

	// HOT: a Save (the config API's write path) applies the new value
	// to the live config — the next per-request read sees it, no
	// restart involved.
	cfg := m.Get()
	cfg.ImageGenGroupWalkTimeout = Duration(90 * time.Second)
	result, err := m.Save(cfg)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if result.RestartRequired {
		t.Errorf("Save() reported restart_required — the walk budget must be a HOT knob")
	}
	if got := m.GetImageGenGroupWalkTimeout(); got != 90*time.Second {
		t.Errorf("GetImageGenGroupWalkTimeout() after Save = %v, want 90s (the HOT read)", got)
	}

	// The value is persisted (the knob rides the existing config
	// blob — no migration, no new column).
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	var round map[string]interface{}
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal config file: %v", err)
	}
	if v, ok := round["image_gen_group_walk_timeout"]; !ok {
		t.Errorf("the persisted config has no image_gen_group_walk_timeout key: %v", round)
	} else if v != "1m30s" {
		t.Errorf("image_gen_group_walk_timeout = %v, want \"1m30s\"", v)
	}
}

// TestConfig_ImageGenGroupWalkTimeout_JSONRoundtrip — the JSON key is
// the documented one (`image_gen_group_walk_timeout`), and the value
// survives a marshal/unmarshal round-trip.
func TestConfig_ImageGenGroupWalkTimeout_JSONRoundtrip(t *testing.T) {
	cfg := Defaults
	cfg.ImageGenGroupWalkTimeout = Duration(300 * time.Second)

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"image_gen_group_walk_timeout":"5m0s"`) {
		t.Fatalf("the marshalled config has no image_gen_group_walk_timeout key: %s", string(data))
	}

	var back Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.ImageGenGroupWalkTimeout != Duration(300*time.Second) {
		t.Errorf("round-tripped image_gen_group_walk_timeout = %v, want 5m", back.ImageGenGroupWalkTimeout)
	}
}
