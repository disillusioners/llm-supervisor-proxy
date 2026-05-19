package mcp

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/auth"
)

// proxyAuthMiddleware validates API key from Authorization: Bearer <key> header
func proxyAuthMiddleware(tokenStore auth.TokenStoreInterface) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeJSONError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				writeJSONError(w, http.StatusUnauthorized, "invalid authorization header format")
				return
			}

			token := parts[1]
			if token == "" {
				writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}

			_, err := tokenStore.ValidateToken(r.Context(), token)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
