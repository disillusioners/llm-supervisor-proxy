package mcp

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var blockedHosts = []string{"localhost", "127.0.0.1", "0.0.0.0", "::1"}

func ValidateUpstreamURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https schemes allowed")
	}
	if u.Host == "" {
		return fmt.Errorf("host is required")
	}
	host := u.Hostname()
	for _, blocked := range blockedHosts {
		if host == blocked {
			return fmt.Errorf("localhost URLs are not allowed")
		}
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("private/local network URLs are not allowed")
		}
	}
	// Resolve hostname via DNS to catch hex/decimal/octal IPs that bypass net.ParseIP
	ips, err := net.LookupIP(host)
	if err == nil {
		for _, ip := range ips {
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				return fmt.Errorf("private/local network URLs are not allowed")
			}
		}
	}
	return nil
}

var blockedHeaders = map[string]bool{
	"host":                 true,
	"content-length":       true,
	"transfer-encoding":    true,
	"connection":           true,
	"authorization":        true,
	"proxy-authorization":  true,
}

func ValidateCustomHeaders(headersJSON string) error {
	if headersJSON == "" || headersJSON == "{}" {
		return nil
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(headersJSON), &headers); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	for key, value := range headers {
		if blockedHeaders[strings.ToLower(key)] {
			return fmt.Errorf("header '%s' is not allowed", key)
		}
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("header '%s' value contains invalid characters", key)
		}
	}
	return nil
}

// ValidateMCPServerConfig validates a CreateMCPServerRequest
func ValidateMCPServerConfig(req *CreateMCPServerRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if err := ValidateUpstreamURL(req.UpstreamURL); err != nil {
		return fmt.Errorf("invalid upstream_url: %w", err)
	}
	if !req.TransportType.Valid() {
		return fmt.Errorf("invalid transport type: %s", req.TransportType)
	}
	if !req.AuthType.Valid() {
		return fmt.Errorf("invalid auth_type: %s", req.AuthType)
	}
	if req.AuthType != AuthNone && req.AuthToken == "" {
		return fmt.Errorf("auth_token is required when auth_type is not 'none'")
	}
	if err := ValidateCustomHeaders(req.Headers); err != nil {
		return fmt.Errorf("invalid headers: %w", err)
	}
	return nil
}

// ValidateUpdateMCPServerConfig validates an UpdateMCPServerRequest
func ValidateUpdateMCPServerConfig(req *UpdateMCPServerRequest, existing *MCPServer) error {
	if req == nil {
		return nil
	}

	// C1: Check for masked token (user sent back a masked value from UI)
	if req.AuthToken != nil && strings.Contains(*req.AuthToken, "***") {
		return fmt.Errorf("auth_token appears to be a masked value, please provide the actual token")
	}

	// C3: Check if empty token would clear a non-none auth type
	if req.AuthToken != nil && *req.AuthToken == "" {
		effectiveAuthType := AuthNone
		if req.AuthType != nil {
			effectiveAuthType = *req.AuthType
		} else if existing != nil {
			effectiveAuthType = existing.AuthType
		}
		if effectiveAuthType != AuthNone {
			return fmt.Errorf("auth_token cannot be empty when auth_type is not 'none'")
		}
	}

	if req.Name != nil && *req.Name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if req.UpstreamURL != nil {
		if err := ValidateUpstreamURL(*req.UpstreamURL); err != nil {
			return fmt.Errorf("invalid upstream_url: %w", err)
		}
	}
	if req.TransportType != nil && !req.TransportType.Valid() {
		return fmt.Errorf("invalid transport type: %s", *req.TransportType)
	}
	if req.AuthType != nil && !req.AuthType.Valid() {
		return fmt.Errorf("invalid auth_type: %s", *req.AuthType)
	}
	if req.Headers != nil {
		if err := ValidateCustomHeaders(*req.Headers); err != nil {
			return fmt.Errorf("invalid headers: %w", err)
		}
	}
	return nil
}
