// Package testhelpers provides mock implementations for testing
package testhelpers

import (
	"context"
	"sync"

	"github.com/akmatori/akmatori/internal/alerts"
	"github.com/akmatori/akmatori/internal/database"
)

// ========================================
// Mock Alert Service
// ========================================

// MockAlertService provides a mock implementation of alert service for testing
type MockAlertService struct {
	mu sync.RWMutex

	// Configured responses
	instances       map[string]*database.AlertSourceInstance
	instancesErr    error
	processedAlerts []alerts.NormalizedAlert
	processErr      error

	// Call tracking
	GetInstanceCalls    []string
	ProcessAlertCalls   []ProcessAlertCall
	RegisterSourceCalls []string
}

// ProcessAlertCall tracks a call to ProcessAlert
type ProcessAlertCall struct {
	InstanceUUID string
	Body         []byte
}

// NewMockAlertService creates a new mock alert service
func NewMockAlertService() *MockAlertService {
	return &MockAlertService{
		instances:           make(map[string]*database.AlertSourceInstance),
		processedAlerts:     []alerts.NormalizedAlert{},
		GetInstanceCalls:    []string{},
		ProcessAlertCalls:   []ProcessAlertCall{},
		RegisterSourceCalls: []string{},
	}
}

// WithInstance configures an instance to return for GetInstance
func (m *MockAlertService) WithInstance(uuid string, instance *database.AlertSourceInstance) *MockAlertService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances[uuid] = instance
	return m
}

// WithInstanceError configures GetInstance to return an error
func (m *MockAlertService) WithInstanceError(err error) *MockAlertService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.instancesErr = err
	return m
}

// WithProcessedAlerts configures alerts to return from ProcessAlert
func (m *MockAlertService) WithProcessedAlerts(alerts ...alerts.NormalizedAlert) *MockAlertService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processedAlerts = alerts
	return m
}

// WithProcessError configures ProcessAlert to return an error
func (m *MockAlertService) WithProcessError(err error) *MockAlertService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processErr = err
	return m
}

// GetInstance returns a configured instance or error
func (m *MockAlertService) GetInstance(ctx context.Context, uuid string) (*database.AlertSourceInstance, error) {
	m.mu.Lock()
	m.GetInstanceCalls = append(m.GetInstanceCalls, uuid)
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.instancesErr != nil {
		return nil, m.instancesErr
	}

	if instance, ok := m.instances[uuid]; ok {
		return instance, nil
	}
	return nil, nil
}

// ProcessAlert processes an alert and returns configured response
func (m *MockAlertService) ProcessAlert(ctx context.Context, instanceUUID string, body []byte) ([]alerts.NormalizedAlert, error) {
	m.mu.Lock()
	m.ProcessAlertCalls = append(m.ProcessAlertCalls, ProcessAlertCall{
		InstanceUUID: instanceUUID,
		Body:         body,
	})
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.processErr != nil {
		return nil, m.processErr
	}
	return m.processedAlerts, nil
}

// Reset clears all call tracking
func (m *MockAlertService) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetInstanceCalls = []string{}
	m.ProcessAlertCalls = []ProcessAlertCall{}
	m.RegisterSourceCalls = []string{}
}

// ========================================
// Mock Incident Service
// ========================================

// MockIncidentService provides a mock implementation for incident service
type MockIncidentService struct {
	mu sync.RWMutex

	// Configured responses
	incidents       map[string]*database.Incident
	incidentsErr    error
	createErr       error
	createdIncident *database.Incident

	// Call tracking
	GetIncidentCalls    []string
	CreateIncidentCalls []database.Incident
	UpdateIncidentCalls []UpdateIncidentCall
}

// UpdateIncidentCall tracks a call to UpdateIncident
type UpdateIncidentCall struct {
	UUID    string
	Updates map[string]interface{}
}

// NewMockIncidentService creates a new mock incident service
func NewMockIncidentService() *MockIncidentService {
	return &MockIncidentService{
		incidents:           make(map[string]*database.Incident),
		GetIncidentCalls:    []string{},
		CreateIncidentCalls: []database.Incident{},
		UpdateIncidentCalls: []UpdateIncidentCall{},
	}
}

// WithIncident configures an incident to return
func (m *MockIncidentService) WithIncident(uuid string, incident *database.Incident) *MockIncidentService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.incidents[uuid] = incident
	return m
}

// WithIncidentError configures GetIncident to return an error
func (m *MockIncidentService) WithIncidentError(err error) *MockIncidentService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.incidentsErr = err
	return m
}

// WithCreateError configures CreateIncident to return an error
func (m *MockIncidentService) WithCreateError(err error) *MockIncidentService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createErr = err
	return m
}

// WithCreatedIncident configures the incident to return from CreateIncident
func (m *MockIncidentService) WithCreatedIncident(incident *database.Incident) *MockIncidentService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.createdIncident = incident
	return m
}

// GetIncident returns a configured incident or error
func (m *MockIncidentService) GetIncident(ctx context.Context, uuid string) (*database.Incident, error) {
	m.mu.Lock()
	m.GetIncidentCalls = append(m.GetIncidentCalls, uuid)
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.incidentsErr != nil {
		return nil, m.incidentsErr
	}

	if incident, ok := m.incidents[uuid]; ok {
		return incident, nil
	}
	return nil, nil
}

// CreateIncident creates an incident and tracks the call
func (m *MockIncidentService) CreateIncident(ctx context.Context, incident database.Incident) (*database.Incident, error) {
	m.mu.Lock()
	m.CreateIncidentCalls = append(m.CreateIncidentCalls, incident)
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.createErr != nil {
		return nil, m.createErr
	}

	if m.createdIncident != nil {
		return m.createdIncident, nil
	}

	// Return the incident with an ID set
	incident.ID = uint(len(m.CreateIncidentCalls))
	return &incident, nil
}

// Reset clears all call tracking
func (m *MockIncidentService) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetIncidentCalls = []string{}
	m.CreateIncidentCalls = []database.Incident{}
	m.UpdateIncidentCalls = []UpdateIncidentCall{}
}

// ========================================
// Mock Skill Service
// ========================================

// MockSkillService provides a mock implementation for skill service
type MockSkillService struct {
	mu sync.RWMutex

	// Configured responses
	skills    map[string]*database.Skill
	allSkills []database.Skill
	skillErr  error

	// Call tracking
	GetSkillCalls     []string
	ListSkillsCalled  int
	CreateSkillCalls  []database.Skill
	EnableSkillCalls  []string
	DisableSkillCalls []string
}

// NewMockSkillService creates a new mock skill service
func NewMockSkillService() *MockSkillService {
	return &MockSkillService{
		skills:            make(map[string]*database.Skill),
		allSkills:         []database.Skill{},
		GetSkillCalls:     []string{},
		CreateSkillCalls:  []database.Skill{},
		EnableSkillCalls:  []string{},
		DisableSkillCalls: []string{},
	}
}

// WithSkill configures a skill to return
func (m *MockSkillService) WithSkill(name string, skill *database.Skill) *MockSkillService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.skills[name] = skill
	return m
}

// WithAllSkills configures skills to return from ListSkills
func (m *MockSkillService) WithAllSkills(skills ...database.Skill) *MockSkillService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.allSkills = skills
	return m
}

// WithSkillError configures GetSkill to return an error
func (m *MockSkillService) WithSkillError(err error) *MockSkillService {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.skillErr = err
	return m
}

// GetSkill returns a configured skill or error
func (m *MockSkillService) GetSkill(ctx context.Context, name string) (*database.Skill, error) {
	m.mu.Lock()
	m.GetSkillCalls = append(m.GetSkillCalls, name)
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.skillErr != nil {
		return nil, m.skillErr
	}

	if skill, ok := m.skills[name]; ok {
		return skill, nil
	}
	return nil, nil
}

// ListSkills returns all configured skills
func (m *MockSkillService) ListSkills(ctx context.Context) ([]database.Skill, error) {
	m.mu.Lock()
	m.ListSkillsCalled++
	m.mu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.skillErr != nil {
		return nil, m.skillErr
	}
	return m.allSkills, nil
}

// Reset clears all call tracking
func (m *MockSkillService) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetSkillCalls = []string{}
	m.ListSkillsCalled = 0
	m.CreateSkillCalls = []database.Skill{}
	m.EnableSkillCalls = []string{}
	m.DisableSkillCalls = []string{}
}
