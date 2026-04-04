package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/vm"
)

// Server holds the API dependencies and serves HTTP.
type Server struct {
	router               chi.Router
	vmManager            vm.Manager
	storageMgr           *storage.Manager
	portFwd              *network.PortForwarder
	maxRequestBodyBytes  int64
	maxUploadBodyBytes   int64
	maxConcurrentCreates int
	authConfig           config.AuthConfig
	createTokens         chan struct{}
	rateLimiter          *ipRateLimiter
}

type ipRateLimiter struct {
	mu      sync.Mutex
	clients map[string]*tokenBucket
	rate    float64
	burst   int
	now     func() time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// NewServer creates a new API server with default body-size limits.
func NewServer(vmMgr vm.Manager, storageMgr *storage.Manager, portFwd *network.PortForwarder) *Server {
	return NewServerWithConfig(vmMgr, storageMgr, portFwd, config.DefaultConfig(), nil)
}

// NewServerWithConfig creates a new API server using the provided config.
func NewServerWithConfig(vmMgr vm.Manager, storageMgr *storage.Manager, portFwd *network.PortForwarder, cfg *config.Config, webHandler http.Handler) *Server {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	s := &Server{
		vmManager:            vmMgr,
		storageMgr:           storageMgr,
		portFwd:              portFwd,
		maxRequestBodyBytes:  cfg.Daemon.MaxRequestBodyBytes,
		maxUploadBodyBytes:   cfg.Daemon.MaxUploadBodyBytes,
		maxConcurrentCreates: cfg.Daemon.MaxConcurrentCreates,
		authConfig:           cfg.Daemon.Auth,
		rateLimiter:          newIPRateLimiter(cfg.Daemon.RateLimitPerSecond, cfg.Daemon.RateLimitBurst),
	}
	if s.maxConcurrentCreates > 0 {
		s.createTokens = make(chan struct{}, s.maxConcurrentCreates)
		for i := 0; i < s.maxConcurrentCreates; i++ {
			s.createTokens <- struct{}{}
		}
	}
	s.setupRoutes(webHandler)
	return s
}

// NewServerWithWeb creates a new API server with an embedded web GUI handler.
func NewServerWithWeb(vmMgr vm.Manager, storageMgr *storage.Manager, portFwd *network.PortForwarder, webHandler http.Handler) *Server {
	return NewServerWithConfig(vmMgr, storageMgr, portFwd, config.DefaultConfig(), webHandler)
}

func (s *Server) setupRoutes(webHandler http.Handler) {
	r := chi.NewRouter()

	// Middleware
	r.Use(requestLogger) // structured request/response logging
	r.Use(middleware.Recoverer)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.rateLimitMiddleware)
		r.Use(middleware.SetHeader("Content-Type", "application/json"))
		r.Use(apiKeyAuth(s.authConfig))

		// Log viewer endpoint
		r.Get("/logs", s.GetLogs)

		// VM endpoints
		r.Route("/vms", func(r chi.Router) {
			r.Post("/", s.withRequestBodyLimit(s.CreateVM))
			r.Get("/", s.ListVMs)
			r.Post("/bulk", s.withRequestBodyLimit(s.BulkVMAction))
			r.Route("/{vmID}", func(r chi.Router) {
				r.Get("/", s.GetVM)
				r.Patch("/", s.withRequestBodyLimit(s.UpdateVM))
				r.Delete("/", s.DeleteVM)
				r.Post("/start", s.StartVM)
				r.Post("/stop", s.StopVM)

				// Snapshots
				r.Route("/snapshots", func(r chi.Router) {
					r.Post("/", s.withRequestBodyLimit(s.CreateSnapshot))
					r.Get("/", s.ListSnapshots)
					r.Post("/{snapName}/restore", s.RestoreSnapshot)
					r.Delete("/{snapName}", s.DeleteSnapshot)
				})

				// Port forwards
				r.Route("/ports", func(r chi.Router) {
					r.Get("/", s.ListPorts)
					r.Post("/", s.withRequestBodyLimit(s.AddPort))
					r.Delete("/{portID}", s.RemovePort)
				})
			})
		})

		// Image endpoints
		r.Route("/images", func(r chi.Router) {
			r.Get("/", s.ListImages)
			r.Post("/", s.withRequestBodyLimit(s.CreateImage))
			r.Post("/upload", s.withUploadBodyLimit(s.UploadImage))
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
