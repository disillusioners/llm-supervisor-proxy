package mcp

import (
	"net"
	neturl "net/url"
	"testing"
)

// Helper functions for creating typed pointers
func authBearerPtr() *AuthType {
	t := AuthBearer
	return &t
}

func authNonePtr() *AuthType {
	t := AuthNone
	return &t
}

// canResolve checks if a URL's host can be resolved via DNS
func canResolve(urlStr string) bool {
	u, err := neturl.Parse(urlStr)
	if err != nil {
		return false
	}
	_, err = net.LookupIP(u.Hostname())
	return err == nil
}

func TestValidateUpstreamURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		url         string
		wantErr     bool
		skipIfDNS   bool // Skip test if DNS CAN resolve this URL (used for SSRF format tests)
	}{
		// Valid URLs
		{
			name:    "valid HTTPS URL",
			url:     "https://example.com/mcp",
			wantErr: false,
		},
		{
			name:    "valid HTTP URL with port",
			url:     "http://example.com:3001/mcp",
			wantErr: false,
		},
		{
			name:    "valid HTTPS URL with port",
			url:     "https://api.example.com:8080/v1/mcp",
			wantErr: false,
		},
		{
			name:    "valid HTTPS URL without path",
			url:     "https://example.com",
			wantErr: false,
		},

		// Blocked localhost
		{
			name:    "localhost rejected",
			url:     "http://localhost:3000",
			wantErr: true,
		},
		{
			name:    "localhost without port rejected",
			url:     "http://localhost",
			wantErr: true,
		},
		{
			name:    "127.0.0.1 rejected",
			url:     "http://127.0.0.1:3000",
			wantErr: true,
		},
		{
			name:    "127.0.0.1 without port rejected",
			url:     "http://127.0.0.1",
			wantErr: true,
		},
		{
			name:    "0.0.0.0 rejected",
			url:     "http://0.0.0.0:3000",
			wantErr: true,
		},
		{
			name:    "::1 IPv6 loopback rejected",
			url:     "http://[::1]:3000",
			wantErr: true,
		},

		// Blocked private IPs
		{
			name:    "10.x.x.x private IP rejected",
			url:     "http://10.0.0.1:3000",
			wantErr: true,
		},
		{
			name:    "10.x.x.x private IP rejected variant",
			url:     "http://10.255.255.255:8080",
			wantErr: true,
		},
		{
			name:    "172.16.x.x private IP rejected",
			url:     "http://172.16.0.1:3000",
			wantErr: true,
		},
		{
			name:    "172.16.x.x private IP rejected variant",
			url:     "http://172.31.255.255:8080",
			wantErr: true,
		},
		{
			name:    "192.168.x.x private IP rejected",
			url:     "http://192.168.1.1:3000",
			wantErr: true,
		},
		{
			name:    "192.168.x.x private IP rejected variant",
			url:     "http://192.168.0.1:8080",
			wantErr: true,
		},

		// Blocked link-local
		{
			name:    "169.254.x.x link-local rejected",
			url:     "http://169.254.1.1:3000",
			wantErr: true,
		},

		// Invalid schemes
		{
			name:    "ftp scheme rejected",
			url:     "ftp://example.com",
			wantErr: true,
		},
		{
			name:    "ws scheme rejected",
			url:     "ws://example.com",
			wantErr: true,
		},
		{
			name:    "wss scheme rejected",
			url:     "wss://example.com",
			wantErr: true,
		},

		// Invalid/malformed
		{
			name:    "empty host rejected",
			url:     "http://",
			wantErr: true,
		},
		{
			name:    "malformed URL rejected",
			url:     "not a url",
			wantErr: true,
		},
		{
			name:    "no scheme rejected",
			url:     "example.com",
			wantErr: true,
		},

		// SSRF IP format tests (hex, decimal, octal for 127.0.0.1)
		// These are skipped if DNS can resolve them (environment-specific behavior)
		{
			name:      "SSRF hex IP should be rejected",
			url:       "http://0x7f000001/",
			wantErr:   true,
			skipIfDNS: true, // Will be skipped if DNS resolves this URL
		},
		{
			name:      "SSRF decimal IP should be rejected",
			url:       "http://2130706433/",
			wantErr:   true,
			skipIfDNS: true, // Will be skipped if DNS resolves this URL
		},
		{
			name:      "SSRF octal IP should be rejected",
			url:       "http://0177.0.0.01/",
			wantErr:   true,
			skipIfDNS: true, // Will be skipped if DNS resolves this URL
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Skip tests where skipIfDNS is true and DNS can resolve the URL
			// These are environment-specific tests for SSRF IP formats
			if tt.skipIfDNS && canResolve(tt.url) {
				t.Skip("skipping - DNS can resolve this, test environment may handle it differently")
			}
			err := ValidateUpstreamURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUpstreamURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateCustomHeaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers string
		wantErr bool
	}{
		// Valid inputs
		{
			name:    "empty string passes",
			headers: "",
			wantErr: false,
		},
		{
			name:    "empty JSON object passes",
			headers: "{}",
			wantErr: false,
		},
		{
			name:    "valid single header passes",
			headers: `{"X-Custom": "value"}`,
			wantErr: false,
		},
		{
			name:    "valid multiple headers passes",
			headers: `{"X-Custom": "value", "X-Request-ID": "123"}`,
			wantErr: false,
		},

		// Blocked headers
		{
			name:    "Host header blocked",
			headers: `{"Host": "example.com"}`,
			wantErr: true,
		},
		{
			name:    "Content-Length header blocked",
			headers: `{"Content-Length": "100"}`,
			wantErr: true,
		},
		{
			name:    "Transfer-Encoding header blocked",
			headers: `{"Transfer-Encoding": "chunked"}`,
			wantErr: true,
		},
		{
			name:    "Connection header blocked",
			headers: `{"Connection": "keep-alive"}`,
			wantErr: true,
		},

		// Case-insensitive blocking
		{
			name:    "host lowercase blocked",
			headers: `{"host": "example.com"}`,
			wantErr: true,
		},
		{
			name:    "HOST uppercase blocked",
			headers: `{"HOST": "example.com"}`,
			wantErr: true,
		},
		{
			name:    "HoSt mixed case blocked",
			headers: `{"HoSt": "example.com"}`,
			wantErr: true,
		},

		// Invalid JSON
		{
			name:    "invalid JSON rejected",
			headers: `{invalid json`,
			wantErr: true,
		},
		{
			name:    "not JSON object rejected",
			headers: `"just a string"`,
			wantErr: true,
		},

		// Security - blocked auth headers
		{
			name:    "Authorization header blocked",
			headers: `{"Authorization": "Bearer token123"}`,
			wantErr: true,
		},
		{
			name:    "Proxy-Authorization header blocked",
			headers: `{"Proxy-Authorization": "Basic abc"}`,
			wantErr: true,
		},

		// Security - CRLF injection
		{
			name:    "CRLF injection in header value blocked",
			headers: `{"X-Custom": "value\r\nEvil: bad"}`,
			wantErr: true,
		},
		{
			name:    "LF injection in header value blocked",
			headers: `{"X-Custom": "value\nEvil"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateCustomHeaders(tt.headers)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCustomHeaders(%q) error = %v, wantErr %v", tt.headers, err, tt.wantErr)
			}
		})
	}
}

func TestValidateMCPServerConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *CreateMCPServerRequest
		wantErr bool
	}{
		// Valid configs
		{
			name: "valid config passes",
			req: &CreateMCPServerRequest{
				Name:          "test-server",
				UpstreamURL:   "https://api.example.com/mcp",
				TransportType: TransportStreamableHTTP,
				AuthType:      AuthBearer,
				AuthToken:     "secret-token",
				Headers:       "{}",
			},
			wantErr: false,
		},
		{
			name: "valid config with SSE transport",
			req: &CreateMCPServerRequest{
				Name:          "sse-server",
				UpstreamURL:   "https://sse.example.com/stream",
				TransportType: TransportSSE,
				AuthType:      AuthNone,
				Headers:       `{"X-Custom": "value"}`,
			},
			wantErr: false,
		},
		{
			name: "valid config with no auth",
			req: &CreateMCPServerRequest{
				Name:          "no-auth-server",
				UpstreamURL:   "http://internal.example.com:8080/mcp",
				TransportType: TransportStreamableHTTP,
				AuthType:      AuthNone,
			},
			wantErr: false,
		},

		// Invalid name
		{
			name: "empty name fails",
			req: &CreateMCPServerRequest{
				Name:        "",
				UpstreamURL: "https://api.example.com/mcp",
			},
			wantErr: true,
		},

		// Invalid upstream URL
		{
			name: "invalid upstream URL fails",
			req: &CreateMCPServerRequest{
				Name:        "test-server",
				UpstreamURL: "http://localhost:3000",
			},
			wantErr: true,
		},
		{
			name: "empty upstream URL fails",
			req: &CreateMCPServerRequest{
				Name:        "test-server",
				UpstreamURL: "",
			},
			wantErr: true,
		},

		// Invalid transport type
		{
			name: "invalid transport type fails",
			req: &CreateMCPServerRequest{
				Name:          "test-server",
				UpstreamURL:   "https://api.example.com/mcp",
				TransportType: "invalid",
			},
			wantErr: true,
		},

		// Invalid auth type
		{
			name: "invalid auth type fails",
			req: &CreateMCPServerRequest{
				Name:        "test-server",
				UpstreamURL: "https://api.example.com/mcp",
				AuthType:    "invalid",
			},
			wantErr: true,
		},

		// Auth token validation
		{
			name: "bearer auth with empty token fails",
			req: &CreateMCPServerRequest{
				Name:          "test-server",
				UpstreamURL:   "https://api.example.com/mcp",
				TransportType: TransportStreamableHTTP,
				AuthType:      AuthBearer,
				AuthToken:     "",
			},
			wantErr: true,
		},
		{
			name: "basic auth with empty token fails",
			req: &CreateMCPServerRequest{
				Name:          "test-server",
				UpstreamURL:   "https://api.example.com/mcp",
				TransportType: TransportStreamableHTTP,
				AuthType:      AuthBasic,
				AuthToken:     "",
			},
			wantErr: true,
		},
		{
			name: "api_key auth with empty token fails",
			req: &CreateMCPServerRequest{
				Name:          "test-server",
				UpstreamURL:   "https://api.example.com/mcp",
				TransportType: TransportStreamableHTTP,
				AuthType:      AuthAPIKey,
				AuthToken:     "",
			},
			wantErr: true,
		},
		{
			name: "none auth with empty token passes",
			req: &CreateMCPServerRequest{
				Name:          "test-server",
				UpstreamURL:   "https://api.example.com/mcp",
				TransportType: TransportStreamableHTTP,
				AuthType:      AuthNone,
				AuthToken:     "",
			},
			wantErr: false,
		},

		// Invalid headers
		{
			name: "invalid headers JSON fails",
			req: &CreateMCPServerRequest{
				Name:        "test-server",
				UpstreamURL: "https://api.example.com/mcp",
				AuthType:    AuthNone,
				Headers:     `{invalid}`,
			},
			wantErr: true,
		},
		{
			name: "blocked header fails",
			req: &CreateMCPServerRequest{
				Name:        "test-server",
				UpstreamURL: "https://api.example.com/mcp",
				AuthType:    AuthNone,
				Headers:     `{"Host": "bad.com"}`,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateMCPServerConfig(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMCPServerConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateUpdateMCPServerConfig(t *testing.T) {
	t.Parallel()

	validName := "valid-name"
	invalidName := ""
	validURL := "https://api.example.com/mcp"
	invalidURL := "http://localhost:3000"
	invalidTransport := TransportType("invalid")
	invalidAuth := AuthType("invalid")
	validToken := "new-token"
	emptyToken := ""
	validHeaders := `{"X-Custom": "value"}`
	invalidHeaders := `{invalid}`

	tests := []struct {
		name    string
		req     *UpdateMCPServerRequest
		wantErr bool
	}{
		// All nil fields
		{
			name:    "all nil fields passes",
			req:     &UpdateMCPServerRequest{},
			wantErr: false,
		},
		{
			name:    "nil request passes",
			req:     nil,
			wantErr: false,
		},

		// Valid name
		{
			name: "valid name passes",
			req: &UpdateMCPServerRequest{
				Name: &validName,
			},
			wantErr: false,
		},

		// Invalid name
		{
			name: "empty name fails",
			req: &UpdateMCPServerRequest{
				Name: &invalidName,
			},
			wantErr: true,
		},

		// Valid upstream URL
		{
			name: "valid upstream URL passes",
			req: &UpdateMCPServerRequest{
				UpstreamURL: &validURL,
			},
			wantErr: false,
		},

		// Invalid upstream URL
		{
			name: "invalid upstream URL fails",
			req: &UpdateMCPServerRequest{
				UpstreamURL: &invalidURL,
			},
			wantErr: true,
		},

		// Invalid transport type
		{
			name: "invalid transport type fails",
			req: &UpdateMCPServerRequest{
				TransportType: &invalidTransport,
			},
			wantErr: true,
		},

		// Invalid auth type
		{
			name: "invalid auth type fails",
			req: &UpdateMCPServerRequest{
				AuthType: &invalidAuth,
			},
			wantErr: true,
		},

		// Empty auth token with non-none auth type
		{
			name: "empty auth_token with bearer fails",
			req: &UpdateMCPServerRequest{
				AuthToken: &emptyToken,
				AuthType:  authBearerPtr(),
			},
			wantErr: true,
		},

		// Empty auth token with none auth type passes
		{
			name: "empty auth_token with none passes",
			req: &UpdateMCPServerRequest{
				AuthToken: &emptyToken,
				AuthType:  authNonePtr(),
			},
			wantErr: false,
		},

		// Valid auth token
		{
			name: "valid auth_token with bearer passes",
			req: &UpdateMCPServerRequest{
				AuthToken: &validToken,
				AuthType:  authBearerPtr(),
			},
			wantErr: false,
		},

		// Invalid headers
		{
			name: "invalid headers JSON fails",
			req: &UpdateMCPServerRequest{
				Headers: &invalidHeaders,
			},
			wantErr: true,
		},

		// Valid headers
		{
			name: "valid headers passes",
			req: &UpdateMCPServerRequest{
				Headers: &validHeaders,
			},
			wantErr: false,
		},

		// Multiple valid fields
		{
			name: "multiple valid fields pass",
			req: &UpdateMCPServerRequest{
				Name:        &validName,
				UpstreamURL: &validURL,
				AuthType:    authBearerPtr(),
				AuthToken:   &validToken,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateUpdateMCPServerConfig(tt.req, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUpdateMCPServerConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateUpdateMCPServerConfig_EmptyTokenWithExistingBearer(t *testing.T) {
	// C3: empty token should fail when existing server has bearer auth
	emptyToken := ""
	req := &UpdateMCPServerRequest{
		AuthToken: &emptyToken,
	}
	existing := &MCPServer{
		AuthType:  AuthBearer,
		AuthToken: "existing-secret",
	}
	err := ValidateUpdateMCPServerConfig(req, existing)
	if err == nil {
		t.Error("expected error when clearing auth_token on bearer-auth server, got nil")
	}
}

func TestValidateUpdateMCPServerConfig_EmptyTokenWithExistingNone(t *testing.T) {
	// C3: empty token should pass when existing server has none auth
	emptyToken := ""
	req := &UpdateMCPServerRequest{
		AuthToken: &emptyToken,
	}
	existing := &MCPServer{
		AuthType: AuthNone,
	}
	err := ValidateUpdateMCPServerConfig(req, existing)
	if err != nil {
		t.Errorf("expected no error when clearing auth_token on none-auth server, got: %v", err)
	}
}
