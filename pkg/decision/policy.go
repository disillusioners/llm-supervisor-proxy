package decision

// The GROUP layer of the decision lane: the POLICY facts and the
// policy pick.
//
// POLICY ONLY. This file carries NO failure state — no marks, no
// cooldowns, no attempt history, no health cache of any kind. The
// pick is a pure function of the group's pools + weights + configured
// order, so a credential-layer or model-layer state change can never
// reach it (the layer fence is structural, not a rule to remember).
//
// The attempt-suppression stage — the only place that reads the health
// layers — lives in its own file; this file must never import it.
//
// The doctrine (verbatim, also pinned at the suppression seam):
//
//	The cache is an optimization, not the truth: it may reorder candidates, never exclude them — and it
//	may suppress an ATTEMPT, never a PICK or a CHAIN.

import (
	"math/rand"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// policyMember is one CANDIDATE of a decision group: its id, its
// configured weight and the pool it belongs to.
type policyMember struct {
	ID     string
	Weight float64
	Pool   string
}

// groupPolicy is the GROUP-layer policy fact set of one group: the
// candidates, their weights and their pools, in CONFIGURED order.
//
// The lane is carried along so the walk can assert it never drifts
// from the group's own lane (defence in depth: the write-time
// homogeneity rule plus the request-time inference already agree).
type groupPolicy struct {
	GroupID string
	Lane    models.GroupLane
	Members []policyMember
}

// buildGroupPolicy derives the policy facts of a group from the group
// row and the CURRENT member rows.
//
// Candidacy (the request-time half of the servability rule): a member
// is a candidate when its row EXISTS, is ENABLED and belongs to the
// group's lane. An inactive member is never offered as a candidate
// (it is a chain target, not a group member — two populations).
//
// The member rows are read from the caller's row set (the live model
// set), never through the claims-first client-facing lookup: a
// shadow-claimed member must resolve to ITSELF, not to the claiming
// group.
func buildGroupPolicy(group *models.ModelConfig, rows []models.ModelConfig, lane models.GroupLane) groupPolicy {
	policy := groupPolicy{GroupID: group.ID, Lane: lane}
	if group == nil {
		return policy
	}
	byID := make(map[string]models.ModelConfig, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	for _, member := range group.Members {
		if member.ModelID == "" {
			continue
		}
		row, ok := byID[member.ModelID]
		if !ok {
			continue // vanished member — unservable
		}
		if !row.Enabled {
			continue // inactive member — never a candidate
		}
		if memberLane, ok := models.MemberLane(row.Kind); !ok || memberLane != lane {
			// The member does not belong to the group's lane — it is
			// not a candidate on this lane (the defence-in-depth half
			// of the kind-parameterized rule).
			continue
		}
		weight := member.Weight
		if weight <= 0 {
			weight = 1
		}
		pool := models.PoolDefault
		if member.BackupOnly {
			pool = models.PoolBackup
		}
		policy.Members = append(policy.Members, policyMember{ID: member.ModelID, Weight: weight, Pool: pool})
	}
	return policy
}

// order returns the ATTEMPT ORDER: the policy head first, then the
// remaining default-pool members in configured order, then the backup
// pool in configured order.
//
// The head is a FRESH weighted pick from the default pool — no
// binding, no marks, no cooldowns (the empty conversation key
// semantics: a decision request carries no conversation, so every
// request draws afresh).
//
// The weighted selector is a mechanical clone of the engine's own
// selector (cumulative prefix sums + a binary search) — the repo
// already carries two clones of this algorithm.
func (p groupPolicy) order() []policyMember {
	if len(p.Members) == 0 {
		return nil
	}
	defaults := make([]policyMember, 0, len(p.Members))
	backups := make([]policyMember, 0, len(p.Members))
	for _, m := range p.Members {
		if m.Pool == models.PoolBackup {
			backups = append(backups, m)
			continue
		}
		defaults = append(defaults, m)
	}
	ordered := make([]policyMember, 0, len(p.Members))
	if len(defaults) > 0 {
		head := defaults[pickWeighted(defaults)]
		ordered = append(ordered, head)
		for _, m := range defaults {
			if m.ID != head.ID {
				ordered = append(ordered, m)
			}
		}
	}
	// The backup pool is a RECOURSE: it follows every default member.
	ordered = append(ordered, backups...)
	return ordered
}

// pickWeighted maps a uniform draw onto a weighted member of the
// pool (cumulative prefix sums; the configured order is the interval
// order, so equal weights sit on adjacent equal-width intervals).
func pickWeighted(members []policyMember) int {
	if len(members) == 0 {
		return -1
	}
	total := 0.0
	for _, m := range members {
		if m.Weight > 0 {
			total += m.Weight
		}
	}
	if total <= 0 {
		return 0
	}
	r := rand.Float64() * total
	acc := 0.0
	for i, m := range members {
		if m.Weight <= 0 {
			continue
		}
		acc += m.Weight
		if r < acc {
			return i
		}
	}
	return len(members) - 1
}
