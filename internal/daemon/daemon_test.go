package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

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
