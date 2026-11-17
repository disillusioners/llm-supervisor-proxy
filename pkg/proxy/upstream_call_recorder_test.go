package proxy

// ─────────────────────────────────────────────────────────────────────────────
// upstream_call_recorder_test.go — the SHARED upstream-call recorder
// (wi-4b8d21e7, review quality pass).
//
// The race-lane harness (quota_mint_scope_test.go) and the Anthropic-lane
// harness (anthropic_quota_class_test.go) both need the same observation:
// "which credential did the proxy actually call, in which order?". Two
// identical recorders were a drift risk (the same bug class this
// commission exists to close), so there is ONE recorder now.
//
// The KEY is the credential's API key — the harness's marker token. A
// recorded key therefore identifies the ACCOUNT that served the call,
// which is exactly what the mint-scope assertions read.
// ─────────────────────────────────────────────────────────────────────────────

import "sync"

// upstreamCallRecorder records the API key of every upstream call, in
// order. Safe for concurrent use (the race lane calls from several
// goroutines).
type upstreamCallRecorder struct {
	mu    sync.Mutex
	calls []string
}

// record appends the call's API key (the credential's marker token).
func (r *upstreamCallRecorder) record(apiKey string) {
	r.mu.Lock()
	r.calls = append(r.calls, apiKey)
	r.mu.Unlock()
}

// calledKeys returns a copy of the recorded API keys, in order.
func (r *upstreamCallRecorder) calledKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{}, r.calls...)
}

// called reports whether the given API key was called at least once.
func (r *upstreamCallRecorder) called(apiKey string) bool {
	for _, k := range r.calledKeys() {
		if k == apiKey {
			return true
		}
	}
	return false
}
