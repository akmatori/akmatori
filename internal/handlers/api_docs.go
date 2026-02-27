package handlers

import (
	"net/http"

	"github.com/akmatori/akmatori/docs"
)

// handleOpenAPISpec serves the embedded OpenAPI specification file.
func (h *APIHandler) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	if _, err := w.Write(docs.OpenAPISpec); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
	}
}

// handleDocs serves the Swagger UI HTML page.
func (h *APIHandler) handleDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write([]byte(swaggerUIHTML)); err != nil {
		http.Error(w, "Failed to write response", http.StatusInternalServerError)
	}
}

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Akmatori API Docs</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
  <style>body { margin: 0; }</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: "/api/openapi.yaml",
      dom_id: "#swagger-ui",
      presets: [SwaggerUIBundle.presets.apis],
      layout: "BaseLayout"
    });
  </script>
</body>
</html>`
