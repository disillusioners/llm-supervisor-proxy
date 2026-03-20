-- Migration 013: Add sse_heartbeat_enabled column to configs table
-- This column enables SSE heartbeat for streaming responses
ALTER TABLE configs ADD COLUMN sse_heartbeat_enabled BOOLEAN NOT NULL DEFAULT TRUE;
