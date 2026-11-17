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
	"sort"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

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
// visible here but invisible to the flatten until RebuildCredentialGroupsFromDB
// runs (see the refresh rule on that method).
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
	if existing, err := m.getCredentialGroupRow(context.Background(), g.ID); err == nil && existing != nil {
		return fmt.Errorf("credential group '%s' already exists", g.ID)
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
// cache. Caller holds m.mu (write).
func (m *ModelsManager) removeCredentialGroupFromCache(id string) {
	m.groupsMu.Lock()
	defer m.groupsMu.Unlock()

	delete(m.groups, id)
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
		log.Printf("[WARN] [credential-groups] model scan failed while computing group usage (%v) — the used_by map is empty", err)
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

// FlattenCredentialRefsByID is the ID-keyed preview accessor: it reads
// the model row and returns its flattened credential list (nil for an
// unknown model). Manager-only — it is NOT part of the
// models.ModelsConfigInterface surface (C3 grows the interface with the
// eight locked methods).
//
// LOCKING: m.mu.RLock for the row read, then groupsMu.RLock inside the
// flatten — the m.mu → groupsMu order holds.
func (m *ModelsManager) FlattenCredentialRefsByID(modelID string) []models.CredentialRef {
	m.mu.RLock()
	row := m.getModelRowByID(modelID)
	m.mu.RUnlock()

	if row == nil {
		return nil
	}
	return m.FlattenCredentialRefs(row)
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
// REFRESH RULE (T-13b): the store-side group map has NO reconcile of its
// own — a group written by DIRECT SQL is invisible to the flatten until
// this method runs (or the process restarts). C3's decorator reconcile
// (<=60s) calls this method; until then such a group fails closed (WARN
// + no members expanded).
func (m *ModelsManager) RebuildCredentialGroupsFromDB() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.rebuildCredentialGroupsCache(context.Background()); err != nil {
		return err
	}
	m.fanOutCredentialGroupsLocked()
	return nil
}

// rebuildCredentialGroupsCache replaces the in-memory group cache (and
// the member-provider snapshot) from the DB. The DB reads happen BEFORE
// the swap so the lock is held only for the map replacement.
//
// The caller must NOT hold m.mu if it intends to take it later — this
// helper takes groupsMu only (lock order m.mu → groupsMu is respected
// because no m.mu is taken here at all).
func (m *ModelsManager) rebuildCredentialGroupsCache(ctx context.Context) error {
	groups, err := m.scanCredentialGroups(ctx)
	if err != nil {
		return err
	}

	memberIDs := make([]string, 0, len(groups))
	for _, g := range groups {
		memberIDs = append(memberIDs, g.Members...)
	}
	members := m.credentialConfigSnapshotForIDs(memberIDs)

	providers := make(map[string]string, len(members))
	for id, c := range members {
		providers[id] = c.Provider
	}
	byID := make(map[string]models.CredentialGroupConfig, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
	}

	m.groupsMu.Lock()
	m.groups = byID
	m.groupMemberProviders = providers
	m.groupsMu.Unlock()
	return nil
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
		log.Printf("[WARN] [credential-groups] fan-out for group '%s' failed — model scan error: %v", groupID, err)
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

// fanOutCredentialGroupsLocked refreshes the engine state of EVERY model
// that references ANY group. Used after a wholesale cache rebuild (the
// direct-SQL heal path). Caller holds m.mu (write).
func (m *ModelsManager) fanOutCredentialGroupsLocked() {
	modelList, err := m.scanModels(m.qb.GetAllModels())
	if err != nil {
		log.Printf("[WARN] [credential-groups] fan-out failed — model scan error: %v", err)
		return
	}
	for i := range modelList {
		mc := &modelList[i]
		if !modelHasCredentialGroupRef(mc) {
			continue
		}
		flat := m.FlattenCredentialRefs(mc)
		m.engine.OnModelChanged(mc.ID, flat)
		m.publishCredentialsChanged(mc.ID)
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

// credentialConfigSnapshotForIDs reads the credential rows (id + provider)
// for the given IDs WITHOUT taking m.mu.
//
// LOCK DISCIPLINE: the group write path holds m.mu.Lock(); a nested
// m.mu.RLock() would self-deadlock (Go's RWMutex is non-reentrant). The
// read is a direct query on m.store.DB — the same lock-free posture as
// scanModels. A missing row is simply absent from the result.
func (m *ModelsManager) credentialConfigSnapshotForIDs(ids []string) map[string]models.CredentialConfig {
	out := make(map[string]models.CredentialConfig, len(ids))
	if len(ids) == 0 {
		return out
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		query := `SELECT id, provider FROM credentials WHERE id = ?`
		if m.store.Dialect == PostgreSQL {
			query = `SELECT id, provider FROM credentials WHERE id = $1`
		}
		var rowID, provider string
		if err := m.store.DB.QueryRowContext(context.Background(), query, id).Scan(&rowID, &provider); err != nil {
			continue
		}
		out[rowID] = models.CredentialConfig{ID: rowID, Provider: provider}
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
