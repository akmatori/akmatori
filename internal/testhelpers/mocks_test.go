package testhelpers

import (
	"context"
	"errors"
	"testing"

	"github.com/akmatori/akmatori/internal/database"
)

func TestMockAlertService_GetInstance(t *testing.T) {
	instance := NewAlertSourceInstanceBuilder().
		WithUUID("test-uuid").
		WithName("Test Instance").
		Build()

	mock := NewMockAlertService().
		WithInstance("test-uuid", &instance)

	ctx := context.Background()
	result, err := mock.GetInstance(ctx, "test-uuid")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected instance, got nil")
	}
	if result.Name != "Test Instance" {
		t.Errorf("expected name 'Test Instance', got %s", result.Name)
	}

	// Check call tracking
	if len(mock.GetInstanceCalls) != 1 {
		t.Errorf("expected 1 call, got %d", len(mock.GetInstanceCalls))
	}
	if mock.GetInstanceCalls[0] != "test-uuid" {
		t.Errorf("expected call with 'test-uuid', got %s", mock.GetInstanceCalls[0])
	}
}

func TestMockAlertService_GetInstanceError(t *testing.T) {
	expectedErr := errors.New("database error")
	mock := NewMockAlertService().WithInstanceError(expectedErr)

	ctx := context.Background()
	_, err := mock.GetInstance(ctx, "any-uuid")

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestMockAlertService_ProcessAlert(t *testing.T) {
	alert := NewAlertBuilder().
		WithName("TestAlert").
		WithSeverity(database.AlertSeverityCritical).
		Build()

	mock := NewMockAlertService().
		WithProcessedAlerts(alert)

	ctx := context.Background()
	body := []byte(`{"status": "firing"}`)
	result, err := mock.ProcessAlert(ctx, "test-instance", body)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(result))
	}
	if result[0].AlertName != "TestAlert" {
		t.Errorf("expected alert name 'TestAlert', got %s", result[0].AlertName)
	}

	// Check call tracking
	if len(mock.ProcessAlertCalls) != 1 {
		t.Errorf("expected 1 call, got %d", len(mock.ProcessAlertCalls))
	}
	call := mock.ProcessAlertCalls[0]
	if call.InstanceUUID != "test-instance" {
		t.Errorf("expected instance UUID 'test-instance', got %s", call.InstanceUUID)
	}
	if string(call.Body) != string(body) {
		t.Errorf("expected body %q, got %q", body, call.Body)
	}
}

func TestMockAlertService_ProcessAlertError(t *testing.T) {
	expectedErr := errors.New("process error")
	mock := NewMockAlertService().WithProcessError(expectedErr)

	ctx := context.Background()
	_, err := mock.ProcessAlert(ctx, "any", nil)

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestMockAlertService_Reset(t *testing.T) {
	mock := NewMockAlertService()

	ctx := context.Background()
	mock.GetInstance(ctx, "uuid1")
	mock.GetInstance(ctx, "uuid2")
	mock.ProcessAlert(ctx, "inst1", nil)

	if len(mock.GetInstanceCalls) != 2 {
		t.Errorf("expected 2 GetInstance calls before reset")
	}
	if len(mock.ProcessAlertCalls) != 1 {
		t.Errorf("expected 1 ProcessAlert call before reset")
	}

	mock.Reset()

	if len(mock.GetInstanceCalls) != 0 {
		t.Errorf("expected 0 GetInstance calls after reset")
	}
	if len(mock.ProcessAlertCalls) != 0 {
		t.Errorf("expected 0 ProcessAlert calls after reset")
	}
}

func TestMockIncidentService_GetIncident(t *testing.T) {
	incident := NewIncidentBuilder().
		WithUUID("test-incident").
		WithTitle("Test Incident").
		Build()

	mock := NewMockIncidentService().
		WithIncident("test-incident", &incident)

	ctx := context.Background()
	result, err := mock.GetIncident(ctx, "test-incident")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected incident, got nil")
	}
	if result.Title != "Test Incident" {
		t.Errorf("expected title 'Test Incident', got %s", result.Title)
	}

	if len(mock.GetIncidentCalls) != 1 {
		t.Errorf("expected 1 call, got %d", len(mock.GetIncidentCalls))
	}
}

func TestMockIncidentService_CreateIncident(t *testing.T) {
	mock := NewMockIncidentService()

	ctx := context.Background()
	incident := NewIncidentBuilder().
		WithTitle("New Incident").
		Build()

	result, err := mock.CreateIncident(ctx, incident)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected incident, got nil")
	}
	if result.ID == 0 {
		t.Error("expected non-zero ID")
	}

	if len(mock.CreateIncidentCalls) != 1 {
		t.Errorf("expected 1 call, got %d", len(mock.CreateIncidentCalls))
	}
	if mock.CreateIncidentCalls[0].Title != "New Incident" {
		t.Errorf("expected title 'New Incident', got %s", mock.CreateIncidentCalls[0].Title)
	}
}

func TestMockIncidentService_CreateIncidentWithConfigured(t *testing.T) {
	configuredIncident := NewIncidentBuilder().
		WithID(42).
		WithUUID("configured-uuid").
		WithTitle("Configured Incident").
		Build()

	mock := NewMockIncidentService().
		WithCreatedIncident(&configuredIncident)

	ctx := context.Background()
	input := NewIncidentBuilder().WithTitle("Input Incident").Build()

	result, err := mock.CreateIncident(ctx, input)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.ID != 42 {
		t.Errorf("expected ID 42, got %d", result.ID)
	}
	if result.UUID != "configured-uuid" {
		t.Errorf("expected UUID 'configured-uuid', got %s", result.UUID)
	}
}

func TestMockIncidentService_CreateIncidentError(t *testing.T) {
	expectedErr := errors.New("create error")
	mock := NewMockIncidentService().WithCreateError(expectedErr)

	ctx := context.Background()
	_, err := mock.CreateIncident(ctx, database.Incident{})

	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

func TestMockSkillService_GetSkill(t *testing.T) {
	skill := NewSkillBuilder().
		WithName("test-skill").
		WithDescription("Test Description").
		Build()

	mock := NewMockSkillService().
		WithSkill("test-skill", &skill)

	ctx := context.Background()
	result, err := mock.GetSkill(ctx, "test-skill")

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected skill, got nil")
	}
	if result.Description != "Test Description" {
		t.Errorf("expected description 'Test Description', got %s", result.Description)
	}

	if len(mock.GetSkillCalls) != 1 {
		t.Errorf("expected 1 call, got %d", len(mock.GetSkillCalls))
	}
}

func TestMockSkillService_ListSkills(t *testing.T) {
	skills := []database.Skill{
		NewSkillBuilder().WithName("skill1").Build(),
		NewSkillBuilder().WithName("skill2").Build(),
	}

	mock := NewMockSkillService().WithAllSkills(skills...)

	ctx := context.Background()
	result, err := mock.ListSkills(ctx)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 skills, got %d", len(result))
	}

	if mock.ListSkillsCalled != 1 {
		t.Errorf("expected ListSkillsCalled = 1, got %d", mock.ListSkillsCalled)
	}
}

func TestMockSkillService_Error(t *testing.T) {
	expectedErr := errors.New("skill error")
	mock := NewMockSkillService().WithSkillError(expectedErr)

	ctx := context.Background()

	_, err := mock.GetSkill(ctx, "any")
	if err != expectedErr {
		t.Errorf("GetSkill: expected error %v, got %v", expectedErr, err)
	}

	_, err = mock.ListSkills(ctx)
	if err != expectedErr {
		t.Errorf("ListSkills: expected error %v, got %v", expectedErr, err)
	}
}

// Benchmark mock creation and usage
func BenchmarkMockAlertService_GetInstance(b *testing.B) {
	instance := NewAlertSourceInstanceBuilder().WithUUID("bench-uuid").Build()
	mock := NewMockAlertService().WithInstance("bench-uuid", &instance)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mock.GetInstance(ctx, "bench-uuid")
	}
}

func BenchmarkMockAlertService_ProcessAlert(b *testing.B) {
	alert := NewAlertBuilder().Build()
	mock := NewMockAlertService().WithProcessedAlerts(alert)
	ctx := context.Background()
	body := []byte(`{"status": "firing"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mock.ProcessAlert(ctx, "inst", body)
	}
}

// Test concurrent access to mocks
func TestMockAlertService_ConcurrentAccess(t *testing.T) {
	instance := NewAlertSourceInstanceBuilder().WithUUID("concurrent-uuid").Build()
	alert := NewAlertBuilder().Build()

	mock := NewMockAlertService().
		WithInstance("concurrent-uuid", &instance).
		WithProcessedAlerts(alert)

	ctx := context.Background()

	ConcurrentTestWithTimeout(t, 5*Second, 10, func(workerID int) {
		for i := 0; i < 100; i++ {
			mock.GetInstance(ctx, "concurrent-uuid")
			mock.ProcessAlert(ctx, "inst", nil)
		}
	})

	// Verify call counts
	expectedGetCalls := 10 * 100
	expectedProcessCalls := 10 * 100

	if len(mock.GetInstanceCalls) != expectedGetCalls {
		t.Errorf("expected %d GetInstance calls, got %d", expectedGetCalls, len(mock.GetInstanceCalls))
	}
	if len(mock.ProcessAlertCalls) != expectedProcessCalls {
		t.Errorf("expected %d ProcessAlert calls, got %d", expectedProcessCalls, len(mock.ProcessAlertCalls))
	}
}

// Helper constant for tests
const Second = 1000000000 // time.Second in nanoseconds

func TestMockAlertService_NilInstance(t *testing.T) {
	mock := NewMockAlertService()
	ctx := context.Background()

	result, err := mock.GetInstance(ctx, "nonexistent")

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for nonexistent instance, got %v", result)
	}
}

// Integration-style test showing mock usage
func TestMockAlertService_IntegrationExample(t *testing.T) {
	// Setup mocks
	instance := NewAlertSourceInstanceBuilder().
		WithUUID("prod-alertmanager").
		WithName("Production Alertmanager").
		WithAlertSourceTypeID(1).
		Build()

	alertMock := NewMockAlertService().
		WithInstance("prod-alertmanager", &instance).
		WithProcessedAlerts(
			NewAlertBuilder().WithName("HighCPU").WithSeverity(database.AlertSeverityCritical).Build(),
			NewAlertBuilder().WithName("LowMemory").WithSeverity(database.AlertSeverityWarning).Build(),
		)

	// Simulate handler flow
	ctx := context.Background()

	// 1. Get instance
	inst, err := alertMock.GetInstance(ctx, "prod-alertmanager")
	AssertNoError(t, err, "GetInstance")
	AssertNotNil(t, inst, "instance")
	AssertEqual(t, "Production Alertmanager", inst.Name, "instance name")

	// 2. Process alert
	parsedAlerts, err := alertMock.ProcessAlert(ctx, inst.UUID, []byte(`{}`))
	AssertNoError(t, err, "ProcessAlert")
	AssertSliceLen(t, parsedAlerts, 2, "parsed alerts")

	// 3. Verify critical alerts
	var criticalCount int
	for _, a := range parsedAlerts {
		if a.Severity == database.AlertSeverityCritical {
			criticalCount++
		}
	}
	AssertEqual(t, 1, criticalCount, "critical alert count")
}
