package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/network"
	"github.com/vmsmith/vmsmith/internal/storage"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/internal/vm"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestCollectHostStats(t *testing.T) {
	origReadFile := readFile
	origStatFS := statFS
	defer func() {
		readFile = origReadFile
		statFS = origStatFS
	}()

	var statReads int
	readFile = func(path string) ([]byte, error) {
		switch path {
		case "/proc/stat":
			statReads++
			if statReads == 1 {
				return []byte("cpu  10 0 10 80 0 0 0 0 0 0\ncpu0 5 0 5 40 0 0 0 0 0 0\ncpu1 5 0 5 40 0 0 0 0 0 0\n"), nil
			}
			return []byte("cpu  20 0 20 120 0 0 0 0 0 0\ncpu0 10 0 10 60 0 0 0 0 0 0\ncpu1 10 0 10 60 0 0 0 0 0 0\n"), nil
		case "/proc/meminfo":
			return []byte("MemTotal:       1024000 kB\nMemAvailable:    256000 kB\n"), nil
		default:
			return nil, fmt.Errorf("unexpected path %s", path)
		}
	}
	statFS = func(path string, fs *syscall.Statfs_t) error {
		fs.Blocks = 1000
		fs.Bavail = 250
		fs.Bsize = 4096
		return nil
	}

	stats, err := collectHostStats(context.Background(), "/tmp/vmsmith", 7)
	if err != nil {
		t.Fatalf("collectHostStats: %v", err)
	}

	if stats.VMCount != 7 {
		t.Fatalf("VMCount = %d, want 7", stats.VMCount)
	}
	if stats.CPU.Percentage != 33 || stats.CPU.Used != 33 || stats.CPU.Total != 100 {
		t.Fatalf("unexpected CPU stats: %+v", stats.CPU)
	}
	if stats.RAM.Total != 1024000*1024 || stats.RAM.Available != 256000*1024 {
		t.Fatalf("unexpected RAM stats: %+v", stats.RAM)
	}
	if stats.RAM.Percentage != 75 {
		t.Fatalf("RAM.Percentage = %d, want 75", stats.RAM.Percentage)
	}
	if stats.Disk.Total != 1000*4096 || stats.Disk.Available != 250*4096 {
		t.Fatalf("unexpected disk stats: %+v", stats.Disk)
	}
	if stats.Disk.Percentage != 75 {
		t.Fatalf("Disk.Percentage = %d, want 75", stats.Disk.Percentage)
	}
}

func TestGetHostStats(t *testing.T) {
	ts, _, cleanup := testServerWithConfig(t, func(cfg *config.Config) {
		cfg.Storage.BaseDir = t.TempDir()
	})
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/host/stats")
	if err != nil {
		t.Fatalf("GET /host/stats: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var stats types.HostStats
	decodeJSON(t, resp, &stats)
	if stats.CPU.Total == 0 || stats.RAM.Total == 0 || stats.Disk.Total == 0 {
		t.Fatalf("expected non-zero totals, got %+v", stats)
	}
}

func TestCollectHostStatsFallsBackToParentDir(t *testing.T) {
	origReadFile := readFile
	origStatFS := statFS
	defer func() {
		readFile = origReadFile
		statFS = origStatFS
	}()

	readFile = func(path string) ([]byte, error) {
		switch path {
		case "/proc/stat":
			return []byte("cpu  10 0 10 80 0 0 0 0 0 0\ncpu0 10 0 10 80 0 0 0 0 0 0\n"), nil
		case "/proc/meminfo":
			return []byte("MemTotal:       1024 kB\nMemAvailable:    512 kB\n"), nil
		default:
			return nil, fmt.Errorf("unexpected path %s", path)
		}
	}

	dir := t.TempDir()
	existingParent := filepath.Join(dir, "existing")
	if err := os.MkdirAll(existingParent, 0755); err != nil {
		t.Fatalf("mkdir existing parent: %v", err)
	}
	missing := filepath.Join(existingParent, "missing", "base")
	var gotPath string
	statFS = func(path string, fs *syscall.Statfs_t) error {
		gotPath = path
		fs.Blocks = 100
		fs.Bavail = 50
		fs.Bsize = 1024
		return nil
	}

	_, err := collectHostStats(context.Background(), missing, 0)
	if err != nil {
		t.Fatalf("collectHostStats: %v", err)
	}
	if gotPath != existingParent {
		t.Fatalf("statFS path = %q, want %q", gotPath, existingParent)
	}
}

func TestGetHostStatsSanitizesManagerErrors(t *testing.T) {
	ts, mockMgr, cleanup := testServer(t)
	defer cleanup()

	mockMgr.ListErr = fmt.Errorf("connecting to libvirt: dial tcp 127.0.0.1:16509: connect: connection refused")

	resp, err := http.Get(ts.URL + "/api/v1/host/stats")
	if err != nil {
		t.Fatalf("GET /host/stats: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "service_unavailable")
}

func TestGetHostStatsSanitizesCollectorErrors(t *testing.T) {
	origReadFile := readFile
	defer func() { readFile = origReadFile }()
	readFile = func(path string) ([]byte, error) {
		return nil, fmt.Errorf("open %s: permission denied", path)
	}

	ts, _, cleanup := testServer(t)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/v1/host/stats")
	if err != nil {
		t.Fatalf("GET /host/stats: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	errResp := assertAPIErrorCode(t, resp, "internal_error")
	if strings.Contains(strings.ToLower(errResp.Message+" "+errResp.Error), "permission denied") {
		t.Fatalf("expected sanitized error, got %#v", errResp)
	}
}

func TestGetHostStatsTimesOutCanceledRequests(t *testing.T) {
	origReadFile := readFile
	defer func() { readFile = origReadFile }()
	readFile = func(path string) ([]byte, error) {
		if path == "/proc/stat" {
			return []byte("cpu  10 0 10 80 0 0 0 0 0 0\n"), nil
		}
		return origReadFile(path)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	imagesDir := filepath.Join(dir, "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		t.Fatalf("mkdir images dir: %v", err)
	}

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer s.Close()

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = imagesDir
	cfg.Storage.DBPath = dbPath

	mockMgr := vm.NewMockManager()
	server := NewServerWithConfig(mockMgr, storage.NewManager(cfg, s), network.NewPortForwarder(s), cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/host/stats", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("status = %d, want 408", resp.StatusCode)
	}
	assertAPIErrorCode(t, resp, "request_timeout")
}
