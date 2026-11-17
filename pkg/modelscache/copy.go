package modelscache

import (
	"sort"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// Deep-copy helpers (cache isolation contract, success criterion I):
// every value handed OUT of the cache is a deep copy, and every value
// stored IN the cache is a deep copy of what the caller/inner store
// produced — so no caller can mutate cache state through a returned
// slice/pointer and no inner-store aliasing can leak in.

func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// copyCredentialRefs clones a credential-ref slice. A ref carries
// (credential_id, group_id, weight, position) — NO secret material — so
// the element copy is a full clone. GroupID survives because this is a
// plain copy(dst, src) over the value struct (A-14).
func copyCredentialRefs(src []models.CredentialRef) []models.CredentialRef {
	if src == nil {
		return nil
	}
	dst := make([]models.CredentialRef, len(src))
	copy(dst, src)
	return dst
}

// copyGroupMembers clones a group member list. GroupMember carries
// value fields only (ModelID, Weight, BackupOnly), so the element
// copy is a full clone.
func copyGroupMembers(src []models.GroupMember) []models.GroupMember {
	if src == nil {
		return nil
	}
	dst := make([]models.GroupMember, len(src))
	copy(dst, src)
	return dst
}

// deepCopyModelConfig clones a ModelConfig including the mutable
// slices (FallbackChain, TruncateParams, Credentials, Members).
// PeakHour* and other fields are value types carried by the struct
// copy.
//
// Members (the group member list) is cloned since the cached group
// rows are now handed out by the claim-blind native read too
// (GetModelNative) — a shared slice would let a caller mutate cache
// state through the returned pointer.
func deepCopyModelConfig(m *models.ModelConfig) *models.ModelConfig {
	if m == nil {
		return nil
	}
	cp := *m
	cp.FallbackChain = copyStrings(m.FallbackChain)
	cp.TruncateParams = copyStrings(m.TruncateParams)
	cp.Credentials = copyCredentialRefs(m.Credentials)
	cp.Members = copyGroupMembers(m.Members)
	return &cp
}

// deepCopyCredentialConfig clones a CredentialConfig. The struct is
// all value fields today; the copy still goes through a fresh struct
// so future reference-typed fields get cloned here, at one site.
func deepCopyCredentialConfig(c *models.CredentialConfig) *models.CredentialConfig {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

// deepCopyModelConfigs clones a whole snapshot (value slice of
// structs whose slice fields must be cloned per entry).
func deepCopyModelConfigs(ms []models.ModelConfig) []models.ModelConfig {
	if ms == nil {
		return nil
	}
	dst := make([]models.ModelConfig, len(ms))
	for i := range ms {
		dst[i] = *deepCopyModelConfig(&ms[i])
	}
	return dst
}

// rebuildSnapshotsLocked derives the ordered full/enabled snapshots
// from modelsByID (write lock must be held). Ordering matches the
// store's `ORDER BY name` so UI list output is stable.
func rebuildSnapshotsLocked(modelsByID map[string]*models.ModelConfig) ([]models.ModelConfig, []models.ModelConfig) {
	all := make([]models.ModelConfig, 0, len(modelsByID))
	for _, m := range modelsByID {
		all = append(all, *m)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })

	enabled := make([]models.ModelConfig, 0, len(all))
	for i := range all {
		if all[i].Enabled {
			enabled = append(enabled, all[i])
		}
	}
	return all, enabled
}

// deepCopyCredentialGroupConfig clones a CredentialGroupConfig including
// its Members slice.
func deepCopyCredentialGroupConfig(g *models.CredentialGroupConfig) *models.CredentialGroupConfig {
	if g == nil {
		return nil
	}
	cp := *g
	cp.Members = copyStrings(g.Members)
	return &cp
}

// deepCopyCredentialGroupConfigs clones a whole snapshot.
func deepCopyCredentialGroupConfigs(gs []models.CredentialGroupConfig) []models.CredentialGroupConfig {
	if gs == nil {
		return nil
	}
	dst := make([]models.CredentialGroupConfig, len(gs))
	for i := range gs {
		cp := deepCopyCredentialGroupConfig(&gs[i])
		dst[i] = *cp
	}
	return dst
}
