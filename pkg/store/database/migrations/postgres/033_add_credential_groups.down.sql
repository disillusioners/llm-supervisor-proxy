-- Migration 033 (down): documentation-only artifact. No down-runner exists
-- in this repo (025/026/027 have NO down file); rollback = restore backup.
DROP TABLE IF EXISTS credential_groups;
