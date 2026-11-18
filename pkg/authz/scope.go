// Package authz is the ONE token-scope resolver and the ONE permission
// predicate of the AUTH commission.
//
// Reach(T) =  { id : id ∈ T.AllowedModels }
//
//	∪ { g  : g  ∈ T.AllowedGroups }                  // the group TARGET is reachable …
//	∪ ⋃_{g ∈ T.AllowedGroups} { m : m ∈ Members(g) } // … and its CURRENT members
//
// Two rules are load-bearing and are stated here so no lane can re-derive
// them differently (plan §2.3, ADR D3/D7/D10/R-3):
//
//  1. THE WILDCARD TEST IS CONJUNCTIVE. A token is UNRESTRICTED iff BOTH
//     axes are ABSENT and cleanly parsed. allowed_models ABSENT +
//     allowed_groups PRESENT is RESTRICTED — never a model-level wildcard.
//     Migration 034 produces exactly that shape for narrow group tokens; a
//     per-axis wildcard rule would grant more access than the defect did.
//  2. FAIL CLOSED. Either axis INVALID (unreadable row) => DENY-ALL. A
//     corrupt row is never a wildcard and never an empty grant list.
//
// The member expansion is injected as `membersOf` so this package stays a
// leaf: it imports pkg/auth (plus stdlib) and nothing else — no pkg/proxy,
// no pkg/decision, no pkg/store/database, no pkg/modelscache. That is
// enforced by TestPackage_ImportGuard_AuthzStaysLeaf.
package authz

import "github.com/disillusioners/llm-supervisor-proxy/pkg/auth"

// Scope is the per-request reach set of one token.
//
//	nil *Scope   => UNRESTRICTED (anonymous or wildcard token): Allows() == true for everything.
//	denyAll      => DENY-ALL: Allows() == false for everything.
//	otherwise    => restricted: Allows() is a set lookup.
//
// A nil *Scope is meaningful and safe: every method is nil-receiver-safe.
type Scope struct {
	denyAll      bool
	unrestricted bool
	ids          map[string]struct{}
}

// Resolve computes Reach(T) once per request (plan §2.1/§2.6).
//
//	t == nil                       -> nil (unrestricted; the lane's own auth gate decides anonymously)
//	t.ModelsInvalid||GroupsInvalid -> &Scope{denyAll:true}
//	both axes ABSENT               -> nil (CONJUNCTIVE wildcard, plan §2.3)
//	otherwise                      -> restricted set = allowed_models ∪ allowed_groups ∪ ⋃ Members(g)
//
// membersOf returns a group's CURRENT member row ids; it MUST be
// cache-backed (pkg/proxy: directGetModel/GetModelNative; pkg/decision:
// h.currentModelRows()). It is called ONLY for group ids present in
// t.AllowedGroups — a token with an ABSENT/empty group axis never touches
// membership at all (the hot-path guarantee).
func Resolve(t *auth.AuthToken, membersOf func(groupID string) []string) *Scope {
	if t == nil {
		return nil
	}
	if t.ModelsInvalid || t.GroupsInvalid {
		return &Scope{denyAll: true}
	}

	modelsAbsent := t.AllowedModels == nil
	groupsAbsent := t.AllowedGroups == nil
	if modelsAbsent && groupsAbsent {
		// Conjunctive wildcard: no list on EITHER axis.
		return nil
	}

	ids := make(map[string]struct{}, len(t.AllowedModels)+len(t.AllowedGroups))
	for _, id := range t.AllowedModels {
		if id == "" {
			continue
		}
		ids[id] = struct{}{}
	}
	for _, g := range t.AllowedGroups {
		if g == "" {
			continue
		}
		// The group TARGET itself is reachable (plan §2.1) — load-bearing for
		// migration 034's behaviour preservation: the client may still name
		// the group.
		ids[g] = struct{}{}
		if membersOf == nil {
			continue
		}
		for _, m := range membersOf(g) {
			if m == "" {
				continue
			}
			ids[m] = struct{}{}
		}
	}
	return &Scope{ids: ids}
}

// Allows is THE predicate. Every lane and every candidate-construction
// site calls this — never a local re-implementation (D7/D8).
//
// A nil receiver is the unrestricted/anonymous case and allows everything.
// A deny-all scope allows nothing. A restricted scope allows only set
// members; the empty id is never a granted id.
func (s *Scope) Allows(id string) bool {
	if s == nil || s.unrestricted {
		return true
	}
	if s.denyAll {
		return false
	}
	if id == "" {
		return false
	}
	_, ok := s.ids[id]
	return ok
}

// IsUnrestricted reports the wildcard state (telemetry/debug only — never
// used as a per-site shortcut).
func (s *Scope) IsUnrestricted() bool {
	return s == nil || (s.unrestricted && !s.denyAll)
}

// IsDenyAll reports the fail-closed state (telemetry/debug only).
func (s *Scope) IsDenyAll() bool {
	return s != nil && s.denyAll
}
