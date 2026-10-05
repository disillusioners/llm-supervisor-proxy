package models

// ImgGen Models commission / T1.1.2 — table-driven ValidateKindRules
// + ModelsManager.AddModel enforcement tests (Architect Amendment 2
// split-brain fix). The shared ValidateKindRules helper is called
// from BOTH ModelsConfig.Validate AND
// ModelsManager.validateModelAgainstCredentials so the
// production /fe/api/models POST/PUT write path enforces the
// same rules as the JSON-file bulk path.

import (
	"strings"
	"testing"
)

func TestValidateKindRules_Empty_DefaultsToChat(t *testing.T) {
	m := &ModelConfig{}
	if err := ValidateKindRules(m, "minimax"); err != nil {
		t.Errorf("empty kind should default to chat, got: %v", err)
	}
	if err := ValidateKindRules(m, ""); err != nil {
		t.Errorf("empty kind + empty provider should default to chat, got: %v", err)
	}
}

func TestValidateKindRules_Chat_AlwaysValid(t *testing.T) {
	m := &ModelConfig{Kind: KindChat}
	if err := ValidateKindRules(m, "openai"); err != nil {
		t.Errorf("chat kind should always be valid, got: %v", err)
	}
}

func TestValidateKindRules_UnknownKind_Rejected(t *testing.T) {
	m := &ModelConfig{ID: "x", Kind: "image_gen"}
	err := ValidateKindRules(m, "minimax")
	if err == nil {
		t.Fatal("expected unknown kind to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown kind") {
		t.Errorf("error message = %q, want it to mention 'unknown kind'", err.Error())
	}
}

func TestValidateKindRules_ImageGen_AllRules(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(m *ModelConfig)
		provider   string
		wantErrSub string
	}{
		{
			name: "valid image-gen",
			mutate: func(m *ModelConfig) {
				m.Internal = true
				m.InternalModel = "image-01"
				m.Credentials = []CredentialRef{{CredentialID: "c", Weight: 1, Position: 0}}
			},
			provider:   "minimax",
			wantErrSub: "",
		},
		{
			name: "image-gen requires internal",
			mutate: func(m *ModelConfig) {
				m.Internal = false
				m.InternalModel = "image-01"
				m.Credentials = []CredentialRef{{CredentialID: "c", Weight: 1, Position: 0}}
			},
			provider:   "minimax",
			wantErrSub: "requires internal to be true",
		},
		{
			name: "image-gen requires internal_model",
			mutate: func(m *ModelConfig) {
				m.Internal = true
				m.Credentials = []CredentialRef{{CredentialID: "c", Weight: 1, Position: 0}}
			},
			provider:   "minimax",
			wantErrSub: "internal_model",
		},
		{
			name: "image-gen requires credentials",
			mutate: func(m *ModelConfig) {
				m.Internal = true
				m.InternalModel = "image-01"
			},
			provider:   "minimax",
			wantErrSub: "at least one credential",
		},
		{
			name: "image-gen requires minimax provider",
			mutate: func(m *ModelConfig) {
				m.Internal = true
				m.InternalModel = "image-01"
				m.Credentials = []CredentialRef{{CredentialID: "c", Weight: 1, Position: 0}}
			},
			provider:   "openai",
			wantErrSub: "minimax",
		},
		{
			name: "image-gen rejects fallback_chain",
			mutate: func(m *ModelConfig) {
				m.Internal = true
				m.InternalModel = "image-01"
				m.Credentials = []CredentialRef{{CredentialID: "c", Weight: 1, Position: 0}}
				m.FallbackChain = []string{"other"}
			},
			provider:   "minimax",
			wantErrSub: "fallback_chain",
		},
		{
			name: "image-gen rejects secondary_upstream_model",
			mutate: func(m *ModelConfig) {
				m.Internal = true
				m.InternalModel = "image-01"
				m.Credentials = []CredentialRef{{CredentialID: "c", Weight: 1, Position: 0}}
				m.SecondaryUpstreamModel = "x"
			},
			provider:   "minimax",
			wantErrSub: "secondary_upstream_model",
		},
		{
			name: "image-gen rejects peak_hour_enabled",
			mutate: func(m *ModelConfig) {
				m.Internal = true
				m.InternalModel = "image-01"
				m.Credentials = []CredentialRef{{CredentialID: "c", Weight: 1, Position: 0}}
				m.PeakHourEnabled = true
			},
			provider:   "minimax",
			wantErrSub: "peak_hour_enabled",
		},
		{
			name: "image-gen rejects release_stream_chunk_deadline",
			mutate: func(m *ModelConfig) {
				m.Internal = true
				m.InternalModel = "image-01"
				m.Credentials = []CredentialRef{{CredentialID: "c", Weight: 1, Position: 0}}
				m.ReleaseStreamChunkDeadline = Duration(60 * 1e9)
			},
			provider:   "minimax",
			wantErrSub: "release_stream_chunk_deadline",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &ModelConfig{ID: "img", Name: "Img", Kind: KindImageGen}
			tc.mutate(m)
			err := ValidateKindRules(m, tc.provider)
			if tc.wantErrSub == "" {
				if err != nil {
					t.Errorf("expected valid, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

func TestIsImageGen(t *testing.T) {
	tests := []struct {
		kind string
		want bool
	}{
		{"", false},
		{KindChat, false},
		{KindImageGen, true},
		{"image_gen", false}, // typo - not image-gen
	}
	for _, tc := range tests {
		m := &ModelConfig{Kind: tc.kind}
		if got := m.IsImageGen(); got != tc.want {
			t.Errorf("IsImageGen() for kind=%q = %v, want %v", tc.kind, got, tc.want)
		}
	}
}
