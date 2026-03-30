-- Migration 018: Add peak hours configuration to models
-- This enables automatic model switching during peak hours

-- Peak hour configuration columns:
-- peak_hour_enabled: Whether peak hour switching is enabled (0/1 for SQLite)
-- peak_hour_start: Start time in HH:MM format (e.g., "09:00")
-- peak_hour_end: End time in HH:MM format (e.g., "17:00")
-- peak_hour_timezone: UTC offset in hours (e.g., "+7", "-5", "+5.5")
-- peak_hour_model: Model ID to switch to during peak hours

ALTER TABLE models ADD COLUMN peak_hour_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE models ADD COLUMN peak_hour_start TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN peak_hour_end TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN peak_hour_timezone TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN peak_hour_model TEXT NOT NULL DEFAULT '';
