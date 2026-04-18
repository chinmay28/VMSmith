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

	"github.com/vmsmith/vmsmith/internal/api"
	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/logger"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/internal/web"
	"libvirt.org/go/libvirt"
)

// Daemon encapsulates the long-running vmSmith server.
type Daemon struct {
	cfg        *config.Config
	store      *store.Store
	vmManager  vm.Manager
	storageMgr *storage.Manager
	netManager *network.Manager
	portFwd    *network.PortForwarder
	apiServer  *api.Server
	server     *http.Server
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

	var vmMgr vm.Manager
	vmMgr, err = vm.NewLibvirtManager(cfg, s)
	if err != nil {
		s.Close()
		logger.Error("daemon", "connecting to libvirt failed", "error", err.Error())
		return nil, fmt.Errorf("connecting to libvirt: %w", err)
	}
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

	// Restore port forwarding rules.
	if err := portFwd.RestoreAll(); err != nil {
		logger.Warn("daemon", "failed to restore some port forwards", "error", err.Error())
	} else {
		logger.Info("daemon", "port forwarding rules restored")
	}

	apiServer := api.NewServerWithConfig(vmMgr, storageMgr, portFwd, cfg, web.Handler())

	server := &http.Server{
		Addr:    cfg.Daemon.Listen,
		Handler: apiServer,
	}
	if cfg.Daemon.AutoCertConfigured() {
		server.TLSConfig = autoCertTLSConfig(cfg)
	}

	return &Daemon{
		cfg:        cfg,
		store:      s,
		vmManager:  vmMgr,
		storageMgr: storageMgr,
		netManager: netMgr,
		portFwd:    portFwd,
		apiServer:  apiServer,
		server:     server,
	}, nil
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
