package handlers

import (
	"errors"
	"fmt"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// slackThreadWork is the agent work a Slack thread belongs to: request work
// that answers in the thread (a mention thread) or an incident whose alert
// thread it is.
type slackThreadWork struct {
	// ScopeUUID is the scope the worker keys the runs by: the request scope,
	// or the incident UUID.
	ScopeUUID string
	// IncidentUUID is set for incident threads.
	IncidentUUID string
	Title        string
	// Response is the newest agent answer in the thread's work.
	Response string
}

// lookupSlackThread resolves a thread ts to its work: first request work
// whose runs answer in the thread (agent_runs.slack_thread_ts), then an
// incident whose alert or report thread it is (incidents.slack_message_ts).
// Returns an error when the thread belongs to no work.
func lookupSlackThread(threadTS string) (*slackThreadWork, error) {
	if threadTS == "" {
		return nil, fmt.Errorf("empty thread")
	}
	db := database.GetDB()
	if db == nil {
		return nil, fmt.Errorf("db unavailable")
	}

	var run database.AgentRun
	err := db.Select("scope_uuid").
		Where("slack_thread_ts = ? AND (incident_uuid IS NULL OR incident_uuid = '') AND scope_uuid <> ''", threadTS).
		Order("id DESC").First(&run).Error
	if err == nil {
		work := &slackThreadWork{ScopeUUID: run.ScopeUUID}
		var ev database.Event
		if db.Select("title").Where("uuid = ?", run.ScopeUUID).Limit(1).Find(&ev).Error == nil {
			work.Title = ev.Title
		}
		var answered database.AgentRun
		if db.Select("response").Where("scope_uuid = ? AND response <> ''", run.ScopeUUID).
			Order("id DESC").Limit(1).Find(&answered).Error == nil {
			work.Response = answered.Response
		}
		return work, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var incident database.Incident
	if err := db.Select("uuid, title").Where("slack_message_ts = ?", threadTS).First(&incident).Error; err != nil {
		return nil, fmt.Errorf("no work for thread %s: %w", threadTS, err)
	}
	work := &slackThreadWork{ScopeUUID: incident.UUID, IncidentUUID: incident.UUID, Title: incident.Title}
	if responses, err := database.LatestRunResponses(db, []string{incident.UUID}); err == nil {
		work.Response = responses[incident.UUID]
	}
	return work, nil
}
