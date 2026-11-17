package models

import (
	"fmt"
	"strings"
)

// MaxCredentialGroupMembers mirrors MaxGroupMembers (group.go) and caps
// the number of member credential IDs a group may hold.
const MaxCredentialGroupMembers = 16

// CredentialGroupConfig is a named, same-provider bundle of existing
// credentials. Management-only entity; flattened at model resolution.
type CredentialGroupConfig struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Provider  string   `json:"provider"`
	Members   []string `json:"members"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// Sentinels (all *ConfigError so errors.Is works).
var (
	ErrCredentialGroupNotFound         = &ConfigError{"credential group not found"}
	ErrCredentialGroupDuplicate        = &ConfigError{"credential group already exists"}
	ErrCredentialGroupInUse            = &ConfigError{"credential group is in use by one or more models"}
	ErrCredentialGroupMemberNotFound   = &ConfigError{"credential group member not found"}
	ErrCredentialGroupProviderMismatch = &ConfigError{"credential group member provider mismatch"}
	ErrCredentialGroupMemberDuplicate  = &ConfigError{"duplicate credential group member"}
	ErrCredentialGroupMemberEmpty      = &ConfigError{"credential group member id is empty"}
)

// ValidateCredentialGroup validates a group against the collection rules
// G-1..G-9. credLookup may be nil (then member-existence checks are
// skipped) or a function/map/slice normalised internally.
func ValidateCredentialGroup(g CredentialGroupConfig, credLookup interface{}) error {
	if g.ID == "" {
		return fmt.Errorf("credential group id cannot be empty")
	}
	if g.Name == "" {
		return fmt.Errorf("credential group name cannot be empty")
	}
	// G-3 provider
	ok := false
	for _, p := range ValidProviderNames() {
		if strings.EqualFold(p, g.Provider) {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("credential group provider %q is not valid", g.Provider)
	}
	if len(g.Members) > MaxCredentialGroupMembers {
		return fmt.Errorf("credential group members exceed max of %d", MaxCredentialGroupMembers)
	}
	// duplicates + empty
	seen := make(map[string]bool)
	for _, m := range g.Members {
		if m == "" {
			return ErrCredentialGroupMemberEmpty
		}
		if seen[m] {
			return ErrCredentialGroupMemberDuplicate
		}
		seen[m] = true
	}
	// credLookup normalise
	lookup := toCredLookup(credLookup)
	if lookup != nil {
		for _, m := range g.Members {
			c := lookup(m)
			if c == nil {
				return ErrCredentialGroupMemberNotFound
			}
			if !strings.EqualFold(c.Provider, g.Provider) {
				return ErrCredentialGroupProviderMismatch
			}
		}
	}
	return nil
}

// ValidateCredentialRefs validates model credential refs (R-1..R-7).
// groups/creds may be slices or maps. kind is optional (variadic);
// empty defaults to "chat" (groups allowed).
func ValidateCredentialRefs(refs []CredentialRef, groups interface{}, creds interface{}, kind ...string) error {
	gm := toGroupMap(groups)
	cm := toCredMap(creds)

	k := ""
	if len(kind) > 0 {
		k = kind[0]
	}
	// R-5: the non-chat kinds forbid group refs. The kinds are the
	// package's OWN constants — a literal here can drift from
	// config.go's constants and silently STOP firing (the "imggen"
	// literal matched NO kind in this repo; the image-generation kind
	// is KindImageGen == "image-gen").
	nonChat := k == KindDecision || k == KindImageGen || k == KindGroup

	// R-1 exact one of direct/group
	for idx, ref := range refs {
		if ref.CredentialID != "" && ref.GroupID != "" {
			return fmt.Errorf("credentials[%d]: both credential_id and group_id set", idx)
		}
		if ref.CredentialID == "" && ref.GroupID == "" {
			return fmt.Errorf("credentials[%d]: neither credential_id nor group_id set", idx)
		}
	}

	// primary provider
	primaryProvider := ""
	if len(refs) > 0 {
		if refs[0].IsGroupRef() {
			if g, ok := gm[refs[0].GroupID]; ok {
				primaryProvider = strings.ToLower(g.Provider)
			}
		} else {
			if c, ok := cm[refs[0].CredentialID]; ok {
				primaryProvider = strings.ToLower(c.Provider)
			}
		}
	}

	expanded := 0
	for idx, ref := range refs {
		if ref.Weight <= 0 {
			id := ref.CredentialID
			if ref.IsGroupRef() {
				id = ref.GroupID
			}
			return fmt.Errorf("credentials[%d] (%s): weight must be > 0, got %d", idx, id, ref.Weight)
		}
		if ref.IsGroupRef() {
			if nonChat {
				return fmt.Errorf("credentials[%d]: group refs not allowed for kind %q", idx, k)
			}
			g, ok := gm[ref.GroupID]
			if !ok {
				return fmt.Errorf("credentials[%d]: group %q not found", idx, ref.GroupID)
			}
			// provider match
			if primaryProvider != "" && strings.ToLower(g.Provider) != primaryProvider {
				return ErrCredentialGroupProviderMismatch
			}
			if len(g.Members) == 0 {
				// empty group contributes nothing; allowed (legacy degrade)
				continue
			}
			expanded += len(g.Members)
		} else {
			c, ok := cm[ref.CredentialID]
			if !ok {
				return fmt.Errorf("credentials[%d]: credential %q not found", idx, ref.CredentialID)
			}
			if primaryProvider != "" && strings.ToLower(c.Provider) != primaryProvider {
				return fmt.Errorf("credentials[%d] (credential_id=%q) provider %q does not match primary provider", idx, ref.CredentialID, c.Provider)
			}
			expanded++
		}
	}
	// R-4 expanded <= MaxCredentialRefs (16). Empty expanded (all empty groups) allowed.
	if expanded > MaxCredentialRefs {
		return fmt.Errorf("credentials expanded length %d exceeds max of %d", expanded, MaxCredentialRefs)
	}
	return nil
}

// --- helpers ---

func toCredLookup(v interface{}) func(string) *CredentialConfig {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case func(string) *CredentialConfig:
		return t
	case func(string) (*CredentialConfig, bool):
		return func(id string) *CredentialConfig {
			c, _ := t(id)
			return c
		}
	case map[string]CredentialConfig:
		return func(id string) *CredentialConfig {
			if c, ok := t[id]; ok {
				cc := c
				return &cc
			}
			return nil
		}
	case map[string]*CredentialConfig:
		return func(id string) *CredentialConfig { return t[id] }
	case []CredentialConfig:
		m := make(map[string]CredentialConfig)
		for _, c := range t {
			m[c.ID] = c
		}
		return func(id string) *CredentialConfig {
			if c, ok := m[id]; ok {
				cc := c
				return &cc
			}
			return nil
		}
	}
	return nil
}

func toGroupMap(v interface{}) map[string]CredentialGroupConfig {
	out := make(map[string]CredentialGroupConfig)
	if v == nil {
		return out
	}
	switch t := v.(type) {
	case map[string]CredentialGroupConfig:
		for k, val := range t {
			out[k] = val
		}
	case map[string]*CredentialGroupConfig:
		for k, val := range t {
			if val != nil {
				out[k] = *val
			}
		}
	case []CredentialGroupConfig:
		for _, g := range t {
			out[g.ID] = g
		}
	}
	return out
}

func toCredMap(v interface{}) map[string]CredentialConfig {
	out := make(map[string]CredentialConfig)
	if v == nil {
		return out
	}
	switch t := v.(type) {
	case map[string]CredentialConfig:
		for k, val := range t {
			out[k] = val
		}
	case map[string]*CredentialConfig:
		for k, val := range t {
			if val != nil {
				out[k] = *val
			}
		}
	case []CredentialConfig:
		for _, c := range t {
			out[c.ID] = c
		}
	}
	return out
}
