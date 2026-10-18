package proxy

// race_reason_test.go — behavioral tests for the race/fallback
// event-log reason classification (race-error-reason-classification).
//
// Coverage map:
//   - sanitizeReasonDetail: deterministic bounding, single-line
//     flattening, secret redaction (sk- keys, bearer tokens,
//     api_key/x-api-key assignment shapes, GitHub gh*_ tokens,
//     Google AIza… keys, HTTP Basic credentials).
//   - classifyAttemptFailure: the classification table (status
//     families, auth, rate limit incl. wrap-transparency and
//     vocabulary precedence, conflict, timeout, connection,
//     proxy-internal, cancelled, unknown).
//   - failureReason.String: the stable greppable segment format.
//   - End-to-end through the REAL coordinator + a REAL failing
//     httptest upstream: race_request_failed / race_spawn /
//     race_all_failed carry class + status + truncated detail; event
//     names and pre-existing keys unchanged; idle_timeout spawns stay
//     byte-identical (additive-only enrichment).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/providers"
)

// ─────────────────────────────────────────────────────────────────────────────
// sanitizeReasonDetail
// ─────────────────────────────────────────────────────────────────────────────

func TestSanitizeReasonDetail_BoundedDeterministic(t *testing.T) {
	// 100KB upstream-style error body with newlines and a long tail.
	body := `{"error":{"message":"` + strings.Repeat("quota exhausted. ", 6000) + `\nsecond line\n\tthird line"}}`

	got := sanitizeReasonDetail(body)

	if got == "" {
		t.Fatal("sanitizeReasonDetail returned empty string for non-empty input")
	}
	// Bound: at most maxReasonDetailRunes runes + the 1-rune ellipsis.
	if n := len([]rune(got)); n > maxReasonDetailRunes+1 {
		t.Fatalf("detail length = %d runes, want <= %d (+ellipsis)", n, maxReasonDetailRunes)
	}
	// Single line: no raw newline may survive.
	if strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("detail contains raw newline/tab: %q", got)
	}
	// Deterministic: same input ⇒ byte-identical output.
	if again := sanitizeReasonDetail(body); again != got {
		t.Fatalf("sanitizeReasonDetail not deterministic:\n first=%q\n again=%q", got, again)
	}
	// Truncation is marked.
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated detail should end with ellipsis, got %q", got)
	}
	// Head of the message is preserved (triage value).
	if !strings.Contains(got, "quota exhausted") {
		t.Fatalf("truncated detail lost the message head: %q", got)
	}

	// Below the cap: returned verbatim (modulo whitespace flattening).
	short := "upstream returned error: 429 Too Many Requests"
	if got := sanitizeReasonDetail(short); got != short {
		t.Fatalf("short detail mutated: got %q, want %q", got, short)
	}

	// Empty stays empty.
	if got := sanitizeReasonDetail(""); got != "" {
		t.Fatalf("empty input should stay empty, got %q", got)
	}
}

func TestSanitizeReasonDetail_RedactsSecrets(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		secret string
		marker string
		// keep is a slice of surrounding fragments that MUST survive —
		// guards against over-redaction (the fix must not eat useful
		// error detail). Empty = no survival assertion for that case.
		keep []string
	}{
		{
			name:   "sk key",
			in:     `auth failed for key sk-test-SECRETVALUE123456 at gateway`,
			secret: "sk-test-SECRETVALUE123456",
			marker: "sk-***",
			keep:   []string{"auth failed for key", "at gateway"},
		},
		{
			name:   "bearer token",
			in:     `Authorization: Bearer eyJhbGciOi.SUPER.SECRET rejected`,
			secret: "eyJhbGciOi.SUPER.SECRET",
			marker: "Bearer ***",
			keep:   []string{"Authorization:", "rejected"},
		},
		{
			name:   "api_key assignment",
			in:     `{"api_key":"supersecretkeyvalue1","code":401}`,
			secret: "supersecretkeyvalue1",
			marker: "api_key=***",
			keep:   []string{`"code":401`},
		},
		{
			// reasonSecretAssign deliberately consumes the header label
			// AND the separator as part of the secret span: "x-api-key:
			// anothersecretvalue99" → "api_key=***". The marker + the
			// post-secret "missing" context are the post-redaction
			// survival signals; the header label itself is the
			// defensible over-redaction (operators expect to see a
			// fixed sentinel, not the raw label that triggered it).
			name:   "x-api-key header",
			in:     `x-api-key: anothersecretvalue99 missing`,
			secret: "anothersecretvalue99",
			marker: "api_key=***",
			keep:   []string{"missing"},
		},
		{
			// GitHub PAT: ghp_ + 36 base62. Must be redacted AND the
			// surrounding status/reason text must survive.
			name:   "github PAT token",
			in:     `upstream 401 token ghp_ABCdefGHIjklMNOpqrSTUvwxYZ0123456789 rejected, retry later`,
			secret: "ghp_ABCdefGHIjklMNOpqrSTUvwxYZ0123456789",
			marker: "gh_***",
			keep:   []string{"upstream 401 token", "rejected, retry later"},
		},
		{
			// Google API key: AIza + exactly 35 key chars.
			name:   "google api key",
			in:     `{"error":{"message":"API key AIzaSyA1234567890abcdefghijklmnopqrstuv is not valid"}}`,
			secret: "AIzaSyA1234567890abcdefghijklmnopqrstuv",
			marker: "AIza***",
			keep:   []string{"API key", "is not valid"},
		},
		{
			// HTTP Basic credential echoed by an upstream. The b64 run
			// carries digits + padding so it is unambiguously a secret,
			// and "denied by upstream" must NOT be over-redacted.
			name:   "basic auth credential",
			in:     `Authorization: Basic dXNlcjpwYXNzd29yZA== denied by upstream`,
			secret: "dXNlcjpwYXNzd29yZA==",
			marker: "Basic ***",
			keep:   []string{"Authorization:", "denied by upstream"},
		},
		{
			// Over-redaction guard: the plain-English phrase "basic
			// authentication" (no digit / base64 symbol) must be left
			// intact by the Basic pattern.
			name:   "basic prose is not over-redacted",
			in:     `basic authentication failed`,
			secret: "",
			marker: "",
			keep:   []string{"basic authentication failed"},
		},
		{
			// Over-redaction guard (length floor): short digit-bearing
			// prose after "basic " must survive INTACT — the ≥8-char
			// credential-alphabet floor rejects it even though it
			// carries a digit, preserving triage detail.
			name:   "short basic prose with digits survives",
			in:     `basic v2 rejected; basic 401 challenge issued`,
			secret: "",
			marker: "",
			keep:   []string{"basic v2 rejected; basic 401 challenge issued"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeReasonDetail(tc.in)
			if tc.secret != "" && strings.Contains(got, tc.secret) {
				t.Fatalf("secret %q leaked into detail: %q", tc.secret, got)
			}
			if tc.marker != "" && !strings.Contains(got, tc.marker) {
				t.Fatalf("redaction marker %q missing from detail: %q", tc.marker, got)
			}
			for _, frag := range tc.keep {
				if !strings.Contains(got, frag) {
					t.Fatalf("surrounding detail over-redacted: fragment %q missing from %q", frag, got)
				}
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// classifyAttemptFailure — the classification table
// ─────────────────────────────────────────────────────────────────────────────

// fakeNetTimeoutErr is a minimal net.Error with Timeout()=true.
type fakeNetTimeoutErr struct{}

func (fakeNetTimeoutErr) Error() string   { return "i/o timeout while reading upstream" }
func (fakeNetTimeoutErr) Timeout() bool   { return true }
func (fakeNetTimeoutErr) Temporary() bool { return false }

// raceExternalBuildError drives the REAL external-request builder
// (executeExternalRequest, race_executor.go) so the table rows below
// assert on the producer's ACTUAL output. If a producer literal drifts,
// the row's calculated error drifts with it and the classification
// assertion fails — instead of silently passing on a hand-typed copy
// (which is exactly how the case-sensitivity defect survived).
//
//   - upstreamURL = "http://[::1" → url.Parse fails at
//     race_executor.go:478, returning "invalid upstream URL: …".
//   - ctx = nil → http.NewRequestWithContext fails at
//     race_executor.go:511, returning "failed to create upstream request: …".
func raceExternalBuildError(t *testing.T, upstreamURL string, ctx context.Context) error {
	t.Helper()
	cfg := newTestConfigSnapshot("m1")
	cfg.UpstreamURL = upstreamURL
	rawBody := []byte(`{"model":"m1"}`)
	origReq := httptest.NewRequest("POST", "http://placeholder/v1/chat/completions", nil)
	upReq := newUpstreamRequest(0, upstreamModelType(ModelTypeMain), "m1", 1024*1024)
	err := executeExternalRequest(ctx, cfg, origReq, rawBody, upReq, false)
	if err == nil {
		t.Fatalf("executeExternalRequest(upstreamURL=%q, ctx=%v) unexpectedly returned nil", upstreamURL, ctx)
	}
	return err
}

func TestClassifyAttemptFailure_Table(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		storedStatus int
		wantClass    string
		wantStatus   int
	}{
		{
			name:       "nil error",
			err:        nil,
			wantClass:  reasonClassUnknown,
			wantStatus: 0,
		},
		{
			name:       "ProviderError 429",
			err:        &providers.ProviderError{Provider: "openai", StatusCode: 429, Message: "limit"},
			wantClass:  reasonClassRateLimit,
			wantStatus: 429,
		},
		{
			name:       "ProviderError 401 → auth",
			err:        &providers.ProviderError{Provider: "openai", StatusCode: 401, Message: "bad key"},
			wantClass:  reasonClassAuth,
			wantStatus: 401,
		},
		{
			name:       "ProviderError 403 → auth",
			err:        &providers.ProviderError{Provider: "openai", StatusCode: 403, Message: "forbidden"},
			wantClass:  reasonClassAuth,
			wantStatus: 403,
		},
		{
			name:       "ProviderError 409 → conflict",
			err:        &providers.ProviderError{Provider: "openai", StatusCode: 409, Message: "conflict"},
			wantClass:  reasonClassConflict,
			wantStatus: 409,
		},
		{
			name:       "ProviderError 502 → upstream_5xx",
			err:        &providers.ProviderError{Provider: "openai", StatusCode: 502, Message: "bad gateway"},
			wantClass:  reasonClassUpstream5xx,
			wantStatus: 502,
		},
		{
			name:       "ProviderError 400 → upstream_4xx",
			err:        &providers.ProviderError{Provider: "openai", StatusCode: 400, Message: "bad request"},
			wantClass:  reasonClassUpstream4xx,
			wantStatus: 400,
		},
		{
			name:       "wrapped ProviderError seen through fmt.Errorf",
			err:        fmt.Errorf("upstream request failed: %w", &providers.ProviderError{Provider: "p", StatusCode: 429}),
			wantClass:  reasonClassRateLimit,
			wantStatus: 429,
		},
		{
			name:       "external string shape 429 (stored status absent)",
			err:        errors.New("upstream returned error: 429 Too Many Requests"),
			wantClass:  reasonClassRateLimit,
			wantStatus: 429,
		},
		{
			name:       "external string shape 500 with body suffix",
			err:        errors.New("upstream returned error: 500 Internal Server Error - " + strings.Repeat("body ", 200)),
			wantClass:  reasonClassUpstream5xx,
			wantStatus: 500,
		},
		{
			name:         "stored status wins for generic errors (504)",
			err:          errors.New("something odd"),
			storedStatus: 504,
			wantClass:    reasonClassUpstream5xx,
			wantStatus:   504,
		},
		{
			name:       "rate-limit vocabulary beats 5xx status (Round-3b matrix)",
			err:        &providers.ProviderError{Provider: "p", StatusCode: 503, ErrorType: "rate_limit_error"},
			wantClass:  reasonClassRateLimit,
			wantStatus: 503,
		},
		{
			name:       "context deadline → timeout",
			err:        fmt.Errorf("upstream request failed: %w", context.DeadlineExceeded),
			wantClass:  reasonClassTimeout,
			wantStatus: 0,
		},
		{
			name:       "net.Error timeout → timeout",
			err:        fakeNetTimeoutErr{},
			wantClass:  reasonClassTimeout,
			wantStatus: 0,
		},
		{
			name:       "idle timeout string → timeout",
			err:        errors.New("request idle timeout after 60s"),
			wantClass:  reasonClassTimeout,
			wantStatus: 0,
		},
		{
			name:       "connection refused via url.Error → connection",
			err:        &url.Error{Op: "Post", URL: "http://up:1/v1", Err: errors.New("dial tcp 10.0.0.1:443: connect: connection refused")},
			wantClass:  reasonClassConnection,
			wantStatus: 0,
		},
		{
			name:       "DNS failure → connection",
			err:        errors.New("upstream request failed: Post \"http://nope.invalid/v1\": dial tcp: lookup nope.invalid: no such host"),
			wantClass:  reasonClassConnection,
			wantStatus: 0,
		},
		{
			name:       "premature close → connection",
			err:        errors.New("upstream closed connection prematurely"),
			wantClass:  reasonClassConnection,
			wantStatus: 0,
		},
		{
			name:       "resolution failure → proxy_internal",
			err:        errors.New("failed to resolve internal config for model m1"),
			wantClass:  reasonClassProxyInternal,
			wantStatus: 0,
		},
		{
			name:       "buffer cap → proxy_internal",
			err:        errors.New("buffer limit exceeded: non-streaming response for model m1"),
			wantClass:  reasonClassProxyInternal,
			wantStatus: 0,
		},
		{
			name:       "provider build failure → proxy_internal",
			err:        fmt.Errorf("failed to create provider: %w", errors.New("unknown provider type")),
			wantClass:  reasonClassProxyInternal,
			wantStatus: 0,
		},
		{
			// Driven from the REAL producer at race_executor.go:478 via
			// raceExternalBuildError. The producer emits the MIXED-CASE
			// "invalid upstream URL"; the classifier must lowercase
			// before matching (a case-sensitive compare yields unknown).
			name:       "invalid upstream URL (real producer race_executor.go:478) → proxy_internal",
			err:        raceExternalBuildError(t, "http://[::1", context.Background()),
			wantClass:  reasonClassProxyInternal,
			wantStatus: 0,
		},
		{
			// Driven from the REAL producer at race_executor.go:511 via
			// raceExternalBuildError (nil ctx ⇒ NewRequestWithContext
			// error). The producer literal is "failed to create upstream
			// request" — the old marker's "failed to build upstream
			// request" had no producer in pkg/proxy and never matched.
			name:       "failed to create upstream request (real producer race_executor.go:511) → proxy_internal",
			err:        raceExternalBuildError(t, "http://example.invalid/v1", nil),
			wantClass:  reasonClassProxyInternal,
			wantStatus: 0,
		},
		{
			name:       "race teardown cancellation → cancelled",
			err:        fmt.Errorf("upstream request failed: %w", context.Canceled),
			wantClass:  reasonClassCancelled,
			wantStatus: 0,
		},
		{
			name:       "unrecognized → unknown (still classified, still bounded)",
			err:        errors.New("something completely unfamiliar happened: " + strings.Repeat("z", 500)),
			wantClass:  reasonClassUnknown,
			wantStatus: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fr := classifyAttemptFailure(tc.err, tc.storedStatus)
			if fr.Class != tc.wantClass {
				t.Errorf("class = %q, want %q (reason=%q)", fr.Class, tc.wantClass, fr.String())
			}
			if fr.Status != tc.wantStatus {
				t.Errorf("status = %d, want %d (reason=%q)", fr.Status, tc.wantStatus, fr.String())
			}
			if tc.err != nil {
				if len([]rune(fr.Detail)) > maxReasonDetailRunes+1 {
					t.Errorf("detail unbounded: %d runes", len([]rune(fr.Detail)))
				}
				if fr.Detail == "" {
					t.Errorf("non-nil error must yield non-empty detail")
				}
			}
		})
	}
}

func TestFailureReasonString_Format(t *testing.T) {
	// class only
	if got := (failureReason{Class: reasonClassTimeout}).String(); got != "reason=timeout" {
		t.Errorf("class-only format = %q", got)
	}
	// class + status
	if got := (failureReason{Class: reasonClassRateLimit, Status: 429}).String(); got != "reason=rate_limit status=429" {
		t.Errorf("class+status format = %q", got)
	}
	// class + status + detail (deterministic order, quoted detail)
	fr := failureReason{Class: reasonClassUpstream5xx, Status: 502, Detail: "upstream returned error: 502 Bad Gateway"}
	want := `reason=upstream_5xx status=502 detail="upstream returned error: 502 Bad Gateway"`
	if got := fr.String(); got != want {
		t.Errorf("full format =\n  %q\nwant\n  %q", got, want)
	}
	// empty class falls back to unknown
	if got := (failureReason{}).String(); got != "reason=unknown" {
		t.Errorf("zero-value format = %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Real-flow: failing httptest upstream through the coordinator
// ─────────────────────────────────────────────────────────────────────────────

// collectEvents subscribes to the bus and returns a channel of events.
func collectReasonEvents(t *testing.T, bus *events.Bus) chan events.Event {
	t.Helper()
	ch, err := bus.Subscribe()
	if err != nil {
		t.Fatalf("bus.Subscribe: %v", err)
	}
	t.Cleanup(func() { bus.Unsubscribe(ch) })
	out := make(chan events.Event, 64)
	go func() {
		for evt := range ch {
			select {
			case out <- evt:
			default: // never block the bus on a slow test
			}
		}
	}()
	return out
}

func eventMap(t *testing.T, evt events.Event) map[string]interface{} {
	t.Helper()
	data, ok := evt.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("event %s payload is not a map: %T", evt.Type, evt.Data)
	}
	return data
}

// waitEvent pulls the next event of the wanted type (skipping others).
func waitEvent(t *testing.T, ch chan events.Event, eventType string, timeout time.Duration) events.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case evt := <-ch:
			if evt.Type == eventType {
				return evt
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s event", eventType)
		}
	}
}

// TestRaceFailureReason_FailingUpstreamEndToEnd drives a REAL failing
// upstream (429 + huge secret-bearing body) through the coordinator's
// full lifecycle and asserts every emission site carries the
// classified, bounded reason — while event names and the legacy keys
// keep their shape.
func TestRaceFailureReason_FailingUpstreamEndToEnd(t *testing.T) {
	hugeSecretBody := `{"error":{"message":"quota blown for key sk-test-SECRETVALUE123456 — ` +
		strings.Repeat("details details ", 8000) + `","code":429}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(hugeSecretBody))
	}))
	defer srv.Close()

	bus := events.NewBus()
	evCh := collectReasonEvents(t, bus)

	cfg := newTestConfigSnapshot("m1")
	cfg.UpstreamURL = srv.URL

	coord := newRaceCoordinatorWithEvents(
		context.Background(), cfg, newTestRequest(),
		[]byte(`{"model":"m1","messages":[{"role":"user","content":"hi"}]}`),
		[]string{"m1"}, bus, "req-reason-e2e", false, nil, "", false,
	)

	coord.Start()
	select {
	case <-coord.done:
	case <-time.After(5 * time.Second):
		t.Fatal("race did not terminate within 5s")
	}

	// 1. race_request_failed — class + status + truncated detail; no
	// secret; legacy keys intact.
	failEvt := waitEvent(t, evCh, "race_request_failed", 2*time.Second)
	failData := eventMap(t, failEvt)

	if got := failData["reason_class"]; got != reasonClassRateLimit {
		t.Errorf("race_request_failed reason_class = %v, want %q", got, reasonClassRateLimit)
	}
	if got, _ := failData["reason_status"].(int); got != http.StatusTooManyRequests {
		t.Errorf("race_request_failed reason_status = %v, want 429", failData["reason_status"])
	}
	reasonStr, _ := failData["reason"].(string)
	wantReason := `reason=rate_limit status=429`
	if !strings.HasPrefix(reasonStr, wantReason) {
		t.Errorf("race_request_failed reason = %q, want prefix %q", reasonStr, wantReason)
	}
	errStr, _ := failData["error"].(string)
	if len([]rune(errStr)) > maxReasonDetailRunes+1 {
		t.Errorf("race_request_failed error unbounded: %d runes", len([]rune(errStr)))
	}
	if strings.Contains(errStr+reasonStr, "sk-test-SECRETVALUE123456") {
		t.Errorf("secret leaked into race_request_failed payload: %q", errStr)
	}
	// Legacy keys unchanged.
	if failData["model"] != "m1" || failData["type"] != string(modelTypeMain) {
		t.Errorf("legacy keys changed: model=%v type=%v", failData["model"], failData["type"])
	}
	if failData["id"] != "req-reason-e2e" {
		t.Errorf("correlation id missing: %v", failData["id"])
	}
	// Sample line for the report (visible with -v).
	t.Logf("race_request_failed reason=%q error=%q", reasonStr, errStr)

	// 2. race_all_failed — per-row classified reasons, bounded errors.
	allEvt := waitEvent(t, evCh, "race_all_failed", 2*time.Second)
	allData := eventMap(t, allEvt)
	rows, ok := allData["errors"].([]map[string]interface{})
	if !ok || len(rows) == 0 {
		t.Fatalf("race_all_failed errors missing/empty: %T", allData["errors"])
	}
	row := rows[0]
	if row["reason_class"] != reasonClassRateLimit {
		t.Errorf("race_all_failed row reason_class = %v, want rate_limit", row["reason_class"])
	}
	if row["reason_status"] != http.StatusTooManyRequests {
		t.Errorf("race_all_failed row reason_status = %v, want 429", row["reason_status"])
	}
	if rowErr, _ := row["error"].(string); strings.Contains(rowErr, "sk-test-SECRETVALUE123456") {
		t.Errorf("secret leaked into race_all_failed row: %q", rowErr)
	}
	t.Logf("race_all_failed row=%v", row)

	// 3. The fallback-transition classification, RE-DERIVED LOCALLY:
	// this assertion does not exercise the manage-loop spawnTriggerInfo
	// wiring — it only checks that the failureReason the loop would
	// attach to a main_error spawn renders its greppable line form.
	// The real spawnTriggerInfo → race_spawn wiring is pinned in
	// TestRaceSpawnEvent_ReasonAdditiveOnly.
	line := classifyAttemptFailure(errors.New("upstream returned error: 429 Too Many Requests"), 0)
	if !strings.HasPrefix(line.String(), "reason=rate_limit status=429 detail=\"upstream returned error: 429") {
		t.Errorf("spawn-transition reason line malformed: %q", line.String())
	}
}

// TestRaceSpawnEvent_ReasonAdditiveOnly pins the additive-only
// contract: error-triggered spawns GAIN reason/reason_class(+status);
// idle_timeout spawns publish NO extra keys (byte-identical to the
// pre-change shape); the legacy trigger key is untouched.
func TestRaceSpawnEvent_ReasonAdditiveOnly(t *testing.T) {
	bus := events.NewBus()
	evCh := collectReasonEvents(t, bus)

	cfg := newTestConfigSnapshot("m1")
	coord := newRaceCoordinatorWithEvents(
		context.Background(), cfg, newTestRequest(), []byte("{}"),
		[]string{"m1", "fallback"}, bus, "req-reason-spawn", false, nil, "", false,
	)

	// Idle-timeout spawn (no error to classify — W4).
	coord.spawn(modelTypeSecond, spawnTriggerInfo{
		trigger:       triggerIdleTimeout,
		failedRequest: -1,
	})
	idleEvt := waitEvent(t, evCh, "race_spawn", 2*time.Second)
	idleData := eventMap(t, idleEvt)
	if _, present := idleData["reason"]; present {
		t.Errorf("idle_timeout spawn gained reason key: %v", idleData)
	}
	if _, present := idleData["reason_class"]; present {
		t.Errorf("idle_timeout spawn gained reason_class key: %v", idleData)
	}
	if idleData["trigger"] != string(triggerIdleTimeout) {
		t.Errorf("legacy trigger key changed: %v", idleData["trigger"])
	}

	// Error-triggered spawn carries the classified reason.
	fr := classifyAttemptFailure(errors.New("upstream returned error: 502 Bad Gateway"), 502)
	coord.spawn(modelTypeFallback, spawnTriggerInfo{
		trigger:       triggerMainError,
		failedRequest: 0,
		reason:        fr,
	})
	failEvt := waitEvent(t, evCh, "race_spawn", 2*time.Second)
	failData := eventMap(t, failEvt)

	// Legacy shape preserved (what the FE renders).
	if failData["trigger"] != string(triggerMainError) {
		t.Errorf("legacy trigger key changed: %v", failData["trigger"])
	}
	if failData["type"] != string(modelTypeFallback) || failData["model"] != "fallback" {
		t.Errorf("legacy type/model keys changed: %v", failData)
	}
	// Additive classified fields.
	if failData["reason_class"] != reasonClassUpstream5xx {
		t.Errorf("reason_class = %v, want upstream_5xx", failData["reason_class"])
	}
	if failData["reason_status"] != 502 {
		t.Errorf("reason_status = %v, want 502", failData["reason_status"])
	}
	if reasonStr, _ := failData["reason"].(string); !strings.HasPrefix(reasonStr, "reason=upstream_5xx status=502") {
		t.Errorf("reason = %q, want prefix reason=upstream_5xx status=502", reasonStr)
	}
	// trigger_reason is the bounded detail, not the raw error.
	if tr, _ := failData["trigger_reason"].(string); tr != "upstream returned error: 502 Bad Gateway" {
		t.Errorf("trigger_reason = %q, want the bounded detail", tr)
	}
	t.Logf("race_spawn (main_error) payload=%v", failData)
}
