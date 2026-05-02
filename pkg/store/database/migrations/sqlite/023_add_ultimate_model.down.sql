-- Drop ultimate_model column (SQLite 3.35+ supports DROP COLUMN)
ALTER TABLE auth_tokens DROP COLUMN ultimate_model;
