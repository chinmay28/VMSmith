package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/*
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded SPA.
// It serves static files from dist/ and falls back to index.html
// for client-side routing (React Router).
func Handler() http.Handler {
	subFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("embedded dist/ not found: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(subFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't serve the SPA for API routes
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Try to serve static file first
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		// Check if the file exists in the embedded FS
		f, err := subFS.Open(strings.TrimPrefix(path, "/"))
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Fall back to index.html for SPA client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
