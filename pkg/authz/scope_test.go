package authz

// Unit tests for the ONE token-scope resolver and the ONE predicate
// (token-authz plan §2.1–§2.3, ADR D3/D7/D10, R-3).

import (
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestAuthzResolve_TruthTable walks every token state the contract
// defines: unrestricted, restricted (by model ids / by group ids),
// PRESENT-empty, and the two INVALID flags.
func TestAuthzResolve_TruthTable(t *testing.T) {
	members := func(groupID string) []string {
		if groupID == "g1" {
			return []string{"m-g1-a", "m-g1-b"}
		}
		return nil
	}

	t.Run("nil token is unrestricted", func(t *testing.T) {
		s := Resolve(nil, members)
		if s != nil {
			t.Fatalf("Resolve(nil) = %+v, want nil scope (unrestricted)", s)
		}
		if !s.Allows("anything") {
			t.Error("nil scope must allow any id")
		}
		if !s.IsUnrestricted() {
			t.Error("nil scope must report IsUnrestricted")
		}
	})

	t.Run("both axes absent is the conjunctive wildcard", func(t *testing.T) {
		s := Resolve(&auth.AuthToken{}, members)
		if s != nil {
			t.Fatalf("Resolve(both absent) = %+v, want nil (wildcard)", s)
		}
		if !s.Allows("m1") {
			t.Error("wildcard must allow any id")
		}
	})

	t.Run("one axis present-empty is RESTRICTED and grants nothing", func(t *testing.T) {
		s := Resolve(&auth.AuthToken{AllowedModels: []string{}}, members)
		if s == nil {
			t.Fatal("Resolve(models PRESENT-empty) must NOT be a wildcard")
		}
		if s.IsUnrestricted() {
			t.Error("PRESENT-empty must not report unrestricted")
		}
		if s.Allows("m1") {
			t.Error("PRESENT-empty models axis must grant nothing")
		}
	})

	t.Run("model ids are granted exactly", func(t *testing.T) {
		s := Resolve(&auth.AuthToken{AllowedModels: []string{"m1", "m2"}}, members)
		if !s.Allows("m1") || !s.Allows("m2") {
			t.Error("listed model ids must be allowed")
		}
		if s.Allows("m3") {
			t.Error("unlisted model id must be denied")
		}
	})

	t.Run("group grants the target and its members", func(t *testing.T) {
		s := Resolve(&auth.AuthToken{AllowedGroups: []string{"g1"}}, members)
		if !s.Allows("g1") {
			t.Error("the group TARGET itself must be reachable (plan §2.1 — load-bearing for 034 behaviour preservation)")
		}
		if !s.Allows("m-g1-a") || !s.Allows("m-g1-b") {
			t.Error("every current member of a granted group must be reachable")
		}
		if s.Allows("m-outside") {
			t.Error("a model outside every grant must be denied")
		}
	})

	t.Run("models invalid is deny-all", func(t *testing.T) {
		s := Resolve(&auth.AuthToken{ModelsInvalid: true, AllowedModels: []string{"m1"}}, members)
		if s == nil || !s.IsDenyAll() {
			t.Fatal("ModelsInvalid must resolve to DENY-ALL, never wildcard")
		}
		if s.Allows("m1") {
			t.Error("DENY-ALL must not allow an explicitly listed id")
		}
	})

	t.Run("groups invalid is deny-all", func(t *testing.T) {
		s := Resolve(&auth.AuthToken{GroupsInvalid: true, AllowedGroups: []string{"g1"}}, members)
		if s == nil || !s.IsDenyAll() {
			t.Fatal("GroupsInvalid must resolve to DENY-ALL, never wildcard")
		}
		if s.Allows("m-g1-a") {
			t.Error("DENY-ALL must not reach a granted group's member")
		}
	})
}

// TestAuthzResolve_ConjunctiveWildcard is the R-3 anti-escalation proof:
// models ABSENT + groups PRESENT must be RESTRICTED (the exact shape
// migration 034 produces for a narrow group token). A per-axis wildcard rule
// would hand that token every model in the system.
func TestAuthzResolve_ConjunctiveWildcard(t *testing.T) {
	members := func(groupID string) []string {
		if groupID == "coding-group" {
			return []string{"gpt-x", "claude-y"}
		}
		return nil
	}

	tok := &auth.AuthToken{
		AllowedModels: nil, // ABSENT — the post-034 narrow shape
		AllowedGroups: []string{"coding-group"},
	}
	s := Resolve(tok, members)

	if s == nil {
		t.Fatal("models ABSENT + groups PRESENT resolved to nil (wildcard) — this is the R-3 privilege escalation")
	}
	if s.IsUnrestricted() {
		t.Fatal("models ABSENT + groups PRESENT must NOT report unrestricted")
	}

	// The model axis grants NOTHING.
	if s.Allows("any-unlisted-model") {
		t.Error("the model axis must grant nothing when it is ABSENT and the group axis is PRESENT")
	}

	// The group target and its current members are reachable.
	if !s.Allows("coding-group") {
		t.Error("the granted group target must be reachable")
	}
	if !s.Allows("gpt-x") || !s.Allows("claude-y") {
		t.Error("the granted group's members must be reachable")
	}
}

// TestAuthzResolve_NoMembersExpansionForAbsentGroups pins the hot-path
// guarantee: membership is only consulted for groups the token actually
// grants.
func TestAuthzResolve_NoMembersExpansionForAbsentGroups(t *testing.T) {
	calls := 0
	members := func(groupID string) []string {
		calls++
		return []string{"m-" + groupID}
	}

	cases := []struct {
		name string
		tok  *auth.AuthToken
	}{
		{"both absent", &auth.AuthToken{}},
		{"groups absent, models present", &auth.AuthToken{AllowedModels: []string{"m1"}}},
		{"groups present-empty", &auth.AuthToken{AllowedGroups: []string{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls = 0
			Resolve(tc.tok, members)
			if calls != 0 {
				t.Errorf("membersOf called %d time(s) with no granted group id — membership must never be expanded for an absent/empty group axis", calls)
			}
		})
	}

	// Positive control: a granted group DOES expand (proves the counter works
	// and the guard above is not vacuous).
	calls = 0
	Resolve(&auth.AuthToken{AllowedGroups: []string{"g1", "g2"}}, members)
	if calls != 2 {
		t.Errorf("membersOf called %d time(s), want exactly 2 (one per granted group)", calls)
	}
}

// TestAuthzScope_AllowsEmptyID pins that a restricted scope denies the empty
// id — a lookup for "" must never be satisfied by a malformed grant entry.
func TestAuthzScope_AllowsEmptyID(t *testing.T) {
	s := Resolve(&auth.AuthToken{AllowedModels: []string{"m1"}}, nil)
	if s == nil {
		t.Fatal("restricted token resolved to nil scope")
	}
	if s.Allows("") {
		t.Error(`Allows("") on a restricted scope must be false`)
	}

	// Even if a corrupt grant list literally carries an empty string, the
	// empty id stays unreachable (Resolve drops it while building the set).
	s = Resolve(&auth.AuthToken{AllowedModels: []string{"", "m1"}}, nil)
	if s.Allows("") {
		t.Error(`Allows("") must stay false even when an empty grant entry is present`)
	}
	if !s.Allows("m1") {
		t.Error("a valid sibling grant must still work")
	}
}

// TestAuthzResolve_EmptyTextGrantIsDenyAll_NotWildcard is the PAIRED pin
// for the scanner rule (Phase-1.1 item A): a whitespace-only
// `allowed_models` column is UNREADABLE, and an unreadable axis collapses
// the WHOLE token to DENY-ALL.
//
// The pair matters because the two states are indistinguishable at the
// struct level once scanned: `AllowedModels == nil` means ABSENT (a
// wildcard when the group axis is absent too) while `ModelsInvalid == true`
// means DENY-ALL. If the scanner ever maps an empty column back to ABSENT,
// this token silently regains FULL ACCESS on public /v1/* — the exact
// escalation class the commission closes. The pin therefore asserts both
// halves: the scanner says INVALID, and the resolver says DENY-ALL (never
// WILDCARD).
func TestAuthzResolve_EmptyTextGrantIsDenyAll_NotWildcard(t *testing.T) {
	for _, raw := range []interface{}{"", "   ", "\t\n", []byte("  ")} {
		ids, present, invalid := auth.ScanGrant(raw)
		if present {
			t.Errorf("ScanGrant(%q) present = true, want false (an empty column is not a grant list)", raw)
		}
		if !invalid {
			t.Errorf("ScanGrant(%q) invalid = false, want true (an empty column is UNREADABLE, not ABSENT)", raw)
		}
		if len(ids) != 0 {
			t.Errorf("ScanGrant(%q) returned ids %v alongside invalid=true", raw, ids)
		}

		// The pair: a whitespace model axis + an ABSENT group axis.
		tok := &auth.AuthToken{
			AllowedModels: ids,
			AllowedGroups: nil, // ABSENT — the other axis is clean
			ModelsInvalid: invalid,
		}
		s := Resolve(tok, nil)
		if s == nil {
			t.Fatalf("Resolve(whitespace models axis, absent groups axis) = nil — the token collapsed to WILDCARD")
		}
		if s.IsUnrestricted() {
			t.Error("an unreadable grant axis must never resolve to UNRESTRICTED")
		}
		if !s.IsDenyAll() {
			t.Error("an unreadable grant axis must resolve to DENY-ALL")
		}
		if s.Allows("any-model") {
			t.Error("a DENY-ALL scope must not allow any id")
		}
	}
}

// TestReachFilter_InterfaceShape pins models.ReachFilter's implementation by
// *authz.Scope at COMPILE time. It lives here (not in pkg/models) because
// pkg/models must not import pkg/authz — the seam exists precisely to keep
// that direction of the graph absent.
func TestReachFilter_InterfaceShape(t *testing.T) {
	var _ models.ReachFilter = (*Scope)(nil)

	// Runtime leg: the interface value must behave like the predicate,
	// including the nil-receiver (no-filter) case.
	var filter models.ReachFilter = Resolve(&auth.AuthToken{AllowedModels: []string{"m1"}}, nil)
	if !filter.Allows("m1") || filter.Allows("m2") {
		t.Error("a restricted scope used through models.ReachFilter must filter by the reach set")
	}

	var nilFilter models.ReachFilter = Resolve(&auth.AuthToken{}, nil)
	if !nilFilter.Allows("anything") {
		t.Error("a wildcard scope used through models.ReachFilter must allow everything")
	}
}
