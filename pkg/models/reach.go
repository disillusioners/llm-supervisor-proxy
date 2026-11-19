package models

// ReachFilter is the authorization predicate a candidate-construction
// site consults. Implemented by *authz.Scope. nil => no filtering
// (wildcard token / no permission backend).
//
// It lives in pkg/models (which imports nothing) because it is the
// cycle-breaker for the permission seam: pkg/auth already imports
// pkg/store/database, so pkg/store/database may not import pkg/auth (or
// pkg/authz) — the predicate therefore crosses that boundary as this
// tiny interface, which both sides already depend on.
type ReachFilter interface{ Allows(modelID string) bool }
