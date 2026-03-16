-- Migration: Drop aggregation tables and columns
-- Context: The alert aggregation/correlation system has been removed from the codebase.
-- These tables and columns are no longer referenced by any application code.
-- Run this migration against production PostgreSQL after deploying the updated application.
--
-- Date: 2026-03-16
-- Related: docs/plans/completed/2026-03-16-remove-aggregation-and-legacy-cleanup.md

BEGIN;

-- Drop aggregation-related tables
DROP TABLE IF EXISTS aggregation_settings;
DROP TABLE IF EXISTS incident_alerts;
DROP TABLE IF EXISTS incident_merges;

-- Remove aggregation-related columns from incidents table
ALTER TABLE incidents DROP COLUMN IF EXISTS alert_count;
ALTER TABLE incidents DROP COLUMN IF EXISTS last_alert_at;
ALTER TABLE incidents DROP COLUMN IF EXISTS observing_started_at;
ALTER TABLE incidents DROP COLUMN IF EXISTS observing_duration_minutes;

COMMIT;
