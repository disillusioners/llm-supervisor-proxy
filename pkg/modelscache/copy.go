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

func copyCredentialRefs(src []models.CredentialRef) []models.CredentialRef {
	if src == nil {
		return nil
	}
	dst := make([]models.CredentialRef, len(src))
	copy(dst, src)
	return dst
}

// deepCopyModelConfig clones a ModelConfig including the mutable
// slices (FallbackChain, TruncateParams, Credentials). PeakHour*
// and other fields are value types carried by the struct copy.
func deepCopyModelConfig(m *models.ModelConfig) *models.ModelConfig {
	if m == nil {
		return nil
	}
	cp := *m
	cp.FallbackChain = copyStrings(m.FallbackChain)
	cp.TruncateParams = copyStrings(m.TruncateParams)
	cp.Credentials = copyCredentialRefs(m.Credentials)
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
