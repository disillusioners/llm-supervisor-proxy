package models

// Lane-fold mirror (decision-groups commission): the fold primitives
// in this file (MemberLane, GroupLanesOf, UnanimousGroupLane,
// GroupLaneFromCollection) are mirrored on the FE at
// pkg/ui/frontend/src/components/config/groupLane.ts. Any change to
// the server fold MUST be mirrored in the FE twin in the same commit
// — the two must not ship different answers (ADR-19).

import "fmt"

// Group-lane primitives for the decision-groups commission.
//
// A group's LANE is never persisted: it is DERIVED from the group's
// members. The derivation is a single fold over the members' normalized
// kinds, and it is the SAME fold everywhere — the canonical write-time
// validator, the two admin-API mirrors and the request-time inference
// all call the primitives below, so the lane can never drift between
// them.
//
// The lane vocabulary:
//
//	chat      — members are chat-kind (or the legacy "" kind)
//	decision  — members are decision-kind
//	image-gen — members are image-gen-kind
//
// A member whose kind belongs to no lane (group, unknown) carries NO
// lane evidence: it is an unservable member, not a lane vote. Two
// VALID lanes in one group (a chat member next to a decision member, a
// chat member next to an image-gen member) is a MIXED group —
// unrepresentable at write time, unservable at request time (fail
// closed, never misrouted).

// GroupLane names the lane a group's members belong to.
type GroupLane string

const (
	// GroupLaneChat is the chat lane: members are chat-kind models
	// (kind "chat" or the legacy "" normalization).
	GroupLaneChat GroupLane = "chat"

	// GroupLaneDecision is the decision lane: members are
	// decision-kind models (kind "decision").
	GroupLaneDecision GroupLane = "decision"

	// GroupLaneImageGen is the image-gen lane: members are
	// image-gen-kind models (kind "image-gen"). The group is served by
	// /v1/image_generation only.
	GroupLaneImageGen GroupLane = "image-gen"
)

// MemberLane normalizes a model kind into the lane its row belongs to.
//
// The legacy "" kind normalizes to CHAT — never to decision: a
// chat-era row must not silently join a decision group (contract 3).
// A kind that belongs to no lane (group, unknown) returns ok=false —
// the caller decides what an invalid member means (the write-time
// validator rejects it; the request-time fold ignores it as
// unservable).
func MemberLane(kind string) (GroupLane, bool) {
	switch kind {
	case "", KindChat:
		return GroupLaneChat, true
	case KindDecision:
		return GroupLaneDecision, true
	case KindImageGen:
		return GroupLaneImageGen, true
	default:
		return "", false
	}
}

// GroupLanesOf returns the DISTINCT lanes present in kinds, in a
// stable order (chat before decision before image-gen). Kinds that
// belong to no lane are ignored — they are unservable members, not
// lane evidence.
//
// len(result) > 1 means the group is MIXED (write-time rejection /
// request-time fail-closed); len(result) == 0 means every member is
// invalid (the write-time validator words the error with the chat
// lane by convention — the chat wording is the historical default).
func GroupLanesOf(kinds []string) []GroupLane {
	var chat, decision, imageGen bool
	for _, kind := range kinds {
		lane, ok := MemberLane(kind)
		if !ok {
			continue
		}
		switch lane {
		case GroupLaneChat:
			chat = true
		case GroupLaneDecision:
			decision = true
		case GroupLaneImageGen:
			imageGen = true
		}
	}
	lanes := make([]GroupLane, 0, 3)
	if chat {
		lanes = append(lanes, GroupLaneChat)
	}
	if decision {
		lanes = append(lanes, GroupLaneDecision)
	}
	if imageGen {
		lanes = append(lanes, GroupLaneImageGen)
	}
	return lanes
}

// UnanimousGroupLane folds the given member kinds into the single lane
// they share — the SINGLE-SOURCE-OF-TRUTH fold (the canonical
// validator, the admin-API mirrors and the request-time inference all
// use it, so the lane can never drift between them).
//
// ok=false when the valid lanes are not unanimous (a MIXED group) or
// when no valid lane is present at all.
func UnanimousGroupLane(kinds []string) (GroupLane, bool) {
	lanes := GroupLanesOf(kinds)
	if len(lanes) != 1 {
		return "", false
	}
	return lanes[0], true
}

// EffectiveKind normalizes a model kind for an EFFECTIVE-KIND
// comparison: the legacy "" kind IS the chat kind (the same
// normalization MemberLane applies).
//
// The kind-immutability guard (ADR-1) compares EFFECTIVE kinds: a
// group member whose kind is rewritten from "chat" to the legacy ""
// is NOT retyped (the effective kind is unchanged), while a member
// rewritten from "decision" to "chat" (or to "") IS retyped.
func EffectiveKind(kind string) string {
	if kind == "" {
		return KindChat
	}
	return kind
}

// KindChangeGroupMemberMessage is the ADR-1 rejection message for a
// kind change on a group member — the SINGLE source of truth for the
// text, shared by the canonical write path (pkg/store/database) and
// the API mirror (pkg/ui) so the two can never drift.
//
// The message names BOTH ids (actionable, not generic):
//
//	model <member-id>: kind cannot be changed while it is a member of group <group-id> (remove it from the group first)
func KindChangeGroupMemberMessage(memberID, groupID string) string {
	return fmt.Sprintf("model %s: kind cannot be changed while it is a member of group %s (remove it from the group first)", memberID, groupID)
}

// GroupLaneFromCollection derives a group's lane from the model
// COLLECTION (the group's current member rows are looked up by id).
//
// It is the collection-shaped twin of the manager's request-time
// inference: both fold the CURRENT member kinds (never the write-time
// value). A vanished member contributes no lane evidence; a disabled
// member still counts (its kind is readable).
func GroupLaneFromCollection(group ModelConfig, collection []ModelConfig) (GroupLane, bool) {
	if !group.IsGroup() {
		return "", false
	}
	byID := make(map[string]ModelConfig, len(collection))
	for _, row := range collection {
		byID[row.ID] = row
	}
	kinds := make([]string, 0, len(group.Members))
	for _, member := range group.Members {
		row, ok := byID[member.ModelID]
		if !ok {
			continue // vanished member — no lane evidence
		}
		kinds = append(kinds, row.Kind)
	}
	return UnanimousGroupLane(kinds)
}

// DeriveGroupLane returns the group's lane: the first VALID member's
// lane, defaulting to GroupLaneChat when no member carries a valid
// kind. Single source for the lane derivation used by
// ValidateGroupMembers and the POST/PUT admin-API mirrors.
func DeriveGroupLane(memberKinds []string) GroupLane {
	groupLane := GroupLaneChat
	for _, kind := range memberKinds {
		if lane, ok := MemberLane(kind); ok {
			groupLane = lane
			break
		}
	}
	return groupLane
}

// MixedGroupMembersError is the shared rejection message for a
// mixed-kind group (the canonical validator + the POST/PUT mirrors
// all use it — ONE vocabulary).
func MixedGroupMembersError(groupID, memberID, kind string) string {
	return fmt.Sprintf("model %s: kind %q members must all share one kind (chat, decision or image-gen) — member %q has kind %q", groupID, KindGroup, memberID, kind)
}

// InvalidMemberKindError is the shared rejection message for a member
// whose kind belongs to no lane.
//
// The article is lane-aware (W-5): "a chat-kind model", "a
// decision-kind model" but "an image-gen-kind model" — the historical
// chat wording stays byte-identical.
func InvalidMemberKindError(groupID, memberID, groupLane, kind string) string {
	return fmt.Sprintf("model %s: kind %q member %q must be %s %s-kind model (got kind %q)", groupID, KindGroup, memberID, LaneArticle(groupLane), groupLane, kind)
}

// LaneArticle returns the INDEFINITE ARTICLE that reads correctly
// before a lane word: "a chat-kind model", "a decision-kind model",
// "an image-gen-kind model".
//
// It exists so that NO lane-worded message carries the grammatical bug
// ("a image-gen-kind model"). The chat and decision wording is
// byte-identical to the historical text.
func LaneArticle(lane string) string {
	if lane == string(GroupLaneImageGen) {
		return "an"
	}
	return "a"
}

// ValidateFallbackChainLanes refuses a model's fallback chain that
// names a DECISION-lane or an IMAGE-GEN-lane group.
//
// The write-time half of the rewrite-boundary lane check: a chat lane
// must never carry a decision group or an image-gen group in its chain
// (the foreign member would enter the chat race list). The rule is
// lane-gated — a chain target that is not a decision / image-gen group
// is untouched (the historical existence-only check still applies).
func ValidateFallbackChainLanes(m *ModelConfig, collection []ModelConfig) error {
	if m == nil || len(m.FallbackChain) == 0 {
		return nil
	}
	if m.IsGroup() {
		return nil // a group carries no fallback_chain (G6)
	}
	for _, targetID := range m.FallbackChain {
		for _, row := range collection {
			if row.ID != targetID || !row.IsGroup() {
				continue
			}
			lane, ok := GroupLaneFromCollection(row, collection)
			if !ok {
				continue // an unknown / mixed-lane group — untouched
			}
			switch lane {
			case GroupLaneDecision:
				return fmt.Errorf("model %s: fallback_chain must not reference the decision-lane group %q (a decision group is served by /v1/decisions only)", m.ID, targetID)
			case GroupLaneImageGen:
				return fmt.Errorf("model %s: fallback_chain must not reference the image-gen-lane group %q (an image-gen group is served by /v1/image_generation only)", m.ID, targetID)
			}
		}
	}
	return nil
}
