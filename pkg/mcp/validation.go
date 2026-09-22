package mcp

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var blockedHosts = []string{"localhost", "127.0.0.1", "0.0.0.0", "::1"}

// ValidateUpstreamURL validates that rawURL is an http/https URL whose host is
// safe to connect to. It rejects:
//
//   - non-http(s) schemes
//   - empty hosts and known string-form loopback names ("localhost", "127.0.0.1",
//     "0.0.0.0", "::1")
//   - canonical IPv4/IPv6 hosts that parse as loopback, private, or link-local
//     (covers 10/8, 172.16/12, 192.168/16, 169.254/16, ::1, fc00::/7, etc.)
//   - hostnames whose DNS resolution maps to a loopback/private/link-local IP
//   - hosts whose string form is a non-canonical IPv4 encoding — hex
//     ("0x7f000001"), decimal ("2130706433"), or octal ("0177.0.0.01"). These
//     bypass net.ParseIP's strict IPv4 format check, and most DNS resolvers do
//     not recognize them either, so DNS lookup alone cannot catch them.
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
	// Defense-in-depth: strip a single trailing '.' so "localhost." or
	// "127.0.0.1." don't slip past the equality checks below.
	host = strings.TrimSuffix(host, ".")
	for _, blocked := range blockedHosts {
		if strings.EqualFold(host, blocked) {
			return fmt.Errorf("localhost URLs are not allowed")
		}
	}
	// Reject non-canonical numeric IP encodings (hex/decimal/octal/short forms).
	// Most DNS resolvers do NOT recognize these, so the DNS-lookup defense
	// below cannot catch them on its own. Real hostnames contain letters
	// outside the [0-9.xX] set and pass through unchanged.
	if isNonCanonicalNumericHost(host) {
		return fmt.Errorf("invalid IP encoding in host: %s", host)
	}
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("private/local network URLs are not allowed")
		}
	}
	// Resolve hostname via DNS to catch hostnames pointing at private/loopback IPs.
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

// isNonCanonicalNumericHost reports whether host is a non-canonical IPv4
// encoding that net.ParseIP's strict IPv4 parser would not accept. Such hosts
// are common SSRF bypass vectors:
//   - hex with 0x/0X prefix:    "0x7f000001", "0X7F000001"
//   - decimal as a single uint32: "2130706433"
//   - dotted with octal-leading segments: "0177.0.0.01"
//   - dotted with per-segment hex:  "0x7f.0.0.1"
//
// Real hostnames contain letters outside the hex alphabet and return false.
// Canonical IPv4 like "127.0.0.1" returns false (handled by net.ParseIP
// below); canonical IPv6 like "::1" returns false (handled by the string-based
// blockedHosts check above).
func isNonCanonicalNumericHost(host string) bool {
	if host == "" {
		return false
	}
	// 1. Hex with 0x/0X prefix. If the suffix is entirely hex digits, the
	//    whole host is a hex-encoded IPv4 (e.g. "0x7f000001"). If the
	//    suffix contains a non-hex character (notably '.'), do NOT return
	//    false here — fall through to branch 3 so per-segment forms like
	//    "0x7f.0.0.1" are caught by the dotted-form check rather than
	//    being silently accepted as a real hostname.
	if len(host) > 2 && (host[0:2] == "0x" || host[0:2] == "0X") {
		allHex := true
		for _, r := range host[2:] {
			if !isHexDigit(r) {
				allHex = false
				break
			}
		}
		if allHex {
			return true
		}
		// fall through to branch 3 (dotted form)
	}
	// 2. Pure decimal digits: single uint32-as-IP encoding.
	if isAllDigits(host) {
		return true
	}
	// 3. Dotted form. Two sub-checks:
	//    (a) Shorthand dotted forms like "127.1", "10.1", "172.31.1" have
	//        fewer than 4 segments but every segment looks canonical, so
	//        the per-segment checks below would miss them. If the segment
	//        count is not 4 and every segment is numeric, treat as a
	//        non-canonical numeric encoding.
	//    (b) For 4-segment forms, check each segment for octal-leading
	//        (more than one digit and starts with '0') or per-segment
	//        0x/0X hex.
	if strings.Contains(host, ".") {
		parts := strings.Split(host, ".")
		if len(parts) != 4 {
			allNumeric := true
			for _, p := range parts {
				if p == "" || !isAllDigits(p) {
					allNumeric = false
					break
				}
			}
			if allNumeric {
				return true
			}
		}
		for _, p := range parts {
			if p == "" {
				return false
			}
			if len(p) > 2 && (p[0:2] == "0x" || p[0:2] == "0X") {
				return true
			}
			if len(p) > 1 && p[0] == '0' {
				return true
			}
		}
	}
	return false
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var blockedHeaders = map[string]bool{
	"host":                 true,
	"content-length":       true,
	"transfer-encoding":    true,
	"connection":           true,
	"authorization":        true,
	"proxy-authorization":  true,
}

// MaxServerIDLength is the maximum allowed length for a server ID
const MaxServerIDLength = 128

// serverIDPattern allows lowercase alphanumeric, hyphens, and underscores
// Must start with alphanumeric, cannot end with hyphen or underscore
var serverIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*[a-z0-9]$|^[a-z0-9]$`)

// ValidateServerID validates a server ID string
func ValidateServerID(id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if len(id) > MaxServerIDLength {
		return fmt.Errorf("id must be at most %d characters", MaxServerIDLength)
	}
	if !serverIDPattern.MatchString(id) {
		return fmt.Errorf("id must start with a lowercase letter or number and contain only lowercase letters, numbers, hyphens, and underscores")
	}
	return nil
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
	if err := ValidateServerID(req.ID); err != nil {
		return fmt.Errorf("invalid id: %w", err)
	}
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
