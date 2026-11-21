package imggen

// The image-gen lane's GROUP branch — the in-lane member loop.
//
// An image-gen group is a group whose members are image-gen-kind
// models. The lane is INFERRED from the members (never persisted); the
// write path guarantees the members share one lane.
//
// THE LOOP CONTRACT (the decision lane's shape, mirrored WITHOUT
// importing it — the import guard forbids pkg/imggen → pkg/decision):
//
//   - The PICK is POLICY: pools + weights + configured order, never a
//     health mark. The policy may REORDER, never exclude.
//   - A member is failed only when its OWN resilience is consumed: its
//     own credentials are attempted first (the rotation-eligible
//     classes), and only then does the walk advance to the next member.
//   - Exactly ONE client write, at the winner. No attempt before the
//     winner touches the writer.
//   - The walk is BOUNDED: the total walk budget (the hot knob
//     image_gen_group_walk_timeout) is the outer bound, the per-member
//     ImageGenTimeout the inner one.
//   - Exhaustion (every member genuinely attempted and failed) ⇒ 502 +
//     code=imggen_all_members_failed, one terminal event.
//
// The failure-class policy (imggen-groups commission / §4.6.4) —
// branching on the imggen-own classifier outputs (the InspectResponse
// Outcome seam), never on a proxy classifier:
//
//	quota (upstream 429 / Outcome.Code == CodeCooldown) ⇒ rotate
//	    within the member, then advance; the class is the W-8
//	    short-circuit class (3 consecutive ⇒ stop the walk).
//	upstream 401/403 ⇒ rotate (the credential was rejected).
//	client-attributable (400/413/415/422) ⇒ FAIL FAST: advancing past
//	    the member would be dishonest thrash (the request bytes do not
//	    change between members) — the member's status + body are
//	    relayed verbatim.
//	5xx / everything else ⇒ ADVANCE — no rotation.
//
// v1 writes ZERO cross-request health state (no marks, no skip-cache
// writes, no group-owned cooldown — the fallback-engine law applied
// honestly: a fresh policy order per request leaves nothing for a mark
// to order, so writing marks would be cost without effect).

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/authz"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

const (
	// imggenAllMembersFailedCode is the machine code of the group's
	// exhaustion envelope — the distinctive, greppable feature marker
	// (the decision-lane sibling: decisionAllMembersFailedCode).
	imggenAllMembersFailedCode = "imggen_all_members_failed"

	// imggenMisrouteCode is the machine code of the image-gen lane's
	// own misroute envelope (a non-image-gen model or a non-image-gen
	// group posted to /v1/image_generation).
	//
	// It is a DIFFERENT string from the chat family's
	// "image_gen_misroute" (pkg/proxy/handler.go) on purpose: two
	// distinct machine codes, never one shared string (decisions.md
	// D-15).
	imggenMisrouteCode = "image_misroute"

	// quotaFailureClass is the failure class of the quota-class
	// failures (the W-8 short-circuit's class). It rides the events
	// only — never the error body (the body's machine code stays
	// stable: clients never parse a class out of the error body).
	quotaFailureClass = "upstream_quota_exceeded"

	// quotaShortCircuitThreshold is the W-8 bound: N CONSECUTIVE
	// quota-class failures during the walk stop the walk (the
	// 9router img-gen quota wall is ACCOUNT-wide — every member fails
	// identically, so the remaining members' honest attempts would
	// only re-prove it while burning the budget).
	//
	// The counter is over quota-class FAILURE OUTCOMES ONLY. A non-quota
	// FAILURE OUTCOME (or a success) RESETS it; an UNRESOLVED member
	// (credential_unresolved — no attempt, no class) does NOT reset it
	// (D-6: pinned both ways). No account-fingerprinting.
	quotaShortCircuitThreshold = 3
)

// memberFailure is the CLASSIFIED failure of one member attempt.
type memberFailure struct {
	// outcome is the telemetry outcome for the attempt.
	outcome string
	// status is the status to relay when the walk fails fast (0 = the
	// walk continues to the next member).
	status int
	// body is the upstream body to relay on a fail-fast stop.
	body []byte
	// advance reports whether the walk may advance to the next member.
	advance bool
	// rotate reports whether the member may rotate to its next
	// credential (and consume its own resilience first).
	rotate bool
	// class is the failure class ("" = unclassified). It rides the
	// events only.
	class string
}

// memberAttempt is the result of ONE member attempt (the shared
// attempt + the member's identity).
type memberAttempt struct {
	memberID string
	attempt  imgGenAttempt
}

// policyMember is one CANDIDATE of an image-gen group: its id, its
// configured weight and the pool it belongs to.
type policyMember struct {
	ID     string
	Weight float64
	Pool   string
}

// groupPolicy is the GROUP-layer policy fact set of one group: the
// candidates, their weights and their pools, in CONFIGURED order.
//
// The lane is carried along so the walk can assert it never drifts from
// the group's own lane (defence in depth: the write-time homogeneity
// rule plus the request-time inference already agree).
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
// group's lane. An inactive member is never offered as a candidate (it
// is a chain target, not a group member — two populations).
//
// The member rows come from the caller's row set (the live model set
// read ONCE per request — see currentModelRows), never through the
// claims-first client-facing lookup.
func buildGroupPolicy(group *models.ModelConfig, rows []models.ModelConfig, lane models.GroupLane) groupPolicy {
	if group == nil {
		// Contract: a nil group yields an EMPTY policy — defensive
		// only (the caller dereferences the group first).
		return groupPolicy{Lane: lane}
	}
	policy := groupPolicy{GroupID: group.ID, Lane: lane}
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
// The head is a FRESH weighted pick from the default pool — no binding,
// no marks, no cooldowns (the imggen lane carries no conversation key:
// every request draws afresh — the decision lane's F-4 analogue).
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

// pickWeighted maps a uniform draw onto a weighted member of the pool
// (cumulative prefix sums; the configured order is the interval order,
// so equal weights sit on adjacent equal-width intervals).
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

// currentModelRows returns the live model set (the decorator-forwarded
// GetModels). Nil-safe.
//
// ONE READ PER REQUEST: the policy builder and the counting rule share
// this single call — GetModels() deep-copies the whole model set, so a
// second call per request would be per-request garbage for the same
// answer (the decision lane's W-3 / PD-5 rule).
func (h *Handler) currentModelRows() []models.ModelConfig {
	if h == nil || h.modelsConfig == nil {
		return nil
	}
	return h.modelsConfig.GetModels()
}

// countBaseServableMembers counts the members buildGroupPolicy WOULD have
// admitted on the UNFILTERED row — the SAME candidacy predicate the policy
// uses (the row exists AND is enabled AND the member's lane matches the
// group's lane — buildGroupPolicy's candidacy block above). It is the
// discriminator between "empty by scope" (403) and "empty by unservability"
// (502): a group whose members are ALL disabled has baseServable == 0, so its
// empty order is an AVAILABILITY failure even when scope also excluded members
// (a disabled out-of-scope member is still a disabled member — availability,
// not permission). The decision lane's countBaseServableMembers mirror.
//
// The count is computed on the UNFILTERED member list — counting the filtered
// copy would make the two cases indistinguishable.
func countBaseServableMembers(members []models.GroupMember, rows []models.ModelConfig, lane models.GroupLane) int {
	byID := make(map[string]models.ModelConfig, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	n := 0
	for _, member := range members {
		if member.ModelID == "" {
			continue
		}
		row, ok := byID[member.ModelID]
		if !ok || !row.Enabled {
			continue
		}
		if memberLane, ok := models.MemberLane(row.Kind); !ok || memberLane != lane {
			continue
		}
		n++
	}
	return n
}

// serveImageGenGroup serves an image-gen-lane group request: the
// policy order, the per-member attempts, the single commit at the
// winner and the exhaustion envelope.
func (h *Handler) serveImageGenGroup(w http.ResponseWriter, r *http.Request, group *models.ModelConfig, authToken *auth.AuthToken, clientModel string, bodyBytes []byte, startTime time.Time) {
	groupID := group.ID

	// 1) THE LANE GATE (fail closed, BEFORE any scope work).
	//
	// The seam must be present AND the group must be an image-gen-lane
	// group. An absent seam is loud: the group's lane is INFERRED from
	// its members, and an unresolvable lane must never be served.
	lane, ok := h.groupLane(groupID)
	if !ok || lane != models.GroupLaneImageGen {
		log.Printf("[imggen] group=%s refused: the group lane is not image-gen (lane=%q resolved=%v) — failing closed", groupID, lane, ok)
		h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
			"outcome":         imggenMisrouteCode,
			"model":           groupID,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIErrorWithCode(w, http.StatusBadRequest,
			fmt.Sprintf("image-generation models are served by /v1/image_generation only (group %q is not an image-gen group)", groupID),
			imggenMisrouteCode)
		return
	}

	// 2) THE SCOPE — ARM#1 (bypass-closed): the GROUP itself must be in reach.
	//    Resolved INSIDE the group arm, right after the lane gate (the
	//    v203 bypass-closed pattern: the arm owns its OWN scope; the
	//    handler keeps no cross-request scope state). The membersOf
	//    adapter reads the IN-MEMORY model rows (no DB read on the request
	//    path — Ruling 5). This is the SECOND authz.Resolve site in
	//    pkg/imggen (the entry gate keeps its own for PLAIN models).
	//
	//    ADR O1 ("Image-gen has no group or chain surfaces", token-authz
	//    ADR) is SUPERSEDED by this commission: the image-gen lane now
	//    HAS a group surface, so its group arm composes the same three
	//    v203 arms as pkg/decision/group.go:117-208. P4's decisions.md
	//    carries the formal supersession entry.
	scope := authz.Resolve(authToken, h.membersOf())

	// ARM#1 — the check: AFTER the lane gate (a chat/decision group stays
	// a 400 image_misroute regardless of grants — T8) and BEFORE any
	// candidate construction. Authorize the RESOLVED group id (group.ID);
	// QUOTE the CLIENT-requested string (clientModel) — a claimed id
	// resolves INTO this arm, so the canonical id never appears in the
	// body while the client string does (PD-9 mirror).
	if scope != nil && !scope.Allows(group.ID) {
		h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
			"outcome": "model_not_allowed", "model": clientModel,
			"duration_ms": time.Since(startTime).Milliseconds(), "upstream_status": 0,
		})
		h.sendModelNotAllowedError(w, clientModel)
		return
	}

	// ARM#2 — filter a COPY of the member slice BEFORE buildGroupPolicy.
	// The policy's doctrine is "may reorder candidates, never exclude
	// them", so exclusion happens HERE, on a FRESH backing array
	// (make(..., 0, len)) — the cached row's member slice is never
	// mutated (PD-15; group.Members[:0] would share capacity and write
	// into the cached row). buildGroupPolicy stays BYTE-UNCHANGED.
	filtered := *group
	filtered.Members = make([]models.GroupMember, 0, len(group.Members))
	scopeExcluded := 0
	for _, mbr := range group.Members {
		if scope == nil || scope.Allows(mbr.ModelID) {
			filtered.Members = append(filtered.Members, mbr)
			continue
		}
		scopeExcluded++
	}

	// 3) THE POLICY + THE ATTEMPT ORDER — computed BEFORE the first
	//    attempt (the structural law: the order is known before any
	//    upstream call, so the scope-caused emptiness is decidable
	//    without an attempt). ONE live-row read serves BOTH the
	//    base-servable count (ARM#3) and the policy — GetModels
	//    deep-copies the whole set, so it must not be called twice
	//    (W-3 / PD-5).
	rows := h.currentModelRows()
	// rowsByID serves the in-request credential-list seam (D-1): the
	// member's OWN row (never the claims-first lookup) carries its
	// credential refs. Built from the SAME single GetModels read (W-3).
	rowsByID := make(map[string]models.ModelConfig, len(rows))
	for _, row := range rows {
		rowsByID[row.ID] = row
	}
	baseServable := countBaseServableMembers(group.Members, rows, lane)
	policy := buildGroupPolicy(&filtered, rows, lane)
	// D-3: skipped = POLICY-skipped members (unservable at pick): the
	// scope-admitted members the policy refused as candidates (vanished /
	// disabled / wrong-kind). Scope-excluded members are NOT counted —
	// they are a permission matter, handled by the 403 fork above.
	skipped := len(filtered.Members) - len(policy.Members)
	order := policy.order()
	if len(order) == 0 {
		// ARM#3 — "empty by scope" (403) vs "empty by unservability"
		// (502), ATTEMPT-COUNT-KEYED: the order is computed BEFORE the
		// first attempt, so ZERO upstream calls ran here ⇒ a
		// scope-caused emptiness is a PERMISSION failure (403), never
		// the availability envelope.
		//
		// baseServable is counted on the UNFILTERED row: if every
		// member is DISABLED, baseServable == 0 ⇒ the empty order is
		// genuine unservability ⇒ 502, even when scopeExcluded > 0 (a
		// disabled out-of-scope member is still a disabled member).
		if scope != nil && scopeExcluded > 0 && baseServable > 0 {
			h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
				"outcome": "model_not_allowed", "model": clientModel,
				"duration_ms": time.Since(startTime).Milliseconds(), "upstream_status": 0,
			})
			h.sendModelNotAllowedError(w, clientModel)
			return
		}
		h.exhaustImageGenGroup(w, groupID, authToken, 0, skipped, 0, startTime, "", false, false)
		return
	}

	// 4) THE WALK.
	//
	// The budget is the OUTER bound (the total walk budget); the
	// per-member ImageGenTimeout is the INNER one (a member attempt's
	// ctx = min(remaining budget, ImageGenTimeout)).
	walkDeadline := startTime.Add(h.imageGenGroupWalkTimeout())

	triedMembers := make(map[string]bool, len(order))
	triedPairs := make(map[string]bool, len(order))
	attempted := 0
	membersAttempted := 0
	consecutiveQuota := 0
	var winner *memberAttempt
	lastClass := ""
	budgetExhausted := false
	shortCircuited := false
	stopWalk := false

	for idx := range order {
		candidate := order[idx]
		if triedMembers[candidate.ID] {
			continue
		}
		triedMembers[candidate.ID] = true

		// The budget is checked BEFORE each member attempt (the fresh
		// per-attempt check below is authoritative — D-2).
		remaining := time.Until(walkDeadline)
		if remaining <= 0 {
			budgetExhausted = true
			break
		}

		// THE MEMBER'S OWN CREDENTIALS — the IN-REQUEST candidate list
		// (D-1). Built from the member row's configured credential refs
		// (flattened through the models seam), resolved into executable
		// credentials. NO engine call, NO affinity binding, NO cooldown
		// or mark write: rotation advances WITHIN this request, so the
		// production (modelID, modelID) affinity binding cannot pin the
		// member to one credential. F-3 (zero cross-request health
		// writes) stays fully intact.
		memberRow := rowsByID[candidate.ID]
		candidates := h.memberCredentialCandidates(&memberRow)
		if len(candidates) == 0 {
			// No usable credential — the member's own resilience is
			// consumed; the walk advances. D-6: an UNRESOLVED member
			// has NO attempt and NO failure class, so it does NOT
			// reset the consecutive-quota counter (documented at the
			// counter below).
			h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
				"outcome":         "credential_unresolved",
				"model":           groupID,
				"member":          candidate.ID,
				"duration_ms":     time.Since(startTime).Milliseconds(),
				"upstream_status": 0,
			})
			continue // the walk ADVANCES to the next member
		}

		memberAttempted := false
		for {
			// D-2: recompute remaining PER ATTEMPT (inside the
			// credential loop, before this attempt grants its ctx) —
			// not once per member. A rotating member's later attempts
			// must see the SHRINKING outer budget; a stale per-member
			// snapshot would let them overrun it.
			remaining = time.Until(walkDeadline)
			if remaining <= 0 {
				budgetExhausted = true
				stopWalk = true
				break
			}

			// Each attempt uses the NEXT UNTRIED credential of this
			// member (the in-request list's configured order).
			var cred models.ResolvedCredential
			found := false
			for _, c := range candidates {
				if triedPairs[candidate.ID+"|"+c.CredentialID] {
					continue
				}
				cred = c
				found = true
				break
			}
			if !found {
				// Every credential of this member was attempted —
				// its own resilience is consumed.
				break
			}
			triedPairs[candidate.ID+"|"+cred.CredentialID] = true

			// The per-attempt deadline = min(FRESH remaining walk
			// budget, the per-member deadline).
			attemptBudget := remaining
			if perMember := h.imageGenTimeout(); perMember < attemptBudget {
				attemptBudget = perMember
			}
			if attemptBudget <= 0 {
				budgetExhausted = true
				stopWalk = true
				break
			}

			attempted++
			// D-3: membersAttempted counts DISTINCT members attempted
			// (a member rotating across N credentials contributes N to
			// attempted but only ONE here).
			if !memberAttempted {
				membersAttempted++
				memberAttempted = true
			}
			h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
				"outcome":         "member_attempted",
				"model":           groupID,
				"member":          candidate.ID,
				"provider":        cred.Provider,
				"duration_ms":     time.Since(startTime).Milliseconds(),
				"upstream_status": 0,
			})

			ctx, cancel := context.WithTimeout(r.Context(), attemptBudget)
			attempt := h.attemptImageGenMember(ctx, cred, bodyBytes, r)
			cancel()

			if attempt.failure == nil && attempt.outcome.RelayStatus == 0 {
				// THE WINNER — the first successful attempt.
				winner = &memberAttempt{memberID: candidate.ID, attempt: attempt}
				stopWalk = true
				break
			}

			failure := h.classifyAttemptFailure(attempt)
			lastClass = failure.class

			// D-5: exactly ONE authoritative terminal event per
			// request. On the fail-fast path the per-attempt failure
			// event is NOT published here — failFastImageGenGroup
			// publishes the single terminal event (same outcome +
			// member + provider + upstream_status, now with the
			// accounting counters). Emitting both duplicated the
			// meaning of the same failure twice.
			if !failure.advance {
				// Client-attributable failure: the walk stops (the
				// upstream's own failure is the answer).
				h.failFastImageGenGroup(w, groupID, authToken, candidate.ID, attempt, failure, attempted, skipped, membersAttempted, startTime)
				return
			}

			h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
				"outcome":         failure.outcome,
				"model":           groupID,
				"member":          candidate.ID,
				"provider":        cred.Provider,
				"upstream_status": attempt.upstreamStatus,
				"duration_ms":     time.Since(startTime).Milliseconds(),
			})

			// The W-8 short-circuit: N CONSECUTIVE quota-class
			// FAILURE OUTCOMES stop the walk (the quota wall is
			// account-wide — the remaining members would only re-prove
			// it while burning the budget).
			//
			// D-6 semantics (pinned both ways): the counter counts
			// consecutive quota-class FAILURE OUTCOMES only. A non-quota
			// FAILURE OUTCOME (e.g. a 5xx) RESETS it. An UNRESOLVED
			// member (credential_unresolved — no attempt, no class)
			// does NOT reach this code and therefore does NOT reset it.
			if failure.class == quotaFailureClass {
				consecutiveQuota++
				if consecutiveQuota >= quotaShortCircuitThreshold {
					shortCircuited = true
					stopWalk = true
					break
				}
			} else {
				consecutiveQuota = 0
			}

			if failure.rotate {
				// Rotation-eligible: the member's next credential is
				// attempted before the walk advances (the member's own
				// resilience is consumed first).
				continue
			}
			break
		}
		if winner != nil || stopWalk {
			break
		}
	}

	if winner == nil {
		// The walk is over: every member was genuinely attempted and
		// failed (or the walk was cut by the budget / the quota
		// short-circuit). The exhaustion envelope owns the answer.
		h.exhaustImageGenGroup(w, groupID, authToken, attempted, skipped, membersAttempted, startTime, lastClass, budgetExhausted, shortCircuited)
		return
	}

	// THE WINNER — the single commit. The win-clear is a NO-OP in v1
	// (F-3: zero cross-request health writes) but the call site keeps
	// its shape so the v2 health mirror can fill it in.
	h.clearImageGenWinner(winner)
	h.commitImageGenWinner(w, groupID, winner, authToken)
	h.publishEvent(eventTypeImageGeneration, map[string]interface{}{
		"outcome":         "success",
		"model":           groupID,
		"member":          winner.memberID,
		"provider":        winner.attempt.cred.Provider,
		"image_count":     imageCountOfWinner(winner),
		"upstream_status": winner.attempt.upstreamStatus,
		"duration_ms":     time.Since(startTime).Milliseconds(),
	})
}

// classifyAttemptFailure classifies ONE failed member attempt.
//
// The classification branches on the imggen-own classifier outputs (the
// InspectResponse Outcome seam — never pkg/providers.IsQuotaError,
// never a proxy classifier) plus the RAW upstream status (the same
// shape as the decision lane's classifier, which also takes the raw
// status).
//
// The RELAY status stays the seam's decision (outcome.RelayStatus —
// Architect Amendment 3: the handler never branches on
// resp.StatusCode directly); the raw status only CLASSIFIES.
func (h *Handler) classifyAttemptFailure(attempt imgGenAttempt) *memberFailure {
	// A pre-relay failure (build / transport / read / over-cap /
	// conversion) never reached a classified response: ADVANCE (the
	// member's own resilience is consumed — the decision lane's
	// transport-error treatment), no rotation.
	if attempt.failure != nil {
		return &memberFailure{
			outcome: attempt.failure.outcome,
			advance: true,
		}
	}

	status := attempt.upstreamStatus
	outcome := attempt.outcome

	// Quota class: the upstream's own 429 OR the adapter's cooldown
	// code (the 9router adapter preserves the 429 with an UNWRAPPED
	// nested-JSON quota body). Rotate within the member, then advance.
	if status == http.StatusTooManyRequests || outcome.Code == CodeCooldown {
		return &memberFailure{
			outcome: quotaFailureClass,
			advance: true,
			rotate:  true,
			class:   quotaFailureClass,
		}
	}

	// An upstream 401/403 rejected the CREDENTIAL — rotation-eligible.
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return &memberFailure{
			outcome: "upstream_auth_rejected",
			advance: true,
			rotate:  true,
		}
	}

	// Client-attributable classes fail fast: advancing past the member
	// would be dishonest thrash (the request bytes do not change
	// between members).
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return &memberFailure{
			outcome: "client_attributable",
			status:  outcome.RelayStatus,
			body:    attempt.body,
			advance: false,
		}
	}

	// 5xx and everything else: ADVANCE — no rotation (transient,
	// provider-attributable).
	return &memberFailure{
		outcome: "upstream_error",
		advance: true,
	}
}

// commitImageGenWinner performs the ONE client write for the winner
// (the relay semantics mirror the standalone path exactly) and meters
// the SERVED MEMBER's row.
func (h *Handler) commitImageGenWinner(w http.ResponseWriter, groupID string, winner *memberAttempt, authToken *auth.AuthToken) {
	attempt := winner.attempt

	copyResponseHeaders(w, attempt.headers)
	// Content-Length is already stripped by isHopByHopHeader in
	// copyResponseHeaders, so no stale upstream length can survive
	// when a hook rewrites or normalizes the body. The Del call below
	// is the defensive belt-and-suspenders guard ensuring Go's HTTP
	// server stack computes the exact length for the relayed body.
	if _, ok := attempt.provider.(ResponseRewriter); ok {
		w.Header().Del("Content-Length")
	}
	w.WriteHeader(attempt.upstreamStatus)
	if _, err := w.Write(attempt.body); err != nil {
		// The client disconnected; the request is still billed (the
		// upstream completed) but log for observability.
		log.Printf("[imggen] write to client failed: %v (group=%s, member=%s)", err, groupID, winner.memberID)
	}

	// Meter the SERVED MEMBER's row: request_count=1,
	// image_count=BillableImages(the winner's body). The group row
	// gets NOTHING on the winner path (F-2: the member row IS the
	// metered row — the FE usage charts attribute the images to the
	// model that rendered them).
	h.meter(authToken, winner.memberID, imageCountOfWinner(winner))
}

// imageCountOfWinner lifts the billable image count of the winner's
// body (the adapter's own coercion — the same call the standalone path
// makes).
func imageCountOfWinner(winner *memberAttempt) int {
	if winner == nil || winner.attempt.provider == nil {
		return 0
	}
	n, _ := winner.attempt.provider.BillableImages(winner.attempt.body)
	return n
}

// clearImageGenWinner is the WIN-CLEAR call site.
//
// v1: a NO-OP — the imggen lane writes ZERO cross-request health state
// (no marks, no skip-cache writes, no group-owned cooldown — F-3). The
// call site keeps its shape so the v2 health mirror has a single place
// to fill in.
func (h *Handler) clearImageGenWinner(winner *memberAttempt) {
	if winner == nil {
		return
	}
}

// exhaustImageGenGroup writes the exhaustion envelope: 502 + the
// machine code, the GROUP row billed once (request_count=1,
// image_count=0 — the imggen lane's bill-on-error continuity), exactly
// ONE terminal event.
//
// The event carries the walk's accounting counters plus the cause
// suffixes: budget_exhausted (the walk was cut by the budget) and
// short_circuited + last_class (the W-8 quota short-circuit). The
// class detail is EVENT-ONLY — the body's machine code stays stable so
// clients never parse a class out of the error body.
func (h *Handler) exhaustImageGenGroup(w http.ResponseWriter, groupID string, authToken *auth.AuthToken, attempted, skipped, membersAttempted int, startTime time.Time, lastClass string, budgetExhausted, shortCircuited bool) {
	log.Printf("[imggen] group=%s exhausted: %d attempted, %d skipped, %d members attempted (budget_exhausted=%v short_circuited=%v last_class=%q)",
		groupID, attempted, skipped, membersAttempted, budgetExhausted, shortCircuited, lastClass)
	event := map[string]interface{}{
		"outcome":           "all_members_failed",
		"model":             groupID,
		"upstream_status":   0,
		"duration_ms":       time.Since(startTime).Milliseconds(),
		"attempted":         attempted,
		"skipped":           skipped,
		"members_attempted": membersAttempted,
	}
	if budgetExhausted {
		event["budget_exhausted"] = true
	}
	if shortCircuited {
		event["short_circuited"] = true
	}
	if lastClass != "" {
		event["last_class"] = lastClass
	}
	h.publishEvent(eventTypeImageGeneration, event)
	h.openAIErrorWithCode(w, http.StatusBadGateway,
		fmt.Sprintf("all image-gen group members failed (%d attempted)", attempted),
		imggenAllMembersFailedCode)
	// The GROUP row is billed once (F-2: a failed walk meters the
	// group row — never a per-member failure row, never no row at
	// all).
	h.meter(authToken, groupID, 0)
}

// failFastImageGenGroup relays a client-attributable upstream failure
// (the walk stopped — the group did NOT exhaust).
//
// The member's own status + body are relayed verbatim (the standalone
// PromoteToError relay semantics) and the GROUP row is billed once
// (the failed-walk metering stance).
func (h *Handler) failFastImageGenGroup(w http.ResponseWriter, groupID string, authToken *auth.AuthToken, memberID string, attempt imgGenAttempt, failure *memberFailure, attempted, skipped, membersAttempted int, startTime time.Time) {
	event := map[string]interface{}{
		"outcome":           failure.outcome,
		"model":             groupID,
		"member":            memberID,
		"provider":          attempt.cred.Provider,
		"upstream_status":   attempt.upstreamStatus,
		"duration_ms":       time.Since(startTime).Milliseconds(),
		"attempted":         attempted,
		"skipped":           skipped,
		"members_attempted": membersAttempted,
	}
	h.publishEvent(eventTypeImageGeneration, event)

	status := failure.status
	if status == 0 {
		status = http.StatusBadGateway
	}
	if status == http.StatusOK {
		// The RelayStatus==200 clamp (the standalone path's
		// future-proofing tripwire): a failure never relays a 200.
		log.Printf("[imggen] member=%s returned RelayStatus=200 on a failure — clamping to 502", memberID)
		status = http.StatusBadGateway
	}
	// D-4: copy the upstream response headers, parity with the
	// standalone PromoteToError relay (handler.go:511). Hop-by-hop
	// headers — including Content-Length — are skipped by the shared
	// copier, so a rewritten/normalized body can never carry a stale
	// length. The app-owned Content-Type below is set AFTER the copy
	// so it stays authoritative.
	copyResponseHeaders(w, attempt.headers)
	if _, ok := attempt.provider.(ErrorBodyNormalizer); ok {
		w.Header().Del("Content-Length")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(failure.body); err != nil {
		log.Printf("[imggen] write to client failed: %v (group=%s, member=%s)", err, groupID, memberID)
	}
	h.meter(authToken, groupID, 0)
}
