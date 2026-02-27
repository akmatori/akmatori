package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/akmatori/akmatori/internal/api"
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
		api.RespondJSON(w, http.StatusOK, files)

	case http.MethodPost:
		if err := r.ParseMultipartForm(services.MaxFileSize); err != nil {
			api.RespondError(w, http.StatusBadRequest, "Failed to parse form")
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			api.RespondError(w, http.StatusBadRequest, "Failed to get file")
			return
		}
		defer file.Close()

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

		api.RespondJSON(w, http.StatusCreated, contextFile)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleContextByID handles GET /api/context/:id, GET /api/context/:id/download, DELETE /api/context/:id
func (h *APIHandler) handleContextByID(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path[len("/api/context/"):]
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
		api.RespondJSON(w, http.StatusOK, file)

	case http.MethodDelete:
		if err := h.contextService.DeleteFile(uint(id)); err != nil {
			api.RespondError(w, http.StatusInternalServerError, "Failed to delete file")
			return
		}
		api.RespondNoContent(w)

	default:
		api.RespondError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
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
