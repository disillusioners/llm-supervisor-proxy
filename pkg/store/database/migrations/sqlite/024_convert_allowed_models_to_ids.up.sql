-- Migration 024: Convert allowed_models from model names to model IDs
-- For each auth_token, replace model names with their corresponding IDs
-- If a name doesn't match any model, remove that entry
-- If the array becomes empty, set allowed_models to NULL

-- SQLite doesn't support stored procedures, so we use a workaround with a recursive CTE
-- to iterate through each auth_token row and update them individually

-- First, let's update all rows that have non-NULL allowed_models
-- We do this with a single UPDATE that joins with a subquery to convert names to IDs

-- The approach: for each row, we reconstruct the JSON array by:
-- 1. Extracting each name from the JSON array using json_each
-- 2. Looking up the corresponding ID from the models table
-- 3. Using json_group_array to rebuild the array (only including matched entries)
-- 4. If the result is empty, we set it to NULL

-- Update auth_tokens by converting each name to its ID
UPDATE auth_tokens
SET allowed_models = (
    SELECT CASE
        WHEN COUNT(*) = 0 THEN NULL
        ELSE '[' || GROUP_CONCAT('"' || COALESCE(m.id, '') || '"', ',') || ']'
    END
    FROM json_each(auth_tokens.allowed_models) AS je
    LEFT JOIN models AS m ON m.name = je.value
    WHERE m.id IS NOT NULL
)
WHERE allowed_models IS NOT NULL
AND allowed_models != '[]'
AND allowed_models != '[null]';

-- Clean up any arrays that might have empty/null entries due to the above
-- This handles edge cases where the json_group_array produced malformed output
UPDATE auth_tokens
SET allowed_models = NULL
WHERE allowed_models IS NOT NULL
AND (
    allowed_models = '[]'
    OR allowed_models = '[""]'
    OR json_array_length(allowed_models) = 0
);
