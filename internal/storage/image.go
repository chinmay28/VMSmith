package storage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/internal/store"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// Manager handles image creation, export, and management.
type Manager struct {
	store *store.Store
	cfg   *config.Config
}

// NewManager creates a new storage manager.
func NewManager(cfg *config.Config, store *store.Store) *Manager {
	return &Manager{store: store, cfg: cfg}
}

// CreateImage creates a flattened qcow2 image from a VM's disk.
func (m *Manager) CreateImage(vmDiskPath, name string, sourceVM string) (*types.Image, error) {
	imagePath := filepath.Join(m.cfg.Storage.ImagesDir, name+".qcow2")

	// Flatten the overlay into a standalone image (no backing file)
	cmd := exec.Command("qemu-img", "convert",
		"-f", "qcow2",
		"-O", "qcow2",
		"-c", // compress
		vmDiskPath,
		imagePath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("qemu-img convert: %s: %w", string(out), err)
	}

	// Get the file size
	info, err := os.Stat(imagePath)
	if err != nil {
		return nil, err
	}

	img := &types.Image{
		ID:        fmt.Sprintf("img-%d", time.Now().UnixNano()),
		Name:      name,
		Path:      imagePath,
		SizeBytes: info.Size(),
		Format:    "qcow2",
		SourceVM:  sourceVM,
		CreatedAt: time.Now(),
	}

	if err := m.store.PutImage(img); err != nil {
		return nil, err
	}

	return img, nil
}

// ListImages returns all available images.
func (m *Manager) ListImages() ([]*types.Image, error) {
	return m.store.ListImages()
}

// CreateTemplate stores a reusable VM template.
func (m *Manager) CreateTemplate(tpl *types.VMTemplate) (*types.VMTemplate, error) {
	if tpl == nil {
		return nil, fmt.Errorf("template is required")
	}

	now := time.Now()
	tpl.Name = strings.TrimSpace(tpl.Name)
	tpl.Image = strings.TrimSpace(tpl.Image)
	tpl.Description = strings.TrimSpace(tpl.Description)
	tpl.DefaultUser = strings.TrimSpace(tpl.DefaultUser)
	if tpl.ID == "" {
		tpl.ID = fmt.Sprintf("tmpl-%d", now.UnixNano())
	}
	if tpl.CreatedAt.IsZero() {
		tpl.CreatedAt = now
	}
	if tpl.UpdatedAt.IsZero() {
		tpl.UpdatedAt = now
	}
	if err := m.store.PutTemplate(tpl); err != nil {
		return nil, err
	}
	return tpl, nil
}

// ListTemplates returns all available VM templates.
func (m *Manager) ListTemplates() ([]*types.VMTemplate, error) {
	return m.store.ListTemplates()
}

// GetTemplate retrieves a VM template by ID.
func (m *Manager) GetTemplate(id string) (*types.VMTemplate, error) {
	return m.store.GetTemplate(id)
}

// DeleteTemplate removes a VM template from metadata storage.
func (m *Manager) DeleteTemplate(id string) error {
	if _, err := m.store.GetTemplate(id); err != nil {
		return err
	}
	return m.store.DeleteTemplate(id)
}

// DeleteImage removes an image from disk and metadata.
func (m *Manager) DeleteImage(id string) error {
	img, err := m.store.GetImage(id)
	if err != nil {
		return err
	}

	os.Remove(img.Path)
	return m.store.DeleteImage(id)
}

// GetImage retrieves image metadata.
func (m *Manager) GetImage(id string) (*types.Image, error) {
	return m.store.GetImage(id)
}

// ImagePath returns the filesystem path for a named image.
func (m *Manager) ImagePath(name string) string {
	return filepath.Join(m.cfg.Storage.ImagesDir, name+".qcow2")
}

// ImportImage saves an uploaded file into the images directory and registers it.
func (m *Manager) ImportImage(name string, src []byte) (*types.Image, error) {
	if err := os.MkdirAll(m.cfg.Storage.ImagesDir, 0o755); err != nil {
		return nil, err
	}

	imagePath := filepath.Join(m.cfg.Storage.ImagesDir, name+".qcow2")
	if err := os.WriteFile(imagePath, src, 0o644); err != nil {
		return nil, fmt.Errorf("writing image file: %w", err)
	}

	img := &types.Image{
		ID:        fmt.Sprintf("img-%d", time.Now().UnixNano()),
		Name:      name,
		Path:      imagePath,
		SizeBytes: int64(len(src)),
		Format:    "qcow2",
		CreatedAt: time.Now(),
	}

	if err := m.store.PutImage(img); err != nil {
		os.Remove(imagePath)
		return nil, err
	}

	return img, nil
}
