package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/events"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestServeUsesHTTPWithoutTLS(t *testing.T) {
	called := false
	d := &Daemon{
		cfg: &config.Config{},
		server: &http.Server{},
	}
	d.server = &http.Server{}

	orig := listenAndServe
	origTLS := listenAndServeTLS
	listenAndServe = func(s *http.Server) error {
		called = true
		return http.ErrServerClosed
	}
	listenAndServeTLS = func(s *http.Server, certFile, keyFile string) error {
		t.Fatalf("ListenAndServeTLS called unexpectedly with cert=%q key=%q", certFile, keyFile)
		return nil
	}
	defer func() {
		listenAndServe = orig
		listenAndServeTLS = origTLS
	}()

	if err := d.serve(); err != http.ErrServerClosed {
		t.Fatalf("serve() error = %v, want %v", err, http.ErrServerClosed)
	}
	if !called {
		t.Fatal("ListenAndServe was not called")
	}
}

func TestServeUsesHTTPSWhenTLSConfigured(t *testing.T) {
	var gotCert, gotKey string
	d := &Daemon{
		cfg: &config.Config{
			Daemon: config.DaemonConfig{
				TLS: config.TLSConfig{
					CertFile: "/etc/vmsmith/server.crt",
					KeyFile:  "/etc/vmsmith/server.key",
				},
			},
		},
		server: &http.Server{TLSConfig: &tls.Config{}},
	}

	orig := listenAndServe
	origTLS := listenAndServeTLS
	listenAndServe = func(s *http.Server) error {
		t.Fatal("ListenAndServe called unexpectedly")
		return nil
	}
	listenAndServeTLS = func(s *http.Server, certFile, keyFile string) error {
		gotCert = certFile
		gotKey = keyFile
		return http.ErrServerClosed
	}
	defer func() {
		listenAndServe = orig
		listenAndServeTLS = origTLS
	}()

	if err := d.serve(); err != http.ErrServerClosed {
		t.Fatalf("serve() error = %v, want %v", err, http.ErrServerClosed)
	}
	if gotCert != "/etc/vmsmith/server.crt" || gotKey != "/etc/vmsmith/server.key" {
		t.Fatalf("ListenAndServeTLS called with cert=%q key=%q", gotCert, gotKey)
	}
}

func TestServeUsesAutoCertWhenConfigured(t *testing.T) {
	called := false
	d := &Daemon{
		cfg: &config.Config{
			Daemon: config.DaemonConfig{
				TLS: config.TLSConfig{AutoCert: "vmsmith.example.com"},
			},
		},
		server: &http.Server{Addr: "127.0.0.1:0", TLSConfig: &tls.Config{}},
	}

	orig := listenAndServe
	origTLS := listenAndServeTLS
	origAuto := serveAutoTLS
	listenAndServe = func(s *http.Server) error {
		t.Fatal("ListenAndServe called unexpectedly")
		return nil
	}
	listenAndServeTLS = func(s *http.Server, certFile, keyFile string) error {
		t.Fatal("ListenAndServeTLS called unexpectedly")
		return nil
	}
	serveAutoTLS = func(s *http.Server) error {
		called = true
		return http.ErrServerClosed
	}
	defer func() {
		listenAndServe = orig
		listenAndServeTLS = origTLS
		serveAutoTLS = origAuto
	}()

	if err := d.serve(); err != http.ErrServerClosed {
		t.Fatalf("serve() error = %v, want %v", err, http.ErrServerClosed)
	}
	if !called {
		t.Fatal("serveAutoTLS was not called")
	}
}

func TestAutoCertTLSConfigUsesHostnameAndALPN(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Daemon.TLS.AutoCert = "vmsmith.example.com"
	cfg.Daemon.TLS.AutoCertCacheDir = t.TempDir()

	tlsCfg := autoCertTLSConfig(cfg)
	if tlsCfg == nil {
		t.Fatal("autoCertTLSConfig() returned nil")
	}
	if tlsCfg.GetCertificate == nil {
		t.Fatal("autoCertTLSConfig() GetCertificate is nil")
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %v, want %v", tlsCfg.MinVersion, tls.VersionTLS12)
	}
	if !strings.Contains(strings.Join(tlsCfg.NextProtos, ","), "acme-tls/1") {
		t.Fatalf("NextProtos = %#v, want acme-tls/1 included", tlsCfg.NextProtos)
	}
}

func TestStatusReturnsRunningForCurrentProcessPIDFile(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "vmsmith.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	running, pid := Status(pidFile)
	if !running {
		t.Fatal("Status() = not running, want running")
	}
	if pid != os.Getpid() {
		t.Fatalf("Status() pid = %d, want %d", pid, os.Getpid())
	}
}

func TestStatusReturnsFalseWhenPIDFileMissing(t *testing.T) {
	running, pid := Status(filepath.Join(t.TempDir(), "missing.pid"))
	if running {
		t.Fatal("Status() = running, want not running")
	}
	if pid != 0 {
		t.Fatalf("Status() pid = %d, want 0", pid)
	}
}

func TestStatusReturnsFalseForInvalidPIDContents(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "vmsmith.pid")
	if err := os.WriteFile(pidFile, []byte("not-a-pid"), 0644); err != nil {
		t.Fatalf("write pid file: %v", err)
	}

	running, pid := Status(pidFile)
	if running {
		t.Fatal("Status() = running, want not running")
	}
	if pid != 0 {
		t.Fatalf("Status() pid = %d, want 0", pid)
	}
}

func TestCloseResourcesClosesAllManagedDependencies(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New(): %v", err)
	}

	d := &Daemon{
		vmManager: vm.NewMockManager(),
		netManager: &network.Manager{},
		store:     db,
	}

	origCloseVMManager := closeVMManager
	origCloseNetworkMgr := closeNetworkMgr
	origCloseStore := closeStore
	defer func() {
		closeVMManager = origCloseVMManager
		closeNetworkMgr = origCloseNetworkMgr
		closeStore = origCloseStore
	}()

	var closed []string
	closeVMManager = func(m vm.Manager) error {
		closed = append(closed, "vm")
		return nil
	}
	closeNetworkMgr = func(m *network.Manager) error {
		closed = append(closed, "network")
		return nil
	}
	closeStore = func(s *store.Store) error {
		closed = append(closed, "store")
		return nil
	}

	if err := d.closeResources(); err != nil {
		t.Fatalf("closeResources() error = %v", err)
	}
	if strings.Join(closed, ",") != "vm,network,store" {
		t.Fatalf("closed resources = %v", closed)
	}
}

func TestCloseResourcesJoinsCleanupErrors(t *testing.T) {
	db, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.New(): %v", err)
	}

	d := &Daemon{
		vmManager: vm.NewMockManager(),
		netManager: &network.Manager{},
		store:     db,
	}

	origCloseVMManager := closeVMManager
	origCloseNetworkMgr := closeNetworkMgr
	origCloseStore := closeStore
	defer func() {
		closeVMManager = origCloseVMManager
		closeNetworkMgr = origCloseNetworkMgr
		closeStore = origCloseStore
	}()

	closeVMManager = func(m vm.Manager) error { return errors.New("vm boom") }
	closeNetworkMgr = func(m *network.Manager) error { return nil }
	closeStore = func(s *store.Store) error { return errors.New("store boom") }

	err = d.closeResources()
	if err == nil {
		t.Fatal("closeResources() error = nil, want joined error")
	}
	if !strings.Contains(err.Error(), "closing VM manager: vm boom") {
		t.Fatalf("joined error missing VM manager failure: %v", err)
	}
	if !strings.Contains(err.Error(), "closing store: store boom") {
		t.Fatalf("joined error missing store failure: %v", err)
	}
}

// noopEventStore is a minimal events.Store for daemon tests; it accepts
// every append and assigns an incrementing sequence.
type noopEventStore struct {
	seq uint64
}

func (s *noopEventStore) AppendEvent(evt *types.Event) (uint64, error) {
	s.seq++
	return s.seq, nil
}

func TestRunAutoStartSweepStartsOnlyMarkedStoppedVMs(t *testing.T) {
	mgr := vm.NewMockManager()

	// Three VMs:
	//   off-marked  → AutoStart=true, state=stopped → should be started
	//   on-marked   → AutoStart=true, state=running → already running, skip
	//   off-plain   → AutoStart=false              → ignore entirely
	mgr.SeedVM(&types.VM{
		ID:   "off-marked",
		Name: "off-marked",
		Spec: types.VMSpec{Name: "off-marked", AutoStart: true},
		State: types.VMStateStopped,
	})
	mgr.SeedVM(&types.VM{
		ID:   "on-marked",
		Name: "on-marked",
		Spec: types.VMSpec{Name: "on-marked", AutoStart: true},
		State: types.VMStateRunning,
	})
	mgr.SeedVM(&types.VM{
		ID:   "off-plain",
		Name: "off-plain",
		Spec: types.VMSpec{Name: "off-plain", AutoStart: false},
		State: types.VMStateStopped,
	})

	bus := events.New(&noopEventStore{})
	bus.Start()
	defer bus.Stop()

	d := &Daemon{vmManager: mgr, eventBus: bus}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.runAutoStartSweep(ctx)

	got, err := mgr.Get(ctx, "off-marked")
	if err != nil {
		t.Fatalf("Get off-marked: %v", err)
	}
	if got.State != types.VMStateRunning {
		t.Fatalf("off-marked State = %q, want running (auto-start should have flipped it)", got.State)
	}
	plain, _ := mgr.Get(ctx, "off-plain")
	if plain.State != types.VMStateStopped {
		t.Fatalf("off-plain State = %q, want stopped (no AutoStart flag)", plain.State)
	}
}

func TestRunAutoStartSweepReportsFailures(t *testing.T) {
	mgr := vm.NewMockManager()
	mgr.SeedVM(&types.VM{
		ID:   "boom",
		Name: "boom",
		Spec: types.VMSpec{Name: "boom", AutoStart: true},
		State: types.VMStateStopped,
	})
	mgr.StartErr = errors.New("libvirt: simulated boot failure")

	bus := events.New(&noopEventStore{})
	bus.Start()
	defer bus.Stop()

	d := &Daemon{vmManager: mgr, eventBus: bus}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.runAutoStartSweep(ctx) // must not panic
}
