package grouplb

// Event type constants. The ENGINE never publishes these itself (it
// must not import pkg/events); ModelsManager and the proxy call sites
// publish them via the bus from the engine's caller-side hooks
// (clone of credentiallb/events.go, member axis).
const (
	// EventBindingDropped is published by the engine's caller-side
	// hooks when a group binding is dropped (observability in the
	// SSE UI).
	EventBindingDropped = "model_group_binding_dropped"

	// EventMembersChanged is published by ModelsManager after a
	// successful AddModel/UpdateModel/RemoveModel that touched a
	// group's members (the survivor-filter input).
	EventMembersChanged = "model.group.members.changed"

	// EventMemberSelected is the Q15 member-resolution observability
	// event, published by the CALLER at the W-1 publish site
	// (race_executor.go) — newlyBound-ONLY, mirroring the
	// model_credential_selected first-binding asymmetry. Payload:
	// {group_id, member_id, upstream_provider, upstream_model}.
	EventMemberSelected = "model_member_selected"

	// EventMemberFailover is the group-level failover observability
	// event, published by the CALLER after a group-level
	// ExcludeAndReselect rebind succeeds (Am24 re-entry). Payload:
	// {id, group_id, member_id, replacement}.
	//
	// Mirrored at the OpenAI emission site (pkg/proxy/handler.go
	// tryGroupReentry, ~:1221) and the Anthropic emission site
	// (pkg/proxy/handler_groups.go tryGroupReentryAnthropic, ~:387);
	// the keys above are the actual map keys both call sites
	// publish — earlier revisions of this doc referenced a stale
	// {group_id, from_member_id, to_member_id} shape that never
	// matched the wired code. Kept verbatim so FE EventLog + downstream
	// failover dashboards can match the real payload.
	EventMemberFailover = "model_member_failover"
)
