package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/akmatori/akmatori/internal/api"
	"github.com/akmatori/akmatori/internal/database"
	"github.com/akmatori/akmatori/internal/services"
)

// handleContext handles GET /api/context and POST /api/context
func (h *APIHandler) handleContext(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		files, err := h.contextService.ListFiles()
		if err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to list files")
			return
		}
		h.attachSummaryMemoryIDs(files)
		api.RespondJSON(w, http.StatusOK, files)

	case http.MethodPost:
		// ParseMultipartForm's argument is only the in-memory part; the
		// rest spills to temp files. MaxBytesReader is the real body cap.
		r.Body = http.MaxBytesReader(w, r.Body, services.MaxFileSize+contextUploadFormOverhead)
		if err := r.ParseMultipartForm(services.MaxFileSize); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				api.RespondError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("File too large (max %d MB)", services.MaxFileSize>>20))
				return
			}
			api.RespondError(w, http.StatusBadRequest, "Failed to parse form")
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "Failed to get file")
			return
		}
		defer func() {
			if err := file.Close(); err != nil {
				slog.Warn("failed to close uploaded context file", "err", err)
			}
		}()

		filename := r.FormValue("filename")
		if filename == "" {
			api.RespondError(w, http.StatusBadRequest, "Filename is required")
			return
		}

		description := r.FormValue("description")

		mimeType := header.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "text/plain"
		}

		contextFile, err := h.contextService.SaveFile(filename, header.Filename, mimeType, description, header.Size, file)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}

		if h.contextSummaries != nil {
			h.contextSummaries.Enqueue(contextFile.ID)
			contextFile.SummaryStatus = database.ContextSummaryPending
		}
		api.RespondJSON(w, http.StatusCreated, contextFile)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// contextUploadFormOverhead is the room above MaxFileSize for the multipart
// boundaries and the filename and description fields.
const contextUploadFormOverhead = 1 << 20

// attachSummaryMemoryIDs fills SummaryMemoryID for the file list. A failure
// only hides the links.
func (h *APIHandler) attachSummaryMemoryIDs(files []database.ContextFile) {
	if h.contextSummaries == nil || len(files) == 0 {
		return
	}
	ids := make([]uint, len(files))
	for i := range files {
		ids[i] = files[i].ID
	}
	byFile, err := h.contextSummaries.SummaryMemoryIDs(ids)
	if err != nil {
		slog.Warn("failed to look up context file summaries", "err", err)
		return
	}
	for i := range files {
		if id, ok := byFile[files[i].ID]; ok {
			files[i].SummaryMemoryID = &id
		}
	}
}

// handleContextByID handles GET /api/context/:id, GET /api/context/:id/download,
// POST /api/context/:id/summarize and DELETE /api/context/:id
func (h *APIHandler) handleContextByID(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/context/"):]
	if strings.HasSuffix(path, "/summarize") {
		id, err := strconv.ParseUint(strings.TrimSuffix(path, "/summarize"), 10, 32)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid file ID")
			return
		}
		h.handleContextSummarize(w, r, uint(id))
		return
	}
	if strings.HasSuffix(path, "/download") {
		idStr := strings.TrimSuffix(path, "/download")
		id, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "Invalid file ID")
			return
		}
		h.handleContextDownload(w, r, uint(id))
		return
	}

	id, err := strconv.ParseUint(path, 10, 32)
	if err != nil {
		api.RespondError(w, http.StatusBadRequest, "Invalid file ID")
		return
	}

	switch r.Method {
	case http.MethodGet:
		file, err := h.contextService.GetFile(uint(id))
		if err != nil {
			api.RespondError(w, http.StatusNotFound, "File not found")
			return
		}
		files := []database.ContextFile{*file}
		h.attachSummaryMemoryIDs(files)
		api.RespondJSON(w, http.StatusOK, files[0])

	case http.MethodDelete:
		if err := h.contextService.DeleteFile(uint(id)); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to delete file")
			return
		}
		if h.contextSummaries != nil {
			if err := h.contextSummaries.Forget(uint(id)); err != nil {
				slog.Warn("failed to delete context file memories", "file_id", id, "err", err)
			}
		}
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleContextSummarize handles POST /api/context/:id/summarize: it runs
// the summary again (after a failure, or for a file uploaded before
// summaries existed).
func (h *APIHandler) handleContextSummarize(w http.ResponseWriter, r *http.Request, id uint) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if h.contextSummaries == nil {
		api.RespondError(w, http.StatusServiceUnavailable, "File summaries are not available")
		return
	}
	file, err := h.contextService.GetFile(id)
	if err != nil {
		api.RespondError(w, http.StatusNotFound, "File not found")
		return
	}
	h.contextSummaries.Enqueue(file.ID)
	file.SummaryStatus = database.ContextSummaryPending
	file.SummaryError = ""
	api.RespondJSON(w, http.StatusAccepted, file)
}

// handleContextDownload handles GET /api/context/:id/download
func (h *APIHandler) handleContextDownload(w http.ResponseWriter, r *http.Request, id uint) {
	if r.Method != http.MethodGet {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	file, err := h.contextService.GetFile(id)
	if err != nil {
		api.RespondError(w, http.StatusNotFound, "File not found")
		return
	}

	filePath := h.contextService.GetFilePath(file.Filename)

	w.Header().Set("Content-Type", file.MimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", file.Filename))

	http.ServeFile(w, r, filePath)
}

// handleContextValidate handles POST /api/context/validate
func (h *APIHandler) handleContextValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req api.ValidateReferencesRequest
	if err := api.DecodeJSON(r, &req); err != nil {
		api.RespondError(w, http.StatusBadRequest, err.Error())
		return
	}

	references := h.contextService.ParseReferences(req.Text)
	valid, missing, found := h.contextService.ValidateReferences(req.Text)

	response := map[string]interface{}{
		"valid":      valid,
		"references": references,
		"found":      found,
		"missing":    missing,
	}

	api.RespondJSON(w, http.StatusOK, response)
}
