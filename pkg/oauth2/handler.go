package oauth2

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/identity"
)

type Handler struct {
	cfg      *Config
	provider *OIDCProvider
	verifier *Verifier
	cookie   *CookieCodec
	txn      *CookieCodec
	states   *stateStore
	limiter  *rateLimiter
}

type stateRecord struct {
	State        string
	Nonce        string
	CodeVerifier string
	RedirectTo   string
	TxnDigest    string
	CreatedAt    time.Time
}

type stateStore struct {
	mu          sync.Mutex
	records     map[string]*stateRecord
	max         int
	count       int
	evictedOnce bool
}

func newStateStore(max int) *stateStore {
	return &stateStore{records: map[string]*stateRecord{}, max: max}
}

func (s *stateStore) add(rec *stateRecord) (bool, bool) {
	// returns (added, overflow429)
	s.mu.Lock()
	defer s.mu.Unlock()
	// prune expired
	now := time.Now()
	for k, v := range s.records {
		if now.Sub(v.CreatedAt) > 10*time.Minute {
			delete(s.records, k)
			s.count--
		}
	}
	if len(s.records) >= s.max {
		if !s.evictedOnce {
			var oldestKey string
			var oldestTime time.Time
			first := true
			for k, v := range s.records {
				if first || v.CreatedAt.Before(oldestTime) {
					oldestKey = k
					oldestTime = v.CreatedAt
					first = false
				}
			}
			if oldestKey != "" {
				delete(s.records, oldestKey)
				s.count--
				s.evictedOnce = true
			}
		} else {
			return false, true
		}
		if len(s.records) >= s.max {
			return false, true
		}
	}
	s.records[rec.State] = rec
	s.count++
	return true, false
}

func (s *stateStore) get(state string) (*stateRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[state]
	if !ok {
		return nil, false
	}
	if time.Since(rec.CreatedAt) > 10*time.Minute {
		delete(s.records, state)
		s.count--
		return nil, false
	}
	return rec, true
}

func (s *stateStore) delete(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[state]; ok {
		delete(s.records, state)
		s.count--
	}
}

// consume atomically gets and deletes a state record under the same lock,
// closing the replay TOCTOU window (M7).
func (s *stateStore) consume(state string) (*stateRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[state]
	if !ok {
		return nil, false
	}
	if time.Since(rec.CreatedAt) > 10*time.Minute {
		delete(s.records, state)
		s.count--
		return nil, false
	}
	delete(s.records, state)
	s.count--
	return rec, true
}

// restore re-inserts a consumed state record. It is used when a TRANSIENT
// upstream failure (e.g. a throttled JWKS refresh) must not burn the one-time
// state: the operator's login survives the throttle window and the same state
// can be retried. It never resurrects a record the store cannot hold.
func (s *stateStore) restore(rec *stateRecord) bool {
	if rec == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[rec.State]; exists {
		return false
	}
	if len(s.records) >= s.max {
		return false
	}
	s.records[rec.State] = rec
	s.count++
	return true
}

func (s *stateStore) countLive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	// prune
	now := time.Now()
	for k, v := range s.records {
		if now.Sub(v.CreatedAt) > 10*time.Minute {
			delete(s.records, k)
			s.count--
		}
	}
	if s.count < 0 {
		s.count = len(s.records)
	}
	return len(s.records)
}

// rate limiter token bucket per IP
type rateLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*bucket
	rpm        int
	burst      int
	maxBuckets int
	idleTTL    time.Duration
}

// Policy (per-IP bucket map pruning): the bucket map is keyed on the client
// IP, so it must not grow without bound. On every insert of a NEW bucket the
// map is swept of buckets that have been idle for longer than idleTTL; if the
// map is still at the cap afterwards, the oldest bucket is evicted. The map is
// therefore hard-bounded by maxBuckets.
const (
	rateLimiterMaxBuckets = 4096
	rateLimiterIdleTTL    = 10 * time.Minute
)

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rpm, burst int) *rateLimiter {
	return &rateLimiter{
		buckets:    map[string]*bucket{},
		rpm:        rpm,
		burst:      burst,
		maxBuckets: rateLimiterMaxBuckets,
		idleTTL:    rateLimiterIdleTTL,
	}
}

// pruneLocked drops buckets that have been idle for longer than idleTTL.
func (rl *rateLimiter) pruneLocked(now time.Time) {
	for ip, b := range rl.buckets {
		if now.Sub(b.last) > rl.idleTTL {
			delete(rl.buckets, ip)
		}
	}
}

// evictOldestLocked drops the single oldest bucket — the hard cap backstop
// when the idle sweep did not free a slot.
func (rl *rateLimiter) evictOldestLocked() {
	oldestIP := ""
	var oldest time.Time
	first := true
	for ip, b := range rl.buckets {
		if first || b.last.Before(oldest) {
			oldestIP = ip
			oldest = b.last
			first = false
		}
	}
	if oldestIP != "" {
		delete(rl.buckets, oldestIP)
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok {
		// a new client IP: sweep the idle buckets first so the map cannot be
		// used as an unbounded memory sink.
		rl.pruneLocked(now)
		if len(rl.buckets) >= rl.maxBuckets {
			rl.evictOldestLocked()
		}
		b = &bucket{tokens: float64(rl.burst), last: now}
		rl.buckets[ip] = b
	}
	// refill
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * float64(rl.rpm) / 60.0
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens -= 1
		return true
	}
	return false
}

func NewHandler(cfg *Config, provider *OIDCProvider, verifier *Verifier, cookie, txn *CookieCodec) *Handler {
	h := &Handler{
		cfg:      cfg,
		provider: provider,
		verifier: verifier,
		cookie:   cookie,
		txn:      txn,
		states:   newStateStore(cfg.StateStoreMaxRecords()),
	}
	rpm, burst := cfg.RateLimit()
	h.limiter = newRateLimiter(rpm, burst)
	return h
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/oauth2/start", h.HandleStart)
	mux.HandleFunc("/oauth2/callback", h.HandleCallback)
	mux.HandleFunc("/oauth2/logout", h.HandleLogout)
}

// identity.Provider implementation
func (h *Handler) Enabled() bool { return h.cfg != nil && h.cfg.Enabled }

func (h *Handler) Current(r *http.Request) (*identity.Identity, error) {
	if h.cookie == nil {
		return nil, fmt.Errorf("no cookie codec")
	}
	c, err := h.cookie.Open(cookieValue(r, h.cfg.CookieName))
	if err != nil {
		return nil, err
	}
	return &identity.Identity{Sub: c.Sub, Email: c.Email, Name: c.Name}, nil
}

func (h *Handler) StateStoreCount() int {
	if h.states == nil {
		return 0
	}
	return h.states.countLive()
}

func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func (h *Handler) HandleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// rate limit
	ip := hostPart(r.RemoteAddr)
	if !h.limiter.allow(ip) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	// valid session short-circuit
	if val := cookieValue(r, h.cfg.CookieName); val != "" {
		if _, err := h.cookie.Open(val); err == nil {
			rd := sanitizeRedirectTarget(r.URL.Query().Get("rd"))
			http.Redirect(w, r, rd, http.StatusFound)
			return
		}
	}
	rd := sanitizeRedirectTarget(r.URL.Query().Get("rd"))

	// check oauth2 paths
	// generate state etc
	state := randomToken(32)
	nonce := randomToken(32)
	codeVerifier := randomToken(32)
	codeChallenge := s256(codeVerifier)

	// txn cookie value
	txnSession := &Session{IssuedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}
	txnCookieValue, err := h.txn.Seal(txnSession)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	txnDigest := sha256.Sum256([]byte(txnCookieValue))
	txnDigestHex := hex.EncodeToString(txnDigest[:])

	rec := &stateRecord{
		State:        state,
		Nonce:        nonce,
		CodeVerifier: codeVerifier,
		RedirectTo:   rd,
		TxnDigest:    txnDigestHex,
		CreatedAt:    time.Now(),
	}
	added, overflow := h.states.add(rec)
	if !added {
		if overflow {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "state store full", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "state store full", http.StatusTooManyRequests)
		return
	}

	// set txn cookie: exactly ONE, whose value's SHA-256 equals the stored TxnDigest.
	http.SetCookie(w, &http.Cookie{
		Name:     h.cfg.TxnCookieName,
		Value:    txnCookieValue,
		Path:     "/oauth2",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	// redirect to authorization endpoint
	meta, err := h.provider.Discover(r.Context())
	if err != nil {
		http.Error(w, "oidc_discovery_unavailable", http.StatusBadGateway)
		return
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", h.cfg.ClientID)
	q.Set("redirect_uri", h.cfg.RedirectURL)
	q.Set("scope", "openid profile email")
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	authURL := meta.AuthorizationEndpoint + "?" + q.Encode()
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (h *Handler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ip := hostPart(r.RemoteAddr)
	if !h.limiter.allow(ip) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		writeJSONError(w, http.StatusBadGateway, "oauth2_callback_error")
		return
	}

	state := r.URL.Query().Get("state")
	if state == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	rec, ok := h.states.consume(state)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "invalid_state")
		return
	}

	// txn cookie binding
	txnCookie, err := r.Cookie(h.cfg.TxnCookieName)
	if err != nil || txnCookie == nil || txnCookie.Value == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	// open under txn subkey
	_, err = h.txn.Open(txnCookie.Value)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	// sha256 compare
	actual := sha256.Sum256([]byte(txnCookie.Value))
	actualHex := hex.EncodeToString(actual[:])
	if subtle.ConstantTimeCompare([]byte(actualHex), []byte(rec.TxnDigest)) != 1 {
		writeJSONError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	// delete txn cookie — done ONLY on the paths that complete the transaction.
	// A transient failure (throttled JWKS refresh) must leave the txn cookie
	// intact so the same login can be retried.
	code := r.URL.Query().Get("code")
	if code == "" {
		h.txn.ClearCookie(w)
		writeJSONError(w, http.StatusBadRequest, "invalid_state")
		// spec says 400 invalid_state? Actually code missing => 400 (no specific). Use invalid_state.
		return
	}

	idToken, accessToken, err := h.provider.ExchangeCode(r.Context(), code, h.cfg.RedirectURL, rec.CodeVerifier)
	if err != nil {
		h.txn.ClearCookie(w)
		writeJSONError(w, http.StatusBadGateway, "token_exchange_failed")
		return
	}
	claims, err := h.verifier.Verify(r.Context(), idToken)
	if err != nil {
		if errors.Is(err, ErrJWKSRateLimited) {
			// The IdP key set is momentarily throttled. This is NOT a failed
			// login: answer 503 and put the one-time state back so the
			// operator can retry the same callback once the throttle window
			// passes. The txn cookie is deliberately NOT cleared.
			h.states.restore(rec)
			writeJSONError(w, http.StatusServiceUnavailable, "jwks_rate_limited")
			return
		}
		h.txn.ClearCookie(w)
		writeJSONError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	// the transaction completed: the txn cookie is no longer needed.
	h.txn.ClearCookie(w)
	// nonce check
	if claims.Nonce != rec.Nonce {
		writeJSONError(w, http.StatusBadRequest, "invalid_nonce")
		return
	}

	// principal admission
	email := claims.Email
	if email == "" && h.provider != nil && claims.Subject != "" && accessToken != "" {
		// fetch userinfo
		ui, err := h.provider.FetchUserinfo(r.Context(), accessToken, claims.Subject)
		if err == nil && ui != nil {
			if e, _ := ui["email"].(string); e != "" {
				email = e
			}
			// email_verified from userinfo? id token preferred; if absent in id token, userinfo may provide
			if claims.EmailVerified == nil {
				if v, ok := ui["email_verified"].(bool); ok {
					bv := v
					claims.EmailVerified = &bv
				}
			}
		} else if err != nil {
			// ignore? spec: userinfo best-effort. But sub mismatch => 403
			if err.Error() == "userinfo sub mismatch" {
				writeJSONError(w, http.StatusForbidden, "email_missing")
				return
			}
		}
	}
	if email == "" {
		writeJSONError(w, http.StatusForbidden, "email_missing")
		return
	}
	// email_verified strictness
	verified := claims.EmailVerified
	domainAllowedAny := false
	for _, d := range h.cfg.EmailDomains {
		if d == "*" {
			domainAllowedAny = true
		}
	}
	anyMode := domainAllowedAny && h.cfg.AllowAnyDomain
	if verified == nil {
		if !anyMode {
			writeJSONError(w, http.StatusForbidden, "email_not_verified")
			return
		}
	} else if !*verified {
		writeJSONError(w, http.StatusForbidden, "email_not_verified")
		return
	}
	// domain allowlist
	domain := ""
	if idx := strings.LastIndex(email, "@"); idx >= 0 {
		domain = email[idx+1:]
	}
	if !domainAllowed(h.cfg.EmailDomains, domain) {
		writeJSONError(w, http.StatusForbidden, "email_domain_not_allowed")
		return
	}

	name := claims.Name
	session := &Session{
		Sub:       claims.Subject,
		Email:     email,
		Name:      name,
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(h.cfg.SessionTTL).Unix(),
	}
	// A seal failure is NEVER swallowed: no session cookie is written and the
	// caller is told. The stable code is logged; the value never is.
	if err := h.cookie.SetCookie(w, session); err != nil {
		log.Printf("[oauth2] %s: session cookie not written (%v)", ErrCodeSessionCookieSeal, err)
		writeJSONError(w, http.StatusInternalServerError, ErrCodeSessionCookieSeal)
		return
	}

	redirectTo := rec.RedirectTo
	if redirectTo == "" {
		redirectTo = "/ui/"
	}
	http.Redirect(w, r, redirectTo, http.StatusFound)
}

func (h *Handler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.cookie.ClearCookie(w)
	// best-effort end session
	redirectTo := h.cfg.LogoutRedirect
	if redirectTo == "" {
		redirectTo = "/ui/"
	}
	// if cached discovery metadata is fresh AND provides end_session_endpoint, take the hop.
	// AR-23: a stale cache (or none) skips the hop without forcing a refresh.
	endSession := ""
	meta := h.provider.CachedMetadata()
	fetchedAt := h.provider.FetchedAt()
	if meta != nil && meta.EndSessionEndpoint != "" && !fetchedAt.IsZero() && time.Since(fetchedAt) < h.cfg.JWKSCacheTTL {
		endSession = meta.EndSessionEndpoint
	}
	if meta == nil {
		// best-effort: try a fresh discover; a just-fetched doc is fresh
		m, err := h.provider.Discover(r.Context())
		if err == nil && m != nil && m.EndSessionEndpoint != "" {
			if ft := h.provider.FetchedAt(); !ft.IsZero() && time.Since(ft) < h.cfg.JWKSCacheTTL {
				endSession = m.EndSessionEndpoint
			}
		}
	}
	if endSession != "" {
		// post_logout_redirect_uri MUST be absolute. With an empty ExternalURL
		// the concatenation below would yield a RELATIVE uri ("/ui/"), which
		// the IdP rejects or mis-resolves. Policy: no-hop fallback — skip the
		// end-session hop rather than build a uri from an empty base.
		if h.cfg.ExternalURL == "" {
			log.Printf("[oauth2] %s: end-session hop skipped, OAUTH2_EXTERNAL_URL is not set", ErrCodeLogoutNoExternalURL)
			http.Redirect(w, r, redirectTo, http.StatusFound)
			return
		}
		u := endSession
		q := url.Values{}
		q.Set("post_logout_redirect_uri", h.cfg.ExternalURL+redirectTo)
		if h.cfg.ClientID != "" {
			q.Set("client_id", h.cfg.ClientID)
		}
		http.Redirect(w, r, u+"?"+q.Encode(), http.StatusFound)
		return
	}
	http.Redirect(w, r, redirectTo, http.StatusFound)
}

func domainAllowed(domains []string, domain string) bool {
	for _, d := range domains {
		if d == "*" {
			return true
		}
		if strings.EqualFold(d, domain) {
			return true
		}
	}
	return false
}

// sanitizeRedirectTarget validates the operator-supplied post-login redirect
// target ("rd") and falls back to /ui/ for every shape that is not a safe
// same-origin path.
//
// The denylist is evaluated against the NORMALIZED path (path.Clean), because
// http.Redirect cleans the Location it emits: a target like
// "/ui/../oauth2/start" passes a raw prefix check but is EMITTED as
// "/oauth2/start" — the denylist bypass the tester recorded as R1. The
// structural checks (single leading slash, no "//" or "/\" prefix, no control
// characters, no scheme) run on the RAW string BEFORE the normalization,
// because path.Clean would fold "//host" into "/host" and hide the
// scheme-relative shape.
func sanitizeRedirectTarget(rd string) string {
	rd = strings.TrimSpace(rd)
	if rd == "" {
		return "/ui/"
	}
	// must start with single /
	if !strings.HasPrefix(rd, "/") {
		return "/ui/"
	}
	if strings.HasPrefix(rd, "//") || strings.HasPrefix(rd, "/\\") {
		return "/ui/"
	}
	// reject control chars
	for _, c := range rd {
		if c < 0x20 || c == 0x7f {
			return "/ui/"
		}
	}
	// reject scheme injection (already no scheme if starts with /, but check)
	if strings.Contains(rd, "://") {
		return "/ui/"
	}
	// NORMALIZE before the denylist: the emitted Location is path.Clean'd by
	// http.Redirect, so the denylist must be judged on that same shape.
	rd = cleanRedirectPath(rd)
	// reject /oauth2/* and /fe/api/auth/me
	if strings.HasPrefix(rd, "/oauth2/") || rd == "/oauth2/start" || rd == "/oauth2/callback" || rd == "/oauth2/logout" {
		return "/ui/"
	}
	if rd == "/fe/api/auth/me" {
		return "/ui/"
	}
	// Also reject /oauth2 (exact)
	if rd == "/oauth2" {
		return "/ui/"
	}
	return rd
}

// cleanRedirectPath normalizes a redirect target exactly the way
// http.Redirect normalizes the Location it emits (path.Clean), preserving the
// trailing slash so an allowed target keeps the shape the operator asked for
// ("/ui/" stays "/ui/", it does not become "/ui").
func cleanRedirectPath(p string) string {
	cleaned := path.Clean(p)
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func s256(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func hostPart(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func writeJSONError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": code})
}
