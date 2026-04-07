package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/store"
)

func TestFindLastSlash(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"user@host/image-name", 9},
		{"a/b/c", 3},
		{"/leading", 0},
		{"noslash", -1},
		{"", -1},
		{"trailing/", 8},
	}

	for _, tt := range tests {
		got := findLastSlash(tt.input)
		if got != tt.want {
			t.Errorf("findLastSlash(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func newTestStorageManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = filepath.Join(dir, "images")
	cfg.Storage.DBPath = filepath.Join(dir, "images.db")
	if err := os.MkdirAll(cfg.Storage.ImagesDir, 0o755); err != nil {
		t.Fatalf("mkdir images dir: %v", err)
	}
	s, err := store.New(cfg.Storage.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return NewManager(cfg, s)
}

func TestPullHTTPAddsAuthorizationHeaderWhenAPIKeyProvided(t *testing.T) {
	mgr := newTestStorageManager(t)
	const content = "qcow2-bytes"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Fatalf("Authorization header = %q, want %q", got, "Bearer secret-key")
		}
		_, _ = io.WriteString(w, content)
	}))
	defer ts.Close()

	url := ts.URL + "/image.qcow2"
	if err := mgr.Pull(url, "secret-key"); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(mgr.cfg.Storage.ImagesDir, "image.qcow2"))
	if err != nil {
		t.Fatalf("read downloaded image: %v", err)
	}
	if string(data) != content {
		t.Fatalf("downloaded content = %q, want %q", string(data), content)
	}
}

func TestPullHTTPSkipsAuthorizationHeaderWhenAPIKeyMissing(t *testing.T) {
	mgr := newTestStorageManager(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization header = %q, want empty", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer ts.Close()

	if err := mgr.Pull(ts.URL+"/plain.qcow2", ""); err != nil {
		t.Fatalf("Pull: %v", err)
	}
}

func TestPullHTTPNonOKStatusReturnsError(t *testing.T) {
	mgr := newTestStorageManager(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	err := mgr.Pull(ts.URL+"/missing.qcow2", "")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestPullDispatchesSCPForNonHTTPSource(t *testing.T) {
	mgr := newTestStorageManager(t)

	// pullSCP will fail because scp isn't available or host is unreachable,
	// but we verify it doesn't try HTTP and returns an scp-related error.
	err := mgr.Pull("user@remote/imagename", "")
	if err == nil {
		t.Skip("scp unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "http") {
		t.Errorf("non-HTTP source should use SCP, got HTTP error: %v", err)
	}
}

func TestPullSCP_InvalidSource(t *testing.T) {
	mgr := newTestStorageManager(t)

	err := mgr.pullSCP("noslash")
	if err == nil {
		t.Fatal("expected error for invalid SCP source")
	}
	if !strings.Contains(err.Error(), "invalid SCP source") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFindImageByName_Found(t *testing.T) {
	mgr := newTestStorageManager(t)

	// Import an image first
	_, err := mgr.ImportImage("test-image", []byte("data"))
	if err != nil {
		t.Fatalf("ImportImage: %v", err)
	}

	img, err := mgr.findImageByName("test-image")
	if err != nil {
		t.Fatalf("findImageByName: %v", err)
	}
	if img.Name != "test-image" {
		t.Errorf("Name = %q, want test-image", img.Name)
	}
}

func TestFindImageByName_NotFound(t *testing.T) {
	mgr := newTestStorageManager(t)

	_, err := mgr.findImageByName("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent image")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention not found: %v", err)
	}
}

func TestPullHTTPSavesToCorrectFilename(t *testing.T) {
	mgr := newTestStorageManager(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "image data")
	}))
	defer ts.Close()

	if err := mgr.Pull(ts.URL+"/my-image.qcow2", ""); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// Verify file was saved with correct name
	savedPath := filepath.Join(mgr.cfg.Storage.ImagesDir, "my-image.qcow2")
	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("file not saved: %v", err)
	}
	if string(data) != "image data" {
		t.Errorf("content = %q, want %q", string(data), "image data")
	}
}
