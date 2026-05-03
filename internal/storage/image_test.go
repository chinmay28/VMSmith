package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func newTestManager(t *testing.T) (*Manager, *store.Store, string) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	imagesDir := filepath.Join(dir, "images")
	os.MkdirAll(imagesDir, 0755)

	s, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	cfg := config.DefaultConfig()
	cfg.Storage.ImagesDir = imagesDir
	cfg.Storage.DBPath = dbPath

	return NewManager(cfg, s), s, imagesDir
}

func TestStorageManager_NewManager(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	if mgr == nil {
		t.Fatal("expected non-nil Manager")
	}
}

func TestStorageManager_ListImages_Empty(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	imgs, err := mgr.ListImages()
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(imgs) != 0 {
		t.Errorf("expected 0 images, got %d", len(imgs))
	}
}

func TestStorageManager_ListImages_WithData(t *testing.T) {
	mgr, s, _ := newTestManager(t)

	img := &types.Image{ID: "img-1", Name: "ubuntu", Format: "qcow2", CreatedAt: time.Now()}
	if err := s.PutImage(img); err != nil {
		t.Fatalf("seed: %v", err)
	}

	imgs, err := mgr.ListImages()
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image, got %d", len(imgs))
	}
	if imgs[0].ID != "img-1" {
		t.Errorf("ID = %q, want img-1", imgs[0].ID)
	}
}

func TestStorageManager_GetImage_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	_, err := mgr.GetImage("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent image")
	}
}

func TestStorageManager_GetImage_Found(t *testing.T) {
	mgr, s, _ := newTestManager(t)

	img := &types.Image{ID: "img-g", Name: "golden", Format: "qcow2", CreatedAt: time.Now()}
	if err := s.PutImage(img); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := mgr.GetImage("img-g")
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if got.Name != "golden" {
		t.Errorf("Name = %q, want golden", got.Name)
	}
}

func TestStorageManager_DeleteImage_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	err := mgr.DeleteImage("nonexistent")
	if err == nil {
		t.Error("expected error deleting nonexistent image")
	}
}

func TestStorageManager_DeleteImage_RemovesMetadataAndFile(t *testing.T) {
	mgr, s, imagesDir := newTestManager(t)

	fakePath := filepath.Join(imagesDir, "fake.qcow2")
	if err := os.WriteFile(fakePath, []byte("fake qcow2"), 0644); err != nil {
		t.Fatalf("write fake file: %v", err)
	}

	img := &types.Image{ID: "img-del", Name: "fake", Path: fakePath, CreatedAt: time.Now()}
	if err := s.PutImage(img); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := mgr.DeleteImage("img-del"); err != nil {
		t.Fatalf("DeleteImage: %v", err)
	}

	if _, err := mgr.GetImage("img-del"); err == nil {
		t.Error("expected error after deletion, image still in store")
	}

	if _, err := os.Stat(fakePath); !os.IsNotExist(err) {
		t.Error("expected file to be deleted from disk")
	}
}

func TestStorageManager_ImagePath(t *testing.T) {
	mgr, _, imagesDir := newTestManager(t)

	got := mgr.ImagePath("ubuntu-22.04")
	want := filepath.Join(imagesDir, "ubuntu-22.04.qcow2")
	if got != want {
		t.Errorf("ImagePath = %q, want %q", got, want)
	}
}

func TestStorageManager_ImportImage_Success(t *testing.T) {
	mgr, _, imagesDir := newTestManager(t)

	content := []byte("fake qcow2 data")
	img, err := mgr.ImportImage("uploaded-image", content, CreateImageOptions{})
	if err != nil {
		t.Fatalf("ImportImage: %v", err)
	}

	if img.Name != "uploaded-image" {
		t.Errorf("Name = %q, want uploaded-image", img.Name)
	}
	if img.ID == "" {
		t.Error("ID should not be empty")
	}
	expectedPath := filepath.Join(imagesDir, "uploaded-image.qcow2")
	if img.Path != expectedPath {
		t.Errorf("Path = %q, want %q", img.Path, expectedPath)
	}
	if img.SizeBytes != int64(len(content)) {
		t.Errorf("SizeBytes = %d, want %d", img.SizeBytes, len(content))
	}
	if img.Format != "qcow2" {
		t.Errorf("Format = %q, want qcow2", img.Format)
	}

	// File should exist on disk.
	if _, err := os.Stat(expectedPath); err != nil {
		t.Errorf("file not on disk: %v", err)
	}
}

func TestStorageManager_ImportImage_Persisted(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	if _, err := mgr.ImportImage("persist-test", []byte("data"), CreateImageOptions{}); err != nil {
		t.Fatalf("ImportImage: %v", err)
	}

	imgs, err := mgr.ListImages()
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(imgs) != 1 {
		t.Fatalf("expected 1 image in store, got %d", len(imgs))
	}
	if imgs[0].Name != "persist-test" {
		t.Errorf("Name = %q, want persist-test", imgs[0].Name)
	}
}

func TestStorageManager_ImportImage_PersistsMetadata(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	img, err := mgr.ImportImage("with-meta", []byte("data"), CreateImageOptions{
		Description: "  qcow2 lab build ",
		Tags:        []string{"lab", "ubuntu"},
	})
	if err != nil {
		t.Fatalf("ImportImage: %v", err)
	}
	if img.Description != "qcow2 lab build" {
		t.Errorf("Description = %q, want trimmed", img.Description)
	}
	if got := img.Tags; len(got) != 2 || got[0] != "lab" || got[1] != "ubuntu" {
		t.Errorf("Tags = %v, want [lab ubuntu]", got)
	}
	if img.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set on import")
	}

	stored, err := mgr.GetImage(img.ID)
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if stored.Description != "qcow2 lab build" {
		t.Errorf("stored.Description = %q", stored.Description)
	}
}

func TestStorageManager_UpdateImage_AppliesPatch(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	img, err := mgr.ImportImage("patchable", []byte("data"), CreateImageOptions{
		Description: "old",
		Tags:        []string{"alpha"},
	})
	if err != nil {
		t.Fatalf("ImportImage: %v", err)
	}
	originalUpdated := img.UpdatedAt

	// Description change triggers an update; tags untouched (nil = no change).
	updated, err := mgr.UpdateImage(img.ID, types.ImageUpdateSpec{Description: "fresh"})
	if err != nil {
		t.Fatalf("UpdateImage: %v", err)
	}
	if updated.Description != "fresh" {
		t.Errorf("Description = %q, want fresh", updated.Description)
	}
	if got := updated.Tags; len(got) != 1 || got[0] != "alpha" {
		t.Errorf("Tags = %v, want [alpha]", got)
	}
	if !updated.UpdatedAt.After(originalUpdated) {
		t.Error("UpdatedAt should advance after a real change")
	}

	// Empty Description is treated as "no change"; non-nil empty Tags clears.
	cleared, err := mgr.UpdateImage(img.ID, types.ImageUpdateSpec{Tags: []string{}})
	if err != nil {
		t.Fatalf("UpdateImage clear: %v", err)
	}
	if cleared.Description != "fresh" {
		t.Errorf("Description = %q, want unchanged 'fresh'", cleared.Description)
	}
	if len(cleared.Tags) != 0 {
		t.Errorf("Tags = %v, want empty", cleared.Tags)
	}
}

func TestStorageManager_UpdateImage_NotFound(t *testing.T) {
	mgr, _, _ := newTestManager(t)

	if _, err := mgr.UpdateImage("img-missing", types.ImageUpdateSpec{Description: "x"}); err == nil {
		t.Error("expected error updating missing image")
	}
}
