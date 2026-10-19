package models

// load-balance-groups §3.6.1 (Am21) — the modelList rewrite seam.
//
// The rewrite is ONE shared manager-level helper (implemented on
// *store/database.ModelsManager, the DB backend that owns the
// grouplb engine) called by BOTH protocol sites after their
// conversation-key derivation (handler.go post-auth wiring /
// handler_anthropic.go mirror) — the parity test locks the two call
// sites in sync.
//
// It is a NARROW side interface (NOT a ModelsConfigInterface method)
// deliberately: asserted structurally at the two protocol call sites,
// so the ~11 test fakes and the JSON-file backend are untouched. The
// modelscache decorator forwards it to the inner manager. Backends
// that do not implement it (the JSON twin carries no grouplb engine)
// simply never rewrite — group rows resolve and validate everywhere,
// but member substitution at request time is a DB-manager capability
// (production main.go always binds the DB manager).

// GroupRewriteResult is the outcome of RewriteGroupEntries.
type GroupRewriteResult struct {
	// ModelList is the rewritten execution list: every group entry
	// replaced by [member.ID] + member.FallbackChain; non-group
	// entries pass through unchanged (Q16: chat→group fallback
	// entries are rewritten natively).
	ModelList []string

	// AnyGroup is true when at least one entry was a group row.
	AnyGroup bool

	// GroupID/MemberID identify the FIRST (main) group entry's pick
	// — the direct-named group (or the first fallback-reached one).
	// Empty when no group entry existed. Metering stamps (Am25),
	// failure semantics (Am24) and the Q15 event key on these.
	GroupID  string
	MemberID string

	// MemberNewlyBound is the grouplb W-1 signal for the GroupID
	// pick (true ⇔ this call created the group binding). Drives the
	// model_member_selected event (newlyBound-only, Q15).
	MemberNewlyBound bool

	// MemberPool is the pool the served member was drawn from
	// (group-backup-members / D5 observability): "default" when the
	// member came from the configured default sub-pool, "backup"
	// when every default member in the run's exclusion set had
	// failed. Empty when no group entry was rewritten. Surfaced
	// through model_member_selected and the proxy log line — the
	// reason taxonomy is unchanged (D5).
	MemberPool string

	// BindingStale is true when the selection-time repair
	// (group-backup-members / D8) discarded a sticky binding whose
	// member had LEFT the group, and the pick was re-made. False
	// for the ordinary case (binding honoured, or fresh pick with
	// no prior binding). Surfaced as the additive "binding" event
	// field on model_member_selected.
	BindingStale bool

	// FailedGroupID is non-empty when a group entry had NO servable
	// member at runtime (Am28 re-validation exhausted the member
	// set — all stale/disabled/deleted). The caller surfaces the
	// normal resolve-failure envelope; never a panic.
	FailedGroupID string
}

// Pool constants (group-backup-members / D5): the only two strings
// MemberPool may carry on the rewrite path. Public so consumers
// (proxy telemetry, FE surfaces) can reference the same vocabulary
// instead of duplicating literals.
const (
	PoolDefault = "default"
	PoolBackup  = "backup"
)

// GroupRewriter is the rewrite + group-level-reselect seam (Am21 +
// Am24). Implemented by *database.ModelsManager; forwarded by the
// modelscache decorator.
type GroupRewriter interface {
	// RewriteGroupEntries rewrites every group entry in modelList to
	// its sticky-picked member (Am22 GetModelNative direct-ID +
	// Am28 stale-member re-validation). conversationKey is the
	// request's key — for a direct-named group it is the
	// GROUP-salted key (ComputeConversationKey(groupID, tokenID,
	// leadingUserRun) — leadingUserRun being the canonical JSON
	// array of the leading contiguous user-message run); empty key
	// ⇒ fresh weighted pick, no binding.
	RewriteGroupEntries(modelList []string, conversationKey string) GroupRewriteResult

	// ExcludeGroupMember marks the member as cooling for the group
	// (60s default) and rebinds the conversation to the next healthy
	// member (ExcludeAndReselect). Returns the replacement member,
	// the pool the member was drawn from ("default" / "backup"),
	// and ok=true when a member is available for a re-entry attempt
	// (Healthy or SoonestExpiry modes); ("", "", false) when no
	// alternative exists (single-member group / all exhausted) —
	// the caller treats the failure as real (max-ONE re-entry,
	// Am24). group-backup-members / D5: the pool string is
	// observable ONLY on actual fallback; ReselectNone / ReselectSoonestExpiry
	// in the default pool cascades into the backup pool before
	// returning ok=false (the fall-through is invisible to the
	// caller unless the pool=backup path actually serves).
	ExcludeGroupMember(groupID, conversationKey, memberID string) (replacement string, pool string, ok bool)
}
