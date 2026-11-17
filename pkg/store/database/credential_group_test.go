package database

// credential-groups commission (phase1-plan.md WU-3) — manager CRUD,
// reverse lookups, the engine fan-out, the credential-write guards and
// the store-side validation wiring.
//
// Every test in this file asserts a CONCRETE artifact (a value, an
// error, a log line) — no zero-assert tests (phase1 anti-vacuous rule).

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// setupCredGroupManager builds a manager over a migrated SQLite store
// with four seeded credentials: cred-A / cred-B / cred-C (openai) and
// cred-OTHER (anthropic).
func setupCredGroupManager(t *testing.T) (*ModelsManager, *Store, func()) {
	t.Helper()

	store, cleanup := newStoreWithMigrations(t)
	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		cleanup()
		t.Fatalf("NewModelsManager: %v", err)
	}
	seedCredential(t, store, "cred-A", "openai")
	seedCredential(t, store, "cred-B", "openai")
	seedCredential(t, store, "cred-C", "openai")
	seedCredential(t, store, "cred-OTHER", "anthropic")
	return mgr, store, cleanup
}

// captureLog redirects the standard logger into a buffer for the
// duration of the test and returns it (the WARN assertions read it).
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })
	return &buf
}

// groupRef builds a group credential ref (the locked representation:
// credential_id empty, group_id set).
func groupRef(groupID string, weight int, position int) models.CredentialRef {
	return models.CredentialRef{GroupID: groupID, Weight: weight, Position: position}
}

// TestCredentialGroup_CRUD_RoundTrip covers the manager CRUD: add →
// read (legacy + strict) → update → remove, plus the ID-immutability
// and duplicate-id guards.
func TestCredentialGroup_CRUD_RoundTrip(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	group := models.CredentialGroupConfig{
		ID:       "g1",
		Name:     "OpenAI pool",
		Provider: "openai",
		Members:  []string{"cred-A", "cred-B"},
	}
	if err := mgr.AddCredentialGroup(group); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	got := mgr.GetCredentialGroup("g1")
	if got == nil {
		t.Fatal("GetCredentialGroup(g1) = nil, want the group")
	}
	if got.Name != "OpenAI pool" || got.Provider != "openai" {
		t.Fatalf("GetCredentialGroup fields = %+v, want name/provider preserved", got)
	}
	if len(got.Members) != 2 || got.Members[0] != "cred-A" || got.Members[1] != "cred-B" {
		t.Fatalf("GetCredentialGroup members = %v, want [cred-A cred-B] in stored order", got.Members)
	}

	all := mgr.GetCredentialGroups()
	if len(all) != 1 || all[0].ID != "g1" {
		t.Fatalf("GetCredentialGroups = %+v, want exactly [g1]", all)
	}

	strict, err := mgr.GetCredentialGroupStrict(context.Background(), "g1")
	if err != nil {
		t.Fatalf("GetCredentialGroupStrict: %v", err)
	}
	if strict == nil || strict.ID != "g1" {
		t.Fatalf("GetCredentialGroupStrict = %+v, want g1", strict)
	}
	strictAll, err := mgr.GetCredentialGroupsStrict(context.Background())
	if err != nil {
		t.Fatalf("GetCredentialGroupsStrict: %v", err)
	}
	if len(strictAll) != 1 {
		t.Fatalf("GetCredentialGroupsStrict len = %d, want 1", len(strictAll))
	}
	if _, err := mgr.GetCredentialGroupStrict(context.Background(), "g-missing"); !errors.Is(err, models.ErrCredentialGroupNotFound) {
		t.Fatalf("GetCredentialGroupStrict(missing) err = %v, want ErrCredentialGroupNotFound", err)
	}

	// ID immutability (the ErrCannotChangeCredentialID precedent).
	if err := mgr.UpdateCredentialGroup("g1", models.CredentialGroupConfig{ID: "g2", Name: "x", Provider: "openai"}); !errors.Is(err, models.ErrCannotChangeCredentialID) {
		t.Fatalf("UpdateCredentialGroup with a changed ID err = %v, want ErrCannotChangeCredentialID", err)
	}

	// Duplicate id is rejected (not silently overwritten).
	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{ID: "g1", Name: "dup", Provider: "openai"}); err == nil {
		t.Fatal("AddCredentialGroup with a duplicate ID returned nil error, want a rejection")
	}

	// Update: the member set changes.
	if err := mgr.UpdateCredentialGroup("g1", models.CredentialGroupConfig{
		ID:       "g1",
		Name:     "OpenAI pool v2",
		Provider: "openai",
		Members:  []string{"cred-A", "cred-C"},
	}); err != nil {
		t.Fatalf("UpdateCredentialGroup: %v", err)
	}
	got = mgr.GetCredentialGroup("g1")
	if got == nil || len(got.Members) != 2 || got.Members[1] != "cred-C" {
		t.Fatalf("GetCredentialGroup after update = %+v, want members [cred-A cred-C]", got)
	}
	if got.Name != "OpenAI pool v2" {
		t.Fatalf("GetCredentialGroup name after update = %q, want %q", got.Name, "OpenAI pool v2")
	}

	// Remove.
	if err := mgr.RemoveCredentialGroup("g1"); err != nil {
		t.Fatalf("RemoveCredentialGroup: %v", err)
	}
	if mgr.GetCredentialGroup("g1") != nil {
		t.Fatal("GetCredentialGroup after remove != nil, want nil")
	}
	if err := mgr.RemoveCredentialGroup("g1"); !errors.Is(err, models.ErrCredentialGroupNotFound) {
		t.Fatalf("RemoveCredentialGroup(missing) err = %v, want ErrCredentialGroupNotFound", err)
	}
}

// TestCredentialGroup_Flatten_MixedRefsDedupOrdering covers the flatten
// algorithm: expansion in place, weight/position inheritance and the
// dedup keep-first rule (phase1 §d.1).
func TestCredentialGroup_Flatten_MixedRefsDedupOrdering(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A", "cred-B"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	// [direct cred-C][group g1][direct cred-A] → the group expands IN
	// PLACE; cred-A is seen first inside the group expansion, so the
	// later direct ref is the duplicate that gets dropped.
	model := &models.ModelConfig{
		ID: "m-mixed",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-C", Weight: 3, Position: 0},
			groupRef("g1", 5, 1),
			{CredentialID: "cred-A", Weight: 7, Position: 2},
		},
	}
	flat := mgr.FlattenCredentialRefs(model)
	if len(flat) != 3 {
		t.Fatalf("flat len = %d (%+v), want 3", len(flat), flat)
	}
	wantIDs := []string{"cred-C", "cred-A", "cred-B"}
	for i, want := range wantIDs {
		if flat[i].CredentialID != want {
			t.Fatalf("flat[%d].CredentialID = %q, want %q (flat = %+v)", i, flat[i].CredentialID, want, flat)
		}
	}
	if flat[1].Weight != 5 || flat[1].Position != 1 {
		t.Fatalf("expanded member weight/position = %d/%d, want 5/1 (inherited from the group ref)", flat[1].Weight, flat[1].Position)
	}
	if flat[2].Weight != 5 || flat[2].Position != 1 {
		t.Fatalf("expanded member weight/position = %d/%d, want 5/1 (inherited from the group ref)", flat[2].Weight, flat[2].Position)
	}
	if flat[0].Weight != 3 || flat[0].Position != 0 {
		t.Fatalf("direct ref weight/position = %d/%d, want 3/0 (verbatim)", flat[0].Weight, flat[0].Position)
	}

	// Dedup keep-first: a direct ref that PRECEDES the group wins the
	// credential (the group's later duplicate is dropped).
	model2 := &models.ModelConfig{
		ID: "m-dedup",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-A", Weight: 9, Position: 0},
			groupRef("g1", 5, 1),
		},
	}
	flat2 := mgr.FlattenCredentialRefs(model2)
	if len(flat2) != 2 {
		t.Fatalf("dedup flat len = %d (%+v), want 2", len(flat2), flat2)
	}
	if flat2[0].CredentialID != "cred-A" || flat2[0].Weight != 9 {
		t.Fatalf("dedup keep-first broken: flat[0] = %+v, want cred-A with weight 9", flat2[0])
	}
	if flat2[1].CredentialID != "cred-B" {
		t.Fatalf("dedup flat[1] = %+v, want cred-B", flat2[1])
	}

	// A nil model / an empty list flatten to nil.
	if mgr.FlattenCredentialRefs(nil) != nil {
		t.Fatal("FlattenCredentialRefs(nil) != nil")
	}
	if mgr.FlattenCredentialRefs(&models.ModelConfig{ID: "m-empty"}) != nil {
		t.Fatal("FlattenCredentialRefs(empty) != nil")
	}
}

// TestCredentialGroup_Flatten_EmptyGroup covers the empty-group rule
// (G-8: an empty group is allowed and contributes nothing).
func TestCredentialGroup_Flatten_EmptyGroup(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{ID: "g-empty", Name: "Empty", Provider: "openai"}); err != nil {
		t.Fatalf("AddCredentialGroup(empty): %v", err)
	}

	model := &models.ModelConfig{ID: "m-empty", Credentials: []models.CredentialRef{groupRef("g-empty", 1, 0)}}
	flat := mgr.FlattenCredentialRefs(model)
	if len(flat) != 0 {
		t.Fatalf("flat = %+v, want an empty list for an empty group", flat)
	}

	// An empty group next to a direct ref contributes nothing but does
	// not break the direct ref.
	model2 := &models.ModelConfig{
		ID: "m-mixed-empty",
		Credentials: []models.CredentialRef{
			{CredentialID: "cred-A", Weight: 1, Position: 0},
			groupRef("g-empty", 1, 1),
		},
	}
	flat2 := mgr.FlattenCredentialRefs(model2)
	if len(flat2) != 1 || flat2[0].CredentialID != "cred-A" {
		t.Fatalf("flat = %+v, want exactly [cred-A]", flat2)
	}
}

// TestCredentialGroup_Flatten_MissingGroupFailsClosed covers T-13b(b):
// a missing group contributes nothing AND is WARN-logged with the group
// id (a silent skip would be a 502 with no diagnostic).
func TestCredentialGroup_Flatten_MissingGroupFailsClosed(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	buf := captureLog(t)

	model := &models.ModelConfig{ID: "m-missing", Credentials: []models.CredentialRef{groupRef("g-ghost", 1, 0)}}
	flat := mgr.FlattenCredentialRefs(model)
	if len(flat) != 0 {
		t.Fatalf("flat = %+v, want an empty list (fail closed)", flat)
	}
	if !strings.Contains(buf.String(), "g-ghost") {
		t.Fatalf("log = %q, want a WARN naming the missing group id 'g-ghost'", buf.String())
	}
	if !strings.Contains(buf.String(), "[WARN]") {
		t.Fatalf("log = %q, want a WARN line", buf.String())
	}
}

// TestCredentialGroup_Flatten_ProviderDivergenceSkipsMember covers A-8:
// a member whose provider diverges from the group's provider is skipped
// (never expanded) and WARN-logged; the remaining members expand.
func TestCredentialGroup_Flatten_ProviderDivergenceSkipsMember(t *testing.T) {
	mgr, store, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g-div", Name: "Divergent", Provider: "openai", Members: []string{"cred-A", "cred-B"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	// A direct-SQL provider edit bypasses the write-time validator
	// (T-17 blocks the in-process path) — the member now diverges.
	if _, err := store.DB.ExecContext(context.Background(),
		`UPDATE credentials SET provider = ? WHERE id = ?`, "anthropic", "cred-B"); err != nil {
		t.Fatalf("direct SQL provider edit: %v", err)
	}

	// The provider snapshot is refreshed on every rebuild (the
	// decorator's <=60s reconcile trigger / a restart).
	if err := mgr.RebuildCredentialGroupsFromDB(); err != nil {
		t.Fatalf("RebuildCredentialGroupsFromDB: %v", err)
	}

	buf := captureLog(t)
	model := &models.ModelConfig{ID: "m-div", Credentials: []models.CredentialRef{groupRef("g-div", 1, 0)}}
	flat := mgr.FlattenCredentialRefs(model)
	if len(flat) != 1 || flat[0].CredentialID != "cred-A" {
		t.Fatalf("flat = %+v, want exactly [cred-A] (the divergent member is skipped)", flat)
	}
	if !strings.Contains(buf.String(), "cred-B") {
		t.Fatalf("log = %q, want a WARN naming the skipped member 'cred-B'", buf.String())
	}
}

// TestCredentialGroup_FanOut_RefreshesEngineState covers T-13/T-36: a
// group write fans the flattened list out to EVERY referencing model, so
// the engine's selection set is fresh (no restart, no stale selection).
func TestCredentialGroup_FanOut_RefreshesEngineState(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	for _, id := range []string{"m1", "m2"} {
		if err := mgr.AddModel(models.ModelConfig{
			ID: id, Name: "M " + id, Enabled: true, Internal: true, InternalModel: "gpt-x",
			Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
		}); err != nil {
			t.Fatalf("AddModel(%s): %v", id, err)
		}
	}

	// One member → no failover alternative exists (for EVERY referencing
	// model).
	for _, id := range []string{"m1", "m2"} {
		if _, mode := mgr.Engine().ExcludeAndReselect(id, "conv-"+id, "cred-A", 0); mode != credentiallb.ReselectNone {
			t.Fatalf("ExcludeAndReselect(%s) mode = %v, want ReselectNone for a single-credential model", id, mode)
		}
	}

	// Grow the group → the fan-out must refresh the engine state of
	// every referencing model.
	if err := mgr.UpdateCredentialGroup("g1", models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A", "cred-B"},
	}); err != nil {
		t.Fatalf("UpdateCredentialGroup: %v", err)
	}

	for _, id := range []string{"m1", "m2"} {
		picked, mode := mgr.Engine().ExcludeAndReselect(id, "conv-"+id, "cred-A", 0)
		if mode != credentiallb.ReselectHealthy {
			t.Fatalf("ExcludeAndReselect(%s) mode = %v, want ReselectHealthy after the group grew", id, mode)
		}
		if picked != "cred-B" {
			t.Fatalf("ExcludeAndReselect(%s) picked %q, want cred-B (the new group member)", id, picked)
		}
	}
}

// TestCredentialGroup_FanOut_OnAdd covers A-15i: AddCredentialGroup
// fans out TOO. A model that referenced the group BEFORE it existed
// (the direct-SQL / DB-restore / R-1-repair shape) must get a fresh
// engine state from the Add.
func TestCredentialGroup_FanOut_OnAdd(t *testing.T) {
	mgr, store, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// The group disappears (direct SQL + rebuild) → the model has no
	// resolvable credential any more.
	if _, err := store.DB.ExecContext(context.Background(),
		`DELETE FROM credential_groups WHERE id = ?`, "g1"); err != nil {
		t.Fatalf("direct SQL group delete: %v", err)
	}
	if err := mgr.RebuildCredentialGroupsFromDB(); err != nil {
		t.Fatalf("RebuildCredentialGroupsFromDB: %v", err)
	}
	if _, mode := mgr.Engine().ExcludeAndReselect("m1", "conv-1", "cred-A", 0); mode != credentiallb.ReselectNone {
		t.Fatalf("ExcludeAndReselect mode = %v, want ReselectNone (no group → no credentials)", mode)
	}

	// Re-adding the group must fan out to the model that references it.
	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A", "cred-B"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup (re-add): %v", err)
	}
	picked, mode := mgr.Engine().ExcludeAndReselect("m1", "conv-2", "cred-A", 0)
	if mode != credentiallb.ReselectHealthy {
		t.Fatalf("ExcludeAndReselect mode = %v, want ReselectHealthy — AddCredentialGroup must fan out (A-15i)", mode)
	}
	if picked != "cred-B" {
		t.Fatalf("ExcludeAndReselect picked %q, want cred-B (the new group member)", picked)
	}
}

// TestCredentialGroup_RemoveWhileReferenced_Blocked covers T-13: the
// in-use guard blocks the delete and names the referencing models.
func TestCredentialGroup_RemoveWhileReferenced_Blocked(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	err := mgr.RemoveCredentialGroup("g1")
	if !errors.Is(err, models.ErrCredentialGroupInUse) {
		t.Fatalf("RemoveCredentialGroup err = %v, want ErrCredentialGroupInUse", err)
	}
	if !strings.Contains(err.Error(), "m1") {
		t.Fatalf("RemoveCredentialGroup err = %q, want it to name the referencing model 'm1'", err.Error())
	}
	if mgr.GetCredentialGroup("g1") == nil {
		t.Fatal("the group was deleted despite the in-use guard")
	}

	// Inline the members, drop the ref → the delete now succeeds.
	if err := mgr.UpdateModel("m1", models.ModelConfig{
		ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{{CredentialID: "cred-A", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("UpdateModel (inline members): %v", err)
	}
	if err := mgr.RemoveCredentialGroup("g1"); err != nil {
		t.Fatalf("RemoveCredentialGroup after inlining: %v", err)
	}
}

// TestCredentialGroup_RemoveCredential_BlockedByGroup covers T-16: a
// group-only credential cannot be deleted and the message names the
// group.
func TestCredentialGroup_RemoveCredential_BlockedByGroup(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	err := mgr.RemoveCredential("cred-A")
	if !errors.Is(err, models.ErrCredentialInUse) {
		t.Fatalf("RemoveCredential err = %v, want ErrCredentialInUse", err)
	}
	if !strings.Contains(err.Error(), "g1") {
		t.Fatalf("RemoveCredential err = %q, want it to name the credential group 'g1'", err.Error())
	}
	if !strings.Contains(err.Error(), "cred-A") {
		t.Fatalf("RemoveCredential err = %q, want it to name the credential 'cred-A'", err.Error())
	}
}

// TestCredentialGroup_RemoveCredential_BlockedByBothSurfaces covers the
// T-16 message shape when the credential is used by a model AND a group.
func TestCredentialGroup_RemoveCredential_BlockedByBothSurfaces(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{{CredentialID: "cred-A", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	err := mgr.RemoveCredential("cred-A")
	if !errors.Is(err, models.ErrCredentialInUse) {
		t.Fatalf("RemoveCredential err = %v, want ErrCredentialInUse", err)
	}
	if !strings.Contains(err.Error(), "m1") || !strings.Contains(err.Error(), "g1") {
		t.Fatalf("RemoveCredential err = %q, want it to name BOTH the model 'm1' and the group 'g1'", err.Error())
	}
}

// TestCredentialGroup_UpdateCredential_ProviderChangeBlocked covers T-17
// (plan-overview I-7): a provider change on a grouped credential is
// rejected and the message names the group.
func TestCredentialGroup_UpdateCredential_ProviderChangeBlocked(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	err := mgr.UpdateCredential("cred-A", models.CredentialConfig{ID: "cred-A", Provider: "anthropic", APIKey: "k"})
	if err == nil {
		t.Fatal("UpdateCredential with a provider change on a grouped credential returned nil error, want a rejection")
	}
	if !strings.Contains(err.Error(), "g1") {
		t.Fatalf("UpdateCredential err = %q, want it to name the credential group 'g1'", err.Error())
	}

	// The provider is unchanged in the DB (the write was blocked).
	if got := mgr.GetCredential("cred-A"); got == nil || got.Provider != "openai" {
		t.Fatalf("credential provider = %+v, want it unchanged (openai)", got)
	}
}

// TestCredentialGroup_StoreValidation_GroupRules covers the store-side
// validation wiring (T-9): the cross-provider member is rejected on the
// group write, a non-chat kind with a group ref is rejected through the
// store path, and a missing group ref is rejected.
func TestCredentialGroup_StoreValidation_GroupRules(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	// G-7: a member whose provider differs from the group's is rejected.
	err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g-bad", Name: "Bad", Provider: "openai", Members: []string{"cred-OTHER"},
	})
	if !errors.Is(err, models.ErrCredentialGroupProviderMismatch) {
		t.Fatalf("AddCredentialGroup(cross-provider) err = %v, want ErrCredentialGroupProviderMismatch", err)
	}

	// G-6: an unknown member is rejected.
	err = mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g-bad2", Name: "Bad2", Provider: "openai", Members: []string{"cred-NOPE"},
	})
	if !errors.Is(err, models.ErrCredentialGroupMemberNotFound) {
		t.Fatalf("AddCredentialGroup(unknown member) err = %v, want ErrCredentialGroupMemberNotFound", err)
	}

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	// R-5: non-chat kinds forbid group refs — through the STORE path.
	for _, kind := range []string{models.KindDecision, models.KindImageGen, models.KindGroup} {
		model := models.ModelConfig{
			ID: "m-" + kind, Name: "M " + kind, Enabled: true, Internal: true, InternalModel: "gpt-x",
			Kind:        kind,
			Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
		}
		err := mgr.AddModel(model)
		if err == nil {
			t.Fatalf("AddModel(kind=%s) with a group ref returned nil error, want a rejection", kind)
		}
		if !strings.Contains(err.Error(), "credential group refs are not allowed") {
			t.Fatalf("AddModel(kind=%s) err = %q, want the R-5 rejection", kind, err.Error())
		}
	}

	// R-3: a group ref that names a missing group is rejected.
	err = mgr.AddModel(models.ModelConfig{
		ID: "m-missing-group", Name: "M missing", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g-ghost", 1, 0)},
	})
	if err == nil {
		t.Fatal("AddModel with a missing group ref returned nil error, want a rejection")
	}
	if !strings.Contains(err.Error(), "g-ghost") {
		t.Fatalf("AddModel err = %q, want it to name the missing group 'g-ghost'", err.Error())
	}

	// The happy path still works (a chat model with a group ref).
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m-ok", Name: "M ok", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel(chat + group ref): %v", err)
	}
}

// TestCredentialGroup_ShadowColumn_EmptyForGroupPrimary covers T-18b /
// A-7: models.credential_id stays derived from PrimaryCredentialID() —
// for a group-ref-first model that is EMPTY, never the group id.
func TestCredentialGroup_ShadowColumn_EmptyForGroupPrimary(t *testing.T) {
	mgr, store, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m-group", Name: "M group", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	var shadow string
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT credential_id FROM models WHERE id = ?`, "m-group").Scan(&shadow); err != nil {
		t.Fatalf("read models.credential_id: %v", err)
	}
	if shadow != "" {
		t.Fatalf("models.credential_id = %q, want \"\" (legacy readers see no credential for a group-ref-first model)", shadow)
	}
	if shadow == "g1" {
		t.Fatal("models.credential_id holds the group id — the shadow must never bind a group id")
	}

	// A plain model still persists its primary credential id.
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m-plain", Name: "M plain", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{{CredentialID: "cred-A", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("AddModel(plain): %v", err)
	}
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT credential_id FROM models WHERE id = ?`, "m-plain").Scan(&shadow); err != nil {
		t.Fatalf("read models.credential_id (plain): %v", err)
	}
	if shadow != "cred-A" {
		t.Fatalf("models.credential_id (plain) = %q, want %q", shadow, "cred-A")
	}
}

// TestCredentialGroup_RebuildFromDB_ReplacesMap covers T-13b(a): a group
// written by DIRECT SQL is invisible to the flatten until the rebuild
// runs, and the rebuild REPLACES the map (never merges it).
func TestCredentialGroup_RebuildFromDB_ReplacesMap(t *testing.T) {
	mgr, store, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	// A direct-SQL group write (the R-1 repair shape).
	if _, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO credential_groups (id, name, provider, members_json, created_at, updated_at)
		 VALUES ('g-direct', 'Direct', 'openai', '["cred-B"]', datetime('now'), datetime('now'))`); err != nil {
		t.Fatalf("direct SQL group insert: %v", err)
	}
	// ... and a direct-SQL DELETE of the manager's own group.
	if _, err := store.DB.ExecContext(context.Background(),
		`DELETE FROM credential_groups WHERE id = ?`, "g1"); err != nil {
		t.Fatalf("direct SQL group delete: %v", err)
	}

	// Before the rebuild the store map is unchanged (the direct-SQL
	// group is invisible; the deleted one is still cached).
	model := &models.ModelConfig{ID: "m1", Credentials: []models.CredentialRef{groupRef("g-direct", 1, 0)}}
	if flat := mgr.FlattenCredentialRefs(model); len(flat) != 0 {
		t.Fatalf("flat = %+v, want empty — a direct-SQL group is invisible before the rebuild", flat)
	}

	if err := mgr.RebuildCredentialGroupsFromDB(); err != nil {
		t.Fatalf("RebuildCredentialGroupsFromDB: %v", err)
	}

	// The rebuild REPLACED the map: the new group is visible and the
	// deleted one is gone.
	flat := mgr.FlattenCredentialRefs(model)
	if len(flat) != 1 || flat[0].CredentialID != "cred-B" {
		t.Fatalf("flat after rebuild = %+v, want [cred-B]", flat)
	}
	deleted := &models.ModelConfig{ID: "m2", Credentials: []models.CredentialRef{groupRef("g1", 1, 0)}}
	if flat := mgr.FlattenCredentialRefs(deleted); len(flat) != 0 {
		t.Fatalf("flat after rebuild = %+v, want empty — a merge-based rebuild would keep the deleted group", flat)
	}
}

// TestCredentialGroup_LockDiscipline_NoDeadlock covers A-3: the group
// cache has its OWN lock, so a group write (which holds m.mu and takes
// groupsMu at the swap) can run concurrently with a model write and
// with a resolve (which takes groupsMu.RLock() only). The lock order is
// strictly m.mu → groupsMu; the reverse never occurs.
//
// The test FAILS FAST on a deadlock (a bounded wait, not a hang).
func TestCredentialGroup_LockDiscipline_NoDeadlock(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A", "cred-B"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 64)

	// Group writer: swaps the group map under groupsMu while holding m.mu.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			if err := mgr.UpdateCredentialGroup("g1", models.CredentialGroupConfig{
				ID: "g1", Name: "G1", Provider: "openai",
				Members: []string{"cred-A", "cred-B", "cred-C"},
			}); err != nil {
				errCh <- err
				return
			}
		}
	}()

	// Model writer: the engine-notify span holds m.mu and flattens.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			if err := mgr.UpdateModel("m1", models.ModelConfig{
				ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
				Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
			}); err != nil {
				errCh <- err
				return
			}
		}
	}()

	// Resolver: takes groupsMu.RLock() only (never m.mu).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			mgr.FlattenCredentialRefs(mgr.GetModelNative("m1"))
			mgr.ResolveInternalConfigWithAffinity("m1", "")
			mgr.GroupsContainingCredential("cred-A")
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the concurrent group write / model write / resolve did NOT finish within 20s — a lock-order deadlock (A-3)")
	}
	select {
	case err := <-errCh:
		t.Fatalf("concurrent writer failed: %v", err)
	default:
	}
}

// used_by map and the credential→group membership lookup.
func TestCredentialGroup_ReverseLookups(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A", "cred-B"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g2", Name: "G2", Provider: "openai", Members: []string{"cred-B"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m2", Name: "M2", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g1", 1, 0), groupRef("g2", 1, 1)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	used := mgr.ModelsUsingCredentialGroupsAll()
	if len(used["g1"]) != 2 {
		t.Fatalf("used_by[g1] = %v, want 2 models", used["g1"])
	}
	if len(used["g2"]) != 1 || used["g2"][0] != "m2" {
		t.Fatalf("used_by[g2] = %v, want [m2]", used["g2"])
	}
	if got := mgr.ModelsUsingCredentialGroup("g1"); len(got) != 2 {
		t.Fatalf("ModelsUsingCredentialGroup(g1) = %v, want 2 models", got)
	}
	if got := mgr.ModelsUsingCredentialGroup("g-none"); len(got) != 0 {
		t.Fatalf("ModelsUsingCredentialGroup(g-none) = %v, want empty", got)
	}

	// Membership lookup: cred-B is in both groups, cred-C in none.
	got := mgr.GroupsContainingCredential("cred-B")
	if len(got) != 2 || got[0] != "g1" || got[1] != "g2" {
		t.Fatalf("GroupsContainingCredential(cred-B) = %v, want [g1 g2] (sorted)", got)
	}
	if got := mgr.GroupsContainingCredential("cred-C"); len(got) != 0 {
		t.Fatalf("GroupsContainingCredential(cred-C) = %v, want empty", got)
	}
}
