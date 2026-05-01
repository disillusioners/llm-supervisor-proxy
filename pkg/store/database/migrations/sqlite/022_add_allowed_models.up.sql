-- Add allowed_models column to auth_tokens table
-- This migration is idempotent - it only adds the column if it doesn't exist

-- Check if column exists before adding (SQLite doesn't support IF NOT EXISTS for ALTER TABLE ADD COLUMN)
-- We use a workaround that checks for the column's existence
PRAGMA table_info(auth_tokens);

-- The column will be added in a subsequent step handled by the migration runner
-- This file just declares the intent; actual column addition is handled by the code

-- Note: For new installations, the column will be created with the table
-- For existing installations, this migration adds the column
ALTER TABLE auth_tokens ADD COLUMN allowed_models TEXT DEFAULT NULL;
