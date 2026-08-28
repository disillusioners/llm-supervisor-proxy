package modelscache

// stress_test.go — concurrent-readers + mutator + reconciler-driver
// stress test (review remediation 2026-08-28, finding: zero
// `t.Parallel()`/interleaving coverage; `-race` only proves
// sequential paths).
//
// Runs four reader classes, a mutator, and a reconciler-driver
// simultaneously under `-race`. Asserts:
//   - no torn reads (every returned model.ID matches the queried ID,
//     every returned model.Name matches the queried name, every
//     returned credential.ID matches the queried ID),
//   - deep-copy invariant (mutating a returned value never affects
//     subsequent reads),
//   - final-state correctness (after the mutator settles and one
//     final reconcile, GetModel returns the last written state).
//
// Bounded: ~2.5s of hammering, goroutine counts modest (4 per
// reader class), deterministic teardown via WaitGroup + done channel.

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestStress_ConcurrentReadersMutatorReconciler is the headline
// concurrency test. Each class runs in its own goroutines; the test
// asserts no race detector reports, no torn reads, no deep-copy leaks,
// and final-state correctness.
func TestStress_ConcurrentReadersMutatorReconciler(t *testing.T) {
	// Real-time clock + an effectively-disabled ticker reconciler;
	// we drive reconcileOnce manually below so the swaps are
	// synchronized with the reader/mutator traffic for clean
	// teardown.
	clk := newFakeClock(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	inner := &fakeStrictSource{models: fixtureModels(), creds: fixtureCreds()}
	c := mustWrap(t, inner, Options{
		Clock:             clk.Now,
		ReconcileInterval: time.Hour,
	})

	// Token cache: wrap a fake inner store with two known tokens so
	// ValidateToken reads have a non-trivial path to hammer.
	tokInner := newFakeTokenStore()
	tokPlain1 := seedStressToken(t, tokInner, "stress-tok-1")
	tokPlain2 := seedStressToken(t, tokInner, "stress-tok-2")
	cTokens := WrapTokens(tokInner, Options{Clock: clk.Now})
	defer cTokens.Stop()

	// Track the mutator's last written state for the final-state
	// assertion at the bottom.
	var (
		lastMu    sync.Mutex
		lastOp    string
		lastModel *models.ModelConfig
	)
	recordLast := func(op string, m *models.ModelConfig) {
		lastMu.Lock()
		defer lastMu.Unlock()
		lastOp = op
		lastModel = m
	}
	getLast := func() (string, *models.ModelConfig) {
		lastMu.Lock()
		defer lastMu.Unlock()
		return lastOp, lastModel
	}

	// ── Readers ──────────────────────────────────────────────────────────────
	var readersWG sync.WaitGroup
	readersDone := make(chan struct{})
	var readErrors atomic.Int64

	// runReader spawns one goroutine per slot. It loops fn() until
	// readersDone closes. A torn read or invariant violation is
	// reported via t.Errorf and counted in readErrors.
	runReader := func(name string, fn func() error) {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-readersDone:
					return
				default:
				}
				if err := fn(); err != nil {
					readErrors.Add(1)
					t.Errorf("[%s] %v", name, err)
					return
				}
				runtime.Gosched()
			}
		}()
	}

	modelIDs := []string{"m-alpha", "m-beta", "m-gamma", "never-seen"}
	modelNames := []string{"alpha", "beta", "gamma", "neverseen"}
	credIDs := []string{"cred-1", "cred-2", "cred-unknown"}

	// Reader class (a): GetModel — torn-read + deep-copy invariant.
	for i := 0; i < 4; i++ {
		runReader(fmt.Sprintf("getmodel-%d", i), func() error {
			for _, id := range modelIDs {
				m := c.GetModel(id)
				if m == nil {
					continue
				}
				if m.ID != id {
					return fmt.Errorf("torn read: GetModel(%q).ID=%q", id, m.ID)
				}
				// Deep-copy invariant: mutating the returned
				// value must not affect the next read.
				m.Name = "MUTATED-LEAK"
				m2 := c.GetModel(id)
				if m2 != nil && m2.Name == "MUTATED-LEAK" {
					return fmt.Errorf("deep-copy violation: mutation of returned %q.Name leaked to next read", id)
				}
			}
			return nil
		})
	}

	// Reader class (b): GetModelByName — torn-read + cross-index
	// consistency with GetModel.
	for i := 0; i < 4; i++ {
		runReader(fmt.Sprintf("getmodelbyname-%d", i), func() error {
			for j, name := range modelNames {
				m := c.GetModelByName(name)
				if m == nil {
					continue
				}
				if m.Name != name {
					return fmt.Errorf("torn read: GetModelByName(%q).Name=%q", name, m.Name)
				}
				// Cross-index consistency: the by-name result must
				// agree with the by-id result on existence and ID.
				id := modelIDs[j]
				m2 := c.GetModel(id)
				if (m == nil) != (m2 == nil) {
					return fmt.Errorf("cross-index inconsistency: by-name nil=%v by-id nil=%v for %q", m == nil, m2 == nil, id)
				}
				if m != nil && m2 != nil && m.ID != m2.ID {
					return fmt.Errorf("cross-index ID mismatch: by-name.ID=%q by-id.ID=%q", m.ID, m2.ID)
				}
			}
			return nil
		})
	}

	// Reader class (c): GetCredential — torn-read + deep-copy
	// invariant on the APIKey field.
	for i := 0; i < 4; i++ {
		runReader(fmt.Sprintf("getcred-%d", i), func() error {
			for _, id := range credIDs {
				cred := c.GetCredential(id)
				if cred == nil {
					continue
				}
				if cred.ID != id {
					return fmt.Errorf("torn read: GetCredential(%q).ID=%q", id, cred.ID)
				}
				cred.APIKey = "leaked"
				cred2 := c.GetCredential(id)
				if cred2 != nil && cred2.APIKey == "leaked" {
					return fmt.Errorf("deep-copy violation: mutation of returned %q.APIKey leaked to next read", id)
				}
			}
			return nil
		})
	}

	// Reader class (d): ValidateToken on the wrapped token store —
	// exercises the token cache hot path alongside the models cache
	// reader traffic.
	for i := 0; i < 4; i++ {
		runReader(fmt.Sprintf("validate-%d", i), func() error {
			for _, plaintext := range []string{tokPlain1, tokPlain2} {
				tok, err := cTokens.ValidateToken(context.Background(), plaintext)
				if err != nil || tok == nil {
					return fmt.Errorf("ValidateToken: %v %v", tok, err)
				}
			}
			return nil
		})
	}

	// ── Mutator ──────────────────────────────────────────────────────────────
	// Calls UpdateModel on m-alpha in a tight loop, varying the
	// name (rename path) and other fields (Enabled, InternalModel,
	// Credentials). The ID stays "m-alpha" so UpdateModel always
	// resolves; the rename exercises the name-index update path.
	var mutatorWG sync.WaitGroup
	mutatorDone := make(chan struct{})
	mutatorWG.Add(1)
	go func() {
		defer mutatorWG.Done()
		defer close(mutatorDone)

		names := []string{"alpha", "alpha-v1", "alpha-v2", "alpha-renamed"}
		i := 0
		end := time.Now().Add(1800 * time.Millisecond)
		for time.Now().Before(end) {
			m := models.ModelConfig{
				ID:            "m-alpha",
				Name:          names[i%len(names)],
				Enabled:       i%2 == 0,
				InternalModel: fmt.Sprintf("v-%d", i),
				Credentials: []models.CredentialRef{
					{CredentialID: "cred-1", Weight: 1, Position: 0},
				},
			}
			if err := c.UpdateModel("m-alpha", m); err != nil {
				t.Errorf("UpdateModel: %v", err)
				return
			}
			recordLast("UpdateModel:m-alpha", &m)
			i++
			runtime.Gosched()
		}
	}()

	// ── Reconciler driver ────────────────────────────────────────────────────
	// Drive reconcileOnce manually (the same seam the existing
	// models_test.go tests use), alternating between "DB healthy"
	// and "DB down" states via listErr. The forced swaps straddle
	// reader activity so any read-during-swap race surfaces.
	//
	// Note: only the test goroutine owns close(reconcilerDone); the
	// goroutine itself must NOT also defer-close the channel, or the
	// select-armed return races the explicit close and panics with
	// "close of closed channel".
	var reconcilerWG sync.WaitGroup
	reconcilerDone := make(chan struct{})
	reconcilerWG.Add(1)
	go func() {
		defer reconcilerWG.Done()
		i := 0
		for {
			select {
			case <-reconcilerDone:
				return
			case <-time.After(20 * time.Millisecond):
				inner.mu.Lock()
				if i%5 == 4 {
					inner.listErr = connRefused("connection refused")
				} else {
					inner.listErr = nil
				}
				inner.mu.Unlock()
				c.reconcileOnce()
				i++
			}
		}
	}()

	// ── Bounded hammering + deterministic teardown ──────────────────────────
	time.Sleep(2 * time.Second)
	close(readersDone)
	readersWG.Wait()
	<-mutatorDone
	mutatorWG.Wait()
	close(reconcilerDone)
	<-reconcilerDone
	reconcilerWG.Wait()

	// ── Final state correctness ─────────────────────────────────────────────
	// Clear listErr and force one final reconcile so the mutator's
	// last write-through propagates to the cache snapshot.
	inner.mu.Lock()
	inner.listErr = nil
	inner.mu.Unlock()
	c.reconcileOnce()

	if readErrors.Load() != 0 {
		t.Fatalf("readers reported %d torn-read / invariant violations", readErrors.Load())
	}
	if !c.Healthy() {
		t.Error("final reconcile must restore healthy=true")
	}
	op, m := getLast()
	if m == nil {
		t.Fatal("mutator never recorded a state — test wiring bug")
	}
	got := c.GetModel(m.ID)
	if got == nil {
		t.Fatalf("final reconcile did not surface last mutator state (%s, ID=%q)", op, m.ID)
	}
	if got.InternalModel != m.InternalModel {
		t.Errorf("final state mismatch: got.InternalModel=%q, want %q", got.InternalModel, m.InternalModel)
	}
	if got.Name != m.Name {
		t.Errorf("final state name mismatch: got=%q, want %q", got.Name, m.Name)
	}
}

// seedStressToken inserts a known token into the fake store under a
// chosen ID and returns the plaintext handle so callers can drive
// ValidateToken reads against the wrapped token cache.
func seedStressToken(t *testing.T, store *fakeTokenStore, id string) string {
	t.Helper()
	plaintext, hash, _ := auth.GenerateToken()
	store.mu.Lock()
	store.tokens[hash] = &auth.AuthToken{ID: id, TokenHash: hash}
	store.mu.Unlock()
	return plaintext
}
