-- name: GetConfig :one
SELECT * FROM configs WHERE id = 1;

-- name: UpdateConfig :exec
UPDATE configs SET
    version = sqlc.narg('version'),
    upstream_url = sqlc.narg('upstream_url'),
    upstream_token = sqlc.narg('upstream_token'),
    port = sqlc.narg('port'),
    idle_timeout_ms = sqlc.narg('idle_timeout_ms'),
    max_generation_time_ms = sqlc.narg('max_generation_time_ms'),
    max_upstream_error_retries = sqlc.narg('max_upstream_error_retries'),
    max_idle_retries = sqlc.narg('max_idle_retries'),
    max_generation_retries = sqlc.narg('max_generation_retries'),
    loop_detection_json = sqlc.narg('loop_detection_json'),
    updated_at = sqlc.narg('updated_at')
WHERE id = 1;

-- name: GetAllModels :many
SELECT * FROM models ORDER BY name;

-- name: GetEnabledModels :many
SELECT * FROM models WHERE enabled = 1 ORDER BY name;

-- name: GetModelByID :one
SELECT * FROM models WHERE id = ?;

-- name: InsertModel :exec
INSERT INTO models (
    id,
    name,
    enabled,
    fallback_chain_json,
    truncate_params_json
) VALUES (
    ?,
    ?,
    ?,
    ?,
    ?
);

-- name: UpdateModel :exec
UPDATE models SET
    name = ?,
    enabled = ?,
    fallback_chain_json = ?,
    truncate_params_json = ?,
    updated_at = datetime('now')
WHERE id = ?;

-- name: DeleteModel :exec
DELETE FROM models WHERE id = ?;

-- name: CountModels :one
SELECT COUNT(*) FROM models;

-- name: CountConfigs :one
SELECT COUNT(*) FROM configs;
