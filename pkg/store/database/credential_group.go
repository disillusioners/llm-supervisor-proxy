package database

// credential-groups commission (phase1-plan.md WU-3 + WU-4) — the
// credential-group manager CRUD, the reverse lookups, the engine
// fan-out, and THE flattening seam (the single helper that expands a
// group's members into plain credential refs BEFORE the selection layer
// ever sees them).
//
// LOCKED REPRESENTATION: a group ref is a models.CredentialRef with
// GroupID set and CredentialID empty. Groups are MANAGEMENT-ONLY: no
// selection package (pkg/credentiallb, pkg/stickyorder, pkg/skipcache,
// pkg/grouplb) ever sees a group — the expansion happens here, in the
// store, before the engine is handed the refs.
//
// LOCK DISCIPLINE (A-3 — binding):
//
//   - m.groups / m.groupMemberProviders are guarded by a DEDICATED
//     m.groupsMu (sync.RWMutex). Flatten takes groupsMu.RLock() and
//     NEVER m.mu.RLock(): several flatten call sites sit inside a held
//     m.mu.Lock() span (the AddModel / UpdateModel engine-notify, the
//     group-write fan-out, RemoveCredential) and Go's RWMutex is
//     NON-REENTRANT — an m.mu.RLock() inside flatten SELF-DEADLOCKS.
//   - Group WRITES hold m.mu for the whole validate → SQL → swap →
//     fan-out span and take groupsMu at the swap. The lock order is
//     strictly m.mu → groupsMu; the reverse never occurs.
//   - The resolve-hot path NEVER reads the group map without the lock
//     (a lock-free map read is a fatal concurrent-map read — a process
//     crash on the critical substrate).
//
// ZERO-DB CONTRACT (phase1 §d.4): the flatten reads the in-memory group
// map only. The cached resolver (ResolveInternalConfigWithAffinityCached)
// therefore performs NO database query when it expands a group.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// credentialGroupRebuildTimeout bounds EVERY DB read in the group
// rebuild/reconcile path. The sibling budget is the decorator's
// StrictFillTimeout (pkg/modelscache/models.go:373 — 5s); the same
// bound applies here so a stalled DB can never hold the reconcile
// open indefinitely.
//
// It is a package-level const (not a literal) so it is tunable and
// testable.
const credentialGroupRebuildTimeout = 5 * time.Second

// ---------------------------------------------------------------------------
// Row shape + JSON codecs (house style: JSON-on-row, no join tables)
// ---------------------------------------------------------------------------

// credentialGroupRow is the DB row shape of the credential_groups table
// (migration 033). members_json holds a JSON array of credential IDs —
// the same JSON-on-row posture as models.credentials_json.
type credentialGroupRow struct {
	ID          string
	Name        string
	Provider    string
	MembersJSON string
	CreatedAt   string
	UpdatedAt   string
}

// parseCredentialGroupMembersJSON parses the members_json column into an
// ordered []string. House precedent (parseCredentialsJSON): unparseable
// or empty input yields an EMPTY (non-nil) slice with no error — the
// column is app-level, not DB-enforced.
func parseCredentialGroupMembersJSON(raw string) []string {
	if raw == "" || raw == "[]" {
		return []string{}
	}
	var members []string
	if err := json.Unmarshal([]byte(raw), &members); err != nil {
		return []string{}
	}
	if members == nil {
		return []string{}
	}
	return members
}

// marshalCredentialGroupMembersJSON serializes the member list. nil /
// empty yields '[]' (never NULL — the column's NOT NULL DEFAULT '[]'
// invariant); a marshal failure falls back to '[]'.
func marshalCredentialGroupMembersJSON(members []string) string {
	if len(members) == 0 {
		return "[]"
	}
	b, err := json.Marshal(members)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Reads
// ---------------------------------------------------------------------------

// scanCredentialGroups reads every credential_groups row. The SQL is
// dialect-neutral (no placeholders in the projection) — the credential
// CRUD's inline-SQL house style.
func (m *ModelsManager) scanCredentialGroups(ctx context.Context) ([]models.CredentialGroupConfig, error) {
	query := `SELECT id, name, provider, members_json, created_at, updated_at FROM credential_groups ORDER BY id`
	rows, err := m.store.DB.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]models.CredentialGroupConfig, 0)
	for rows.Next() {
		var r credentialGroupRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Provider, &r.MembersJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan error: %w", err)
		}
		result = append(result, models.CredentialGroupConfig{
			ID:        r.ID,
			Name:      r.Name,
			Provider:  r.Provider,
			Members:   parseCredentialGroupMembersJSON(r.MembersJSON),
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return result, nil
}

// getCredentialGroupRow reads a single credential_groups row. It does NOT
// take m.mu — the group write path holds m.mu.Lock() and a nested
// m.mu.RLock() would self-deadlock (Go's RWMutex is non-reentrant).
// A missing row surfaces as models.ErrCredentialGroupNotFound.
func (m *ModelsManager) getCredentialGroupRow(ctx context.Context, id string) (*models.CredentialGroupConfig, error) {
	query := `SELECT id, name, provider, members_json, created_at, updated_at FROM credential_groups WHERE id = ?`
	if m.store.Dialect == PostgreSQL {
		query = `SELECT id, name, provider, members_json, created_at, updated_at FROM credential_groups WHERE id = $1`
	}
	var r credentialGroupRow
	if err := m.store.DB.QueryRowContext(ctx, query, id).Scan(&r.ID, &r.Name, &r.Provider, &r.MembersJSON, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, models.ErrCredentialGroupNotFound
		}
		return nil, err
	}
	return &models.CredentialGroupConfig{
		ID:        r.ID,
		Name:      r.Name,
		Provider:  r.Provider,
		Members:   parseCredentialGroupMembersJSON(r.MembersJSON),
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}, nil
}

// GetCredentialGroup returns the credential group for a given ID, or nil
// when it does not exist. Legacy read shape — mirrors GetCredential
// (store.go): a DB read, nil on any failure.
//
// NOTE: the READ surface is the persisted truth; the in-memory group map
// (m.groups) is the FLATTEN authority. The two agree for every write
// that went through this manager; a group written by DIRECT SQL is
// visible here and heals into the flatten via RebuildCredentialGroupsFromDB,
// which self-heals at the decorator's <=60s reconcile tick (and
// immediately after a restart) — never mid-request.
func (m *ModelsManager) GetCredentialGroup(id string) *models.CredentialGroupConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	g, err := m.getCredentialGroupRow(context.Background(), id)
	if err != nil {
		return nil
	}
	return g
}

// GetCredentialGroups returns every credential group. Legacy read shape —
// mirrors GetCredentials (store.go).
func (m *ModelsManager) GetCredentialGroups() []models.CredentialGroupConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	groups, err := m.scanCredentialGroups(context.Background())
	if err != nil {
		return []models.CredentialGroupConfig{}
	}
	return groups
}

// GetCredentialGroupStrict is the error-propagating twin of
// GetCredentialGroup: (nil, models.ErrCredentialGroupNotFound) when the
// row does not exist, (nil, err) unchanged on an infrastructure failure.
// Mirrors GetCredentialStrict (store.go) — the decorator's strict source
// contract.
func (m *ModelsManager) GetCredentialGroupStrict(ctx context.Context, id string) (*models.CredentialGroupConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.getCredentialGroupRow(ctx, id)
}

// GetCredentialGroupsStrict is the error-propagating twin of
// GetCredentialGroups. Mirrors GetCredentialsStrict (store.go).
func (m *ModelsManager) GetCredentialGroupsStrict(ctx context.Context) ([]models.CredentialGroupConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.scanCredentialGroups(ctx)
}

// ---------------------------------------------------------------------------
// CRUD
// ---------------------------------------------------------------------------

// AddCredentialGroup creates a credential group.
//
// The whole validate → INSERT → cache-swap → fan-out span runs under
// m.mu (write) so a concurrent model write cannot interleave between the
// swap and the fan-out (that window is the stale-flat-list
// last-writer-wins hole A-3 closes).
//
// A-15i: Add fans out TOO — a group can be referenced by a model row
// written by direct SQL / a DB restore / the R-1 repair dance, which
// never passed through this method. One reverse lookup on a rare admin
// op closes the hole.
func (m *ModelsManager) AddCredentialGroup(g models.CredentialGroupConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// G-1..G-9: field rules + member existence + member/provider match.
	// The member rows are read WITHOUT m.mu (this span holds
	// m.mu.Lock() — a nested RLock would self-deadlock).
	members := m.credentialConfigSnapshotForIDs(g.Members)
	if err := models.ValidateCredentialGroup(g, members); err != nil {
		return err
	}

	// Duplicate id: the PK would reject the INSERT anyway, but an
	// explicit message beats a driver-specific constraint error.
	// The message WRAPS models.ErrCredentialGroupDuplicate so the API
	// layer maps the rejection to a status by errors.Is (A-9) instead
	// of string-matching the message text.
	if existing, err := m.getCredentialGroupRow(context.Background(), g.ID); err == nil && existing != nil {
		return fmt.Errorf("credential group '%s' already exists: %w", g.ID, models.ErrCredentialGroupDuplicate)
	}

	var query string
	if m.store.Dialect == PostgreSQL {
		query = `INSERT INTO credential_groups (id, name, provider, members_json, created_at, updated_at) VALUES ($1, $2, $3, $4, NOW(), NOW())`
	} else {
		query = `INSERT INTO credential_groups (id, name, provider, members_json, created_at, updated_at) VALUES (?, ?, ?, ?, datetime('now'), datetime('now'))`
	}
	if _, err := m.store.DB.ExecContext(context.Background(), query, g.ID, g.Name, g.Provider, marshalCredentialGroupMembersJSON(g.Members)); err != nil {
		return err
	}

	// Re-read the row so the cached copy carries the DB's canonical
	// timestamps (the rebuild produces byte-identical entries).
	stored, err := m.getCredentialGroupRow(context.Background(), g.ID)
	if err != nil {
		return err
	}
	m.applyCredentialGroupToCache(*stored, members)
	m.fanOutCredentialGroupLocked(g.ID)
	return nil
}

// UpdateCredentialGroup updates an existing credential group.
//
// The ID is IMMUTABLE (the ErrCannotChangeCredentialID precedent). The
// update fans out to every referencing model — a membership edit is
// exactly the case where selection would otherwise run stale.
func (m *ModelsManager) UpdateCredentialGroup(id string, g models.CredentialGroupConfig) error {
	if g.ID != id {
		return models.ErrCannotChangeCredentialID
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Existence first: a missing group is a 404-mapped error, not an
	// UPDATE that silently affects zero rows.
	if _, err := m.getCredentialGroupRow(context.Background(), id); err != nil {
		return err
	}

	members := m.credentialConfigSnapshotForIDs(g.Members)
	if err := models.ValidateCredentialGroup(g, members); err != nil {
		return err
	}

	var query string
	if m.store.Dialect == PostgreSQL {
		query = `UPDATE credential_groups SET name = $1, provider = $2, members_json = $3, updated_at = NOW() WHERE id = $4`
	} else {
		query = `UPDATE credential_groups SET name = ?, provider = ?, members_json = ?, updated_at = datetime('now') WHERE id = ?`
	}
	result, err := m.store.DB.ExecContext(context.Background(), query, g.Name, g.Provider, marshalCredentialGroupMembersJSON(g.Members), id)
	if err != nil {
		return err
	}
	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		return models.ErrCredentialGroupNotFound
	}

	stored, err := m.getCredentialGroupRow(context.Background(), id)
	if err != nil {
		return err
	}
	m.applyCredentialGroupToCache(*stored, members)
	m.fanOutCredentialGroupLocked(id)
	return nil
}

// RemoveCredentialGroup removes a credential group.
//
// IN-USE GUARD FIRST: a group referenced by any model is NOT deleted —
// the error names the referencing models (409-mapped upstream) and the
// operator inlines the members first. Never cascade.
//
// No fan-out on delete: the in-use guard guarantees that no model
// references the group, so there is nothing to refresh.
func (m *ModelsManager) RemoveCredentialGroup(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	usedBy := m.ModelsUsingCredentialGroup(id)
	if len(usedBy) > 0 {
		return fmt.Errorf("credential group '%s' is in use by model(s) %s: %w", id, strings.Join(usedBy, ", "), models.ErrCredentialGroupInUse)
	}

	if _, err := m.getCredentialGroupRow(context.Background(), id); err != nil {
		return err
	}

	var query string
	if m.store.Dialect == PostgreSQL {
		query = `DELETE FROM credential_groups WHERE id = $1`
	} else {
		query = `DELETE FROM credential_groups WHERE id = ?`
	}
	result, err := m.store.DB.ExecContext(context.Background(), query, id)
	if err != nil {
		return err
	}
	if rowsAffected, _ := result.RowsAffected(); rowsAffected == 0 {
		return models.ErrCredentialGroupNotFound
	}

	m.removeCredentialGroupFromCache(id)
	return nil
}

// ---------------------------------------------------------------------------
// Cache swap helpers (groupsMu)
// ---------------------------------------------------------------------------

// applyCredentialGroupToCache swaps the group into the in-memory cache
// and refreshes the member-provider snapshot for its members.
//
// Caller holds m.mu (write). The swap takes groupsMu — lock order is
// strictly m.mu → groupsMu.
func (m *ModelsManager) applyCredentialGroupToCache(g models.CredentialGroupConfig, members map[string]models.CredentialConfig) {
	m.groupsMu.Lock()
	defer m.groupsMu.Unlock()

	if m.groups == nil {
		m.groups = make(map[string]models.CredentialGroupConfig)
	}
	if m.groupMemberProviders == nil {
		m.groupMemberProviders = make(map[string]string)
	}
	m.groups[g.ID] = g
	for id, c := range members {
		m.groupMemberProviders[id] = c.Provider
	}
}

// removeCredentialGroupFromCache drops the group from the in-memory
// cache AND prunes its members from the member-provider snapshot.
//
// O-8: the provider snapshot is the A-8 fail-closed input; a member of
// a REMOVED group that stays in the snapshot is stale state that
// outlives its group (and it also defeats the rebuild's early-return,
// which would never see the snapshot compare equal).
//
// A member that ANOTHER cached group still lists KEEPS its provider
// entry — that group still expands it.
//
// Caller holds m.mu (write); the prune happens in the SAME groupsMu
// write span as the removal (no window where the group is gone but its
// member providers linger).
func (m *ModelsManager) removeCredentialGroupFromCache(id string) {
	m.groupsMu.Lock()
	defer m.groupsMu.Unlock()

	removed, ok := m.groups[id]
	delete(m.groups, id)
	if !ok {
		return
	}
	for _, member := range removed.Members {
		if m.memberInAnyGroupLocked(member) {
			continue
		}
		delete(m.groupMemberProviders, member)
	}
}

// memberInAnyGroupLocked reports whether any CACHED group still lists the
// credential as a member. The caller must hold groupsMu (read or write).
func (m *ModelsManager) memberInAnyGroupLocked(credID string) bool {
	for _, g := range m.groups {
		for _, member := range g.Members {
			if member == credID {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Reverse lookups
// ---------------------------------------------------------------------------

// ModelsUsingCredentialGroup returns the IDs of every model whose
// credentials_json references the given group.
//
// LOCK-FREE with respect to m.mu: the reverse scan (scanModels) does not
// take the manager lock, so this is safe inside the m.mu.Lock() spans
// (RemoveCredentialGroup's in-use guard, the fan-out).
//
// The IDs are ordered by model NAME (the models table's ORDER BY name) —
// deterministic, not insertion order.
func (m *ModelsManager) ModelsUsingCredentialGroup(groupID string) []string {
	if groupID == "" {
		return nil
	}
	return m.ModelsUsingCredentialGroupsAll()[groupID]
}

// ModelsUsingCredentialGroupsAll computes the "used_by" map for EVERY
// credential group in ONE pass over models.credentials_json.
//
// The list endpoint's used_by badge map must not cost one full-table
// scan PER GROUP — this is the batched form (T-14). The scan is
// lock-free with respect to m.mu.
func (m *ModelsManager) ModelsUsingCredentialGroupsAll() map[string][]string {
	out := make(map[string][]string)

	modelList, err := m.scanModels(m.qb.GetAllModels())
	if err != nil {
		log.Printf("[WARN] [credential-groups] model scan failed while computing credential group usage (%v) — the used_by map is empty", err)
		return out
	}
	for _, mc := range modelList {
		for _, ref := range mc.Credentials {
			if !ref.IsGroupRef() {
				continue
			}
			out[ref.GroupID] = append(out[ref.GroupID], mc.ID)
		}
	}
	return out
}

// GroupsContainingCredential returns the IDs of every credential group
// that lists the credential as a member.
//
// LOCK DISCIPLINE: this accessor takes ONLY groupsMu.RLock() — never
// m.mu.RLock() — because it is called from INSIDE RemoveCredential's
// m.mu.Lock() span and Go's RWMutex is non-reentrant (a nested RLock on
// the same mutex self-deadlocks).
//
// The IDs are sorted for determinism.
func (m *ModelsManager) GroupsContainingCredential(credID string) []string {
	m.groupsMu.RLock()
	defer m.groupsMu.RUnlock()

	return m.groupsContainingCredentialNoLock(credID)
}

// credentialGroupMembership is a (group id, group provider) pair — the
// shape the credential-write guards need (the group provider is the
// same-provider invariant a member must satisfy).
type credentialGroupMembership struct {
	ID       string
	Provider string
}

// credentialGroupsContainingCredential returns the groups that list the
// credential as a member, sorted by group id, together with the group's
// provider. It takes groupsMu.RLock() only — safe inside an m.mu.Lock()
// span (lock order m.mu → groupsMu).
func (m *ModelsManager) credentialGroupsContainingCredential(credID string) []credentialGroupMembership {
	m.groupsMu.RLock()
	defer m.groupsMu.RUnlock()

	out := make([]credentialGroupMembership, 0)
	for id, g := range m.groups {
		for _, member := range g.Members {
			if member == credID {
				out = append(out, credentialGroupMembership{ID: id, Provider: g.Provider})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// groupsContainingCredentialNoLock is the LOCK-FREE internal accessor:
// the caller must ALREADY hold groupsMu (read or write) — taking it
// again would self-deadlock. Use it from a span that holds groupsMu;
// use GroupsContainingCredential everywhere else.
func (m *ModelsManager) groupsContainingCredentialNoLock(credID string) []string {
	if credID == "" {
		return nil
	}
	var out []string
	for id, g := range m.groups {
		for _, member := range g.Members {
			if member == credID {
				out = append(out, id)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// THE flattening seam (phase1 §d.1)
// ---------------------------------------------------------------------------

// FlattenCredentialRefs expands a model's group refs into plain
// credential refs — the ONE helper every store site uses.
//
// Algorithm (phase1 §d.1):
//
//  1. model == nil || len(model.Credentials) == 0 → nil.
//  2. Walk the refs IN ORDER:
//     - direct ref (GroupID == "") → appended verbatim;
//     - group ref (GroupID != "") → the group expands IN PLACE: each
//     member becomes {CredentialID: member, Weight: <the group ref's
//     Weight>, Position: <the group ref's Position>}.
//  3. Dedup KEEP-FIRST by CredentialID across the whole list (the first
//     occurrence wins; later duplicates are dropped).
//
// A-8 (fail closed): only members whose provider matches the GROUP's
// provider are expanded; a divergent member is skipped + WARN-logged.
// The provider snapshot is refreshed on every group write and on every
// RebuildCredentialGroupsFromDB — a direct-SQL credential-provider edit
// is therefore observed at the next rebuild (restart or the decorator's
// <=60s reconcile heal), never mid-request.
//
// T-13b (fail closed + observable): a MISSING group and an EMPTY group
// both contribute nothing, and BOTH are WARN-logged with the group id —
// a silent skip would be a 502 with no diagnostic.
//
// LOCKING: takes groupsMu.RLock() for the whole walk and NEVER m.mu —
// several call sites hold m.mu.Lock() (the AddModel / UpdateModel
// engine-notify, the group-write fan-out, RemoveCredential) and an
// m.mu.RLock() here would self-deadlock.
func (m *ModelsManager) FlattenCredentialRefs(model *models.ModelConfig) []models.CredentialRef {
	m.groupsMu.RLock()
	defer m.groupsMu.RUnlock()

	return m.flattenCredentialRefsLocked(model)
}

// flattenCredentialRefsLocked is the internal walker. The caller MUST
// hold m.groupsMu (read lock) for the whole walk.
func (m *ModelsManager) flattenCredentialRefsLocked(model *models.ModelConfig) []models.CredentialRef {
	if model == nil || len(model.Credentials) == 0 {
		return nil
	}

	out := make([]models.CredentialRef, 0, len(model.Credentials))
	seen := make(map[string]bool, len(model.Credentials))
	appendRef := func(ref models.CredentialRef) {
		if ref.CredentialID == "" {
			return
		}
		if seen[ref.CredentialID] {
			return // dedup keep-first: the first occurrence wins
		}
		seen[ref.CredentialID] = true
		out = append(out, ref)
	}

	for _, ref := range model.Credentials {
		if !ref.IsGroupRef() {
			appendRef(ref) // direct ref — verbatim
			continue
		}

		group, ok := m.groups[ref.GroupID]
		if !ok {
			log.Printf("[WARN] [credential-groups] model %s references credential group '%s' which does not exist — the group contributes no credentials (fail closed)", model.ID, ref.GroupID)
			continue
		}
		if len(group.Members) == 0 {
			// An empty group is ALLOWED (G-8: create-then-populate) and
			// contributes nothing. If this leaves the model with zero
			// refs the resolver degrades to the legacy 502 path — the
			// WARN is what makes that observable.
			log.Printf("[WARN] [credential-groups] credential group '%s' (model %s) has no members — it contributes no credentials", group.ID, model.ID)
			continue
		}

		for _, memberID := range group.Members {
			provider, known := m.groupMemberProviders[memberID]
			if !known {
				log.Printf("[WARN] [credential-groups] member '%s' of credential group '%s' has no known provider — skipped (fail closed)", memberID, group.ID)
				continue
			}
			if !strings.EqualFold(provider, group.Provider) {
				log.Printf("[WARN] [credential-groups] member '%s' of credential group '%s' has provider %q which differs from the group provider %q — skipped (fail closed)", memberID, group.ID, provider, group.Provider)
				continue
			}
			// Weight is inherited from the group ref (locked); Position
			// is the group ref's Position (cosmetic — the selector uses
			// Weight, the UI re-stamps Position).
			appendRef(models.CredentialRef{CredentialID: memberID, Weight: ref.Weight, Position: ref.Position})
		}
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// ---------------------------------------------------------------------------
// Cache rebuild / reconcile
// ---------------------------------------------------------------------------

// RebuildCredentialGroupsFromDB replaces the in-memory credential-group
// cache wholesale from the DB and refreshes the engine state of every
// model that references a group.
//
// REBUILD, NEVER MERGE: the R-1 repair writes groups by DIRECT SQL, so a
// merge-based refresh would keep the stale entry forever.
//
// REFRESH RULE (T-13b): the store-side group map IS reconciled — the
// decorator's <=60s reconcile tick calls this method (pkg/modelscache/
// models.go:387) and the boot prime calls it too (pkg/modelscache/
// models.go:163). A group written by DIRECT SQL is therefore visible to
// the flatten within <=60s (and immediately after a restart), not only
// after a restart.
//
// LATENCY (the request path must never wait on a reconcile):
//
//   - NO DB READ runs under the store's write lock. sync.RWMutex is
//     writer-preferring: a pending (or held) m.mu.Lock() starves the
//     m.mu.RLock() readers the LIVE request path takes (GetModelNative
//     -> m.mu.RLock(), reached from the group rewrite). The reads
//     therefore happen FIRST, with NO m.mu held; the write lock covers
//     ONLY the compare + the swap + the engine re-apply.
//   - EVERY read is bounded by credentialGroupRebuildTimeout.
//   - The member-provider snapshot is read in ONE round-trip (no N+1).
//   - An UNCHANGED reconcile returns early: no models scan, no engine
//     re-apply.
func (m *ModelsManager) RebuildCredentialGroupsFromDB() error {
	ctx, cancel := context.WithTimeout(context.Background(), credentialGroupRebuildTimeout)
	defer cancel()

	// PHASE 1 — READ (no m.mu held).
	byID, providers, err := m.readCredentialGroupsSnapshot(ctx)
	if err != nil {
		return err
	}

	// PHASE 2 — FAST PATH: nothing changed → no models scan, no
	// engine re-apply. The comparison covers the group rows AND the
	// member-provider snapshot (a direct-SQL credential-PROVIDER edit
	// must still be observed — the A-8 fail-closed check depends on
	// it).
	if !m.credentialGroupSnapshotChanged(byID, providers) {
		return nil
	}

	// PHASE 3 — the models scan (still NO m.mu held): it is a full
	// table scan and must never sit inside the write lock.
	scanCtx, scanCancel := context.WithTimeout(context.Background(), credentialGroupRebuildTimeout)
	modelList, scanErr := m.scanModelsContext(scanCtx, m.qb.GetAllModels())
	scanCancel()
	if scanErr != nil {
		log.Printf("[WARN] [credential-groups] rebuild fan-out skipped — model scan error: %v", scanErr)
		modelList = nil
	}

	// PHASE 4 — APPLY: the write lock covers the compare + the swap +
	// the engine re-apply ONLY.
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.applyCredentialGroupSnapshot(byID, providers) {
		return nil
	}
	if scanErr == nil {
		m.fanOutCredentialGroups(modelList)
	}
	return nil
}

// credentialGroupScanHook is a TEST SEAM (package-level func var — the
// pkg/proxy/race_executor.go:26 newProviderClient precedent). It fires
// immediately AFTER the credential-group scan returns, i.e. inside the
// rebuild's READ phase.
//
// It is the probe for the reconcile-vs-request-path concurrency
// regression: before the fix the read phase ran INSIDE the store's
// write lock (m.mu.Lock()), so a pending writer starved the request
// path's m.mu.RLock() readers (sync.RWMutex is writer-preferring);
// after the fix the read phase runs OUTSIDE the write lock and the
// request path is never blocked.
//
// nil in production. Restore it (defer) after use — a leaked hook
// fires in unrelated tests.
var credentialGroupScanHook func()

// credentialGroupProviderQueryHook is a TEST SEAM: it fires after each
// provider round-trip in credentialProviderSnapshotForIDs, carrying the
// number of round-trips performed so far. The one-query proof asserts
// exactly ONE round-trip for a multi-member group.
//
// nil in production.
var credentialGroupProviderQueryHook func(int)

// credentialGroupRebuildFanOutHook is a TEST SEAM: it fires once per
// group-referencing model in the rebuild fan-out. The early-return test
// counts the calls to prove that an UNCHANGED reconcile skips the
// fan-out (and therefore the full models scan) entirely.
//
// nil in production.
var credentialGroupRebuildFanOutHook func()

// readCredentialGroupsSnapshot reads the credential-group rows AND the
// member-provider snapshot from the DB.
//
// LOCK DISCIPLINE: NO m.mu IS HELD. The reads are IO; the store's
// write lock must never cover them (the request path's m.mu.RLock()
// readers would starve behind the pending writer). The callers take
// m.mu only for the compare + swap + fan-out.
//
// The member providers are read in ONE round-trip (`id IN (...)`) — the
// per-member N+1 read compounded a DB stall by the member count.
func (m *ModelsManager) readCredentialGroupsSnapshot(ctx context.Context) (map[string]models.CredentialGroupConfig, map[string]string, error) {
	groups, err := m.scanCredentialGroups(ctx)
	if err != nil {
		return nil, nil, err
	}
	if credentialGroupScanHook != nil {
		credentialGroupScanHook()
	}

	memberIDs := make([]string, 0, len(groups))
	for _, g := range groups {
		memberIDs = append(memberIDs, g.Members...)
	}
	providers, err := m.credentialProviderSnapshotForIDs(ctx, memberIDs)
	if err != nil {
		return nil, nil, err
	}

	byID := make(map[string]models.CredentialGroupConfig, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
	}
	return byID, providers, nil
}

// rebuildCredentialGroupsCache replaces the in-memory group cache (and
// the member-provider snapshot) from the DB. It is the BOOT PRIME path
// (pkg/store/database/store.go:813 — NewModelsManager).
//
// The DB reads happen OUTSIDE any m.mu span (readCredentialGroupsSnapshot
// takes NO lock) — the comment that used to claim "the lock is held
// only for the map replacement" was FALSE for the reconcile caller,
// which held m.mu across the whole call. Both callers now read first,
// then swap.
//
// The caller must NOT hold m.mu: this helper takes groupsMu only (the
// lock order m.mu → groupsMu is respected because no m.mu is taken
// here at all).
func (m *ModelsManager) rebuildCredentialGroupsCache(ctx context.Context) error {
	byID, providers, err := m.readCredentialGroupsSnapshot(ctx)
	if err != nil {
		return err
	}
	m.applyCredentialGroupSnapshot(byID, providers)
	return nil
}

// applyCredentialGroupSnapshot swaps the freshly-read group map and the
// member-provider snapshot into the cache — but ONLY when they differ.
//
// The comparison covers BOTH maps:
//   - the group rows (id / name / provider / members / created_at /
//     updated_at), and
//   - the MEMBER→PROVIDER snapshot. The member-provider half is
//     load-bearing: the A-8 fail-closed expand reads it, so a
//     direct-SQL credential-PROVIDER edit that leaves the group rows
//     untouched must still be observed (a group-rows-only comparison
//     would mask it and the divergent member would keep expanding with
//     its stale provider).
//
// A BEST-EFFORT guard aborts the swap when the cache holds a group row
// strictly newer than the snapshot's (a manager group write landed
// during the rebuild's DB read and its cache update must win — the
// write path stores the row re-read from the DB, so its updated_at is
// the DB's). The next tick re-scans. This closes the most likely
// concurrent-write clobber; a group add/remove during the scan is
// rarer and self-heals at the next tick.
//
// Returns true when the cache was replaced.
//
// LOCKING: takes groupsMu (write) only — the caller may hold m.mu
// (write) or hold nothing (the boot prime). The lock order stays
// m.mu → groupsMu.
func (m *ModelsManager) applyCredentialGroupSnapshot(byID map[string]models.CredentialGroupConfig, providers map[string]string) bool {
	m.groupsMu.Lock()
	defer m.groupsMu.Unlock()

	if credentialGroupSnapshotsEqual(m.groups, m.groupMemberProviders, byID, providers) {
		return false
	}

	// Best-effort guard: the cache holds a group row strictly newer
	// than the snapshot's for the same id → a manager write landed
	// during the read → abort (its cache update must win).
	for id, cached := range m.groups {
		if fresh, ok := byID[id]; ok {
			if cached.UpdatedAt != "" && fresh.UpdatedAt != "" && cached.UpdatedAt > fresh.UpdatedAt {
				return false
			}
		}
	}

	m.groups = byID
	m.groupMemberProviders = providers
	return true
}

// credentialGroupSnapshotChanged reports whether the freshly-read
// snapshot differs from the cache. LOCK-FREE with respect to m.mu —
// it takes groupsMu.RLock() only (the request path's flatten takes the
// same lock).
func (m *ModelsManager) credentialGroupSnapshotChanged(byID map[string]models.CredentialGroupConfig, providers map[string]string) bool {
	m.groupsMu.RLock()
	defer m.groupsMu.RUnlock()
	return !credentialGroupSnapshotsEqual(m.groups, m.groupMemberProviders, byID, providers)
}

// credentialGroupSnapshotsEqual reports whether the cached group map and
// the member-provider snapshot are IDENTICAL to the freshly-read ones.
//
// BOTH maps are compared: the group rows (id / name / provider / members
// / created_at / updated_at) AND the member→provider snapshot. The
// member-provider half is load-bearing: the A-8 fail-closed expand reads
// it, so a direct-SQL credential-PROVIDER edit that leaves the group
// rows untouched must still be observed (a group-rows-only comparison
// would mask it and the divergent member would keep expanding with its
// stale provider).
func credentialGroupSnapshotsEqual(cachedGroups map[string]models.CredentialGroupConfig, cachedProviders map[string]string, newGroups map[string]models.CredentialGroupConfig, newProviders map[string]string) bool {
	if len(cachedGroups) != len(newGroups) {
		return false
	}
	for id, ng := range newGroups {
		cg, ok := cachedGroups[id]
		if !ok {
			return false
		}
		if cg.ID != ng.ID || cg.Name != ng.Name || cg.Provider != ng.Provider ||
			cg.CreatedAt != ng.CreatedAt || cg.UpdatedAt != ng.UpdatedAt ||
			!slices.Equal(cg.Members, ng.Members) {
			return false
		}
	}
	if len(cachedProviders) != len(newProviders) {
		return false
	}
	for id, np := range newProviders {
		cp, ok := cachedProviders[id]
		if !ok || cp != np {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Fan-out
// ---------------------------------------------------------------------------

// fanOutCredentialGroupLocked refreshes the engine state of every model
// that references the given group.
//
// The fan-out executes INSIDE the m.mu write span (A-3): the group write
// holds m.mu across validate → SQL → swap → fan-out, so a concurrent
// UpdateModel cannot interleave between the swap and the fan-out (that
// window is the stale-flat-list last-writer-wins hole).
//
// Caller holds m.mu (write). groupsMu must NOT be held here — the flatten
// takes groupsMu.RLock() and Go's RWMutex is non-reentrant.
func (m *ModelsManager) fanOutCredentialGroupLocked(groupID string) {
	modelList, err := m.scanModels(m.qb.GetAllModels())
	if err != nil {
		log.Printf("[WARN] [credential-groups] fan-out for credential group '%s' failed — model scan error: %v", groupID, err)
		return
	}
	for i := range modelList {
		mc := &modelList[i]
		if !modelReferencesCredentialGroup(mc, groupID) {
			continue
		}
		flat := m.FlattenCredentialRefs(mc)
		m.engine.OnModelChanged(mc.ID, flat)
		m.publishCredentialsChanged(mc.ID)
	}
}

// fanOutCredentialGroups refreshes the engine state of EVERY model that
// references ANY group. Used after a wholesale cache rebuild (the
// direct-SQL heal path). The model list is scanned by the caller OUTSIDE
// the write lock; this helper only applies the engine state.
//
// Caller holds m.mu (write). groupsMu must NOT be held here.
func (m *ModelsManager) fanOutCredentialGroups(modelList []models.ModelConfig) {
	for i := range modelList {
		mc := &modelList[i]
		if !modelHasCredentialGroupRef(mc) {
			continue
		}
		flat := m.FlattenCredentialRefs(mc)
		m.engine.OnModelChanged(mc.ID, flat)
		m.publishCredentialsChanged(mc.ID)
		if credentialGroupRebuildFanOutHook != nil {
			credentialGroupRebuildFanOutHook()
		}
	}
}

// modelReferencesCredentialGroup reports whether the model row carries a
// group ref for the given group id.
func modelReferencesCredentialGroup(mc *models.ModelConfig, groupID string) bool {
	if mc == nil || groupID == "" {
		return false
	}
	for _, ref := range mc.Credentials {
		if ref.IsGroupRef() && ref.GroupID == groupID {
			return true
		}
	}
	return false
}

// modelHasCredentialGroupRef reports whether the model row carries any
// group ref.
func modelHasCredentialGroupRef(mc *models.ModelConfig) bool {
	if mc == nil {
		return false
	}
	for _, ref := range mc.Credentials {
		if ref.IsGroupRef() {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Credential snapshot (lock-free DB read)
// ---------------------------------------------------------------------------

// credentialProviderSnapshotForIDs reads the (credential id → provider)
// pairs for the given IDs in ONE round-trip.
//
// DIALECT: `m.qb.Placeholders(n)` yields `?` for SQLite and `$1..$n`
// for PostgreSQL.
//
// CHUNKING: the ID list is chunked at credentialIDQueryChunk so a
// pathological member count can never exceed the driver's parameter
// limit (SQLite: 999 by default). The normal case (a handful of
// members) is a SINGLE round-trip.
//
// LOCK-FREE with respect to m.mu — a direct query on m.store.DB (the
// same posture as scanModels).
func (m *ModelsManager) credentialProviderSnapshotForIDs(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}

	roundTrips := 0
	for start := 0; start < len(unique); start += credentialIDQueryChunk {
		end := start + credentialIDQueryChunk
		if end > len(unique) {
			end = len(unique)
		}
		chunk := unique[start:end]
		if len(chunk) == 0 {
			continue
		}
		query := fmt.Sprintf(`SELECT id, provider FROM credentials WHERE id IN (%s)`, m.qb.Placeholders(len(chunk)))
		args := make([]interface{}, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := m.store.DB.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, provider string
			if err := rows.Scan(&id, &provider); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan error: %w", err)
			}
			out[id] = provider
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("rows error: %w", err)
		}
		rows.Close()
		roundTrips++
		if credentialGroupProviderQueryHook != nil {
			credentialGroupProviderQueryHook(roundTrips)
		}
	}
	return out, nil
}

// credentialIDQueryChunk is the maximum number of IDs sent in a single
// `id IN (...)` round-trip. SQLite's default parameter limit is 999;
// keep a generous margin. A single group with more members than this
// chunk size costs ceil(n/chunk) round-trips — still bounded, and never
// one round-trip per member.
const credentialIDQueryChunk = 500

// credentialConfigSnapshotForIDs reads the credential rows (id + provider)
// for the given IDs WITHOUT taking m.mu.
//
// ONE round-trip (no N+1): the providers are read via
// credentialProviderSnapshotForIDs (an `id IN (...)` query). A read
// failure yields an EMPTY snapshot (the members are then simply absent,
// exactly as when a row is missing) — the caller validates against it.
//
// LOCK DISCIPLINE: the group write path holds m.mu.Lock(); a nested
// m.mu.RLock() would self-deadlock (Go's RWMutex is non-reentrant). The
// read is a direct query on m.store.DB — the same lock-free posture as
// scanModels.
func (m *ModelsManager) credentialConfigSnapshotForIDs(ids []string) map[string]models.CredentialConfig {
	ctx, cancel := context.WithTimeout(context.Background(), credentialGroupRebuildTimeout)
	defer cancel()

	providers, err := m.credentialProviderSnapshotForIDs(ctx, ids)
	out := make(map[string]models.CredentialConfig, len(providers))
	if err != nil {
		log.Printf("[WARN] [credential-groups] credential snapshot read failed (%v) — the member set is empty", err)
		return out
	}
	for id, provider := range providers {
		out[id] = models.CredentialConfig{ID: id, Provider: provider}
	}
	return out
}

// credentialProviderNoLock reads the persisted provider of a credential
// WITHOUT taking m.mu ("" when the row does not exist).
//
// LOCK DISCIPLINE: the credential write path holds m.mu.Lock(); a nested
// m.mu.RLock() would self-deadlock (Go's RWMutex is non-reentrant). The
// read is a direct query on m.store.DB — the same lock-free posture as
// scanModels.
func (m *ModelsManager) credentialProviderNoLock(id string) string {
	if id == "" {
		return ""
	}
	query := `SELECT provider FROM credentials WHERE id = ?`
	if m.store.Dialect == PostgreSQL {
		query = `SELECT provider FROM credentials WHERE id = $1`
	}
	var provider string
	if err := m.store.DB.QueryRowContext(context.Background(), query, id).Scan(&provider); err != nil {
		return ""
	}
	return provider
}

// credentialGroupSnapshot returns a COPY of the in-memory group map (the
// flatten authority). The copy lets a caller (the model write-path
// validator) hold the group set without keeping the lock.
func (m *ModelsManager) credentialGroupSnapshot() map[string]models.CredentialGroupConfig {
	m.groupsMu.RLock()
	defer m.groupsMu.RUnlock()

	out := make(map[string]models.CredentialGroupConfig, len(m.groups))
	for id, g := range m.groups {
		out[id] = g
	}
	return out
}
