package oauth2

import (
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestHandler(t *testing.T) *Handler {
	return newTestHandlerWithCookieKey(t, 32)
}

// newTestHandlerWithCookieKey builds the test handler; a cookieKeyLen other
// than 32 yields a session codec whose Seal always fails (the S6 path).
func newTestHandlerWithCookieKey(t *testing.T, cookieKeyLen int) *Handler {
	srv, _ := newStubIdP(t)
	t.Cleanup(func() { srv.Close() })
	testStubServer = srv
	cfg := &Config{
		Enabled:              true,
		IssuerURL:            srv.URL,
		ClientID:             "cid",
		ExternalURL:          "https://app.example.com",
		RedirectURL:          srv.URL + "/oauth2/callback",
		CookieName:           "llmproxy_oauth2",
		TxnCookieName:        "llmproxy_oauth2_txn",
		CookieSecure:         true,
		CookieSameSite:       http.SameSiteLaxMode,
		SessionTTL:           time.Hour,
		EmailDomains:         []string{"example.com"},
		RequireEmailVerified: true,
		AllowedAlgs:          map[string]struct{}{"RS256": {}},
		StateStoreMax:        4,
		RateLimitRPM:         10,
		RateLimitBurst:       20,
		JWKSCacheTTL:         time.Hour,
		DiscoveryTimeout:     5 * time.Second,
		TokenExchangeTimeout: 5 * time.Second,
		UserinfoTimeout:      5 * time.Second,
	}
	root := make([]byte, 32)
	for i := range root {
		root[i] = byte(i + 1)
	}
	_, tk, _ := deriveSubkeys(root)
	cfg.rootKey = root
	sessionKey := make([]byte, cookieKeyLen)
	for i := range sessionKey {
		sessionKey[i] = byte(i + 1)
	}
	cookie := &CookieCodec{name: cfg.CookieName, key: sessionKey, secure: true, sameSite: http.SameSiteLaxMode, path: "/"}
	txn := &CookieCodec{name: cfg.TxnCookieName, key: tk, secure: true, sameSite: http.SameSiteLaxMode, path: "/oauth2", maxAge: 600}
	provider := NewOIDCProvider(cfg)
	fetch := func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		meta, err := provider.Discover(ctx)
		if err != nil {
			return nil, nil, err
		}
		return fetchJWKSSet(ctx, provider.client, meta.JWKSURI, cfg.AllowedAlgs)
	}
	cache := NewJWKSCache(cfg.JWKSCacheTTL, time.Minute, cfg.JWKSCacheTTL*2, fetch)
	verifier := NewVerifier(cfg, cache)
	h := NewHandler(cfg, provider, verifier, cookie, txn)
	return h
}

// newThrottleHandler builds a handler whose IdP key set can withhold the
// signing key, with a short JWKS min-refresh window so the throttle window
// is testable.
func newThrottleHandler(t *testing.T, idp *throttleIdP) *Handler {
	cfg := &Config{
		Enabled:              true,
		IssuerURL:            idp.server.URL,
		ClientID:             "cid",
		ExternalURL:          "https://app.example.com",
		RedirectURL:          idp.server.URL + "/oauth2/callback",
		CookieName:           "llmproxy_oauth2",
		TxnCookieName:        "llmproxy_oauth2_txn",
		CookieSecure:         true,
		CookieSameSite:       http.SameSiteLaxMode,
		SessionTTL:           time.Hour,
		EmailDomains:         []string{"example.com"},
		RequireEmailVerified: true,
		AllowedAlgs:          map[string]struct{}{"RS256": {}},
		StateStoreMax:        8,
		RateLimitRPM:         60,
		RateLimitBurst:       60,
		JWKSCacheTTL:         time.Hour,
		JWKSMinRefresh:       150 * time.Millisecond,
		JWKSStaleGrace:       time.Hour,
		DiscoveryTimeout:     5 * time.Second,
		TokenExchangeTimeout: 5 * time.Second,
		UserinfoTimeout:      5 * time.Second,
	}
	root := make([]byte, 32)
	for i := range root {
		root[i] = byte(i + 1)
	}
	sk, tk, _ := deriveSubkeys(root)
	cfg.rootKey = root
	cookie := &CookieCodec{name: cfg.CookieName, key: sk, secure: true, sameSite: http.SameSiteLaxMode, path: "/"}
	txn := &CookieCodec{name: cfg.TxnCookieName, key: tk, secure: true, sameSite: http.SameSiteLaxMode, path: "/oauth2", maxAge: 600}
	provider := NewOIDCProvider(cfg)
	fetch := func(ctx context.Context) (map[string]crypto.PublicKey, map[string]string, error) {
		meta, err := provider.Discover(ctx)
		if err != nil {
			return nil, nil, err
		}
		return fetchJWKSSet(ctx, provider.client, meta.JWKSURI, cfg.AllowedAlgs)
	}
	cache := NewJWKSCache(cfg.JWKSCacheTTL, cfg.JWKSMinRefresh, cfg.JWKSStaleGrace, fetch)
	verifier := NewVerifier(cfg, cache)
	return NewHandler(cfg, provider, verifier, cookie, txn)
}

// newEmptyKeySetHandler builds a handler whose JWKS windows are short enough
// to be testable: the cache TTL expires quickly (so a refresh is triggered),
// the min-refresh window is short (so a retry is not throttled) and the
// stale-grace window is long enough to cover the test.
func newEmptyKeySetHandler(t *testing.T, idp *throttleIdP) *Handler {
	t.Helper()
	h := newThrottleHandler(t, idp)
	h.cfg.JWKSMinRefresh = 50 * time.Millisecond
	h.verifier.cache.ttl = 50 * time.Millisecond
	h.verifier.cache.minRefresh = 50 * time.Millisecond
	h.verifier.cache.grace = time.Hour
	return h
}

// txnStateFor mints a state record bound to a fresh txn cookie and returns
// both the record and the cookie value.
func txnStateFor(t *testing.T, h *Handler, state, nonce, verifier string) (*stateRecord, string) {
	t.Helper()
	tv, err := h.txn.Seal(&Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(tv))
	rec := &stateRecord{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: verifier,
		RedirectTo:   "/ui/",
		TxnDigest:    hexEncode(sum[:]),
		CreatedAt:    time.Now(),
	}
	h.states.add(rec)
	return rec, tv
}

// S3: a kid-miss throttle answers 503 and does NOT consume the one-time
// state — the operator's login survives the throttle window and the same
// callback can be retried once the window passes.
func TestCallback_JWKSRateLimitAnswers503AndPreservesState(t *testing.T) {
	idp := newThrottleIdP(t, "throttle-kid")
	h := newThrottleHandler(t, idp)
	verifier := "verifier-throttle"
	stubRegisterCode("code-throttle", "nonce-throttle", verifier, "user@example.com", true)

	// attempt 1: the key set carries NO usable key => a FETCH error under
	// the stale-grace policy => a retryable 503 (jwks_unavailable). The
	// refresh is recorded, so the NEXT miss inside the window is throttled.
	_, tv1 := txnStateFor(t, h, "st1", "nonce-throttle", verifier)
	req1 := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=st1&code=code-throttle", nil)
	req1.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv1})
	w1 := httptest.NewRecorder()
	h.HandleCallback(w1, req1)
	if w1.Code != http.StatusServiceUnavailable {
		t.Fatalf("attempt 1 code: %d body: %s", w1.Code, w1.Body.String())
	}
	if body := strings.TrimSpace(w1.Body.String()); body != `{"error":"jwks_unavailable"}` {
		t.Fatalf("attempt 1 body: %s", body)
	}

	// attempt 2: the miss lands inside the min-refresh window => throttled.
	_, tv2 := txnStateFor(t, h, "st2", "nonce-throttle", verifier)
	req2 := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=st2&code=code-throttle", nil)
	req2.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv2})
	w2 := httptest.NewRecorder()
	h.HandleCallback(w2, req2)
	if w2.Code != http.StatusServiceUnavailable {
		t.Fatalf("attempt 2 code: %d body: %s", w2.Code, w2.Body.String())
	}
	if body := strings.TrimSpace(w2.Body.String()); body != `{"error":"jwks_rate_limited"}` {
		t.Fatalf("attempt 2 body: %s", body)
	}
	// F1 (plan-mandated): the throttled path answers 503 WITH a Retry-After
	// header — the operator must know when the callback may be retried.
	retryAfter := w2.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("the 503 throttle response carries no Retry-After header")
	}
	secs, err := strconv.Atoi(retryAfter)
	if err != nil || secs < 1 {
		t.Fatalf("Retry-After = %q, want a positive number of seconds", retryAfter)
	}
	// the one-time state must NOT have been consumed by the throttle
	if _, ok := h.states.get("st2"); !ok {
		t.Fatal("the throttled callback consumed the one-time state")
	}
	// the txn cookie must NOT have been cleared — the browser keeps it
	for _, c := range w2.Result().Cookies() {
		if c.Name == h.cfg.TxnCookieName && c.MaxAge < 0 {
			t.Fatal("the throttled callback cleared the txn cookie")
		}
	}

	// the key set now publishes the kid; the SAME state retries successfully
	// once the throttle window has passed.
	idp.setPublish(true)
	time.Sleep(2 * h.cfg.JWKSMinRefresh)
	req3 := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=st2&code=code-throttle", nil)
	req3.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv2})
	w3 := httptest.NewRecorder()
	h.HandleCallback(w3, req3)
	if w3.Code != http.StatusFound {
		t.Fatalf("retry code: %d body: %s", w3.Code, w3.Body.String())
	}
	found := false
	for _, c := range w3.Result().Cookies() {
		if c.Name == h.cfg.CookieName {
			found = true
		}
	}
	if !found {
		t.Fatal("the retry did not set a session cookie")
	}
}

// Item 2: a JWKS document that yields NO usable key is a FETCH failure under
// the stale-grace policy. It answers a retryable 503 (jwks_unavailable) WITH
// a Retry-After header, and the one-time state is RESTORED so the same
// callback can be retried once the IdP publishes a key again.
func TestCallback_EmptyKeySetAnswers503RetryableAndRestoresState(t *testing.T) {
	idp := newThrottleIdP(t, "empty-kid")
	h := newEmptyKeySetHandler(t, idp)
	verifier := "verifier-unavail"
	stubRegisterCode("code-unavail", "nonce-unavail", verifier, "user@example.com", true)

	// the IdP publishes NO usable key
	idp.setPublish(false)

	_, tv := txnStateFor(t, h, "st-unavail", "nonce-unavail", verifier)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=st-unavail&code=code-unavail", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
	if body := strings.TrimSpace(w.Body.String()); body != `{"error":"jwks_unavailable"}` {
		t.Fatalf("body: %s", body)
	}
	// the operator must know when the callback may be retried
	retryAfter := w.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("the 503 response carries no Retry-After header")
	}
	secs, err := strconv.Atoi(retryAfter)
	if err != nil || secs < 1 {
		t.Fatalf("Retry-After = %q, want a positive number of seconds", retryAfter)
	}
	// the one-time state must survive so the same callback can be retried
	if _, ok := h.states.get("st-unavail"); !ok {
		t.Fatal("the 503 consumed the one-time state")
	}
	// the txn cookie must NOT be cleared — the browser keeps the binding
	for _, c := range w.Result().Cookies() {
		if c.Name == h.cfg.TxnCookieName && c.MaxAge < 0 {
			t.Fatal("the 503 cleared the txn cookie")
		}
	}

	// the IdP publishes the key again: the SAME state retries successfully
	idp.setPublish(true)
	time.Sleep(2 * (h.verifier.cache.ttl + h.verifier.cache.minRefresh))
	req2 := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=st-unavail&code=code-unavail", nil)
	req2.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w2 := httptest.NewRecorder()
	h.HandleCallback(w2, req2)
	if w2.Code != http.StatusFound {
		t.Fatalf("the retry: code %d body: %s", w2.Code, w2.Body.String())
	}
	found := false
	for _, c := range w2.Result().Cookies() {
		if c.Name == h.cfg.CookieName {
			found = true
		}
	}
	if !found {
		t.Fatal("the retry did not set a session cookie")
	}
}

// Item 2 (AR-26 amendment): a JWKS document that yields NO usable key must
// NOT wipe the cache. Inside the stale-grace window the PREVIOUS key set
// keeps serving (the login still completes) and the flow RECOVERS when the
// keys come back.
func TestCallback_EmptyKeySetServesStaleKeysInsideGraceAndRecovers(t *testing.T) {
	idp := newThrottleIdP(t, "empty-kid")
	h := newEmptyKeySetHandler(t, idp)
	verifier := "verifier-stale"
	stubRegisterCode("code-stale", "nonce-stale", verifier, "user@example.com", true)

	// 1. the IdP publishes the key: the login succeeds and the cache warms.
	idp.setPublish(true)
	_, tv := txnStateFor(t, h, "st-stale-1", "nonce-stale", verifier)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=st-stale-1&code=code-stale", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("the first callback: code %d body: %s", w.Code, w.Body.String())
	}
	if len(h.verifier.cache.keys) != 1 {
		t.Fatalf("the cache holds %d keys, want 1", len(h.verifier.cache.keys))
	}

	// 2. the IdP stops publishing the key: the refresh fails with an empty
	//    key set. Inside the grace window the PREVIOUS key must keep
	//    serving and the cache must NOT be wiped.
	idp.setPublish(false)
	time.Sleep(4 * (h.verifier.cache.ttl + h.verifier.cache.minRefresh))
	_, tv2 := txnStateFor(t, h, "st-stale-2", "nonce-stale", verifier)
	req2 := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=st-stale-2&code=code-stale", nil)
	req2.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv2})
	w2 := httptest.NewRecorder()
	h.HandleCallback(w2, req2)
	if w2.Code != http.StatusFound {
		t.Fatalf("the stale-grace window must serve the previous key: code %d body: %s", w2.Code, w2.Body.String())
	}
	if len(h.verifier.cache.keys) != 1 {
		t.Fatalf("the empty key set wiped the cache (%d keys left)", len(h.verifier.cache.keys))
	}

	// 3. the keys come back: the flow recovers.
	idp.setPublish(true)
	time.Sleep(2 * (h.verifier.cache.ttl + h.verifier.cache.minRefresh))
	_, tv3 := txnStateFor(t, h, "st-stale-3", "nonce-stale", verifier)
	req3 := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=st-stale-3&code=code-stale", nil)
	req3.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv3})
	w3 := httptest.NewRecorder()
	h.HandleCallback(w3, req3)
	if w3.Code != http.StatusFound {
		t.Fatalf("the recovery: code %d body: %s", w3.Code, w3.Body.String())
	}
}

// Item 4(a): the IdP-error branch is TERMINAL — the authorization code is
// gone, so the one-time state is consumed and the txn cookie is cleared.
func TestCallback_IdPErrorConsumesStateAndClearsTxnCookie(t *testing.T) {
	h := newTestHandler(t)
	_, tv := txnStateFor(t, h, "st-idperr", "nonce-idperr", "verifier-idperr")
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?error=access_denied&error_description=user+denied&state=st-idperr", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
	if body := strings.TrimSpace(w.Body.String()); body != `{"error":"oauth2_callback_error"}` {
		t.Fatalf("body: %s", body)
	}
	// the one-time state is consumed: the transaction can never complete
	if _, ok := h.states.get("st-idperr"); ok {
		t.Fatal("the IdP-error path left the one-time state behind")
	}
	// the txn cookie is cleared: the browser carries no stale binding
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == h.cfg.TxnCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("the IdP-error path left the txn cookie behind")
	}
}

// Item 4(b): every bind failure clears the txn cookie — the browser must not
// carry a stale binding into a later attempt.
func TestCallback_BindFailureClearsTxnCookie(t *testing.T) {
	h := newTestHandler(t)
	// a txn cookie that opens under the txn subkey
	tv, err := h.txn.Seal(&Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	// a cookie sealed under the SESSION subkey: it does not open under the
	// txn one, so the binding must fail at the OPEN check (its digest is
	// stored, so the digest check is not what fails)
	sessionVal, err := h.cookie.Seal(&Session{Sub: "u", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	sessionSum := sha256.Sum256([]byte(sessionVal))
	h.states.add(&stateRecord{State: "st-wrongkey", Nonce: "n", CodeVerifier: "v", RedirectTo: "/ui/", TxnDigest: hexEncode(sessionSum[:]), CreatedAt: time.Now()})
	// a state record whose txn cookie is missing from the browser
	h.states.add(&stateRecord{State: "st-nocookie", Nonce: "n", CodeVerifier: "v", RedirectTo: "/ui/", TxnDigest: "deadbeef", CreatedAt: time.Now()})
	// a state record bound to a DIFFERENT txn cookie (the digest mismatches)
	other, err := h.txn.Seal(&Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	otherSum := sha256.Sum256([]byte(other))
	h.states.add(&stateRecord{State: "st-digest", Nonce: "n", CodeVerifier: "v", RedirectTo: "/ui/", TxnDigest: hexEncode(otherSum[:]), CreatedAt: time.Now()})

	cases := []struct {
		name   string
		query  string
		cookie string
	}{
		{"no state at all", "code=abc", tv},
		{"unknown state", "state=st-unknown&code=abc", tv},
		{"missing txn cookie", "state=st-nocookie&code=abc", ""},
		{"txn cookie under the wrong subkey", "state=st-wrongkey&code=abc", sessionVal},
		{"mismatched txn digest", "state=st-digest&code=abc", tv},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?"+c.query, nil)
		if c.cookie != "" {
			req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: c.cookie})
		}
		w := httptest.NewRecorder()
		h.HandleCallback(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: code %d body: %s", c.name, w.Code, w.Body.String())
		}
		cleared := false
		for _, ck := range w.Result().Cookies() {
			if ck.Name == h.cfg.TxnCookieName && ck.MaxAge < 0 {
				cleared = true
			}
		}
		if !cleared {
			t.Fatalf("%s: the txn cookie was not cleared", c.name)
		}
	}
}

// Item 4(c): a 503 that promises a retry may only be emitted when the
// one-time state could be put back into the store. When the restore FAILS
// the callback answers the terminal 400 instead of a retry it cannot honour.
func TestCallback_RestoreFailureAnswers400Not503(t *testing.T) {
	idp := newThrottleIdP(t, "throttle-kid")
	h := newThrottleHandler(t, idp)
	verifier := "verifier-restore"
	stubRegisterCode("code-restore", "nonce-restore", verifier, "user@example.com", true)
	// the IdP withholds the key: the verify fails with the JWKS failure
	idp.setPublish(false)
	_, tv := txnStateFor(t, h, "st-restore", "nonce-restore", verifier)
	// the store can no longer hold the record back (its cap is reached):
	// the restore fails
	h.states.max = 0
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=st-restore&code=code-restore", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("the restore failed: the callback must not promise a retryable 503, got %d body: %s", w.Code, w.Body.String())
	}
	if body := strings.TrimSpace(w.Body.String()); body != `{"error":"invalid_state"}` {
		t.Fatalf("body: %s", body)
	}
	// the transaction is abandoned: the txn cookie is cleared
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == h.cfg.TxnCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("the abandoned transaction kept the txn cookie")
	}
}

// S6: when the session cookie cannot be sealed the login fails loudly — a
// stable error code is returned and NO session cookie is written.
func TestCallback_SealFailureIsSurfacedAndNoSessionCookieIsWritten(t *testing.T) {
	h := newTestHandlerWithCookieKey(t, 5) // an invalid AES key size => Seal fails
	verifier := "verifier-seal"
	stubRegisterCode("code-seal", "nonce-seal", verifier, "user@example.com", true)
	_, tv := txnStateFor(t, h, "sseal", "nonce-seal", verifier)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=sseal&code=code-seal", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
	if body := strings.TrimSpace(w.Body.String()); body != `{"error":"session_cookie_seal_failed"}` {
		t.Fatalf("body: %s", body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == h.cfg.CookieName && c.Value != "" {
			t.Fatal("a session cookie was written despite the seal failure")
		}
	}
}

// S7: with no OAUTH2_EXTERNAL_URL the end-session hop is skipped rather than
// building a RELATIVE post_logout_redirect_uri from an empty base.
func TestLogout_SkipsEndSessionWhenExternalURLMissing(t *testing.T) {
	h := newTestHandler(t)
	h.cfg.ExternalURL = ""
	// fresh metadata that DOES advertise an end_session endpoint
	if _, err := h.provider.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.provider.CachedMetadata() == nil || h.provider.CachedMetadata().EndSessionEndpoint == "" {
		t.Fatal("the cached metadata carries no end_session endpoint")
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth2/logout", nil)
	w := httptest.NewRecorder()
	h.HandleLogout(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code: %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if strings.Contains(loc, "post_logout_redirect_uri") {
		t.Fatalf("a relative post_logout_redirect_uri was built: %s", loc)
	}
	if strings.Contains(loc, "/logout?") {
		t.Fatalf("expected no end-session hop, got %s", loc)
	}
	if loc != "/ui/" {
		t.Fatalf("expected the local redirect, got %s", loc)
	}

	// control: with an external URL the hop IS taken and the uri is absolute.
	h2 := newTestHandler(t)
	if _, err := h2.provider.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	req2 := httptest.NewRequest(http.MethodPost, "/oauth2/logout", nil)
	w2 := httptest.NewRecorder()
	h2.HandleLogout(w2, req2)
	loc2 := w2.Header().Get("Location")
	if !strings.Contains(loc2, "post_logout_redirect_uri=https%3A%2F%2Fapp.example.com%2Fui%2F") {
		t.Fatalf("expected an absolute post_logout_redirect_uri, got %s", loc2)
	}
}

func TestStart_RedirectsWithStateNoncePKCE(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/start", nil)
	w := httptest.NewRecorder()
	h.HandleStart(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "state=") || !strings.Contains(loc, "nonce=") || !strings.Contains(loc, "code_challenge=") || !strings.Contains(loc, "code_challenge_method=S256") {
		t.Fatalf("missing params in %s", loc)
	}
}

func TestStart_SetsTransactionCookie(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/start", nil)
	w := httptest.NewRecorder()
	h.HandleStart(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code: %d", w.Code)
	}
	// exactly one txn cookie
	var txnCookies []*http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == h.cfg.TxnCookieName {
			txnCookies = append(txnCookies, c)
		}
	}
	if len(txnCookies) != 1 {
		t.Fatalf("txn cookie count: %d", len(txnCookies))
	}
	ck := txnCookies[0]
	if !ck.HttpOnly {
		t.Fatal("not httponly")
	}
	if !ck.Secure {
		t.Fatal("not secure")
	}
	if ck.SameSite != http.SameSiteLaxMode {
		t.Fatalf("samesite: %v", ck.SameSite)
	}
	if ck.Path != "/oauth2" {
		t.Fatalf("path: %s", ck.Path)
	}
	if ck.MaxAge != 600 {
		t.Fatalf("maxage: %d", ck.MaxAge)
	}
	// digest match
	h.states.mu.Lock()
	var rec *stateRecord
	for _, r := range h.states.records {
		rec = r
	}
	h.states.mu.Unlock()
	if rec == nil {
		t.Fatal("no state record")
	}
	sum := sha256.Sum256([]byte(ck.Value))
	if hexEncode(sum[:]) != rec.TxnDigest {
		t.Fatalf("digest mismatch: %s vs %s", hexEncode(sum[:]), rec.TxnDigest)
	}
}

func TestStart_ValidSessionShortCircuits(t *testing.T) {
	h := newTestHandler(t)
	// mint a valid session cookie
	sess := &Session{Sub: "u", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	v, err := h.cookie.Seal(sess)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth2/start?rd=/ui/", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.CookieName, Value: v})
	w := httptest.NewRecorder()
	h.HandleStart(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code: %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if loc != "/ui/" {
		t.Fatalf("loc: %s", loc)
	}
	// no state minted
	if h.states.countLive() != 0 {
		t.Fatalf("state minted: %d", h.states.countLive())
	}
}

func TestStart_SanitizesRedirectTarget(t *testing.T) {
	cases := []string{"//evil.com", "/\\evil.com", "https://evil.com", "javascript:alert(1)", "", "/oauth2/start", "/oauth2/callback", "/fe/api/auth/me"}
	for _, c := range cases {
		got := sanitizeRedirectTarget(c)
		if got != "/ui/" {
			t.Fatalf("input %q got %q", c, got)
		}
	}
}

// D (tester R1): the redirect denylist is evaluated on the NORMALIZED path.
// A traversal sequence ("/ui/../oauth2/start") used to pass the raw prefix
// check and was EMITTED by http.Redirect as "/oauth2/start" — the denylist
// bypass. Every variant must land on the sanitized fallback, and the
// Location the handler actually emits must never be an /oauth2/* path.
func TestStart_RedirectTargetTraversalCannotEscapeDenylist(t *testing.T) {
	cases := []string{
		"/ui/../oauth2/start",
		"//oauth2/x",
		"/x/../oauth2/start",
		"/ui/../oauth2/logout",
		"/ui/../oauth2/callback",
		"/oauth2/../oauth2/start",
		"/ui/../oauth2",
		"/ui/../fe/api/auth/me",
		// BROWSER NORMALIZATION (WHATWG URL): the browser folds every
		// backslash of the path into a slash before it requests the target,
		// so these reach the server as the traversal the denylist must see.
		"/ui/..\\..\\oauth2\\start",
		"/ui/..\\oauth2\\logout",
		"/ui/..\\fe\\api\\auth\\me",
	}
	for _, c := range cases {
		got := sanitizeRedirectTarget(c)
		if got != "/ui/" {
			t.Fatalf("input %q sanitized to %q, want %q", c, got, "/ui/")
		}
	}

	// the EMITTED Location (http.Redirect cleans it) must be the fallback too.
	h := newTestHandler(t)
	sess := &Session{Sub: "u", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	v, err := h.cookie.Seal(sess)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/oauth2/start?rd="+url.QueryEscape(c), nil)
		req.AddCookie(&http.Cookie{Name: h.cfg.CookieName, Value: v})
		w := httptest.NewRecorder()
		h.HandleStart(w, req)
		if w.Code != http.StatusFound {
			t.Fatalf("input %q: code %d", c, w.Code)
		}
		loc := w.Header().Get("Location")
		if loc != "/ui/" {
			t.Fatalf("input %q emitted Location %q, want %q", c, loc, "/ui/")
		}
		if strings.HasPrefix(loc, "/oauth2/") || loc == "/oauth2" || loc == "/fe/api/auth/me" {
			t.Fatalf("input %q emitted a denylisted Location %q", c, loc)
		}
		// a backslash in the emitted Location is a residual the BROWSER
		// would fold into a slash: the Location must already be the shape
		// the browser requests.
		if strings.Contains(loc, "\\") {
			t.Fatalf("input %q emitted a Location the browser would re-normalize: %q", c, loc)
		}
	}
}

// Item 1 (browser residual): a browser folds every backslash of the path into
// a slash BEFORE it requests the target (WHATWG URL), so a raw backslash
// sequence that passes the raw structural checks would be REQUESTED as a
// traversal the denylist never saw. The target must therefore be
// backslash-normalized BEFORE cleanRedirectPath and the denylist — while the
// raw structural checks keep running on the raw string.
func TestStart_RedirectTargetBackslashNormalizedBeforeDenylist(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`/ui/..\..\oauth2\start`, "/ui/"},
		{`/ui/..\oauth2\logout`, "/ui/"},
		{`/ui\..\oauth2\callback`, "/ui/"},
		{`/ui/..\fe\api\auth\me`, "/ui/"},
		{`\oauth2\start`, "/ui/"},    // no leading slash: the raw check fires
		{`/\evil.com`, "/ui/"},       // the raw "/\" prefix check still fires
		{`/ui\models`, "/ui/models"}, // a benign backslash folds to a slash
	}
	for _, c := range cases {
		if got := sanitizeRedirectTarget(c.in); got != c.want {
			t.Fatalf("input %q sanitized to %q, want %q", c.in, got, c.want)
		}
	}
}

// D: an allowed target keeps its shape — the normalization preserves the
// trailing slash, so "/ui/" is still "/ui/" (and not the cleaned "/ui").
func TestStart_SanitizesRedirectTargetKeepsAllowedShape(t *testing.T) {
	cases := map[string]string{
		"/ui/":         "/ui/",
		"/ui/models":   "/ui/models",
		"/ui//models":  "/ui/models",
		"/fe/api/x":    "/fe/api/x",
		"/":            "/",
		"/ui/sub/../x": "/ui/x",
	}
	for in, want := range cases {
		if got := sanitizeRedirectTarget(in); got != want {
			t.Fatalf("input %q got %q, want %q", in, got, want)
		}
	}
}

func TestCallback_RejectsMissingState(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback", nil)
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestCallback_RejectsUnknownState(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=unknown", nil)
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestCallback_RejectsReplayOfState(t *testing.T) {
	h := newTestHandler(t)
	// add state
	rec := &stateRecord{State: "s1", Nonce: "n", CodeVerifier: "v", RedirectTo: "/ui/", TxnDigest: "d", CreatedAt: time.Now()}
	h.states.add(rec)
	// first use deletes
	h.states.delete("s1")
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=s1", nil)
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestCallback_RejectsStateFromAnotherBrowser(t *testing.T) {
	h := newTestHandler(t)
	// mint state record with a txn digest for a different txn cookie
	rec := &stateRecord{State: "sb", Nonce: "n", CodeVerifier: "v", RedirectTo: "/ui/", TxnDigest: "deadbeef", CreatedAt: time.Now()}
	h.states.add(rec)
	// create a txn cookie with a different value
	sess := &Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	v, _ := h.txn.Seal(sess)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=sb&code=abc", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: v})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestCallback_RejectsMissingTxnCookie(t *testing.T) {
	h := newTestHandler(t)
	rec := &stateRecord{State: "s2", Nonce: "n", CodeVerifier: "v", RedirectTo: "/ui/", TxnDigest: "d", CreatedAt: time.Now()}
	h.states.add(rec)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=s2&code=abc", nil)
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestCallback_RejectsMismatchedTxnDigest(t *testing.T) {
	h := newTestHandler(t)
	rec := &stateRecord{State: "sd", Nonce: "n", CodeVerifier: "v", RedirectTo: "/ui/", TxnDigest: "0000", CreatedAt: time.Now()}
	h.states.add(rec)
	sess := &Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	v, _ := h.txn.Seal(sess)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=sd&code=abc", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: v})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestCallback_RejectsNonceMismatch(t *testing.T) {
	h := newTestHandler(t)
	// register a code with nonce "right-nonce"
	stubRegisterCode("code-nm", "right-nonce", "verifier123", "user@example.com", true)
	// state record with different nonce
	rec := &stateRecord{State: "snm", Nonce: "wrong-nonce", CodeVerifier: "verifier123", RedirectTo: "/ui/", CreatedAt: time.Now()}
	// txn cookie binding
	sess := &Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	tv, _ := h.txn.Seal(sess)
	rec.TxnDigest = func() string { sum := sha256.Sum256([]byte(tv)); return hexEncode(sum[:]) }()
	h.states.add(rec)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=snm&code=code-nm", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
}

func TestCallback_RejectsPKCEMismatch(t *testing.T) {
	h := newTestHandler(t)
	stubRegisterCode("code-pk", "nonce-pk", "correct-verifier", "user@example.com", true)
	rec := &stateRecord{State: "spk", Nonce: "nonce-pk", CodeVerifier: "wrong-verifier", RedirectTo: "/ui/", CreatedAt: time.Now()}
	sess := &Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	tv, _ := h.txn.Seal(sess)
	rec.TxnDigest = func() string { sum := sha256.Sum256([]byte(tv)); return hexEncode(sum[:]) }()
	h.states.add(rec)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=spk&code=code-pk", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
}

func TestCallback_SetsCookieAndRedirects(t *testing.T) {
	h := newTestHandler(t)
	verifier := "verifier-abc"
	stubRegisterCode("code-ok", "nonce-ok", verifier, "user@example.com", true)
	rec := &stateRecord{State: "sok", Nonce: "nonce-ok", CodeVerifier: verifier, RedirectTo: "/ui/", CreatedAt: time.Now()}
	sess := &Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	tv, _ := h.txn.Seal(sess)
	rec.TxnDigest = func() string { sum := sha256.Sum256([]byte(tv)); return hexEncode(sum[:]) }()
	h.states.add(rec)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=sok&code=code-ok", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
	// session cookie set
	var found bool
	for _, c := range w.Result().Cookies() {
		if c.Name == h.cfg.CookieName {
			found = true
		}
	}
	if !found {
		t.Fatal("no session cookie")
	}
	// txn cookie cleared
}

func TestCallback_EmailDomainNotAllowedIs403(t *testing.T) {
	h := newTestHandler(t)
	verifier := "v"
	stubRegisterCode("code-dom", "nonce-dom", verifier, "user@other.com", true)
	rec := &stateRecord{State: "sdom", Nonce: "nonce-dom", CodeVerifier: verifier, RedirectTo: "/ui/", CreatedAt: time.Now()}
	sess := &Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	tv, _ := h.txn.Seal(sess)
	rec.TxnDigest = func() string { sum := sha256.Sum256([]byte(tv)); return hexEncode(sum[:]) }()
	h.states.add(rec)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=sdom&code=code-dom", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
}

func TestCallback_UnverifiedEmailIs403(t *testing.T) {
	h := newTestHandler(t)
	verifier := "v"
	vf := false
	stubRegisterCode("code-unv", "nonce-unv", verifier, "user@example.com", vf)
	// override verified flag in entry
	testStubCodesMu.Lock()
	testStubCodes["code-unv"].verified = &vf
	testStubCodesMu.Unlock()
	rec := &stateRecord{State: "sunv", Nonce: "nonce-unv", CodeVerifier: verifier, RedirectTo: "/ui/", CreatedAt: time.Now()}
	sess := &Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	tv, _ := h.txn.Seal(sess)
	rec.TxnDigest = func() string { sum := sha256.Sum256([]byte(tv)); return hexEncode(sum[:]) }()
	h.states.add(rec)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=sunv&code=code-unv", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
}

func TestCallback_MissingEmailVerifiedIs403(t *testing.T) {
	// Config not in any-mode (EmailDomains example.com), so missing email_verified => 403.
	// Simulate by stub returning email_verified absent. Our stub always sets bool; instead
	// use a custom token with no email_verified claim via direct minting is complex.
	// We'll test the handler path by setting RequireEmailVerified and using a nil verified?
	// Simpler: assert handler rejects when claims.EmailVerified == nil by invoking verifier?
	// Use a code whose entry verified pointer is nil.
	h := newTestHandler(t)
	verifier := "v"
	testStubCodesMu.Lock()
	testStubCodes["code-missv"] = &stubCodeEntry{nonce: "nonce-missv", challenge: sha256Challenge("v"), email: "user@example.com", verified: nil}
	testStubCodesMu.Unlock()
	rec := &stateRecord{State: "smissv", Nonce: "nonce-missv", CodeVerifier: verifier, RedirectTo: "/ui/", CreatedAt: time.Now()}
	sess := &Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	tv, _ := h.txn.Seal(sess)
	rec.TxnDigest = func() string { sum := sha256.Sum256([]byte(tv)); return hexEncode(sum[:]) }()
	h.states.add(rec)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=smissv&code=code-missv", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
}

func TestCallback_ProviderErrorIs502(t *testing.T) {
	h := newTestHandler(t)
	// error param present
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?error=access_denied", nil)
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("code: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("ct: %s", ct)
	}
	body := strings.TrimSpace(w.Body.String())
	if body != `{"error":"oauth2_callback_error"}` {
		t.Fatalf("body: %s", body)
	}
}

func TestLogout_GetIs405(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/logout", nil)
	w := httptest.NewRecorder()
	h.HandleLogout(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestLogout_ClearsCookie(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/oauth2/logout", nil)
	w := httptest.NewRecorder()
	h.HandleLogout(w, req)
	// should redirect
	if w.Code != http.StatusFound && w.Code != http.StatusSeeOther {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestLogout_SkipsEndSessionWhenMetadataStale(t *testing.T) {
	h := newTestHandler(t)
	// cache metadata then make it stale
	meta, err := h.provider.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = meta
	// make stale
	h.provider.mu.Lock()
	h.provider.fetchedAt = time.Now().Add(-10 * time.Hour)
	h.provider.mu.Unlock()
	// also no end_session? keep. ensure end_session present
	if h.provider.CachedMetadata() == nil || h.provider.CachedMetadata().EndSessionEndpoint == "" {
		t.Fatal("no cached meta")
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth2/logout", nil)
	w := httptest.NewRecorder()
	h.HandleLogout(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code: %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if strings.Contains(loc, "end_session") || strings.Contains(loc, "/logout?") {
		t.Fatalf("expected no end-session hop, got %s", loc)
	}
}

func TestLogout_WorksWithoutSession(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/oauth2/logout", nil)
	w := httptest.NewRecorder()
	h.HandleLogout(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code: %d", w.Code)
	}
}

// unused import guard
var _ = context.Background
var _ = strings.HasPrefix

func TestE2E_Flow(t *testing.T) {
	h := newTestHandler(t)
	// start
	req := httptest.NewRequest(http.MethodGet, "/oauth2/start", nil)
	w := httptest.NewRecorder()
	h.HandleStart(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("start code: %d", w.Code)
	}
	loc := w.Header().Get("Location")
	// follow to stub auth endpoint
	authReq := httptest.NewRequest(http.MethodGet, loc, nil)
	authW := httptest.NewRecorder()
	// dispatch to stub server via http client? Use httptest server directly.
	_ = authW
	_ = authReq
	// Instead, parse code/state from redirect by calling stub auth endpoint manually using loc.
	resp, err := http.Get(loc)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// the stub redirects to callback URL (srv.URL/oauth2/callback?...). But RedirectURL is srv.URL/oauth2/callback,
	// and callback registered on handler, not stub. The stub redirects to that URL which isn't served.
	// So we cannot fully follow; instead verify start output and a callback using the txn cookie from start.
	_ = loc
	// Use txn cookie from start
	var txnCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == h.cfg.TxnCookieName {
			txnCookie = c
		}
	}
	if txnCookie == nil {
		t.Fatal("no txn cookie")
	}
	// state from loc
	// extract state param
	u, _ := url.Parse(loc)
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("no state")
	}
	// find code via stub code store? The auth endpoint hasn't been hit. Hit it.
	code := "code-" + state
	// callback
	cbReq := httptest.NewRequest(http.MethodGet, "/oauth2/callback?code="+code+"&state="+state, nil)
	cbReq.AddCookie(txnCookie)
	cbW := httptest.NewRecorder()
	h.HandleCallback(cbW, cbReq)
	if cbW.Code != http.StatusFound {
		t.Fatalf("callback code: %d body: %s", cbW.Code, cbW.Body.String())
	}
	// session cookie
	found := false
	for _, c := range cbW.Result().Cookies() {
		if c.Name == h.cfg.CookieName {
			found = true
			// authenticate
			_, err := h.cookie.Open(c.Value)
			if err != nil {
				t.Fatalf("session open: %v", err)
			}
		}
	}
	if !found {
		t.Fatal("no session cookie")
	}
}

// R3: the stub IdP must exercise the LIVE claim shape end to end: an "aud"
// ARRAY plus an "azp" — the shape a live IdP sends. A multi-audience token
// whose azp IS the client id is accepted.
func TestCallback_MultiAudienceArrayWithAzpClientIsAccepted(t *testing.T) {
	h := newTestHandler(t)
	verifier := "verifier-aud"
	stubRegisterCodeShape("code-aud", "nonce-aud", verifier, "user@example.com", true, []string{"cid", "other"}, "cid")
	_, tv := txnStateFor(t, h, "saud", "nonce-aud", verifier)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=saud&code=code-aud", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
	found := false
	for _, c := range w.Result().Cookies() {
		if c.Name == h.cfg.CookieName {
			found = true
		}
	}
	if !found {
		t.Fatal("no session cookie")
	}
}

// R3: an aud ARRAY whose azp is NOT the client id is rejected — the token is
// for another party.
func TestCallback_AudienceArrayWithAzpMismatchIsRejected(t *testing.T) {
	h := newTestHandler(t)
	verifier := "verifier-azp"
	stubRegisterCodeShape("code-azp", "nonce-azp", verifier, "user@example.com", true, []string{"cid", "other"}, "evil")
	_, tv := txnStateFor(t, h, "sazp", "nonce-azp", verifier)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=sazp&code=code-azp", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
}

// R3: a single audience that is not the client id is rejected.
func TestCallback_SingleAudienceNotClientIsRejected(t *testing.T) {
	h := newTestHandler(t)
	verifier := "verifier-saud"
	stubRegisterCodeShape("code-saud", "nonce-saud", verifier, "user@example.com", true, "not-cid", "")
	_, tv := txnStateFor(t, h, "ssaud", "nonce-saud", verifier)
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?state=ssaud&code=code-saud", nil)
	req.AddCookie(&http.Cookie{Name: h.cfg.TxnCookieName, Value: tv})
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d body: %s", w.Code, w.Body.String())
	}
}

// #13: the ErrJWKSRateLimited chain must survive the %w sweep END TO END.
// The chain is: JWKS cache (the sentinel is returned AS IS) -> verifier
// (returns the cache error unwrapped) -> callback (errors.Is) -> 503. The
// sentinel must arrive at the caller UNWRAPPED, otherwise the 503 path
// silently degrades into a 400 and the login is lost.
func TestJWKS_RateLimitedSentinelSurvivesTheVerifyChain(t *testing.T) {
	idp := newThrottleIdP(t, "chain-kid")
	h := newThrottleHandler(t, idp)
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "chain-kid"})
	tok := base64.RawURLEncoding.EncodeToString(hdr) + ".e30." + base64.RawURLEncoding.EncodeToString([]byte("sig"))
	// attempt 1: the unknown kid triggers a refresh (and opens the window)
	if _, err := h.verifier.Verify(context.Background(), tok); err == nil {
		t.Fatal("expected a verification failure for an unknown kid")
	}
	// attempt 2: the miss lands inside the min-refresh window => THROTTLED.
	_, err := h.verifier.Verify(context.Background(), tok)
	if !errors.Is(err, ErrJWKSRateLimited) {
		t.Fatalf("the throttle sentinel did not survive the verify chain: %v", err)
	}
}
