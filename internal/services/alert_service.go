package services

import (
	"fmt"
	"log"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AlertService manages alert sources, instances, and alerts
type AlertService struct {
	db *gorm.DB
}

// NewAlertService creates a new AlertService
func NewAlertService() *AlertService {
	return &AlertService{
		db: database.GetDB(),
	}
}

// ========== Alert Source Type Operations ==========

// ListSourceTypes returns all alert source types (alias for API)
func (s *AlertService) ListSourceTypes() ([]database.AlertSourceType, error) {
	var types []database.AlertSourceType
	if err := s.db.Find(&types).Error; err != nil {
		return nil, err
	}
	return types, nil
}

// ListAlertSourceTypes returns all alert source types
func (s *AlertService) ListAlertSourceTypes() ([]database.AlertSourceType, error) {
	return s.ListSourceTypes()
}

// GetAlertSourceType retrieves an alert source type by ID
func (s *AlertService) GetAlertSourceType(id uint) (*database.AlertSourceType, error) {
	var sourceType database.AlertSourceType
	if err := s.db.First(&sourceType, id).Error; err != nil {
		return nil, err
	}
	return &sourceType, nil
}

// GetAlertSourceTypeByName retrieves an alert source type by name
func (s *AlertService) GetAlertSourceTypeByName(name string) (*database.AlertSourceType, error) {
	var sourceType database.AlertSourceType
	if err := s.db.Where("name = ?", name).First(&sourceType).Error; err != nil {
		return nil, err
	}
	return &sourceType, nil
}

// CreateAlertSourceType creates a new alert source type
func (s *AlertService) CreateAlertSourceType(name, displayName, description string, defaultMappings database.JSONB, webhookSecretHeader string) (*database.AlertSourceType, error) {
	sourceType := &database.AlertSourceType{
		Name:                 name,
		DisplayName:          displayName,
		Description:          description,
		DefaultFieldMappings: defaultMappings,
		WebhookSecretHeader:  webhookSecretHeader,
	}
	if err := s.db.Create(sourceType).Error; err != nil {
		return nil, err
	}
	return sourceType, nil
}

// EnsureAlertSourceType creates or updates an alert source type
func (s *AlertService) EnsureAlertSourceType(name, displayName, description string, defaultMappings database.JSONB, webhookSecretHeader string) (*database.AlertSourceType, error) {
	var sourceType database.AlertSourceType
	result := s.db.Where("name = ?", name).First(&sourceType)

	if result.Error != nil {
		// Create new
		sourceType = database.AlertSourceType{
			Name:                 name,
			DisplayName:          displayName,
			Description:          description,
			DefaultFieldMappings: defaultMappings,
			WebhookSecretHeader:  webhookSecretHeader,
		}
		if err := s.db.Create(&sourceType).Error; err != nil {
			return nil, err
		}
		log.Printf("Created alert source type: %s", name)
	} else {
		// Update existing
		updates := map[string]interface{}{
			"display_name":           displayName,
			"description":            description,
			"default_field_mappings": defaultMappings,
			"webhook_secret_header":  webhookSecretHeader,
		}
		if err := s.db.Model(&sourceType).Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	return &sourceType, nil
}

// ========== Alert Source Instance Operations ==========

// ListInstances returns all alert source instances
func (s *AlertService) ListInstances() ([]database.AlertSourceInstance, error) {
	var instances []database.AlertSourceInstance
	if err := s.db.Preload("AlertSourceType").Find(&instances).Error; err != nil {
		return nil, err
	}
	return instances, nil
}

// GetInstance retrieves an alert source instance by ID
func (s *AlertService) GetInstance(id uint) (*database.AlertSourceInstance, error) {
	var instance database.AlertSourceInstance
	if err := s.db.Preload("AlertSourceType").First(&instance, id).Error; err != nil {
		return nil, err
	}
	return &instance, nil
}

// GetInstanceByUUID retrieves an alert source instance by UUID
func (s *AlertService) GetInstanceByUUID(uuid string) (*database.AlertSourceInstance, error) {
	var instance database.AlertSourceInstance
	if err := s.db.Preload("AlertSourceType").Where("uuid = ?", uuid).First(&instance).Error; err != nil {
		return nil, err
	}
	return &instance, nil
}

// CreateInstance creates a new alert source instance (takes source type name)
func (s *AlertService) CreateInstance(sourceTypeName, name, description, webhookSecret string, fieldMappings, settings database.JSONB) (*database.AlertSourceInstance, error) {
	// Look up source type by name
	sourceType, err := s.GetAlertSourceTypeByName(sourceTypeName)
	if err != nil {
		return nil, fmt.Errorf("alert source type not found: %s", sourceTypeName)
	}

	instance := &database.AlertSourceInstance{
		UUID:              uuid.New().String(),
		AlertSourceTypeID: sourceType.ID,
		Name:              name,
		Description:       description,
		WebhookSecret:     webhookSecret,
		FieldMappings:     fieldMappings,
		Settings:          settings,
		Enabled:           true,
	}
	if err := s.db.Create(instance).Error; err != nil {
		return nil, err
	}

	// Reload with source type
	return s.GetInstance(instance.ID)
}

// CreateInstanceByTypeID creates a new alert source instance (takes source type ID)
func (s *AlertService) CreateInstanceByTypeID(sourceTypeID uint, name, description, webhookSecret string, fieldMappings, settings database.JSONB) (*database.AlertSourceInstance, error) {
	instance := &database.AlertSourceInstance{
		UUID:              uuid.New().String(),
		AlertSourceTypeID: sourceTypeID,
		Name:              name,
		Description:       description,
		WebhookSecret:     webhookSecret,
		FieldMappings:     fieldMappings,
		Settings:          settings,
		Enabled:           true,
	}
	if err := s.db.Create(instance).Error; err != nil {
		return nil, err
	}

	// Reload with source type
	return s.GetInstance(instance.ID)
}

// UpdateInstance updates an alert source instance by UUID
func (s *AlertService) UpdateInstance(uuid string, updates map[string]interface{}) error {
	return s.db.Model(&database.AlertSourceInstance{}).Where("uuid = ?", uuid).Updates(updates).Error
}

// UpdateInstanceByID updates an alert source instance by ID
func (s *AlertService) UpdateInstanceByID(id uint, name, description, webhookSecret string, fieldMappings, settings database.JSONB, enabled bool) error {
	updates := map[string]interface{}{
		"name":           name,
		"description":    description,
		"webhook_secret": webhookSecret,
		"field_mappings": fieldMappings,
		"settings":       settings,
		"enabled":        enabled,
	}
	return s.db.Model(&database.AlertSourceInstance{}).Where("id = ?", id).Updates(updates).Error
}

// DeleteInstance deletes an alert source instance by UUID
func (s *AlertService) DeleteInstance(uuid string) error {
	return s.db.Where("uuid = ?", uuid).Delete(&database.AlertSourceInstance{}).Error
}

// DeleteInstanceByID deletes an alert source instance by ID
func (s *AlertService) DeleteInstanceByID(id uint) error {
	return s.db.Delete(&database.AlertSourceInstance{}, id).Error
}

// ========== Initialization ==========

// InitializeDefaultSourceTypes creates the default alert source types
func (s *AlertService) InitializeDefaultSourceTypes() error {
	sourceTypes := []struct {
		Name                string
		DisplayName         string
		Description         string
		WebhookSecretHeader string
		DefaultMappings     database.JSONB
	}{
		{
			Name:                "alertmanager",
			DisplayName:         "Prometheus Alertmanager",
			Description:         "Receive alerts from Prometheus Alertmanager",
			WebhookSecretHeader: "X-Alertmanager-Secret",
			DefaultMappings: database.JSONB{
				"alert_name":         "labels.alertname",
				"severity":           "labels.severity",
				"status":             "status",
				"summary":            "annotations.summary",
				"description":        "annotations.description",
				"target_host":        "labels.instance",
				"target_service":     "labels.job",
				"runbook_url":        "annotations.runbook_url",
				"source_fingerprint": "fingerprint",
				"started_at":         "startsAt",
				"ended_at":           "endsAt",
			},
		},
		{
			Name:                "pagerduty",
			DisplayName:         "PagerDuty",
			Description:         "Receive alerts from PagerDuty",
			WebhookSecretHeader: "X-PagerDuty-Signature",
			DefaultMappings: database.JSONB{
				"alert_name":      "event.data.title",
				"severity":        "event.data.priority.summary",
				"status":          "event.event_type",
				"summary":         "event.data.description",
				"target_host":     "event.data.source",
				"target_service":  "event.data.service.name",
				"runbook_url":     "event.data.body.details.runbook",
				"source_alert_id": "event.data.id",
			},
		},
		{
			Name:                "grafana",
			DisplayName:         "Grafana Alerting",
			Description:         "Receive alerts from Grafana",
			WebhookSecretHeader: "X-Grafana-Secret",
			DefaultMappings: database.JSONB{
				"alert_name":      "ruleName",
				"severity":        "state",
				"status":          "state",
				"summary":         "message",
				"target_host":     "evalMatches.0.tags.instance",
				"runbook_url":     "ruleUrl",
				"source_alert_id": "ruleId",
			},
		},
		{
			Name:                "datadog",
			DisplayName:         "Datadog",
			Description:         "Receive alerts from Datadog",
			WebhookSecretHeader: "X-Datadog-Signature",
			DefaultMappings: database.JSONB{
				"alert_name":      "title",
				"severity":        "priority",
				"status":          "alert_type",
				"summary":         "body",
				"target_host":     "tags.host",
				"runbook_url":     "event_links.0.url",
				"source_alert_id": "id",
			},
		},
		{
			Name:                "zabbix",
			DisplayName:         "Zabbix",
			Description:         "Receive alerts from Zabbix",
			WebhookSecretHeader: "X-Zabbix-Secret",
			DefaultMappings: database.JSONB{
				"alert_name":      "alert_name",
				"severity":        "priority",
				"status":          "event_status",
				"summary":         "trigger_expression",
				"target_host":     "hardware",
				"metric_name":     "metric_name",
				"metric_value":    "metric_value",
				"runbook_url":     "runbook_url",
				"source_alert_id": "event_id",
				"started_at":      "event_time",
			},
		},
	}

	for _, st := range sourceTypes {
		_, err := s.EnsureAlertSourceType(st.Name, st.DisplayName, st.Description, st.DefaultMappings, st.WebhookSecretHeader)
		if err != nil {
			return fmt.Errorf("failed to create alert source type %s: %w", st.Name, err)
		}
	}

	log.Println("Alert source types initialized")
	return nil
}
