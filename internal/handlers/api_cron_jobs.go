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
type cronJobResponse struct {
	ID            uint                 `json:"id"`
	UUID          string               `json:"uuid"`
	Name          string               `json:"name"`
	Description   string               `json:"description"`
	Schedule      string               `json:"schedule"`
	Prompt        string               `json:"prompt"`
	Mode          database.CronJobMode `json:"mode"`
	ChannelID     *uint                `json:"channel_id"`
	Enabled       bool                 `json:"enabled"`
	LastRunAt     *time.Time           `json:"last_run_at,omitempty"`
	LastRunStatus string               `json:"last_run_status"`
	LastRunError  string               `json:"last_run_error"`
	NextRunAt     *time.Time           `json:"next_run_at,omitempty"`
	CreatedAt     time.Time            `json:"created_at"`
	UpdatedAt     time.Time            `json:"updated_at"`
	Channel       *channelResponse     `json:"channel,omitempty"`
}

func toCronJobResponse(row *database.CronJob) cronJobResponse {
	resp := cronJobResponse{
		ID:            row.ID,
		UUID:          row.UUID,
		Name:          row.Name,
		Description:   row.Description,
		Schedule:      row.Schedule,
		Prompt:        row.Prompt,
		Mode:          row.Mode,
		ChannelID:     row.ChannelID,
		Enabled:       row.Enabled,
		LastRunAt:     row.LastRunAt,
		LastRunStatus: row.LastRunStatus,
		LastRunError:  row.LastRunError,
		NextRunAt:     row.NextRunAt,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
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

// CreateCronJobRequest is the request body for POST /api/cron-jobs. Mode and
// channel are optional at the API layer — the service defaults Mode to
// oneshot and leaves ChannelID nil so the runner falls back to the workspace
// default at tick time.
type CreateCronJobRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Schedule    string `json:"schedule"`
	Prompt      string `json:"prompt"`
	Mode        string `json:"mode,omitempty"`
	ChannelUUID string `json:"channel_uuid,omitempty"`
	Enabled     *bool  `json:"enabled,omitempty"`
}

// UpdateCronJobRequest is the request body for PUT /api/cron-jobs/{uuid}.
// Pointer fields keep partial updates ergonomic; passing channel_uuid="" on a
// PUT explicitly clears the channel association.
type UpdateCronJobRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Schedule    *string `json:"schedule,omitempty"`
	Prompt      *string `json:"prompt,omitempty"`
	Mode        *string `json:"mode,omitempty"`
	ChannelUUID *string `json:"channel_uuid,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
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
		row, err := h.cronService.CreateJob(
			req.Name,
			req.Description,
			req.Schedule,
			req.Prompt,
			database.CronJobMode(req.Mode),
			req.ChannelUUID,
			enabled,
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
			Name:        req.Name,
			Description: req.Description,
			Schedule:    req.Schedule,
			Prompt:      req.Prompt,
			ChannelUUID: req.ChannelUUID,
			Enabled:     req.Enabled,
		}
		if req.Mode != nil {
			mode := database.CronJobMode(*req.Mode)
			patch.Mode = &mode
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
	case errors.Is(err, services.ErrChannelNotFound),
		errors.Is(err, services.ErrIntegrationNotFound):
		return http.StatusBadRequest
	case errors.Is(err, services.ErrInvalidCronSchedule),
		errors.Is(err, services.ErrChannelNotPostable):
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
	}
	for _, p := range prefixes {
		if strings.HasPrefix(msg, p) {
			return false
		}
	}
	return true
}
