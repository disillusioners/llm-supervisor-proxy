-- Migration 024 DOWN: Convert allowed_models from model IDs back to model names
-- For each auth_token, replace model IDs with their corresponding names
-- If an ID doesn't match any model, remove that entry
-- If the array becomes empty, set allowed_models to NULL

-- SQLite doesn't support stored procedures, so we use json_each to iterate

-- Update auth_tokens by converting each ID to its name
UPDATE auth_tokens
SET allowed_models = (
    SELECT CASE
        WHEN COUNT(*) = 0 THEN NULL
        ELSE '[' || GROUP_CONCAT('"' || COALESCE(m.name, '') || '"', ',') || ']'
    END
    FROM json_each(auth_tokens.allowed_models) AS je
    LEFT JOIN models AS m ON m.id = je.value
    WHERE m.name IS NOT NULL
)
WHERE allowed_models IS NOT NULL
AND allowed_models != '[]'
AND allowed_models != '[null]';

-- Clean up any arrays that might have empty/null entries
UPDATE auth_tokens
SET allowed_models = NULL
WHERE allowed_models IS NOT NULL
AND (
    allowed_models = '[]'
    OR allowed_models = '[""]'
    OR json_array_length(allowed_models) = 0
);
