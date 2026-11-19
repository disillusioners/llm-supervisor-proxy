package modelscache

// The AUTH COMMISSION's degraded-shape pin for the decorator's scoped
// forwards.
//
// THE SHAPE UNDER TEST: an inner backend that implements the UNSCOPED
// models.GroupRewriter but NOT models.GroupRewriterScoped. The
// decorator's fallback branch then runs — and that branch must still
// enforce the token's reach set, because the legacy offer walk cannot
// scope itself.
//
// Production always runs through the decorator over the DB manager,
// which DOES implement the scoped seam, so this shape is a DEGRADED
// mode (a third-party backend, a test fake, an older inner). It is
// unreachable today — and that is exactly why it needs a pin: the
// fallback is the code that would run if the production assumption ever
// broke, and it must fail closed on its own.

import (
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// unscopedGroupRewriterInner is a strictSource that ALSO implements the
// UNSCOPED models.GroupRewriter — and deliberately NOT the scoped seam.
//
// NOTE THE EMBEDDING: the field type is the strictSource INTERFACE, not
// the concrete manager. Embedding the interface gives this struct the
// interface's method set only — the manager's scoped methods are NOT
// promoted, so the scoped type assertion inside the decorator genuinely
// fails and the fallback branch is exercised.
type unscopedGroupRewriterInner struct {
	strictSource

	// replacement is what the legacy ExcludeGroupMember hands back.
	replacement string
	pool        string
	ok          bool
}

func (u *unscopedGroupRewriterInner) RewriteGroupEntries(modelList []string, conversationKey string) models.GroupRewriteResult {
	return models.GroupRewriteResult{ModelList: modelList}
}

func (u *unscopedGroupRewriterInner) ExcludeGroupMember(groupID, conversationKey, memberID string, cooldown time.Duration) (string, string, bool) {
	return u.replacement, u.pool, u.ok
}

// reachSet is a models.ReachFilter over a plain id set.
type reachSet map[string]bool

func (r reachSet) Allows(id string) bool { return r[id] }

// TestExcludeGroupMemberScoped_LegacyFallback_RefusesOutOfReachReplacement
// pins the defense in depth of the decorator's degraded branch: an
// out-of-reach replacement offered by the LEGACY exclude is REFUSED
// (the zero value — OK=false), while an in-reach replacement is still
// returned unchanged.
//
// Without the reach check the degraded branch would hand the re-entry
// walk a member outside the token's reach — the walk would offer it and
// the handler would stamp it as the served member.
func TestExcludeGroupMemberScoped_LegacyFallback_RefusesOutOfReachReplacement(t *testing.T) {
	st := newOutageStack(t)

	inner := &unscopedGroupRewriterInner{
		strictSource: st.src,
		replacement:  "member-out",
		pool:         models.PoolDefault,
		ok:           true,
	}
	// PRE-CONDITION: the fake must NOT implement the scoped seam —
	// otherwise the degraded branch is never exercised and the pin is
	// vacuous.
	if _, scoped := interface{}(inner).(models.GroupRewriterScoped); scoped {
		t.Fatal("the fake inner implements models.GroupRewriterScoped — the degraded shape under test is not exercised")
	}
	if _, unscoped := interface{}(inner).(models.GroupRewriter); !unscoped {
		t.Fatal("the fake inner must implement models.GroupRewriter (the unscoped seam) — otherwise the fallback never reaches the legacy exclude")
	}

	c, err := NewCachedModelsConfig(inner, Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	t.Cleanup(c.Stop)

	reach := reachSet{"member-in": true}

	// (1) The OUT-OF-REACH replacement is refused.
	out := c.ExcludeGroupMemberScoped("g", "conv", "member-failed", time.Second, reach)
	if out.OK {
		t.Fatalf("OK = true with Replacement=%q — the degraded branch handed back a replacement OUTSIDE the reach set %v", out.Replacement, reach)
	}
	if out.Replacement != "" {
		t.Errorf("Replacement = %q, want \"\" — a refused replacement must carry no id (the caller must not stamp it)", out.Replacement)
	}

	// (2) An IN-REACH replacement is still returned (the fallback must
	// not break the normal path).
	inner.replacement = "member-in"
	out = c.ExcludeGroupMemberScoped("g", "conv", "member-failed", time.Second, reach)
	if !out.OK {
		t.Fatalf("OK = false for the IN-REACH replacement %q — the degraded branch must still serve a usable offer", inner.replacement)
	}
	if out.Replacement != "member-in" {
		t.Errorf("Replacement = %q, want %q", out.Replacement, "member-in")
	}
	if out.Pool != models.PoolDefault {
		t.Errorf("Pool = %q, want %q", out.Pool, models.PoolDefault)
	}

	// (3) A nil reach (wildcard / no permission backend) keeps the
	// legacy behaviour byte-for-byte.
	out = c.ExcludeGroupMemberScoped("g", "conv", "member-failed", time.Second, nil)
	if !out.OK || out.Replacement != "member-in" {
		t.Errorf("nil reach: outcome = %+v, want the legacy offer (OK=true, Replacement=member-in)", out)
	}
}

// TestRewriteGroupEntriesScoped_LegacyFallback_DegradesUnscoped pins the
// other degraded branch: without the scoped seam the decorator falls
// back to the UNSCOPED rewrite (the request layer's reach filter is
// then the fail-closed backstop). The list must still come back
// UNCHANGED — the fallback must never rewrite or drop entries on its
// own.
func TestRewriteGroupEntriesScoped_LegacyFallback_DegradesUnscoped(t *testing.T) {
	st := newOutageStack(t)

	inner := &unscopedGroupRewriterInner{strictSource: st.src}
	c, err := NewCachedModelsConfig(inner, Options{ReconcileInterval: time.Hour})
	if err != nil {
		t.Fatalf("NewCachedModelsConfig: %v", err)
	}
	t.Cleanup(c.Stop)

	list := []string{"m1", "m2"}
	res := c.RewriteGroupEntriesScoped(list, "conv", reachSet{"m1": true})
	if len(res.ModelList) != len(list) {
		t.Fatalf("ModelList = %v, want the input list %v unchanged — the degraded fallback must not rewrite the list", res.ModelList, list)
	}
	for i := range list {
		if res.ModelList[i] != list[i] {
			t.Errorf("ModelList[%d] = %q, want %q", i, res.ModelList[i], list[i])
		}
	}
	if res.AnyGroup {
		t.Error("AnyGroup = true, want false — the degraded fallback reports no group pick")
	}
}
