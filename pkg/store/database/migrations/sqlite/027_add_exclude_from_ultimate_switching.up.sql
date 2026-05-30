-- Migration 027: Add exclude_from_ultimate_switching to models
-- This field prevents a model from being used in ultimate model switching logic.
-- Purpose: Allow certain models to opt-out of automatic ultimate model selection.

-- Values:
-- 0 (default): Model participates in ultimate model switching
-- 1: Model is excluded from ultimate model switching

ALTER TABLE models ADD COLUMN exclude_from_ultimate_switching INTEGER NOT NULL DEFAULT 0;
