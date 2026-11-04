package services

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/akmatori/akmatori/internal/database"
	"gorm.io/gorm"
)

// HTTP connectors get their credentials from a tool instance whose tool type
// name equals the connector's tool_type_name (the MCP gateway calls
// ResolveToolCredentials with that name). The helpers below keep a matching
// tool_types row in step with the connector so operators can create such an
// instance from the Tools page and assign it to skills.

// connectorSettingsSchema builds the settings schema (same shape as the MCP
// gateway's settings_schema) for a connector's tool type: the base URL field
// plus the credential fields its auth method reads.
func connectorSettingsSchema(c *database.HTTPConnector) database.JSONB {
	properties := map[string]interface{}{
		c.BaseURLField: map[string]interface{}{
			"type":        "string",
			"format":      "uri",
			"description": "Base URL of the " + c.ToolTypeName + " API",
			"example":     "https://api.example.com",
		},
	}
	required := []interface{}{c.BaseURLField}

	if auth, err := c.GetAuthConfig(); err == nil && auth != nil {
		tokenField := auth.TokenField
		if tokenField == "" {
			tokenField = "token" // gateway default (httpconnector.injectAuth)
		}
		switch auth.Method {
		case database.HTTPConnectorAuthBearer:
			properties[tokenField] = map[string]interface{}{
				"type": "string", "secret": true, "description": "Bearer token",
			}
			required = append(required, tokenField)
		case database.HTTPConnectorAuthAPIKey:
			header := auth.HeaderName
			if header == "" {
				header = "X-API-Key"
			}
			properties[tokenField] = map[string]interface{}{
				"type": "string", "secret": true, "description": "API key (sent in the " + header + " header)",
			}
			required = append(required, tokenField)
		case database.HTTPConnectorAuthBasic:
			properties["username"] = map[string]interface{}{"type": "string", "description": "Username"}
			properties["password"] = map[string]interface{}{"type": "string", "secret": true, "description": "Password"}
			required = append(required, "username")
		}
	}

	return database.JSONB{
		"type":       "object",
		"required":   required,
		"properties": properties,
	}
}

// connectorToolTypeDescription is the tool type description for a connector.
func connectorToolTypeDescription(c *database.HTTPConnector) string {
	if c.Description != "" {
		return c.Description
	}
	return "HTTP connector " + c.ToolTypeName
}

// ensureConnectorToolType creates or refreshes the tool_types row for a
// connector. When previousName is set and differs (a rename), the old row is
// renamed so existing tool instances follow the connector; if a row with the
// new name already exists, the old row is dropped when no instance uses it.
func ensureConnectorToolType(tx *gorm.DB, c *database.HTTPConnector, previousName string) error {
	fields := map[string]interface{}{
		"description": connectorToolTypeDescription(c),
		"schema":      connectorSettingsSchema(c),
	}

	var current database.ToolType
	err := tx.Where("name = ?", c.ToolTypeName).First(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to look up tool type %q: %w", c.ToolTypeName, err)
	}
	currentExists := err == nil

	if previousName != "" && previousName != c.ToolTypeName {
		var old database.ToolType
		oldErr := tx.Where("name = ?", previousName).First(&old).Error
		if oldErr != nil && !errors.Is(oldErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("failed to look up tool type %q: %w", previousName, oldErr)
		}
		if oldErr == nil {
			if !currentExists {
				fields["name"] = c.ToolTypeName
				if err := tx.Model(&database.ToolType{}).Where("id = ?", old.ID).Updates(fields).Error; err != nil {
					return fmt.Errorf("failed to rename tool type %q to %q: %w", previousName, c.ToolTypeName, err)
				}
				return nil
			}
			if err := deleteToolTypeIfUnused(tx, previousName); err != nil {
				return err
			}
		}
	}

	if currentExists {
		if err := tx.Model(&database.ToolType{}).Where("id = ?", current.ID).Updates(fields).Error; err != nil {
			return fmt.Errorf("failed to update tool type %q: %w", c.ToolTypeName, err)
		}
		return nil
	}

	tt := database.ToolType{
		Name:        c.ToolTypeName,
		Description: fields["description"].(string),
		Schema:      fields["schema"].(database.JSONB),
	}
	if err := tx.Create(&tt).Error; err != nil {
		return fmt.Errorf("failed to create tool type %q: %w", c.ToolTypeName, err)
	}
	return nil
}

// deleteToolTypeIfUnused removes the named tool type unless a tool instance
// still references it. Missing rows are not an error.
func deleteToolTypeIfUnused(tx *gorm.DB, name string) error {
	var tt database.ToolType
	if err := tx.Where("name = ?", name).First(&tt).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return fmt.Errorf("failed to look up tool type %q: %w", name, err)
	}
	var inUse int64
	if err := tx.Model(&database.ToolInstance{}).Where("tool_type_id = ?", tt.ID).Count(&inUse).Error; err != nil {
		return fmt.Errorf("failed to count instances of tool type %q: %w", name, err)
	}
	if inUse > 0 {
		slog.Info("keeping tool type of deleted HTTP connector: tool instances still use it",
			"tool_type", name, "instances", inUse)
		return nil
	}
	if err := tx.Delete(&database.ToolType{}, tt.ID).Error; err != nil {
		return fmt.Errorf("failed to delete tool type %q: %w", name, err)
	}
	return nil
}

// EnsureConnectorToolTypes backfills tool types for every HTTP connector.
// Connectors created before tool types were synced have none, so their
// tools could never resolve credentials. Safe to run on every startup.
func (s *HTTPConnectorService) EnsureConnectorToolTypes() error {
	var connectors []database.HTTPConnector
	if err := s.db.Find(&connectors).Error; err != nil {
		return fmt.Errorf("failed to list HTTP connectors: %w", err)
	}
	for i := range connectors {
		c := &connectors[i]
		if isReservedToolNamespace(c.ToolTypeName) {
			continue
		}
		if err := ensureConnectorToolType(s.db, c, ""); err != nil {
			return err
		}
	}
	return nil
}
