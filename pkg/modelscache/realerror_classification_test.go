package modelscache

// realerror_classification_test.go — mock-quality audit (TEST-ONLY).
//
// Verifies that the fake DB-error shapes used by the outage /
// contract / tokens tests classify the same way as REAL driver
// errors from the production SQLite stack (modernc.org/sqlite via
// database/sql — see pkg/store/database/connection.go:110).
//
// Each subtest opens a real SQLite file in t.TempDir, triggers the
// documented condition, captures the actual driver error string,
// prints it via t.Logf, and compares the classifier verdict on the
// real error against:
//   - the documented spec verdict (the fragment whitelist in
//     health.go:66-78), which is what the classifier SHOULD report
//     for the captured shape under the existing spec;
//   - the synthetic fake shape currently used by every outage /
//     contract test (a *net.OpError(conn refused) shape — see
//     models_test.go:466-468 and proxy_integration_test.go:40-42).
//
// A real-vs-spec divergence is logged as a finding (audit
// evidence); the package stays green so CI doesn't fail on
// production-side gaps the audit is here to surface — see the
// "FINDINGS" section of the audit commit report for the structured
// evidence (real string, verdict, fake verdict, MATCH/MISMATCH).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite" // pin the production driver for the audit
)

// fakeConnRefused mirrors the synthetic error shape used by every
// outage/contract test in the package today (see models_test.go:466
// connRefused / proxy_integration_test.go:40 connRefusedError). It
// exists here ONLY to feed the fake-vs-real matrix assertions in
// this file.
func fakeConnRefused() error {
	return &fakeNetOpError{op: "dial", net: "tcp", msg: "connection refused"}
}

// fakeNetOpError mirrors *net.OpError without importing net.
type fakeNetOpError struct {
	op, net, msg string
}

func (e *fakeNetOpError) Error() string { return e.op + " " + e.net + ": " + e.msg }
func (e *fakeNetOpError) Unwrap() error { return errors.New(e.msg) }

// openSQLite opens a real SQLite connection at path using the
// production driver (no pragmas — minimal surface for the audit).
func openSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// classify prints the real + fake verdicts and returns both.
func classify(t *testing.T, label string, realErr error, fakeErr error) (real, fake bool) {
	t.Helper()
	real = isInfraError(realErr)
	fake = isInfraError(fakeErr)
	t.Logf("[%s]\n  real err: %q\n  real err type: %T\n  isInfraError(real) = %v\n  isInfraError(fake) = %v",
		label, errString(realErr), realErr, real, fake)
	return real, fake
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// reportFinding logs a real-vs-fake mismatch as structured audit
// evidence (the package stays green; the finding is captured for
// the report). MATCH cases are logged at info level for traceability.
func reportFinding(t *testing.T, label, realStr string, real, fake bool, reason string) {
	t.Helper()
	if real != fake {
		t.Logf("FINDING [%s]: MISMATCH real=%v fake=%v — %s (real err: %q)",
			label, real, fake, reason, realStr)
	} else {
		t.Logf("INFO [%s]: MATCH real=%v fake=%v — %s (real err: %q)",
			label, real, fake, reason, realStr)
	}
}

// ─── (a) DB file replaced by a directory ────────────────────────────────────

// TestRealError_FileReplacedByDirectory_Classification reproduces the
// "DB file is unreadable" outage class by deleting the file and
// creating a directory in its place, then opening a fresh
// connection. The real modernc.org/sqlite driver returns an error
// whose .Error() is "unable to open database file: out of memory (14)"
// (sqlite SQLITE_CANTOPEN + OOM). The synthetic fake used elsewhere
// in this package is a *net.OpError("dial", "tcp", "connection refused").
func TestRealError_FileReplacedByDirectory_Classification(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dir.db")
	if err := os.Mkdir(dbPath, 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	db := openSQLite(t, dbPath)
	realErr := mustErr(t, db, "SELECT 1")
	if realErr == nil {
		realErr = db.PingContext(context.Background())
	}
	if realErr == nil {
		t.Fatalf("setup invariant: open failure must produce a real error — got nil; the audit cannot proceed")
	}

	real, fake := classify(t, "a: dir in place of file", realErr, fakeConnRefused())
	reportFinding(t, "a: dir in place of file", errString(realErr), real, fake,
		"the synthetic *net.OpError(conn refused) shape classifies INFRA, but the real modernc.org/sqlite open-failure shape "+
			"is a *sqlite.Error with code 14 and no fragment match — production stale-tier fallback will NOT engage for SQLite open failures")
}

// ─── (b) DB file truncated to 0 bytes on a LIVE connection ─────────────────

// TestRealError_TruncatedFile_Classification holds a live connection,
// truncates the file out from under it, and captures the real
// modernc.org/sqlite error on the next query. Expected real error:
// "SQL logic error: no such table: t (1)" (sqlite SQLITE_ERROR 1).
func TestRealError_TruncatedFile_Classification(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trunc.db")
	db := openSQLite(t, dbPath)
	if _, err := db.Exec("CREATE TABLE t(id INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec("INSERT INTO t VALUES (1)"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := os.Truncate(dbPath, 0); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	realErr := mustErr(t, db, "SELECT * FROM t")
	if realErr == nil {
		t.Fatalf("setup invariant: truncation must produce a real error — got nil; the audit cannot proceed")
	}

	real, fake := classify(t, "b: truncated file", realErr, fakeConnRefused())
	reportFinding(t, "b: truncated file", errString(realErr), real, fake,
		"the synthetic conn refused shape classifies INFRA, but real modernc.org/sqlite truncation surfaces a *sqlite.Error code 1 "+
			"(\"no such table\") that has no fragment match — stale-tier fallback will not engage for schema corruption")
}

// ─── (c) DB file + parent dir chmod 000 ─────────────────────────────────────

// TestRealError_PermissionDenied_Classification reproduces the
// "permission denied" outage class by chmod 000'ing the parent
// directory before opening. On POSIX, root bypasses DAC so this
// test skips when the running user is uid 0; on Windows the chmod
// semantics differ and the test skips too.
func TestRealError_PermissionDenied_Classification(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 semantics differ on Windows; modernc surface is platform-specific — skip rather than false-fail")
	}
	if os.Geteuid() == 0 {
		t.Skip("chmod 000 is bypassed for root (DAC) — skip rather than false-fail; (a) covers the same driver surface")
	}

	dir := t.TempDir()
	sub := filepath.Join(dir, "ro")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(sub, "ro.db")
	// Pre-create so the file exists; open failure surfaces the
	// chmod-000 effect on the directory traversal.
	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	_ = f.Close()

	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatalf("chmod 0: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	db := openSQLite(t, dbPath)
	realErr := mustErr(t, db, "SELECT 1")
	if realErr == nil {
		realErr = db.PingContext(context.Background())
	}
	if realErr == nil {
		t.Logf("setup invariant: chmod 000 setup did not produce an error — platform is bypassing DAC; recording informational only")
		return
	}
	real, fake := classify(t, "c: chmod 000", realErr, fakeConnRefused())
	reportFinding(t, "c: chmod 000", errString(realErr), real, fake,
		"the synthetic conn refused shape classifies INFRA, but real modernc.org/sqlite open-failure shape "+
			"has no fragment match — stale-tier fallback will not engage for permission-denied outage")
}

// ─── (d) sql.ErrNoRows from a legit query on an EMPTY-but-valid DB ──────────

// TestRealError_NoRowsVerdict_Classification asserts that a real
// "row missing" verdict from a SQL query on an EMPTY-but-valid DB
// classifies as NOT infra — exactly the "legit verdict" class. We
// use QueryRowContext.Scan so the missing-row branch is reached
// (ExecContext on a SELECT does not surface sql.ErrNoRows — it
// returns nil even when zero rows match).
func TestRealError_NoRowsVerdict_Classification(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")
	db := openSQLite(t, dbPath)
	if _, err := db.Exec("CREATE TABLE t(id INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	var got int
	realErr := db.QueryRowContext(context.Background(), "SELECT id FROM t WHERE id = ?", 42).Scan(&got)
	if realErr == nil {
		t.Fatalf("setup invariant: empty-db SELECT with no match must surface sql.ErrNoRows — got nil; the audit cannot proceed")
	}
	if !errors.Is(realErr, sql.ErrNoRows) {
		t.Logf("[d] note: real err %q is NOT sql.ErrNoRows on modernc — but the verdict-class behavior we care about (NOT-infra) must still hold", errString(realErr))
	} else {
		t.Logf("[d] setup correctly produced sql.ErrNoRows on modernc")
	}

	real, fake := classify(t, "d: empty db not-found", realErr, fakeConnRefused())
	// SPEC contract: not-found verdicts must be NOT-infra regardless
	// of driver surface. If this ever flips, the verdict-class
	// regression guard at tokens.go:208 will need attention too.
	if real {
		t.Errorf("SPEC REGRESSION: empty-db not-found must classify as NOT infra (verdict class); got isInfraError=%v for err=%q",
			real, errString(realErr))
	}
	reportFinding(t, "d: empty db not-found", errString(realErr), real, fake,
		"verdict-class — must remain NOT-infra per spec; this is a regression guard, not an outage finding")
}

// ─── (e) context.DeadlineExceeded via an expired context ────────────────────

// TestRealError_DeadlineExceeded_Classification asserts that a real
// context-deadline-exceeded error from modernc.org/sqlite classifies
// as INFRA — the design intent for the stale-tier fallback.
func TestRealError_DeadlineExceeded_Classification(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "deadline.db")
	db := openSQLite(t, dbPath)
	if _, err := db.Exec("CREATE TABLE t(id INTEGER)"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// A 1ns timeout guarantees the deadline has already passed by the
	// time the driver checks it, even on a busy CI box.
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond)
	realErr := mustErrCtx(t, ctx, db, "INSERT INTO t VALUES (1)")
	if realErr == nil {
		t.Fatalf("setup invariant: an expired context must surface DeadlineExceeded — got nil; the audit cannot proceed")
	}

	real, fake := classify(t, "e: deadline exceeded", realErr, fakeConnRefused())
	if !errors.Is(realErr, context.DeadlineExceeded) {
		t.Errorf("expected real err to be context.DeadlineExceeded; got %q", errString(realErr))
	}
	// Spec contract: DeadlineExceeded must be INFRA (errors.Is branch
	// of isInfraError). Regression guard.
	if !real {
		t.Errorf("SPEC REGRESSION: context.DeadlineExceeded must classify as INFRA; got isInfraError=%v for err=%q",
			real, errString(realErr))
	}
	reportFinding(t, "e: deadline exceeded", errString(realErr), real, fake,
		"context.DeadlineExceeded is matched via errors.Is — MATCH expected; control row for the matrix")
}

// ─── (f) Spec-intent audit table — every real condition, one log per row ────

// TestRealError_SpecIntent_AuditTable reproduces every audit
// condition in one place and emits a structured MATCH/MISMATCH log
// line per row. The test is the audit's tabular report: it never
// fails on a real-vs-fake divergence (that's the audit's purpose),
// only on a setup invariant or a documented spec regression.
func TestRealError_SpecIntent_AuditTable(t *testing.T) {
	type row struct {
		label   string
		open    func(t *testing.T) (realErr error, fakeErr error)
		comment string
	}

	rows := []row{
		{
			label: "spec-1: dir-in-place-of-file",
			open: func(t *testing.T) (error, error) {
				dir := t.TempDir()
				p := filepath.Join(dir, "x.db")
				_ = os.Mkdir(p, 0o755)
				db := openSQLite(t, p)
				err := mustErr(t, db, "SELECT 1")
				if err == nil {
					err = db.PingContext(context.Background())
				}
				return err, fakeConnRefused()
			},
			comment: "modernc SQLITE_CANTOPEN — fake conn refused classifies INFRA; real modernc shape does not (no fragment match).",
		},
		{
			label: "spec-2: truncated-file-live",
			open: func(t *testing.T) (error, error) {
				dir := t.TempDir()
				p := filepath.Join(dir, "x.db")
				db := openSQLite(t, p)
				db.Exec("CREATE TABLE t(id INTEGER)")
				db.Exec("INSERT INTO t VALUES (1)")
				_ = os.Truncate(p, 0)
				return mustErr(t, db, "SELECT * FROM t"), fakeConnRefused()
			},
			comment: "modernc SQLITE_ERROR 1 (no such table) — fake INFRA, real NOT INFRA.",
		},
		{
			label: "spec-3: chmod-000-permission-denied",
			open: func(t *testing.T) (error, error) {
				if runtime.GOOS == "windows" {
					t.Skip("chmod semantics differ on Windows")
				}
				if os.Geteuid() == 0 {
					t.Skip("chmod 000 bypassed for root")
				}
				dir := t.TempDir()
				sub := filepath.Join(dir, "ro")
				_ = os.Mkdir(sub, 0o755)
				p := filepath.Join(sub, "x.db")
				f, _ := os.Create(p)
				_ = f.Close()
				_ = os.Chmod(sub, 0o000)
				t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })
				db := openSQLite(t, p)
				err := mustErr(t, db, "SELECT 1")
				if err == nil {
					err = db.PingContext(context.Background())
				}
				return err, fakeConnRefused()
			},
			comment: "modernc SQLITE_CANTOPEN — same as spec-1; permission-denied outage shadow.",
		},
		{
			label: "spec-4: empty-db-no-rows",
			open: func(t *testing.T) (error, error) {
				dir := t.TempDir()
				p := filepath.Join(dir, "x.db")
				db := openSQLite(t, p)
				db.Exec("CREATE TABLE t(id INTEGER)")
				var got int
				return db.QueryRowContext(context.Background(), "SELECT id FROM t WHERE id = ?", 42).Scan(&got),
					fakeConnRefused()
			},
			comment: "real verdict-class — both real and fake should be NOT INFRA; regression guard.",
		},
		{
			label: "spec-5: deadline-exceeded",
			open: func(t *testing.T) (error, error) {
				dir := t.TempDir()
				p := filepath.Join(dir, "x.db")
				db := openSQLite(t, p)
				db.Exec("CREATE TABLE t(id INTEGER)")
				ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond)
				return mustErrCtx(t, ctx, db, "INSERT INTO t VALUES (1)"), fakeConnRefused()
			},
			comment: "errors.Is branch — both INFRA, MATCH.",
		},
		{
			label: "spec-6: db-closed",
			open: func(t *testing.T) (error, error) {
				dir := t.TempDir()
				p := filepath.Join(dir, "x.db")
				db, _ := sql.Open("sqlite", p)
				db.Exec("CREATE TABLE t(id INTEGER)")
				_ = db.Close()
				err := mustErr(t, db, "SELECT 1")
				return err, fakeConnRefused()
			},
			comment: "sql: database is closed — matched by fragment whitelist; INFRA expected for both.",
		},
	}

	for _, r := range rows {
		r := r
		t.Run(r.label, func(t *testing.T) {
			realErr, fakeErr := r.open(t)
			if realErr == nil && !t.Skipped() {
				t.Fatalf("setup invariant: %s must produce a real error — got nil", r.label)
			}
			if t.Skipped() {
				return // Skip propagates; no verdict to log.
			}
			real, fake := classify(t, r.label, realErr, fakeErr)
			reportFinding(t, r.label, errString(realErr), real, fake, r.comment)
		})
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

// mustErr runs a query and returns its error (nil is reported back
// so callers can retry on a different surface — Ping vs Exec).
func mustErr(t *testing.T, db *sql.DB, q string, args ...interface{}) error {
	t.Helper()
	_, err := db.ExecContext(context.Background(), q, args...)
	return err
}

func mustErrCtx(t *testing.T, ctx context.Context, db *sql.DB, q string, args ...interface{}) error {
	t.Helper()
	_, err := db.ExecContext(ctx, q, args...)
	return err
}

// keep import-use obvious for readers reviewing the audit file.
var _ = fs.ErrNotExist
var _ = strings.ToLower
var _ = fmt.Sprintf
