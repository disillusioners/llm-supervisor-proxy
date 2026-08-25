package credentiallb

// Event type constants. The ENGINE never publishes these itself (it
// must not import pkg/events); ModelsManager and the Phase 3 proxy
// call sites publish them via the bus from the engine's caller-side
// hooks.
const (
	// EventBindingDropped is published by the engine's caller-side
	// hooks when a binding is dropped (observability in the SSE UI).
	EventBindingDropped = "model_credential_binding_dropped"

	// EventCredentialsChanged is published by ModelsManager after a
	// successful AddModel/UpdateModel that touched credentials_json.
	// The ModelsManager subscription loop (which forwards to
	// Engine.OnModelChanged) listens for it on the engine's behalf.
	EventCredentialsChanged = "model.credentials.changed"

	// EventCredentialFailover is the Round-3 (R3-8) failover
	// observability event, published by the CALLER after a
	// non-no-op ExcludeAndReselect rebind succeeds. Payload:
	// {model_id, from_credential_id, to_credential_id, reason,
	// retry_after_ms, cooldown_ms, attempt_index}.
	EventCredentialFailover = "model_credential_failover"
)
