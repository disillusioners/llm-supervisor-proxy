package oauth2

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

var (
	testStubServer  *httptest.Server
	testStubPriv    *rsa.PrivateKey
	testStubKid     = "test-kid"
	testStubCodesMu sync.Mutex
	testStubCodes   = map[string]*stubCodeEntry{}
)

type stubCodeEntry struct {
	nonce     string
	challenge string
	email     string
	verified  *bool
	// aud is the "aud" claim of the minted id_token: a string OR a []string.
	// A live IdP sends an ARRAY (R3) — the stub must be able to exercise
	// that shape end to end, not only the single-string shape.
	aud any
	// azp is the "azp" claim (empty = absent).
	azp string
}

// stubInvalidGrantBody is the canonical OAuth2 error document the stub IdP
// returns for a rejected authorization code. ONE definition, reused by every
// stub server in this package.
const stubInvalidGrantBody = `{"error":"invalid_grant"}`

// stubWriteInvalidGrant writes the canonical rejection document.
func stubWriteInvalidGrant(w http.ResponseWriter) {
	w.WriteHeader(400)
	w.Write([]byte(stubInvalidGrantBody))
}

// stubTokenShape is the canonical shape of a stub id_token: the JOSE header,
// the claim set and the error document are defined ONCE here and reused by
// every stub IdP in this package, so the mock shape cannot drift between
// them (R5).
type stubTokenShape struct {
	issuer   string
	kid      string
	nonce    string
	email    string
	name     string
	verified *bool
	// aud is the "aud" claim: a string OR a []string (the live IdP shape).
	aud any
	// azp is the "azp" claim (empty = absent).
	azp string
}

// stubJOSEHeader is the canonical JOSE header of a stub id_token.
func (s stubTokenShape) stubJOSEHeader() map[string]string {
	return map[string]string{"alg": "RS256", "kid": s.kid, "typ": "JWT"}
}

// stubClaims is the canonical claim set of a stub id_token.
func (s stubTokenShape) stubClaims() map[string]any {
	claims := map[string]any{
		"iss":   s.issuer,
		"sub":   "user1",
		"aud":   s.aud,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
		"nonce": s.nonce,
		"email": s.email,
		"name":  s.name,
	}
	if s.verified != nil {
		claims["email_verified"] = *s.verified
	}
	if s.azp != "" {
		claims["azp"] = s.azp
	}
	return claims
}

// stubSignToken signs the canonical shape into a compact JWS.
func (s stubTokenShape) stubSignToken(priv *rsa.PrivateKey) string {
	hb, _ := json.Marshal(s.stubJOSEHeader())
	pb, _ := json.Marshal(s.stubClaims())
	signing := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(pb)
	hh := sha256.Sum256([]byte(signing))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hh[:])
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func initStubKey(t *testing.T) {
	if testStubPriv == nil {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		testStubPriv = k
	}
}

func registerStubCode(code string, entry *stubCodeEntry) {
	testStubCodesMu.Lock()
	defer testStubCodesMu.Unlock()
	testStubCodes[code] = entry
}

func newStubIdP(t *testing.T) (*httptest.Server, *rsa.PrivateKey) {
	initStubKey(t)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 srv.URL,
				"authorization_endpoint": srv.URL + "/auth",
				"token_endpoint":         srv.URL + "/token",
				"jwks_uri":               srv.URL + "/jwks",
				"userinfo_endpoint":      srv.URL + "/userinfo",
				"end_session_endpoint":   srv.URL + "/logout",
			})
		case r.URL.Path == "/jwks":
			w.Header().Set("Content-Type", "application/json")
			n := testStubPriv.N.Bytes()
			e := []byte{0x01, 0x00, 0x01}
			json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]string{
					{
						"kty": "RSA",
						"kid": testStubKid,
						"use": "sig",
						"alg": "RS256",
						"n":   base64.RawURLEncoding.EncodeToString(n),
						"e":   base64.RawURLEncoding.EncodeToString(e),
					},
				},
			})
		case r.URL.Path == "/auth":
			state := r.URL.Query().Get("state")
			nonce := r.URL.Query().Get("nonce")
			challenge := r.URL.Query().Get("code_challenge")
			code := "code-" + state
			verified := true
			email := "user@example.com"
			testStubCodesMu.Lock()
			testStubCodes[code] = &stubCodeEntry{nonce: nonce, challenge: challenge, email: email, verified: &verified}
			testStubCodesMu.Unlock()
			rd := r.URL.Query().Get("redirect_uri")
			u := rd + "?code=" + code + "&state=" + state
			http.Redirect(w, r, u, http.StatusFound)
		case r.URL.Path == "/token":
			r.ParseForm()
			code := r.FormValue("code")
			verifier := r.FormValue("code_verifier")
			testStubCodesMu.Lock()
			entry, ok := testStubCodes[code]
			testStubCodesMu.Unlock()
			if ok && entry.challenge != "" {
				h := sha256.Sum256([]byte(verifier))
				got := base64.RawURLEncoding.EncodeToString(h[:])
				if got != entry.challenge {
					stubWriteInvalidGrant(w)
					return
				}
			}
			nonce := "test-nonce"
			email := "user@example.com"
			verified := true
			aud := any("cid")
			azp := ""
			if ok {
				nonce = entry.nonce
				email = entry.email
				if entry.verified != nil {
					verified = *entry.verified
				}
				if entry.aud != nil {
					aud = entry.aud
				}
				azp = entry.azp
			}
			// the claim set of an unknown code carries email_verified; a known
			// code carries it only when its entry pins it.
			var verifiedPtr *bool
			if !ok {
				verifiedPtr = &verified
			} else if entry.verified != nil {
				v := *entry.verified
				verifiedPtr = &v
			}
			shape := stubTokenShape{
				issuer:   srv.URL,
				kid:      testStubKid,
				nonce:    nonce,
				email:    email,
				name:     "Test User",
				verified: verifiedPtr,
				aud:      aud,
				azp:      azp,
			}
			tok := shape.stubSignToken(testStubPriv)
			json.NewEncoder(w).Encode(map[string]string{"id_token": tok, "access_token": "at"})
		case r.URL.Path == "/userinfo":
			json.NewEncoder(w).Encode(map[string]any{"sub": "user1", "email": "user@example.com", "email_verified": true})
		case r.URL.Path == "/logout":
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	testStubServer = srv
	return srv, testStubPriv
}

// mint a code entry manually for tests
func stubRegisterCode(code, nonce, verifier string, email string, verified bool) {
	stubRegisterCodeShape(code, nonce, verifier, email, verified, nil, "")
}

// stubRegisterCodeShape mints a code entry whose id_token carries a custom
// audience (a string OR a []string — the live IdP sends an array) and an
// optional authorized party (R3). A nil aud keeps the stub default ("cid").
func stubRegisterCodeShape(code, nonce, verifier string, email string, verified bool, aud any, azp string) {
	challenge := ""
	if verifier != "" {
		h := sha256.Sum256([]byte(verifier))
		challenge = base64.RawURLEncoding.EncodeToString(h[:])
	}
	v := verified
	registerStubCode(code, &stubCodeEntry{nonce: nonce, challenge: challenge, email: email, verified: &v, aud: aud, azp: azp})
}

func sha256Challenge(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// throttleIdP is a stub IdP whose JWKS document can withhold the signing key
// (a "kid miss") and publish it later — the exact shape that produces the JWKS
// refresh throttle (ErrJWKSRateLimited) on the second miss inside the
// min-refresh window.
type throttleIdP struct {
	server  *httptest.Server
	priv    *rsa.PrivateKey
	kid     string
	mu      sync.Mutex
	publish bool
}

func newThrottleIdP(t *testing.T, kid string) *throttleIdP {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &throttleIdP{priv: priv, kid: kid}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 p.server.URL,
				"authorization_endpoint": p.server.URL + "/auth",
				"token_endpoint":         p.server.URL + "/token",
				"jwks_uri":               p.server.URL + "/jwks",
			})
		case "/jwks":
			p.mu.Lock()
			publish := p.publish
			p.mu.Unlock()
			keys := []map[string]string{}
			if publish {
				keys = append(keys, map[string]string{
					"kty": "RSA",
					"kid": p.kid,
					"use": "sig",
					"alg": "RS256",
					"n":   base64.RawURLEncoding.EncodeToString(p.priv.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01}),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"keys": keys})
		case "/token":
			r.ParseForm()
			code := r.FormValue("code")
			verifier := r.FormValue("code_verifier")
			testStubCodesMu.Lock()
			entry, ok := testStubCodes[code]
			testStubCodesMu.Unlock()
			if !ok {
				stubWriteInvalidGrant(w)
				return
			}
			if entry.challenge != "" && entry.challenge != sha256Challenge(verifier) {
				stubWriteInvalidGrant(w)
				return
			}
			verified := true
			if entry.verified != nil {
				verified = *entry.verified
			}
			aud := any("cid")
			if entry.aud != nil {
				aud = entry.aud
			}
			shape := stubTokenShape{
				issuer:   p.server.URL,
				kid:      p.kid,
				nonce:    entry.nonce,
				email:    entry.email,
				name:     "Test User",
				verified: &verified,
				aud:      aud,
				azp:      entry.azp,
			}
			tok := shape.stubSignToken(p.priv)
			json.NewEncoder(w).Encode(map[string]string{"id_token": tok, "access_token": "at"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(func() { p.server.Close() })
	return p
}

// setPublish controls whether the JWKS document carries the signing key.
func (p *throttleIdP) setPublish(v bool) {
	p.mu.Lock()
	p.publish = v
	p.mu.Unlock()
}

func hexEncode(b []byte) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, x := range b {
		out[i*2] = hexd[x>>4]
		out[i*2+1] = hexd[x&15]
	}
	return string(out)
}
