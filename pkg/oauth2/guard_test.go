package oauth2

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGuard_DormantIsNoOp(t *testing.T) {
	g, err := NewGuard(nil)
	if err != nil {
		t.Fatal(err)
	}
	if g != nil {
		t.Fatal("expected nil guard")
	}
}

func TestGuard_EnabledWrapsMux(t *testing.T) {
	cfg := &Config{Enabled: true, IssuerURL: "https://issuer.example.com", ClientID: "cid", ExternalURL: "https://app.example.com", RedirectURL: "https://app.example.com/oauth2/callback", CookieName: "sess", TxnCookieName: "txn", CookieSecure: true, CookieSameSite: http.SameSiteLaxMode, SessionTTL: 3600, EmailDomains: []string{"*"}, AllowAnyDomain: true, AllowedAlgs: map[string]struct{}{"RS256": {}}, StateStoreMax: 4}
	root := make([]byte, 32)
	for i := range root {
		root[i] = byte(i + 1)
	}
	sk, tk, _ := deriveSubkeys(root)
	_ = sk
	_ = tk
	cfg.rootKey = root
	g, err := NewGuard(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g == nil {
		t.Fatal("guard nil")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	g.RegisterRoutes(mux)
	h := g.Wrap(mux)
	// gate installed: unauthenticated /fe/api/config => 401
	req := httptest.NewRequest(http.MethodGet, "/fe/api/config", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("gate not installed: %d", w.Code)
	}
	// /healthz passes
	req2 := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("healthz: %d", w2.Code)
	}
}
