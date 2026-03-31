CREATE TABLE IF NOT EXISTS token_hourly_usage (
    token_id           TEXT    NOT NULL,
    hour_bucket        TEXT    NOT NULL,
    request_count      INTEGER NOT NULL DEFAULT 0,
    prompt_tokens      INTEGER NOT NULL DEFAULT 0,
    completion_tokens  INTEGER NOT NULL DEFAULT 0,
    total_tokens       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (token_id, hour_bucket)
);
CREATE INDEX IF NOT EXISTS idx_token_hourly_usage_token ON token_hourly_usage(token_id);
CREATE INDEX IF NOT EXISTS idx_token_hourly_usage_hour ON token_hourly_usage(hour_bucket);
