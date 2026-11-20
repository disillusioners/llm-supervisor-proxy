package models

// Virtual Models as Load-Balancing Groups (load-balance-groups
// commission / §3.2 + §5.1, Round-2 membership pivot). This file
// owns the group data model and the G1-G8 validation rule set.
//
// Split-brain discipline: the FIELD-level rules (validateGroupRules)
// hang off the shared ValidateKindRules seam so BOTH write paths
// (JSON-file ModelsConfig.Validate AND DB
// ModelsManager.validateModelAgainstCredentials via the Am23
// validateBeforeWrite routing) plus the pkg/ui POST/PUT inline
// mirrors enforce the same texts. The COLLECTION-aware rules
// (ValidateGroupMembers — member existence/kind/weight/dup/cap and
// the G8 cross-group uniqueness scan) need the model collection and
// are exported so every write path and the parity test share ONE
// reference implementation.

import (
	"fmt"
)

// MaxGroupMembers is the maximum number of member entries allowed
// in one group (G2 cap — reuses the same 16 ceiling as
// MaxCredentialRefs / the MultiCredentialEditor MAX).
const MaxGroupMembers = 16

// GroupMember is a single (model, weight) entry in a group's flat
// member list (§3.2, Round-2 pivot — members are PROXY MODEL IDs,
// not credential triples).
//
//   - ModelID: references models.id of an EXISTING model row of the
//     group's LANE — chat-kind (or the legacy "" normalization) for a
//     chat group, decision-kind for a decision group (app-level FK;
//     G3 + the decision-groups lane rule). Group-as-member is
//     forbidden (G5 — flat membership only, USER-DECIDED).
//   - Weight: positive float; the higher, the more often picked by
//     the sticky weighted selector. Validation: must be > 0 (G3).
//   - BackupOnly: group-backup-members (D2). When true, the member
//     participates in the BACKUP sub-pool only — served exclusively
//     after every default member in the request's exclusion set has
//     failed. Round-trips through models.members_json as the
//     additive bool field `backup_only`; absent ⇒ false (D7 backward
//     compat). Carries no required-migration cost (additive JSON).
type GroupMember struct {
	ModelID    string  `json:"model_id"`
	Weight     float64 `json:"weight"`
	BackupOnly bool    `json:"backup_only,omitempty"`
}

// validateGroupRules enforces the FIELD-level subset of the group
// rule set (§5.1): G1, G6, G7, and the self-referential half of G8.
// Pulled out of ValidateKindRules for readability, mirroring
// validateImageGenRules / validateDecisionRules.
//
// The collection-aware half (G2 members present + cap, G3 member
// existence + weight, G4 no-dup, G5 flat membership, G8 target
// existence/kind + cross-group uniqueness) lives in
// ValidateGroupMembers below — this helper has no collection
// access. `internalProvider` is unused for groups (they carry no
// credentials) and exists only to match the per-kind validator
// signature.
func validateGroupRules(m *ModelConfig, internalProvider string) error {
	_ = internalProvider // groups carry no credentials — no provider gate

	// G1 — Internal==true routes the row through the ~11 internal
	// dispatch predicates (§2.4) before the member rewrite lands;
	// Internal=false would external-passthrough the group.
	if !m.Internal {
		return fmt.Errorf("model %s: kind %q requires internal to be true", m.ID, KindGroup)
	}
	// G6 — imggen/decision posture: deny the aux features that do
	// not apply to groups (and make group→group fallback
	// impossible).
	if len(m.FallbackChain) != 0 {
		return fmt.Errorf("model %s: kind %q does not support fallback_chain", m.ID, KindGroup)
	}
	if m.SecondaryUpstreamModel != "" {
		return fmt.Errorf("model %s: kind %q does not support secondary_upstream_model", m.ID, KindGroup)
	}
	if m.PeakHourEnabled {
		return fmt.Errorf("model %s: kind %q does not support peak_hour_enabled", m.ID, KindGroup)
	}
	if m.ReleaseStreamChunkDeadline != 0 {
		return fmt.Errorf("model %s: kind %q does not support release_stream_chunk_deadline", m.ID, KindGroup)
	}
	// (Former G7 exclusion flag — retired with the
	// ultimate model subsystem; no gate remains for it.)
	// G8 self-part — a group cannot claim its OWN ID (pointless:
	// GetModel(groupID) already returns the group; it would only
	// mask configuration errors). Claiming one of the group's own
	// MEMBERS is legal (self-shadow — canonical day-1 onboarding).
	if m.ShadowModelID == m.ID && m.ShadowModelID != "" {
		return fmt.Errorf("model %s: kind %q shadow_model_id cannot be the group's own ID", m.ID, KindGroup)
	}
	return nil
}

// ValidateGroupMembers enforces the COLLECTION-aware half of the
// group rule set (§5.1): G2 (>=1 member, cap 16), G3 (member
// exists via direct-ID check + is a lane-consistent model + weight
// > 0), G4 (no duplicate members), G5 (group-as-member forbidden —
// flat membership only), and the collection half of G8 (shadow
// target exists, is lane-consistent, is not a group, and no OTHER
// ENABLED group claims the same ID).
//
// `collection` is the FULL proposed model set (it may include m
// itself — the function skips self-references by ID everywhere).
// Callers: the DB write path (ModelsManager.validateGroupWrite,
// Am23 routing inside validateBeforeWrite), the JSON-file backend
// (ModelsConfig.Validate group branch), and the pkg/ui POST/PUT
// inline mirrors' parity reference (kind_rules_parity_test.go).
//
// G3 kind rule (decision-groups commission, widened by the
// imggen-groups commission): a group's members must all share ONE
// normalized kind — chat (or the legacy "" ⇒ chat), decision OR
// image-gen — and the group's shadow target must belong to that same
// lane. A mixed-kind group is rejected with a dedicated message (the
// lane is never persisted; it is inferred from the members). The group
// kind and any unknown kind are not valid members in any lane, and
// groups are rejected with the explicit G5 flat-membership message.
//
// The member-kind and shadow-target errors are worded with the
// group's LANE; the CHAT wording is byte-identical to the historical
// text (the text lane's messages are untouched).
func ValidateGroupMembers(m *ModelConfig, collection []ModelConfig) error {
	if m == nil {
		return nil
	}

	// G2 — at least one member keeps the group live at every write
	// (a one-member group is a valid staging state; >=2 was
	// rejected as paternalism).
	if len(m.Members) == 0 {
		return fmt.Errorf("model %s: kind %q requires at least one member", m.ID, KindGroup)
	}
	if len(m.Members) > MaxGroupMembers {
		return fmt.Errorf("model %s: kind %q members list exceeds max of %d entries (got %d)", m.ID, KindGroup, MaxGroupMembers, len(m.Members))
	}

	// group-backup-members / D2: a group must keep ≥1 NON-BACKUP
	// (default) member. An all-backup group would have no first-tier
	// pick — the engine's default pool would be empty and the
	// fall-to-backup engine entry would carry every request (which
	// the operator does not want, since backups are a recourse
	// only). Rejected here at write time so the operator sees a
	// clear message rather than a misroute-shaped failure later.
	// Cheaper client-side mirror lives in
	// pkg/ui/frontend/src/components/config/GroupModelForm.tsx.
	defaults := 0
	for _, mm := range m.Members {
		if !mm.BackupOnly {
			defaults++
		}
	}
	if defaults < 1 {
		return fmt.Errorf("model %s: kind %q requires at least one non-backup (default) member (currently %d default, %d backup)", m.ID, KindGroup, defaults, len(m.Members)-defaults)
	}

	// Build the collection lookup: ID -> (kind, row).
	byID := make(map[string]ModelConfig, len(collection))
	for _, row := range collection {
		byID[row.ID] = row
	}

	// G3 + G4 + G5 — per-member checks. The member KIND is NOT
	// checked here: the lane is derived from the members FIRST
	// (below), because the kind error is worded with the group's
	// lane — which is only known once every member's kind is known.
	seen := make(map[string]bool, len(m.Members))
	memberRows := make([]ModelConfig, 0, len(m.Members))
	for idx, member := range m.Members {
		if member.ModelID == "" {
			return fmt.Errorf("model %s: kind %q members[%d].model_id is empty", m.ID, KindGroup, idx)
		}
		if member.Weight <= 0 {
			return fmt.Errorf("model %s: kind %q members[%d] (model_id=%q): weight must be > 0, got %v", m.ID, KindGroup, idx, member.ModelID, member.Weight)
		}
		if seen[member.ModelID] {
			// G4 — member-key uniqueness (mirrors the credential
			// dup rule).
			return fmt.Errorf("model %s: kind %q duplicate member model_id %q in members list", m.ID, KindGroup, member.ModelID)
		}
		seen[member.ModelID] = true

		target, exists := byID[member.ModelID]
		if !exists {
			// G3 — direct-ID existence check.
			return fmt.Errorf("model %s: kind %q member %q does not exist (members must reference existing chat-kind models)", m.ID, KindGroup, member.ModelID)
		}
		if target.Kind == KindGroup {
			// G5 — flat membership only (USER-DECIDED).
			return fmt.Errorf("model %s: kind %q member %q is itself a group (flat membership only — group-as-member is forbidden)", m.ID, KindGroup, member.ModelID)
		}
		memberRows = append(memberRows, target)
	}

	// G3 kind rule — the LANE fold (decision-groups commission,
	// widened by the imggen-groups commission).
	// A group's members must all share one normalized kind (chat or
	// the legacy "" ⇒ chat, OR decision, OR image-gen): a mixed-kind
	// group is unrepresentable at write time, because the request-time
	// lane inference (unanimity over the CURRENT member rows) would be
	// ambiguous and the group could then be served on the wrong
	// lane. The error is worded with the group's lane — the CHAT
	// wording stays byte-identical to the historical text (the
	// lane-worded design keeps the text lane's messages untouched).
	memberKinds := make([]string, 0, len(memberRows))
	for _, target := range memberRows {
		memberKinds = append(memberKinds, target.Kind)
	}
	groupLane := DeriveGroupLane(memberKinds)
	lanes := GroupLanesOf(memberKinds)
	if len(lanes) > 1 {
		// MIXED lanes — a dedicated message (not the member-kind
		// one): name the first member that disagrees with the group's
		// lane.
		for idx, target := range memberRows {
			lane, ok := MemberLane(target.Kind)
			if !ok || lane == groupLane {
				continue
			}
			return fmt.Errorf("%s", MixedGroupMembersError(m.ID, m.Members[idx].ModelID, target.Kind))
		}
		// Unreachable in practice (len(lanes) > 1 implies a
		// disagreeing member exists) — kept as a defensive fallback
		// so the function can never fall through without an error.
		return fmt.Errorf("model %s: kind %q members must all share one kind (chat, decision or image-gen)", m.ID, KindGroup)
	}
	// Per-member kind check (now lane-worded). An invalid kind (a
	// kind that belongs to no lane — group, unknown) is rejected: the
	// member cannot serve any lane.
	for idx, target := range memberRows {
		if _, ok := MemberLane(target.Kind); ok {
			continue
		}
		return fmt.Errorf("%s", InvalidMemberKindError(m.ID, m.Members[idx].ModelID, string(groupLane), target.Kind))
	}

	// G8 collection half — shadow-target integrity + uniqueness.
	// The shadow target must belong to the GROUP'S LANE (the
	// decision-groups commission, widened by the imggen-groups
	// commission): a chat group claims a chat-kind row, a decision
	// group claims a decision-kind row, an image-gen group claims an
	// image-gen-kind row. The CHAT wording stays byte-identical to the
	// historical text.
	if m.ShadowModelID != "" {
		target, exists := byID[m.ShadowModelID]
		if !exists {
			return fmt.Errorf("model %s: kind %q shadow_model_id %q does not exist (must reference an existing %s-kind model)", m.ID, KindGroup, m.ShadowModelID, groupLane)
		}
		if target.Kind == KindGroup {
			return fmt.Errorf("model %s: kind %q shadow_model_id %q must reference %s %s-kind model, not a group", m.ID, KindGroup, m.ShadowModelID, LaneArticle(string(groupLane)), groupLane)
		}
		if shadowLane, ok := MemberLane(target.Kind); !ok || shadowLane != groupLane {
			return fmt.Errorf("model %s: kind %q shadow_model_id %q must reference %s %s-kind model (got kind %q)", m.ID, KindGroup, m.ShadowModelID, LaneArticle(string(groupLane)), groupLane, target.Kind)
		}
		// Uniqueness scan: no OTHER ENABLED group may claim the
		// same ID (two disabled claims are inert — claims activate
		// only on enabled groups; enable-ordering resolves through
		// this same scan).
		for _, other := range collection {
			if other.ID == m.ID || other.Kind != KindGroup {
				continue
			}
			if other.Enabled && other.ShadowModelID == m.ShadowModelID {
				return fmt.Errorf("model %s: kind %q shadow_model_id %q is already claimed by enabled group %q", m.ID, KindGroup, m.ShadowModelID, other.ID)
			}
		}
	}

	return nil
}
