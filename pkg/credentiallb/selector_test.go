package credentiallb

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestSelector_PrefixSum — weights [1,1,2] build prefix sums [1,2,4]
// over the refs' Position order (Files row #3 / Task 3 acceptance).
func TestSelector_PrefixSum(t *testing.T) {
	w := newWeightedSelector(models.TestRefsWeighted(
		models.CredentialRef{CredentialID: "A", Weight: 1},
		models.CredentialRef{CredentialID: "B", Weight: 1},
		models.CredentialRef{CredentialID: "C", Weight: 2},
	))
	if w == nil {
		t.Fatal("nil selector for valid refs")
	}
	want := []int{1, 2, 4}
	if len(w.prefixSum) != len(want) {
		t.Fatalf("prefix length: got %d want %d", len(w.prefixSum), len(want))
	}
	for i, got := range w.prefixSum {
		if got != want[i] {
			t.Fatalf("prefixSum[%d]: got %d want %d", i, got, want[i])
		}
	}
	if w.totalWeight != 4 {
		t.Fatalf("totalWeight: got %d want 4", w.totalWeight)
	}
}

// TestSelector_IntervalBoundaries — r=0..3 map to the interval owner:
// A owns [0,1), B owns [1,2), C owns [2,4). r=0 and r=total-1 are the
// pinned boundary probes.
func TestSelector_IntervalBoundaries(t *testing.T) {
	w := newWeightedSelector(models.TestRefsWeighted(
		models.CredentialRef{CredentialID: "A", Weight: 1},
		models.CredentialRef{CredentialID: "B", Weight: 1},
		models.CredentialRef{CredentialID: "C", Weight: 2},
	))
	cases := map[int]string{0: "A", 1: "B", 2: "C", 3: "C"}
	for r, want := range cases {
		if got := w.pick(r); got != want {
			t.Fatalf("pick(%d): got %s want %s", r, got, want)
		}
	}
}

// TestSelector_FourCredentialBoundary — Item 5b: the boundary
// correctness must hold at n>=4 too. Weights [1,1,2,1] → prefix
// [1,2,4,5], totalWeight=5. The r=totalWeight-1=4 probe MUST land
// in the LAST bucket (index 3 = D), proving the binary-search
// upper-bound semantics work at the very end of the prefix array
// (a class of off-by-one that the n=3 fixture cannot catch). The
// neighbouring r=3 probe lands in the SECOND-TO-LAST bucket (index
// 2 = C, the weight-2 entry), pinning the last-vs-second-to-last
// boundary transition.
func TestSelector_FourCredentialBoundary(t *testing.T) {
	w := newWeightedSelector(models.TestRefsWeighted(
		models.CredentialRef{CredentialID: "A", Weight: 1},
		models.CredentialRef{CredentialID: "B", Weight: 1},
		models.CredentialRef{CredentialID: "C", Weight: 2},
		models.CredentialRef{CredentialID: "D", Weight: 1},
	))
	if w == nil {
		t.Fatal("nil selector for valid 4-ref fixture")
	}
	if w.totalWeight != 5 {
		t.Fatalf("totalWeight: got %d want 5", w.totalWeight)
	}
	// r=0..4 → A, B, C, C, D.
	cases := map[int]string{0: "A", 1: "B", 2: "C", 3: "C", 4: "D"}
	for r, want := range cases {
		if got := w.pick(r); got != want {
			t.Fatalf("pick(%d): got %s want %s", r, got, want)
		}
	}
	// The pinned r=totalWeight-1=4 probe lands in the LAST bucket
	// (D, index 3) — off-by-one in the binary search would land
	// it in C (index 2).
	if got := w.pick(w.totalWeight - 1); got != "D" {
		t.Fatalf("last-bucket boundary: pick(totalWeight-1=%d): got %s want D", w.totalWeight-1, got)
	}
	// And the neighbouring r=3 (second-to-last interval boundary)
	// lands in C (index 2), NOT D.
	if got := w.pick(3); got != "C" {
		t.Fatalf("second-to-last boundary: pick(3): got %s want C", got)
	}
}

// TestSelector_SingleCredential — k=1 always returns the same
// credential for every r.
func TestSelector_SingleCredential(t *testing.T) {
	w := newWeightedSelector(models.TestRefs("only"))
	for r := 0; r < w.totalWeight; r++ {
		if got := w.pick(r); got != "only" {
			t.Fatalf("pick(%d): got %s want only", r, got)
		}
	}
}

// TestSelector_RejectsInvalidBuilds — empty refs and any weight <= 0
// are rejected at build time (defensive; Phase 1 Validate is the
// primary gate). A nil selector means "no valid credentials". Raw
// literals (NOT TestRefsWeighted — the fixture defensively rewrites
// non-positive weights to 1) reach the selector's own gate.
func TestSelector_RejectsInvalidBuilds(t *testing.T) {
	if w := newWeightedSelector(nil); w != nil {
		t.Fatal("nil refs must build a nil selector")
	}
	if w := newWeightedSelector(models.TestRefs()); w != nil {
		t.Fatal("empty refs must build a nil selector")
	}
	if w := newWeightedSelector([]models.CredentialRef{{CredentialID: "A", Weight: 0}}); w != nil {
		t.Fatal("weight 0 must be rejected at build")
	}
	if w := newWeightedSelector([]models.CredentialRef{
		{CredentialID: "A", Weight: 1},
		{CredentialID: "B", Weight: -3},
	}); w != nil {
		t.Fatal("negative weight must be rejected at build")
	}
}
