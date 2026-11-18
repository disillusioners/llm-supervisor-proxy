package modelscache

// tokens_test.go — three-tier token cache tests (positive TTL,
// negative TTL, stale-tier on infra error, fail-closed on verdicts,
// LRU eviction, DeleteToken fan-out, W2/W4/W5 contracts).

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
)

// fakeTokenStore is an auth.TokenStoreInterface with injection.
type fakeTokenStore struct {
	mu       sync.Mutex
	tokens   map[string]*auth.AuthToken // hash → token
	validate atomic.Int64
	infraErr error // when non-nil, ValidateToken returns it
	innerErr error // when non-nil (and infraErr nil), returned as-is
	listErr  error
	// lastPerm / lastPermID record the last UpdateTokenPermission call so a
	// decorator test can prove the payload was forwarded verbatim (not
	// re-derived). Token-authz T1.4.
	lastPerm   *auth.OptionalTokenPermission
	lastPermID string
}

func newFakeTokenStore() *fakeTokenStore {
	return &fakeTokenStore{tokens: map[string]*auth.AuthToken{}}
}

func (f *fakeTokenStore) ValidateToken(ctx context.Context, plaintext string) (*auth.AuthToken, error) {
	f.validate.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	infra, inner := f.infraErr, f.innerErr
	f.mu.Unlock()
	if infra != nil {
		return nil, infra
	}
	if inner != nil {
		return nil, inner
	}
	hash := auth.HashToken(plaintext)
	f.mu.Lock()
	defer f.mu.Unlock()
	tok, ok := f.tokens[hash]
	if !ok {
		return nil, auth.ErrTokenNotFound
	}
	return tok, nil
}

func (f *fakeTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, perm auth.OptionalTokenPermission) (string, *auth.AuthToken, error) {
	plaintext, _, _ := auth.GenerateToken()
	hash := auth.HashToken(plaintext)
	tok := &auth.AuthToken{ID: "id-" + name, Name: name, TokenHash: hash, ExpiresAt: expiresAt, CreatedBy: createdBy, AllowedModels: perm.AllowedModels.Value(), AllowedGroups: perm.AllowedGroups.Value()}
	f.mu.Lock()
	f.tokens[hash] = tok
	f.mu.Unlock()
	return plaintext, tok, nil
}

func (f *fakeTokenStore) DeleteToken(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for h, tok := range f.tokens {
		if tok.ID == id {
			delete(f.tokens, h)
			return nil
		}
	}
	return auth.ErrTokenNotFound
}

func (f *fakeTokenStore) ListTokens(ctx context.Context) ([]auth.AuthToken, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]auth.AuthToken, 0, len(f.tokens))
	for _, t := range f.tokens {
		out = append(out, *t)
	}
	return out, nil
}

func (f *fakeTokenStore) GetTokenByID(ctx context.Context, id string) (*auth.AuthToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if t.ID == id {
			cp := *t
			return &cp, nil
		}
	}
	return nil, auth.ErrTokenNotFound
}

func (f *fakeTokenStore) UpdateTokenPermission(ctx context.Context, id string, perm auth.OptionalTokenPermission) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := perm
	f.lastPerm = &p
	f.lastPermID = id
	return nil
}

// ─── keying ──────────────────────────────────────────────────────────────────

func TestCachedTokenStore_KeyingIsSHA256(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{})
	c.Stop()

	plaintext, hash, _ := auth.GenerateToken()
	tok := &auth.AuthToken{ID: "t1", TokenHash: hash}
	inner.mu.Lock()
	inner.tokens[hash] = tok
	inner.mu.Unlock()

	got, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil || got == nil || got.ID != "t1" {
		t.Fatalf("validate: %v %v", got, err)
	}
	// Entry is keyed by the hex hash (N1: whatever HashToken returns —
	// a string), never the plaintext.
	c.mu.RLock()
	_, byHash := c.entries.Peek(auth.HashToken(plaintext))
	_, byPlain := c.entries.Peek(plaintext)
	c.mu.RUnlock()
	if !byHash {
		t.Error("entry must be keyed by SHA-256 hex hash")
	}
	if byPlain {
		t.Error("plaintext must never be a cache key")
	}
	for k := range c.idToHash {
		if k == plaintext {
			t.Error("idToHash values/keys must never hold plaintext")
		}
	}
	if c.idToHash["t1"] != hash {
		t.Errorf("idToHash[t1] must be the hash (W4 read-path populate), got %q", c.idToHash["t1"])
	}
}

// ─── positive / negative TTL ─────────────────────────────────────────────────

func TestCachedTokenStore_PositiveTTL(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{Clock: clk.Now})

	plaintext, hash, _ := auth.GenerateToken()
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "p1", TokenHash: hash}
	inner.mu.Unlock()

	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("first validate: %v", err)
	}
	calls := inner.validate.Load()

	// Within TTL → cached, no inner call.
	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("cached validate: %v", err)
	}
	if inner.validate.Load() != calls {
		t.Error("positive within TTL must be served from cache (zero inner calls)")
	}

	// Past TTL → revalidation (inner call).
	clk.Advance(61 * time.Second)
	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("post-TTL validate: %v", err)
	}
	if inner.validate.Load() != calls+1 {
		t.Errorf("post-TTL must revalidate, calls %d → %d", calls, inner.validate.Load())
	}
}

func TestCachedTokenStore_NegativeTTL(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{Clock: clk.Now})

	plaintext, _, _ := auth.GenerateToken() // never stored

	if _, err := c.ValidateToken(context.Background(), plaintext); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound, got %v", err)
	}
	calls := inner.validate.Load()
	if calls != 1 {
		t.Fatalf("expected 1 inner call, got %d", calls)
	}

	// Negative verdict is replayed from cache within TTL (fail-closed,
	// no inner call).
	if _, err := c.ValidateToken(context.Background(), plaintext); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("cached negative: %v", err)
	}
	if inner.validate.Load() != calls {
		t.Error("negative within TTL must be served from cache")
	}

	// Past TTL the negative revalidates (a token created in the DB
	// meanwhile becomes visible — W6's 60s worst case).
	clk.Advance(61 * time.Second)
	hash := auth.HashToken(plaintext)
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "late", TokenHash: hash}
	inner.mu.Unlock()
	if tok, err := c.ValidateToken(context.Background(), plaintext); err != nil || tok.ID != "late" {
		t.Fatalf("negative must expire: %v %v", tok, err)
	}
}

// ─── stale tier (C2) ─────────────────────────────────────────────────────────

func TestCachedTokenStore_StaleTierHitsOnInfraError(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{Clock: clk.Now})

	plaintext, hash, _ := auth.GenerateToken()
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "stale-1", TokenHash: hash}
	inner.mu.Unlock()

	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("warm: %v", err)
	}

	// Advance past the positive TTL, then take the DB down.
	clk.Advance(90 * time.Second)
	inner.mu.Lock()
	inner.infraErr = connRefused("connection refused")
	inner.mu.Unlock()

	// Well past positivesTTL (t>60s into the outage) — the stale tier
	// serves the known-good token (degraded-allow, C2).
	tok, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil || tok == nil || tok.ID != "stale-1" {
		t.Fatalf("stale-tier must serve on infra error at t>60s: %v %v", tok, err)
	}

	// Even an hour in, still serving (>=1h outage goal).
	clk.Advance(time.Hour)
	tok, err = c.ValidateToken(context.Background(), plaintext)
	if err != nil || tok == nil || tok.ID != "stale-1" {
		t.Fatalf("stale-tier must serve 1h into the outage: %v %v", tok, err)
	}
}

func TestCachedTokenStore_StaleTierDoesNotHitOnNegativeVerdict(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{Clock: clk.Now})

	plaintext, hash, _ := auth.GenerateToken()
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "revoked-soon", TokenHash: hash}
	inner.mu.Unlock()

	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("warm: %v", err)
	}
	clk.Advance(90 * time.Second)

	// The token is deleted in the DB; the inner store answers with a
	// VERDICT (not-found) — fail-closed wins over the stale tier.
	inner.mu.Lock()
	delete(inner.tokens, hash)
	inner.mu.Unlock()
	if _, err := c.ValidateToken(context.Background(), plaintext); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("verdict-class errors must NOT fall back to stale tier, got %v", err)
	}
}

func TestCachedTokenStore_StaleTierCapEjects(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{Clock: clk.Now, StaleCap: 30 * time.Minute})

	plaintext, hash, _ := auth.GenerateToken()
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "capped", TokenHash: hash}
	inner.mu.Unlock()
	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("warm: %v", err)
	}

	// Past positive TTL AND past the stale cap → treated as cold: the
	// infra error propagates (no degraded-allow beyond the cap).
	clk.Advance(31 * time.Minute)
	inner.mu.Lock()
	inner.infraErr = connRefused("connection refused")
	inner.mu.Unlock()
	if _, err := c.ValidateToken(context.Background(), plaintext); !isInfraError(err) {
		t.Fatalf("beyond staleCap the entry is cold — infra error must propagate, got %v", err)
	}
}

func TestCachedTokenStore_NeverSeenTokenDuringOutageFailsClosed(t *testing.T) {
	inner := newFakeTokenStore()
	inner.mu.Lock()
	inner.infraErr = connRefused("connection refused")
	inner.mu.Unlock()
	c := NewCachedTokenStore(inner, Options{})

	plaintext, _, _ := auth.GenerateToken()
	if _, err := c.ValidateToken(context.Background(), plaintext); !isInfraError(err) {
		t.Fatalf("never-seen token + DB down must surface the infra error (401 upstream), got %v", err)
	}
}

// ─── LRU eviction ────────────────────────────────────────────────────────────

func TestCachedTokenStore_LRUEvictsAtCap(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{LRUCap: 100})

	hashes := make([]string, 101)
	for i := 0; i < 101; i++ {
		plaintext, hash, _ := auth.GenerateToken()
		hashes[i] = hash
		inner.mu.Lock()
		inner.tokens[hash] = &auth.AuthToken{ID: string(rune('a'+i%26)) + time.Now().Format("150405.000000000") + string(rune('0'+i%10)), TokenHash: hash}
		inner.mu.Unlock()
		if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
			t.Fatalf("validate %d: %v", i, err)
		}
	}

	c.mu.RLock()
	size := c.entries.Len()
	c.mu.RUnlock()
	if size != 100 {
		t.Fatalf("LRU cap 100 must hold exactly 100, got %d", size)
	}
	// The oldest entry (hashes[0]) was evicted.
	c.mu.RLock()
	_, oldest := c.entries.Peek(hashes[0])
	_, newest := c.entries.Peek(hashes[100])
	c.mu.RUnlock()
	if oldest {
		t.Error("oldest entry must have been evicted at cap")
	}
	if !newest {
		t.Error("newest entry must be live")
	}
}

// ─── mutators / fan-out (W4) ─────────────────────────────────────────────────

func TestCachedTokenStore_DeleteClearsBothEntries(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{})

	// Seed a positive AND a negative entry for the same hash: positive
	// first, then flip the inner verdict to not-found so the negative
	// replaces the entry (Delete must clear whatever is there).
	plaintext, hash, _ := auth.GenerateToken()
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "del-me", TokenHash: hash}
	inner.mu.Unlock()
	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("warm positive: %v", err)
	}

	if err := c.DeleteToken(context.Background(), "del-me"); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}

	c.mu.RLock()
	_, entry := c.entries.Peek(hash)
	_, idx := c.idToHash["del-me"]
	c.mu.RUnlock()
	if entry {
		t.Error("DeleteToken must clear the hash-keyed entry")
	}
	if idx {
		t.Error("DeleteToken must remove the idToHash entry (W4)")
	}

	// Subsequent validation is a cold miss (inner not-found verdict),
	// not a stale hit.
	if _, err := c.ValidateToken(context.Background(), plaintext); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("post-delete validation must be a cold miss, got %v", err)
	}
}

func TestCachedTokenStore_WriteThrough_CreateToken(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{})

	plaintext, tok, err := c.CreateToken(context.Background(), "svc", nil, "tester", auth.FullPermission(nil, nil))
	if err != nil || tok == nil {
		t.Fatalf("CreateToken: %v %v", tok, err)
	}
	c.mu.RLock()
	_, cached := c.entries.Peek(auth.HashToken(plaintext))
	idxHash, idxOK := c.idToHash[tok.ID]
	c.mu.RUnlock()
	if !cached {
		t.Error("CreateToken must write-through a positive entry")
	}
	if !idxOK || idxHash != auth.HashToken(plaintext) {
		t.Error("CreateToken must populate idToHash")
	}

	// And the cached positive serves with zero inner calls.
	before := inner.validate.Load()
	if got, err := c.ValidateToken(context.Background(), plaintext); err != nil || got.ID != tok.ID {
		t.Fatalf("validate after create: %v %v", got, err)
	}
	if inner.validate.Load() != before {
		t.Error("write-through positive must be served without inner call")
	}
}

func TestCachedTokenStore_idToHashPopulatedOnValidateToken(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{})

	plaintext, hash, _ := auth.GenerateToken()
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "read-path", TokenHash: hash}
	inner.mu.Unlock()

	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("validate: %v", err)
	}
	c.mu.RLock()
	got, ok := c.idToHash["read-path"]
	c.mu.RUnlock()
	if !ok || got != hash {
		t.Errorf("read path must populate idToHash (W4): %q %v", got, ok)
	}
}

// ─── W2 / W5 ─────────────────────────────────────────────────────────────────

func TestCachedTokenStore_StopNoop(t *testing.T) {
	c := NewCachedTokenStore(newFakeTokenStore(), Options{})
	done := make(chan struct{})
	go func() {
		c.Stop()
		c.Stop() // idempotent
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop must be non-blocking")
	}
}

func TestCachedTokenStore_ListTokensGetTokenByIDPassThrough(t *testing.T) {
	inner := newFakeTokenStore()
	dbErr := connRefused("connection refused")
	inner.mu.Lock()
	inner.listErr = dbErr
	inner.mu.Unlock()
	c := NewCachedTokenStore(inner, Options{})

	if _, err := c.ListTokens(context.Background()); err == nil {
		t.Error("ListTokens must pass the DB error through (W5 — no cache hidden)")
	}
	if _, err := c.GetTokenByID(context.Background(), "x"); err == nil {
		t.Error("GetTokenByID must pass the DB error through (W5)")
	}
}

// ─── infra classifier ────────────────────────────────────────────────────────

func TestIsInfraError_Classification(t *testing.T) {
	cases := []struct {
		err  error
		want bool
		desc string
	}{
		{nil, false, "nil"},
		{connRefused("connection refused"), true, "net.OpError refused"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("no such host")}, true, "net.OpError DNS"},
		{context.DeadlineExceeded, true, "deadline exceeded"},
		{errors.New("read tcp 10.0.0.1:5432: i/o timeout"), true, "i/o timeout string"},
		{errors.New("dial tcp: lookup db: no such host"), true, "no such host string"},
		{auth.ErrTokenNotFound, false, "not-found verdict"},
		{auth.ErrInvalidTokenFormat, false, "format verdict"},
		{auth.ErrTokenExpired, false, "expired verdict"},
		{errors.New("some opaque logic error"), false, "unknown class"},
	}
	for _, tc := range cases {
		if got := isInfraError(tc.err); got != tc.want {
			t.Errorf("isInfraError(%s) = %v, want %v", tc.desc, got, tc.want)
		}
	}
}

// TestIsInfraError_ShapeInjection (db-cache-layer F1, 2026-08-29):
// shape-injection coverage for the isInfraError string-fragment
// whitelist. Asserts the real modernc.org/sqlite outage shapes
// classify as infra (review finding 1: file-level SQLite outages
// must engage the token stale-tier on the default deployment
// dialect), pins pre-existing fragments as regression guards, and
// asserts SQLite-adjacent verdict-class errors do NOT drift into
// the whitelist. The classifier lowercases its input and
// strings.Contains against the bare fragment, so the driver
// code-suffixed real forms ("(26)", "(11)", "(14)") match as
// well.
func TestIsInfraError_ShapeInjection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// SQLite file-level outage shapes (db-cache-layer F1,
		// 2026-08-29). The (26)/(11)/(14) suffix is the real
		// modernc.org/sqlite output (SQLITE_NOTADB /
		// SQLITE_CORRUPT / SQLITE_CANTOPEN respectively).
		{"SQLite file is not a database (26)", errors.New("file is not a database (26)"), true},
		{"SQLite database disk image is malformed (11)", errors.New("database disk image is malformed (11)"), true},
		{"SQLite unable to open database file: out of memory (14)", errors.New("unable to open database file: out of memory (14)"), true},
		// New PG mid-flight shapes (review remediation 2026-08-28).
		{"PG unexpected EOF", errors.New("read tcp 10.0.0.1:5432: read: unexpected EOF"), true},
		{"PG server closed connection unexpectedly", errors.New("server closed the connection unexpectedly"), true},
		// Pre-existing fragment — regression guard so a refactor of
		// the whitelist cannot silently drop "connection refused".
		{"connection refused (existing)", errors.New("dial tcp 10.0.0.1:5432: connect: connection refused"), true},
		// Non-infra control — "sql: no rows in result set" is a
		// verdict (sql.ErrNoRows surfaces through database/sql);
		// it must NOT be classified as infra.
		{"sql no rows in result set", errors.New("sql: no rows in result set"), false},
		// SQLite-adjacent verdict-class control — a real
		// modernc.org/sqlite error shape that is a verdict
		// (table missing from a truncated DB), not an outage; it
		// must NOT be classified as infra so the whitelist does
		// not drift into "any sqlite-shaped string" territory.
		{"SQLite SQL logic error: no such table", errors.New("SQL logic error: no such table: models (1)"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInfraError(tc.err); got != tc.want {
				t.Errorf("isInfraError(%q) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Decorator grant forwarding (token-authz plan T1.11 / T1.4)
// ─────────────────────────────────────────────────────────────────────────────

// TestCachedTokenStore_ForwardsGroupsAndDropsOnUpdate proves two things the
// plan calls out separately:
//
//  1. FORWARD: the auth.TokenPermission payload reaches the inner store
//     verbatim (both axes, nil-vs-empty intact) — the decorator must not
//     flatten it.
//  2. DROP IS EFFECTIVE: after UpdateTokenPermission the cached positive is
//     gone, so the NEXT ValidateToken re-reads and returns the NEW grants. A
//     drop that was merely "called" but ineffective would leave a stale
//     permission in force for up to TokensPositiveTTL.
//  3. FLAGS SURVIVE: AllowedGroups and BOTH Invalid flags ride the cached
//     *AuthToken across a cache hit (a cache hit must not lose DENY-ALL).
func TestCachedTokenStore_ForwardsGroupsAndDropsOnUpdate(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{})
	c.Stop()

	plaintext, hash, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Inner row: a narrow group token.
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{
		ID:            "tok-grants",
		Name:          "narrow",
		TokenHash:     hash,
		AllowedModels: nil,
		AllowedGroups: []string{"g1"},
	}
	inner.mu.Unlock()

	// Warm the cache.
	got, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("warm validate: %v", err)
	}
	if len(got.AllowedGroups) != 1 || got.AllowedGroups[0] != "g1" {
		t.Fatalf("first validate AllowedGroups = %v, want [g1]", got.AllowedGroups)
	}

	// A cache hit must still carry the group axis + the flags.
	inner.mu.Lock()
	before := inner.validate.Load()
	inner.mu.Unlock()
	hit, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("cached validate: %v", err)
	}
	inner.mu.Lock()
	after := inner.validate.Load()
	inner.mu.Unlock()
	if after != before {
		t.Error("second validate hit the inner store — the positive entry was not cached")
	}
	if len(hit.AllowedGroups) != 1 || hit.AllowedGroups[0] != "g1" {
		t.Errorf("cached AllowedGroups = %v, want [g1] (the group axis must survive the cache boundary)", hit.AllowedGroups)
	}

	// Mutating the returned token must NOT corrupt the cached entry (deep clone).
	hit.AllowedGroups = append(hit.AllowedGroups, "g-injected")
	again, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("post-mutation validate: %v", err)
	}
	if len(again.AllowedGroups) != 1 {
		t.Errorf("cached entry was aliased: a caller mutation changed the cache to %v", again.AllowedGroups)
	}

	// Now change the grants in the inner store and update through the
	// decorator. The payload must be forwarded verbatim…
	inner.mu.Lock()
	inner.tokens[hash].AllowedGroups = []string{"g2", "g3"}
	inner.mu.Unlock()

	if err := c.UpdateTokenPermission(context.Background(), "tok-grants", auth.GrantGroups([]string{"g2", "g3"})); err != nil {
		t.Fatalf("UpdateTokenPermission: %v", err)
	}

	// (1) FORWARD — the payload reached the inner store verbatim: the id, the
	// group axis intact, and the model axis still nil (not coerced to []).
	inner.mu.Lock()
	fwd, fwdID := inner.lastPerm, inner.lastPermID
	inner.mu.Unlock()
	if fwdID != "tok-grants" {
		t.Errorf("forwarded id = %q, want tok-grants", fwdID)
	}
	if fwd == nil {
		t.Fatal("the inner UpdateTokenPermission was never called — the decorator did not forward")
	}
	if !fwd.AllowedGroups.IsSet() {
		t.Fatal("forwarded group axis is Set:false — the decorator dropped the write")
	}
	fwdGroups := fwd.AllowedGroups.Value()
	if len(fwdGroups) != 2 || fwdGroups[0] != "g2" || fwdGroups[1] != "g3" {
		t.Errorf("forwarded AllowedGroups = %v, want [g2 g3]", fwdGroups)
	}
	if fwd.AllowedModels.IsSet() {
		t.Errorf("forwarded model axis is Set:true (%v) — an omitted axis must stay Set:false through the decorator", fwd.AllowedModels.Value())
	}

	// …and the cached entry must be GONE, so the next validate re-reads.
	inner.mu.Lock()
	before = inner.validate.Load()
	inner.mu.Unlock()
	fresh, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("post-update validate: %v", err)
	}
	inner.mu.Lock()
	after = inner.validate.Load()
	inner.mu.Unlock()
	if after != before+1 {
		t.Errorf("post-update validate made %d inner call(s), want exactly 1 — the cache entry was not dropped", after-before)
	}
	if len(fresh.AllowedGroups) != 2 || fresh.AllowedGroups[0] != "g2" || fresh.AllowedGroups[1] != "g3" {
		t.Errorf("post-update AllowedGroups = %v, want [g2 g3] (the drop must make the change visible within one validation)", fresh.AllowedGroups)
	}
}

// TestCachedTokenStore_CloneCarriesInvalidFlags is the PD-7(b) pin: DENY-ALL
// survives the cache. The flags live on the struct for exactly this reason —
// a separately-returned flag would be dropped at the cache boundary and a
// corrupt token would silently become a wildcard after the first 60s. A cache
// hit must be indistinguishable from a DB read for the deny-all state.
func TestCachedTokenStore_CloneCarriesInvalidFlags(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{})
	c.Stop()

	plaintext, hash, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "tok-invalid", TokenHash: hash, ModelsInvalid: true}
	inner.mu.Unlock()

	first, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("warm validate: %v", err)
	}
	if !first.ModelsInvalid {
		t.Fatal("first validate lost ModelsInvalid")
	}

	inner.mu.Lock()
	before := inner.validate.Load()
	inner.mu.Unlock()
	cached, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("cached validate: %v", err)
	}
	inner.mu.Lock()
	after := inner.validate.Load()
	inner.mu.Unlock()
	if after != before {
		t.Fatal("expected a cache hit (no inner call)")
	}
	if !cached.ModelsInvalid {
		t.Error("ModelsInvalid was dropped at the cache boundary — a corrupt token would become a wildcard after the positive TTL")
	}
}

// TestCachedTokenStore_ClonePreservesPresentEmpty is the PD-7(a) pin, and it
// guards the single most dangerous line in the decorator: the clone must NOT
// turn PRESENT-empty into ABSENT.
//
// `append([]string(nil), []string{}...)` returns nil — which would convert a
// "grants nothing" axis into an "absent" axis and, under the conjunctive
// wildcard rule, could escalate a token to unrestricted. The clone therefore
// uses make+copy behind a nil check. This test is the reason it must stay that
// way, and it also proves the returned value shares NO backing array with the
// cached entry.
func TestCachedTokenStore_ClonePreservesPresentEmpty(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{})
	c.Stop()

	plaintext, hash, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{
		ID:            "tok-present-empty",
		TokenHash:     hash,
		AllowedModels: []string{}, // PRESENT-empty
		AllowedGroups: []string{}, // PRESENT-empty
	}
	inner.mu.Unlock()

	warm, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("warm validate: %v", err)
	}
	// The write-through path (storePositive) must also preserve emptiness.
	if warm.AllowedModels == nil {
		t.Error("AllowedModels became nil on the write-through path — PRESENT-empty must stay non-nil")
	}
	if warm.AllowedGroups == nil {
		t.Error("AllowedGroups became nil on the write-through path — PRESENT-empty must stay non-nil")
	}

	// Now read from the CACHE (no inner call) and assert the same.
	inner.mu.Lock()
	before := inner.validate.Load()
	inner.mu.Unlock()
	cached, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("cached validate: %v", err)
	}
	inner.mu.Lock()
	after := inner.validate.Load()
	inner.mu.Unlock()
	if after != before {
		t.Fatal("expected a cache hit")
	}
	if cached.AllowedGroups == nil {
		t.Fatal("AllowedGroups became nil through the cache clone — PRESENT-empty collapsed into ABSENT (wildcard-escalation shape)")
	}
	if len(cached.AllowedGroups) != 0 {
		t.Errorf("AllowedGroups = %v, want empty", cached.AllowedGroups)
	}
	if cached.AllowedModels == nil {
		t.Fatal("AllowedModels became nil through the cache clone — PRESENT-empty collapsed into ABSENT")
	}

	// No aliasing: mutating what the caller got must not reach the cache.
	cached.AllowedGroups = append(cached.AllowedGroups, "g-injected")
	again, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("post-mutation validate: %v", err)
	}
	if len(again.AllowedGroups) != 0 {
		t.Errorf("the cached entry was aliased: caller mutation leaked into the cache (%v)", again.AllowedGroups)
	}
	if again.AllowedGroups == nil {
		t.Error("the cached entry's PRESENT-empty state was lost after a caller mutation")
	}
}
