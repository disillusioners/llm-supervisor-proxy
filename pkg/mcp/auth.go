package mcp

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// contextKey is a private type to avoid collisions in context values.
type contextKey string

const (
	// tokenContextKey is the context key for storing validated token info.
	tokenContextKey contextKey = "mcp_auth_token"
)

// proxyAuthMiddleware validates API key from Authorization: Bearer <key> header.
// Returns 401 with JSON error for missing/invalid/expired tokens.
// Injects validated *auth.AuthToken into request context for downstream handlers.
func (s *Server) proxyAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing authorization header")
			log.Printf("[MCP] auth failed: missing authorization header from %s", r.RemoteAddr)
			return
		}

		// Validate Bearer format
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeJSONError(w, http.StatusUnauthorized, "invalid authorization header format")
			log.Printf("[MCP] auth failed: invalid authorization header format from %s", r.RemoteAddr)
			return
		}

		token := parts[1]
		if token == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
			log.Printf("[MCP] auth failed: missing bearer token from %s", r.RemoteAddr)
			return
		}

		// Validate token against store
		authToken, err := s.tokenStore.ValidateToken(r.Context(), token)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "invalid or expired token")
			log.Printf("[MCP] auth failed: %v from %s", err, r.RemoteAddr)
			return
		}

		// Inject validated token into context
		ctx := context.WithValue(r.Context(), tokenContextKey, authToken)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
