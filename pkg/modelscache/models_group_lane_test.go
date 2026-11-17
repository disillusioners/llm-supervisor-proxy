package modelscache

// decision-groups commission — the DECORATOR-THROUGH test for the
// GroupLane seam (the modelscache-decorator-forward-trap).
//
// The trap, exactly: production conf.ModelsConfig is
// *CachedModelsConfig (CACHE_LAYER_ENABLED defaults ON —
// cmd/main.go). The decorator WRAPS, it does not embed: without the
// GroupLane forward, every caller's structural assertion fails
// SILENTLY (the wrapper does not expose the inner manager's method),
// so a raw-manager repro test stays green while the PRODUCTION lane
// never sees a lane — a decision group is misrouted instead of
// served.
//
// The failure message therefore NAMES the trap, exactly like the
// ClearGroupMemberCooldown precedent
// (pkg/proxy/win_clear_winner_identity_test.go).

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

// newLaneTestStack builds a migrated SQLite stack + a raw models
// manager + a CACHED decorator over it, with one chat member, one
// decision member and two groups (a chat group and a decision group).
func newLaneTestStack(t *testing.T) (*CachedModelsConfig, *database.ModelsManager, *database.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "lane.db")
	t.Setenv("DATABASE_URL", "sqlite:"+dbPath)
	dbStore, err := database.NewConnection(context.Background())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { dbStore.Close() })
	if err := dbStore.RunMigrations(context.Background()); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	mgr, err := database.NewModelsManager(dbStore, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	t.Cleanup(mgr.Close)

	if err := mgr.AddCredential(models.CredentialConfig{ID: "cred-openai", Provider: "openai", APIKey: "sk-test"}); err != nil {
		t.Fatalf("AddCredential: %v", err)
	}
	for _, m := range []models.ModelConfig{
		{ID: "coding", Name: "Coding", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "glm-5", Credentials: models.TestRefs("cred-openai")},
		{ID: "dec-a", Name: "Decision A", Enabled: true, Kind: models.KindDecision, Internal: true, InternalModel: "gpt-5", Credentials: models.TestRefs("cred-openai")},
	} {
		if err := mgr.AddModel(m); err != nil {
			t.Fatalf("seed member %s: %v", m.ID, err)
		}
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "chat-group", Name: "Chat Group", Enabled: true, Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "coding", Weight: 1}},
	}); err != nil {
		t.Fatalf("seed chat group: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "decision-group", Name: "Decision Group", Enabled: true, Kind: models.KindGroup, Internal: true,
		Members: []models.GroupMember{{ModelID: "dec-a", Weight: 1}},
	}); err != nil {
		t.Fatalf("seed decision group: %v", err)
	}

	cached, err := NewCachedModelsConfig(mgr, Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	t.Cleanup(cached.Stop)
	return cached, mgr, dbStore
}

// TestCachedModelsConfig_GroupLane_ReachesInnerManager — the
// decorator-through proof: the lane answered through the CACHED
// config equals the lane answered by the raw manager.
//
// If the GroupLane forward is removed from the decorator, this test
// fails with the trap message.
func TestCachedModelsConfig_GroupLane_ReachesInnerManager(t *testing.T) {
	cached, mgr, _ := newLaneTestStack(t)

	for _, tc := range []struct {
		groupID string
		want    models.GroupLane
	}{
		{"chat-group", models.GroupLaneChat},
		{"decision-group", models.GroupLaneDecision},
	} {
		rawLane, rawOK := mgr.GroupLane(tc.groupID)
		cachedLane, cachedOK := cached.GroupLane(tc.groupID)
		if rawLane != tc.want || !rawOK {
			t.Fatalf("raw manager GroupLane(%q) = (%q, %v), want (%q, true)", tc.groupID, rawLane, rawOK, tc.want)
		}
		if cachedLane != tc.want || !cachedOK {
			t.Fatalf("GroupLane is a no-op through the decorator: cached.GroupLane(%q) = (%q, %v), want (%q, true) — the modelscache decorator forward is missing (production binds *CachedModelsConfig)",
				tc.groupID, cachedLane, cachedOK, tc.want)
		}
	}
}

// TestCachedModelsConfig_GroupLane_MixedGroup_ReturnsFalse — the
// decorator does NOT paper over a mixed group: the lane stays
// unresolvable through the cache exactly as it is on the raw manager.
func TestCachedModelsConfig_GroupLane_MixedGroup_ReturnsFalse(t *testing.T) {
	cached, mgr, dbStore := newLaneTestStack(t)

	// Write a MIXED group row directly (the hand-edited shape the
	// write-time validator rejects).
	membersJSON, err := json.Marshal([]models.GroupMember{{ModelID: "dec-a", Weight: 1}, {ModelID: "coding", Weight: 1}})
	if err != nil {
		t.Fatalf("marshal members: %v", err)
	}
	if _, err := dbStore.DB.ExecContext(context.Background(),
		"INSERT INTO models (id, name, enabled, internal, kind, members_json) VALUES (?, ?, ?, ?, ?, ?)",
		"mixed-group", "Mixed Group", true, true, models.KindGroup, string(membersJSON)); err != nil {
		t.Fatalf("raw mixed-group row: %v", err)
	}

	rawLane, rawOK := mgr.GroupLane("mixed-group")
	cachedLane, cachedOK := cached.GroupLane("mixed-group")
	if rawOK {
		t.Fatalf("the raw manager must not resolve a mixed group's lane, got %q", rawLane)
	}
	if cachedOK {
		t.Fatalf("the decorator must not paper over a mixed group: cached.GroupLane(mixed-group) = (%q, true), want ok=false", cachedLane)
	}
	if cachedLane != "" {
		t.Fatalf("an unresolvable lane must be the empty lane, got %q", cachedLane)
	}
}
