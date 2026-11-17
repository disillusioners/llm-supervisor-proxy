package database

import (
	"context"
	"strconv"
	"strings"
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
	//
	// NOTE: the 500ms threshold below bounds RLock ACQUISITION (about
	// ~1ms uncontended), NOT the total test duration. The test FAILS
	// when acquisition exceeds the bound (the rebuild holds the write
	// lock across the DB read), not when the test as a whole runs long.
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

// TestCredentialGroup_ProviderRead_ChunkBoundaries proves the IN-query
// chunking at credentialIDQueryChunk: empty member set => zero
// round-trips, exactly 500 ids => exactly one trip, and 501 ids => exactly
// two trips (the second chunk holds a single id).
//
// The chunk size (500) is larger than MaxCredentialGroupMembers (16), so
// a single group cannot reach the boundary; the boundary is exercised
// directly through the seam's underlying reader (same package) with a
// constructed id slice. The hook still counts round-trips exactly as in
// the production rebuild path.
func TestCredentialGroup_ProviderRead_ChunkBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		idCount   int
		wantTrips int
	}{
		{"empty", 0, 0},
		{"exactly_500", 500, 1},
		{"501", 501, 2},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mgr, _, cleanup := setupCredGroupManager(t)
			defer cleanup()

			ids := make([]string, tc.idCount)
			for i := 0; i < tc.idCount; i++ {
				ids[i] = "cred-x-" + strconv.Itoa(i)
			}

			roundTrips := 0
			credentialGroupProviderQueryHook = func(n int) { roundTrips = n }
			defer func() { credentialGroupProviderQueryHook = nil }()

			if _, err := mgr.credentialProviderSnapshotForIDs(context.Background(), ids); err != nil {
				t.Fatalf("credentialProviderSnapshotForIDs: %v", err)
			}
			if roundTrips != tc.wantTrips {
				t.Fatalf("provider snapshot took %d round-trip(s) for %d ids — expected %d", roundTrips, tc.idCount, tc.wantTrips)
			}
		})
	}
}

// TestCredentialGroup_Rebuild_ProviderEdit_DirectSQL proves the
// member-provider half of the early-return comparison: a group whose
// rows are UNCHANGED but whose member credential's provider changes via
// direct SQL makes credentialGroupSnapshotChanged return true, drives the
// rebuild through the swap/fan-out (not the early return), and the A-8
// fail-closed flatten then skips the divergent member (WARN-logged)
// instead of expanding it with the stale provider.
func TestCredentialGroup_Rebuild_ProviderEdit_DirectSQL(t *testing.T) {
	mgr, store, cleanup := setupCredGroupManager(t)
	defer cleanup()

	// group provider openai; cred-A and cred-B are openai members.
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

	// Prime the cache (so the second rebuild has something to compare).
	if err := mgr.RebuildCredentialGroupsFromDB(); err != nil {
		t.Fatalf("prime rebuild: %v", err)
	}

	// Direct-SQL provider edit on a member credential: group rows untouched.
	var updateQuery string
	if store.Dialect == PostgreSQL {
		updateQuery = `UPDATE credentials SET provider = 'anthropic' WHERE id = 'cred-A'`
	} else {
		updateQuery = `UPDATE credentials SET provider = 'anthropic' WHERE id = 'cred-A'`
	}
	if _, err := store.DB.Exec(updateQuery); err != nil {
		t.Fatalf("direct-sql provider edit: %v", err)
	}

	// Read the freshly-persisted snapshot for the assertion (i).
	byID, providers, err := mgr.readCredentialGroupsSnapshot(context.Background())
	if err != nil {
		t.Fatalf("readCredentialGroupsSnapshot: %v", err)
	}
	if !mgr.credentialGroupSnapshotChanged(byID, providers) {
		t.Fatal("(i) credentialGroupSnapshotChanged returned false after a member provider edit — the member-provider half is missing")
	}

	// (ii) rebuild proceeds to swap/fan-out.
	fanOutCalls := 0
	credentialGroupRebuildFanOutHook = func() { fanOutCalls++ }
	defer func() { credentialGroupRebuildFanOutHook = nil }()
	buf := captureLog(t)

	if err := mgr.RebuildCredentialGroupsFromDB(); err != nil {
		t.Fatalf("RebuildCredentialGroupsFromDB: %v", err)
	}
	if fanOutCalls == 0 {
		t.Fatal("(ii) changed reconcile did NOT run the fan-out — the early return fired despite the provider edit")
	}

	// (iii) A-8 fail-closed: cred-A's new provider (anthropic) diverges
	// from the group provider (openai) -> skipped + WARN-logged on flatten.
	flat := mgr.FlattenCredentialRefs(&models.ModelConfig{
		ID: "m1", Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
	})

	for _, r := range flat {
		if r.CredentialID == "cred-A" {
			t.Fatal("(iii) cred-A was expanded despite its provider diverging from the group provider — fail-closed skipped-member path did not fire")
		}
	}
	foundB := false
	for _, r := range flat {
		if r.CredentialID == "cred-B" {
			foundB = true
		}
	}
	if !foundB {
		t.Fatal("(iii) cred-B (provider matches group) was not expanded — over-skipped")
	}
	if !strings.Contains(buf.String(), "provider") || !strings.Contains(buf.String(), "differs") {
		t.Fatalf("(iii) expected a WARN about the divergent provider; log was:\n%s", buf.String())
	}
}
