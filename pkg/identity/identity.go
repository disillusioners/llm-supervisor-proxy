package identity

import "net/http"

// Identity is the minimal principal carried by the internal OAuth2 layer.
// preferred_username is deliberately absent (AR-33/AR-62).
type Identity struct {
	Sub   string `json:"sub"`
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
}

// Provider is the OAuth2 seam. A nil provider means "internal OAuth2 is
// dormant" — the /fe/api/auth/me endpoint then answers {"enabled":false}.
type Provider interface {
	Enabled() bool
	Current(r *http.Request) (*Identity, error)
	// StateStoreCount returns the LIVE count of the capped /oauth2/start
	// state store. A nil provider (dormant) reports 0.
	StateStoreCount() int
}
