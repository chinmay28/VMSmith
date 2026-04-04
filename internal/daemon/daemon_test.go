package daemon

import (
	"crypto/tls"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
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
