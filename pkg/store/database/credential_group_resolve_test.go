package database

// credential-groups commission (phase1-plan.md WU-4) — the resolution
// seam pins: the E-3 single-group-ref trap (both affinity resolvers),
// the legacy 5-tuple resolver (A-1), the fail-closed missing/empty
// group behaviour, and the ZERO-DB contract of the cached resolver.

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	sqlite "modernc.org/sqlite"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
)

// ---------------------------------------------------------------------------
// A counting SQLite driver — the zero-DB pin needs a LITERAL statement
// count, not a proxy ("the DB was closed" only proves the happy path
// failed).
// ---------------------------------------------------------------------------

// countingStmtCount counts every statement that reaches the database
// through the counting driver.
var countingStmtCount int64

func init() {
	// Registering the same driver name twice panics, so the registration
	// happens exactly once (package init).
	sql.Register("sqlite-counting", &countingSQLiteDriver{
		inner:  &sqlite.Driver{},
		counts: &countingStmtCount,
	})
}

// countingSQLiteDriver wraps modernc's sqlite driver and counts the
// statements it executes.
type countingSQLiteDriver struct {
	inner  driver.Driver
	counts *int64
}

func (d *countingSQLiteDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingSQLiteConn{Conn: conn, counts: d.counts}, nil
}

type countingSQLiteConn struct {
	driver.Conn
	counts *int64
}

func (c *countingSQLiteConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	atomic.AddInt64(c.counts, 1)
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return q.QueryContext(ctx, query, args)
}

func (c *countingSQLiteConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	atomic.AddInt64(c.counts, 1)
	e, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return e.ExecContext(ctx, query, args)
}

func (c *countingSQLiteConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	atomic.AddInt64(c.counts, 1)
	p, ok := c.Conn.(driver.ConnPrepareContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	stmt, err := p.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &countingSQLiteStmt{Stmt: stmt, counts: c.counts}, nil
}

type countingSQLiteStmt struct {
	driver.Stmt
	counts *int64
}

func (s *countingSQLiteStmt) Exec(args []driver.Value) (driver.Result, error) {
	atomic.AddInt64(s.counts, 1)
	return s.Stmt.Exec(args)
}

func (s *countingSQLiteStmt) Query(args []driver.Value) (driver.Rows, error) {
	atomic.AddInt64(s.counts, 1)
	return s.Stmt.Query(args)
}

func (s *countingSQLiteStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	atomic.AddInt64(s.counts, 1)
	e, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return e.ExecContext(ctx, args)
}

func (s *countingSQLiteStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	atomic.AddInt64(s.counts, 1)
	q, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return q.QueryContext(ctx, args)
}

// newCountingStore opens a migrated SQLite store whose statements are
// counted (the zero-DB pin's instrument).
func newCountingStore(t *testing.T) (*Store, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "counting.db")
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite-counting", dsn)
	if err != nil {
		t.Fatalf("open counting sqlite: %v", err)
	}
	db.SetMaxOpenConns(10)

	store := &Store{DB: db, Dialect: SQLite, dbPath: dbPath}
	if err := store.RunMigrations(context.Background()); err != nil {
		store.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	return store, func() { store.Close() }
}

// ---------------------------------------------------------------------------
// The resolution seam pins
// ---------------------------------------------------------------------------

// TestCredentialGroup_ResolveAffinity_SingleGroupRefResolvesMember is
// the E-3 trap pin (phase1 §d.3): a model whose RAW credential list is a
// SINGLE group ref must resolve a MEMBER of that group — never the group
// id (an empty CredentialID under the locked representation → a
// credentials-table miss → ok=false → 502) and never an empty resolve.
//
// Both affinity resolvers are covered (the raw resolver and the cached
// twin).
func TestCredentialGroup_ResolveAffinity_SingleGroupRefResolvesMember(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	// A group with ONE member: the flattened list has exactly one ref,
	// so the ==1 fast path is taken — with the MEMBER's id, not the
	// group's.
	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g-one", Name: "G one", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m-one", Name: "M one", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g-one", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	res, ok := mgr.ResolveInternalConfigWithAffinity("m-one", "")
	if !ok {
		t.Fatal("ResolveInternalConfigWithAffinity ok = false, want true (E-3 trap: the group id must not be resolved as a credential)")
	}
	if res.CredentialID != "cred-A" {
		t.Fatalf("ResolveInternalConfigWithAffinity CredentialID = %q, want cred-A", res.CredentialID)
	}
	if res.Provider != "openai" {
		t.Fatalf("ResolveInternalConfigWithAffinity Provider = %q, want openai", res.Provider)
	}

	cached := mgr.GetModelNative("m-one")
	if cached == nil {
		t.Fatal("GetModelNative(m-one) = nil")
	}
	lookup := func(id string) (*models.CredentialConfig, bool) {
		c := mgr.GetCredential(id)
		if c == nil {
			return nil, false
		}
		return c, true
	}
	cres, cok := mgr.ResolveInternalConfigWithAffinityCached(cached, "", lookup)
	if !cok {
		t.Fatal("ResolveInternalConfigWithAffinityCached ok = false, want true (E-3 trap)")
	}
	if cres.CredentialID != "cred-A" {
		t.Fatalf("ResolveInternalConfigWithAffinityCached CredentialID = %q, want cred-A", cres.CredentialID)
	}

	// A group with TWO members: the flattened list has two refs, so the
	// engine path runs — and it still resolves a MEMBER.
	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g-two", Name: "G two", Provider: "openai", Members: []string{"cred-A", "cred-B"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m-two", Name: "M two", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g-two", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	res2, ok2 := mgr.ResolveInternalConfigWithAffinity("m-two", "")
	if !ok2 {
		t.Fatal("ResolveInternalConfigWithAffinity(m-two) ok = false, want true")
	}
	if res2.CredentialID != "cred-A" && res2.CredentialID != "cred-B" {
		t.Fatalf("ResolveInternalConfigWithAffinity(m-two) CredentialID = %q, want a group member", res2.CredentialID)
	}

	// Regression: a plain single-credential model resolves exactly as
	// before (the group-free path is byte-identical).
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m-plain", Name: "M plain", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{{CredentialID: "cred-A", Weight: 1, Position: 0}},
	}); err != nil {
		t.Fatalf("AddModel(plain): %v", err)
	}
	pres, pok := mgr.ResolveInternalConfigWithAffinity("m-plain", "")
	if !pok || pres.CredentialID != "cred-A" || pres.Provider != "openai" {
		t.Fatalf("plain model resolve = %+v (ok=%v), want cred-A/openai", pres, pok)
	}
}

// TestCredentialGroup_ResolveLegacy5Tuple_GroupPrimary is the A-1 pin:
// the legacy 5-tuple resolver must return the REAL provider for a
// group-primary model. The old code read PrimaryCredentialID() raw —
// empty for a group ref — and degraded the provider to "" / false (a
// SILENT failure: the only production consumer is normalizers.DetectProvider).
func TestCredentialGroup_ResolveLegacy5Tuple_GroupPrimary(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	provider, apiKey, baseURL, model, ok := mgr.ResolveInternalConfig("m1")
	if !ok {
		t.Fatal("ResolveInternalConfig ok = false, want true (A-1: the flat primary credential must be used)")
	}
	if provider != "openai" {
		t.Fatalf("ResolveInternalConfig provider = %q, want openai", provider)
	}
	if apiKey == "" {
		t.Fatal("ResolveInternalConfig apiKey is empty, want the member's key")
	}
	if baseURL == "" {
		t.Fatal("ResolveInternalConfig baseURL is empty, want the member's base URL")
	}
	if model != "gpt-x" {
		t.Fatalf("ResolveInternalConfig model = %q, want gpt-x", model)
	}
}

// TestCredentialGroup_Resolve_MissingGroupFailsClosed is the T-13b(b)
// pin at the resolution seam: a dangling group ref fails CLOSED (no
// silent wrong resolve) and the WARN names the missing group.
func TestCredentialGroup_Resolve_MissingGroupFailsClosed(t *testing.T) {
	mgr, store, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	// The group disappears (a direct SQL delete + rebuild — the
	// reconcile path).
	if _, err := store.DB.ExecContext(context.Background(),
		`DELETE FROM credential_groups WHERE id = ?`, "g1"); err != nil {
		t.Fatalf("direct SQL group delete: %v", err)
	}
	if err := mgr.RebuildCredentialGroupsFromDB(); err != nil {
		t.Fatalf("RebuildCredentialGroupsFromDB: %v", err)
	}

	buf := captureLog(t)
	res, ok := mgr.ResolveInternalConfigWithAffinity("m1", "")
	if ok {
		t.Fatalf("resolve ok = true (%+v), want false (fail closed on a missing group)", res)
	}
	if !strings.Contains(buf.String(), "g1") {
		t.Fatalf("log = %q, want a WARN naming the missing group 'g1'", buf.String())
	}
}

// TestCredentialGroup_Resolve_EmptyGroup_LegacyDegrade is the T-13b(c)
// pin: an empty group leaves the model with zero refs and the failure
// is OBSERVABLE — the WARN names the group and the resolver degrades to
// the legacy ok=false path (the 502 the caller sees).
func TestCredentialGroup_Resolve_EmptyGroup_LegacyDegrade(t *testing.T) {
	mgr, _, cleanup := setupCredGroupManager(t)
	defer cleanup()

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g-empty", Name: "Empty", Provider: "openai",
	}); err != nil {
		t.Fatalf("AddCredentialGroup(empty): %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g-empty", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	buf := captureLog(t)
	res, ok := mgr.ResolveInternalConfigWithAffinity("m1", "")
	if ok {
		t.Fatalf("resolve ok = true (%+v), want false (legacy degrade on a zero-ref model)", res)
	}
	if !strings.Contains(buf.String(), "g-empty") {
		t.Fatalf("log = %q, want a WARN naming the empty group 'g-empty'", buf.String())
	}

	// The cached twin degrades identically.
	cached := mgr.GetModelNative("m1")
	lookup := func(id string) (*models.CredentialConfig, bool) {
		c := mgr.GetCredential(id)
		if c == nil {
			return nil, false
		}
		return c, true
	}
	if _, cok := mgr.ResolveInternalConfigWithAffinityCached(cached, "", lookup); cok {
		t.Fatal("cached resolve ok = true, want false (legacy degrade on a zero-ref model)")
	}
}

// TestCredentialGroup_ResolveCached_ZeroDB is the T-23 pin (phase1
// §d.4): the cached resolver performs NO database statement when it
// flattens a group — the group map is in-memory.
func TestCredentialGroup_ResolveCached_ZeroDB(t *testing.T) {
	store, cleanup := newCountingStore(t)
	defer cleanup()

	mgr, err := NewModelsManager(store, nil)
	if err != nil {
		t.Fatalf("NewModelsManager: %v", err)
	}
	seedCredential(t, store, "cred-A", "openai")
	seedCredential(t, store, "cred-B", "openai")

	if err := mgr.AddCredentialGroup(models.CredentialGroupConfig{
		ID: "g1", Name: "G1", Provider: "openai", Members: []string{"cred-A", "cred-B"},
	}); err != nil {
		t.Fatalf("AddCredentialGroup: %v", err)
	}
	if err := mgr.AddModel(models.ModelConfig{
		ID: "m1", Name: "M1", Enabled: true, Internal: true, InternalModel: "gpt-x",
		Credentials: []models.CredentialRef{groupRef("g1", 1, 0)},
	}); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	cached := mgr.GetModelNative("m1")
	if cached == nil {
		t.Fatal("GetModelNative(m1) = nil")
	}
	// The decorator's credential lookup is served from ITS cache — it
	// never touches the DB either.
	creds := map[string]*models.CredentialConfig{}
	for _, id := range []string{"cred-A", "cred-B"} {
		c := mgr.GetCredential(id)
		if c == nil {
			t.Fatalf("GetCredential(%s) = nil", id)
		}
		creds[id] = c
	}
	lookup := func(id string) (*models.CredentialConfig, bool) {
		c, ok := creds[id]
		return c, ok
	}

	before := atomic.LoadInt64(&countingStmtCount)
	res, ok := mgr.ResolveInternalConfigWithAffinityCached(cached, "conv-1", lookup)
	after := atomic.LoadInt64(&countingStmtCount)

	if after != before {
		t.Fatalf("the cached resolver issued %d database statements, want 0 (zero-DB contract)", after-before)
	}
	if !ok {
		t.Fatal("ResolveInternalConfigWithAffinityCached ok = false, want true")
	}
	if res.CredentialID != "cred-A" && res.CredentialID != "cred-B" {
		t.Fatalf("ResolveInternalConfigWithAffinityCached CredentialID = %q, want a group member", res.CredentialID)
	}
}
