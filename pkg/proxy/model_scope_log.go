package proxy

// ─────────────────────────────────────────────────────────────────────────────
// model_scope_log.go — the operator-facing MINT-SCOPE report
// (wi-4b8d21e7, review S-5).
//
// WHY A HELPER: both protocol lanes reach the same decision (the scope
// gate refused a model-layer mark) and both must say the SAME thing
// about it — one wording, one field set, one place to keep honest.
//
// WHAT IT STATES — and only what is CERTAIN:
//
//   - the scope the classifier assigned (account / unknown) — never a
//     claim the classifier did not make (an unknown scope is reported
//     as unknown, not as "account");
//   - the WALLED CREDENTIAL the body came from (the account that the
//     body implicates);
//   - the MODEL layer: NO mark (the scope gate refused it);
//   - the UPSTREAM layer: the demotion window that was actually
//     written (0 ⇒ no demotion);
//   - the CREDENTIAL layer: the rotation that actually happened and
//     the cooldown the engine ACTUALLY seeded (0 ⇒ no rotation, hence
//     NO credential-layer cooldown — the F-8 shape, where the rotation
//     budget was spent before the exclusion was written).
//
// The previous wording ("credential-layer mark only") asserted a mark
// that does not exist in the F-8 shape, and it was emitted BEFORE the
// rotation decision — so it could not name the real outcome.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"log"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/modelcache"
)

// modelScopeSkipLog is the payload of one [MODEL-SKIP] line.
type modelScopeSkipLog struct {
	lane   string // "race" | "anthropic" — the protocol lane
	model  string // the model ROW identity (the suppression key)
	credID string // the WALLED credential the body came from
	scope  modelcache.Scope

	// upstreamTTL is the upstream-layer demotion window that was
	// written (0 ⇒ the demotion was NOT written).
	upstreamTTL time.Duration

	// rotation names the credential the rotation moved to ("" ⇒ no
	// rotation happened); cooldown is the credential-layer cooldown the
	// engine actually seeded (0 ⇒ none).
	rotation string
	cooldown time.Duration
}

// logModelScopeSkip emits the [MODEL-SKIP] line — but ONLY when the
// model layer really carries no mark for the model (the scope gate
// refused it). A model-scoped body (the model layer marked) is not a
// skip and is not reported as one.
func logModelScopeSkip(l modelScopeSkipLog) {
	if l.scope == modelcache.ScopeModel {
		return // the model layer IS marked — nothing was skipped
	}
	upstream := "no upstream demotion"
	if l.upstreamTTL > 0 {
		upstream = "upstream demotion " + l.upstreamTTL.String()
	}
	credential := "no credential-layer cooldown (no rotation)"
	if l.cooldown > 0 {
		credential = "credential rotation -> " + l.rotation + ", credential cooldown " + l.cooldown.String()
	}
	log.Printf("[MODEL-SKIP][%s] model=%s cred=%s scope=%s: no model-layer mark; %s; %s",
		l.lane, l.model, l.credID, l.scope, upstream, credential)
}
