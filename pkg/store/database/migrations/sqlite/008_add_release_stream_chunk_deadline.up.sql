-- Migration 008: Add release_stream_chunk_deadline to models
-- This field configures how long to buffer stream chunks before flushing to downstream.
-- Purpose: Support clients with idle chunk detection that drop connections if no data is received within a timeout.

-- How it works:
-- 1. Stream chunks are buffered (not sent to client) by default
-- 2. After release_stream_chunk_deadline duration, the buffer is flushed to client
-- 3. Request is marked as non-retryable (no upstream retry if errors occur after this point)
-- 4. Streaming continues without buffering

-- Values:
-- 0 (default): Use system default (1m50s / 110000 milliseconds)
-- >0: Duration in milliseconds (e.g., 110000 for 1m50s)
-- Special: Set to 1 to flush immediately without deadline (not recommended)

ALTER TABLE models ADD COLUMN release_stream_chunk_deadline INTEGER NOT NULL DEFAULT 0;

-- Add index for efficient queries on deadline
CREATE INDEX IF NOT EXISTS idx_models_release_deadline ON models(release_stream_chunk_deadline);
