package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Duration is a custom type that serializes to human-readable format (e.g., "1m50s")
// instead of nanoseconds. Required because time.Duration marshals to int64.
type Duration int64

// MarshalJSON serializes Duration to a human-readable string format
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON parses Duration from string or number
func (d *Duration) UnmarshalJSON(data []byte) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case string:
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid duration format: %s", value)
		}
		if parsed < 0 {
			return errors.New("duration cannot be negative")
		}
		*d = Duration(parsed)
	case float64:
		if value < 0 {
			return errors.New("duration cannot be negative")
		}
		*d = Duration(time.Duration(value))
	default:
		return errors.New("invalid duration format")
	}
	return nil
}

// String returns the Duration as a human-readable string
func (d Duration) String() string {
	return time.Duration(d).String()
}

// Duration returns the time.Duration value
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// AppName is the application name used for config directory
const AppName = "llm-supervisor-proxy"

// MaxFallbackDepth is the maximum depth allowed for fallback chains (primary + 2 fallbacks).
// Deprecated: This constant is no longer used as fallback is now single-level (max 1 item).
const MaxFallbackDepth = 3

// GetConfigPath returns the path to the models config file.
// Uses XDG standard: ~/.config/llm-supervisor-proxy/models.json
func GetConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		// Fallback to current directory
		return "models.json"
	}
	return filepath.Join(configDir, AppName, "models.json")
}

// MaxCredentialRefs is the maximum number of credential references
// allowed per model. Multi-credential load balancing lives in
// Phase 2 (pkg/credentiallb); Phase 1 enforces the upper bound so
// the JSON column and the engine inputs stay aligned.
const MaxCredentialRefs = 16

// Kind discriminator values (ImgGen Models + Decision Models
// commissions). Models default to chat (empty/KindChat);
// image-gen models are served exclusively by /v1/image_generation
// and excluded from /v1/models and the chat picker surfaces;
// decision models are served exclusively by /v1/decisions and
// excluded from /v1/models and the chat picker surfaces. Unknown
// values are rejected by Validate.
const (
	// KindChat is the default; every existing pre-ImgGen model is
	// implicitly chat. Empty Kind also normalizes to chat.
	KindChat = "chat"

	// KindImageGen marks models served by /v1/image_generation
	// (ImgGen Models commission / BE-D2). These require
	// Internal==true, InternalModel!="", Credentials non-empty,
	// primary credential provider=="minimax", FallbackChain
	// empty, SecondaryUpstreamModel=="", PeakHourEnabled==false,
	// ReleaseStreamChunkDeadline==0. Excluded from /v1/models.
	KindImageGen = "image-gen"

	// KindDecision marks models served by /v1/decisions
	// (Decision Models commission / §3.1 site 1). These require
	// Internal==true, InternalModel!="", Credentials non-empty,
	// primary credential provider∈{"openai","typesafe"}
	// (recommended identity B — openai reused, typesafe new;
	// override A uses {"openai-decision","typesafe"}),
	// FallbackChain empty, SecondaryUpstreamModel=="",
	// PeakHourEnabled==false, ReleaseStreamChunkDeadline==0,
	// and EXPLICIT ExcludeFromUltimateSwitching==true (D9 — the
	// image-gen doc/code drift at config.go:258-259 is NOT
	// inherited). Excluded from /v1/models.
	KindDecision = "decision"

	// KindGroup marks VIRTUAL load-balancing group models
	// (load-balance-groups commission / §3.2, Round-2 membership
	// pivot — members are PROXY MODEL IDs, not credential
	// triples). A group row carries a flat Members list
	// ([]GroupMember{ModelID, Weight}, 1..16 entries, each an
	// existing text/chat-kind model ID) and an optional single
	// ID-space claim (ShadowModelID). Groups are served by the
	// normal chat pipeline: one thin layer rewrites the request's
	// modelList to the sticky-picked member, after which the
	// member's existing pipeline runs unchanged. Groups carry no
	// credentials, no fallback_chain, no secondary_upstream_model,
	// no peak_hour_*, no release_stream_chunk_deadline (G6), and
	// REQUIRE ExcludeFromUltimateSwitching==true (G7). Listed in
	// /v1/models; a claimed model's ID resolves to the group
	// (claim-first GetModel) while the claimed row stays editable
	// via the strict twins.
	KindGroup = "group"
)

// IsImageGen reports whether the model is served by the image
// generation endpoint. Empty Kind is treated as chat.
func (m *ModelConfig) IsImageGen() bool {
	return m != nil && m.Kind == KindImageGen
}

// IsDecision reports whether the model is served by the decision
// endpoint (/v1/decisions; Decision Models commission / §3.1
// site 2). Empty Kind is treated as chat.
func (m *ModelConfig) IsDecision() bool {
	return m != nil && m.Kind == KindDecision
}

// IsGroup reports whether the model is a virtual load-balancing
// group (load-balance-groups commission / §3.2). Empty Kind is
// treated as chat. Groups are chat-servable (they stay listed in
// /v1/models) but are excluded from the FE chat tab and token
// pickers; they are neither IsImageGen nor IsDecision, so the
// misroute guards pass them through to the normal chat paths.
func (m *ModelConfig) IsGroup() bool {
	return m != nil && m.Kind == KindGroup
}

// ValidateKindRules enforces the `kind` discriminator rules
// (ImgGen Models + Decision Models commissions / BE-D2 / §3.1
// sites 3-4). It is the single source of truth called from BOTH
// `ModelsConfig.Validate` (JSON-file backend) AND
// `ModelsManager.validateModelAgainstCredentials` (DB-backed
// production write path) — the Architect Amendment 2 split-brain
// fix. Returns nil for empty/KindChat (existing chat rules apply
// unchanged).
//
// `internalProvider` is the lowercase provider string of the model's
// PRIMARY credential (`Credentials[0]`). Pass "" when unknown — the
// provider check is then skipped and the image-gen or decision
// row with a non-allowed provider will be caught the moment the
// credential is persisted (the legacy
// `validateModelAgainstCredentials` per-ref provider-match
// invariant still fires).
//
// The caller is responsible for the cross-cutting chat-validation
// rules (InternalModel != "" when Internal, credentials non-empty,
// etc.); this helper ONLY adds the kind-specific gates. The
// image-gen rule set is:
//
//   - Internal == true
//   - InternalModel != "" (the body-rewrite alias has a target)
//   - Credentials non-empty
//   - internalProvider == "minimax" (Phase 1 ships minimax-only)
//   - FallbackChain empty
//   - SecondaryUpstreamModel == ""
//   - PeakHourEnabled == false
//   - ReleaseStreamChunkDeadline == 0 (no stream chunks in image resp)
//
// The decision rule set (D1-D9; Decision Models / §3.1 sites 3-4,
// §5.6 validateDecisionRules): Internal == true, InternalModel
// != "", Credentials non-empty, internalProvider ∈ {"openai",
// "typesafe"} (recommended identity B), FallbackChain empty,
// SecondaryUpstreamModel == "", PeakHourEnabled == false,
// ReleaseStreamChunkDeadline == 0, ExcludeFromUltimateSwitching
// == true (D9 — explicit enforcement, not the image-gen
// doc/code drift).
//
// The group rule set (G1-G8 field-level subset; load-balance-groups
// commission / §5.1, §5.2): Internal == true (G1), FallbackChain
// empty, SecondaryUpstreamModel == "", PeakHourEnabled == false,
// ReleaseStreamChunkDeadline == 0 (G6), ExcludeFromUltimateSwitching
// == true (G7 — D9 posture), and ShadowModelID != the group's own
// ID (G8 self-part). The collection-aware half of the G-rules
// (member existence/kind/weight/dup/cap, shadow-target checks and
// the G8 cross-group uniqueness scan) lives in ValidateGroupMembers
// (pkg/models/group.go) — it needs the model collection and is run
// by the write paths, not by this helper.
//
// Unknown `kind` values are rejected (typo protection —
// `image_gen` silently meaning chat is exactly the failure mode
// this guard prevents).
func ValidateKindRules(m *ModelConfig, internalProvider string) error {
	if m == nil {
		return nil
	}
	switch m.Kind {
	case "", KindChat:
		return nil
	case KindImageGen:
		return validateImageGenRules(m, internalProvider)
	case KindDecision:
		return validateDecisionRules(m, internalProvider)
	case KindGroup:
		return validateGroupRules(m, internalProvider)
	default:
		return fmt.Errorf("model %s: unknown kind %q (expected %q, %q, %q, or %q)", m.ID, m.Kind, KindChat, KindImageGen, KindDecision, KindGroup)
	}
}

// validateImageGenRules enforces the image-gen rule set (BE-D2).
// Pulled out of ValidateKindRules for readability.
func validateImageGenRules(m *ModelConfig, internalProvider string) error {
	if !m.Internal {
		return fmt.Errorf("model %s: kind %q requires internal to be true", m.ID, KindImageGen)
	}
	if m.InternalModel == "" {
		return fmt.Errorf("model %s: kind %q requires internal_model to be set (the body-rewrite alias has nothing to resolve)", m.ID, KindImageGen)
	}
	if len(m.Credentials) == 0 {
		return fmt.Errorf("model %s: kind %q requires at least one credential", m.ID, KindImageGen)
	}
	// Pass "" from callers without a loaded credential (JSON-file
	// Validate path); the per-ref provider-match invariant in
	// ModelsManager.validateModelAgainstCredentials catches the
	// non-minimax provider at DB write time.
	if internalProvider != "" && internalProvider != "minimax" {
		return fmt.Errorf("model %s: kind %q requires the primary credential's provider to be %q (got %q)", m.ID, KindImageGen, "minimax", internalProvider)
	}
	if len(m.FallbackChain) != 0 {
		return fmt.Errorf("model %s: kind %q does not support fallback_chain", m.ID, KindImageGen)
	}
	if m.SecondaryUpstreamModel != "" {
		return fmt.Errorf("model %s: kind %q does not support secondary_upstream_model", m.ID, KindImageGen)
	}
	if m.PeakHourEnabled {
		return fmt.Errorf("model %s: kind %q does not support peak_hour_enabled", m.ID, KindImageGen)
	}
	if m.ReleaseStreamChunkDeadline != 0 {
		return fmt.Errorf("model %s: kind %q does not support release_stream_chunk_deadline (image responses are not streamed)", m.ID, KindImageGen)
	}
	return nil
}

// validateDecisionRules enforces the decision rule set
// (Decision Models commission / §3.1 site 4 / §5.6 / D1-D9).
// Pulled out of ValidateKindRules for readability.
//
// Justified analog of validateImageGenRules — same posture:
// internal-only, deny aux features that don't apply,
// provider-gated (recommended identity B: `openai` reused +
// `typesafe` new; override A: `openai-decision` + `typesafe`).
//
// D9 ExcludeFromUltimateSwitching == true is EXPLICITLY enforced
// here — the image-gen doc/code drift at config.go:258-259 (the
// doc claimed enforcement but no code enforced) is NOT inherited.
// Decision gets belt-and-suspenders write-time enforcement.
func validateDecisionRules(m *ModelConfig, internalProvider string) error {
	if !m.Internal {
		return fmt.Errorf("model %s: kind %q requires internal to be true", m.ID, KindDecision)
	}
	if m.InternalModel == "" {
		return fmt.Errorf("model %s: kind %q requires internal_model to be set", m.ID, KindDecision)
	}
	if len(m.Credentials) == 0 {
		return fmt.Errorf("model %s: kind %q requires at least one credential", m.ID, KindDecision)
	}
	// D4 provider gate — recommended identity B ("openai" reused,
	// "typesafe" new). Override A would swap "openai" → "openai-decision".
	// Pass "" from callers without a loaded credential (JSON-file
	// Validate path); the per-ref provider-match invariant in
	// ModelsManager.validateModelAgainstCredentials catches the
	// non-allowed provider at DB write time.
	if internalProvider != "" {
		switch internalProvider {
		case "openai", "typesafe":
			// ok
		default:
			return fmt.Errorf("model %s: kind %q requires the primary credential's provider to be %q or %q (got %q)", m.ID, KindDecision, "openai", "typesafe", internalProvider)
		}
	}
	// D5
	if len(m.FallbackChain) != 0 {
		return fmt.Errorf("model %s: kind %q does not support fallback_chain", m.ID, KindDecision)
	}
	// D6
	if m.SecondaryUpstreamModel != "" {
		return fmt.Errorf("model %s: kind %q does not support secondary_upstream_model", m.ID, KindDecision)
	}
	// D7
	if m.PeakHourEnabled {
		return fmt.Errorf("model %s: kind %q does not support peak_hour_enabled", m.ID, KindDecision)
	}
	// D8
	if m.ReleaseStreamChunkDeadline != 0 {
		return fmt.Errorf("model %s: kind %q does not support release_stream_chunk_deadline (decision responses are not streamed)", m.ID, KindDecision)
	}
	// D9 — EXPLICIT enforcement. Decision ships with belt-and-suspenders
	// write-time enforcement of exclude_from_ultimate_switching=true;
	// unlike image-gen, the doc/code drift at config.go:258-259 is
	// NOT inherited.
	if !m.ExcludeFromUltimateSwitching {
		return fmt.Errorf("model %s: kind %q requires exclude_from_ultimate_switching to be true (D9 explicit enforcement)", m.ID, KindDecision)
	}
	return nil
}

// CredentialRef is a single (credential, weight, position) entry in a
// model's ordered, weighted credential list.
//
//   - CredentialID: references credentials.id (app-level FK).
//   - Weight: positive integer; the higher, the more often picked.
//     Default 1. Validation: must be > 0.
//   - Position: 0-based deterministic ordering. Used to break weight ties
//     (lower position wins) and to define the "primary" credential
//     (Credentials[0]).
type CredentialRef struct {
	CredentialID string `json:"credential_id"`
	Weight       int    `json:"weight"`
	Position     int    `json:"position"`
}

// ModelConfig represents the configuration for a single model.
type ModelConfig struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	FallbackChain  []string `json:"fallback_chain,omitempty"`
	TruncateParams []string `json:"truncate_params,omitempty"` // Parameters to strip before forwarding (e.g. ["max_completion_tokens", "store"])

	// Internal upstream configuration (bypass external LiteLLM, call AI provider directly)
	Internal        bool           `json:"internal,omitempty"`
	InternalBaseURL string         `json:"internal_base_url,omitempty"` // Base URL override (optional, uses credential's base_url if empty)
	InternalModel   string         `json:"internal_model,omitempty"`    // Actual model name for provider (e.g., GLM-5.0)

	// Credentials is the ordered, weighted list of credential refs. Empty for
	// external / non-internal models. Validation: if non-empty, every entry's
	// CredentialID must exist in the credentials table; every entry's weight
	// must be > 0; every entry's provider must match Credentials[0]'s provider
	// (provider-match invariant). Use PrimaryCredentialID() to read the
	// "primary" (back-compat single-credential view) — it returns
	// Credentials[0].CredentialID.
	Credentials []CredentialRef `json:"credentials,omitempty"`

	// ReleaseStreamChunkDeadline is the duration after which buffered stream chunks
	// should be flushed to downstream even if the stream hasn't completed.
	// This prevents clients with idle chunk detection from dropping the connection.
	// Example: "1m50s" (110 seconds). Set to 0 or omit to disable this feature.
	// DORMANT: no call sites in pkg/proxy consume this per-model deadline; the
	// ACTIVE stream deadline is the global StreamDeadline enforced inside the
	// race coordinator (pkg/proxy/race_coordinator.go). The field is persisted
	// and surfaced by the UI but currently has no runtime effect.
	ReleaseStreamChunkDeadline Duration `json:"release_stream_chunk_deadline,omitempty"`

	// PeakHourConfig controls automatic model switching during peak hours.
	PeakHourEnabled  bool   `json:"peak_hour_enabled,omitempty"`
	PeakHourStart    string `json:"peak_hour_start,omitempty"`    // HH:MM format (local time)
	PeakHourEnd      string `json:"peak_hour_end,omitempty"`      // HH:MM format (local time)
	PeakHourTimezone string `json:"peak_hour_timezone,omitempty"` // UTC offset like +7, -5, +5.5
	PeakHourModel    string `json:"peak_hour_model,omitempty"`    // Upstream model name during peak hours

	// Secondary upstream model for retry logic (only valid for internal=true)
	SecondaryUpstreamModel string `json:"secondary_upstream_model,omitempty"`

	// ExcludeFromUltimateSwitching prevents this model from being used in ultimate model switching
	ExcludeFromUltimateSwitching bool `json:"exclude_from_ultimate_switching,omitempty"`

	// Kind discriminates between chat and image-generation models
	// (ImgGen Models commission / BE-D1). `chat` (default; also
	// implied by an empty value) is the historical chat / completion
	// surface; `image-gen` is served exclusively by /v1/image_generation
	// and is excluded from /v1/models and all chat pickers. Unknown
	// values are rejected by ModelsConfig.Validate (via the shared
	// ValidateKindRules helper called from BOTH ModelsConfig.Validate
	// AND ModelsManager.validateModelAgainstCredentials, per
	// Architect Amendment 2). Internally, `kind:"image-gen"` also
	// forces ExcludeFromUltimateSwitching to true on write so the
	// chat-side path stays correct even if the operator edits a row
	// from the chat form. Client `fallback_chain`,
	// `secondary_upstream_model`, `peak_hour_*`, and
	// `release_stream_chunk_deadline` are rejected on image-gen
	// rows (BE-D2 + the FE-5 hidden-field posture).
	Kind string `json:"kind,omitempty"`

	// Members is the flat member list of a kind=="group" row
	// (load-balance-groups commission / §3.2, Round-2 membership
	// pivot). Each entry references an EXISTING text (chat-kind)
	// model by its proxy model ID plus a positive weight; the
	// group is one thin layer that rewrites the request's model
	// list to the sticky-picked member, after which the member's
	// own pipeline (race, retry, credential LB, fallback) runs
	// unchanged. 1..16 entries (G2); no group-as-member (G5 —
	// flat membership only, USER-DECIDED); validated by
	// ValidateGroupMembers at every write path.
	Members []GroupMember `json:"members,omitempty"`

	// ShadowModelID is the optional single ID-space claim of a
	// kind=="group" row (§3.4 — THE design center): when set (and
	// the group is enabled), requests for that model ID resolve to
	// the GROUP row via claim-first GetModel, and the claimed row
	// is hidden from /v1/models. The target must be an existing
	// chat-kind model (G8); claiming one of the group's own
	// members is LEGAL (self-shadow — the canonical day-1
	// `coding[coding]` onboarding); claiming the group's own ID is
	// not. Strict twins and GetModelNative bypass claims.
	ShadowModelID string `json:"shadow_model_id,omitempty"`
}

// GetReleaseStreamChunkDeadline returns the configured deadline duration.
// Returns 0 if not set (feature disabled), otherwise returns the configured duration.
// Note: The comment "Default: 1m50s" refers to the suggested value, but 0 means disabled.
func (m *ModelConfig) GetReleaseStreamChunkDeadline() time.Duration {
	if m.ReleaseStreamChunkDeadline == 0 {
		return 0 // Disabled - no deadline
	}
	return time.Duration(m.ReleaseStreamChunkDeadline)
}

// PrimaryCredentialID returns the legacy single-credential view of the
// model's credentials list: the first ref's CredentialID, or "" if
// the model has no credentials configured.
//
// This is the single-credential fast path used by every existing
// call site (race-internal, ultimate-internal, ultimate-external's
// D3 provider probe) until the Phase 2 LB engine (pkg/credentiallb)
// wires the affinity-aware variant. Behavior is byte-identical to
// the pre-Phase-1 m.CredentialID reads when the model has exactly
// one credential; it returns "" for external models with empty
// Credentials (same as the old default string value).
func (m *ModelConfig) PrimaryCredentialID() string {
	if len(m.Credentials) == 0 {
		return ""
	}
	return m.Credentials[0].CredentialID
}

// ModelsConfigInterface defines the interface for models configuration
// Both JSON and database-backed implementations must satisfy this interface
type ModelsConfigInterface interface {
	GetModels() []ModelConfig
	GetEnabledModels() []ModelConfig
	GetModel(modelID string) *ModelConfig
	GetModelByName(modelName string) *ModelConfig
	GetTruncateParams(modelID string) []string
	GetFallbackChain(modelID string) []string
	AddModel(model ModelConfig) error
	UpdateModel(modelID string, model ModelConfig) error
	RemoveModel(modelID string) error
	Save() error
	Validate() error

	// Credential management
	GetCredential(id string) *CredentialConfig
	GetCredentials() []CredentialConfig
	AddCredential(cred CredentialConfig) error
	UpdateCredential(id string, cred CredentialConfig) error
	RemoveCredential(id string) error

	// Internal config resolution
	ResolveInternalConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool)

	// Phase 3 / Task 16 + Tasks 4/8/9 — affinity resolution seam.
	// The struct form (ResolvedCredential + trailing ok) carries the
	// engine's W-1 NewlyBound signal in addition to the credentials
	// resolved fields. The legacy 5-tuple ResolveInternalConfig remains
	// as the inner fallback for the Credentials == nil || len == 0
	// fast path inside ResolveInternalConfigWithAffinity (and for the
	// single-credential fast path that produces NO engine call).
	ResolveInternalConfigWithAffinity(modelID, conversationKey string) (ResolvedCredential, bool)
}

// ResolvedCredential is the struct form returned by
// ResolveInternalConfigWithAffinity (Phase 3 / #3 — leader-ruled
// struct over 6-tuple). Fields map to legacy 5-tuple except for
// CredentialID (engine-selector output for observability + the
// model_credential_selected event payload) and NewlyBound (W-1
// only-on-first-binding event signal). The canonical concrete
// implementation lives in pkg/store/database.ResolvedCredential;
// pkg/models declares it here so pkg/proxy / pkg/ultimatemodel
// can talk about the shape without an import cycle.
type ResolvedCredential struct {
	Provider      string
	APIKey        string
	BaseURL       string
	InternalModel string
	CredentialID  string
	NewlyBound    bool
}

// ModelsConfig manages the collection of model configurations.
type ModelsConfig struct {
	mu          sync.RWMutex
	Models      []ModelConfig      `json:"models"`
	Credentials *CredentialsConfig `json:"-"` // Credentials are managed separately
	filePath    string

	// claims is the ID-space shadow-claim index (§3.4):
	// shadowed model ID → owning ENABLED group ID. nil-safe: a
	// zero-value ModelsConfig (or a snapshot built by
	// snapshotForValidation, which never consults claims) behaves
	// as claim-free. Rebuilt from the Models slice wherever the
	// in-memory state is rebuilt (Load, AddModel, UpdateModel,
	// RemoveModel) — same rebuild points as the DB twin's
	// post-write rebuild. Disabled groups contribute NO entries
	// (a disabled group must not hijack traffic it isn't
	// serving).
	claims map[string]string
}

// NewModelsConfig creates a new empty ModelsConfig.
func NewModelsConfig() *ModelsConfig {
	return &ModelsConfig{
		Models:      make([]ModelConfig, 0),
		Credentials: NewCredentialsConfig(),
	}
}

// GetTruncateParams returns the list of request-body parameters that should be
// removed before forwarding to the upstream for the given model ID.
// Returns nil if the model is not found or has no truncate_params configured.
func (mc *ModelsConfig) GetTruncateParams(modelID string) []string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	for _, model := range mc.Models {
		if model.ID == modelID {
			if len(model.TruncateParams) == 0 {
				return nil
			}
			result := make([]string, len(model.TruncateParams))
			copy(result, model.TruncateParams)
			return result
		}
	}
	return nil
}

// GetModel returns the model configuration for a given model ID.
// Returns nil if the model is not found.
//
// load-balance-groups §3.4 — CLAIM-FIRST resolution: if the ID is
// claimed by an ENABLED group, the GROUP row is returned (the
// client-facing virtualization seam). Admin surfaces use
// GetModelByName / GetModelNative (claims are ID-space only and
// bypassable by direct ID).
func (mc *ModelsConfig) GetModel(modelID string) *ModelConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Claim-first: claims hit + owning group exists + enabled →
	// the group row (a disabled group's claim is inactive — its
	// entry is absent from the index; the Enabled re-check is
	// defense against any drift).
	if groupID, ok := mc.claims[modelID]; ok && groupID != "" {
		for _, model := range mc.Models {
			if model.ID == groupID {
				if model.Enabled {
					copy := model
					return &copy
				}
				break // disabled owner — claim inactive, fall through
			}
		}
	}

	for _, model := range mc.Models {
		if model.ID == modelID {
			// Return a copy to avoid mutations
			copy := model
			return &copy
		}
	}
	return nil
}

// GetModelNative returns the row for modelID by DIRECT ID lookup,
// BYPASSING the shadow-claim index (§3.4 / Am22 L1 hard rule:
// member refs resolve by row identity; claims are client-facing
// resolution only). This is the lookup the group member machinery
// must use — a claims-first lookup here would infinite-loop the
// day-1 self-shadow group (`coding[coding]`).
func (mc *ModelsConfig) GetModelNative(modelID string) *ModelConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	for _, model := range mc.Models {
		if model.ID == modelID {
			copy := model
			return &copy
		}
	}
	return nil
}

// GetModelByName returns the model configuration for a given model name.
// Returns nil if the model is not found.
func (mc *ModelsConfig) GetModelByName(modelName string) *ModelConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	for _, model := range mc.Models {
		if model.Name == modelName {
			// Return a copy to avoid mutations
			copy := model
			return &copy
		}
	}
	return nil
}

// GetFallbackChain returns the fallback chain for a given model ID.
// Returns nil if the model is not found.
func (mc *ModelsConfig) GetFallbackChain(modelID string) []string {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	for _, model := range mc.Models {
		if model.ID == modelID {
			result := make([]string, 0, len(model.FallbackChain)+1)
			result = append(result, model.ID)
			result = append(result, model.FallbackChain...)
			return result
		}
	}
	return nil
}

// Load loads the models configuration from a JSON file.
func (mc *ModelsConfig) Load(filePath string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// File doesn't exist, initialize with empty config
		mc.Models = make([]ModelConfig, 0)
		mc.Credentials = NewCredentialsConfig()
		mc.filePath = filePath

		// Ensure directory exists and create empty file
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}

		// Create empty models.json file
		emptyData := []byte(`{"models":[],"credentials":[]}`)
		if err := os.WriteFile(filePath, emptyData, 0644); err != nil {
			return fmt.Errorf("failed to create models.json: %w", err)
		}

		return nil
	}

	// Read and parse file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	var config struct {
		Models      []ModelConfig      `json:"models"`
		Credentials []CredentialConfig `json:"credentials"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}

	mc.Models = config.Models
	mc.Credentials = NewCredentialsConfig()
	mc.Credentials.SetCredentials(config.Credentials)
	mc.filePath = filePath

	mc.rebuildClaimsLocked()

	return nil
}

// rebuildClaimsLocked rebuilds the shadow-claim index from the
// Models slice (§3.4). Caller must hold mc.mu (write). Only
// ENABLED groups with a non-empty ShadowModelID contribute
// entries; a stale/nil index degrades to claim-free resolution
// (safe direction: no hijack).
func (mc *ModelsConfig) rebuildClaimsLocked() {
	claims := make(map[string]string)
	for _, model := range mc.Models {
		if model.Kind == KindGroup && model.Enabled && model.ShadowModelID != "" {
			claims[model.ShadowModelID] = model.ID
		}
	}
	mc.claims = claims
}

// Save atomically saves the models configuration to a JSON file.
// It writes to a temporary file first, then renames to the target file.
func (mc *ModelsConfig) Save() error {
	// remove-external-upstream P1 (task 7, layer iii/iii): Save takes no
	// parameter, so the Internal=true stamp runs as a LOOP over the
	// in-memory models — every row leaving through the JSON backend is
	// internal, converging any legacy unstamped rows on the next save.
	// Stamped under the WRITE lock (the slice header below shares the
	// backing array with mc.Models — a read lock must not cover a
	// mutation). (The DB backend's Save is a NO-OP by design — store.go
	// — and is deliberately NOT mirrored there.)
	mc.mu.Lock()
	for i := range mc.Models {
		mc.Models[i].Internal = true
	}
	filePath := mc.filePath
	models := mc.Models
	credentials := mc.Credentials
	mc.mu.Unlock()

	// Validate before saving
	tempConfig := snapshotForValidation(models, credentials)
	if err := tempConfig.Validate(); err != nil {
		return err
	}

	// Get credentials slice for serialization
	var credsSlice []CredentialConfig
	if credentials != nil {
		credsSlice = credentials.ToSlice()
	}

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(struct {
		Models      []ModelConfig      `json:"models"`
		Credentials []CredentialConfig `json:"credentials"`
	}{Models: models, Credentials: credsSlice}, "", "  ")
	if err != nil {
		return err
	}

	// Get directory and filename
	dir := filepath.Dir(filePath)
	filename := filepath.Base(filePath)

	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write to temporary file
	tmpFile, err := os.CreateTemp(dir, filename+".tmp.*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	_, err = tmpFile.Write(data)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Atomic rename
	if err := os.Rename(tmpPath, filePath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Update file path on success
	mc.mu.Lock()
	mc.filePath = filePath
	mc.mu.Unlock()

	return nil
}

// snapshotForValidation returns a fresh *ModelsConfig composed
// of the given Models slice and Credentials pointer, ready to
// pass to Validate(). The returned struct is fresh, but its
// Models and Credentials fields alias the caller's arguments —
// the helper does not copy. The Models slice's backing array is
// the caller's responsibility: pass a copy if you intend to
// mutate the underlying data after the snapshot.
//
// Locking contract: every writer caller (AddModel / UpdateModel /
// RemoveModel) holds mc.mu.Lock() for the duration and passes
// mc.Credentials as the credentials argument. Save() holds
// mc.mu.RLock() only long enough to capture models + credentials
// (the captured credentials pointer is identical to mc.Credentials
// at the RLock instant — RUnlock does not mutate the pointer).
//
// The Models slice is the caller's responsibility: each call site
// builds whatever variant reflects the proposed state to validate
// (AddModel appends the new model to a copy of mc.Models;
// UpdateModel / RemoveModel pass a copy of the post-mutate
// mc.Models; Save passes the captured current set).
func snapshotForValidation(models []ModelConfig, credentials *CredentialsConfig) *ModelsConfig {
	return &ModelsConfig{
		Models:      models,
		Credentials: credentials,
	}
}

// AddModel adds a new model configuration after validation.
func (mc *ModelsConfig) AddModel(model ModelConfig) error {
	// Validate the model
	if model.ID == "" {
		return ErrInvalidModelID
	}
	if model.Name == "" {
		return ErrInvalidModelName
	}

	// remove-external-upstream P1 (task 7, layer iii/iii): the
	// Internal=true WRITE-STAMP on the JSON backend, BEFORE validation
	// (ordering mandate — the snapshot validation below must see the
	// stamped value so the credential gates enforce on it).
	model.Internal = true

	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Check for duplicate ID
	for _, m := range mc.Models {
		if m.ID == model.ID {
			return ErrDuplicateModelID
		}
	}

	// Create a copy for validation
	proposed := append(append([]ModelConfig{}, mc.Models...), model)
	if err := snapshotForValidation(proposed, mc.Credentials).Validate(); err != nil {
		return err
	}

	// Add the model
	mc.Models = append(mc.Models, model)
	mc.rebuildClaimsLocked()
	return nil
}

// UpdateModel updates an existing model configuration after validation.
func (mc *ModelsConfig) UpdateModel(modelID string, model ModelConfig) error {
	// Validate the model
	if model.ID == "" {
		return ErrInvalidModelID
	}
	if model.Name == "" {
		return ErrInvalidModelName
	}

	// remove-external-upstream P1 (task 7, layer iii/iii): the
	// Internal=true WRITE-STAMP on the JSON backend, BEFORE validation
	// (ordering mandate — same as AddModel above).
	model.Internal = true

	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Find the target index first; bail on not-found or
	// ID-mismatch before doing any allocation. We must NOT
	// mutate mc.Models[i] yet — the validate-before-mutate
	// contract (Item-2 fix) requires that a failed Validate
	// leaves mc.Models byte-identical to its pre-call state.
	targetIdx := -1
	for i, m := range mc.Models {
		if m.ID == modelID {
			// Ensure the ID doesn't change
			if model.ID != modelID {
				return ErrCannotChangeModelID
			}
			targetIdx = i
			break
		}
	}

	if targetIdx == -1 {
		return ErrModelNotFound
	}

	// Validate the PROPOSED post-update state BEFORE mutating
	// mc.Models. The snapshot is a fresh copy of mc.Models
	// with the target slot overwritten by the proposed model
	// — no aliasing with the live slice. If Validate fails
	// (a per-model rule violation OR a sibling-row violation
	// the inline handler validation cannot see), the function
	// returns WITHOUT mutating mc.Models. mc.mu stays held
	// throughout — no TOCTOU window between Validate and the
	// commit below.
	//
	// Copy BOTH Models AND Credentials into the snapshot —
	// without the credentials mirror,
	// validateModelAgainstCredentials' "credential_id
	// references non-existent credential" check fires on every
	// internal model with a ref, blocking PUT updates for
	// every kind (chat / image-gen / decision) equally. The
	// symmetry with AddModel's pre-add snapshot is the
	// invariant.
	proposed := make([]ModelConfig, len(mc.Models))
	copy(proposed, mc.Models)
	proposed[targetIdx] = model
	if err := snapshotForValidation(proposed, mc.Credentials).Validate(); err != nil {
		return err
	}

	// Validation passed — commit the mutation in place.
	mc.Models[targetIdx] = model
	mc.rebuildClaimsLocked()
	return nil
}

// RemoveModel removes a model configuration by ID.
//
// load-balance-groups / Am27: refuses while the model ID is a
// member of any group (the error names the group) — the operator
// removes the member from the group first. Groups carry no
// credentials, so there is no credential-side scan.
func (mc *ModelsConfig) RemoveModel(modelID string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Am27 member-scan: block-at-delete (Q11 recommended default).
	for _, m := range mc.Models {
		if m.Kind != KindGroup {
			continue
		}
		for _, member := range m.Members {
			if member.ModelID == modelID {
				return fmt.Errorf("model '%s' is a member of group '%s' — remove it from the group before deleting the model", modelID, m.ID)
			}
		}
	}

	// Find and remove the model
	found := false
	for i, m := range mc.Models {
		if m.ID == modelID {
			mc.Models = append(mc.Models[:i], mc.Models[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		return ErrModelNotFound
	}

	// Validate after removal (check for dangling references).
	// Mirror the UpdateModel fix: copy BOTH Models AND Credentials
	// into the snapshot — without the credentials mirror, the
	// surviving internal model with a credential ref trips
	// validateModelAgainstCredentials' "credential_id
	// references non-existent credential" guard and the
	// semantically valid DELETE returns 404 spuriously.
	proposed := make([]ModelConfig, len(mc.Models))
	copy(proposed, mc.Models)
	if err := snapshotForValidation(proposed, mc.Credentials).Validate(); err != nil {
		return err
	}

	mc.rebuildClaimsLocked()

	return nil
}

// Validate validates the model configuration.
// Since fallback is now single-level (max 1 item), we only perform basic validation:
// - Model IDs must be non-empty
// - Fallback references must reference existing models
// - If internal is true, credential_id must reference an existing credential
func (mc *ModelsConfig) Validate() error {
	// Build set of valid model IDs
	modelIDs := make(map[string]bool)
	for _, model := range mc.Models {
		modelIDs[model.ID] = true
	}

	// Build set of valid credential IDs
	credentialIDs := make(map[string]bool)
	if mc.Credentials != nil {
		for _, cred := range mc.Credentials.GetCredentials() {
			credentialIDs[cred.ID] = true
		}
	}

	// Basic validation: check for empty IDs and valid fallback references
	for _, model := range mc.Models {
		if model.ID == "" {
			return ErrInvalidModelID
		}

		// load-balance-groups / Am23 routing (JSON twin): group
		// rows run the GROUP validators INSTEAD of the generic
		// internal/credential block below — that block's
		// "credential_id is required when internal is true" gate
		// rejects exactly the group-row shape (Internal=true,
		// Credentials=[]). The branch must precede it.
		if model.Kind == KindGroup {
			if err := ValidateKindRules(&model, ""); err != nil {
				return err
			}
			if err := ValidateGroupMembers(&model, mc.Models); err != nil {
				return err
			}
			continue
		}

		// Enforce max 1 fallback model
		if len(model.FallbackChain) > 1 {
			return fmt.Errorf("fallback chain is limited to maximum 1 fallback model")
		}

		// Fallback chain is now limited to max 1 item, so just validate references
		for _, fallbackID := range model.FallbackChain {
			if fallbackID != "" && !modelIDs[fallbackID] {
				// Unknown model reference - warn but allow for forward compatibility
				// This enables adding new models without updating all configs
			}
		}

		// Validate internal upstream configuration
		if model.Internal {
			// Phase 1: internal models must carry at least one credential ref.
			// Empty Credentials ⇒ keep the legacy error text verbatim so
			// downstream callers that grep the message keep working.
			if len(model.Credentials) == 0 {
				return fmt.Errorf("model %s: credential_id is required when internal is true", model.ID)
			}

			// Per-ref validation (Task 6 validation matrix).
			seen := make(map[string]bool, len(model.Credentials))
			var primaryCred *CredentialConfig
			for idx, ref := range model.Credentials {
				if ref.CredentialID == "" {
					return fmt.Errorf("model %s: credentials[%d].credential_id is empty", model.ID, idx)
				}
				if !credentialIDs[ref.CredentialID] {
					return fmt.Errorf("model %s: credential_id '%s' references non-existent credential", model.ID, ref.CredentialID)
				}
				if ref.Weight <= 0 {
					return fmt.Errorf("model %s: credentials[%d] (credential_id=%q): weight must be > 0, got %d", model.ID, idx, ref.CredentialID, ref.Weight)
				}
				if seen[ref.CredentialID] {
					return fmt.Errorf("model %s: duplicate credential_id '%s' in credentials list", model.ID, ref.CredentialID)
				}
				seen[ref.CredentialID] = true
			}
			if len(model.Credentials) > MaxCredentialRefs {
				return fmt.Errorf("model %s: credentials list exceeds max of %d refs", model.ID, MaxCredentialRefs)
			}

			// Provider-match invariant: every ref's credential must share
			// Credentials[0]'s provider (case-insensitive). Looks up via
			// mc.Credentials when available, else falls back to the
			// already-built credentialIDs set (best-effort: matches when
			// the credential row's Provider is the same — the database
			// Validate mirror builds the same lookup).
			if mc.Credentials != nil {
				primaryCred = mc.Credentials.GetCredential(model.Credentials[0].CredentialID)
			}
			if primaryCred != nil {
				primaryProvider := strings.ToLower(primaryCred.Provider)
				for idx, ref := range model.Credentials[1:] {
					refCred := mc.Credentials.GetCredential(ref.CredentialID)
					if refCred == nil {
						continue // already caught by the existence check above
					}
					if strings.ToLower(refCred.Provider) != primaryProvider {
						return fmt.Errorf("model %s: credentials[%d] (credential_id=%q) provider %q does not match primary provider %q",
							model.ID, idx+1, ref.CredentialID, refCred.Provider, primaryCred.Provider)
					}
				}
			}

			if model.InternalModel == "" {
				return fmt.Errorf("model %s: internal_model is required when internal is true", model.ID)
			}
		}
		// Note: non-internal models MAY carry Credentials — the
		// ultimate-external D3 provider probe reads Credentials[0] to
		// classify the upstream provider without injecting credentials.
		// (See ModelsConfig.Validate comment for the rationale on the
		// plan's "non-internal with creds ⇒ reject" rule deviation.)

		// Validate secondary upstream model
		if model.SecondaryUpstreamModel != "" {
			if !model.Internal {
				return fmt.Errorf("model %s: secondary_upstream_model requires internal to be true", model.ID)
			}
		}

		// Validate peak hour configuration
		if model.PeakHourEnabled {
			// Peak hours require internal upstream
			if !model.Internal {
				return fmt.Errorf("model %s: peak_hour_enabled requires internal to be true", model.ID)
			}

			// All peak hour fields must be provided
			if model.PeakHourStart == "" {
				return fmt.Errorf("model %s: peak_hour_start is required when peak_hour_enabled is true", model.ID)
			}
			if model.PeakHourEnd == "" {
				return fmt.Errorf("model %s: peak_hour_end is required when peak_hour_enabled is true", model.ID)
			}
			if model.PeakHourTimezone == "" {
				return fmt.Errorf("model %s: peak_hour_timezone is required when peak_hour_enabled is true", model.ID)
			}
			if model.PeakHourModel == "" {
				return fmt.Errorf("model %s: peak_hour_model is required when peak_hour_enabled is true", model.ID)
			}

			// Validate HH:MM format for start and end
			if err := ValidateTimeFormat(model.PeakHourStart); err != nil {
				return fmt.Errorf("model %s: invalid peak_hour_start: %w", model.ID, err)
			}
			if err := ValidateTimeFormat(model.PeakHourEnd); err != nil {
				return fmt.Errorf("model %s: invalid peak_hour_end: %w", model.ID, err)
			}

			// Validate UTC offset
			if err := ValidateUTCOffset(model.PeakHourTimezone); err != nil {
				return fmt.Errorf("model %s: invalid peak_hour_timezone: %w", model.ID, err)
			}

			// Reject same start and end times (would create empty or full-day window)
			if model.PeakHourStart == model.PeakHourEnd {
				return fmt.Errorf("model %s: peak_hour_start and peak_hour_end cannot be the same", model.ID)
			}
		}

		// ImgGen Models commission / BE-D2 + Architect Amendment 2:
		// call the shared kind-rule validator. For image-gen rows the
		// primary-credential provider is read from mc.Credentials (best
		// effort, may be nil when the credential set isn't loaded yet).
		var primaryProvider string
		if mc.Credentials != nil && len(model.Credentials) > 0 {
			if pcred := mc.Credentials.GetCredential(model.Credentials[0].CredentialID); pcred != nil {
				primaryProvider = strings.ToLower(pcred.Provider)
			}
		}
		if err := ValidateKindRules(&model, primaryProvider); err != nil {
			return err
		}
	}

	// Validate all credentials
	if mc.Credentials != nil {
		for _, cred := range mc.Credentials.GetCredentials() {
			if err := cred.Validate(); err != nil {
				return err
			}
		}
	}

	return nil
}

// IsInternal returns true if the model uses internal upstream
func (m *ModelConfig) IsInternal() bool {
	return m.Internal
}

// GetInternalConfig returns the internal upstream configuration.
// Note: This returns only the model-level config. Use ModelsConfig.ResolveInternalConfig()
// to get the full config including resolved credential.
func (m *ModelConfig) GetInternalConfig() (credentialID, provider, baseURL, model string, ok bool) {
	if !m.Internal {
		return "", "", "", "", false
	}
	return m.PrimaryCredentialID(), "", m.InternalBaseURL, m.InternalModel, true
}

// ResolveInternalConfig resolves the full internal upstream configuration including
// credentials. It returns the provider, apiKey, baseURL, and model name.
// The provider comes from the credential. The baseURL is taken from the model if specified, otherwise from the credential.
//
// Legacy single-credential resolution — always uses Credentials[0].
// The Phase 2 LB engine (pkg/credentiallb) introduces
// ResolveInternalConfigWithAffinity for multi-credential models with
// conversation-sticky affinity; for Phase 1 this stays as the
// single-credential fast path (byte-identical to pre-change behavior).
func (mc *ModelsConfig) ResolveInternalConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	var modelConfig *ModelConfig
	for _, m := range mc.Models {
		if m.ID == modelID {
			copy := m
			modelConfig = &copy
			break
		}
	}

	if modelConfig == nil || !modelConfig.Internal {
		return "", "", "", "", false
	}

	// Get credential via the primary (single-credential fast path).
	primaryCredentialID := modelConfig.PrimaryCredentialID()
	if primaryCredentialID == "" {
		return "", "", "", "", false
	}

	// Get credential
	if mc.Credentials == nil {
		return "", "", "", "", false
	}

	cred := mc.Credentials.GetCredential(primaryCredentialID)
	if cred == nil {
		return "", "", "", "", false
	}

	// Provider comes from credential only
	provider = cred.Provider

	// Resolve baseURL: model override > credential
	baseURL = modelConfig.InternalBaseURL
	if baseURL == "" {
		baseURL = cred.BaseURL
	}

	// Determine actual model: check peak hour first
	actualModel := modelConfig.InternalModel

	if peakModel := modelConfig.ResolvePeakHourModel(time.Now()); peakModel != "" {
		log.Printf("[PEAK-HOUR] peak hour active for model %s: using %s instead of %s",
			modelConfig.ID, peakModel, modelConfig.InternalModel)
		actualModel = peakModel
	}

	return provider, cred.APIKey, baseURL, actualModel, true
}

// ResolveInternalConfigWithAffinity (Phase 3 / Task 16 + Tasks 4/8/9 /
// ModelsConfigInterface seam). The JSON-file-backed ModelsConfig has no
// engine wired — the phase-2 ModelsManager owns the engine — so this
// struct degrades to the legacy single-credential resolution (the
// phase2 E-3 fast path): ResolveInternalConfig returns a struct with
// .CredentialID == primaryID + .NewlyBound=false + .ok=resolves the
// same shape as the single-cred path inside ModelsManager.
//
// Single-credential behavior is byte-identical — no engine call, no
// binding write, no engine overhead.
func (mc *ModelsConfig) ResolveInternalConfigWithAffinity(modelID, conversationKey string) (ResolvedCredential, bool) {
	provider, apiKey, baseURL, internalModel, ok := mc.ResolveInternalConfig(modelID)
	if !ok {
		return ResolvedCredential{}, false
	}
	// Derive CredentialID for the legacy single-credential path.
	mc.mu.RLock()
	var primaryID string
	for _, m := range mc.Models {
		if m.ID == modelID {
			primaryID = m.PrimaryCredentialID()
			break
		}
	}
	mc.mu.RUnlock()
	return ResolvedCredential{
		Provider:      provider,
		APIKey:        apiKey,
		BaseURL:       baseURL,
		InternalModel: internalModel,
		CredentialID:  primaryID,
		// NewlyBound is always false for the JSON-file backend (no
		// engine wired; this is the E-3 single-credential fast path).
		NewlyBound: false,
	}, true
}

// GetEnabledModels returns only the enabled model configurations.
func (mc *ModelsConfig) GetEnabledModels() []ModelConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make([]ModelConfig, 0)
	for _, model := range mc.Models {
		if model.Enabled {
			result = append(result, model)
		}
	}
	return result
}

// GetModels returns all model configurations.
func (mc *ModelsConfig) GetModels() []ModelConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make([]ModelConfig, len(mc.Models))
	copy(result, mc.Models)
	return result
}

// LoadWithContext loads the models configuration with context for deadline/cancellation support.
func (mc *ModelsConfig) LoadWithContext(ctx context.Context, filePath string) error {
	errCh := make(chan error, 1)

	go func() {
		errCh <- mc.Load(filePath)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// SaveWithContext saves the models configuration with context for deadline/cancellation support.
func (mc *ModelsConfig) SaveWithContext(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		errCh <- mc.Save()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

// Model errors
var (
	ErrInvalidModelID      = &ConfigError{"invalid model ID: cannot be empty"}
	ErrInvalidModelName    = &ConfigError{"invalid model name: cannot be empty"}
	ErrDuplicateModelID    = &ConfigError{"duplicate model ID"}
	ErrModelNotFound       = &ConfigError{"model not found"}
	ErrCannotChangeModelID = &ConfigError{"cannot change model ID"}
)

// ConfigError represents a configuration error.
type ConfigError struct {
	msg string
}

func (e *ConfigError) Error() string {
	return e.msg
}

// GetCredential returns the credential configuration for a given ID.
// Returns nil if the credential is not found.
func (mc *ModelsConfig) GetCredential(id string) *CredentialConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	if mc.Credentials == nil {
		return nil
	}
	return mc.Credentials.GetCredential(id)
}

// GetCredentials returns all credential configurations.
func (mc *ModelsConfig) GetCredentials() []CredentialConfig {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	if mc.Credentials == nil {
		return []CredentialConfig{}
	}
	return mc.Credentials.GetCredentials()
}

// AddCredential adds a new credential configuration after validation.
func (mc *ModelsConfig) AddCredential(cred CredentialConfig) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.Credentials == nil {
		mc.Credentials = NewCredentialsConfig()
	}
	return mc.Credentials.AddCredential(cred)
}

// UpdateCredential updates an existing credential configuration after validation.
func (mc *ModelsConfig) UpdateCredential(id string, cred CredentialConfig) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.Credentials == nil {
		return ErrCredentialNotFound
	}
	return mc.Credentials.UpdateCredential(id, cred)
}

// RemoveCredential removes a credential configuration by ID.
// Returns an error if the credential is in use by any model.
//
// In-use guard scans every model's Credentials slice (the new
// ordered, weighted list). When a single-credential model with the
// legacy CredentialID field would have been caught by `model.CredentialID
// == id`, the new guard catches it on `Credentials[i].CredentialID == id`
// (the primary ref). When the field-drop is in place, only the slice
// matters; both shapes remain covered for the deprecation window.
func (mc *ModelsConfig) RemoveCredential(id string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if mc.Credentials == nil {
		return ErrCredentialNotFound
	}

	// Check if credential is in use by any model
	for _, model := range mc.Models {
		for _, ref := range model.Credentials {
			if ref.CredentialID == id {
				return fmt.Errorf("credential '%s' is in use by model '%s': %w", id, model.ID, ErrCredentialInUse)
			}
		}
	}

	return mc.Credentials.RemoveCredential(id)
}
