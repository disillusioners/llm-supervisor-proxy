-- Migration 012: Fix race_max_buffer_bytes default value (was incorrectly set to 1)
UPDATE configs SET race_max_buffer_bytes = 5242880 WHERE race_max_buffer_bytes = 1;
