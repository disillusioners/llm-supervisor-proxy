package grouplb

import (
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
)

func TestBinding_Expired_SlidingIdle(t *testing.T) {
	now := time.Now()
	b := &binding{memberID: "m", boundAt: now}
	if b.expired(time.Hour, now) {
		t.Error("fresh binding must not be expired")
	}
	if !b.expired(time.Hour, now.Add(time.Hour+time.Millisecond)) {
		t.Error("binding idle beyond ttl must be expired")
	}
	// Idle ceiling, not lifetime: an OLD binding whose boundAt was
	// recently REFRESHED is not expired (the idle window restarts
	// on refresh).
	b.boundAt = now.Add(-5 * time.Millisecond)
	if b.expired(time.Hour, now) {
		t.Error("refreshed boundAt must reset the idle window")
	}
}

func TestBinding_MemberKeyUniqueness(t *testing.T) {
	// Bindings key on the plain member model ID — two distinct
	// members never collide (the Round-1 composite-key concern is
	// evaporated; this pin guards the regression).
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", refs2())
	e.InjectPreconditionStateForTest("g1", "conv-1", "member-a")
	e.InjectPreconditionStateForTest("g1", "conv-1", "member-b") // same key, rebind
	if got := e.bindingsCountForTest("g1"); got != 1 {
		t.Fatalf("rebind on same conv key must replace, not duplicate; count=%d", got)
	}
	if m, _, _ := e.GetOrSelect("g1", "conv-1"); m != "member-b" {
		t.Fatalf("binding = %q, want member-b (last write wins)", m)
	}
}

func TestBinding_GroupSaltedKeySpaces(t *testing.T) {
	// ComputeConversationKey imported from credentiallb (Am31 —
	// key.go is NOT forked): the same conversation under two
	// different group IDs yields DIFFERENT keys (group-salted).
	ka := credentiallb.ComputeConversationKey("group-a", "tok", "hello")
	kb := credentiallb.ComputeConversationKey("group-b", "tok", "hello")
	if ka == kb {
		t.Fatal("group-salted keys must differ per group")
	}
	if len(ka) != 64 {
		t.Fatalf("key len = %d, want 64 hex chars", len(ka))
	}
	// The engine keys its binding spaces per group — the same conv
	// key under two groups holds independent bindings.
	e := newTestEngine(t, time.Hour, time.Hour)
	e.OnGroupChanged("g1", refs2())
	e.OnGroupChanged("g2", refs2())
	e.InjectPreconditionStateForTest("g1", "shared-conv", "member-a")
	e.InjectPreconditionStateForTest("g2", "shared-conv", "member-b")
	if m, _, _ := e.GetOrSelect("g1", "shared-conv"); m != "member-a" {
		t.Fatalf("g1 binding = %q, want member-a (distinct space)", m)
	}
	if m, _, _ := e.GetOrSelect("g2", "shared-conv"); m != "member-b" {
		t.Fatalf("g2 binding = %q, want member-b (distinct space)", m)
	}
}
