package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/services"
)

// alertChannelErr carries an HTTP status + user-facing message produced when
// resolving a notification_channel_uuid; keeping the pair together avoids
// scattering status decisions across multiple call sites.
type alertChannelErr struct {
	status int
	msg    string
}

// resolveNotificationChannel looks up a Channel by its public UUID and returns
// its primary key for use in the alert source's notification_channel_id FK.
// Missing channel surfaces as 400 (operator-visible validation), missing
// channel service surfaces as 503 (server misconfiguration).
func (h *APIHandler) resolveNotificationChannel(uuidStr string) (*uint, *alertChannelErr) {
	if h.channelService == nil {
		return nil, &alertChannelErr{status: http.StatusServiceUnavailable, msg: "Channel service is not configured"}
	}
	ch, err := h.channelService.GetChannelByUUID(strings.TrimSpace(uuidStr))
	if err != nil {
		if errors.Is(err, services.ErrChannelNotFound) {
			return nil, &alertChannelErr{status: http.StatusBadRequest, msg: "notification_channel_uuid does not match any channel"}
		}
		return nil, &alertChannelErr{status: http.StatusInternalServerError, msg: "Failed to resolve notification channel"}
	}
	if !ch.CanPost {
		return nil, &alertChannelErr{status: http.StatusBadRequest, msg: "notification_channel_uuid points at a channel that cannot post (CanPost=false)"}
	}
	id := ch.ID
	return &id, nil
}

// isDuplicateNameErr reports whether err is a database unique-constraint
// violation on the alert source name. Both Postgres (GORM) and SQLite
// (used by tests) surface this via distinctive substrings; we match on the
// same set already used by api_tools.go / api_settings_llm.go so behavior is
// consistent across handlers.
func isDuplicateNameErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "UNIQUE constraint") ||
		strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "already exists")
}

// handleAlertSourceTypes handles GET /api/alert-source-types
func (h *APIHandler) handleAlertSourceTypes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	sourceTypes, err := h.alertService.ListSourceTypes()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to list source types")
		return
	}

	api.RespondJSON(w, http.StatusOK, sourceTypes)
}

// handleAlertSources handles GET /api/alert-sources and POST /api/alert-sources
func (h *APIHandler) handleAlertSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		instances, err := h.alertService.ListInstances()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to list alert sources")
			return
		}
		api.RespondJSON(w, http.StatusOK, instances)

	case http.MethodPost:
		var req api.CreateAlertSourceRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		req.SourceTypeName = strings.TrimSpace(req.SourceTypeName)
		req.Name = strings.TrimSpace(req.Name)

		if req.SourceTypeName == "" || req.Name == "" {
			api.RespondError(w, http.StatusBadRequest, "source_type_name and name are required")
			return
		}

		// Reject creation against deprecated source types. slack_channel is
		// deprecated as of Task 6 of the unified-channels plan — operators
		// should configure listener channels under /api/channels instead.
		// Missing rows (sterr or sourceType==nil) fall through to the
		// AlertService.CreateInstance call, which surfaces the error in its
		// own response shape; we only intercept on a definite deprecated row.
		if sourceType, sterr := h.alertService.GetAlertSourceTypeByName(req.SourceTypeName); sterr == nil && sourceType != nil && sourceType.Deprecated {
			api.RespondError(w, http.StatusBadRequest, "alert source type '"+req.SourceTypeName+"' is deprecated; configure a Channel under /api/channels instead")
			return
		}

		// Resolve optional notification_channel_uuid up-front so we can
		// reject unknown channel UUIDs without creating the alert source.
		var notifChannelID *uint
		if req.NotificationChannelUUID != nil && strings.TrimSpace(*req.NotificationChannelUUID) != "" {
			id, herr := h.resolveNotificationChannel(*req.NotificationChannelUUID)
			if herr != nil {
				api.RespondError(w, herr.status, herr.msg)
				return
			}
			notifChannelID = id
		}

		instance, err := h.alertService.CreateInstance(req.SourceTypeName, req.Name, req.Description, req.WebhookSecret, req.FieldMappings, req.Settings)
		if err != nil {
			if isDuplicateNameErr(err) {
				api.RespondError(w, http.StatusConflict, "An alert source with that name already exists")
				return
			}
			api.RespondError(w, http.StatusInternalServerError, "Failed to create alert source")
			return
		}

		if notifChannelID != nil {
			if err := h.alertService.UpdateInstance(instance.UUID, map[string]interface{}{
				"notification_channel_id": *notifChannelID,
			}); err != nil {
				api.RespondError(w, http.StatusInternalServerError, "Failed to set notification channel")
				return
			}
			if refreshed, gerr := h.alertService.GetInstanceByUUID(instance.UUID); gerr == nil {
				instance = refreshed
			}
		}

		api.RespondJSON(w, http.StatusCreated, instance)
		h.reloadAlertChannels()

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleAlertSourceByUUID handles GET/PUT/DELETE /api/alert-sources/{uuid}
func (h *APIHandler) handleAlertSourceByUUID(w http.ResponseWriter, r *http.Request) {
	uuid := r.URL.Path[len("/api/alert-sources/"):]
	if uuid == "" {
		api.RespondError(w, http.StatusBadRequest, "UUID is required")
		return
	}

	switch r.Method {
	case http.MethodGet:
		instance, err := h.alertService.GetInstanceByUUID(uuid)
		if err != nil {
			api.RespondError(w, http.StatusNotFound, "Alert source not found")
			return
		}
		api.RespondJSON(w, http.StatusOK, instance)

	case http.MethodPut:
		var req api.UpdateAlertSourceRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		updates := make(map[string]interface{})
		if req.Name != nil {
			trimmed := strings.TrimSpace(*req.Name)
			if trimmed == "" {
				api.RespondError(w, http.StatusBadRequest, "name cannot be empty")
				return
			}
			updates["name"] = trimmed
		}
		if req.Description != nil {
			updates["description"] = *req.Description
		}
		if req.WebhookSecret != nil {
			updates["webhook_secret"] = *req.WebhookSecret
		}
		if req.FieldMappings != nil {
			updates["field_mappings"] = *req.FieldMappings
		}
		if req.Settings != nil {
			updates["settings"] = *req.Settings
		}
		if req.Enabled != nil {
			updates["enabled"] = *req.Enabled
		}

		if req.Settings != nil {
			existing, err := h.alertService.GetInstanceByUUID(uuid)
			if err == nil && existing.AlertSourceType.Name == "slack_channel" {
				channelID, _ := (*req.Settings)["slack_channel_id"].(string)
				if strings.TrimSpace(channelID) == "" {
					api.RespondError(w, http.StatusBadRequest, "slack_channel_id is required in settings for slack_channel source type")
					return
				}
			}
		}

		// notification_channel_uuid is tri-state: omitted (nil pointer) leaves
		// the existing FK untouched; explicit empty string clears it; a valid
		// UUID resolves to a Channel and sets it.
		if req.NotificationChannelUUID != nil {
			trimmed := strings.TrimSpace(*req.NotificationChannelUUID)
			if trimmed == "" {
				updates["notification_channel_id"] = nil
			} else {
				id, herr := h.resolveNotificationChannel(trimmed)
				if herr != nil {
					api.RespondError(w, herr.status, herr.msg)
					return
				}
				updates["notification_channel_id"] = *id
			}
		}

		if err := h.alertService.UpdateInstance(uuid, updates); err != nil {
			if isDuplicateNameErr(err) {
				api.RespondError(w, http.StatusConflict, "An alert source with that name already exists")
				return
			}
			api.RespondError(w, http.StatusInternalServerError, "Failed to update alert source")
			return
		}

		instance, _ := h.alertService.GetInstanceByUUID(uuid)
		api.RespondJSON(w, http.StatusOK, instance)
		h.reloadAlertChannels()

	case http.MethodDelete:
		if err := h.alertService.DeleteInstance(uuid); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to delete alert source")
			return
		}
		api.RespondNoContent(w)
		h.reloadAlertChannels()

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
