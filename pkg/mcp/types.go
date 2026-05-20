package mcp

type TransportType string

const (
	TransportSSE            TransportType = "sse"
	TransportStreamableHTTP TransportType = "streamable_http"
)

func (t TransportType) Valid() bool {
	return t == TransportSSE || t == TransportStreamableHTTP
}

type AuthType string

const (
	AuthNone   AuthType = "none"
	AuthBearer AuthType = "bearer"
	AuthBasic  AuthType = "basic"
	AuthAPIKey AuthType = "api_key"
)

func (a AuthType) Valid() bool {
	return a == AuthNone || a == AuthBearer || a == AuthBasic || a == AuthAPIKey
}

type MCPServer struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	UpstreamURL   string        `json:"upstream_url"`
	TransportType TransportType `json:"transport_type"`
	AuthType      AuthType      `json:"auth_type"`
	AuthToken     string        `json:"auth_token,omitempty"` // masked in API responses, encrypted in DB
	Headers       string        `json:"headers"`              // JSON map
	Enabled       bool          `json:"enabled"`
	CreatedAt     string        `json:"created_at"`
	UpdatedAt     string        `json:"updated_at"`
}

// For create requests
type CreateMCPServerRequest struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Description   string        `json:"description"`
	UpstreamURL   string        `json:"upstream_url"`
	TransportType TransportType `json:"transport_type"`
	AuthType      AuthType      `json:"auth_type"`
	AuthToken     string        `json:"auth_token"`
	Headers       string        `json:"headers"`
	Enabled       *bool         `json:"enabled"`
}

// For update requests (all optional)
type UpdateMCPServerRequest struct {
	Name          *string        `json:"name"`
	Description   *string        `json:"description"`
	UpstreamURL   *string        `json:"upstream_url"`
	TransportType *TransportType `json:"transport_type"`
	AuthType      *AuthType      `json:"auth_type"`
	AuthToken     *string        `json:"auth_token"`
	Headers       *string        `json:"headers"`
	Enabled       *bool          `json:"enabled"`
}
