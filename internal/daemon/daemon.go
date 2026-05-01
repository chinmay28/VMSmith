package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/go-chi/chi/v5"
	"github.com/vmsmith/vmsmith/internal/api"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/metrics"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/internal/web"
	"github.com/vmsmith/vmsmith/internal/webhooks"
	"github.com/vmsmith/vmsmith/pkg/types"
	"libvirt.org/go/libvirt"
)

// Daemon encapsulates the long-running vmSmith server.
type Daemon struct {
	cfg            *config.Config
	store          *store.Store
	vmManager      vm.Manager
	storageMgr     *storage.Manager
	netManager     *network.Manager
	portFwd        *network.PortForwarder
	metricsManager metrics.Manager
	eventBus       *events.EventBus
	retention      *events.Retention
	webhookMgr     *webhooks.Manager
	apiServer      *api.Server
	server         *http.Server
	metricsSrv     *http.Server // optional separate Prometheus scrape server
}

var (
	closeVMManager  = func(m vm.Manager) error { return m.Close() }
	closeNetworkMgr = func(m *network.Manager) error { return m.Close() }
	closeStore      = func(s *store.Store) error { return s.Close() }
	closeLogger     = func() { logger.Close() }
)

var (
	listenAndServe = func(s *http.Server) error {
		return s.ListenAndServe()
	}
	listenAndServeTLS = func(s *http.Server, certFile, keyFile string) error {
		return s.ListenAndServeTLS(certFile, keyFile)
	}
	serveAutoTLS = func(s *http.Server) error {
		ln, err := tls.Listen("tcp", s.Addr, s.TLSConfig)
		if err != nil {
			return err
		}
		return s.Serve(ln)
	}
)

// New creates and initializes a new daemon.
func New(cfg *config.Config) (*Daemon, error) {
	// Initialise structured logger.
	if err := logger.Init(cfg.Daemon.LogFile, logger.LevelInfo); err != nil {
		// Non-fatal: fall back to stderr logging.
		fmt.Printf("warning: could not open log file: %v\n", err)
	}
	logger.Info("daemon", "initialising vmSmith daemon", "listen", cfg.Daemon.Listen)

	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		logger.Error("daemon", "opening store failed", "error", err.Error())
		return nil, fmt.Errorf("opening store: %w", err)
	}
	logger.Info("daemon", "store opened", "path", cfg.Storage.DBPath)

	libvirtMgr, err := vm.NewLibvirtManager(cfg, s)
	if err != nil {
		s.Close()
		logger.Error("daemon", "connecting to libvirt failed", "error", err.Error())
		return nil, fmt.Errorf("connecting to libvirt: %w", err)
	}
	var vmMgr vm.Manager = libvirtMgr
	logger.Info("daemon", "connected to libvirt", "uri", cfg.Libvirt.URI)

	// Set up the NAT network.
	conn, err := libvirt.NewConnect(cfg.Libvirt.URI)
	if err != nil {
		logger.Error("daemon", "libvirt connection for network failed", "error", err.Error())
		return nil, fmt.Errorf("libvirt connection for network: %w", err)
	}
	netMgr := network.NewManager(conn, cfg)
	if err := netMgr.EnsureNetwork(); err != nil {
		logger.Error("daemon", "ensuring NAT network failed", "error", err.Error())
		return nil, fmt.Errorf("ensuring network: %w", err)
	}
	logger.Info("daemon", "NAT network ready", "network", cfg.Network.Name)

	storageMgr := storage.NewManager(cfg, s)
	portFwd := network.NewPortForwarder(s)

	// Create event bus backed by the store.  Create early so we can emit
	// system events for failures during the rest of startup (e.g. partial
	// port-forward restore).  Start() is called from Run().
	eventBus := events.New(s)
	logger.Info("daemon", "event bus initialised")
	libvirtMgr.SetEventBus(eventBus)

	// Restore port forwarding rules.
	if err := portFwd.RestoreAll(); err != nil {
		logger.Warn("daemon", "failed to restore some port forwards", "error", err.Error())
		eventBus.Publish(events.NewSystemEventWithAttrs(
			"port_forward.restore_failed",
			types.EventSeverityWarn,
			"failed to restore one or more port forwards on daemon startup",
			map[string]string{"error": err.Error()},
		))
	} else {
		logger.Info("daemon", "port forwarding rules restored")
	}

	// Initialise metrics manager.
	// Open a dedicated libvirt connection for the metrics sampler so the polling
	// goroutine does not contend with the VM manager's connection.
	var metricsMgr metrics.Manager
	if cfg.Metrics.Enabled {
		metricsConn, metricsConnErr := libvirt.NewConnect(cfg.Libvirt.URI)
		if metricsConnErr != nil {
			logger.Warn("daemon", "metrics: could not open libvirt connection; metrics disabled", "error", metricsConnErr.Error())
		} else {
			sampleInterval := time.Duration(cfg.Metrics.SampleInterval) * time.Second
			histSize := cfg.Metrics.HistorySize
			resolver := newStoreNameResolver(s)
			metricsMgr = metrics.NewLibvirtMetricsManager(metricsConn, resolver, sampleInterval, histSize)
			logger.Info("daemon", "metrics sampler initialised",
				"interval_seconds", fmt.Sprintf("%d", cfg.Metrics.SampleInterval),
				"history_size", fmt.Sprintf("%d", histSize))
		}
	} else {
		logger.Info("daemon", "metrics collection disabled by config")
	}

	// Optional retention loop for the events bucket.
	var retention *events.Retention
	if (cfg.Events.MaxRecords > 0 || cfg.Events.MaxAgeSeconds > 0) && cfg.Events.RetentionInterval > 0 {
		maxAge := time.Duration(cfg.Events.MaxAgeSeconds) * time.Second
		retention = events.NewRetention(s, cfg.Events.MaxRecords, maxAge,
			time.Duration(cfg.Events.RetentionInterval)*time.Second, eventBus)
		logger.Info("daemon", "events retention loop configured",
			"max_records", fmt.Sprintf("%d", cfg.Events.MaxRecords),
			"max_age_seconds", fmt.Sprintf("%d", cfg.Events.MaxAgeSeconds),
			"interval_sec", fmt.Sprintf("%d", cfg.Events.RetentionInterval))
	}

	apiServer := api.NewServerWithMetrics(vmMgr, storageMgr, portFwd, s, cfg, web.Handler(), metricsMgr)
	apiServer.SetEventBus(eventBus)

	// Webhook subsystem.  Always wire so the API surface is available; if no
	// webhooks are registered the manager simply has no workers.
	webhookMgr := webhooks.NewManager(s, eventBus, webhooks.Config{
		AllowedHosts: cfg.Webhooks.AllowedHosts,
	})
	apiServer.SetWebhookSubsystem(s, webhookMgr)

	server := &http.Server{
		Addr:    cfg.Daemon.Listen,
		Handler: apiServer,
	}
	if cfg.Daemon.AutoCertConfigured() {
		server.TLSConfig = autoCertTLSConfig(cfg)
	}

	d := &Daemon{
		cfg:            cfg,
		store:          s,
		vmManager:      vmMgr,
		storageMgr:     storageMgr,
		netManager:     netMgr,
		portFwd:        portFwd,
		metricsManager: metricsMgr,
		eventBus:       eventBus,
		retention:      retention,
		webhookMgr:     webhookMgr,
		apiServer:      apiServer,
		server:         server,
	}

	// If a separate scrape listen address is configured, spin up a secondary
	// HTTP server that serves only GET /metrics (no auth required).
	if cfg.Metrics.Enabled && cfg.Metrics.ScrapeListen != "" {
		scrapeRouter := chi.NewRouter()
		scrapeRouter.Get("/metrics", apiServer.PrometheusMetrics)
		d.metricsSrv = &http.Server{
			Addr:    cfg.Metrics.ScrapeListen,
			Handler: scrapeRouter,
		}
		logger.Info("daemon", "prometheus scrape endpoint on separate port", "addr", cfg.Metrics.ScrapeListen)
	}

	return d, nil
}

// Run starts the HTTP server and blocks until shutdown signal.
func (d *Daemon) Run() error {
	// Write PID file.
	if err := writePIDFile(d.cfg.Daemon.PIDFile); err != nil {
		logger.Warn("daemon", "could not write PID file", "error", err.Error())
	}
	defer os.Remove(d.cfg.Daemon.PIDFile)

	// Handle shutdown signals.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start event bus.
	if d.eventBus != nil {
		d.eventBus.Start()
		logger.Info("daemon", "event bus started")

		// Start the VM state persister consumer before publishing any libvirt
		// events.  It subscribes to the bus and applies `vm.*` state changes
		// to bbolt, replacing the previous in-callback store.PutVM write so
		// the libvirt event loop never contends on a store transaction.
		persister := vm.NewVMStatePersister(d.eventBus, d.store)
		go persister.Run(ctx)
		logger.Info("daemon", "vm state persister started")

		evt := events.NewSystemEvent("daemon.started", "info", "vmsmith daemon started")
		evt.Attributes = map[string]string{
			"listen": d.cfg.Daemon.Listen,
			"tls":    strconv.FormatBool(d.cfg.Daemon.TLSEnabled()),
		}
		d.eventBus.Publish(evt)
	}

	// Start retention loop.
	if d.retention != nil {
		go d.retention.Run(ctx)
	}

	// Start the webhook delivery subsystem.  Each registered, active webhook
	// gets a goroutine that subscribes to the bus and delivers matching events.
	if d.webhookMgr != nil {
		if err := d.webhookMgr.Start(ctx); err != nil {
			logger.Warn("daemon", "webhook manager failed to start", "error", err.Error())
		} else {
			logger.Info("daemon", "webhook manager started")
		}
	}

	// Start metrics sampler.
	if d.metricsManager != nil {
		if err := d.metricsManager.Start(ctx); err != nil {
			logger.Warn("daemon", "metrics sampler failed to start", "error", err.Error())
		} else {
			logger.Info("daemon", "metrics sampler started")
		}
	}

	// Start optional separate Prometheus scrape server.
	if d.metricsSrv != nil {
		go func() {
			logger.Info("daemon", "prometheus scrape server listening", "addr", d.metricsSrv.Addr)
			if err := d.metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Warn("daemon", "prometheus scrape server error", "error", err.Error())
			}
		}()
	}

	// Auto-start sweep: start any VM marked AutoStart=true that is currently
	// stopped. Failures are logged and emitted as system events but do not
	// block daemon startup — operators can investigate after the fact via the
	// activity feed or `vmsmith vm list`.
	if d.vmManager != nil {
		go d.runAutoStartSweep(ctx)
	}

	// Start server in goroutine.
	errCh := make(chan error, 1)
	go func() {
		logger.Info("daemon", "HTTP server listening", "addr", d.cfg.Daemon.Listen, "tls", strconv.FormatBool(d.cfg.Daemon.TLSEnabled()), "auto_cert", d.cfg.Daemon.TLS.AutoCert)
		fmt.Printf("vmSmith daemon listening on %s\n", d.cfg.Daemon.Listen)
		if err := d.serve(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for signal or error.
	select {
	case <-ctx.Done():
		logger.Info("daemon", "shutdown signal received")
		fmt.Println("\nShutting down daemon...")
	case err := <-errCh:
		logger.Error("daemon", "HTTP server error", "error", err.Error())
		return err
	}

	// Emit shutdown event before tearing things down so it's persisted before
	// the bus is stopped in closeResources().
	if d.eventBus != nil {
		d.eventBus.Publish(events.NewSystemEvent("daemon.shutdown", "info", "vmsmith daemon shutting down"))
	}

	// Graceful shutdown with timeout.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if d.apiServer != nil {
		d.apiServer.BeginShutdown()
	}

	if err := d.server.Shutdown(shutdownCtx); err != nil {
		logger.Error("daemon", "graceful shutdown error", "error", err.Error())
		_ = d.closeResources()
		return fmt.Errorf("shutdown: %w", err)
	}

	// Stop the optional scrape server.
	if d.metricsSrv != nil {
		if err := d.metricsSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("daemon", "prometheus scrape server shutdown error", "error", err.Error())
		}
	}

	if d.apiServer != nil {
		if err := d.apiServer.WaitForDrain(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.Error("daemon", "waiting for in-flight requests failed", "error", err.Error())
		}
	}

	if err := d.closeResources(); err != nil {
		logger.Error("daemon", "resource cleanup error", "error", err.Error())
		return err
	}

	logger.Info("daemon", "daemon stopped cleanly")
	closeLogger()

	fmt.Println("Daemon stopped")
	return nil
}

// runAutoStartSweep walks the persisted VM set once at daemon boot and
// asks the VM manager to Start any VM marked AutoStart=true that is not
// already running. Errors are logged and emitted as system events; they do
// not block startup.
func (d *Daemon) runAutoStartSweep(ctx context.Context) {
	vms, err := d.vmManager.List(ctx)
	if err != nil {
		logger.Warn("daemon", "auto-start sweep: list failed", "error", err.Error())
		return
	}

	var attempted, started, failed int
	for _, v := range vms {
		if v == nil || !v.Spec.AutoStart {
			continue
		}
		if v.State == types.VMStateRunning {
			continue
		}
		attempted++
		logger.Info("daemon", "auto-starting VM", "vm", v.Name, "id", v.ID)
		if err := d.vmManager.Start(ctx, v.ID); err != nil {
			failed++
			logger.Error("daemon", "auto-start failed", "vm", v.Name, "id", v.ID, "error", err.Error())
			if d.eventBus != nil {
				d.eventBus.Publish(events.NewSystemEventWithAttrs(
					"vm.auto_start_failed",
					types.EventSeverityWarn,
					fmt.Sprintf("auto-start failed for VM %q: %s", v.Name, err.Error()),
					map[string]string{"vm_id": v.ID, "vm_name": v.Name, "error": err.Error()},
				))
			}
			continue
		}
		started++
		if d.eventBus != nil {
			d.eventBus.Publish(events.NewAppEvent(
				"vm.auto_started",
				v.ID,
				fmt.Sprintf("VM %q auto-started by daemon", v.Name),
				map[string]string{"vm_name": v.Name},
			))
		}
	}

	if attempted > 0 {
		logger.Info("daemon", "auto-start sweep complete",
			"attempted", strconv.Itoa(attempted),
			"started", strconv.Itoa(started),
			"failed", strconv.Itoa(failed))
	}
}

// Stop sends SIGTERM to a running daemon.
func Stop(pidFile string) error {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return fmt.Errorf("reading PID file: %w (is the daemon running?)", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid PID: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	return proc.Signal(syscall.SIGTERM)
}

// Status checks if the daemon is running.
func Status(pidFile string) (bool, int) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false, 0
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false, 0
	}

	// Check if process exists.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}

	// Signal 0 checks if process exists without actually sending a signal.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false, 0
	}

	return true, pid
}

func (d *Daemon) serve() error {
	if d.cfg.Daemon.TLSConfigured() {
		return listenAndServeTLS(d.server, d.cfg.Daemon.TLS.CertFile, d.cfg.Daemon.TLS.KeyFile)
	}
	if d.cfg.Daemon.AutoCertConfigured() {
		return serveAutoTLS(d.server)
	}
	return listenAndServe(d.server)
}

func autoCertTLSConfig(cfg *config.Config) *tls.Config {
	cacheDir := cfg.Daemon.TLS.AutoCertCacheDir
	if cacheDir == "" {
		cacheDir = config.DefaultConfig().Daemon.TLS.AutoCertCacheDir
	}

	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(cacheDir),
		HostPolicy: autocert.HostWhitelist(cfg.Daemon.TLS.AutoCert),
	}

	return &tls.Config{
		GetCertificate: manager.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos: []string{
			"h2",
			"http/1.1",
			acme.ALPNProto,
		},
	}
}

func (d *Daemon) closeResources() error {
	var errs []error

	// Stop the webhook manager before the bus so its workers drain on a
	// quiescent queue and don't try to publish onto a stopped bus.
	if d.webhookMgr != nil {
		d.webhookMgr.Stop()
	}

	// Stop event bus (drains pending events before closing store).
	if d.eventBus != nil {
		d.eventBus.Stop()
	}

	// Stop metrics sampler before closing the libvirt connection.
	if d.metricsManager != nil {
		if err := d.metricsManager.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("stopping metrics manager: %w", err))
		}
	}

	if d.vmManager != nil {
		if err := closeVMManager(d.vmManager); err != nil {
			errs = append(errs, fmt.Errorf("closing VM manager: %w", err))
		}
	}
	if d.netManager != nil {
		if err := closeNetworkMgr(d.netManager); err != nil {
			errs = append(errs, fmt.Errorf("closing network manager: %w", err))
		}
	}
	if d.store != nil {
		if err := closeStore(d.store); err != nil {
			errs = append(errs, fmt.Errorf("closing store: %w", err))
		}
	}
	return errors.Join(errs...)
}

func writePIDFile(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}
