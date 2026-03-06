-- Migration 002: Add max_stream_buffer_size column
ALTER TABLE configs ADD COLUMN max_stream_buffer_size INTEGER NOT NULL DEFAULT 10485760;
