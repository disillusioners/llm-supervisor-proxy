package proxy

// P2 re-gate — Am25 metering dedup gate regression (counter-wired).
//
// The P2 green runs missed the missing group predicate because the
// group harness passed counter=nil (newGroupHarness). These tests
// wire a REAL *usage.Counter over SQLite (migration 025+029 table
// shapes) into the handler and pin the FOUR model_hourly_usage row
// shapes on the non-stream gate, plus the non-group fallback shape
// on the STREAM gate (both Am25 sites):
//
//   (a) Direct request            → exactly 1 row (the requested model)
//   (b) Day-1 self-shadow group   → exactly 1 row (coding[coding] onboarding)
//   (c) Multi-member group rewrite→ exactly 2 rows (group-name + member)
//   (d) NON-GROUP fallback        → exactly 1 row — the regression case:
//       without the rc.groupPick != nil predicate, B serving after A
//       fails emits a SECOND IncrementModelUsage(B) row and breaks the
//       first-class AC "zero-groups == today (metering)".
//
// The first-class AC is restored exactly (base behavior for non-group
// traffic); fallback-winner accounting as a FEATURE is an AC change
// reserved for the user's morning review — deliberately NOT tested
// here.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/models"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store/database"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/usage"

	_ "modernc.org/sqlite"
)

// newMeteringCounter builds a REAL *usage.Counter over a fresh
// file-backed SQLite (production DSN — WAL + busy_timeout, per
// connection.go:109 — so the async metering goroutines and the
// polling reads share one database across the pool) with the
// token_hourly_usage + model_hourly_usage tables (migration 025
// shape + migration 029's image_count column), mirroring how
// pkg/usage tests construct the counter. (A bare ":memory:" DSN is
// NOT safe here: each pooled connection gets its own empty memory
// DB and concurrent metering writes hit "no such table".)
func newMeteringCounter(t *testing.T) *usage.Counter {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "metering.db")
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for name, ddl := range map[string]string{
		"token_hourly_usage": `CREATE TABLE IF NOT EXISTS token_hourly_usage (
			token_id TEXT NOT NULL,
			hour_bucket TEXT NOT NULL,
			request_count INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			image_count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (token_id, hour_bucket)
		)`,
		"model_hourly_usage": `CREATE TABLE IF NOT EXISTS model_hourly_usage (
			model_id TEXT NOT NULL,
			hour_bucket TEXT NOT NULL,
			request_count INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			image_count INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (model_id, hour_bucket)
		)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	return usage.NewCounter(db, database.SQLite)
}

// newMeteringHarness is the counter-wired variant of newGroupHarness:
// the same real *database.ModelsManager + SQLite stack, but with a
// REAL *usage.Counter wired into the handler and a caller-supplied
// upstream handler (case (d) needs per-model fail/succeed). The
// existing counter-nil harness is deliberately untouched.
func newMeteringHarness(t *testing.T, upstream http.HandlerFunc) (*groupHarness, *usage.Counter) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("DATABASE_URL", "sqlite:"+filepath.Join(tmp, "test.db"))
	store1, err := database.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	bus := events.NewBus()
	_, mgr, err := database.InitializeManagers(store1, bus)
	if err != nil {
		store1.Close()
		t.Fatalf("InitializeManagers: %v", err)
	}
	up := httptest.NewServer(upstream)
	mgrCfg := newTestManagerWithConfig(t, up.URL)
	reqStore := store.NewRequestStore(100, store.WithMaxBytes(256<<20))
	counter := newMeteringCounter(t)
	// remove-external-upstream P5: member chat rows need a credential
	// (write-stamp forces Internal=true); point it at the stub upstream.
	if err := mgr.AddCredential(models.CredentialConfig{
		ID: p5FixtureCredID, Provider: "openai", APIKey: "test-key", BaseURL: up.URL,
	}); err != nil {
		t.Fatalf("AddCredential(%s): %v", p5FixtureCredID, err)
	}
	g := &groupHarness{mgr: mgr, h: NewHandler(&Config{ConfigMgr: mgrCfg, ModelsConfig: mgr}, bus, reqStore, nil, nil, counter, mgr.Engine()), up: up, bus: bus}
	g.clean = func() {
		up.Close()
		mgr.Close()
		store1.Close()
		_ = os.RemoveAll(tmp)
	}
	return g, counter
}

// meteringStubUpstream returns a stub OpenAI upstream: 502 for
// failModel ("" = never), a plain JSON 200 for non-stream requests,
// and a minimal SSE stream for stream requests.
func meteringStubUpstream(failModel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model, _ := body["model"].(string)
		if failModel != "" && model == failModel {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"stub fail"}}`))
			return
		}
		isStream := false
		if s, ok := body["stream"].(bool); ok {
			isStream = s
		}
		if !isStream {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(stubUpstreamBody("stub-model", "hi")))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		chunk, _ := json.Marshal(map[string]interface{}{
			"choices": []interface{}{map[string]interface{}{
				"delta": map[string]interface{}{"content": "hi"},
			}},
		})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// waitForModelUsageRows polls GetModelUsage until want DISTINCT model
// rows appear (metering writes are async goroutines with a detached
// 5s context), then settles briefly — long enough for any second row
// from the same metering block to land — and re-reads the final row
// set.
func waitForModelUsageRows(t *testing.T, counter *usage.Counter, want int) []usage.ModelHourlyUsageRow {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := counter.GetModelUsage(context.Background(), "2000-01-01T00", "2999-12-31T23")
		if err != nil {
			t.Fatalf("GetModelUsage: %v", err)
		}
		if len(rows) >= want {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // settle — member row fires in the same block
	rows, err := counter.GetModelUsage(context.Background(), "2000-01-01T00", "2999-12-31T23")
	if err != nil {
		t.Fatalf("GetModelUsage (settle): %v", err)
	}
	return rows
}

// assertModelUsageRows asserts the row set is EXACTLY the wanted
// model keys (order-insensitive; one row per key).
func assertModelUsageRows(t *testing.T, rows []usage.ModelHourlyUsageRow, wantKeys ...string) {
	t.Helper()
	got := map[string]int{}
	for _, r := range rows {
		got[r.ModelID] += r.RequestCount
	}
	if len(got) != len(wantKeys) {
		t.Fatalf("model_hourly_usage rows = %v, want exactly keys %v", got, wantKeys)
	}
	for _, k := range wantKeys {
		if got[k] != 1 {
			t.Fatalf("model_hourly_usage[%s] request_count = %d, want exactly 1 (rows=%v)", k, got[k], got)
		}
	}
}

// TestHandlerGroups_Am25_Metering_RowShapes — the counter-wired
// four-case regression (P2 re-gate). Each subtest asserts the EXACT
// model_hourly_usage row set after one successful request.
func TestHandlerGroups_Am25_Metering_RowShapes(t *testing.T) {
	t.Run("Direct_ExactlyOneRow", func(t *testing.T) {
		g, counter := newMeteringHarness(t, meteringStubUpstream(""))
		defer g.close()
		if err := g.mgr.AddModel(models.ModelConfig{
			ID: "solo", Name: "Solo", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs(p5FixtureCredID),
		}); err != nil {
			t.Fatalf("AddModel(solo): %v", err)
		}
		rec := httptest.NewRecorder()
		g.h.HandleChatCompletions(rec, makeChatRequest(t, map[string]interface{}{
			"model":    "solo",
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}))
		if rec.Code != http.StatusOK {
			t.Fatalf("direct: status=%d body=%s", rec.Code, rec.Body.String())
		}
		rows := waitForModelUsageRows(t, counter, 1)
		assertModelUsageRows(t, rows, "solo")
	})

	t.Run("Day1SelfShadow_ExactlyOneRow", func(t *testing.T) {
		g, counter := newMeteringHarness(t, meteringStubUpstream(""))
		defer g.close()
		seedSelfShadowGroup(t, g)
		rec := httptest.NewRecorder()
		g.h.HandleChatCompletions(rec, makeChatRequest(t, map[string]interface{}{
			"model":    "coding",
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}))
		if rec.Code != http.StatusOK {
			t.Fatalf("day-1 self-shadow: status=%d body=%s", rec.Code, rec.Body.String())
		}
		rows := waitForModelUsageRows(t, counter, 1)
		// The real chat row serves (Am22 first-pass) and its ID equals
		// the client string — exactly ONE row, keyed "coding".
		assertModelUsageRows(t, rows, "coding")
	})

	t.Run("MultiMemberGroup_ExactlyTwoRows", func(t *testing.T) {
		g, counter := newMeteringHarness(t, meteringStubUpstream(""))
		defer g.close()
		seedGroupWithFallbacks(t, g)
		rec := httptest.NewRecorder()
		g.h.HandleChatCompletions(rec, makeChatRequest(t, map[string]interface{}{
			"model":    "g-multi",
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}))
		if rec.Code != http.StatusOK {
			t.Fatalf("multi-member: status=%d body=%s", rec.Code, rec.Body.String())
		}
		rows := waitForModelUsageRows(t, counter, 2)
		// Two rows: the group-name row + the sticky member row. The
		// sticky pick is deterministic per conversation key but not
		// pinned to a specific member here — assert the member is one
		// of the group's members.
		got := map[string]int{}
		for _, r := range rows {
			got[r.ModelID] += r.RequestCount
		}
		if len(got) != 2 {
			t.Fatalf("multi-member rows = %v, want exactly 2 rows (group + member)", got)
		}
		if got["g-multi"] != 1 {
			t.Fatalf("group-name row missing/wrong: %v", got)
		}
		memberRows := 0
		for _, m := range []string{"m-a", "m-b", "m-c"} {
			if v, ok := got[m]; ok {
				if v != 1 {
					t.Fatalf("member row %s count = %d, want 1 (%v)", m, v, got)
				}
				memberRows++
			}
		}
		if memberRows != 1 {
			t.Fatalf("expected exactly 1 member row among m-a/m-b/m-c, got %d (%v)", memberRows, got)
		}
	})

	t.Run("BackupServed_ExactlyTwoRows_GroupMasked", func(t *testing.T) {
		// group-backup-members / C1 + D3 + D4: the group's ONLY
		// default fails terminally; the re-entry cascades to the
		// BACKUP member (C1). Metering must produce the SAME
		// dual-row shape as a default-served request (group row +
		// served-member row), and the masked response identity must
		// be the GROUP id — never the backup member's id.
		//
		// This leg is the backup-served counterpart of
		// MultiMemberGroup_ExactlyTwoRows (which covers
		// default-served only) and doubles as the C1 end-to-end
		// regression pin: without the cascade the request would
		// re-run the failed default and end 502.
		g, counter := newMeteringHarness(t, meteringStubUpstream("b-default"))
		defer g.close()
		for _, id := range []string{"b-default", "b-backup"} {
			if err := g.mgr.AddModel(models.ModelConfig{
				ID: id, Name: id, Enabled: true, Kind: models.KindChat,
				// InternalModel == the row ID so the stub's fail-model
				// comparison (which sees the UPSTREAM wire model)
				// targets this member. The metering assertions still
				// key on the row ID (winner.GetModelID()).
				Internal: true, InternalModel: id,
				Credentials: models.TestRefs(p5FixtureCredID),
			}); err != nil {
				t.Fatalf("AddModel(%s): %v", id, err)
			}
		}
		if err := g.mgr.AddModel(models.ModelConfig{
			ID: "g-backup", Name: "Backup Group", Enabled: true,
			Kind: models.KindGroup, Internal: true,
			ExcludeFromUltimateSwitching: true,
			Members: []models.GroupMember{
				{ModelID: "b-default", Weight: 1},
				{ModelID: "b-backup", Weight: 1, BackupOnly: true},
			},
		}); err != nil {
			t.Fatalf("AddModel(g-backup): %v", err)
		}

		rec := httptest.NewRecorder()
		g.h.HandleChatCompletions(rec, makeChatRequest(t, map[string]interface{}{
			"model":    "g-backup",
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}))
		if rec.Code != http.StatusOK {
			t.Fatalf("backup-served: status=%d body=%s (C1 cascade did not reach the backup)", rec.Code, rec.Body.String())
		}

		// D3 masking: the served member is internal; the client sees
		// the GROUP id only.
		var body map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if m, _ := body["model"].(string); m != "g-backup" {
			t.Errorf("masking: model = %q, want the group id \"g-backup\"", m)
		}

		rows := waitForModelUsageRows(t, counter, 2)
		got := map[string]int{}
		for _, r := range rows {
			got[r.ModelID] += r.RequestCount
		}
		// Exactly two rows: the group row + the BACKUP member row.
		if len(got) != 2 {
			t.Fatalf("backup-served rows = %v, want exactly 2 (group + backup member)", got)
		}
		if got["g-backup"] != 1 {
			t.Fatalf("group-name row missing/wrong: %v", got)
		}
		if got["b-backup"] != 1 {
			t.Fatalf("backup member row missing/wrong: %v", got)
		}
		if _, ok := got["b-default"]; ok {
			t.Fatalf("the failed default must not be metered: %v", got)
		}
	})

	t.Run("NonGroupFallback_ExactlyOneRow", func(t *testing.T) {
		// THE regression case: fb-a fails, fb-b serves — an ordinary
		// NON-group fallback. Base behavior (first-class AC
		// "zero-groups == today"): exactly ONE row, keyed the
		// client-requested model. Without the rc.groupPick != nil
		// predicate this emits a SECOND IncrementModelUsage("fb-b") row.
		g, counter := newMeteringHarness(t, meteringStubUpstream("fb-a"))
		defer g.close()
		if err := g.mgr.AddModel(models.ModelConfig{
			ID: "fb-a", Name: "FB-A", Enabled: true, Kind: models.KindChat,
			Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs(p5FixtureCredID), FallbackChain: []string{"fb-b"},
		}); err != nil {
			t.Fatalf("AddModel(fb-a): %v", err)
		}
		if err := g.mgr.AddModel(models.ModelConfig{
			ID: "fb-b", Name: "FB-B", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs(p5FixtureCredID),
		}); err != nil {
			t.Fatalf("AddModel(fb-b): %v", err)
		}
		rec := httptest.NewRecorder()
		g.h.HandleChatCompletions(rec, makeChatRequest(t, map[string]interface{}{
			"model":    "fb-a",
			"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
		}))
		if rec.Code != http.StatusOK {
			t.Fatalf("non-group fallback: status=%d body=%s", rec.Code, rec.Body.String())
		}
		rows := waitForModelUsageRows(t, counter, 1)
		// Exactly ONE row — keyed reqLog.Model ("fb-a"), NOT the
		// fallback winner. (The pre-fix code emitted a second row keyed
		// "fb-b" here.)
		assertModelUsageRows(t, rows, "fb-a")
	})
}

// TestHandlerGroups_Am25_Metering_StreamSite_NonGroupFallback pins the
// same non-group fallback shape on the STREAM gate (handler.go stream
// site): exactly ONE model_hourly_usage row keyed the client-requested
// model, even though the fallback winner's model ID differs.
func TestHandlerGroups_Am25_Metering_StreamSite_NonGroupFallback(t *testing.T) {
	g, counter := newMeteringHarness(t, meteringStubUpstream("fb-a"))
	defer g.close()
	if err := g.mgr.AddModel(models.ModelConfig{
		ID: "fb-a", Name: "FB-A", Enabled: true, Kind: models.KindChat,
		Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs(p5FixtureCredID), FallbackChain: []string{"fb-b"},
	}); err != nil {
		t.Fatalf("AddModel(fb-a): %v", err)
	}
	if err := g.mgr.AddModel(models.ModelConfig{
		ID: "fb-b", Name: "FB-B", Enabled: true, Kind: models.KindChat, Internal: true, InternalModel: "stub-model", Credentials: models.TestRefs(p5FixtureCredID),
	}); err != nil {
		t.Fatalf("AddModel(fb-b): %v", err)
	}
	rec := httptest.NewRecorder()
	g.h.HandleChatCompletions(rec, makeChatRequest(t, map[string]interface{}{
		"model":    "fb-a",
		"stream":   true,
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("stream non-group fallback: status=%d body=%s", rec.Code, rec.Body.String())
	}
	rows := waitForModelUsageRows(t, counter, 1)
	assertModelUsageRows(t, rows, "fb-a")
}
