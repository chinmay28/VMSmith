package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<html>shell</html>")},
		"sw.js":                {Data: []byte("/* sw */")},
		"manifest.webmanifest": {Data: []byte(`{"name":"VM Smith"}`)},
		"icons/icon-192.png":   {Data: []byte("\x89PNG")},
		"assets/index-abc.js":  {Data: []byte("console.log(1)")},
	}
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestNewHandler(t *testing.T) {
	h := NewHandler(testFS())

	tests := []struct {
		name        string
		target      string
		wantStatus  int
		wantBody    string
		wantCache   string
		wantType    string
		wantSWScope string
	}{
		{name: "root serves shell", target: "/", wantStatus: 200, wantBody: "<html>shell</html>", wantCache: cacheRevalidate, wantType: "text/html"},
		{name: "index.html serves shell without redirect", target: "/index.html", wantStatus: 200, wantBody: "<html>shell</html>", wantCache: cacheRevalidate},
		{name: "client route falls back to shell", target: "/vms/vm-1/console", wantStatus: 200, wantBody: "<html>shell</html>", wantCache: cacheRevalidate},
		{name: "hashed asset is immutable", target: "/assets/index-abc.js", wantStatus: 200, wantBody: "console.log(1)", wantCache: cacheImmutable, wantType: "javascript"},
		{name: "missing asset is 404 not shell", target: "/assets/index-old.js", wantStatus: 404, wantCache: cacheRevalidate},
		{name: "service worker revalidates and allows root scope", target: "/sw.js", wantStatus: 200, wantBody: "/* sw */", wantCache: cacheRevalidate, wantSWScope: "/"},
		{name: "manifest content type", target: "/manifest.webmanifest", wantStatus: 200, wantCache: cacheRevalidate, wantType: "application/manifest+json"},
		{name: "icon revalidates", target: "/icons/icon-192.png", wantStatus: 200, wantCache: cacheRevalidate},
		{name: "api paths are not served", target: "/api/v1/vms", wantStatus: 404},
		{name: "traversal is cleaned to shell", target: "/../../etc/passwd", wantStatus: 200, wantBody: "<html>shell</html>"},
		{name: "directory falls back to shell", target: "/icons", wantStatus: 200, wantBody: "<html>shell</html>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, h, tt.target)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantBody != "" {
				body, _ := io.ReadAll(rec.Body)
				if string(body) != tt.wantBody {
					t.Errorf("body = %q, want %q", body, tt.wantBody)
				}
			}
			if tt.wantCache != "" {
				if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
					t.Errorf("Cache-Control = %q, want %q", got, tt.wantCache)
				}
			}
			if tt.wantType != "" {
				if got := rec.Header().Get("Content-Type"); !strings.Contains(got, tt.wantType) {
					t.Errorf("Content-Type = %q, want it to contain %q", got, tt.wantType)
				}
			}
			if got := rec.Header().Get("Service-Worker-Allowed"); got != tt.wantSWScope {
				t.Errorf("Service-Worker-Allowed = %q, want %q", got, tt.wantSWScope)
			}
		})
	}
}
