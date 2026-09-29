package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed dist/*
var distFS embed.FS

// Cache-Control policies for the embedded GUI (see docs/PWA.md).
const (
	// cacheRevalidate is used for everything whose URL is stable across
	// releases (index.html, sw.js, manifest, icons). Browsers must revalidate
	// so a daemon upgrade is picked up on the next load; for sw.js this is
	// what lets the service worker's update check see a new build.
	cacheRevalidate = "no-cache"
	// cacheImmutable is used for Vite's content-hashed /assets/ output: a
	// new build is a new URL, so these can be cached forever.
	cacheImmutable = "public, max-age=31536000, immutable"
)

// Handler returns an http.Handler that serves the embedded SPA.
func Handler() http.Handler {
	subFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("embedded dist/ not found: " + err.Error())
	}
	return NewHandler(subFS)
}

// NewHandler serves a built SPA from fsys. It serves static files, falls
// back to index.html for client-side routes (React Router), and sets the
// caching / content-type headers the PWA service worker relies on:
//
//   - /api/* is never served here (404).
//   - /assets/* that do not exist return 404 rather than index.html, so a
//     stale chunk request can never be cached as HTML under a .js URL.
//   - /sw.js carries Service-Worker-Allowed: / and must be revalidated.
//   - *.webmanifest is served as application/manifest+json.
func NewHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		if !fileExists(fsys, name) {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", cacheRevalidate)
				http.NotFound(w, r)
				return
			}
			// Client-side route: serve the app shell.
			name = "index.html"
		}

		setStaticHeaders(w.Header(), name)
		if name == "index.html" {
			// http.FileServer redirects /index.html to ./, so always hand it
			// the directory root for the shell.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func fileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// setStaticHeaders applies the per-file caching and content-type policy.
// name is the fs-relative path being served.
func setStaticHeaders(h http.Header, name string) {
	switch {
	case strings.HasPrefix(name, "assets/"):
		h.Set("Cache-Control", cacheImmutable)
	case name == "sw.js":
		h.Set("Cache-Control", cacheRevalidate)
		h.Set("Service-Worker-Allowed", "/")
	default:
		h.Set("Cache-Control", cacheRevalidate)
	}
	// Go's mime table does not know .webmanifest; browsers accept it either
	// way, but Lighthouse and some installers check the type.
	if strings.HasSuffix(name, ".webmanifest") {
		h.Set("Content-Type", "application/manifest+json")
	}
}
