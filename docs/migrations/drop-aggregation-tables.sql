-- Migration: Drop aggregation tables and columns
-- Context: The alert aggregation/correlation system has been removed from the codebase.
-- These tables and columns are no longer referenced by any application code.
-- IMPORTANT: Run this migration AFTER deploying the application version that removes
-- aggregation code. Running it before deployment will break the running application
-- because the old binary still references these columns and tables.
--
-- Date: 2026-03-16
-- Related: docs/plans/completed/2026-03-16-remove-aggregation-and-legacy-cleanup.md

BEGIN;

-- Drop aggregation-related tables
DROP TABLE IF EXISTS aggregation_settings;
DROP TABLE IF EXISTS incident_alerts;
DROP TABLE IF EXISTS incident_merges;

-- Transition any incidents stuck in 'observing' status (no longer a valid state)
UPDATE incidents SET status = 'diagnosed' WHERE status = 'observing';

-- Remove aggregation-related columns from incidents table
ALTER TABLE incidents DROP COLUMN IF EXISTS alert_count;
ALTER TABLE incidents DROP COLUMN IF EXISTS last_alert_at;
ALTER TABLE incidents DROP COLUMN IF EXISTS observing_started_at;
ALTER TABLE incidents DROP COLUMN IF EXISTS observing_duration_minutes;

COMMIT;
