-- Migration 027: Add exclude_from_ultimate_switching to models
-- This field prevents a model from being used in ultimate model switching logic.
-- Purpose: Allow certain models to opt-out of automatic ultimate model selection.

-- Values:
-- false (default): Model participates in ultimate model switching
-- true: Model is excluded from ultimate model switching

ALTER TABLE models ADD COLUMN exclude_from_ultimate_switching BOOLEAN NOT NULL DEFAULT false;
