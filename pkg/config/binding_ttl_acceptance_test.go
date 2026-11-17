package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

// TestBindingTTL_ConfigIndependence proves the two knobs are INDEPENDENT
// — neither one can move, clobber, or mask the other:
//
//  1. Round-trip: a Config carrying BOTH knobs at DISTINCT values
//     survives marshal → unmarshal with both values intact (a
//     cross-wiring or a shared-field bug would show up here).
//  2. Per-knob Validate: the 30m default validates for both knobs,
//     and an out-of-range value is rejected for EACH knob on its own —
//     a bad GROUP knob is rejected even when the MODEL knob is valid,
//     and a bad MODEL knob is rejected even when the GROUP knob is
//     valid.
//
// The knob NAMES stay as specified (group_binding_ttl /
// model_binding_ttl); the `model_` prefix on the second knob refers to
// the CREDENTIAL-binding layer of the affinity stack and must not be
// confused with the model-layer skip-cache knob (model_skip_cache_ttl).
//
// Engine-level independence (SetBindingTTL on one engine does not touch
// the other) is covered by the engine packages' own tests.
func TestBindingTTL_ConfigIndependence(t *testing.T) {
	const groupTTL = 7 * time.Minute
	const modelTTL = 11 * time.Minute

	// (1) Round-trip with BOTH knobs set to distinct values.
	c := Defaults
	c.GroupBindingTTL = Duration(groupTTL)
	c.ModelBindingTTL = Duration(modelTTL)

	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Config
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.GroupBindingTTL != Duration(groupTTL) {
		t.Fatalf("group_binding_ttl lost in round-trip: %v (want %v)", back.GroupBindingTTL, groupTTL)
	}
	if back.ModelBindingTTL != Duration(modelTTL) {
		t.Fatalf("model_binding_ttl lost in round-trip: %v (want %v)", back.ModelBindingTTL, modelTTL)
	}
	// Independence: the two knobs carry their OWN values, not one value
	// mirrored into both fields.
	if back.GroupBindingTTL == back.ModelBindingTTL {
		t.Fatalf("the two knobs must survive with distinct values, both are %v", back.GroupBindingTTL)
	}

	// (2a) The 30m default validates for both knobs.
	def := Defaults
	if err := def.Validate(); err != nil {
		t.Fatalf("default config rejected: %v", err)
	}
	if def.GroupBindingTTL != Duration(30*time.Minute) || def.ModelBindingTTL != Duration(30*time.Minute) {
		t.Fatalf("defaults are not 30m/30m: %v / %v", def.GroupBindingTTL, def.ModelBindingTTL)
	}

	// (2b) Bad GROUP knob + good MODEL knob ⇒ rejected on the GROUP knob.
	badGroup := Defaults
	badGroup.GroupBindingTTL = Duration(30 * time.Second)
	badGroup.ModelBindingTTL = Duration(modelTTL)
	err = badGroup.Validate()
	if err == nil {
		t.Fatal("out-of-range group_binding_ttl was accepted")
	}
	if err.Error() != "group_binding_ttl must be at least 1m when set" {
		t.Fatalf("group knob rejection message = %q", err.Error())
	}

	// (2c) Good GROUP knob + bad MODEL knob ⇒ rejected on the MODEL knob.
	badModel := Defaults
	badModel.GroupBindingTTL = Duration(groupTTL)
	badModel.ModelBindingTTL = Duration(25 * time.Hour)
	err = badModel.Validate()
	if err == nil {
		t.Fatal("out-of-range model_binding_ttl was accepted")
	}
	if err.Error() != "model_binding_ttl must be at most 24h when set" {
		t.Fatalf("model knob rejection message = %q", err.Error())
	}
}

// TestConfig_BindingTTL_DefaultsAndValidation
func TestConfig_BindingTTL_DefaultsAndValidation(t *testing.T) {
	if Defaults.GroupBindingTTL != Duration(30*time.Minute) {
		t.Fatalf("default GroupBindingTTL=%v", Defaults.GroupBindingTTL)
	}
	if Defaults.ModelBindingTTL != Duration(30*time.Minute) {
		t.Fatalf("default ModelBindingTTL=%v", Defaults.ModelBindingTTL)
	}

	// Reject too-small / too-large for each field.
	for _, tc := range []struct {
		field string
		cfg   Config
		msg   string
	}{
		{"group_small", Config{GroupBindingTTL: Duration(30 * time.Second)}, "group_binding_ttl must be at least 1m when set"},
		{"group_large", Config{GroupBindingTTL: Duration(25 * time.Hour)}, "group_binding_ttl must be at most 24h when set"},
		{"model_small", Config{ModelBindingTTL: Duration(30 * time.Second)}, "model_binding_ttl must be at least 1m when set"},
		{"model_large", Config{ModelBindingTTL: Duration(25 * time.Hour)}, "model_binding_ttl must be at most 24h when set"},
	} {
		c := Defaults
		if tc.cfg.GroupBindingTTL != 0 {
			c.GroupBindingTTL = tc.cfg.GroupBindingTTL
		}
		if tc.cfg.ModelBindingTTL != 0 {
			c.ModelBindingTTL = tc.cfg.ModelBindingTTL
		}
		err := c.Validate()
		if err == nil || err.Error() != tc.msg {
			t.Fatalf("%s: err=%v want %q", tc.field, err, tc.msg)
		}
	}

	// Accept boundaries.
	for _, d := range []time.Duration{time.Minute, 24 * time.Hour} {
		c := Defaults
		c.GroupBindingTTL = Duration(d)
		c.ModelBindingTTL = Duration(d)
		if err := c.Validate(); err != nil {
			t.Fatalf("boundary %v rejected: %v", d, err)
		}
	}
}

// TestManager_Load_EnvOverride_BindingTTLs
func TestManager_Load_EnvOverride_BindingTTLs(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("GROUP_BINDING_TTL", "45m")
	t.Setenv("MODEL_BINDING_TTL", "90m")

	m := &Manager{filePath: configPath}
	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	cfg := m.Get()
	if cfg.GroupBindingTTL != Duration(45*time.Minute) {
		t.Errorf("GroupBindingTTL = %v, want 45m", cfg.GroupBindingTTL)
	}
	if cfg.ModelBindingTTL != Duration(90*time.Minute) {
		t.Errorf("ModelBindingTTL = %v, want 90m", cfg.ModelBindingTTL)
	}
}

// TestManager_Load_EnvOverride_BindingTTLs_Gated
func TestManager_Load_EnvOverride_BindingTTLs_Gated(t *testing.T) {
	// The gate is APPLY_ENV_OVERRIDES itself — clear it explicitly so
	// the test is hermetic even when the harness exports it.
	t.Setenv("APPLY_ENV_OVERRIDES", "")

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	t.Setenv("GROUP_BINDING_TTL", "45m")
	t.Setenv("MODEL_BINDING_TTL", "90m")

	m := &Manager{filePath: configPath}
	if err := m.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	cfg := m.Get()
	if cfg.GroupBindingTTL != Defaults.GroupBindingTTL {
		t.Errorf("GroupBindingTTL = %v, want default %v", cfg.GroupBindingTTL, Defaults.GroupBindingTTL)
	}
	if cfg.ModelBindingTTL != Defaults.ModelBindingTTL {
		t.Errorf("ModelBindingTTL = %v, want default %v", cfg.ModelBindingTTL, Defaults.ModelBindingTTL)
	}
}
