package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
)

// sseEvent represents a parsed SSE event with accumulated data
type sseEvent struct {
	eventType string
	dataLines []string
	rawLines  []string
}

// rewriteEndpointData rewrites URLs in endpoint event data to proxy paths
func rewriteEndpointData(dataLines []string, serverID string) []string {
	proxyPath := fmt.Sprintf("/mcp/%s/messages", serverID)
	result := make([]string, 0, len(dataLines))

	for _, line := range dataLines {
		rewritten := rewriteSingleEndpointLine(line, proxyPath)
		result = append(result, rewritten)
	}

	return result
}

// rewriteSingleEndpointLine rewrites a single endpoint data line
func rewriteSingleEndpointLine(line, proxyPath string) string {
	// Look for URL patterns in the line
	// Common patterns: http://host:port/path, https://host:port/path, or just /path

	trimmed := strings.TrimSpace(line)

	// Check if line contains a full URL (http:// or https://)
	if strings.Contains(trimmed, "http://") || strings.Contains(trimmed, "https://") {
		return rewriteFullURL(trimmed, proxyPath)
	}

	// Check for absolute path starting with /
	if strings.HasPrefix(trimmed, "/") {
		return rewriteAbsolutePath(trimmed, proxyPath)
	}

	// Otherwise return as-is
	return line
}

// rewriteFullURL handles URLs with protocol (http:// or https://)
func rewriteFullURL(line, proxyPath string) string {
	// Find the URL in the line
	// The data line format is typically: data: <url> or just <url>
	trimmed := strings.TrimSpace(line)

	// Remove "data: " prefix if present
	dataPrefix := ""
	if strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		dataPrefix = "data:"
		trimmed = strings.TrimPrefix(trimmed, "data:")
		trimmed = strings.TrimSpace(trimmed)
	}

	// Find the URL - look for http:// or https://
	urlStart := strings.Index(trimmed, "http://")
	if urlStart == -1 {
		urlStart = strings.Index(trimmed, "https://")
	}

	if urlStart == -1 {
		// No URL found, return original
		if dataPrefix != "" {
			return dataPrefix + " " + trimmed
		}
		return line
	}

	// Find where the URL ends (at whitespace or end of line)
	urlPart := trimmed[urlStart:]
	spaceIdx := strings.IndexAny(urlPart, " \t\n")
	if spaceIdx != -1 {
		urlPart = urlPart[:spaceIdx]
	}

	// Extract query string from original URL
	queryString := ""
	if qIdx := strings.Index(urlPart, "?"); qIdx != -1 {
		queryString = urlPart[qIdx:] // includes the ?
	}

	// Build new URL - use proxy path + original query string only
	// The proxy endpoint is always /mcp/{id}/messages, original path is irrelevant
	var newURL string
	if queryString != "" {
		newURL = proxyPath + queryString
	} else {
		newURL = proxyPath
	}

	// Reconstruct the line with proxy path + query string only
	beforeURL := trimmed[:urlStart]
	if dataPrefix != "" {
		return fmt.Sprintf("%s %s%s", dataPrefix, beforeURL, newURL)
	}
	return beforeURL + newURL
}

// rewriteAbsolutePath handles absolute paths (starting with /)
func rewriteAbsolutePath(line, proxyPath string) string {
	trimmed := strings.TrimSpace(line)

	// Remove "data: " prefix if present
	dataPrefix := ""
	if strings.HasPrefix(strings.ToLower(trimmed), "data:") {
		dataPrefix = "data:"
		trimmed = strings.TrimPrefix(trimmed, "data:")
		trimmed = strings.TrimSpace(trimmed)
	}

	// Find where path ends (at query string or end)
	pathEnd := len(trimmed)
	queryIdx := strings.Index(trimmed, "?")
	if queryIdx != -1 {
		pathEnd = queryIdx
	}

	pathPart := trimmed[:pathEnd]
	queryPart := ""
	if queryIdx != -1 {
		queryPart = trimmed[queryIdx:]
	}

	// Replace with proxy path (always use proxy path for all absolute paths)
	pathPart = proxyPath

	if dataPrefix != "" {
		return fmt.Sprintf("%s %s%s", dataPrefix, pathPart, queryPart)
	}
	return pathPart + queryPart
}

// handleSSEConnection handles SSE endpoint connections to upstream MCP servers
func (s *Server) handleSSEConnection(w http.ResponseWriter, r *http.Request) {
	// Extract serverID from URL path
	serverID := extractServerID(r.URL.Path)
	if serverID == "" {
		log.Printf("[MCP] SSE: missing server ID in path %s", r.URL.Path)
		http.Error(w, "missing server ID", http.StatusBadRequest)
		return
	}

	// Look up server config
	server, err := s.store.GetServer(r.Context(), serverID)
	if err != nil {
		log.Printf("[MCP] SSE: failed to get server %s: %v", serverID, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if server == nil {
		log.Printf("[MCP] SSE: server not found: %s", serverID)
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}
	if !server.Enabled {
		log.Printf("[MCP] SSE: server is disabled: %s", serverID)
		http.Error(w, `{"error":"server is disabled"}`, http.StatusServiceUnavailable)
		return
	}

	// Set SSE response headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Get flusher for streaming responses
	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("[MCP] SSE: ResponseWriter does not support flushing")
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Write 200 status and flush headers
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Create cancellable context for this connection
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Register connection in registry
	s.connMgr.GetRegistry().Register(serverID, cancel)
	defer s.connMgr.GetRegistry().Unregister(serverID, cancel)

	// Connect to upstream SSE endpoint
	resp, err := s.connMgr.ConnectUpstream(ctx, server)
	if err != nil {
		log.Printf("[MCP] SSE: failed to connect to upstream for server %s: %v", serverID, err)
		// Close response body if non-nil before returning
		if resp != nil {
			resp.Body.Close()
		}
		// Don't write error to response since headers already sent
		return
	}
	defer resp.Body.Close()

	// Check upstream response status
	if resp.StatusCode != http.StatusOK {
		log.Printf("[MCP] SSE: upstream returned status %d for server %s", resp.StatusCode, serverID)
		return
	}

	// Stream SSE events from upstream to client
	err = s.streamSSEEvents(ctx, resp.Body, w, flusher, serverID)
	if err != nil {
		// Context cancelled is expected on client disconnect
		if ctx.Err() == context.Canceled {
			log.Printf("[MCP] SSE: client disconnected for server %s", serverID)
		} else {
			log.Printf("[MCP] SSE: stream error for server %s: %v", serverID, err)
		}
	}

	// Publish connection lost event to events bus
	s.bus.Publish(events.Event{
		Type:      "mcp_connection_lost",
		Timestamp: time.Now().Unix(),
		Data: map[string]string{
			"server_id": serverID,
		},
	})

	log.Printf("[MCP] SSE: connection closed for server %s", serverID)
}

// streamSSEEvents reads SSE events from upstream and forwards them to the client
func (s *Server) streamSSEEvents(ctx context.Context, body io.Reader, w http.ResponseWriter, flusher http.Flusher, serverID string) error {
	scanner := bufio.NewScanner(body)
	buf := make([]byte, 0, 10*1024*1024) // 10MB buffer
	scanner.Buffer(buf, 10*1024*1024)

	var currentEvent sseEvent

	// Process SSE stream
	for scanner.Scan() {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()

		// Empty line = end of event, flush accumulated event
		if line == "" {
			if len(currentEvent.dataLines) > 0 || len(currentEvent.rawLines) > 0 {
				s.flushSSEEvent(w, &currentEvent, flusher, serverID)
				currentEvent = sseEvent{}
			}
			continue
		}

		// Parse SSE line
		if strings.HasPrefix(line, "event:") {
			currentEvent.eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			currentEvent.rawLines = append(currentEvent.rawLines, line)
		} else if strings.HasPrefix(line, "data:") {
			dataValue := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			currentEvent.dataLines = append(currentEvent.dataLines, dataValue)
		} else {
			// Other SSE directives (id:, retry:, comments)
			currentEvent.rawLines = append(currentEvent.rawLines, line)
		}
	}

	// Flush any remaining event
	if len(currentEvent.dataLines) > 0 || len(currentEvent.rawLines) > 0 {
		s.flushSSEEvent(w, &currentEvent, flusher, serverID)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

// flushSSEEvent writes a single SSE event to the response
func (s *Server) flushSSEEvent(w http.ResponseWriter, event *sseEvent, flusher http.Flusher, serverID string) {
	// Handle endpoint events - rewrite URLs
	dataLines := event.dataLines
	if event.eventType == "endpoint" {
		dataLines = rewriteEndpointData(event.dataLines, serverID)
	}

	// Write raw lines first (event:, id:, retry:, comments)
	for _, rawLine := range event.rawLines {
		fmt.Fprintf(w, "%s\n", rawLine)
	}

	// Write data lines
	for _, dataLine := range dataLines {
		fmt.Fprintf(w, "data: %s\n", dataLine)
	}

	// Write empty line to terminate event
	fmt.Fprintf(w, "\n")

	// Flush to client
	flusher.Flush()
}

// handleSSEMessage handles POST requests to the messages endpoint
func (s *Server) handleSSEMessage(w http.ResponseWriter, r *http.Request) {
	// Only allow POST method
	if r.Method != http.MethodPost {
		log.Printf("[MCP] Messages: invalid method %s", r.Method)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract serverID from URL path
	serverID := extractServerID(r.URL.Path)
	if serverID == "" {
		log.Printf("[MCP] Messages: missing server ID in path %s", r.URL.Path)
		http.Error(w, "missing server ID", http.StatusBadRequest)
		return
	}

	// Look up server config
	server, err := s.store.GetServer(r.Context(), serverID)
	if err != nil {
		log.Printf("[MCP] Messages: failed to get server %s: %v", serverID, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if server == nil {
		log.Printf("[MCP] Messages: server not found: %s", serverID)
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}

	// Forward request to upstream
	resp, err := s.connMgr.ForwardHTTPRequest(r.Context(), server, r)
	if err != nil {
		log.Printf("[MCP] Messages: failed to forward request for server %s: %v", serverID, err)
		// C2: Close response body if non-nil before returning
		if resp != nil {
			resp.Body.Close()
		}
		http.Error(w, "failed to connect to upstream", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Pass through important headers
	// W3: Prefer upstream's Mcp-Session-Id, fall back to client's
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	} else if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))

	// Stream response back to client
	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		// Client may have disconnected, log but don't fail
		if err != io.EOF {
			log.Printf("[MCP] Messages: error streaming response for server %s: %v", serverID, err)
		}
	}
}

// extractServerID extracts the server ID from the URL path
// Handles paths like /mcp/{id}/sse, /mcp/{id}/messages
func extractServerID(path string) string {
	// Normalize path - remove leading/trailing slashes
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")

	// Expected format: mcp/{serverID}/{endpoint}
	// parts should be: ["mcp", "{serverID}", "{endpoint}"]
	if len(parts) < 3 {
		return ""
	}

	if parts[0] != "mcp" {
		return ""
	}

	return parts[1]
}
