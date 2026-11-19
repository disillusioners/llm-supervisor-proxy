package auth

// OptionalAllowlist is THE write-payload axis type (fix-pass 2, A4 — one name
// everywhere; it supersedes the interim TokenPermission write shape used during
// the contract commit).
//
//	Set:false                       -> the axis is UNTOUCHED by this write
//	                                   (the SQL UPDATE omits the column — PD-3;
//	                                   raw bytes survive, including a seeded
//	                                   malformed DENY-ALL row).
//	Set:true, Values:nil            -> write NULL. Store-level only: the PATCH
//	                                   wire (two-state, Ruling 2) cannot express
//	                                   it, so only an internal caller can reset
//	                                   an axis to ABSENT.
//	Set:true, Values:&[]string{}    -> write '[]' (PRESENT-empty, grants nothing).
//	Set:true, Values:&ids           -> write the JSON array (replace).
//
// The nil-vs-empty READ distinction stays normative (plan §2.2); see
// auth.ScanGrant for the read side.
type OptionalAllowlist struct {
	Set    bool
	Values *[]string
}

// IsSet reports whether this write touches the axis at all.
func (a OptionalAllowlist) IsSet() bool { return a.Set }

// Value returns the axis value when set (nil pointer or nil slice => nil,
// i.e. ABSENT). It is the accessor the mocks and the store share so the
// Set/NULL distinction is never re-derived by hand.
func (a OptionalAllowlist) Value() []string {
	if !a.Set || a.Values == nil {
		return nil
	}
	return *a.Values
}

// OptionalTokenPermission is the write payload carried by CreateToken and
// UpdateTokenPermission — ONE type for both methods, so a positional swap of
// two grant axes is a compile error and "keep this axis" is expressible
// without a sentinel value.
type OptionalTokenPermission struct {
	AllowedModels OptionalAllowlist
	AllowedGroups OptionalAllowlist
}

// FullPermission builds a full-write payload with BOTH axes set — the CREATE
// shape (a create writes every column it was given; there is no
// keep-semantics on create).
//
// A nil slice passed here means "write NULL" (the axis is ABSENT on the new
// token); an empty-but-non-nil slice means "write '[]'" (PRESENT-empty, which
// grants nothing). That is the escalation-trap distinction (decisions.md
// R-6/Q13): [] must never collapse into NULL.
func FullPermission(models, groups []string) OptionalTokenPermission {
	m, g := models, groups
	return OptionalTokenPermission{
		AllowedModels: OptionalAllowlist{Set: true, Values: &m},
		AllowedGroups: OptionalAllowlist{Set: true, Values: &g},
	}
}

// GrantModels builds a payload that SETS only the MODEL axis (the group axis
// is untouched). `ids` may be nil — which writes NULL (store-level reset) — or
// empty-but-non-nil, which writes '[]'.
func GrantModels(ids []string) OptionalTokenPermission {
	v := ids
	return OptionalTokenPermission{AllowedModels: OptionalAllowlist{Set: true, Values: &v}}
}

// GrantGroups builds a payload that SETS only the GROUP axis (the model axis is
// untouched). Same nil-vs-empty rule as GrantModels.
func GrantGroups(ids []string) OptionalTokenPermission {
	v := ids
	return OptionalTokenPermission{AllowedGroups: OptionalAllowlist{Set: true, Values: &v}}
}
