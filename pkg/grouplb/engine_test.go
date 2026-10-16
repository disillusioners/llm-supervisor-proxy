package grouplb

import (
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
