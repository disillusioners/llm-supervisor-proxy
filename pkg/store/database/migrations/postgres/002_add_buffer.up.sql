-- Migration 002: Add max_stream_buffer_size column
ALTER TABLE configs ADD COLUMN IF NOT EXISTS max_stream_buffer_size BIGINT NOT NULL DEFAULT 10485760;
