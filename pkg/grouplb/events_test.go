package grouplb

import "testing"

// TestEventConstants — the caller-side publish contract (Q15 payload
// event + observability mirrors). The ENGINE never publishes these
// itself; these pins keep the wire names stable for the bus consumers
// (SSE UI).
func TestEventConstants(t *testing.T) {
	if EventMemberSelected != "model_member_selected" {
		t.Errorf("EventMemberSelected = %q", EventMemberSelected)
	}
	if EventBindingDropped != "model_group_binding_dropped" {
		t.Errorf("EventBindingDropped = %q", EventBindingDropped)
	}
	if EventMembersChanged != "model.group.members.changed" {
		t.Errorf("EventMembersChanged = %q", EventMembersChanged)
	}
	if EventMemberFailover != "model_member_failover" {
		t.Errorf("EventMemberFailover = %q", EventMemberFailover)
	}
}
