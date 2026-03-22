package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/vm"
)

// Server holds the API dependencies and serves HTTP.
type Server struct {
	router     chi.Router
	vmManager  vm.Manager
	storageMgr *storage.Manager
	portFwd    *network.PortForwarder
}

// NewServer creates a new API server with an optional static file handler.
// Pass nil for webHandler to serve API-only (e.g., in tests).
func NewServer(vmMgr vm.Manager, storageMgr *storage.Manager, portFwd *network.PortForwarder) *Server {
	return NewServerWithWeb(vmMgr, storageMgr, portFwd, nil)
}

// NewServerWithWeb creates a new API server with an embedded web GUI handler.
func NewServerWithWeb(vmMgr vm.Manager, storageMgr *storage.Manager, portFwd *network.PortForwarder, webHandler http.Handler) *Server {
	s := &Server{
		vmManager:  vmMgr,
		storageMgr: storageMgr,
		portFwd:    portFwd,
	}
	s.setupRoutes(webHandler)
	return s
}

func (s *Server) setupRoutes(webHandler http.Handler) {
	r := chi.NewRouter()

	// Middleware
	r.Use(requestLogger) // structured request/response logging
	r.Use(middleware.Recoverer)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.SetHeader("Content-Type", "application/json"))

		// Log viewer endpoint
		r.Get("/logs", s.GetLogs)

		// VM endpoints
		r.Route("/vms", func(r chi.Router) {
			r.Post("/", s.CreateVM)
			r.Get("/", s.ListVMs)
			r.Route("/{vmID}", func(r chi.Router) {
				r.Get("/", s.GetVM)
				r.Patch("/", s.UpdateVM)
				r.Delete("/", s.DeleteVM)
				r.Post("/start", s.StartVM)
				r.Post("/stop", s.StopVM)

				// Snapshots
				r.Route("/snapshots", func(r chi.Router) {
					r.Post("/", s.CreateSnapshot)
					r.Get("/", s.ListSnapshots)
					r.Post("/{snapName}/restore", s.RestoreSnapshot)
					r.Delete("/{snapName}", s.DeleteSnapshot)
				})

				// Port forwards
				r.Route("/ports", func(r chi.Router) {
					r.Get("/", s.ListPorts)
					r.Post("/", s.AddPort)
					r.Delete("/{portID}", s.RemovePort)
				})
			})
		})

		// Image endpoints
		r.Route("/images", func(r chi.Router) {
			r.Get("/", s.ListImages)
			r.Post("/", s.CreateImage)
			r.Post("/upload", s.UploadImage)
			r.Delete("/{imageID}", s.DeleteImage)
			r.Get("/{imageID}/download", s.DownloadImage)
		})

		// Host network discovery
		r.Get("/host/interfaces", s.ListHostInterfaces)
	})

	// Serve embedded Web GUI if handler provided
	if webHandler != nil {
		r.Handle("/*", webHandler)
	}

	s.router = r
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}
