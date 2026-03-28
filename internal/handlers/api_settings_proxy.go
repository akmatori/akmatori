package handlers

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
)

// handleProxySettings handles GET /api/settings/proxy and PUT /api/settings/proxy
func (h *APIHandler) handleProxySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.GetProxySettings(w, r)
	case http.MethodPut:
		h.UpdateProxySettings(w, r)
	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// GetProxySettings returns the current proxy configuration
func (h *APIHandler) GetProxySettings(w http.ResponseWriter, r *http.Request) {
	settings, err := database.GetOrCreateProxySettings()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to get proxy settings")
		return
	}

	maskedURL := maskProxyURL(settings.ProxyURL)

	response := map[string]interface{}{
		"proxy_url": maskedURL,
		"no_proxy":  settings.NoProxy,
		"services": map[string]interface{}{
			"llm": map[string]interface{}{
				"enabled":   settings.LLMEnabled,
				"supported": true,
			},
			"slack": map[string]interface{}{
				"enabled":   settings.SlackEnabled,
				"supported": true,
			},
			"zabbix": map[string]interface{}{
				"enabled":   settings.ZabbixEnabled,
				"supported": true,
			},
			"victoria_metrics": map[string]interface{}{
				"enabled":   settings.VictoriaMetricsEnabled,
				"supported": true,
			},
			"catchpoint": map[string]interface{}{
				"enabled":   settings.CatchpointEnabled,
				"supported": true,
			},
			"grafana": map[string]interface{}{
				"enabled":   settings.GrafanaEnabled,
				"supported": true,
			},
			"pagerduty": map[string]interface{}{
				"enabled":   settings.PagerDutyEnabled,
				"supported": true,
			},
			"ssh": map[string]interface{}{
				"enabled":   false,
				"supported": false,
			},
		},
	}

	api.RespondJSON(w, http.StatusOK, response)
}

// UpdateProxySettings updates proxy configuration
func (h *APIHandler) UpdateProxySettings(w http.ResponseWriter, r *http.Request) {
	var input api.UpdateProxySettingsRequest
	if err := api.DecodeJSON(r, &input); err != nil {
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if input.ProxyURL != "" && !isValidURL(input.ProxyURL) {
		api.RespondError(w, http.StatusBadRequest, "Invalid proxy URL format")
		return
	}

	settings, err := database.GetOrCreateProxySettings()
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to get proxy settings")
		return
	}

	settings.ProxyURL = input.ProxyURL
	settings.NoProxy = input.NoProxy
	settings.LLMEnabled = input.Services.LLM.Enabled
	settings.SlackEnabled = input.Services.Slack.Enabled
	settings.ZabbixEnabled = input.Services.Zabbix.Enabled
	settings.VictoriaMetricsEnabled = input.Services.VictoriaMetrics.Enabled
	settings.CatchpointEnabled = input.Services.Catchpoint.Enabled
	settings.GrafanaEnabled = input.Services.Grafana.Enabled
	settings.PagerDutyEnabled = input.Services.PagerDuty.Enabled

	if err := database.UpdateProxySettings(settings); err != nil {
		api.RespondError(w, http.StatusInternalServerError, "Failed to update proxy settings")
		return
	}

	if h.agentWSHandler != nil && h.agentWSHandler.IsWorkerConnected() {
		if err := h.agentWSHandler.BroadcastProxyConfig(settings); err != nil {
			slog.Warn("failed to broadcast proxy config to agent worker", "err", err)
		}
	}

	h.GetProxySettings(w, r)
}

// maskProxyURL masks the password in a proxy URL if present
func maskProxyURL(proxyURL string) string {
	if proxyURL == "" {
		return ""
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return proxyURL
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(parsed.User.Username(), "****")
		}
	}
	return parsed.String()
}

// isValidURL validates that a string is a valid HTTP or HTTPS URL
func isValidURL(rawURL string) bool {
	if rawURL == "" {
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}
