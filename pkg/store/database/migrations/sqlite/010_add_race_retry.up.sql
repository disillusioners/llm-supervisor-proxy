-- Migration 010: Add race retry fields to configs table
ALTER TABLE configs ADD COLUMN race_retry_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE configs ADD COLUMN race_parallel_on_idle INTEGER NOT NULL DEFAULT 1;
ALTER TABLE configs ADD COLUMN race_max_parallel INTEGER NOT NULL DEFAULT 3;
ALTER TABLE configs ADD COLUMN race_max_buffer_bytes INTEGER NOT NULL DEFAULT 5242880; -- 5MB default
