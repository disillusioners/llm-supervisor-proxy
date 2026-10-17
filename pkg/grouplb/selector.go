package grouplb

// weightedSelector implements cumulative-prefix-sum weighted random
// selection over MemberRef weights (float64 — G3 weights are floats):
// O(k) build, O(log k) pick, zero allocation on the pick hot path.
//
// Mechanical clone of pkg/credentiallb/selector.go with the weight
// axis widened to float64 (GroupMember.Weight is a float; the
// credential twin uses int). Ordering: the slice order of refs (==
// Position order, enforced by validation) defines the interval order,
// so equal weights occupy adjacent equal-width intervals and
// distribute evenly and deterministically — Position is the
// weight-tie breaker by construction.
type weightedSelector struct {
	memberIDs   []string
	prefixSum   []float64 // cumulative weights; prefixSum[i] = sum(weights[0..i])
	totalWeight float64
}

// newWeightedSelector builds the prefix sums over refs.
//
// Defensive rejects (write-path Validate is the primary gate; this is
// the belt-and-braces path): returns nil when refs is empty or ANY
// weight is <= 0. Callers treat a nil selector as "no valid members"
// (GetOrSelect surfaces ErrNoMembers).
func newWeightedSelector(refs []MemberRef) *weightedSelector {
	if len(refs) == 0 {
		return nil
	}
	ids := make([]string, len(refs))
	prefix := make([]float64, len(refs))
	total := 0.0
	for i, r := range refs {
		if r.Weight <= 0 {
			return nil
		}
		total += r.Weight
		prefix[i] = total
		ids[i] = r.ModelID
	}
	return &weightedSelector{
		memberIDs:   ids,
		prefixSum:   prefix,
		totalWeight: total,
	}
}

// pick maps r ∈ [0, totalWeight) to the member whose interval
// contains r: binary search for the FIRST prefix strictly greater
// than r. Callers generate r via rng.Float64()*totalWeight; pick must
// not be called on a nil selector or with r outside the range.
func (w *weightedSelector) pick(r float64) string {
	lo, hi := 0, len(w.prefixSum)-1
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if w.prefixSum[mid] > r {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return w.memberIDs[lo]
}
