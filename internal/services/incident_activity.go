package services

import "time"

// An incident's "last activity" is the most recent of:
//
//  1. alert activity — MAX(COALESCE(alerts.last_seen_at, alerts.fired_at))
//     across every linked alert, so both a fresh recurrence (new row) and a
//     re-send of an already-firing alert (last_seen_at bump) count;
//  2. investigation activity — completed_at, which advances on every turn of a
//     multi-turn incident, so an operator chatting in the Slack thread keeps
//     the incident alive;
//  3. started_at, as the floor for incidents with neither.
//
// The two consumers are the stale-close sweep (incident_close_service.go) and
// the correlator's fingerprint fast path (alert_correlator.go). They must
// agree on the definition: if the fast path considers an incident live while
// the sweep considers it stale, alerts get attached to an incident that is
// about to close.
//
// The conditions below are expressed as conjunctions/disjunctions rather than
// GREATEST(...) because production runs PostgreSQL and tests run SQLite, which
// disagree on multi-argument MAX and lack a common GREATEST.

// alertActivityExpr is the per-alert activity timestamp: the last re-send if
// the alerting system ever repeated the alert, otherwise the original fire.
const alertActivityExpr = "COALESCE(alerts.last_seen_at, alerts.fired_at)"

// staleIncidentCond returns a WHERE fragment and its args matching incidents
// whose last activity is strictly before cutoff. Intended to be ANDed onto a
// query whose primary table is `incidents`.
func staleIncidentCond(cutoff time.Time) (string, []interface{}) {
	cond := "incidents.started_at < ?" +
		" AND COALESCE(incidents.completed_at, incidents.started_at) < ?" +
		" AND NOT EXISTS (SELECT 1 FROM alerts WHERE alerts.incident_uuid = incidents.uuid AND " +
		alertActivityExpr + " >= ?)"
	return cond, []interface{}{cutoff, cutoff, cutoff}
}

// liveIncidentCond returns a WHERE fragment and its args matching incidents
// whose last activity is at or after since — the exact complement of
// staleIncidentCond for the same instant.
func liveIncidentCond(since time.Time) (string, []interface{}) {
	cond := "(COALESCE(incidents.completed_at, incidents.started_at) >= ?" +
		" OR EXISTS (SELECT 1 FROM alerts WHERE alerts.incident_uuid = incidents.uuid AND " +
		alertActivityExpr + " >= ?))"
	return cond, []interface{}{since, since}
}
