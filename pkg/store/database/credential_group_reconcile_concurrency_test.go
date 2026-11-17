package database

import (
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestCredentialGroup_Rebuild_DoesNotBlockRequestPath is the regression
// test for the reconcile-vs-request-path latency defect: the decorator's
// <=60s reconcile tick used to run RebuildCredentialGroupsFromDB under
// the store's WRITE lock (m.mu.Lock), and the store's write mutex is
// WRITER-PREFERRING — a pending/holding writer starves the m.mu.RLock()
// readers the LIVE request path takes (GetModelNative -> m.mu.RLock(),
// reached from handler_groups.go:68 / handler.go:712/:1160 /
// handler_anthropic.go:433).
//
// RED: against the current code the seam fires INSIDE m.mu.Lock() (the
// group scan runs under the write lock), so the test's m.mu.RLock()
// attempt blocks behind the pending writer -> the test FAILS with
// "did NOT complete within 500ms".
// GREEN: after the fix the read phase runs OUTSIDE the write lock, so
// the m.mu.RLock() is acquired immediately -> the test PASSES.
func TestCredentialGroup_Rebuild_DoesNotBlockRequestPath(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	// seed a group so the rebuild has something to scan
	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A", "cred-B"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	signalled := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	credentialGroupScanHook = func() {
		once.Do(func() { close(signalled) })
		<-release
	}
	defer func() {
		close(release)
		<-finished
		credentialGroupScanHook = nil
	}()

	go func() {
		defer close(finished)
		if err := mgr.RebuildCredentialGroupsFromDB(); err != nil {
			t.Errorf("RebuildCredentialGroupsFromDB: %v", err)
		}
	}()

	select {
	case <-signalled:
	case <-time.After(5 * time.Second):
		t.Fatal("the rebuild never reached the group-scan seam")
	}

	// The request path's reader takes m.mu.RLock() (GetModelNative).
	// It must be acquirable while the rebuild is inside its DB read.
	acquired := make(chan struct{})
	go func() {
		mgr.mu.RLock()
		mgr.mu.RUnlock()
		close(acquired)
	}()
	select {
	case <-acquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("m.mu.RLock() did NOT complete within 500ms while the rebuild was inside its DB read — the rebuild holds the store WRITE lock across the DB read (the request path is blocked)")
	}
}

// TestCredentialGroup_Rebuild_Unchanged_SkipsFanOut proves the early
// return: when the freshly-read snapshot is IDENTICAL to the cached one,
// the rebuild does NOT scan the models table and does NOT re-apply the
// engine state. The counter seam (credentialGroupRebuildFanOutHook) stays
// at zero.
//
// Anti-vacuity: a direct-SQL change to a group's membership makes the
// snapshot differ, so the rebuild DOES run the fan-out (counter > 0)
// and the engine state is refreshed.
func TestCredentialGroup_Rebuild_Unchanged_SkipsFanOut(t *testing.T) {
	mgr, store, cleanup := setupCredGroupManager(t)
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

	// First rebuild: the cache is already in sync with the DB -> the
	// snapshot compares equal -> early return -> the fan-out MUST NOT run.
	fanOutCalls := 0
	credentialGroupRebuildFanOutHook = func() { fanOutCalls++ }
	defer func() { credentialGroupRebuildFanOutHook = nil }()

	if err := mgr.RebuildCredentialGroupsFromDB(); err != nil {
		t.Fatalf("RebuildCredentialGroupsFromDB: %v", err)
	}
	if fanOutCalls != 0 {
		t.Fatalf("unchanged reconcile ran the fan-out %d time(s) — expected 0 (early return)", fanOutCalls)
	}

	// Direct-SQL membership edit: the DB now differs from the cache.
	var updateQuery string
	if store.Dialect == PostgreSQL {
		updateQuery = `UPDATE credential_groups SET members_json = '["cred-A","cred-B","cred-C"]', updated_at = NOW() WHERE id = 'g1'`
	} else {
		updateQuery = `UPDATE credential_groups SET members_json = '["cred-A","cred-B","cred-C"]', updated_at = datetime('now') WHERE id = 'g1'`
	}
	if _, err := store.DB.Exec(updateQuery); err != nil {
		t.Fatalf("direct-sql update: %v", err)
	}

	fanOutCalls = 0
	if err := mgr.RebuildCredentialGroupsFromDB(); err != nil {
		t.Fatalf("RebuildCredentialGroupsFromDB: %v", err)
	}
	if fanOutCalls == 0 {
		t.Fatal("changed reconcile did NOT run the fan-out — the early return is vacuous (the fan-out seam never fires)")
	}
}

// TestCredentialGroup_Rebuild_ProviderRead_OneQuery proves (c): the
// member-provider snapshot is read in a single round-trip for a
// multi-member group (not one round-trip per member). The counter seam
// (credentialGroupProviderQueryHook) must report exactly 1.
func TestCredentialGroup_Rebuild_ProviderRead_OneQuery(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A", "cred-B", "cred-C"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}

	roundTrips := 0
	credentialGroupProviderQueryHook = func(n int) { roundTrips = n }
	defer func() { credentialGroupProviderQueryHook = nil }()

	if err := mgr.RebuildCredentialGroupsFromDB(); err != nil {
		t.Fatalf("RebuildCredentialGroupsFromDB: %v", err)
	}
	if roundTrips != 1 {
		t.Fatalf("provider snapshot took %d round-trip(s) for a 4-member group — expected 1 (no N+1)", roundTrips)
	}
}

// TestCredentialGroup_RemoveGroup_PrunesMemberProviders proves O-8: after
// removing a group, its members (that no other group lists) are gone
// from the member-provider snapshot. A member shared with a surviving
// group keeps its entry.
func TestCredentialGroup_RemoveGroup_PrunesMemberProviders(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A", "cred-B"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup g1: %v", err)
	}
	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g2", Name: "G2", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup g2: %v", err)
	}

	if err := mgr.RemoveCredentialGroup("g1"); err != nil {
		t.Fatalf("RemoveCredentialGroup g1: %v", err)
	}

	mgr.groupsMu.RLock()
	defer mgr.groupsMu.RUnlock()
	if _, ok := mgr.groupMemberProviders["cred-B"]; ok {
		t.Fatal("cred-B (exclusive to the removed group g1) is still in the provider snapshot — O-8 prune missing")
	}
	if _, ok := mgr.groupMemberProviders["cred-A"]; !ok {
		t.Fatal("cred-A (shared with the surviving group g2) was pruned — O-8 over-pruned")
	}
}
