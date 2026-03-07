package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vmsmith/vmsmith/internal/api"
	"github.com/vmsmith/vmsmith/internal/config"
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
	server     *http.Server
}

// New creates and initializes a new daemon.
func New(cfg *config.Config) (*Daemon, error) {
	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}

	vmMgr, err := vm.NewLibvirtManager(cfg, s)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("connecting to libvirt: %w", err)
	}

	// Set up the NAT network
	conn, err := libvirt.NewConnect(cfg.Libvirt.URI)
	if err != nil {
		return nil, fmt.Errorf("libvirt connection for network: %w", err)
	}
	netMgr := network.NewManager(conn, cfg)
	if err := netMgr.EnsureNetwork(); err != nil {
		return nil, fmt.Errorf("ensuring network: %w", err)
	}

	storageMgr := storage.NewManager(cfg, s)
	portFwd := network.NewPortForwarder(s)

	// Restore port forwarding rules
	if err := portFwd.RestoreAll(); err != nil {
		fmt.Printf("warning: failed to restore some port forwards: %v\n", err)
	}

	apiServer := api.NewServerWithWeb(vmMgr, storageMgr, portFwd, web.Handler())

	return &Daemon{
		cfg:        cfg,
		store:      s,
		vmManager:  vmMgr,
		storageMgr: storageMgr,
		netManager: netMgr,
		portFwd:    portFwd,
		server: &http.Server{
			Addr:    cfg.Daemon.Listen,
			Handler: apiServer,
		},
	}, nil
}

// Run starts the HTTP server and blocks until shutdown signal.
func (d *Daemon) Run() error {
	// Write PID file
	if err := writePIDFile(d.cfg.Daemon.PIDFile); err != nil {
		fmt.Printf("warning: could not write PID file: %v\n", err)
	}
	defer os.Remove(d.cfg.Daemon.PIDFile)

	// Handle shutdown signals
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		fmt.Printf("vmSmith daemon listening on %s\n", d.cfg.Daemon.Listen)
		if err := d.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for signal or error
	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down daemon...")
	case err := <-errCh:
		return err
	}

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := d.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	d.vmManager.Close()
	d.store.Close()

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

	// Check if process exists
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}

	// Signal 0 checks if process exists without actually sending a signal
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false, 0
	}

	return true, pid
}

func writePIDFile(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}
