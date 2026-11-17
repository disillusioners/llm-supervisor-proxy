package decision

// The decision lane's GROUP branch — the in-lane member loop.
//
// A decision group is a group whose members are decision-kind models.
// The lane is INFERRED from the members (never persisted); the write
// path guarantees the members share one lane.
//
// THE LOOP CONTRACT:
//
//   - The PICK is POLICY: pools + weights + configured order, never a
//     mark (the pick reads no failure state — the policy file carries
//     none).
//   - An ATTEMPT may be SUPPRESSED by a health mark (zero upstream
//     cost, recorded as a skip — never a failure).
//   - A member is failed only when its OWN resilience is consumed:
//     its own credentials are attempted first (rotation-eligible
//     classes), and only then does the loop advance to the next
//     member.
//   - Exactly ONE client write, at the winner. No attempt before the
//     winner touches the writer.
//   - Exhaustion (every member genuinely attempted and failed) ⇒ 502
//     + code=decision_all_members_failed, no usage row, one event.
//
// The failure-class policy mirrors the text lane exactly:
//
//	rotation-eligible (limit-class + upstream 401/403) ⇒ rotate
//	    within the member (the member's next credential is
//	    attempted); the limit classes also write the upstream-layer
//	    marks.
//	5xx ⇒ ADVANCE to the next member immediately — no within-member
//	    rotation, no mark, no cooldown write.
//	client-attributable (400/413/415/422) ⇒ FAIL FAST: advancing
//	    past the member would be dishonest thrash (the request bytes
//	    do not change between members).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credclass"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/credentiallb"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// decisionAllMembersFailedCode is the machine code of the group's
// exhaustion envelope — the distinctive, greppable feature marker.
const decisionAllMembersFailedCode = "decision_all_members_failed"

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
	// mark reports whether the limit-class marks must be written.
	mark bool
	// class is the credential-engine failure class for the rotation.
	class credentiallb.FailureClass
}

// memberAttempt is the result of ONE upstream attempt for one member
// (or the classified failure).
type memberAttempt struct {
	memberID       string
	cred           models.ResolvedCredential
	provider       UpstreamProvider
	typed          *DecisionResponse
	outcome        *Outcome
	upstreamStatus int
	body           []byte
	err            error
	failure        *memberFailure
}

// serveDecisionGroup serves a decision-lane group request: the policy
// order, the per-member attempts, the single commit at the winner and
// the exhaustion envelope.
func (h *Handler) serveDecisionGroup(w http.ResponseWriter, r *http.Request, group *models.ModelConfig, authToken *auth.AuthToken, req *DecisionRequest, rawBody []byte, startTime time.Time) {
	groupID := group.ID

	// The lane gate (fail closed): the seam must be present AND the
	// group must be a decision-lane group. An absent seam is loud.
	lane, ok := h.groupLane(groupID)
	if !ok || lane != models.GroupLaneDecision {
		log.Printf("[decision] group=%s refused: the group lane is not decision (lane=%q resolved=%v) — failing closed", groupID, lane, ok)
		h.publishEvent(eventTypeDecision, map[string]interface{}{
			"outcome":         "decision_misroute",
			"model":           groupID,
			"duration_ms":     time.Since(startTime).Milliseconds(),
			"upstream_status": 0,
		})
		h.openAIErrorWithCode(w, http.StatusBadRequest,
			fmt.Sprintf("decision models are served by /v1/decisions only (group %q is not a decision group)", groupID),
			"decision_misroute")
		return
	}

	// The GROUP-layer policy facts (no failure state) and the attempt
	// order (the policy head first).
	policy := buildGroupPolicy(group, h.currentModelRows(), lane)
	order := policy.order()
	if len(order) == 0 {
		h.exhaustDecisionGroup(w, groupID, 0, 0, 0, startTime)
		return
	}

	triedMembers := make(map[string]bool, len(order))
	triedPairs := make(map[string]bool, len(order))
	attempted := 0
	skipped := 0
	membersAttempted := 0
	var winner *memberAttempt
	var headSuppressed *policyMember
	var headCred models.ResolvedCredential
	var lastFailure *memberAttempt
	hasSuppressedOnly := false
	dcGrantedMember := ""

	for idx := range order {
		candidate := order[idx]
		if triedMembers[candidate.ID] {
			continue
		}
		triedMembers[candidate.ID] = true

		// The member's own resilience: its credentials are attempted
		// one by one (the rotation-eligible classes consume them
		// BEFORE the loop advances).
		for {
			cred, credOK := h.safeResolveCredential(candidate.ID)
			if !credOK {
				// The member has no usable credential — its own
				// resilience is consumed; the walk advances.
				h.publishEvent(eventTypeDecision, map[string]interface{}{
					"outcome":         "credential_unresolved",
					"model":           groupID,
					"member":          candidate.ID,
					"duration_ms":     time.Since(startTime).Milliseconds(),
					"upstream_status": 0,
				})
				break
			}
			pairKey := candidate.ID + "|" + cred.CredentialID
			if triedPairs[pairKey] {
				// Every credential of this member was already
				// attempted — the member's resilience is consumed.
				break
			}
			triedPairs[pairKey] = true

			wireModel := h.decisionMarkKey(candidate.ID)
			suppressed, reason, honestAttempt := h.attemptSuppressed(candidate.ID, cred, wireModel, &dcGrantedMember)
			if suppressed {
				skipped++
				hasSuppressedOnly = true
				if idx == 0 && headSuppressed == nil {
					headSuppressed = &candidate
					headCred = cred
				}
				h.publishEvent(eventTypeDecision, map[string]interface{}{
					"outcome":         "member_suppressed",
					"model":           groupID,
					"member":          candidate.ID,
					"reason":          reason,
					"duration_ms":     time.Since(startTime).Milliseconds(),
					"upstream_status": 0,
				})
				break
			}

			attempted++
			membersAttempted++
			h.publishEvent(eventTypeDecision, map[string]interface{}{
				"outcome":         "member_attempted",
				"model":           groupID,
				"member":          candidate.ID,
				"provider":        cred.Provider,
				"duration_ms":     time.Since(startTime).Milliseconds(),
				"upstream_status": 0,
			})

			res := h.attemptDecisionMember(candidate.ID, cred, req, rawBody, r)
			if res.failure == nil {
				winner = &res
				break
			}
			lf := res
			lastFailure = &lf
			if res.failure.mark {
				h.suppression.markLimit(candidate.ID, cred.CredentialID, cred.Provider, wireModel)
			}
			h.publishEvent(eventTypeDecision, map[string]interface{}{
				"outcome":         res.failure.outcome,
				"model":           groupID,
				"member":          candidate.ID,
				"provider":        cred.Provider,
				"upstream_status": res.upstreamStatus,
				"duration_ms":     time.Since(startTime).Milliseconds(),
			})
			if !res.failure.advance {
				// Client-attributable failure: the walk stops (the
				// upstream's own failure is the answer).
				h.failFastDecisionGroup(w, groupID, res, attempted, skipped, membersAttempted, startTime)
				return
			}
			if res.failure.rotate {
				if honestAttempt {
					break
				}
				h.rotateMemberCredential(candidate.ID, cred, res.failure.class)
				continue
			}
			break
		}
		if winner != nil {
			break
		}
	}

	if winner == nil {
		// OQ-1: suppression never removes the LAST attempt. When
		// every candidate was suppressed (and nothing was genuinely
		// attempted), the POLICY HEAD gets exactly one honest
		// (unsuppressed) attempt — the pick stays policy.
		if attempted == 0 && headSuppressed != nil {
			res := h.attemptDecisionMember(headSuppressed.ID, headCred, req, rawBody, r)
			if res.failure == nil {
				winner = &res
				attempted++
				membersAttempted++
			} else {
				lf := res
				lastFailure = &lf
				if res.failure.mark {
					h.suppression.markLimit(headSuppressed.ID, headCred.CredentialID, headCred.Provider, h.decisionMarkKey(headSuppressed.ID))
				}
				if !res.failure.advance {
					h.failFastDecisionGroup(w, groupID, res, attempted, skipped, membersAttempted, startTime)
					return
				}
				attempted++
				membersAttempted++
			}
		}
	}

	if winner == nil {
		// B1: the envelope fires only when every member was
		// genuinely attempted-and-failed. A member that was only
		// SUPPRESSED (never attempted) must not count as a failure:
		// if a genuine failure exists, fail with that failure's error
		// instead of the exhaustion envelope.
		if hasSuppressedOnly && lastFailure != nil {
			h.failFastDecisionGroup(w, groupID, *lastFailure, attempted, skipped, membersAttempted, startTime)
			return
		}
		h.exhaustDecisionGroup(w, groupID, attempted, skipped, membersAttempted, startTime)
		return
	}

	// The WINNER — the single commit. The win-clear runs on the
	// WINNER's identity (the failed head's marks survive).
	h.suppression.clearWinner(winner.memberID, winner.cred.CredentialID, winner.cred.Provider, h.decisionMarkKey(winner.memberID))
	h.commitDecisionWinner(w, groupID, winner, authToken)
	h.publishEvent(eventTypeDecision, map[string]interface{}{
		"outcome":         "success",
		"model":           groupID,
		"member":          winner.memberID,
		"provider":        winner.cred.Provider,
		"input_tokens":    decisionUsageTokens(winner.typed),
		"output_tokens":   decisionUsageTokens(winner.typed),
		"upstream_status": winner.upstreamStatus,
		"duration_ms":     time.Since(startTime).Milliseconds(),
	})
}

// commitDecisionWinner performs the ONE client write for the winner
// (the relay semantics mirror the non-group path exactly) and meters
// the served member's row (success only).
func (h *Handler) commitDecisionWinner(w http.ResponseWriter, groupID string, winner *memberAttempt, authToken *auth.AuthToken) {
	if winner.outcome != nil && winner.outcome.RelayStatus == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(winner.upstreamStatus)
		if winner.typed != nil {
			if err := json.NewEncoder(w).Encode(winner.typed); err != nil {
				log.Printf("[decision] write to client failed: %v (group=%s, member=%s)", err, groupID, winner.memberID)
			}
		} else {
			if _, err := w.Write(winner.body); err != nil {
				log.Printf("[decision] write to client failed: %v (group=%s, member=%s)", err, groupID, winner.memberID)
			}
		}
	} else {
		status := http.StatusBadGateway
		if winner.outcome != nil && winner.outcome.RelayStatus != 0 {
			status = winner.outcome.RelayStatus
		}
		if status == http.StatusOK {
			status = http.StatusBadGateway
		}
		w.WriteHeader(status)
		if _, err := w.Write(winner.body); err != nil {
			log.Printf("[decision] write to client failed: %v (group=%s, member=%s)", err, groupID, winner.memberID)
		}
	}
	// Meter the SERVED MEMBER's row (the decision lane has no
	// RouteModel stamp — the member row IS the metered row).
	if winner.outcome == nil || winner.outcome.RelayStatus == 0 {
		h.meterOnSuccess(authToken, winner.memberID, winner.provider, winner.typed)
	}
}

// exhaustDecisionGroup writes the exhaustion envelope: 502 + the
// machine code, no usage row, exactly ONE event.
func (h *Handler) exhaustDecisionGroup(w http.ResponseWriter, groupID string, attempted, skipped, membersAttempted int, startTime time.Time) {
	log.Printf("[decision] group=%s exhausted: %d attempted, %d skipped, %d members attempted", groupID, attempted, skipped, membersAttempted)
	h.publishEvent(eventTypeDecision, map[string]interface{}{
		"outcome":           "all_members_failed",
		"model":             groupID,
		"attempted":         attempted,
		"skipped":           skipped,
		"members_attempted": membersAttempted,
		"upstream_status":   0,
		"duration_ms":       time.Since(startTime).Milliseconds(),
	})
	h.openAIErrorWithCode(w, http.StatusBadGateway,
		fmt.Sprintf("all decision group members failed (%d attempted)", attempted),
		decisionAllMembersFailedCode)
}

// failFastDecisionGroup relays a client-attributable upstream failure
// (the walk stopped — the group did NOT exhaust).
func (h *Handler) failFastDecisionGroup(w http.ResponseWriter, groupID string, res memberAttempt, attempted, skipped, membersAttempted int, startTime time.Time) {
	status := http.StatusBadGateway
	if res.failure != nil && res.failure.status != 0 {
		status = res.failure.status
	}
	h.publishEvent(eventTypeDecision, map[string]interface{}{
		"outcome":           res.failure.outcome,
		"model":             groupID,
		"member":            res.memberID,
		"provider":          res.provider,
		"upstream_status":   res.upstreamStatus,
		"attempted":         attempted,
		"skipped":           skipped,
		"members_attempted": membersAttempted,
		"duration_ms":       time.Since(startTime).Milliseconds(),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(res.body); err != nil {
		log.Printf("[decision] write to client failed: %v (group=%s, member=%s)", err, groupID, res.memberID)
	}
}

// attemptSuppressed decides attempt-vs-skip for one (member,
// credential) pair — the ONLY place on this lane that reads a health
// layer.
//
// A suppressed attempt performs ZERO upstream calls and is recorded
// as a skip (never a failure). The all-credentials-cooling case (the
// member's own resilience) gets exactly ONE honest attempt: the cache
// is never a failure oracle at any layer.
func (h *Handler) attemptSuppressed(memberID string, cred models.ResolvedCredential, wireModel string, dcGranted *string) (bool, string, bool) {
	// No health layer wired ⇒ nothing can suppress an attempt (the
	// lane degrades to "always attempt"; the demotion path still runs
	// when a layer IS wired).
	if h.suppression.upstream == nil && h.suppression.model == nil {
		return false, "", false
	}
	marked := h.suppression.credentialMarked(cred.CredentialID, cred.Provider, wireModel)
	modelMarked := h.suppression.modelMarked(memberID)
	if !marked && !modelMarked {
		return false, "", false
	}
	// The member-level symmetry (ADR-15's D-C): when EVERY credential
	// of the member is unavailable — cooling OR marked in the
	// upstream layer — the member gets exactly ONE honest attempt on
	// its policy-first credential. The cache is never a failure oracle
	// at any layer: a mark/cooling record cannot, by itself, remove
	// the member's last attempt.
	if h.memberFullyUnavailable(memberID) {
		// Per-member latch + per-request cap (B2, rulings Q1+Q2):
		// at most one honest attempt per member AND at most one per
		// request (the first fully-unavailable member in policy order).
		// A later fully-unavailable member is skipped zero-cost.
		if *dcGranted != "" {
			return true, "all_credentials_unavailable_already_granted", false
		}
		*dcGranted = memberID
		return false, "all_credentials_unavailable", true
	}
	if modelMarked {
		return true, "model_layer_mark", false
	}
	return true, "upstream_layer_mark", false
}

// memberFullyUnavailable reports whether EVERY credential of the member
// is UNAVAILABLE — cooling (the credential engine's own rotation state)
// OR marked in the UPSTREAM layer (the shared, process-wide skip cache —
// the credential-scope mark, the reader is
// skipcache.Cache.Marked(ScopeCredential, credID, wireModel)).
//
// ADR-15's D-C: a health-marked credential is NOT an upstream outage —
// it is an upstream-layer record of a limited/unhealthy credential,
// exactly the state this escape covers. "Every credential cooling or
// marked" ⇒ the member is in the D-C state ⇒ it gets EXACTLY ONE
// honest attempt on its policy-first credential (the cache is never a
// failure oracle at any layer).
//
// The wire model is the MEMBER ROW's internal model (the composed
// credential tuple mirrors the manager's row-identity resolution: the
// wire model is the member row's).
func (h *Handler) memberFullyUnavailable(memberID string) bool {
	row := h.memberRow(memberID)
	if row == nil || len(row.Credentials) == 0 {
		return false
	}
	wireModel := h.decisionMarkKey(memberID)
	for _, ref := range row.Credentials {
		cooling := false
		if h.credEngine != nil {
			if _, ok := h.credEngine.CooldownUntil(memberID, ref.CredentialID); ok {
				cooling = true
			}
		}
		marked := h.suppression.credentialScopeMarked(ref.CredentialID, wireModel)
		if !cooling && !marked {
			return false
		}
	}
	return true
}

// rotateMemberCredential seeds the credential engine's cooldown for the
// failed credential (the per-class policy stays owned by the engine)
// so the member's NEXT credential is offered.
func (h *Handler) rotateMemberCredential(memberID string, cred models.ResolvedCredential, class credentiallb.FailureClass) {
	if h.credEngine == nil || cred.CredentialID == "" {
		return
	}
	// The credential key is (memberID, memberID) — the lane's stable
	// per-model credential key (no conversation binding on this lane).
	h.credEngine.ExcludeAndReselectForClass(memberID, memberID, cred.CredentialID, class, 0)
}

// memberRow returns the member's CURRENT row (claim-blind: the live
// model set, never the claims-first client-facing lookup).
func (h *Handler) memberRow(memberID string) *models.ModelConfig {
	for _, row := range h.currentModelRows() {
		if row.ID == memberID {
			return &row
		}
	}
	return nil
}

// currentModelRows returns the live model set (the decorator-forwarded
// GetModels). Nil-safe.
func (h *Handler) currentModelRows() []models.ModelConfig {
	if h.modelsConfig == nil {
		return nil
	}
	return h.modelsConfig.GetModels()
}

// decisionMarkKey is THE mark key of the decision lane — the single
// chain shared by the mark-WRITE path and the mark-READ path:
//
//	row.InternalModel  ->  memberID
//
// The member row's internal model (the wire model the lane sends
// upstream), else the member's row id.
//
// The chain is MEMBER-LEVEL on purpose. The read side sees only
// credential REFS (an id, a weight, a position — never a resolved
// credential's internal model), so a credential-derived term could not
// be computed identically on both sides; a member-level chain CAN, so
// the write key and the read key are equal BY CONSTRUCTION, not by
// inspection.
//
// The credential's internal-model term is deliberately NOT part of the
// chain: the row's internal model is the chain's HEAD, so the
// credential term only ever mattered for a degenerate row (an empty
// internal model — constructible only outside the validated path),
// where the write key and the read key would otherwise disagree. A
// mark written under one key and read under another is exactly how the
// cache would become a failure oracle.
//
// (The resolved credential's internal model may be SUBSTITUTED — a
// peak-hour window serves the peak model — while the chain stays on
// the row's internal model. That never diverges the two sides: the
// row's head decides, on both.)
func (h *Handler) decisionMarkKey(memberID string) string {
	if row := h.memberRow(memberID); row != nil && row.InternalModel != "" {
		return row.InternalModel
	}
	return memberID
}

// decisionUsageTokens reads the typed usage of a served response.
func decisionUsageTokens(resp *DecisionResponse) int64 {
	if resp == nil {
		return 0
	}
	return resp.Usage.InputTokens + resp.Usage.OutputTokens
}

// attemptDecisionMember performs ONE upstream attempt for one member
// and classifies the outcome. It performs NO client write — the write
// happens only at the winner.
func (h *Handler) attemptDecisionMember(memberID string, cred models.ResolvedCredential, req *DecisionRequest, rawBody []byte, r *http.Request) memberAttempt {
	res := memberAttempt{memberID: memberID, cred: cred}

	provider, ok := lookup(cred.Provider)
	if !ok {
		res.failure = &memberFailure{
			outcome: "unsupported_provider",
			advance: true,
		}
		return res
	}
	res.provider = provider

	if sp, isSystemOne := provider.(*systemOneProvider); isSystemOne {
		if _, failed := sp.PreflightValidate(req); failed {
			res.failure = &memberFailure{
				outcome: "systemone_preflight_failed",
				status:  http.StatusBadRequest,
				advance: false,
			}
			return res
		}
	}

	upstreamURL, upstreamBody, err := provider.BuildUpstream(cred, req, rawBody)
	if err != nil {
		res.failure = &memberFailure{outcome: "build_failed", advance: true}
		return res
	}

	ctx, cancel := context.WithTimeout(context.Background(), h.decisionTimeout())
	defer cancel()

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(upstreamBody))
	if err != nil {
		res.failure = &memberFailure{outcome: "request_build_failed", advance: true}
		return res
	}
	upstreamReq.Header.Set("Content-Type", "application/json")
	upstreamReq.Header.Set("Authorization", "Bearer "+cred.APIKey)
	for key, values := range r.Header {
		if isHopByHopHeader(key) || isClientAuthHeader(key) ||
			strings.EqualFold(key, "Content-Type") ||
			strings.EqualFold(key, "Accept-Encoding") {
			continue
		}
		for _, value := range values {
			upstreamReq.Header.Add(key, value)
		}
	}

	resp, err := sharedHTTPClient.Do(upstreamReq)
	if err != nil {
		outcome := "upstream_transport_error"
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			outcome = "deadline"
		}
		res.failure = &memberFailure{outcome: outcome, advance: true}
		return res
	}
	defer resp.Body.Close()
	res.upstreamStatus = resp.StatusCode

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		res.failure = &memberFailure{outcome: "upstream_read_failed", advance: true}
		return res
	}
	if len(respBody) > maxResponseBodyBytes {
		res.failure = &memberFailure{outcome: "response_too_large", advance: true}
		return res
	}
	res.body = respBody

	typedResp, outcome, err := provider.InspectResponse(respBody, req)
	if err != nil {
		res.failure = &memberFailure{outcome: "inspect_error", advance: true}
		return res
	}
	res.typed = typedResp
	res.outcome = outcome
	if outcome == nil {
		res.failure = &memberFailure{outcome: "inspect_error", advance: true}
		return res
	}
	// The handler stamps the RAW upstream status — the adapter seam
	// never sees it, and the classification needs it.
	outcome.UpstreamStatus = resp.StatusCode

	if outcome.RelayStatus == 0 {
		// Success — the winner.
		return res
	}
	res.failure = classifyMemberFailure(resp.StatusCode, outcome)
	return res
}

// classifyMemberFailure classifies a failed member attempt per the
// failure-class policy (the text lane's policy, mirrored exactly).
func classifyMemberFailure(status int, outcome *Outcome) *memberFailure {
	errType, errCode, errMessage := "", "", ""
	if outcome != nil {
		errType, errCode, errMessage = outcome.ErrorType, outcome.ErrorCode, outcome.ErrorMessage
	}
	failure := &memberFailure{outcome: "upstream_error", advance: true}

	// Limit classes first (quota before rate-limit — the shared
	// vocabulary in the leaf package).
	switch {
	case credclass.IsQuota(status, errType, errCode, errMessage):
		failure.outcome = "upstream_quota_exceeded"
		failure.rotate = true
		failure.mark = true
		failure.class = credentiallb.FailureClassQuota
		return failure
	case credclass.IsRateLimit(status, errType, errCode, errMessage):
		failure.outcome = "upstream_rate_limited"
		failure.rotate = true
		failure.mark = true
		failure.class = credentiallb.FailureClassRateLimit
		return failure
	}
	// An upstream 401/403 rejected the CREDENTIAL — rotation-eligible
	// (no mark: the marks fire on the limit classes only).
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		failure.outcome = "upstream_auth_rejected"
		failure.rotate = true
		failure.class = credentiallb.FailureClassRateLimit
		return failure
	}
	// Client-attributable classes fail fast: advancing past the
	// member would be dishonest thrash (the request bytes do not
	// change between members).
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge,
		http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		failure.outcome = "client_attributable"
		failure.status = status
		failure.advance = false
		return failure
	}
	// 5xx and everything else: ADVANCE — no rotation, no mark, no
	// cooldown write (transient, provider-attributable).
	return failure
}
