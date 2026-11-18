package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
)

// Token prefix for easy identification
const TokenPrefix = "sk-"

// Token length (excluding prefix) in bytes - results in 64 hex chars
const TokenLength = 32

var (
	ErrInvalidTokenFormat = errors.New("invalid token format")
	ErrTokenExpired       = errors.New("token has expired")
	ErrTokenNotFound      = errors.New("token not found")
)

// AuthToken represents a stored API token.
type AuthToken struct {
	ID        string
	Name      string
	TokenHash string
	ExpiresAt *time.Time
	CreatedAt time.Time
	CreatedBy string

	// AllowedModels is the MODEL-axis grant. nil = ABSENT (no list on
	// this axis); non-nil, possibly empty = PRESENT ([] grants nothing).
	AllowedModels []string
	// AllowedGroups is the GROUP-axis grant (ids of models rows with
	// kind='group'). Same nil-vs-empty rule.
	AllowedGroups []string

	// ModelsInvalid / GroupsInvalid are the fail-closed flags: the
	// column existed but could not be read/parsed. They live ON THE
	// STRUCT because the token cache caches the *AuthToken
	// (pkg/modelscache/tokens.go:117 storePositive) — a separately
	// returned flag would be dropped at the cache boundary.
	// Either flag true => the token is DENY-ALL (never wildcard).
	ModelsInvalid bool
	GroupsInvalid bool
}

// ScanGrant parses ONE grant column into its tri-state.
//
//	raw == nil                                   -> (nil,  present=false, invalid=false)  ABSENT
//	unknown raw type (not string / []byte)        -> (nil,  present=false, invalid=true )  INVALID   [old :52-55]
//	"" / whitespace-only TEXT                     -> (nil,  present=false, invalid=true )  INVALID   (fail closed — see below)
//	valid JSON array (incl. "[]")                 -> (ids,  present=true,  invalid=false)  PRESENT
//	malformed JSON / non-array JSON literal       -> (nil,  present=false, invalid=true )  INVALID   [old :62-66]
//
// BOTH historical fail-open branches land in INVALID (plan §2.7, ADR D10):
// a malformed JSON payload AND an unknown driver raw type. Collapsing either
// into `[]string{}` (the pre-change behaviour) made a corrupt row
// indistinguishable from a wildcard grant — full access on a row the log
// claimed to be denying.
//
// THE EMPTY-STRING ARM (Phase-1.1 item A, fail closed): an EMPTY or
// whitespace-only TEXT value is UNREADABLE, never ABSENT. Only SQL NULL
// means ABSENT. An empty string used to read as ABSENT, which made a
// legacy `""` row indistinguishable from a NULL row — and a NULL row on
// BOTH axes is the WILDCARD state (ADR D3/D11). Reading `""` as ABSENT
// therefore turned a legacy empty column into a full-access wildcard, the
// exact escalation class this commission closes. It now lands in INVALID
// (DENY-ALL) like every other unreadable grant.
//
// OPERATIONAL CONSEQUENCE (stated, not silently absorbed): any PRE-EXISTING
// row whose column holds `""` (rather than NULL) becomes DENY-ALL under
// the new scanner — such a token can no longer reach anything until the
// column is repaired (NULL for "no grant on this axis", or a JSON array
// for a real grant). Migration 034 does NOT normalize legacy `""` rows
// (the SQL is frozen — Ruling 6); the repair is an operator-side
// inspection/reconciliation step, reported as an escalation item.
//
// This is a PARSER, not an authorization decision: the token-level state
// (wildcard / restricted / deny-all) is computed by authz.Resolve, which
// consumes the tri-state. Never re-derive the wildcard rule from one axis.
func ScanGrant(raw interface{}) (ids []string, present bool, invalid bool) {
	return scanGrant(raw, "")
}

// scanGrant is ScanGrant with an axis name for the SECURITY log line.
// axis is never a token value — only the column/axis label.
func scanGrant(raw interface{}, axis string) (ids []string, present bool, invalid bool) {
	if raw == nil {
		return nil, false, false
	}

	// Handle both string and []byte (some DB drivers return []byte for TEXT)
	var str string
	switch v := raw.(type) {
	case string:
		str = v
	case []byte:
		str = string(v)
	default:
		// Fail closed: an unknown driver raw type is an unreadable grant.
		log.Printf("SECURITY: unreadable %s (unknown driver raw type %T), denying all models on this axis", axisLabel(axis), raw)
		return nil, false, true
	}

	if strings.TrimSpace(str) == "" {
		// Fail closed (Phase-1.1 item A): an empty / whitespace-only TEXT
		// value is UNREADABLE, never ABSENT. Only SQL NULL means ABSENT.
		// Reading `""` as ABSENT made a legacy empty column
		// indistinguishable from NULL — and NULL on BOTH axes is the
		// WILDCARD state (ADR D3/D11). An empty column is therefore
		// DENY-ALL, exactly like a malformed or unknown-typed column.
		log.Printf("SECURITY: empty %s column (empty or whitespace-only text), denying all models on this axis", axisLabel(axis))
		return nil, false, true
	}

	// A non-array JSON literal ('null', '123', '"x"', '{...}') is NOT a
	// grant list. json.Unmarshal into []string would silently accept
	// 'null' as nil (indistinguishable from ABSENT) — reject it first so
	// it lands in INVALID, per plan §2.7 / ADR D10.
	if !startsWithJSONArray(str) {
		log.Printf("SECURITY: malformed %s JSON (not a JSON array), denying all models on this axis", axisLabel(axis))
		return nil, false, true
	}

	var granted []string
	if err := json.Unmarshal([]byte(str), &granted); err != nil {
		// Fail closed - deny all models on malformed JSON
		log.Printf("SECURITY: malformed %s JSON, denying all models on this axis", axisLabel(axis))
		return nil, false, true
	}
	if granted == nil {
		// Defensive: `[]` unmarshals to a non-nil empty slice; a nil
		// here means the literal was not an array after all.
		log.Printf("SECURITY: malformed %s JSON (decoded to nil), denying all models on this axis", axisLabel(axis))
		return nil, false, true
	}
	return granted, true, false
}

// axisLabel renders the log label for a grant axis — the column name
// ("allowed_models" / "allowed_groups"), or "grant axis" when unspecified.
// It NEVER carries a token value or any part of the row's contents.
func axisLabel(axis string) string {
	if axis == "" {
		return "grant axis"
	}
	return axis
}

// startsWithJSONArray reports whether s (already known non-blank) begins
// with '[' after leading whitespace — i.e. is a JSON array literal.
func startsWithJSONArray(s string) bool {
	for _, r := range s {
		switch r {
		case ' ', '\t', '\r', '\n':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

// ScanAllowedModels deserializes allowed_models into its ids.
//
// Deprecated: this is a []string-shaped shim over ScanGrant kept for
// callers that merely need the ids. It discards `present` and `invalid`,
// so it is NOT an authorization decision — use authz.Resolve (which
// consumes ScanGrant's full tri-state) for that.
func ScanAllowedModels(raw interface{}) []string {
	ids, _, invalid := ScanGrant(raw)
	if invalid {
		// Preserve the historical shim contract: unreadable -> empty
		// slice. Callers that must distinguish INVALID use ScanGrant.
		return []string{}
	}
	return ids
}

// GenerateToken generates a new random token with sk- prefix
// Returns the plaintext token (show once) and its hash for storage
func GenerateToken() (plaintext string, hash string, err error) {
	bytes := make([]byte, TokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}

	plaintext = TokenPrefix + hex.EncodeToString(bytes)
	hash = HashToken(plaintext)
	return plaintext, hash, nil
}

// HashToken creates a SHA-256 hash of the token for storage
func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// ValidateTokenFormat checks if token has correct format (sk- prefix + 64 hex chars)
func ValidateTokenFormat(token string) bool {
	if len(token) != len(TokenPrefix)+64 {
		return false
	}
	if token[:len(TokenPrefix)] != TokenPrefix {
		return false
	}
	// Check if rest is valid hex
	_, err := hex.DecodeString(token[len(TokenPrefix):])
	return err == nil
}

// IsExpired checks if the token has expired
func (t *AuthToken) IsExpired() bool {
	if t.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*t.ExpiresAt)
}

// IsModelAllowed checks if the given model ID is in the MODEL-axis grant.
//
// DEMOTED (token-authz plan §4.1): this is a model-axis helper only, NEVER
// the authorization decision. It can only see one of the two axes, so it
// cannot express the conjunctive wildcard rule (a token with
// allowed_models ABSENT but allowed_groups PRESENT is RESTRICTED, not a
// wildcard) — and it still treats an empty list as "all models allowed",
// which is exactly the escalation this commission removes. Every lane
// consults authz.Resolve / (*Scope).Allows instead; a grep guard in Phase 2
// proves no lane calls this method.
//
// Returns true if AllowedModels is nil or empty (the legacy per-axis
// wildcard view) or if modelID is found in the list. Case-sensitive
// exact match.
func (t *AuthToken) IsModelAllowed(modelID string) bool {
	// nil or empty slice means "no model-axis restriction" (legacy view).
	if len(t.AllowedModels) == 0 {
		return true
	}
	for _, allowed := range t.AllowedModels {
		if allowed == modelID {
			return true
		}
	}
	return false
}
