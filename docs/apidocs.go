package apidocs

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

//go:embed openapi.yaml swagger-ui/*
var assetsFS embed.FS

var docsHTML = []byte(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>VMSmith API Docs</title>
    <link rel="stylesheet" href="/api/docs/swagger-ui.css" />
    <style>
      html { box-sizing: border-box; overflow-y: scroll; }
      *, *:before, *:after { box-sizing: inherit; }
      body { margin: 0; background: #10141b; }
    </style>
  </head>
  <body>
    <div id="swagger-ui"></div>
    <script src="/api/docs/swagger-ui-bundle.js"></script>
    <script>
      window.onload = function () {
        window.ui = SwaggerUIBundle({
          url: '/api/openapi.yaml',
          dom_id: '#swagger-ui',
          deepLinking: true,
          presets: [SwaggerUIBundle.presets.apis],
        });
      };
    </script>
  </body>
</html>`)

func SpecHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		http.ServeFileFS(w, r, assetsFS, "openapi.yaml")
	})
}

func UIHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(docsHTML)
	})
}

func AssetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		name = path.Clean(name)
		if name == "." || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, assetsFS, path.Join("swagger-ui", name))
	})
}
