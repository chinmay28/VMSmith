package daemon

import (
	"crypto/tls"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"golang.org/x/crypto/acme/autocert"
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
	var gotManager *autocert.Manager
	d := &Daemon{
		cfg: &config.Config{
			Daemon: config.DaemonConfig{
				TLS: config.TLSConfig{
					AutoCert:         true,
					AutoCertHosts:    []string{"vmsmith.example.com"},
					AutoCertCacheDir: "/var/lib/vmsmith/autocert",
					AutoCertEmail:    "ops@example.com",
				},
			},
		},
		server: &http.Server{TLSConfig: &tls.Config{}},
	}

	orig := listenAndServe
	origTLS := listenAndServeTLS
	origAutoTLS := listenAndServeAutoTLS
	listenAndServe = func(s *http.Server) error {
		t.Fatal("ListenAndServe called unexpectedly")
		return nil
	}
	listenAndServeTLS = func(s *http.Server, certFile, keyFile string) error {
		t.Fatalf("ListenAndServeTLS called unexpectedly with cert=%q key=%q", certFile, keyFile)
		return nil
	}
	listenAndServeAutoTLS = func(s *http.Server, mgr *autocert.Manager) error {
		gotManager = mgr
		return http.ErrServerClosed
	}
	defer func() {
		listenAndServe = orig
		listenAndServeTLS = origTLS
		listenAndServeAutoTLS = origAutoTLS
	}()

	if err := d.serve(); err != http.ErrServerClosed {
		t.Fatalf("serve() error = %v, want %v", err, http.ErrServerClosed)
	}
	if gotManager == nil {
		t.Fatal("autocert manager was not provided")
	}
	if gotManager.Cache == nil {
		t.Fatal("autocert cache = nil, want DirCache")
	}
	if gotManager.Email != "ops@example.com" {
		t.Fatalf("autocert email = %q, want ops@example.com", gotManager.Email)
	}
	if err := gotManager.HostPolicy(nil, "vmsmith.example.com"); err != nil {
		t.Fatalf("HostPolicy rejected configured host: %v", err)
	}
	if err := gotManager.HostPolicy(nil, "other.example.com"); err == nil {
		t.Fatal("HostPolicy accepted unexpected host")
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
