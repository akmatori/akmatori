package services

import (
	"fmt"
	"time"

	"github.com/akmatori/akmatori/internal/database"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SSHKeyEntry represents an SSH key without the private key content (for listing)
type SSHKeyEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	CreatedAt string `json:"created_at"`
}

// SSHKeyFull represents an SSH key with all fields (for internal use)
type SSHKeyFull struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PrivateKey string `json:"private_key"`
	IsDefault  bool   `json:"is_default"`
	CreatedAt  string `json:"created_at"`
}

// ToolService manages tool types and instances
type ToolService struct {
	db *gorm.DB
}

// NewToolService creates a new tool service
func NewToolService() *ToolService {
	return &ToolService{
		db: database.GetDB(),
	}
}

// CreateToolInstance creates a new tool instance
func (s *ToolService) CreateToolInstance(toolTypeID uint, name string, settings database.JSONB) (*database.ToolInstance, error) {
	instance := &database.ToolInstance{
		ToolTypeID: toolTypeID,
		Name:       name,
		Settings:   settings,
		Enabled:    true,
	}

	if err := s.db.Create(instance).Error; err != nil {
		return nil, fmt.Errorf("failed to create tool instance: %w", err)
	}

	return instance, nil
}

// GetToolInstance retrieves a tool instance by ID
func (s *ToolService) GetToolInstance(id uint) (*database.ToolInstance, error) {
	var instance database.ToolInstance
	if err := s.db.Preload("ToolType").First(&instance, id).Error; err != nil {
		return nil, fmt.Errorf("failed to get tool instance: %w", err)
	}
	return &instance, nil
}

// UpdateToolInstance updates a tool instance
func (s *ToolService) UpdateToolInstance(id uint, name string, settings database.JSONB, enabled bool) error {
	// Get existing instance to preserve ssh_keys
	var existing database.ToolInstance
	if err := s.db.First(&existing, id).Error; err != nil {
		return fmt.Errorf("failed to find tool instance: %w", err)
	}

	// Always preserve ssh_keys from existing settings - they are managed via dedicated SSH key endpoints
	if settings != nil {
		if existingKeys, ok := existing.Settings["ssh_keys"]; ok {
			settings["ssh_keys"] = existingKeys
		} else {
			delete(settings, "ssh_keys")
		}
	}

	updates := map[string]interface{}{
		"name":     name,
		"settings": settings,
		"enabled":  enabled,
	}

	if err := s.db.Model(&database.ToolInstance{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fmt.Errorf("failed to update tool instance: %w", err)
	}

	return nil
}

// DeleteToolInstance deletes a tool instance
func (s *ToolService) DeleteToolInstance(id uint) error {
	if err := s.db.Delete(&database.ToolInstance{}, id).Error; err != nil {
		return fmt.Errorf("failed to delete tool instance: %w", err)
	}
	return nil
}

// ListToolTypes lists all tool types
func (s *ToolService) ListToolTypes() ([]database.ToolType, error) {
	var toolTypes []database.ToolType
	if err := s.db.Find(&toolTypes).Error; err != nil {
		return nil, fmt.Errorf("failed to list tool types: %w", err)
	}
	return toolTypes, nil
}

// ListToolInstances lists all tool instances
func (s *ToolService) ListToolInstances() ([]database.ToolInstance, error) {
	var instances []database.ToolInstance
	if err := s.db.Preload("ToolType").Find(&instances).Error; err != nil {
		return nil, fmt.Errorf("failed to list tool instances: %w", err)
	}
	return instances, nil
}

// EnsureToolTypes ensures the basic tool types exist in the database
func (s *ToolService) EnsureToolTypes() error {
	toolTypes := []database.ToolType{
		{Name: "ssh", Description: "SSH remote command execution tool"},
		{Name: "zabbix", Description: "Zabbix monitoring integration"},
	}

	for _, tt := range toolTypes {
		var existing database.ToolType
		result := s.db.Where("name = ?", tt.Name).First(&existing)
		if result.Error != nil {
			// Create if not exists
			if err := s.db.Create(&tt).Error; err != nil {
				return fmt.Errorf("failed to create tool type %s: %w", tt.Name, err)
			}
		}
	}

	return nil
}

// GetSSHKeys returns all SSH keys for a tool instance (without private key content)
func (s *ToolService) GetSSHKeys(toolInstanceID uint) ([]SSHKeyEntry, error) {
	instance, err := s.GetToolInstance(toolInstanceID)
	if err != nil {
		return nil, err
	}

	keys := s.extractSSHKeys(instance.Settings)
	result := make([]SSHKeyEntry, 0, len(keys))
	for _, key := range keys {
		result = append(result, SSHKeyEntry{
			ID:        key.ID,
			Name:      key.Name,
			IsDefault: key.IsDefault,
			CreatedAt: key.CreatedAt,
		})
	}

	return result, nil
}

// AddSSHKey adds a new SSH key to a tool instance
func (s *ToolService) AddSSHKey(toolInstanceID uint, name string, privateKey string, setAsDefault bool) (*SSHKeyEntry, error) {
	instance, err := s.GetToolInstance(toolInstanceID)
	if err != nil {
		return nil, err
	}

	// Validate that name is unique
	existingKeys := s.extractSSHKeys(instance.Settings)
	for _, key := range existingKeys {
		if key.Name == name {
			return nil, fmt.Errorf("SSH key with name '%s' already exists", name)
		}
	}

	// Create new key
	newKey := SSHKeyFull{
		ID:         uuid.New().String(),
		Name:       name,
		PrivateKey: privateKey,
		IsDefault:  setAsDefault || len(existingKeys) == 0, // First key is always default
		CreatedAt:  time.Now().Format(time.RFC3339),
	}

	// If setting as default, unset other defaults
	if newKey.IsDefault {
		for i := range existingKeys {
			existingKeys[i].IsDefault = false
		}
	}

	// Add new key to list
	existingKeys = append(existingKeys, newKey)

	// Update settings
	if instance.Settings == nil {
		instance.Settings = make(database.JSONB)
	}
	instance.Settings["ssh_keys"] = s.keysToInterface(existingKeys)

	if err := s.db.Model(&database.ToolInstance{}).Where("id = ?", toolInstanceID).Update("settings", instance.Settings).Error; err != nil {
		return nil, fmt.Errorf("failed to save SSH key: %w", err)
	}

	return &SSHKeyEntry{
		ID:        newKey.ID,
		Name:      newKey.Name,
		IsDefault: newKey.IsDefault,
		CreatedAt: newKey.CreatedAt,
	}, nil
}

// UpdateSSHKey updates an SSH key's name or default status
func (s *ToolService) UpdateSSHKey(toolInstanceID uint, keyID string, name *string, setAsDefault *bool) (*SSHKeyEntry, error) {
	instance, err := s.GetToolInstance(toolInstanceID)
	if err != nil {
		return nil, err
	}

	keys := s.extractSSHKeys(instance.Settings)
	var targetKey *SSHKeyFull
	var targetIndex int

	for i := range keys {
		if keys[i].ID == keyID {
			targetKey = &keys[i]
			targetIndex = i
			break
		}
	}

	if targetKey == nil {
		return nil, fmt.Errorf("SSH key with ID '%s' not found", keyID)
	}

	// Update name if provided
	if name != nil && *name != "" {
		// Check for name uniqueness
		for i, key := range keys {
			if i != targetIndex && key.Name == *name {
				return nil, fmt.Errorf("SSH key with name '%s' already exists", *name)
			}
		}
		targetKey.Name = *name
	}

	// Update default status if provided
	if setAsDefault != nil && *setAsDefault {
		for i := range keys {
			keys[i].IsDefault = (i == targetIndex)
		}
	}

	keys[targetIndex] = *targetKey

	// Update settings
	instance.Settings["ssh_keys"] = s.keysToInterface(keys)

	if err := s.db.Model(&database.ToolInstance{}).Where("id = ?", toolInstanceID).Update("settings", instance.Settings).Error; err != nil {
		return nil, fmt.Errorf("failed to update SSH key: %w", err)
	}

	return &SSHKeyEntry{
		ID:        targetKey.ID,
		Name:      targetKey.Name,
		IsDefault: targetKey.IsDefault,
		CreatedAt: targetKey.CreatedAt,
	}, nil
}

// DeleteSSHKey deletes an SSH key from a tool instance
func (s *ToolService) DeleteSSHKey(toolInstanceID uint, keyID string) error {
	instance, err := s.GetToolInstance(toolInstanceID)
	if err != nil {
		return err
	}

	keys := s.extractSSHKeys(instance.Settings)
	var targetIndex = -1
	var isDefault bool

	for i, key := range keys {
		if key.ID == keyID {
			targetIndex = i
			isDefault = key.IsDefault
			break
		}
	}

	if targetIndex == -1 {
		return fmt.Errorf("SSH key with ID '%s' not found", keyID)
	}

	// Check if key is in use by any host
	if hosts, ok := instance.Settings["ssh_hosts"].([]interface{}); ok {
		for _, hostData := range hosts {
			if hostMap, ok := hostData.(map[string]interface{}); ok {
				if hostKeyID, ok := hostMap["key_id"].(string); ok && hostKeyID == keyID {
					hostname := "unknown"
					if h, ok := hostMap["hostname"].(string); ok {
						hostname = h
					}
					return fmt.Errorf("cannot delete key: it is used by host '%s'", hostname)
				}
			}
		}
	}

	// Cannot delete the only key or the default key if it's the last one
	if len(keys) == 1 {
		return fmt.Errorf("cannot delete the only SSH key")
	}

	// Remove the key
	keys = append(keys[:targetIndex], keys[targetIndex+1:]...)

	// If we deleted the default key, make the first remaining key the default
	if isDefault && len(keys) > 0 {
		keys[0].IsDefault = true
	}

	// Update settings
	instance.Settings["ssh_keys"] = s.keysToInterface(keys)

	if err := s.db.Model(&database.ToolInstance{}).Where("id = ?", toolInstanceID).Update("settings", instance.Settings).Error; err != nil {
		return fmt.Errorf("failed to delete SSH key: %w", err)
	}

	return nil
}

// extractSSHKeys extracts SSH keys from tool instance settings
func (s *ToolService) extractSSHKeys(settings database.JSONB) []SSHKeyFull {
	var keys []SSHKeyFull

	keysData, ok := settings["ssh_keys"].([]interface{})
	if !ok {
		return keys
	}

	for _, keyData := range keysData {
		keyMap, ok := keyData.(map[string]interface{})
		if !ok {
			continue
		}

		key := SSHKeyFull{}
		if id, ok := keyMap["id"].(string); ok {
			key.ID = id
		}
		if name, ok := keyMap["name"].(string); ok {
			key.Name = name
		}
		if privateKey, ok := keyMap["private_key"].(string); ok {
			key.PrivateKey = privateKey
		}
		if isDefault, ok := keyMap["is_default"].(bool); ok {
			key.IsDefault = isDefault
		}
		if createdAt, ok := keyMap["created_at"].(string); ok {
			key.CreatedAt = createdAt
		}

		if key.ID != "" {
			keys = append(keys, key)
		}
	}

	return keys
}

// keysToInterface converts SSH keys to interface slice for JSONB storage
func (s *ToolService) keysToInterface(keys []SSHKeyFull) []interface{} {
	result := make([]interface{}, len(keys))
	for i, key := range keys {
		result[i] = map[string]interface{}{
			"id":          key.ID,
			"name":        key.Name,
			"private_key": key.PrivateKey,
			"is_default":  key.IsDefault,
			"created_at":  key.CreatedAt,
		}
	}
	return result
}
