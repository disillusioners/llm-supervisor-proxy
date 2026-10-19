package grouplb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestEngine builds an engine with short TTLs so cooldown/expiry
// assertions never sleep long (the W8 constructor-arg discipline).
func newTestEngine(t *testing.T, ttl, sweep time.Duration) *Engine {
	t.Helper()
	e := NewEngine(ttl, sweep, 42, 50*time.Millisecond)
	t.Cleanup(e.Stop)
	return e
}

// refs2 returns a two-member ref list (a heavier + b).
func refs2() []MemberRef {
	return []MemberRef{
		{ModelID: "member-a", Weight: 2, Position: 0},
		{ModelID: "member-b", Weight: 1, Position: 1},
	}
}

func TestEngine_LazyState_NoBootWalk(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	if e.HasGroup("g1") {
		t.Fatal("fresh engine must hold no group state (Am15 lazy posture)")
	}
	// Unknown group ⇒ ErrNoMembers (not a panic, not a silent pick).
	if _, _, err := e.GetOrSelect("g1", "conv-1"); err != ErrNoMembers {
		t.Fatalf("GetOrSelect(unknown group) err = %v, want ErrNoMembers", err)
	}
	// Seed via OnGroupChanged (the manager's lazy-seed call) — now
	// HasGroup is true.
	e.OnGroupChanged("g1", refs2())
	if !e.HasGroup("g1") {
		t.Fatal("HasGroup must be true after OnGroupChanged seed")
	}
}

func TestEngine_StickyBindThenRebind(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", refs2())

	m1, newly1, err := e.GetOrSelect("g1", "conv-key")
	if err != nil || m1 == "" {
		t.Fatalf("first GetOrSelect: member=%q err=%v", m1, err)
	}
	if !newly1 {
		t.Fatal("first GetOrSelect on a fresh key must be newlyBound=true")
	}

	// Sticky: the same conversation re-picks the same member, NOT
	// newly bound.
	for i := 0; i < 10; i++ {
		m2, newly2, err := e.GetOrSelect("g1", "conv-key")
		if err != nil {
			t.Fatalf("sticky GetOrSelect: %v", err)
		}
		if m2 != m1 {
			t.Fatalf("sticky violated: got %q, want %q (iter %d)", m2, m1, i)
		}
		if newly2 {
			t.Fatal("in-TTL reuse must be newlyBound=false")
		}
	}

	// The OTHER member's key space is distinct (per-key bindings).
	other := "member-b"
	if m1 == "member-b" {
		other = "member-a"
	}
	if _, _, err := e.GetOrSelect("g1", "conv-key-b"); err != nil {
		t.Fatalf("second key: %v", err)
	}
	_ = other // (weight distribution covered by the selector test)
}

func TestEngine_EmptyConvKey_FreshPickNoBinding(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", refs2())

	for i := 0; i < 5; i++ {
		m, newly, err := e.GetOrSelect("g1", "")
		if err != nil {
			t.Fatalf("empty-key GetOrSelect: %v", err)
		}
		if m == "" {
			t.Fatal("empty-key pick returned no member")
		}
		if newly {
			t.Fatal("empty-key pick must be newlyBound=false (W-2: no binding stored)")
		}
	}
	if got := e.bindingsCountForTest("g1"); got != 0 {
		t.Fatalf("empty-key calls must store NO bindings; bindingsCount=%d", got)
	}
}

func TestEngine_SingleMember_FastPath(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("solo", []MemberRef{{ModelID: "only-member", Weight: 1}})

	m, newly, err := e.GetOrSelect("solo", "conv-key")
	if err != nil || m != "only-member" {
		t.Fatalf("n==1 fast path: member=%q err=%v", m, err)
	}
	if newly {
		t.Fatal("E-3 fast path must return newlyBound=false (no map writes)")
	}
	if got := e.bindingsCountForTest("solo"); got != 0 {
		t.Fatalf("E-3 fast path must not store bindings; got %d", got)
	}
}

func TestEngine_ExcludeAndReselect_CooldownFromRetryAfter(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", refs2())

	// Bind conv-key deterministically to member-a.
	e.InjectPreconditionStateForTest("g1", "conv-key", "member-a")

	pick, mode := e.ExcludeAndReselect("g1", "conv-key", "member-a", 30*time.Second)
	if mode != ReselectHealthy {
		t.Fatalf("mode = %v, want ReselectHealthy", mode)
	}
	if pick != "member-b" {
		t.Fatalf("reselect pick = %q, want the non-excluded member-b", pick)
	}
	// Cooldown seeded from retryAfter (30s — persisted beyond the
	// engine's 50ms default so the distinction is observable).
	until := e.cooldownUntil("g1", "member-a")
	if until.IsZero() {
		t.Fatal("excluded member must be cooling")
	}
	remaining := time.Until(until)
	if remaining < 25*time.Second {
		t.Fatalf("cooldown should be seeded from retryAfter (~30s), remaining=%v", remaining)
	}
	// The binding was REBOUND to member-b (rebind-only-if-bound).
	if m, _, err := e.GetOrSelect("g1", "conv-key"); err != nil || m != "member-b" {
		t.Fatalf("post-reselect binding: member=%q err=%v, want member-b", m, err)
	}
}

func TestEngine_ExcludeAndReselect_DefaultCooldownWhenNoRetryAfter(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", refs2())
	e.InjectPreconditionStateForTest("g1", "conv-key", "member-a")

	if _, mode := e.ExcludeAndReselect("g1", "conv-key", "member-a", 0); mode != ReselectHealthy {
		t.Fatalf("mode = %v, want ReselectHealthy", mode)
	}
	until := e.cooldownUntil("g1", "member-a")
	if until.IsZero() {
		t.Fatal("excluded member must be cooling")
	}
	// Engine built with defaultCooldown=50ms → the default path
	// expires quickly; asserting it is in the near future (not 30s+)
	// distinguishes default- from retryAfter-seeded.
	if rem := time.Until(until); rem > 5*time.Second {
		t.Fatalf("default cooldown should be the engine's 50ms default, remaining=%v", rem)
	}
}

func TestEngine_ExcludeAndReselect_SingleMember_NoCooldown(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("solo", []MemberRef{{ModelID: "only-member", Weight: 1}})
	e.InjectPreconditionStateForTest("solo", "conv-key", "only-member")

	pick, mode := e.ExcludeAndReselect("solo", "conv-key", "only-member", 0)
	if mode != ReselectNone || pick != "" {
		t.Fatalf("n==1 exclusion: (%q, %v), want (\"\", ReselectNone)", pick, mode)
	}
	if until := e.cooldownUntil("solo", "only-member"); !until.IsZero() {
		t.Fatal("n==1 exclusion must NOT write a cooldown (no alternative exists)")
	}
}

func TestEngine_ExcludeAndReselect_AllCooling_SoonestExpiry(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", refs2())
	e.InjectPreconditionStateForTest("g1", "conv-key", "member-b")

	// Cool member-a FIRST (its cooldown expiry is earliest), then
	// exclude member-b (all members now cooling).
	if _, mode := e.ExcludeAndReselect("g1", "conv-x", "member-a", 0); mode != ReselectHealthy {
		t.Fatalf("setup exclude: mode=%v", mode)
	}
	pick, mode := e.ExcludeAndReselect("g1", "conv-key", "member-b", 0)
	if mode != ReselectSoonestExpiry {
		t.Fatalf("mode = %v, want ReselectSoonestExpiry", mode)
	}
	// member-a cooled first ⇒ its cooldown expires soonest.
	if pick != "member-a" {
		t.Fatalf("soonest-expiry pick = %q, want member-a (cooled first)", pick)
	}
	// Selection-only: no binding stored for the soonest pick.
	if m, _, _ := e.GetOrSelect("g1", "conv-key"); m != "member-a" {
		t.Fatalf("all-cooling selection-only pick: got %q, want member-a", m)
	}
}

func TestEngine_ExcludeAndReselect_B2NoOpWhenRebindAlreadyHappened(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", refs2())
	// Binding already points at member-b (a concurrent request
	// rebinded away from member-a).
	e.InjectPreconditionStateForTest("g1", "conv-key", "member-b")

	pick, mode := e.ExcludeAndReselect("g1", "conv-key", "member-a", 0)
	if mode != ReselectHealthy {
		t.Fatalf("mode = %v, want ReselectHealthy (B2 no-op)", mode)
	}
	if pick != "member-b" {
		t.Fatalf("B2 no-op must return the unchanged binding member-b, got %q", pick)
	}
	// The excluded member is STILL cooling (it genuinely failed).
	if until := e.cooldownUntil("g1", "member-a"); until.IsZero() {
		t.Fatal("B2 no-op still marks the excluded member cooling")
	}
}

func TestEngine_OnGroupChanged_SurvivorFilter(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", []MemberRef{
		{ModelID: "member-a", Weight: 1},
		{ModelID: "member-b", Weight: 1},
	})
	e.InjectPreconditionStateForTest("g1", "conv-stays", "member-a")
	e.InjectPreconditionStateForTest("g1", "conv-drops", "member-b")

	// member-b removed — its binding drops; member-a's survives with
	// boundAt intact (weight nudges keep affinity).
	before, _ := e.bindingBoundAtForTest("g1", "conv-stays")

	e.OnGroupChanged("g1", []MemberRef{{ModelID: "member-a", Weight: 5}})

	if got := e.bindingsCountForTest("g1"); got != 1 {
		t.Fatalf("survivor filter: bindingsCount=%d, want 1 (orphan dropped)", got)
	}
	if _, ok := e.bindingBoundAtForTest("g1", "conv-drops"); ok {
		t.Fatal("binding to removed member must be dropped")
	}
	after, okAfter := e.bindingBoundAtForTest("g1", "conv-stays")
	if !okAfter || !before.Equal(after) {
		t.Fatalf("surviving binding must keep boundAt (%v → %v, ok=%v)", before, after, okAfter)
	}

	// Empty refs (group removed / member-less) ⇒ state deleted.
	e.OnGroupChanged("g1", nil)
	if e.HasGroup("g1") {
		t.Fatal("OnGroupChanged(nil) must delete the group state")
	}
	if _, _, err := e.GetOrSelect("g1", "conv-stays"); err != ErrNoMembers {
		t.Fatalf("post-removal GetOrSelect err = %v, want ErrNoMembers", err)
	}
}

func TestEngine_JanitorTTLSweep(t *testing.T) {
	// Short idle TTL (15ms) + manual sweep — deterministic, no
	// sleeping on the janitor ticker.
	e := newTestEngine(t, 15*time.Millisecond, time.Hour)
	e.OnGroupChanged("g1", refs2())
	e.InjectPreconditionStateForTest("g1", "conv-key", "member-a")

	// In-TTL: nothing swept.
	e.sweepOnceForTest()
	if got := e.bindingsCountForTest("g1"); got != 1 {
		t.Fatalf("in-TTL sweep must keep the binding; got %d", got)
	}

	// Idle beyond TTL (bounded sleep < the 50ms cooldown; still
	// deterministic for the BINDING sweep — boundAt is 15ms old).
	time.Sleep(20 * time.Millisecond)
	e.sweepOnceForTest()
	if got := e.bindingsCountForTest("g1"); got != 0 {
		t.Fatalf("idle-expired binding must be swept; got %d", got)
	}
}

func TestEngine_StopIdempotent(t *testing.T) {
	e := NewEngine(time.Hour, time.Hour, 1, time.Minute)
	e.Stop()
	e.Stop() // second call must not panic or block
	e.Stop()
	// Post-Stop GetOrSelect still works (lazy expiry only).
	e.OnGroupChanged("g1", refs2())
	if _, _, err := e.GetOrSelect("g1", "conv"); err != nil {
		t.Fatalf("post-Stop GetOrSelect: %v", err)
	}
}

func TestEngine_ConcurrentGetOrSelect(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", []MemberRef{
		{ModelID: "member-a", Weight: 1},
		{ModelID: "member-b", Weight: 1},
		{ModelID: "member-c", Weight: 1},
	})

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			key := "conv-worker"
			for i := 0; i < 50; i++ {
				m, _, err := e.GetOrSelect("g1", key)
				if err != nil {
					t.Errorf("worker %d: GetOrSelect: %v", w, err)
					return
				}
				if m == "" {
					t.Errorf("worker %d: empty pick", w)
					return
				}
			}
		}(w)
	}
	// Concurrent membership edits (live-edit safety under race).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			e.OnGroupChanged("g1", refs2())
			e.OnGroupChanged("g1", []MemberRef{
				{ModelID: "member-a", Weight: 1},
				{ModelID: "member-b", Weight: 1},
				{ModelID: "member-c", Weight: 1},
			})
		}
	}()
	wg.Wait()
}

func TestEngine_BoundAtRefreshedOnHit(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", refs2())
	e.InjectPreconditionStateForTest("g1", "conv-key", "member-a")

	before, ok := e.bindingBoundAtForTest("g1", "conv-key")
	if !ok {
		t.Fatal("precondition binding missing")
	}
	time.Sleep(2 * time.Millisecond)
	if _, _, err := e.GetOrSelect("g1", "conv-key"); err != nil {
		t.Fatalf("hit: %v", err)
	}
	after, ok := e.bindingBoundAtForTest("g1", "conv-key")
	if !ok {
		t.Fatal("binding vanished on hit")
	}
	if !after.After(before) {
		t.Fatalf("in-TTL hit must REFRESH boundAt (#10): %v → %v", before, after)
	}
}

func TestEngine_NilReceiverSafe(t *testing.T) {
	var e *Engine
	if _, _, err := e.GetOrSelect("g", "k"); err != ErrNoMembers {
		t.Fatal("nil GetOrSelect must return ErrNoMembers")
	}
	if _, mode := e.ExcludeAndReselect("g", "k", "m", 0); mode != ReselectNone {
		t.Fatal("nil ExcludeAndReselect must return ReselectNone")
	}
	e.OnGroupChanged("g", refs2()) // no-op
	e.Stop()                       // no-op
	if e.HasGroup("g") {
		t.Fatal("nil engine has no groups")
	}
}

// TestEngine_ZeroCredentialImportGuard — Am31 anti-pattern pin: the
// package source must never import credential/provider-aware
// packages. Greps the package directory for the banned import paths
// (pkg/models credential types, pkg/providers, and any credentiallb
// import beyond the sanctioned key.go helper — the engine itself
// must not know credentiallb at all).
func TestEngine_ZeroCredentialImportGuard(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	banned := []string{
		`"github.com/disillusioners/llm-supervisor-proxy/pkg/models"`,
		`"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"`,
		`"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"`,
		`credentiallb.`, // any credentiallb symbol use at all
	}
	for _, ent := range entries {
		name := ent.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(wd, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(raw)
		// Scan only non-comment lines: Am31 bans credential/provider
		// IMPORTS and symbol USES, not prose references in doc
		// comments (the package doc deliberately cites the clone
		// source).
		var code strings.Builder
		for _, line := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			code.WriteString(line)
			code.WriteString("\n")
		}
		for _, b := range banned {
			if strings.Contains(code.String(), b) {
				t.Errorf("%s contains banned credential/provider reference %q (Am31: zero credential awareness)", name, b)
			}
		}
	}
}

// ─── group-backup-members / D1, D2, D5 behavioural tests ───────────────────
//
// (a) backup never picked while a default can serve;
// (b) backup picked only when defaults exhausted;
// (c) backup pool respects weights + sticky among themselves;
// (d) multi-backup (≥2 backups; exclusion chain across backups).
//
// The (e)/(f) legs live in pkg/proxy (existing suite must remain
// green) and pkg/models (D2 validation).

// defaultPlusBackupRefs builds a 2-default + 1-backup member list
// for the (a)/(b)/(c) tests. The backup carries weight 1 to mirror
// the defaults' weight 1 — distribution is therefore uniform
// within each pool; binding semantics carry the affinity.
func defaultPlusBackupRefs() []MemberRef {
	return []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "default-b", Weight: 1, Position: 1},
		{ModelID: "backup-x", Weight: 1, Position: 2, BackupOnly: true},
	}
}

// onlyBackupsRefs builds a 3-backup member list with no defaults
// for the (d) test. Engine tests bypass the write-path D2
// validation deliberately (D2 lives at models.ValidateGroupMembers;
// the engine is the picker, not the validator).
func onlyBackupsRefs() []MemberRef {
	return []MemberRef{
		{ModelID: "backup-a", Weight: 2, Position: 0, BackupOnly: true},
		{ModelID: "backup-b", Weight: 1, Position: 1, BackupOnly: true},
		{ModelID: "backup-c", Weight: 1, Position: 2, BackupOnly: true},
	}
}

// (a) backup never picked while a default can serve — over many
// GetOrSelect calls with distinct conversation keys (so every call
// is a fresh pick), the result is always a default member. The
// backup pool's `backupSelector` is built and HasBackupPool is
// true, but GetOrSelect never touches it. PickBackupMember works
// independently because the backup pool exists.
func TestBackup_NeverPickedWhileDefaultCanServe(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", defaultPlusBackupRefs())
	if !e.HasBackupPool("g") {
		t.Fatal("HasBackupPool must be true for a group with a backup member")
	}

	for i := 0; i < 200; i++ {
		key := fmt.Sprintf("conv-a-%d", i)
		m, _, err := e.GetOrSelect("g", key)
		if err != nil {
			t.Fatalf("GetOrSelect[%d]: %v", i, err)
		}
		if m == "backup-x" {
			t.Fatalf("iteration %d: GetOrSelect returned the backup member %q while defaults existed", i, m)
		}
		if m != "default-a" && m != "default-b" {
			t.Fatalf("iteration %d: unexpected pick %q (want default-a|default-b)", i, m)
		}
	}

	// PickBackupMember DOES reach the backup pool — proving the
	// partition is correctly populated, not silently dropped.
	bp, _, err := e.PickBackupMember("g", "conv-a-backup")
	if err != nil {
		t.Fatalf("PickBackupMember: %v", err)
	}
	if bp != "backup-x" {
		t.Fatalf("PickBackupMember pick = %q, want backup-x (the only configured backup)", bp)
	}
}

// (b) backup picked only when defaults exhausted. With every
// default in cooldown (InjectAllCoolingForTest minus the backup),
// GetOrSelect still picks a default (the all-cooling soonest-expiry
// path is a per-pool check, so the default pool's soonest-expiry
// returns one of the cooling defaults), and a direct
// PickBackupMember yields the backup with its own sticky binding.
//
// More importantly: when ALL defaults are stuck (e.g. degraded + a
// stale re-validation cycles them through ExcludeAndReselect), the
// call flow returns the backup via PickBackupMember.
func TestBackup_PickedWhenDefaultsExhausted(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", defaultPlusBackupRefs())

	// Defaults are all live — must NOT pick a backup.
	m1, _, err := e.GetOrSelect("g", "conv-b-default")
	if err != nil {
		t.Fatalf("first GetOrSelect: %v", err)
	}
	if m1 == "backup-x" {
		t.Fatalf("default-pool pick returned backup: %q", m1)
	}

	// Now mark the defaults cooling — ExcludeAndReselect re-bind
	// loops: cool the picked default + bound member, repeat, until
	// the default pool is fully cooling. The expected behaviour is
	// that the default pick degrades to the all-cooling
	// soonest-expiry path. Concurrent with that, the backup pool
	// is reachable directly.
	e.InjectAllCoolingForTest("g") // cools BOTH pools (shared cooldown map)
	// Direct call on the backup pool — the single configured
	// backup is also cooling now, so the call falls into the
	// backup pool's all-cooling soonest-expiry path. The picked
	// member is one of the backups.
	soonestPick, _, err := e.PickBackupMember("g", "conv-b-backup")
	if err != nil {
		t.Fatalf("PickBackupMember under shared cooldown: %v", err)
	}
	if soonestPick != "backup-x" {
		t.Fatalf("backup-only group all-cooling soonest pick = %q, want backup-x", soonestPick)
	}

	// Reset the backup-only cooldowns so the binding map path is
	// reachable. Defaults remain cooling.
	e.forceCooldownExpiryForTest("g", "backup-x")
	bp, newly, err := e.PickBackupMember("g", "conv-b-backup-reset")
	if err != nil {
		t.Fatalf("PickBackupMember after backup cooldown expiry: %v", err)
	}
	if bp != "backup-x" {
		t.Fatalf("PickBackupMember pick = %q, want backup-x", bp)
	}
	if !newly {
		t.Fatal("first PickBackupMember on a fresh key must be newlyBound=true")
	}

	// Verify sticky on the backup binding.
	bp2, newly2, err := e.PickBackupMember("g", "conv-b-backup-reset")
	if err != nil {
		t.Fatalf("re-pick: %v", err)
	}
	if bp2 != bp {
		t.Fatalf("backup sticky violated: %q vs %q", bp2, bp)
	}
	if newly2 {
		t.Fatal("in-TTL backup reuse must be newlyBound=false")
	}
}

// (c) backup pool respects weights + sticky among themselves.
// With multiple backups and the all-defaults excluded,
// PickBackupMember distributes among backups according to weight
// (statistical) and pins the same conversation to the picked
// backup (sticky).
func TestBackup_WeightAndStickyWithinBackupPool(t *testing.T) {
	refs := []MemberRef{
		{ModelID: "default-z", Weight: 1, Position: 0},
		{ModelID: "backup-heavy", Weight: 5, Position: 1, BackupOnly: true},
		{ModelID: "backup-light", Weight: 1, Position: 2, BackupOnly: true},
	}
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", refs)

	// Cool the default and switch to backup pool — multiple
	// distinct keys fresh-pick the backup pool. Heavy must
	// dominate.
	e.InjectAllCoolingForTest("g", "backup-heavy", "backup-light") // cool the default; leave backups alive
	counts := map[string]int{}
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("conv-c-%d", i)
		m, _, err := e.PickBackupMember("g", key)
		if err != nil {
			t.Fatalf("PickBackupMember[%d]: %v", i, err)
		}
		counts[m]++
	}
	// 5:1 ⇒ heavy ~ 415±30 of 500 within tolerance.
	if counts["backup-heavy"] < 380 || counts["backup-heavy"] > 460 {
		t.Errorf("5:1 weight distribution off: %+v", counts)
	}
	if counts["default-z"] != 0 {
		t.Errorf("backup pool yielded a default member: %+v", counts)
	}

	// Sticky within backup: same conversation re-picks same
	// backup. Pick once, then re-pick 5x — all identical.
	m1, _, _ := e.PickBackupMember("g", "conv-c-sticky")
	if m1 != "backup-heavy" && m1 != "backup-light" {
		t.Fatalf("first sticky pick = %q (want a backup)", m1)
	}
	for i := 0; i < 5; i++ {
		m2, _, _ := e.PickBackupMember("g", "conv-c-sticky")
		if m2 != m1 {
			t.Fatalf("sticky violated: got %q want %q (iter %d)", m2, m1, i)
		}
	}
}

// (d) multi-backup — exclusion chain across backups. The
// 3-backup group + ExcludeAndBackupReselect chain drives
// ReselectHealthy → ReselectHealthy → ReselectHealthy → and
// finally ReselectNone. Note: the engine itself accepts a
// backup-only configuration (D2 is a write-path concern; the
// engine is the picker) so this test pins the picker semantics
// independent of write validation.
func TestBackup_MultiBackupExclusionChain(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", onlyBackupsRefs())

	// First pick
	p1, _, err := e.PickBackupMember("g", "conv-d-sticky")
	if err != nil {
		t.Fatalf("first PickBackupMember: %v", err)
	}
	if p1 != "backup-a" && p1 != "backup-b" && p1 != "backup-c" {
		t.Fatalf("unexpected pick %q", p1)
	}

	// Cycle 1: exclude the picked member.
	p2, mode := e.ExcludeAndBackupReselect("g", "conv-d-sticky", p1, 0)
	if mode != ReselectHealthy {
		t.Fatalf("exclude 1: mode=%v want ReselectHealthy", mode)
	}
	if p2 == p1 {
		t.Fatalf("exclude 1 must rebind to a DIFFERENT member; got %q again", p2)
	}

	// Cycle 2: exclude that one too.
	p3, mode := e.ExcludeAndBackupReselect("g", "conv-d-sticky", p2, 0)
	if mode != ReselectHealthy {
		t.Fatalf("exclude 2: mode=%v want ReselectHealthy", mode)
	}
	if p3 == p2 || p3 == p1 {
		t.Fatalf("exclude 2 must rebind to a fresh member; got %q (prev %q, %q)", p3, p1, p2)
	}

	// Cycle 3: exclude the third — all-cooling again ⇒
	// ReselectSoonestExpiry.
	p4, mode := e.ExcludeAndBackupReselect("g", "conv-d-sticky", p3, 0)
	if mode != ReselectSoonestExpiry {
		t.Fatalf("exclude 3: mode=%v want ReselectSoonestExpiry", mode)
	}
	if p4 == "" {
		t.Fatal("all-cooling soonest pick must be non-empty")
	}
}

// (helper) HasBackupPool negative case — a default-only group
// returns false even though it has per-group state. Pins the
// distinction between "configured" and "configured-with-backups".
func TestBackup_HasBackupPoolFalseForDefaultOnly(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", refs2())
	if e.HasBackupPool("g") {
		t.Fatal("default-only group must report HasBackupPool=false")
	}
}

// (helper) ExcludeAndBackupReselect on a single-backup group ⇒
// ReselectNone (no alternative within the backup pool).
func TestBackup_SingleBackup_NoCooldown(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("solo-backup", []MemberRef{
		{ModelID: "only-backup", Weight: 1, BackupOnly: true},
	})
	e.InjectPreconditionStateForTest("solo-backup", "conv-key", "only-backup")

	_, mode := e.ExcludeAndBackupReselect("solo-backup", "conv-key", "only-backup", 0)
	if mode != ReselectNone {
		t.Fatalf("single-backup mode=%v want ReselectNone", mode)
	}
}

// (helper) Orphans a backup binding via OnGroupChanged — proves
// the survivor filter operates across BOTH binding maps.
func TestBackup_OnGroupChanged_DropsOrphanedBackupBinding(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", defaultPlusBackupRefs())
	// Prime a backup binding directly: pickBackup + repeat.
	_, _, _ = e.PickBackupMember("g", "conv-orphan-stick")
	if e.backupBindingsCountForTest("g") != 1 {
		t.Fatalf("backupBindingsCount = %d, want 1 after prime", e.backupBindingsCountForTest("g"))
	}

	// Remove the only backup — survivor filter must drop the
	// orphan backup binding.
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "default-a", Weight: 1},
		{ModelID: "default-b", Weight: 1},
	})
	if e.backupBindingsCountForTest("g") != 0 {
		t.Fatalf("backupBindingsCount = %d after backup removal, want 0", e.backupBindingsCountForTest("g"))
	}
}

// singleBackupRefs returns a one-member BACKUP-only ref list — the
// shape that triggers PickBackupMember's single-member fast path
// (n == 1 && len(cooldowns) == 0).
func singleBackupRefs(id string) []MemberRef {
	return []MemberRef{{ModelID: id, Weight: 1, Position: 0, BackupOnly: true}}
}

// TestPickBackupMember_FastPath_ReValidatesMemberID pins the
// single-backup fast path's WLock re-validation: the member ID read
// under RLock must STILL be the pool's only configured member when
// the binding write fires, else the pick must fall through to the
// full path and re-select against the CURRENT backup set.
//
// The race window is RUnlock → WLock inside the fast path; an
// OnGroupChanged swap landing there used to persist the stale id as
// the binding (an orphan the next OnGroupChanged had to clean).
//
// Shape ( cribbed from TestEngine_ConcurrentGetOrSelect ): pickers
// hammer fresh conversation keys while a swapper alternates the
// single backup between two IDs via OnGroupChanged. n stays == 1 and
// cooldowns stay empty throughout, so the fast path's RLock
// preconditions hold at every attempt — the ONLY thing that varies
// is WHICH member is configured, exactly the swap the fix guards.
func TestPickBackupMember_FastPath_ReValidatesMemberID(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", singleBackupRefs("backup-a"))

	// Deterministic prime: fresh pool, no swap — fast path binds the
	// only member and the binding row matches the returned id.
	id, newly, err := e.PickBackupMember("g", "conv-prime")
	if err != nil {
		t.Fatalf("prime: %v", err)
	}
	if id != "backup-a" || !newly {
		t.Fatalf("prime: id=%q newly=%v want backup-a/true", id, newly)
	}

	// Swap away: orphan filter drops the prime binding, leaving a
	// fresh pool where the next pick must re-bind to the NEW member.
	e.OnGroupChanged("g", singleBackupRefs("backup-b"))
	if got := e.backupBindingsCountForTest("g"); got != 0 {
		t.Fatalf("backupBindingsCount = %d after swap, want 0 (orphan filter)", got)
	}
	id, _, err = e.PickBackupMember("g", "conv-post-swap")
	if err != nil {
		t.Fatalf("post-swap: %v", err)
	}
	if id != "backup-b" {
		t.Fatalf("post-swap pick = %q, want backup-b (current member)", id)
	}

	// Concurrent phase: pickers race the swapper. Post-fix, every
	// pick reflects the CURRENT backup set at its own write-lock
	// section — a stale RLock-read id falls through to the full
	// path, which re-picks against the fresh pool.
	const pickers = 8
	const iters = 200
	var wg sync.WaitGroup
	for w := 0; w < pickers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				key := fmt.Sprintf("conv-w%d-i%d", w, i)
				m, newlyBound, err := e.PickBackupMember("g", key)
				if err != nil {
					t.Errorf("worker %d pick[%d]: %v", w, i, err)
					return
				}
				if m != "backup-a" && m != "backup-b" {
					t.Errorf("worker %d pick[%d]: id %q outside the swap set {backup-a, backup-b}", w, i, m)
					return
				}
				if !newlyBound {
					t.Errorf("worker %d pick[%d]: fresh key must be newlyBound=true", w, i)
					return
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < pickers*iters; i++ {
			if i%2 == 0 {
				e.OnGroupChanged("g", singleBackupRefs("backup-b"))
			} else {
				e.OnGroupChanged("g", singleBackupRefs("backup-a"))
			}
		}
	}()
	wg.Wait()

	// Freeze at backup-a, then walk every surviving backup binding.
	// OnGroupChanged's orphan filter has just dropped anything whose
	// memberID ∉ {backup-a}; a FRESH pick after the freeze must bind
	// the current member — proving the fast path still binds the
	// pool's sole member after heavy swap pressure.
	e.OnGroupChanged("g", singleBackupRefs("backup-a"))
	e.mu.RLock()
	st := e.groups["g"]
	e.mu.RUnlock()
	st.mu.RLock()
	if len(st.backupMembers) != 1 || st.backupMembers[0].ModelID != "backup-a" {
		st.mu.RUnlock()
		t.Fatalf("freeze failed: backupMembers = %v", st.backupMembers)
	}
	for key, b := range st.backupBindings {
		if b.memberID != "backup-a" {
			st.mu.RUnlock()
			t.Fatalf("stale binding survived the freeze: conv=%s→member=%s (want backup-a)", key, b.memberID)
		}
	}
	st.mu.RUnlock()

	id, newly, err = e.PickBackupMember("g", "conv-post-race")
	if err != nil {
		t.Fatalf("post-race: %v", err)
	}
	if id != "backup-a" || !newly {
		t.Fatalf("post-race pick: id=%q newly=%v want backup-a/true", id, newly)
	}
	if got := e.backupBindingsCountForTest("g"); got < 1 {
		t.Fatalf("backupBindingsCount = %d after post-race pick, want >= 1", got)
	}
}

// ─── group-backup-members / D8 — selection-time stale-binding repair ───────
//
// (h) remove the sticky member mid-conversation → same hash re-picks a
//     valid member (no error, no stale id);
// (i) removal from the backup pool while bound to backup → re-picks a
//     remaining backup;
// (j) default→backup reclassification behaves per D8 (existing binding
//     continues; NEW conversation never lands on the reclassified member
//     while backup-flagged);
// (k) add a new member → new picks may land on it, existing bindings
//     unaffected.
//
// The D8 model is TWO-LAYER: the PUSH layer (OnGroupChanged's survivor
// filter) drops bindings whose member left the group at config-change
// time; the PULL layer (this file's subject) resolves the bound member
// against the CURRENT membership (the UNION of both pools) at
// selection time — covering the config-change→selection race and the
// reclassification nuance.

// staleMemberRefs returns a 2-default + 1-backup member list. The
// default member ids are "default-a" / "default-b"; the backup id is
// "backup-x".
func staleMemberRefs() []MemberRef {
	return []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "default-b", Weight: 1, Position: 1},
		{ModelID: "backup-x", Weight: 1, Position: 2, BackupOnly: true},
	}
}

// (h) remove the sticky member mid-conversation → same hash re-picks
// a valid member (no error, no stale id).
//
// Two sub-cases, one per layer:
//
//	h1 (PULL layer) — the binding points at a member that is no longer
//	    in the membership. This is the config-change→selection race
//	    window: the binding write landed AFTER the OnGroupChanged that
//	    dropped the old row (or the notification never arrived). The
//	    engine must detect it at selection time, discard it, and
//	    re-pick — with the staleRepaired signal set.
//	h2 (PUSH layer) — the removal goes through OnGroupChanged, whose
//	    survivor filter drops the orphan. The end state is the same
//	    (the conversation lands on a live member); the pull layer
//	    simply never sees a stale row.
//
// The removed member must NEVER be returned to the caller, and the
// call must not error.
func TestD8_StaleBinding_RemovedMemberRepicks(t *testing.T) {
	// h1 — PULL layer: binding to a member that left the group.
	e := newTestEngine(t, time.Hour, time.Hour)
	// The membership changes FIRST (the member is removed), THEN an
	// in-flight request writes its binding against the OLD member —
	// the config-change→selection race window. The push layer
	// (OnGroupChanged) ran before the binding existed, so it could
	// not drop it; the PULL layer must catch it at selection time.
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "default-b", Weight: 1, Position: 0},
		{ModelID: "backup-x", Weight: 1, Position: 1, BackupOnly: true},
	})
	e.InjectPreconditionStateForTest("g", "conv-h", "default-a")

	member, _, stale, err := e.GetOrSelectWithRepair("g", "conv-h")
	if err != nil {
		t.Fatalf("(h1) GetOrSelectWithRepair: unexpected error %v", err)
	}
	if member == "default-a" {
		t.Fatalf("(h1) returned the REMOVED member %q — must re-pick a live member", member)
	}
	if member != "default-b" {
		t.Fatalf("(h1) pick = %q, want default-b (the only remaining default)", member)
	}
	if !stale {
		t.Fatal("(h1) staleRepaired must be true when a stale binding was discarded")
	}
	// The repaired binding now points at a LIVE member (no stale
	// repopulation).
	m2, _, stale2, err := e.GetOrSelectWithRepair("g", "conv-h")
	if err != nil {
		t.Fatalf("(h1) re-select: %v", err)
	}
	if m2 != "default-b" {
		t.Fatalf("(h1) re-select = %q, want default-b (sticky on the repaired binding)", m2)
	}
	if stale2 {
		t.Fatal("(h1) the second select must NOT report a repair (binding is healthy now)")
	}

	// h2 — PUSH layer: the removal goes through OnGroupChanged.
	e2 := newTestEngine(t, time.Hour, time.Hour)
	e2.OnGroupChanged("g2", staleMemberRefs())
	// Bind deterministically to "default-a" (the member that is
	// about to be removed).
	e2.InjectPreconditionStateForTest("g2", "conv-h2", "default-a")
	first, _, err := e2.GetOrSelect("g2", "conv-h2")
	if err != nil {
		t.Fatalf("(h2) first GetOrSelect: %v", err)
	}
	if first != "default-a" {
		t.Fatalf("(h2) first pick = %q, want default-a", first)
	}
	// Remove the bound member (the push layer's survivor filter drops
	// the binding).
	e2.OnGroupChanged("g2", []MemberRef{
		{ModelID: "default-b", Weight: 1, Position: 0},
		{ModelID: "backup-x", Weight: 1, Position: 1, BackupOnly: true},
	})
	member2, _, err := e2.GetOrSelect("g2", "conv-h2")
	if err != nil {
		t.Fatalf("(h2) post-removal GetOrSelect: %v", err)
	}
	if member2 == first {
		t.Fatalf("(h2) returned the REMOVED member %q", member2)
	}
	if member2 != "default-b" {
		t.Fatalf("(h2) pick = %q, want default-b (the only remaining default)", member2)
	}
}

// (i) removal from the backup pool while bound to backup → re-picks a
// remaining backup.
//
// The conversation is bound to a BACKUP member. That member leaves the
// group. When the backup pool is consulted again, the stale binding is
// detected (union-scoped existence check) and the pick is re-made among
// the REMAINING backups — never the removed one, and never an error.
//
// The group is backup-only here (constructed directly at the engine
// level, bypassing the D2 write-path validation) so the backup pool is
// the only pool — the same construction the (d) test uses.
func TestD8_StaleBinding_BackupMemberRemovedRepicks(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", onlyBackupsRefs())

	// Bind the conversation to "backup-a" (a live backup) in the
	// BACKUP binding map.
	e.InjectBackupPreconditionStateForTest("g", "conv-i", "backup-a")
	member, _, stale, err := e.PickBackupMemberWithRepair("g", "conv-i")
	if err != nil {
		t.Fatalf("(i) PickBackupMemberWithRepair: %v", err)
	}
	if member != "backup-a" {
		t.Fatalf("(i) pick = %q, want backup-a (bound member is still a member)", member)
	}
	if stale {
		t.Fatal("(i) no repair expected — backup-a is still in the group")
	}

	// "backup-a" leaves the group. The membership changes FIRST, THEN
	// an in-flight request writes its backup binding against the OLD
	// member (the config-change→selection race window the push
	// layer cannot see).
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "backup-b", Weight: 1, Position: 0, BackupOnly: true},
		{ModelID: "backup-c", Weight: 1, Position: 1, BackupOnly: true},
	})
	e.InjectBackupPreconditionStateForTest("g", "conv-i", "backup-a")

	member2, _, stale2, err := e.PickBackupMemberWithRepair("g", "conv-i")
	if err != nil {
		t.Fatalf("(i) post-removal PickBackupMemberWithRepair: %v", err)
	}
	if member2 == "backup-a" {
		t.Fatalf("(i) returned the REMOVED backup %q — must re-pick a remaining backup", member2)
	}
	if member2 != "backup-b" && member2 != "backup-c" {
		t.Fatalf("(i) pick = %q, want backup-b or backup-c (a remaining backup)", member2)
	}
	if !stale2 {
		t.Fatal("(i) staleRepaired must be true when a stale BACKUP binding was discarded")
	}
}

// (j) default→backup reclassification behaves per D8: the existing
// binding CONTINUES on that member (grandfathered — the member still
// EXISTS, it merely changed pools), and a NEW conversation never lands
// on the reclassified member while it is backup-flagged.
//
// The flip is a config change ⇒ OnGroupChanged carries the new refs.
func TestD8_Reclassification_DefaultToBackup_Grandfathered(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", staleMemberRefs())

	// Bind the conversation to a default member.
	bound, _, err := e.GetOrSelect("g", "conv-j")
	if err != nil {
		t.Fatalf("(j) first GetOrSelect: %v", err)
	}
	if bound != "default-a" && bound != "default-b" {
		t.Fatalf("(j) first pick = %q, want a default member", bound)
	}

	// Reclassify the bound member: default → backup (the member still
	// EXISTS — only its pool changed).
	reclassified := []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "default-b", Weight: 1, Position: 1},
		{ModelID: "backup-x", Weight: 1, Position: 2, BackupOnly: true},
	}
	for i, r := range reclassified {
		if r.ModelID == bound {
			reclassified[i].BackupOnly = true
		}
	}
	e.OnGroupChanged("g", reclassified)

	// The EXISTING binding continues on that member (grandfathered).
	after, _, stale, err := e.GetOrSelectWithRepair("g", "conv-j")
	if err != nil {
		t.Fatalf("(j) post-reclassification GetOrSelectWithRepair: %v", err)
	}
	if after != bound {
		t.Fatalf("(j) pick = %q, want the grandfathered %q (binding continues on a member that still exists)", after, bound)
	}
	if stale {
		t.Fatal("(j) a reclassification is NOT a removal — staleRepaired must stay false")
	}

	// A NEW conversation must NEVER land on the reclassified member
	// while it is backup-flagged — the default pool's selector no
	// longer contains it.
	for i := 0; i < 100; i++ {
		m, _, err := e.GetOrSelect("g", fmt.Sprintf("conv-j-new-%d", i))
		if err != nil {
			t.Fatalf("(j) new-conversation GetOrSelect[%d]: %v", i, err)
		}
		if m == bound {
			t.Fatalf("(j) new conversation %d landed on the reclassified (backup-flagged) member %q", i, m)
		}
	}
}

// (j-bis) backup→default reclassification: the backup binding
// CONTINUES on the member (fully consistent with the default→backup
// case). The union-scoped existence check in the backup pool is what
// makes this work — a pool-scoped check would kill the binding.
func TestD8_Reclassification_BackupToDefault_Grandfathered(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	// TWO backups — the backup pool must remain non-empty after the
	// reclassification, otherwise the backup pool reports "exhausted"
	// and the grandfathering case is unobservable.
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "backup-x", Weight: 1, Position: 1, BackupOnly: true},
		{ModelID: "backup-y", Weight: 1, Position: 2, BackupOnly: true},
	})

	// Bind the conversation to the backup member.
	e.InjectBackupPreconditionStateForTest("g", "conv-jb", "backup-x")
	bound, _, err := e.PickBackupMember("g", "conv-jb")
	if err != nil {
		t.Fatalf("(j-bis) first PickBackupMember: %v", err)
	}
	if bound != "backup-x" {
		t.Fatalf("(j-bis) first pick = %q, want backup-x", bound)
	}

	// Reclassify backup-x: backup → default (still EXISTS).
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "backup-x", Weight: 1, Position: 1}, // no BackupOnly ⇒ default now
		{ModelID: "backup-y", Weight: 1, Position: 2, BackupOnly: true},
	})

	after, _, stale, err := e.PickBackupMemberWithRepair("g", "conv-jb")
	if err != nil {
		t.Fatalf("(j-bis) post-reclassification PickBackupMemberWithRepair: %v", err)
	}
	if after != bound {
		t.Fatalf("(j-bis) pick = %q, want the grandfathered %q", after, bound)
	}
	if stale {
		t.Fatal("(j-bis) a reclassification is NOT a removal — staleRepaired must stay false")
	}
}

// (k) add a new member → new picks may land on it, existing bindings
// are unaffected.
func TestD8_AddMember_NewPicksMayLandOnIt(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "default-b", Weight: 1, Position: 1},
	})

	// Bind the conversation before the addition.
	bound, _, err := e.GetOrSelect("g", "conv-k")
	if err != nil {
		t.Fatalf("(k) first GetOrSelect: %v", err)
	}

	// Add a third member.
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "default-b", Weight: 1, Position: 1},
		{ModelID: "default-c", Weight: 1, Position: 2},
	})

	// The existing binding is UNAFFECTED (the addition does not
	// disturb it).
	after, _, stale, err := e.GetOrSelectWithRepair("g", "conv-k")
	if err != nil {
		t.Fatalf("(k) post-addition GetOrSelectWithRepair: %v", err)
	}
	if after != bound {
		t.Fatalf("(k) pick = %q, want the unchanged binding %q", after, bound)
	}
	if stale {
		t.Fatal("(k) an addition is NOT a removal — staleRepaired must stay false")
	}

	// NEW conversations MAY land on the new member (the selector was
	// rebuilt with it).
	sawNew := false
	for i := 0; i < 200; i++ {
		m, _, err := e.GetOrSelect("g", fmt.Sprintf("conv-k-new-%d", i))
		if err != nil {
			t.Fatalf("(k) new-conversation GetOrSelect[%d]: %v", i, err)
		}
		if m == "default-c" {
			sawNew = true
			break
		}
	}
	if !sawNew {
		t.Fatal("(k) new conversations never landed on the added member — the selector was not rebuilt")
	}
}

// (defensive) the all-defaults-removed state is unreachable via the
// API (D2 blocks zero-default writes) — but the picker must still
// handle it: the default pool reports "exhausted" (ErrNoMembers —
// NOT a wrong member) and the BACKUP pool serves. The engine-level
// construction bypasses the write-path validation on purpose.
func TestD8_EmptyDefaultPool_FallsToBackup(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	// Backup-only group: the default pool is EMPTY.
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "backup-x", Weight: 1, Position: 0, BackupOnly: true},
	})

	// The default pool reports exhaustion (ErrNoMembers) — NOT a
	// hard failure the caller must treat as fatal, and NOT the backup
	// member leaking through the default entry point.
	if member, _, err := e.GetOrSelect("g", "conv-empty"); err != ErrNoMembers {
		t.Fatalf("empty default pool: err = %v (member %q), want ErrNoMembers", err, member)
	}
	// The backup pool serves the request.
	member, _, _, err := e.PickBackupMemberWithRepair("g", "conv-empty")
	if err != nil {
		t.Fatalf("empty default pool: PickBackupMemberWithRepair: %v", err)
	}
	if member != "backup-x" {
		t.Fatalf("empty default pool: pick = %q, want backup-x", member)
	}
}

// TestD8_NoDoubleDrop_PullLayer pins the D8 two-layer composition's
// counter invariant mechanically.
//
// The PUSH layer (OnGroupChanged's membership-delta survivor filter)
// and the PULL layer (selection-time stale repair) BOTH drop binding
// rows, and each drop decrements the O(1) row mirror exactly once.
// The decrements are guarded by a row-presence check
// (`if b, ok := map[key]; ok`); this test drives the interleavings
// that would expose a missing or mis-scoped guard — a double
// decrement, or a counter underflow (bindingsCount is uint64, so an
// underflow surfaces as a negative int through bindingsCountForTest).
//
// Deterministic: no race harness and no sleeps — the config-change →
// selection race window is constructed DIRECTLY by injecting the
// binding after the push layer has already run.
func TestD8_NoDoubleDrop_PullLayer(t *testing.T) {
	const key = "conv-d8-nodouble"

	// ── Default-pool map discipline ─────────────────────────────────
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", staleMemberRefs()) // [default-a, default-b, backup-x]

	// (1) Prime a binding to a live member: one row, count 1.
	e.InjectPreconditionStateForTest("g", key, "default-a")
	if got := e.bindingsCountForTest("g"); got != 1 {
		t.Fatalf("(1) prime: bindingsCount = %d, want 1", got)
	}

	// (2) PUSH layer removes the bound member — the survivor filter
	// drops the row, decrementing EXACTLY once.
	reduced := []MemberRef{
		{ModelID: "default-b", Weight: 1, Position: 0},
		{ModelID: "backup-x", Weight: 1, Position: 1, BackupOnly: true},
	}
	e.OnGroupChanged("g", reduced)
	if got := e.bindingsCountForTest("g"); got != 0 {
		t.Fatalf("(2) push-layer drop: bindingsCount = %d, want 0 (exactly one decrement, no underflow)", got)
	}

	// (2b) The push layer runs AGAIN over the same (now empty) map —
	// the already-dropped row must not be double-decremented.
	e.OnGroupChanged("g", reduced)
	if got := e.bindingsCountForTest("g"); got != 0 {
		t.Fatalf("(2b) repeated push-layer pass: bindingsCount = %d, want 0 (no double decrement / underflow)", got)
	}

	// (3) Re-create the race state: an in-flight request writes the
	// OLD (removed-member) binding AFTER the push layer ran.
	e.InjectPreconditionStateForTest("g", key, "default-a")
	if got := e.bindingsCountForTest("g"); got != 1 {
		t.Fatalf("(3) race injection: bindingsCount = %d, want 1", got)
	}

	// (4) PULL layer repairs: one decrement (stale row) + one
	// increment (re-picked row) ⇒ count is exactly 1, never -1.
	member, _, stale, err := e.GetOrSelectWithRepair("g", key)
	if err != nil {
		t.Fatalf("(4) repair: %v", err)
	}
	if !stale {
		t.Fatal("(4) staleRepaired must be true — the bound member left the group")
	}
	if member != "default-b" {
		t.Fatalf("(4) repaired pick = %q, want default-b", member)
	}
	if got := e.bindingsCountForTest("g"); got != 1 {
		t.Fatalf("(4) pull-layer repair: bindingsCount = %d, want 1 (one drop + one re-pick; underflow would show as -1)", got)
	}

	// (5) Repeat the selection for the SAME key — the repaired
	// binding is healthy, so neither layer may drop or re-add it.
	for i := 0; i < 3; i++ {
		m, _, stale2, err := e.GetOrSelectWithRepair("g", key)
		if err != nil {
			t.Fatalf("(5) repeat %d: %v", i, err)
		}
		if m != "default-b" {
			t.Fatalf("(5) repeat %d: pick = %q, want default-b (sticky)", i, m)
		}
		if stale2 {
			t.Fatalf("(5) repeat %d: the healthy binding must not be reported stale", i)
		}
		if got := e.bindingsCountForTest("g"); got != 1 {
			t.Fatalf("(5) repeat %d: bindingsCount = %d, want 1", i, got)
		}
	}

	// (6) The push layer re-runs with the same membership — the
	// surviving binding must be untouched.
	e.OnGroupChanged("g", reduced)
	if got := e.bindingsCountForTest("g"); got != 1 {
		t.Fatalf("(6) idempotent push-layer pass: bindingsCount = %d, want 1", got)
	}

	// (7) A second conversation binds — one row PER KEY, no inflation.
	if _, _, _, err := e.GetOrSelectWithRepair("g", "conv-d8-nodouble-2"); err != nil {
		t.Fatalf("(7) second conversation: %v", err)
	}
	if got := e.bindingsCountForTest("g"); got != 2 {
		t.Fatalf("(7) second conversation: bindingsCount = %d, want 2 (one row per key)", got)
	}

	// (8) Remove the member the first conversation is bound to — the
	// push layer drops exactly that row; the second conversation's
	// row survives.
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "backup-x", Weight: 1, Position: 0, BackupOnly: true},
	})
	if got := e.bindingsCountForTest("g"); got != 0 {
		t.Fatalf("(8) all-defaults-removed push pass: bindingsCount = %d, want 0 (both bound defaults gone, one decrement each)", got)
	}

	// (9) The default pool is now empty (the D8 defensive path); the
	// BACKUP pool serves and binds — count stays consistent.
	if m, _, _, err := e.PickBackupMemberWithRepair("g", key); err != nil || m != "backup-x" {
		t.Fatalf("(9) backup serve: member=%q err=%v, want backup-x", m, err)
	}
	if got := e.bindingsCountForTest("g"); got != 0 {
		t.Fatalf("(9) backup serve must not touch the DEFAULT map; bindingsCount = %d, want 0", got)
	}

	// ── Backup-pool map discipline ──────────────────────────────────
	eb := newTestEngine(t, time.Hour, time.Hour)
	eb.OnGroupChanged("gb", onlyBackupsRefs()) // [backup-a, backup-b, backup-c]

	// (10) Prime a BACKUP binding: one row, count 1.
	eb.InjectBackupPreconditionStateForTest("gb", key, "backup-a")
	if got := eb.backupBindingsCountForTest("gb"); got != 1 {
		t.Fatalf("(10) prime: backupBindingsCount = %d, want 1", got)
	}

	// (11) PUSH layer removes the bound backup — exactly one
	// decrement.
	reducedBackups := []MemberRef{
		{ModelID: "backup-b", Weight: 1, Position: 0, BackupOnly: true},
		{ModelID: "backup-c", Weight: 1, Position: 1, BackupOnly: true},
	}
	eb.OnGroupChanged("gb", reducedBackups)
	if got := eb.backupBindingsCountForTest("gb"); got != 0 {
		t.Fatalf("(11) push-layer drop: backupBindingsCount = %d, want 0", got)
	}
	// (11b) repeated push-layer pass — no double decrement.
	eb.OnGroupChanged("gb", reducedBackups)
	if got := eb.backupBindingsCountForTest("gb"); got != 0 {
		t.Fatalf("(11b) repeated push-layer pass: backupBindingsCount = %d, want 0 (no underflow)", got)
	}

	// (12) Race injection + PULL-layer repair: one drop + one
	// re-pick ⇒ exactly 1.
	eb.InjectBackupPreconditionStateForTest("gb", key, "backup-a")
	m2, _, stale2, err := eb.PickBackupMemberWithRepair("gb", key)
	if err != nil {
		t.Fatalf("(12) backup repair: %v", err)
	}
	if !stale2 {
		t.Fatal("(12) staleRepaired must be true — the bound backup left the group")
	}
	if m2 != "backup-b" && m2 != "backup-c" {
		t.Fatalf("(12) repaired pick = %q, want a remaining backup", m2)
	}
	if got := eb.backupBindingsCountForTest("gb"); got != 1 {
		t.Fatalf("(12) pull-layer repair: backupBindingsCount = %d, want 1 (underflow would show as -1)", got)
	}

	// (13) Repeat selection + a re-run of the push layer — stable at 1.
	for i := 0; i < 3; i++ {
		if _, _, s, err := eb.PickBackupMemberWithRepair("gb", key); err != nil || s {
			t.Fatalf("(13) repeat %d: stale=%v err=%v, want healthy", i, s, err)
		}
		if got := eb.backupBindingsCountForTest("gb"); got != 1 {
			t.Fatalf("(13) repeat %d: backupBindingsCount = %d, want 1", i, got)
		}
	}
	eb.OnGroupChanged("gb", reducedBackups)
	if got := eb.backupBindingsCountForTest("gb"); got != 1 {
		t.Fatalf("(13) idempotent push-layer pass: backupBindingsCount = %d, want 1", got)
	}

	// (14) Final invariant sweep: both mirrors are non-negative and
	// match the number of live rows (never a stale/decremented value).
	if got := e.bindingsCountForTest("g"); got < 0 {
		t.Fatalf("(14) default mirror underflowed: %d", got)
	}
	if got := eb.backupBindingsCountForTest("gb"); got < 0 {
		t.Fatalf("(14) backup mirror underflowed: %d", got)
	}
}

// ─── group-backup-members / C4 — refresh semantics + counter drift ─────────
//
// A same-member STICKY REFRESH must report newlyBound=false: the proxy
// emits model_member_selected only when NewlyBound is true
// (race_executor.go gate), so a refresh that reported true would
// re-emit the event on every request. The O(1) binding mirror must also
// not drift: the increment is guarded by map-existence (C4).

// TestBackup_FastPath_RefreshNotNewlyBoundAndNoCountDrift — the
// single-backup FAST path (n==1, no cooldowns).
func TestBackup_FastPath_RefreshNotNewlyBoundAndNoCountDrift(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "backup-x", Weight: 1, Position: 1, BackupOnly: true},
	})

	first, newly1, err := e.PickBackupMember("g", "conv-refresh")
	if err != nil {
		t.Fatalf("first pick: %v", err)
	}
	if first != "backup-x" || !newly1 {
		t.Fatalf("first pick = (%q, %v), want (backup-x, true)", first, newly1)
	}
	if got := e.backupBindingsCountForTest("g"); got != 1 {
		t.Fatalf("after first pick: backupBindingsCount = %d, want 1", got)
	}

	// C4: repeated same-member refreshes must NOT report newlyBound
	// and must NOT inflate the counter.
	for i := 0; i < 5; i++ {
		m, newly, err := e.PickBackupMember("g", "conv-refresh")
		if err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
		if m != "backup-x" {
			t.Fatalf("refresh %d: member = %q, want backup-x (sticky)", i, m)
		}
		if newly {
			t.Fatalf("refresh %d: newlyBound = true — the model_member_selected event would re-emit", i)
		}
		if got := e.backupBindingsCountForTest("g"); got != 1 {
			t.Fatalf("refresh %d: backupBindingsCount = %d, want 1 (no count drift)", i, got)
		}
	}

	// The repair-aware variant agrees: a healthy refresh is not a repair.
	if _, _, stale, err := e.PickBackupMemberWithRepair("g", "conv-refresh"); err != nil || stale {
		t.Fatalf("repair-aware refresh: stale=%v err=%v, want healthy", stale, err)
	}
}

// TestBackup_FullPath_RefreshNotNewlyBoundAndNoCountDrift — the same
// C4 contract on the FULL path (2 backups ⇒ no fast path).
func TestBackup_FullPath_RefreshNotNewlyBoundAndNoCountDrift(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "backup-x", Weight: 1, Position: 1, BackupOnly: true},
		{ModelID: "backup-y", Weight: 1, Position: 2, BackupOnly: true},
	})

	first, newly1, err := e.PickBackupMember("g", "conv-refresh-full")
	if err != nil || first == "" || !newly1 {
		t.Fatalf("first pick = (%q, %v, %v), want (member, true, nil)", first, newly1, err)
	}
	if got := e.backupBindingsCountForTest("g"); got != 1 {
		t.Fatalf("after first pick: backupBindingsCount = %d, want 1", got)
	}
	for i := 0; i < 5; i++ {
		m, newly, err := e.PickBackupMember("g", "conv-refresh-full")
		if err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
		if m != first {
			t.Fatalf("refresh %d: member = %q, want sticky %q", i, m, first)
		}
		if newly {
			t.Fatalf("refresh %d: newlyBound = true on a same-member refresh", i)
		}
		if got := e.backupBindingsCountForTest("g"); got != 1 {
			t.Fatalf("refresh %d: backupBindingsCount = %d, want 1", i, got)
		}
	}
}

// TestDefaultPoolHealthy — the C1 support query: true iff the DEFAULT
// pool has at least one non-cooling member; false for an empty default
// pool / unknown group / nil receiver.
func TestDefaultPoolHealthy(t *testing.T) {
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g", []MemberRef{
		{ModelID: "default-a", Weight: 1, Position: 0},
		{ModelID: "default-b", Weight: 1, Position: 1},
		{ModelID: "backup-x", Weight: 1, Position: 2, BackupOnly: true},
	})
	if !e.DefaultPoolHealthy("g") {
		t.Fatal("healthy defaults must report true")
	}
	// Cool one default — still healthy.
	e.InjectAllCoolingForTest("g", "default-b", "backup-x")
	if !e.DefaultPoolHealthy("g") {
		t.Fatal("one healthy default must keep the pool healthy")
	}
	// Cool both defaults — exhausted.
	e.InjectAllCoolingForTest("g", "backup-x")
	if e.DefaultPoolHealthy("g") {
		t.Fatal("all defaults cooling must report the pool exhausted")
	}
	// Defensive: unknown group / nil receiver.
	if e.DefaultPoolHealthy("nope") {
		t.Error("unknown group must report false")
	}
	var nilE *Engine
	if nilE.DefaultPoolHealthy("g") {
		t.Error("nil receiver must report false")
	}
}
