package database

// decision-groups commission — the ADR-1 kind-immutability guard for
// group members.
//
// The lane of a group is INFERRED from its members (no lane column on
// the group row), so a member whose kind is rewritten can silently
// RE-LANE its group: a single-member decision group whose member
// becomes chat-kind is indistinguishable from a native chat group.
//
// The fence therefore lives at the RETYPING site: a model that is a
// member of ANY group has an IMMUTABLE kind. The rejection names BOTH
// ids (actionable, not generic) and rides the existing group-save 400
// family (the bare {"error":"…"} envelope at the API layer).
//
// The guard is SCOPED: a model that is not a member of any group may
// still be retyped — this is not a blanket "kinds are immutable"
// change.

import (
	"errors"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// validateKindChange rejects a kind change for a model that is a
// member of any group. Called from the model-update write path
// (UpdateModel) BEFORE the write.
//
// The membership is derived from the CURRENT model set (the already
// forwarded GetModels — no new manager method is spent here, per
// ADR-1's decorator note).
func (m *ModelsManager) validateKindChange(modelID, newKind string) error {
	current := m.GetModelNative(modelID)
	if current == nil {
		// Unknown row — the existence check reports it; the guard has
		// nothing to compare against.
		return nil
	}
	if models.EffectiveKind(current.Kind) == models.EffectiveKind(newKind) {
		return nil
	}
	for _, group := range m.GetModels() {
		if !group.IsGroup() {
			continue
		}
		for _, member := range group.Members {
			if member.ModelID == modelID {
				return errors.New(models.KindChangeGroupMemberMessage(modelID, group.ID))
			}
		}
	}
	return nil
}
