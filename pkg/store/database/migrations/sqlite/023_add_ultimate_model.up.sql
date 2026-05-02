-- Add ultimate_model column to auth_tokens table for per-token ultimate model override
-- NULL means use global config, non-NULL overrides the global ultimate model setting
ALTER TABLE auth_tokens ADD COLUMN ultimate_model TEXT DEFAULT NULL;
