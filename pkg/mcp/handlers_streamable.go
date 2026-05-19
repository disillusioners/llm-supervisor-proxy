package mcp

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
)

// handleStreamableHTTP handles Streamable HTTP transport for MCP proxy.
// It forwards client requests to the upstream MCP server and streams responses back.
func (s *Server) handleStreamableHTTP(w http.ResponseWriter, r *http.Request) {
	// Only accept POST method
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract serverID from URL path (e.g., /mcp/{serverID} or /mcp/{serverID}/)
	path := strings.TrimPrefix(r.URL.Path, "/mcp/")
	path = strings.TrimSuffix(path, "/")
	if path == "" || path == r.URL.Path {
		// /mcp/ without serverID
		http.Error(w, "Server ID required", http.StatusBadRequest)
		return
	}
	serverID := path

	// Look up server config from store
	ctx := r.Context()
	server, err := s.store.GetServer(ctx, serverID)
	if err != nil {
		log.Printf("[MCP] streamable: failed to get server %s: %v", serverID, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if server == nil {
		http.Error(w, "Server not found", http.StatusNotFound)
		return
	}
	if !server.Enabled {
		http.Error(w, "Server disabled", http.StatusServiceUnavailable)
		return
	}

	// Forward request to upstream using server's connection manager
	resp, err := s.connMgr.ForwardHTTPRequest(ctx, server, r)
	if err != nil {
		// C2: Close response body if non-nil before returning
		if resp != nil {
			resp.Body.Close()
		}
		if ctx.Err() != nil {
			// Client disconnected
			log.Printf("[MCP] streamable: client disconnected during request to %s", serverID)
			return
		}
		log.Printf("[MCP] streamable: upstream error for %s: %v", serverID, err)
		http.Error(w, "Upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers to client
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Add Mcp-Session-Id from request if not already set by upstream
	if resp.Header.Get("Mcp-Session-Id") == "" {
		if sessionID := r.Header.Get("Mcp-Session-Id"); sessionID != "" {
			w.Header().Set("Mcp-Session-Id", sessionID)
		}
	}

	// Set status code
	w.WriteHeader(resp.StatusCode)

	// Stream response body to client with flushing for chunked transfer
	flusher, canFlush := w.(http.Flusher)

	// Buffer for streaming
	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		select {
		case <-ctx.Done():
			// Client disconnected
			log.Printf("[MCP] streamable: client disconnected while streaming from %s", serverID)
			// Publish connection lost event
			s.bus.Publish(events.Event{
				Type:      "mcp_connection_lost",
				Timestamp: time.Now().Unix(),
				Data: map[string]interface{}{
					"server_id": serverID,
					"reason":    "client_disconnect",
				},
			})
			return
		default:
			n, err := resp.Body.Read(buf)
			if n > 0 {
				_, writeErr := w.Write(buf[:n])
				if writeErr != nil {
					log.Printf("[MCP] streamable: error writing to client for %s: %v", serverID, writeErr)
					return
				}
				if canFlush {
					flusher.Flush()
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[MCP] streamable: upstream disconnected for %s: %v", serverID, err)
					// Publish connection lost event
					s.bus.Publish(events.Event{
						Type:      "mcp_connection_lost",
						Timestamp: time.Now().Unix(),
						Data: map[string]interface{}{
							"server_id": serverID,
							"reason":    "upstream_disconnect",
						},
					})
				}
				return
			}
		}
	}
}
