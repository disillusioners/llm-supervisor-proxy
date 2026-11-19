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
					w.WriteHeader(400)
					json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
					return
				}
			}
			nonce := "test-nonce"
			email := "user@example.com"
			verified := true
			if ok {
				nonce = entry.nonce
				email = entry.email
				if entry.verified != nil {
					verified = *entry.verified
				}
			}
			header := map[string]string{"alg": "RS256", "kid": testStubKid, "typ": "JWT"}
			hb, _ := json.Marshal(header)
			claims := map[string]any{
				"iss":   srv.URL,
				"sub":   "user1",
				"aud":   "cid",
				"exp":   time.Now().Add(time.Hour).Unix(),
				"iat":   time.Now().Unix(),
				"nonce": nonce,
				"email": email,
				"name":  "Test User",
			}
			if ok && entry.verified != nil {
				claims["email_verified"] = verified
			} else if !ok {
				claims["email_verified"] = verified
			}
			pb, _ := json.Marshal(claims)
			he := base64.RawURLEncoding.EncodeToString(hb)
			pe := base64.RawURLEncoding.EncodeToString(pb)
			signing := he + "." + pe
			hh := sha256.Sum256([]byte(signing))
			sig, _ := rsa.SignPKCS1v15(rand.Reader, testStubPriv, crypto.SHA256, hh[:])
			tok := signing + "." + base64.RawURLEncoding.EncodeToString(sig)
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
	challenge := ""
	if verifier != "" {
		h := sha256.Sum256([]byte(verifier))
		challenge = base64.RawURLEncoding.EncodeToString(h[:])
	}
	v := verified
	registerStubCode(code, &stubCodeEntry{nonce: nonce, challenge: challenge, email: email, verified: &v})
}

func sha256Challenge(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
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
