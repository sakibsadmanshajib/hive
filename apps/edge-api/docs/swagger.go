package docs

import (
	"net/http"
	"os"
	"strings"
)

// SwaggerHandler returns an http.Handler that serves:
//   - /docs/ -> Swagger UI HTML page (loads from CDN)
//   - /docs/openapi.yaml -> the OpenAPI spec file from disk
func SwaggerHandler(specPath string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/docs/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(specPath)
		if err != nil {
			http.Error(w, "spec file not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.Write(data)
	})

	mux.HandleFunc("/docs/", func(w http.ResponseWriter, r *http.Request) {
		// Only serve the index at /docs/ exactly (or /docs/index.html)
		if r.URL.Path != "/docs/" && !strings.HasSuffix(r.URL.Path, "/index.html") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(swaggerHTML))
	})

	return mux
}

const swaggerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>Hive API - Swagger UI</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    SwaggerUIBundle({
      url: './openapi.yaml',
      dom_id: '#swagger-ui',
      presets: [
        SwaggerUIBundle.presets.apis,
        SwaggerUIBundle.SwaggerUIStandalonePreset
      ],
      layout: 'BaseLayout'
    });
  </script>
</body>
</html>`
