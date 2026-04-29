package api

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	apidocs "github.com/vmsmith/vmsmith/docs"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// MetricsManager is the subset of the metrics.Manager interface used by the API server.
// Defining it locally avoids a transitive CGO dependency on libvirt from the API
// package (the concrete implementation in internal/metrics imports libvirt; the
// interface itself does not).
type MetricsManager interface {
	Snapshot(vmID string) (*types.VMStatsSnapshot, error)
}

// Server holds the API dependencies and serves HTTP.
type Server struct {
	router               chi.Router
	vmManager            vm.Manager
	storageMgr           *storage.Manager
	portFwd              *network.PortForwarder
	store                Store
	metricsManager       MetricsManager
	eventBus             *events.EventBus
	hostStatsPath        string
	quotas               config.QuotasConfig
	metricsConfig        config.MetricsConfig
	maxRequestBodyBytes  int64
	maxUploadBodyBytes   int64
	maxConcurrentCreates int
	authConfig           config.AuthConfig
	createTokens         chan struct{}
	rateLimiter          *ipRateLimiter
	inflight             sync.WaitGroup
	shuttingDown         atomic.Bool
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

// Store is the persistence interface used by the API server.
type Store interface {
	ListEvents() ([]*types.Event, error)
	ListEventsAfterSeq(afterSeq uint64, limit int) ([]*types.Event, error)
	ListEventsFiltered(filter store.EventFilter) ([]*types.Event, int, error)
}

// NewServer creates a new API server with default body-size limits.
func NewServer(vmMgr vm.Manager, storageMgr *storage.Manager, portFwd *network.PortForwarder, store Store) *Server {
	return NewServerWithConfig(vmMgr, storageMgr, portFwd, store, config.DefaultConfig(), nil)
}

// NewServerWithConfig creates a new API server using the provided config.
func NewServerWithConfig(vmMgr vm.Manager, storageMgr *storage.Manager, portFwd *network.PortForwarder, store Store, cfg *config.Config, webHandler http.Handler) *Server {
	return NewServerWithMetrics(vmMgr, storageMgr, portFwd, store, cfg, webHandler, nil)
}

// NewServerWithMetrics creates a new API server with an optional metrics manager.
// Pass nil for metricsMgr to disable the metrics endpoints.
// The metricsMgr parameter accepts any value implementing MetricsManager (including
// *metrics.LibvirtMetricsManager and *metrics.MockMetricsManager); this avoids a
// transitive CGO dependency on libvirt from callers that only use the mock.
func NewServerWithMetrics(vmMgr vm.Manager, storageMgr *storage.Manager, portFwd *network.PortForwarder, store Store, cfg *config.Config, webHandler http.Handler, metricsMgr MetricsManager) *Server {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	vmMgr = vm.WithQuotas(vmMgr, cfg.Quotas)

	s := &Server{
		vmManager:            vmMgr,
		storageMgr:           storageMgr,
		portFwd:              portFwd,
		store:                store,
		metricsManager:       metricsMgr,
		hostStatsPath:        cfg.Storage.BaseDir,
		quotas:               cfg.Quotas,
		metricsConfig:        cfg.Metrics,
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

// SetMetricsManager installs a MetricsManager after server construction.
// This allows the daemon to set the manager after libvirt connects.
func (s *Server) SetMetricsManager(m MetricsManager) {
	s.metricsManager = m
}

// SetEventBus installs an EventBus for live event streaming.
func (s *Server) SetEventBus(bus *events.EventBus) {
	s.eventBus = bus
}

// NewServerWithWeb creates a new API server with an embedded web GUI handler.
func NewServerWithWeb(vmMgr vm.Manager, storageMgr *storage.Manager, portFwd *network.PortForwarder, store Store, webHandler http.Handler) *Server {
	return NewServerWithConfig(vmMgr, storageMgr, portFwd, store, config.DefaultConfig(), webHandler)
}

func (s *Server) setupRoutes(webHandler http.Handler) {
	r := chi.NewRouter()

	// Middleware
	r.Use(s.trackInFlightRequests)
	r.Use(requestLogger) // structured request/response logging
	r.Use(middleware.Recoverer)

	r.Get("/api/docs", apidocs.UIHandler().ServeHTTP)
	r.Get("/api/docs/*", func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/api/docs/", apidocs.AssetHandler()).ServeHTTP(w, r)
	})
	r.Get("/api/openapi.yaml", apidocs.SpecHandler().ServeHTTP)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.rateLimitMiddleware)
		r.Use(middleware.SetHeader("Content-Type", "application/json"))
		r.Use(apiKeyAuth(s.authConfig))

		// Log viewer endpoint
		r.Get("/logs", s.GetLogs)

		// Events REST + SSE stream.
		// /events/stream must not receive the JSON Content-Type middleware since
		// it writes text/event-stream; it uses its own inline middleware group.
		r.Get("/events", s.ListEvents)
		r.With(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Del("Content-Type") // SSE sets its own
				next.ServeHTTP(w, r)
			})
		}).Get("/events/stream", s.StreamEvents)

		// VM endpoints
		r.Route("/vms", func(r chi.Router) {
			r.Post("/", s.withRequestBodyLimit(s.CreateVM))
			r.Get("/", s.ListVMs)
			r.Post("/bulk", s.withRequestBodyLimit(s.BulkVMAction))
			r.Route("/{vmID}", func(r chi.Router) {
				r.Get("/", s.GetVM)
				r.Post("/clone", s.withRequestBodyLimit(s.CloneVM))
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

				// VM metrics
				r.Get("/stats", s.GetVMStats)

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

		// VM template endpoints
		r.Route("/templates", func(r chi.Router) {
			r.Get("/", s.ListTemplates)
			r.Post("/", s.withRequestBodyLimit(s.CreateTemplate))
			r.Delete("/{templateID}", s.DeleteTemplate)
		})

		// Host discovery and stats
		r.Get("/host/interfaces", s.ListHostInterfaces)
		r.Get("/host/stats", s.GetHostStats)

		// Quotas / allocation overview
		r.Get("/quotas/usage", s.GetQuotaUsage)
	})

	// Prometheus metrics endpoint — served without auth, outside /api/v1.
	// Only registered when metrics are enabled and no separate scrape_listen is configured.
	if s.metricsConfig.Enabled && s.metricsConfig.ScrapeListen == "" {
		r.Get("/metrics", s.PrometheusMetrics)
	}

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

// BeginShutdown marks the API server as draining so new requests are rejected.
func (s *Server) BeginShutdown() {
	s.shuttingDown.Store(true)
}

// WaitForDrain waits until in-flight requests finish or the context expires.
func (s *Server) WaitForDrain(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.inflight.Wait()
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) trackInFlightRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.shuttingDown.Load() {
			w.Header().Set("Connection", "close")
			writeError(w, http.StatusServiceUnavailable, "server is shutting down")
			return
		}

		s.inflight.Add(1)
		defer s.inflight.Done()
		next.ServeHTTP(w, r)
	})
}
