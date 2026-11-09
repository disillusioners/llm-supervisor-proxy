package models

// Model copy semantics (Copy-model commission — BE half).
//
// A model row is a FLAT row in the single `models` table
// (ModelConfig — pkg/models/config.go); the only uniqueness rule is
// the `id` PRIMARY KEY. Groups are ROWS in the SAME table
// (kind="group") that carry their membership inline (members_json) —
// there is NO join table and NO back-reference on the member row.
// That single fact drives the whole copy contract:
//
//   - The copy of a chat / image-gen / decision row is STANDALONE BY
//     CONSTRUCTION: group membership lives on the GROUP row, so a
//     copied model is in no group, joins no race chain and no
//     load-balance pool. The copy route never writes a group row.
//   - A group row is NOT copyable: a group's identity IS its
//     membership. A copy carrying the same members would silently
//     double those models' traffic share; a copy with no members would
//     violate G2 (1..16 members — pkg/models/group.go:128-133) and D2
//     (>=1 non-backup member — pkg/models/group.go:144-152).
//
// The builder lives here (not in the HTTP layer) so the fidelity
// contract is unit-testable without a server.

import (
	"strings"
	"unicode"
)

// Copy-model sentinels. The route maps these onto the model-API
// error vocabulary (400 + {"error":"<string>"}).
var (
	// ErrCopyEmptyNewID — the operator did not supply a new id.
	ErrCopyEmptyNewID = &ConfigError{"new_id is required"}
	// ErrCopyInvalidNewID — the new id contains "/" or whitespace.
	ErrCopyInvalidNewID = &ConfigError{`new_id must not contain "/" or whitespace`}
	// ErrCopySameID — the new id equals the source model's id.
	ErrCopySameID = &ConfigError{"new_id must differ from the source model id"}
	// ErrCopyGroupNotCopyable — the source row is a group row.
	ErrCopyGroupNotCopyable = &ConfigError{"group models cannot be copied"}
)

// ValidateCopyID enforces the MINIMAL new-id rule for a copy target:
// non-empty, no "/" (the id is used as a URL path segment) and no
// whitespace.
//
// The schema carries NO other id format rule, so this is
// deliberately the ONLY id constraint the copy route enforces — the
// copy does not invent a stricter id grammar than the rest of the
// model API.
func ValidateCopyID(newID string) error {
	if newID == "" {
		return ErrCopyEmptyNewID
	}
	if strings.Contains(newID, "/") {
		return ErrCopyInvalidNewID
	}
	if strings.IndexFunc(newID, unicode.IsSpace) >= 0 {
		return ErrCopyInvalidNewID
	}
	return nil
}

// BuildModelCopy builds a DORMANT clone of src under newID.
//
// Copy semantics (the contract):
//
//   - COPIED VERBATIM (behaviour-defining): truncate_params;
//     internal; internal_base_url; internal_model; credentials[] (as
//     REFS — the copy SHARES the credential rows, no secret is ever
//     duplicated and the credentials table is NEVER written);
//     release_stream_chunk_deadline; peak_hour_enabled / start / end
//     / timezone / model; secondary_upstream_model;
//     exclude_from_ultimate_switching; kind. The provider is inherited
//     implicitly through Credentials[0] (it is not a model-row field).
//
//   - REGENERATED: id = newID; name = newID (unique by construction —
//     it never clobbers the cache name index, which is last-write-wins
//     at pkg/modelscache/models.go:816/861 — the operator can rename
//     the copy later through the existing edit form); enabled = false
//     (a dormant clone: it is not auto-selected and appears in no
//     /v1/models listing, race chain or pool; a client that names its
//     id explicitly is still served).
//
//   - fallback_chain: copied, EXCEPT any entry equal to the SOURCE id
//     is dropped — a clone must not silently fall back into its own
//     source (hidden traffic coupling). This is the ONE documented
//     deviation from a verbatim copy.
//
//   - NEVER TOUCHED: created_at / updated_at (DB-managed, not carried
//     on the Go struct); credential_id (derived in Go from
//     Credentials[0] — never set here); shadow_model_id (CLEARED — a
//     verbatim copy would double-claim the shadowed id and be
//     rejected by G8); members (group-only); usage stats /
//     credentiallb cooldowns / sticky / request-log state (all keyed
//     by model id — the copy starts fresh and does NOT inherit the
//     source's quota cooldown).
//
// The returned copy shares no slice with the source (the credential
// refs and the string slices are cloned), so a later mutation of the
// copy can never reach back into the source row.
func BuildModelCopy(src *ModelConfig, newID string) (*ModelConfig, error) {
	if src == nil {
		return nil, ErrModelNotFound
	}
	if err := ValidateCopyID(newID); err != nil {
		return nil, err
	}
	if newID == src.ID {
		return nil, ErrCopySameID
	}
	if src.IsGroup() {
		return nil, ErrCopyGroupNotCopyable
	}

	cp := &ModelConfig{
		ID: newID,
		// Name = the new id: unique by construction, so the copy can
		// never collide with an existing model name in the cache's
		// name index.
		Name:           newID,
		Enabled:        false, // dormant clone
		Kind:           src.Kind,
		TruncateParams: copyStringSlice(src.TruncateParams),
		// Internal upstream configuration — verbatim.
		Internal:        src.Internal,
		InternalBaseURL: src.InternalBaseURL,
		InternalModel:   src.InternalModel,
		// Credential REFS only — the credential ROWS are shared with
		// the source model (never duplicated, never written here).
		Credentials:                copyCredentialRefs(src.Credentials),
		ReleaseStreamChunkDeadline: src.ReleaseStreamChunkDeadline,
		// Peak-hour auto-switch — verbatim.
		PeakHourEnabled:  src.PeakHourEnabled,
		PeakHourStart:    src.PeakHourStart,
		PeakHourEnd:      src.PeakHourEnd,
		PeakHourTimezone: src.PeakHourTimezone,
		PeakHourModel:    src.PeakHourModel,
		// Secondary upstream + ultimate-switching exclusion — verbatim.
		SecondaryUpstreamModel:       src.SecondaryUpstreamModel,
		ExcludeFromUltimateSwitching: src.ExcludeFromUltimateSwitching,
		// Members is deliberately NOT copied: membership lives on the
		// group row, so the copy is standalone by construction (and a
		// group source is refused above anyway).
		// ShadowModelID is deliberately CLEARED: a verbatim copy would
		// double-claim the shadowed id (G8 rejection).
	}

	// fallback_chain — copied, except entries pointing at the source
	// itself (a clone must not fall back into its own source).
	for _, fallbackID := range src.FallbackChain {
		if fallbackID == src.ID {
			continue
		}
		cp.FallbackChain = append(cp.FallbackChain, fallbackID)
	}

	return cp, nil
}

// copyStringSlice clones a string slice so the copy shares no
// backing array with the source row.
func copyStringSlice(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// copyCredentialRefs clones a credential-ref slice. A ref carries
// only (credential_id, weight, position) — NO secret material — so the
// element copy is a full clone.
func copyCredentialRefs(src []CredentialRef) []CredentialRef {
	if src == nil {
		return nil
	}
	dst := make([]CredentialRef, len(src))
	copy(dst, src)
	return dst
}
