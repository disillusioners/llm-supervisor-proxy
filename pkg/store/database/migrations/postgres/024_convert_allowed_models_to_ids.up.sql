-- Migration 024: Convert allowed_models from model names to model IDs
-- For each auth_token, replace model names with their corresponding IDs
-- If a name doesn't match any model, remove that entry
-- If the array becomes empty, set allowed_models to NULL

DO $$
DECLARE
    row_record RECORD;
    converted_ids TEXT[];
    model_id TEXT;
    original_value TEXT;
BEGIN
    -- Process each row with non-NULL allowed_models
    FOR row_record IN
        SELECT id, allowed_models FROM auth_tokens
        WHERE allowed_models IS NOT NULL
        AND allowed_models != '[]'::TEXT
        AND allowed_models != '[null]'::TEXT
    LOOP
        original_value := row_record.allowed_models;
        converted_ids := ARRAY[]::TEXT[];

        -- Iterate through each element in the JSON array
        FOR model_id IN
            SELECT value::TEXT
            FROM jsonb_array_elements_text(row_record.allowed_models::JSONB) AS value
        LOOP
            -- Look up the model ID by name
            SELECT m.id INTO model_id
            FROM models m
            WHERE m.name = model_id;

            -- Only add if we found a matching model
            IF model_id IS NOT NULL THEN
                converted_ids := array_append(converted_ids, model_id);
            END IF;
        END LOOP;

        -- Update the row: use the converted IDs array, or NULL if empty
        IF array_length(converted_ids, 1) IS NULL THEN
            UPDATE auth_tokens SET allowed_models = NULL WHERE id = row_record.id;
        ELSE
            UPDATE auth_tokens
            SET allowed_models = array_to_json(converted_ids)::TEXT
            WHERE id = row_record.id;
        END IF;
    END LOOP;
END $$;

-- Clean up any empty arrays that might have been created
UPDATE auth_tokens
SET allowed_models = NULL
WHERE allowed_models IS NOT NULL
AND (
    allowed_models = '[]'
    OR allowed_models = '[null]'
    OR jsonb_array_length(allowed_models::JSONB) = 0
);
