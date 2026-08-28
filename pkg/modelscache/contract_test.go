package modelscache

// contract_test.go — the failure-mode matrix from
// architecture-recommendation.md §2 plus the C2-amended token rows.
// Every row is one focused subtest so a regression maps to exactly
// one cell of the matrix.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
)

func TestFailureModeMatrix_Row1_HitServesCachedZeroInner(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	listBefore := inner.listCalls()
	singleBefore := func() int { m, mb, g := inner.snapshotCounts(); return m + mb + g }

	// Reads with the cache warm — including while the source is down.
	inner.mu.Lock()
	inner.listErr = connRefused("connection refused")
	inner.mu.Unlock()

	if c.GetModel("m-alpha") == nil {
		t.Error("row 1: model hit must serve")
	}
	if len(c.GetModels()) != 3 {
		t.Error("row 1: list hit must serve")
	}
	if len(c.GetCredentials()) != 2 {
		t.Error("row 1: credentials hit must serve")
	}
	if _, ok := c.ResolveInternalConfigWithAffinity("m-alpha", "conv"); !ok {
		t.Error("row 1: resolution hit must serve")
	}
	if inner.listCalls() != listBefore {
		t.Error("row 1: hits must make zero list reads")
	}
	if m, mb, g := inner.snapshotCounts(); m+mb+g != singleBefore() {
		t.Errorf("row 1: hits must make zero single-row reads (%d/%d/%d)", m, mb, g)
	}
}

func TestFailureModeMatrix_Row2_MissStrictFillsWhenOK(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	// A credential not covered by boot priming? Boot primes all —
	// evict one to force a miss, then read: strict-fill serves it.
	c.mu.Lock()
	delete(c.credsByID, "cred-2")
	c.mu.Unlock()
	if cred := c.GetCredential("cred-2"); cred == nil || cred.APIKey != "sk-plain-2" {
		t.Errorf("row 2: miss must strict-fill and serve: %+v", cred)
	}
}

func TestFailureModeMatrix_Row3_NotFoundNegativeLegitExternal(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	if m := c.GetModel("external-gpt"); m != nil {
		t.Fatalf("row 3: not-found must return nil, got %+v", m)
	}
	if !c.Healthy() {
		t.Error("row 3: a not-found verdict with DB up must keep healthy=true (legit external passthrough signal)")
	}
	// Negative is cached: repeat read stays off the DB.
	byID, _, _ := inner.snapshotCounts()
	if c.GetModel("external-gpt") != nil {
		t.Error("row 3: repeat not-found read")
	}
	byID2, _, _ := inner.snapshotCounts()
	if byID2 != byID {
		t.Error("row 3: negative must be served from cache")
	}
}

func TestFailureModeMatrix_Row4_UnknownMissDownIsUnavailable(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	inner.mu.Lock()
	inner.modelErr = connRefused("connection refused")
	inner.credErr = connRefused("connection refused")
	inner.listErr = connRefused("connection refused")
	inner.mu.Unlock()

	m := c.GetModel("never-seen")
	if m != nil {
		t.Fatalf("row 4: expected nil, got %+v", m)
	}
	if c.Healthy() {
		t.Error("row 4: nil + infra error must mark the store unhealthy (the 503 signal)")
	}
	// The exported sentinel is available for errors.Is matching by the
	// boundary / contract tests.
	if !errors.Is(ErrConfigUnavailable, ErrConfigUnavailable) {
		t.Error("ErrConfigUnavailable must be usable with errors.Is")
	}
}

func TestFailureModeMatrix_Row5_CredentialMissingNeverCiphertext(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{})

	// Simulate an undecryptable stored credential: the strict read
	// returns ErrDecryptionFailed (never the ciphertext).
	inner.mu.Lock()
	inner.credErr = database.ErrDecryptionFailed
	inner.mu.Unlock()
	c.mu.Lock()
	delete(c.credsByID, "cred-1")
	c.mu.Unlock()

	cred := c.GetCredential("cred-1")
	if cred != nil {
		t.Fatalf("row 5: decrypt failure must NOT serve a credential: %+v", cred)
	}
	// And it is negative-cached: repeat reads neither serve nor retry.
	if c.GetCredential("cred-1") != nil {
		t.Error("row 5: decrypt failure must be negative-cached")
	}
	// The resolver fails closed for a model referencing it (ok=false —
	// never a dangling reference, never ciphertext).
	if res, ok := c.ResolveInternalConfigWithAffinity("m-alpha", "conv"); ok {
		t.Errorf("row 5: resolution must fail, got %+v", res)
	}
	if _, _, _, _, ok := c.ResolveInternalConfig("m-alpha"); ok {
		t.Error("row 5: legacy resolution must fail too")
	}
	// Never ciphertext: nothing in the cache carries raw ciphertext.
	c.mu.RLock()
	e := c.credsByID["cred-1"]
	c.mu.RUnlock()
	if e.cred != nil || e.decryptOK {
		t.Error("row 5: decrypt-failed entry must hold no key material")
	}
}

func TestFailureModeMatrix_Row6_StaleDownServesLastKnownGood(t *testing.T) {
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{ReconcileInterval: time.Hour})

	inner.mu.Lock()
	inner.listErr = connRefused("connection refused")
	inner.mu.Unlock()
	c.reconcileOnce()

	if c.Healthy() {
		t.Fatal("row 6: store must be marked unhealthy after failed tick")
	}
	if len(c.GetModels()) != 3 {
		t.Error("row 6: stale read must serve last-known-good")
	}
	if m := c.GetModel("m-alpha"); m == nil {
		t.Error("row 6: stale model read must serve last-known-good")
	}
	if _, ok := c.ResolveInternalConfigWithAffinity("m-alpha", "conv"); !ok {
		t.Error("row 6: stale resolution must serve last-known-good")
	}
}

// ─── token matrix rows ───────────────────────────────────────────────────────

func TestFailureModeMatrix_RowA_AuthKnownServesToken(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{})
	c.Stop()

	plaintext, hash, _ := auth.GenerateToken()
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "rowA", TokenHash: hash}
	inner.mu.Unlock()

	tok, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil || tok == nil || tok.ID != "rowA" {
		t.Fatalf("row A: %v %v", tok, err)
	}
	// Second read is served from the positive cache with the store down.
	inner.mu.Lock()
	inner.infraErr = connRefused("connection refused")
	inner.mu.Unlock()
	if tok, err := c.ValidateToken(context.Background(), plaintext); err != nil || tok == nil {
		t.Fatalf("row A (fresh positive, store down): %v %v", tok, err)
	}
}

func TestFailureModeMatrix_RowB_AuthInvalidNegativeRejects(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{})
	c.Stop()

	plaintext, _, _ := auth.GenerateToken()
	if _, err := c.ValidateToken(context.Background(), plaintext); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("row B: %v", err)
	}
	// Even with the store down, the negative verdict is replayed
	// (fail-closed; the negative tier is never stale-served).
	inner.mu.Lock()
	inner.infraErr = connRefused("connection refused")
	inner.mu.Unlock()
	if _, err := c.ValidateToken(context.Background(), plaintext); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("row B (store down): %v", err)
	}
}

func TestFailureModeMatrix_RowC_AuthNeverSeenDownIs401Class(t *testing.T) {
	inner := newFakeTokenStore()
	inner.mu.Lock()
	inner.infraErr = connRefused("connection refused")
	inner.mu.Unlock()
	c := NewCachedTokenStore(inner, Options{})
	c.Stop()

	plaintext, _, _ := auth.GenerateToken()
	_, err := c.ValidateToken(context.Background(), plaintext)
	if err == nil || !isInfraError(err) {
		t.Fatalf("row C: never-seen + down must surface infra error (caller 401s), got %v", err)
	}
}

func TestFailureModeMatrix_RowA2_StaleTierServesDuringInfraOutage(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{Clock: clk.Now})
	c.Stop()

	plaintext, hash, _ := auth.GenerateToken()
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "rowA2", TokenHash: hash}
	inner.mu.Unlock()
	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("row A2 warm: %v", err)
	}

	clk.Advance(time.Hour) // t=∞ within the 24h cap
	inner.mu.Lock()
	inner.infraErr = connRefused("connection refused")
	inner.mu.Unlock()

	tok, err := c.ValidateToken(context.Background(), plaintext)
	if err != nil || tok == nil || tok.ID != "rowA2" {
		t.Fatalf("row A2: stale tier must serve during infra outage, got %v %v", tok, err)
	}
}

func TestFailureModeMatrix_RowB2_VerdictsDoNotFallBackToStale(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{Clock: clk.Now})
	c.Stop()

	plaintext, hash, _ := auth.GenerateToken()
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "rowB2", TokenHash: hash}
	inner.mu.Unlock()
	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("row B2 warm: %v", err)
	}
	clk.Advance(90 * time.Second)

	// Revalidation reaches the DB, which answers with a VERDICT — the
	// stale tier must NOT be served.
	inner.mu.Lock()
	delete(inner.tokens, hash)
	inner.mu.Unlock()
	if _, err := c.ValidateToken(context.Background(), plaintext); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("row B2: verdict-class error must win, got %v", err)
	}
}

func TestFailureModeMatrix_RowC2_DeleteTokenRemovesIDIndex(t *testing.T) {
	inner := newFakeTokenStore()
	c := NewCachedTokenStore(inner, Options{})
	c.Stop()

	plaintext, hash, _ := auth.GenerateToken()
	inner.mu.Lock()
	inner.tokens[hash] = &auth.AuthToken{ID: "rowC2", TokenHash: hash}
	inner.mu.Unlock()
	if _, err := c.ValidateToken(context.Background(), plaintext); err != nil {
		t.Fatalf("row C2 warm: %v", err)
	}
	if err := c.DeleteToken(context.Background(), "rowC2"); err != nil {
		t.Fatalf("row C2 delete: %v", err)
	}

	c.mu.RLock()
	_, hashEntry := c.entries.Peek(hash)
	_, idx := c.idToHash["rowC2"]
	c.mu.RUnlock()
	if hashEntry || idx {
		t.Fatalf("row C2: entries must be cleared (hash=%v idx=%v)", hashEntry, idx)
	}
	// Re-validation is a cold miss against the inner store — not a
	// stale hit.
	if _, err := c.ValidateToken(context.Background(), plaintext); !errors.Is(err, auth.ErrTokenNotFound) {
		t.Fatalf("row C2: post-delete must be cold, got %v", err)
	}
}
