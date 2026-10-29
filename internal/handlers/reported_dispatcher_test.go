package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/akmatori/akmatori/internal/database"
)

// recordingReportedProcessor records dispatched reports.
type recordingReportedProcessor struct {
	got []ReportedAlert
	err error
}

func (p *recordingReportedProcessor) ProcessReportedAlert(r ReportedAlert) (string, bool, error) {
	p.got = append(p.got, r)
	return "inc-1", false, p.err
}

func seedQueuedReport(t *testing.T, uuid string) {
	t.Helper()
	if err := database.GetDB().Create(&database.Event{
		UUID: uuid, SourceKind: database.EventSourceCron, SourceUUID: "job-1", SourceType: "cron",
		ExternalID: "cron:job-1:disk full:db1", Kind: database.EventKindAlert, Classification: database.EventClassificationSource,
		ClassificationReasoning: "reported by cron run run-1",
		Title:                   "disk full", Summary: "/var at 94%", Host: "db1", Severity: "high", Status: "firing",
		ReceivedAt: time.Now(), OccurredAt: time.Now(),
		RawPayload:      database.JSONB{"cron_job_name": "disk check", "original_message": "/var at 94%"},
		PendingDispatch: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestReportedEventDispatcher_DispatchesOnce(t *testing.T) {
	db := setupEventsHandlerDB(t)
	seedQueuedReport(t, "ev-1")
	proc := &recordingReportedProcessor{}
	d := NewReportedEventDispatcher(db, proc)

	if n := d.DispatchPending(context.Background()); n != 1 {
		t.Fatalf("dispatched %d, want 1", n)
	}
	if n := d.DispatchPending(context.Background()); n != 0 {
		t.Fatalf("second pass dispatched %d, want 0 (claimed)", n)
	}
	if len(proc.got) != 1 {
		t.Fatalf("processor calls = %d, want 1", len(proc.got))
	}
	r := proc.got[0]
	if r.SourceUUID != "job-1" || r.SourceLabel != database.IncidentSourceKindCron || r.SourceName != "disk check" ||
		r.SlackChannelID != "" || r.Alert.EventUUID != "ev-1" || r.Alert.SourceFingerprint != "cron:job-1:disk full:db1" ||
		r.Alert.TargetHost != "db1" || r.Alert.Severity != database.AlertSeverityHigh || r.Alert.Status != database.AlertStatusFiring {
		t.Fatalf("report = %+v", r)
	}
}

func TestReportedEventDispatcher_PassesJobChannel(t *testing.T) {
	db := setupEventsHandlerDB(t)
	seedQueuedReport(t, "ev-1")
	proc := &recordingReportedProcessor{}
	d := NewReportedEventDispatcher(db, proc)
	var asked string
	d.SetReportChannel(func(jobUUID string) *database.Channel {
		asked = jobUUID
		return &database.Channel{UUID: "ch-ops"}
	})

	d.DispatchPending(context.Background())

	if asked != "job-1" || len(proc.got) != 1 || proc.got[0].PostChannel == nil || proc.got[0].PostChannel.UUID != "ch-ops" {
		t.Fatalf("asked=%q reports=%+v, want the job's channel passed on", asked, proc.got)
	}
}

func TestReportedEventDispatcher_RecordsFailure(t *testing.T) {
	db := setupEventsHandlerDB(t)
	seedQueuedReport(t, "ev-1")
	proc := &recordingReportedProcessor{err: errors.New("spawn failed")}

	NewReportedEventDispatcher(db, proc).DispatchPending(context.Background())

	var ev database.Event
	db.First(&ev, "uuid = ?", "ev-1")
	if ev.PendingDispatch || !strings.Contains(ev.ClassificationReasoning, "dispatch failed: spawn failed") {
		t.Fatalf("event = pending:%v reasoning:%q", ev.PendingDispatch, ev.ClassificationReasoning)
	}
}

func TestProcessReportedAlert_RepeatJoinsFiringAlert(t *testing.T) {
	db := setupEventsHandlerDB(t)
	if err := db.Create(&database.Alert{UUID: "al-1", IncidentUUID: "inc-open", Status: database.AlertStatusFiring,
		SourceUUID: "job-1", SourceFingerprint: "cron:job-1:disk full:db1", FiredAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &eventsSkillService{corrGateSkillService: &corrGateSkillService{}}
	h := NewAlertHandler(nil, nil, nil, nil, svc, nil, nil)

	alert := newCorrTestAlert()
	alert.SourceFingerprint = "cron:job-1:disk full:db1"
	incidentUUID, linked, err := h.ProcessReportedAlert(ReportedAlert{SourceUUID: "job-1", SourceLabel: "cron", Alert: alert})

	if err != nil || !linked || incidentUUID != "inc-open" || svc.getSpawnCount() != 0 || svc.getLinkCount() != 1 {
		t.Fatalf("got (%q, %v, %v) spawn=%d link=%d, want a link to inc-open", incidentUUID, linked, err, svc.getSpawnCount(), svc.getLinkCount())
	}
}
