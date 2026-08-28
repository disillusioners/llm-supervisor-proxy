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

func (f *fakeTokenStore) CreateToken(ctx context.Context, name string, expiresAt *time.Time, createdBy string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) (string, *auth.AuthToken, error) {
	plaintext, _, _ := auth.GenerateToken()
	hash := auth.HashToken(plaintext)
	tok := &auth.AuthToken{ID: "id-" + name, Name: name, TokenHash: hash, ExpiresAt: expiresAt, CreatedBy: createdBy}
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

func (f *fakeTokenStore) UpdateTokenPermission(ctx context.Context, id string, ultimateModelEnabled bool, ultimateModelID string, allowedModels []string) error {
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

	plaintext, tok, err := c.CreateToken(context.Background(), "svc", nil, "tester", false, "", nil)
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

// TestIsInfraError_ShapeInjection (review remediation 2026-08-28):
// shape-injection coverage for the isInfraError string-fragment
// whitelist. Asserts both new PG mid-flight disconnect shapes
// classify as infra, pins one pre-existing fragment as a regression
// guard, and asserts that an unrelated "no rows in result set" string
// (a verdict-class message, not infrastructure) classifies as NOT
// infra — a control to keep the whitelist from drifting into verdict
// territory.
func TestIsInfraError_ShapeInjection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInfraError(tc.err); got != tc.want {
				t.Errorf("isInfraError(%q) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
