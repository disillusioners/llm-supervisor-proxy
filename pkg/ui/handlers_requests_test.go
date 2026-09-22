package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// =============================================================================
// Mock RequestStore + test harness for handleRequests / handleRequestDetail
// =============================================================================
//
// P0-1 (FE API payload & RAM incident fix) — these tests exercise the
// metadata-only projection + pagination on /fe/api/requests. The current
// handler is a ~3-line pass-through to s.store.List() + json.Encode, so
// the test surface for it is brand new. We construct a Server with a
// real *store.RequestStore (the package is in the same module) and
// register only the two handlers under test — no DB, no bus, no
// buffer store, no token store. The other handlers stay unregistered
// so the mux 404s on anything else.

type requestsTestServer struct {
	*Server
	store *store.RequestStore
}

func newRequestsTestServer(t *testing.T) *requestsTestServer {
	t.Helper()
	rs := store.NewRequestStore(100)
	s := &Server{
		store: rs,
	}
	return &requestsTestServer{
		Server: s,
		store:  rs,
	}
}

func (ts *requestsTestServer) serve() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/requests", ts.handleRequests)
	mux.HandleFunc("/fe/api/requests/", ts.handleRequestDetail)
	return httptest.NewServer(mux)
}

// makeRequestWithContent constructs a RequestLog with the given id/model/status/appTag
// and `nMessages` messages whose content is roughly `contentSize` bytes each so we
// can predict the approximate total size.
func makeRequestWithContent(id, model, status, appTag string, nMessages int, contentSize int) *store.RequestLog {
	msgs := make([]store.Message, nMessages)
	for i := 0; i < nMessages; i++ {
		msgs[i] = store.Message{
			Role:    "user",
			Content: strings.Repeat("x", contentSize),
		}
	}
	return &store.RequestLog{
		ID:        id,
		Status:    status,
		Model:     model,
		StartTime: time.Now().Add(-time.Hour),
		EndTime:   time.Now(),
		Duration:  "1h",
		Messages:  msgs,
		Retries:   0,
		AppTag:    appTag,
		Usage: &store.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
}

// =============================================================================
// P0-1 (a) — default projection: no `messages`, has `message_count` + `total_size_bytes`
// =============================================================================

func newRequestsTestServerWithMaxSize(t *testing.T, maxSize int) *requestsTestServer {
	t.Helper()
	rs := store.NewRequestStore(maxSize)
	s := &Server{
		store: rs,
	}
	return &requestsTestServer{
		Server: s,
		store:  rs,
	}
}

func TestHandleRequests_DefaultProjection_NoMessagesField(t *testing.T) {
	ts := newRequestsTestServer(t)
	ts.store.Add(makeRequestWithContent("req-1", "model-a", "completed", "app-a", 5, 100))
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Response is a JSON array at top level (no envelope).
	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("response is not a JSON array: %v\nbody=%s", err, body)
	}
	if len(arr) != 1 {
		t.Fatalf("len(arr) = %d, want 1", len(arr))
	}

	item := arr[0]
	if _, ok := item["messages"]; ok {
		t.Errorf("default projection must omit `messages` field; got item=%v", item)
	}
	if _, ok := item["message_count"]; !ok {
		t.Errorf("default projection must include `message_count`; got item=%v", item)
	}
	if _, ok := item["total_size_bytes"]; !ok {
		t.Errorf("default projection must include `total_size_bytes`; got item=%v", item)
	}

	// message_count and total_size_bytes must be ints (JSON numbers).
	mc, ok := item["message_count"].(float64)
	if !ok {
		t.Fatalf("message_count is not a number: %T", item["message_count"])
	}
	if int(mc) != 5 {
		t.Errorf("message_count = %v, want 5", mc)
	}
	tsz, ok := item["total_size_bytes"].(float64)
	if !ok {
		t.Fatalf("total_size_bytes is not a number: %T", item["total_size_bytes"])
	}
	if tsz <= 0 {
		t.Errorf("total_size_bytes = %v, want > 0 (5 msgs × 100 bytes)", tsz)
	}
}

func TestHandleRequests_DefaultProjection_KeepsIdentityFields(t *testing.T) {
	ts := newRequestsTestServer(t)
	ts.store.Add(&store.RequestLog{
		ID:              "req-keep",
		Status:          "completed",
		Model:           "model-x",
		StartTime:       time.Unix(1700000000, 0).UTC(),
		EndTime:         time.Unix(1700000060, 0).UTC(),
		Duration:        "1m0s",
		Retries:         2,
		Error:           "boom",
		Usage:           &store.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		TokenID:         "tok-1",
		TokenName:       "demo",
		OriginalModel:   "model-orig",
		FallbackUsed:    []string{"fb-1"},
		IsStream:        true,
		AppTag:          "my-app",
		UltimateModelID: "ult-1",
		Messages:        []store.Message{{Role: "user", Content: "hello world"}},
	})
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, body)
	}
	if len(arr) != 1 {
		t.Fatalf("len(arr) = %d, want 1", len(arr))
	}
	item := arr[0]

	wantStrings := map[string]string{
		"id":                "req-keep",
		"status":            "completed",
		"model":             "model-x",
		"duration":          "1m0s",
		"error":             "boom",
		"token_id":          "tok-1",
		"token_name":        "demo",
		"original_model":    "model-orig",
		"app_tag":           "my-app",
		"ultimate_model_id": "ult-1",
	}
	for k, want := range wantStrings {
		got, ok := item[k].(string)
		if !ok {
			t.Errorf("item[%q] missing or not a string: %v", k, item[k])
			continue
		}
		if got != want {
			t.Errorf("item[%q] = %q, want %q", k, got, want)
		}
	}
	if got := item["retries"]; got != float64(2) {
		t.Errorf("retries = %v, want 2", got)
	}
	if got := item["is_stream"]; got != true {
		t.Errorf("is_stream = %v, want true", got)
	}

	// parameters must also be omitted by default (heavy fields stripped).
	if _, ok := item["parameters"]; ok {
		t.Errorf("default projection must omit `parameters`; got item=%v", item)
	}
}

// =============================================================================
// P0-1 (b) — ?include=messages returns the FULL current shape (legacy parity)
// =============================================================================

func TestHandleRequests_IncludeMessagesReturnsFullShape(t *testing.T) {
	ts := newRequestsTestServer(t)
	req := makeRequestWithContent("req-1", "model-a", "completed", "app-a", 3, 10)
	req.Parameters = map[string]interface{}{"temperature": 0.7}
	ts.store.Add(req)
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests?include=messages")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, body)
	}
	if len(arr) != 1 {
		t.Fatalf("len(arr) = %d, want 1", len(arr))
	}
	item := arr[0]

	// `messages` MUST be present when include=messages.
	msgs, ok := item["messages"].([]interface{})
	if !ok {
		t.Fatalf("include=messages must return messages; got item=%v", item)
	}
	if len(msgs) != 3 {
		t.Errorf("len(messages) = %d, want 3", len(msgs))
	}
	// `parameters` MUST also be present (legacy parity).
	if _, ok := item["parameters"]; !ok {
		t.Errorf("include=messages must include parameters; got item=%v", item)
	}
	// Identity fields still preserved.
	if item["id"] != "req-1" {
		t.Errorf("id = %v, want req-1", item["id"])
	}
	if item["app_tag"] != "app-a" {
		t.Errorf("app_tag = %v, want app-a", item["app_tag"])
	}
}

// =============================================================================
// P0-1 (c) — existing app tag filter still works
// =============================================================================

func TestHandleRequests_AppFilterStillWorks_DefaultProjection(t *testing.T) {
	ts := newRequestsTestServer(t)
	ts.store.Add(makeRequestWithContent("r1", "m", "completed", "alpha", 2, 10))
	ts.store.Add(makeRequestWithContent("r2", "m", "completed", "beta", 2, 10))
	ts.store.Add(makeRequestWithContent("r3", "m", "completed", "alpha", 2, 10))
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests?app=alpha")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 2 {
		t.Fatalf("len(arr) = %d, want 2 (alpha tag only)", len(arr))
	}
	for _, item := range arr {
		if item["app_tag"] != "alpha" {
			t.Errorf("non-alpha tag leaked through filter: %v", item)
		}
	}
}

// =============================================================================
// P0-1 (d) — pagination: limit default 50, cap 200; offset default 0
// =============================================================================

func TestHandleRequests_Pagination_DefaultLimitClips(t *testing.T) {
	ts := newRequestsTestServer(t)
	for i := 0; i < 60; i++ {
		ts.store.Add(makeRequestWithContent(fmt.Sprintf("r-%d", i), "m", "completed", "", 1, 5))
	}
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 50 {
		t.Errorf("len(arr) = %d, want 50 (default limit)", len(arr))
	}
}

func TestHandleRequests_Pagination_RespectsLimit(t *testing.T) {
	ts := newRequestsTestServer(t)
	for i := 0; i < 10; i++ {
		ts.store.Add(makeRequestWithContent(fmt.Sprintf("r-%d", i), "m", "completed", "", 1, 5))
	}
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests?limit=3")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 3 {
		t.Errorf("len(arr) = %d, want 3", len(arr))
	}
}

func TestHandleRequests_Pagination_LimitCappedAt200(t *testing.T) {
	ts := newRequestsTestServer(t)
	for i := 0; i < 250; i++ {
		ts.store.Add(makeRequestWithContent(fmt.Sprintf("r-%d", i), "m", "completed", "", 1, 5))
	}
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests?limit=10000")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 100 {
		// Only 100 stored; cap=200 means cap allows up to 200 but the
		// store only holds 100. So we should get all 100.
		t.Errorf("len(arr) = %d, want 100 (store has 100, cap=200 doesn't truncate further)", len(arr))
	}
}

func TestHandleRequests_Pagination_LimitSanitized_NonNumericDefaults(t *testing.T) {
	ts := newRequestsTestServer(t)
	for i := 0; i < 80; i++ {
		ts.store.Add(makeRequestWithContent(fmt.Sprintf("r-%d", i), "m", "completed", "", 1, 5))
	}
	server := ts.serve()
	defer server.Close()

	for _, q := range []string{"limit=abc", "limit=-5", "limit=0"} {
		resp, err := http.Get(server.URL + "/fe/api/requests?" + q)
		if err != nil {
			t.Fatalf("GET failed: %v", err)
		}
		body, _ := readAll(resp.Body)
		resp.Body.Close()
		var arr []map[string]interface{}
		if err := json.Unmarshal(body, &arr); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(arr) != 50 {
			t.Errorf("%s → len(arr) = %d, want 50 (default)", q, len(arr))
		}
	}
}

func TestHandleRequests_Pagination_OffsetSkips(t *testing.T) {
	ts := newRequestsTestServer(t)
	for i := 0; i < 10; i++ {
		ts.store.Add(makeRequestWithContent(fmt.Sprintf("r-%d", i), "m", "completed", "", 1, 5))
	}
	server := ts.serve()
	defer server.Close()

	// store returns newest-first; the LAST 5 added were r-5..r-9.
	// With offset=5 the page should be the 5 OLDEST items: r-0..r-4.
	resp, err := http.Get(server.URL + "/fe/api/requests?limit=10&offset=5")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 5 {
		t.Fatalf("len(arr) = %d, want 5", len(arr))
	}
	// Items are returned newest-first from store.List(); offset=5 skips
	// the 5 newest (r-9,r-8,r-7,r-6,r-5), so we should see r-4 first.
	if arr[0]["id"] != "r-4" {
		t.Errorf("arr[0].id = %v, want r-4 (offset 5 skips 5 newest)", arr[0]["id"])
	}
	if arr[4]["id"] != "r-0" {
		t.Errorf("arr[4].id = %v, want r-0", arr[4]["id"])
	}
}

func TestHandleRequests_Pagination_OffsetNegativeClampedToZero(t *testing.T) {
	ts := newRequestsTestServer(t)
	for i := 0; i < 5; i++ {
		ts.store.Add(makeRequestWithContent(fmt.Sprintf("r-%d", i), "m", "completed", "", 1, 5))
	}
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests?offset=-100&limit=3")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 3 {
		t.Fatalf("len(arr) = %d, want 3", len(arr))
	}
	if arr[0]["id"] != "r-4" {
		t.Errorf("arr[0].id = %v, want r-4 (offset=-100 should clamp to 0)", arr[0]["id"])
	}
}

// =============================================================================
// P0-1 (legacy parity) — no limit/offset AND include=messages → return ALL
// =============================================================================

func TestHandleRequests_IncludeMessages_NoPaginationParams_ReturnsAll(t *testing.T) {
	ts := newRequestsTestServer(t)
	for i := 0; i < 80; i++ {
		ts.store.Add(makeRequestWithContent(fmt.Sprintf("r-%d", i), "m", "completed", "", 2, 10))
	}
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests?include=messages")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 80 {
		t.Errorf("len(arr) = %d, want 80 (legacy parity — no pagination with include=messages)", len(arr))
	}
	// messages must still be in payload.
	item := arr[0]
	if _, ok := item["messages"]; !ok {
		t.Errorf("include=messages must include messages; got item=%v", item)
	}
}

func TestHandleRequests_IncludeMessages_WithLimitParams_HonorsLimit(t *testing.T) {
	// When both include=messages AND limit are present, the limit wins
	// (pagination applies regardless of include mode).
	ts := newRequestsTestServer(t)
	for i := 0; i < 20; i++ {
		ts.store.Add(makeRequestWithContent(fmt.Sprintf("r-%d", i), "m", "completed", "", 1, 5))
	}
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests?include=messages&limit=5")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 5 {
		t.Errorf("len(arr) = %d, want 5 (limit wins even with include=messages)", len(arr))
	}
	if _, ok := arr[0]["messages"]; !ok {
		t.Errorf("messages must still be present (include=messages honored); got item=%v", arr[0])
	}
}

// =============================================================================
// Method-not-allowed regression
// =============================================================================

func TestHandleRequests_MethodNotAllowed(t *testing.T) {
	ts := newRequestsTestServer(t)
	server := ts.serve()
	defer server.Close()

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/fe/api/requests", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// =============================================================================
// handleRequestDetail still serves the full payload (incl. messages)
// =============================================================================

func TestHandleRequestDetail_FullPayloadWithMessages(t *testing.T) {
	ts := newRequestsTestServer(t)
	ts.store.Add(makeRequestWithContent("req-1", "model-a", "completed", "app-a", 3, 10))
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests/req-1")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := readAll(resp.Body)
	var item map[string]interface{}
	if err := json.Unmarshal(body, &item); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if item["id"] != "req-1" {
		t.Errorf("id = %v, want req-1", item["id"])
	}
	msgs, ok := item["messages"].([]interface{})
	if !ok {
		t.Fatalf("messages missing; got item=%v", item)
	}
	if len(msgs) != 3 {
		t.Errorf("len(messages) = %d, want 3", len(msgs))
	}
}

func TestHandleRequestDetail_NotFound(t *testing.T) {
	ts := newRequestsTestServer(t)
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests/does-not-exist")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// =============================================================================
// GET /fe/api/requests/{id}/summary — single-request metadata projection
// =============================================================================
//
// Used by the FE to insert brand-new rows arriving via SSE
// request_started events without triggering a full list refetch. Shape
// matches a single element of the default handleRequests() output:
//   - NO `messages` field (heavy)
//   - HAS `message_count` (int) + `total_size_bytes` (int)
//
// Tests cover:
//   (a) found case: payload shape matches single-summary contract
//   (b) 404 case: unknown id → 404
//   (c) dispatch sanity: /summary suffix is stripped from the id path
//       so an id containing the literal substring "summary" is NOT
//       misrouted to the summary handler.
//   (d) bare /fe/api/requests/ (no id, no /summary) stays a 404 from
//       handleRequestDetail, not the summary handler — confirms the
//       dispatcher routes correctly.

func (ts *requestsTestServer) serveWithSummary() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/fe/api/requests", ts.handleRequests)
	mux.HandleFunc("/fe/api/requests/", ts.handleRequestDetailOrSummary)
	return httptest.NewServer(mux)
}

func TestHandleRequestSummary_Found(t *testing.T) {
	ts := newRequestsTestServer(t)
	req := makeRequestWithContent("req-1", "model-a", "completed", "app-a", 7, 50)
	req.Parameters = map[string]interface{}{"temperature": 0.7}
	ts.store.Add(req)
	server := ts.serveWithSummary()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests/req-1/summary")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := readAll(resp.Body)
	var item map[string]interface{}
	if err := json.Unmarshal(body, &item); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, body)
	}

	// MUST NOT contain `messages` (heavy field stripped).
	if _, ok := item["messages"]; ok {
		t.Errorf("summary must omit `messages` field; got item=%v", item)
	}
	// MUST NOT contain `parameters` (heavy field stripped).
	if _, ok := item["parameters"]; ok {
		t.Errorf("summary must omit `parameters` field; got item=%v", item)
	}
	// MUST contain message_count (int) and total_size_bytes (int).
	mc, ok := item["message_count"]
	if !ok {
		t.Fatalf("summary must include message_count; got item=%v", item)
	}
	mcf, ok := mc.(float64)
	if !ok {
		t.Fatalf("message_count must be a number; got %T (%v)", mc, mc)
	}
	if int(mcf) != 7 {
		t.Errorf("message_count = %v, want 7", mcf)
	}
	tsz, ok := item["total_size_bytes"]
	if !ok {
		t.Fatalf("summary must include total_size_bytes; got item=%v", item)
	}
	tszf, ok := tsz.(float64)
	if !ok {
		t.Fatalf("total_size_bytes must be a number; got %T (%v)", tsz, tsz)
	}
	if tszf <= 0 {
		t.Errorf("total_size_bytes = %v, want > 0 (7 msgs × 50 bytes)", tszf)
	}
	// Identity fields preserved.
	if item["id"] != "req-1" {
		t.Errorf("id = %v, want req-1", item["id"])
	}
	if item["model"] != "model-a" {
		t.Errorf("model = %v, want model-a", item["model"])
	}
	if item["status"] != "completed" {
		t.Errorf("status = %v, want completed", item["status"])
	}
	if item["app_tag"] != "app-a" {
		t.Errorf("app_tag = %v, want app-a", item["app_tag"])
	}
}

func TestHandleRequestSummary_NotFound(t *testing.T) {
	ts := newRequestsTestServer(t)
	server := ts.serveWithSummary()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests/does-not-exist/summary")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleRequestSummary_MethodNotAllowed(t *testing.T) {
	ts := newRequestsTestServer(t)
	ts.store.Add(makeRequestWithContent("req-1", "m", "completed", "", 1, 5))
	server := ts.serveWithSummary()
	defer server.Close()

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/fe/api/requests/req-1/summary", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// =============================================================================
// #7 — regression tests
// =============================================================================
//
// (a) 200-limit clamp end-to-end: a store with >200 entries must
//     return exactly 200 when no limit param is supplied (the
//     parsePagination default+clamp chain).
// (b) byte-level golden parity: ?include=messages output must be
//     BYTE-FOR-BYTE identical to the legacy full-shape JSON so
//     external scripts and serialization-sensitive clients don't
//     break.

func TestHandleRequests_Pagination_ClampAt200_FromOversizedStore(t *testing.T) {
	// Use a store with maxSize > 200 so the parsePagination 200-cap is
	// the binding constraint, not the store's count cap. With a 300-
	// entry store and ?limit=50000, the handler must clamp to 200
	// (not 300 and not 50000). The "clamp path" specifically means
	// "limit param larger than cap is clamped down to cap" — the
	// no-limit path uses default 50, not 200, so it doesn't exercise
	// the clamp. This test verifies the cap, not the default.
	ts := newRequestsTestServerWithMaxSize(t, 300)
	const n = 250
	for i := 0; i < n; i++ {
		ts.store.Add(makeRequestWithContent(fmt.Sprintf("r-%04d", i), "m", "completed", "", 1, 5))
	}
	server := ts.serve()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests?limit=50000")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 200 {
		t.Errorf("len(arr) = %d, want 200 (maxListLimit clamp); store has %d, requested limit=50000", len(arr), n)
	}
}

func TestHandleRequests_Pagination_ClampAt200_ExplicitLimitWayAboveCap(t *testing.T) {
	ts := newRequestsTestServerWithMaxSize(t, 300)
	for i := 0; i < 250; i++ {
		ts.store.Add(makeRequestWithContent(fmt.Sprintf("r-%04d", i), "m", "completed", "", 1, 5))
	}
	server := ts.serve()
	defer server.Close()

	// ?limit=50000 must clamp to 200.
	resp, err := http.Get(server.URL + "/fe/api/requests?limit=50000")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := readAll(resp.Body)
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(arr) != 200 {
		t.Errorf("len(arr) = %d, want 200 (clamped from 50000)", len(arr))
	}
}

// goldenRequest constructs a RequestLog with deterministic fields and
// stable JSON output. Used to verify the legacy full-shape wire output
// is BYTE-IDENTICAL between handleRequests (?include=messages) and the
// historical behavior (json.NewEncoder.Encode(*RequestLog)).
func goldenRequest() *store.RequestLog {
	return &store.RequestLog{
		ID:               "golden-1",
		Status:           "completed",
		Model:            "test-model",
		StartTime:        time.Unix(1700000000, 0).UTC(),
		EndTime:          time.Unix(1700000060, 0).UTC(),
		Duration:         "1m0s",
		Retries:          0,
		Usage:            &store.Usage{PromptTokens: 11, CompletionTokens: 22, TotalTokens: 33},
		TokenID:          "tok-1",
		TokenName:        "demo",
		OriginalModel:    "orig-model",
		FallbackUsed:     []string{"fb-1"},
		UltimateModelUsed: false,
		UltimateModelID:  "",
		IsStream:         true,
		Parameters:       map[string]interface{}{"temperature": 0.7},
		AppTag:           "golden-app",
		Messages: []store.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		},
	}
}

func TestHandleRequests_IncludeMessages_ByteLevelGoldenParity(t *testing.T) {
	ts := newRequestsTestServer(t)
	ts.store.Add(goldenRequest())
	server := ts.serve()
	defer server.Close()

	// 1. Capture the wire output from /fe/api/requests?include=messages
	resp, err := http.Get(server.URL + "/fe/api/requests?include=messages")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	wireBody, _ := readAll(resp.Body)

	// 2. Compute the EXPECTED output by encoding the same RequestLog
	//    through json.Encoder (the same path the handler takes). This
	//    is the "golden" we want to match byte-for-byte — any drift
	//    here means the handler is doing something different from the
	//    legacy encode path (custom MarshalJSON, manual field
	//    reordering, etc.).
	expectedReq := goldenRequest()
	var expectedBuf bytes.Buffer
	if err := json.NewEncoder(&expectedBuf).Encode([]*store.RequestLog{expectedReq}); err != nil {
		t.Fatalf("golden encode: %v", err)
	}
	expectedBody := expectedBuf.Bytes()

	// 3. Compare byte-for-byte.
	if !bytes.Equal(wireBody, expectedBody) {
		t.Errorf("?include=messages output differs from golden encoding\nwire:  %s\ngolden: %s\ndiff:  %s",
			wireBody, expectedBody, subtleDiff(wireBody, expectedBody))
	}
}

func TestHandleRequestDetail_ByteLevelGoldenParity(t *testing.T) {
	ts := newRequestsTestServer(t)
	ts.store.Add(goldenRequest())
	server := ts.serveWithSummary()
	defer server.Close()

	resp, err := http.Get(server.URL + "/fe/api/requests/golden-1")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	wireBody, _ := readAll(resp.Body)

	expectedReq := goldenRequest()
	var expectedBuf bytes.Buffer
	if err := json.NewEncoder(&expectedBuf).Encode(expectedReq); err != nil {
		t.Fatalf("golden encode: %v", err)
	}
	expectedBody := expectedBuf.Bytes()

	if !bytes.Equal(wireBody, expectedBody) {
		t.Errorf("/fe/api/requests/{id} output differs from golden encoding\nwire:  %s\ngolden: %s",
			wireBody, expectedBody)
	}
}

// subtleDiff returns a short human-readable hint of where two byte
// slices diverge (first differing position + nearby bytes). Used by
// the golden-parity test failure messages; not a full diff.
func subtleDiff(a, b []byte) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			from := i - 10
			if from < 0 {
				from = 0
			}
			to := i + 10
			if to > len(a) {
				to = len(a)
			}
			return fmt.Sprintf("first diff at byte %d: wire=%q... golden=%q...", i, a[from:to], b[from:to])
		}
	}
	if len(a) != len(b) {
		return fmt.Sprintf("common prefix %d bytes; len(wire)=%d, len(golden)=%d", n, len(a), len(b))
	}
	return "(identical bytes)"
}

// =============================================================================
// Helper
// =============================================================================

// readAll drains the response body. io.ReadAll handles EOF as a
// non-error (correct modern idiom), so callers don't need to special-
// case it.
func readAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}