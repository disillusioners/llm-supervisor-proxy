package models

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
//	chat     — members are chat-kind (or the legacy "" kind)
//	decision — members are decision-kind
//
// A member whose kind belongs to no lane (image-gen, group, unknown)
// carries NO lane evidence: it is an unservable member, not a lane
// vote. Two VALID lanes in one group (a chat member next to a
// decision member) is a MIXED group — unrepresentable at write time,
// unservable at request time (fail closed, never misrouted).

// GroupLane names the lane a group's members belong to.
type GroupLane string

const (
	// GroupLaneChat is the chat lane: members are chat-kind models
	// (kind "chat" or the legacy "" normalization).
	GroupLaneChat GroupLane = "chat"

	// GroupLaneDecision is the decision lane: members are
	// decision-kind models (kind "decision").
	GroupLaneDecision GroupLane = "decision"
)

// MemberLane normalizes a model kind into the lane its row belongs to.
//
// The legacy "" kind normalizes to CHAT — never to decision: a
// chat-era row must not silently join a decision group (contract 3).
// A kind that belongs to no lane (image-gen, group, unknown) returns
// ok=false — the caller decides what an invalid member means (the
// write-time validator rejects it; the request-time fold ignores it
// as unservable).
func MemberLane(kind string) (GroupLane, bool) {
	switch kind {
	case "", KindChat:
		return GroupLaneChat, true
	case KindDecision:
		return GroupLaneDecision, true
	default:
		return "", false
	}
}

// GroupLanesOf returns the DISTINCT lanes present in kinds, in a
// stable order (chat before decision). Kinds that belong to no lane
// are ignored — they are unservable members, not lane evidence.
//
// len(result) > 1 means the group is MIXED (write-time rejection /
// request-time fail-closed); len(result) == 0 means every member is
// invalid (the write-time validator words the error with the chat
// lane by convention — the chat wording is the historical default).
func GroupLanesOf(kinds []string) []GroupLane {
	var chat, decision bool
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
		}
	}
	lanes := make([]GroupLane, 0, 2)
	if chat {
		lanes = append(lanes, GroupLaneChat)
	}
	if decision {
		lanes = append(lanes, GroupLaneDecision)
	}
	return lanes
}

// UnanimousGroupLane folds the given member kinds into the single lane// they share — the SINGLE-SOURCE-OF-TRUTH fold (the canonical
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
