package credentiallb

import (
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// weightedSelector implements cumulative-prefix-sum weighted random
// selection: O(k) build, O(log k) pick, zero allocation on the pick
// hot path.
//
// Ordering: the slice order of refs (== Position order, enforced by
// Phase 1 validation) defines the interval order, so equal weights
// occupy adjacent equal-width intervals and distribute evenly and
// deterministically — Position is the weight-tie breaker by
// construction (lower position wins the earlier interval).
type weightedSelector struct {
	credentialIDs []string
	prefixSum     []int // cumulative weights; prefixSum[i] = sum(weights[0..i])
	totalWeight   int
}

// newWeightedSelector builds the prefix sums over refs.
//
// Defensive rejects (Phase 1 Validate is the primary gate; this is the
// belt-and-braces path): returns nil when refs is empty or ANY weight
// is <= 0. Callers treat a nil selector as "no valid credentials"
// (GetOrSelect surfaces ErrNoCredentials).
func newWeightedSelector(refs []models.CredentialRef) *weightedSelector {
	if len(refs) == 0 {
		return nil
	}
	ids := make([]string, len(refs))
	prefix := make([]int, len(refs))
	total := 0
	for i, r := range refs {
		if r.Weight <= 0 {
			return nil
		}
		total += r.Weight
		prefix[i] = total
		ids[i] = r.CredentialID
	}
	return &weightedSelector{
		credentialIDs: ids,
		prefixSum:     prefix,
		totalWeight:   total,
	}
}

// pick maps r ∈ [0, totalWeight) to the credential whose interval
// contains r: binary search for the FIRST prefix strictly greater
// than r. Callers generate r via rng.Intn(totalWeight); pick must not
// be called on a nil selector or with r outside the range.
func (w *weightedSelector) pick(r int) string {
	lo, hi := 0, len(w.prefixSum)-1
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if w.prefixSum[mid] > r {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return w.credentialIDs[lo]
}
