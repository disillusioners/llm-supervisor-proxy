package auth

// TokenPermission is the WRITE-side payload of a token's grant axes.
//
// nil == the axis is ABSENT; non-nil (possibly empty) == PRESENT.
// The nil-vs-empty distinction is normative (plan §2.2): nil -> NULL,
// [] -> '[]', ids -> JSON array. See auth.ScanGrant for the read side.
//
// ONE struct carries both axes (never two adjacent []string parameters):
// a positional swap of two slices is a silent privilege change, a field
// swap is a compile error. The single struct is also what makes the
// PATCH merge expressible without a sentinel (plan §6.3 PD-3): an axis
// left nil is NOT written to SQL at all, so a keep on an unreadable
// (INVALID) axis can never heal the corrupt bytes into NULL/wildcard.
type TokenPermission struct {
	AllowedModels []string
	AllowedGroups []string
}
