package models

// Copy-model commission / BE half — unit tests for the copy builder.
//
// The builder owns the fidelity contract (pkg/models/copy.go), so
// every rule is asserted here WITHOUT an HTTP server: the route only
// maps the builder's sentinels onto status codes.

import (
	"errors"
	"reflect"
	"testing"
)

// copySourceFixture is a fully-populated source row: every
// behaviour-defining field carries a non-zero value so a verbatim
// copy is distinguishable from a zero-valued row.
func copySourceFixture() *ModelConfig {
	return &ModelConfig{
		ID:                           "source-model",
		Name:                         "Source Model",
		Enabled:                      true,
		FallbackChain:                []string{"other-model"},
		TruncateParams:               []string{"max_completion_tokens", "store"},
		Kind:                         KindChat,
		Internal:                     true,
		InternalBaseURL:              "https://upstream.example/v1",
		InternalModel:                "glm-5.0",
		Credentials:                  TestRefsWeighted(CredentialRef{CredentialID: "cred-a", Weight: 2}, CredentialRef{CredentialID: "cred-b", Weight: 1}),
		ReleaseStreamChunkDeadline:   Duration(90_000_000_000),
		PeakHourEnabled:              true,
		PeakHourStart:                "09:00",
		PeakHourEnd:                  "18:00",
		PeakHourTimezone:             "+7",
		PeakHourModel:                "glm-4-flash",
		SecondaryUpstreamModel:       "glm-4-flash",
		// A chat row never carries members, but the field must stay
		// empty on the copy (membership lives on the group row).
		Members: []GroupMember{},
		// A chat row normally has no shadow claim; the field must be
		// CLEARED on the copy all the same (a verbatim copy would
		// double-claim the shadowed id → G8).
		ShadowModelID: "shadowed-model",
	}
}

// TestBuildModelCopy_CopiesBehaviourFieldsVerbatim asserts every
// behaviour-defining field is carried over unchanged while the
// identity fields are regenerated.
func TestBuildModelCopy_CopiesBehaviourFieldsVerbatim(t *testing.T) {
	src := copySourceFixture()
	cp, err := BuildModelCopy(src, "copy-model")
	if err != nil {
		t.Fatalf("BuildModelCopy returned error: %v", err)
	}

	// REGENERATED identity fields.
	if cp.ID != "copy-model" {
		t.Errorf("ID = %q, want %q", cp.ID, "copy-model")
	}
	if cp.Name != "copy-model" {
		t.Errorf("Name = %q, want %q (the name is the new id — unique by construction)", cp.Name, "copy-model")
	}
	if cp.Enabled {
		t.Error("Enabled = true, want false (the copy is a dormant clone)")
	}

	// COPIED VERBATIM behaviour fields.
	if !reflect.DeepEqual(cp.TruncateParams, src.TruncateParams) {
		t.Errorf("TruncateParams = %v, want %v", cp.TruncateParams, src.TruncateParams)
	}
	if cp.Internal != src.Internal {
		t.Errorf("Internal = %v, want %v", cp.Internal, src.Internal)
	}
	if cp.InternalBaseURL != src.InternalBaseURL {
		t.Errorf("InternalBaseURL = %q, want %q", cp.InternalBaseURL, src.InternalBaseURL)
	}
	if cp.InternalModel != src.InternalModel {
		t.Errorf("InternalModel = %q, want %q", cp.InternalModel, src.InternalModel)
	}
	if !reflect.DeepEqual(cp.Credentials, src.Credentials) {
		t.Errorf("Credentials = %v, want %v", cp.Credentials, src.Credentials)
	}
	if cp.ReleaseStreamChunkDeadline != src.ReleaseStreamChunkDeadline {
		t.Errorf("ReleaseStreamChunkDeadline = %v, want %v", cp.ReleaseStreamChunkDeadline, src.ReleaseStreamChunkDeadline)
	}
	if cp.PeakHourEnabled != src.PeakHourEnabled ||
		cp.PeakHourStart != src.PeakHourStart ||
		cp.PeakHourEnd != src.PeakHourEnd ||
		cp.PeakHourTimezone != src.PeakHourTimezone ||
		cp.PeakHourModel != src.PeakHourModel {
		t.Errorf("peak-hour fields not copied verbatim: %+v", cp)
	}
	if cp.SecondaryUpstreamModel != src.SecondaryUpstreamModel {
		t.Errorf("SecondaryUpstreamModel = %q, want %q", cp.SecondaryUpstreamModel, src.SecondaryUpstreamModel)
	}
	if cp.Kind != src.Kind {
		t.Errorf("Kind = %q, want %q", cp.Kind, src.Kind)
	}

	// NEVER TOUCHED: the shadow claim is cleared, members are empty.
	if cp.ShadowModelID != "" {
		t.Errorf("ShadowModelID = %q, want empty (a verbatim copy would double-claim the shadow id)", cp.ShadowModelID)
	}
	if len(cp.Members) != 0 {
		t.Errorf("Members = %v, want empty (membership lives on the group row)", cp.Members)
	}
}

// TestBuildModelCopy_CredentialRefsSharedNoSecret asserts the copy
// carries REFS only (the credential rows are shared, never duplicated)
// and that the copy shares no slice with the source row.
func TestBuildModelCopy_CredentialRefsSharedNoSecret(t *testing.T) {
	src := copySourceFixture()
	cp, err := BuildModelCopy(src, "copy-model")
	if err != nil {
		t.Fatalf("BuildModelCopy returned error: %v", err)
	}

	if len(cp.Credentials) != len(src.Credentials) {
		t.Fatalf("Credentials length = %d, want %d", len(cp.Credentials), len(src.Credentials))
	}
	for i, ref := range cp.Credentials {
		if ref.CredentialID != src.Credentials[i].CredentialID {
			t.Errorf("Credentials[%d].CredentialID = %q, want %q", i, ref.CredentialID, src.Credentials[i].CredentialID)
		}
		if ref.Weight != src.Credentials[i].Weight {
			t.Errorf("Credentials[%d].Weight = %d, want %d", i, ref.Weight, src.Credentials[i].Weight)
		}
		if ref.Position != src.Credentials[i].Position {
			t.Errorf("Credentials[%d].Position = %d, want %d", i, ref.Position, src.Credentials[i].Position)
		}
	}

	// No slice aliasing: mutating the copy must not reach the source.
	cp.Credentials[0].CredentialID = "mutated"
	cp.TruncateParams[0] = "mutated"
	if src.Credentials[0].CredentialID != "cred-a" {
		t.Errorf("source credential ref was aliased: %q", src.Credentials[0].CredentialID)
	}
	if src.TruncateParams[0] != "max_completion_tokens" {
		t.Errorf("source truncate params were aliased: %q", src.TruncateParams[0])
	}
}

// TestBuildModelCopy_FallbackChainDropsSourceEntry asserts the ONE
// documented deviation from a verbatim copy: a fallback entry equal to
// the source id is dropped (a clone must not fall back into its own
// source) while every other entry is carried over.
func TestBuildModelCopy_FallbackChainDropsSourceEntry(t *testing.T) {
	src := copySourceFixture()
	src.FallbackChain = []string{"source-model", "other-model", "third-model"}

	cp, err := BuildModelCopy(src, "copy-model")
	if err != nil {
		t.Fatalf("BuildModelCopy returned error: %v", err)
	}

	want := []string{"other-model", "third-model"}
	if !reflect.DeepEqual(cp.FallbackChain, want) {
		t.Errorf("FallbackChain = %v, want %v (the source entry must be dropped)", cp.FallbackChain, want)
	}
}

// TestBuildModelCopy_GroupSourceRefused asserts a group row is not
// copyable (a group's identity IS its membership).
func TestBuildModelCopy_GroupSourceRefused(t *testing.T) {
	src := copySourceFixture()
	src.Kind = KindGroup
	src.Members = []GroupMember{{ModelID: "member-a", Weight: 1}}

	cp, err := BuildModelCopy(src, "copy-model")
	if err == nil {
		t.Fatal("BuildModelCopy returned no error for a group source")
	}
	if !errors.Is(err, ErrCopyGroupNotCopyable) {
		t.Errorf("error = %v, want ErrCopyGroupNotCopyable", err)
	}
	if err.Error() != "group models cannot be copied" {
		t.Errorf("error message = %q, want %q", err.Error(), "group models cannot be copied")
	}
	if cp != nil {
		t.Errorf("copy = %+v, want nil on refusal", cp)
	}
}

// TestBuildModelCopy_RejectsInvalidNewID asserts the new-id
// validation rules (empty, "/", whitespace, same-as-source).
func TestBuildModelCopy_RejectsInvalidNewID(t *testing.T) {
	tests := []struct {
		name    string
		newID   string
		wantErr error
	}{
		{name: "empty", newID: "", wantErr: ErrCopyEmptyNewID},
		{name: "slash", newID: "a/b", wantErr: ErrCopyInvalidNewID},
		{name: "leading slash", newID: "/abs", wantErr: ErrCopyInvalidNewID},
		{name: "space", newID: "a b", wantErr: ErrCopyInvalidNewID},
		{name: "tab", newID: "a\tb", wantErr: ErrCopyInvalidNewID},
		{name: "newline", newID: "a\nb", wantErr: ErrCopyInvalidNewID},
		{name: "same as source", newID: "source-model", wantErr: ErrCopySameID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := copySourceFixture()
			cp, err := BuildModelCopy(src, tt.newID)
			if err == nil {
				t.Fatalf("BuildModelCopy(%q) returned no error", tt.newID)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
			if cp != nil {
				t.Errorf("copy = %+v, want nil on rejection", cp)
			}
		})
	}
}

// TestBuildModelCopy_NilSource asserts a nil source is refused (the
// route maps a missing source to 404 before it ever reaches here, so
// this is the defensive floor).
func TestBuildModelCopy_NilSource(t *testing.T) {
	cp, err := BuildModelCopy(nil, "copy-model")
	if err == nil {
		t.Fatal("BuildModelCopy(nil, ...) returned no error")
	}
	if !errors.Is(err, ErrModelNotFound) {
		t.Errorf("error = %v, want ErrModelNotFound", err)
	}
	if cp != nil {
		t.Errorf("copy = %+v, want nil", cp)
	}
}

// TestValidateCopyID_AcceptsPlainID asserts the minimal id rule
// accepts an ordinary id (the schema carries NO other id format
// rule, so the copy route must not invent a stricter grammar).
func TestValidateCopyID_AcceptsPlainID(t *testing.T) {
	for _, id := range []string{"model-1", "a.b_c:d", "ünïcode", "with-dash"} {
		if err := ValidateCopyID(id); err != nil {
			t.Errorf("ValidateCopyID(%q) = %v, want nil", id, err)
		}
	}
}
