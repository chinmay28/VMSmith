package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

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
	if stats.CPU.Percentage != 50 || stats.CPU.Used != 50 || stats.CPU.Total != 100 {
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
	ts, _, cleanup := testServer(t)
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
	missing := filepath.Join(dir, "missing", "base")
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
	if !strings.HasPrefix(gotPath, dir) {
		t.Fatalf("statFS path = %q, want within %q", gotPath, dir)
	}
	if _, err := os.Stat(filepath.Dir(missing)); err != nil {
		t.Fatalf("expected parent dir to exist: %v", err)
	}
}
