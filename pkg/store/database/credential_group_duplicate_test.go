package database

// credential-groups commission — the duplicate-group-id sentinel.
//
// A-9: "Status mapping uses errors.Is sentinels, not the legacy
// strings.Contains(err.Error(), 'in use') string match". The duplicate
// group-id rejection previously returned a PLAIN error, forcing the API
// layer to string-match the message to answer 400. This test proves the
// store returns models.ErrCredentialGroupDuplicate (WRAPPED, so the
// context-bearing message survives) and that the FIRST add of an id
// still succeeds.

import (
	"errors"
	"strings"
	"testing"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// TestCredentialGroup_Add_DuplicateIDReturnsSentinel proves the
// duplicate group-id rejection is identity-mappable: the API layer
// answers 400 via errors.Is(err, models.ErrCredentialGroupDuplicate),
// never by matching the error TEXT.
func TestCredentialGroup_Add_DuplicateIDReturnsSentinel(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	group := models.CredentialGroupConfig{
		ID:       "g-dup",
		Name:     "OpenAI pool",
		Provider: "openai",
		Members:  []string{"cred-A"},
	}

	// FIRST add succeeds — the sentinel must not fire on a fresh id.
	if err := mgr.AddCredentialGroup(group); err != nil {
		t.Fatalf("AddCredentialGroup(first) err = %v, want nil", err)
	}

	// SECOND add of the same id is rejected WITH the sentinel.
	err := mgr.AddCredentialGroup(group)
	if err == nil {
		t.Fatal("AddCredentialGroup(duplicate) err = nil, want a rejection")
	}
	if !errors.Is(err, models.ErrCredentialGroupDuplicate) {
		t.Fatalf("AddCredentialGroup(duplicate) err = %v, want errors.Is(err, models.ErrCredentialGroupDuplicate) == true", err)
	}

	// The sentinel is WRAPPED, not replaced: the message still names the
	// offending id so the operator can act on it.
	if !containsAll(err.Error(), "g-dup", "already exists") {
		t.Fatalf("AddCredentialGroup(duplicate) message = %q, want it to name the id and the reason", err.Error())
	}

	// The duplicate add did NOT overwrite the original group.
	got := mgr.GetCredentialGroup("g-dup")
	if got == nil {
		t.Fatal("GetCredentialGroup(g-dup) = nil, want the group created by the FIRST add")
	}
	if got.Name != "OpenAI pool" {
		t.Fatalf("GetCredentialGroup(g-dup).Name = %q, want %q", got.Name, "OpenAI pool")
	}
	if len(got.Members) != 1 || got.Members[0] != "cred-A" {
		t.Fatalf("GetCredentialGroup(g-dup).Members = %v, want [cred-A]", got.Members)
	}
}

// containsAll reports whether s contains every one of the substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
