-- Migration 024 DOWN: Convert allowed_models from model IDs back to model names
-- For each auth_token, replace model IDs with their corresponding names
-- If an ID doesn't match any model, remove that entry
-- If the array becomes empty, set allowed_models to NULL

DO $$
DECLARE
    row_record RECORD;
    converted_names TEXT[];
    model_name TEXT;
BEGIN
    -- Process each row with non-NULL allowed_models
    FOR row_record IN
        SELECT id, allowed_models FROM auth_tokens
        WHERE allowed_models IS NOT NULL
        AND allowed_models != '[]'::TEXT
        AND allowed_models != '[null]'::TEXT
    LOOP
        converted_names := ARRAY[]::TEXT[];

        -- Iterate through each ID in the JSON array
        FOR model_name IN
            -- We reuse model_name variable to store the ID temporarily
            SELECT value::TEXT
            FROM jsonb_array_elements_text(row_record.allowed_models::JSONB) AS value
        LOOP
            -- Look up the model name by ID
            SELECT m.name INTO model_name
            FROM models m
            WHERE m.id = model_name;

            -- Only add if we found a matching model
            IF model_name IS NOT NULL THEN
                converted_names := array_append(converted_names, model_name);
            END IF;
        END LOOP;

        -- Update the row: use the converted names array, or NULL if empty
        IF array_length(converted_names, 1) IS NULL THEN
            UPDATE auth_tokens SET allowed_models = NULL WHERE id = row_record.id;
        ELSE
            UPDATE auth_tokens
            SET allowed_models = array_to_json(converted_names)::TEXT
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
