package handlers

import (
	"net/http"
	"strings"

	"github.com/akmatori/akmatori/internal/api"
)

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

		if req.SourceTypeName == "" || req.Name == "" {
			api.RespondError(w, http.StatusBadRequest, "source_type_name and name are required")
			return
		}

		if req.SourceTypeName == "slack_channel" {
			channelID, _ := req.Settings["slack_channel_id"].(string)
			if strings.TrimSpace(channelID) == "" {
				api.RespondError(w, http.StatusBadRequest, "slack_channel_id is required in settings for slack_channel source type")
				return
			}
		}

		instance, err := h.alertService.CreateInstance(req.SourceTypeName, req.Name, req.Description, req.WebhookSecret, req.FieldMappings, req.Settings)
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to create alert source")
			return
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
			updates["name"] = *req.Name
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

		if err := h.alertService.UpdateInstance(uuid, updates); err != nil {
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
