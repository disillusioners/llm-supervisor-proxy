package imggen

// imggen-groups commission — the IN-REQUEST credential-list seam (D-1).
//
// WHY THIS EXISTS
//
// Production wires the image-gen handler's credential resolver to
// modelsConfig.ResolveInternalConfigWithAffinity(modelID, modelID)
// (cmd/main.go:343-349). The conversation key is NON-EMPTY (= the member's
// model id), so the credentiallb engine BINDS that key to one credential
// with a 30m TTL. A re-resolve after a rotation-eligible failure hits the
// binding and returns the SAME credential — triedPairs then fires and the
// member gets exactly ONE attempt. The decision lane escapes this by
// seeding an engine cooldown (ExcludeAndReselectForClass), but that is a
// CROSS-REQUEST health write, forbidden on this lane by F-3.
//
// THE FIX (adjudicated option (a))
//
// For each member the walk builds an ORDERED candidate list from the
// member's OWN credential refs (flattened through the models seam, so
// credential groups expand the same way the store resolves them), and
// each attempt consumes the NEXT UNTRIED entry. No engine call, no
// affinity binding, no cooldown/mark/skip-cache write: the list is built
// and consumed entirely WITHIN the request. Zero cross-request state.
//
// INTERFACE / DECORATOR SURFACE
//
// NO new manager or decorator method is needed. The seam is composed from
// two methods that already exist on models.ModelsConfigInterface and are
// already implemented by the PRODUCTION binding (modelscache.CachedModelsConfig):
//   - FlattenCredentialRefs(model) — the decorator DELEGATES to the inner
//     store (read-through, authoritative, modelscache/models.go:879).
//   - GetCredential(id) — the decorator serves the CACHED DECRYPTED
//     credential (modelscache/models.go:623), zero DB on the hot path.
// The per-credential field composition (provider / apiKey / baseURL /
// wire model) uses models.ResolveCredentialFor — the SAME helper the
// store's resolver now delegates to — so the two paths cannot drift.
//
// Keeping the seam in pkg/imggen avoids an interface-surface gap by
// construction: there is no new method for the decorator to forget to
// forward. The decorator-through proof is a test that drives the walk
// with the REAL CachedModelsConfig over a real store (see
// TestServeImageGenGroup_RotationThroughCachedConfig).

import (
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// memberCredentialCandidates returns the member's credential candidates
// in CONFIGURED order (the flattened credential refs of its own row),
// each resolved into the executable credential shape.
//
// The member row is the LIVE row from the request's single GetModels
// read (never the claims-first client-facing lookup). A dangling ref
// (credential deleted mid-flight) is SKIPPED — the store's resolver
// treats it as unresolvable too — so the walk simply tries the next
// candidate instead of stalling on a vanished credential.
func (h *Handler) memberCredentialCandidates(member *models.ModelConfig) []models.ResolvedCredential {
	if h == nil || h.modelsConfig == nil || member == nil {
		return nil
	}
	refs := h.modelsConfig.FlattenCredentialRefs(member)
	out := make([]models.ResolvedCredential, 0, len(refs))
	for _, ref := range refs {
		if ref.CredentialID == "" {
			continue
		}
		cred := h.modelsConfig.GetCredential(ref.CredentialID)
		if cred == nil {
			continue // dangling ref — skip, try the next candidate
		}
		resolved := models.ResolveCredentialFor(member, cred)
		resolved.CredentialID = ref.CredentialID
		out = append(out, resolved)
	}
	return out
}
