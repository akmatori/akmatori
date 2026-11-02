package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// cronJobResponse is the API-facing view of a CronJob row. It mirrors the
// model's exported JSON fields but routes the preloaded Channel through
// channelResponse so the embedded Integration.Credentials are masked before
// going on the wire — the model alone would echo bot_token / signing_secret /
// app_token in plaintext via the eager Preload("Channel.Integration") chain.
//
// ToolsEnabled + BuiltinTools are the cron's tool access (see the
// tool-authorization rule).
type cronJobResponse struct {
	ID            uint             `json:"id"`
	UUID          string           `json:"uuid"`
	Name          string           `json:"name"`
	Schedule      string           `json:"schedule"`
	Prompt        string           `json:"prompt"`
	IsSystem      bool             `json:"is_system"`
	ChannelID     *uint            `json:"channel_id"`
	Enabled       bool             `json:"enabled"`
	PostResults   bool             `json:"post_results"`
	LastRunAt     *time.Time       `json:"last_run_at,omitempty"`
	LastRunStatus string           `json:"last_run_status"`
	LastRunError  string           `json:"last_run_error"`
	NextRunAt     *time.Time       `json:"next_run_at,omitempty"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Channel       *channelResponse `json:"channel,omitempty"`
	ToolsEnabled  bool             `json:"tools_enabled"`
	BuiltinTools  []string         `json:"builtin_tools"`
}

func toCronJobResponse(row *database.CronJob) cronJobResponse {
	resp := cronJobResponse{
		ID:            row.ID,
		UUID:          row.UUID,
		Name:          row.Name,
		Schedule:      row.Schedule,
		Prompt:        row.Prompt,
		IsSystem:      row.IsSystem,
		ChannelID:     row.ChannelID,
		Enabled:       row.Enabled,
		PostResults:   row.PostResults,
		LastRunAt:     row.LastRunAt,
		LastRunStatus: row.LastRunStatus,
		LastRunError:  row.LastRunError,
		NextRunAt:     row.NextRunAt,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
		ToolsEnabled:  row.ToolsEnabled,
		BuiltinTools:  []string(row.BuiltinTools),
	}
	if resp.BuiltinTools == nil {
		resp.BuiltinTools = []string{}
	}
	if row.Channel != nil && row.Channel.ID != 0 {
		masked := toChannelResponse(row.Channel)
		resp.Channel = &masked
	}
	return resp
}

func toCronJobResponses(rows []database.CronJob) []cronJobResponse {
	out := make([]cronJobResponse, len(rows))
	for i := range rows {
		out[i] = toCronJobResponse(&rows[i])
	}
	return out
}

// CreateCronJobRequest is the request body for POST /api/cron-jobs. Channel is
// optional at the API layer — when omitted the service leaves ChannelID nil so
// the runner falls back to the workspace default at tick time.
//
// ToolsEnabled (default true) grants every enabled connector tool;
// BuiltinTools (default events, runbooks, memory) names the built-ins. The
// legacy tool_instance_ids field is refused as an unknown field (400).
type CreateCronJobRequest struct {
	Name         string    `json:"name"`
	Schedule     string    `json:"schedule"`
	Prompt       string    `json:"prompt"`
	ChannelUUID  string    `json:"channel_uuid,omitempty"`
	Enabled      *bool     `json:"enabled,omitempty"`
	PostResults  *bool     `json:"post_results,omitempty"`
	ToolsEnabled *bool     `json:"tools_enabled,omitempty"`
	BuiltinTools *[]string `json:"builtin_tools,omitempty"`
}

// UpdateCronJobRequest is the request body for PUT /api/cron-jobs/{uuid}.
// Pointer fields keep partial updates ergonomic; passing channel_uuid="" on a
// PUT explicitly clears the channel association.
//
// BuiltinTools is a pointer to a slice so the handler can distinguish
// "leave built-ins alone" (absent) from "no built-ins" (explicit []).
type UpdateCronJobRequest struct {
	Name         *string   `json:"name,omitempty"`
	Schedule     *string   `json:"schedule,omitempty"`
	Prompt       *string   `json:"prompt,omitempty"`
	ChannelUUID  *string   `json:"channel_uuid,omitempty"`
	Enabled      *bool     `json:"enabled,omitempty"`
	PostResults  *bool     `json:"post_results,omitempty"`
	ToolsEnabled *bool     `json:"tools_enabled,omitempty"`
	BuiltinTools *[]string `json:"builtin_tools,omitempty"`
}

// handleCronJobs dispatches GET /api/cron-jobs and POST /api/cron-jobs.
func (h *APIHandler) handleCronJobs(w http.ResponseWriter, r *http.Request) {
	if h.cronService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "Cron service is not configured")
		return
	}

	switch r.Method {
	case http.MethodGet:
		rows, err := h.cronService.ListJobs()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		api.RespondJSON(w, http.StatusOK, toCronJobResponses(rows))

	case http.MethodPost:
		var req CreateCronJobRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		postResults := true
		if req.PostResults != nil {
			postResults = *req.PostResults
		}
		tools := services.CronJobTools{ToolsEnabled: true}
		if req.ToolsEnabled != nil {
			tools.ToolsEnabled = *req.ToolsEnabled
		}
		if req.BuiltinTools != nil {
			tools.BuiltinTools = *req.BuiltinTools
			if tools.BuiltinTools == nil {
				tools.BuiltinTools = []string{}
			}
		}
		row, err := h.cronService.CreateJob(
			req.Name,
			req.Schedule,
			req.Prompt,
			req.ChannelUUID,
			enabled,
			postResults,
			tools,
		)
		if err != nil {
			api.RespondError(w, cronErrStatus(err), err.Error())
			return
		}
		api.RespondJSON(w, http.StatusCreated, toCronJobResponse(row))

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleCronJobByUUID dispatches GET/PUT/DELETE /api/cron-jobs/{uuid} and
// POST /api/cron-jobs/{uuid}/run. Splitting the run sub-route in here (rather
// than a dedicated mux handler) keeps the routes table in api.go terse.
func (h *APIHandler) handleCronJobByUUID(w http.ResponseWriter, r *http.Request) {
	if h.cronService == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "Cron service is not configured")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/cron-jobs/")
	uuid, sub, hasSub := strings.Cut(rest, "/")
	if uuid == "" {
		api.RespondError(w, http.StatusBadRequest, "Invalid cron job UUID")
		return
	}

	if hasSub {
		switch sub {
		case "run":
			if r.Method != http.MethodPost {
				api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
				return
			}
			if err := h.cronService.RunNow(uuid); err != nil {
				api.RespondError(w, cronErrStatus(err), err.Error())
				return
			}
			// 202: the tick was accepted and is running in the background.
			// Operators poll LastRunStatus / LastRunError on the row for the
			// outcome.
			api.RespondJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
		default:
			api.RespondError(w, http.StatusNotFound, "Not found")
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		row, err := h.cronService.GetJobByUUID(uuid)
		if err != nil {
			api.RespondError(w, cronErrStatus(err), err.Error())
			return
		}
		api.RespondJSON(w, http.StatusOK, toCronJobResponse(row))

	case http.MethodPut:
		var req UpdateCronJobRequest
		if err := api.DecodeJSON(r, &req); err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}
		patch := services.CronJobUpdate{
			Name:         req.Name,
			Schedule:     req.Schedule,
			Prompt:       req.Prompt,
			ChannelUUID:  req.ChannelUUID,
			Enabled:      req.Enabled,
			PostResults:  req.PostResults,
			ToolsEnabled: req.ToolsEnabled,
			BuiltinTools: req.BuiltinTools,
		}
		row, err := h.cronService.UpdateJob(uuid, patch)
		if err != nil {
			api.RespondError(w, cronErrStatus(err), err.Error())
			return
		}
		api.RespondJSON(w, http.StatusOK, toCronJobResponse(row))

	case http.MethodDelete:
		if err := h.cronService.DeleteJob(uuid); err != nil {
			api.RespondError(w, cronErrStatus(err), err.Error())
			return
		}
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// cronErrStatus translates service-layer errors into HTTP status codes. Bad
// schedules and validation failures become 400, missing rows become 404, and
// everything else surfaces as 500 so unexpected failures stand out in logs.
func cronErrStatus(err error) int {
	switch {
	case errors.Is(err, services.ErrCronJobNotFound):
		return http.StatusNotFound
	case errors.Is(err, services.ErrSystemCronImmutable):
		// 409 Conflict: the row exists and is well-formed, but its IsSystem
		// flag forbids deletion. Operators can disable system rows via PUT
		// instead; the UI surfaces this with a "system" pill + disabled
		// delete button.
		return http.StatusConflict
	case errors.Is(err, services.ErrChannelNotFound),
		errors.Is(err, services.ErrIntegrationNotFound):
		return http.StatusBadRequest
	case errors.Is(err, services.ErrInvalidCronSchedule),
		errors.Is(err, services.ErrChannelNotPostable),
		errors.Is(err, services.ErrUnknownBuiltinTool):
		return http.StatusBadRequest
	default:
		// Duplicate cron job name violates the uniqueIndex on cron_jobs.name —
		// translate to 409 so the UI can surface a clean validation message
		// rather than rendering a server-error banner.
		if isDuplicateNameErr(err) {
			return http.StatusConflict
		}
		if isCronClientError(err) {
			return http.StatusBadRequest
		}
		return http.StatusInternalServerError
	}
}

// isCronClientError reports whether the error message looks like a
// validation failure rather than an unexpected backend issue. The CronRunner
// returns plain-text messages for validation problems (e.g. "cron job name
// cannot be empty") and wraps DB errors with explicit "create cron job: ..."
// / "update cron job: ..." prefixes.
func isCronClientError(err error) bool {
	msg := err.Error()
	prefixes := []string{
		"create cron job: ",
		"update cron job: ",
		"delete cron job",
		"list cron jobs: ",
		"get cron job ",
		"load cron job ",
		"load LLM settings: ",
		"reload cron job ",
		"apply enabled=false on create: ",
		"apply post_results=false on create: ",
		"apply tools_enabled=false on create: ",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(msg, p) {
			return false
		}
	}
	return true
}
