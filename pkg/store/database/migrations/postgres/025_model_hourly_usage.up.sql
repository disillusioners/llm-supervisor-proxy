CREATE TABLE IF NOT EXISTS model_hourly_usage (
    model_id          TEXT    NOT NULL,
    hour_bucket       TEXT    NOT NULL,
    request_count     INTEGER NOT NULL DEFAULT 0,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0,
    completion_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (model_id, hour_bucket)
);
CREATE INDEX IF NOT EXISTS idx_model_hourly_usage_model ON model_hourly_usage(model_id);
CREATE INDEX IF NOT EXISTS idx_model_hourly_usage_hour ON model_hourly_usage(hour_bucket);
